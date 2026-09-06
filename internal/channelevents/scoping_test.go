package channelevents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// countEvents returns the .event filenames in dir, or nil when dir is absent.
// It returns names rather than a count so a wrong-population failure is visible
// in the assertion message instead of hiding behind a matching number.
func countEvents(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".event") {
			names = append(names, e.Name())
		}
	}
	return names
}

// TestTwoRigsDoNotShareAChannel is the regression test for gt-a3qs.
//
// Two rigs emit on the same channel name concurrently. Each rig's directory
// must hold exactly its own events and none of the other's — which is what makes
// await-event's single-consumer invariant true, and therefore what makes
// --cleanup safe again.
//
// On the pre-fix code both rigs wrote to events/refinery/, so each directory
// held all six events and this test fails.
func TestTwoRigsDoNotShareAChannel(t *testing.T) {
	// These assert the writer's output, so they opt back in past the test-binary
	// suppression gate (gastown-rv6), the same way TestEmitToTown does.
	t.Setenv(EnvSuppress, "0")
	townRoot := t.TempDir()

	const perRig = 3
	for i := 0; i < perRig; i++ {
		if _, err := EmitToTown(townRoot, "gastown", "refinery", "MQ_SUBMIT", []string{"source=sling"}); err != nil {
			t.Fatalf("emit gastown: %v", err)
		}
		if _, err := EmitToTown(townRoot, "liveop", "refinery", "MQ_SUBMIT", []string{"source=sling"}); err != nil {
			t.Fatalf("emit liveop: %v", err)
		}
	}

	gastownDir, err := ChannelDir(townRoot, "gastown", "refinery")
	if err != nil {
		t.Fatalf("ChannelDir gastown: %v", err)
	}
	liveopDir, err := ChannelDir(townRoot, "liveop", "refinery")
	if err != nil {
		t.Fatalf("ChannelDir liveop: %v", err)
	}

	if gastownDir == liveopDir {
		t.Fatalf("two rigs resolved to the same channel directory %q: "+
			"one rig's consumer would read and delete the other's events", gastownDir)
	}

	gastownEvents := countEvents(t, gastownDir)
	liveopEvents := countEvents(t, liveopDir)
	if len(gastownEvents) != perRig {
		t.Errorf("gastown channel holds %d events, want %d: %v", len(gastownEvents), perRig, gastownEvents)
	}
	if len(liveopEvents) != perRig {
		t.Errorf("liveop channel holds %d events, want %d: %v", len(liveopEvents), perRig, liveopEvents)
	}

	// A consumer must not be able to reach the other rig's files at all.
	for _, name := range gastownEvents {
		for _, other := range liveopEvents {
			if name == other {
				t.Errorf("event %s appears in both rigs' channels", name)
			}
		}
	}

	// And the legacy shared directory must be left empty, or the fix would only
	// have added a second copy rather than moved the events.
	if shared := countEvents(t, filepath.Join(townRoot, "events", "refinery")); len(shared) != 0 {
		t.Errorf("shared events/refinery/ still receives events: %v", shared)
	}
}

// TestEmittedEventRecordsItsRig pins the attribution field. Without it a moved
// or copied event is unattributable, which is what made the gt-a3qs loss
// forensically expensive.
func TestEmittedEventRecordsItsRig(t *testing.T) {
	// These assert the writer's output, so they opt back in past the test-binary
	// suppression gate (gastown-rv6), the same way TestEmitToTown does.
	t.Setenv(EnvSuppress, "0")
	townRoot := t.TempDir()
	path, err := EmitToTown(townRoot, "gastown", "refinery", "MQ_SUBMIT", []string{"source=sling"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := event["rig"]; got != "gastown" {
		t.Errorf("event rig = %v, want gastown (event: %s)", got, data)
	}
}

// TestTownScopedChannelStaysUnscoped pins the deliberate exception. The mayor is
// a town-level singleton; rig-scoping its channel would scatter its events into
// directories it does not watch, converting a correct channel into a silent one.
func TestTownScopedChannelStaysUnscoped(t *testing.T) {
	// These assert the writer's output, so they opt back in past the test-binary
	// suppression gate (gastown-rv6), the same way TestEmitToTown does.
	t.Setenv(EnvSuppress, "0")
	townRoot := t.TempDir()
	dir, err := ChannelDir(townRoot, TownScope, "mayor")
	if err != nil {
		t.Fatalf("ChannelDir: %v", err)
	}
	want := filepath.Join(townRoot, "events", "mayor")
	if dir != want {
		t.Errorf("town-scoped mayor channel = %q, want %q", dir, want)
	}

	path, err := EmitToTown(townRoot, TownScope, "mayor", "SLOT_OPEN", nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	data, _ := os.ReadFile(path)
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := event["rig"]; ok {
		t.Errorf("town-scoped event carries a rig field: %s", data)
	}
}

func TestChannelDirRejectsUnsafeNames(t *testing.T) {
	townRoot := t.TempDir()
	cases := []struct {
		name    string
		rig     string
		channel string
	}{
		{"channel traversal", TownScope, "../etc"},
		{"channel separator", TownScope, "foo/bar"},
		{"empty channel", TownScope, ""},
		{"rig traversal", "../etc", "refinery"},
		{"rig separator", "foo/bar", "refinery"},
		{"reserved rigs dir", TownScope, RigsDirName},
		{"reserved legacy dir", TownScope, LegacyDirName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ChannelDir(townRoot, tc.rig, tc.channel); err == nil {
				t.Errorf("ChannelDir(%q, %q) accepted an unsafe name", tc.rig, tc.channel)
			}
		})
	}

	// The reserved names are only reserved at TOWN scope; under a rig they are
	// ordinary channel names and must still work.
	for _, ch := range []string{RigsDirName, LegacyDirName} {
		if _, err := ChannelDir(townRoot, "gastown", ch); err != nil {
			t.Errorf("ChannelDir rejected rig-scoped channel %q: %v", ch, err)
		}
	}
}

// TestMoveEventNeverReplacesDestination pins the no-clobber contract that the
// migration and the daemon's legacy delivery both rely on. The destination is
// claimed with O_EXCL because those two movers can hold the same source file at
// once — the migration is a manual command, the daemon delivers on every
// heartbeat — and a Stat-then-rename would let one silently overwrite the other.
func TestMoveEventNeverReplacesDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.event")
	dst := filepath.Join(dir, "sub", "dst.event")

	if err := os.WriteFile(src, []byte(`{"type":"NEW"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte(`{"type":"EXISTING"}`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := MoveEvent(src, dst); err == nil {
		t.Error("MoveEvent replaced an existing destination")
	}

	// Neither event may be lost.
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != `{"type":"EXISTING"}` {
		t.Errorf("destination = %s (%v), want it untouched", got, err)
	}
	if got, err := os.ReadFile(src); err != nil || string(got) != `{"type":"NEW"}` {
		t.Errorf("source = %s (%v), want it left in place after a refused move", got, err)
	}
}

// TestMoveEventCreatesDestinationDirectory covers the ordinary path, including
// the mkdir the daemon relies on when a rig's channel directory does not exist
// yet.
func TestMoveEventCreatesDestinationDirectory(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.event")
	dst := filepath.Join(dir, "rigs", "gastown", "refinery", "1-1-1.event")

	if err := os.WriteFile(src, []byte(`{"type":"MQ_SUBMIT"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := MoveEvent(src, dst); err != nil {
		t.Fatalf("MoveEvent: %v", err)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != `{"type":"MQ_SUBMIT"}` {
		t.Errorf("destination = %s (%v)", got, err)
	}
	if _, err := os.Stat(src); err == nil {
		t.Error("source survived a successful move")
	}
}

// TestMoveEventConcurrentClaimsProduceOneWinner exercises the race the O_EXCL
// claim exists for: many movers, one destination. Exactly one must succeed, and
// the destination must never be a torn or replaced file.
func TestMoveEventConcurrentClaimsProduceOneWinner(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst", "1-1-1.event")
	const movers = 8

	srcs := make([]string, movers)
	for i := range srcs {
		srcs[i] = filepath.Join(dir, fmt.Sprintf("src-%d.event", i))
		if err := os.WriteFile(srcs[i], []byte(`{"type":"MQ_SUBMIT"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	results := make([]error, movers)
	for i := 0; i < movers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = MoveEvent(srcs[i], dst)
		}(i)
	}
	wg.Wait()

	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Errorf("%d movers succeeded, want exactly 1", winners)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != `{"type":"MQ_SUBMIT"}` {
		t.Errorf("destination = %s (%v), want one intact event", got, err)
	}
	// Every loser must have left its source alone, so nothing is lost.
	surviving := 0
	for _, src := range srcs {
		if _, err := os.Stat(src); err == nil {
			surviving++
		}
	}
	if surviving != movers-1 {
		t.Errorf("%d sources survived, want %d — a loser destroyed its own event", surviving, movers-1)
	}
}
