package nudge

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWritePollerPidFileMatchesTheFormatReadersExpect(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "gastown-witness"

	if err := WritePollerPidFile(townRoot, session, 1221266); err != nil {
		t.Fatalf("WritePollerPidFile: %v", err)
	}

	path := filepath.Join(townRoot, ".runtime", "nudge_poller", session+".pid")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	// Bare decimal, no trailing newline — existing files on disk are 7 bytes
	// for a 7-digit pid, and StopPoller/pollerAlive parse that shape.
	if got := string(data); got != "1221266" {
		t.Errorf("pid file contents = %q, want %q", got, "1221266")
	}
	if len(data) != 7 {
		t.Errorf("pid file length = %d, want 7 (a trailing newline would make it 8)", len(data))
	}
}

func TestWritePollerPidFileUsesTheSamePathAsStartPoller(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "gastown/polecats/onyx"

	if err := WritePollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("WritePollerPidFile: %v", err)
	}

	// The slash-flattening is part of the contract; a reimplemented path would
	// write somewhere pollerAlive never looks.
	if _, err := os.Stat(pollerPidFile(townRoot, session)); err != nil {
		t.Fatalf("stat %s: %v", pollerPidFile(townRoot, session), err)
	}
}

func TestWritePollerPidFileIsVisibleToPollerAlive(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "be-witness"

	// The point of the write is that the "already running" guard can see a
	// directly launched poller. Use our own pid, which is genuinely alive.
	if err := WritePollerPidFile(townRoot, session, os.Getpid()); err != nil {
		t.Fatalf("WritePollerPidFile: %v", err)
	}

	pid, alive := pollerAlive(townRoot, session)
	if !alive {
		t.Fatalf("pollerAlive(%q) = false; a directly launched poller is still invisible", session)
	}
	if pid != os.Getpid() {
		t.Errorf("pollerAlive returned pid %d, want %d", pid, os.Getpid())
	}
}

func TestRemovePollerPidFileClearsOurOwnEntry(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "liveop-refinery"

	if err := WritePollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("WritePollerPidFile: %v", err)
	}
	if err := RemovePollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("RemovePollerPidFile: %v", err)
	}

	if _, err := os.Stat(pollerPidFile(townRoot, session)); !os.IsNotExist(err) {
		t.Errorf("pid file still present after removal, stat err = %v", err)
	}
}

func TestRemovePollerPidFileLeavesAnotherPollersEntryAlone(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "hq-deacon"

	// StopPoller removes the file right after SIGTERM, so a replacement poller
	// can own the slot before the dying one runs its own cleanup. Deleting that
	// entry would reopen the very blind spot this file closes.
	if err := WritePollerPidFile(townRoot, session, 5555); err != nil {
		t.Fatalf("WritePollerPidFile: %v", err)
	}
	if err := RemovePollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("RemovePollerPidFile: %v", err)
	}

	data, err := os.ReadFile(pollerPidFile(townRoot, session))
	if err != nil {
		t.Fatalf("successor's pid file was deleted: %v", err)
	}
	if got := string(data); got != strconv.Itoa(5555) {
		t.Errorf("pid file contents = %q, want %q", got, "5555")
	}
}

func TestRemovePollerPidFileToleratesAMissingFile(t *testing.T) {
	t.Parallel()

	// StopPoller getting there first is the normal case, not an error.
	if err := RemovePollerPidFile(t.TempDir(), "never-started", 4242); err != nil {
		t.Errorf("RemovePollerPidFile on a missing file = %v, want nil", err)
	}
}

func TestRemovePollerPidFileClearsAnUnparseableEntry(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "li-witness"

	if err := WritePollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("WritePollerPidFile: %v", err)
	}
	if err := os.WriteFile(pollerPidFile(townRoot, session), []byte("garbage"), 0644); err != nil {
		t.Fatalf("corrupting pid file: %v", err)
	}

	// A corrupt entry names nobody, so nobody is harmed by clearing it — and
	// leaving it would keep pollerAlive answering from an unreadable file.
	if err := RemovePollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("RemovePollerPidFile: %v", err)
	}
	if _, err := os.Stat(pollerPidFile(townRoot, session)); !os.IsNotExist(err) {
		t.Errorf("corrupt pid file still present, stat err = %v", err)
	}
}
