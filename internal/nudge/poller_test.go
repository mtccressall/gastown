package nudge

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/steveyegge/gastown/internal/lock"
	"github.com/steveyegge/gastown/internal/util"
)

func TestPollerPidFile(t *testing.T) {
	townRoot := t.TempDir()
	session := "gt-gastown-crew-bear"

	pidFile := pollerPidFile(townRoot, session)
	expected := filepath.Join(townRoot, ".runtime", "nudge_poller", session+".pid")
	if pidFile != expected {
		t.Errorf("pollerPidFile() = %q, want %q", pidFile, expected)
	}
}

func TestPollerPidFile_SlashSanitized(t *testing.T) {
	townRoot := t.TempDir()
	session := "some/session"

	pidFile := pollerPidFile(townRoot, session)
	// Slashes should be replaced with underscores
	expected := filepath.Join(townRoot, ".runtime", "nudge_poller", "some_session.pid")
	if pidFile != expected {
		t.Errorf("pollerPidFile() = %q, want %q", pidFile, expected)
	}
}

func TestPollerAlive_NoPidFile(t *testing.T) {
	townRoot := t.TempDir()
	_, alive := pollerAlive(townRoot, "nonexistent-session")
	if alive {
		t.Error("pollerAlive() returned true for nonexistent PID file")
	}
}

func TestPollerAlive_StalePid(t *testing.T) {
	townRoot := t.TempDir()
	session := "gt-gastown-crew-test"

	// Write a PID file with an invalid PID (process doesn't exist).
	pidDir := pollerPidDir(townRoot)
	if err := os.MkdirAll(pidDir, 0755); err != nil {
		t.Fatal(err)
	}
	pidPath := pollerPidFile(townRoot, session)
	// Use a very high PID that's almost certainly not running.
	if err := os.WriteFile(pidPath, []byte("999999999"), 0644); err != nil {
		t.Fatal(err)
	}

	_, alive := pollerAlive(townRoot, session)
	if alive {
		t.Error("pollerAlive() returned true for dead PID")
	}

	// Stale PID file should be cleaned up.
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("stale PID file was not cleaned up")
	}
}

func TestPollerAlive_CorruptPidFile(t *testing.T) {
	townRoot := t.TempDir()
	session := "gt-gastown-crew-test"

	pidDir := pollerPidDir(townRoot)
	if err := os.MkdirAll(pidDir, 0755); err != nil {
		t.Fatal(err)
	}
	pidPath := pollerPidFile(townRoot, session)
	if err := os.WriteFile(pidPath, []byte("not-a-number"), 0644); err != nil {
		t.Fatal(err)
	}

	_, alive := pollerAlive(townRoot, session)
	if alive {
		t.Error("pollerAlive() returned true for corrupt PID file")
	}
}

func TestStopPoller_NoPidFile(t *testing.T) {
	townRoot := t.TempDir()
	// Should be a no-op, no error.
	if err := StopPoller(townRoot, "nonexistent"); err != nil {
		t.Errorf("StopPoller() unexpected error: %v", err)
	}
}

func TestStopPoller_StalePid(t *testing.T) {
	townRoot := t.TempDir()
	session := "gt-gastown-crew-test"

	// Write a stale PID file.
	pidDir := pollerPidDir(townRoot)
	if err := os.MkdirAll(pidDir, 0755); err != nil {
		t.Fatal(err)
	}
	pidPath := pollerPidFile(townRoot, session)
	if err := os.WriteFile(pidPath, []byte("999999999"), 0644); err != nil {
		t.Fatal(err)
	}

	// Should succeed and clean up the stale PID file.
	if err := StopPoller(townRoot, session); err != nil {
		t.Errorf("StopPoller() unexpected error: %v", err)
	}

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("StopPoller did not clean up stale PID file")
	}
}

func TestPollerAlive_LiveProcess(t *testing.T) {
	townRoot := t.TempDir()
	session := "gt-gastown-crew-test"

	// Write our own PID — we're definitely alive.
	pidDir := pollerPidDir(townRoot)
	if err := os.MkdirAll(pidDir, 0755); err != nil {
		t.Fatal(err)
	}
	pidPath := pollerPidFile(townRoot, session)
	myPid := os.Getpid()
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(myPid)), 0644); err != nil {
		t.Fatal(err)
	}

	pid, alive := pollerAlive(townRoot, session)
	if !alive {
		t.Error("pollerAlive() returned false for live process")
	}
	if pid != myPid {
		t.Errorf("pollerAlive() pid = %d, want %d", pid, myPid)
	}
}

func TestBuildPollerCommand_UsesDetachedProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group management is not supported on Windows")
	}
	townRoot := t.TempDir()
	cmd := buildPollerCommand("/tmp/fake-gt", townRoot, "gt-gastown-crew-bear")

	if got, want := cmd.Dir, townRoot; got != want {
		t.Fatalf("cmd.Dir = %q, want %q", got, want)
	}
	if got, want := cmd.Path, "/tmp/fake-gt"; got != want {
		t.Fatalf("cmd.Path = %q, want %q", got, want)
	}
	if len(cmd.Args) != 3 || cmd.Args[1] != "nudge-poller" || cmd.Args[2] != "gt-gastown-crew-bear" {
		t.Fatalf("cmd.Args = %#v, want poller invocation", cmd.Args)
	}
	if cmd.Cancel != nil {
		t.Fatal("buildPollerCommand() installed cmd.Cancel; detached pollers must leave it nil")
	}
	if cmd.Stdout != nil || cmd.Stderr != nil {
		t.Fatal("buildPollerCommand() should discard stdout/stderr")
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("buildPollerCommand() did not configure SysProcAttr")
	}
}

func TestSetProcessGroup_InstallsCancelHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SetProcessGroup is a no-op on Windows")
	}
	cmd := exec.Command("true")
	util.SetProcessGroup(cmd)

	if cmd.Cancel == nil {
		t.Fatal("SetProcessGroup() should install a cancel hook")
	}
}

func TestPollerStartLockFile(t *testing.T) {
	townRoot := t.TempDir()
	got := pollerStartLockFile(townRoot, "some/session")
	want := filepath.Join(townRoot, ".runtime", "nudge_poller", "some_session.start.lock")
	if got != want {
		t.Errorf("pollerStartLockFile() = %q, want %q", got, want)
	}
}

// TestStartPoller_SkipsWhenAnotherStarterHoldsLock covers the concurrent
// recovery case: two senders both see the same stale PID file and both try to
// start a poller. Without the lock each spawns one, and StopPoller can only
// ever track the last PID written — the rest survive until the session dies.
// The loser of the race must return without spawning.
func TestStartPoller_SkipsWhenAnotherStarterHoldsLock(t *testing.T) {
	townRoot := t.TempDir()
	session := "gt-liveop-refinery"

	pidDir := pollerPidDir(townRoot)
	if err := os.MkdirAll(pidDir, 0755); err != nil {
		t.Fatal(err)
	}
	pidPath := pollerPidFile(townRoot, session)
	if err := os.WriteFile(pidPath, []byte("999999999"), 0644); err != nil {
		t.Fatal(err)
	}

	// Stand in for the sender that got there first and is mid-start.
	release, locked, err := lock.FlockTryAcquire(pollerStartLockFile(townRoot, session))
	if err != nil {
		t.Fatalf("FlockTryAcquire: %v", err)
	}
	if !locked {
		t.Fatal("could not acquire the start lock on a fresh temp dir")
	}
	defer release()

	pid, err := StartPoller(townRoot, session)
	if err != nil {
		t.Errorf("StartPoller() error = %v, want nil (contended start is not a failure)", err)
	}
	if pid != 0 {
		t.Errorf("StartPoller() pid = %d, want 0 (should not spawn while another starter holds the lock)", pid)
	}

	// The stale PID file must be untouched: StartPoller returned before it
	// reached the aliveness check, so nothing was spawned or recorded.
	data, readErr := os.ReadFile(pidPath)
	if readErr != nil {
		t.Fatalf("reading pid file: %v", readErr)
	}
	if string(data) != "999999999" {
		t.Errorf("pid file = %q, want %q (a second poller was started)", string(data), "999999999")
	}
}

// Positive control for the test above: the lock is what makes StartPoller
// return early, not some unrelated short-circuit. Deliberately does NOT let
// StartPoller spawn — it launches os.Executable(), which under `go test` is
// the test binary itself, and a test binary re-invoked with stray positional
// args re-runs the whole suite.
func TestStartPollerLock_IsTheDiscriminator(t *testing.T) {
	townRoot := t.TempDir()
	session := "gt-liveop-refinery"
	lockPath := pollerStartLockFile(townRoot, session)

	if err := os.MkdirAll(pollerPidDir(townRoot), 0755); err != nil {
		t.Fatal(err)
	}

	// Held: a second acquirer must be turned away — this is the state
	// TestStartPoller_SkipsWhenAnotherStarterHoldsLock runs StartPoller in.
	release, locked, err := lock.FlockTryAcquire(lockPath)
	if err != nil || !locked {
		t.Fatalf("FlockTryAcquire on a fresh lock: locked=%v err=%v", locked, err)
	}
	_, contended, err := lock.FlockTryAcquire(lockPath)
	if err != nil {
		t.Fatalf("contended FlockTryAcquire: %v", err)
	}
	if contended {
		t.Fatal("acquired a held lock; the contention test above proves nothing")
	}

	// Released: the same call now succeeds, so the early return in the test
	// above was caused by contention and not by a permanently stuck lock.
	release()
	release2, free, err := lock.FlockTryAcquire(lockPath)
	if err != nil {
		t.Fatalf("post-release FlockTryAcquire: %v", err)
	}
	if !free {
		t.Fatal("lock still held after release")
	}
	release2()
}
