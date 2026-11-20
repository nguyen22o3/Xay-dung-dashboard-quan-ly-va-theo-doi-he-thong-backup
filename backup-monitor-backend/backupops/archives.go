package backupops

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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

const VersionLedgerName = ".backup-monitor-versions.json"
const CleanNamesMarker = "# backup-monitor clean archive names v1"

var cleanSiteName = regexp.MustCompile(`^web_([A-Za-z0-9_.-]+)_(20[0-9]{6})_([0-9]{6})(?:_([1-9][0-9]*))?_site\.tar\.gz$`)
var cleanDatabaseName = regexp.MustCompile(`^db_([A-Za-z0-9_.-]+)_(20[0-9]{6})_([0-9]{6})(?:_([1-9][0-9]*))?_mysql_data\.sql\.gz$`)
var cleanPanelName = regexp.MustCompile(`^(20[0-9]{2}-[0-9]{2}-[0-9]{2})_([0-9]{6})(?:_([1-9][0-9]*))?\.zip$`)

type VersionLedger struct {
	Format  int               `json:"format"`
	Origins map[string]string `json:"origins"`
}

func cleanVersion(category, name string) (ArchiveVersion, bool) {
	v := ArchiveVersion{Category: category}
	if category == "panel" {
		m := cleanPanelName.FindStringSubmatch(name)
		if m == nil {
			return v, false
		}
		v.Entity, v.Day, v.Stamp = "aaPanel", m[1], m[1]+" "+m[2]
		_, err := time.Parse("2006-01-02 150405", v.Stamp)
		return v, err == nil && ValidDay(v.Day)
	}
	pattern := cleanSiteName
	if category == "database" {
		pattern = cleanDatabaseName
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
	v.Entity, v.Day, v.Stamp = m[1], stamp.Format("2006-01-02"), stamp.Format("2006-01-02 150405")
	return v, true
}

func CleanVersionName(category, name string, ordinal int) (string, error) {
	v, ok := ParseVersion(category, name)
	if !ok || ordinal < 1 || ordinal > 100000 {
		return "", errors.New("Invalid tagged archive name")
	}
	suffix := ""
	if ordinal > 1 {
		suffix = fmt.Sprintf("_%d", ordinal)
	}
	if category == "panel" {
		return v.Day + "_" + strings.Split(v.Stamp, " ")[1] + suffix + ".zip", nil
	}
	prefix, end := "web_", "_site.tar.gz"
	if category == "database" {
		prefix, end = "db_", "_mysql_data.sql.gz"
	}
	return prefix + v.Entity + "_" + strings.ReplaceAll(v.Day, "-", "") + "_" + strings.Split(v.Stamp, " ")[1] + suffix + end, nil
}

func versionGroupWithLedger(relative string, ledger VersionLedger) (ArchiveVersion, string, bool) {
	if v, key, ok := versionGroup(relative); ok {
		return v, key, true
	}
	if !versionRelativeChars.MatchString(relative) || strings.HasPrefix(relative, "/") || filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative))) != relative {
		return ArchiveVersion{}, "", false
	}
	parts := strings.Split(relative, "/")
	if len(parts) < 2 {
		return ArchiveVersion{}, "", false
	}
	v, ok := cleanVersion(parts[0], parts[len(parts)-1])
	if !ok || (v.Category == "panel" && len(parts) != 2) || (v.Category != "panel" && (len(parts) < 3 || parts[len(parts)-2] != v.Entity)) {
		return v, "", false
	}
	v.Origin = ledger.Origins[relative]
	if v.Origin != "cron" && v.Origin != "manual" {
		return v, "", false
	}
	return v, strings.Join(parts[:len(parts)-1], "/") + "|" + v.Origin, true
}

func (l *Layout) versionLedgerPath(day string) (string, error) {
	if !ValidDay(day) || l.MetadataDir == "" || !filepath.IsAbs(l.MetadataDir) || filepath.Clean(l.MetadataDir) != l.MetadataDir || within(l.MetadataDir, l.Root) || within(l.Root, l.MetadataDir) {
		return "", errors.New("Invalid private version metadata directory")
	}
	return filepath.Join(l.MetadataDir, day+".json"), nil
}

func readVersionLedgerFile(day, path string) (VersionLedger, error) {
	ledger := VersionLedger{Format: 1, Origins: map[string]string{}}
	if !ValidDay(day) {
		return ledger, errors.New("Invalid ledger day")
	}
	if err := SafeParents(path); err != nil {
		return ledger, err
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return ledger, nil
	}
	if err != nil {
		return ledger, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return ledger, errors.New("Invalid version ledger")
	}
	raw, err := readRegular(path)
	if err != nil {
		return ledger, err
	}
	if json.Unmarshal(raw, &ledger) != nil || ledger.Format != 1 || ledger.Origins == nil || len(ledger.Origins) > 100000 {
		return ledger, errors.New("Invalid version ledger")
	}
	for relative, origin := range ledger.Origins {
		if origin != "cron" && origin != "manual" {
			return ledger, errors.New("Invalid archive origin metadata")
		}
		v, _, ok := versionGroupWithLedger(relative, ledger)
		if !ok || v.Day != day || v.Origin != origin {
			return ledger, errors.New("Invalid archive metadata path")
		}
	}
	return ledger, nil
}

func mergeVersionLedgers(a, b VersionLedger) (VersionLedger, error) {
	result := VersionLedger{Format: 1, Origins: map[string]string{}}
	for _, ledger := range []VersionLedger{a, b} {
		for key, origin := range ledger.Origins {
			if previous, ok := result.Origins[key]; ok && previous != origin {
				return result, errors.New("Conflicting cron/manual origin metadata")
			}
			result.Origins[key] = origin
		}
	}
	if len(result.Origins) > 100000 {
		return result, errors.New("Version metadata exceeds safe limit")
	}
	return result, nil
}

// Read legacy metadata during migration, but all new writes use the private store.
func (l *Layout) readVersionLedger(day string) (VersionLedger, error) {
	central := VersionLedger{Format: 1, Origins: map[string]string{}}
	if l.MetadataDir != "" {
		path, err := l.versionLedgerPath(day)
		if err != nil {
			return central, err
		}
		central, err = readVersionLedgerFile(day, path)
		if err != nil {
			return central, err
		}
	}
	legacy, err := readVersionLedgerFile(day, filepath.Join(l.Root, day, VersionLedgerName))
	if err != nil {
		return central, err
	}
	return mergeVersionLedgers(central, legacy)
}

func (l *Layout) writeVersionLedger(day string, ledger VersionLedger) error {
	path, err := l.versionLedgerPath(day)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	if err = mkdirPrivate(l.MetadataDir); err != nil {
		return err
	}
	info, err := os.Stat(l.MetadataDir)
	if err != nil || !info.IsDir() || !privateRootOwned(info) || (runtime.GOOS == "linux" && info.Mode().Perm()&0077 != 0) {
		return errors.New("Version metadata store must be root-private")
	}
	return AtomicWrite(path, append(raw, '\n'), 0600)
}

func (l *Layout) archiveVersion(category, path string) (ArchiveVersion, bool, error) {
	if v, ok := ParseVersion(category, filepath.Base(path)); ok {
		return v, true, nil
	}
	v, ok := cleanVersion(category, filepath.Base(path))
	if !ok {
		return v, false, nil
	}
	ledger, err := l.readVersionLedger(v.Day)
	if err != nil {
		return v, false, err
	}
	relative, err := filepath.Rel(filepath.Join(l.Root, v.Day), path)
	if err != nil {
		return v, false, err
	}
	v, _, ok = versionGroupWithLedger(filepath.ToSlash(relative), ledger)
	return v, ok, nil
}

// A no-replace rename within the same filesystem preserves bytes, inode and mtime.
// If interrupted between link and unlink, both recovery names still hold the data.
func renameArchiveExclusive(source, destination string) error {
	if source == destination {
		return errors.New("Identical rename paths")
	}
	if err := SafeParents(source); err != nil {
		return err
	}
	if err := SafeParents(destination); err != nil {
		return err
	}
	before, err := os.Stat(source)
	if err != nil || !before.Mode().IsRegular() {
		return errors.New("Archive source is not a regular file")
	}
	if err = os.Link(source, destination); err != nil {
		return err
	}
	after, err := os.Stat(source)
	linked, linkErr := os.Stat(destination)
	if err != nil || linkErr != nil || !os.SameFile(before, after) || !os.SameFile(before, linked) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return errors.New("Archive changed during rename; both names retained")
	}
	return os.Remove(source)
}

// Finalize only a known tagged completed backup; old unclassified files stay so.
// Caller holds VersionLock. Rotation still distinguishes origins via the ledger.
func (l *Layout) FinalizeVersion(category, path string, out io.Writer) (string, error) {
	v, ok := ParseVersion(category, filepath.Base(path))
	if !ok || filepath.Clean(path) != path || !within(path, filepath.Join(l.Root, v.Day, category)) {
		return "", errors.New("Only a managed tagged archive may be finalized")
	}
	if _, err := l.safeArchive(path); err != nil {
		return "", err
	}
	if err := verifyVersion(path, category); err != nil {
		return "", err
	}
	ledger, err := l.readVersionLedger(v.Day)
	if err != nil {
		return "", err
	}
	var destination string
	for ordinal := 1; ordinal <= 100000; ordinal++ {
		name, _ := CleanVersionName(category, filepath.Base(path), ordinal)
		destination = filepath.Join(filepath.Dir(path), name)
		relative, _ := filepath.Rel(filepath.Join(l.Root, v.Day), destination)
		if _, reserved := ledger.Origins[filepath.ToSlash(relative)]; reserved {
			destination = ""
			continue
		}
		if _, err := os.Lstat(destination); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", err
		}
		destination = ""
	}
	if destination == "" {
		return "", errors.New("No safe archive name available")
	}
	relative, _ := filepath.Rel(filepath.Join(l.Root, v.Day), destination)
	original, _ := filepath.Rel(filepath.Join(l.Root, v.Day), path)
	ledger.Origins[filepath.ToSlash(relative)] = v.Origin
	ledger.Origins[filepath.ToSlash(original)] = v.Origin
	if err = l.writeVersionLedger(v.Day, ledger); err != nil {
		return "", err
	}
	if err = l.mark(path); err != nil {
		return "", err
	}
	if err = renameArchiveExclusive(path, destination); err != nil {
		return "", err
	}
	if err = l.CommitVersion(category, destination, out); err != nil {
		return "", err
	}
	return destination, nil
}

func PatchDailyVersions(source, category string) (string, error) {
	if strings.Contains(source, DailyLimitMarker) {
		if category != "drive" && !strings.Contains(source, " versions commit "+category+" ") && !strings.Contains(source, " versions finalize "+category+" ") {
			return "", errors.New("Incomplete daily-version script")
		}
		if category == "drive" && !strings.Contains(source, " versions drive ") {
			return "", errors.New("Incomplete Drive daily-version script")
		}
		return PatchCleanArchiveNames(source, category)
	}
	if category == "panel" {
		old := `PUBLISHED_FILE="$DAY_DIR/${DAY}_${STAMP:9:6}_${nonce}.zip"`
		updated, err := replaceOnce(source, old, `PUBLISHED_FILE="$DAY_DIR/${DAY}_${STAMP:9:6}_${BACKUP_MONITOR_ORIGIN}_${nonce}.zip"`)
		if err != nil {
			return "", err
		}
		updated, err = replaceOnce(updated, `printf 'SOURCE=aaPanel data/config/vhost\nBACKUP=%s\n' "$PUBLISHED_FILE"`,
			`"$MANAGER" versions commit panel "$PUBLISHED_FILE"
printf 'SOURCE=aaPanel data/config/vhost\nBACKUP=%s\n' "$PUBLISHED_FILE"`)
		if err != nil {
			return "", err
		}
		updated = strings.ReplaceAll(updated, "Does not install cron, upload to Drive, disable aaPanel, or delete old backups.", "Rotates tagged same-day versions after verification; no cron edits, Drive upload or old-date pruning.")
		updated = strings.ReplaceAll(updated, "duration_ms=$duration; no existing backup was removed", "duration_ms=$duration; check preceding validation/rotation messages")
		updated, err = addVersionOrigin(updated)
		if err != nil {
			return "", err
		}
		return PatchCleanArchiveNames(updated, category)
	}
	if !strings.Contains(source, LayoutMarker) {
		return "", errors.New("Install date-first layout before daily limits")
	}
	if category == "site" || category == "database" {
		suffix := "site.tar.gz"
		if category == "database" {
			suffix = "mysql_data.sql.gz"
		}
		old := `    final="$dir/${prefix}${stamp}_` + suffix + `"`
		updated, err := replaceOnce(source, old, `    local nonce
    nonce=$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n') || return 1
    [[ "$nonce" =~ ^[0-9a-f]{16}$ ]] || return 1
    final="$dir/${prefix}${stamp}_${BACKUP_MONITOR_ORIGIN}_${nonce}_`+suffix+`"`)
		if err != nil {
			return "", err
		}
		old = `    printf 'CREATED %s DURATION %d.%03d\n' "$final" "$((duration_ms / 1000))" "$((duration_ms % 1000))"`
		updated, err = replaceOnce(updated, old, "    "+BinaryPath+` versions commit `+category+` "$final" || return 1
`+old)
		if err != nil {
			return "", err
		}
		updated, err = addVersionOrigin(updated)
		if err != nil {
			return "", err
		}
		return PatchCleanArchiveNames(updated, category)
	}
	if category == "drive" {
		old := `        if ! rclone copy "$day_dir" "gdrive:Backup/$day" --config /root/.config/rclone/rclone.conf --include '*.tar.gz' --include '*.sql.gz' --include '*.zip'; then
            STATUS="Failed"
        fi`
		new := strings.TrimSuffix(old, "        fi") + "        elif ! " + BinaryPath + ` versions drive "$day" --apply; then
            STATUS="Failed"
        fi`
		updated, err := replaceOnce(source, old, new)
		if err != nil {
			return "", err
		}
		updated, err = replaceOnce(updated, "#!/bin/bash", "#!/bin/bash\n"+DailyLimitMarker)
		if err != nil {
			return "", err
		}
		return PatchCleanArchiveNames(updated, category)
	}
	return "", errors.New("Invalid daily-version category")
}

func addVersionOrigin(source string) (string, error) {
	return replaceOnce(source, "set -Eeuo pipefail", "set -Eeuo pipefail\n"+DailyLimitMarker+`
BACKUP_MONITOR_ORIGIN=${BACKUP_MONITOR_ORIGIN:-manual}
if [[ "$BACKUP_MONITOR_ORIGIN" != cron && "$BACKUP_MONITOR_ORIGIN" != manual ]]; then
    printf 'Invalid backup origin; use cron or manual.\n' >&2
    exit 2
fi`)
}

func (c *Controller) DailyVersionsPlan() ([]scriptChange, error) {
	cfg, _, err := c.Active()
	if err != nil {
		return nil, err
	}
	var plan []scriptChange
	for _, item := range []struct{ name, category string }{{"backup-site.sh", "site"}, {"backup-database.sh", "database"}, {"auto_backup.sh", "drive"}, {"backup-panel.sh", "panel"}} {
		p := filepath.Join(cfg.ScriptsDir, item.name)
		if item.category == "panel" && !exists(p) {
			continue
		}
		before, err := readRegular(p)
		if err != nil {
			return nil, err
		}
		text := string(before)
		if strings.Contains(text, GeneratedTaskMarker) {
			continue
		}
		if item.category == "drive" {
			old := "gdrive:Backup/$day"
			actual := cfg.DriveRemote + ":" + cfg.DriveFolder + "/$day"
			text = strings.ReplaceAll(text, actual, old)
			after, err := PatchDailyVersions(text, item.category)
			if err != nil {
				return nil, err
			}
			after, err = PatchDriveReliability(after)
			if err != nil {
				return nil, err
			}
			after = strings.ReplaceAll(after, old, actual)
			if after != string(before) {
				plan = append(plan, scriptChange{Source: p, Target: p, Original: before, Updated: []byte(after)})
			}
			continue
		}
		after, err := PatchDailyVersions(text, item.category)
		if err != nil {
			return nil, err
		}
		if after != string(before) {
			plan = append(plan, scriptChange{Source: p, Target: p, Original: before, Updated: []byte(after)})
		}
	}
	return plan, nil
}

func (c *Controller) InstallDailyVersions(apply bool, out io.Writer) (resultErr error) {
	if !apply {
		plan, err := c.DailyVersionsPlan()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "DAILY_VERSION_PREVIEW: %d scripts; no archives or cron changed.\n", len(plan))
		return nil
	}
	locks, err := acquireAll(c.Locks)
	if err != nil {
		return err
	}
	defer releaseAll(locks)
	plan, err := c.DailyVersionsPlan()
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		fmt.Fprintln(out, "Daily-version limits already installed.")
		return nil
	}
	temp, err := os.MkdirTemp("", "versions-validate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	for _, s := range plan {
		p := filepath.Join(temp, filepath.Base(s.Source))
		if err = AtomicWrite(p, s.Updated, 0600); err != nil {
			return err
		}
		if _, err = c.Run([]string{"/bin/bash", "-n", p}, nil); err != nil {
			return errors.New("Daily-version script failed syntax validation")
		}
	}
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return err
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "before-daily-versions-")
	if err != nil {
		return err
	}
	for _, s := range plan {
		if err = AtomicWrite(filepath.Join(recovery, filepath.Base(s.Source)), s.Original, 0600); err != nil {
			return err
		}
	}
	var installed []scriptChange
	defer func() {
		if resultErr == nil {
			return
		}
		for i := len(installed) - 1; i >= 0; i-- {
			if err := AtomicWrite(installed[i].Source, installed[i].Original, 0700); err != nil {
				resultErr = fmt.Errorf("Rollback needs review: %s", recovery)
			}
		}
	}()
	for _, s := range plan {
		current, err := readRegular(s.Source)
		if err != nil {
			return err
		}
		if string(current) != string(s.Original) {
			return errors.New("Script changed; refusing overwrite")
		}
		if err = c.Write(s.Source, s.Updated, 0700); err != nil {
			return err
		}
		installed = append(installed, s)
	}
	fmt.Fprintf(out, "DAILY_VERSIONS_INSTALLED: 1 cron + 2 manual per target/day; recovery=%s. No existing archive deleted.\n", recovery)
	return nil
}

var dayPattern = regexp.MustCompile(`^20[0-9]{2}-[0-9]{2}-[0-9]{2}$`)
var stampPattern = regexp.MustCompile(`_(20[0-9]{6})_([0-9]{6})_`)
var panelPattern = regexp.MustCompile(`^(20[0-9]{2}-[0-9]{2}-[0-9]{2})(?:_[0-9a-f_]+|_[0-9]{6}_(?:cron|manual)_[0-9a-f]{16})?\.zip$`)

func ValidDay(day string) bool {
	_, err := time.Parse("2006-01-02", day)
	return err == nil && dayPattern.MatchString(day)
}
func ArchiveDay(category, name string) string {
	if category == "panel" {
		m := panelPattern.FindStringSubmatch(name)
		if len(m) > 1 && ValidDay(m[1]) {
			return m[1]
		}
		return ""
	}
	prefix, suffix := "web_", ".tar.gz"
	if category == "database" {
		prefix, suffix = "db_", ".sql.gz"
	} else if category != "site" {
		return ""
	}
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return ""
	}
	m := stampPattern.FindStringSubmatch(name)
	if len(m) < 3 {
		return ""
	}
	stamp, err := time.Parse("20060102150405", m[1]+m[2])
	if err != nil {
		return ""
	}
	return stamp.Format("2006-01-02")
}
func validCategory(category string) bool {
	for _, c := range Categories {
		if c == category {
			return true
		}
	}
	return false
}

type Archive struct{ Day, Path, Relative string }
type Move struct {
	Category    string `json:"category"`
	Day         string `json:"day"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Size        int64  `json:"size"`
	MtimeNS     int64  `json:"mtime_ns"`
	Inode       uint64 `json:"inode"`
	Device      uint64 `json:"device"`
	Hash        string `json:"sha256"`
}
type Layout struct {
	Root, LegacyRoot, Markers, Journal string
	MetadataDir                        string
	Now                                func() time.Time
}

func (l *Layout) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}
func (l *Layout) safeArchive(path string) (os.FileInfo, error) {
	if !within(path, l.Root) && (l.LegacyRoot == "" || !within(path, l.LegacyRoot)) {
		return nil, errors.New("Archive is outside configured backup roots")
	}
	if err := SafeParents(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, errors.New("Not a nonempty regular archive: " + path)
	}
	return info, nil
}
func (l *Layout) Archives(category string, legacyOnly bool) ([]Archive, error) {
	result := []Archive{}
	if !validCategory(category) {
		return nil, errors.New("Invalid backup category")
	}
	if err := SafeParents(l.Root); err != nil {
		return nil, err
	}
	roots := []string{filepath.Join(l.Root, category)}
	if l.LegacyRoot != "" && l.LegacyRoot != l.Root {
		roots = append(roots, filepath.Join(l.LegacyRoot, category))
	}
	if !legacyOnly {
		entries, err := os.ReadDir(l.Root)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if ValidDay(entry.Name()) {
				roots = append(roots, filepath.Join(l.Root, entry.Name(), category))
			}
		}
	}
	for _, base := range roots {
		if err := SafeParents(base); err != nil {
			return nil, err
		}
		info, err := os.Stat(base)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("Archive directory is not a directory")
		}
		err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return SafeParents(path)
			}
			// Refuse a symbolic-link directory/file, even if its name looks unrelated.
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("Symlink is not allowed: " + path)
			}
			day := ArchiveDay(category, entry.Name())
			if day == "" {
				return nil
			}
			if _, err := l.safeArchive(path); err != nil {
				return err
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			result = append(result, Archive{day, path, rel})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Day != result[j].Day {
			return result[i].Day < result[j].Day
		}
		return result[i].Path < result[j].Path
	})
	return result, nil
}
func (l *Layout) Plan(categories []string) ([]Move, error) {
	result := []Move{}
	destinations := map[string]bool{}
	root, err := os.Stat(l.Root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, category := range categories {
		if seen[category] {
			continue
		}
		seen[category] = true
		archives, err := l.Archives(category, true)
		if err != nil {
			return nil, err
		}
		for _, a := range archives {
			info, err := l.safeArchive(a.Path)
			if err != nil {
				return nil, err
			}
			if device(info) != device(root) {
				return nil, errors.New("Cross-filesystem move requires a separate copy plan")
			}
			sum, err := Digest(a.Path)
			if err != nil {
				return nil, err
			}
			destination := filepath.Join(l.Root, a.Day, category, a.Relative)
			if err = SafeParents(destination); err != nil {
				return nil, err
			}
			if exists(destination) && category == "panel" {
				destination = filepath.Join(filepath.Dir(destination), a.Day+"_"+info.ModTime().In(time.Local).Format("150405")+"_"+sum[:12]+".zip")
			}
			if err = SafeParents(destination); err != nil {
				return nil, err
			}
			if exists(destination) || destinations[destination] {
				return nil, errors.New("Destination already exists; nothing overwritten: " + destination)
			}
			destinations[destination] = true
			result = append(result, Move{category, a.Day, a.Path, destination, info.Size(), info.ModTime().UnixNano(), inode(info), device(info), sum})
		}
	}
	return result, nil
}
func (l *Layout) mark(path string) error {
	if err := mkdirPrivate(l.Markers); err != nil {
		return err
	}
	return AtomicWrite(filepath.Join(l.Markers, hashBytes([]byte(path))), []byte(fmt.Sprintf("%d\n", l.now().Unix())), 0600)
}
func (l *Layout) Organize(categories []string, apply bool, out io.Writer) ([]Move, error) {
	plan, err := l.Plan(categories)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, cat := range categories {
		counts[cat] = 0
	}
	for _, m := range plan {
		counts[m.Category]++
	}
	if !apply {
		if err = json.NewEncoder(out).Encode(map[string]interface{}{"mode": "preview", "counts": counts, "total": len(plan)}); err != nil {
			return nil, err
		}
		return plan, nil
	}
	if len(plan) == 0 {
		fmt.Fprintln(out, "Already organized; no legacy archives to move.")
		return plan, nil
	}
	if err = mkdirPrivate(l.Journal); err != nil {
		return nil, err
	}
	manifest := filepath.Join(l.Journal, "moves-"+l.now().Format("20060102T150405")+"-"+randomID()+".json")
	data, _ := json.MarshalIndent(plan, "", "  ")
	f, err := os.OpenFile(manifest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return nil, err
	}
	for _, m := range plan {
		info, err := l.safeArchive(m.Source)
		if err != nil {
			return nil, err
		}
		if info.Size() != m.Size || info.ModTime().UnixNano() != m.MtimeNS || device(info) != m.Device || inode(info) != m.Inode {
			return nil, errors.New("Source changed during migration: " + m.Source)
		}
		if err = mkdirPrivate(filepath.Dir(m.Destination)); err != nil {
			return nil, err
		}
		parent, err := os.Stat(filepath.Dir(m.Destination))
		if err != nil {
			return nil, err
		}
		if device(parent) != m.Device {
			return nil, errors.New("Destination is on another filesystem; original retained")
		}
		if err = SafeParents(m.Destination); err != nil {
			return nil, err
		}
		// Exclusive hard link: no overwrite, copy, recompression, or mtime change.
		if err = os.Link(m.Source, m.Destination); err != nil {
			return nil, err
		}
		sum, err := Digest(m.Destination)
		if err != nil {
			return nil, err
		}
		if sum != m.Hash {
			return nil, errors.New("Checksum mismatch; original retained")
		}
		// Re-check after hashing, so a concurrent native writer cannot mutate the
		// source during hashing and then lose its original name.
		current, err := l.safeArchive(m.Source)
		if err != nil {
			return nil, err
		}
		if current.Size() != m.Size || current.ModTime().UnixNano() != m.MtimeNS || !os.SameFile(info, current) {
			return nil, errors.New("Source changed; both archive names retained")
		}
		if err = l.mark(m.Source); err != nil {
			return nil, err
		}
		if err = os.Remove(m.Source); err != nil {
			return nil, err
		}
	}
	for _, m := range plan {
		sum, err := Digest(m.Destination)
		if err != nil {
			return nil, err
		}
		if sum != m.Hash {
			return nil, errors.New("Post-move checksum mismatch")
		}
	}
	err = json.NewEncoder(out).Encode(map[string]interface{}{"mode": "moved-and-verified", "counts": counts, "total": len(plan), "manifest": manifest})
	return plan, err
}
func (l *Layout) Expired(category string) ([]string, error) {
	archives, err := l.Archives(category, false)
	if err != nil {
		return nil, err
	}
	today := l.now().Format("2006-01-02")
	days := map[string]bool{}
	for _, a := range archives {
		if a.Day <= today {
			days[a.Day] = true
		}
	}
	ordered := []string{}
	for day := range days {
		ordered = append(ordered, day)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ordered)))
	expired := map[string]bool{}
	for i := RetentionDays; i < len(ordered); i++ {
		expired[ordered[i]] = true
	}
	result := []string{}
	for _, a := range archives {
		if expired[a.Day] {
			result = append(result, a.Path)
		}
	}
	return result, nil
}
func (l *Layout) Prune(category string, delete bool, out io.Writer) error {
	files, err := l.Expired(category)
	if err != nil {
		return err
	}
	for _, path := range files {
		if _, err = l.safeArchive(path); err != nil {
			return err
		}
		label := "Would delete: "
		if delete {
			if err = l.mark(path); err != nil {
				return err
			}
			if err = os.Remove(path); err != nil {
				return err
			}
			label = "Deleted: "
		}
		fmt.Fprintln(out, label+path)
	}
	if len(files) == 0 {
		fmt.Fprintf(out, "Already cleaned: no expired %s archives.\n", category)
	}
	if delete {
		return l.removeEmptyOldDays(out)
	}
	return nil
}

// removeEmptyOldDays removes only empty, dated directories outside the latest
// 14 calendar dates. Known category trees are visited bottom-up; unknown
// entries and nonempty directories are left untouched.
func (l *Layout) removeEmptyOldDays(out io.Writer) error {
	if err := SafeParents(l.Root); err != nil {
		return err
	}
	entries, err := os.ReadDir(l.Root)
	if err != nil {
		return err
	}
	cutoff := l.now().AddDate(0, 0, 1-RetentionDays).Format("2006-01-02")
	for _, entry := range entries {
		day := entry.Name()
		if !ValidDay(day) || day >= cutoff || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		dayDir := filepath.Join(l.Root, day)
		if err := SafeParents(dayDir); err != nil {
			return err
		}
		for _, category := range Categories {
			base := filepath.Join(dayDir, category)
			info, err := os.Lstat(base)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.IsDir() {
				continue
			}
			dirs := []string{}
			err = filepath.WalkDir(base, func(path string, item fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if item.IsDir() {
					dirs = append(dirs, path)
				}
				return nil
			})
			if err != nil {
				return err
			}
			for i := len(dirs) - 1; i >= 0; i-- {
				if err := removeEmptyDirectory(dirs[i], out); err != nil {
					return err
				}
			}
		}
		if err := removeEmptyDirectory(dayDir, out); err != nil {
			return err
		}
	}
	return nil
}

func removeEmptyDirectory(path string, out io.Writer) error {
	if err := SafeParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || len(entries) != 0 {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// A concurrent writer may have populated the directory after ReadDir.
		if current, readErr := os.ReadDir(path); readErr == nil && len(current) != 0 {
			return nil
		}
		return err
	}
	fmt.Fprintln(out, "Removed empty directory: "+path)
	return nil
}
func (c *Controller) Layout() (*Layout, error) {
	cfg, _, err := c.Active()
	if err != nil {
		return nil, err
	}
	markers := c.RotationMarkers
	if markers == "" {
		markers = "/run/backup-monitor-rotations"
	}
	legacy, metadata := "/www/backup", c.VersionMetadataDir
	// Keep the existing ledger for the shared root; partition additional roots
	// by path (not cron ID), so a backup and its Drive cron share origin metadata.
	base := *c
	base.CronSelector = ""
	global, _, err := base.Active()
	if err != nil {
		return nil, err
	}
	if cfg.BackupRoot != global.BackupRoot {
		legacy = "" // Private helpers must never organize the shared legacy tree.
		if metadata != "" {
			metadata = filepath.Join(metadata, "roots", hashBytes([]byte(cfg.BackupRoot))[:32])
		}
	}
	return &Layout{Root: cfg.BackupRoot, LegacyRoot: legacy, Markers: markers, Journal: c.RecoveryRoot, MetadataDir: metadata}, nil
}

// LayoutLock lets a shell script hold its existing shared layout lock while
// the helper takes another shared lock. Exclusive install/apply blocks both.
func LayoutLock() (func(), error) {
	deployment, err := acquire("/run/backup-monitor-layout.lock", true, false)
	if err != nil {
		return nil, err
	}
	organize, err := acquire("/run/backup-monitor-organize.lock", false, false)
	if err != nil {
		deployment.Close()
		return nil, err
	}
	return func() { organize.Close(); deployment.Close() }, nil
}

type Audit struct {
	Files       int            `json:"files"`
	Bytes       int64          `json:"bytes"`
	Fingerprint string         `json:"fingerprint"`
	Counts      map[string]int `json:"counts"`
}

// AuditArchives hashes every retained/staging archive without changing names,
// bytes, mode or mtime. Used to verify helper deployments do not touch backups.
func (l *Layout) AuditArchives() (Audit, error) {
	records := []Move{}
	result := Audit{Counts: map[string]int{}}
	for _, category := range Categories {
		archives, err := l.Archives(category, false)
		if err != nil {
			return result, err
		}
		result.Counts[category] = len(archives)
		for _, a := range archives {
			info, err := l.safeArchive(a.Path)
			if err != nil {
				return result, err
			}
			sum, err := Digest(a.Path)
			if err != nil {
				return result, err
			}
			records = append(records, Move{Category: category, Day: a.Day, Source: a.Path, Size: info.Size(), MtimeNS: info.ModTime().UnixNano(), Inode: inode(info), Device: device(info), Hash: sum})
			result.Bytes += info.Size()
			result.Files++
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Source < records[j].Source })
	raw, _ := json.Marshal(records)
	result.Fingerprint = hashBytes(raw)
	return result, nil
}
func (l *Layout) VerifyManifest(path string) (Audit, error) {
	result := Audit{Counts: map[string]int{}}
	raw, err := readRegular(path)
	if err != nil {
		return result, err
	}
	var records []Move
	if err = json.Unmarshal(raw, &records); err != nil {
		return result, err
	}
	for _, m := range records {
		if !validCategory(m.Category) || !ValidDay(m.Day) {
			return result, errors.New("Invalid migration manifest")
		}
		info, err := l.safeArchive(m.Destination)
		if err != nil {
			return result, err
		}
		sum, err := Digest(m.Destination)
		if err != nil {
			return result, err
		}
		if info.Size() != m.Size || info.ModTime().UnixNano() != m.MtimeNS || sum != m.Hash {
			return result, errors.New("Migrated archive changed: " + m.Destination)
		}
		if source, err := os.Stat(m.Source); err == nil && source.ModTime().UnixNano() == m.MtimeNS {
			return result, errors.New("Original legacy source still remains")
		}
		result.Counts[m.Category]++
		result.Files++
		result.Bytes += info.Size()
	}
	result.Fingerprint = hashBytes(raw)
	return result, nil
}
