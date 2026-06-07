package handler

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dimitar-grigorov/mcp-file-tools/internal/config"
	"github.com/gofrs/flock"
)

func TestCwdRegistryHighWaterSurvivesHandlerRestart(t *testing.T) {
	cfg := cwdRegistryTestConfig(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	first := NewHandler(WithConfig(cfg))
	idA := setCwdWithHandlerForTest(t, first, dirA)

	second := NewHandler(WithConfig(cfg))
	idB := setCwdWithHandlerForTest(t, second, dirB)
	if idB <= idA {
		t.Fatalf("cwd allocator should advance after handler restart: first=%d second=%d", idA, idB)
	}

	_, cwdErr := second.BuildPathContext(CwdIDInput{Present: true, Value: idA})
	if cwdErr == nil || cwdErr.Code != "cwd_id_unknown" {
		t.Fatalf("old in-memory cwd_id should be unknown after handler restart, got %#v", cwdErr)
	}
}

func TestCwdRegistryStateResetFailsClosedButKeepsActiveLookup(t *testing.T) {
	cfg := cwdRegistryTestConfig(t)
	dirA := t.TempDir()
	dirB := t.TempDir()
	h := NewHandler(WithConfig(cfg))

	idA := setCwdWithHandlerForTest(t, h, dirA)
	removeCwdStateBundleForTest(t, cfg.CwdStatePath)

	_, output, err := h.HandleSetCwd(context.Background(), nil, SetCwdInput{Directory: dirB})
	if err != nil {
		t.Fatal(err)
	}
	if output.ErrorCode != "cwd_state_unavailable" {
		t.Fatalf("set_cwd after live state reset should fail closed: %#v", output)
	}

	pathCtx, cwdErr := h.BuildPathContext(CwdIDInput{Present: true, Value: idA})
	if cwdErr != nil {
		t.Fatalf("active lookup should continue after allocator failure: %#v", cwdErr)
	}
	if pathCtx.CwdOut != filepath.ToSlash(dirA) {
		t.Fatalf("active id was remapped: %#v", pathCtx)
	}
}

func TestCwdRegistryExpiredIDReturnsExpiredCode(t *testing.T) {
	cfg := cwdRegistryTestConfig(t)
	h := NewHandler(WithConfig(cfg))
	h.cwdRegistry.ttl = 5 * time.Millisecond

	id := setCwdWithHandlerForTest(t, h, t.TempDir())
	time.Sleep(15 * time.Millisecond)

	_, cwdErr := h.BuildPathContext(CwdIDInput{Present: true, Value: id})
	if cwdErr == nil || cwdErr.Code != "cwd_id_expired" || cwdErr.CwdID == nil || *cwdErr.CwdID != id {
		t.Fatalf("expired id should return cwd_id_expired with id echo, got %#v", cwdErr)
	}
}

func TestCwdRegistryCreatesStateParentBeforeLock(t *testing.T) {
	cfg := cwdRegistryTestConfig(t)
	cfg.CwdStatePath = filepath.Join(t.TempDir(), "nested", "state", "cwd-state.sqlite")
	h := NewHandler(WithConfig(cfg))

	id := setCwdWithHandlerForTest(t, h, t.TempDir())
	if id != 1 {
		t.Fatalf("first cwd_id = %d, want 1", id)
	}
	if _, err := os.Stat(cfg.CwdStatePath + ".lock"); err != nil {
		t.Fatalf("state lock should be creatable under nested parent: %v", err)
	}
}

func TestCwdRegistryLockWaitIsBounded(t *testing.T) {
	cfg := cwdRegistryTestConfig(t)
	if err := os.MkdirAll(filepath.Dir(cfg.CwdStatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(cfg.CwdStatePath + ".lock")
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(cfg))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, output, err := h.HandleSetCwd(ctx, nil, SetCwdInput{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if output.ErrorCode != "cwd_state_unavailable" {
		t.Fatalf("lock contention should fail as cwd_state_unavailable: %#v", output)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	id := setCwdWithHandlerForTest(t, h, t.TempDir())
	if id != 1 {
		t.Fatalf("allocator should recover after transient lock contention, got id %d", id)
	}
}

func TestBuildPathContextPrefersRequestScopedContextAfterExpiry(t *testing.T) {
	cfg := cwdRegistryTestConfig(t)
	h := NewHandler(WithConfig(cfg))
	h.cwdRegistry.ttl = 5 * time.Millisecond

	id := setCwdWithHandlerForTest(t, h, t.TempDir())
	requestPathCtx, cwdErr := h.BuildPathContext(CwdIDInput{Present: true, Value: id})
	if cwdErr != nil {
		t.Fatalf("initial lookup failed: %#v", cwdErr)
	}
	time.Sleep(15 * time.Millisecond)
	if _, cwdErr := h.BuildPathContext(CwdIDInput{Present: true, Value: id}); cwdErr == nil || cwdErr.Code != "cwd_id_expired" {
		t.Fatalf("plain lookup should expire, got %#v", cwdErr)
	}
	if _, cwdErr := h.BuildPathContext(CwdIDInput{Present: true, Value: id, PathContext: &requestPathCtx}); cwdErr != nil {
		t.Fatalf("request-scoped path context should remain usable after registry expiry: %#v", cwdErr)
	}
}

func TestCwdRegistryIncompleteStateBundlesFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove string
	}{
		{name: "missing_guard", remove: "guard"},
		{name: "missing_db", remove: "db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cwdRegistryTestConfig(t)
			h := NewHandler(WithConfig(cfg))
			setCwdWithHandlerForTest(t, h, t.TempDir())
			switch tc.remove {
			case "guard":
				if err := os.Remove(cfg.CwdStatePath + ".guard"); err != nil {
					t.Fatal(err)
				}
			case "db":
				if err := os.Remove(cfg.CwdStatePath); err != nil {
					t.Fatal(err)
				}
			}

			_, output, err := h.HandleSetCwd(context.Background(), nil, SetCwdInput{Directory: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if output.ErrorCode != "cwd_state_unavailable" {
				t.Fatalf("incomplete state bundle should fail closed: %#v", output)
			}
		})
	}
}

func TestCwdRegistrySameUUIDRollbackFailsClosed(t *testing.T) {
	cfg := cwdRegistryTestConfig(t)
	h := NewHandler(WithConfig(cfg))
	setCwdWithHandlerForTest(t, h, t.TempDir())
	updateCwdLastIssuedForTest(t, cfg.CwdStatePath, 0)

	_, output, err := h.HandleSetCwd(context.Background(), nil, SetCwdInput{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if output.ErrorCode != "cwd_state_unavailable" {
		t.Fatalf("same-UUID allocator rollback should fail closed: %#v", output)
	}
}

func TestCwdRegistryMaxIDBoundaryFailsClosed(t *testing.T) {
	cfg := cwdRegistryTestConfig(t)
	h := NewHandler(WithConfig(cfg))
	setCwdWithHandlerForTest(t, h, t.TempDir())
	updateCwdLastIssuedForTest(t, cfg.CwdStatePath, maxCwdID)

	_, output, err := h.HandleSetCwd(context.Background(), nil, SetCwdInput{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if output.ErrorCode != "cwd_state_unavailable" {
		t.Fatalf("max cwd_id boundary should fail closed: %#v", output)
	}
}

func TestCwdRegistryIgnoresStaleLockFileAndTempGuard(t *testing.T) {
	cfg := cwdRegistryTestConfig(t)
	if err := os.MkdirAll(filepath.Dir(cfg.CwdStatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.CwdStatePath+".lock", []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.CwdStatePath+".guard.tmp", []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(WithConfig(cfg))
	if id := setCwdWithHandlerForTest(t, h, t.TempDir()); id != 1 {
		t.Fatalf("stale lock/temp guard should not consume ids, got %d", id)
	}
}

func TestCwdRegistryConcurrentHandlersAllocateUniqueIDs(t *testing.T) {
	cfg := cwdRegistryTestConfig(t)
	const workers = 8
	dirs := make([]string, workers)
	for i := range dirs {
		dirs[i] = t.TempDir()
	}
	ids := make(chan int64, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(directory string) {
			defer wg.Done()
			h := NewHandler(WithConfig(cfg))
			_, output, err := h.HandleSetCwd(context.Background(), nil, SetCwdInput{Directory: directory})
			if err != nil {
				errs <- err
				return
			}
			if output.ErrorCode != "" || output.CwdID < 1 {
				errs <- os.ErrInvalid
				return
			}
			ids <- output.CwdID
		}(dirs[i])
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent set_cwd failed: %v", err)
	}
	seen := map[int64]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate cwd_id allocated concurrently: %d", id)
		}
		seen[id] = true
	}
	if len(seen) != workers {
		t.Fatalf("allocated %d ids, want %d", len(seen), workers)
	}
}

func cwdRegistryTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Load()
	cfg.CwdStatePath = filepath.Join(t.TempDir(), "cwd-state.sqlite")
	cfg.CwdStateConfigError = ""
	cfg.CwdRequireExplicitStatePath = false
	return cfg
}

func setCwdWithHandlerForTest(t *testing.T, h *Handler, directory string) int64 {
	t.Helper()
	_, output, err := h.HandleSetCwd(context.Background(), nil, SetCwdInput{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	if output.ErrorCode != "" || output.CwdID < 1 {
		t.Fatalf("set_cwd failed: %#v", output)
	}
	return output.CwdID
}

func removeCwdStateBundleForTest(t *testing.T, statePath string) {
	t.Helper()
	for _, path := range []string{
		statePath,
		statePath + ".guard",
		statePath + ".lock",
		statePath + "-journal",
		statePath + "-wal",
		statePath + "-shm",
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func updateCwdLastIssuedForTest(t *testing.T, statePath string, value int64) {
	t.Helper()
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE cwd_state SET last_issued = ? WHERE id = 1`, value); err != nil {
		t.Fatal(err)
	}
}
