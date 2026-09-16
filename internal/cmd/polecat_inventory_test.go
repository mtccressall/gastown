package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/polecat"
)

func TestPolecatSessionSet(t *testing.T) {
	setupPolecatTestRegistry(t)
	sessions := newPolecatSessionSet([]string{
		"gt-thunder",
		"gt-crew-dom",
		"gp-mirelurk",
		"not-a-polecat",
	})

	if got, ok := sessions.lookup("gastown", "thunder"); !ok || got != "gt-thunder" {
		t.Fatalf("lookup gastown/thunder = %q, %v", got, ok)
	}
	if _, ok := sessions.lookup("gastown", "dom"); ok {
		t.Fatal("crew session should not be indexed as polecat")
	}
	if got := sessions.namesForRig("gastown"); len(got) != 1 || got[0] != "gt-thunder" {
		t.Fatalf("namesForRig(gastown) = %v", got)
	}
}

// pinOpenMRs makes the open-MR resolver deterministic for one test.
//
// It replaces the live lookup and CLEARS THE PROCESS CACHE on both sides. Both
// halves are required: the resolver alone is not enough because openMRsForRig
// memoises per rig for the life of the process, so a test that ran earlier —
// including one that reached the real town — would still be answering for this
// one. (gastown-8mm)
//
// Passing no ids pins a LOADED, EMPTY set: "the queue really is empty, so every
// recorded MR is closed". That is deliberately distinct from pinOpenMRsUnloaded
// below, which pins "the lookup did not run", and the two must not be conflated
// — they are the fail-open and fail-closed directions of the same branch.
func pinOpenMRs(t *testing.T, openIDs ...string) {
	t.Helper()
	ids := make(map[string]bool, len(openIDs))
	for _, id := range openIDs {
		ids[id] = true
	}
	pinOpenMRResolver(t, func(string) openMRSet {
		return openMRSet{ids: ids, loaded: true}
	})
}

// pinOpenMRsUnloaded pins the "lookup unavailable" case: a set that never
// loaded, which every caller must treat as unknown rather than as closed.
func pinOpenMRsUnloaded(t *testing.T) {
	t.Helper()
	pinOpenMRResolver(t, func(string) openMRSet { return openMRSet{} })
}

func pinOpenMRResolver(t *testing.T, resolve func(string) openMRSet) {
	t.Helper()
	old := resolveOpenMRsForRig
	resolveOpenMRsForRig = resolve
	resetOpenMRCacheForTest()
	t.Cleanup(func() {
		resolveOpenMRsForRig = old
		resetOpenMRCacheForTest()
	})
}

func resetOpenMRCacheForTest() {
	openMRCache.mu.Lock()
	defer openMRCache.mu.Unlock()
	openMRCache.byRig = map[string]openMRSet{}
}

func TestBuildPolecatInventoryItem(t *testing.T) {
	setupPolecatTestRegistry(t)
	// The two ActiveMR cases below assert PENDING_MR, which is only a claim
	// about this builder if the MR is known to be OPEN. Pin it. (gastown-8mm)
	pinOpenMRs(t, "gt-mr")
	sessions := newPolecatSessionSet([]string{"gt-running"})
	tests := []struct {
		name         string
		polecatName  string
		fields       *beads.AgentFields
		activeWork   *beads.Issue
		wantState    polecat.State
		wantIssue    string
		wantVerdict  string
		wantReusable bool
		wantRecovery bool
		wantCapacity bool
	}{
		{
			name:         "clean idle reusable",
			polecatName:  "idle",
			fields:       &beads.AgentFields{AgentState: string(beads.AgentStateIdle), CleanupStatus: string(polecat.CleanupClean)},
			wantState:    polecat.StateIdle,
			wantVerdict:  polecat.WorkstateVerdictSafeToNuke,
			wantReusable: true,
		},
		{
			name:         "hooked running is working capacity",
			polecatName:  "running",
			fields:       &beads.AgentFields{AgentState: string(beads.AgentStateIdle), CleanupStatus: string(polecat.CleanupClean)},
			activeWork:   &beads.Issue{ID: "gt-hook", Status: string(beads.IssueStatusHooked), Assignee: "gastown/polecats/running"},
			wantState:    polecat.StateWorking,
			wantIssue:    "gt-hook",
			wantVerdict:  polecat.WorkstateVerdictWorking,
			wantCapacity: true,
		},
		{
			name:         "open stopped is stalled capacity",
			polecatName:  "stopped",
			fields:       &beads.AgentFields{AgentState: string(beads.AgentStateIdle), CleanupStatus: string(polecat.CleanupClean)},
			activeWork:   &beads.Issue{ID: "gt-open", Status: string(beads.StatusOpen), Assignee: "gastown/polecats/stopped"},
			wantState:    polecat.StateStalled,
			wantIssue:    "gt-open",
			wantVerdict:  polecat.WorkstateVerdictNeedsRecovery,
			wantRecovery: true,
			wantCapacity: true,
		},
		{
			name:         "deferred protects without capacity",
			polecatName:  "deferred",
			fields:       &beads.AgentFields{AgentState: string(beads.AgentStateIdle), CleanupStatus: string(polecat.CleanupClean)},
			activeWork:   &beads.Issue{ID: "gt-deferred", Status: string(beads.StatusDeferred), Assignee: "gastown/polecats/deferred"},
			wantState:    polecat.StateIdle,
			wantIssue:    "gt-deferred",
			wantVerdict:  polecat.WorkstateVerdictNeedsRecovery,
			wantRecovery: true,
		},
		{
			name:         "hook fallback protects without capacity",
			polecatName:  "hookonly",
			fields:       &beads.AgentFields{AgentState: string(beads.AgentStateIdle), CleanupStatus: string(polecat.CleanupClean), HookBead: "gt-old"},
			wantState:    polecat.StateIdle,
			wantVerdict:  polecat.WorkstateVerdictNeedsRecovery,
			wantRecovery: true,
		},
		{
			name:         "paused agent state protects without capacity",
			polecatName:  "paused",
			fields:       &beads.AgentFields{AgentState: string(beads.AgentStatePaused), CleanupStatus: string(polecat.CleanupClean)},
			wantState:    polecat.StateIdle,
			wantVerdict:  polecat.WorkstateVerdictNeedsRecovery,
			wantRecovery: true,
		},
		{
			name:        "active mr is pending non capacity",
			polecatName: "pendingmr",
			fields:      &beads.AgentFields{AgentState: string(beads.AgentStateIdle), CleanupStatus: string(polecat.CleanupClean), ActiveMR: "gt-mr"},
			wantState:   polecat.StateIdle,
			wantVerdict: polecat.WorkstateVerdictPendingMR,
		},
		{
			name:         "done without active mr and clean cleanup is reusable",
			polecatName:  "done",
			fields:       &beads.AgentFields{AgentState: string(beads.AgentStateDone), CleanupStatus: string(polecat.CleanupClean)},
			wantState:    polecat.StateDone,
			wantVerdict:  polecat.WorkstateVerdictSafeToNuke,
			wantReusable: true,
		},
		{
			name:         "done without active mr blocks reuse when cleanup is dirty",
			polecatName:  "donedirty",
			fields:       &beads.AgentFields{AgentState: string(beads.AgentStateDone), CleanupStatus: string(polecat.CleanupUnpushed)},
			wantState:    polecat.StateDone,
			wantVerdict:  polecat.WorkstateVerdictNeedsRecovery,
			wantRecovery: true,
			wantCapacity: true,
		},
		{
			name:        "done with active mr remains pending",
			polecatName: "donepending",
			fields:      &beads.AgentFields{AgentState: string(beads.AgentStateDone), CleanupStatus: string(polecat.CleanupClean), ActiveMR: "gt-mr"},
			wantState:   polecat.StateDone,
			wantVerdict: polecat.WorkstateVerdictPendingMR,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := buildPolecatInventoryItem("gastown", tt.polecatName, tt.fields, tt.activeWork, sessions)
			if item.State != tt.wantState || item.Issue != tt.wantIssue || item.Disposition.Verdict != tt.wantVerdict || item.Disposition.Reusable != tt.wantReusable || item.Disposition.NeedsRecovery != tt.wantRecovery || item.Disposition.CountsTowardCapacity != tt.wantCapacity {
				t.Fatalf("item = %+v disposition=%+v", item, item.Disposition)
			}
		})
	}
}

func TestBuildPolecatInventoryItemActiveWorkLookupErrorFailsClosed(t *testing.T) {
	item := buildPolecatInventoryItemFromEvidence(
		"gastown",
		"lookup",
		&beads.AgentFields{AgentState: string(beads.AgentStateIdle), CleanupStatus: string(polecat.CleanupClean)},
		polecatActiveWorkLookupError(errors.New("bd failed")),
		polecatSessionSet{},
	)

	if item.Disposition.Reusable || item.Disposition.SafeToNuke || !item.Disposition.NeedsRecovery || item.Disposition.CountsTowardCapacity {
		t.Fatalf("lookup error disposition = %+v", item.Disposition)
	}
	if item.Disposition.Reason != "active-work" {
		t.Fatalf("reason = %q, want active-work", item.Disposition.Reason)
	}
	if len(item.Disposition.Blockers) != 1 || !strings.Contains(item.Disposition.Blockers[0], "lookup_error") {
		t.Fatalf("blockers = %v, want lookup_error", item.Disposition.Blockers)
	}
}

func TestPolecatSummaryIssueRankPrefersActiveWork(t *testing.T) {
	ordered := []*beads.Issue{
		{ID: "hook", Status: string(beads.IssueStatusHooked)},
		{ID: "progress", Status: string(beads.StatusInProgress)},
		{ID: "open", Status: string(beads.StatusOpen)},
		{ID: "blocked", Status: string(beads.StatusBlocked)},
		{ID: "deferred", Status: string(beads.StatusDeferred)},
	}
	for i := 1; i < len(ordered); i++ {
		if polecatSummaryIssueRank(ordered[i-1]) >= polecatSummaryIssueRank(ordered[i]) {
			t.Fatalf("rank(%s) should be before rank(%s)", ordered[i-1].Status, ordered[i].Status)
		}
	}
}

func TestPolecatNameFromAssignee(t *testing.T) {
	tests := []struct {
		assignee string
		wantName string
		wantOK   bool
	}{
		{assignee: "gastown/polecats/thunder", wantName: "thunder", wantOK: true},
		{assignee: "other/polecats/thunder"},
		{assignee: "gastown/crew/dom"},
		{assignee: "gastown/polecats/"},
		{assignee: "gastown/polecats/a/b"},
	}
	for _, tt := range tests {
		got, ok := polecatNameFromAssignee("gastown", tt.assignee)
		if got != tt.wantName || ok != tt.wantOK {
			t.Fatalf("polecatNameFromAssignee(%q) = %q, %v", tt.assignee, got, ok)
		}
	}
}

// TestBuildPolecatInventoryItemResolvesRecordedMRAgainstThePinnedOpenSet pins
// all three states of the open-MR lookup and asserts the disposition of each.
//
// The regression (gastown-8mm) was not that one verdict was wrong. It was that
// the verdict was decided by a store no test controlled: the lookup reached the
// live rig, so a fixture MR id was absent from a real seven-entry queue, read
// CLOSED, and the polecat holding it read SAFE_TO_NUKE. The same source passed
// outside a town. So asserting PENDING_MR alone is not a regression test for it
// — that assertion goes green wherever the ambient queue happens not to contain
// the fixture id, which includes CI.
//
// Hence all three branches, together, in one place. OPEN must block and CLOSED
// must free, or a resolver that answered a constant would satisfy the test; and
// UNLOADED must block, because "I could not look" is the fail-closed case that
// separates an unavailable lookup from an empty queue.
func TestBuildPolecatInventoryItemResolvesRecordedMRAgainstThePinnedOpenSet(t *testing.T) {
	setupPolecatTestRegistry(t)
	fields := &beads.AgentFields{
		AgentState:    string(beads.AgentStateIdle),
		CleanupStatus: string(polecat.CleanupClean),
		ActiveMR:      "gt-mr",
	}

	t.Run("open mr blocks reuse", func(t *testing.T) {
		pinOpenMRs(t, "gt-mr")
		d := buildPolecatInventoryItem("gastown", "pendingmr", fields, nil, polecatSessionSet{}).Disposition
		if d.Verdict != polecat.WorkstateVerdictPendingMR || d.Reusable || d.SafeToNuke {
			t.Fatalf("open MR disposition = %+v, want PENDING_MR and not reusable", d)
		}
	})

	t.Run("mr absent from a loaded set frees the pool", func(t *testing.T) {
		// The case 28b025cf was written for: a recorded MR that has since
		// merged must not pin its polecat forever.
		pinOpenMRs(t, "gt-some-other-mr")
		d := buildPolecatInventoryItem("gastown", "freed", fields, nil, polecatSessionSet{}).Disposition
		if d.Verdict != polecat.WorkstateVerdictSafeToNuke || !d.Reusable {
			t.Fatalf("closed MR disposition = %+v, want SAFE_TO_NUKE and reusable", d)
		}
	})

	t.Run("unavailable lookup fails closed", func(t *testing.T) {
		pinOpenMRsUnloaded(t)
		d := buildPolecatInventoryItem("gastown", "unknownmr", fields, nil, polecatSessionSet{}).Disposition
		if d.Verdict != polecat.WorkstateVerdictPendingMR || d.Reusable || d.SafeToNuke {
			t.Fatalf("unloaded-set disposition = %+v, want PENDING_MR and not reusable", d)
		}
	})
}

// TestOpenMRsForRigCachesPerRigAndQueriesOnce pins the invariant the cache
// exists for: one query per rig per process, and a failed query cached as
// UNLOADED rather than retried per polecat.
func TestOpenMRsForRigCachesPerRigAndQueriesOnce(t *testing.T) {
	calls := map[string]int{}
	pinOpenMRResolver(t, func(rigName string) openMRSet {
		calls[rigName]++
		if rigName == "unreachable" {
			return openMRSet{}
		}
		return openMRSet{ids: map[string]bool{"gt-mr": true}, loaded: true}
	})

	for i := 0; i < 3; i++ {
		if got := openMRsForRig("gastown").statusOf("gt-mr"); got != mrStatusOpen {
			t.Fatalf("call %d: statusOf = %v, want open", i, got)
		}
		if got := openMRsForRig("unreachable").statusOf("gt-mr"); got != mrStatusUnknown {
			t.Fatalf("call %d: unreachable statusOf = %v, want unknown", i, got)
		}
	}
	if calls["gastown"] != 1 || calls["unreachable"] != 1 {
		t.Fatalf("resolver calls = %v, want exactly 1 per rig", calls)
	}
}
