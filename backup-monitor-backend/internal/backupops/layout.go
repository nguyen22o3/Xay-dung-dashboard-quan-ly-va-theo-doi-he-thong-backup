package backupops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

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
	return &Layout{Root: cfg.BackupRoot, LegacyRoot: "/www/backup", Markers: markers, Journal: c.RecoveryRoot, MetadataDir: c.VersionMetadataDir}, nil
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
