package backupops

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
