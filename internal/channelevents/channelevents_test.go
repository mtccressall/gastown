package channelevents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitToTown(t *testing.T) {
	// This test exercises the writer itself, so it opts out of the test-binary
	// suppression the package applies by default (gastown-rv6).
	t.Setenv(EnvSuppress, "0")
	townRoot := t.TempDir()

	path, err := EmitToTown(townRoot, "refinery", "MERGE_READY", []string{
		"source=witness",
		"rig=dashboard",
	})
	if err != nil {
		t.Fatalf("EmitToTown failed: %v", err)
	}

	if !strings.HasSuffix(path, ".event") {
		t.Errorf("expected .event suffix, got %q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading event file: %v", err)
	}

	var event map[string]interface{}
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("unmarshaling event: %v", err)
	}

	if event["type"] != "MERGE_READY" {
		t.Errorf("type = %v, want MERGE_READY", event["type"])
	}
	if event["channel"] != "refinery" {
		t.Errorf("channel = %v, want refinery", event["channel"])
	}

	payload, ok := event["payload"].(map[string]interface{})
	if !ok {
		t.Fatal("payload is not a map")
	}
	if payload["source"] != "witness" {
		t.Errorf("payload.source = %v, want witness", payload["source"])
	}
	if payload["rig"] != "dashboard" {
		t.Errorf("payload.rig = %v, want dashboard", payload["rig"])
	}
}

func TestEmitToTown_InvalidChannel(t *testing.T) {
	// No opt-in: name validation is rejected ahead of the suppression gate,
	// so this path is the same in a test binary and in production.
	t.Parallel()
	_, err := EmitToTown(t.TempDir(), "../escape", "TEST", nil)
	if err == nil {
		t.Error("expected error for invalid channel name")
	}
}

func TestEmitToTown_UniqueFilenames(t *testing.T) {
	// This test exercises the writer itself, so it opts out of the test-binary
	// suppression the package applies by default (gastown-rv6).
	t.Setenv(EnvSuppress, "0")
	townRoot := t.TempDir()
	seen := make(map[string]bool)

	for i := 0; i < 10; i++ {
		path, err := EmitToTown(townRoot, "test", "EVENT", nil)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if seen[path] {
			t.Errorf("duplicate filename: %s", path)
		}
		seen[path] = true
	}
}

func TestValidChannelName(t *testing.T) {
	t.Parallel()
	valid := []string{"refinery", "witness", "my-channel", "test_chan", "abc123"}
	for _, name := range valid {
		if !ValidChannelName.MatchString(name) {
			t.Errorf("%q should be valid", name)
		}
	}

	invalid := []string{"../escape", "has space", "has/slash", "", "has.dot"}
	for _, name := range invalid {
		if ValidChannelName.MatchString(name) {
			t.Errorf("%q should be invalid", name)
		}
	}
}

func TestEmitToTown_CreatesDirectory(t *testing.T) {
	// This test exercises the writer itself, so it opts out of the test-binary
	// suppression the package applies by default (gastown-rv6).
	t.Setenv(EnvSuppress, "0")
	townRoot := t.TempDir()
	channelDir := filepath.Join(townRoot, "events", "newchannel")

	if _, err := os.Stat(channelDir); !os.IsNotExist(err) {
		t.Fatal("channel dir should not exist yet")
	}

	_, err := EmitToTown(townRoot, "newchannel", "TEST", nil)
	if err != nil {
		t.Fatalf("EmitToTown failed: %v", err)
	}

	if _, err := os.Stat(channelDir); err != nil {
		t.Errorf("channel dir should exist after emit: %v", err)
	}
}

// TestEmitSuppressedInsideTestBinary asserts the gate is ENGAGED by default in
// a test binary (gastown-rv6). Without it, `go test` from a worktree under the
// town appended MQ_SUBMIT events to the production events/refinery directory,
// where a refinery consumes them as real wake-ups.
//
// This is the negative half of a pair: TestEmitToTown above opts back in and
// asserts the writer still works, so a gate stuck ON cannot pass both.
func TestEmitSuppressedInsideTestBinary(t *testing.T) {
	townRoot := t.TempDir()

	path, err := EmitToTown(townRoot, "refinery", "MQ_SUBMIT", []string{"message=test message"})
	if err != nil {
		t.Fatalf("a suppressed emit must not error, got %v", err)
	}
	if path != "" {
		t.Errorf("a suppressed emit must return an empty path, got %q", path)
	}

	// Nothing on disk: not the event, and not the directory either. The mkdir
	// used to happen ahead of the write, which left production channel dirs
	// behind even when no event was written.
	if _, err := os.Stat(filepath.Join(townRoot, "events")); !os.IsNotExist(err) {
		t.Errorf("a suppressed emit created %s/events (stat err: %v)", townRoot, err)
	}
}

// TestEmitSuppressionIsOptOut pins the escape hatch itself: the same call that
// writes nothing above must write when a test opts back in. Tests in other
// packages depend on this contract, so it is asserted rather than assumed.
func TestEmitSuppressionIsOptOut(t *testing.T) {
	townRoot := t.TempDir()

	if _, err := EmitToTown(townRoot, "refinery", "MQ_SUBMIT", nil); err != nil {
		t.Fatalf("suppressed emit: %v", err)
	}
	suppressed, err := filepath.Glob(filepath.Join(townRoot, "events", "refinery", "*.event"))
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv(EnvSuppress, "0")
	if _, err := EmitToTown(townRoot, "refinery", "MQ_SUBMIT", nil); err != nil {
		t.Fatalf("opted-in emit: %v", err)
	}
	allowed, err := filepath.Glob(filepath.Join(townRoot, "events", "refinery", "*.event"))
	if err != nil {
		t.Fatal(err)
	}

	if len(suppressed) != 0 {
		t.Errorf("suppressed emit wrote %d event(s), want 0", len(suppressed))
	}
	if len(allowed) != 1 {
		t.Errorf("opted-in emit left %d event(s), want 1", len(allowed))
	}
}
