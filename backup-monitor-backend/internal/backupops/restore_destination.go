package backupops

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
)

var restoreParentChars = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
var restoreNameChars = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)

// Use POSIX paths even when the API is running on Windows. The destination is
// always on the AlmaLinux host, never on the machine running the browser/API.
func RestoreDestination(parent, name string) (string, error) {
	if len(parent) > 1024 || parent == "/" || !restoreParentChars.MatchString(parent) || path.Clean(parent) != parent || len(name) > 128 || !restoreNameChars.MatchString(name) {
		return "", errors.New("Chọn đường dẫn tuyệt đối trên AlmaLinux và tên thư mục mới (chữ không dấu, số, -, _, .; tối đa 128 ký tự)")
	}
	return path.Join(parent, name), nil
}

func (c *Controller) validateRestoreDestination(cfg Settings, parent, name string) (string, error) {
	dir, err := RestoreDestination(parent, name)
	if err != nil {
		return "", err
	}
	if err = c.LocalPath(parent, "backup"); err != nil {
		return "", err
	}
	// Every ancestor must be a real, root-owned directory that another user
	// cannot replace while the privileged download/extractor is running.
	for p := parent; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil || !info.IsDir() || !privateRootOwned(info) {
			return "", errors.New("Thư mục cha phải tồn tại, do root sở hữu và không cho người khác ghi; không dùng liên kết tượng trưng")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	protected := []string{c.RecoveryRoot, c.VersionMetadataDir, filepath.Dir(c.ConfigPath), filepath.Dir(c.RcloneConfig), filepath.Dir(c.Binary), "/etc", "/usr", "/boot", "/bin", "/sbin", "/lib", "/lib64", "/var/lib", "/www/wwwroot", "/www/server/data", "/www/server/panel"}
	settings := []Settings{cfg}
	for key, loc := range cfg.CronLocations {
		local, e := CronSettings(cfg, loc.ID, key)
		if e != nil {
			return "", e
		}
		settings = append(settings, local)
	}
	for _, local := range settings {
		for _, id := range []string{"backup-site", "backup-database", "backup-panel"} {
			protected = append(protected, effectiveSource(local, id).Path)
		}
		// A separate top-level output folder inside a backup root is allowed,
		// but never a day/category namespace scanned by sync/retention jobs.
		if validateAuxiliaryPath(local.BackupRoot, dir) != nil {
			return "", errors.New("Chọn thư mục riêng ngoài các bản backup theo ngày hoặc site/database/panel")
		}
	}
	for _, p := range protected {
		if p != "" && p != "." && (within(dir, p) || within(p, dir)) {
			return "", errors.New("Không tải/giải nén vào thư mục dữ liệu đang chạy hoặc thư mục quản lý hệ thống")
		}
	}
	if _, err = os.Lstat(dir); !os.IsNotExist(err) {
		return "", errors.New("Tên thư mục đích đã tồn tại hoặc không kiểm tra được; chọn tên khác, không ghi đè")
	}
	return dir, nil
}

func (c *Controller) checkRestoreDestination(cfg Settings, plan RestorePlan) error {
	if plan.Parent == "" && plan.Name == "" {
		// Backward-compatible legacy plans use their token's private data dir.
		if _, err := os.Lstat(plan.Directory); !os.IsNotExist(err) {
			return errors.New("Thư mục đích đã tồn tại; hãy kiểm tra lại")
		}
		return SafeParents(plan.Directory)
	}
	dir, err := c.validateRestoreDestination(cfg, plan.Parent, plan.Name)
	if err != nil {
		return err
	}
	if dir != plan.Directory {
		return errors.New("Thư mục đích không khớp kế hoạch đã xác nhận")
	}
	return nil
}
