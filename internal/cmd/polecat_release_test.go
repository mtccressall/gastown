package cmd

import (
	"os"
	"strings"
	"testing"
)

// The gates of `gt polecat release`, in the order they must fire.
//
// This command exists because three dead polecats held liveop's whole pool on
// 2026-09-03 and the only tools available were an irreversible nuke or a
// hand-written write to the data plane.
//
// The behaviour worth pinning is not that it releases — it is WHAT IT REFUSES.
func TestPolecatReleaseGateOrdering(t *testing.T) {
	// Documented contract, asserted so the help text and the code cannot drift.
	long := polecatReleaseCmd.Long

	for _, must := range []string{
		"the tmux session is gone",
		"the worktree holds nothing",
		"the hook is genuinely stale",
		"matched by NAME, not",
		"Nothing relaxes gate 2",
	} {
		if !strings.Contains(long, must) {
			t.Errorf("release help no longer documents %q — the gate it describes is the safety story", must)
		}
	}
}

// Gate 2 must never be reachable by --force. A flag that can free a worktree
// holding unsaved work is the raider incident with a command-line interface.
func TestPolecatReleaseForceCannotBypassWorktreeGate(t *testing.T) {
	src := releaseSourceForTest(t)

	// The worktree check must not be guarded by the force flag on the same line
	// or in the same condition.
	idx := strings.Index(src, "activeMRGitSafeForWorktree")
	if idx == -1 {
		t.Fatal("release no longer calls activeMRGitSafeForWorktree; gate 2 is gone")
	}
	// Take the enclosing statement and assert force is absent from it.
	start := strings.LastIndex(src[:idx], "\n\tif ")
	if start == -1 {
		t.Fatal("could not locate the worktree guard")
	}
	stmt := src[start : idx+200]
	if strings.Contains(stmt, "polecatReleaseForce") {
		t.Error("the worktree gate consults polecatReleaseForce — --force must NOT be able to bypass it")
	}
}

// The staleness gate must ask about OWNERSHIP, not completion.
//
// The first version reused hookBeadSafeForCleanup, which answers "is this work
// finished?" — a neighbouring question. It refused to release vault over a bead
// that was status=open with NO assignee, i.e. a hook that was plainly stale.
func TestPolecatReleaseStalenessIsAboutOwnership(t *testing.T) {
	src := releaseSourceForTest(t)

	// A CALL, not a mention — the comment above the gate deliberately names the
	// predicate it used to use, and that explanation should survive.
	if strings.Contains(src, "hookBeadSafeForCleanup(") {
		t.Error("release is back on hookBeadSafeForCleanup(), which answers completion, not ownership")
	}
	if !strings.Contains(src, "issue.Assignee") {
		t.Error("release no longer compares the hooked bead's assignee; staleness is an ownership question")
	}
	if !strings.Contains(src, `rigName + "/polecats/" + polecatName`) {
		t.Error("release no longer builds this polecat's assignee id to compare against")
	}
}

// A write that reports success without persisting is the defect this command
// cleans up after. It must not be the defect the command ships.
func TestPolecatReleaseReadsBackTheClear(t *testing.T) {
	src := releaseSourceForTest(t)
	after := src[strings.Index(src, "UpdateAgentDescriptionFields"):]
	if !strings.Contains(after, "GetAgentBead") {
		t.Error("release does not re-read the agent bead after writing; a silent no-op write would report success")
	}
	if !strings.Contains(after, "NOT released") {
		t.Error("release does not fail loudly when the clear did not persist")
	}
}

// An indeterminate check must not be treated as permission (gt-365, gt-28k).
func TestPolecatReleaseRefusesOnUnverifiableChecks(t *testing.T) {
	src := releaseSourceForTest(t)
	for _, must := range []string{
		"cannot list tmux sessions",
		"refusing to release without knowing",
		"an unverifiable check must not be treated as permission",
	} {
		if !strings.Contains(src, must) {
			t.Errorf("release lost its refusal path for %q", must)
		}
	}
}

func releaseSourceForTest(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("polecat_release.go")
	if err != nil {
		t.Fatalf("reading polecat_release.go: %v", err)
	}
	return string(b)
}
