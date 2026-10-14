package backupops

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCleanupLayout(t *testing.T) *Layout {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Backup")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	return &Layout{
		Root:    root,
		Markers: filepath.Join(t.TempDir(), "markers"),
		Now:     func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.Local) },
	}
}

func TestPruneRemovesOnlyEmptyOldDayDirectories(t *testing.T) {
	l := testCleanupLayout(t)
	old := filepath.Join(l.Root, "2026-09-30")
	for _, dir := range []string{
		filepath.Join(old, "panel", "nested"),
		filepath.Join(old, "database"),
		filepath.Join(l.Root, "2026-10-01", "panel"),
		filepath.Join(l.Root, "2026-09-29", "other"),
		filepath.Join(l.Root, "2026-09-28", "site"),
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	keptFile := filepath.Join(l.Root, "2026-09-28", "site", "keep.txt")
	if err := os.WriteFile(keptFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := l.Prune("panel", false, &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("dry run removed old directory: %v", err)
	}
	if err := l.Prune("panel", true, &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("empty old day remains: %v", err)
	}
	for _, day := range []string{"2026-10-01", "2026-09-29", "2026-09-28"} {
		if _, err := os.Stat(filepath.Join(l.Root, day)); err != nil {
			t.Fatalf("day %s should remain: %v", day, err)
		}
	}
	if data, err := os.ReadFile(keptFile); err != nil || string(data) != "keep" {
		t.Fatalf("nonempty category was changed: %q, %v", data, err)
	}
	if !strings.Contains(output.String(), "Removed empty directory: "+old) {
		t.Fatalf("missing directory removal log: %s", output.String())
	}
}

func TestPruneRemovesExpiredArchiveAndItsEmptyDay(t *testing.T) {
	l := testCleanupLayout(t)
	for day := time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local); !day.After(l.now()); day = day.AddDate(0, 0, 1) {
		name := day.Format("2006-01-02")
		dir := filepath.Join(l.Root, name, "panel")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".zip"), []byte("archive"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Prune("panel", true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(l.Root, "2026-09-30")); !os.IsNotExist(err) {
		t.Fatalf("expired day remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.Root, "2026-10-01", "panel", "2026-10-01.zip")); err != nil {
		t.Fatalf("one of the latest 14 backup days was removed: %v", err)
	}
}
