package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestShouldSkipDrainUntilIdle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		hasPromptDetection bool
		waitErr            error
		want               bool
	}{
		{"prompt aware idle", true, nil, false},
		{"prompt aware busy", true, errors.New("timeout"), true},
		{"no prompt detection busy", false, errors.New("timeout"), false},
		{"no prompt detection idle", false, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSkipDrainUntilIdle(tt.hasPromptDetection, tt.waitErr); got != tt.want {
				t.Errorf("shouldSkipDrainUntilIdle(%v, %v) = %v, want %v", tt.hasPromptDetection, tt.waitErr, got, tt.want)
			}
		})
	}
}

func TestTrackPollerPidFileRoundTrip(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "gastown-refinery"
	path := filepath.Join(townRoot, ".runtime", "nudge_poller", session+".pid")

	untrack := trackPollerPidFile(townRoot, session, 1221266)

	// A directly invoked `gt nudge-poller` used to leave nothing here, so the
	// "already running" guard could not fire and any pid-file census reported a
	// live poller as absent (gt-di75).
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if got := string(data); got != "1221266" {
		t.Errorf("pid file contents = %q, want %q", got, "1221266")
	}

	untrack()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("pid file still present after cleanup, stat err = %v", err)
	}
}

func TestTrackPollerPidFileCleanupSparesASuccessor(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "hq-deacon"
	path := filepath.Join(townRoot, ".runtime", "nudge_poller", session+".pid")

	untrack := trackPollerPidFile(townRoot, session, 4242)

	// StopPoller removes the file straight after SIGTERM, so a replacement can
	// claim the slot before the dying poller's deferred cleanup runs.
	if err := os.WriteFile(path, []byte("5555"), 0644); err != nil {
		t.Fatalf("writing successor pid file: %v", err)
	}

	untrack()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the successor's pid file was deleted: %v", err)
	}
	if got := string(data); got != "5555" {
		t.Errorf("pid file contents = %q, want %q", got, "5555")
	}
}
