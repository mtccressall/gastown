package nudge

import (
	"fmt"
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

// Every reader of this index reads it unlocked. os.WriteFile truncates before it
// writes, so a concurrent pollerAlive could read an empty file, fail to parse a
// pid, report no poller and let StartPoller spawn a duplicate. The write must be
// a rename, so a reader sees either the old pid or the new one and never a
// half-written file.
func TestWritePollerPidFileIsAtomicForConcurrentReaders(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "gastown-witness"
	path := pollerPidFile(townRoot, session)

	if err := WritePollerPidFile(townRoot, session, 1111111); err != nil {
		t.Fatalf("WritePollerPidFile: %v", err)
	}

	done := make(chan struct{})
	bad := make(chan string, 1)

	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			pid := 2000000 + i
			if err := WritePollerPidFile(townRoot, session, pid); err != nil {
				bad <- fmt.Sprintf("WritePollerPidFile: %v", err)
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
			// A rename never unlinks the destination, so the path is always there.
			t.Fatalf("reading %s during concurrent writes: %v", path, err)
		}
		if _, err := strconv.Atoi(string(data)); err != nil {
			t.Fatalf("reader observed a non-pid %q mid-write; the write is not atomic", string(data))
		}
	}
}

// The temp files the atomic write uses must not pile up in the index directory —
// a census that lists the directory would count them as pollers.
func TestWritePollerPidFileLeavesNoTempFiles(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "li-refinery"

	for i := 0; i < 5; i++ {
		if err := WritePollerPidFile(townRoot, session, 4242+i); err != nil {
			t.Fatalf("WritePollerPidFile: %v", err)
		}
	}

	entries, err := os.ReadDir(pollerPidDir(townRoot))
	if err != nil {
		t.Fatalf("reading pid dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("pid dir holds %d entries %v, want exactly 1", len(entries), names)
	}
}

func TestWritePollerPidFileIsWorldReadable(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	const session = "hq-boot"

	if err := WritePollerPidFile(townRoot, session, 4242); err != nil {
		t.Fatalf("WritePollerPidFile: %v", err)
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
