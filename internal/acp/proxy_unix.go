//go:build unix

package acp

import (
	"os"
	"syscall"
	"time"

	"github.com/steveyegge/gastown/internal/procsig"

	"github.com/steveyegge/gastown/internal/util"
)

// signalsToHandle returns the signals that Forward() should listen for.
// On Unix, we handle both SIGTERM and SIGINT for graceful shutdown.
func signalsToHandle() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGINT}
}

// setupProcessGroup configures the command to run in its own process group.
// This allows us to terminate the agent and all its children on shutdown.
func (p *Proxy) setupProcessGroup() {
	util.SetProcessGroup(p.cmd)
}

// isProcessAlive checks if the agent process is still running.
// On Unix, we use signal 0 to check process liveness.
func (p *Proxy) isProcessAlive() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	err := p.cmd.Process.Signal(syscall.Signal(0))
	return err == nil
}

// terminateProcess gracefully terminates the agent process.
// On Unix, we send SIGTERM to the process group, then SIGKILL after 2 seconds
// if the process hasn't exited.
func (p *Proxy) terminateProcess() {
	if p.cmd != nil && p.cmd.Process != nil {
		debugLog(p.townRoot, "[Proxy] Shutdown: sending SIGTERM to agent process (pid=%d)", p.cmd.Process.Pid)
		pgid, err := syscall.Getpgid(p.cmd.Process.Pid)
		if err == nil {
			// SAFETY: Never kill our own process group during tests/local runs.
			//
			// That check is necessary and was never sufficient. It compares the
			// target against OUR group and says nothing about whether the target
			// is a group at all: a pgid of 1 passes it, and kill(-1, sig) is
			// "every process this uid may signal". The SIGTERM branch below had
			// no lower bound whatsoever, and the SIGKILL branch tested pgid > 0,
			// which rejects 0 and the negatives and admits precisely the value
			// that empties the machine. procsig supplies the missing half.
			myPgid, _ := syscall.Getpgid(0)
			if pgid != myPgid {
				// Send SIGTERM to the entire process group
				_ = procsig.SignalGroup(pgid, syscall.SIGTERM)
			} else {
				// Only kill the process itself if it shares our group
				_ = procsig.SignalPID(p.cmd.Process.Pid, syscall.SIGTERM)
			}
		} else {
			_ = procsig.SignalPID(p.cmd.Process.Pid, syscall.SIGTERM)
		}

		time.AfterFunc(2*time.Second, func() {
			if p.cmd.ProcessState == nil || !p.cmd.ProcessState.Exited() {
				if pgid == 0 {
					pgid, _ = syscall.Getpgid(p.cmd.Process.Pid)
				}
				myPgid, _ := syscall.Getpgid(0)
				if pgid != myPgid {
					// A refusal here is not a reason to give up on the child:
					// fall through to signalling the process itself, which is
					// what the unguarded code did for pgid <= 0 anyway.
					if procsig.SignalGroup(pgid, syscall.SIGKILL) == nil {
						return
					}
				}
				_ = procsig.SignalPID(p.cmd.Process.Pid, syscall.SIGKILL)
			}
		})
	}
}
