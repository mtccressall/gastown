package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/scheduler/capacity"
)

func TestShouldFireCrossRigEscalation_Debounces(t *testing.T) {
	townRoot := t.TempDir()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	if !shouldFireCrossRigEscalation(townRoot, "walletui", "hq", now) {
		t.Fatalf("first call must fire")
	}
	// Second call inside the debounce window must NOT fire.
	if shouldFireCrossRigEscalation(townRoot, "walletui", "hq", now.Add(30*time.Minute)) {
		t.Fatalf("second call inside debounce window must not fire")
	}
	// After the debounce window elapses, fire again.
	if !shouldFireCrossRigEscalation(townRoot, "walletui", "hq", now.Add(crossRigEscalationDebounce+time.Minute)) {
		t.Fatalf("call past debounce window must fire")
	}
}

// TestShouldFireCrossRigEscalation_SurvivesProcessRestart is the whole point of
// gt-83kc's second half, and the case the previous in-memory implementation
// could not represent.
//
// The daemon does not call this in-process. It runs `gt scheduler run` as a
// fresh process on every dispatch pass, so the debounce has to survive a
// process boundary or it suppresses nothing. An in-memory map passed the two
// tests above and still let every pass send Marc a new MEDIUM escalation.
//
// Dropping all in-process state between the two calls is what a new process
// looks like to this function; only the state file can carry the answer across.
func TestShouldFireCrossRigEscalation_SurvivesProcessRestart(t *testing.T) {
	townRoot := t.TempDir()
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)

	if !shouldFireCrossRigEscalation(townRoot, "liveop", "live", now) {
		t.Fatalf("first call must fire")
	}

	// Positive control: the state must actually be on disk. Without this the
	// test would also pass against an implementation that never persisted and
	// happened to be called twice in one process.
	if _, err := os.Stat(crossRigEscalationStatePath(townRoot)); err != nil {
		t.Fatalf("debounce state was not persisted: %v", err)
	}

	// A second process, five minutes later, carrying no memory of the first.
	if shouldFireCrossRigEscalation(townRoot, "liveop", "live", now.Add(5*time.Minute)) {
		t.Error("a fresh process re-fired inside the debounce window; the daemon " +
			"spawns one per dispatch pass, so this is an escalation per pass")
	}
}

func TestShouldFireCrossRigEscalation_KeyedByRigAndPrefix(t *testing.T) {
	townRoot := t.TempDir()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)

	if !shouldFireCrossRigEscalation(townRoot, "walletui", "hq", now) {
		t.Fatalf("walletui/hq first call must fire")
	}
	// Different rig — should fire independently.
	if !shouldFireCrossRigEscalation(townRoot, "furiosa", "hq", now) {
		t.Fatalf("furiosa/hq must fire (different rig)")
	}
	// Different prefix on same rig — should fire independently.
	if !shouldFireCrossRigEscalation(townRoot, "walletui", "wisp", now) {
		t.Fatalf("walletui/wisp must fire (different prefix)")
	}
	// Same (rig, prefix) repeats — debounced.
	if shouldFireCrossRigEscalation(townRoot, "walletui", "hq", now.Add(time.Minute)) {
		t.Fatalf("walletui/hq repeat must not fire")
	}
}

func TestDispatchSingleBeadRawReviewOnlyHookFailureClearsMetadata(t *testing.T) {
	townRoot, _, descPath := setupMutableBDRawSlingTest(t, "Keep this body.")

	prevSpawn := spawnPolecatForSling
	prevHook := hookBeadWithRetryWithTownRootFn
	t.Cleanup(func() {
		spawnPolecatForSling = prevSpawn
		hookBeadWithRetryWithTownRootFn = prevHook
	})
	spawnPolecatForSling = func(rigName string, opts SlingSpawnOptions) (*SpawnedPolecatInfo, error) {
		return &SpawnedPolecatInfo{
			RigName:     rigName,
			PolecatName: "toast",
			ClonePath:   filepath.Join(townRoot, "gastown", "polecats", "toast"),
		}, nil
	}
	hookBeadWithRetryWithTownRootFn = func(beadID, targetAgent, hookDir, townRoot string) error {
		assertHasRawReviewMetadata(t, readMutableBDDescription(t, descPath))
		return errors.New("forced hook failure")
	}

	_, err := dispatchSingleBead(capacity.PendingBead{
		ID:         "gt-context",
		WorkBeadID: "gt-rawrollback",
		TargetRig:  "gastown",
		Context: &capacity.SlingContextFields{
			WorkBeadID:  "gt-rawrollback",
			TargetRig:   "gastown",
			HookRawBead: true,
			NoMerge:     true,
			ReviewOnly:  true,
		},
	}, townRoot, "test")
	if err == nil {
		t.Fatal("expected scheduler dispatch hook failure")
	}
	assertNoRawReviewMetadata(t, readMutableBDDescription(t, descPath))
}

func TestListBlockedWorkBeadIDStatesPartialFailureFailsClosedPerGroup(t *testing.T) {
	townRoot := t.TempDir()
	townBeadsDir := filepath.Join(townRoot, ".beads")
	if err := os.MkdirAll(townBeadsDir, 0o755); err != nil {
		t.Fatalf("mkdir town beads: %v", err)
	}
	routes := []beads.Route{
		{Prefix: "a-", Path: "rig-a"},
		{Prefix: "b-", Path: "rig-b"},
	}
	if err := beads.WriteRoutes(townBeadsDir, routes); err != nil {
		t.Fatalf("write routes: %v", err)
	}

	blocked, unknown, err := listBlockedWorkBeadIDStatesWithRunner(townRoot, []string{"a-ready", "b-ready", "b-other"}, func(beadsDir string, groupedIDs []string) ([]byte, error) {
		switch groupedIDs[0][:1] {
		case "a":
			return []byte(`[{"id":"a-ready"}]`), nil
		case "b":
			return nil, fmt.Errorf("blocked query failed")
		default:
			return nil, fmt.Errorf("unexpected group %s", beadsDir)
		}
	})
	if err != nil {
		t.Fatalf("partial blocked query failure returned error: %v", err)
	}
	if !blocked["a-ready"] {
		t.Fatalf("a-ready should be marked blocked from successful group")
	}
	if unknown["a-ready"] {
		t.Fatalf("a-ready should not be blocked-unknown")
	}
	if !unknown["b-ready"] || !unknown["b-other"] {
		t.Fatalf("failed group IDs should be blocked-unknown, got %#v", unknown)
	}

	_, unknown, err = listBlockedWorkBeadIDStatesWithRunner(townRoot, []string{"a-ready", "b-ready"}, func(string, []string) ([]byte, error) {
		return []byte(`not-json`), nil
	})
	if err == nil {
		t.Fatalf("all blocked query JSON failures should return an error")
	}
	if !unknown["a-ready"] || !unknown["b-ready"] {
		t.Fatalf("all failed groups should mark every ID blocked-unknown, got %#v", unknown)
	}
}

func TestIsScheduledWorkBeadReadyFailsClosedForBlockedUnknown(t *testing.T) {
	info := beadStatusInfo{Status: "open"}
	if isScheduledWorkBeadReady("gt-ready", info, true, nil, map[string]bool{"gt-ready": true}) {
		t.Fatalf("blocked-unknown source must not be scheduler-ready")
	}
}
