package daemon

import "testing"

// gt-365: the daemon killed a healthy liveop refinery mid-work because a
// transient Dolt hiccup made the rig state unverifiable, and the kill path read
// the same boolean as the "rig is docked" path.
//
// isRigOperational returns false for BOTH "known docked/parked" and "could not
// check". That conflation is safe for its original caller — declining to START
// an agent you cannot verify only delays work — and unsafe for any caller that
// DESTROYS a running session on the same answer.
//
// These pin the discriminator. The distinction is the whole fix: not knowing is
// not the same as knowing it is off.
func TestRigStateUnverifiable(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   bool
	}{
		{"the gt-365 case: Dolt hiccup", "cannot verify rig status (Dolt unavailable)", true},

		// KNOWN states. These are real determinations and a kill is legitimate:
		// the operator docked or parked the rig on purpose.
		{"deliberately docked", "rig is docked", false},
		{"deliberately parked", "rig is parked", false},
		{"auto-restart switched off", "auto_restart is blocked", false},

		// Operational: never reaches a kill path at all.
		{"empty reason", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rigStateUnverifiable(tc.reason); got != tc.want {
				t.Fatalf("rigStateUnverifiable(%q) = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}

// The guard must be reachable from BOTH kill paths. A fix applied to the refinery
// alone would leave witnesses being destroyed by the identical mechanism — which
// is the shape of gt-3up, gt-b3a2 and gt-rsj9, all of which were one caller
// repaired and another left behind.
func TestBothKillPathsGuarded(t *testing.T) {
	src := readDaemonSource(t)
	guards := countOccurrences(src, "rigStateUnverifiable(reason)")
	if guards < 2 {
		t.Fatalf("expected the unverifiable guard on BOTH the refinery and witness kill paths, found %d", guards)
	}
}
