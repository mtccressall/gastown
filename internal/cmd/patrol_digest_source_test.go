package cmd

import "testing"

// gt-3uty: `gt patrol digest` queried --label=digest, a label only `gt mol squash`
// creates. Patrol stopped calling squash when `gt patrol report` replaced the
// squash-and-new pattern, so the aggregation had no input and returned nothing,
// silently, for as long as that has been true.
//
// Both title shapes must be accepted: a window spanning the change contains
// legacy squash digests AND patrol root wisps, and taking only one loses half.
func TestIsPatrolCycleTitle(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  bool
	}{
		{"mol-deacon-patrol", true}, // the wisp that carries summaries today
		{"mol-witness-patrol", true},
		{"mol-refinery-patrol", true},
		{"Digest: mol-deacon-patrol", true}, // the legacy squash digest
		{"Digest: mol-witness-patrol", true},

		{"mol-deacon-patrol-extra", false}, // suffix must terminate the title
		{"Digest: gt-wisp-abc123", false},  // a squash digest for something else
		{"mol-session-gc", false},
		{"", false},
		{"Digest: ", false},
		{"patrol", false},
	} {
		t.Run(tc.title, func(t *testing.T) {
			if got := isPatrolCycleTitle(tc.title); got != tc.want {
				t.Fatalf("isPatrolCycleTitle(%q) = %v, want %v", tc.title, got, tc.want)
			}
		})
	}
}

// The role extractor already handled the bare wisp title, which is why the fix
// is a query change rather than a rewrite. Pinned so a later edit to one does
// not silently desynchronise it from the other.
func TestExtractPatrolRoleHandlesBothTitleShapes(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"mol-deacon-patrol", "deacon"},
		{"Digest: mol-deacon-patrol", "deacon"},
		{"mol-witness-patrol", "witness"},
		{"Digest: gt-wisp-abc123", "patrol"},
	} {
		if got := extractPatrolRole(tc.title); got != tc.want {
			t.Errorf("extractPatrolRole(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

// gt-fwzgp: this aggregation DELETES its sources, and the sources are wisps,
// which sit in dolt_ignore and are therefore never committed — there is no AS OF
// to recover from and dolt_diff holds no row for a deleted id. Verified after
// the fact on the live store, the hard way.
//
// So the delete must be gated on the aggregate demonstrably containing each
// cycle, and the gate must fail toward KEEPING the sources.
func TestDigestMissingCyclesFailsTowardKeeping(t *testing.T) {
	cycles := []PatrolCycleEntry{
		{ID: "gt-wisp-aaa", Role: "deacon", Description: "cycle one"},
		{ID: "gt-wisp-bbb", Role: "witness", Description: "cycle two"},
	}

	// THIS CALLS THE PRODUCTION PREDICATE. The first version of this test
	// reimplemented the loop inline, so defeating the real verification left it
	// green — on the path that deletes beads permanently. Caught by
	// gastown/refinery, by sabotage rather than by reading.
	for _, tc := range []struct {
		name        string
		body        string
		wantMissing int
	}{
		{"carries both", "…gt-wisp-aaa… and …gt-wisp-bbb…", 0},
		{"carries one", "…gt-wisp-aaa… only", 1},
		{"counts only, the destructive case", "Total Cycles: 2\nBy Role\n- deacon: 1", 2},
		{"empty", "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := missingCycleIDs(tc.body, cycles)
			if len(got) != tc.wantMissing {
				t.Fatalf("missingCycleIDs = %v (%d), want %d missing", got, len(got), tc.wantMissing)
			}
		})
	}

	// And the property the delete depends on, stated directly: a body that
	// mentions nothing must report EVERY cycle missing, never an empty slice.
	if got := missingCycleIDs("", cycles); len(got) != len(cycles) {
		t.Fatalf("an empty body reported %d missing; a verification that reports nothing missing deletes everything", len(got))
	}
}

// codex P1: a patrol-shaped title is not evidence of a REPORTED cycle. Roots are
// also closed by cleanup and rollback paths that leave no summary, and this
// command counts, archives and permanently deletes what it selects.
func TestHasPatrolReportBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"a reported cycle", "Patrol report: ABBREVIATED 23:54Z. Inbox clear.", true},
		{"leading whitespace is fine", "\n  Patrol report: FULL 02:31-02:37Z", true},
		{"burned root", "burned: replaced by new patrol cycle", false},
		{"empty", "", false},
		{"the formula preamble, not a report", "Per-rig worker monitor patrol loop.\n\nThe Witness is…", false},
		{"mentions the phrase later, but did not report", "cleanup: no Patrol report: here", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasPatrolReportBody(tc.body); got != tc.want {
				t.Fatalf("hasPatrolReportBody(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// The delete path takes verified ids and must be inert on an empty set, since
// "nothing to delete" and "delete everything matching a re-query" are the two
// outcomes that differ here (gt-fwzgp).
func TestCycleIDsAndEmptyDeleteIsInert(t *testing.T) {
	ids := cycleIDs([]PatrolCycleEntry{{ID: "gt-wisp-a"}, {ID: "gt-wisp-b"}})
	if len(ids) != 2 || ids[0] != "gt-wisp-a" || ids[1] != "gt-wisp-b" {
		t.Fatalf("cycleIDs = %v", ids)
	}
	if n, err := deletePatrolDigestsByID(nil); n != 0 || err != nil {
		t.Fatalf("deletePatrolDigestsByID(nil) = %d, %v; want 0, nil", n, err)
	}
}
