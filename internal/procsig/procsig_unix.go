//go:build !windows

package procsig

import "syscall"

// SignalPID sends sig to exactly one process.
//
// It returns ErrUnsafeTarget, without signalling anything, for any pid that
// kill(2) would widen into a group or a session.
func SignalPID(pid int, sig syscall.Signal) error {
	if err := checkPID(pid); err != nil {
		return err
	}
	return syscall.Kill(pid, sig)
}

// SignalGroup sends sig to every member of the process group pgid.
//
// pgid is POSITIVE. The negation kill(2) requires is applied here and nowhere
// else, which is the point: a call site that never writes the minus sign cannot
// pass an already-negative value and silently flip it back into a single pid,
// and cannot turn a released -1 into a legitimate-looking 1.
func SignalGroup(pgid int, sig syscall.Signal) error {
	if err := checkPGID(pgid); err != nil {
		return err
	}
	return syscall.Kill(-pgid, sig)
}
