package cmd

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/polecat"
)

// gastown-o8q: the scheduler's dispatch pass burned its 5-minute deadline
// walking every polecat in the town SERIALLY. Each polecat costs several `bd`
// and `git` subprocesses, one of which is a network `git ls-remote`, so the walk
// is latency-bound: 43 polecats at roughly a second each is most of the budget
// before any dispatch happens.
//
// This test fails if the walk goes back to being serial. It does not measure
// wall-clock -- a timing assertion is flaky on a loaded host and proves nothing
// on a fast one. It uses a BARRIER: every probe blocks until enough probes have
// arrived, which a serial implementation can never satisfy because it never has
// two in flight.
func TestCapacityFanOutRunsProbesConcurrently(t *testing.T) {
	orig := reconcilePolecatDispositionFn
	t.Cleanup(func() { reconcilePolecatDispositionFn = orig })

	// Deliberately below polecatCapacityProbeConcurrency, so the barrier is
	// satisfiable by the real semaphore rather than by luck.
	const probeCount = 4
	if probeCount > polecatCapacityProbeConcurrency {
		t.Fatalf("barrier of %d cannot be met by a fan-out bounded at %d",
			probeCount, polecatCapacityProbeConcurrency)
	}

	var mu sync.Mutex
	arrived := 0
	allArrived := make(chan struct{})
	reconcilePolecatDispositionFn = func(rigName, polecatName string, item polecatInventoryItem) polecat.WorkstateDisposition {
		mu.Lock()
		arrived++
		if arrived == probeCount {
			close(allArrived)
		}
		mu.Unlock()
		select {
		case <-allArrived:
		case <-time.After(3 * time.Second):
			// Serial execution lands here: probe 1 waits for probe 2, which has
			// not started and never will.
			t.Errorf("probe %s/%s waited 3s for %d concurrent probes; only %d ever arrived — "+
				"the capacity walk is serial again (gastown-o8q)", rigName, polecatName, probeCount, arrived)
		}
		return polecat.WorkstateDisposition{Reason: polecatName}
	}

	probes := make([]polecatCapacityProbe, probeCount)
	for i := range probes {
		probes[i] = polecatCapacityProbe{rigName: "gastown", polecatName: fmt.Sprintf("p%d", i)}
	}

	got := resolvePolecatCapacityContributions(probes, nil)
	if len(got) != probeCount {
		t.Fatalf("got %d contributions for %d probes", len(got), probeCount)
	}
}

// A parallel walk that returns results in completion order would make one town's
// snapshot depend on scheduling, so two operators reading `gt scheduler status`
// seconds apart could not tell a real change from a reordering. The counts
// themselves commute; reproducibility is what is being pinned.
func TestCapacityFanOutPreservesProbeOrder(t *testing.T) {
	origReconcile := reconcilePolecatDispositionFn
	t.Cleanup(func() { reconcilePolecatDispositionFn = origReconcile })

	// Later probes finish first, so any implementation that appends on
	// completion returns the reverse of the input.
	const probeCount = 6
	reconcilePolecatDispositionFn = func(rigName, polecatName string, item polecatInventoryItem) polecat.WorkstateDisposition {
		var idx int
		if _, err := fmt.Sscanf(polecatName, "p%d", &idx); err == nil {
			time.Sleep(time.Duration(probeCount-idx) * 10 * time.Millisecond)
		}
		return polecat.WorkstateDisposition{Reason: polecatName}
	}

	probes := make([]polecatCapacityProbe, probeCount)
	for i := range probes {
		probes[i] = polecatCapacityProbe{rigName: "gastown", polecatName: fmt.Sprintf("p%d", i)}
	}

	got := resolvePolecatCapacityContributions(probes, nil)
	for i, c := range got {
		want := fmt.Sprintf("p%d", i)
		if c.disposition.Reason != want {
			t.Fatalf("contribution %d is for %q, want %q — the fan-out returns results in "+
				"completion order, so a snapshot is no longer reproducible", i, c.disposition.Reason, want)
		}
	}
}

// The gt-b3a2 invariant, pinned on the path the scheduler now executes: every
// probe must be routed through the reconciler, not read off item.Disposition.
// A parallel rewrite is exactly the kind of change that drops a call.
func TestCapacityFanOutReconcilesEveryProbe(t *testing.T) {
	orig := reconcilePolecatDispositionFn
	t.Cleanup(func() { reconcilePolecatDispositionFn = orig })

	var mu sync.Mutex
	seen := map[string]int{}
	reconcilePolecatDispositionFn = func(rigName, polecatName string, item polecatInventoryItem) polecat.WorkstateDisposition {
		mu.Lock()
		seen[rigName+"/"+polecatName]++
		mu.Unlock()
		return polecat.WorkstateDisposition{Reusable: true, ReuseStatus: "idle-preserved"}
	}

	probes := []polecatCapacityProbe{
		{rigName: "gastown", polecatName: "shale"},
		{rigName: "liveop", polecatName: "chrome"},
		{rigName: "beadsrig", polecatName: "slit"},
	}
	got := resolvePolecatCapacityContributions(probes, nil)

	if len(seen) != len(probes) {
		t.Fatalf("reconciler saw %d distinct polecats, want %d: %v", len(seen), len(probes), seen)
	}
	for _, p := range probes {
		key := p.rigName + "/" + p.polecatName
		if seen[key] != 1 {
			t.Fatalf("reconciler called %d times for %s, want exactly 1 (gt-b3a2)", seen[key], key)
		}
	}

	snapshot := polecatCapacitySnapshot{Max: 4}
	for _, c := range got {
		applyWorkstateDispositionToCapacitySnapshot(&snapshot, c.state, c.disposition)
	}
	if snapshot.ReusableIdle != len(probes) {
		t.Fatalf("ReusableIdle = %d, want %d: the reconciled disposition is not reaching the snapshot",
			snapshot.ReusableIdle, len(probes))
	}
}

// An empty pool must not open a memo window or spawn anything. The dispatch pass
// runs on every daemon heartbeat, including in towns with no polecats at all.
func TestCapacityFanOutHandlesEmptyProbeSet(t *testing.T) {
	if got := resolvePolecatCapacityContributions(nil, nil); len(got) != 0 {
		t.Fatalf("got %d contributions for zero probes", len(got))
	}
}
