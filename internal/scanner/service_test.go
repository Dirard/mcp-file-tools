package scanner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
)

func TestAdvanceInitialRootOnlyDoesNotEnumerateChildren(t *testing.T) {
	t.Parallel()

	fixture := t.TempDir()
	writeFixtureFile(t, fixture, "child.txt", "child")
	root, lease, requested := openFixtureRoot(t, fixture)
	defer root.Close()
	defer lease.Close()
	directory, err := lease.OpenDir(requested)
	if err != nil {
		t.Fatalf("OpenDir: %v", err)
	}
	seed, err := NewInitialSeed(requested, directory, InitialRootOnly)
	if err != nil {
		t.Fatalf("NewInitialSeed: %v", err)
	}

	service := NewService(runtimepkg.NewSubLimiter(1))
	ctx, work := scannerWork(t, "root-only")
	batch, next, code := service.AdvanceInitial(
		ctx,
		time.Now().Add(time.Second),
		work,
		lease,
		seed,
		Request{Tool: api.ToolProject, CWDID: 7, Mode: Project, Root: requested, Depth: 0},
		generousLimits(),
		1,
		ConsumerFunc(projectRows),
	)
	work.WorkerReturned()
	if code != "" {
		t.Fatalf("AdvanceInitial code = %q", code)
	}
	if next != nil || !batch.Complete {
		t.Fatalf("root-only returned successor: complete=%v next=%v", batch.Complete, next != nil)
	}
	if len(batch.Rows) != 1 || batch.Rows[0].Kind != RowDirectory || batch.Rows[0].Path != "." {
		t.Fatalf("root-only rows = %#v", batch.Rows)
	}
	if batch.Counters != (Counters{Dirs: 1}) {
		t.Fatalf("root-only counters = %#v", batch.Counters)
	}
}

func TestScannerPagesOnePassInCanonicalOrder(t *testing.T) {
	t.Parallel()

	fixture := t.TempDir()
	writeFixtureFile(t, fixture, "c.txt", "c")
	writeFixtureFile(t, fixture, "a.txt", "a")
	writeFixtureFile(t, fixture, "dir/b.txt", "b")
	writeFixtureFile(t, fixture, "node_modules/ignored.txt", "ignored")
	writeFixtureFile(t, fixture, ".git/ignored.txt", "ignored")
	root, lease, requested := openFixtureRoot(t, fixture)
	defer root.Close()
	defer lease.Close()
	directory, err := lease.OpenDir(requested)
	if err != nil {
		t.Fatalf("OpenDir: %v", err)
	}
	seed, err := NewInitialSeed(requested, directory, InitialEnumerate)
	if err != nil {
		t.Fatalf("NewInitialSeed: %v", err)
	}

	service := NewService(runtimepkg.NewSubLimiter(1))
	request := Request{Tool: api.ToolProject, CWDID: 9, Mode: Project, Root: requested, Depth: 2}
	ctx, work := scannerWork(t, "initial")
	batch, state, code := service.AdvanceInitial(ctx, time.Now().Add(time.Second), work, lease, seed, request, generousLimits(), 2, ConsumerFunc(projectRows))
	work.WorkerReturned()
	if code != "" {
		t.Fatalf("AdvanceInitial code = %q", code)
	}
	paths := rowPaths(batch.Rows)

	for page := 0; state != nil; page++ {
		before := state.Digest()
		ctx, work = scannerWork(t, "continuation")
		type result struct {
			batch Batch
			next  *State
			code  api.ErrorCode
		}
		continued, borrowErr := rootfs.WithBorrow(lease, func(borrowed rootfs.Borrowed) result {
			pageBatch, next, pageCode := service.Advance(ctx, time.Now().Add(time.Second), work, borrowed, state, 2, ConsumerFunc(projectRows))
			return result{batch: pageBatch, next: next, code: pageCode}
		})
		work.WorkerReturned()
		if borrowErr != nil {
			t.Fatalf("WithBorrow: %v", borrowErr)
		}
		if continued.code != "" {
			t.Fatalf("Advance page %d code = %q", page, continued.code)
		}
		if after := state.Digest(); after != before {
			t.Fatalf("Advance page %d mutated its parent state", page)
		}
		paths = append(paths, rowPaths(continued.batch.Rows)...)
		state = continued.next
	}

	want := []string{".", "a.txt", "c.txt", "dir", "dir/b.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

func TestScannerSkipsAReplacedFrontierFile(t *testing.T) {
	t.Parallel()

	fixture := t.TempDir()
	writeFixtureFile(t, fixture, "a.txt", "a")
	writeFixtureFile(t, fixture, "b.txt", "b")
	root, lease, requested := openFixtureRoot(t, fixture)
	defer root.Close()
	defer lease.Close()
	directory, err := lease.OpenDir(requested)
	if err != nil {
		t.Fatalf("OpenDir: %v", err)
	}
	seed, err := NewInitialSeed(requested, directory, InitialEnumerate)
	if err != nil {
		t.Fatalf("NewInitialSeed: %v", err)
	}
	service := NewService(runtimepkg.NewSubLimiter(1))
	request := Request{Tool: api.ToolProject, CWDID: 10, Mode: Project, Root: requested, Depth: 1}
	ctx, work := scannerWork(t, "replace-initial")
	_, state, resultCode := service.AdvanceInitial(ctx, time.Now().Add(time.Second), work, lease, seed, request, generousLimits(), 1, ConsumerFunc(projectRows))
	work.WorkerReturned()
	if resultCode != "" || state == nil {
		t.Fatalf("initial result: code=%q state=%v", resultCode, state != nil)
	}

	bPath := filepath.Join(fixture, "b.txt")
	if err := os.Remove(bPath); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.WriteFile(bPath, []byte("replacement"), 0o644); err != nil {
		t.Fatalf("WriteFile replacement: %v", err)
	}
	ctx, work = scannerWork(t, "replace-continuation")
	type result struct {
		batch Batch
		next  *State
		code  api.ErrorCode
	}
	continued, borrowErr := rootfs.WithBorrow(lease, func(borrowed rootfs.Borrowed) result {
		batch, next, code := service.Advance(ctx, time.Now().Add(time.Second), work, borrowed, state, 1, ConsumerFunc(projectRows))
		return result{batch: batch, next: next, code: code}
	})
	work.WorkerReturned()
	if borrowErr != nil || continued.code != "" || continued.next != nil || !continued.batch.Complete {
		t.Fatalf("continuation: borrow=%v code=%q next=%v complete=%v", borrowErr, continued.code, continued.next != nil, continued.batch.Complete)
	}
	if got := rowPaths(continued.batch.Rows); !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Fatalf("rows = %#v", got)
	}
	if len(continued.batch.Warnings) != 1 || continued.batch.Warnings[0].Code() != api.WarningSourceChangedSkipped {
		t.Fatalf("warnings = %#v", continued.batch.Warnings)
	}
}

func TestExplicitFileContinuationRetainsDescriptorsWithoutIO(t *testing.T) {
	t.Parallel()

	fixture := t.TempDir()
	writeFixtureFile(t, fixture, "one.txt", "one")
	root, lease, _ := openFixtureRoot(t, fixture)
	defer root.Close()
	defer lease.Close()
	filePath, code := pathspec.ParseRelative(pathspec.POSIX, "one.txt", false)
	if code != "" {
		t.Fatalf("ParseRelative: %q", code)
	}
	file, err := lease.OpenRegular(filePath)
	if err != nil {
		t.Fatalf("OpenRegular: %v", err)
	}
	seed, err := NewInitialFileSeed(filePath, file)
	if err != nil {
		t.Fatalf("NewInitialFileSeed: %v", err)
	}

	consumerCalls := 0
	consumer := ConsumerFunc(func(_ context.Context, candidate Candidate, _ *rootfs.File) ConsumeResult {
		consumerCalls++
		return ConsumeResult{Rows: []Row{
			{Kind: RowTextMatch, Path: candidate.Path.String(), Line: 1, Text: "one"},
			{Kind: RowTextContext, Path: candidate.Path.String(), Line: 2, Text: "two"},
			{Kind: RowTextMatch, Path: candidate.Path.String(), Line: 3, Text: "three"},
		}}
	})
	service := NewService(runtimepkg.NewSubLimiter(1))
	request := Request{Tool: api.ToolSearch, CWDID: 11, Mode: TextSearch, Root: filePath, Depth: 255}
	ctx, work := scannerWork(t, "explicit")
	batch, state, resultCode := service.AdvanceInitialFile(ctx, time.Now().Add(time.Second), work, seed, request, generousLimits(), 1, consumer)
	work.WorkerReturned()
	if resultCode != "" || state == nil || len(batch.Rows) != 1 {
		t.Fatalf("initial explicit result: code=%q state=%v rows=%#v", resultCode, state != nil, batch.Rows)
	}
	all := append([]string(nil), batch.Rows[0].Text)

	for state != nil {
		ctx, work = scannerWork(t, "descriptor")
		batch, state, resultCode = service.Advance(ctx, time.Now().Add(time.Second), work, nil, state, 1, consumer)
		work.WorkerReturned()
		if resultCode != "" {
			t.Fatalf("descriptor continuation code = %q", resultCode)
		}
		for _, row := range batch.Rows {
			all = append(all, row.Text)
		}
	}
	if consumerCalls != 1 {
		t.Fatalf("explicit consumer calls = %d, want 1", consumerCalls)
	}
	if want := []string{"one", "two", "three"}; !reflect.DeepEqual(all, want) {
		t.Fatalf("descriptor rows = %#v, want %#v", all, want)
	}
}

func projectRows(_ context.Context, candidate Candidate, _ *rootfs.File) ConsumeResult {
	kind := RowFile
	if candidate.Kind == rootfs.EntryDir {
		kind = RowDirectory
	}
	return ConsumeResult{Rows: []Row{{Kind: kind, Path: candidate.Path.String()}}}
}

func generousLimits() Limits {
	return Limits{
		MaxFiles:         1_000,
		MaxDirs:          1_000,
		MaxBytes:         16 << 20,
		MaxParserBytes:   16 << 20,
		FrontierMaxBytes: 1 << 20,
	}
}

func scannerWork(t *testing.T, id string) (context.Context, *runtimepkg.WorkLease) {
	t.Helper()
	coordinator := runtimepkg.NewCoordinator(runtimepkg.Limits{
		MaxConcurrent: 1,
		QueueMax:      1,
		QueueTimeout:  time.Second,
	})
	reservation, outcome := coordinator.Admit(context.Background(), []byte(id))
	if outcome != runtimepkg.AdmitRun {
		t.Fatalf("Admit outcome = %d", outcome)
	}
	work, start := reservation.Start()
	if start.Kind != runtimepkg.StartRun {
		t.Fatalf("Start outcome = %d", start.Kind)
	}
	return reservation.Context(), work
}

func openFixtureRoot(t *testing.T, directory string) (*rootfs.Root, *rootfs.Lease, pathspec.Relative) {
	t.Helper()
	rootPath, code := pathspec.ParseRootDirectory(pathspec.POSIX, directory)
	if code != "" {
		t.Fatalf("ParseRootDirectory: %q", code)
	}
	root, err := rootfs.OpenRoot(rootPath)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	lease, err := root.Duplicate()
	if err != nil {
		root.Close()
		t.Fatalf("Duplicate: %v", err)
	}
	requested, code := pathspec.ParseRelative(pathspec.POSIX, ".", true)
	if code != "" {
		lease.Close()
		root.Close()
		t.Fatalf("ParseRelative root: %q", code)
	}
	return root, lease, requested
}

func writeFixtureFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func rowPaths(rows []Row) []string {
	paths := make([]string, len(rows))
	for index, row := range rows {
		paths[index] = row.Path
	}
	return paths
}
