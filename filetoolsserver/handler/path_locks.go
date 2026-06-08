package handler

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

type pathLockManager struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newPathLockManager() *pathLockManager {
	return &pathLockManager{locks: map[string]*sync.Mutex{}}
}

func (m *pathLockManager) acquire(paths []string) func() {
	if m == nil {
		return func() {}
	}
	unique := make(map[string]struct{}, len(paths))
	ordered := make([]string, 0, len(paths))
	for _, path := range paths {
		key := pathLockKey(path)
		if key == "" {
			continue
		}
		if _, ok := unique[key]; ok {
			continue
		}
		unique[key] = struct{}{}
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)

	locks := make([]*sync.Mutex, 0, len(ordered))
	m.mu.Lock()
	for _, path := range ordered {
		lock := m.locks[path]
		if lock == nil {
			lock = &sync.Mutex{}
			m.locks[path] = lock
		}
		locks = append(locks, lock)
	}
	m.mu.Unlock()

	for _, lock := range locks {
		lock.Lock()
	}
	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
	}
}

func pathLockKey(path string) string {
	key := filepath.Clean(path)
	if key == "." {
		return ""
	}
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}
