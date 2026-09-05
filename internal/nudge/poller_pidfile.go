package nudge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrPollerAlreadyRunning reports that another live poller already holds the
// pid-file slot for a session, so the caller must not start a second one.
var ErrPollerAlreadyRunning = errors.New("a nudge poller is already running for this session")

// ClaimPollerPidFile takes the pid-file slot for session on behalf of pid.
//
// The slot is <townRoot>/.runtime/nudge_poller/<session>.pid, the index that
// StartPoller, StopPoller and pollerAlive all consult. StartPoller writes it for
// the pollers it spawns, but a directly invoked `gt nudge-poller <session>` used
// to write nothing at all (gt-di75): the "already running" guard could not fire,
// so a later StartPoller put a SECOND poller on the same queue — double delivery
// into a live composer — and any census keyed on pid files reported the running
// poller as absent.
//
// Inspecting the slot and then taking it is a read-then-write pair, so it runs
// under the slot lock; see poller_lock.go for why no single filesystem call can
// express it. An occupied slot is resolved by who occupies it:
//
//   - our own pid, because StartPoller registered the child it just spawned
//     before the child got here: adopt it and carry on;
//   - a process that is still alive: refuse with ErrPollerAlreadyRunning,
//     because starting anyway is the duplicate this function exists to prevent;
//   - anything else — a corpse, a pid that is gone, unparseable bytes: it names
//     no poller, so take the slot.
//
// The write is a rename rather than an in-place write, because every reader of
// this index reads it WITHOUT the lock. os.WriteFile truncates before it writes,
// and a reader landing in that gap sees an empty file, fails to parse a pid, and
// concludes there is no poller — which is the duplicate again, arriving by the
// other road. A rename is atomic: a reader sees the old pid or the new one.
//
// The format is the one every reader already parses: a bare decimal pid with no
// trailing newline.
func ClaimPollerPidFile(townRoot, session string, pid int) error {
	unlock, err := lockPollerSlot(townRoot, session)
	if err != nil {
		return err
	}
	defer unlock()

	path := pollerPidFile(townRoot, session)

	occupant, ok, err := readPidFile(path)
	if err != nil {
		return err
	}
	if ok && occupant != pid && pollerProcessAlive(occupant) {
		return fmt.Errorf("%w (pid %d)", ErrPollerAlreadyRunning, occupant)
	}

	tmpPath, err := writePidTemp(filepath.Dir(path), filepath.Base(path), pid)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publishing poller pid file %s: %w", path, err)
	}
	return nil
}

// ReleasePollerPidFile gives up session's slot, but never at the expense of a
// poller that has since taken it over.
//
// StopPoller removes this file itself right after SIGTERM, so by the time a
// dying poller runs its cleanup a replacement may already own the slot. Deleting
// that would leave a live poller untracked and let the next StartPoller spawn a
// duplicate — precisely what the pid file is for. The check and the unlink are
// therefore one critical section, under the same lock the claim takes.
//
// A missing file is not an error — StopPoller having got there first is the
// normal case. Neither is an entry nobody can parse: it names no poller, so
// clearing it costs nothing and leaving it would keep pollerAlive answering from
// unreadable bytes.
func ReleasePollerPidFile(townRoot, session string, pid int) error {
	unlock, err := lockPollerSlot(townRoot, session)
	if err != nil {
		return err
	}
	defer unlock()

	path := pollerPidFile(townRoot, session)

	occupant, ok, err := readPidFile(path)
	if err != nil {
		return err
	}
	if ok && occupant != pid {
		return nil // someone else owns this slot now
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing poller pid file %s: %w", path, err)
	}
	return nil
}

// writePidTemp writes pid to a fresh temp file in dir and returns its path. The
// file is complete and correctly permissioned before any caller renames it into
// the index, which is what keeps a half-written pid out of an unlocked reader's
// hands.
func writePidTemp(dir, base string, pid int) (string, error) {
	f, err := os.CreateTemp(dir, base+".tmp")
	if err != nil {
		return "", fmt.Errorf("creating temp poller pid file in %s: %w", dir, err)
	}
	path := f.Name()

	if _, err := f.WriteString(strconv.Itoa(pid)); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("writing temp poller pid file %s: %w", path, err)
	}
	// CreateTemp makes the file 0600; the rest of the index is 0644 and other
	// agents' censuses read this directory.
	if err := f.Chmod(0644); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("chmod temp poller pid file %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("closing temp poller pid file %s: %w", path, err)
	}
	return path, nil
}

// readPidFile returns the pid recorded at path. The bool is false when the file
// is gone or holds something that is not a pid — an absence and a corrupt entry
// both mean "this names no poller", and neither is an error to the caller.
func readPidFile(path string) (int, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("reading poller pid file %s: %w", path, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false, nil
	}
	return pid, true, nil
}
