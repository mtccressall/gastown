//go:build !windows

package util

import (
	"os/exec"
	"syscall"

	"github.com/steveyegge/gastown/internal/procsig"
)

// SetProcessGroup configures a command to run in its own process group so that
// context cancellation kills the entire process tree, preventing orphaned children.
func SetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Setpgid above makes the child a group leader, so its pid is its pgid.
		//
		// procsig applies the negation kill(2) wants and refuses a target that
		// is not a real group. That matters because Process.Pid is not stable:
		// os.Process.Release sets it to -1, and an inline kill(-cmd.Process.Pid,
		// ...) would then compute -(-1) == 1 and silently signal init instead of
		// reporting a problem.
		return procsig.SignalGroup(cmd.Process.Pid, syscall.SIGKILL)
	}
}

// SetDetachedProcessGroup configures a command to run in its own process
// group without installing a cancellation hook.
func SetDetachedProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
