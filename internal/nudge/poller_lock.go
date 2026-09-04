package nudge

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Claiming and releasing a poller slot is a read followed by a write, and no
// filesystem primitive expresses that pair atomically: link() cannot replace,
// rename() cannot refuse, and unlink() cannot compare. Two claimants inspecting
// the same stale entry will therefore both decide to take it, and one will
// delete the other's registration on the way past — leaving a live poller
// untracked and letting the next StartPoller add a duplicate consumer to the
// queue, which is the whole defect the pid file exists to prevent.
//
// So the pair is serialized by an advisory lock on a sidecar file. The lock is
// held by the kernel against an open descriptor, which means it is released when
// the holder dies however it dies: it cannot go stale the way an O_EXCL lock
// file can, and no reaper has to clean up after a killed poller.
//
// Scope, stated rather than implied: StartPoller and StopPoller in poller.go
// touch this pid file without taking the lock, so a claimant is serialized
// against other claimants but not against those two. Making them participate
// means editing poller.go, which belongs to an unmerged PR, so it is a follow-up
// rather than a silent gap.

const (
	// slotLockRetryInterval is how long to wait between attempts at a busy lock.
	slotLockRetryInterval = 20 * time.Millisecond
	// slotLockTimeout bounds the wait. A poller that cannot get the lock must
	// fail loudly: a hang here would look exactly like the silent undelivered
	// queue this whole change is about.
	slotLockTimeout = 2 * time.Second
)

// errSlotLockBusy reports that another process holds the lock right now.
var errSlotLockBusy = errors.New("poller slot lock is held")

// pollerSlotLockFile returns the sidecar lock path for a session's slot. It sits
// beside the pid file with a distinct suffix so a census of *.pid never sees it.
func pollerSlotLockFile(townRoot, session string) string {
	return pollerPidFile(townRoot, session) + ".lock"
}

// lockPollerSlot takes the advisory lock for a session's pid-file slot and
// returns the function that gives it back. The returned function is safe to call
// exactly once and is never nil on success.
func lockPollerSlot(townRoot, session string) (func(), error) {
	dir := pollerPidDir(townRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating poller pid dir: %w", err)
	}
	path := pollerSlotLockFile(townRoot, session)

	deadline := time.Now().Add(slotLockTimeout)
	for {
		unlock, err := tryLockFile(path)
		if err == nil {
			return unlock, nil
		}
		if !errors.Is(err, errSlotLockBusy) {
			return nil, fmt.Errorf("locking poller slot %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("locking poller slot %s: still held after %s", path, slotLockTimeout)
		}
		time.Sleep(slotLockRetryInterval)
	}
}
