package backupops

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func replaceOnce(text, old, new string) (string, error) {
	if strings.Count(text, old) != 1 {
		return "", errors.New("Unexpected script structure: " + strings.Split(old, "\n")[0])
	}
	return strings.Replace(text, old, new, 1), nil
}

// PatchBackup/PatchDrive retain the original date-layout installer behavior,
// but all helper calls point to the stable Go binary rather than Python.
func PatchBackup(source, category string) (string, error) {
	site := category == "site"
	if !site && category != "database" {
		return "", errors.New("Invalid category")
	}
	variable, oldRoot, subdir, fn := "SITE_BACKUP_ROOT", "/www/backup/site", "site", "rotate_site"
	if !site {
		variable, oldRoot, subdir, fn = "DATABASE_BACKUP_ROOT", "/www/backup/database/mysql/crontab_backup", "database/mysql/crontab_backup", "rotate_database"
	}
	var err error
	source, err = replaceOnce(source, "BACKUP_ROOT=${"+variable+":-"+oldRoot+"}", "BACKUP_ROOT=${"+variable+":-}")
	if err != nil {
		return "", err
	}
	pattern := regexp.MustCompile(`(?ms)^` + fn + `\(\) \{.*?^\}\n`)
	if len(pattern.FindAllStringIndex(source, -1)) != 1 {
		return "", errors.New("Unexpected rotation function")
	}
	source = pattern.ReplaceAllString(source, "")
	source, err = replaceOnce(source, "    "+fn+` "$dir" "$prefix"`, "    return 0")
	if err != nil {
		return "", err
	}
	source, err = replaceOnce(source, "    stamp=$(date '+%Y%m%d_%H%M%S')", "    exec 7>/run/backup-monitor-layout.lock\n    flock -s 7\n    stamp=$(date '+%Y%m%d_%H%M%S')\n    BACKUP_ROOT=${"+variable+":-/www/backup/${stamp:0:4}-${stamp:4:2}-${stamp:6:2}/"+subdir+"}")
	if err != nil {
		return "", err
	}
	source, err = replaceOnce(source, `    return "$failures"`, "    if (( failures == 0 )); then\n        "+BinaryPath+" layout prune "+category+" --delete || failures=1\n    fi\n    return \"$failures\"")
	if err != nil {
		return "", err
	}
	return replaceOnce(source, "set -Eeuo pipefail", LayoutMarker+"\nset -Eeuo pipefail")
}
func PatchDrive(source string) (string, error) {
	start, end := strings.Index(source, "# 1. Day toan bo ma nguon Website len Drive"), strings.Index(source, "# 4. Giu 14 thu muc ngay co backup gan nhat")
	if start < 0 || end <= start {
		return "", errors.New("Unexpected Drive upload script structure")
	}
	uploads := `# Collect completed aaPanel ZIPs; native staging stays unchanged.
if ! ` + BinaryPath + ` layout organize --apply; then
    STATUS="Failed"
else
    for day_dir in /www/backup/20??-??-??; do
        [[ -d "$day_dir" && ! -L "$day_dir" ]] || continue
        day=${day_dir##*/}
        [[ "$(date -d "$day" +%F 2>/dev/null)" == "$day" ]] || continue
        [[ "$day" > "$TODAY" ]] && continue
        echo "--> Uploading backup day: $day"
        if ! rclone copy "$day_dir" "gdrive:Backup/$day" --config /root/.config/rclone/rclone.conf --include '*.tar.gz' --include '*.sql.gz' --include '*.zip'; then
            STATUS="Failed"
        fi
    done
fi

`
	source = source[:start] + uploads + source[end:]
	var err error
	source, err = replaceOnce(source, `START_TIME=$(date +"%I:%M:%S %p")`, `START_TIME=$(date +"%H:%M:%S")`)
	if err != nil {
		return "", err
	}
	return replaceOnce(source, "#!/bin/bash", "#!/bin/bash\n"+LayoutMarker+`
exec 7>/run/backup-monitor-layout.lock
flock -s 7
exec 6>/run/backup-monitor-drive.lock
flock -n 6 || { echo 'Drive upload already running' >&2; exit 75; }`)
}
func CleanupScript() string {
	return `#!/usr/bin/env bash
` + LayoutMarker + `
set -euo pipefail
mode=${1:---dry-run}
if [[ "$mode" != --delete && "$mode" != --dry-run ]] || (( $# > 1 )); then
    echo 'Usage: cleanup-panel-backups.sh [--dry-run|--delete]' >&2
    exit 2
fi
exec 7>/run/backup-monitor-layout.lock
flock -s 7
exec 9>/run/backup-monitor-panel-retention.lock
flock -n 9 || { echo 'Panel cleanup already running' >&2; exit 75; }
if [[ "$mode" == --delete ]]; then
    ` + BinaryPath + ` layout organize --category panel --apply
    ` + BinaryPath + ` layout prune panel --delete
else
    ` + BinaryPath + ` layout prune panel
fi
`
}
func (c *Controller) InstallPlan() ([]scriptChange, error) {
	cfg, _, err := c.Active()
	if err != nil {
		return nil, err
	}
	if err = c.LocalPath(cfg.ScriptsDir, "scripts"); err != nil {
		return nil, err
	}
	result := []scriptChange{}
	required := map[string]int{"backup-site.sh": 1, "backup-database.sh": 1, "auto_backup.sh": 1, "cleanup-panel-backups.sh": 3}
	for _, name := range []string{"backup-site.sh", "backup-database.sh", "auto_backup.sh", "cleanup-panel-backups.sh"} {
		source := filepath.Join(cfg.ScriptsDir, name)
		original, err := readRegular(source)
		if err != nil {
			return nil, err
		}
		content := string(original)
		var updated string
		if strings.Contains(content, LayoutMarker) {
			reference := "/usr/bin/python3 " + filepath.ToSlash(filepath.Join(cfg.ScriptsDir, "daily_backup_layout.py"))
			count := strings.Count(content, reference)
			if count == 0 && !strings.Contains(content, "daily_backup_layout.py") && strings.Count(content, BinaryPath+" layout ") == required[name] {
				continue
			}
			if count != required[name] {
				return nil, errors.New("Unexpected legacy helper calls: " + name)
			}
			updated = strings.ReplaceAll(content, reference, BinaryPath+" layout")
			if strings.Contains(updated, "daily_backup_layout.py") {
				return nil, errors.New("Unrecognized Python helper reference: " + name)
			}
		} else {
			switch name {
			case "backup-site.sh":
				updated, err = PatchBackup(content, "site")
			case "backup-database.sh":
				updated, err = PatchBackup(content, "database")
			case "auto_backup.sh":
				updated, err = PatchDrive(content)
			case "cleanup-panel-backups.sh":
				updated = CleanupScript()
			}
			if err != nil {
				return nil, err
			}
		}
		result = append(result, scriptChange{source, source, original, []byte(updated)})
	}
	return result, nil
}
func (c *Controller) Install(apply bool, out io.Writer) (resultErr error) {
	if !apply {
		plan, err := c.InstallPlan()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "INSTALL_PREVIEW: %d managed scripts; no backup, cleanup, upload or cron change.\n", len(plan))
		return nil
	}
	locks, err := acquireAll(c.Locks)
	if err != nil {
		return err
	}
	defer releaseAll(locks)
	plan, err := c.InstallPlan()
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		fmt.Fprintln(out, "Already installed; scripts use the Go helper.")
		return nil
	}
	// Stage every script and validate Bash syntax before replacing any file.
	temporary, err := os.MkdirTemp("", "go-helper-validate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	for _, script := range plan {
		path := filepath.Join(temporary, filepath.Base(script.Source))
		if err = AtomicWrite(path, script.Updated, 0600); err != nil {
			return err
		}
		if _, err = c.Run([]string{"/bin/bash", "-n", path}, nil); err != nil {
			return errors.New("Updated script failed syntax validation")
		}
	}
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return err
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "before-go-helper-*")
	if err != nil {
		return err
	}
	for _, script := range plan {
		if err = AtomicWrite(filepath.Join(recovery, filepath.Base(script.Source)), script.Original, 0600); err != nil {
			return err
		}
	}
	cfg, _, err := c.Active()
	if err != nil {
		return err
	}
	python := filepath.Join(cfg.ScriptsDir, "daily_backup_layout.py")
	if exists(python) {
		data, err := readRegular(python)
		if err != nil {
			return err
		}
		if err = AtomicWrite(filepath.Join(recovery, "daily_backup_layout.py"), data, 0600); err != nil {
			return err
		}
	}
	installed := []scriptChange{}
	defer func() {
		if resultErr == nil {
			return
		}
		for i := len(installed) - 1; i >= 0; i-- {
			if err := AtomicWrite(installed[i].Source, installed[i].Original, 0700); err != nil {
				resultErr = fmt.Errorf("Install failed; inspect recovery copies at %s", recovery)
			}
		}
	}()
	for _, script := range plan {
		current, err := readRegular(script.Source)
		if err != nil {
			return err
		}
		if string(current) != string(script.Original) {
			return errors.New("Script changed after preflight; refusing overwrite")
		}
		if err = c.Write(script.Source, script.Updated, 0700); err != nil {
			return err
		}
		installed = append(installed, script)
	}
	fmt.Fprintf(out, "GO_HELPER_INSTALLED; previous scripts/helper retained at %s. No backup, cleanup, upload or cron executed.\n", recovery)
	return nil
}
