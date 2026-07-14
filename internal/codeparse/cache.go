package codeparse

import (
	"container/list"
	"math"
	"strings"
	"sync"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

const (
	parserVersion     uint32 = 1
	projectionVersion uint32 = 1
	optionsVersion    uint32 = 1
	cacheMapOverhead  uint64 = 64
)

type cacheKey struct {
	path              string
	digest            [32]byte
	language          api.Language
	parserVersion     uint32
	projectionVersion uint32
	optionsVersion    uint32
}

func cacheKeyFor(input Input) cacheKey {
	return cacheKey{
		path:              input.Path,
		digest:            input.SHA256,
		language:          input.Language,
		parserVersion:     parserVersion,
		projectionVersion: projectionVersion,
		optionsVersion:    optionsVersion,
	}
}

type cacheEntry struct {
	key    cacheKey
	result Result
	bytes  uint64
}

// Cache is a process-owned bounded LRU of immutable projected parser results.
// A zero/zero limit pair disables storage without changing call semantics.
type Cache struct {
	mu         sync.Mutex
	maxEntries uint64
	maxBytes   uint64
	bytes      uint64
	lru        list.List
	entries    map[cacheKey]*list.Element
}

func NewCache(maxEntries, maxBytes uint64) *Cache {
	if (maxEntries == 0) != (maxBytes == 0) {
		return nil
	}
	cache := &Cache{maxEntries: maxEntries, maxBytes: maxBytes}
	if maxEntries != 0 {
		cache.entries = make(map[cacheKey]*list.Element)
	}
	return cache
}

func (cache *Cache) get(key cacheKey) (Result, bool) {
	if cache == nil || cache.maxEntries == 0 {
		return Result{}, false
	}
	cache.mu.Lock()
	element := cache.entries[key]
	if element == nil {
		cache.mu.Unlock()
		return Result{}, false
	}
	entry := element.Value.(*cacheEntry)
	result, ok := cloneResult(entry.result)
	if !ok {
		cache.removeLocked(element)
		cache.mu.Unlock()
		return Result{}, false
	}
	cache.lru.MoveToFront(element)
	cache.mu.Unlock()
	return result, true
}

func (cache *Cache) put(key cacheKey, result Result) bool {
	if cache == nil || cache.maxEntries == 0 || (result.State != Clean && result.State != Recoverable) || result.Language != key.language {
		return false
	}
	ownedResult, ok := cloneResult(result)
	if !ok {
		return false
	}
	key.path = strings.Clone(key.path)
	entryBytes := cacheEntryFootprint(key, ownedResult)
	if entryBytes > cache.maxBytes {
		return false
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if existing := cache.entries[key]; existing != nil {
		cache.removeLocked(existing)
	}
	entry := &cacheEntry{key: key, result: ownedResult, bytes: entryBytes}
	element := cache.lru.PushFront(entry)
	cache.entries[key] = element
	cache.bytes += entryBytes
	for uint64(len(cache.entries)) > cache.maxEntries || cache.bytes > cache.maxBytes {
		cache.removeLocked(cache.lru.Back())
	}
	return cache.entries[key] != nil
}

func (cache *Cache) removeLocked(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*cacheEntry)
	delete(cache.entries, entry.key)
	cache.lru.Remove(element)
	if entry.bytes > cache.bytes {
		cache.bytes = 0
	} else {
		cache.bytes -= entry.bytes
	}
}

func (cache *Cache) entryCount() uint64 {
	if cache == nil {
		return 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return uint64(len(cache.entries))
}

func (cache *Cache) usedBytes() uint64 {
	if cache == nil {
		return 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.bytes
}

func cacheEntryFootprint(key cacheKey, result Result) uint64 {
	bytes := uint64(unsafe.Sizeof(cacheEntry{})) + uint64(unsafe.Sizeof(list.Element{})) + cacheMapOverhead
	bytes = saturatingAdd(bytes, uint64(len(key.path)))
	bytes = saturatingAdd(bytes, navmodel.RecordsFootprint(result.Records))
	return bytes
}

func saturatingAdd(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}
