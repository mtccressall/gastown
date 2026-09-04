package nudge

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// WritePollerPidFile records pid as the poller for session in the pid-file index
// under <townRoot>/.runtime/nudge_poller/.
//
// StartPoller writes this file for the pollers it spawns, but a directly invoked
// `gt nudge-poller <session>` used to leave no trace at all (gt-di75): the
// "already running" guard could not fire, so a later StartPoller spawned a
// SECOND poller onto the same queue — double delivery into a live composer — and
// any census keyed on pid files reported the running poller as absent.
//
// The path and the format are the ones StartPoller and pollerAlive already
// agree on: a bare decimal pid with no trailing newline.
func WritePollerPidFile(townRoot, session string, pid int) error {
	if err := os.MkdirAll(pollerPidDir(townRoot), 0755); err != nil {
		return fmt.Errorf("creating poller pid dir: %w", err)
	}
	path := pollerPidFile(townRoot, session)
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("writing poller pid file %s: %w", path, err)
	}
	return nil
}

// RemovePollerPidFile clears session's entry from the pid-file index, but only
// if it still names pid.
//
// The ownership check is what keeps an exiting poller from deleting the index
// entry of the poller that replaced it: StopPoller removes the file itself right
// after SIGTERM, so by the time the dying process runs its own cleanup a fresh
// poller may already own the slot. Removing that would recreate exactly the
// blind spot this function exists to close.
//
// A missing file is not an error — StopPoller having got there first is the
// normal case.
func RemovePollerPidFile(townRoot, session string, pid int) error {
	path := pollerPidFile(townRoot, session)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading poller pid file %s: %w", path, err)
	}

	recorded, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err == nil && recorded != pid {
		return nil // someone else owns this slot now
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing poller pid file %s: %w", path, err)
	}
	return nil
}
