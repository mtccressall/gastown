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

// TestRemoteRefCacheDroppedByPushWithEnv is gastown-vvq's behavioural half, and
// it drives the PRODUCTION entry point rather than the exec path underneath it.
//
// That distinction is the whole test. Calling runWithEnvAndTimeout directly with
// a hand-built {"push", ...} would exercise the guard and prove nothing about
// whether any real caller reaches it -- a fixture constructed by the test tells
// you about the fixture. PushWithEnv is the method integration land actually
// calls (internal/cmd/mq_integration.go), and it is the reason the gap was
// reachable at all.
//
// The assertion is that a push through PushWithEnv inside an open memo window
// makes the next RemoteBranchTip live. Before the fix it served the pre-push
// sha, with no error anywhere: the push succeeded, the read succeeded, and the
// answer was silently one commit behind -- a failure that reads as a stale
// remote rather than as a cache bug.
func TestRemoteRefCacheDroppedByPushWithEnv(t *testing.T) {
	clone, _ := newRemoteFixture(t)
	g := NewGit(clone)

	// The bare repo, asked for by git rather than rebuilt from path arithmetic.
	// It is the independent witness that the push actually landed, so a push
	// that silently did nothing cannot masquerade as a memo that was dropped.
	bare := runIn(t, clone, "config", "--get", "remote.origin.url")

	BeginRemoteRefCache()
	defer EndRemoteRefCache()

	before, err := g.RemoteBranchTip("origin", "feature")
	if err != nil {
		t.Fatalf("RemoteBranchTip: %v", err)
	}

	// Build the new commit with raw git so the setup itself cannot drop the memo
	// -- otherwise the test passes for a reason that has nothing to do with the
	// push under test.
	runIn(t, clone, "checkout", "feature")
	if err := os.WriteFile(filepath.Join(clone, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, clone, "add", "f.txt")
	runIn(t, clone, "commit", "-m", "two")
	want := runIn(t, clone, "rev-parse", "feature")
	if want == before {
		t.Fatal("fixture did not create a new commit")
	}

	if err := g.PushWithEnv("origin", "feature", false, []string{"GIT_TERMINAL_PROMPT=0"}); err != nil {
		t.Fatalf("PushWithEnv: %v", err)
	}
	if landed := runIn(t, bare, "rev-parse", "refs/heads/feature"); landed != want {
		t.Fatalf("push did not land on the remote: remote at %s, want %s", landed, want)
	}

	after, err := g.RemoteBranchTip("origin", "feature")
	if err != nil {
		t.Fatalf("RemoteBranchTip after push: %v", err)
	}
	if after == before {
		t.Fatalf("PushWithEnv did not drop the memo: still serving the pre-push sha %s "+
			"while the remote is at %s (gastown-vvq)", before, want)
	}
	if after != want {
		t.Fatalf("post-push read wrong: got %s want %s", after, want)
	}
}

// TestRemoteRefCacheGuardSeesSubcommandBehindGlobalFlags pins the parser the
// guard uses. It drives preExec rather than the predicate underneath it, so it
// also covers the wiring: a correct predicate that preExec did not consult would
// pass a direct test of the predicate and fail this one. `git -C /repo push origin main` has "/repo" as its first non-flag
// token, so a first-non-flag-token rule reads the subcommand as "/repo" and
// leaves a stale memo standing across a real push; the same holds for every
// value-carrying global flag. gitSubcommand -- the parser
// guardUnsafeTownRootMutation already used -- skips the value.
//
// Both directions are covered deliberately. The "-C push" row is the one a
// first-token parser gets wrong the OTHER way: a directory that happens to be
// named "push" is not a push, and dropping the memo there is a silent
// performance regression with no failing symptom.
func TestRemoteRefCacheGuardSeesSubcommandBehindGlobalFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		drop bool
	}{
		{"bare push", []string{"push", "origin", "main"}, true},
		{"bare fetch", []string{"fetch", "origin"}, true},
		{"-C then push", []string{"-C", "/repo", "push", "origin", "main"}, true},
		{"-c then push", []string{"-c", "user.name=t", "push", "origin", "main"}, true},
		{"--git-dir= then fetch", []string{"--git-dir=/r/.git", "fetch", "origin"}, true},
		{"--work-tree then remote", []string{"--work-tree", "/r", "remote", "prune", "origin"}, true},
		{"read-only rev-parse", []string{"rev-parse", "HEAD"}, false},
		{"-C then status", []string{"-C", "/repo", "status"}, false},
		{"a directory named push", []string{"-C", "push", "status"}, false},
		{"no subcommand at all", []string{"--version"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			BeginRemoteRefCache()
			defer EndRemoteRefCache()

			remoteRefCacheMu.Lock()
			remoteRefCacheEntries["seed"] = &remoteRefEntry{}
			remoteRefCacheMu.Unlock()

			g := NewGit(t.TempDir())
			done, err := g.preExec(tc.args)
			if err != nil {
				t.Fatalf("preExec: %v", err)
			}
			defer done()

			remoteRefCacheMu.Lock()
			dropped := len(remoteRefCacheEntries) == 0
			remoteRefCacheMu.Unlock()

			if dropped != tc.drop {
				verb := map[bool]string{true: "dropped", false: "kept"}
				t.Fatalf("git %s: memo %s, want %s", strings.Join(tc.args, " "),
					verb[dropped], verb[tc.drop])
			}
		})
	}
}

// TestRemoteRefCacheDropIsClosedOnBothSidesOfAMutation is the regression test
// for the race codex found in the first cut of gastown-vvq's fix: dropping the
// memo only BEFORE the subprocess is not enough.
//
// Once the capacity walk runs in parallel (gastown-o8q), a read can start after
// the pre-drop and finish before the push does. It writes the PRE-push answer
// into the freshly emptied map, and that answer survives the push -- the exact
// stale read the guard exists to prevent, arriving through a three-line window
// instead of through a missing call.
//
// IT IS DELIBERATELY NOT A RACING TEST. A version that spawns a reader against a
// real push discriminates only when the goroutines interleave the wrong way, and
// a test that fails one run in five is not evidence when it passes -- the same
// reason the concurrent-read test above asserts memo CARDINALITY rather than
// racing a moving ref. What the race needs is an entry that appears BETWEEN the
// two drops, so this places one there directly and asserts it does not survive.
// The ordering is the property; the concurrency is only how it is reached.
func TestRemoteRefCacheDropIsClosedOnBothSidesOfAMutation(t *testing.T) {
	g := NewGit(t.TempDir())

	BeginRemoteRefCache()
	defer EndRemoteRefCache()

	// An answer memoized before the push, as a real pass would have.
	remoteRefCacheMu.Lock()
	remoteRefCacheEntries["pre"] = &remoteRefEntry{}
	remoteRefCacheMu.Unlock()

	done, err := g.preExec([]string{"push", "origin", "main"})
	if err != nil {
		t.Fatalf("preExec: %v", err)
	}

	remoteRefCacheMu.Lock()
	clearedUpFront := len(remoteRefCacheEntries) == 0
	// The racing read: it lands while the subprocess is still in flight, so the
	// value it stores describes the remote as it was BEFORE the push.
	remoteRefCacheEntries["raced"] = &remoteRefEntry{}
	remoteRefCacheMu.Unlock()

	if !clearedUpFront {
		t.Fatal("preExec did not drop the memo before the subprocess")
	}

	done()

	remoteRefCacheMu.Lock()
	left := len(remoteRefCacheEntries)
	remoteRefCacheMu.Unlock()
	if left != 0 {
		t.Fatalf("%d memo entry(ies) survived the push: an answer cached while the "+
			"push was in flight describes the pre-push remote and must not outlive it",
			left)
	}
}

// TestRemoteRefCacheSurvivesAReadOnlyCommand is the negative control for the
// test above, and without it that one proves nothing useful: a preExec hook that
// emptied the memo after EVERY command would pass it and would also make the
// memo worthless, since a dispatch pass runs many read-only git commands between
// its remote lookups. That regression has no failing symptom -- only a slower
// pass, which is the condition gastown-o8q exists to fix.
func TestRemoteRefCacheSurvivesAReadOnlyCommand(t *testing.T) {
	g := NewGit(t.TempDir())

	BeginRemoteRefCache()
	defer EndRemoteRefCache()

	remoteRefCacheMu.Lock()
	remoteRefCacheEntries["kept"] = &remoteRefEntry{}
	remoteRefCacheMu.Unlock()

	done, err := g.preExec([]string{"rev-parse", "HEAD"})
	if err != nil {
		t.Fatalf("preExec: %v", err)
	}
	done()

	remoteRefCacheMu.Lock()
	left := len(remoteRefCacheEntries)
	remoteRefCacheMu.Unlock()
	if left != 1 {
		t.Fatalf("a read-only command left %d memo entries, want 1: dropping the memo "+
			"on every command makes it worthless, and nothing fails when it does", left)
	}
}
