package backupops

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type metadataMove struct {
	Day         string `json:"day"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	SHA256      string `json:"sha256"`
	Entries     int    `json:"entries"`
	raw         []byte
	merged      VersionLedger
	info        os.FileInfo
}

func equalVersionLedgers(a, b VersionLedger) bool {
	if a.Format != b.Format || len(a.Origins) != len(b.Origins) {
		return false
	}
	for key, value := range a.Origins {
		if b.Origins[key] != value {
			return false
		}
	}
	return true
}

// Explicit migration: verify every private copy before removing any legacy JSON.
// Managed job locks prevent origin updates while copying and removing files.
func (c *Controller) MigrateVersionMetadata(apply bool, out io.Writer) (resultErr error) {
	if apply {
		locks, err := acquireAll(c.Locks)
		if err != nil {
			return err
		}
		defer releaseAll(locks)
	}
	l, err := c.Layout()
	if err != nil {
		return err
	}
	if _, err = l.versionLedgerPath("2026-01-01"); err != nil {
		return err
	}
	days, err := os.ReadDir(l.Root)
	if err != nil {
		return err
	}
	moves := []metadataMove{}
	for _, entry := range days {
		day := entry.Name()
		if !ValidDay(day) {
			continue
		}
		source := filepath.Join(l.Root, day, VersionLedgerName)
		if err = SafeParents(source); err != nil {
			return err
		}
		info, err := os.Stat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		legacy, err := readVersionLedgerFile(day, source)
		if err != nil {
			return err
		}
		destination, err := l.versionLedgerPath(day)
		if err != nil {
			return err
		}
		central, err := readVersionLedgerFile(day, destination)
		if err != nil {
			return err
		}
		merged, err := mergeVersionLedgers(central, legacy)
		if err != nil {
			return err
		}
		raw, err := readRegular(source)
		if err != nil {
			return err
		}
		moves = append(moves, metadataMove{Day: day, Source: source, Destination: destination, SHA256: hashBytes(raw), Entries: len(legacy.Origins), raw: raw, merged: merged, info: info})
	}
	fmt.Fprintf(out, "METADATA_PREVIEW files=%d store=%s\n", len(moves), l.MetadataDir)
	if !apply {
		for _, m := range moves {
			fmt.Fprintf(out, "MOVE_METADATA %s -> %s entries=%d\n", m.Source, m.Destination, m.Entries)
		}
		return nil
	}
	if len(moves) == 0 {
		fmt.Fprintln(out, "No legacy JSON remains in backup folders.")
		return nil
	}
	if err = mkdirPrivate(c.RecoveryRoot); err != nil {
		return err
	}
	recovery, err := os.MkdirTemp(c.RecoveryRoot, "before-metadata-store-")
	if err != nil {
		return err
	}
	manifest, _ := json.MarshalIndent(moves, "", "  ")
	if err = AtomicWrite(filepath.Join(recovery, "moves.json"), manifest, 0600); err != nil {
		return err
	}
	for _, m := range moves {
		if err = AtomicWrite(filepath.Join(recovery, m.Day+".source.json"), m.raw, 0600); err != nil {
			return err
		}
		before, err := readRegular(m.Destination)
		if err == nil {
			if err = AtomicWrite(filepath.Join(recovery, m.Day+".central.before.json"), before, 0600); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	// Source files are retained until all copied records have been verified.
	for _, m := range moves {
		if err = l.writeVersionLedger(m.Day, m.merged); err != nil {
			return err
		}
		copied, err := readVersionLedgerFile(m.Day, m.Destination)
		if err != nil || !equalVersionLedgers(copied, m.merged) {
			return errors.New("Private metadata verification failed; legacy JSON retained")
		}
	}
	for _, m := range moves {
		current, err := readRegular(m.Source)
		info, statErr := os.Stat(m.Source)
		if err != nil || statErr != nil || !bytes.Equal(current, m.raw) || !os.SameFile(info, m.info) || !info.ModTime().Equal(m.info.ModTime()) {
			return errors.New("Legacy JSON changed; refusing removal")
		}
	}
	removed := []metadataMove{}
	defer func() {
		if resultErr == nil {
			return
		}
		cause := resultErr
		failed := false
		for _, m := range removed {
			if err := copyExclusive(filepath.Join(recovery, m.Day+".source.json"), m.Source); err != nil {
				failed = true
			}
		}
		if failed {
			resultErr = fmt.Errorf("%w; restore needs review: %s", cause, recovery)
		} else {
			resultErr = fmt.Errorf("%w; removed source JSON restored; recovery=%s", cause, recovery)
		}
	}()
	for _, m := range moves {
		if err = SafeParents(m.Source); err != nil {
			return err
		}
		current, err := readRegular(m.Source)
		if err != nil || hashBytes(current) != m.SHA256 {
			return errors.New("Legacy JSON changed before removal")
		}
		if err = os.Remove(m.Source); err != nil {
			return err
		}
		removed = append(removed, m)
		fmt.Fprintf(out, "MOVED_METADATA day=%s entries=%d destination=%s\n", m.Day, m.Entries, m.Destination)
	}
	fmt.Fprintf(out, "METADATA_APPLIED files=%d store=%s recovery=%s; no archives, cron or Drive changed\n", len(moves), l.MetadataDir, recovery)
	return nil
}
