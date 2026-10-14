package backupops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Files are staged without removing the source. Source deletion happens only
// after scripts, crontab and settings commit, so failed copies cannot lose data.
type archiveTransfer struct {
	Source, Target, Hash string
	Size                 int64
}
type ledgerTransfer struct {
	Target            string
	Original, Updated []byte
}

// Older website scripts used a six-character mktemp suffix instead of _site.
// This is migration-only: do not infer cron/manual origin or enable rotation.
var legacyTransferSiteName = regexp.MustCompile(`^web_([A-Za-z0-9_.-]+)_(20[0-9]{6})_([0-9]{6})_[A-Za-z0-9]{6}\.tar\.gz$`)

func legacyTransferVersion(category, root string, archive Archive) (ArchiveVersion, bool) {
	v := ArchiveVersion{Category: category}
	if category != "site" {
		return v, false
	}
	m := legacyTransferSiteName.FindStringSubmatch(filepath.Base(archive.Path))
	if m == nil || filepath.Base(filepath.Dir(archive.Path)) != m[1] {
		return v, false
	}
	stamp, err := time.Parse("20060102 150405", m[2]+" "+m[3])
	if err != nil {
		return v, false
	}
	v.Entity, v.Day, v.Stamp = m[1], stamp.Format("2006-01-02"), stamp.Format("2006-01-02 150405")
	if v.Day != archive.Day || (!within(archive.Path, filepath.Join(root, v.Day, category)) && !within(archive.Path, filepath.Join(root, category))) {
		return v, false
	}
	return v, true
}

func migrationLayout(c *Controller, global Settings, root string) *Layout {
	metadata := c.VersionMetadataDir
	if metadata != "" && root != global.BackupRoot {
		metadata = filepath.Join(metadata, "roots", hashBytes([]byte(root))[:32])
	}
	return &Layout{Root: root, MetadataDir: metadata}
}

func (c *Controller) planCronBackupTransfer(p *Proposal, change ScheduleChange, oldLocal, nextLocal Settings) error {
	category := map[string]string{"backup-site": "site", "backup-database": "database", "backup-panel": "panel"}[change.ID]
	if category == "" {
		return errors.New("Chỉ cron sao lưu website, database và aaPanel được chuyển backup cũ")
	}
	if within(oldLocal.BackupRoot, nextLocal.BackupRoot) || within(nextLocal.BackupRoot, oldLocal.BackupRoot) {
		return errors.New("Nơi lưu mới không được nằm trong hoặc chứa nơi lưu cũ")
	}
	// Old archive names record source/category, not the schedule instance. Never
	// guess ownership if another same-category cron uses this root (paused too).
	for key, loc := range p.Old.CronLocations {
		if key == change.InstanceKey || loc.ID != change.ID {
			continue
		}
		other, err := CronSettings(p.Old, loc.ID, key)
		if err != nil {
			return err
		}
		if other.BackupRoot == oldLocal.BackupRoot {
			return errors.New("Nhiều cron cùng loại dùng chung kho; chưa xác định được backup riêng để chuyển an toàn")
		}
	}
	selectedSkipped := false
	if p.Old.BackupRoot == oldLocal.BackupRoot {
		for _, line := range strings.Split(string(p.Cron), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, pausedSchedulePrefix) {
				line = strings.TrimPrefix(line, pausedSchedulePrefix)
			} else if strings.HasPrefix(line, "#") {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 7 || f[5] != "/bin/bash" {
				continue
			}
			direct := f[6] == filepath.Join(p.Old.ScriptsDir, TaskScripts[change.ID])
			wrapped := len(f) >= 8 && f[6] == filepath.Join(p.Old.ScriptsDir, "backup-monitor-run.sh") && f[7] == change.ID
			if !direct && !wrapped {
				continue
			}
			if change.InstanceKey == "" && !selectedSkipped && strings.Join(f[:5], " ") == change.ExpectedSchedule {
				selectedSkipped = true
				continue
			}
			return errors.New("Kho còn được cron cùng loại khác sử dụng; không chuyển backup dùng chung")
		}
	}
	p.Summary.BackupMigration = true
	if _, err := os.Stat(oldLocal.BackupRoot); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	oldLayout := migrationLayout(c, p.Old, oldLocal.BackupRoot)
	newLayout := migrationLayout(c, p.Old, nextLocal.BackupRoot)
	archives, err := oldLayout.Archives(category, false)
	if err != nil {
		return err
	}
	days := map[string]bool{}
	targets := map[string]bool{}
	ledgers := map[string]VersionLedger{}
	source := effectiveSource(oldLocal, change.ID)
	for _, archive := range archives {
		v, ok := ParseVersion(category, filepath.Base(archive.Path))
		if !ok {
			v, ok = cleanVersion(category, filepath.Base(archive.Path))
		}
		if !ok {
			v, ok = legacyTransferVersion(category, oldLocal.BackupRoot, archive)
		}
		if category == "panel" && !ok {
			v = ArchiveVersion{Category: category, Entity: "aaPanel", Day: archive.Day}
			ok = true
		}
		if !ok {
			return fmt.Errorf("Backup chưa xác định được nguồn hoặc định dạng: %s; chưa chuyển dữ liệu", filepath.Base(archive.Path))
		}
		if category != "panel" && source.Scope == "folder" && v.Entity != filepath.Base(source.Path) {
			continue
		}
		target := filepath.Join(nextLocal.BackupRoot, archive.Day, category, archive.Relative)
		if !within(target, nextLocal.BackupRoot) || SafeParents(target) != nil || exists(target) || targets[target] {
			return errors.New("File backup đích đã tồn tại hoặc đường dẫn không an toàn; không ghi đè")
		}
		targets[target] = true
		info, err := oldLayout.safeArchive(archive.Path)
		if err != nil {
			return err
		}
		sum, err := Digest(archive.Path)
		if err != nil {
			return err
		}
		p.Archives = append(p.Archives, archiveTransfer{archive.Path, target, sum, info.Size()})
		p.Summary.FilesToMove++
		p.Summary.BytesToMove += info.Size()
		days[archive.Day] = true
		if oldLayout.MetadataDir == "" {
			continue
		}
		ledger, err := oldLayout.readVersionLedger(archive.Day)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Join(oldLocal.BackupRoot, archive.Day), archive.Path)
		if err != nil {
			return err
		}
		origin := ledger.Origins[filepath.ToSlash(rel)]
		if origin == "" {
			continue
		} // Untagged legacy versions remain untagged.
		merged, loaded := ledgers[archive.Day]
		if !loaded {
			merged, err = newLayout.readVersionLedger(archive.Day)
			if err != nil {
				return err
			}
		}
		rel, err = filepath.Rel(filepath.Join(nextLocal.BackupRoot, archive.Day), target)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if previous := merged.Origins[rel]; previous != "" && previous != origin {
			return errors.New("Metadata backup đích bị xung đột")
		}
		merged.Origins[rel] = origin
		ledgers[archive.Day] = merged
	}
	p.Summary.DaysToMove = len(days)
	// Stable order is required for preview-token revalidation.
	for _, archive := range archives {
		ledger, ok := ledgers[archive.Day]
		if !ok {
			continue
		}
		delete(ledgers, archive.Day)
		path, err := newLayout.versionLedgerPath(archive.Day)
		if err != nil {
			return err
		}
		var previous []byte
		if exists(path) {
			previous, err = readRegular(path)
			if err != nil {
				return err
			}
		}
		raw, err := json.MarshalIndent(ledger, "", "  ")
		if err != nil {
			return err
		}
		p.Ledgers = append(p.Ledgers, ledgerTransfer{path, previous, append(raw, '\n')})
	}
	return nil
}

func verifyTransferFile(path string, move archiveTransfer) error {
	if err := SafeParents(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != move.Size {
		return errors.New("Backup đã thay đổi trong lúc chuyển")
	}
	sum, err := Digest(path)
	if err != nil {
		return err
	}
	if sum != move.Hash {
		return errors.New("Checksum backup không khớp; giữ dữ liệu gốc")
	}
	return nil
}

func stageBackupTransfer(move archiveTransfer) (resultErr error) {
	verifiedDestination := false
	if err := verifyTransferFile(move.Source, move); err != nil {
		return err
	}
	if err := mkdirPrivate(filepath.Dir(move.Target)); err != nil {
		return err
	}
	// Hard-link on one filesystem; streamed, exclusive copy across disks.
	linked := os.Link(move.Source, move.Target) == nil
	if !linked {
		source, err := openRegular(move.Source)
		if err != nil {
			return err
		}
		defer source.Close()
		target, err := os.OpenFile(move.Target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer func() {
			if resultErr != nil && !verifiedDestination {
				_ = os.Remove(move.Target)
			}
		}()
		_, err = io.Copy(target, source)
		if err == nil {
			err = target.Sync()
		}
		closeErr := target.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		info, err := source.Stat()
		if err != nil {
			return err
		}
		if err = os.Chtimes(move.Target, info.ModTime(), info.ModTime()); err != nil {
			return err
		}
	} else {
		defer func() {
			if resultErr != nil && !verifiedDestination {
				_ = os.Remove(move.Target)
			}
		}()
	}
	if err := verifyTransferFile(move.Target, move); err != nil {
		return err
	}
	verifiedDestination = true
	if err := verifyTransferFile(move.Source, move); err != nil {
		return errors.New("Nguồn backup thay đổi; giữ bản đã kiểm tra để khôi phục: " + move.Target)
	}
	return nil
}
