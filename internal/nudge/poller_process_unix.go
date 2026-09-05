//go:build !windows

package nudge

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// pollerProcessAlive reports whether pid names a process that can still do work.
//
// Signal(0) on its own is not a liveness check. A zombie stays in the process
// table until its parent reaps it, so the signal succeeds on a corpse — and
// because StartPoller early-returns "already running" whenever this function
// says yes, a defunct poller permanently blocks its own replacement while its
// queue silently stops draining (gt-5kri part A). Measured live: kill -0 on a
// reaped-pending poller returned rc=0 while ps reported state Z.
//
// On Linux we keep Signal(0) as the first gate — it correctly rejects pids that
// are fully gone — and then read the process state from /proc/<pid>/stat,
// treating 'Z' as dead. Other unix platforms have no /proc; there the state read
// fails and the historical Signal(0) answer stands.
func pollerProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}

	state, ok := procState(pid)
	if !ok {
		// No /proc (darwin), or the process went away between the two reads.
		// Fall back to what Signal(0) just told us.
		return true
	}

	return state != "Z"
}

// procState returns the scheduling state of pid as reported by /proc/<pid>/stat.
// The second return is false when the state could not be read or parsed, which
// callers must treat as "unknown", never as "dead".
func procState(pid int) (string, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	return parseProcState(string(data))
}

// parseProcState extracts field 3 (state) from a raw /proc/<pid>/stat line.
//
// Field 2 is the comm, wrapped in parentheses, and it may contain spaces AND
// parentheses — this town runs a process named "gt.old-be09d037", and a comm of
// "sh (old) v2" is legal. Splitting on spaces from the left is the standard bug.
// The state is the first field after the LAST ')'.
func parseProcState(stat string) (string, bool) {
	lastParen := strings.LastIndexByte(stat, ')')
	if lastParen < 0 {
		return "", false
	}

	rest := strings.TrimLeft(stat[lastParen+1:], " \t\r\n")
	if rest == "" {
		return "", false
	}

	if end := strings.IndexAny(rest, " \t\r\n"); end >= 0 {
		return rest[:end], true
	}
	return rest, true
}
