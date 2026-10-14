package backupops

import (
	"os"
	"path/filepath"
	"strings"
)

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
