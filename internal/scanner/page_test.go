package scanner

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
)

type boundedRowPage struct {
	maximum   int
	committed []Row
}

func (page *boundedRowPage) Try(Row) RowFit {
	if len(page.committed) >= page.maximum {
		return RowNextPage
	}
	return RowFits
}

func (page *boundedRowPage) Commit(row Row) {
	page.committed = append(page.committed, row)
}

func TestAdvancePageRetainsTheFirstRowThatDoesNotFit(t *testing.T) {
	t.Parallel()

	fixture := t.TempDir()
	writeFixtureFile(t, fixture, "b.txt", "b")
	writeFixtureFile(t, fixture, "a.txt", "a")
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
	request := Request{Tool: api.ToolProject, CWDID: 11, Mode: Project, Root: requested, Depth: 1}
	consumerCalls := 0
	consumer := ConsumerFunc(func(ctx context.Context, candidate Candidate, file *rootfs.File) ConsumeResult {
		consumerCalls++
		return projectRows(ctx, candidate, file)
	})

	firstPage := &boundedRowPage{maximum: 2}
	ctx, work := scannerWork(t, "page-fit-initial")
	batch, state, code := service.AdvanceInitialPage(
		ctx,
		time.Now().Add(time.Second),
		work,
		lease,
		seed,
		request,
		generousLimits(),
		100,
		consumer,
		firstPage,
	)
	work.WorkerReturned()
	if code != "" {
		t.Fatalf("AdvanceInitialPage code = %q", code)
	}
	if state == nil || batch.Complete {
		t.Fatalf("first page complete=%v state=%v", batch.Complete, state != nil)
	}
	if got, want := rowPaths(batch.Rows), []string{".", "a.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first rows = %#v, want %#v", got, want)
	}

	secondPage := &boundedRowPage{maximum: 100}
	ctx, work = scannerWork(t, "page-fit-next")
	type continuationResult struct {
		batch Batch
		next  *State
		code  api.ErrorCode
	}
	continued, borrowErr := rootfs.WithBorrow(lease, func(borrowed rootfs.Borrowed) continuationResult {
		pageBatch, next, pageCode := service.AdvancePage(ctx, time.Now().Add(time.Second), work, borrowed, state, 100, consumer, secondPage)
		return continuationResult{batch: pageBatch, next: next, code: pageCode}
	})
	work.WorkerReturned()
	if borrowErr != nil {
		t.Fatalf("WithBorrow: %v", borrowErr)
	}
	if continued.code != "" || continued.next != nil || !continued.batch.Complete {
		t.Fatalf("continuation code=%q complete=%v next=%v", continued.code, continued.batch.Complete, continued.next != nil)
	}
	if got, want := rowPaths(continued.batch.Rows), []string{"b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second rows = %#v, want %#v", got, want)
	}
	if consumerCalls != 3 {
		t.Fatalf("consumer calls = %d, want 3", consumerCalls)
	}
}
