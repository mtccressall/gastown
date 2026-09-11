package nudge

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// reapTestChild kills and reaps a test child, addressed by a pid captured while
// it was still valid.
//
// It refuses any pid that does not name a real process, and that refusal is the
// entire point of the helper rather than defensive habit. kill(2) gives 0, -1
// and negative pids a COMPLETELY DIFFERENT MEANING from "this process": -1 is
// "every process this uid may signal", and 0 is "my own process group". So a
// cleanup that forwards a stale Process.Pid does not fail, and does not signal
// the wrong process — it takes the whole login session down.
//
// That is not hypothetical here. StartPoller calls cmd.Process.Release() on its
// success path to detach the poller, and Go's Release sets Process.Pid to -1
// ("Unfortunately, for historical reasons, on systems other than Windows,
// Release sets the Pid field to -1" — os/exec.go). A t.Cleanup registered
// around StartPoller therefore reads -1, not a pid. On 2026-09-11 that reached
// kill(-1, SIGKILL) from this test binary four times and destroyed every
// process the user owned — all tmux sessions and agents, the Dolt server, two
// VMs, and both the SSH and RDP login sessions — while the test itself
// reported PASS. Audit record: syscall=kill a0=0xffffffffffffffff a1=9
// success=yes comm="nudge.test".
//
// Capture the pid at a moment it is known good and pass it here; never read
// Process.Pid after the code under test may have released it.
func reapTestChild(t *testing.T, c *exec.Cmd, pid int) {
	t.Helper()

	// Already waited for by the code under test. The pid has been reaped and
	// the kernel is free to hand that number to somebody else, so signalling
	// it now is a coin flip on an unrelated process.
	if c != nil && c.ProcessState != nil {
		return
	}
	if pid <= 1 {
		// Nothing addressable. A child that was released is detached by
		// design; leaking a short-lived `sleep` is the correct trade against
		// handing kill(2) a wildcard.
		t.Logf("reapTestChild: no addressable pid (%d); leaving the child to exit on its own", pid)
		return
	}

	_ = syscall.Kill(pid, syscall.SIGKILL)
	var ws syscall.WaitStatus
	_, _ = syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
}

// livePID returns c's pid, or 0 when c has no process that can be addressed.
// It collapses "never started", "already released" (-1) and the reserved pids
// into one unusable value so callers cannot accidentally forward them.
func livePID(c *exec.Cmd) int {
	if c == nil || c.Process == nil || c.Process.Pid <= 1 {
		return 0
	}
	return c.Process.Pid
}

// waitUntilDead blocks until pid stops reading as alive. A duplicate poller that
// is merely "being stopped" is still draining the queue, so the assertion has to
// be that it is gone, not that a kill was issued.
//
// It fails on a pid that names no process rather than returning: pollerProcessAlive
// answers false for anything <= 0, so a released or unset pid would satisfy this
// loop on the first iteration and the test would pass having proved nothing.
func waitUntilDead(t *testing.T, pid int) {
	t.Helper()
	if pid <= 1 {
		t.Fatalf("waitUntilDead got pid %d, which names no process; the assertion would have passed vacuously", pid)
	}
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
	// Set from StartPoller's return value, which is the pid it captured before
	// releasing the process — the last moment the child is addressable.
	spawnedPid := 0

	original := spawnPollerCommand
	spawnPollerCommand = func(_, _, _ string) *exec.Cmd {
		child = exec.Command("/bin/sh", "-c", "sleep 30")
		return child
	}
	t.Cleanup(func() {
		spawnPollerCommand = original
		reapTestChild(t, child, spawnedPid)
	})

	pid, err := StartPoller(townRoot, session)
	spawnedPid = pid
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
	childPid := 0

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
		reapTestChild(t, child, childPid)
	})

	if _, err := StartPoller(townRoot, session); err != nil {
		t.Fatalf("StartPoller() error = %v, want nil (losing the slot is not a failure)", err)
	}

	// Snapshot the pid immediately, before anything else can release it. On
	// this path StartPoller stops the duplicate with Kill+Wait rather than
	// Release, so the pid is still readable here — but reading it once, now,
	// is what keeps that a property of this line instead of a property of
	// StartPoller's internals.
	childPid = livePID(child)

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
	waitUntilDead(t, childPid)
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
