// Package procsig sends signals to one process or one process group, and
// refuses the target values that kill(2) reinterprets as wildcards.
//
// kill(2) does not read its first argument as "a process id". It reads it as a
// selector, and three of its values select something far larger than a caller
// ever means:
//
//	pid > 0   the process with that id            <- the only intended case
//	pid == 0  EVERY process in the caller's group
//	pid == -1 EVERY process the caller may signal, i.e. the whole session
//	pid < -1  every process in group -pid
//
// So a stale, unset or sentinel pid does not produce a failed signal, and it
// does not produce a signal to the wrong process. It produces a signal to
// everything. The failure has no error return to check and no partial state to
// notice: the caller is usually killed by its own call.
//
// This is not theoretical. On 2026-09-11 a Gas Town test cleanup read
// os/exec.Cmd.Process.Pid after the code under test had called Process.Release(),
// which Go documents as setting that field to -1 ("Unfortunately, for historical
// reasons, on systems other than Windows, Release sets the Pid field to -1" --
// os/exec.go). The resulting kill(-1, SIGKILL) destroyed every process the user
// owned -- all tmux sessions and agents, the Dolt server, two VMs, and both the
// SSH and RDP login sessions -- four times in one morning, while the test
// reported PASS each time. Audit record:
//
//	syscall=kill a0=0xffffffffffffffff a1=9 success=yes comm="nudge.test"
//
// Two properties of this package are what make that unrepresentable rather than
// merely discouraged:
//
//   - SignalGroup takes a POSITIVE pgid and performs the negation kill(2) wants
//     itself, exactly once. Call sites never write the minus sign, so they
//     cannot write it twice, and a released -1 arrives as -1 (rejected) instead
//     of arriving as a plausible-looking +1.
//   - Every target is validated before the syscall, and a refusal is a returned
//     error rather than a silent no-op, so a caller that was about to do
//     something catastrophic finds out.
//
// Callers that legitimately need the wildcard semantics of kill(2) must call
// syscall.Kill directly and say why.
package procsig

import (
	"errors"
	"fmt"
)

// ErrUnsafeTarget reports a pid or pgid that names something other than one
// specific process or one specific process group. Match it with errors.Is.
var ErrUnsafeTarget = errors.New("unsafe signal target")

// checkPID rejects every pid kill(2) would not treat as a single process.
//
// 1 is rejected along with 0 and the negatives. Nothing in this codebase has a
// reason to signal init, and a 1 in a pid variable is far more likely to be a
// parsed empty string, a pgid that got adopted by init, or a sentinel than a
// deliberate decision to kill the system's process 1.
func checkPID(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("%w: refusing to signal pid %d", ErrUnsafeTarget, pid)
	}
	return nil
}

// checkPGID rejects every process-group id that does not name one real group.
//
// pgid 1 is the trap this package exists for: a caller writing the documented
// kill(-pgid, sig) idiom turns it into kill(-1, sig), which is the whole-session
// wildcard rather than a group. An orphan reparented to init, or a pgid parsed
// out of empty or malformed output, lands here.
func checkPGID(pgid int) error {
	if pgid <= 1 {
		return fmt.Errorf("%w: refusing to signal process group %d", ErrUnsafeTarget, pgid)
	}
	return nil
}
