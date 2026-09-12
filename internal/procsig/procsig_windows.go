//go:build windows

package procsig

import (
	"os"
	"syscall"
)

// SignalPID sends sig to exactly one process.
//
// Windows has no kill(2) and therefore none of the wildcard semantics this
// package guards against, but the validation is kept identical so a target that
// would be rejected on Unix is rejected here too. A guard that holds on only
// one platform is one a developer can stop believing in.
func SignalPID(pid int, sig syscall.Signal) error {
	if err := checkPID(pid); err != nil {
		return err
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

// SignalGroup terminates the process identified by pgid.
//
// Windows does not expose POSIX process groups; the rest of this codebase
// already models a group as the process itself (see internal/tmux), and that
// convention is preserved here.
func SignalGroup(pgid int, sig syscall.Signal) error {
	if err := checkPGID(pgid); err != nil {
		return err
	}
	proc, err := os.FindProcess(pgid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
