package backupops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ScheduleChange struct {
	SourceScript     *ManagedScriptSelection `json:"sourceScript,omitempty"`
	MoveBackups      bool                    `json:"moveBackups,omitempty"`
	InstanceKey      string                  `json:"instanceKey,omitempty"`
	Directories      *CronDirectories        `json:"directories,omitempty"`
	ID               string                  `json:"id"`
	Clock            string                  `json:"clock"`
	ExpectedSchedule string                  `json:"expectedSchedule"`
	ExpectedEnabled  *bool                   `json:"expectedEnabled"`
	Cycle            *ScheduleCycle          `json:"cycle,omitempty"`
	Create           bool                    `json:"create,omitempty"`
}

type ScheduleCycle struct {
	Type    string `json:"type"`
	Every   int    `json:"every,omitempty"`
	Weekday int    `json:"weekday,omitempty"`
	Day     int    `json:"day,omitempty"`
}

func cycleSchedule(change *ScheduleChange) (string, error) {
	if !scheduleClockPattern.MatchString(change.Clock) {
		return "", errors.New("Giờ thực hiện không hợp lệ")
	}
	hour, _ := strconv.Atoi(change.Clock[:2])
	minute, _ := strconv.Atoi(change.Clock[3:])
	h, m := strconv.Itoa(hour), strconv.Itoa(minute)
	if change.Cycle == nil {
		return "", nil
	}
	c := change.Cycle
	switch c.Type {
	case "daily":
		return m + " " + h + " * * *", nil
	case "hourly":
		return m + " * * * *", nil
	case "days":
		if c.Every >= 2 && c.Every <= 31 {
			return m + " " + h + " */" + strconv.Itoa(c.Every) + " * *", nil
		}
	case "hours":
		if c.Every >= 2 && c.Every <= 23 {
			return m + " */" + strconv.Itoa(c.Every) + " * * *", nil
		}
	case "minutes":
		if c.Every >= 1 && c.Every <= 59 {
			return "*/" + strconv.Itoa(c.Every) + " * * * *", nil
		}
	case "weekly":
		if c.Weekday >= 0 && c.Weekday <= 6 {
			return m + " " + h + " * * " + strconv.Itoa(c.Weekday), nil
		}
	case "monthly":
		if c.Day >= 1 && c.Day <= 31 {
			return m + " " + h + " " + strconv.Itoa(c.Day) + " * *", nil
		}
	}
	return "", errors.New("Chu kỳ thực hiện không hợp lệ hoặc chưa được hỗ trợ")
}

type SchedulePreview struct {
	ID       string `json:"id"`
	Previous string `json:"previous"`
	Proposed string `json:"proposed"`
	Enabled  bool   `json:"enabled"`
	Changed  bool   `json:"changed"`
}

const pausedSchedulePrefix = "# backup-monitor-paused "

var scheduleClockPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
var scheduleTokens = regexp.MustCompile(`[^ \t]+`)

// Update only a known direct/wrapped task and preserve its arguments, recurrence
// and pause marker. This runs inside the storage transaction, never on its own.
func proposeSchedulePlanned(cron []byte, scriptsDir string, change *ScheduleChange, planned map[string]bool, logsDirs ...string) ([]byte, *SchedulePreview, error) {
	cfg := Settings{ScriptsDir: scriptsDir}
	if len(logsDirs) == 1 {
		cfg.LogsDir = logsDirs[0]
	}
	return proposeScheduleSettings(cron, cfg, change, planned)
}
func proposeScheduleSettings(cron []byte, cfg Settings, change *ScheduleChange, planned map[string]bool) ([]byte, *SchedulePreview, error) {
	if change == nil {
		return cron, nil, nil
	}
	files := map[string]string{
		"backup-site": "backup-site.sh", "backup-database": "backup-database.sh", "backup-panel": "backup-panel.sh",
		"cleanup-panel": "cleanup-panel-backups.sh", "drive-sync": "auto_backup.sh", "integrity-check": "check_integrity.sh",
	}
	file, known := files[change.ID]
	expected := strings.Fields(change.ExpectedSchedule)
	creating := change.Create && change.ExpectedSchedule == "" && change.ExpectedEnabled != nil && !*change.ExpectedEnabled && change.Cycle != nil
	if !known || !scheduleClockPattern.MatchString(change.Clock) || change.ExpectedEnabled == nil ||
		(!creating && (change.Create || len(change.ExpectedSchedule) > 100 || len(expected) != 5 || strings.Join(expected, " ") != change.ExpectedSchedule)) {
		return nil, nil, errors.New("Yêu cầu đổi lịch cron không hợp lệ")
	}
	cycle, err := cycleSchedule(change)
	if err != nil {
		return nil, nil, err
	}
	hour, _ := strconv.Atoi(change.Clock[:2])
	minute, _ := strconv.Atoi(change.Clock[3:])
	script := scriptPath(cfg, file)
	wrapper := scriptPath(cfg, "backup-monitor-run.sh")
	lines := strings.Split(string(cron), "\n")
	matches := 0
	otherSchedules := map[string]bool{}
	var preview *SchedulePreview
	for i, original := range lines {
		paused := strings.HasPrefix(original, pausedSchedulePrefix)
		body := original
		if paused {
			body = strings.TrimPrefix(body, pausedSchedulePrefix)
		}
		fields := strings.Fields(body)
		if len(fields) < 7 || strings.HasPrefix(fields[0], "#") || fields[5] != "/bin/bash" {
			continue
		}
		wrapped := len(fields) >= 8 && fields[6] == wrapper && fields[7] == change.ID
		if fields[6] != script && !wrapped {
			continue
		}
		actual := strings.Join(fields[:5], " ")
		// A task type may have several schedules. Only the selected schedule
		// is editable; other rows and their enabled states stay unchanged.
		if creating || actual != change.ExpectedSchedule {
			otherSchedules[actual] = true
			continue
		}
		matches++
		if !paused != *change.ExpectedEnabled {
			return nil, nil, errors.New("Lịch hoặc trạng thái cron đã thay đổi; hãy tải lại")
		}
		proposed := strconv.Itoa(minute) + " " + strconv.Itoa(hour) + " " + strings.Join(fields[2:5], " ")
		if cycle != "" {
			proposed = cycle
		}
		preview = &SchedulePreview{change.ID, actual, proposed, !paused, proposed != actual}
		if preview.Changed {
			tokens := scheduleTokens.FindAllStringIndex(body, -1)
			updated := body[:tokens[0][0]] + strconv.Itoa(minute) + body[tokens[0][1]:tokens[1][0]] + strconv.Itoa(hour) + body[tokens[1][1]:]
			if cycle != "" {
				updated = body[:tokens[0][0]] + cycle + body[tokens[4][1]:]
			}
			if paused {
				updated = pausedSchedulePrefix + updated
			}
			lines[i] = updated
		}
	}
	if creating {
		if otherSchedules[cycle] {
			return nil, nil, errors.New("Loại tác vụ này đã có đúng lịch đã chọn (kể cả lịch tạm dừng); hãy chọn giờ hoặc chu kỳ khác")
		}
		if cfg.LogsDir == "" || SafeParents(script) != nil {
			return nil, nil, errors.New("Không xác nhận được script hoặc thư mục log")
		}
		info, err := os.Stat(script)
		if !planned[script] && (err != nil || !info.Mode().IsRegular()) {
			return nil, nil, errors.New("Script tác vụ chưa tồn tại")
		}
		logNames := map[string]string{"backup-site": "backup-site.log", "backup-database": "backup-database.log", "backup-panel": "backup-panel.log", "cleanup-panel": "cleanup-panel-backups.log", "drive-sync": "auto_backup.log", "integrity-check": "check_integrity.log"}
		command := "/bin/bash " + script
		// Older wrappers only support the three backup jobs. Preserve them;
		// maintenance jobs stay direct until an explicit tracking upgrade.
		supported := change.ID == "backup-site" || change.ID == "backup-database" || change.ID == "backup-panel" || planned[wrapper]
		if !supported && exists(wrapper) {
			data, readErr := readRegular(wrapper)
			supported = readErr == nil && strings.Contains(string(data), TaskControlMarker+"\n")
		}
		if supported && (exists(wrapper) || planned[wrapper]) && SafeParents(wrapper) == nil {
			command = "/bin/bash " + wrapper + " " + change.ID
		}
		if change.ID == "cleanup-panel" {
			command += " --delete"
		}
		command += " >> " + logPath(cfg, logNames[change.ID]) + " 2>&1"
		return []byte(strings.TrimRight(string(cron), "\n") + "\n" + cycle + " " + command + "\n"), &SchedulePreview{change.ID, "", cycle, true, true}, nil
	}
	if matches == 0 {
		return nil, nil, errors.New("Không tìm thấy lịch cron được quản lý; hãy tải lại")
	}
	if matches != 1 {
		return nil, nil, errors.New("Có nhiều dòng cùng loại và cùng lịch; hãy xử lý dòng trùng trước")
	}
	if otherSchedules[preview.Proposed] {
		return nil, nil, errors.New("Loại tác vụ này đã có đúng lịch mới; hãy chọn giờ hoặc chu kỳ khác")
	}
	return []byte(strings.Join(lines, "\n")), preview, nil
}

type CronFilePreview struct {
	Script     string `json:"script"`
	Runner     string `json:"runner"`
	Log        string `json:"log"`
	Flattening bool   `json:"flattening"`
}

// After a successful commit, remove only verified files belonging to the
// selected old namespace. Never recursively delete a directory or unknown files.
func cleanupFlattenedCron(p *Proposal, change *ScheduleChange) []string {
	if change == nil || change.InstanceKey == "" {
		return nil
	}
	oldLoc, ok := p.Old.CronLocations[change.InstanceKey]
	nextLoc, exists := p.New.CronLocations[change.InstanceKey]
	if !ok || !exists || oldLoc.FileLayout != "" || nextLoc.FileLayout != "files" {
		return nil
	}
	old, err := CronSettings(p.Old, change.ID, change.InstanceKey)
	if err != nil {
		return []string{"Chưa dọn được thư mục cron cũ"}
	}
	warnings := []string{}
	for _, s := range p.Scripts {
		if s.Source == s.Target || !within(s.Source, old.ScriptsDir) {
			continue
		}
		a, ae := Digest(s.Source)
		b, be := Digest(s.Target)
		if ae != nil || be != nil || a != hashBytes(s.Original) || b != hashBytes(s.Updated) || os.Remove(s.Source) != nil {
			warnings = append(warnings, "Đã dùng file mới; giữ script cũ để kiểm tra: "+s.Source)
		}
	}
	for _, l := range p.Logs {
		if !within(l.Source, old.LogsDir) {
			continue
		}
		a, ae := Digest(l.Source)
		b, be := Digest(l.Target)
		if ae != nil || be != nil || a != l.Hash || b != l.Hash || os.Remove(l.Source) != nil {
			warnings = append(warnings, "Đã dùng file mới; giữ log cũ để kiểm tra: "+l.Source)
		}
	}
	seen := map[string]bool{}
	for _, dir := range []string{old.ScriptsDir, old.LogsDir} {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
			warnings = append(warnings, "Giữ thư mục cron cũ vì còn file hoặc chưa thể xóa: "+dir)
		}
	}
	return warnings
}

// Keep the entire stable key: two same-type schedules may share their parents.
// The Windows API joins the returned names with POSIX paths separately.
func CronFileName(cfg Settings, name string) string {
	if cfg.fileKey == "" {
		return name
	}
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + "-" + cfg.fileKey + ext
}

func scriptPath(cfg Settings, name string) string {
	return filepath.Join(cfg.ScriptsDir, CronFileName(cfg, name))
}
func logPath(cfg Settings, name string) string {
	return filepath.Join(cfg.LogsDir, CronFileName(cfg, name))
}
func taskStatePath(cfg Settings, id string) string {
	return logPath(cfg, ".backup-monitor-"+id+".run.json")
}

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

// replaceableCronTarget permits an edit to take over a stale private-cron
// script only. Creation always remains exclusive. The file must be a safe,
// root-owned executable regular file and no current (including paused) cron
// entry may reference it.
func replaceableCronTarget(cron []byte, target string) ([]byte, error) {
	if err := SafeParents(target); err != nil {
		return nil, errors.New("Script đích không an toàn")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !privateRootOwned(info) || info.Mode().Perm()&0100 == 0 || info.Size() > MaxScriptBytes {
		return nil, errors.New("Script đích không an toàn; chỉ ghi đè tệp script riêng do root sở hữu")
	}
	for _, line := range strings.Split(string(cron), "\n") {
		body := strings.TrimSpace(strings.TrimPrefix(line, pausedSchedulePrefix))
		if body == "" || strings.HasPrefix(body, "#") {
			continue
		}
		for _, field := range strings.Fields(body) {
			if field == target {
				return nil, errors.New("Script đích đang được cron khác sử dụng; không thể ghi đè")
			}
		}
	}
	data, err := readRegular(target)
	if err != nil || len(data) > MaxScriptBytes {
		return nil, errors.New("Không đọc được script đích để tạo bản dự phòng")
	}
	return data, nil
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
			p.Scripts = append(p.Scripts, scriptChange{Source: source, Target: scriptPath(next, name), Original: data, Updated: updated})
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
			var targetOriginal []byte
			targetExisted := false
			if err := SafeParents(target); err != nil {
				return nil, errors.New("Script đích không an toàn")
			}
			if source != target && exists(target) {
				// Only an existing private cron may replace an orphaned script
				// while its directories are being moved. New schedules remain
				// create-only, so they cannot overwrite another task's file.
				if change.Create || change.InstanceKey == "" || !moved {
					return nil, errors.New("Script đích đã tồn tại; chỉ cron đang sửa mới được ghi đè script cũ khi đổi thư mục")
				}
				targetOriginal, err = replaceableCronTarget(p.Cron, target)
				if err != nil {
					return nil, err
				}
				targetExisted = true
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
			p.Scripts = append([]scriptChange{{Source: source, Target: target, Original: original, Updated: data, TargetOriginal: targetOriginal, TargetExistedBeforePreview: targetExisted}}, p.Scripts...)
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

// Files are staged without removing the source. Source deletion happens only
// after scripts, crontab and settings commit, so failed copies cannot lose data.
type archiveTransfer struct {
	Source, Target, Hash string
	Size                 int64
}
type ledgerTransfer struct {
	Target            string
	Original, Updated []byte
}

// Older website scripts used a six-character mktemp suffix instead of _site.
// This is migration-only: do not infer cron/manual origin or enable rotation.
var legacyTransferSiteName = regexp.MustCompile(`^web_([A-Za-z0-9_.-]+)_(20[0-9]{6})_([0-9]{6})_[A-Za-z0-9]{6}\.tar\.gz$`)

func legacyTransferVersion(category, root string, archive Archive) (ArchiveVersion, bool) {
	v := ArchiveVersion{Category: category}
	if category != "site" {
		return v, false
	}
	m := legacyTransferSiteName.FindStringSubmatch(filepath.Base(archive.Path))
	if m == nil || filepath.Base(filepath.Dir(archive.Path)) != m[1] {
		return v, false
	}
	stamp, err := time.Parse("20060102 150405", m[2]+" "+m[3])
	if err != nil {
		return v, false
	}
	v.Entity, v.Day, v.Stamp = m[1], stamp.Format("2006-01-02"), stamp.Format("2006-01-02 150405")
	if v.Day != archive.Day || (!within(archive.Path, filepath.Join(root, v.Day, category)) && !within(archive.Path, filepath.Join(root, category))) {
		return v, false
	}
	return v, true
}

func migrationLayout(c *Controller, global Settings, root string) *Layout {
	metadata := c.VersionMetadataDir
	if metadata != "" && root != global.BackupRoot {
		metadata = filepath.Join(metadata, "roots", hashBytes([]byte(root))[:32])
	}
	return &Layout{Root: root, MetadataDir: metadata}
}

// cronUsesTaskLocation reports whether a private cron location is still
// scheduled. CronLocations deliberately retains deleted rows so their files
// can be recovered or inspected later; those stale rows must not prevent a
// remaining schedule from moving its own archive root.
func cronUsesTaskLocation(cron []byte, cfg Settings, id string) bool {
	name, ok := TaskScripts[id]
	if !ok {
		return false
	}
	script := scriptPath(cfg, name)
	runner := scriptPath(cfg, "backup-monitor-run.sh")
	for _, line := range strings.Split(string(cron), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, pausedSchedulePrefix) {
			line = strings.TrimPrefix(line, pausedSchedulePrefix)
		} else if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 || fields[5] != "/bin/bash" {
			continue
		}
		if fields[6] == script || (len(fields) >= 8 && fields[6] == runner && fields[7] == id) {
			return true
		}
	}
	return false
}

func (c *Controller) planCronBackupTransfer(p *Proposal, change ScheduleChange, oldLocal, nextLocal Settings) error {
	category := map[string]string{"backup-site": "site", "backup-database": "database", "backup-panel": "panel"}[change.ID]
	if category == "" {
		return errors.New("Chỉ cron sao lưu website, database và aaPanel được chuyển backup cũ")
	}
	if within(oldLocal.BackupRoot, nextLocal.BackupRoot) || within(nextLocal.BackupRoot, oldLocal.BackupRoot) {
		return errors.New("Nơi lưu mới không được nằm trong hoặc chứa nơi lưu cũ")
	}
	// Old archive names record source/category, not the schedule instance. Never
	// guess ownership if another same-category cron uses this root (paused too).
	for key, loc := range p.Old.CronLocations {
		if key == change.InstanceKey || loc.ID != change.ID {
			continue
		}
		other, err := CronSettings(p.Old, loc.ID, key)
		if err != nil {
			return err
		}
		if !cronUsesTaskLocation(p.Cron, other, loc.ID) {
			continue
		}
		if other.BackupRoot == oldLocal.BackupRoot {
			return errors.New("Nhiều cron cùng loại dùng chung kho; chưa xác định được backup riêng để chuyển an toàn")
		}
	}
	selectedSkipped := false
	if p.Old.BackupRoot == oldLocal.BackupRoot {
		for _, line := range strings.Split(string(p.Cron), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, pausedSchedulePrefix) {
				line = strings.TrimPrefix(line, pausedSchedulePrefix)
			} else if strings.HasPrefix(line, "#") {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 7 || f[5] != "/bin/bash" {
				continue
			}
			direct := f[6] == filepath.Join(p.Old.ScriptsDir, TaskScripts[change.ID])
			wrapped := len(f) >= 8 && f[6] == filepath.Join(p.Old.ScriptsDir, "backup-monitor-run.sh") && f[7] == change.ID
			if !direct && !wrapped {
				continue
			}
			if change.InstanceKey == "" && !selectedSkipped && strings.Join(f[:5], " ") == change.ExpectedSchedule {
				selectedSkipped = true
				continue
			}
			return errors.New("Kho còn được cron cùng loại khác sử dụng; không chuyển backup dùng chung")
		}
	}
	p.Summary.BackupMigration = true
	if _, err := os.Stat(oldLocal.BackupRoot); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	oldLayout := migrationLayout(c, p.Old, oldLocal.BackupRoot)
	newLayout := migrationLayout(c, p.Old, nextLocal.BackupRoot)
	archives, err := oldLayout.Archives(category, false)
	if err != nil {
		return err
	}
	days := map[string]bool{}
	targets := map[string]bool{}
	ledgers := map[string]VersionLedger{}
	source := effectiveSource(oldLocal, change.ID)
	for _, archive := range archives {
		v, ok := ParseVersion(category, filepath.Base(archive.Path))
		if !ok {
			v, ok = cleanVersion(category, filepath.Base(archive.Path))
		}
		if !ok {
			v, ok = legacyTransferVersion(category, oldLocal.BackupRoot, archive)
		}
		if category == "panel" && !ok {
			v = ArchiveVersion{Category: category, Entity: "aaPanel", Day: archive.Day}
			ok = true
		}
		if !ok {
			return fmt.Errorf("Backup chưa xác định được nguồn hoặc định dạng: %s; chưa chuyển dữ liệu", filepath.Base(archive.Path))
		}
		if category != "panel" && source.Scope == "folder" && v.Entity != filepath.Base(source.Path) {
			continue
		}
		target := filepath.Join(nextLocal.BackupRoot, archive.Day, category, archive.Relative)
		if !within(target, nextLocal.BackupRoot) || SafeParents(target) != nil || exists(target) || targets[target] {
			return errors.New("File backup đích đã tồn tại hoặc đường dẫn không an toàn; không ghi đè")
		}
		targets[target] = true
		info, err := oldLayout.safeArchive(archive.Path)
		if err != nil {
			return err
		}
		sum, err := Digest(archive.Path)
		if err != nil {
			return err
		}
		p.Archives = append(p.Archives, archiveTransfer{archive.Path, target, sum, info.Size()})
		p.Summary.FilesToMove++
		p.Summary.BytesToMove += info.Size()
		days[archive.Day] = true
		if oldLayout.MetadataDir == "" {
			continue
		}
		ledger, err := oldLayout.readVersionLedger(archive.Day)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Join(oldLocal.BackupRoot, archive.Day), archive.Path)
		if err != nil {
			return err
		}
		origin := ledger.Origins[filepath.ToSlash(rel)]
		if origin == "" {
			continue
		} // Untagged legacy versions remain untagged.
		merged, loaded := ledgers[archive.Day]
		if !loaded {
			merged, err = newLayout.readVersionLedger(archive.Day)
			if err != nil {
				return err
			}
		}
		rel, err = filepath.Rel(filepath.Join(nextLocal.BackupRoot, archive.Day), target)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if previous := merged.Origins[rel]; previous != "" && previous != origin {
			return errors.New("Metadata backup đích bị xung đột")
		}
		merged.Origins[rel] = origin
		ledgers[archive.Day] = merged
	}
	p.Summary.DaysToMove = len(days)
	// Stable order is required for preview-token revalidation.
	for _, archive := range archives {
		ledger, ok := ledgers[archive.Day]
		if !ok {
			continue
		}
		delete(ledgers, archive.Day)
		path, err := newLayout.versionLedgerPath(archive.Day)
		if err != nil {
			return err
		}
		var previous []byte
		if exists(path) {
			previous, err = readRegular(path)
			if err != nil {
				return err
			}
		}
		raw, err := json.MarshalIndent(ledger, "", "  ")
		if err != nil {
			return err
		}
		p.Ledgers = append(p.Ledgers, ledgerTransfer{path, previous, append(raw, '\n')})
	}
	return nil
}

func verifyTransferFile(path string, move archiveTransfer) error {
	if err := SafeParents(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != move.Size {
		return errors.New("Backup đã thay đổi trong lúc chuyển")
	}
	sum, err := Digest(path)
	if err != nil {
		return err
	}
	if sum != move.Hash {
		return errors.New("Checksum backup không khớp; giữ dữ liệu gốc")
	}
	return nil
}

func stageBackupTransfer(move archiveTransfer) (resultErr error) {
	verifiedDestination := false
	if err := verifyTransferFile(move.Source, move); err != nil {
		return err
	}
	if err := mkdirPrivate(filepath.Dir(move.Target)); err != nil {
		return err
	}
	// Hard-link on one filesystem; streamed, exclusive copy across disks.
	linked := os.Link(move.Source, move.Target) == nil
	if !linked {
		source, err := openRegular(move.Source)
		if err != nil {
			return err
		}
		defer source.Close()
		target, err := os.OpenFile(move.Target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer func() {
			if resultErr != nil && !verifiedDestination {
				_ = os.Remove(move.Target)
			}
		}()
		_, err = io.Copy(target, source)
		if err == nil {
			err = target.Sync()
		}
		closeErr := target.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		info, err := source.Stat()
		if err != nil {
			return err
		}
		if err = os.Chtimes(move.Target, info.ModTime(), info.ModTime()); err != nil {
			return err
		}
	} else {
		defer func() {
			if resultErr != nil && !verifiedDestination {
				_ = os.Remove(move.Target)
			}
		}()
	}
	if err := verifyTransferFile(move.Target, move); err != nil {
		return err
	}
	verifiedDestination = true
	if err := verifyTransferFile(move.Source, move); err != nil {
		return errors.New("Nguồn backup thay đổi; giữ bản đã kiểm tra để khôi phục: " + move.Target)
	}
	return nil
}

type CronRepairFile struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Source string `json:"source,omitempty"`
}
type CronRepairPreview struct {
	Token       string           `json:"token"`
	Files       []CronRepairFile `json:"filesToCreate"`
	Applied     bool             `json:"applied"`
	RecoveryDir string           `json:"recoveryDir,omitempty"`
}
type cronRepairPlan struct {
	Preview  CronRepairPreview
	Scripts  []scriptChange
	Cron     []byte
	LogsDirs []string
}

// Repair only missing files in registered private namespaces that still have
// a managed crontab row. Preserve existing files and the shared source scripts.
func (c *Controller) cronRepairPlan() (cronRepairPlan, error) {
	p := cronRepairPlan{Preview: CronRepairPreview{Files: []CronRepairFile{}}}
	if c.CronSelector != "" {
		return p, errors.New("Cron đang chạy không được khôi phục script")
	}
	cfg, version, err := c.Active()
	if err != nil {
		return p, err
	}
	p.Cron, err = c.Run([]string{"crontab", "-l"}, nil)
	if err != nil {
		return p, err
	}
	keys := []string{}
	for key := range cfg.CronLocations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fingerprints := []interface{}{Version, version, hashBytes(p.Cron)}
	for _, key := range keys {
		loc := cfg.CronLocations[key]
		local, err := CronSettings(cfg, loc.ID, key)
		if err != nil {
			return p, err
		}
		matched := false
		for _, line := range strings.Split(string(p.Cron), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, pausedSchedulePrefix) {
				line = strings.TrimPrefix(line, pausedSchedulePrefix)
			} else if strings.HasPrefix(line, "#") {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 7 || f[5] != "/bin/bash" {
				continue
			}
			direct := f[6] == scriptPath(local, TaskScripts[loc.ID])
			wrapped := len(f) >= 8 && f[6] == scriptPath(local, "backup-monitor-run.sh") && f[7] == loc.ID
			if direct || wrapped {
				matched = true
			}
		}
		if !matched {
			continue
		} // Deleted schedules are not resurrected.
		for _, name := range []string{TaskScripts[loc.ID], "backup-monitor-run.sh"} {
			target := scriptPath(local, name)
			if err := SafeParents(target); err != nil {
				return p, err
			}
			info, err := os.Lstat(target)
			if err == nil {
				if !info.Mode().IsRegular() || !privateRootOwned(info) {
					return p, errors.New("Script hiện có không an toàn; không ghi đè: " + target)
				}
				sum, err := Digest(target)
				if err != nil {
					return p, err
				}
				fingerprints = append(fingerprints, []string{target, sum})
				continue
			}
			if !os.IsNotExist(err) {
				return p, err
			}
			var data []byte
			source := ""
			if name == "backup-monitor-run.sh" {
				data = privateRunner(cfg, local)
			} else {
				source = filepath.Join(cfg.ScriptsDir, name)
				original, err := readRegular(source)
				if err != nil {
					return p, errors.New("Thiếu cả script riêng và bản chung; cần khôi phục bản dự phòng: " + name)
				}
				info, err := os.Stat(source)
				if err != nil || !privateRootOwned(info) {
					return p, errors.New("Script nguồn không an toàn")
				}
				data = []byte(Rewrite(string(original), settingsPairs(cfg, local)))
				if effectiveSource(cfg, loc.ID) != effectiveSource(local, loc.ID) {
					data, err = patchRepairSource(data, loc.ID, effectiveSource(local, loc.ID))
					if err != nil {
						return p, err
					}
				}
				fingerprints = append(fingerprints, []string{source, hashBytes(original)})
			}
			data = bindCronScript(data, loc.ID, key, local)
			p.Scripts = append(p.Scripts, scriptChange{Source: source, Target: target, Updated: data})
			p.Preview.Files = append(p.Preview.Files, CronRepairFile{ID: loc.ID, Path: target, Source: source})
			p.LogsDirs = append(p.LogsDirs, local.LogsDir)
			fingerprints = append(fingerprints, []string{target, hashBytes(data)})
		}
	}
	raw, err := json.Marshal(fingerprints)
	if err != nil {
		return p, err
	}
	p.Preview.Token = hashBytes(raw)
	return p, nil
}

func patchRepairSource(data []byte, id string, source SourceSpec) ([]byte, error) {
	text, err := PatchSource(string(data), id, source)
	return []byte(text), err
}

func (c *Controller) PreviewCronRepair() (CronRepairPreview, error) {
	p, err := c.cronRepairPlan()
	return p.Preview, err
}

func (c *Controller) ApplyCronRepair(token string) (result CronRepairPreview, resultErr error) {
	locks, err := acquireAll(c.Locks)
	if err != nil {
		return result, err
	}
	defer releaseAll(locks)
	p, err := c.cronRepairPlan()
	if err != nil {
		return result, err
	}
	if token == "" || token != p.Preview.Token {
		return result, errors.New("Dữ liệu đã thay đổi; xem trước khôi phục lại")
	}
	result = p.Preview
	if len(p.Scripts) == 0 {
		result.Applied = true
		return result, nil
	}
	temporary, err := os.MkdirTemp("", "cron-repair-validate-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(temporary)
	for i, script := range p.Scripts {
		file := filepath.Join(temporary, hashBytes([]byte(script.Target))[:16]+".sh")
		if err = AtomicWrite(file, script.Updated, 0600); err != nil {
			return result, err
		}
		if _, err = c.Run([]string{"/bin/bash", "-n", file}, nil); err != nil {
			return result, errors.New("Script khôi phục không hợp lệ")
		}
		if err = mkdirPrivate(filepath.Dir(script.Target)); err != nil {
			return result, err
		}
		if err = mkdirPrivate(p.LogsDirs[i]); err != nil {
			return result, err
		}
	}
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return result, err
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "repair-cron-files-*")
	if err != nil {
		return result, err
	}
	raw, _ := json.MarshalIndent(p.Preview, "", "  ")
	if err = c.Write(filepath.Join(recovery, "plan.json"), raw, 0600); err != nil {
		return result, err
	}
	if err = c.Write(filepath.Join(recovery, "crontab-before.txt"), p.Cron, 0600); err != nil {
		return result, err
	}
	created := []scriptChange{}
	defer func() {
		if resultErr == nil {
			return
		}
		for i := len(created) - 1; i >= 0; i-- {
			script := created[i]
			sum, err := Digest(script.Target)
			if err != nil || sum != hashBytes(script.Updated) || os.Remove(script.Target) != nil {
				resultErr = errors.New("Chưa hoàn tất khôi phục; giữ file đã thay đổi để kiểm tra. Nhật ký: " + recovery)
			}
		}
	}()
	for _, script := range p.Scripts {
		if err = writeScriptExclusive(script.Target, script.Updated); err != nil {
			return result, err
		}
		created = append(created, script)
	}
	for _, script := range created {
		if sum, err := Digest(script.Target); err != nil || sum != hashBytes(script.Updated) {
			return result, errors.New("Kiểm tra script sau khôi phục thất bại")
		}
	}
	result.Applied = true
	result.RecoveryDir = recovery
	return result, nil
}
