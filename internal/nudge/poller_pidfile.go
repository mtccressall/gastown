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

// claimAttempts bounds the retry loop that clears a stale entry and re-claims.
// Each retry costs one lost race against another starter; three is generous for
// a contest that only two processes can enter.
const claimAttempts = 3

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
// The claim is a single atomic step, not a check followed by a write. The pid is
// written to a temp file first and linked into place, so a claim either wins
// outright or loses to an existing file; there is no moment at which the slot
// holds a created-but-empty file for an unlocked reader to misread as "no
// poller" and act on. An occupied slot is then resolved by who occupies it:
//
//   - our own pid, because StartPoller registered the child it just spawned
//     before the child got here: adopt it and carry on;
//   - a process that is still alive: refuse with ErrPollerAlreadyRunning,
//     because starting anyway is the duplicate this function exists to prevent;
//   - anything else — a corpse, a pid that is gone, unparseable bytes: clear it
//     and try the claim again.
//
// The format is the one every reader already parses: a bare decimal pid with no
// trailing newline.
func ClaimPollerPidFile(townRoot, session string, pid int) error {
	dir := pollerPidDir(townRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating poller pid dir: %w", err)
	}
	path := pollerPidFile(townRoot, session)

	tmpPath, err := writePidTemp(dir, filepath.Base(path), pid)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPath) }()

	for attempt := 0; attempt < claimAttempts; attempt++ {
		err := os.Link(tmpPath, path)
		if err == nil {
			return nil // claimed, and the content was complete before it appeared
		}
		if !os.IsExist(err) {
			// Not a contested slot — the filesystem refused the link. Report it
			// rather than falling back to a non-atomic write.
			return fmt.Errorf("claiming poller pid file %s: %w", path, err)
		}

		occupant, ok, err := readPidFile(path)
		if err != nil {
			return err
		}
		switch {
		case ok && occupant == pid:
			return nil // StartPoller registered us already
		case ok && pollerProcessAlive(occupant):
			return fmt.Errorf("%w (pid %d)", ErrPollerAlreadyRunning, occupant)
		default:
			// A corpse, a pid that is gone, bytes nobody can parse, or a file
			// that vanished between the link and the read. None of those names a
			// live poller, so clearing it costs nothing and the claim retries.
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("clearing stale poller pid file %s: %w", path, err)
			}
		}
	}

	return fmt.Errorf("claiming poller pid file %s: lost the claim race %d times", path, claimAttempts)
}

// ReleasePollerPidFile gives up session's slot, but never at the expense of a
// poller that has since taken it over.
//
// Reading the file and then unlinking it is two steps, and the gap between them
// is not bounded by anything: the process can be descheduled there, which is long
// enough for StopPoller to remove the entry and a replacement to publish its own.
// The old cleanup would then delete the replacement's registration, leave a live
// poller untracked, and let the next StartPoller spawn a duplicate — precisely
// what the pid file is for.
//
// So the decision is made by the rename, which is atomic: one step both empties
// the slot and hands us the file to inspect. If it was ours, emptying the slot
// was the whole intent and we are done. If a successor had already claimed it, we
// put it back with a link, which fails if a newer claim has arrived rather than
// overwriting it.
//
// A missing file is not an error — StopPoller having got there first is the
// normal case.
func ReleasePollerPidFile(townRoot, session string, pid int) error {
	path := pollerPidFile(townRoot, session)
	held := path + ".releasing." + strconv.Itoa(pid)

	if err := os.Rename(path, held); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("releasing poller pid file %s: %w", path, err)
	}
	defer func() { _ = os.Remove(held) }()

	occupant, ok, err := readPidFile(held)
	if err != nil {
		return err
	}
	if ok && occupant != pid {
		// Not ours. Restore it, but never on top of a newer claim: Link fails
		// with EEXIST instead of clobbering, and a slot somebody else has already
		// claimed needs nothing from us.
		if err := os.Link(held, path); err != nil && !os.IsExist(err) {
			return fmt.Errorf("restoring poller pid file %s: %w", path, err)
		}
	}
	return nil
}

// writePidTemp writes pid to a fresh temp file in dir and returns its path. The
// file is complete and correctly permissioned before any caller links it into
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
