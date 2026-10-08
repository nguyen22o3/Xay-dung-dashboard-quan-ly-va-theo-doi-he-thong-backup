package backupops

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
)

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
	if _, ok := TaskScripts[r.Task.ID]; !ok {
		return result, errors.New("Tác vụ không hợp lệ")
	}
	cfg, _, err := c.Active()
	if err != nil {
		return result, err
	}
	path := filepath.Join(cfg.LogsDir, ".backup-monitor-"+r.Task.ID+".run.json")
	if !exists(path) {
		return result, nil
	}
	raw, err := readRegular(path)
	if err != nil || len(raw) > 4096 {
		return result, errors.New("Không xác minh được trạng thái tiến trình")
	}
	var state TaskState
	if json.Unmarshal(raw, &state) != nil || state.JobID != r.Task.ID || !managedRunID.MatchString(state.RunID) || state.Script != filepath.Join(cfg.ScriptsDir, TaskScripts[r.Task.ID]) || state.PID <= 1 {
		return result, errors.New("Trạng thái tiến trình không hợp lệ")
	}
	info, err := nearest(path)
	if err != nil || !privateRootOwned(info) {
		return result, errors.New("Trạng thái tiến trình không an toàn")
	}
	result.Managed = true
	result.RunID = state.RunID
	result.Running, err = validateTaskProcess(state, filepath.Join(cfg.ScriptsDir, "backup-monitor-run.sh"))
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
		if err = cancelTaskProcess(state, filepath.Join(cfg.ScriptsDir, "backup-monitor-run.sh")); err != nil {
			return result, err
		}
		result.CancelRequested = true
	}
	return result, nil
}
