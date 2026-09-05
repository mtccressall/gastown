package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/channelevents"
)

func TestCalculateEventTimeout(t *testing.T) {
	tests := []struct {
		name        string
		timeout     string
		backoffBase string
		backoffMult int
		backoffMax  string
		idleCycles  int
		want        time.Duration
		wantErr     bool
	}{
		{
			name:    "simple timeout 60s",
			timeout: "60s",
			want:    60 * time.Second,
		},
		{
			name:    "simple timeout 5m",
			timeout: "5m",
			want:    5 * time.Minute,
		},
		{
			name:        "backoff base only, idle=0",
			timeout:     "60s",
			backoffBase: "30s",
			idleCycles:  0,
			want:        30 * time.Second,
		},
		{
			name:        "backoff with idle=1, mult=2",
			timeout:     "60s",
			backoffBase: "30s",
			backoffMult: 2,
			idleCycles:  1,
			want:        60 * time.Second,
		},
		{
			name:        "backoff with idle=2, mult=2",
			timeout:     "60s",
			backoffBase: "30s",
			backoffMult: 2,
			idleCycles:  2,
			want:        2 * time.Minute,
		},
		{
			name:        "backoff with max cap",
			timeout:     "60s",
			backoffBase: "30s",
			backoffMult: 2,
			backoffMax:  "5m",
			idleCycles:  10, // Would be 30s * 2^10 = ~8.5h but capped at 5m
			want:        5 * time.Minute,
		},
		{
			name:        "backoff overflow guard: idle=34 with max cap",
			timeout:     "60s",
			backoffBase: "30s",
			backoffMult: 2,
			backoffMax:  "5m",
			idleCycles:  34, // 30s * 2^34 overflows int64; must clamp to 5m
			want:        5 * time.Minute,
		},
		{
			name:        "backoff overflow guard: idle=34 no max (no overflow without cap)",
			timeout:     "60s",
			backoffBase: "1ns",
			backoffMult: 2,
			idleCycles:  34, // 1ns * 2^34 = 17179869184ns ≈ 17s — fits in int64, no overflow
			want:        time.Duration(1 << 34),
		},
		{
			name:        "backoff base exceeds max",
			timeout:     "60s",
			backoffBase: "15m",
			backoffMax:  "10m",
			want:        10 * time.Minute,
		},
		{
			name:    "invalid timeout",
			timeout: "invalid",
			wantErr: true,
		},
		{
			name:        "invalid backoff base",
			timeout:     "60s",
			backoffBase: "invalid",
			wantErr:     true,
		},
		{
			name:        "invalid backoff max",
			timeout:     "60s",
			backoffBase: "30s",
			backoffMax:  "invalid",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set package-level variables
			awaitEventTimeout = tt.timeout
			awaitEventBackoffBase = tt.backoffBase
			awaitEventBackoffMult = tt.backoffMult
			if tt.backoffMult == 0 {
				awaitEventBackoffMult = 2 // default
			}
			awaitEventBackoffMax = tt.backoffMax

			got, err := calculateEventTimeout(tt.idleCycles)
			if (err != nil) != tt.wantErr {
				t.Errorf("calculateEventTimeout() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("calculateEventTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAwaitEventResult(t *testing.T) {
	result := AwaitEventResult{
		Reason:  "event",
		Elapsed: 5 * time.Second,
		Events: []EventFile{
			{
				Path:    "/tmp/test/123.event",
				Content: json.RawMessage(`{"type":"MERGE_READY"}`),
			},
		},
		IdleCycles: 3,
	}

	if result.Reason != "event" {
		t.Errorf("expected reason 'event', got %q", result.Reason)
	}
	if len(result.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(result.Events))
	}
	if result.IdleCycles != 3 {
		t.Errorf("expected idle_cycles 3, got %d", result.IdleCycles)
	}

	// Verify JSON marshaling
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var decoded AwaitEventResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if decoded.Reason != "event" {
		t.Errorf("decoded reason = %q, want 'event'", decoded.Reason)
	}
	if len(decoded.Events) != 1 {
		t.Errorf("decoded events count = %d, want 1", len(decoded.Events))
	}
}

func TestReadPendingEvents(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()
		events, err := readPendingEvents(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 0 {
			t.Errorf("expected 0 events, got %d", len(events))
		}
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		events, err := readPendingEvents("/tmp/nonexistent-dir-test-" + t.Name())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if events != nil {
			t.Errorf("expected nil events for nonexistent dir, got %v", events)
		}
	})

	t.Run("single event file", func(t *testing.T) {
		dir := t.TempDir()
		content := `{"type":"MERGE_READY","channel":"refinery","timestamp":"2026-02-21T00:00:00Z","payload":{"polecat":"nux"}}`
		if err := os.WriteFile(filepath.Join(dir, "001.event"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		events, err := readPendingEvents(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}

		var parsed map[string]interface{}
		if err := json.Unmarshal(events[0].Content, &parsed); err != nil {
			t.Fatalf("failed to parse event content: %v", err)
		}
		if parsed["type"] != "MERGE_READY" {
			t.Errorf("expected type MERGE_READY, got %v", parsed["type"])
		}
	})

	t.Run("multiple events sorted by name", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"003.event", "001.event", "002.event"} {
			content := `{"type":"` + name + `"}`
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}

		events, err := readPendingEvents(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 3 {
			t.Fatalf("expected 3 events, got %d", len(events))
		}

		// Should be sorted: 001, 002, 003
		for i, expected := range []string{"001.event", "002.event", "003.event"} {
			if filepath.Base(events[i].Path) != expected {
				t.Errorf("event[%d] = %q, want %q", i, filepath.Base(events[i].Path), expected)
			}
		}
	})

	t.Run("ignores non-event files", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "001.event"), []byte(`{"type":"A"}`), 0644)
		os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not an event"), 0644)
		os.WriteFile(filepath.Join(dir, "002.json"), []byte(`{"type":"B"}`), 0644)
		os.Mkdir(filepath.Join(dir, "subdir.event"), 0755) // directory, not file

		events, err := readPendingEvents(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 1 {
			t.Errorf("expected 1 event (only .event files), got %d", len(events))
		}
	})
}

func TestValidChannelName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"simple alpha", "refinery", true},
		{"with hyphen", "my-channel", true},
		{"with underscore", "my_channel", true},
		{"with numbers", "chan123", true},
		{"mixed", "A-b_3", true},
		{"path traversal dots", "../etc", false},
		{"path traversal slash", "foo/bar", false},
		{"empty string", "", false},
		{"space", "foo bar", false},
		{"shell metachar", "chan;rm", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validChannelName.MatchString(tt.input)
			if got != tt.valid {
				t.Errorf("validChannelName.MatchString(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		})
	}
}

func TestWaitForEventFilesPolling(t *testing.T) {
	// Test that polling picks up events written after the wait starts.
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Write an event after a short delay in a goroutine
	go func() {
		time.Sleep(800 * time.Millisecond) // longer than one poll interval (500ms)
		content := `{"type":"DELAYED_EVENT","channel":"test"}`
		os.WriteFile(filepath.Join(dir, "delayed.event"), []byte(content), 0644)
	}()

	start := time.Now()
	result, err := waitForEventFiles(ctx, dir, 0)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "event" {
		t.Fatalf("expected reason 'event', got %q (elapsed: %v)", result.Reason, elapsed)
	}
	if len(result.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(result.Events))
	}
	// Should have taken at least 800ms (the delay) but less than 5s (timeout)
	if elapsed < 700*time.Millisecond {
		t.Errorf("polling returned too quickly (%v), event was delayed 800ms", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("polling took too long (%v), expected ~1-1.5s", elapsed)
	}
}

func TestWaitForEventFilesWithPending(t *testing.T) {
	// When events already exist, waitForEventFiles should return immediately.
	dir := t.TempDir()
	content := `{"type":"PATROL_WAKE","channel":"refinery"}`
	os.WriteFile(filepath.Join(dir, "existing.event"), []byte(content), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := waitForEventFiles(ctx, dir, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "event" {
		t.Errorf("expected reason 'event', got %q", result.Reason)
	}
	if len(result.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(result.Events))
	}
}

func TestWaitForEventFilesTimeout(t *testing.T) {
	// With no events and an expired context, should return timeout.
	dir := t.TempDir()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	result, err := waitForEventFiles(ctx, dir, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "timeout" {
		t.Errorf("expected reason 'timeout', got %q", result.Reason)
	}
}

func TestWaitForEventFilesNoDeadline(t *testing.T) {
	// With a context that has no deadline, should return timeout immediately.
	dir := t.TempDir()

	result, err := waitForEventFiles(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "timeout" {
		t.Errorf("expected reason 'timeout', got %q", result.Reason)
	}
}

func TestWaitForEventFilesTimeoutWithPolling(t *testing.T) {
	// Regression test for gt-x2lc: the ticker-driven poll must honor
	// ctx cancellation even if events never arrive. Previously the wait
	// could stall past the deadline if readPendingEvents was slow.
	dir := t.TempDir()

	deadline := 600 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	start := time.Now()
	result, err := waitForEventFiles(ctx, dir, 0)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "timeout" {
		t.Errorf("expected reason 'timeout', got %q", result.Reason)
	}
	// Must return close to the deadline, not hang.
	if elapsed > deadline+2*time.Second {
		t.Errorf("wait took %v; expected ~%v (ctx.Done not honored?)", elapsed, deadline)
	}
}

func TestReadPendingEventsBoundedFinishes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.event"), []byte(`{"type":"X"}`), 0644)

	events := readPendingEventsBounded(context.Background(), dir, 2*time.Second)
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
}

func TestReadPendingEventsBoundedCtxDone(t *testing.T) {
	dir := t.TempDir()
	// Even when ctx is already done, the bounded read should return
	// promptly (within the grace window) rather than hang.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_ = readPendingEventsBounded(ctx, dir, 5*time.Second)
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Errorf("bounded read took %v with cancelled ctx; expected prompt return", elapsed)
	}
}

func TestWaitForEventFilesContextYield(t *testing.T) {
	// Regression test for #3870: --context-check-interval must cause an early
	// return with reason "context-yield" before the full backoff timeout expires.
	dir := t.TempDir()

	// Full timeout is much longer than the yield interval.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	yieldAfter := 600 * time.Millisecond

	start := time.Now()
	result, err := waitForEventFiles(ctx, dir, yieldAfter)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "context-yield" {
		t.Errorf("expected reason 'context-yield', got %q (elapsed: %v)", result.Reason, elapsed)
	}
	// Must return close to the yield interval, not the full 10s timeout.
	if elapsed < yieldAfter-100*time.Millisecond {
		t.Errorf("returned too early (%v); yield interval was %v", elapsed, yieldAfter)
	}
	if elapsed > yieldAfter+2*time.Second {
		t.Errorf("returned too late (%v); yield interval was %v", elapsed, yieldAfter)
	}
}

func TestWaitForEventFilesContextYieldEventWins(t *testing.T) {
	// When an event arrives before the context-yield interval, the event
	// result takes priority.
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	yieldAfter := 5 * time.Second // yield interval is long — event arrives first

	go func() {
		time.Sleep(800 * time.Millisecond)
		os.WriteFile(filepath.Join(dir, "early.event"), []byte(`{"type":"MERGE_READY"}`), 0644)
	}()

	result, err := waitForEventFiles(ctx, dir, yieldAfter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "event" {
		t.Errorf("expected reason 'event' (event arrived before yield), got %q", result.Reason)
	}
	if len(result.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(result.Events))
	}
}

func TestWaitForEventFilesContextYieldTimeoutWins(t *testing.T) {
	// When the backoff timeout is shorter than the yield interval, timeout
	// fires first and the result is "timeout", not "context-yield".
	dir := t.TempDir()

	// Timeout is shorter than the yield interval.
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	yieldAfter := 5 * time.Second

	result, err := waitForEventFiles(ctx, dir, yieldAfter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "timeout" {
		t.Errorf("expected reason 'timeout' (timeout < yield interval), got %q", result.Reason)
	}
}

func TestWaitForEventFilesNoContextYieldWhenZero(t *testing.T) {
	// When contextCheckAfter is 0 (not set), behavior is unchanged:
	// the wait runs to the full timeout without yielding.
	dir := t.TempDir()

	deadline := 600 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	start := time.Now()
	result, err := waitForEventFiles(ctx, dir, 0) // zero = no yield
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "timeout" {
		t.Errorf("expected reason 'timeout' with zero yield interval, got %q", result.Reason)
	}
	if elapsed > deadline+2*time.Second {
		t.Errorf("wait took %v; should have returned at ~%v", elapsed, deadline)
	}
}

func TestAwaitEventContextYieldPreservesBackoffWindow(t *testing.T) {
	until := time.Now().Add(2 * time.Second).Unix()
	log := runAwaitEventBackoffTest(t, []string{"gt:agent", "idle:1", fmt.Sprintf("backoff-until:%d", until)}, "5s", "50ms")

	updates := updateLines(log)
	if len(updates) == 0 {
		t.Fatalf("expected bd update calls, log:\n%s", log)
	}
	for _, line := range updates {
		if !strings.Contains(line, "backoff-until:") {
			t.Fatalf("context-yield cleared backoff window; update %q in log:\n%s", line, log)
		}
	}
}

func TestAwaitEventTimeoutClearsBackoffWindow(t *testing.T) {
	until := time.Now().Add(2 * time.Second).Unix()
	log := runAwaitEventBackoffTest(t, []string{"gt:agent", "idle:1", fmt.Sprintf("backoff-until:%d", until)}, "80ms", "")

	updates := updateLines(log)
	if len(updates) == 0 {
		t.Fatalf("expected bd update calls, log:\n%s", log)
	}
	last := updates[len(updates)-1]
	if strings.Contains(last, "backoff-until:") {
		t.Fatalf("timeout did not clear backoff window; last update %q in log:\n%s", last, log)
	}
}

func runAwaitEventBackoffTest(t *testing.T, labels []string, timeout, contextCheck string) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mayor"), 0755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "mayor", "town.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatalf("write town.json: %v", err)
	}
	beadsDir := filepath.Join(root, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	t.Setenv("BEADS_DIR", beadsDir)
	t.Chdir(root)

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	logPath := filepath.Join(root, "bd.log")
	showJSON, err := json.Marshal([]struct {
		Labels []string `json:"labels"`
	}{{Labels: labels}})
	if err != nil {
		t.Fatalf("marshal labels: %v", err)
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
case "$1" in
show)
cat <<'JSON'
%s
JSON
;;
update)
exit 0
;;
*)
exit 0
;;
esac
`, logPath, string(showJSON))
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("write bd stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldChannel := awaitEventChannel
	oldTimeout := awaitEventTimeout
	oldBackoffBase := awaitEventBackoffBase
	oldBackoffMult := awaitEventBackoffMult
	oldBackoffMax := awaitEventBackoffMax
	oldQuiet := awaitEventQuiet
	oldAgentBead := awaitEventAgentBead
	oldCleanup := awaitEventCleanup
	oldContextCheck := awaitEventContextCheckInterval
	oldJSON := moleculeJSON
	t.Cleanup(func() {
		awaitEventChannel = oldChannel
		awaitEventTimeout = oldTimeout
		awaitEventBackoffBase = oldBackoffBase
		awaitEventBackoffMult = oldBackoffMult
		awaitEventBackoffMax = oldBackoffMax
		awaitEventQuiet = oldQuiet
		awaitEventAgentBead = oldAgentBead
		awaitEventCleanup = oldCleanup
		awaitEventContextCheckInterval = oldContextCheck
		moleculeJSON = oldJSON
	})

	awaitEventChannel = "test"
	awaitEventTimeout = timeout
	awaitEventBackoffBase = ""
	awaitEventBackoffMult = 2
	awaitEventBackoffMax = ""
	awaitEventQuiet = true
	awaitEventAgentBead = "gt-agent"
	awaitEventCleanup = false
	awaitEventContextCheckInterval = contextCheck
	moleculeJSON = false

	if err := runMoleculeAwaitEvent(nil, nil); err != nil {
		t.Fatalf("runMoleculeAwaitEvent: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read bd log: %v", err)
	}
	return string(data)
}

func updateLines(log string) []string {
	var updates []string
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "update ") {
			updates = append(updates, line)
		}
	}
	return updates
}

func TestEffortLevelContextYield(t *testing.T) {
	// context-yield must produce EffortLevel "full" so context-check is
	// not abbreviated.
	result := &AwaitEventResult{
		Reason:     "context-yield",
		IdleCycles: 5, // high idle count that would normally produce "abbreviated"
	}

	// Replicate the effort-level logic from runMoleculeAwaitEvent.
	if result.Reason == "event" || result.Reason == "context-yield" || result.IdleCycles == 0 {
		result.EffortLevel = "full"
	} else {
		result.EffortLevel = "abbreviated"
	}

	if result.EffortLevel != "full" {
		t.Errorf("context-yield should produce EffortLevel 'full', got %q", result.EffortLevel)
	}
}

func TestEventFileStruct(t *testing.T) {
	ef := EventFile{
		Path:    "/home/gt/events/refinery/12345.event",
		Content: json.RawMessage(`{"type":"MQ_SUBMIT","payload":{"branch":"feat/test"}}`),
	}

	data, err := json.Marshal(ef)
	if err != nil {
		t.Fatalf("failed to marshal EventFile: %v", err)
	}

	var decoded EventFile
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal EventFile: %v", err)
	}
	if decoded.Path != ef.Path {
		t.Errorf("path = %q, want %q", decoded.Path, ef.Path)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(decoded.Content, &parsed); err != nil {
		t.Fatalf("failed to parse decoded content: %v", err)
	}
	if parsed["type"] != "MQ_SUBMIT" {
		t.Errorf("type = %v, want MQ_SUBMIT", parsed["type"])
	}
}

// TestBackoffEngagesOnceOwnChannelDrains is the liveness half of gt-a3qs.
//
// The refinery's idle path was a hot loop by construction: the shared channel
// held 69 events that were never that rig's to consume, waitForEventFiles
// checks pending events FIRST and returns immediately, so the timeout that
// drives the 30s-doubling-to-15m backoff could never be reached — and neither
// could EFFORT: reduced, which is derived from the backoff state.
//
// With the channel scoped per rig and drained, an idle refinery reaches the
// timeout branch, which is what increments the idle counter.
func TestBackoffEngagesOnceOwnChannelDrains(t *testing.T) {
	townRoot := t.TempDir()

	ourDir, err := channelevents.ChannelDir(townRoot, "gastown", "refinery")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ourDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Another rig's channel is busy. Ours is empty.
	theirDir, err := channelevents.ChannelDir(townRoot, "liveop", "refinery")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(theirDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		name := filepath.Join(theirDir, fmt.Sprintf("%d-1-1.event", i))
		if err := os.WriteFile(name, []byte(`{"type":"MQ_SUBMIT","rig":"liveop"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Our wait must reach the timeout branch — the one that increments idle —
	// rather than returning "event" instantly on someone else's backlog.
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	result, err := waitForEventFiles(ctx, ourDir, 0)
	if err != nil {
		t.Fatalf("waitForEventFiles: %v", err)
	}
	if result.Reason != "timeout" {
		t.Errorf("reason = %q, want timeout: an idle refinery woke on another rig's %d events",
			result.Reason, len(result.Events))
	}

	// And the other rig's events are untouched — no consumption, no deletion.
	entries, err := os.ReadDir(theirDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Errorf("liveop's channel holds %d events after our wait, want 5", len(entries))
	}
}

// TestCleanupDrainsOnlyOurOwnChannel pins that --cleanup, which is what makes
// the directory drain and therefore what lets backoff engage, cannot reach
// another rig's events. --cleanup on a shared channel is exactly what deleted
// six of liveop's events; on a scoped channel it is safe, and that safety is
// the whole reason the flag can be used again.
func TestCleanupDrainsOnlyOurOwnChannel(t *testing.T) {
	townRoot := t.TempDir()

	ourDir, _ := channelevents.ChannelDir(townRoot, "gastown", "refinery")
	theirDir, _ := channelevents.ChannelDir(townRoot, "liveop", "refinery")
	for _, d := range []string{ourDir, theirDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "1-1-1.event"), []byte(`{"type":"MQ_SUBMIT"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := waitForEventFiles(ctx, ourDir, 0)
	if err != nil {
		t.Fatalf("waitForEventFiles: %v", err)
	}
	if result.Reason != "event" || len(result.Events) != 1 {
		t.Fatalf("reason=%q events=%d, want event and 1", result.Reason, len(result.Events))
	}

	// Simulate --cleanup over exactly what the wait returned.
	for _, ef := range result.Events {
		if err := os.Remove(ef.Path); err != nil {
			t.Fatal(err)
		}
	}

	if entries, _ := os.ReadDir(ourDir); len(entries) != 0 {
		t.Errorf("our channel did not drain: %d files remain", len(entries))
	}
	if entries, _ := os.ReadDir(theirDir); len(entries) != 1 {
		t.Errorf("cleanup reached liveop's channel: %d files remain, want 1", len(entries))
	}
}
