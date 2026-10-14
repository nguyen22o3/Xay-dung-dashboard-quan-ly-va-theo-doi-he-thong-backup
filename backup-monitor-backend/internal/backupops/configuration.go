package backupops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

type Preview struct {
	SourceScript    *ManagedScriptOption `json:"sourceScript,omitempty"`
	CronFiles       *CronFilePreview     `json:"cronFiles,omitempty"`
	BackupMigration bool                 `json:"backupMigration"`
	CronDirectories *CronDirectories     `json:"cronDirectories,omitempty"`
	Token           string               `json:"token"`
	Changed         bool                 `json:"changed"`
	FilesToMove     int                  `json:"filesToMove"`
	BytesToMove     int64                `json:"bytesToMove"`
	DaysToMove      int                  `json:"daysToMove"`
	ScriptsToUpdate int                  `json:"scriptsToUpdate"`
	ScriptsToCreate []string             `json:"scriptsToCreate"`
	LogsToMove      int                  `json:"logsToMove"`
	DriveChanged    bool                 `json:"driveChanged"`
	Config          Settings             `json:"config"`
	Version         string               `json:"version"`
	Schedule        *SchedulePreview     `json:"schedule,omitempty"`
	Sources         []SourcePreview      `json:"sources,omitempty"`
}
type scriptChange struct {
	Source, Target    string
	Original, Updated []byte
}
type logChange struct{ Source, Target, Hash string }
type fileCheck struct {
	Relative string
	Size     int64
	Hash     string
}
type dayChange struct {
	Source, Target string
	Files          []fileCheck
}
type Proposal struct {
	Archives      []archiveTransfer
	Ledgers       []ledgerTransfer
	Summary       Preview
	Old, New      Settings
	Scripts       []scriptChange
	Logs          []logChange
	Days          []dayChange
	Cron, NewCron []byte
}

func settingsPairs(old, new Settings) [][2]string {
	pairs := [][2]string{{old.ScriptsDir, new.ScriptsDir}, {old.BackupRoot, new.BackupRoot}, {old.DriveRemote + ":" + old.DriveFolder, new.DriveRemote + ":" + new.DriveFolder}, {old.DriveHistoryLog, new.DriveHistoryLog}}
	for _, name := range ScriptNames {
		pairs = append(pairs, [2]string{scriptPath(old, name), scriptPath(new, name)})
	}
	for _, name := range LogNames {
		pairs = append(pairs, [2]string{logPath(old, name), logPath(new, name)})
	}
	result := [][2]string{}
	changed := false
	for _, p := range pairs {
		if p[0] != p[1] {
			changed = true
		}
	}
	if !changed {
		return result
	}
	// Longest-match identity mappings protect unchanged script/log paths inside
	// the old backup root when only the archive destination changes.
	result = append(result, pairs...)
	if old.LogsDir == new.LogsDir {
		result = append(result, [2]string{old.LogsDir, new.LogsDir})
	}
	return result
}
func Rewrite(text string, pairs [][2]string) string {
	if len(pairs) == 0 {
		return text
	}
	mapping := map[string]string{}
	for _, p := range pairs {
		mapping[p[0]] = p[1]
		if strings.Contains(p[0], "/") {
			mapping[strings.ReplaceAll(p[0], "/", `\/`)] = strings.ReplaceAll(p[1], "/", `\/`)
		}
	}
	keys := []string{}
	for k := range mapping {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for i, k := range keys {
		keys[i] = regexp.QuoteMeta(k)
	}
	return regexp.MustCompile(strings.Join(keys, "|")).ReplaceAllStringFunc(text, func(value string) string { return mapping[value] })
}
func (c *Controller) Proposal(r Request) (*Proposal, error) {
	if r.Schedule != nil && (r.Schedule.Directories != nil || r.Schedule.InstanceKey != "") {
		return c.cronProposal(r)
	}
	old, version, err := c.Active()
	if err != nil {
		return nil, err
	}
	if r.ExpectedVersion != version {
		return nil, errors.New("Cấu hình đã thay đổi; hãy tải lại")
	}
	if r.Config == nil {
		return nil, errors.New("Thiếu cấu hình mới")
	}
	new := *r.Config
	if !sameCronLocations(old.CronLocations, new.CronLocations) {
		return nil, errors.New("Đường dẫn cron chỉ được đổi qua tác vụ đang chọn")
	}
	if r.Schedule != nil {
		before, after := old, new
		before.CronLocations, after.CronLocations = nil, nil
		if !reflect.DeepEqual(before, after) {
			return nil, errors.New("Không sửa cấu hình chung từ cron; cập nhật dashboard và gửi cấu hình riêng của tác vụ")
		}
	}
	new.DriveHistoryLog = old.DriveHistoryLog
	if new.LogsDir != old.LogsDir {
		new.DriveHistoryLog = filepath.Join(new.LogsDir, "aapanel_backup.log")
	}
	if err = c.Validate(new); err != nil {
		return nil, err
	}
	pairs := settingsPairs(old, new)
	sources := []SourcePreview{}
	for _, id := range []string{"backup-site", "backup-database", "backup-panel"} {
		if configuredSource(old, id) != configuredSource(new, id) {
			if configuredSource(new, id) == (SourceSpec{}) {
				return nil, errors.New("Chọn nguồn mới rõ ràng; không xóa cấu hình nguồn đã lưu")
			}
			sources = append(sources, SourcePreview{id, effectiveSource(old, id), effectiveSource(new, id)})
		}
	}
	p := &Proposal{Old: old, New: new, Scripts: []scriptChange{}, Logs: []logChange{}, Days: []dayChange{}}
	planned := map[string]bool{}
	missing := map[string]bool{}
	creating := r.Schedule != nil && r.Schedule.Create
	if creating {
		if _, ok := TaskScripts[r.Schedule.ID]; !ok {
			return nil, errors.New("Loại tác vụ không hợp lệ")
		}
	}
	for _, name := range ScriptNames {
		source, target := filepath.Join(old.ScriptsDir, name), filepath.Join(new.ScriptsDir, name)
		// Only genuinely absent files may be optional. Do not turn unreadable
		// paths, directories or dangling symlinks into missing-script exceptions.
		if err := SafeParents(source); err != nil {
			return nil, err
		}
		_, statErr := os.Lstat(source)
		if statErr != nil && !os.IsNotExist(statErr) {
			return nil, errors.New("Không kiểm tra được script được quản lý: " + name)
		}
		if os.IsNotExist(statErr) && creating {
			isTask := name == TaskScripts[r.Schedule.ID]
			isRunner := name == "backup-monitor-run.sh"
			if isTask || isRunner {
				if exists(target) || SafeParents(target) != nil {
					return nil, errors.New("Script đích đã tồn tại hoặc không an toàn: " + target)
				}
				var data []byte
				if isRunner {
					data = []byte(ScheduledRunner(new))
				} else {
					if r.Schedule.ID == "backup-site" || r.Schedule.ID == "backup-database" || r.Schedule.ID == "backup-panel" {
						if err = c.ValidateSource(new, r.Schedule.ID, effectiveSource(new, r.Schedule.ID)); err != nil {
							return nil, err
						}
					}
					data, err = GenerateTaskScript(r.Schedule.ID, new)
					if err != nil {
						return nil, err
					}
				}
				p.Scripts = append(p.Scripts, scriptChange{source, target, nil, data})
				planned[source] = true
				continue
			}
		}
		if os.IsNotExist(statErr) {
			for _, change := range sources {
				if name == TaskScripts[change.ID] && r.creatingTask != change.ID {
					return nil, errors.New("Thiếu script để cập nhật nguồn: " + name)
				}
			}
			if r.Schedule != nil && name == TaskScripts[r.Schedule.ID] {
				return nil, errors.New("Thiếu script của tác vụ đang sửa: " + name)
			}
			// Partial installations remain editable. Cron dependencies, including
			// the task dispatched by a wrapper, are checked below in every mode.
			missing[name] = true
			continue
		}
		original, err := readRegular(source)
		if err != nil {
			return nil, errors.New("Thiếu hoặc không đọc được script được quản lý: " + name)
		}
		if source != target && exists(target) {
			return nil, errors.New("Script đích đã tồn tại: " + target)
		}
		if name == "backup-site.sh" && !strings.Contains(string(original), LayoutMarker) {
			return nil, errors.New("Cần cài cấu trúc backup theo ngày trước khi áp dụng cấu hình")
		}
		if strings.Contains(string(original), "daily_backup_layout.py") {
			return nil, errors.New("Script vẫn dùng helper Python; hãy cài công cụ Go trước khi đổi cấu hình")
		}
		updated := []byte(Rewrite(string(original), pairs))
		for _, source := range sources {
			if name == source.ID+".sh" {
				patched, err := PatchSource(string(updated), source.ID, source.Proposed)
				if err != nil {
					return nil, err
				}
				updated = []byte(patched)
			}
		}
		p.Scripts = append(p.Scripts, scriptChange{source, target, original, updated})
	}
	if err = c.updatePrivateScripts(p); err != nil {
		return nil, err
	}
	logs := [][2]string{{old.DriveHistoryLog, new.DriveHistoryLog}}
	for _, name := range LogNames {
		logs = append(logs, [2]string{filepath.Join(old.LogsDir, name), filepath.Join(new.LogsDir, name)})
	}
	for _, pair := range logs {
		if pair[0] == pair[1] || !exists(pair[0]) {
			continue
		}
		if err = SafeParents(pair[1]); err != nil {
			return nil, err
		}
		if exists(pair[1]) {
			return nil, errors.New("Log đích đã tồn tại: " + pair[1])
		}
		sum, err := Digest(pair[0])
		if err != nil {
			return nil, err
		}
		p.Logs = append(p.Logs, logChange{pair[0], pair[1], sum})
	}
	if old.BackupRoot != new.BackupRoot {
		for _, loc := range old.CronLocations {
			if loc.Options != nil && loc.Options.BackupRoot == old.BackupRoot {
				return nil, errors.New("Nơi lưu chung đang được cron riêng sử dụng; đổi nơi lưu qua từng cron, không chuyển cả kho chung")
			}
		}
		if within(new.BackupRoot, old.BackupRoot) || within(old.BackupRoot, new.BackupRoot) {
			return nil, errors.New("Thư mục backup mới không được lồng trong hoặc chứa thư mục cũ")
		}
		original, err := os.Stat(old.BackupRoot)
		if err != nil {
			return nil, err
		}
		targetInfo, err := nearest(new.BackupRoot)
		if err != nil {
			return nil, err
		}
		if device(original) != device(targetInfo) {
			return nil, errors.New("Bản đầu tiên chỉ hỗ trợ đổi thư mục trên cùng filesystem; chưa chuyển giữa hai ổ đĩa")
		}
		entries, err := os.ReadDir(old.BackupRoot)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !ValidDay(entry.Name()) {
				continue
			}
			source, target := filepath.Join(old.BackupRoot, entry.Name()), filepath.Join(new.BackupRoot, entry.Name())
			if err = SafeParents(source); err != nil {
				return nil, err
			}
			if !entry.IsDir() {
				continue
			}
			if err = SafeParents(target); err != nil {
				return nil, err
			}
			if exists(target) {
				return nil, errors.New("Thư mục ngày đích đã tồn tại: " + target)
			}
			day := dayChange{Source: source, Target: target, Files: []fileCheck{}}
			err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if err := SafeParents(path); err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return errors.New("Tệp không hợp lệ trong thư mục backup")
				}
				sum, err := Digest(path)
				if err != nil {
					return err
				}
				relative, err := filepath.Rel(source, path)
				if err != nil {
					return err
				}
				day.Files = append(day.Files, fileCheck{relative, info.Size(), sum})
				return nil
			})
			if err != nil {
				return nil, err
			}
			p.Days = append(p.Days, day)
		}
	}
	p.Cron, err = c.readStorageCron()
	if err != nil {
		return nil, err
	}
	if err := validateMissingCronScripts(p.Cron, old.ScriptsDir, missing); err != nil {
		return nil, err
	}
	for key, loc := range old.CronLocations {
		local, _ := CronSettings(old, loc.ID, key)
		missing := map[string]bool{}
		for _, name := range []string{TaskScripts[loc.ID], "backup-monitor-run.sh"} {
			script := scriptPath(local, name)
			if err := SafeParents(script); err != nil {
				return nil, err
			}
			_, err := os.Lstat(script)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			missing[name] = os.IsNotExist(err)
		}
		if err := validateMissingCronSettings(p.Cron, local, missing); err != nil {
			return nil, err
		}
	}
	scheduledCron, schedulePreview, err := proposeSchedulePlanned(p.Cron, old.ScriptsDir, r.Schedule, planned, old.LogsDir)
	if err != nil {
		return nil, err
	}
	managed := map[string]bool{}
	for _, name := range ScriptNames {
		managed[filepath.Join(old.ScriptsDir, name)] = true
	}
	lines := strings.Split(strings.TrimSuffix(string(scheduledCron), "\n"), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") && !strings.HasPrefix(line, "# backup-monitor-paused ") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if managed[field] {
				lines[i] = Rewrite(line, pairs)
				break
			}
		}
	}
	p.NewCron = []byte(strings.Join(lines, "\n") + "\n")
	fingerprints := []interface{}{Version, version, new, hashBytes(p.Cron), r.Schedule, hashBytes(p.NewCron)}
	for _, script := range p.Scripts {
		fingerprints = append(fingerprints, []string{script.Source, script.Target, hashBytes(script.Original), hashBytes(script.Updated)})
	}
	for _, log := range p.Logs {
		fingerprints = append(fingerprints, []string{log.Source, log.Hash})
	}
	for _, day := range p.Days {
		fingerprints = append(fingerprints, day)
	}
	raw, err := json.Marshal(fingerprints)
	if err != nil {
		return nil, err
	}
	p.Summary = Preview{Token: hashBytes(raw), Changed: len(pairs) > 0 || len(sources) > 0 || (schedulePreview != nil && schedulePreview.Changed), DaysToMove: len(p.Days), LogsToMove: len(p.Logs), DriveChanged: old.DriveRemote != new.DriveRemote || old.DriveFolder != new.DriveFolder, Config: new, Version: version, Schedule: schedulePreview, Sources: sources}
	p.Summary.ScriptsToCreate = []string{}
	for _, script := range p.Scripts {
		if script.Original == nil {
			p.Summary.ScriptsToCreate = append(p.Summary.ScriptsToCreate, script.Target)
			p.Summary.Changed = true
		} else if script.Source != script.Target || string(script.Original) != string(script.Updated) {
			p.Summary.ScriptsToUpdate++
		}
	}
	for _, day := range p.Days {
		for _, file := range day.Files {
			p.Summary.FilesToMove++
			p.Summary.BytesToMove += file.Size
		}
	}
	return p, nil
}

// Paused jobs still depend on their scripts, but ordinary cron comments do not.
// Wrapped cron lines name the runner rather than the dispatched task's file;
// checking only literal script paths would miss a deleted backup task.
func validateMissingCronScripts(cron []byte, scriptsDir string, missing map[string]bool) error {
	return validateMissingCronSettings(cron, Settings{ScriptsDir: scriptsDir}, missing)
}
func validateMissingCronSettings(cron []byte, cfg Settings, missing map[string]bool) error {
	paths := map[string]string{}
	for _, name := range ScriptNames {
		paths[scriptPath(cfg, name)] = name
	}
	wrapper := scriptPath(cfg, "backup-monitor-run.sh")
	for _, line := range strings.Split(string(cron), "\n") {
		body := strings.TrimSpace(line)
		if strings.HasPrefix(body, pausedSchedulePrefix) {
			body = strings.TrimPrefix(body, pausedSchedulePrefix)
		} else if strings.HasPrefix(body, "#") {
			continue
		}
		fields := strings.Fields(body)
		for i, field := range fields {
			if strings.HasPrefix(field, "#") {
				break
			}
			if name := paths[field]; missing[name] {
				return errors.New("Cron hiện có trỏ tới script bị thiếu: " + name)
			}
			if field == wrapper && i+1 < len(fields) {
				if name := TaskScripts[fields[i+1]]; missing[name] {
					return errors.New("Cron hiện có trỏ tới script bị thiếu: " + name)
				}
			}
		}
	}
	return nil
}

func verifyDay(day dayChange) error {
	for _, file := range day.Files {
		path := filepath.Join(day.Target, file.Relative)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		sum, err := Digest(path)
		if err != nil {
			return err
		}
		if info.Size() != file.Size || sum != file.Hash {
			return errors.New("Kiểm tra dữ liệu sau chuyển thất bại")
		}
	}
	return nil
}
func acquireAll(paths []string) ([]*os.File, error) {
	locks := []*os.File{}
	for _, path := range paths {
		f, err := acquire(path, false, true)
		if err != nil {
			releaseAll(locks)
			return nil, err
		}
		locks = append(locks, f)
	}
	return locks, nil
}
func releaseAll(locks []*os.File) {
	for i := len(locks) - 1; i >= 0; i-- {
		_ = locks[i].Close()
	}
}
func (c *Controller) Apply(r Request) (state State, resultErr error) {
	locks, err := acquireAll(c.Locks)
	if err != nil {
		return state, err
	}
	defer func() { releaseAll(locks) }()
	p, err := c.Proposal(r)
	if err != nil {
		return state, err
	}
	if r.PreviewToken != p.Summary.Token {
		return state, errors.New("Dữ liệu đã thay đổi sau khi xem trước; hãy xem trước lại")
	}
	if !p.Summary.Changed {
		return c.Get()
	}
	temporary, err := os.MkdirTemp("", "backup-system-validate-*")
	if err != nil {
		return state, err
	}
	defer os.RemoveAll(temporary)
	for _, script := range p.Scripts {
		target := filepath.Join(temporary, hashBytes([]byte(script.Source))[:16]+"-"+filepath.Base(script.Source))
		if err = AtomicWrite(target, script.Updated, 0600); err != nil {
			return state, err
		}
		if _, err = c.Run([]string{"/bin/bash", "-n", target}, nil); err != nil {
			return state, errors.New("Script cập nhật không hợp lệ: " + filepath.Base(script.Source))
		}
	}
	if p.Summary.DriveChanged && r.Schedule == nil {
		if err = c.Create(Request{Location: "drive", Remote: p.New.DriveRemote, Path: p.New.DriveFolder}); err != nil {
			return state, err
		}
	}
	for _, path := range []string{p.New.BackupRoot, p.New.ScriptsDir, p.New.LogsDir, c.RecoveryRoot} {
		if err = mkdirPrivate(path); err != nil {
			return state, err
		}
	}
	for _, script := range p.Scripts {
		if err = mkdirPrivate(filepath.Dir(script.Target)); err != nil {
			return state, err
		}
	}
	for _, log := range p.Logs {
		if err = mkdirPrivate(filepath.Dir(log.Target)); err != nil {
			return state, err
		}
	}
	for key, loc := range p.New.CronLocations {
		cfg, _ := CronSettings(p.New, loc.ID, key)
		if err = mkdirPrivate(cfg.BackupRoot); err != nil {
			return state, err
		}
		if err = mkdirPrivate(cfg.LogsDir); err != nil {
			return state, err
		}
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "system-config-go-*")
	if err != nil {
		return state, err
	}
	previous, _ := json.MarshalIndent(p.Old, "", "  ")
	if err = c.Write(filepath.Join(recovery, "previous.json"), previous, 0600); err != nil {
		return state, err
	}
	if err = c.Write(filepath.Join(recovery, "crontab.txt"), p.Cron, 0600); err != nil {
		return state, err
	}
	for _, script := range p.Scripts {
		if script.Original == nil {
			continue
		}
		if err = c.Write(filepath.Join(recovery, hashBytes([]byte(script.Source))[:16]+"-"+filepath.Base(script.Source)), script.Original, 0600); err != nil {
			return state, err
		}
	}
	manifest, _ := json.MarshalIndent(struct {
		Days     []dayChange
		Logs     []logChange
		Archives []archiveTransfer
		Ledgers  []ledgerTransfer
	}{p.Days, p.Logs, p.Archives, p.Ledgers}, "", "  ")
	if err = c.Write(filepath.Join(recovery, "moves.json"), manifest, 0600); err != nil {
		return state, err
	}
	movedDays := []dayChange{}
	movedLogs := []logChange{}
	installed := []scriptChange{}
	stagedArchives := []archiveTransfer{}
	installedLedgers := []ledgerTransfer{}
	cronAttempted := false
	committed := false
	defer func() {
		if resultErr == nil || committed {
			return
		}
		rollbackErrors := []string{}
		for i := len(installedLedgers) - 1; i >= 0; i-- {
			ledger := installedLedgers[i]
			if sum, err := Digest(ledger.Target); err != nil || sum != hashBytes(ledger.Updated) {
				rollbackErrors = append(rollbackErrors, "metadata đã thay đổi")
				continue
			}
			var err error
			if ledger.Original == nil {
				err = os.Remove(ledger.Target)
			} else {
				err = c.Write(ledger.Target, ledger.Original, 0600)
			}
			if err != nil {
				rollbackErrors = append(rollbackErrors, "metadata")
			}
		}
		for i := len(stagedArchives) - 1; i >= 0; i-- {
			move := stagedArchives[i]
			if verifyTransferFile(move.Source, move) != nil || verifyTransferFile(move.Target, move) != nil {
				rollbackErrors = append(rollbackErrors, "backup đã thay đổi; giữ cả hai bản")
				continue
			}
			if err := os.Remove(move.Target); err != nil {
				rollbackErrors = append(rollbackErrors, "backup đích")
			}
		}
		if cronAttempted {
			if _, err := c.Run([]string{"crontab", "-"}, p.Cron); err != nil {
				rollbackErrors = append(rollbackErrors, "crontab")
			}
		}
		for i := len(installed) - 1; i >= 0; i-- {
			script := installed[i]
			var err error
			if script.Original == nil {
				sum, readErr := Digest(script.Target)
				if readErr != nil || sum != hashBytes(script.Updated) {
					err = errors.New("Script mới đã thay đổi; không xóa")
				} else {
					err = os.Remove(script.Target)
				}
			} else if script.Source == script.Target {
				err = c.Write(script.Source, script.Original, 0700)
			} else {
				if sum, readErr := Digest(script.Target); readErr != nil || sum != hashBytes(script.Updated) {
					err = errors.New("Script đích đã thay đổi; không xóa")
				} else {
					err = os.Remove(script.Target)
				}
			}
			if err != nil {
				rollbackErrors = append(rollbackErrors, filepath.Base(script.Source))
			}
		}
		for i := len(movedLogs) - 1; i >= 0; i-- {
			if err := os.Remove(movedLogs[i].Target); err != nil {
				rollbackErrors = append(rollbackErrors, "log")
			}
		}
		for i := len(movedDays) - 1; i >= 0; i-- {
			day := movedDays[i]
			if exists(day.Source) {
				rollbackErrors = append(rollbackErrors, "thư mục gốc đã xuất hiện lại")
				continue
			}
			if err := os.Rename(day.Target, day.Source); err != nil {
				rollbackErrors = append(rollbackErrors, "backup")
			}
		}
		if len(rollbackErrors) > 0 {
			resultErr = fmt.Errorf("Không hoàn tất khôi phục (%s); bản dự phòng: %s", strings.Join(rollbackErrors, ", "), recovery)
		}
	}()
	for _, move := range p.Archives {
		if err = stageBackupTransfer(move); err != nil {
			return state, err
		}
		stagedArchives = append(stagedArchives, move)
	}
	for _, ledger := range p.Ledgers {
		if err = SafeParents(ledger.Target); err != nil {
			return state, err
		}
		if ledger.Original == nil {
			if exists(ledger.Target) {
				return state, errors.New("Metadata đích đã xuất hiện sau xem trước")
			}
		} else if sum, readErr := Digest(ledger.Target); readErr != nil || sum != hashBytes(ledger.Original) {
			return state, errors.New("Metadata đích đã thay đổi sau xem trước")
		}
		if err = c.Write(ledger.Target, ledger.Updated, 0600); err != nil {
			return state, err
		}
		installedLedgers = append(installedLedgers, ledger)
	}
	for _, day := range p.Days {
		if err = SafeParents(day.Target); err != nil {
			return state, err
		}
		if exists(day.Target) {
			return state, errors.New("Đích đã xuất hiện sau xem trước")
		}
		if err = os.Rename(day.Source, day.Target); err != nil {
			return state, err
		}
		movedDays = append(movedDays, day)
		if err = verifyDay(day); err != nil {
			return state, err
		}
	}
	for _, log := range p.Logs {
		if err = copyExclusive(log.Source, log.Target); err != nil {
			return state, err
		}
		movedLogs = append(movedLogs, log)
		sum, err := Digest(log.Target)
		if err != nil {
			return state, err
		}
		if sum != log.Hash {
			return state, errors.New("Kiểm tra log sau chuyển thất bại")
		}
	}
	for _, script := range p.Scripts {
		if script.Original == nil {
			if err = writeScriptExclusive(script.Target, script.Updated); err != nil {
				return state, err
			}
			installed = append(installed, script)
			continue
		}
		if script.Source == script.Target && string(script.Original) == string(script.Updated) {
			continue
		}
		if script.Source != script.Target && exists(script.Target) {
			return state, errors.New("Script đích đã xuất hiện sau xem trước")
		}
		current, readErr := readRegular(script.Source)
		if readErr != nil || hashBytes(current) != hashBytes(script.Original) {
			return state, errors.New("Script đã thay đổi sau xem trước; chưa ghi đè")
		}
		if script.Source != script.Target {
			err = writeScriptExclusive(script.Target, script.Updated)
		} else {
			err = c.Write(script.Target, script.Updated, 0700)
		}
		if err != nil {
			return state, err
		}
		installed = append(installed, script)
	}
	// Never point crontab at a clone that did not survive installation.
	for _, script := range p.Scripts {
		if sum, readErr := Digest(script.Target); readErr != nil || sum != hashBytes(script.Updated) {
			return state, errors.New("Script đích thiếu hoặc thay đổi; chưa cập nhật cron: " + filepath.Base(script.Target))
		}
	}
	cronAttempted = true
	if _, err = c.Run([]string{"crontab", "-"}, p.NewCron); err != nil {
		return state, err
	}
	raw, _ := json.MarshalIndent(p.New, "", "  ")
	for _, move := range stagedArchives {
		if err = verifyTransferFile(move.Source, move); err != nil {
			return state, err
		}
		if err = verifyTransferFile(move.Target, move); err != nil {
			return state, err
		}
	}
	if err = c.Write(c.ConfigPath, raw, 0600); err != nil {
		return state, err
	}
	committed = true
	transferWarnings := []string{}
	for _, move := range stagedArchives {
		if verifyTransferFile(move.Source, move) != nil || verifyTransferFile(move.Target, move) != nil {
			transferWarnings = append(transferWarnings, "Nơi lưu mới đã áp dụng; bản gốc thay đổi nên được giữ lại: "+move.Source)
			continue
		}
		if err := os.Remove(move.Source); err != nil {
			transferWarnings = append(transferWarnings, "Backup đã có ở nơi mới; chưa xóa được bản gốc: "+move.Source)
		}
	}
	state, err = c.Get()
	if err != nil {
		return state, errors.New("Cấu hình đã áp dụng nhưng chưa xác nhận được trạng thái; hãy tải lại")
	}
	state.Applied = true
	state.RecoveryDir = recovery
	state.Warnings = append(state.Warnings, transferWarnings...)
	state.Warnings = append(state.Warnings, cleanupFlattenedCron(p, r.Schedule)...)
	// Unlock before restarting an existing monitor. Never start a monitor that
	// was stopped before this operation; never return failure after commit just
	// because restarting the monitor failed.
	releaseAll(locks)
	locks = nil
	if c.RestartMonitor != nil && r.Schedule == nil {
		warnings, err := c.RestartMonitor(p.Old, p.New)
		state.Warnings = warnings
		if err != nil {
			state.Warnings = []string{"Cấu hình đã áp dụng; chưa xác nhận được việc khởi động lại trình giám sát"}
		}
	}
	if state.Warnings == nil {
		state.Warnings = []string{}
	}
	return state, nil
}
