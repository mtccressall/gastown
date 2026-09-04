package nudge

import (
	"fmt"
	"os"
	"path/filepath"
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
//
// The write goes through a temp file and a rename rather than os.WriteFile,
// because every reader of this index reads it unlocked. os.WriteFile truncates
// before it writes, so a concurrent pollerAlive can read the empty file, fail to
// parse a pid, report no poller, and let StartPoller spawn a duplicate onto the
// queue — the exact outcome this function exists to prevent. A rename is atomic:
// a reader sees either the old pid or the new one, never a half-written file.
func WritePollerPidFile(townRoot, session string, pid int) error {
	dir := pollerPidDir(townRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating poller pid dir: %w", err)
	}
	path := pollerPidFile(townRoot, session)

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp")
	if err != nil {
		return fmt.Errorf("creating temp poller pid file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		// No-op once the rename has succeeded.
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.WriteString(strconv.Itoa(pid)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp poller pid file %s: %w", tmpPath, err)
	}
	// CreateTemp makes the file 0600; the index is world-readable elsewhere.
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp poller pid file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp poller pid file %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publishing poller pid file %s: %w", path, err)
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
// The compare and the unlink are two adjacent syscalls, not one atomic step, and
// no lock available here would make them one: the competing writer is
// StartPoller, which writes this file unlocked from poller.go. What that leaves
// is a window a few microseconds wide in which a whole StartPoller cycle —
// pollerAlive, a process spawn, then the write — would have to complete, and a
// spawn costs milliseconds. Every gate here fails towards DECLINING to remove,
// which is the safe direction: a stale entry is self-healing, because pollerAlive
// deletes any pid file whose process is not alive, and after the zombie fix in
// poller_process_unix.go that now includes unreaped corpses. A wrongly deleted
// entry is not self-healing — it is a duplicate poller.
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
