package backupops

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixture struct {
	c             *Controller
	root, archive string
	old           Settings
	cron          string
	cronWrites    int
	rcloneWrites  int
	failCron      bool
}

func configurationFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	c := NewController()
	old := Settings{BackupRoot: filepath.Join(root, "backup"), ScriptsDir: filepath.Join(root, "scripts"), LogsDir: filepath.Join(root, "logs"), DriveRemote: "gdrive", DriveFolder: "Backup", DriveHistoryLog: filepath.Join(root, "logs", "aapanel_backup.log")}
	for _, path := range []string{old.BackupRoot, old.ScriptsDir, old.LogsDir} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c.ConfigPath = filepath.Join(root, "config.json")
	c.Defaults = old
	c.RcloneConfig = filepath.Join(root, "rclone.conf")
	c.RecoveryRoot = filepath.Join(root, "recovery")
	c.Binary = filepath.Join(root, "backup-manager")
	c.Locks = []string{filepath.Join(root, "test.lock")}
	c.Roots = map[string][]string{"backup": {root}, "scripts": {root}, "logs": {root}}
	c.RestartMonitor = nil
	if err := os.WriteFile(c.RcloneConfig, []byte("[gdrive]\ntype = drive\ntoken = super-secret-must-not-leak\n[other]\ntype = s3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Binary, []byte("fixture only"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range ScriptNames {
		if err := os.WriteFile(filepath.Join(old.ScriptsDir, name), []byte("#!/bin/bash\n"+LayoutMarker+"\necho '"+old.BackupRoot+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range append(append([]string{}, LogNames...), "aapanel_backup.log") {
		if err := os.WriteFile(filepath.Join(old.LogsDir, name), []byte("original log\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive := createArchive(t, old.BackupRoot, "2026-10-02/site/web1.local/test.tar.gz", "original archive")
	f := &fixture{c: c, root: root, archive: archive, old: old, cron: "0 5 * * * /bin/bash " + filepath.Join(old.ScriptsDir, "backup-site.sh") + " >> " + filepath.Join(old.LogsDir, "backup-site.log") + "\n25 22 * * * /www/server/cron/native-aapanel\n"}
	c.Run = func(args []string, input []byte) ([]byte, error) {
		if len(args) == 2 && args[0] == "crontab" && args[1] == "-l" {
			return []byte(f.cron), nil
		}
		if len(args) == 2 && args[0] == "crontab" && args[1] == "-" {
			f.cronWrites++
			if f.failCron {
				return nil, errors.New("simulated cron failure")
			}
			f.cron = string(input)
			return nil, nil
		}
		if args[0] == "/bin/bash" && args[1] == "-n" {
			return nil, nil
		}
		if args[0] == "/usr/bin/rclone" {
			if args[1] == "mkdir" {
				f.rcloneWrites++
				return nil, nil
			}
			return []byte("Folder/\n"), nil
		}
		return nil, errors.New("unexpected fixture command")
	}
	return f
}
func (f *fixture) request(t *testing.T, new Settings) Request {
	t.Helper()
	_, version, err := f.c.Active()
	if err != nil {
		t.Fatal(err)
	}
	return Request{Operation: "preview", Config: &new, ExpectedVersion: version}
}
func snapshotFiles(t *testing.T, root string) string {
	t.Helper()
	var result []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		result = append(result, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(result, "\n")
}
func TestConfigurationPreviewIsReadOnlyAndFingerprintDetectsChanges(t *testing.T) {
	f := configurationFixture(t)
	new := f.old
	new.BackupRoot = filepath.Join(f.root, "new-backup")
	request := f.request(t, new)
	before := snapshotFiles(t, f.root)
	p, err := f.c.Proposal(request)
	if err != nil {
		t.Fatal(err)
	}
	if p.Summary.FilesToMove != 1 || p.Summary.DaysToMove != 1 {
		t.Fatal(p.Summary)
	}
	if snapshotFiles(t, f.root) != before || f.cronWrites != 0 || f.rcloneWrites != 0 {
		t.Fatal("preview mutated state")
	}
	if err = os.WriteFile(f.archive, []byte("changed archive"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := f.c.Proposal(request)
	if err != nil || changed.Summary.Token == p.Summary.Token {
		t.Fatal("token not tied to archive content", err)
	}
}
func TestConfigurationApplyPreservesDataAndUnrelatedCron(t *testing.T) {
	f := configurationFixture(t)
	f.cron += "# backup-monitor-paused 30 4 * * * /bin/bash " + filepath.Join(f.old.ScriptsDir, "backup-database.sh") + " --flag\n"
	new := f.old
	new.BackupRoot = filepath.Join(f.root, "new-backup")
	new.ScriptsDir = filepath.Join(f.root, "new-scripts")
	new.LogsDir = filepath.Join(f.root, "new-logs")
	info, _ := os.Stat(f.archive)
	request := f.request(t, new)
	p, err := f.c.Proposal(request)
	if err != nil {
		t.Fatal(err)
	}
	request.PreviewToken = p.Summary.Token
	state, err := f.c.Apply(request)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Applied || state.LocalRetentionDays != 14 || state.DriveRetentionDays != 14 {
		t.Fatal(state)
	}
	moved := filepath.Join(new.BackupRoot, "2026-10-02", "site", "web1.local", "test.tar.gz")
	data, err := os.ReadFile(moved)
	if err != nil || string(data) != "original archive" || exists(f.archive) {
		t.Fatal("archive transfer failed", err)
	}
	after, _ := os.Stat(moved)
	if after.ModTime().UnixNano() != info.ModTime().UnixNano() {
		t.Fatal("mtime changed")
	}
	if !strings.Contains(f.cron, "/www/server/cron/native-aapanel") || !strings.Contains(f.cron, filepath.Join(new.ScriptsDir, "backup-site.sh")) {
		t.Fatal("wrong crontab")
	}
	if !strings.Contains(f.cron, "# backup-monitor-paused 30 4 * * * /bin/bash "+filepath.Join(new.ScriptsDir, "backup-database.sh")+" --flag") {
		t.Fatal("paused cron lost marker or kept old script path")
	}
	log, _ := os.ReadFile(filepath.Join(new.LogsDir, "backup-site.log"))
	if string(log) != "original log\n" || !exists(filepath.Join(f.old.ScriptsDir, "backup-site.sh")) {
		t.Fatal("logs or originals missing")
	}
	active, _, err := f.c.Active()
	if err != nil || active.BackupRoot != new.BackupRoot || active.DriveHistoryLog != filepath.Join(new.LogsDir, "aapanel_backup.log") {
		t.Fatal("config commit failed", err)
	}
}
func TestConfigurationFailedCommitRollsBack(t *testing.T) {
	f := configurationFixture(t)
	new := f.old
	new.BackupRoot = filepath.Join(f.root, "new-backup")
	new.ScriptsDir = filepath.Join(f.root, "new-scripts")
	new.LogsDir = filepath.Join(f.root, "new-logs")
	request := f.request(t, new)
	p, err := f.c.Proposal(request)
	if err != nil {
		t.Fatal(err)
	}
	request.PreviewToken = p.Summary.Token
	originalCron := f.cron
	f.c.Write = func(path string, data []byte, mode os.FileMode) error {
		if path == f.c.ConfigPath {
			return errors.New("simulated disk failure")
		}
		return AtomicWrite(path, data, mode)
	}
	if _, err = f.c.Apply(request); err == nil {
		t.Fatal("expected failure")
	}
	data, _ := os.ReadFile(f.archive)
	if string(data) != "original archive" || f.cron != originalCron || exists(f.c.ConfigPath) || exists(filepath.Join(new.ScriptsDir, "backup-site.sh")) || exists(filepath.Join(new.LogsDir, "backup-site.log")) {
		t.Fatal("rollback failed")
	}
}
func TestConfigurationInvalidPathsCollisionsAndUnknownFields(t *testing.T) {
	f := configurationFixture(t)
	for _, path := range []string{filepath.Join(f.root, "bad name"), filepath.Join(f.root, "bad;echo"), f.root + string(filepath.Separator) + ".." + string(filepath.Separator) + "escape", "/etc"} {
		if f.c.LocalPath(path, "backup") == nil {
			t.Fatal("invalid path accepted", path)
		}
	}
	dest := filepath.Join(f.root, "new-scripts")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "backup-site.sh"), []byte("do not overwrite"), 0600); err != nil {
		t.Fatal(err)
	}
	new := f.old
	new.ScriptsDir = dest
	if _, err := f.c.Proposal(f.request(t, new)); err == nil {
		t.Fatal("collision accepted")
	}
	if _, err := DecodeRequest(strings.NewReader(`{"operation":"get","arbitraryCommand":"rm"}`)); err == nil {
		t.Fatal("unknown request field accepted")
	}
	if _, err := DecodeRequest(strings.NewReader(`{"operation":"get"} {"operation":"apply"}`)); err == nil {
		t.Fatal("multiple requests accepted")
	}
	remotes, err := f.c.Remotes()
	if err != nil || len(remotes) != 1 || remotes[0] != "gdrive" {
		t.Fatal("remote selection", err)
	}
	state, err := f.c.Get()
	raw, _ := json.Marshal(state)
	if err != nil || bytes.Contains(raw, []byte("super-secret")) {
		t.Fatal("credential leak")
	}
}
func TestConfigurationRejectsStaleVersionTokenAndBusyLocks(t *testing.T) {
	f := configurationFixture(t)
	new := f.old
	new.BackupRoot = filepath.Join(f.root, "new-backup")
	request := f.request(t, new)
	request.PreviewToken = "stale"
	if _, err := f.c.Apply(request); err == nil {
		t.Fatal("stale preview accepted")
	}
	request.ExpectedVersion = "stale"
	if _, err := f.c.Proposal(request); err == nil {
		t.Fatal("stale version accepted")
	}
	request = f.request(t, new)
	p, err := f.c.Proposal(request)
	if err != nil {
		t.Fatal(err)
	}
	request.PreviewToken = p.Summary.Token
	lock, err := acquire(f.c.Locks[0], false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = f.c.Apply(request); err == nil {
		t.Fatal("busy job accepted")
	}
	if !exists(f.archive) || f.cronWrites != 0 {
		t.Fatal("busy job mutated data")
	}
}
func TestConfigurationCreationIsExplicitAndNoOpDoesNotCommit(t *testing.T) {
	f := configurationFixture(t)
	path := filepath.Join(f.root, "new-folder")
	request := Request{Location: "local", Purpose: "backup", Path: path}
	listing, err := f.c.List(request)
	if err != nil || listing.Exists || exists(path) {
		t.Fatal("listing created folder", err)
	}
	if err = f.c.Create(request); err != nil || !exists(path) {
		t.Fatal("explicit creation failed", err)
	}
	current := f.request(t, f.old)
	p, err := f.c.Proposal(current)
	if err != nil {
		t.Fatal(err)
	}
	current.PreviewToken = p.Summary.Token
	state, err := f.c.Apply(current)
	if err != nil || state.Applied || exists(f.c.ConfigPath) || f.cronWrites != 0 {
		t.Fatal("no-op changed config", err)
	}
}
func TestRewriteOnePassWithNestedDrivePaths(t *testing.T) {
	got := Rewrite("rclone copy gdrive:Backup; /root/scripts/backup-site.sh", [][2]string{{"gdrive:Backup", "gdrive:Backup/new"}, {"/root/scripts", "/root/scripts/new"}})
	if got != "rclone copy gdrive:Backup/new; /root/scripts/new/backup-site.sh" {
		t.Fatal(got)
	}
	for _, path := range []string{"../Backup", "Backup//new", "Backup/../new", "/Backup", "Backup/./new", "Backup/new folder"} {
		if DrivePath(path) == nil {
			t.Fatal("unsafe Drive path accepted", path)
		}
	}
}
func TestConfigurationRejectsSymlinks(t *testing.T) {
	f := configurationFixture(t)
	linked := filepath.Join(f.root, "link")
	if err := os.Symlink(f.old.BackupRoot, linked); err != nil {
		t.Skip("symlink privileges unavailable")
	}
	if f.c.LocalPath(linked, "backup") == nil {
		t.Fatal("symlink directory accepted")
	}
}
