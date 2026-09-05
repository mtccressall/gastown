package nudge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClaimPollerPidFileMatchesTheFormatReadersExpect(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "gastown-witness"

	if err := ClaimPollerPidFile(townRoot, session, 1221266); err != nil {
		t.Fatalf("ClaimPollerPidFile: %v", err)
	}

	path := filepath.Join(townRoot, ".runtime", "nudge_poller", session+".pid")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	// Bare decimal, no trailing newline — existing files on disk are 7 bytes for
	// a 7-digit pid, and StopPoller/pollerAlive parse that shape.
	if got := string(data); got != "1221266" {
		t.Errorf("pid file contents = %q, want %q", got, "1221266")
	}
	if len(data) != 7 {
		t.Errorf("pid file length = %d, want 7 (a trailing newline would make it 8)", len(data))
	}
}

func TestClaimPollerPidFileUsesTheSamePathAsStartPoller(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "gastown/polecats/onyx"

	if err := ClaimPollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("ClaimPollerPidFile: %v", err)
	}

	// The slash-flattening is part of the contract; a reimplemented path would
	// write somewhere pollerAlive never looks.
	if _, err := os.Stat(pollerPidFile(townRoot, session)); err != nil {
		t.Fatalf("stat %s: %v", pollerPidFile(townRoot, session), err)
	}
}

func TestClaimPollerPidFileIsVisibleToPollerAlive(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "be-witness"

	// The point of the claim is that the "already running" guard can see a
	// directly launched poller. Use our own pid, which is genuinely alive.
	if err := ClaimPollerPidFile(townRoot, session, os.Getpid()); err != nil {
		t.Fatalf("ClaimPollerPidFile: %v", err)
	}

	pid, alive := pollerAlive(townRoot, session)
	if !alive {
		t.Fatalf("pollerAlive(%q) = false; a directly launched poller is still invisible", session)
	}
	if pid != os.Getpid() {
		t.Errorf("pollerAlive returned pid %d, want %d", pid, os.Getpid())
	}
}

func TestClaimPollerPidFileRefusesToDisplaceALivePoller(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "liveop-refinery"

	// A live poller holds the slot. Our own pid is the one process we can be
	// certain is alive.
	if err := ClaimPollerPidFile(townRoot, session, os.Getpid()); err != nil {
		t.Fatalf("ClaimPollerPidFile: %v", err)
	}

	// Overwriting would make the second invocation a second consumer on one
	// queue, and its exit would then erase the only record of either.
	err := ClaimPollerPidFile(townRoot, session, 4242)
	if !errors.Is(err, ErrPollerAlreadyRunning) {
		t.Fatalf("second claim err = %v, want ErrPollerAlreadyRunning", err)
	}

	data, readErr := os.ReadFile(pollerPidFile(townRoot, session))
	if readErr != nil {
		t.Fatalf("reading pid file: %v", readErr)
	}
	if got := string(data); got != strconv.Itoa(os.Getpid()) {
		t.Errorf("pid file contents = %q, want the incumbent %d", got, os.Getpid())
	}
}

func TestClaimPollerPidFileAdoptsAnEntryStartPollerAlreadyWrote(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "li-witness"

	// StartPoller writes the child's pid after spawning it, so the child can
	// arrive to find itself already registered. That is not a conflict.
	if err := os.MkdirAll(pollerPidDir(townRoot), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pollerPidFile(townRoot, session), []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatalf("writing incumbent pid file: %v", err)
	}

	if err := ClaimPollerPidFile(townRoot, session, os.Getpid()); err != nil {
		t.Fatalf("ClaimPollerPidFile on our own entry = %v, want nil", err)
	}
}

func TestClaimPollerPidFileTakesOverAZombiesSlot(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "hq-deacon"

	// This is the whole point of the zombie fix one file over: a corpse that
	// Signal(0) still answers for must not hold the slot against a replacement.
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}
	corpse := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Wait() })

	deadline := time.Now().Add(10 * time.Second)
	for pollerProcessAlive(corpse) {
		if time.Now().After(deadline) {
			t.Skipf("child pid %d never stopped reading as alive", corpse)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := os.MkdirAll(pollerPidDir(townRoot), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pollerPidFile(townRoot, session), []byte(strconv.Itoa(corpse)), 0644); err != nil {
		t.Fatalf("writing corpse pid file: %v", err)
	}

	if err := ClaimPollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("ClaimPollerPidFile over a corpse = %v, want nil", err)
	}
	data, err := os.ReadFile(pollerPidFile(townRoot, session))
	if err != nil {
		t.Fatalf("reading pid file: %v", err)
	}
	if got := string(data); got != "4242" {
		t.Errorf("pid file contents = %q, want %q", got, "4242")
	}
}

func TestClaimPollerPidFileClearsAnUnparseableEntry(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "li-refinery"

	if err := os.MkdirAll(pollerPidDir(townRoot), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pollerPidFile(townRoot, session), []byte("garbage"), 0644); err != nil {
		t.Fatalf("writing corrupt pid file: %v", err)
	}

	// Bytes nobody can parse name no live poller, so they hold the slot against
	// nothing.
	if err := ClaimPollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("ClaimPollerPidFile over a corrupt entry = %v, want nil", err)
	}
	data, err := os.ReadFile(pollerPidFile(townRoot, session))
	if err != nil {
		t.Fatalf("reading pid file: %v", err)
	}
	if got := string(data); got != "4242" {
		t.Errorf("pid file contents = %q, want %q", got, "4242")
	}
}

// Every reader of this index reads it unlocked. An in-place write truncates
// first, so a reader landing in that gap sees an empty file, fails to parse a
// pid, concludes there is no poller, and lets StartPoller spawn a duplicate. The
// content must be complete before the name appears.
func TestClaimPollerPidFileIsAtomicForConcurrentReaders(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "gastown-refinery"
	path := pollerPidFile(townRoot, session)

	done := make(chan struct{})
	bad := make(chan string, 1)

	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			pid := 2000000 + i
			if err := ClaimPollerPidFile(townRoot, session, pid); err != nil {
				bad <- fmt.Sprintf("ClaimPollerPidFile: %v", err)
				return
			}
			if err := ReleasePollerPidFile(townRoot, session, pid); err != nil {
				bad <- fmt.Sprintf("ReleasePollerPidFile: %v", err)
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			select {
			case msg := <-bad:
				t.Fatal(msg)
			default:
			}
			return
		default:
		}

		data, err := os.ReadFile(path)
		if err != nil {
			continue // between a release and the next claim; that is a real state
		}
		if _, err := strconv.Atoi(string(data)); err != nil {
			t.Fatalf("reader observed a non-pid %q mid-claim; the claim is not atomic", string(data))
		}
	}
}

// Two claimants inspecting the slot at the same moment must not both conclude it
// is theirs. Without the slot lock they can: each reads the same stale entry,
// each decides to take it, and the second overwrites the first — two pollers on
// one queue, which is the defect this file exists to prevent.
func TestConcurrentClaimsProduceExactlyOneWinner(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "hq-mayor"
	const claimants = 8

	// Real, live processes: the refusal turns on pollerProcessAlive, so fake pids
	// would all read as dead and every claimant would legitimately take over.
	pids := make([]int, 0, claimants)
	for i := 0; i < claimants; i++ {
		cmd := exec.Command("/bin/sh", "-c", "sleep 30")
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting child %d: %v", i, err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		pids = append(pids, cmd.Process.Pid)
	}

	// A stale entry is the contended starting state codex named: everyone sees
	// it, everyone is entitled to take it, only one may.
	if err := os.MkdirAll(pollerPidDir(townRoot), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pollerPidFile(townRoot, session), []byte("999999999"), 0644); err != nil {
		t.Fatalf("writing stale pid file: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, claimants)
	for _, pid := range pids {
		go func(pid int) {
			<-start
			results <- ClaimPollerPidFile(townRoot, session, pid)
		}(pid)
	}
	close(start)

	var won, refused int
	for i := 0; i < claimants; i++ {
		switch err := <-results; {
		case err == nil:
			won++
		case errors.Is(err, ErrPollerAlreadyRunning):
			refused++
		default:
			t.Errorf("unexpected claim error: %v", err)
		}
	}

	if won != 1 {
		t.Errorf("%d claimants won the slot, want exactly 1 (%d refused)", won, refused)
	}

	// And the slot must name one of them, not a corpse or a half-written file.
	data, err := os.ReadFile(pollerPidFile(townRoot, session))
	if err != nil {
		t.Fatalf("reading pid file: %v", err)
	}
	winner, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("pid file holds %q, which is not a pid", string(data))
	}
	if !slices.Contains(pids, winner) {
		t.Errorf("pid file names %d, which is not one of the claimants %v", winner, pids)
	}
}

func TestReleasePollerPidFileClearsOurOwnEntry(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "liveop-witness"

	if err := ClaimPollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("ClaimPollerPidFile: %v", err)
	}
	if err := ReleasePollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("ReleasePollerPidFile: %v", err)
	}

	if _, err := os.Stat(pollerPidFile(townRoot, session)); !os.IsNotExist(err) {
		t.Errorf("pid file still present after release, stat err = %v", err)
	}
}

func TestReleasePollerPidFileLeavesASuccessorsEntryAlone(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "hq-boot"

	// StopPoller removes the file right after SIGTERM, so a replacement can own
	// the slot before the dying poller runs its cleanup. Deleting that entry
	// would leave a live poller untracked and let the next StartPoller spawn a
	// duplicate — the exact blind spot this file closes.
	if err := ClaimPollerPidFile(townRoot, session, 5555); err != nil {
		t.Fatalf("ClaimPollerPidFile: %v", err)
	}
	if err := ReleasePollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("ReleasePollerPidFile: %v", err)
	}

	data, err := os.ReadFile(pollerPidFile(townRoot, session))
	if err != nil {
		t.Fatalf("the successor's pid file was deleted: %v", err)
	}
	if got := string(data); got != "5555" {
		t.Errorf("pid file contents = %q, want %q", got, "5555")
	}
}

func TestReleasePollerPidFileToleratesAMissingFile(t *testing.T) {
	t.Parallel()

	// StopPoller getting there first is the normal case, not an error.
	if err := ReleasePollerPidFile(t.TempDir(), "never-started", 4242); err != nil {
		t.Errorf("ReleasePollerPidFile on a missing file = %v, want nil", err)
	}
}

func TestReleasePollerPidFileClearsAnUnparseableEntry(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "be-refinery"

	if err := ClaimPollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("ClaimPollerPidFile: %v", err)
	}
	if err := os.WriteFile(pollerPidFile(townRoot, session), []byte("garbage"), 0644); err != nil {
		t.Fatalf("corrupting pid file: %v", err)
	}

	// A corrupt entry names nobody, so nobody is harmed by clearing it — and
	// leaving it would keep pollerAlive answering from an unreadable file.
	if err := ReleasePollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("ReleasePollerPidFile: %v", err)
	}
	if _, err := os.Stat(pollerPidFile(townRoot, session)); !os.IsNotExist(err) {
		t.Errorf("corrupt pid file still present, stat err = %v", err)
	}
}

// The temp files these operations use must not pile up in the index directory —
// a census that lists it would count them as pollers. The sidecar lock is the one
// permitted extra, and it carries a suffix a *.pid census does not match.
func TestPidFileOperationsLeaveNoDebris(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "gastown/polecats/quartz"

	for i := 0; i < 5; i++ {
		if err := ClaimPollerPidFile(townRoot, session, 4242+i); err != nil {
			t.Fatalf("ClaimPollerPidFile: %v", err)
		}
		if err := ReleasePollerPidFile(townRoot, session, 4242+i); err != nil {
			t.Fatalf("ReleasePollerPidFile: %v", err)
		}
	}
	if err := ClaimPollerPidFile(townRoot, session, 9999); err != nil {
		t.Fatalf("ClaimPollerPidFile: %v", err)
	}

	entries, err := os.ReadDir(pollerPidDir(townRoot))
	if err != nil {
		t.Fatalf("reading pid dir: %v", err)
	}

	var pidFiles, other []string
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".pid"):
			pidFiles = append(pidFiles, e.Name())
		case strings.HasSuffix(e.Name(), ".pid.lock"):
			// The lock file is permanent by design: unlinking a locked file lets
			// the next process lock a different inode under the same name.
		default:
			other = append(other, e.Name())
		}
	}
	if len(pidFiles) != 1 {
		t.Errorf("pid dir holds %d pid files %v, want exactly 1", len(pidFiles), pidFiles)
	}
	if len(other) != 0 {
		t.Errorf("pid dir holds unexpected debris %v", other)
	}
}

func TestClaimPollerPidFileIsWorldReadable(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "steward-witness"

	if err := ClaimPollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("ClaimPollerPidFile: %v", err)
	}

	// os.CreateTemp makes files 0600; StartPoller's own writes are 0644 and other
	// agents' censuses read this directory.
	info, err := os.Stat(pollerPidFile(townRoot, session))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Errorf("pid file mode = %o, want 0644", got)
	}
}
