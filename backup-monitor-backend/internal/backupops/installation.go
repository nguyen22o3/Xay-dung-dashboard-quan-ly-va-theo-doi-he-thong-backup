package backupops

import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
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
