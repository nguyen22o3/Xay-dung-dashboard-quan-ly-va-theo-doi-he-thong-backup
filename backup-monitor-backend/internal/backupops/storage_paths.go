package backupops

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

// Managed backup operations scan day directories and legacy category roots,
// not arbitrary top-level folders. Keep auxiliary files outside those namespaces.
// Reserve even malformed date-shaped names: shell sync uses the same broad glob.
var backupDayNamespace = regexp.MustCompile(`^20..-..-..$`)

func validateAuxiliaryPath(root, dir string) error {
	if within(root, dir) {
		return errors.New("Thư mục script/log không được trùng hoặc chứa nơi lưu backup; chọn thư mục con riêng như Scripts hoặc Logs")
	}
	if !within(dir, root) {
		return nil
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	if backupDayNamespace.MatchString(first) || validCategory(first) {
		return errors.New("Thư mục script/log không được nằm trong thư mục backup theo ngày hoặc site/database/panel; chọn thư mục con riêng như Scripts hoặc Logs")
	}
	return nil
}

func validateAuxiliaryLocations(cfg Settings) error {
	roots := []string{cfg.BackupRoot}
	dirs := []string{cfg.ScriptsDir, cfg.LogsDir}
	for _, loc := range cfg.CronLocations {
		// Validate the chosen parents, not only their effective cron-ID children:
		// using the backup root itself as a scripts/logs parent is unsafe.
		dirs = append(dirs, loc.ScriptsDir, loc.LogsDir)
		if loc.Options != nil {
			roots = append(roots, loc.Options.BackupRoot)
		}
	}
	for _, root := range roots {
		for _, dir := range dirs {
			if err := validateAuxiliaryPath(root, dir); err != nil {
				return err
			}
		}
	}
	return nil
}
