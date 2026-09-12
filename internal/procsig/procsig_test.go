package procsig

import (
	"errors"
	"testing"
)

// The validators are tested directly, without a syscall anywhere near them, so
// this file is portable and so a failure here can never signal anything.
func TestCheckPIDRejectsEveryWildcardSelector(t *testing.T) {
	// -1 and 0 are the two that destroy a machine; 1 is init; the rest are the
	// kill(2) "process group -pid" range that a caller reaches by negating a
	// pgid twice.
	for _, pid := range []int{-1, 0, 1, -2, -1000, -2147483647} {
		if err := checkPID(pid); err == nil {
			t.Errorf("checkPID(%d) = nil, want an error: kill(2) does not read this as one process", pid)
		} else if !errors.Is(err, ErrUnsafeTarget) {
			t.Errorf("checkPID(%d) error = %v, want it to wrap ErrUnsafeTarget", pid, err)
		}
	}
}

func TestCheckPIDAcceptsRealPIDs(t *testing.T) {
	// Without this the test above passes just as well against a checkPID that
	// rejects everything, which would be a guard that silently disables every
	// signal in the program.
	for _, pid := range []int{2, 17, 3241670, 4194304} {
		if err := checkPID(pid); err != nil {
			t.Errorf("checkPID(%d) = %v, want nil", pid, err)
		}
	}
}

func TestCheckPGIDRejectsEveryWildcardSelector(t *testing.T) {
	// pgid 1 is the one this package exists for: kill(-1, sig) is "every
	// process this uid may signal", and a caller writing the ordinary
	// kill(-pgid, sig) idiom produces it without any mistake being visible.
	for _, pgid := range []int{-1, 0, 1, -2, -1000} {
		if err := checkPGID(pgid); err == nil {
			t.Errorf("checkPGID(%d) = nil, want an error", pgid)
		} else if !errors.Is(err, ErrUnsafeTarget) {
			t.Errorf("checkPGID(%d) error = %v, want it to wrap ErrUnsafeTarget", pgid, err)
		}
	}
}

func TestCheckPGIDAcceptsRealGroups(t *testing.T) {
	for _, pgid := range []int{2, 17, 3241670} {
		if err := checkPGID(pgid); err != nil {
			t.Errorf("checkPGID(%d) = %v, want nil", pgid, err)
		}
	}
}

// The message has to name the number that was refused. A guard that rejects
// silently, or that says only "invalid argument", leaves the next person
// debugging why a legitimate kill stopped working.
func TestRefusalNamesTheTarget(t *testing.T) {
	// Checked for nil before .Error(), so that a regression which drops the
	// guard reports a failure here instead of panicking. A panic aborts the
	// whole package run and hides every result after it -- including the
	// SignalPID and SignalGroup refusals, which are the ones that matter most.
	if err := checkPID(-1); err == nil {
		t.Error("checkPID(-1) = nil, want a refusal")
	} else if got := err.Error(); got != "unsafe signal target: refusing to signal pid -1" {
		t.Errorf("checkPID(-1) message = %q", got)
	}
	if err := checkPGID(1); err == nil {
		t.Error("checkPGID(1) = nil, want a refusal")
	} else if got := err.Error(); got != "unsafe signal target: refusing to signal process group 1" {
		t.Errorf("checkPGID(1) message = %q", got)
	}
}
