package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestWorkLeaseTransfersEachResourceOwnerExactlyOnce(t *testing.T) {
	ownerFactories := map[string]func() (Owner, *ownerProbe){
		"root-lease": func() (Owner, *ownerProbe) {
			probe := &ownerProbe{}
			return &fakeRootLease{ownerProbe: probe}, probe
		},
		"snapshot-buffer": func() (Owner, *ownerProbe) {
			probe := &ownerProbe{}
			return &fakeSnapshotBuffer{ownerProbe: probe}, probe
		},
		"parser-buffer": func() (Owner, *ownerProbe) {
			probe := &ownerProbe{}
			return &fakeParserBuffer{ownerProbe: probe}, probe
		},
		"cursor-lineage": func() (Owner, *ownerProbe) {
			probe := &ownerProbe{}
			return &fakeCursorLineage{ownerProbe: probe}, probe
		},
	}

	for name, factory := range ownerFactories {
		t.Run(name, func(t *testing.T) {
			coordinator, _, lease := startOwnedWorkLease(t, context.Background(), name)
			owner, probe := factory()
			if err := lease.Transfer(owner); err != nil {
				t.Fatalf("transfer failed: %v", err)
			}
			lease.mu.Lock()
			generation := lease.ownerGeneration
			lease.mu.Unlock()
			if generation != 1 {
				t.Fatalf("owner generation = %d, want 1", generation)
			}

			lease.WorkerReturned()
			lease.WorkerReturned()
			if releases, noCommits := probe.counts(); releases != 1 || noCommits != 0 {
				t.Fatalf("owner callbacks = release %d no-commit %d, want 1/0", releases, noCommits)
			}
			if err := lease.Transfer(&fakeRootLease{ownerProbe: &ownerProbe{}}); !errors.Is(err, errLeaseReturned) {
				t.Fatalf("transfer after return error = %v, want %v", err, errLeaseReturned)
			}
			assertCoordinatorCounts(t, coordinator, 0, 0)
		})
	}
}

func TestWorkLeaseTransferAfterNoCommitRetainsOriginalOwner(t *testing.T) {
	coordinator, _, lease := startOwnedWorkLease(t, context.Background(), "no-commit")
	originalProbe := &ownerProbe{}
	original := &fakeRootLease{ownerProbe: originalProbe}
	replacementProbe := &ownerProbe{}
	replacement := &fakeCursorLineage{ownerProbe: replacementProbe}
	if err := lease.Transfer(original); err != nil {
		t.Fatalf("initial transfer failed: %v", err)
	}
	lease.MarkNoCommit()
	if err := lease.Transfer(replacement); !errors.Is(err, errLeaseNoCommit) {
		t.Fatalf("transfer after no-commit error = %v, want %v", err, errLeaseNoCommit)
	}
	lease.mu.Lock()
	generation := lease.ownerGeneration
	lease.mu.Unlock()
	if generation != 1 {
		t.Fatalf("owner generation after rejected transfer = %d, want 1", generation)
	}
	lease.WorkerReturned()

	if releases, noCommits := originalProbe.counts(); releases != 1 || noCommits != 1 {
		t.Fatalf("original callbacks = release %d no-commit %d, want 1/1", releases, noCommits)
	}
	if releases, noCommits := replacementProbe.counts(); releases != 0 || noCommits != 0 {
		t.Fatalf("replacement callbacks = release %d no-commit %d, want 0/0", releases, noCommits)
	}
	assertCoordinatorCounts(t, coordinator, 0, 0)
}

func TestWorkLeaseTransferRacesChooseOneFinalOwner(t *testing.T) {
	for _, terminal := range []string{"cancel", "timeout", "return"} {
		t.Run(terminal, func(t *testing.T) {
			for iteration := 0; iteration < 200; iteration++ {
				parent := context.Background()
				expire := func() {}
				if terminal == "timeout" {
					var cancelParent context.CancelFunc
					parent, cancelParent = context.WithCancel(parent)
					expire = cancelParent
				}
				coordinator, reservation, lease := startOwnedWorkLease(t, parent, fmt.Sprintf("%s-%d", terminal, iteration))
				originalProbe := &ownerProbe{}
				original := &fakeRootLease{ownerProbe: originalProbe}
				replacementProbe := &ownerProbe{}
				replacement := &fakeParserBuffer{ownerProbe: replacementProbe}
				if err := lease.Transfer(original); err != nil {
					t.Fatalf("iteration %d initial transfer failed: %v", iteration, err)
				}

				start := make(chan struct{})
				transferResult := make(chan error, 1)
				terminalDone := make(chan struct{})
				go func() {
					<-start
					transferResult <- lease.Transfer(replacement)
				}()
				go func() {
					<-start
					switch terminal {
					case "cancel":
						reservation.Cancel()
					case "timeout":
						expire()
						reservation.Cancel()
					case "return":
						lease.WorkerReturned()
					}
					close(terminalDone)
				}()
				close(start)
				transferErr := <-transferResult
				awaitSignal(t, terminalDone, "transfer race terminal")
				if terminal != "return" {
					lease.WorkerReturned()
				}

				originalReleases, originalNoCommits := originalProbe.counts()
				replacementReleases, replacementNoCommits := replacementProbe.counts()
				if total := originalReleases + replacementReleases; total != 1 {
					t.Fatalf("iteration %d release total = %d (%d/%d), want 1", iteration, total, originalReleases, replacementReleases)
				}
				if transferErr == nil {
					if originalReleases != 0 || replacementReleases != 1 {
						t.Fatalf("iteration %d successful transfer released owners %d/%d, want 0/1", iteration, originalReleases, replacementReleases)
					}
				} else if originalReleases != 1 || replacementReleases != 0 {
					t.Fatalf("iteration %d rejected transfer released owners %d/%d, want 1/0: %v", iteration, originalReleases, replacementReleases, transferErr)
				}
				if transferErr != nil {
					wantError := errLeaseNoCommit
					if terminal == "return" {
						wantError = errLeaseReturned
					}
					if !errors.Is(transferErr, wantError) {
						t.Fatalf("iteration %d transfer error = %v, want %v", iteration, transferErr, wantError)
					}
				}
				lease.mu.Lock()
				generation := lease.ownerGeneration
				retainedRelease := lease.release
				lease.mu.Unlock()
				wantGeneration := uint64(1)
				if transferErr == nil {
					wantGeneration = 2
				}
				if generation != wantGeneration {
					t.Fatalf("iteration %d owner generation = %d, want %d", iteration, generation, wantGeneration)
				}
				if retainedRelease != nil {
					t.Fatalf("iteration %d returned lease retained coordinator release hook", iteration)
				}
				if terminal == "return" {
					if total := originalNoCommits + replacementNoCommits; total != 0 {
						t.Fatalf("iteration %d return race no-commit total = %d, want 0", iteration, total)
					}
				} else if total := originalNoCommits + replacementNoCommits; total != 1 {
					t.Fatalf("iteration %d %s race no-commit total = %d, want 1", iteration, terminal, total)
				}
				assertCoordinatorCounts(t, coordinator, 0, 0)
				assertCoordinatorRequestCount(t, coordinator, 0)
			}
		})
	}
}

func TestWorkLeaseDoesNotNotifyOwnerAfterReturn(t *testing.T) {
	_, _, lease := startOwnedWorkLease(t, context.Background(), "returned-no-commit")
	probe := &ownerProbe{}
	if err := lease.Transfer(&fakeSnapshotBuffer{ownerProbe: probe}); err != nil {
		t.Fatalf("transfer failed: %v", err)
	}
	lease.WorkerReturned()
	lease.MarkNoCommit()
	if releases, noCommits := probe.counts(); releases != 1 || noCommits != 0 {
		t.Fatalf("callbacks after return = release %d no-commit %d, want 1/0", releases, noCommits)
	}
}

func startOwnedWorkLease(t *testing.T, parent context.Context, id string) (*Coordinator, Reservation, *WorkLease) {
	t.Helper()
	coordinator := NewCoordinator(Limits{MaxConcurrent: 1, QueueTimeout: time.Second})
	reservation, admitted := coordinator.Admit(parent, []byte(id))
	if admitted != AdmitRun {
		t.Fatalf("admission = %d, want run", admitted)
	}
	lease, started := reservation.Start()
	if started.Kind != StartRun || lease == nil {
		t.Fatalf("start = (%p, %#v), want work lease", lease, started)
	}
	return coordinator, reservation, lease
}

type ownerProbe struct {
	mu        sync.Mutex
	releases  int
	noCommits int
}

func (owner *ownerProbe) Release() {
	owner.mu.Lock()
	owner.releases++
	owner.mu.Unlock()
}

func (owner *ownerProbe) MarkNoCommit() {
	owner.mu.Lock()
	owner.noCommits++
	owner.mu.Unlock()
}

func (owner *ownerProbe) counts() (int, int) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.releases, owner.noCommits
}

type fakeRootLease struct {
	*ownerProbe
}

type fakeSnapshotBuffer struct {
	*ownerProbe
}

type fakeParserBuffer struct {
	*ownerProbe
}

type fakeCursorLineage struct {
	*ownerProbe
}
