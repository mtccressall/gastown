package cmd

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// The tests in this file pin the ORDER in which gt patrol report ends one
// patrol cycle and begins the next (gastown-9pc, town gt-9dwa).
//
// A happy-path test cannot see this. Both orderings mint a successor and close
// the outgoing root, so both go green; the difference only shows when the
// command does not reach its own last line. That is why the acceptance
// criteria asked for the order rather than the outcome.
//
// Two layers, because they fail in different situations:
//
//   - The behavioural tests below need bd and a Dolt container and SKIP without
//     them, and a skipped test reads exactly like a passing one.
//   - The structural test needs neither and always runs.

// TestRotatePatrolCycle_MintFailureLeavesOutgoingPatrolHooked is the regression
// test for gastown-9pc.
//
// Process death cannot be provoked from a test, so this exercises the same
// window through the one failure that is reachable: the successor does not get
// minted. The invariant is the same either way — THE OUTGOING ROOT MUST NOT BE
// CLOSED UNLESS A SUCCESSOR EXISTS — and it is the invariant, not the cause,
// that decides whether the role is left hookless.
//
// Under the original close-then-mint order the root is already closed by the
// time the mint is attempted, so the role ends the call with ZERO hooked patrol
// wisps. gt hook and the town-scoped bd query then both report empty, truthfully,
// and a respawning agent stands down believing them.
func TestRotatePatrolCycle_MintFailureLeavesOutgoingPatrolHooked(t *testing.T) {
	requireBd(t)
	tmpDir, b := setupPatrolTestDB(t)

	molName := "mol-test-patrol"
	assignee := "testrig/witness"
	patrolID := createHookedPatrol(t, b, molName, assignee, true /* withOpenChild */)

	cfg := PatrolConfig{
		PatrolMolName: molName,
		BeadsDir:      tmpDir,
		Assignee:      assignee,
		Beads:         b,
	}

	stubPatrolSpawn(t, func(PatrolConfig, string) (string, error) {
		return "", errors.New("failed to list formulas: catalog unreachable")
	})

	rotated, err := rotatePatrolCycle(b, cfg, patrolID, "all clear")
	if err == nil {
		t.Fatal("rotatePatrolCycle returned nil error when the mint failed")
	}
	if rotated {
		t.Error("rotatePatrolCycle reported a completed rotation despite a failed mint")
	}

	issue, showErr := b.Show(patrolID)
	if showErr != nil {
		t.Fatalf("show outgoing patrol: %v", showErr)
	}
	if issue.Status != beads.StatusHooked {
		t.Fatalf("outgoing patrol %s status = %q, want %q: the mint failed, so closing it "+
			"leaves the role with no hooked patrol at all — the terminal state of gastown-9pc, "+
			"which no error branch and no later cycle recovers",
			patrolID, issue.Status, beads.StatusHooked)
	}
}

// TestRotatePatrolCycle_ClosesOutgoingRootOnceSuccessorExists is the other half:
// a rotation that mints successfully must still close the cycle it reported on.
// Without this, the fix for gastown-9pc would trade a hookless role for an
// unbounded pile of hooked roots.
func TestRotatePatrolCycle_ClosesOutgoingRootOnceSuccessorExists(t *testing.T) {
	requireBd(t)
	tmpDir, b := setupPatrolTestDB(t)

	molName := "mol-test-patrol"
	assignee := "testrig/witness"
	patrolID := createHookedPatrol(t, b, molName, assignee, true /* withOpenChild */)

	cfg := PatrolConfig{
		PatrolMolName: molName,
		BeadsDir:      tmpDir,
		Assignee:      assignee,
		Beads:         b,
	}

	var gotKeep string
	var mintedWhileOutgoingHooked bool
	stubPatrolSpawn(t, func(_ PatrolConfig, keep string) (string, error) {
		gotKeep = keep
		// Observe the outgoing root from inside the mint: this is the window a
		// death would land in, and the role must still hold a patrol here.
		if issue, err := b.Show(patrolID); err == nil {
			mintedWhileOutgoingHooked = issue.Status == beads.StatusHooked
		}
		return "testrig-wisp-successor", nil
	})

	rotated, err := rotatePatrolCycle(b, cfg, patrolID, "all clear")
	if err != nil {
		t.Fatalf("rotatePatrolCycle: %v", err)
	}
	if !rotated {
		t.Error("rotatePatrolCycle reported an incomplete rotation on the happy path")
	}
	if !mintedWhileOutgoingHooked {
		t.Error("the outgoing patrol was already closed when the mint ran; a death in that " +
			"window leaves the role hookless (gastown-9pc)")
	}
	if gotKeep != patrolID {
		t.Errorf("mint was passed keepPatrolID %q, want %q: without the exclusion the new "+
			"cycle's burn closes the outgoing root as \"burned: replaced by new patrol cycle\" "+
			"and the cycle summary never reaches close_reason", gotKeep, patrolID)
	}

	issue, showErr := b.Show(patrolID)
	if showErr != nil {
		t.Fatalf("show outgoing patrol: %v", showErr)
	}
	if issue.Status != "closed" {
		t.Errorf("outgoing patrol %s status = %q, want %q", patrolID, issue.Status, "closed")
	}
}

// TestRotatePatrolCycle_PartialMintLeavesOutgoingHooked covers the branch where
// the successor wisp is created but not hooked. autoSpawnPatrol reports that as
// (id, error). The wisp exists and is unreachable, so closing the outgoing root
// on the strength of the returned id lands in the same hookless state the
// reorder exists to prevent.
func TestRotatePatrolCycle_PartialMintLeavesOutgoingHooked(t *testing.T) {
	requireBd(t)
	tmpDir, b := setupPatrolTestDB(t)

	molName := "mol-test-patrol"
	assignee := "testrig/witness"
	patrolID := createHookedPatrol(t, b, molName, assignee, true /* withOpenChild */)

	cfg := PatrolConfig{
		PatrolMolName: molName,
		BeadsDir:      tmpDir,
		Assignee:      assignee,
		Beads:         b,
	}

	stubPatrolSpawn(t, func(PatrolConfig, string) (string, error) {
		return "testrig-wisp-unhooked", errors.New("created wisp testrig-wisp-unhooked but failed to hook")
	})

	rotated, err := rotatePatrolCycle(b, cfg, patrolID, "all clear")
	if err != nil {
		t.Fatalf("rotatePatrolCycle returned an error on the partial-mint branch: %v", err)
	}
	if rotated {
		t.Error("rotatePatrolCycle reported a completed rotation for a successor that was never hooked")
	}

	issue, showErr := b.Show(patrolID)
	if showErr != nil {
		t.Fatalf("show outgoing patrol: %v", showErr)
	}
	if issue.Status != beads.StatusHooked {
		t.Fatalf("outgoing patrol %s status = %q, want %q: the successor was created but not "+
			"hooked, so closing this one leaves the role with nothing on its hook",
			patrolID, issue.Status, beads.StatusHooked)
	}
}

// TestBurnPreviousPatrolWisps_KeepsExcludedRoot pins the mechanism that lets the
// successor be minted while the outgoing root is still hooked.
//
// autoSpawnPatrol burns every patrol wisp for the role before creating the new
// one (gt-92jh). Minting first therefore puts the outgoing root in the burn's
// path, where it would be closed as "burned: replaced by new patrol cycle" —
// overwriting the one field that records what the cycle actually did, and
// close_reason cannot be amended after the fact.
func TestBurnPreviousPatrolWisps_KeepsExcludedRoot(t *testing.T) {
	requireBd(t)
	tmpDir, b := setupPatrolTestDB(t)

	molName := "mol-test-patrol"
	assignee := "testrig/witness"

	orphan := createHookedPatrol(t, b, molName, assignee, false)
	keep := createHookedPatrol(t, b, molName, assignee, true)

	cfg := PatrolConfig{
		PatrolMolName: molName,
		BeadsDir:      tmpDir,
		Assignee:      assignee,
		Beads:         b,
	}

	burnPreviousPatrolWisps(cfg, keep)

	orphanIssue, err := b.Show(orphan)
	if err != nil {
		t.Fatalf("show orphan: %v", err)
	}
	if orphanIssue.Status != "closed" {
		t.Errorf("orphan patrol %s status = %q, want %q: the exclusion must not disable the "+
			"burn for everything else (gt-92jh)", orphan, orphanIssue.Status, "closed")
	}

	keepIssue, err := b.Show(keep)
	if err != nil {
		t.Fatalf("show kept patrol: %v", err)
	}
	if keepIssue.Status != beads.StatusHooked {
		t.Errorf("kept patrol %s status = %q, want %q: the report path closes this one itself, "+
			"with the cycle summary", keep, keepIssue.Status, beads.StatusHooked)
	}
}

func stubPatrolSpawn(t *testing.T, fn func(PatrolConfig, string) (string, error)) {
	t.Helper()
	old := patrolSpawnSuccessor
	patrolSpawnSuccessor = fn
	t.Cleanup(func() { patrolSpawnSuccessor = old })
}

// TestPatrolReportMintsBeforeClosing is the structural half, and it is the half
// that runs when Docker does not.
//
// It also covers a gap the behavioural tests cannot: they exercise
// rotatePatrolCycle, so they stay green if a close is reintroduced into
// runPatrolReport ahead of the call. That is the shape that let gt-7rne survive
// PR #15 — a green test pinning a function the broken path no longer went
// through.
//
// This asserts source order, which is not execution order in general. It is
// sound here because rotatePatrolCycle is straight-line code with the mint at
// the top; the behavioural tests above cover the behaviour.
func TestPatrolReportMintsBeforeClosing(t *testing.T) {
	const file = "patrol_report.go"
	// Calls that end the outgoing cycle. Both must follow the mint.
	closers := map[string]bool{
		"forceCloseDescendants": true,
		"ForceCloseWithReason":  true,
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}

	fn := func(name string) *ast.FuncDecl {
		for _, decl := range f.Decls {
			if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == name {
				return d
			}
		}
		return nil
	}

	rotate := fn("rotatePatrolCycle")
	if rotate == nil {
		t.Fatalf("%s has no rotatePatrolCycle; the close/mint ordering moved and this test "+
			"is scanning nothing (gastown-9pc)", file)
	}

	callName := func(call *ast.CallExpr) string {
		switch f := call.Fun.(type) {
		case *ast.Ident:
			return f.Name
		case *ast.SelectorExpr:
			return f.Sel.Name
		}
		return ""
	}

	var mintPos token.Pos
	var closePositions []token.Pos
	ast.Inspect(rotate, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch name := callName(call); {
		case name == "patrolSpawnSuccessor":
			if !mintPos.IsValid() {
				mintPos = call.Pos()
			}
		case closers[name]:
			closePositions = append(closePositions, call.Pos())
		}
		return true
	})

	// Positive controls: a scan that matched nothing and a scan that found
	// nothing return the same zero.
	if !mintPos.IsValid() {
		t.Fatalf("rotatePatrolCycle never calls patrolSpawnSuccessor; either the mint moved out "+
			"of it or it was renamed, and this test now passes vacuously (%s)", file)
	}
	if len(closePositions) == 0 {
		t.Fatalf("rotatePatrolCycle closes nothing; the close moved elsewhere and the ordering "+
			"this test exists to pin is no longer here (%s)", file)
	}

	for _, pos := range closePositions {
		if pos < mintPos {
			t.Errorf("%s:%d closes the outgoing patrol before minting its successor. "+
				"Close and mint are not atomic, so a death between them leaves the role with "+
				"ZERO hooked patrol wisps, which is terminal and indistinguishable from having "+
				"no work assigned. Minting first leaves TWO, which the next cycle's burn already "+
				"cleans up by design (gastown-9pc).",
				file, fset.Position(pos).Line)
		}
	}

	// runPatrolReport must delegate: a close reintroduced there would run ahead
	// of the mint no matter how rotatePatrolCycle is ordered.
	report := fn("runPatrolReport")
	if report == nil {
		t.Fatalf("%s has no runPatrolReport", file)
	}
	ast.Inspect(report, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if closers[callName(call)] {
			t.Errorf("%s:%d closes the patrol from runPatrolReport. The close belongs behind "+
				"rotatePatrolCycle, after the successor exists (gastown-9pc).",
				file, fset.Position(call.Pos()).Line)
		}
		return true
	})
}
