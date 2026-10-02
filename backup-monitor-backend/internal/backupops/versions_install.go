package backupops

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func PatchDailyVersions(source, category string) (string, error) {
	if strings.Contains(source, DailyLimitMarker) {
		if category != "drive" && !strings.Contains(source, " versions commit "+category+" ") {
			return "", errors.New("Incomplete daily-version script")
		}
		if category == "drive" && !strings.Contains(source, " versions drive ") {
			return "", errors.New("Incomplete Drive daily-version script")
		}
		return source, nil
	}
	if category == "panel" {
		old := `PUBLISHED_FILE="$DAY_DIR/${DAY}_${STAMP:9:6}_${nonce}.zip"`
		updated, err := replaceOnce(source, old, `PUBLISHED_FILE="$DAY_DIR/${DAY}_${STAMP:9:6}_${BACKUP_MONITOR_ORIGIN}_${nonce}.zip"`)
		if err != nil {
			return "", err
		}
		updated, err = replaceOnce(updated, `printf 'SOURCE=aaPanel data/config/vhost\nBACKUP=%s\n' "$PUBLISHED_FILE"`,
			`"$MANAGER" versions commit panel "$PUBLISHED_FILE"
printf 'SOURCE=aaPanel data/config/vhost\nBACKUP=%s\n' "$PUBLISHED_FILE"`)
		if err != nil {
			return "", err
		}
		updated = strings.ReplaceAll(updated, "Does not install cron, upload to Drive, disable aaPanel, or delete old backups.", "Rotates tagged same-day versions after verification; no cron edits, Drive upload or old-date pruning.")
		updated = strings.ReplaceAll(updated, "duration_ms=$duration; no existing backup was removed", "duration_ms=$duration; check preceding validation/rotation messages")
		return addVersionOrigin(updated)
	}
	if !strings.Contains(source, LayoutMarker) {
		return "", errors.New("Install date-first layout before daily limits")
	}
	if category == "site" || category == "database" {
		suffix := "site.tar.gz"
		if category == "database" {
			suffix = "mysql_data.sql.gz"
		}
		old := `    final="$dir/${prefix}${stamp}_` + suffix + `"`
		updated, err := replaceOnce(source, old, `    local nonce
    nonce=$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n') || return 1
    [[ "$nonce" =~ ^[0-9a-f]{16}$ ]] || return 1
    final="$dir/${prefix}${stamp}_${BACKUP_MONITOR_ORIGIN}_${nonce}_`+suffix+`"`)
		if err != nil {
			return "", err
		}
		old = `    printf 'CREATED %s DURATION %d.%03d\n' "$final" "$((duration_ms / 1000))" "$((duration_ms % 1000))"`
		updated, err = replaceOnce(updated, old, "    "+BinaryPath+` versions commit `+category+` "$final" || return 1
`+old)
		if err != nil {
			return "", err
		}
		return addVersionOrigin(updated)
	}
	if category == "drive" {
		old := `        if ! rclone copy "$day_dir" "gdrive:Backup/$day" --config /root/.config/rclone/rclone.conf --include '*.tar.gz' --include '*.sql.gz' --include '*.zip'; then
            STATUS="Failed"
        fi`
		new := strings.TrimSuffix(old, "        fi") + "        elif ! " + BinaryPath + ` versions drive "$day" --apply; then
            STATUS="Failed"
        fi`
		updated, err := replaceOnce(source, old, new)
		if err != nil {
			return "", err
		}
		return replaceOnce(updated, "#!/bin/bash", "#!/bin/bash\n"+DailyLimitMarker)
	}
	return "", errors.New("Invalid daily-version category")
}

func addVersionOrigin(source string) (string, error) {
	return replaceOnce(source, "set -Eeuo pipefail", "set -Eeuo pipefail\n"+DailyLimitMarker+`
BACKUP_MONITOR_ORIGIN=${BACKUP_MONITOR_ORIGIN:-manual}
if [[ "$BACKUP_MONITOR_ORIGIN" != cron && "$BACKUP_MONITOR_ORIGIN" != manual ]]; then
    printf 'Invalid backup origin; use cron or manual.\n' >&2
    exit 2
fi`)
}

func (c *Controller) DailyVersionsPlan() ([]scriptChange, error) {
	cfg, _, err := c.Active()
	if err != nil {
		return nil, err
	}
	var plan []scriptChange
	for _, item := range []struct{ name, category string }{{"backup-site.sh", "site"}, {"backup-database.sh", "database"}, {"auto_backup.sh", "drive"}, {"backup-panel.sh", "panel"}} {
		p := filepath.Join(cfg.ScriptsDir, item.name)
		if item.category == "panel" && !exists(p) {
			continue
		}
		before, err := readRegular(p)
		if err != nil {
			return nil, err
		}
		text := string(before)
		if item.category == "drive" {
			old := "gdrive:Backup/$day"
			actual := cfg.DriveRemote + ":" + cfg.DriveFolder + "/$day"
			text = strings.ReplaceAll(text, actual, old)
			after, err := PatchDailyVersions(text, item.category)
			if err != nil {
				return nil, err
			}
			after = strings.ReplaceAll(after, old, actual)
			if after != string(before) {
				plan = append(plan, scriptChange{p, p, before, []byte(after)})
			}
			continue
		}
		after, err := PatchDailyVersions(text, item.category)
		if err != nil {
			return nil, err
		}
		if after != string(before) {
			plan = append(plan, scriptChange{p, p, before, []byte(after)})
		}
	}
	return plan, nil
}

func (c *Controller) InstallDailyVersions(apply bool, out io.Writer) (resultErr error) {
	if !apply {
		plan, err := c.DailyVersionsPlan()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "DAILY_VERSION_PREVIEW: %d scripts; no archives or cron changed.\n", len(plan))
		return nil
	}
	locks, err := acquireAll(c.Locks)
	if err != nil {
		return err
	}
	defer releaseAll(locks)
	plan, err := c.DailyVersionsPlan()
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		fmt.Fprintln(out, "Daily-version limits already installed.")
		return nil
	}
	temp, err := os.MkdirTemp("", "versions-validate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	for _, s := range plan {
		p := filepath.Join(temp, filepath.Base(s.Source))
		if err = AtomicWrite(p, s.Updated, 0600); err != nil {
			return err
		}
		if _, err = c.Run([]string{"/bin/bash", "-n", p}, nil); err != nil {
			return errors.New("Daily-version script failed syntax validation")
		}
	}
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return err
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "before-daily-versions-")
	if err != nil {
		return err
	}
	for _, s := range plan {
		if err = AtomicWrite(filepath.Join(recovery, filepath.Base(s.Source)), s.Original, 0600); err != nil {
			return err
		}
	}
	var installed []scriptChange
	defer func() {
		if resultErr == nil {
			return
		}
		for i := len(installed) - 1; i >= 0; i-- {
			if err := AtomicWrite(installed[i].Source, installed[i].Original, 0700); err != nil {
				resultErr = fmt.Errorf("Rollback needs review: %s", recovery)
			}
		}
	}()
	for _, s := range plan {
		current, err := readRegular(s.Source)
		if err != nil {
			return err
		}
		if string(current) != string(s.Original) {
			return errors.New("Script changed; refusing overwrite")
		}
		if err = c.Write(s.Source, s.Updated, 0700); err != nil {
			return err
		}
		installed = append(installed, s)
	}
	fmt.Fprintf(out, "DAILY_VERSIONS_INSTALLED: 1 cron + 2 manual per target/day; recovery=%s. No existing archive deleted.\n", recovery)
	return nil
}
