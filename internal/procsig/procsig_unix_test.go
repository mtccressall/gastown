//go:build !windows

package procsig

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// EVERY rejection test in this file signals with syscall.Signal(0), NOT SIGKILL,
// and that is deliberate rather than incidental.
//
// A rejection test only means anything if the guard is broken. If a future edit
// breaks it, the syscall these tests would then reach is the one under
// discussion -- and with SIGKILL that is kill(-1, SIGKILL), which does not fail
// the test, it destroys the machine running it and every other process on it.
// With signal 0 the same regression fails loudly and harmlessly: kill(-1, 0)
// only asks about permissions.
//
// A test for a catastrophic failure must not be capable of causing it.
const harmless = syscall.Signal(0)

func TestSignalPIDRefusesWildcardsWithoutSyscalling(t *testing.T) {
	for _, pid := range []int{-1, 0, 1, -2} {
		err := SignalPID(pid, harmless)
		if err == nil {
			t.Errorf("SignalPID(%d) = nil, want refusal", pid)
		} else if !errors.Is(err, ErrUnsafeTarget) {
			t.Errorf("SignalPID(%d) error = %v, want ErrUnsafeTarget", pid, err)
		}
	}
}

func TestSignalGroupRefusesWildcardsWithoutSyscalling(t *testing.T) {
	for _, pgid := range []int{-1, 0, 1, -2} {
		err := SignalGroup(pgid, harmless)
		if err == nil {
			t.Errorf("SignalGroup(%d) = nil, want refusal", pgid)
		} else if !errors.Is(err, ErrUnsafeTarget) {
			t.Errorf("SignalGroup(%d) error = %v, want ErrUnsafeTarget", pgid, err)
		}
	}
}

// Positive control for both tests above. Without it they pass identically
// against a SignalPID that refuses everything, which would be a guard that
// quietly stops the program signalling anything at all.
func TestSignalPIDActuallySignalsARealProcess(t *testing.T) {
	child := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := child.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}
	pid := child.Process.Pid
	t.Cleanup(func() { _ = child.Wait() })

	if err := SignalPID(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("SignalPID(%d, SIGKILL) = %v, want nil", pid, err)
	}
	waitGone(t, child)
}

// The group path needs its own positive control: it is the one that negates,
// so "accepts a real target" and "negates correctly" are different claims and
// only a real group death establishes the second.
func TestSignalGroupActuallySignalsARealGroup(t *testing.T) {
	child := exec.Command("/bin/sh", "-c", "sleep 30")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}
	// Setpgid makes the child a group leader, so its pgid equals its pid.
	pgid := child.Process.Pid
	t.Cleanup(func() { _ = child.Wait() })

	if err := SignalGroup(pgid, syscall.SIGKILL); err != nil {
		t.Fatalf("SignalGroup(%d, SIGKILL) = %v, want nil", pgid, err)
	}
	waitGone(t, child)
}

// The regression, driven through the exact mechanism that took this host down
// four times on 2026-09-11.
//
// os/exec.Cmd.Process.Pid is not stable for the lifetime of the Cmd. Release()
// sets it to -1, and Gas Town calls Release on every detached child it spawns
// (internal/nudge.StartPoller does it to let the poller outlive the caller).
// Anything that reads Process.Pid afterwards -- a t.Cleanup, a stop path, a
// bookkeeping struct populated later -- is holding a wildcard, not a pid.
//
// Signal 0 here for the reason given at the top of this file: if this assertion
// ever fails, it must fail as a test result and not as an outage.
func TestAReleasedExecProcessIsRefusedByBothEntryPoints(t *testing.T) {
	child := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := child.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}
	realPID := child.Process.Pid
	t.Cleanup(func() { _ = SignalPID(realPID, syscall.SIGKILL) })

	if err := child.Process.Release(); err != nil {
		t.Fatalf("Release(): %v", err)
	}

	// Pin Go's documented behaviour, so that if a future Go stops doing this
	// the reason these guards exist is still legible rather than mysterious.
	if child.Process.Pid != -1 {
		t.Fatalf("Process.Pid after Release() = %d, want -1; the premise of this test has changed",
			child.Process.Pid)
	}

	if err := SignalPID(child.Process.Pid, harmless); !errors.Is(err, ErrUnsafeTarget) {
		t.Errorf("SignalPID(released pid) error = %v, want ErrUnsafeTarget", err)
	}
	// The dangerous spelling: the caller believes it is negating a pgid, and
	// -(-1) is +1, so an unguarded kill would quietly target init instead of
	// erroring. SignalGroup takes the positive value and refuses it outright.
	if err := SignalGroup(child.Process.Pid, harmless); !errors.Is(err, ErrUnsafeTarget) {
		t.Errorf("SignalGroup(released pid) error = %v, want ErrUnsafeTarget", err)
	}
}

func waitGone(t *testing.T, c *exec.Cmd) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_, _ = c.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("pid %d survived SIGKILL; the signal did not reach it", c.Process.Pid)
	}
}
