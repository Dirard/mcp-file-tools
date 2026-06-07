package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dimitar-grigorov/mcp-file-tools/internal/config"
	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

const (
	cwdStateLockTimeout = 2 * time.Second
	cwdStateLockRetry   = 25 * time.Millisecond
)

type cwdEntry struct {
	ID        int64
	Abs       string
	Out       string
	Canonical string
	ExpiresAt time.Time
}

type cwdRegistry struct {
	mu            sync.Mutex
	entries       map[int64]cwdEntry
	canonicalToID map[string]int64
	retiredIDs    map[int64]struct{}
	maxSeenID     int64
	ttl           time.Duration
	allocator     *cwdAllocator
}

type cwdAllocator struct {
	statePath string
	guardPath string
	lockPath  string
	unhealthy bool
	reason    string
}

func newCwdRegistry(cfg *config.Config) *cwdRegistry {
	ttlSeconds := config.DefaultCwdTTLSeconds
	if cfg != nil && cfg.CwdTTLSeconds > 0 {
		ttlSeconds = cfg.CwdTTLSeconds
	}
	registry := &cwdRegistry{
		entries:       map[int64]cwdEntry{},
		canonicalToID: map[string]int64{},
		retiredIDs:    map[int64]struct{}{},
		ttl:           time.Duration(ttlSeconds) * time.Second,
		allocator:     newCwdAllocator(cfg),
	}
	return registry
}

func newCwdAllocator(cfg *config.Config) *cwdAllocator {
	allocator := &cwdAllocator{}
	if cfg == nil {
		allocator.markUnhealthy("cwd state configuration is unavailable")
		return allocator
	}
	if strings.TrimSpace(cfg.CwdStateConfigError) != "" {
		allocator.markUnhealthy(cfg.CwdStateConfigError)
		return allocator
	}
	if strings.TrimSpace(cfg.CwdStatePath) == "" {
		allocator.markUnhealthy("cwd state path is unavailable")
		return allocator
	}
	statePath := filepath.Clean(cfg.CwdStatePath)
	allocator.statePath = statePath
	allocator.guardPath = statePath + ".guard"
	allocator.lockPath = statePath + ".lock"
	return allocator
}

func (r *cwdRegistry) register(ctx context.Context, abs string, display string) (int64, *CwdError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(time.Now())
	if r.allocator == nil || r.allocator.unhealthy {
		return 0, cwdStateUnavailableError(r.allocatorReason())
	}
	canonical := canonicalCwdKey(abs)
	if id, ok := r.canonicalToID[canonical]; ok {
		entry := r.entries[id]
		entry.ExpiresAt = time.Now().Add(r.ttl)
		r.entries[id] = entry
		return id, nil
	}
	id, err := r.allocator.reserveNext(ctx, r.maxSeenID, func(candidate int64) bool {
		if _, ok := r.entries[candidate]; ok {
			return true
		}
		_, retired := r.retiredIDs[candidate]
		return retired
	})
	if err != nil {
		return 0, cwdStateUnavailableError(err.Error())
	}
	entry := cwdEntry{
		ID:        id,
		Abs:       filepath.Clean(abs),
		Out:       slashPath(display),
		Canonical: canonical,
		ExpiresAt: time.Now().Add(r.ttl),
	}
	r.entries[id] = entry
	r.canonicalToID[canonical] = id
	if id > r.maxSeenID {
		r.maxSeenID = id
	}
	return id, nil
}

func (r *cwdRegistry) lookup(id int64) (cwdEntry, *CwdError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.expireLocked(now)
	entry, ok := r.entries[id]
	if !ok {
		code := "cwd_id_unknown"
		message := "cwd_id is unknown; call set_cwd again"
		reason := "The cwd_id was not active in this server process; register the directory again."
		if _, expired := r.retiredIDs[id]; expired {
			code = "cwd_id_expired"
			message = "cwd_id is expired; call set_cwd again"
			reason = "The cwd_id expired in this server process; register the directory again."
		}
		return cwdEntry{}, &CwdError{
			Code:    code,
			Message: message,
			CwdID:   int64Ptr(id),
			Hint:    staleCwdHint(reason),
		}
	}
	return entry, nil
}

func (r *cwdRegistry) allocatorReason() string {
	if r == nil || r.allocator == nil || r.allocator.reason == "" {
		return "cwd state allocator is unavailable"
	}
	return r.allocator.reason
}

func (r *cwdRegistry) expireLocked(now time.Time) {
	for id, entry := range r.entries {
		if now.Before(entry.ExpiresAt) {
			continue
		}
		delete(r.entries, id)
		delete(r.canonicalToID, entry.Canonical)
		r.retiredIDs[id] = struct{}{}
	}
}

func (a *cwdAllocator) reserveNext(ctx context.Context, maxSeenID int64, isUsed func(int64) bool) (int64, error) {
	if a == nil || a.unhealthy {
		return 0, fmt.Errorf("cwd_state_unavailable: %s", allocatorReason(a))
	}
	if err := contextError(ctx); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(a.statePath), 0o700); err != nil {
		a.markUnhealthy("cwd state directory cannot be created")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state directory cannot be created")
	}
	lock := flock.New(a.lockPath)
	lockCtx, cancel := context.WithTimeout(ctx, cwdStateLockTimeout)
	defer cancel()
	locked, err := lock.TryLockContext(lockCtx, cwdStateLockRetry)
	if err != nil {
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state lock timed out")
	}
	if !locked {
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state lock unavailable")
	}
	defer lock.Unlock()

	if err := a.ensureStateBundleLocked(maxSeenID); err != nil {
		a.markUnhealthy(err.Error())
		return 0, fmt.Errorf("cwd_state_unavailable: %s", err.Error())
	}
	guardUUID, err := readSmallTextFile(a.guardPath)
	if err != nil {
		a.markUnhealthy("cwd state guard cannot be read")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state guard cannot be read")
	}
	db, err := sql.Open("sqlite", a.statePath)
	if err != nil {
		a.markUnhealthy("cwd state database cannot be opened")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state database cannot be opened")
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=DELETE`); err != nil {
		a.markUnhealthy("cwd state journal mode cannot be set")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state journal mode cannot be set")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		a.markUnhealthy("cwd state transaction cannot start")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state transaction cannot start")
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	var dbUUID string
	var lastIssued int64
	if err := tx.QueryRowContext(ctx, `SELECT state_uuid, last_issued FROM cwd_state WHERE id = 1`).Scan(&dbUUID, &lastIssued); err != nil {
		a.markUnhealthy("cwd state row cannot be read")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state row cannot be read")
	}
	if strings.TrimSpace(dbUUID) == "" || dbUUID != strings.TrimSpace(guardUUID) {
		a.markUnhealthy("cwd state guard does not match database")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state guard does not match database")
	}
	if lastIssued >= maxCwdID {
		a.markUnhealthy("cwd state id space is exhausted")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state id space is exhausted")
	}
	candidate := lastIssued + 1
	if candidate <= maxSeenID || (isUsed != nil && isUsed(candidate)) {
		a.markUnhealthy("cwd state allocator would collide with remembered ids")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state allocator would collide with remembered ids")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cwd_state SET last_issued = ? WHERE id = 1`, candidate); err != nil {
		a.markUnhealthy("cwd state allocator cannot advance")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state allocator cannot advance")
	}
	if err := tx.Commit(); err != nil {
		a.markUnhealthy("cwd state allocator commit failed")
		return 0, fmt.Errorf("cwd_state_unavailable: cwd state allocator commit failed")
	}
	rollback = false
	return candidate, nil
}

func (a *cwdAllocator) ensureStateBundleLocked(maxSeenID int64) error {
	if strings.TrimSpace(a.statePath) == "" || strings.TrimSpace(a.guardPath) == "" {
		return fmt.Errorf("cwd state path is unavailable")
	}
	stateExists := fileExists(a.statePath)
	guardExists := fileExists(a.guardPath)
	if !stateExists && !guardExists {
		if maxSeenID > 0 {
			return fmt.Errorf("cwd state bundle disappeared while ids are remembered")
		}
		return a.initializeStateBundleLocked()
	}
	if stateExists != guardExists {
		return fmt.Errorf("cwd state bundle is incomplete")
	}
	return nil
}

func (a *cwdAllocator) initializeStateBundleLocked() error {
	if err := os.MkdirAll(filepath.Dir(a.statePath), 0o700); err != nil {
		return fmt.Errorf("cwd state directory cannot be created")
	}
	uuid, err := randomStateUUID()
	if err != nil {
		return fmt.Errorf("cwd state uuid cannot be generated")
	}
	db, err := sql.Open("sqlite", a.statePath)
	if err != nil {
		return fmt.Errorf("cwd state database cannot be created")
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		return fmt.Errorf("cwd state journal mode cannot be set")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS cwd_state (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		state_uuid TEXT NOT NULL,
		last_issued INTEGER NOT NULL CHECK(last_issued >= 0 AND last_issued <= 9007199254740991)
	)`); err != nil {
		return fmt.Errorf("cwd state table cannot be created")
	}
	if _, err := db.Exec(`INSERT INTO cwd_state (id, state_uuid, last_issued) VALUES (1, ?, 0)`, uuid); err != nil {
		return fmt.Errorf("cwd state row cannot be initialized")
	}
	if err := os.WriteFile(a.guardPath, []byte(uuid+"\n"), 0o600); err != nil {
		return fmt.Errorf("cwd state guard cannot be written")
	}
	return nil
}

func (a *cwdAllocator) markUnhealthy(reason string) {
	a.unhealthy = true
	if strings.TrimSpace(reason) == "" {
		reason = "cwd state allocator is unavailable"
	}
	a.reason = reason
}

func allocatorReason(a *cwdAllocator) string {
	if a == nil || strings.TrimSpace(a.reason) == "" {
		return "cwd state allocator is unavailable"
	}
	return a.reason
}

func cwdStateUnavailableError(reason string) *CwdError {
	if strings.TrimSpace(reason) == "" {
		reason = "cwd state allocator is unavailable"
	}
	return &CwdError{
		Code:    "cwd_state_unavailable",
		Message: "cwd state is unavailable; active cwd_id lookups may continue, but set_cwd cannot issue or refresh ids",
		Hint:    staleCwdHint(reason),
	}
}

func canonicalCwdKey(path string) string {
	cleaned := filepath.Clean(path)
	if isWindowsRuntime() {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

func randomStateUUID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func readSmallTextFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func int64Ptr(value int64) *int64 {
	return &value
}
