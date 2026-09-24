package doctor

import "testing"

// The live shape of this town: three real Dolt repositories, of which bd reads
// one (.beads/embeddeddolt, 44,988 issues) and the daemon serves another
// (.dolt-data). The third, .beads/dolt, is accounted for by nobody — and a bd
// start-class command run from the town root can serve it, after which every
// server-mode read returns a silent zero (gt-zpnz).
func TestUnaccountedStoreIsReported(t *testing.T) {
	all := func(string) bool { return true }
	un, scanned := unaccountedStores(
		"/t/.beads/embeddeddolt", "/t/.dolt-data",
		[]string{"/t/.beads/dolt", "/t/.beads/embeddeddolt", "/t/.dolt-data"}, all)

	if scanned != 3 {
		t.Fatalf("scanned = %d, want 3 — the denominator must be reported", scanned)
	}
	if len(un) != 1 || un[0] != "/t/.beads/dolt" {
		t.Fatalf("unaccounted = %v, want exactly /t/.beads/dolt", un)
	}
}

// NEGATIVE CONTROL. When every repository is accounted for, nothing is
// reported — otherwise the check would fire on every town and mean nothing.
func TestNothingReportedWhenEveryStoreIsAccountedFor(t *testing.T) {
	all := func(string) bool { return true }
	un, scanned := unaccountedStores(
		"/t/.beads/embeddeddolt", "/t/.dolt-data",
		[]string{"/t/.beads/embeddeddolt", "/t/.dolt-data"}, all)

	if scanned != 2 || len(un) != 0 {
		t.Fatalf("unaccounted = %v (scanned %d), want none", un, scanned)
	}
}

// A path that is not a Dolt repository is not a store, and must not inflate the
// denominator either — a scan that counts non-repositories reports a population
// it did not actually examine.
func TestNonRepositoriesAreNeitherScannedNorReported(t *testing.T) {
	none := func(string) bool { return false }
	un, scanned := unaccountedStores(
		"/t/.beads/embeddeddolt", "/t/.dolt-data",
		[]string{"/t/.beads/dolt", "/t/nope"}, none)

	if scanned != 0 || len(un) != 0 {
		t.Fatalf("unaccounted = %v (scanned %d), want none scanned", un, scanned)
	}
}

// bd honours BEADS_DIR and BEADS_DB from the inherited environment EVEN WITH
// cmd.Dir at the town root. With BEADS_DB=<townRoot>/.beads/dolt exported, the
// unpinned check blessed the DECOY as the real store and reported the actual
// 44,988-issue store as unaccounted — exactly inverted, on the one question it
// exists to answer (gastown/refinery, PR 65 round 2).
//
// This also covers the acquisition half, which the previous tests never reached:
// they drove the pure enumeration and passed with bdReportedStore returning a
// bogus path.
func TestInheritedBeadsSelectorsCannotSurviveIntoTheBdSubprocess(t *testing.T) {
	hostile := []string{
		"PATH=/usr/bin",
		"BEADS_DB=/town/.beads/dolt",       // the decoy
		"BEADS_DIR=/somewhere/else/.beads", // a different store entirely
		"BD_DB=/third/place",
	}

	got := pinnedBDEnv(hostile, "/town")

	for _, e := range got {
		for _, bad := range []string{"BEADS_DB=/town/.beads/dolt", "BEADS_DIR=/somewhere/else/.beads", "BD_DB=/third/place"} {
			if e == bad {
				t.Errorf("inherited selector survived: %q — bd would report the wrong store", e)
			}
		}
	}

	var pinned bool
	for _, e := range got {
		if e == "BEADS_DIR=/town/.beads" {
			pinned = true
		}
	}
	if !pinned {
		t.Errorf("BEADS_DIR was not pinned to the town beads dir; env=%v", got)
	}
}
