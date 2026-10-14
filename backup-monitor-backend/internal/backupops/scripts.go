package backupops

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"backup-monitor-backend/ops"
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
