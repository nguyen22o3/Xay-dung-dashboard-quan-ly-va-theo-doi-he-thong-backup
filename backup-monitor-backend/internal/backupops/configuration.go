package backupops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Preview struct {
	Token           string           `json:"token"`
	Changed         bool             `json:"changed"`
	FilesToMove     int              `json:"filesToMove"`
	BytesToMove     int64            `json:"bytesToMove"`
	DaysToMove      int              `json:"daysToMove"`
	ScriptsToUpdate int              `json:"scriptsToUpdate"`
	LogsToMove      int              `json:"logsToMove"`
	DriveChanged    bool             `json:"driveChanged"`
	Config          Settings         `json:"config"`
	Version         string           `json:"version"`
	Schedule        *SchedulePreview `json:"schedule,omitempty"`
	Sources         []SourcePreview  `json:"sources,omitempty"`
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
	Summary       Preview
	Old, New      Settings
	Scripts       []scriptChange
	Logs          []logChange
	Days          []dayChange
	Cron, NewCron []byte
}

func settingsPairs(old, new Settings) [][2]string {
	pairs := [][2]string{{old.ScriptsDir, new.ScriptsDir}, {old.BackupRoot, new.BackupRoot}, {old.DriveRemote + ":" + old.DriveFolder, new.DriveRemote + ":" + new.DriveFolder}, {old.DriveHistoryLog, new.DriveHistoryLog}}
	for _, name := range LogNames {
		pairs = append(pairs, [2]string{filepath.Join(old.LogsDir, name), filepath.Join(new.LogsDir, name)})
	}
	result := [][2]string{}
	for _, p := range pairs {
		if p[0] != p[1] {
			result = append(result, p)
		}
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
	for _, name := range ScriptNames {
		source, target := filepath.Join(old.ScriptsDir, name), filepath.Join(new.ScriptsDir, name)
		if !exists(source) && (name == "backup-monitor-run.sh" || name == "backup-panel.sh") {
			if name == "backup-panel.sh" && configuredSource(old, "backup-panel") != configuredSource(new, "backup-panel") {
				return nil, errors.New("Thiếu script backup-panel.sh; chưa lưu nguồn aaPanel")
			}
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
	p.Cron, err = c.Run([]string{"crontab", "-l"}, nil)
	if err != nil {
		return nil, err
	}
	scheduledCron, schedulePreview, err := proposeSchedule(p.Cron, old.ScriptsDir, r.Schedule, old.LogsDir)
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
		fingerprints = append(fingerprints, []string{script.Source, hashBytes(script.Original)})
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
	for _, script := range p.Scripts {
		if script.Source != script.Target || string(script.Original) != string(script.Updated) {
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
		target := filepath.Join(temporary, filepath.Base(script.Source))
		if err = AtomicWrite(target, script.Updated, 0600); err != nil {
			return state, err
		}
		if _, err = c.Run([]string{"/bin/bash", "-n", target}, nil); err != nil {
			return state, errors.New("Script cập nhật không hợp lệ: " + filepath.Base(script.Source))
		}
	}
	if p.Summary.DriveChanged {
		if err = c.Create(Request{Location: "drive", Remote: p.New.DriveRemote, Path: p.New.DriveFolder}); err != nil {
			return state, err
		}
	}
	for _, path := range []string{p.New.BackupRoot, p.New.ScriptsDir, p.New.LogsDir, c.RecoveryRoot} {
		if err = mkdirPrivate(path); err != nil {
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
		if err = c.Write(filepath.Join(recovery, filepath.Base(script.Source)), script.Original, 0600); err != nil {
			return state, err
		}
	}
	manifest, _ := json.MarshalIndent(struct {
		Days []dayChange
		Logs []logChange
	}{p.Days, p.Logs}, "", "  ")
	if err = c.Write(filepath.Join(recovery, "moves.json"), manifest, 0600); err != nil {
		return state, err
	}
	movedDays := []dayChange{}
	movedLogs := []logChange{}
	installed := []scriptChange{}
	cronAttempted := false
	committed := false
	defer func() {
		if resultErr == nil || committed {
			return
		}
		rollbackErrors := []string{}
		if cronAttempted {
			if _, err := c.Run([]string{"crontab", "-"}, p.Cron); err != nil {
				rollbackErrors = append(rollbackErrors, "crontab")
			}
		}
		for i := len(installed) - 1; i >= 0; i-- {
			script := installed[i]
			var err error
			if script.Source == script.Target {
				err = c.Write(script.Source, script.Original, 0700)
			} else {
				err = os.Remove(script.Target)
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
		if script.Source == script.Target && string(script.Original) == string(script.Updated) {
			continue
		}
		if script.Source != script.Target && exists(script.Target) {
			return state, errors.New("Script đích đã xuất hiện sau xem trước")
		}
		if err = c.Write(script.Target, script.Updated, 0700); err != nil {
			return state, err
		}
		installed = append(installed, script)
	}
	cronAttempted = true
	if _, err = c.Run([]string{"crontab", "-"}, p.NewCron); err != nil {
		return state, err
	}
	raw, _ := json.MarshalIndent(p.New, "", "  ")
	if err = c.Write(c.ConfigPath, raw, 0600); err != nil {
		return state, err
	}
	committed = true
	state, err = c.Get()
	if err != nil {
		return state, errors.New("Cấu hình đã áp dụng nhưng chưa xác nhận được trạng thái; hãy tải lại")
	}
	state.Applied = true
	state.RecoveryDir = recovery
	// Unlock before restarting an existing monitor. Never start a monitor that
	// was stopped before this operation; never return failure after commit just
	// because restarting the monitor failed.
	releaseAll(locks)
	locks = nil
	if c.RestartMonitor != nil {
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
