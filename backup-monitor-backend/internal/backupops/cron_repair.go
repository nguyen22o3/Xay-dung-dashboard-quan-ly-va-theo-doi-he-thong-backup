package backupops

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
