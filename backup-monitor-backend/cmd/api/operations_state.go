package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"backup-monitor-backend/internal/backupops"
)

// The single installation owns this file for its entire lifetime. No account
// credentials, raw task logs, metrics history or database dumps are stored here.
const stateLimit = 8 << 20

var errNoDelivery = errors.New("no pending delivery")
var activeOperations *operationStore

type monitorOptions struct {
	Enabled        bool `json:"enabled"`
	BackupFailed   bool `json:"backupFailed"`
	DriveFailed    bool `json:"driveFailed"`
	MissedSchedule bool `json:"missedSchedule"`
	DiskThreshold  int  `json:"diskThreshold"`
	GraceMinutes   int  `json:"graceMinutes"`
}

func defaultMonitorOptions() monitorOptions {
	return monitorOptions{BackupFailed: true, DriveFailed: true, MissedSchedule: true, DiskThreshold: 90, GraceMinutes: 30}
}
func (o monitorOptions) validate() error {
	if o.DiskThreshold < 50 || o.DiskThreshold > 100 || o.GraceMinutes < 5 || o.GraceMinutes > 1440 {
		return errors.New("invalid monitor options")
	}
	return nil
}

type delivery struct {
	ID          int64     `json:"id"`
	Channel     string    `json:"channel"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"lastError"`
	NextAttempt time.Time `json:"nextAttempt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Message     string    `json:"-"`
}
type alertEvent struct {
	ID         int64      `json:"id"`
	Key        string     `json:"key"`
	Message    string     `json:"message"`
	ObservedAt time.Time  `json:"observedAt"`
	Kind       string     `json:"kind"`
	Deliveries []delivery `json:"deliveries"`
}
type restoreRecord struct {
	Source         string                `json:"source"`
	Plan           backupops.RestorePlan `json:"plan"`
	StartRequested bool                  `json:"startRequested"`
}
type operationsState struct {
	Version          int                      `json:"version"`
	NextID           int64                    `json:"nextId"`
	Options          monitorOptions           `json:"options"`
	EnabledAt        time.Time                `json:"enabledAt"`
	CollectedAt      *time.Time               `json:"collectedAt"`
	CollectionFailed bool                     `json:"collectionFailed"`
	Alerts           []alertEvent             `json:"alerts"`
	Seen             map[string]time.Time     `json:"seen"`
	Restores         map[string]restoreRecord `json:"restores"`
}
type operationStore struct {
	mu       sync.Mutex
	path     string
	lock     *os.File
	state    operationsState
	worker   bool
	location *time.Location
	wg       sync.WaitGroup
}

func openOperations(path string) (*operationStore, error) {
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
	lock, err := lockOperations(path + ".lock")
	if err != nil {
		return nil, err
	}
	p := &operationStore{path: path, lock: lock, state: operationsState{Version: 1, NextID: 1, Options: defaultMonitorOptions(), Alerts: []alertEvent{}, Seen: map[string]time.Time{}, Restores: map[string]restoreRecord{}}}
	failed := true
	defer func() {
		if failed {
			lock.Close()
		}
	}()
	info, err = os.Lstat(path)
	if os.IsNotExist(err) {
		err = p.persist(p.state)
	} else if err == nil {
		if !info.Mode().IsRegular() || info.Size() > stateLimit {
			return nil, errors.New("unsafe or oversized operations state")
		}
		file, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		raw, e := io.ReadAll(io.LimitReader(file, stateLimit+1))
		file.Close()
		if e != nil || len(raw) > stateLimit {
			return nil, errors.New("cannot read operations state")
		}
		var loaded operationsState
		err = json.Unmarshal(raw, &loaded)
		if err == nil {
			err = loaded.validate()
		}
		if err == nil {
			p.state = loaded
		}
	}
	if err != nil {
		return nil, errors.New("cannot load operations state; existing file preserved")
	}
	failed = false
	return p, nil
}
func (s operationsState) validate() error {
	bad := errors.New("invalid operations state")
	if s.Version != 1 || s.NextID < 1 || s.Options.validate() != nil || s.Seen == nil || s.Restores == nil || len(s.Alerts) > 2000 || len(s.Seen) > 50000 || len(s.Restores) > 1000 {
		return bad
	}
	ids := map[int64]bool{}
	for _, a := range s.Alerts {
		if a.ID < 1 || a.ID >= s.NextID || ids[a.ID] || a.Key == "" || len(a.Message) > 4000 || a.ObservedAt.IsZero() {
			return bad
		}
		ids[a.ID] = true
		channels := map[string]bool{}
		for _, d := range a.Deliveries {
			if d.ID < 1 || d.ID >= s.NextID || ids[d.ID] || channels[d.Channel] || !validChannel(d.Channel) || d.Attempts < 0 || d.Attempts > 5 {
				return bad
			}
			if d.Status != "pending" && d.Status != "sending" && d.Status != "sent" && d.Status != "failed" {
				return bad
			}
			ids[d.ID] = true
			channels[d.Channel] = true
		}
	}
	for token, r := range s.Restores {
		if token != r.Plan.Token || !validRestorePlan(r.Plan) || r.Source == "" {
			return bad
		}
	}
	return nil
}
func (p *operationStore) persist(s operationsState) error {
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
	if len(raw) > stateLimit {
		return errors.New("operations state full")
	}
	f, err := os.CreateTemp(filepath.Dir(p.path), ".operations-state-*")
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
func (p *operationStore) update(ctx context.Context, edit func(*operationsState) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Copy-on-write: failed writes never mutate the last confirmed in-memory state.
	raw, _ := json.Marshal(p.state)
	var next operationsState
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
func (p *operationStore) snapshot() operationsState {
	p.mu.Lock()
	defer p.mu.Unlock()
	raw, _ := json.Marshal(p.state)
	var s operationsState
	_ = json.Unmarshal(raw, &s)
	return s
}
func operationSource() string {
	cfg := backupSystemSettings()
	raw, _ := json.Marshal([]string{runtimeConfig.SSH.Host, cfg.BackupRoot, cfg.ScriptsDir, cfg.LogsDir, cfg.DriveRemote, cfg.DriveFolder})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func validChannel(ch string) bool { return ch == "telegram" || ch == "discord" || ch == "email" }
func (p *operationStore) saveOptions(ctx context.Context, o monitorOptions) error {
	if err := o.validate(); err != nil {
		return err
	}
	return p.update(ctx, func(s *operationsState) error {
		if o.Enabled && !s.Options.Enabled {
			s.EnabledAt = time.Now().UTC()
		}
		s.Options = o
		return nil
	})
}
func (p *operationStore) enqueue(ctx context.Context, key, message string, channels []string) error {
	if key == "" || len(key) > 512 || len(message) > 4000 {
		return errors.New("invalid alert")
	}
	sourceKey := operationSource() + ":" + key
	return p.update(ctx, func(s *operationsState) error {
		now := time.Now().UTC()
		if _, exists := s.Seen[sourceKey]; exists {
			return nil
		}
		// Bound closed history. Never discard pending/sending deliveries.
		if len(s.Alerts) >= 1000 {
			for i, a := range s.Alerts {
				closed := true
				for _, d := range a.Deliveries {
					if d.Status == "pending" || d.Status == "sending" {
						closed = false
					}
				}
				if closed {
					s.Alerts = append(s.Alerts[:i], s.Alerts[i+1:]...)
					break
				}
			}
		}
		if len(s.Alerts) >= 2000 {
			return errors.New("notification queue full")
		}
		for k, date := range s.Seen {
			if date.Before(now.AddDate(0, 0, -90)) {
				delete(s.Seen, k)
			}
		}
		if len(s.Seen) >= 50000 {
			return errors.New("deduplication state full")
		}
		a := alertEvent{ID: s.NextID, Key: sourceKey, Message: message, ObservedAt: now, Kind: "alert", Deliveries: []delivery{}}
		s.NextID++
		if strings.HasPrefix(key, "test:") {
			a.Kind = "test"
		}
		seen := map[string]bool{}
		for _, ch := range channels {
			if !validChannel(ch) {
				return errors.New("invalid channel")
			}
			if seen[ch] {
				continue
			}
			seen[ch] = true
			a.Deliveries = append(a.Deliveries, delivery{ID: s.NextID, Channel: ch, Status: "pending", NextAttempt: now, UpdatedAt: now})
			s.NextID++
		}
		s.Seen[sourceKey] = now
		s.Alerts = append(s.Alerts, a)
		return nil
	})
}
func (p *operationStore) claimDelivery(ctx context.Context) (delivery, error) {
	var result delivery
	prefix := operationSource() + ":"
	err := p.update(ctx, func(s *operationsState) error {
		now := time.Now().UTC()
		for i := range s.Alerts {
			if !strings.HasPrefix(s.Alerts[i].Key, prefix) {
				continue
			}
			for j := range s.Alerts[i].Deliveries {
				d := &s.Alerts[i].Deliveries[j]
				due := d.Status == "pending" && !d.NextAttempt.After(now) || d.Status == "sending" && d.UpdatedAt.Before(now.Add(-2*time.Minute))
				if !due {
					continue
				}
				if d.Attempts >= 5 {
					d.Status = "failed"
					d.LastError = "Lần gửi cuối bị gián đoạn; xác minh trước khi gửi lại"
					d.UpdatedAt = now
					continue
				}
				d.Status = "sending"
				d.Attempts++
				d.UpdatedAt = now
				result = *d
				result.Message = s.Alerts[i].Message
				return nil
			}
		}
		return nil
	})
	if err == nil && result.ID == 0 {
		err = errNoDelivery
	}
	return result, err
}
func (p *operationStore) finishDelivery(ctx context.Context, claimed delivery, sendErr error) error {
	return p.update(ctx, func(s *operationsState) error {
		for i := range s.Alerts {
			for j := range s.Alerts[i].Deliveries {
				d := &s.Alerts[i].Deliveries[j]
				if d.ID != claimed.ID {
					continue
				}
				if d.Status != "sending" || d.Attempts != claimed.Attempts {
					return errors.New("delivery lease changed")
				}
				d.Status = "sent"
				d.LastError = ""
				d.UpdatedAt = time.Now().UTC()
				if sendErr != nil {
					d.Status = "pending"
					if d.Attempts >= 5 {
						d.Status = "failed"
					}
					d.LastError = "Không gửi được; kiểm tra cấu hình kênh và kết nối mạng"
					d.NextAttempt = d.UpdatedAt.Add(time.Duration(1<<min(d.Attempts, 6)) * time.Minute)
				}
				return nil
			}
		}
		return errors.New("delivery missing")
	})
}
func (p *operationStore) retryDelivery(ctx context.Context, id int64) error {
	prefix := operationSource() + ":"
	return p.update(ctx, func(s *operationsState) error {
		for i := range s.Alerts {
			if !strings.HasPrefix(s.Alerts[i].Key, prefix) {
				continue
			}
			for j := range s.Alerts[i].Deliveries {
				d := &s.Alerts[i].Deliveries[j]
				if d.ID == id && d.Status == "failed" {
					d.Status = "pending"
					d.Attempts = 0
					d.LastError = ""
					d.NextAttempt = time.Now().UTC()
					return nil
				}
			}
		}
		return errors.New("not a failed delivery")
	})
}
func validRestoreToken(token string) bool {
	return len(token) == 32 && strings.Trim(token, "0123456789abcdef") == ""
}
func validRestorePlan(p backupops.RestorePlan) bool {
	return validRestoreToken(p.Token) && validSnapshotPath(p.Path) && !p.CreatedAt.IsZero() && (p.Status == "preview" || p.Status == "queued" || p.Status == "running" || p.Status == "ready" || p.Status == "failed")
}
func (p *operationStore) saveRestore(ctx context.Context, plan backupops.RestorePlan) error {
	if !validRestorePlan(plan) {
		return errors.New("invalid restore plan")
	}
	source := operationSource()
	return p.update(ctx, func(s *operationsState) error {
		old, exists := s.Restores[plan.Token]
		if exists {
			if old.Source != source || old.Plan.Path != plan.Path {
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
func (p *operationStore) claimRestore(ctx context.Context, token string) error {
	source := operationSource()
	return p.update(ctx, func(s *operationsState) error {
		r, ok := s.Restores[token]
		if !ok || r.Source != source || r.Plan.Status != "preview" || r.StartRequested || time.Since(r.Plan.CreatedAt) > 10*time.Minute {
			return errors.New("restore unavailable")
		}
		r.StartRequested = true
		s.Restores[token] = r
		return nil
	})
}
func (p *operationStore) knowsRestore(token string) bool {
	r, ok := p.snapshot().Restores[token]
	return ok && r.Source == operationSource()
}
func initializeOperations(ctx context.Context) error {
	path := strings.TrimSpace(os.Getenv("OPERATIONS_STATE_FILE"))
	if path == "" {
		path = "operations_state.json"
	}
	p, err := openOperations(path)
	if err != nil {
		return err
	}
	worker := strings.TrimSpace(os.Getenv("OPERATIONS_WORKER_ENABLED"))
	if worker == "" {
		worker = "true"
	}
	if worker != "true" && worker != "false" {
		p.lock.Close()
		return errors.New("OPERATIONS_WORKER_ENABLED must be true or false")
	}
	p.worker = worker == "true"
	zone := strings.TrimSpace(os.Getenv("MONITOR_TIMEZONE"))
	if zone == "" {
		zone = "Asia/Ho_Chi_Minh"
	}
	p.location, err = time.LoadLocation(zone)
	if err != nil {
		p.lock.Close()
		return errors.New("invalid MONITOR_TIMEZONE")
	}
	activeOperations = p
	if p.worker {
		startOperations(ctx, p)
	}
	return nil
}
