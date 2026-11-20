package backupops

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (c *Controller) InstallProgress() error {
	locks, err := acquireAll(c.Locks)
	if err != nil {
		return err
	}
	defer releaseAll(locks)
	cfg, _, err := c.Active()
	if err != nil {
		return err
	}
	p := scriptPath(cfg, "backup-monitor-run.sh")
	if err = c.LocalPath(cfg.ScriptsDir, "scripts"); err != nil {
		return err
	}
	before, err := readRegular(p)
	if err != nil {
		return err
	}
	anchor := `RUN_ID="$(date +%s%N)-$$"` + "\n"
	export := "export RCLONE_USE_JSON_LOG=true RCLONE_STATS=5s RCLONE_STATS_LOG_LEVEL=NOTICE\n"
	if strings.Contains(string(before), export) {
		return nil
	}
	if !strings.Contains(string(before), TaskControlMarker) || strings.Count(string(before), anchor) != 1 {
		return errors.New("managed wrapper shape changed; progress not installed")
	}
	after := strings.Replace(string(before), anchor, anchor+export, 1)
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return err
	}
	snapshot, err := os.MkdirTemp(c.RecoveryRoot, "before-progress-")
	if err != nil {
		return err
	}
	if err = AtomicWrite(filepath.Join(snapshot, "backup-monitor-run.sh"), before, 0600); err != nil {
		return err
	}
	candidate := filepath.Join(snapshot, "candidate.sh")
	if err = AtomicWrite(candidate, []byte(after), 0600); err != nil {
		return err
	}
	if _, err = c.Run([]string{"/bin/bash", "-n", candidate}, nil); err != nil {
		return err
	}
	current, err := readRegular(p)
	if err != nil || !bytes.Equal(current, before) {
		return errors.New("wrapper changed during progress update")
	}
	return c.Write(p, []byte(after), 0700)
}

type restoreProgressWriter struct {
	carry   []byte
	started time.Time
	update  func(*TaskProgress) error
}

func (w *restoreProgressWriter) Write(data []byte) (int, error) {
	n := len(data)
	w.carry = append(w.carry, data...)
	for {
		end := bytes.IndexByte(w.carry, '\n')
		if end < 0 {
			break
		}
		line := string(w.carry[:end])
		w.carry = w.carry[end+1:]
		if p := ParseRcloneProgress(line, "download", w.started); p != nil {
			if err := w.update(p); err != nil {
				return 0, err
			}
		}
	}
	if len(w.carry) > 1024*1024 {
		w.carry = nil
	}
	return n, nil
}

type TaskProgress struct {
	Phase      string    `json:"phase"`
	Bytes      int64     `json:"bytes"`
	TotalBytes int64     `json:"totalBytes"`
	Files      int64     `json:"files"`
	TotalFiles int64     `json:"totalFiles"`
	Speed      float64   `json:"speed"`
	ETA        *float64  `json:"eta"`
	Percent    *float64  `json:"percent"`
	ObservedAt time.Time `json:"observedAt"`
	Stale      bool      `json:"stale"`
}

func ParseRcloneProgress(line, phase string, started time.Time) *TaskProgress {
	var record struct {
		Time  time.Time `json:"time"`
		Stats *struct {
			Bytes      int64    `json:"bytes"`
			TotalBytes int64    `json:"totalBytes"`
			Files      int64    `json:"transfers"`
			TotalFiles int64    `json:"totalTransfers"`
			Speed      float64  `json:"speed"`
			ETA        *float64 `json:"eta"`
		} `json:"stats"`
	}
	if json.Unmarshal([]byte(line), &record) != nil || record.Stats == nil || record.Time.Before(started) || record.Time.After(time.Now().Add(time.Minute)) {
		return nil
	}
	s := record.Stats
	if s.Bytes < 0 || s.TotalBytes < 0 || s.Files < 0 || s.TotalFiles < 0 || s.Speed < 0 || math.IsNaN(s.Speed) || math.IsInf(s.Speed, 0) {
		return nil
	}
	p := &TaskProgress{Phase: phase, Bytes: s.Bytes, TotalBytes: s.TotalBytes, Files: s.Files, TotalFiles: s.TotalFiles, Speed: s.Speed, ETA: s.ETA, ObservedAt: record.Time, Stale: time.Since(record.Time) > 20*time.Second}
	if s.TotalBytes > 0 {
		n := min(100, float64(s.Bytes)*100/float64(s.TotalBytes))
		p.Percent = &n
	}
	if p.ETA != nil && (*p.ETA < 0 || math.IsNaN(*p.ETA) || math.IsInf(*p.ETA, 0)) {
		p.ETA = nil
	}
	return p
}
func (c *Controller) taskProgress(cfg Settings, state TaskState) (*TaskProgress, int64) {
	id := strings.Split(state.RunID, "-")[0]
	if len(id) < 10 {
		return nil, 0
	}
	sec, e := strconv.ParseInt(id[:10], 10, 64)
	if e != nil {
		return nil, 0
	}
	start := time.Unix(sec, 0)
	elapsed := max(0, int64(time.Since(start).Seconds()))
	name, e := taskLogName(state.JobID)
	if e != nil {
		return nil, elapsed
	}
	p := logPath(cfg, name)
	if SafeParents(p) != nil {
		return nil, elapsed
	}
	f, e := openRegular(p)
	if e != nil {
		return nil, elapsed
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !privateRootOwned(info) {
		return nil, elapsed
	}
	offset := max(0, info.Size()-256*1024)
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		return nil, elapsed
	}
	data, e := io.ReadAll(io.LimitReader(f, 256*1024))
	if e != nil {
		return nil, elapsed
	}
	phase := "transfer"
	var result *TaskProgress
	lines := strings.Split(string(data), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	for _, line := range lines {
		if strings.Contains(line, "Uploading backup day:") || strings.Contains(line, "Đang tải ngày sao lưu:") {
			phase = "day:" + strings.TrimSpace(line[strings.LastIndex(line, ":")+1:])
			result = nil
		}
		if strings.Contains(line, "Dang don dep Google Drive") || strings.Contains(line, "Dọn bản sao lưu quá") {
			phase = "cleanup"
			result = nil
		}
		if progress := ParseRcloneProgress(line, phase, start); progress != nil {
			result = progress
		}
	}
	return result, elapsed
}

type LogRequest struct {
	ID     string `json:"id"`
	Cursor string `json:"cursor"`
}

type logCursor struct {
	Offset, Size  int64
	Device, Inode uint64
	HeadLength    int
	HeadHash      string
}
type LogPage struct {
	Exists           bool   `json:"exists"`
	Content          string `json:"content"`
	OlderCursor      string `json:"olderCursor,omitempty"`
	Start, End, Size int64
	PartialLine      bool `json:"partialLine"`
}

func taskLogName(id string) (string, error) {
	if _, ok := TaskScripts[id]; !ok {
		return "", errors.New("Tác vụ không hợp lệ")
	}
	switch id {
	case "drive-sync":
		return "auto_backup.log", nil
	case "integrity-check":
		return "check_integrity.log", nil
	case "cleanup-panel":
		return "cleanup-panel-backups.log", nil
	}
	return id + ".log", nil
}
func (c *Controller) LogPage(r Request) (LogPage, error) {
	var result LogPage
	if r.Log == nil {
		return result, errors.New("Thiếu thông tin log")
	}
	name, err := taskLogName(strings.Split(r.Log.ID, "~")[0])
	if err != nil {
		return result, err
	}
	cfg, _, err := c.Active()
	if err != nil {
		return result, err
	}
	cfg, _, err = taskSettings(cfg, r.Log.ID)
	if err != nil {
		return result, err
	}
	path := logPath(cfg, name)
	if err = SafeParents(path); err != nil {
		return result, err
	}
	f, err := openRegular(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !privateRootOwned(info) {
		return result, errors.New("Log không an toàn")
	}
	cursor := logCursor{Offset: info.Size(), Size: info.Size(), Device: device(info), Inode: inode(info), HeadLength: int(min(info.Size(), 4096))}
	if r.Log.Cursor != "" {
		if len(r.Log.Cursor) > 2048 {
			return result, errors.New("Con trỏ log không hợp lệ")
		}
		raw, err := base64.RawURLEncoding.DecodeString(r.Log.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Offset < 0 || cursor.Offset > cursor.Size || cursor.Size > info.Size() || cursor.HeadLength < 0 || cursor.HeadLength > 4096 || cursor.HeadLength > int(cursor.Size) || cursor.Device != device(info) || cursor.Inode != inode(info) {
			return result, errors.New("Log đã thay đổi; hãy làm mới từ đầu")
		}
	}
	head := make([]byte, cursor.HeadLength)
	if _, err = io.ReadFull(f, head); err != nil {
		return result, err
	}
	sum := sha256.Sum256(head)
	hash := hex.EncodeToString(sum[:])
	if r.Log.Cursor != "" && hash != cursor.HeadHash {
		return result, errors.New("Log đã bị thay thế hoặc cắt ngắn; hãy làm mới")
	}
	cursor.HeadHash = hash
	result.Exists = true
	result.End = cursor.Offset
	result.Start = max(0, result.End-65536)
	// Do not split a UTF-8 character between independently decoded pages.
	for result.Start > 0 && result.Start < result.End {
		first := []byte{0}
		if _, err = f.ReadAt(first, result.Start); err != nil {
			return result, err
		}
		if utf8.RuneStart(first[0]) {
			break
		}
		result.Start++
	}
	result.Size = cursor.Size
	if _, err = f.Seek(result.Start, io.SeekStart); err != nil {
		return result, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, result.End-result.Start))
	if err != nil || int64(len(raw)) != result.End-result.Start {
		return result, errors.New("Log thay đổi trong lúc đọc")
	}
	after, err := f.Stat()
	if err != nil || after.Size() < cursor.Size {
		return result, errors.New("Log thay đổi trong lúc đọc; hãy thử lại")
	}
	result.Content = strings.ToValidUTF8(string(raw), "�")
	if result.Start > 0 {
		one := []byte{0}
		if _, err = f.ReadAt(one, result.Start-1); err != nil {
			return result, err
		}
		result.PartialLine = one[0] != '\n'
		cursor.Offset = result.Start
		encoded, _ := json.Marshal(cursor)
		result.OlderCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return result, nil
}

type TaskRequest struct {
	ID    string `json:"id"`
	RunID string `json:"runId"`
}
type TaskState struct {
	JobID      string `json:"jobId"`
	RunID      string `json:"runId"`
	PID        int    `json:"pid"`
	StartTicks string `json:"startTicks"`
	Script     string `json:"script"`
}
type TaskStatus struct {
	Progress        *TaskProgress `json:"progress,omitempty"`
	ElapsedSeconds  int64         `json:"elapsedSeconds"`
	Managed         bool          `json:"managed"`
	Running         bool          `json:"running"`
	RunID           string        `json:"runId,omitempty"`
	CancelRequested bool          `json:"cancelRequested"`
}

var managedRunID = regexp.MustCompile(`^[0-9]{10,30}-[0-9]{1,10}$`)

func (c *Controller) TaskOperation(r Request) (TaskStatus, error) {
	var result TaskStatus
	if r.Task == nil {
		return result, errors.New("Thiếu tác vụ")
	}
	selector := r.Task.ID
	task := *r.Task
	task.ID = strings.Split(selector, "~")[0]
	r.Task = &task
	if _, ok := TaskScripts[r.Task.ID]; !ok {
		return result, errors.New("Tác vụ không hợp lệ")
	}
	cfg, _, err := c.Active()
	if err != nil {
		return result, err
	}
	cfg, _, err = taskSettings(cfg, selector)
	if err != nil {
		return result, err
	}
	path := taskStatePath(cfg, r.Task.ID)
	if !exists(path) {
		return result, nil
	}
	raw, err := readRegular(path)
	if err != nil || len(raw) > 4096 {
		return result, errors.New("Không xác minh được trạng thái tiến trình")
	}
	var state TaskState
	if json.Unmarshal(raw, &state) != nil || state.JobID != r.Task.ID || !managedRunID.MatchString(state.RunID) || state.Script != scriptPath(cfg, TaskScripts[r.Task.ID]) || state.PID <= 1 {
		return result, errors.New("Trạng thái tiến trình không hợp lệ")
	}
	info, err := nearest(path)
	if err != nil || !privateRootOwned(info) {
		return result, errors.New("Trạng thái tiến trình không an toàn")
	}
	result.Managed = true
	result.RunID = state.RunID
	result.Running, err = validateTaskProcess(state, scriptPath(cfg, "backup-monitor-run.sh"))
	if err != nil {
		return result, err
	}
	if result.Running {
		result.Progress, result.ElapsedSeconds = c.taskProgress(cfg, state)
	}
	if r.Operation == "task-cancel" {
		if !result.Running || r.Task.RunID != state.RunID {
			return result, errors.New("Lần chạy đã thay đổi hoặc đã kết thúc; hãy làm mới")
		}
		if err = cancelTaskProcess(state, scriptPath(cfg, "backup-monitor-run.sh")); err != nil {
			return result, err
		}
		result.CancelRequested = true
	}
	return result, nil
}
