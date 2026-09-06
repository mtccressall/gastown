package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/channelevents"
)

// writeRigsConfig registers rigs so isRegisteredRig can resolve them.
func writeRigsConfig(t *testing.T, townRoot string, rigs ...string) {
	t.Helper()
	entries := make(map[string]map[string]string, len(rigs))
	for _, r := range rigs {
		entries[r] = map[string]string{"path": filepath.Join(townRoot, r)}
	}
	data, err := json.Marshal(map[string]any{"rigs": entries})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(townRoot, "mayor")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rigs.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// writeLegacyEvent writes an event into the pre-scoping shared directory.
func writeLegacyEvent(t *testing.T, townRoot, channel, name, body string) string {
	t.Helper()
	dir := filepath.Join(townRoot, "events", channel)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAttributeEventRig(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown", "liveop")

	cases := []struct {
		name string
		body string
		want string
	}{
		{"top-level rig field", `{"type":"MQ_SUBMIT","rig":"gastown"}`, "gastown"},
		{"payload rig field", `{"type":"MERGE_READY","payload":{"rig":"liveop"}}`, "liveop"},
		{"no rig anywhere", `{"type":"MQ_SUBMIT","payload":{"source":"sling"}}`, ""},
		{"unregistered rig", `{"type":"MQ_SUBMIT","rig":"ghost"}`, ""},
		{"unsafe rig name", `{"type":"MQ_SUBMIT","rig":"../etc"}`, ""},
		{"unparseable", `not json`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeLegacyEvent(t, t.TempDir(), "refinery", "1-1-1.event", tc.body)
			if got := attributeEventRig(townRoot, path); got != tc.want {
				t.Errorf("attributeEventRig = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEventsMigrateNeverDeletes is the property that matters most here. The
// mitigation this command replaces was "prune the directory", which reproduces
// the original theft with a broom: the shared directory holds events belonging
// to a rig that is still live, and they carry no rig field to sort them by.
// Every input file must still exist somewhere afterwards.
func TestEventsMigrateNeverDeletes(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown", "liveop")

	attributed := writeLegacyEvent(t, townRoot, "refinery", "1-1-1.event",
		`{"type":"MERGE_READY","payload":{"rig":"liveop"}}`)
	orphan := writeLegacyEvent(t, townRoot, "refinery", "2-2-2.event",
		`{"type":"MQ_SUBMIT","payload":{"source":"sling"}}`)

	result := runMigrateForTest(t, townRoot, false)

	if result.Scanned != 2 {
		t.Fatalf("scanned %d events, want 2", result.Scanned)
	}
	if result.Delivered != 1 || result.Archived != 1 {
		t.Errorf("delivered=%d archived=%d, want 1 and 1", result.Delivered, result.Archived)
	}

	// The attributable one reaches its own rig, and only that rig.
	liveopDir, _ := channelevents.ChannelDir(townRoot, "liveop", "refinery")
	if _, err := os.Stat(filepath.Join(liveopDir, "1-1-1.event")); err != nil {
		t.Errorf("attributed event did not reach liveop: %v", err)
	}
	gastownDir, _ := channelevents.ChannelDir(townRoot, "gastown", "refinery")
	if _, err := os.Stat(filepath.Join(gastownDir, "1-1-1.event")); err == nil {
		t.Error("liveop's event was also delivered to gastown")
	}

	// The unattributable one is archived, not guessed at and not destroyed.
	legacyDir, _ := channelevents.LegacyChannelDir(townRoot, "refinery")
	if _, err := os.Stat(filepath.Join(legacyDir, "2-2-2.event")); err != nil {
		t.Errorf("unattributable event was not archived: %v", err)
	}

	// Nothing was left behind in the shared directory.
	for _, src := range []string{attributed, orphan} {
		if _, err := os.Stat(src); err == nil {
			t.Errorf("%s still sits in the shared directory", filepath.Base(src))
		}
	}
}

func TestEventsMigrateDryRunMovesNothing(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown")
	src := writeLegacyEvent(t, townRoot, "refinery", "1-1-1.event",
		`{"type":"MQ_SUBMIT","rig":"gastown"}`)

	result := runMigrateForTest(t, townRoot, true)

	if result.Scanned != 1 || len(result.Moves) != 1 {
		t.Errorf("dry run reported scanned=%d moves=%d, want 1 and 1", result.Scanned, len(result.Moves))
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("dry run moved the file: %v", err)
	}
	dstDir, _ := channelevents.ChannelDir(townRoot, "gastown", "refinery")
	if _, err := os.Stat(filepath.Join(dstDir, "1-1-1.event")); err == nil {
		t.Error("dry run created the destination file")
	}
}

// TestEventsMigrateLeavesTownChannelsAlone pins the deliberate exclusion: the
// mayor's directory is already correctly shaped and moving it would strand the
// mayor's events where nothing watches.
func TestEventsMigrateLeavesTownChannelsAlone(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown")
	src := writeLegacyEvent(t, townRoot, "mayor", "1-1-1.event", `{"type":"SLOT_OPEN"}`)

	result := runMigrateForTest(t, townRoot, false)

	if result.Scanned != 0 {
		t.Errorf("scanned %d events, want 0 — the mayor channel must not be migrated", result.Scanned)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("mayor event was moved: %v", err)
	}
}

// runMigrateForTest runs the migration against an explicit town root.
func runMigrateForTest(t *testing.T, townRoot string, dryRun bool) *EventsMigrateResult {
	t.Helper()
	result, err := collectEventsMigrate(townRoot, dryRun)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return result
}

// TestEventsMigrateDoesNotCountFailedMoves is the regression test for the codex
// P2 on this change. Counts and the move list used to be updated before the
// file was actually moved, so a destination collision reported as a completed
// migration — a job claiming success on partial completion, which is exactly
// how an offsite backup layer went a whole run cycle having never executed.
func TestEventsMigrateDoesNotCountFailedMoves(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown")
	writeLegacyEvent(t, townRoot, "refinery", "1-1-1.event", `{"type":"MQ_SUBMIT","rig":"gastown"}`)

	// Pre-place a file at the destination so the move must refuse to clobber.
	dstDir, err := channelevents.ChannelDir(townRoot, "gastown", "refinery")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "1-1-1.event"), []byte(`{"pre":"existing"}`), 0644); err != nil {
		t.Fatal(err)
	}

	result := runMigrateForTest(t, townRoot, false)

	if result.Scanned != 1 {
		t.Fatalf("scanned %d, want 1", result.Scanned)
	}
	if result.Delivered != 0 || result.Archived != 0 {
		t.Errorf("delivered=%d archived=%d, want 0 and 0: a failed move was counted as done",
			result.Delivered, result.Archived)
	}
	if len(result.Moves) != 0 {
		t.Errorf("failed move appears in the move list: %+v", result.Moves)
	}
	if len(result.Errors) != 1 {
		t.Errorf("errors = %v, want exactly one", result.Errors)
	}

	// The source must survive a failed move — this command never loses an event.
	if _, err := os.Stat(filepath.Join(townRoot, "events", "refinery", "1-1-1.event")); err != nil {
		t.Errorf("source event lost after a failed move: %v", err)
	}
	// And the pre-existing destination must not have been clobbered.
	data, err := os.ReadFile(filepath.Join(dstDir, "1-1-1.event"))
	if err != nil || string(data) != `{"pre":"existing"}` {
		t.Errorf("destination was overwritten: %s (%v)", data, err)
	}
}

// TestEventsMigrateReportsUnreadableDir pins that a read failure is reported
// rather than read as an empty directory. Treating every ReadDir error as
// "nothing to drain" makes a permission problem look like a clean migration.
func TestEventsMigrateReportsUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode bits do not deny access")
	}
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown")
	writeLegacyEvent(t, townRoot, "refinery", "1-1-1.event", `{"type":"MQ_SUBMIT"}`)

	srcDir := filepath.Join(townRoot, "events", "refinery")
	if err := os.Chmod(srcDir, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(srcDir, 0755) })

	result, err := collectEventsMigrate(townRoot, false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(result.Errors) == 0 {
		t.Error("unreadable source directory reported as nothing to drain")
	}
	if result.Scanned != 0 {
		t.Errorf("scanned = %d, want 0", result.Scanned)
	}
}
