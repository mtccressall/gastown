package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// runIn executes a git command for test setup, failing the test on error.
func runIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRemoteFixture builds a bare "remote" plus a clone that has pushed a
// `feature` branch to it, and returns the clone path, the bare path, and a
// function that advances `feature` on the remote WITHOUT going through the
// clone's Git wrapper.
//
// Moving the ref behind the wrapper's back is the whole instrument: a memoized
// read keeps returning the old sha, a live read returns the new one. Nothing
// about the assertion depends on counting subprocesses, which cannot be
// observed from here.
func newRemoteFixture(t *testing.T) (clone string, advance func() string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	runIn(t, root, "init", "--bare", "-b", "main", bare)

	clone = filepath.Join(root, "clone")
	runIn(t, root, "clone", bare, clone)
	if err := os.WriteFile(filepath.Join(clone, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, clone, "add", "f.txt")
	runIn(t, clone, "commit", "-m", "one")
	runIn(t, clone, "branch", "feature")
	runIn(t, clone, "push", "origin", "main", "feature")

	n := 0
	advance = func() string {
		n++
		// Build the new commit inside the bare repo itself so the clone's
		// wrapper never runs a command that could drop the memo.
		tree := runIn(t, bare, "rev-parse", "feature^{tree}")
		parent := runIn(t, bare, "rev-parse", "feature")
		sha := runIn(t, bare, "commit-tree", tree, "-p", parent, "-m", "advance")
		runIn(t, bare, "update-ref", "refs/heads/feature", sha)
		return sha
	}
	return clone, advance
}

// TestRemoteRefCacheIsOffByDefault is the negative control. Without it the
// positive test below cannot distinguish "the memo works" from "this fixture
// never changes the ref".
func TestRemoteRefCacheIsOffByDefault(t *testing.T) {
	clone, advance := newRemoteFixture(t)
	g := NewGit(clone)

	if remoteRefCacheActive() {
		t.Fatal("a memo window is open before any test asked for one")
	}

	before, err := g.RemoteBranchTip("origin", "feature")
	if err != nil {
		t.Fatalf("RemoteBranchTip: %v", err)
	}
	want := advance()

	after, err := g.RemoteBranchTip("origin", "feature")
	if err != nil {
		t.Fatalf("RemoteBranchTip after advance: %v", err)
	}
	if after == before {
		t.Fatalf("read is stale with no memo window open: got %s both times", before)
	}
	if after != want {
		t.Fatalf("read did not see the advanced ref: got %s want %s", after, want)
	}
}

// TestRemoteRefCacheServesOneAnswerPerWindow is the property gastown-o8q needs:
// inside one dispatch pass the same ls-remote question costs one round-trip.
func TestRemoteRefCacheServesOneAnswerPerWindow(t *testing.T) {
	clone, advance := newRemoteFixture(t)
	g := NewGit(clone)

	BeginRemoteRefCache()
	first, err := g.RemoteBranchTip("origin", "feature")
	if err != nil {
		EndRemoteRefCache()
		t.Fatalf("RemoteBranchTip: %v", err)
	}
	moved := advance()
	if moved == first {
		EndRemoteRefCache()
		t.Fatal("fixture did not actually advance the ref")
	}

	second, err := g.RemoteBranchTip("origin", "feature")
	if err != nil {
		EndRemoteRefCache()
		t.Fatalf("RemoteBranchTip (memoized): %v", err)
	}
	if second != first {
		EndRemoteRefCache()
		t.Fatalf("memo window did not reuse the answer: %s then %s", first, second)
	}

	// RemoteBranchExists asks the same ls-remote question through a different
	// exported method. It must share the memo, or the pass still pays twice.
	exists, err := g.RemoteBranchExists("origin", "feature")
	if err != nil {
		EndRemoteRefCache()
		t.Fatalf("RemoteBranchExists: %v", err)
	}
	if !exists {
		EndRemoteRefCache()
		t.Fatal("RemoteBranchExists disagreed with RemoteBranchTip inside one window")
	}

	EndRemoteRefCache()

	if remoteRefCacheActive() {
		t.Fatal("window still open after End")
	}
	third, err := g.RemoteBranchTip("origin", "feature")
	if err != nil {
		t.Fatalf("RemoteBranchTip after window: %v", err)
	}
	if third != moved {
		t.Fatalf("memo outlived its window: got %s want %s", third, moved)
	}
}

// TestRemoteRefCacheNestsWithoutClosingEarly pins the refcount. A dispatch pass
// opens a window and the capacity snapshot inside it opens its own; if the
// inner End closed the shared window the outer half of the pass would silently
// lose its memo, which is a performance regression with no failing symptom.
func TestRemoteRefCacheNestsWithoutClosingEarly(t *testing.T) {
	BeginRemoteRefCache()
	BeginRemoteRefCache()
	EndRemoteRefCache()
	if !remoteRefCacheActive() {
		t.Fatal("inner End closed the outer window")
	}
	EndRemoteRefCache()
	if remoteRefCacheActive() {
		t.Fatal("outer End left the window open")
	}
}

// TestRemoteRefCacheDroppedByRemoteMutation establishes the guard that keeps the
// memo honest. The window "must not span an operation that changes the remote"
// is enforced in code rather than only written in a comment, because a rule that
// lives only in a comment is one refactor away from not existing.
func TestRemoteRefCacheDroppedByRemoteMutation(t *testing.T) {
	clone, advance := newRemoteFixture(t)
	g := NewGit(clone)

	BeginRemoteRefCache()
	defer EndRemoteRefCache()

	first, err := g.RemoteBranchTip("origin", "feature")
	if err != nil {
		t.Fatalf("RemoteBranchTip: %v", err)
	}
	moved := advance()

	if err := g.Fetch("origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	after, err := g.RemoteBranchTip("origin", "feature")
	if err != nil {
		t.Fatalf("RemoteBranchTip after fetch: %v", err)
	}
	if after == first {
		t.Fatalf("fetch did not drop the memo: still serving %s", first)
	}
	if after != moved {
		t.Fatalf("post-fetch read wrong: got %s want %s", after, moved)
	}
}

// TestRemoteRefCacheCollapsesConcurrentReads matters because the capacity walk
// is now parallel: without single-flight, eight goroutines asking one question
// at once would each pay the round-trip and the memo would save nothing on the
// path it was built for.
//
// The assertion is the memo's CARDINALITY, not a race between readers and a
// moving ref. A racing version of this test was written first and sabotage-run:
// with the memo disabled it failed on one run in five, because whether the two
// answers differ depends on when the advance lands. A test that discriminates
// one time in five is not evidence when it passes.
func TestRemoteRefCacheCollapsesConcurrentReads(t *testing.T) {
	clone, _ := newRemoteFixture(t)
	g := NewGit(clone)

	BeginRemoteRefCache()
	defer EndRemoteRefCache()

	const readers = 8
	results := make([]string, readers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			sha, err := g.RemoteBranchTip("origin", "feature")
			if err != nil {
				t.Errorf("reader %d: %v", i, err)
				return
			}
			results[i] = sha
		}(i)
	}
	close(start)
	wg.Wait()

	for i, got := range results {
		if got != results[0] {
			t.Fatalf("reader %d saw %s, reader 0 saw %s: concurrent readers of one "+
				"memo window disagreed", i, got, results[0])
		}
	}

	remoteRefCacheMu.Lock()
	entries := len(remoteRefCacheEntries)
	remoteRefCacheMu.Unlock()
	if entries != 1 {
		t.Fatalf("%d readers of one question produced %d memo entries, want 1: "+
			"concurrent callers are not being collapsed onto a single round-trip",
			readers, entries)
	}
}
