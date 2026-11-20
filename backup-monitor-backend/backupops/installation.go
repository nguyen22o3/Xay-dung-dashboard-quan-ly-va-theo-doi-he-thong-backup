package backupops

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Only this root-owned locator lives outside the selected application folder.
const InstallationFile = "/etc/backup-monitor/installation.json"

type InstallationPaths struct {
	DataDir          string `json:"dataDir"`
	ConfigDir        string `json:"configDir"`
	RcloneConfig     string `json:"rcloneConfig"`
	RecoveryRoot     string `json:"recoveryRoot"`
	MetadataDir      string `json:"metadataDir"`
	LockPrefix       string `json:"lockPrefix"`
	MySQLOptionsFile string `json:"mysqlOptionsFile"`
}

func LegacyInstallation() InstallationPaths {
	return InstallationPaths{"", "/root/backup-monitor", "/root/.config/rclone/rclone.conf", "/root/backup-layout-migrations", VersionMetadataDir, "/root/.backup-monitor-", "/root/.my.cnf"}
}

func InstallationAt(root string) InstallationPaths {
	return InstallationPaths{root, path.Join(root, "config"), path.Join(root, "config/rclone/rclone.conf"), path.Join(root, "recovery"), path.Join(root, "config/metadata/versions"), "/run/backup-monitor-", path.Join(root, "config/mysql.cnf")}
}

func ValidateInstallationDirectory(root string) error {
	if !regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`).MatchString(root) || path.Clean(root) != root || strings.Contains(root, "..") || len(root) > 512 {
		return errors.New("Thư mục ứng dụng phải là đường dẫn Linux tuyệt đối, không có khoảng trắng hoặc ..")
	}
	for _, protected := range []string{"/root", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/proc", "/sys", "/dev", "/run", "/tmp", "/www/server", "/www/wwwroot"} {
		if root == protected || strings.HasPrefix(root, protected+"/") {
			return errors.New("Không dùng thư mục hệ thống, /root hoặc dữ liệu website làm thư mục ứng dụng")
		}
	}
	if len(strings.Split(strings.TrimPrefix(root, "/"), "/")) < 2 {
		return errors.New("Chọn thư mục riêng, ví dụ /opt/backup-monitor")
	}
	return nil
}

func loadInstallation(filename string) (InstallationPaths, error) {
	legacy := LegacyInstallation()
	if err := SafeParents(filename); err != nil {
		return legacy, err
	}
	info, err := os.Lstat(filename)
	if os.IsNotExist(err) {
		return legacy, nil
	}
	if err != nil || !info.Mode().IsRegular() || !privateRootOwned(info) || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return legacy, errors.New("Tệp vị trí cài đặt không an toàn")
	}
	raw, err := readRegular(filename)
	if err != nil {
		return legacy, err
	}
	var locator struct {
		DataDir string `json:"dataDir"`
	}
	if json.Unmarshal(raw, &locator) != nil || ValidateInstallationDirectory(locator.DataDir) != nil {
		return legacy, errors.New("Tệp vị trí cài đặt không hợp lệ")
	}
	p := InstallationAt(locator.DataDir)
	if err = SafeParents(p.DataDir); err != nil {
		return legacy, err
	}
	root, err := os.Stat(p.DataDir)
	if err != nil || !root.IsDir() || !privateRootOwned(root) || root.Mode().Perm()&0077 != 0 {
		return legacy, errors.New("Thư mục ứng dụng bị thiếu hoặc không an toàn; không quay lại cấu hình /root")
	}
	for parent := filepath.Dir(p.DataDir); ; parent = filepath.Dir(parent) {
		info, e := os.Lstat(parent)
		if e != nil || !info.IsDir() || !privateRootOwned(info) {
			return legacy, errors.New("Thư mục cha của ứng dụng không an toàn")
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	return p, nil
}

// Only named control files and the private MySQL options copy are mapped.
// Never map /root wholesale or rewrite SSH/unrelated administrator files.
func InstallationPairs(from, to InstallationPaths) [][2]string {
	pairs := [][2]string{{from.ConfigDir, to.ConfigDir}, {from.RcloneConfig, to.RcloneConfig}, {from.RecoveryRoot, to.RecoveryRoot}, {from.MetadataDir, to.MetadataDir}, {from.LockPrefix, to.LockPrefix}}
	oldMySQL := from.MySQLOptionsFile
	if oldMySQL == "" {
		oldMySQL = "/root/.my.cnf"
	}
	newMySQL := to.MySQLOptionsFile
	if newMySQL == "" {
		newMySQL = oldMySQL
	}
	pairs = append(pairs, [2]string{oldMySQL, newMySQL})
	if to.DataDir != "" {
		pairs = append(pairs, [2]string{"/root/.backup-panel.", path.Join(to.DataDir, "tmp/.backup-panel.")})
	}
	result := [][2]string{}
	for _, pair := range pairs {
		if pair[0] != "" && pair[1] != "" {
			result = append(result, pair)
		}
	}
	return result
}

func runtimeTemplate(text string) string {
	p, err := loadInstallation(InstallationFile)
	if err != nil {
		return text
	} // Controller rejects invalid installation before use.
	return Rewrite(text, InstallationPairs(LegacyInstallation(), p))
}

func (c *Controller) InstallationPaths() InstallationPaths {
	if c.Installation.ConfigDir != "" {
		return c.Installation
	}
	return InstallationPaths{ConfigDir: filepath.ToSlash(filepath.Dir(c.ConfigPath)), RcloneConfig: filepath.ToSlash(c.RcloneConfig), RecoveryRoot: filepath.ToSlash(c.RecoveryRoot), MetadataDir: filepath.ToSlash(c.VersionMetadataDir), LockPrefix: LegacyInstallation().LockPrefix}
}

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
	source, err = replaceOnce(source, "#!/bin/bash", "#!/bin/bash\n"+LayoutMarker+`
exec 7>/run/backup-monitor-layout.lock
flock -s 7
exec 6>/run/backup-monitor-drive.lock
flock -n 6 || { echo 'Drive upload already running' >&2; exit 75; }`)
	if err != nil {
		return "", err
	}
	return PatchDriveExitStatus(source)
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
		// Generated templates already call the stable Go helper and must not
		// be fed into a legacy script rewriter.
		if strings.Contains(content, GeneratedTaskMarker) {
			continue
		}
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
		result = append(result, scriptChange{Source: source, Target: source, Original: original, Updated: []byte(runtimeTemplate(updated))})
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

type storageFile struct {
	Source, Target, Hash string
	Size                 int64
	Mode                 os.FileMode
	Script               bool
}

type storagePlan struct {
	Files    []storageFile
	Pairs    [][2]string
	Config   Settings
	Cron     []byte
	NextCron []byte
	Bytes    int64
}

func (c *Controller) storagePlan(root string) (storagePlan, error) {
	p := storagePlan{}
	if c.InstallationError != nil {
		return p, c.InstallationError
	}
	if err := ValidateInstallationDirectory(root); err != nil {
		return p, err
	}
	if c.Installation.DataDir != "" {
		return p, errors.New("Ứng dụng đã có thư mục chung; dùng lại thư mục đã cài, không đổi vị trí bằng bộ cài nâng cấp")
	}
	if err := SafeParents(root); err != nil {
		return p, err
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		return p, errors.New("Thư mục đích phải chưa tồn tại; không trộn hoặc ghi đè dữ liệu")
	}
	for parent := filepath.Dir(root); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || !privateRootOwned(info) {
			return p, errors.New("Thư mục cha phải tồn tại, do root sở hữu và không cho người khác ghi")
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	old, _, err := c.Active()
	if err != nil {
		return p, err
	}
	next := old
	next.ScriptsDir, next.LogsDir = filepath.Join(root, "scripts"), filepath.Join(root, "logs")
	next.DriveHistoryLog = filepath.Join(next.LogsDir, "aapanel_backup.log")
	next.CronLocations = map[string]CronLocation{}
	for key, loc := range old.CronLocations {
		loc.ScriptsDir, loc.LogsDir = next.ScriptsDir, next.LogsDir
		next.CronLocations[key] = loc
	}
	to := InstallationAt(root)
	for _, cfg := range []Settings{old} {
		for _, protected := range []string{cfg.BackupRoot, effectiveSource(cfg, "backup-site").Path, effectiveSource(cfg, "backup-database").Path, effectiveSource(cfg, "backup-panel").Path} {
			if protected != "" && (within(root, protected) || within(protected, root)) {
				return p, errors.New("Thư mục ứng dụng phải tách biệt nơi chứa backup và nguồn dữ liệu")
			}
		}
	}
	for key, loc := range old.CronLocations {
		local, e := CronSettings(old, loc.ID, key)
		if e != nil {
			return p, e
		}
		for _, protected := range []string{local.BackupRoot, effectiveSource(local, loc.ID).Path} {
			if protected != "" && (within(root, protected) || within(protected, root)) {
				return p, errors.New("Thư mục ứng dụng chồng lấn dữ liệu của cron")
			}
		}
	}
	check := *c
	check.ConfigPath, check.RecoveryRoot, check.VersionMetadataDir = filepath.Join(to.ConfigDir, "system-config.json"), to.RecoveryRoot, to.MetadataDir
	if exists(c.ConfigPath) {
		if err = check.Validate(next); err != nil {
			return p, err
		}
	} else {
		// A fresh installation does not yet have a user's Drive connection.
		for _, item := range []struct{ value, purpose string }{{next.ScriptsDir, "scripts"}, {next.LogsDir, "logs"}, {next.BackupRoot, "backup"}} {
			if err = check.LocalPath(item.value, item.purpose); err != nil {
				return p, err
			}
		}
	}
	p.Config = next
	p.Pairs = append(InstallationPairs(c.InstallationPaths(), to), settingsPairs(old, next)...)
	p.Pairs = append(p.Pairs, [2]string{fmt.Sprintf("RUN_STATE=%q", old.LogsDir), fmt.Sprintf("RUN_STATE=%q", next.LogsDir)})
	targets := map[string]string{}
	sources := map[string]bool{}
	add := func(source, target string, script bool) error {
		info, e := os.Lstat(source)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil || !info.Mode().IsRegular() || !privateRootOwned(info) {
			return fmt.Errorf("Tệp nguồn không an toàn: %s", source)
		}
		if !within(target, root) {
			return errors.New("Đích nằm ngoài thư mục ứng dụng")
		}
		if sources[source] {
			return nil
		}
		if previous, ok := targets[target]; ok && previous != source {
			return fmt.Errorf("Hai tệp cùng tên ở đích; cần xử lý trước: %s", target)
		}
		hash, e := Digest(source)
		if e != nil {
			return e
		}
		mode := os.FileMode(0600)
		if script {
			mode = 0700
			if info.Size() > 1024*1024 {
				return errors.New("Script quá lớn; chưa chuyển dữ liệu")
			}
		}
		p.Files = append(p.Files, storageFile{source, target, hash, info.Size(), mode, script})
		p.Bytes += info.Size()
		targets[target], sources[source] = source, true
		return nil
	}
	copyTree := func(source, target string) error {
		if !exists(source) {
			return nil
		}
		if e := SafeParents(source); e != nil {
			return e
		}
		return filepath.WalkDir(source, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, e := entry.Info()
			if e != nil || !privateRootOwned(info) || entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("Thư mục có liên kết hoặc quyền không an toàn: %s", name)
			}
			if entry.IsDir() {
				return nil
			}
			rel, e := filepath.Rel(source, name)
			if e != nil {
				return e
			}
			return add(name, filepath.Join(target, rel), false)
		})
	}
	if err = copyTree(filepath.Dir(c.ConfigPath), to.ConfigDir); err != nil {
		return p, err
	}
	if err = copyTree(c.RecoveryRoot, to.RecoveryRoot); err != nil {
		return p, err
	}
	if err = add(c.RcloneConfig, to.RcloneConfig, false); err != nil {
		return p, err
	}
	oldMySQL := c.InstallationPaths().MySQLOptionsFile
	if oldMySQL == "" {
		oldMySQL = "/root/.my.cnf"
	}
	if err = add(oldMySQL, to.MySQLOptionsFile, false); err != nil {
		return p, err
	}
	collect := func(before, after Settings, global bool) error {
		for _, name := range ScriptNames {
			if !global && name == "realtime_monitor.sh" {
				continue
			}
			if e := add(scriptPath(before, name), scriptPath(after, name), true); e != nil {
				return e
			}
		}
		for _, name := range append(append([]string{}, LogNames...), "aapanel_backup.log") {
			if e := add(logPath(before, name), logPath(after, name), false); e != nil {
				return e
			}
		}
		if global {
			if e := add(before.DriveHistoryLog, after.DriveHistoryLog, false); e != nil {
				return e
			}
		}
		for id := range TaskScripts {
			suffix := ""
			if before.fileKey != "" {
				suffix = "-" + before.fileKey
			}
			name := ".backup-monitor-" + id + ".run" + suffix + ".json"
			if e := add(filepath.Join(before.LogsDir, name), filepath.Join(after.LogsDir, name), false); e != nil {
				return e
			}
		}
		return nil
	}
	if err = collect(old, next, true); err != nil {
		return p, err
	}
	keys := []string{}
	for key := range old.CronLocations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		loc := old.CronLocations[key]
		before, _ := CronSettings(old, loc.ID, key)
		after, _ := CronSettings(next, loc.ID, key)
		p.Pairs = append(p.Pairs, settingsPairs(before, after)...)
		p.Pairs = append(p.Pairs, [2]string{fmt.Sprintf("RUN_STATE=%q", before.LogsDir), fmt.Sprintf("RUN_STATE=%q", after.LogsDir)})
		if err = collect(before, after, false); err != nil {
			return p, err
		}
	}
	// Missing scripts referenced by cron must be repaired before migration.
	p.Cron, err = c.readStorageCron()
	if err != nil {
		return p, errors.New("Không đọc được crontab; chưa chuyển dữ liệu")
	}
	p.NextCron = []byte(Rewrite(string(p.Cron), p.Pairs))
	for _, file := range p.Files {
		if !file.Script {
			continue
		}
		running, e := scriptProcessRunning(file.Source)
		if e != nil {
			return p, e
		}
		if running && filepath.Base(file.Source) != "realtime_monitor.sh" {
			return p, errors.New("Có script đang chạy; hãy chờ tác vụ kết thúc")
		}
	}
	for _, line := range strings.Split(string(p.Cron), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, field := range strings.Fields(line) {
			field = strings.Trim(field, "\"';")
			if !strings.HasSuffix(field, ".sh") {
				continue
			}
			mapped := Rewrite(field, p.Pairs)
			if mapped != field && within(mapped, next.ScriptsDir) && !sources[field] {
				return p, fmt.Errorf("Cron trỏ đến script thiếu hoặc chưa được quản lý: %s", field)
			}
		}
	}
	return p, nil
}

// InstallStorage copies into an unpublished directory, checks every checksum,
// updates cron and publishes the locator last. Original files are retained.
func (c *Controller) InstallStorage(root string, apply bool, out io.Writer) (resultErr error) {
	if c.InstallationError != nil {
		return c.InstallationError
	}
	if c.Installation.DataDir == root && root != "" {
		fmt.Fprintf(out, "APPLICATION_DIRECTORY: %s (unchanged)\n", root)
		return nil
	}
	locks := []*os.File{}
	if apply {
		paths := append([]string{}, c.Locks...)
		for _, worker := range []string{"restores", "restore-checks"} {
			filename := filepath.Join(c.RecoveryRoot, worker, ".worker.lock")
			if exists(filename) {
				paths = append(paths, filename)
			}
		}
		// Hold both generations of job/config locks during the switch. Locks in
		// /run remain stable when storage paths change; never unlink old locks.
		for _, old := range c.Locks {
			newPath := Rewrite(old, InstallationPairs(c.InstallationPaths(), InstallationAt(root)))
			if newPath != old {
				paths = append(paths, newPath)
			}
		}
		var err error
		locks, err = acquireAll(paths)
		if err != nil {
			return err
		}
		defer releaseAll(locks)
	}
	p, err := c.storagePlan(root)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "APPLICATION_DIRECTORY: %s\nCOPY: %d files, %d bytes\nBackup archives remain in their existing locations. Original application files are retained.\n", root, len(p.Files), p.Bytes)
	if !apply {
		return nil
	}
	free, err := restoreFreeBytes(filepath.Dir(root))
	if err != nil || p.Bytes < 0 || free <= uint64(p.Bytes)+8*1024*1024 {
		return errors.New("Không đủ dung lượng để tạo bản sao an toàn ở thư mục mới")
	}
	beforeConfig, _, err := c.Active()
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), ".backup-monitor-storage-*")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	for _, directory := range []string{"config", "config/rclone", "scripts", "logs", "recovery", "tmp"} {
		if err = mkdirPrivate(filepath.Join(stage, directory)); err != nil {
			return err
		}
	}
	for _, file := range p.Files {
		rel, e := filepath.Rel(root, file.Target)
		if e != nil {
			return e
		}
		target := filepath.Join(stage, rel)
		if e = mkdirPrivate(filepath.Dir(target)); e != nil {
			return e
		}
		if file.Script {
			raw, e := readRegular(file.Source)
			if e != nil || hashBytes(raw) != file.Hash {
				return errors.New("Script đã thay đổi trong lúc chuyển")
			}
			if e = AtomicWrite(target, []byte(Rewrite(string(raw), p.Pairs)), file.Mode); e != nil {
				return e
			}
			if _, e = c.Run([]string{"/bin/bash", "-n", target}, nil); e != nil {
				return errors.New("Script sau chuyển không hợp lệ; cấu hình cũ được giữ nguyên")
			}
		} else {
			if e = copyStorageFile(file, target); e != nil {
				return e
			}
			// Legacy restore records encode their extracted directory absolutely.
			if strings.HasSuffix(file.Source, string(filepath.Separator)+"state.json") && within(file.Source, c.RecoveryRoot) {
				if e = migrateRestoreRecord(target, p.Pairs); e != nil {
					return e
				}
			}
		}
	}
	raw, err := json.MarshalIndent(p.Config, "", "  ")
	if err != nil {
		return err
	}
	if exists(c.ConfigPath) {
		if err = AtomicWrite(filepath.Join(stage, "config/system-config.json"), raw, 0600); err != nil {
			return err
		}
	}
	// Preserve the original cron alongside migration recovery copies.
	if err = AtomicWrite(filepath.Join(stage, "recovery/crontab.before-storage.txt"), p.Cron, 0600); err != nil {
		return err
	}
	for _, file := range p.Files {
		digest, e := Digest(file.Source)
		if e != nil || digest != file.Hash {
			return errors.New("Dữ liệu nguồn đã thay đổi; chưa cập nhật cron")
		}
	}
	currentCron, err := c.readStorageCron()
	if err != nil || string(currentCron) != string(p.Cron) {
		return errors.New("Crontab đã thay đổi; chưa chuyển cấu hình")
	}
	if _, e := os.Lstat(root); !os.IsNotExist(e) {
		return errors.New("Thư mục đích vừa được tạo bởi thao tác khác")
	}
	if err = os.Rename(stage, root); err != nil {
		return err
	}
	published = true
	locator := c.LocatorPath
	if locator == "" {
		locator = InstallationFile
	}
	if err = mkdirPrivate(filepath.Dir(locator)); err != nil {
		return err
	}
	if exists(locator) {
		return errors.New("Vị trí cài đặt vừa thay đổi; giữ dữ liệu sao chép nhưng chưa đổi cron")
	}
	if _, err = c.Run([]string{"crontab", "-"}, p.NextCron); err != nil {
		if _, rollbackErr := c.Run([]string{"crontab", "-"}, p.Cron); rollbackErr != nil {
			return fmt.Errorf("Không khôi phục được cron; dùng %s/recovery/crontab.before-storage.txt", root)
		}
		return fmt.Errorf("Không cập nhật được cron; cấu hình cũ được giữ, bản sao ở %s", root)
	}
	locatorData, _ := json.Marshal(struct {
		DataDir string `json:"dataDir"`
	}{root})
	write := c.Write
	if write == nil {
		write = AtomicWrite
	}
	if err = write(locator, locatorData, 0600); err != nil {
		if _, rollbackErr := c.Run([]string{"crontab", "-"}, p.Cron); rollbackErr != nil {
			return fmt.Errorf("Không khôi phục được cron; dùng %s/recovery/crontab.before-storage.txt", root)
		}
		return fmt.Errorf("Không lưu được vị trí cài đặt; cron cũ đã khôi phục, bản sao ở %s", root)
	}
	if c.RestartMonitor != nil {
		warnings, e := c.RestartMonitor(beforeConfig, p.Config)
		if e != nil {
			fmt.Fprintln(out, "WARNING: monitor restart failed; check the new @reboot path and restart it manually.")
		}
		for _, warning := range warnings {
			fmt.Fprintln(out, "WARNING:", warning)
		}
	}
	fmt.Fprintln(out, "STORAGE_INSTALLED: config, managed scripts/logs, Drive configuration and recovery data now use the selected folder. Restart the Windows backend or host agent. No backup jobs were executed; no original files were deleted.")
	return nil
}

func copyStorageFile(file storageFile, target string) error {
	source, err := openRegular(file.Source)
	if err != nil {
		return err
	}
	defer source.Close()
	dest, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, file.Mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(dest, source)
	if err == nil {
		err = dest.Sync()
	}
	closeErr := dest.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	digest, err := Digest(target)
	if err != nil || digest != file.Hash {
		return errors.New("Kiểm tra checksum bản sao không đạt; chưa đổi cấu hình")
	}
	return nil
}

func (c *Controller) readStorageCron() ([]byte, error) {
	data, err := c.Run([]string{"crontab", "-l"}, nil)
	if err == nil {
		return data, nil
	}
	// AlmaLinux cronie reports an error for a new account with no crontab.
	// Do not interpret command/permission errors on an existing spool as empty.
	if _, lookupErr := exec.LookPath("crontab"); lookupErr == nil {
		if _, statErr := os.Lstat("/var/spool/cron/root"); os.IsNotExist(statErr) {
			return []byte{}, nil
		}
	}
	return nil, err
}

func migrateRestoreRecord(filename string, pairs [][2]string) error {
	raw, err := readRegular(filename)
	if err != nil || len(raw) > 16384 {
		return errors.New("Trạng thái phục hồi không hợp lệ")
	}
	var state map[string]interface{}
	if json.Unmarshal(raw, &state) != nil {
		return errors.New("Không đọc được trạng thái phục hồi")
	}
	if state["status"] == "running" || state["status"] == "queued" {
		return errors.New("Có tác vụ khôi phục chưa kết thúc; chưa chuyển dữ liệu")
	}
	for _, key := range []string{"directory", "parent"} {
		if value, ok := state[key].(string); ok {
			state[key] = Rewrite(value, pairs)
		}
	}
	raw, err = json.Marshal(state)
	if err != nil {
		return err
	}
	return AtomicWrite(filename, raw, 0600)
}

type nameRemoteFile struct {
	Path    string
	Size    int64
	IsDir   bool
	ID      string
	ModTime string
	Hashes  map[string]string
}
type nameRename struct {
	Source      string          `json:"source"`
	Destination string          `json:"destination"`
	Category    string          `json:"category"`
	Day         string          `json:"day"`
	Origin      string          `json:"origin"`
	Local       bool            `json:"local"`
	Size        int64           `json:"size"`
	MtimeNS     int64           `json:"mtime_ns"`
	Inode       uint64          `json:"inode"`
	Device      uint64          `json:"device"`
	Hash        string          `json:"sha256"`
	Remote      *nameRemoteFile `json:"remote,omitempty"`
}
type nameMigrationPlan struct {
	Config  Settings
	Entries []*nameRename
	Remote  map[string]nameRemoteFile
	Scripts []scriptChange
	Ledgers map[string]VersionLedger
}

// Keep origin metadata locally; only compressed archives belong on Drive.
func PatchDriveArchiveOnly(source string) (string, error) {
	if strings.Count(source, "rclone copy ") != 1 {
		return "", errors.New("Unexpected Drive upload command count")
	}
	if strings.Count(source, DriveArchiveFilters) == 1 {
		if strings.Count(source, "--filter ") != 5 || strings.Contains(source, "--include ") || strings.Contains(source, "--exclude ") {
			return "", errors.New("Unexpected mixed Drive archive upload filters")
		}
		return source, nil
	}
	if strings.Contains(source, "--filter ") || strings.Count(source, legacyDriveArchiveIncludes) != 1 {
		return "", errors.New("Unexpected Drive archive upload filters")
	}
	source = strings.ReplaceAll(source, ` --include '`+VersionLedgerName+`'`, "")
	exclude := `--exclude '` + VersionLedgerName + `' `
	if strings.Count(source, exclude) > 1 {
		return "", errors.New("Unexpected duplicate Drive metadata exclusion")
	}
	source = strings.ReplaceAll(source, exclude, "")
	updated, err := replaceOnce(source, legacyDriveArchiveIncludes, DriveArchiveFilters)
	if err != nil || strings.Contains(updated, "--include ") || strings.Contains(updated, "--exclude ") {
		return "", errors.New("Unexpected additional Drive archive upload filters")
	}
	return updated, nil
}

func PatchCleanArchiveNames(source, category string) (string, error) {
	if !strings.Contains(source, DailyLimitMarker) {
		return "", errors.New("Install daily versions before clean archive names")
	}
	if category == "drive" {
		updated, err := PatchDriveArchiveOnly(source)
		if err != nil {
			return "", err
		}
		if strings.Contains(updated, CleanNamesMarker) {
			return updated, nil
		}
		return replaceOnce(updated, DailyLimitMarker, DailyLimitMarker+"\n"+CleanNamesMarker)
	}
	if strings.Contains(source, CleanNamesMarker) {
		if category != "drive" && !strings.Contains(source, " versions finalize "+category+" ") {
			return "", errors.New("Incomplete clean-name script")
		}
		return source, nil
	}
	var old, new string
	switch category {
	case "site", "database":
		old = BinaryPath + ` versions commit ` + category + ` "$final" || return 1`
		new = `final=$(` + BinaryPath + ` versions finalize ` + category + ` "$final") || return 1`
	case "panel":
		old = `"$MANAGER" versions commit panel "$PUBLISHED_FILE"`
		new = `PUBLISHED_FILE=$("$MANAGER" versions finalize panel "$PUBLISHED_FILE")`
	default:
		return "", errors.New("Invalid clean-name script category")
	}
	updated, err := replaceOnce(source, old, new)
	if err != nil {
		return "", err
	}
	return replaceOnce(updated, DailyLimitMarker, DailyLimitMarker+"\n"+CleanNamesMarker)
}

func (c *Controller) nameRemoteListing(cfg Settings) (map[string]nameRemoteFile, error) {
	raw, err := c.Run([]string{"rclone", "lsjson", cfg.DriveRemote + ":" + cfg.DriveFolder, "--recursive", "--files-only", "--hash", "--config", c.RcloneConfig}, nil)
	if err != nil {
		return nil, errors.New("Cannot inventory Drive; no rename performed")
	}
	var files []nameRemoteFile
	if json.Unmarshal(raw, &files) != nil {
		return nil, errors.New("Invalid Drive inventory")
	}
	result := map[string]nameRemoteFile{}
	for _, f := range files {
		if f.IsDir {
			continue
		}
		if _, duplicate := result[f.Path]; duplicate {
			return nil, errors.New("Duplicate Drive paths; resolve before renaming")
		}
		result[f.Path] = f
	}
	return result, nil
}

func nameRelativeVersion(relative string) (ArchiveVersion, bool) {
	parts := strings.SplitN(relative, "/", 2)
	if len(parts) != 2 || !ValidDay(parts[0]) {
		return ArchiveVersion{}, false
	}
	v, _, ok := versionGroup(parts[1])
	return v, ok && v.Day == parts[0]
}

func (c *Controller) cleanArchiveNamesPlan() (nameMigrationPlan, error) {
	p := nameMigrationPlan{Ledgers: map[string]VersionLedger{}}
	cfg, _, err := c.Active()
	if err != nil {
		return p, err
	}
	p.Config = cfg
	layout, err := c.Layout()
	if err != nil {
		return p, err
	}
	remote, err := c.nameRemoteListing(cfg)
	if err != nil {
		return p, err
	}
	p.Remote = remote
	entries := map[string]*nameRename{}
	reserved := map[string]bool{}
	for _, cat := range Categories {
		archives, err := layout.Archives(cat, false)
		if err != nil {
			return p, err
		}
		for _, a := range archives {
			if !within(a.Path, cfg.BackupRoot) {
				continue
			}
			relative, err := filepath.Rel(cfg.BackupRoot, a.Path)
			if err != nil {
				return p, err
			}
			relative = filepath.ToSlash(relative)
			reserved[relative] = true
			v, ok := nameRelativeVersion(relative)
			if !ok {
				continue
			} // Never classify native/legacy files by their run hour.
			info, err := layout.safeArchive(a.Path)
			if err != nil {
				return p, err
			}
			sum, err := Digest(a.Path)
			if err != nil {
				return p, err
			}
			entries[relative] = &nameRename{Source: relative, Category: v.Category, Day: v.Day, Origin: v.Origin, Local: true, Size: info.Size(), MtimeNS: info.ModTime().UnixNano(), Inode: inode(info), Device: device(info), Hash: sum}
		}
	}
	for relative, f := range remote {
		reserved[relative] = true
		v, ok := nameRelativeVersion(relative)
		if !ok {
			continue
		}
		if f.ID == "" || f.Size <= 0 || len(f.Hashes) == 0 {
			return p, errors.New("Drive archive lacks identity/checksum; refuse unsafe rename")
		}
		if entries[relative] == nil {
			entries[relative] = &nameRename{Source: relative, Category: v.Category, Day: v.Day, Origin: v.Origin}
		}
		file := f
		entries[relative].Remote = &file
	}
	sources := make([]string, 0, len(entries))
	for relative := range entries {
		sources = append(sources, relative)
	}
	sort.Strings(sources)
	for _, source := range sources {
		entry := entries[source]
		for ordinal := 1; ordinal <= 100000; ordinal++ {
			name, err := CleanVersionName(entry.Category, filepath.Base(source), ordinal)
			if err != nil {
				return p, err
			}
			candidate := filepath.ToSlash(filepath.Join(filepath.Dir(source), name))
			if reserved[candidate] {
				continue
			}
			localPath := filepath.Join(cfg.BackupRoot, filepath.FromSlash(candidate))
			if err := SafeParents(localPath); err != nil {
				return p, err
			}
			if _, err := os.Lstat(localPath); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return p, err
			}
			entry.Destination = candidate
			reserved[candidate] = true
			break
		}
		if entry.Destination == "" {
			return p, errors.New("Cannot allocate non-overwriting archive name")
		}
		p.Entries = append(p.Entries, entry)
		dayRoot := filepath.Join(cfg.BackupRoot, entry.Day)
		if info, err := os.Stat(dayRoot); err == nil && info.IsDir() {
			ledger, present := p.Ledgers[entry.Day]
			if !present {
				ledger, err = layout.readVersionLedger(entry.Day)
				if err != nil {
					return p, err
				}
			}
			ledger.Origins[strings.TrimPrefix(entry.Source, entry.Day+"/")] = entry.Origin
			ledger.Origins[strings.TrimPrefix(entry.Destination, entry.Day+"/")] = entry.Origin
			p.Ledgers[entry.Day] = ledger
		}
	}
	for _, item := range []struct{ name, category string }{{"backup-site.sh", "site"}, {"backup-database.sh", "database"}, {"backup-panel.sh", "panel"}, {"auto_backup.sh", "drive"}} {
		path := filepath.Join(cfg.ScriptsDir, item.name)
		before, err := readRegular(path)
		if err != nil {
			return p, err
		}
		after, err := PatchCleanArchiveNames(string(before), item.category)
		if err != nil {
			return p, err
		}
		if after != string(before) {
			p.Scripts = append(p.Scripts, scriptChange{Source: path, Target: path, Original: before, Updated: []byte(after)})
		}
	}
	return p, nil
}

func sameRemoteNameFile(a, b nameRemoteFile) bool {
	if a.ID == "" || a.ID != b.ID || a.Size != b.Size || a.ModTime != b.ModTime {
		return false
	}
	for kind, hash := range a.Hashes {
		if hash == "" || b.Hashes[kind] != hash {
			return false
		}
	}
	return len(a.Hashes) > 0
}

func (c *Controller) moveNameRemote(cfg Settings, from, to string) error {
	target := cfg.DriveRemote + ":" + cfg.DriveFolder + "/"
	_, err := c.Run([]string{"rclone", "moveto", target + from, target + to, "--ignore-existing", "--retries", "1", "--config", c.RcloneConfig}, nil)
	return err
}

// Rename only explicit managed tags. No backup, rotation, prune or upload of
// archive contents is performed. Recovery retains both scripts and rename maps.
func (c *Controller) MigrateCleanArchiveNames(apply bool, out io.Writer) (resultErr error) {
	if apply {
		locks, err := acquireAll(c.Locks)
		if err != nil {
			return err
		}
		defer releaseAll(locks)
	}
	plan, err := c.cleanArchiveNamesPlan()
	if err != nil {
		return err
	}
	localCount, remoteCount := 0, 0
	for _, entry := range plan.Entries {
		if entry.Local {
			localCount++
		}
		if entry.Remote != nil {
			remoteCount++
		}
	}
	fmt.Fprintf(out, "CLEAN_NAMES_PREVIEW local=%d drive=%d scripts=%d\n", localCount, remoteCount, len(plan.Scripts))
	if !apply {
		for _, entry := range plan.Entries {
			fmt.Fprintf(out, "RENAME %s -> %s\n", entry.Source, entry.Destination)
		}
		return nil
	}
	if len(plan.Entries) == 0 && len(plan.Scripts) == 0 {
		fmt.Fprintln(out, "Clean archive names already installed.")
		return nil
	}
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return err
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "before-clean-names-")
	if err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(plan.Entries, "", "  ")
	if err = AtomicWrite(filepath.Join(recovery, "renames.json"), raw, 0600); err != nil {
		return err
	}
	fmt.Fprintf(out, "RECOVERY %s\n", recovery)
	layout, err := c.Layout()
	if err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "clean-names-syntax-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	for _, script := range plan.Scripts {
		if err = AtomicWrite(filepath.Join(recovery, filepath.Base(script.Source)), script.Original, 0600); err != nil {
			return err
		}
		candidate := filepath.Join(temp, filepath.Base(script.Source))
		if err = AtomicWrite(candidate, script.Updated, 0600); err != nil {
			return err
		}
		if _, err = c.Run([]string{"/bin/bash", "-n", candidate}, nil); err != nil {
			return errors.New("Clean-name script syntax rejected")
		}
	}
	// Refuse external changes before the first write.
	currentRemote, err := c.nameRemoteListing(plan.Config)
	if err != nil {
		return err
	}
	for _, entry := range plan.Entries {
		if _, exists := currentRemote[entry.Destination]; exists {
			return errors.New("Drive destination appeared; no archive overwritten")
		}
		if entry.Remote != nil && !sameRemoteNameFile(*entry.Remote, currentRemote[entry.Source]) {
			return errors.New("Drive archive changed since inventory")
		}
		if entry.Local {
			path := filepath.Join(plan.Config.BackupRoot, filepath.FromSlash(entry.Source))
			info, err := layout.safeArchive(path)
			if err != nil || info.Size() != entry.Size || info.ModTime().UnixNano() != entry.MtimeNS || inode(info) != entry.Inode || device(info) != entry.Device {
				return errors.New("Local archive changed since inventory")
			}
			hash, err := Digest(path)
			if err != nil || hash != entry.Hash {
				return errors.New("Local archive checksum changed")
			}
		}
	}
	movedLocal := []*nameRename{}
	attemptedRemote := []*nameRename{}
	installed := []scriptChange{}
	defer func() {
		if resultErr == nil {
			return
		}
		cause := resultErr
		failed := false
		for i := len(installed) - 1; i >= 0; i-- {
			s := installed[i]
			if c.Write(s.Source, s.Original, 0700) != nil {
				failed = true
			}
		}
		remote, err := c.nameRemoteListing(plan.Config)
		if err != nil && len(attemptedRemote) > 0 {
			failed = true
		}
		for i := len(attemptedRemote) - 1; i >= 0 && err == nil; i-- {
			e := attemptedRemote[i]
			if sameRemoteNameFile(*e.Remote, remote[e.Source]) {
				continue
			}
			if _, occupied := remote[e.Source]; occupied || !sameRemoteNameFile(*e.Remote, remote[e.Destination]) {
				failed = true
				continue
			}
			if c.moveNameRemote(plan.Config, e.Destination, e.Source) != nil {
				failed = true
			}
		}
		if len(attemptedRemote) > 0 {
			verified, verifyErr := c.nameRemoteListing(plan.Config)
			if verifyErr != nil {
				failed = true
			} else {
				for _, e := range attemptedRemote {
					if !sameRemoteNameFile(*e.Remote, verified[e.Source]) {
						failed = true
					}
					if _, remains := verified[e.Destination]; remains {
						failed = true
					}
				}
			}
		}
		for i := len(movedLocal) - 1; i >= 0; i-- {
			e := movedLocal[i]
			source := filepath.Join(plan.Config.BackupRoot, filepath.FromSlash(e.Source))
			destination := filepath.Join(plan.Config.BackupRoot, filepath.FromSlash(e.Destination))
			info, err := layout.safeArchive(destination)
			if err != nil || inode(info) != e.Inode || device(info) != e.Device || info.Size() != e.Size || info.ModTime().UnixNano() != e.MtimeNS {
				failed = true
				continue
			}
			if renameArchiveExclusive(destination, source) != nil {
				failed = true
			}
		}
		if failed {
			resultErr = fmt.Errorf("%w; rename rollback needs review; originals/checksums at %s", cause, recovery)
		} else {
			resultErr = fmt.Errorf("%w; archive names and scripts restored; recovery=%s", cause, recovery)
		}
	}()
	for day, ledger := range plan.Ledgers {
		ledgerPath, err := layout.versionLedgerPath(day)
		if err != nil {
			return err
		}
		before, err := readRegular(ledgerPath)
		if err == nil {
			if err = AtomicWrite(filepath.Join(recovery, day+".ledger.before.json"), before, 0600); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err = layout.writeVersionLedger(day, ledger); err != nil {
			return err
		}
	}
	for _, entry := range plan.Entries {
		if !entry.Local {
			continue
		}
		source := filepath.Join(plan.Config.BackupRoot, filepath.FromSlash(entry.Source))
		destination := filepath.Join(plan.Config.BackupRoot, filepath.FromSlash(entry.Destination))
		if err = layout.mark(source); err != nil {
			return err
		}
		if err = renameArchiveExclusive(source, destination); err != nil {
			return err
		}
		movedLocal = append(movedLocal, entry)
		sum, err := Digest(destination)
		if err != nil || sum != entry.Hash {
			return errors.New("Renamed local archive failed checksum verification")
		}
		fmt.Fprintf(out, "RENAMED_LOCAL %s\n", entry.Destination)
	}
	for _, entry := range plan.Entries {
		if entry.Remote == nil {
			continue
		}
		attemptedRemote = append(attemptedRemote, entry)
		if err = c.moveNameRemote(plan.Config, entry.Source, entry.Destination); err != nil {
			return errors.New("Drive rename did not complete")
		}
		fmt.Fprintf(out, "RENAMED_DRIVE %s\n", entry.Destination)
	}
	after, err := c.nameRemoteListing(plan.Config)
	if err != nil {
		return err
	}
	for _, entry := range plan.Entries {
		if entry.Remote == nil {
			continue
		}
		if _, remains := after[entry.Source]; remains || !sameRemoteNameFile(*entry.Remote, after[entry.Destination]) {
			return errors.New("Drive identity/checksum changed during rename")
		}
	}
	for _, script := range plan.Scripts {
		current, err := readRegular(script.Source)
		if err != nil || string(current) != string(script.Original) {
			return errors.New("Backup script changed; refuse overwrite")
		}
		installed = append(installed, script)
		if err = c.Write(script.Source, script.Updated, 0700); err != nil {
			return err
		}
	}
	// Origin ledgers remain on the server; never upload JSON to Drive.
	fmt.Fprintf(out, "CLEAN_NAMES_APPLIED local=%d drive=%d scripts=%d; bytes/checksums preserved; no backup or rotation run; recovery=%s\n", localCount, remoteCount, len(plan.Scripts), recovery)
	return nil
}

type metadataMove struct {
	Day         string `json:"day"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	SHA256      string `json:"sha256"`
	Entries     int    `json:"entries"`
	raw         []byte
	merged      VersionLedger
	info        os.FileInfo
}

func equalVersionLedgers(a, b VersionLedger) bool {
	if a.Format != b.Format || len(a.Origins) != len(b.Origins) {
		return false
	}
	for key, value := range a.Origins {
		if b.Origins[key] != value {
			return false
		}
	}
	return true
}

// Explicit migration: verify every private copy before removing any legacy JSON.
// Managed job locks prevent origin updates while copying and removing files.
func (c *Controller) MigrateVersionMetadata(apply bool, out io.Writer) (resultErr error) {
	if apply {
		locks, err := acquireAll(c.Locks)
		if err != nil {
			return err
		}
		defer releaseAll(locks)
	}
	l, err := c.Layout()
	if err != nil {
		return err
	}
	if _, err = l.versionLedgerPath("2026-01-01"); err != nil {
		return err
	}
	days, err := os.ReadDir(l.Root)
	if err != nil {
		return err
	}
	moves := []metadataMove{}
	for _, entry := range days {
		day := entry.Name()
		if !ValidDay(day) {
			continue
		}
		source := filepath.Join(l.Root, day, VersionLedgerName)
		if err = SafeParents(source); err != nil {
			return err
		}
		info, err := os.Stat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		legacy, err := readVersionLedgerFile(day, source)
		if err != nil {
			return err
		}
		destination, err := l.versionLedgerPath(day)
		if err != nil {
			return err
		}
		central, err := readVersionLedgerFile(day, destination)
		if err != nil {
			return err
		}
		merged, err := mergeVersionLedgers(central, legacy)
		if err != nil {
			return err
		}
		raw, err := readRegular(source)
		if err != nil {
			return err
		}
		moves = append(moves, metadataMove{Day: day, Source: source, Destination: destination, SHA256: hashBytes(raw), Entries: len(legacy.Origins), raw: raw, merged: merged, info: info})
	}
	fmt.Fprintf(out, "METADATA_PREVIEW files=%d store=%s\n", len(moves), l.MetadataDir)
	if !apply {
		for _, m := range moves {
			fmt.Fprintf(out, "MOVE_METADATA %s -> %s entries=%d\n", m.Source, m.Destination, m.Entries)
		}
		return nil
	}
	if len(moves) == 0 {
		fmt.Fprintln(out, "No legacy JSON remains in backup folders.")
		return nil
	}
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return err
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "before-metadata-store-")
	if err != nil {
		return err
	}
	manifest, _ := json.MarshalIndent(moves, "", "  ")
	if err = AtomicWrite(filepath.Join(recovery, "moves.json"), manifest, 0600); err != nil {
		return err
	}
	for _, m := range moves {
		if err = AtomicWrite(filepath.Join(recovery, m.Day+".source.json"), m.raw, 0600); err != nil {
			return err
		}
		before, err := readRegular(m.Destination)
		if err == nil {
			if err = AtomicWrite(filepath.Join(recovery, m.Day+".central.before.json"), before, 0600); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	// Source files are retained until all copied records have been verified.
	for _, m := range moves {
		if err = l.writeVersionLedger(m.Day, m.merged); err != nil {
			return err
		}
		copied, err := readVersionLedgerFile(m.Day, m.Destination)
		if err != nil || !equalVersionLedgers(copied, m.merged) {
			return errors.New("Private metadata verification failed; legacy JSON retained")
		}
	}
	for _, m := range moves {
		current, err := readRegular(m.Source)
		info, statErr := os.Stat(m.Source)
		if err != nil || statErr != nil || !bytes.Equal(current, m.raw) || !os.SameFile(info, m.info) || !info.ModTime().Equal(m.info.ModTime()) {
			return errors.New("Legacy JSON changed; refusing removal")
		}
	}
	removed := []metadataMove{}
	defer func() {
		if resultErr == nil {
			return
		}
		cause := resultErr
		failed := false
		for _, m := range removed {
			if err := copyExclusive(filepath.Join(recovery, m.Day+".source.json"), m.Source); err != nil {
				failed = true
			}
		}
		if failed {
			resultErr = fmt.Errorf("%w; restore needs review: %s", cause, recovery)
		} else {
			resultErr = fmt.Errorf("%w; removed source JSON restored; recovery=%s", cause, recovery)
		}
	}()
	for _, m := range moves {
		if err = SafeParents(m.Source); err != nil {
			return err
		}
		current, err := readRegular(m.Source)
		if err != nil || hashBytes(current) != m.SHA256 {
			return errors.New("Legacy JSON changed before removal")
		}
		if err = os.Remove(m.Source); err != nil {
			return err
		}
		removed = append(removed, m)
		fmt.Fprintf(out, "MOVED_METADATA day=%s entries=%d destination=%s\n", m.Day, m.Entries, m.Destination)
	}
	fmt.Fprintf(out, "METADATA_APPLIED files=%d store=%s recovery=%s; no archives, cron or Drive changed\n", len(moves), l.MetadataDir, recovery)
	return nil
}
