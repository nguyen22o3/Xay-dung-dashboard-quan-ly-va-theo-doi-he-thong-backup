package backupops

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
)

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
