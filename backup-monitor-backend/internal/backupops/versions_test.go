package backupops

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validVersionFixture(t *testing.T, root, category, entity, day, origin string, n int) string {
	t.Helper()
	nonce := fmt.Sprintf("%016x", n)
	name := fmt.Sprintf("web_%s_%s_120000_%s_%s_site.tar.gz", entity, strings.ReplaceAll(day, "-", ""), origin, nonce)
	parent := filepath.Join(root, day, category, entity)
	if category == "database" {
		name = fmt.Sprintf("db_%s_%s_120000_%s_%s_mysql_data.sql.gz", entity, strings.ReplaceAll(day, "-", ""), origin, nonce)
		parent = filepath.Join(root, day, category, "mysql", "crontab_backup", entity)
	}
	if category == "panel" {
		name = fmt.Sprintf("%s_120000_%s_%s.zip", day, origin, nonce)
		parent = filepath.Join(root, day, category)
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(parent, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if category == "panel" {
		z := zip.NewWriter(f)
		w, err := z.Create("data/system.sql")
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, "CREATE TABLE fixture(value TEXT);")
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		z := gzip.NewWriter(f)
		if category == "site" {
			tw := tar.NewWriter(z)
			content := []byte("fixture website")
			tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0600, Size: int64(len(content))})
			tw.Write(content)
			if err = tw.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			io.WriteString(z, "CREATE DATABASE fixture;")
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 10, 2, 12, 0, 0, n*1000, time.UTC)
	if err = os.Chtimes(p, ts, ts); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDailyVersionsOneCronTwoManualPerTargetAndDay(t *testing.T) {
	for _, cat := range Categories {
		t.Run(cat, func(t *testing.T) {
			root := t.TempDir()
			l := &Layout{Root: root, Markers: filepath.Join(t.TempDir(), "markers")}
			day := "2026-10-02"
			entity := "web1.local"
			if cat == "database" {
				entity = "sql_web1_local"
			}
			if cat == "panel" {
				entity = "aaPanel"
			}
			var output bytes.Buffer
			cron := validVersionFixture(t, root, cat, entity, day, "cron", 1)
			if err := l.CommitVersion(cat, cron, &output); err != nil {
				t.Fatal(err)
			}
			manual1 := validVersionFixture(t, root, cat, entity, day, "manual", 2)
			if err := l.CommitVersion(cat, manual1, &output); err != nil {
				t.Fatal(err)
			}
			manual2 := validVersionFixture(t, root, cat, entity, day, "manual", 3)
			if err := l.CommitVersion(cat, manual2, &output); err != nil {
				t.Fatal(err)
			}
			oldDay := validVersionFixture(t, root, cat, entity, "2026-10-01", "manual", 4)
			other := ""
			if cat != "panel" {
				other = validVersionFixture(t, root, cat, "other", day, "manual", 5)
			}
			legacyName := "web_web1.local_20261002_040000_site.tar.gz"
			if cat == "database" {
				legacyName = "db_sql_web1_local_20261002_040000_mysql_data.sql.gz"
			}
			if cat == "panel" {
				legacyName = "2026-10-02.zip"
			}
			legacy := filepath.Join(filepath.Dir(cron), legacyName)
			os.WriteFile(legacy, []byte("untagged historic backup"), 0600)
			manual3 := validVersionFixture(t, root, cat, entity, day, "manual", 6)
			if err := l.CommitVersion(cat, manual3, &output); err != nil {
				t.Fatal(err)
			}
			if exists(manual1) || !exists(cron) || !exists(manual2) || !exists(manual3) || !exists(oldDay) || !exists(legacy) || (other != "" && !exists(other)) {
				t.Fatal("wrong version rotated")
			}
			cron2 := validVersionFixture(t, root, cat, entity, day, "cron", 7)
			if err := l.CommitVersion(cat, cron2, &output); err != nil {
				t.Fatal(err)
			}
			if exists(cron) || !exists(cron2) || !exists(manual2) || !exists(manual3) {
				t.Fatal("cron/manual pools mixed")
			}
			if !strings.Contains(output.String(), "ROTATED "+manual1) {
				t.Fatal("rotation not logged")
			}
			if !exists(filepath.Join(l.Markers, hashBytes([]byte(manual1)))) {
				t.Fatal("expected deletion marker missing")
			}
			if ArchiveDay(cat, filepath.Base(manual3)) != day {
				t.Fatal("new name broke date retention")
			}
		})
	}
}

func TestDailyVersionsBadArchiveAndSymlinkNeverDeleteOld(t *testing.T) {
	for _, cat := range Categories {
		t.Run(cat, func(t *testing.T) {
			root := t.TempDir()
			l := &Layout{Root: root, Markers: filepath.Join(t.TempDir(), "markers")}
			a := validVersionFixture(t, root, cat, "item", "2026-10-02", "manual", 1)
			b := validVersionFixture(t, root, cat, "item", "2026-10-02", "manual", 2)
			c := validVersionFixture(t, root, cat, "item", "2026-10-02", "manual", 3)
			os.WriteFile(c, []byte("broken compressed archive"), 0600)
			if err := l.CommitVersion(cat, c, io.Discard); err == nil {
				t.Fatal("broken archive accepted")
			}
			if !exists(a) || !exists(b) {
				t.Fatal("failed creation deleted recovery copies")
			}
			os.Remove(c)
			if err := os.Symlink(a, c); err == nil {
				if err = l.CommitVersion(cat, c, io.Discard); err == nil {
					t.Fatal("symlink accepted")
				}
			}
			if !exists(a) || !exists(b) {
				t.Fatal("symlink touched old files")
			}
			if _, ok := ParseVersion(cat, "old-untagged.zip"); ok {
				t.Fatal("guessed origin")
			}
		})
	}
}

func TestDailyVersionsFourteenBackupDatesUnchanged(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := &Layout{Root: root, Markers: filepath.Join(t.TempDir(), "markers"), Now: func() time.Time { return now }}
	for i := 0; i < 16; i++ {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		for n := 1; n <= 2; n++ {
			validVersionFixture(t, root, "panel", "aaPanel", day, "manual", n)
		}
	}
	expired, err := l.Expired("panel")
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 4 {
		t.Fatalf("expected two expired days / four archives, got %d", len(expired))
	}
}

func TestDailyDriveRotationVerifiesCopyAndKeepsLegacyAndOtherTargets(t *testing.T) {
	f := configurationFixture(t)
	day := "2026-10-02"
	a := validVersionFixture(t, f.old.BackupRoot, "site", "web1.local", day, "manual", 2)
	b := validVersionFixture(t, f.old.BackupRoot, "site", "web1.local", day, "manual", 3)
	cron := validVersionFixture(t, f.old.BackupRoot, "site", "web1.local", day, "cron", 4)
	base := filepath.Join(f.old.BackupRoot, day)
	rel := func(p string) string { s, _ := filepath.Rel(base, p); return filepath.ToSlash(s) }
	old := "site/web1.local/web_web1.local_20261002_120000_manual_0000000000000001_site.tar.gz"
	oldCron := "site/web1.local/web_web1.local_20261002_120000_cron_0000000000000000_site.tar.gz"
	remote := []remoteVersionFile{{Path: rel(a)}, {Path: rel(b)}, {Path: rel(cron)}, {Path: old}, {Path: oldCron},
		{Path: "site/web1.local/web_web1.local_20261002_043000_site.tar.gz"},
		{Path: "site/other/web_other_20261002_120000_manual_0000000000000001_site.tar.gz"},
		{Path: "../site/web1.local/web_web1.local_20261002_120000_manual_0000000000000001_site.tar.gz"}}
	raw, _ := json.Marshal(remote)
	var deleted []string
	checked := false
	failCheck := true
	f.c.Run = func(args []string, input []byte) ([]byte, error) {
		if args[0] != "rclone" {
			t.Fatal("unexpected command")
		}
		switch args[1] {
		case "check":
			if failCheck {
				return nil, errors.New("verification failed")
			}
			if !strings.Contains(strings.Join(args, " "), "--one-way") {
				t.Fatal("missing one-way check")
			}
			checked = true
			return nil, nil
		case "lsjson":
			if !checked {
				t.Fatal("listed before verification")
			}
			return raw, nil
		case "deletefile":
			if !checked {
				t.Fatal("deleted before verification")
			}
			deleted = append(deleted, args[2])
			return nil, nil
		default:
			t.Fatal("broad destructive command")
			return nil, nil
		}
	}
	if err := f.c.ReconcileDriveVersions(day, true, io.Discard); err == nil {
		t.Fatal("verification failure ignored")
	}
	if len(deleted) > 0 {
		t.Fatal("deleted after failed upload verification")
	}
	failCheck = false
	if err := f.c.ReconcileDriveVersions(day, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(deleted) > 0 {
		t.Fatal("preview deleted")
	}
	if err := f.c.ReconcileDriveVersions(day, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || !strings.Contains(strings.Join(deleted, "\n"), old) || !strings.Contains(strings.Join(deleted, "\n"), oldCron) {
		t.Fatalf("wrong Drive delete list: %v", deleted)
	}
}

func TestDailyDriveMissingLocalCopyCannotDeleteIndependentCloudCopy(t *testing.T) {
	f := configurationFixture(t)
	day := "2026-10-02"
	validVersionFixture(t, f.old.BackupRoot, "site", "web1.local", day, "manual", 2)
	f.c.Run = func(args []string, _ []byte) ([]byte, error) {
		if args[1] == "check" {
			return nil, nil
		}
		if args[1] == "lsjson" {
			return []byte(`[{"Path":"site/web1.local/web_web1.local_20261002_120000_manual_0000000000000001_site.tar.gz"}]`), nil
		}
		t.Fatal("cloud copy deleted despite incomplete local group")
		return nil, nil
	}
	if err := f.c.ReconcileDriveVersions(day, true, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestDailyVersionsScriptPatchesPreservePublicationValidationAndAreIdempotent(t *testing.T) {
	for _, cat := range []string{"site", "database"} {
		suffix := "site.tar.gz"
		if cat == "database" {
			suffix = "mysql_data.sql.gz"
		}
		source := "#!/bin/bash\n" + LayoutMarker + "\nset -Eeuo pipefail\nbackup_one() {\n" + `    final="$dir/${prefix}${stamp}_` + suffix + "\u0022\n" + `    printf 'CREATED %s DURATION %d.%03d\n' "$final" "$((duration_ms / 1000))" "$((duration_ms % 1000))"` + "\n}\n"
		updated, err := PatchDailyVersions(source, cat)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(updated, "manual") || !strings.Contains(updated, "versions commit "+cat) {
			t.Fatal("missing daily version hook")
		}
		again, err := PatchDailyVersions(updated, cat)
		if err != nil || again != updated {
			t.Fatal("patch not idempotent", err)
		}
		validateGeneratedBash(t, updated)
	}
	source := "#!/bin/bash\n" + LayoutMarker + "\n" + `        if ! rclone copy "$day_dir" "gdrive:Backup/$day" --config /root/.config/rclone/rclone.conf --include '*.tar.gz' --include '*.sql.gz' --include '*.zip'; then
            STATUS="Failed"
        fi` + "\nKEEP_DAYS=14\n"
	updated, err := PatchDailyVersions(source, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated, "elif ! "+BinaryPath+" versions drive") || !strings.Contains(updated, "KEEP_DAYS=14") || strings.Contains(updated, "rclone sync") {
		t.Fatal("unsafe Drive patch")
	}
	validateGeneratedBash(t, updated)
}
