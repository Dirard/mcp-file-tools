package codeparse

import (
	"crypto/sha256"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestCacheIsBoundedLRUAndReturnsImmutableCopies(t *testing.T) {
	cache := NewCache(2, 1<<20)
	if cache == nil {
		t.Fatal("valid cache rejected")
	}
	first := cacheTestInput("a.go", "package a")
	second := cacheTestInput("b.go", "package b")
	third := cacheTestInput("c.go", "package c")
	result := Result{Language: api.LanguageGo, State: Recoverable, Records: []navmodel.Record{{Type: navmodel.Symbol, Range: navmodel.Range{Start: 1, End: 1}, Kind: api.KindPackage, Name: "a"}}}

	if !cache.put(cacheKeyFor(first), result) {
		t.Fatal("first cache put rejected")
	}
	result.Records[0].Name = "mutated"
	got, ok := cache.get(cacheKeyFor(first))
	if !ok || got.State != Recoverable || got.Records[0].Name != "a" {
		t.Fatalf("immutable cache hit = %#v,%t", got, ok)
	}
	got.Records[0].Name = "caller mutation"
	if again, hit := cache.get(cacheKeyFor(first)); !hit || again.Records[0].Name != "a" {
		t.Fatalf("cache value mutated through hit: %#v,%t", again, hit)
	}

	result.Records[0].Name = "b"
	if !cache.put(cacheKeyFor(second), result) {
		t.Fatal("second cache put rejected")
	}
	if _, hit := cache.get(cacheKeyFor(first)); !hit { // first is now most recent
		t.Fatal("first cache entry unexpectedly missed")
	}
	result.Records[0].Name = "c"
	if !cache.put(cacheKeyFor(third), result) {
		t.Fatal("third cache put rejected")
	}
	if _, hit := cache.get(cacheKeyFor(second)); hit {
		t.Fatal("least-recently-used entry was not evicted")
	}
	if cache.entryCount() != 2 || cache.usedBytes() > 1<<20 {
		t.Fatalf("cache accounting = entries %d, bytes %d", cache.entryCount(), cache.usedBytes())
	}
}

func TestCacheDisabledOversizeAndFatalResultsAreNeverStored(t *testing.T) {
	if NewCache(1, 0) != nil || NewCache(0, 1) != nil {
		t.Fatal("half-disabled cache accepted")
	}
	disabled := NewCache(0, 0)
	input := cacheTestInput("a.go", "package a")
	clean := Result{Language: api.LanguageGo, State: Clean, Records: []navmodel.Record{{Type: navmodel.Symbol, Range: navmodel.Range{Start: 1, End: 1}, Kind: api.KindPackage, Name: "a"}}}
	if disabled.put(cacheKeyFor(input), clean) {
		t.Fatal("disabled cache stored an entry")
	}
	if _, hit := disabled.get(cacheKeyFor(input)); hit {
		t.Fatal("disabled cache returned a hit")
	}

	tooSmall := NewCache(4, 1)
	if tooSmall.put(cacheKeyFor(input), clean) || tooSmall.entryCount() != 0 {
		t.Fatal("oversize cache entry was stored")
	}
	fatal := Result{Language: api.LanguageGo, State: Fatal}
	cache := NewCache(4, 1<<20)
	if cache.put(cacheKeyFor(input), fatal) {
		t.Fatal("fatal parser result was cached")
	}
}

func TestCacheEvictsWholeEntriesAtByteLimit(t *testing.T) {
	first := cacheTestInput("a.go", "package a")
	second := cacheTestInput("b.go", "package b")
	result := Result{Language: api.LanguageGo, State: Clean, Records: []navmodel.Record{{Type: navmodel.Symbol, Range: navmodel.Range{Start: 1, End: 1}, Kind: api.KindPackage, Name: "x"}}}
	oneEntry := cacheEntryFootprint(cacheKeyFor(first), result)
	cache := NewCache(4, oneEntry+oneEntry/2)
	if !cache.put(cacheKeyFor(first), result) || !cache.put(cacheKeyFor(second), result) {
		t.Fatal("individually fitting entries were rejected")
	}
	if _, hit := cache.get(cacheKeyFor(first)); hit {
		t.Fatal("byte pressure did not evict the least-recently-used whole entry")
	}
	if _, hit := cache.get(cacheKeyFor(second)); !hit || cache.entryCount() != 1 || cache.usedBytes() > oneEntry+oneEntry/2 {
		t.Fatalf("byte-bounded cache state = hit %t, entries %d, bytes %d", hit, cache.entryCount(), cache.usedBytes())
	}
}

func TestCacheKeyIncludesPathDigestLanguageAndVersions(t *testing.T) {
	base := cacheTestInput("a.go", "package a")
	key := cacheKeyFor(base)
	changedPath := base
	changedPath.Path = "b.go"
	changedLanguage := base
	changedLanguage.Language = api.LanguageJavaScript
	changedDigest := base
	changedDigest.SHA256 = sha256.Sum256([]byte("package b"))
	if key == cacheKeyFor(changedPath) || key == cacheKeyFor(changedLanguage) || key == cacheKeyFor(changedDigest) {
		t.Fatal("cache key omitted a freshness dimension")
	}
	if key.parserVersion == 0 || key.projectionVersion == 0 || key.optionsVersion == 0 {
		t.Fatalf("cache key versions are not closed: %#v", key)
	}
}

func cacheTestInput(path, source string) Input {
	canonical := []byte(source)
	language, _ := LanguageForPath(path)
	return Input{Path: path, Canonical: canonical, SHA256: sha256.Sum256(canonical), Language: language}
}
