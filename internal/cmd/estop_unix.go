//go:build !windows

package cmd

import (
	"fmt"
	"strconv"
	"syscall"

	"github.com/steveyegge/gastown/internal/procsig"
	"github.com/steveyegge/gastown/internal/tmux"
)

var (
	sigFreeze = syscall.SIGTSTP
	sigThaw   = syscall.SIGCONT
)

// signalSessionGroup sends a signal to the process group of a tmux session's
// pane process. This uses process-group signaling instead of recursive pgrep,
// which is both safer and catches all descendants.
//
// The pid comes from `tmux display-message`, i.e. from parsing another
// program's output, and it is used as a process GROUP. That combination is the
// one that takes a machine down: an empty or unexpected field parses to 0 and
// kill(0, sig) signals the caller's own group, while a 1 makes kill(-1, sig)
// signal every process the user owns. gt has been observed reporting live
// services as PID 0, so this is a value that really does arrive.
//
// procsig.SignalGroup applies the negation and refuses those targets, so a bad
// parse becomes a returned error naming the session instead of a session-wide
// SIGKILL.
func signalSessionGroup(t *tmux.Tmux, sessionName string, sig syscall.Signal) error {
	pidStr, err := t.GetPanePID(sessionName)
	if err != nil {
		return fmt.Errorf("no PID: %w", err)
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return fmt.Errorf("invalid PID %q: %w", pidStr, err)
	}

	// The pane's shell is the process group leader, so its pid is the pgid.
	if err := procsig.SignalGroup(pid, sig); err != nil {
		return fmt.Errorf("signalling session %q (pane PID %q): %w", sessionName, pidStr, err)
	}
	return nil
}
