package backupops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type nameRemoteFile struct {
	Path    string
	Size    int64
	IsDir   bool
	ID      string
	ModTime string
	Hashes  map[string]string
}
type nameRename struct {
	Source      string          `json:"source"`
	Destination string          `json:"destination"`
	Category    string          `json:"category"`
	Day         string          `json:"day"`
	Origin      string          `json:"origin"`
	Local       bool            `json:"local"`
	Size        int64           `json:"size"`
	MtimeNS     int64           `json:"mtime_ns"`
	Inode       uint64          `json:"inode"`
	Device      uint64          `json:"device"`
	Hash        string          `json:"sha256"`
	Remote      *nameRemoteFile `json:"remote,omitempty"`
}
type nameMigrationPlan struct {
	Config  Settings
	Entries []*nameRename
	Remote  map[string]nameRemoteFile
	Scripts []scriptChange
	Ledgers map[string]VersionLedger
}

// Keep origin metadata locally; only compressed archives belong on Drive.
func PatchDriveArchiveOnly(source string) (string, error) {
	filters := `--include '*.tar.gz' --include '*.sql.gz' --include '*.zip'`
	if strings.Count(source, filters) != 1 {
		return "", errors.New("Unexpected Drive archive upload filters")
	}
	source = strings.ReplaceAll(source, ` --include '`+VersionLedgerName+`'`, "")
	exclude := `--exclude '` + VersionLedgerName + `' `
	if strings.Contains(source, exclude+filters) {
		return source, nil
	}
	return replaceOnce(source, filters, exclude+filters)
}

func PatchCleanArchiveNames(source, category string) (string, error) {
	if !strings.Contains(source, DailyLimitMarker) {
		return "", errors.New("Install daily versions before clean archive names")
	}
	if category == "drive" {
		updated, err := PatchDriveArchiveOnly(source)
		if err != nil {
			return "", err
		}
		if strings.Contains(updated, CleanNamesMarker) {
			return updated, nil
		}
		return replaceOnce(updated, DailyLimitMarker, DailyLimitMarker+"\n"+CleanNamesMarker)
	}
	if strings.Contains(source, CleanNamesMarker) {
		if category != "drive" && !strings.Contains(source, " versions finalize "+category+" ") {
			return "", errors.New("Incomplete clean-name script")
		}
		return source, nil
	}
	var old, new string
	switch category {
	case "site", "database":
		old = BinaryPath + ` versions commit ` + category + ` "$final" || return 1`
		new = `final=$(` + BinaryPath + ` versions finalize ` + category + ` "$final") || return 1`
	case "panel":
		old = `"$MANAGER" versions commit panel "$PUBLISHED_FILE"`
		new = `PUBLISHED_FILE=$("$MANAGER" versions finalize panel "$PUBLISHED_FILE")`
	default:
		return "", errors.New("Invalid clean-name script category")
	}
	updated, err := replaceOnce(source, old, new)
	if err != nil {
		return "", err
	}
	return replaceOnce(updated, DailyLimitMarker, DailyLimitMarker+"\n"+CleanNamesMarker)
}

func (c *Controller) nameRemoteListing(cfg Settings) (map[string]nameRemoteFile, error) {
	raw, err := c.Run([]string{"rclone", "lsjson", cfg.DriveRemote + ":" + cfg.DriveFolder, "--recursive", "--files-only", "--hash", "--config", c.RcloneConfig}, nil)
	if err != nil {
		return nil, errors.New("Cannot inventory Drive; no rename performed")
	}
	var files []nameRemoteFile
	if json.Unmarshal(raw, &files) != nil {
		return nil, errors.New("Invalid Drive inventory")
	}
	result := map[string]nameRemoteFile{}
	for _, f := range files {
		if f.IsDir {
			continue
		}
		if _, duplicate := result[f.Path]; duplicate {
			return nil, errors.New("Duplicate Drive paths; resolve before renaming")
		}
		result[f.Path] = f
	}
	return result, nil
}

func nameRelativeVersion(relative string) (ArchiveVersion, bool) {
	parts := strings.SplitN(relative, "/", 2)
	if len(parts) != 2 || !ValidDay(parts[0]) {
		return ArchiveVersion{}, false
	}
	v, _, ok := versionGroup(parts[1])
	return v, ok && v.Day == parts[0]
}

func (c *Controller) cleanArchiveNamesPlan() (nameMigrationPlan, error) {
	p := nameMigrationPlan{Ledgers: map[string]VersionLedger{}}
	cfg, _, err := c.Active()
	if err != nil {
		return p, err
	}
	p.Config = cfg
	layout, err := c.Layout()
	if err != nil {
		return p, err
	}
	remote, err := c.nameRemoteListing(cfg)
	if err != nil {
		return p, err
	}
	p.Remote = remote
	entries := map[string]*nameRename{}
	reserved := map[string]bool{}
	for _, cat := range Categories {
		archives, err := layout.Archives(cat, false)
		if err != nil {
			return p, err
		}
		for _, a := range archives {
			if !within(a.Path, cfg.BackupRoot) {
				continue
			}
			relative, err := filepath.Rel(cfg.BackupRoot, a.Path)
			if err != nil {
				return p, err
			}
			relative = filepath.ToSlash(relative)
			reserved[relative] = true
			v, ok := nameRelativeVersion(relative)
			if !ok {
				continue
			} // Never classify native/legacy files by their run hour.
			info, err := layout.safeArchive(a.Path)
			if err != nil {
				return p, err
			}
			sum, err := Digest(a.Path)
			if err != nil {
				return p, err
			}
			entries[relative] = &nameRename{Source: relative, Category: v.Category, Day: v.Day, Origin: v.Origin, Local: true, Size: info.Size(), MtimeNS: info.ModTime().UnixNano(), Inode: inode(info), Device: device(info), Hash: sum}
		}
	}
	for relative, f := range remote {
		reserved[relative] = true
		v, ok := nameRelativeVersion(relative)
		if !ok {
			continue
		}
		if f.ID == "" || f.Size <= 0 || len(f.Hashes) == 0 {
			return p, errors.New("Drive archive lacks identity/checksum; refuse unsafe rename")
		}
		if entries[relative] == nil {
			entries[relative] = &nameRename{Source: relative, Category: v.Category, Day: v.Day, Origin: v.Origin}
		}
		file := f
		entries[relative].Remote = &file
	}
	sources := make([]string, 0, len(entries))
	for relative := range entries {
		sources = append(sources, relative)
	}
	sort.Strings(sources)
	for _, source := range sources {
		entry := entries[source]
		for ordinal := 1; ordinal <= 100000; ordinal++ {
			name, err := CleanVersionName(entry.Category, filepath.Base(source), ordinal)
			if err != nil {
				return p, err
			}
			candidate := filepath.ToSlash(filepath.Join(filepath.Dir(source), name))
			if reserved[candidate] {
				continue
			}
			localPath := filepath.Join(cfg.BackupRoot, filepath.FromSlash(candidate))
			if err := SafeParents(localPath); err != nil {
				return p, err
			}
			if _, err := os.Lstat(localPath); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return p, err
			}
			entry.Destination = candidate
			reserved[candidate] = true
			break
		}
		if entry.Destination == "" {
			return p, errors.New("Cannot allocate non-overwriting archive name")
		}
		p.Entries = append(p.Entries, entry)
		dayRoot := filepath.Join(cfg.BackupRoot, entry.Day)
		if info, err := os.Stat(dayRoot); err == nil && info.IsDir() {
			ledger, present := p.Ledgers[entry.Day]
			if !present {
				ledger, err = layout.readVersionLedger(entry.Day)
				if err != nil {
					return p, err
				}
			}
			ledger.Origins[strings.TrimPrefix(entry.Source, entry.Day+"/")] = entry.Origin
			ledger.Origins[strings.TrimPrefix(entry.Destination, entry.Day+"/")] = entry.Origin
			p.Ledgers[entry.Day] = ledger
		}
	}
	for _, item := range []struct{ name, category string }{{"backup-site.sh", "site"}, {"backup-database.sh", "database"}, {"backup-panel.sh", "panel"}, {"auto_backup.sh", "drive"}} {
		path := filepath.Join(cfg.ScriptsDir, item.name)
		before, err := readRegular(path)
		if err != nil {
			return p, err
		}
		after, err := PatchCleanArchiveNames(string(before), item.category)
		if err != nil {
			return p, err
		}
		if after != string(before) {
			p.Scripts = append(p.Scripts, scriptChange{path, path, before, []byte(after)})
		}
	}
	return p, nil
}

func sameRemoteNameFile(a, b nameRemoteFile) bool {
	if a.ID == "" || a.ID != b.ID || a.Size != b.Size || a.ModTime != b.ModTime {
		return false
	}
	for kind, hash := range a.Hashes {
		if hash == "" || b.Hashes[kind] != hash {
			return false
		}
	}
	return len(a.Hashes) > 0
}

func (c *Controller) moveNameRemote(cfg Settings, from, to string) error {
	target := cfg.DriveRemote + ":" + cfg.DriveFolder + "/"
	_, err := c.Run([]string{"rclone", "moveto", target + from, target + to, "--ignore-existing", "--retries", "1", "--config", c.RcloneConfig}, nil)
	return err
}

// Rename only explicit managed tags. No backup, rotation, prune or upload of
// archive contents is performed. Recovery retains both scripts and rename maps.
func (c *Controller) MigrateCleanArchiveNames(apply bool, out io.Writer) (resultErr error) {
	if apply {
		locks, err := acquireAll(c.Locks)
		if err != nil {
			return err
		}
		defer releaseAll(locks)
	}
	plan, err := c.cleanArchiveNamesPlan()
	if err != nil {
		return err
	}
	localCount, remoteCount := 0, 0
	for _, entry := range plan.Entries {
		if entry.Local {
			localCount++
		}
		if entry.Remote != nil {
			remoteCount++
		}
	}
	fmt.Fprintf(out, "CLEAN_NAMES_PREVIEW local=%d drive=%d scripts=%d\n", localCount, remoteCount, len(plan.Scripts))
	if !apply {
		for _, entry := range plan.Entries {
			fmt.Fprintf(out, "RENAME %s -> %s\n", entry.Source, entry.Destination)
		}
		return nil
	}
	if len(plan.Entries) == 0 && len(plan.Scripts) == 0 {
		fmt.Fprintln(out, "Clean archive names already installed.")
		return nil
	}
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return err
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "before-clean-names-")
	if err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(plan.Entries, "", "  ")
	if err = AtomicWrite(filepath.Join(recovery, "renames.json"), raw, 0600); err != nil {
		return err
	}
	fmt.Fprintf(out, "RECOVERY %s\n", recovery)
	layout, err := c.Layout()
	if err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "clean-names-syntax-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	for _, script := range plan.Scripts {
		if err = AtomicWrite(filepath.Join(recovery, filepath.Base(script.Source)), script.Original, 0600); err != nil {
			return err
		}
		candidate := filepath.Join(temp, filepath.Base(script.Source))
		if err = AtomicWrite(candidate, script.Updated, 0600); err != nil {
			return err
		}
		if _, err = c.Run([]string{"/bin/bash", "-n", candidate}, nil); err != nil {
			return errors.New("Clean-name script syntax rejected")
		}
	}
	// Refuse external changes before the first write.
	currentRemote, err := c.nameRemoteListing(plan.Config)
	if err != nil {
		return err
	}
	for _, entry := range plan.Entries {
		if _, exists := currentRemote[entry.Destination]; exists {
			return errors.New("Drive destination appeared; no archive overwritten")
		}
		if entry.Remote != nil && !sameRemoteNameFile(*entry.Remote, currentRemote[entry.Source]) {
			return errors.New("Drive archive changed since inventory")
		}
		if entry.Local {
			path := filepath.Join(plan.Config.BackupRoot, filepath.FromSlash(entry.Source))
			info, err := layout.safeArchive(path)
			if err != nil || info.Size() != entry.Size || info.ModTime().UnixNano() != entry.MtimeNS || inode(info) != entry.Inode || device(info) != entry.Device {
				return errors.New("Local archive changed since inventory")
			}
			hash, err := Digest(path)
			if err != nil || hash != entry.Hash {
				return errors.New("Local archive checksum changed")
			}
		}
	}
	movedLocal := []*nameRename{}
	attemptedRemote := []*nameRename{}
	installed := []scriptChange{}
	defer func() {
		if resultErr == nil {
			return
		}
		cause := resultErr
		failed := false
		for i := len(installed) - 1; i >= 0; i-- {
			s := installed[i]
			if c.Write(s.Source, s.Original, 0700) != nil {
				failed = true
			}
		}
		remote, err := c.nameRemoteListing(plan.Config)
		if err != nil && len(attemptedRemote) > 0 {
			failed = true
		}
		for i := len(attemptedRemote) - 1; i >= 0 && err == nil; i-- {
			e := attemptedRemote[i]
			if sameRemoteNameFile(*e.Remote, remote[e.Source]) {
				continue
			}
			if _, occupied := remote[e.Source]; occupied || !sameRemoteNameFile(*e.Remote, remote[e.Destination]) {
				failed = true
				continue
			}
			if c.moveNameRemote(plan.Config, e.Destination, e.Source) != nil {
				failed = true
			}
		}
		if len(attemptedRemote) > 0 {
			verified, verifyErr := c.nameRemoteListing(plan.Config)
			if verifyErr != nil {
				failed = true
			} else {
				for _, e := range attemptedRemote {
					if !sameRemoteNameFile(*e.Remote, verified[e.Source]) {
						failed = true
					}
					if _, remains := verified[e.Destination]; remains {
						failed = true
					}
				}
			}
		}
		for i := len(movedLocal) - 1; i >= 0; i-- {
			e := movedLocal[i]
			source := filepath.Join(plan.Config.BackupRoot, filepath.FromSlash(e.Source))
			destination := filepath.Join(plan.Config.BackupRoot, filepath.FromSlash(e.Destination))
			info, err := layout.safeArchive(destination)
			if err != nil || inode(info) != e.Inode || device(info) != e.Device || info.Size() != e.Size || info.ModTime().UnixNano() != e.MtimeNS {
				failed = true
				continue
			}
			if renameArchiveExclusive(destination, source) != nil {
				failed = true
			}
		}
		if failed {
			resultErr = fmt.Errorf("%w; rename rollback needs review; originals/checksums at %s", cause, recovery)
		} else {
			resultErr = fmt.Errorf("%w; archive names and scripts restored; recovery=%s", cause, recovery)
		}
	}()
	for day, ledger := range plan.Ledgers {
		ledgerPath, err := layout.versionLedgerPath(day)
		if err != nil {
			return err
		}
		before, err := readRegular(ledgerPath)
		if err == nil {
			if err = AtomicWrite(filepath.Join(recovery, day+".ledger.before.json"), before, 0600); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err = layout.writeVersionLedger(day, ledger); err != nil {
			return err
		}
	}
	for _, entry := range plan.Entries {
		if !entry.Local {
			continue
		}
		source := filepath.Join(plan.Config.BackupRoot, filepath.FromSlash(entry.Source))
		destination := filepath.Join(plan.Config.BackupRoot, filepath.FromSlash(entry.Destination))
		if err = layout.mark(source); err != nil {
			return err
		}
		if err = renameArchiveExclusive(source, destination); err != nil {
			return err
		}
		movedLocal = append(movedLocal, entry)
		sum, err := Digest(destination)
		if err != nil || sum != entry.Hash {
			return errors.New("Renamed local archive failed checksum verification")
		}
		fmt.Fprintf(out, "RENAMED_LOCAL %s\n", entry.Destination)
	}
	for _, entry := range plan.Entries {
		if entry.Remote == nil {
			continue
		}
		attemptedRemote = append(attemptedRemote, entry)
		if err = c.moveNameRemote(plan.Config, entry.Source, entry.Destination); err != nil {
			return errors.New("Drive rename did not complete")
		}
		fmt.Fprintf(out, "RENAMED_DRIVE %s\n", entry.Destination)
	}
	after, err := c.nameRemoteListing(plan.Config)
	if err != nil {
		return err
	}
	for _, entry := range plan.Entries {
		if entry.Remote == nil {
			continue
		}
		if _, remains := after[entry.Source]; remains || !sameRemoteNameFile(*entry.Remote, after[entry.Destination]) {
			return errors.New("Drive identity/checksum changed during rename")
		}
	}
	for _, script := range plan.Scripts {
		current, err := readRegular(script.Source)
		if err != nil || string(current) != string(script.Original) {
			return errors.New("Backup script changed; refuse overwrite")
		}
		installed = append(installed, script)
		if err = c.Write(script.Source, script.Updated, 0700); err != nil {
			return err
		}
	}
	// Origin ledgers remain on the server; never upload JSON to Drive.
	fmt.Fprintf(out, "CLEAN_NAMES_APPLIED local=%d drive=%d scripts=%d; bytes/checksums preserved; no backup or rotation run; recovery=%s\n", localCount, remoteCount, len(plan.Scripts), recovery)
	return nil
}
