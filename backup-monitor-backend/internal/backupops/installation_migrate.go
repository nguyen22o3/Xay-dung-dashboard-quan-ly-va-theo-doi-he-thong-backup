package backupops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

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
