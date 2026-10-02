package backupops

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func layoutFixture(t *testing.T) *Layout {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "backup")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	return &Layout{Root: root, Markers: filepath.Join(base, "markers"), Journal: filepath.Join(base, "journal"), Now: func() time.Time { return time.Date(2026, 10, 2, 16, 0, 0, 0, time.Local) }}
}
func createArchive(t *testing.T, root, relative, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestLayoutOrganizePreservesBytesMtimeAndIntradayVersions(t *testing.T) {
	l := layoutFixture(t)
	paths := []string{
		createArchive(t, l.Root, "database/mysql/crontab_backup/sql_web1_local/db_sql_web1_local_20261002_043001_mysql_data.sql.gz", "first"),
		createArchive(t, l.Root, "database/mysql/crontab_backup/sql_web1_local/db_sql_web1_local_20261002_102255_mysql_data.sql.gz", "second"),
		createArchive(t, l.Root, "site/web1.local/web_web1.local_20260919_050001_random.tar.gz", "site"),
		createArchive(t, l.Root, "panel/2026-10-02.zip", "panel"),
	}
	work := createArchive(t, l.Root, "panel/2026-10-02/data/system.sql", "aaPanel working SQL")
	restore := createArchive(t, l.Root, "backup_restore/2026-10-02.zip", "restore")
	sums := map[string]string{}
	modified := map[string]int64{}
	for _, p := range paths {
		sum, err := Digest(p)
		if err != nil {
			t.Fatal(err)
		}
		sums[p] = sum
		info, _ := os.Stat(p)
		modified[p] = info.ModTime().UnixNano()
	}
	var out bytes.Buffer
	preview, err := l.Organize(Categories, false, &out)
	if err != nil || len(preview) != 4 {
		t.Fatalf("preview %d, %v", len(preview), err)
	}
	for _, p := range paths {
		if !exists(p) {
			t.Fatal("preview modified source")
		}
	}
	if exists(l.Journal) || exists(l.Markers) {
		t.Fatal("preview created private state")
	}
	moved, err := l.Organize(Categories, true, &out)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range moved {
		sum, err := Digest(m.Destination)
		if err != nil || sum != sums[m.Source] {
			t.Fatal("bytes changed", err)
		}
		info, _ := os.Stat(m.Destination)
		if info.ModTime().UnixNano() != modified[m.Source] {
			t.Fatal("mtime changed")
		}
		if exists(m.Source) {
			t.Fatal("original name remains")
		}
		if !exists(filepath.Join(l.Markers, hashBytes([]byte(m.Source)))) {
			t.Fatal("rotation marker missing")
		}
	}
	if !exists(work) || !exists(restore) {
		t.Fatal("aaPanel work/restore files touched")
	}
	manifests, _ := filepath.Glob(filepath.Join(l.Journal, "moves-*.json"))
	if len(manifests) != 1 {
		t.Fatal("missing durable manifest")
	}
	plan, err := l.Plan(Categories)
	if err != nil || len(plan) != 0 {
		t.Fatal("already-organized state", err)
	}
}
func TestLayoutCollisionAbortsBeforeMovingAnything(t *testing.T) {
	l := layoutFixture(t)
	relative := "site/web1.local/web_web1.local_20261002_050001_site.tar.gz"
	source := createArchive(t, l.Root, relative, "original")
	dest := createArchive(t, l.Root, "2026-10-02/"+relative, "existing")
	if _, err := l.Organize([]string{"site"}, true, &bytes.Buffer{}); err == nil {
		t.Fatal("expected collision error")
	}
	original, _ := os.ReadFile(source)
	existing, _ := os.ReadFile(dest)
	if string(original) != "original" || string(existing) != "existing" {
		t.Fatal("collision overwrote data")
	}
}
func TestLayoutPanelVersionsPreserved(t *testing.T) {
	l := layoutFixture(t)
	old := createArchive(t, l.Root, "2026-10-02/panel/2026-10-02.zip", "first")
	createArchive(t, l.Root, "panel/2026-10-02.zip", "second")
	moved, err := l.Organize([]string{"panel"}, true, &bytes.Buffer{})
	if err != nil || len(moved) != 1 {
		t.Fatal(err)
	}
	if moved[0].Destination == old {
		t.Fatal("overwritten first version")
	}
	first, _ := os.ReadFile(old)
	second, _ := os.ReadFile(moved[0].Destination)
	if string(first) != "first" || string(second) != "second" {
		t.Fatal("version bytes changed")
	}
}
func TestLayoutRetentionKeepsFourteenDatesAndAllVersions(t *testing.T) {
	l := layoutFixture(t)
	today := l.now()
	for offset := 0; offset < 15; offset++ {
		day := today.AddDate(0, 0, -offset)
		for _, clock := range []string{"043001", "102255"} {
			createArchive(t, l.Root, fmt.Sprintf("%s/database/db_sql_web1_local_%s_%s_mysql_data.sql.gz", day.Format("2006-01-02"), day.Format("20060102"), clock), "archive")
		}
	}
	future := createArchive(t, l.Root, "2026-10-03/database/db_sql_web1_local_20261003_043001_mysql_data.sql.gz", "future")
	expired, err := l.Expired("database")
	if err != nil || len(expired) != 2 {
		t.Fatalf("expired %d %v", len(expired), err)
	}
	if err = l.Prune("database", false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range expired {
		if !exists(p) {
			t.Fatal("preview deleted archive")
		}
	}
	if err = l.Prune("database", true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	archives, err := l.Archives("database", false)
	if err != nil || len(archives) != 29 || !exists(future) {
		t.Fatalf("retention: %d %v", len(archives), err)
	}
}
func TestLayoutInvalidDatesAndSymlinks(t *testing.T) {
	for _, name := range []string{"db_sql_web1_local_20260230_043001_mysql_data.sql.gz", "db_sql_web1_local_20261002_246001_mysql_data.sql.gz"} {
		if ArchiveDay("database", name) != "" {
			t.Fatal("invalid date/time accepted")
		}
	}
	if ArchiveDay("panel", "system.sql") != "" {
		t.Fatal("working SQL accepted")
	}
	l := layoutFixture(t)
	createArchive(t, l.Root, "site/web1.local/web_web1.local_20261002_050001_site.tar.gz", "source")
	if err := os.Symlink(filepath.Dir(l.Root), filepath.Join(l.Root, "2026-10-02")); err != nil {
		t.Skip("symlink privileges unavailable")
	}
	if _, err := l.Plan([]string{"site"}); err == nil {
		t.Fatal("symlink destination accepted")
	}
}
func TestLayoutCollectsNativeStagingAfterRootChanges(t *testing.T) {
	l := layoutFixture(t)
	legacy := filepath.Join(filepath.Dir(l.Root), "legacy")
	if err := os.Mkdir(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	l.LegacyRoot = legacy
	source := createArchive(t, legacy, "panel/2026-10-02.zip", "native panel archive")
	moved, err := l.Organize([]string{"panel"}, true, &bytes.Buffer{})
	if err != nil || len(moved) != 1 || exists(source) {
		t.Fatal("legacy collection", err)
	}
}
