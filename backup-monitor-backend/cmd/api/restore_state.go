package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"backup-monitor-backend/internal/backupops"
)

// Durable ownership of staged restores. Alert rules, notification queues and
// monitoring history are not stored or processed here.
const restoreStateLimit = 8 << 20

var activeRestores *restoreStore

type restoreRecord struct {
	Source         string                `json:"source"`
	Plan           backupops.RestorePlan `json:"plan"`
	StartRequested bool                  `json:"startRequested"`
}
type restoreState struct {
	Version  int                      `json:"version"`
	Restores map[string]restoreRecord `json:"restores"`
}
type restoreStore struct {
	mu    sync.Mutex
	path  string
	lock  *os.File
	state restoreState
}

func openRestoreStore(path string) (*restoreStore, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = backupops.SafeParents(path); err != nil {
		return nil, err
	}
	// Require an existing private installation directory, not arbitrary mkdirs.
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || !info.IsDir() {
		return nil, errors.New("state directory unavailable")
	}
	lock, err := lockRestoreState(path + ".lock")
	if err != nil {
		return nil, err
	}
	p := &restoreStore{path: path, lock: lock, state: restoreState{Version: 1, Restores: map[string]restoreRecord{}}}
	failed := true
	defer func() {
		if failed {
			lock.Close()
		}
	}()
	info, err = os.Lstat(path)
	if os.IsNotExist(err) {
		// Import only restore ownership from the former shared JSON file. Keep
		// that file untouched for recovery; never import or replay alerts.
		legacyPath := filepath.Join(filepath.Dir(path), "operations_state.json")
		if legacyPath != path {
			if _, e := os.Lstat(legacyPath); e == nil {
				legacyLock, e := lockRestoreState(legacyPath + ".lock")
				if e != nil {
					return nil, e
				}
				p.state, e = readRestoreState(legacyPath)
				legacyLock.Close()
				if e != nil {
					return nil, e
				}
			} else if !os.IsNotExist(e) {
				return nil, e
			}
		}
		err = p.persist(p.state)
	} else if err == nil {
		p.state, err = readRestoreState(path)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot load restore state; existing file preserved: %w", err)
	}
	failed = false
	return p, nil
}
func (s restoreState) validate() error {
	bad := errors.New("invalid restore state")
	if s.Version != 1 || s.Restores == nil || len(s.Restores) > 1000 {
		return bad
	}
	for token, r := range s.Restores {
		if token != r.Plan.Token || !validRestorePlan(r.Plan) || r.Source == "" {
			return bad
		}
	}
	return nil
}
func (p *restoreStore) persist(s restoreState) error {
	if err := backupops.SafeParents(p.path); err != nil {
		return err
	}
	if info, err := os.Lstat(p.path); err == nil && !info.Mode().IsRegular() {
		return errors.New("unsafe state target")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(raw) > restoreStateLimit {
		return errors.New("restore state full")
	}
	f, err := os.CreateTemp(filepath.Dir(p.path), ".restore-state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, p.path)
}
func (p *restoreStore) update(ctx context.Context, edit func(*restoreState) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Copy-on-write: failed writes never mutate the last confirmed in-memory state.
	raw, _ := json.Marshal(p.state)
	var next restoreState
	if err := json.Unmarshal(raw, &next); err != nil {
		return err
	}
	if err := edit(&next); err != nil {
		return err
	}
	if err := next.validate(); err != nil {
		return err
	}
	if err := p.persist(next); err != nil {
		return err
	}
	p.state = next
	return nil
}
func (p *restoreStore) snapshot() restoreState {
	p.mu.Lock()
	defer p.mu.Unlock()
	raw, _ := json.Marshal(p.state)
	var s restoreState
	_ = json.Unmarshal(raw, &s)
	return s
}
func restoreSource() string {
	cfg := backupSystemSettings()
	identity := runtimeConfig.HostIdentity
	if identity == "" {
		identity = runtimeConfig.SSH.Host
	}
	raw, _ := json.Marshal([]string{identity, cfg.BackupRoot, cfg.ScriptsDir, cfg.LogsDir, cfg.DriveRemote, cfg.DriveFolder})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func validRestoreToken(token string) bool {
	return len(token) == 32 && strings.Trim(token, "0123456789abcdef") == ""
}
func validRestorePlan(p backupops.RestorePlan) bool {
	if p.Parent != "" || p.Name != "" {
		dir, err := backupops.RestoreDestination(p.Parent, p.Name)
		if err != nil || dir != p.Directory {
			return false
		}
	}
	return validRestoreToken(p.Token) && validSnapshotPath(p.Path) && !p.CreatedAt.IsZero() && (p.Status == "preview" || p.Status == "queued" || p.Status == "running" || p.Status == "ready" || p.Status == "failed")
}
func (p *restoreStore) saveRestore(ctx context.Context, plan backupops.RestorePlan) error {
	if !validRestorePlan(plan) {
		return errors.New("invalid restore plan")
	}
	source := restoreSource()
	return p.update(ctx, func(s *restoreState) error {
		old, exists := s.Restores[plan.Token]
		if exists {
			if old.Source != source || old.Plan.Path != plan.Path || old.Plan.Directory != plan.Directory || old.Plan.Parent != plan.Parent || old.Plan.Name != plan.Name {
				return errors.New("restore source changed")
			}
			if old.Plan.Status == "ready" || old.Plan.Status == "failed" || old.Plan.Status == "running" && (plan.Status == "preview" || plan.Status == "queued") || old.Plan.Status == "queued" && plan.Status == "preview" {
				return nil
			}
		} else {
			for token, r := range s.Restores {
				age := time.Since(r.Plan.CreatedAt)
				if r.Plan.Status == "preview" && age > 24*time.Hour || (r.Plan.Status == "ready" || r.Plan.Status == "failed") && age > 30*24*time.Hour {
					delete(s.Restores, token)
				}
			}
			if len(s.Restores) >= 1000 {
				return errors.New("restore state full")
			}
		}
		s.Restores[plan.Token] = restoreRecord{Source: source, Plan: plan, StartRequested: old.StartRequested}
		return nil
	})
}
func (p *restoreStore) claimRestore(ctx context.Context, token string) error {
	source := restoreSource()
	return p.update(ctx, func(s *restoreState) error {
		r, ok := s.Restores[token]
		if !ok || r.Source != source || r.Plan.Status != "preview" || r.StartRequested || time.Since(r.Plan.CreatedAt) > 10*time.Minute {
			return errors.New("restore unavailable")
		}
		r.StartRequested = true
		s.Restores[token] = r
		return nil
	})
}
func (p *restoreStore) knowsRestore(token string) bool {
	r, ok := p.snapshot().Restores[token]
	return ok && r.Source == restoreSource()
}
func initializeRestoreState() error {
	path := strings.TrimSpace(os.Getenv("RESTORE_STATE_FILE"))
	if path == "" {
		path = "restore_state.json"
	}
	p, err := openRestoreStore(path)
	if err != nil {
		return err
	}
	activeRestores = p
	return nil
}

func readRestoreState(path string) (restoreState, error) {
	var state restoreState
	if err := backupops.SafeParents(path); err != nil {
		return state, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() || info.Size() > restoreStateLimit {
		return state, errors.New("unsafe or oversized restore state")
	}
	file, err := os.Open(path)
	if err != nil {
		return state, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, restoreStateLimit+1))
	if err != nil || len(raw) > restoreStateLimit {
		return state, errors.New("cannot read restore state")
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	return state, state.validate()
}
