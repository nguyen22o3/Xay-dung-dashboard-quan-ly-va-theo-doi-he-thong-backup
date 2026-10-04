package backupops

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const DailyLimitMarker = "# backup-monitor daily versions v1"

var taggedSite = regexp.MustCompile(`^web_([A-Za-z0-9_.-]+)_(20[0-9]{6})_([0-9]{6})_(cron|manual)_([0-9a-f]{16})_site\.tar\.gz$`)
var taggedDatabase = regexp.MustCompile(`^db_([A-Za-z0-9_.-]+)_(20[0-9]{6})_([0-9]{6})_(cron|manual)_([0-9a-f]{16})_mysql_data\.sql\.gz$`)
var taggedPanel = regexp.MustCompile(`^(20[0-9]{2}-[0-9]{2}-[0-9]{2})_([0-9]{6})_(cron|manual)_([0-9a-f]{16})\.zip$`)

type ArchiveVersion struct {
	Category, Entity, Day, Origin, Stamp string
}

// Untagged files are not guessed from their creation hour: manual runs can
// occur at the same time as cron. Only explicitly tagged versions rotate.
func ParseVersion(category, name string) (ArchiveVersion, bool) {
	v := ArchiveVersion{Category: category}
	if category == "panel" {
		m := taggedPanel.FindStringSubmatch(name)
		if m == nil {
			return v, false
		}
		v.Entity, v.Day, v.Origin, v.Stamp = "aaPanel", m[1], m[3], m[1]+" "+m[2]
		_, err := time.Parse("2006-01-02 150405", v.Stamp)
		return v, err == nil && ValidDay(v.Day)
	}
	pattern := taggedSite
	if category == "database" {
		pattern = taggedDatabase
	} else if category != "site" {
		return v, false
	}
	m := pattern.FindStringSubmatch(name)
	if m == nil {
		return v, false
	}
	stamp, err := time.Parse("20060102 150405", m[2]+" "+m[3])
	if err != nil {
		return v, false
	}
	v.Entity, v.Day, v.Origin, v.Stamp = m[1], stamp.Format("2006-01-02"), m[4], stamp.Format("2006-01-02 150405")
	return v, true
}

func versionLimit(origin string) int {
	if origin == "cron" {
		return 1
	}
	return 2
}

// Validate the actual completed compressed stream, not just its extension or
// size. ZIP CRCs, tar structure and gzip checksums must pass before rotation.
func verifyVersion(path, category string) error {
	f, err := openRegular(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return errors.New("Empty or unreadable backup")
	}
	if category == "panel" {
		r, err := zip.NewReader(f, info.Size())
		if err != nil || len(r.File) == 0 {
			return errors.New("Invalid aaPanel ZIP")
		}
		for _, entry := range r.File {
			if entry.FileInfo().IsDir() {
				continue
			}
			reader, err := entry.Open()
			if err != nil {
				return err
			}
			_, err = io.Copy(io.Discard, reader)
			closeErr := reader.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	if category == "site" {
		r := tar.NewReader(gz)
		for {
			_, err := r.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if _, err = io.Copy(io.Discard, r); err != nil {
				return err
			}
		}
	}
	_, err = io.Copy(io.Discard, gz) // Drain footer too; gzip CRC is checked at EOF.
	return err
}

func (l *Layout) CommitVersion(category, path string, out io.Writer) error {
	v, ok, err := l.archiveVersion(category, path)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("Only archives with explicit cron/manual origin may rotate")
	}
	if filepath.Clean(path) != path || !within(path, filepath.Join(l.Root, v.Day, category)) {
		return errors.New("Version is outside its configured day/category")
	}
	info, err := l.safeArchive(path)
	if err != nil {
		return err
	}
	if err = verifyVersion(path, category); err != nil {
		return fmt.Errorf("New backup verification failed; old versions retained: %w", err)
	}
	type candidate struct {
		path string
		info os.FileInfo
	}
	var older []candidate
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		other, tagged, err := l.archiveVersion(category, filepath.Join(filepath.Dir(path), entry.Name()))
		if err != nil {
			return err
		}
		if !tagged || other.Day != v.Day || other.Entity != v.Entity || other.Origin != v.Origin {
			continue
		}
		p := filepath.Join(filepath.Dir(path), entry.Name())
		if p == path {
			continue
		}
		item, err := l.safeArchive(p)
		if err != nil {
			return err
		}
		older = append(older, candidate{p, item})
	}
	sort.Slice(older, func(i, j int) bool {
		if !older[i].info.ModTime().Equal(older[j].info.ModTime()) {
			return older[i].info.ModTime().After(older[j].info.ModTime())
		}
		return older[i].path > older[j].path
	})
	keepOld := versionLimit(v.Origin) - 1
	if len(older) <= keepOld {
		return nil
	}
	// Verify the second retained manual version as well. If damaged, keep the
	// older recovery copies instead of automatically destroying them.
	for i := 0; i < keepOld; i++ {
		if err = verifyVersion(older[i].path, category); err != nil {
			return fmt.Errorf("Retained backup verification failed; rotation skipped: %w", err)
		}
	}
	current, err := l.safeArchive(path)
	if err != nil || !os.SameFile(info, current) || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return errors.New("New backup changed; rotation skipped")
	}
	for _, item := range older[keepOld:] {
		current, err := l.safeArchive(item.path)
		if err != nil {
			return err
		}
		if !os.SameFile(item.info, current) || current.Size() != item.info.Size() || !current.ModTime().Equal(item.info.ModTime()) {
			return errors.New("Old version changed; rotation skipped")
		}
		if err = l.mark(item.path); err != nil {
			return err
		}
		if err = os.Remove(item.path); err != nil {
			return err
		}
		fmt.Fprintln(out, "ROTATED "+item.path)
	}
	return nil
}

type remoteVersionFile struct {
	Path  string
	IsDir bool
}

var versionRelativeChars = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

func versionGroup(relative string) (ArchiveVersion, string, bool) {
	if !versionRelativeChars.MatchString(relative) || strings.HasPrefix(relative, "/") || filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative))) != relative {
		return ArchiveVersion{}, "", false
	}
	parts := strings.Split(relative, "/")
	if len(parts) < 2 {
		return ArchiveVersion{}, "", false
	}
	v, ok := ParseVersion(parts[0], parts[len(parts)-1])
	if !ok {
		return v, "", false
	}
	if v.Category == "panel" && len(parts) != 2 {
		return v, "", false
	}
	if v.Category != "panel" && (len(parts) < 3 || parts[len(parts)-2] != v.Entity) {
		return v, "", false
	}
	return v, strings.Join(parts[:len(parts)-1], "/") + "|" + v.Origin, true
}

// Reconcile only obsolete versions with explicit origin in groups fully present
// locally. Never mirror arbitrary deletions, legacy files, or whole folders.
func (c *Controller) ReconcileDriveVersions(day string, apply bool, out io.Writer) error {
	if !ValidDay(day) {
		return errors.New("Invalid backup day")
	}
	cfg, _, err := c.Active()
	if err != nil {
		return err
	}
	layout, err := c.Layout()
	if err != nil {
		return err
	}
	dayRoot := filepath.Join(cfg.BackupRoot, day)
	if err = SafeParents(dayRoot); err != nil {
		return err
	}
	ledger, err := layout.readVersionLedger(day)
	if err != nil {
		return err
	}
	localGroups := map[string]map[string]bool{}
	for _, cat := range Categories {
		archives, err := layout.Archives(cat, false)
		if err != nil {
			return err
		}
		for _, a := range archives {
			if a.Day != day || !within(a.Path, dayRoot) {
				continue
			}
			relative, err := filepath.Rel(dayRoot, a.Path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			v, key, ok := versionGroupWithLedger(relative, ledger)
			if !ok || v.Day != day {
				continue
			}
			if localGroups[key] == nil {
				localGroups[key] = map[string]bool{}
			}
			localGroups[key][relative] = true
		}
	}
	if len(localGroups) == 0 {
		fmt.Fprintln(out, "No tagged versions to reconcile.")
		return nil
	}
	target := cfg.DriveRemote + ":" + cfg.DriveFolder + "/" + day
	check := []string{"rclone", "check", dayRoot, target, "--one-way", "--config", c.RcloneConfig,
		"--include", "*.tar.gz", "--include", "*.sql.gz", "--include", "*.zip"}
	if _, err = c.Run(check, nil); err != nil {
		return errors.New("Drive verification failed; no old version deleted")
	}
	raw, err := c.Run([]string{"rclone", "lsjson", target, "--recursive", "--files-only", "--config", c.RcloneConfig}, nil)
	if err != nil {
		return errors.New("Cannot list Drive versions; no old version deleted")
	}
	var remote []remoteVersionFile
	if err = json.Unmarshal(raw, &remote); err != nil {
		return errors.New("Invalid Drive listing; cleanup skipped")
	}
	var obsolete []string
	for _, f := range remote {
		v, key, ok := versionGroupWithLedger(f.Path, ledger)
		if !ok || f.IsDir || v.Day != day {
			continue
		}
		local := localGroups[key]
		// A missing local archive must not become an instruction to destroy its
		// independent cloud copy. Only full 1-cron/2-manual groups can rotate.
		if len(local) != versionLimit(v.Origin) || local[f.Path] {
			continue
		}
		for relative := range local {
			file := filepath.Join(dayRoot, filepath.FromSlash(relative))
			if _, err = layout.safeArchive(file); err != nil {
				return err
			}
			if err = verifyVersion(file, v.Category); err != nil {
				return errors.New("Retained archive verification failed; Drive cleanup skipped")
			}
		}
		obsolete = append(obsolete, f.Path)
	}
	sort.Strings(obsolete)
	for _, relative := range obsolete {
		label := "Would rotate Drive: "
		if apply {
			if _, err = c.Run([]string{"rclone", "deletefile", target + "/" + relative, "--config", c.RcloneConfig}, nil); err != nil {
				return errors.New("Cannot rotate old Drive version")
			}
			label = "Rotated Drive: "
		}
		fmt.Fprintln(out, label+relative)
	}
	return nil
}

func VersionLock() (func(), error) {
	layout, err := acquire("/run/backup-monitor-layout.lock", true, false)
	if err != nil {
		return nil, err
	}
	versions, err := acquire("/run/backup-monitor-versions.lock", false, false)
	if err != nil {
		layout.Close()
		return nil, err
	}
	return func() { versions.Close(); layout.Close() }, nil
}
