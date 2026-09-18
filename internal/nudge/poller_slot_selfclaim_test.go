package nudge

import (
	"errors"
	"os"
	"testing"
)

// gastown-213 claims StartPoller can kill the child that just won the slot:
// the child claims independently, the parent's own claim then returns
// ErrPollerAlreadyRunning, and the parent kills the winner.
//
// IT CANNOT. The parent claims on behalf of the CHILD — poller.go passes
// cmd.Process.Pid, and the child passes os.Getpid(), which is the same number —
// and ClaimPollerPidFile only reports ErrPollerAlreadyRunning when the occupant
// is a DIFFERENT live pid (poller_pidfile.go:58, `occupant != pid`). So a
// child-won slot is not an error for the parent and the kill is never reached.
//
// This test is the refutation. If someone later changes the parent to claim
// under its own pid, or drops the `occupant != pid` guard, this fails and the
// bead becomes real.
func TestParentClaimDoesNotFailWhenTheChildAlreadyWonTheSlot(t *testing.T) {
	root := t.TempDir()
	const session = "hq-deacon"

	// The child wins the race and claims the slot under its own pid. A live pid
	// is required or pollerProcessAlive short-circuits and proves nothing, so
	// use this process — the same trick the parent's pid plays in production.
	childPID := os.Getpid()
	if err := ClaimPollerPidFile(root, session, childPID); err != nil {
		t.Fatalf("child could not claim the slot: %v", err)
	}

	// The parent now claims for that same child. This is the exact call
	// StartPoller makes, with the exact pid it passes.
	if err := ClaimPollerPidFile(root, session, childPID); err != nil {
		t.Fatalf("parent's claim for its own child returned %v — "+
			"StartPoller would now kill the process that owns the slot (gastown-213)", err)
	}
}

// The negative control: a DIFFERENT live pid must still be refused, or the test
// above would pass for the wrong reason — a claim that never refuses anyone
// proves nothing about the one case that matters.
func TestClaimStillRefusesADifferentLiveOccupant(t *testing.T) {
	root := t.TempDir()
	const session = "hq-deacon"

	incumbent := os.Getpid()
	if err := ClaimPollerPidFile(root, session, incumbent); err != nil {
		t.Fatalf("incumbent could not claim: %v", err)
	}

	// A different pid, guaranteed not to be the incumbent.
	other := incumbent + 1
	err := ClaimPollerPidFile(root, session, other)
	if !errors.Is(err, ErrPollerAlreadyRunning) {
		t.Fatalf("claim by a different pid returned %v, want ErrPollerAlreadyRunning — "+
			"the guard that makes the first test meaningful is gone", err)
	}
}
