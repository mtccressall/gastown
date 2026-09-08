package nudge

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// waitUntilDead blocks until pid stops reading as alive. A duplicate poller that
// is merely "being stopped" is still draining the queue, so the assertion has to
// be that it is gone, not that a kill was issued.
func waitUntilDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for pollerProcessAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d is still alive; StartPoller left a second consumer running", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Positive control for the test below. If StartPoller stopped registering its
// child at all, the displacement test would pass vacuously — an absent write
// cannot displace anybody.
func TestStartPollerRegistersItsChildInTheIndex(t *testing.T) {
	townRoot := t.TempDir()
	const session = "gastown-emerald-register"

	var child *exec.Cmd
	original := spawnPollerCommand
	spawnPollerCommand = func(_, _, _ string) *exec.Cmd {
		child = exec.Command("/bin/sh", "-c", "sleep 30")
		return child
	}
	t.Cleanup(func() {
		spawnPollerCommand = original
		if child != nil && child.Process != nil {
			_ = syscall.Kill(child.Process.Pid, syscall.SIGKILL)
			var ws syscall.WaitStatus
			_, _ = syscall.Wait4(child.Process.Pid, &ws, syscall.WNOHANG, nil)
		}
	})

	pid, err := StartPoller(townRoot, session)
	if err != nil {
		t.Fatalf("StartPoller() error = %v, want nil", err)
	}
	if pid == 0 {
		t.Fatal("StartPoller() returned pid 0 on an empty slot; nothing was spawned")
	}

	data, err := os.ReadFile(pollerPidFile(townRoot, session))
	if err != nil {
		t.Fatalf("reading the index StartPoller was supposed to write: %v", err)
	}
	if got, want := string(data), strconv.Itoa(pid); got != want {
		t.Errorf("index = %q, want %q (the child StartPoller just spawned)", got, want)
	}

	// And the entry has to be visible to the guard that consults it, which is
	// the only reason the write exists.
	if got, alive := pollerAlive(townRoot, session); !alive || got != pid {
		t.Errorf("pollerAlive() = (%d, %v), want (%d, true)", got, alive, pid)
	}
}

// The defect gastown-cb2 names, driven end to end.
//
// StartPoller checks the slot, spawns, and then registers. A directly invoked
// `gt nudge-poller <session>` can claim the slot inside that window — it takes
// the slot lock, StartPoller's write did not. The unlocked os.WriteFile
// therefore overwrote a LIVE poller's registration with the pid of the child
// StartPoller had just spawned, and that child then ADOPTED the entry (it names
// its own pid) and ran. Two live consumers on one queue, which is double
// delivery into a live composer (gt-sglq).
//
// The interference is injected at the spawn seam rather than raced for, so the
// interleaving is the one under test on every run instead of one time in a
// thousand.
func TestStartPollerDoesNotDisplaceAPollerThatClaimedDuringTheWindow(t *testing.T) {
	townRoot := t.TempDir()
	const session = "gastown-emerald-window"

	// The incumbent: a real live process, because the refusal turns on
	// pollerProcessAlive and a fake pid would read as dead and be displaced
	// legitimately.
	incumbent := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := incumbent.Start(); err != nil {
		t.Fatalf("starting incumbent: %v", err)
	}
	t.Cleanup(func() {
		_ = incumbent.Process.Kill()
		_ = incumbent.Wait()
	})
	incumbentPid := incumbent.Process.Pid

	var child *exec.Cmd
	original := spawnPollerCommand
	spawnPollerCommand = func(_, _, _ string) *exec.Cmd {
		// The window: after StartPoller found the slot empty, before it records
		// anything of its own.
		if err := ClaimPollerPidFile(townRoot, session, incumbentPid); err != nil {
			t.Errorf("incumbent claim: %v", err)
		}
		child = exec.Command("/bin/sh", "-c", "sleep 30")
		return child
	}
	t.Cleanup(func() {
		spawnPollerCommand = original
		if child != nil && child.Process != nil {
			_ = syscall.Kill(child.Process.Pid, syscall.SIGKILL)
			var ws syscall.WaitStatus
			_, _ = syscall.Wait4(child.Process.Pid, &ws, syscall.WNOHANG, nil)
		}
	})

	if _, err := StartPoller(townRoot, session); err != nil {
		t.Fatalf("StartPoller() error = %v, want nil (losing the slot is not a failure)", err)
	}

	// The index must still name the poller that actually holds the slot.
	data, err := os.ReadFile(pollerPidFile(townRoot, session))
	if err != nil {
		t.Fatalf("reading pid file: %v", err)
	}
	if got, want := string(data), strconv.Itoa(incumbentPid); got != want {
		t.Errorf("index = %q, want the incumbent %q; StartPoller displaced a live poller", got, want)
	}

	// And the duplicate it spawned must not be left draining the same queue.
	if child == nil || child.Process == nil {
		t.Fatal("no child was spawned; the seam did not run")
	}
	waitUntilDead(t, child.Process.Pid)
}

// StopPoller used to unlink the slot with a bare os.Remove, outside the lock and
// with no ownership check, so a replacement that had claimed the slot since the
// unlocked read lost its registration — a live poller nothing tracks, which is
// what lets the next StartPoller add a second consumer.
//
// Proving the ownership semantics is ReleasePollerPidFile's own job and its
// tests cover it. What this proves is the half that was missing: StopPoller
// PARTICIPATES in the slot lock. With the lock held elsewhere the removal cannot
// proceed, and the entry survives; the old code took no lock and deleted
// regardless.
func TestStopPollerWillNotUnlinkTheSlotWithoutTheLock(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "gastown-emerald-stop"

	if err := os.MkdirAll(pollerPidDir(townRoot), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A dead pid: StopPoller reaches its cleanup without signalling anybody.
	if err := os.WriteFile(pollerPidFile(townRoot, session), []byte("999999999"), 0644); err != nil {
		t.Fatalf("writing pid file: %v", err)
	}

	unlock, err := tryLockFile(pollerSlotLockFile(townRoot, session))
	if err != nil {
		t.Fatalf("taking the slot lock: %v", err)
	}
	defer unlock()

	if err := StopPoller(townRoot, session); err == nil {
		t.Error("StopPoller() = nil while the slot lock was held; the removal did not take the lock")
	}
	if _, err := os.Stat(pollerPidFile(townRoot, session)); err != nil {
		t.Errorf("the entry was removed without the lock: stat err = %v", err)
	}
}

// pollerAlive clears a stale entry, and StartPoller calls it before spawning —
// so that unlink is one of StartPoller's unlocked writes too, and it can delete
// a successor's live registration exactly the way StopPoller's could. Same
// argument as the test above: what is asserted here is participation in the
// lock, which is what was absent.
func TestPollerAliveWillNotClearAStaleEntryWithoutTheLock(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "gastown-emerald-alive"

	if err := os.MkdirAll(pollerPidDir(townRoot), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pollerPidFile(townRoot, session), []byte("999999999"), 0644); err != nil {
		t.Fatalf("writing pid file: %v", err)
	}

	unlock, err := tryLockFile(pollerSlotLockFile(townRoot, session))
	if err != nil {
		t.Fatalf("taking the slot lock: %v", err)
	}
	defer unlock()

	// The verdict is unchanged — a dead pid names no poller either way.
	if pid, alive := pollerAlive(townRoot, session); alive || pid != 0 {
		t.Errorf("pollerAlive() = (%d, %v), want (0, false)", pid, alive)
	}
	if _, err := os.Stat(pollerPidFile(townRoot, session)); err != nil {
		t.Errorf("the entry was removed without the lock: stat err = %v", err)
	}
}
