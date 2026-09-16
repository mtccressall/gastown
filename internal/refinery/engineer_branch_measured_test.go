package refinery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// gastown-697: `gt refinery ready --json` runs ListReadyMRs, which never calls
// git. When BranchExistsLocal/Remote were bare bools they kept Go's zero value
// and JSON serialised an unmeasured value as a MEASURED `false` — on every MR
// in the rig. mol-witness-patrol's check-refinery step reads false/false as
// "orphaned, likely close it", so the whole queue read as closeable. Verified
// 5 of 5 inverted against `--all` and against git.
//
// The invariant these tests pin: NOT MEASURED must be distinguishable from
// MEASURED FALSE, at the wire, and no consumer may reach "orphan" from the
// former.

// issueToMRInfo is the shared constructor for every list path, including the
// unmeasured ones (ListReadyMRs, ListBlockedMRs). It must leave the branch
// fields nil so only the paths that actually run git can claim a measurement.
func TestIssueToMRInfo_LeavesBranchExistenceUnmeasured(t *testing.T) {
	issue := &beads.Issue{
		ID:     "gt-ready",
		Status: "open",
		Description: `branch: polecat/ready
target: main
worker: nux`,
	}
	fields := beads.ParseMRFields(issue)
	if fields == nil {
		t.Fatal("ParseMRFields returned nil — test fixture is wrong")
	}

	mr := issueToMRInfo(issue, fields)

	if mr.BranchExistsLocal != nil {
		t.Errorf("BranchExistsLocal = %v, want nil (unmeasured): a path that never runs git must not report a measurement", *mr.BranchExistsLocal)
	}
	if mr.BranchExistsRemote != nil {
		t.Errorf("BranchExistsRemote = %v, want nil (unmeasured)", *mr.BranchExistsRemote)
	}
	if mr.BranchMeasured() {
		t.Error("BranchMeasured() = true on an MR built without any git call")
	}
	if mr.BranchOrphaned() {
		t.Error("BranchOrphaned() = true on an UNMEASURED MR — this is gastown-697: the witness formula reads this as licence to close the MR")
	}
}

// The wire format is the actual defect surface: the witness consumes JSON, not
// Go structs. An unmeasured field must be ABSENT, not `false`.
func TestMRInfoJSON_OmitsUnmeasuredBranchExistence(t *testing.T) {
	// The envelope `gt refinery ready --json` emits.
	type readyOutput struct {
		Ready []*MRInfo `json:"ready"`
	}

	out, err := json.Marshal(readyOutput{Ready: []*MRInfo{{ID: "gt-ready", Branch: "polecat/ready"}}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)

	for _, key := range []string{"BranchExistsLocal", "BranchExistsRemote"} {
		if strings.Contains(got, key) {
			t.Errorf("unmeasured %s is present in JSON; a consumer reading it as false concludes the branch is orphaned (gastown-697).\nJSON: %s", key, got)
		}
	}
}

// The negative control: omitting on nil is only useful if a MEASURED false
// still reaches the wire. If omitempty swallowed a real `false` too, the
// genuine orphan signal would disappear — the opposite failure, and the one
// that hides a real orphan instead of inventing one.
func TestMRInfoJSON_EmitsMeasuredFalse(t *testing.T) {
	mr := &MRInfo{
		ID:                 "gt-orphan",
		Branch:             "polecat/deleted",
		BranchExistsLocal:  boolPtr(false),
		BranchExistsRemote: boolPtr(false),
	}

	out, err := json.Marshal(mr)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)

	for _, want := range []string{`"BranchExistsLocal":false`, `"BranchExistsRemote":false`} {
		if !strings.Contains(got, want) {
			t.Errorf("measured %s missing from JSON — a real orphaned branch would go unreported.\nJSON: %s", want, got)
		}
	}
	if !mr.BranchMeasured() {
		t.Error("BranchMeasured() = false on an MR whose fields were both populated")
	}
	if !mr.BranchOrphaned() {
		t.Error("BranchOrphaned() = false on a MEASURED false/false MR — the genuine orphan signal is broken")
	}
}

// A branch that exists is not an orphan, and a half-measured MR is not one
// either: BranchOrphaned must require both fields present and both false.
func TestBranchOrphaned_RequiresBothMeasuredAndAbsent(t *testing.T) {
	tests := []struct {
		name          string
		local, remote *bool
		wantMeasured  bool
		wantOrphaned  bool
	}{
		{"unmeasured", nil, nil, false, false},
		{"local measured only", boolPtr(false), nil, false, false},
		{"remote measured only", nil, boolPtr(false), false, false},
		{"both exist", boolPtr(true), boolPtr(true), true, false},
		{"local only exists", boolPtr(true), boolPtr(false), true, false},
		{"remote only exists", boolPtr(false), boolPtr(true), true, false},
		{"measured orphan", boolPtr(false), boolPtr(false), true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mr := &MRInfo{BranchExistsLocal: tt.local, BranchExistsRemote: tt.remote}
			if got := mr.BranchMeasured(); got != tt.wantMeasured {
				t.Errorf("BranchMeasured() = %v, want %v", got, tt.wantMeasured)
			}
			if got := mr.BranchOrphaned(); got != tt.wantOrphaned {
				t.Errorf("BranchOrphaned() = %v, want %v", got, tt.wantOrphaned)
			}
		})
	}
}
