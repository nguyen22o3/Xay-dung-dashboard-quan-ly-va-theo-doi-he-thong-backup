package backupops

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// New locations use keyed filenames in the selected parents. Legacy locations
// retain their cron-ID folders until an explicit directory edit migrates them.
type CronDirectories struct {
	ScriptsDir string       `json:"scriptsDir"`
	LogsDir    string       `json:"logsDir"`
	Options    *CronOptions `json:"options,omitempty"`
}

// Snapshot the selected cron's settings instead of referring to shared defaults.
type CronOptions struct {
	BackupRoot  string     `json:"backupRoot"`
	DriveRemote string     `json:"driveRemote"`
	DriveFolder string     `json:"driveFolder"`
	Source      SourceSpec `json:"source,omitzero"`
}
type CronLocation struct {
	FileLayout string       `json:"fileLayout,omitempty"`
	ID         string       `json:"id"`
	ScriptsDir string       `json:"scriptsDir"`
	LogsDir    string       `json:"logsDir"`
	Options    *CronOptions `json:"options,omitempty"`
}

var cronKeyPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func taskSettings(cfg Settings, selector string) (Settings, string, error) {
	id, key, _ := strings.Cut(selector, "~")
	if _, ok := TaskScripts[id]; !ok {
		return Settings{}, "", errors.New("Tác vụ không hợp lệ")
	}
	cfg, err := CronSettings(cfg, id, key)
	return cfg, id, err
}

func CronSettings(cfg Settings, id, key string) (Settings, error) {
	if key == "" {
		return cfg, nil
	}
	loc, ok := cfg.CronLocations[key]
	if !cronKeyPattern.MatchString(key) || !ok || loc.ID != id {
		return Settings{}, errors.New("Cron không còn tồn tại trong cấu hình; hãy tải lại")
	}
	cfg.fileKey = ""
	if loc.FileLayout == "files" {
		cfg.ScriptsDir, cfg.LogsDir, cfg.fileKey = loc.ScriptsDir, loc.LogsDir, key
	} else {
		cfg.ScriptsDir = filepath.Join(loc.ScriptsDir, "cron-"+key)
		cfg.LogsDir = filepath.Join(loc.LogsDir, "cron-"+key)
	}
	if loc.Options != nil {
		cfg.BackupRoot = loc.Options.BackupRoot
		cfg.DriveRemote = loc.Options.DriveRemote
		cfg.DriveFolder = loc.Options.DriveFolder
		setSource(&cfg, id, loc.Options.Source)
		cfg.DriveHistoryLog = logPath(cfg, "aapanel_backup.log")
	}
	return cfg, nil
}
func setSource(cfg *Settings, id string, source SourceSpec) {
	switch id {
	case "backup-site":
		cfg.SiteSource = source
	case "backup-database":
		cfg.DatabaseSource = source
	case "backup-panel":
		cfg.PanelSource = source
	}
}
func cronOptions(cfg Settings, id string) *CronOptions {
	return &CronOptions{cfg.BackupRoot, cfg.DriveRemote, cfg.DriveFolder, effectiveSource(cfg, id)}
}
func (c *Controller) scopeSettings(cfg Settings) (Settings, error) {
	if c.CronSelector == "" {
		return cfg, nil
	}
	id, key, ok := strings.Cut(c.CronSelector, "~")
	if !ok || key == "" {
		return Settings{}, errors.New("Mã cron thực thi không hợp lệ")
	}
	return CronSettings(cfg, id, key)
}
func bindCronScript(data []byte, id, key string, cfg Settings) []byte {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = regexp.MustCompile(`(?m)^export BACKUP_MONITOR_CRON='[^'\n]*'\n`).ReplaceAllString(text, "")
	if at := strings.IndexByte(text, '\n'); at >= 0 {
		text = text[:at+1] + "export BACKUP_MONITOR_CRON='" + id + "~" + key + "'\n" + text[at+1:]
	}
	if id == "backup-panel" {
		text = regexp.MustCompile(`(?m)^(BACKUP_ROOT|LOG_FILE)='[^'\n]*'\n`).ReplaceAllString(text, "")
		at := strings.IndexByte(text, '\n')
		text = text[:at+1] + "BACKUP_ROOT='" + cfg.BackupRoot + "'\nLOG_FILE='" + logPath(cfg, "backup-panel.log") + "'\n" + text[at+1:]
	}
	return []byte(text)
}
func sameCronLocations(a, b map[string]CronLocation) bool {
	return (len(a) == 0 && len(b) == 0) || reflect.DeepEqual(a, b)
}
func (c *Controller) validateCronLocations(cfg Settings) error {
	if len(cfg.CronLocations) > 128 {
		return errors.New("Tối đa 128 cron có thư mục riêng")
	}
	for key, loc := range cfg.CronLocations {
		if loc.FileLayout != "" && loc.FileLayout != "files" {
			return errors.New("Cấu trúc script/log của cron không hợp lệ")
		}
		if !cronKeyPattern.MatchString(key) {
			return errors.New("Mã cron không hợp lệ")
		}
		if _, ok := TaskScripts[loc.ID]; !ok {
			return errors.New("Loại cron không hợp lệ")
		}
		for _, v := range []struct{ p, purpose string }{{loc.ScriptsDir, "scripts"}, {loc.LogsDir, "logs"}} {
			if err := c.LocalPath(v.p, v.purpose); err != nil {
				return err
			}
			if err := SafeParents(v.p); err != nil {
				return err
			}
		}
		local, _ := CronSettings(cfg, loc.ID, key)
		if loc.Options != nil {
			check := local
			check.CronLocations = nil // Validate without recursive map traversal.
			if err := c.Validate(check); err != nil {
				return err
			}
		}
		// Backup roots remain non-nested and separate from every source.
		// Auxiliary paths are checked together below against every backup root.
		others := []Settings{cfg}
		for otherKey, otherLoc := range cfg.CronLocations {
			if otherKey != key {
				other, _ := CronSettings(cfg, otherLoc.ID, otherKey)
				others = append(others, other)
			}
		}
		for _, other := range others {
			if local.BackupRoot != other.BackupRoot && (within(local.BackupRoot, other.BackupRoot) || within(other.BackupRoot, local.BackupRoot)) {
				return errors.New("Các nơi lưu backup khác nhau không được lồng vào nhau")
			}
			for _, sourceID := range []string{"backup-site", "backup-database", "backup-panel"} {
				source := effectiveSource(other, sourceID).Path
				for _, dir := range []string{local.ScriptsDir, local.LogsDir, local.BackupRoot} {
					if within(dir, source) || within(source, dir) {
						return errors.New("Đường dẫn cron phải tách khỏi mọi nguồn dữ liệu")
					}
				}
			}
		}
		for _, id := range []string{"backup-site", "backup-database", "backup-panel"} {
			source := effectiveSource(local, id).Path
			for _, dir := range []string{local.ScriptsDir, local.LogsDir, local.BackupRoot} {
				if within(dir, source) || within(source, dir) {
					return errors.New("Backup/script/log riêng phải tách khỏi nguồn dữ liệu")
				}
			}
		}
	}
	return validateAuxiliaryLocations(cfg)
}
func privateRunner(global, local Settings) []byte {
	// Keep the dashboard's structured history central; verbose logs and process
	// state are private to this cron. No credentials are written into wrappers.
	return []byte(Rewrite(ScheduledRunner(local), [][2]string{{logPath(local, "backup-monitor-runs.log"), logPath(global, "backup-monitor-runs.log")}}))
}
func (c *Controller) updatePrivateScripts(p *Proposal) error {
	keys := []string{}
	for key := range p.Old.CronLocations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		loc := p.Old.CronLocations[key]
		old, _ := CronSettings(p.Old, loc.ID, key)
		next, _ := CronSettings(p.New, loc.ID, key)
		for _, name := range []string{TaskScripts[loc.ID], "backup-monitor-run.sh"} {
			source := scriptPath(old, name)
			if !exists(source) {
				continue
			} // Deleted schedules retain their location metadata.
			data, err := readRegular(source)
			if err != nil {
				return err
			}
			updated := []byte(Rewrite(string(data), settingsPairs(old, next)))
			if name == "backup-monitor-run.sh" {
				updated = []byte(Rewrite(string(updated), [][2]string{{filepath.Join(p.Old.LogsDir, "backup-monitor-runs.log"), filepath.Join(p.New.LogsDir, "backup-monitor-runs.log")}}))
			}
			if name == TaskScripts[loc.ID] && effectiveSource(old, loc.ID) != effectiveSource(next, loc.ID) {
				patched, err := PatchSource(string(updated), loc.ID, effectiveSource(next, loc.ID))
				if err != nil {
					return err
				}
				updated = []byte(patched)
			}
			p.Scripts = append(p.Scripts, scriptChange{source, scriptPath(next, name), data, updated})
		}
	}
	return nil
}

func (c *Controller) cronProposal(r Request) (*Proposal, error) {
	change := *r.Schedule
	old, _, err := c.Active()
	if err != nil {
		return nil, err
	}
	if r.Config == nil || !sameCronLocations(old.CronLocations, r.Config.CronLocations) {
		return nil, errors.New("Đường dẫn cron đã thay đổi; tải lại")
	}
	oldLocal, err := CronSettings(old, change.ID, change.InstanceKey)
	if err != nil {
		return nil, err
	}
	var selectedScript *ManagedScriptOption
	var selectedData []byte
	selectedSettings := oldLocal
	if change.SourceScript != nil {
		if !change.Create || change.InstanceKey != "" || change.Directories == nil {
			return nil, errors.New("Chỉ chọn script nguồn khi thêm cron mới có thư mục riêng")
		}
		option, local, data, e := c.managedScript(old, change.ID, change.SourceScript.Selector)
		if e != nil {
			return nil, e
		}
		if option.Hash != change.SourceScript.Hash {
			return nil, errors.New("Script nguồn đã thay đổi; tải lại danh sách và chọn lại")
		}
		selectedScript, selectedSettings, selectedData = &option, local, data
	}
	if !reflect.DeepEqual(old, *r.Config) {
		return nil, errors.New("Sửa cron không được thay đổi cấu hình chung; cập nhật dashboard để gửi cấu hình riêng từng cron")
	}
	base := r
	base.Schedule = nil
	if change.Create {
		base.creatingTask = change.ID
	}
	p, err := c.Proposal(base)
	if err != nil {
		return nil, err
	}
	next := p.New
	next.CronLocations = map[string]CronLocation{}
	for k, v := range old.CronLocations {
		next.CronLocations[k] = v
	}
	key := change.InstanceKey
	if change.Directories != nil {
		if key == "" {
			key = hashBytes([]byte(change.ID + "\n" + change.ExpectedSchedule + "\n" + hashBytes(p.Cron)))[:32]
			if _, ok := next.CronLocations[key]; ok {
				return nil, errors.New("Cron đã có thư mục riêng; tải lại")
			}
		}
		options := change.Directories.Options
		if options == nil {
			options = cronOptions(oldLocal, change.ID)
		}
		if options.Source == (SourceSpec{}) {
			copy := *options
			copy.Source = effectiveSource(oldLocal, change.ID)
			options = &copy
		}
		next.CronLocations[key] = CronLocation{FileLayout: "files", ID: change.ID, ScriptsDir: change.Directories.ScriptsDir, LogsDir: change.Directories.LogsDir, Options: options}
	}
	if err = c.Validate(next); err != nil {
		return nil, err
	}
	nextLocal, err := CronSettings(next, change.ID, key)
	if err != nil {
		return nil, err
	}
	backupTask := change.ID == "backup-site" || change.ID == "backup-database" || change.ID == "backup-panel"
	if backupTask && !change.Create && oldLocal.BackupRoot != nextLocal.BackupRoot && !change.MoveBackups {
		return nil, errors.New("Đổi nơi lưu cron sao lưu phải kèm chuyển backup cũ; cập nhật dashboard rồi xem trước lại")
	}
	if change.MoveBackups {
		if change.Create || oldLocal.BackupRoot == nextLocal.BackupRoot {
			return nil, errors.New("Chỉ chuyển backup khi đổi nơi lưu của cron sao lưu đã tồn tại")
		}
		if err = c.planCronBackupTransfer(p, change, oldLocal, nextLocal); err != nil {
			return nil, err
		}
	}
	moved := oldLocal.ScriptsDir != nextLocal.ScriptsDir || oldLocal.LogsDir != nextLocal.LogsDir || oldLocal.fileKey != nextLocal.fileKey
	if moved && change.InstanceKey != "" {
		// A private namespace belongs to one row. Do not remove or rename files
		// still referenced by another schedule (even a different dispatched task).
		references := 0
		for _, line := range strings.Split(string(p.Cron), "\n") {
			body := strings.TrimSpace(strings.TrimPrefix(line, pausedSchedulePrefix))
			if strings.HasPrefix(body, "#") {
				continue
			}
			fields := strings.Fields(body)
			for _, field := range fields {
				if field == scriptPath(oldLocal, TaskScripts[change.ID]) || field == scriptPath(oldLocal, "backup-monitor-run.sh") {
					references++
					break
				}
			}
		}
		if references != 1 {
			return nil, errors.New("Script riêng đang được nhiều lịch tham chiếu hoặc cron đã thay đổi; chưa chuyển file")
		}
	}
	configChanged := !reflect.DeepEqual(cronOptions(oldLocal, change.ID), cronOptions(nextLocal, change.ID)) || !sameCronLocations(old.CronLocations, next.CronLocations)
	planned := map[string]bool{}
	if moved || change.Create || configChanged {
		// Clone or update only the selected task with its effective settings.
		for _, name := range []string{TaskScripts[change.ID], "backup-monitor-run.sh"} {
			if name == "" {
				return nil, errors.New("Loại tác vụ không hợp lệ")
			}
			source, target := scriptPath(oldLocal, name), scriptPath(nextLocal, name)
			fromSettings := oldLocal
			if selectedScript != nil && name == TaskScripts[change.ID] {
				source, fromSettings = selectedScript.Path, selectedSettings
			}
			if SafeParents(target) != nil || (source != target && exists(target)) {
				return nil, errors.New("Script đích đã tồn tại hoặc không an toàn")
			}
			original, readErr := readRegular(source)
			if selectedScript != nil && name == TaskScripts[change.ID] {
				original, readErr = selectedData, nil
			}
			if readErr != nil && (!os.IsNotExist(readErr) || (!change.Create && name != "backup-monitor-run.sh")) {
				return nil, errors.New("Không đọc được script của cron đang sửa: " + name)
			}
			data := original
			if name == "backup-monitor-run.sh" {
				data = privateRunner(next, nextLocal)
			} else if original == nil {
				data, err = GenerateTaskScript(change.ID, nextLocal)
				if err != nil {
					return nil, err
				}
			} else {
				data = []byte(Rewrite(string(data), settingsPairs(fromSettings, nextLocal)))
				if effectiveSource(fromSettings, change.ID) != effectiveSource(nextLocal, change.ID) {
					patched, err := PatchSource(string(data), change.ID, effectiveSource(nextLocal, change.ID))
					if err != nil {
						return nil, err
					}
					data = []byte(patched)
				}
			}
			if key != "" {
				data = bindCronScript(data, change.ID, key, nextLocal)
			}
			// An unchanged source copy belongs to other cron rows; never rewrite it.
			if source == target {
				filtered := p.Scripts[:0]
				for _, s := range p.Scripts {
					if s.Source != source {
						filtered = append(filtered, s)
					}
				}
				p.Scripts = filtered
			}
			// Keep clones before source-path entries in the transactional plan.
			p.Scripts = append([]scriptChange{{source, target, original, data}}, p.Scripts...)
			planned[target] = true
		}
		logName, _ := taskLogName(change.ID)
		logPairs := [][2]string{{logPath(oldLocal, logName), logPath(nextLocal, logName)}}
		if change.ID == "drive-sync" && change.InstanceKey != "" {
			logPairs = append(logPairs, [2]string{oldLocal.DriveHistoryLog, nextLocal.DriveHistoryLog})
		}
		for _, pair := range logPairs {
			source, target := pair[0], pair[1]
			if source != target && exists(source) {
				if exists(target) || SafeParents(target) != nil {
					return nil, errors.New("Log đích đã tồn tại hoặc không an toàn")
				}
				sum, err := Digest(source)
				if err != nil {
					return nil, err
				}
				p.Logs = append(p.Logs, logChange{source, target, sum})
			}
		}
	}
	// Select the row with its original effective script directory.
	scheduleLocal := oldLocal
	if change.InstanceKey == "" {
		scheduleLocal.ScriptsDir = p.New.ScriptsDir
		scheduleLocal.LogsDir = p.New.LogsDir
	}
	if change.Create {
		scheduleLocal = nextLocal
	}
	updated, preview, err := proposeScheduleSettings(p.NewCron, scheduleLocal, &change, planned)
	if err != nil {
		return nil, err
	}
	// Keep the existing no-duplicate-schedule rule across namespaces, not just
	// within one script directory (including paused schedules).
	directories := map[string]Settings{scriptPath(old, TaskScripts[change.ID]): old}
	for k, loc := range old.CronLocations {
		if loc.ID == change.ID {
			local, _ := CronSettings(old, loc.ID, k)
			directories[scriptPath(local, TaskScripts[change.ID])] = local
		}
	}
	for _, line := range strings.Split(string(p.Cron), "\n") {
		f := strings.Fields(strings.TrimPrefix(line, pausedSchedulePrefix))
		if len(f) < 7 || f[5] != "/bin/bash" || strings.Join(f[:5], " ") != preview.Proposed {
			continue
		}
		for _, local := range directories {
			direct := f[6] == scriptPath(local, TaskScripts[change.ID])
			wrapped := len(f) >= 8 && f[6] == scriptPath(local, "backup-monitor-run.sh") && f[7] == change.ID
			if !direct && !wrapped {
				continue
			}
			if !change.Create && scriptPath(local, TaskScripts[change.ID]) == scriptPath(oldLocal, TaskScripts[change.ID]) && strings.Join(f[:5], " ") == change.ExpectedSchedule {
				continue
			}
			return nil, errors.New("Loại tác vụ này đã có đúng lịch mới (kể cả lịch tạm dừng); hãy chọn chu kỳ khác")
		}
	}
	if moved && !change.Create {
		lines := strings.Split(string(updated), "\n")
		matches := 0
		for i, line := range lines {
			body := strings.TrimPrefix(line, pausedSchedulePrefix)
			f := strings.Fields(body)
			if len(f) < 7 || f[5] != "/bin/bash" || strings.Join(f[:5], " ") != preview.Proposed {
				continue
			}
			direct := f[6] == scriptPath(scheduleLocal, TaskScripts[change.ID])
			wrapped := len(f) >= 8 && f[6] == scriptPath(scheduleLocal, "backup-monitor-run.sh") && f[7] == change.ID
			if !direct && !wrapped {
				continue
			}
			// Normalize the managed command, preserve task arguments and pause state.
			tokens := scheduleTokens.FindAllStringIndex(body, -1)
			end := 7
			if wrapped {
				end = 8
			}
			suffix := body[tokens[end-1][1]:]
			logName, _ := taskLogName(change.ID)
			suffix = Rewrite(suffix, [][2]string{{logPath(scheduleLocal, logName), logPath(nextLocal, logName)}})
			lines[i] = body[:tokens[5][0]] + "/bin/bash " + scriptPath(nextLocal, "backup-monitor-run.sh") + " " + change.ID + suffix
			if strings.HasPrefix(line, pausedSchedulePrefix) {
				lines[i] = pausedSchedulePrefix + lines[i]
			}
			matches++
		}
		if matches != 1 {
			return nil, errors.New("Cron đã thay đổi; tải lại")
		}
		updated = []byte(strings.Join(lines, "\n"))
		preview.Changed = true
	}
	p.New = next
	p.NewCron = updated
	p.Summary.Config = next
	p.Summary.SourceScript = selectedScript
	p.Summary.Schedule = preview
	p.Summary.CronDirectories = &CronDirectories{ScriptsDir: nextLocal.ScriptsDir, LogsDir: nextLocal.LogsDir, Options: cronOptions(nextLocal, change.ID)}
	if nextLocal.fileKey != "" {
		logName, _ := taskLogName(change.ID)
		p.Summary.CronFiles = &CronFilePreview{Script: scriptPath(nextLocal, TaskScripts[change.ID]), Runner: scriptPath(nextLocal, "backup-monitor-run.sh"), Log: logPath(nextLocal, logName), Flattening: change.InstanceKey != "" && oldLocal.fileKey == ""}
	}
	p.Summary.Changed = p.Summary.Changed || moved || configChanged || preview.Changed
	p.Summary.Sources = nil
	if effectiveSource(oldLocal, change.ID) != effectiveSource(nextLocal, change.ID) {
		p.Summary.Sources = []SourcePreview{{change.ID, effectiveSource(oldLocal, change.ID), effectiveSource(nextLocal, change.ID)}}
	}
	// Storage-only edits for non-backup tasks never migrate archives.
	p.Summary.DriveChanged = false
	p.Summary.LogsToMove = len(p.Logs)
	p.Summary.ScriptsToUpdate = 0
	p.Summary.ScriptsToCreate = []string{}
	for _, s := range p.Scripts {
		if s.Original == nil {
			p.Summary.ScriptsToCreate = append(p.Summary.ScriptsToCreate, s.Target)
		} else if s.Source != s.Target || string(s.Original) != string(s.Updated) {
			p.Summary.ScriptsToUpdate++
		}
	}
	raw, _ := json.Marshal(struct {
		Base         string
		New          Settings
		Cron         string
		Scripts      []scriptChange
		Logs         []logChange
		Archives     []archiveTransfer
		Ledgers      []ledgerTransfer
		SourceScript *ManagedScriptOption
	}{p.Summary.Token, next, hashBytes(updated), p.Scripts, p.Logs, p.Archives, p.Ledgers, selectedScript})
	p.Summary.Token = hashBytes(raw)
	return p, nil
}
