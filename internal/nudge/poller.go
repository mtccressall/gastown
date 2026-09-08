// poller.go provides a background nudge-queue poller.
//
// The poller is the ONLY drain path for the nudge queue, for every runtime
// including Claude Code. An earlier version of this comment claimed Claude
// agents drain via the UserPromptSubmit hook on every turn and therefore did
// not need a poller. That is wrong: the UserPromptSubmit hooks deployed in a
// Gas Town workspace drain MAIL, not the nudge queue. A session whose poller
// dies has no fallback, and its queued nudges sit until they expire — measured
// as 24 expired, undelivered nudges town-wide (gastown-ku3, gt-gpwt).
//
// The poller runs as a background goroutine launched by crew/manager.Start().
// It polls the queue every PollInterval, waits for the agent to be idle, then
// drains and injects the formatted nudges via tmux NudgeSession.
//
// Lifecycle: StartPoller() → background loop → StopPoller() (or session death).
// A PID file at <townRoot>/.runtime/nudge_poller/<session>.pid allows Stop()
// to clean up even if the original manager has been replaced.
package nudge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/lock"
	"github.com/steveyegge/gastown/internal/util"
)

// Poller tuning defaults (overridable via flags or tests).
var (
	// DefaultPollInterval is how often the poller checks the queue.
	DefaultPollInterval = "10s"
	// DefaultIdleTimeout is how long to wait for the agent to become idle
	// before skipping this poll cycle and trying again next interval.
	DefaultIdleTimeout = "3s"
)

// pollerPidDir returns the directory for poller PID files.
func pollerPidDir(townRoot string) string {
	return filepath.Join(townRoot, constants.DirRuntime, "nudge_poller")
}

// pollerPidFile returns the PID file path for a session's poller.
func pollerPidFile(townRoot, session string) string {
	return filepath.Join(pollerPidDir(townRoot), safePollerName(session)+".pid")
}

// pollerStartLockFile returns the lock path guarding StartPoller's
// check-then-start for a session.
func pollerStartLockFile(townRoot, session string) string {
	return filepath.Join(pollerPidDir(townRoot), safePollerName(session)+".start.lock")
}

// safePollerName turns a session name into a single path segment.
func safePollerName(session string) string {
	return strings.ReplaceAll(session, "/", "_")
}

// StartPoller launches a background `gt nudge-poller <session>` process.
// The process is detached (Setpgid) so it survives the caller's exit.
// Returns the PID of the launched process, or an error.
func StartPoller(townRoot, session string) (int, error) {
	pidDir := pollerPidDir(townRoot)
	if err := os.MkdirAll(pidDir, 0755); err != nil {
		return 0, fmt.Errorf("creating poller pid dir: %w", err)
	}

	// Serialize check-then-start across processes. Every wait-idle nudge now
	// calls StartPoller, so two senders can both observe the same stale PID
	// file and each spawn a poller. Only the last PID written is tracked, so
	// StopPoller would leave the others running until the session dies.
	//
	// Try, do not block: if another process holds this lock it is starting a
	// poller for this session right now, which is the outcome we wanted. A
	// blocking acquire would hang `gt nudge` behind a stuck starter.
	// (gastown-ku3)
	release, locked, lockErr := lock.FlockTryAcquire(pollerStartLockFile(townRoot, session))
	if lockErr != nil {
		return 0, fmt.Errorf("locking poller start: %w", lockErr)
	}
	if !locked {
		return 0, nil // another process is starting one
	}
	defer release()

	// Check if a poller is already running for this session.
	if pid, alive := pollerAlive(townRoot, session); alive {
		return pid, nil // already running
	}

	// Find the gt binary.
	gtBin, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("finding gt binary: %w", err)
	}

	cmd := spawnPollerCommand(gtBin, townRoot, session)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting nudge-poller: %w", err)
	}

	pid := cmd.Process.Pid

	// Register the child in the index, under the slot lock.
	//
	// This used to be a bare os.WriteFile, which is wrong twice over. It
	// truncates before it writes, so a reader landing in the gap sees an empty
	// file and concludes there is no poller; and it overwrites whoever holds the
	// slot, so a directly invoked `gt nudge-poller` that claimed it during the
	// window opened at the aliveness check above loses its registration to a
	// child that is now the SECOND consumer on one queue. The child then adopts
	// the entry naming it and runs, because the entry names its own pid.
	// ClaimPollerPidFile closes both: the write is a rename, and the read that
	// precedes it happens under the same lock every other claimant takes.
	// (gastown-cb2)
	if err := ClaimPollerPidFile(townRoot, session, pid); err != nil {
		if errors.Is(err, ErrPollerAlreadyRunning) {
			// Somebody registered a live poller while we were spawning, so the
			// child we just started is the duplicate. Stop it here rather than
			// relying on its own claim to turn it away: we started it, we can
			// see it must not run, and killing it now closes the window instead
			// of leaving a second consumer alive for as long as its startup
			// takes. Reaped so it does not linger as a zombie (gt-5kri).
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return 0, nil
		}
		// Any other failure is not fatal: the poller is running and draining, it
		// is merely untracked, which is exactly what this command did before it
		// wrote a pid file at all.
		fmt.Fprintf(os.Stderr, "Warning: failed to write poller PID file: %v\n", err)
	}

	// Release the process so it runs independently.
	_ = cmd.Process.Release()

	return pid, nil
}

// spawnPollerCommand builds the detached poller command. Indirected through a
// var because StartPoller launches os.Executable(), which under `go test` is the
// test binary itself — a test binary re-invoked with stray positional args
// re-runs the whole suite. Tests substitute a harmless child.
var spawnPollerCommand = buildPollerCommand

func buildPollerCommand(gtBin, townRoot, session string) *exec.Cmd {
	cmd := exec.Command(gtBin, "nudge-poller", session)
	cmd.Dir = townRoot
	cmd.Stdout = nil // discard
	cmd.Stderr = nil // discard
	util.SetDetachedProcessGroup(cmd)
	return cmd
}

// pidNobodyOwns stands in for "we could not read an owner out of the slot".
// Process ids start at 1, so it can never collide with a real claimant, which is
// what makes it safe to pass to ReleasePollerPidFile: an unparseable entry gets
// cleared and a successor's valid entry does not.
const pidNobodyOwns = 0

// StopPoller terminates the nudge-poller for a session, if running.
//
// Every exit clears the slot through ReleasePollerPidFile rather than unlinking
// the file directly. StopPoller reads the pid without the lock and then acts on
// it, so a replacement poller can own the slot by the time it gets to the
// cleanup — and removing THAT entry leaves a live poller untracked, which lets
// the next StartPoller put a second consumer on the same queue. The ownership
// check and the unlink have to be one critical section. (gastown-cb2)
func StopPoller(townRoot, session string) error {
	pidPath := pollerPidFile(townRoot, session)

	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no poller to stop
		}
		return fmt.Errorf("reading poller PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return ReleasePollerPidFile(townRoot, session, pidNobodyOwns) // corrupt, clean up
	}

	if !pollerProcessAlive(pid) {
		// Process already dead.
		return ReleasePollerPidFile(townRoot, session, pid)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return ReleasePollerPidFile(townRoot, session, pid)
	}

	// Send SIGTERM for graceful shutdown.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		_ = ReleasePollerPidFile(townRoot, session, pid)
		return fmt.Errorf("sending SIGTERM to poller (pid %d): %w", pid, err)
	}

	return ReleasePollerPidFile(townRoot, session, pid)
}

// pollerAlive checks if a poller is running for the given session.
// Returns the PID and whether the process is alive.
func pollerAlive(townRoot, session string) (int, bool) {
	pidPath := pollerPidFile(townRoot, session)

	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}

	if !pollerProcessAlive(pid) {
		// Stale entry. Clear it under the slot lock and only while it still
		// names the pid we read: this read is unlocked, so a successor may have
		// claimed the slot in between, and deleting a live poller's registration
		// is the untracked-poller hole the index exists to close. StartPoller
		// calls this before spawning, so an unconditional unlink here is one of
		// its writes too. (gastown-cb2)
		if err := ReleasePollerPidFile(townRoot, session, pid); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to clear stale poller PID file: %v\n", err)
		}
		return 0, false
	}

	return pid, true
}

// Watcher provides a filesystem-event-driven interface to the nudge queue.
// This is an ACP-safe alternative to polling and is preferred for long-running
// watchers like ACP Propeller.
type Watcher struct {
	townRoot string
	session  string
	dir      string
	closed   chan struct{}
	wg       sync.WaitGroup
	events   chan struct{}
}

// NewWatcher creates a new watcher for the given town root and session.
// The watcher observes nudge queue writes and signals via the Events() channel.
func NewWatcher(townRoot, session string) (*Watcher, error) {
	dir := queueDir(townRoot, session)
	// Ensure the directory exists so watch can start immediately.
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating nudge queue dir: %w", err)
	}

	w := &Watcher{
		townRoot: townRoot,
		session:  session,
		dir:      dir,
		closed:   make(chan struct{}),
		events:   make(chan struct{}, 1), // buffer one signal for coalescing
	}

	w.wg.Add(1)
	go w.watch()
	return w, nil
}

// Events returns a channel that receives a struct{} when the queue may have
// changed. Multiple changes within a short window are coalesced.
func (w *Watcher) Events() <-chan struct{} {
	return w.events
}

// Close stops the watcher and releases resources.
func (w *Watcher) Close() error {
	select {
	case <-w.closed:
		return fmt.Errorf("watcher already closed")
	default:
	}
	close(w.closed)
	w.wg.Wait()
	return nil
}

func (w *Watcher) watch() {
	defer w.wg.Done()

	// Use fsnotify directly.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Log but don't block; fallback behavior is explicit in callers.
		fmt.Fprintf(os.Stderr, "nudge watcher init failed for %s: %v\n", w.dir, err)
		return
	}
	defer func() { _ = watcher.Close() }()

	// Watch the directory.
	if err := watcher.Add(w.dir); err != nil {
		fmt.Fprintf(os.Stderr, "nudge watcher failed to add dir %s: %v\n", w.dir, err)
		return
	}

	// Coalescing window.
	coalesceTimer := time.NewTicker(100 * time.Millisecond)
	defer coalesceTimer.Stop()

	pending := false
	for {
		select {
		case <-w.closed:
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Only care about file creation/modification in the queue dir
			if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				// Filter: only .json files in the queue directory
				if strings.HasSuffix(event.Name, ".json") && filepath.Dir(event.Name) == w.dir {
					pending = true
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "nudge watcher error: %v\n", err)
		case <-coalesceTimer.C:
			if pending {
				pending = false
				select {
				case w.events <- struct{}{}:
				default:
				}
			}
		}
	}
}

// WatcherForSession returns a Watcher for a specific session or an error if
// creation fails (e.g., filesystem issues). Callers should handle cleanup.
func WatcherForSession(townRoot, session string) (*Watcher, error) {
	return NewWatcher(townRoot, session)
}
