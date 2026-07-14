package cursor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	serverruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

type testClock struct{ now time.Time }

func (clock *testClock) Now() time.Time { return clock.now }

type testState struct {
	tool         api.ToolName
	cwdID        uint64
	digest       [32]byte
	sharedDigest [32]byte
	hasShared    bool
	payload      []byte
}

func newTestState(tool api.ToolName, cwdID uint64, payload string) *testState {
	return &testState{tool: tool, cwdID: cwdID, digest: sha256.Sum256([]byte(payload)), payload: []byte(payload)}
}

func (state *testState) Tool() api.ToolName { return state.tool }
func (state *testState) CWDID() uint64      { return state.cwdID }
func (state *testState) Digest() [32]byte   { return state.digest }
func (state *testState) SharedDigest() ([32]byte, bool) {
	return state.sharedDigest, state.hasShared
}
func (state *testState) Footprint() uint64 { return uint64(cap(state.payload)) }
func (state *testState) CloneForCompute() State {
	clone := *state
	clone.payload = append([]byte(nil), state.payload...)
	return &clone
}

type testShared struct {
	digest  [32]byte
	payload []byte
}

func newTestShared(payload string) *testShared {
	return &testShared{digest: sha256.Sum256([]byte(payload)), payload: []byte(payload)}
}

func (shared *testShared) Digest() [32]byte { return shared.digest }
func (shared *testShared) Footprint() uint64 {
	return uint64(cap(shared.payload))
}

func newTestRegistry(t *testing.T, cfg config.Runtime) (*Registry, *testClock) {
	t.Helper()
	stream := make([]byte, 256)
	for i := range stream {
		stream[i] = byte(i)
	}
	clock := &testClock{now: time.Unix(1_700_000_000, 0)}
	registry := &Registry{cfg: cfg, entropy: bytes.NewReader(stream), clock: clock}
	if _, err := io.ReadFull(registry.entropy, registry.secret[:]); err != nil {
		t.Fatalf("read test secret: %v", err)
	}
	if err := registry.initialize(); err != nil {
		t.Fatalf("initialize registry: %v", err)
	}
	return registry, clock
}

func newWorkLease(t *testing.T, id string) (*serverruntime.Coordinator, *serverruntime.WorkLease) {
	t.Helper()
	coordinator := serverruntime.NewCoordinator(serverruntime.Limits{MaxConcurrent: 1})
	reservation, outcome := coordinator.Admit(context.Background(), []byte(id))
	if outcome != serverruntime.AdmitRun {
		t.Fatalf("Admit = %d, want AdmitRun", outcome)
	}
	lease, start := reservation.Start()
	if start.Kind != serverruntime.StartRun || lease == nil {
		t.Fatalf("Start = (%p, %d), want running lease", lease, start.Kind)
	}
	return coordinator, lease
}

func assertWorkReturned(t *testing.T, coordinator *serverruntime.Coordinator, id string) {
	t.Helper()
	reservation, outcome := coordinator.Admit(context.Background(), []byte(id))
	if outcome != serverruntime.AdmitRun {
		t.Fatalf("work slot was not returned: Admit = %d", outcome)
	}
	lease, start := reservation.Start()
	if start.Kind != serverruntime.StartRun || lease == nil {
		t.Fatalf("second Start = (%p, %d)", lease, start.Kind)
	}
	lease.WorkerReturned()
}

func TestDynamicInitialPublicationIsTransactional(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, _ := newTestRegistry(t, cfg)
	coordinator, work := newWorkLease(t, "dynamic")
	state := newTestState(api.ToolProject, 7, "state")
	commit, err := registry.CommitDynamicInitial(DynamicInitial{
		State: state,
		SummaryPlan: ReservationUse{
			Kind:  BroadSummaryFinal,
			Slots: 1,
			Bytes: 128,
		},
	}, work)
	if err != nil {
		t.Fatalf("CommitDynamicInitial: %v", err)
	}
	var expected Token
	for i := range expected {
		expected[i] = byte(i + 32)
	}
	if commit.Token != expected {
		t.Fatalf("initial token = %q, want %q", commit.Token.String(), expected.String())
	}
	if _, code := registry.Lookup(commit.Token); code != api.ErrorCursorExpired {
		t.Fatalf("provisional Lookup code = %q", code)
	}
	if err := commit.Publication.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, code := registry.Lookup(commit.Token); code != "" {
		t.Fatalf("publishing Lookup code = %q", code)
	}
	if err := commit.Publication.Commit(); err != nil {
		t.Fatalf("Commit publication: %v", err)
	}
	if _, code := registry.Lookup(commit.Token); code != "" {
		t.Fatalf("published Lookup code = %q", code)
	}
	if registry.usedSlots != 2 {
		t.Fatalf("usedSlots = %d, want 2", registry.usedSlots)
	}
	wantBytes := registry.baseBytes + state.Footprint() + 128
	if registry.totalBytes != wantBytes {
		t.Fatalf("totalBytes = %d, want %d", registry.totalBytes, wantBytes)
	}
	assertWorkReturned(t, coordinator, "dynamic-next")
}

func TestDynamicInitialRequiresContinuationPageBudget(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	cfg.CursorMaxPages = 1
	registry, _ := newTestRegistry(t, cfg)
	coordinator, work := newWorkLease(t, "single-page-dynamic")
	commit, err := registry.CommitDynamicInitial(DynamicInitial{
		State:       newTestState(api.ToolProject, 8, "partial"),
		SummaryPlan: ReservationUse{Kind: BroadSummaryFinal, Slots: 1, Bytes: 64},
	}, work)
	if err == nil {
		commit.Publication.Abort()
		t.Fatal("CommitDynamicInitial succeeded with no continuation page budget")
	}
	if code := CodeOf(err); code != api.ErrorBudgetExceeded {
		work.WorkerReturned()
		t.Fatalf("CommitDynamicInitial code = %q", code)
	}
	work.WorkerReturned()
	assertWorkReturned(t, coordinator, "single-page-dynamic-next")
}

func TestScopedLookupUsesToolThenCWDErrorPrecedence(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, _ := newTestRegistry(t, cfg)
	_, work := newWorkLease(t, "scope")
	commit, err := registry.CommitDynamicInitial(DynamicInitial{
		State:       newTestState(api.ToolSearch, 17, "scope-state"),
		SummaryPlan: ReservationUse{Kind: BroadSummaryFinal, Slots: 1, Bytes: 64},
	}, work)
	if err != nil {
		t.Fatalf("CommitDynamicInitial: %v", err)
	}
	if err := commit.Publication.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := commit.Publication.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.clockNowLocked()
	if _, code := registry.lookupScopedLocked(commit.Token, api.ToolProject, 999, now); code != api.ErrorCursorWrongTool {
		t.Fatalf("wrong tool+cwd code = %q", code)
	}
	if _, code := registry.lookupScopedLocked(commit.Token, api.ToolSearch, 999, now); code != api.ErrorCursorWrongCWD {
		t.Fatalf("wrong cwd code = %q", code)
	}
	if _, code := registry.lookupScopedLocked(commit.Token, api.ToolSearch, 17, now); code != "" {
		t.Fatalf("valid scoped lookup code = %q", code)
	}
}

func TestProductionRegistryConstructorAllocatesConfiguredCapacity(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if registry.layout.MaxEntries != 16 || registry.layout.IndexSlots != 32 || registry.baseBytes != registry.layout.Total {
		t.Fatalf("layout = %#v, base = %d", registry.layout, registry.baseBytes)
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAbortRollsBackPublishingLineage(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, _ := newTestRegistry(t, cfg)
	coordinator, work := newWorkLease(t, "abort")
	commit, err := registry.CommitDynamicInitial(DynamicInitial{
		State:       newTestState(api.ToolSearch, 9, "search"),
		SummaryPlan: ReservationUse{Kind: BroadSummaryFinal, Slots: 1, Bytes: 64},
	}, work)
	if err != nil {
		t.Fatalf("CommitDynamicInitial: %v", err)
	}
	if err := commit.Publication.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	commit.Publication.Abort()
	if _, code := registry.Lookup(commit.Token); code != api.ErrorCursorExpired {
		t.Fatalf("Lookup after abort code = %q", code)
	}
	if registry.usedSlots != 0 || registry.totalBytes != registry.baseBytes {
		t.Fatalf("rollback accounting = slots %d, bytes %d/%d", registry.usedSlots, registry.totalBytes, registry.baseBytes)
	}
	assertWorkReturned(t, coordinator, "abort-next")
}

func TestReadInitialChargesSharedAllocationOnce(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, _ := newTestRegistry(t, cfg)
	coordinator, work := newWorkLease(t, "read")
	shared := newTestShared("immutable snapshot")
	state := newTestState(api.ToolRead, 11, "page zero")
	state.hasShared = true
	state.sharedDigest = shared.Digest()
	commit, err := registry.CommitReadInitial(ReadInitial{
		State:    state,
		Shared:   shared,
		PagePlan: ReadPageReservation{Pages: 3, Slots: 2, Bytes: 96},
	}, work)
	if err != nil {
		t.Fatalf("CommitReadInitial: %v", err)
	}
	if err := commit.Publication.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := commit.Publication.Commit(); err != nil {
		t.Fatalf("Commit publication: %v", err)
	}
	if registry.usedSlots != 3 {
		t.Fatalf("usedSlots = %d, want 3", registry.usedSlots)
	}
	wantBytes := registry.baseBytes + state.Footprint() + shared.Footprint() + 96
	if registry.totalBytes != wantBytes {
		t.Fatalf("totalBytes = %d, want %d", registry.totalBytes, wantBytes)
	}
	assertWorkReturned(t, coordinator, "read-next")
}

func TestNoCommitBeforeTransferLeavesRegistryUnchanged(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, _ := newTestRegistry(t, cfg)
	_, work := newWorkLease(t, "cancelled")
	work.MarkNoCommit()
	_, err := registry.CommitDynamicInitial(DynamicInitial{
		State:       newTestState(api.ToolProject, 7, "state"),
		SummaryPlan: ReservationUse{Kind: BroadSummaryFinal, Slots: 1, Bytes: 64},
	}, work)
	if err == nil {
		t.Fatal("CommitDynamicInitial succeeded after MarkNoCommit")
	}
	if registry.usedSlots != 0 || registry.totalBytes != registry.baseBytes {
		t.Fatalf("failed transfer accounting = slots %d, bytes %d/%d", registry.usedSlots, registry.totalBytes, registry.baseBytes)
	}
	work.WorkerReturned()
}

func TestRegistryCloseReleasesFixedStorage(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, _ := newTestRegistry(t, cfg)
	_, work := newWorkLease(t, "close")
	commit, err := registry.CommitDynamicInitial(DynamicInitial{
		State:       newTestState(api.ToolProject, 7, "close-state"),
		SummaryPlan: ReservationUse{Kind: BroadSummaryFinal, Slots: 1, Bytes: 64},
	}, work)
	if err != nil {
		t.Fatalf("CommitDynamicInitial: %v", err)
	}
	if err := commit.Publication.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := commit.Publication.Commit(); err != nil {
		t.Fatalf("Commit publication: %v", err)
	}
	if registry.baseBytes == 0 || registry.totalBytes <= registry.baseBytes {
		t.Fatalf("live accounting = %d/%d", registry.baseBytes, registry.totalBytes)
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if registry.baseBytes != 0 || registry.totalBytes != 0 || registry.usedSlots != 0 {
		t.Fatalf("closed accounting = base %d total %d slots %d", registry.baseBytes, registry.totalBytes, registry.usedSlots)
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestLRUEvictsWholeLeastRecentlyUsedLineage(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 4
	registry, clock := newTestRegistry(t, cfg)
	publish := func(id, payload string) Token {
		t.Helper()
		_, work := newWorkLease(t, id)
		commit, err := registry.CommitDynamicInitial(DynamicInitial{
			State:       newTestState(api.ToolProject, 7, payload),
			SummaryPlan: ReservationUse{Kind: BroadSummaryFinal, Slots: 1, Bytes: 64},
		}, work)
		if err != nil {
			t.Fatalf("CommitDynamicInitial(%s): %v", id, err)
		}
		if err := commit.Publication.Prepare(); err != nil {
			t.Fatalf("Prepare(%s): %v", id, err)
		}
		if err := commit.Publication.Commit(); err != nil {
			t.Fatalf("Commit(%s): %v", id, err)
		}
		return commit.Token
	}

	first := publish("lru-first", "first")
	second := publish("lru-second", "second")
	clock.now = clock.now.Add(config.CursorHandoffGrace + time.Second)
	if _, code := registry.Lookup(first); code != "" {
		t.Fatalf("touch first code = %q", code)
	}
	third := publish("lru-third", "third")
	if _, code := registry.Lookup(first); code != "" {
		t.Fatalf("first code after eviction = %q", code)
	}
	if _, code := registry.Lookup(second); code != api.ErrorCursorExpired {
		t.Fatalf("second code after eviction = %q", code)
	}
	if _, code := registry.Lookup(third); code != "" {
		t.Fatalf("third code after eviction = %q", code)
	}
}

func TestLookupExpiresAtWorkTTLBoundary(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	cfg.CursorTTL = time.Minute
	registry, clock := newTestRegistry(t, cfg)
	_, work := newWorkLease(t, "ttl")
	commit, err := registry.CommitDynamicInitial(DynamicInitial{
		State:       newTestState(api.ToolSearch, 7, "ttl-state"),
		SummaryPlan: ReservationUse{Kind: BroadSummaryFinal, Slots: 1, Bytes: 64},
	}, work)
	if err != nil {
		t.Fatalf("CommitDynamicInitial: %v", err)
	}
	if err := commit.Publication.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := commit.Publication.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	clock.now = clock.now.Add(time.Minute)
	if _, code := registry.Lookup(commit.Token); code != api.ErrorCursorExpired {
		t.Fatalf("Lookup at TTL boundary code = %q", code)
	}
	if registry.usedSlots != 0 || registry.totalBytes != registry.baseBytes {
		t.Fatalf("expired accounting = slots %d bytes %d/%d", registry.usedSlots, registry.totalBytes, registry.baseBytes)
	}
}

func TestReadSuccessorsConsumePrepaidReservation(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, _ := newTestRegistry(t, cfg)
	shared := newTestShared("read snapshot")
	initial := newTestState(api.ToolRead, 12, "read-0")
	initial.hasShared, initial.sharedDigest = true, shared.Digest()
	firstState := newTestState(api.ToolRead, 12, "read-1")
	firstState.hasShared, firstState.sharedDigest = true, shared.Digest()
	secondState := newTestState(api.ToolRead, 12, "read-2")
	secondState.hasShared, secondState.sharedDigest = true, shared.Digest()
	firstResult := api.Navigation("first page\n", false)
	secondResult := api.Navigation("second page\n", false)
	firstMemo, err := resultFootprint(firstResult)
	if err != nil {
		t.Fatalf("resultFootprint(first): %v", err)
	}
	secondMemo, err := resultFootprint(secondResult)
	if err != nil {
		t.Fatalf("resultFootprint(second): %v", err)
	}
	firstBytes := firstState.Footprint() + firstMemo
	secondBytes := secondState.Footprint() + secondMemo
	_, work := newWorkLease(t, "read-reserved")
	commit, err := registry.CommitReadInitial(ReadInitial{
		State:  initial,
		Shared: shared,
		PagePlan: ReadPageReservation{
			Pages: 3,
			Slots: 2,
			Bytes: firstBytes + secondBytes,
		},
	}, work)
	if err != nil {
		t.Fatalf("CommitReadInitial: %v", err)
	}
	if err := commit.Publication.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := commit.Publication.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	parent, code := registry.Lookup(commit.Token)
	if code != "" {
		t.Fatalf("Lookup parent code = %q", code)
	}
	totalBefore := registry.totalBytes
	firstToken, err := registry.materializeSuccessor(parent, firstState, firstResult, ReservationUse{Kind: ReadPagePlan, Slots: 1, Bytes: firstBytes})
	if err != nil {
		t.Fatalf("materialize first: %v", err)
	}
	firstRef, code := registry.Lookup(firstToken)
	if code != "" {
		t.Fatalf("Lookup first child code = %q", code)
	}
	secondToken, err := registry.materializeSuccessor(firstRef, secondState, secondResult, ReservationUse{Kind: ReadPagePlan, Slots: 1, Bytes: secondBytes})
	if err != nil {
		t.Fatalf("materialize second: %v", err)
	}
	if _, code := registry.Lookup(secondToken); code != "" {
		t.Fatalf("Lookup second child code = %q", code)
	}
	lineage := registry.entries.lineage[parent.index]
	if registry.usedSlots != 3 || registry.lineages.reservedSlots[lineage] != 0 || registry.lineages.reservedBytes[lineage] != 0 {
		t.Fatalf("reservation = used %d slots %d bytes %d", registry.usedSlots, registry.lineages.reservedSlots[lineage], registry.lineages.reservedBytes[lineage])
	}
	if registry.totalBytes != totalBefore {
		t.Fatalf("reserved materialization changed totalBytes: %d -> %d", totalBefore, registry.totalBytes)
	}
}

func TestDynamicSuccessorPreservesThenConsumesSummaryCredit(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 8
	registry, _ := newTestRegistry(t, cfg)
	initial := newTestState(api.ToolProject, 14, "project-0")
	ordinary := newTestState(api.ToolProject, 14, "project-1")
	terminal := newTestState(api.ToolProject, 14, "project-final")
	ordinaryResult := api.Navigation("ordinary\n", false)
	terminalResult := api.Navigation("terminal\n", false)
	ordinaryMemo, _ := resultFootprint(ordinaryResult)
	terminalMemo, _ := resultFootprint(terminalResult)
	ordinaryBytes := ordinary.Footprint() + ordinaryMemo
	terminalBytes := terminal.Footprint() + terminalMemo
	_, work := newWorkLease(t, "dynamic-reserved")
	commit, err := registry.CommitDynamicInitial(DynamicInitial{
		State:       initial,
		SummaryPlan: ReservationUse{Kind: BroadSummaryFinal, Slots: 1, Bytes: terminalBytes},
	}, work)
	if err != nil {
		t.Fatalf("CommitDynamicInitial: %v", err)
	}
	if err := commit.Publication.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := commit.Publication.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	parent, code := registry.Lookup(commit.Token)
	if code != "" {
		t.Fatalf("Lookup parent code = %q", code)
	}
	totalBefore := registry.totalBytes
	ordinaryToken, err := registry.materializeSuccessor(parent, ordinary, ordinaryResult, ReservationUse{Kind: BroadSummaryFinal, Bytes: ordinaryBytes})
	if err != nil {
		t.Fatalf("materialize ordinary: %v", err)
	}
	ordinaryRef, code := registry.Lookup(ordinaryToken)
	if code != "" {
		t.Fatalf("Lookup ordinary code = %q", code)
	}
	lineage := registry.entries.lineage[parent.index]
	if registry.usedSlots != 3 || registry.lineages.reservedSlots[lineage] != 1 || registry.totalBytes != totalBefore+ordinaryBytes {
		t.Fatalf("ordinary accounting = used %d reserved %d bytes %d", registry.usedSlots, registry.lineages.reservedSlots[lineage], registry.totalBytes)
	}
	totalBeforeTerminal := registry.totalBytes
	terminalToken, err := registry.materializeSuccessor(ordinaryRef, terminal, terminalResult, ReservationUse{Kind: BroadSummaryFinal, Slots: 1, Bytes: terminalBytes, MustTerminalize: true})
	if err != nil {
		t.Fatalf("materialize terminal: %v", err)
	}
	terminalRef, code := registry.Lookup(terminalToken)
	if code != "" {
		t.Fatalf("Lookup terminal code = %q", code)
	}
	if registry.usedSlots != 3 || registry.lineages.reservedSlots[lineage] != 0 || registry.totalBytes != totalBeforeTerminal {
		t.Fatalf("terminal accounting = used %d reserved %d bytes %d", registry.usedSlots, registry.lineages.reservedSlots[lineage], registry.totalBytes)
	}
	if registry.entries.flags[terminalRef.index]&entryFlagMustTerminalize == 0 {
		t.Fatal("terminal child lacks must-terminalize flag")
	}
}
