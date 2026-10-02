package backupops

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditAndLegacyManifestVerificationAreReadOnly(t *testing.T) {
	root := t.TempDir()
	l := &Layout{Root: root, Journal: filepath.Join(root, "journals"), Markers: filepath.Join(root, "markers"), Now: func() time.Time { return time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) }}
	source := filepath.Join(root, "site", "web1.local", "web_web1.local_20261002_050001_site.tar.gz")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("archive fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	moves, err := l.Organize([]string{"site"}, true, &bytes.Buffer{})
	if err != nil || len(moves) != 1 {
		t.Fatal("fixture organize failed", err)
	}
	manifest := filepath.Join(root, "legacy-manifest.json")
	data, _ := json.Marshal(moves) // Same JSON fields as the former Python helper.
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, root)
	verified, err := l.VerifyManifest(manifest)
	if err != nil || verified.Files != 1 || verified.Counts["site"] != 1 {
		t.Fatal("legacy manifest verification failed", err)
	}
	audit, err := l.AuditArchives()
	if err != nil || audit.Files != 1 || audit.Bytes != int64(len("archive fixture")) {
		t.Fatal("audit failed", err)
	}
	if snapshotFiles(t, root) != before {
		t.Fatal("audit or verification changed files")
	}
	if err := os.WriteFile(moves[0].Destination, []byte("corrupt fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.VerifyManifest(manifest); err == nil {
		t.Fatal("corrupted archive was accepted")
	}
}
