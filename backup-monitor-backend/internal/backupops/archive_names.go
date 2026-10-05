package backupops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

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
