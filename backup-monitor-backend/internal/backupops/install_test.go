package backupops

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func installerFixture(t *testing.T) *fixture {
	t.Helper()
	f := configurationFixture(t)
	required := map[string]int{"backup-site.sh": 1, "backup-database.sh": 1, "auto_backup.sh": 1, "cleanup-panel-backups.sh": 3}
	for name, count := range required {
		content := "#!/bin/bash\n" + LayoutMarker + "\n"
		for i := 0; i < count; i++ {
			content += "/usr/bin/python3 " + filepath.ToSlash(filepath.Join(f.old.ScriptsDir, "daily_backup_layout.py")) + " prune panel\n"
		}
		if err := os.WriteFile(filepath.Join(f.old.ScriptsDir, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestFreshInstallerPreservesDriveRetentionAndGeneratesValidBash(t *testing.T) {
	for _, category := range []string{"site", "database"} {
		variable, root, function := "SITE_BACKUP_ROOT", "/www/backup/site", "rotate_site"
		if category == "database" {
			variable, root, function = "DATABASE_BACKUP_ROOT", "/www/backup/database/mysql/crontab_backup", "rotate_database"
		}
		source := "#!/bin/bash\nset -Eeuo pipefail\nBACKUP_ROOT=${" + variable + ":-" + root + "}\n" + function + "() {\n    :\n}\nbackup_one() {\n    " + function + " \"$dir\" \"$prefix\"\n}\nmain() {\n    local stamp failures=0\n    stamp=$(date '+%Y%m%d_%H%M%S')\n    return \"$failures\"\n}\n"
		updated, err := PatchBackup(source, category)
		if err != nil || strings.Contains(updated, function+"()") || !strings.Contains(updated, BinaryPath+" layout prune "+category+" --delete") || !strings.Contains(updated, "${stamp:0:4}-${stamp:4:2}-${stamp:6:2}") {
			t.Fatal("fresh backup conversion failed", category, err)
		}
		validateGeneratedBash(t, updated)
	}
	retention := "# 4. Giu 14 thu muc ngay co backup gan nhat\necho 'retention unchanged'\n"
	source := "#!/bin/bash\nSTART_TIME=$(date +\"%I:%M:%S %p\")\n# 1. Day toan bo ma nguon Website len Drive\nrclone copy /www/backup/site gdrive:Backup\n" + retention
	updated, err := PatchDrive(source)
	if err != nil || !strings.HasSuffix(updated, retention) || strings.Contains(updated, "rclone sync") || !strings.Contains(updated, BinaryPath+" layout organize --apply") || !strings.Contains(updated, `rclone copy "$day_dir"`) {
		t.Fatal("Drive policy changed during conversion", err)
	}
	validateGeneratedBash(t, updated)
	validateGeneratedBash(t, CleanupScript())
}

func validateGeneratedBash(t *testing.T, source string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return // Real Bash syntax validation is exercised by the Linux test binary.
	}
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/bash", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("generated Bash syntax: %v %s", err, out)
	}
}
func TestInstallerPreviewAndApplyChangeOnlyHelperCalls(t *testing.T) {
	f := installerFixture(t)
	before := snapshotFiles(t, f.root)
	var out bytes.Buffer
	if err := f.c.Install(false, &out); err != nil {
		t.Fatal(err)
	}
	if before != snapshotFiles(t, f.root) {
		t.Fatal("installer preview wrote files")
	}
	originalCron := f.cron
	if err := f.c.Install(true, &out); err != nil {
		t.Fatal(err)
	}
	if f.cron != originalCron || f.cronWrites != 0 || f.rcloneWrites != 0 || !exists(f.archive) {
		t.Fatal("installer changed backup or cron")
	}
	for _, name := range []string{"backup-site.sh", "backup-database.sh", "auto_backup.sh", "cleanup-panel-backups.sh"} {
		content, _ := os.ReadFile(filepath.Join(f.old.ScriptsDir, name))
		if strings.Contains(string(content), "daily_backup_layout.py") || !strings.Contains(string(content), BinaryPath+" layout") {
			t.Fatal("helper not replaced")
		}
	}
	plan, err := f.c.InstallPlan()
	if err != nil || len(plan) != 0 {
		t.Fatal("installer not idempotent", err)
	}
}
func TestInstallerFailureRollsBack(t *testing.T) {
	f := installerFixture(t)
	original, _ := os.ReadFile(filepath.Join(f.old.ScriptsDir, "backup-site.sh"))
	f.c.Write = func(path string, data []byte, mode os.FileMode) error {
		if filepath.Base(path) == "backup-database.sh" {
			return errors.New("simulated install failure")
		}
		return AtomicWrite(path, data, mode)
	}
	if err := f.c.Install(true, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error")
	}
	after, _ := os.ReadFile(filepath.Join(f.old.ScriptsDir, "backup-site.sh"))
	if string(after) != string(original) {
		t.Fatal("installer rollback failed")
	}
}
func TestInstallerUnknownStructureRefusesMutation(t *testing.T) {
	f := installerFixture(t)
	path := filepath.Join(f.old.ScriptsDir, "backup-site.sh")
	if err := os.WriteFile(path, []byte("#!/bin/bash\n"+LayoutMarker+"\npython3 /unexpected/helper.py\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.InstallPlan(); err == nil {
		t.Fatal("unrecognized helper accepted")
	}
}
