package cmd

import (
	"testing"
	"time"
)

// gt-fy6, in the polecat inventory: WORKING was decided from session EXISTENCE
// alone. A polecat frozen on a permission prompt has a live session and a hooked
// bead and makes no progress, so it reported WORKING while holding a capacity
// slot indefinitely.
//
// Observed 2026-09-03 on liveop/brahmin, stuck awaiting approval for a read-only
// grep. Verified against the live pool: the patched binary reports it
// stalled/NEEDS_RECOVERY where the shipped one said working/WORKING, and NO other
// polecat changed state.
//
// isQuiet is the discriminator. These pin its truth table, and in particular that
// MISSING DATA IS NOT QUIET — reporting a polecat blocked because activity could
// not be measured would repeat gt-365 and gt-28k, where an unverifiable check was
// recorded as a definite negative and something was destroyed for it.
func TestIsQuiet(t *testing.T) {
	const rig, name = "liveop", "brahmin"
	key := polecatSessionKey(rig, name)
	sessions := polecatSessionSet{key: "liveop-brahmin"}

	restore := polecatSessionActivity
	defer func() { polecatSessionActivity = restore }()

	cases := []struct {
		name     string
		activity map[string]time.Time
		sessions polecatSessionSet
		want     bool
	}{
		{
			name:     "silent well past the window — the gt-fy6 case",
			activity: map[string]time.Time{"liveop-brahmin": time.Now().Add(-45 * time.Minute)},
			sessions: sessions,
			want:     true,
		},
		{
			name:     "producing output just now",
			activity: map[string]time.Time{"liveop-brahmin": time.Now()},
			sessions: sessions,
			want:     false,
		},
		{
			name: "quiet but INSIDE the window — a long test run is not blocked",
			// The window is generous on purpose: calling a slow build "blocked"
			// is the same error in the other direction.
			activity: map[string]time.Time{"liveop-brahmin": time.Now().Add(-5 * time.Minute)},
			sessions: sessions,
			want:     false,
		},
		{
			name:     "activity map unavailable — NOT quiet, we simply do not know",
			activity: nil,
			sessions: sessions,
			want:     false,
		},
		{
			name:     "session absent from the activity map — NOT quiet",
			activity: map[string]time.Time{"liveop-other": time.Now().Add(-2 * time.Hour)},
			sessions: sessions,
			want:     false,
		},
		{
			name:     "zero timestamp is not evidence of silence",
			activity: map[string]time.Time{"liveop-brahmin": {}},
			sessions: sessions,
			want:     false,
		},
		{
			name:     "no session at all — the caller already handles that as stalled",
			activity: map[string]time.Time{"liveop-brahmin": time.Now().Add(-2 * time.Hour)},
			sessions: polecatSessionSet{},
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			polecatSessionActivity = tc.activity
			if got := tc.sessions.isQuiet(rig, name); got != tc.want {
				t.Fatalf("isQuiet() = %v, want %v", got, tc.want)
			}
		})
	}
}
