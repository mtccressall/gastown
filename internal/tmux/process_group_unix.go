//go:build !windows

package tmux

import (
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/steveyegge/gastown/internal/procsig"
)

// killProcessGroup terminates every member of the process group pgid.
//
// The signal goes through procsig, which refuses a pgid of 0, 1 or a negative,
// because the kill(-pgid, sig) idiom this function used to write inline turns
// those into wildcards: kill(-1, sig) signals every process the user owns.
// A pgid of 1 is not exotic here -- it is what a process reparented to init can
// end up reporting, and it is what an empty or malformed `ps -o pgid=` parses
// to. Callers elsewhere in this package already refuse "0" and "1" as strings
// before getting this far (see collectReparentedGroupMembers); this closes the
// same hole at the site that actually signals.
func killProcessGroup(pgid int) {
	if err := procsig.SignalGroup(pgid, syscall.SIGTERM); err != nil {
		// Either the group is gone or the target was a wildcard. Neither is
		// worth escalating to SIGKILL.
		return
	}
	time.Sleep(100 * time.Millisecond)
	_ = procsig.SignalGroup(pgid, syscall.SIGKILL)
}

// getParentPID returns the parent process ID (PPID) for a given PID.
// Returns empty string if the process doesn't exist or PPID can't be determined.
func getParentPID(pid string) string {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", pid).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getProcessGroupID returns the process group ID (PGID) for a given PID.
// Returns empty string if the process doesn't exist or PGID can't be determined.
func getProcessGroupID(pid string) string {
	out, err := exec.Command("ps", "-o", "pgid=", "-p", pid).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getProcessGroupMembers returns all PIDs in a process group.
// This finds processes that share the same PGID, including those that reparented to init.
func getProcessGroupMembers(pgid string) []string {
	// Use ps to find all processes with this PGID
	// On macOS: ps -axo pid,pgid
	// On Linux: ps -eo pid,pgid
	out, err := exec.Command("ps", "-axo", "pid,pgid").Output()
	if err != nil {
		return nil
	}

	var members []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimSpace(fields[1]) == pgid {
			members = append(members, strings.TrimSpace(fields[0]))
		}
	}
	return members
}
