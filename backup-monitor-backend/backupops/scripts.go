package backupops

import (
	"backup-monitor-backend/ops"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const MaxScriptBytes = 128 * 1024
const GeneratedTaskMarker = "# backup-monitor generated task v1"
const TaskControlMarker = "# backup-monitor task control v1"

// Publish a fully written file using an exclusive hard link. A pre-existing
// target, including a symlink, is never replaced.
func writeScriptExclusive(path string, data []byte) error {
	if err := SafeParents(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".new-task-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0700); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = SafeParents(path); err != nil {
		return err
	}
	return os.Link(f.Name(), path)
}

var TaskScripts = map[string]string{
	"backup-site": "backup-site.sh", "backup-database": "backup-database.sh", "backup-panel": "backup-panel.sh",
	"cleanup-panel": "cleanup-panel-backups.sh", "drive-sync": "auto_backup.sh", "integrity-check": "check_integrity.sh",
}

type ScriptRequest struct {
	ID           string `json:"id"`
	Content      string `json:"content,omitempty"`
	ExpectedHash string `json:"expectedHash,omitempty"`
}
type ScriptDocument struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Content     string `json:"content"`
	Hash        string `json:"hash"`
	Version     string `json:"version"`
	RecoveryDir string `json:"recoveryDir,omitempty"`
}

func (c *Controller) scriptDocument(id string) (ScriptDocument, error) {
	selector := id
	id, _, _ = strings.Cut(id, "~")
	name, ok := TaskScripts[id]
	if !ok {
		return ScriptDocument{}, errors.New("Chỉ sửa script của 6 loại tác vụ được quản lý")
	}
	cfg, version, err := c.Active()
	if err != nil {
		return ScriptDocument{}, err
	}
	cfg, id, err = taskSettings(cfg, selector)
	if err != nil {
		return ScriptDocument{}, err
	}
	if err = c.LocalPath(cfg.ScriptsDir, "scripts"); err != nil {
		return ScriptDocument{}, err
	}
	path := scriptPath(cfg, name)
	f, err := openRegular(path)
	if err != nil {
		return ScriptDocument{}, errors.New("Script chưa tồn tại hoặc không phải tệp thường")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > MaxScriptBytes || !privateRootOwned(info) || SafeParents(path) != nil {
		return ScriptDocument{}, errors.New("Script phải do root sở hữu, không cho người khác ghi và tối đa 128 KB")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxScriptBytes+1))
	if err != nil || len(data) > MaxScriptBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return ScriptDocument{}, errors.New("Không đọc được script UTF-8 hợp lệ")
	}
	return ScriptDocument{ID: selector, Path: path, Content: string(data), Hash: hashBytes(data), Version: version}, nil
}
func (c *Controller) ReadScript(r Request) (ScriptDocument, error) {
	if r.Script == nil {
		return ScriptDocument{}, errors.New("Thiếu tác vụ")
	}
	return c.scriptDocument(r.Script.ID)
}
func (c *Controller) SaveScript(r Request) (ScriptDocument, error) {
	if r.Script == nil {
		return ScriptDocument{}, errors.New("Thiếu tác vụ")
	}
	data := []byte(strings.ReplaceAll(r.Script.Content, "\r\n", "\n"))
	if len(data) == 0 || len(data) > MaxScriptBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) || !strings.HasPrefix(string(data), "#!") {
		return ScriptDocument{}, errors.New("Script phải có dòng #!, nội dung UTF-8 và tối đa 128 KB")
	}
	locks, err := acquireAll(c.Locks)
	if err != nil {
		return ScriptDocument{}, err
	}
	defer releaseAll(locks)
	original, err := c.scriptDocument(r.Script.ID)
	if err != nil {
		return ScriptDocument{}, err
	}
	if running, err := scriptProcessRunning(original.Path); err != nil || running {
		return ScriptDocument{}, errors.New("Không xác nhận được tác vụ đã dừng; chưa sửa script")
	}
	if r.ExpectedVersion != original.Version || r.Script.ExpectedHash != original.Hash {
		return ScriptDocument{}, errors.New("Script hoặc cấu hình đã thay đổi; tải lại trước khi lưu")
	}
	if _, err = c.Run([]string{"/bin/bash", "-n"}, data); err != nil {
		return ScriptDocument{}, errors.New("Cú pháp Bash không hợp lệ; chưa lưu script")
	}
	if string(data) == original.Content {
		return original, nil
	}
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return ScriptDocument{}, err
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "before-script-edit-*")
	if err != nil {
		return ScriptDocument{}, err
	}
	if err = c.Write(filepath.Join(recovery, filepath.Base(original.Path)), []byte(original.Content), 0600); err != nil {
		return ScriptDocument{}, err
	}
	// Re-read after validation and backup; do not overwrite another editor's save.
	current, err := c.scriptDocument(r.Script.ID)
	if err != nil || current.Hash != original.Hash || current.Version != original.Version {
		return ScriptDocument{}, errors.New("Script thay đổi trong lúc lưu; chưa ghi đè")
	}
	if running, err := scriptProcessRunning(original.Path); err != nil || running {
		return ScriptDocument{}, errors.New("Tác vụ vừa bắt đầu chạy; chưa sửa script")
	}
	if err = c.Write(original.Path, data, 0700); err != nil {
		return ScriptDocument{}, fmt.Errorf("Không lưu được script; bản dự phòng: %s", recovery)
	}
	saved, err := c.scriptDocument(r.Script.ID)
	if err != nil || saved.Hash != hashBytes(data) {
		return ScriptDocument{}, fmt.Errorf("Chưa xác nhận nội dung đã lưu; kiểm tra bản dự phòng: %s", recovery)
	}
	saved.RecoveryDir = recovery
	return saved, nil
}

func GenerateTaskScript(id string, cfg Settings) ([]byte, error) {
	name, ok := TaskScripts[id]
	if !ok {
		return nil, errors.New("Loại tác vụ không hỗ trợ tạo script")
	}
	var data []byte
	var err error
	if id == "backup-panel" {
		data, err = ops.Templates.ReadFile("backup-panel.sh")
	} else if id == "cleanup-panel" {
		data = []byte(CleanupScript())
	} else {
		data, err = ops.Templates.ReadFile("templates/" + name)
	}
	if err != nil {
		return nil, errors.New("Không đọc được mẫu script")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	firstLine := strings.IndexByte(text, '\n')
	if firstLine < 0 {
		return nil, errors.New("Mẫu script thiếu header")
	}
	text = text[:firstLine+1] + GeneratedTaskMarker + "\n" + text[firstLine+1:]
	if strings.Contains(text, "# COMMON") {
		common, err := ops.Templates.ReadFile("templates/common.sh")
		if err != nil {
			return nil, err
		}
		text = strings.Replace(text, "# COMMON", strings.ReplaceAll(string(common), "\r\n", "\n"), 1)
	}
	defaults := DefaultSettings()
	// Add the default log paths used by templates, which predate configurable logsDir.
	pairs := settingsPairs(defaults, cfg)
	for _, name := range LogNames {
		pairs = append(pairs, [2]string{filepath.Join("/root/logs", name), logPath(cfg, name)})
	}
	text = Rewrite(text, pairs)
	if id == "backup-site" || id == "backup-database" || id == "backup-panel" {
		text, err = PatchSource(text, id, effectiveSource(cfg, id))
		if err != nil {
			return nil, err
		}
	}
	return []byte(runtimeTemplate(text)), nil
}

// Scheduled backup scripts use the same lifecycle markers as manual runs.
func ScheduledRunner(cfg Settings) string {
	var b strings.Builder
	b.WriteString("#!/bin/bash\n# backup-monitor scheduled tracking v1\n" + TaskControlMarker + "\nset -u\numask 077\nif [ \"$(ps -o pgid= -p $$ | tr -d ' ')\" != \"$$\" ]; then exec setsid /bin/bash \"$0\" \"$@\"; fi\nTASK_ID=${1:-}\ncase \"$TASK_ID\" in\n")
	for _, id := range []string{"backup-site", "backup-database", "backup-panel", "drive-sync", "cleanup-panel", "integrity-check"} {
		name, _ := taskLogName(id)
		fmt.Fprintf(&b, "  %s) SCRIPT=%q; LOG=%q ;;\n", id, filepath.ToSlash(scriptPath(cfg, TaskScripts[id])), filepath.ToSlash(logPath(cfg, name)))
	}
	b.WriteString("  *) exit 2 ;;\nesac\nshift\nexport BACKUP_MONITOR_ORIGIN=cron\nexport BACKUP_PANEL_CAPTURED_LOG=1\n")
	fmt.Fprintf(&b, "RUN_HISTORY=%q\n", filepath.ToSlash(logPath(cfg, "backup-monitor-runs.log")))
	stateSuffix := ""
	if cfg.fileKey != "" {
		stateSuffix = "-" + cfg.fileKey
	}
	fmt.Fprintf(&b, "RUN_STATE=%q/.backup-monitor-$TASK_ID.run%s.json\n", filepath.ToSlash(cfg.LogsDir), stateSuffix)
	b.WriteString(`[[ -f "$SCRIPT" && ! -L "$SCRIPT" ]] || exit 1
[[ ! -L "$LOG" && ( ! -e "$LOG" || -f "$LOG" ) ]] || exit 1
[[ ! -L "$RUN_HISTORY" && ( ! -e "$RUN_HISTORY" || -f "$RUN_HISTORY" ) ]] || exit 1
exec >> "$LOG" 2>&1
exec 8>"/root/.backup-monitor-job-$TASK_ID.lock"
flock -n 8 || exit 75
START_SECONDS=$(date +%s)
START_DATE=$(date +%F)
START_TIME=$(date +%T)
RUN_ID="$(date +%s%N)-$$"
export RCLONE_USE_JSON_LOG=true RCLONE_STATS=5s RCLONE_STATS_LOG_LEVEL=NOTICE
CANCELLED=0
[[ ! -L "$RUN_STATE" && ( ! -e "$RUN_STATE" || -f "$RUN_STATE" ) ]] || exit 1
START_TICKS=$(awk '{print $22}' "/proc/$$/stat")
STATE_TMP=$(mktemp "$RUN_STATE.tmp.XXXXXXXX") || exit 1
printf '{"jobId":"%s","runId":"%s","pid":%s,"startTicks":"%s","script":"%s"}\n' "$TASK_ID" "$RUN_ID" "$$" "$START_TICKS" "$SCRIPT" > "$STATE_TMP" || exit 1
mv -f -- "$STATE_TMP" "$RUN_STATE" || exit 1
finish_run() {
    code=$?
    trap - EXIT
    status=success
    [[ "$code" == 0 ]] || status=failed
    marker="MONITOR_RUN|$TASK_ID|$START_DATE|$START_TIME|$status|$(( $(date +%s) - START_SECONDS ))|$RUN_ID"
    printf '%s\n' "$marker"
    [[ "$CANCELLED" == 0 ]] || printf 'MONITOR_CANCEL|%s|%s\n' "$TASK_ID" "$RUN_ID"
    [[ "$TASK_ID" == backup-panel ]] || printf '%s\n' "$marker" >> "$RUN_HISTORY" || printf 'CRON_HISTORY_WRITE_FAILED\n' >&2
    exit "$code"
}
trap finish_run EXIT
printf 'MONITOR_RUN|%s|%s|%s|running|0|%s\n' "$TASK_ID" "$START_DATE" "$START_TIME" "$RUN_ID"
/bin/bash "$SCRIPT" "$@" &
child=$!
trap 'CANCELLED=1; kill -TERM "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true; exit 143' TERM HUP
trap 'CANCELLED=1; kill -INT "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true; exit 130' INT
wait "$child"
result=$?
trap - TERM HUP INT
exit "$result"
`)
	return runtimeTemplate(b.String())
}

// The scope is explicit: folder archives exactly the chosen directory; children
// produces a separate archive per first-level child (or a logical MySQL dump).
type SourceSpec struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
}
type SourcePreview struct {
	ID       string     `json:"id"`
	Previous SourceSpec `json:"previous"`
	Proposed SourceSpec `json:"proposed"`
}

func configuredSource(s Settings, id string) SourceSpec {
	switch id {
	case "backup-site":
		return s.SiteSource
	case "backup-database":
		return s.DatabaseSource
	case "backup-panel":
		return s.PanelSource
	}
	return SourceSpec{}
}
func effectiveSource(s Settings, id string) SourceSpec {
	spec := configuredSource(s, id)
	if spec != (SourceSpec{}) {
		return spec
	}
	switch id {
	case "backup-site":
		return SourceSpec{"/www/wwwroot", "children"}
	case "backup-database":
		return SourceSpec{"/www/server/data", "children"}
	case "backup-panel":
		return SourceSpec{"/www/server/panel", "folder"}
	}
	return spec
}
func sourceSelections(s Settings) map[string]SourceSpec {
	result := map[string]SourceSpec{}
	for _, id := range []string{"backup-site", "backup-database", "backup-panel"} {
		result[id] = effectiveSource(s, id)
	}
	return result
}
func (c *Controller) ValidateSource(s Settings, id string, spec SourceSpec) error {
	purpose := map[string]string{"backup-site": "source-site", "backup-database": "source-database", "backup-panel": "source-panel"}[id]
	if purpose == "" || (spec.Scope != "folder" && spec.Scope != "children") || (id == "backup-panel" && spec.Scope != "folder") {
		return errors.New("Loại hoặc phạm vi nguồn sao lưu không hợp lệ")
	}
	if err := c.LocalPath(spec.Path, purpose); err != nil {
		return err
	}
	info, err := os.Stat(spec.Path)
	if err != nil || !info.IsDir() {
		return errors.New("Thư mục dữ liệu nguồn phải tồn tại trên máy chủ")
	}
	for _, other := range []string{s.BackupRoot, s.ScriptsDir, s.LogsDir} {
		if within(spec.Path, other) || within(other, spec.Path) {
			return errors.New("Nguồn dữ liệu phải tách khỏi nơi lưu backup, script và log")
		}
	}
	if spec.Scope == "folder" && !regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`).MatchString(filepath.Base(spec.Path)) {
		return errors.New("Tên thư mục nguồn không hợp lệ")
	}
	if id == "backup-database" && spec.Scope == "folder" {
		if systemDatabase(filepath.Base(spec.Path)) || exists(filepath.Join(spec.Path, "mysql")) {
			return errors.New("Chọn thư mục con của data tương ứng database người dùng; không nén thư mục MySQL đang chạy")
		}
	}
	if id == "backup-panel" {
		for _, name := range []string{"data", "config", "vhost"} {
			component := filepath.Join(spec.Path, name)
			if err := SafeParents(component); err != nil {
				return err
			}
			info, err := os.Stat(component)
			if err != nil || !info.IsDir() {
				return errors.New("Nguồn aaPanel phải chứa data, config và vhost")
			}
		}
	}
	return nil
}
func systemDatabase(name string) bool {
	return name == "mysql" || name == "performance_schema" || name == "sys" || name == "phpmyadmin"
}

func sourceReplace(text, pattern, replacement string) (string, error) {
	re := regexp.MustCompile(pattern)
	matches := re.FindAllStringIndex(text, -1)
	if len(matches) != 1 {
		return "", errors.New("Script nguồn không đúng cấu trúc được hỗ trợ; chưa cập nhật")
	}
	p := matches[0]
	return text[:p[0]] + replacement + text[p[1]:], nil
}

// Patch only known source declarations/selection, never commands/credentials or
// archive publication/retention. Original scripts participate in Apply rollback.
func PatchSource(text, id string, spec SourceSpec) (string, error) {
	if !localChars.MatchString(spec.Path) || spec.Path == "" || (spec.Scope != "folder" && spec.Scope != "children") {
		return "", errors.New("Nguồn sao lưu không hợp lệ")
	}
	if id == "backup-panel" {
		return sourceReplace(text, `(?m)^PANEL_ROOT=[^\r\n]+`, "PANEL_ROOT='"+spec.Path+"'")
	}
	array := "SITES"
	if id == "backup-database" {
		array = "DATABASES"
	} else if id != "backup-site" {
		return "", errors.New("Tác vụ không hỗ trợ đổi nguồn")
	}
	var err error
	text, err = sourceReplace(text, `(?m)^SOURCE_ROOT=[^\r\n]+`, "SOURCE_ROOT='"+spec.Path+"'")
	if err != nil {
		return "", err
	}
	selection := array + "=()\n# backup-monitor source selection v1\n"
	if spec.Scope == "folder" {
		selection += array + "=('" + filepath.Base(spec.Path) + "')"
	} else {
		selection += "[[ -d \"$SOURCE_ROOT\" && ! -L \"$SOURCE_ROOT\" ]] || { printf 'Source directory unavailable\\n' >&2; exit 1; }\n"
		selection += "while IFS= read -r -d '' source_path; do\n    source_name=${source_path##*/}\n    [[ \"$source_name\" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]*$ ]] || continue\n"
		if id == "backup-database" {
			selection += "    case \"$source_name\" in mysql|performance_schema|sys|phpmyadmin) continue ;; esac\n"
		} else {
			selection += "    [[ \"$source_name\" != default ]] || continue\n"
		}
		selection += "    " + array + "+=(\"$source_name\")\ndone < <(find \"$SOURCE_ROOT\" -mindepth 1 -maxdepth 1 -type d -print0 | sort -z)\n(( ${#" + array + "[@]} > 0 )) || { printf 'No backup targets found\\n' >&2; exit 1; }"
	}
	// Replace our complete previous block, or a single original array assignment.
	block := `(?ms)^` + array + `=\(\)\n# backup-monitor source selection v1\n.*?\n# backup-monitor source selection end`
	if strings.Contains(text, "# backup-monitor source selection v1") {
		text, err = sourceReplace(text, block, selection+"\n# backup-monitor source selection end")
	} else {
		text, err = sourceReplace(text, `(?m)^`+array+`=\([^\r\n]*\)`, selection+"\n# backup-monitor source selection end")
	}
	if err != nil {
		return "", err
	}
	if id == "backup-site" {
		value := `    source_dir="$SOURCE_ROOT/$site"`
		if spec.Scope == "folder" {
			value = `    source_dir="$SOURCE_ROOT"`
		}
		text, err = sourceReplace(text, `(?m)^    source_dir="\$SOURCE_ROOT(?:/\$site)?"`, value)
	} else {
		value := `    [[ -d "$SOURCE_ROOT/$database" && ! -L "$SOURCE_ROOT/$database" ]] || { printf 'Database source missing: %s\n' "$database" >&2; return 1; }`
		if spec.Scope == "folder" {
			value = `    [[ -d "$SOURCE_ROOT" && ! -L "$SOURCE_ROOT" ]] || { printf 'Database source missing: %s\n' "$database" >&2; return 1; }`
		}
		text, err = sourceReplace(text, `(?m)^    \[\[ -d "\$SOURCE_ROOT(?:/\$database)?"[^\r\n]+`, value)
	}
	return text, err
}

// Only selectors registered in configuration are accepted, never a user path.
type ManagedScriptSelection struct {
	Selector string `json:"selector"`
	Hash     string `json:"hash"`
}
type ManagedScriptOption struct {
	Selector string `json:"selector"`
	Path     string `json:"path"`
	Hash     string `json:"hash"`
}
type ManagedScriptOptions struct {
	Version string                `json:"version"`
	Scripts []ManagedScriptOption `json:"scripts"`
}

func (c *Controller) managedScript(cfg Settings, taskID, selector string) (ManagedScriptOption, Settings, []byte, error) {
	var option ManagedScriptOption
	id, key, hasKey := strings.Cut(selector, "~")
	if id != taskID || TaskScripts[id] == "" || hasKey && (key == "" || strings.Contains(key, "~")) {
		return option, cfg, nil, errors.New("Chỉ chọn script được quản lý cùng loại tác vụ")
	}
	local, err := CronSettings(cfg, id, key)
	if err != nil {
		return option, cfg, nil, err
	}
	if err = c.LocalPath(local.ScriptsDir, "scripts"); err != nil {
		return option, cfg, nil, err
	}
	file := scriptPath(local, TaskScripts[id])
	if err = SafeParents(file); err != nil {
		return option, cfg, nil, err
	}
	f, err := openRegular(file)
	if err != nil {
		return option, cfg, nil, errors.New("Script đã chọn không tồn tại hoặc không phải tệp thường")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > MaxScriptBytes || !privateRootOwned(info) || info.Mode().Perm()&0100 == 0 {
		return option, cfg, nil, errors.New("Script phải do root sở hữu, root có quyền thực thi, không cho người khác ghi và tối đa 128 KB")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxScriptBytes+1))
	if err != nil || len(data) > MaxScriptBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) || !strings.HasPrefix(string(data), "#!") {
		return option, cfg, nil, errors.New("Script đã chọn không có nội dung shell UTF-8 hợp lệ")
	}
	option = ManagedScriptOption{Selector: selector, Path: file, Hash: hashBytes(data)}
	return option, local, data, nil
}

func (c *Controller) ManagedScriptOptions(r Request) (ManagedScriptOptions, error) {
	result := ManagedScriptOptions{Scripts: []ManagedScriptOption{}}
	if r.Script == nil || TaskScripts[r.Script.ID] == "" {
		return result, errors.New("Loại tác vụ không hợp lệ")
	}
	cfg, version, err := c.Active()
	if err != nil {
		return result, err
	}
	result.Version = version
	selectors := []string{r.Script.ID}
	for key, loc := range cfg.CronLocations {
		if loc.ID == r.Script.ID {
			selectors = append(selectors, loc.ID+"~"+key)
		}
	}
	sort.Strings(selectors)
	for _, selector := range selectors {
		option, _, _, e := c.managedScript(cfg, r.Script.ID, selector)
		if e == nil {
			result.Scripts = append(result.Scripts, option)
		}
	}
	return result, nil
}
