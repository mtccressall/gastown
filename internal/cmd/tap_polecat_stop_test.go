package cmd

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const polecatStopTestBranch = "polecat/test/gt-ksnv@abc123"

// polecatStopDoneSentinelEnv names a file this test binary touches when it is
// re-invoked with "done", so a hook that shells out to gt done is visible.
const polecatStopDoneSentinelEnv = "GT_TEST_POLECAT_STOP_DONE_SENTINEL"

func TestPolecatStopPendingWork(t *testing.T) {
	t.Run("clean feature branch has no pending work", func(t *testing.T) {
		repo := initPolecatStopTestRepo(t)

		pending, reason, err := polecatStopPendingWork(repo, polecatStopTestBranch)
		if err != nil {
			t.Fatalf("polecatStopPendingWork: %v", err)
		}
		if pending {
			t.Fatalf("pending = true (%s), want false", reason)
		}
	})

	t.Run("non-runtime dirty work is pending", func(t *testing.T) {
		repo := initPolecatStopTestRepo(t)
		writePolecatStopTestFile(t, repo, "internal/cmd/work.go", "package cmd\n")

		pending, reason, err := polecatStopPendingWork(repo, polecatStopTestBranch)
		if err != nil {
			t.Fatalf("polecatStopPendingWork: %v", err)
		}
		if !pending {
			t.Fatal("pending = false, want true")
		}
		if !strings.Contains(reason, "non-runtime dirty") {
			t.Fatalf("reason = %q, want non-runtime dirty work", reason)
		}
	})

	t.Run("runtime-only dirty work is ignored", func(t *testing.T) {
		repo := initPolecatStopTestRepo(t)
		writePolecatStopTestFile(t, repo, ".opencode/state.json", "{}\n")

		pending, reason, err := polecatStopPendingWork(repo, polecatStopTestBranch)
		if err != nil {
			t.Fatalf("polecatStopPendingWork: %v", err)
		}
		if pending {
			t.Fatalf("pending = true (%s), want false", reason)
		}
	})

	t.Run("branch stash is pending", func(t *testing.T) {
		repo := initPolecatStopTestRepo(t)
		writePolecatStopTestFile(t, repo, "stash-work.txt", "saved work\n")
		runPolecatStopTestGit(t, repo, "stash", "push", "-u", "-m", "branch stash")

		pending, reason, err := polecatStopPendingWork(repo, polecatStopTestBranch)
		if err != nil {
			t.Fatalf("polecatStopPendingWork: %v", err)
		}
		if !pending {
			t.Fatal("pending = false, want true")
		}
		if !strings.Contains(reason, "branch stash") {
			t.Fatalf("reason = %q, want branch stash", reason)
		}
	})

	t.Run("pushed source branch still pending until target contains it", func(t *testing.T) {
		repo := initPolecatStopTestRepo(t)
		writePolecatStopTestFile(t, repo, "submitted.go", "package main\n")
		runPolecatStopTestGit(t, repo, "add", "submitted.go")
		runPolecatStopTestGit(t, repo, "commit", "-m", "add submitted work")
		runPolecatStopTestGit(t, repo, "push", "origin", "HEAD:"+polecatStopTestBranch)

		pending, reason, err := polecatStopPendingWork(repo, polecatStopTestBranch)
		if err != nil {
			t.Fatalf("polecatStopPendingWork: %v", err)
		}
		if !pending {
			t.Fatal("pending = false, want true")
		}
		if !strings.Contains(reason, "unsubmitted commit") {
			t.Fatalf("reason = %q, want unsubmitted commit", reason)
		}
	})

	t.Run("target-contained commit has no pending work", func(t *testing.T) {
		repo := initPolecatStopTestRepo(t)
		writePolecatStopTestFile(t, repo, "merged.go", "package main\n")
		runPolecatStopTestGit(t, repo, "add", "merged.go")
		runPolecatStopTestGit(t, repo, "commit", "-m", "add merged work")
		runPolecatStopTestGit(t, repo, "checkout", "main")
		runPolecatStopTestGit(t, repo, "merge", "--ff-only", polecatStopTestBranch)
		runPolecatStopTestGit(t, repo, "push", "origin", "main")
		runPolecatStopTestGit(t, repo, "checkout", polecatStopTestBranch)

		pending, reason, err := polecatStopPendingWork(repo, polecatStopTestBranch)
		if err != nil {
			t.Fatalf("polecatStopPendingWork: %v", err)
		}
		if pending {
			t.Fatalf("pending = true (%s), want false", reason)
		}
	})

	t.Run("invalid repo fails closed", func(t *testing.T) {
		pending, reason, err := polecatStopPendingWork(t.TempDir(), polecatStopTestBranch)
		if err == nil {
			t.Fatal("polecatStopPendingWork error = nil, want error")
		}
		if pending {
			t.Fatalf("pending = true (%s), want false", reason)
		}
	})
}

func TestStopHookActive(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  bool
	}{
		{"active", `{"session_id":"s","stop_hook_active":true}`, true},
		{"inactive", `{"session_id":"s","stop_hook_active":false}`, false},
		{"absent", `{"session_id":"s"}`, false},
		{"empty input", ``, false},
		{"invalid json", `not json`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stopHookActive([]byte(tc.input)); got != tc.want {
				t.Fatalf("stopHookActive(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestPolecatStopShouldBlock(t *testing.T) {
	for _, tc := range []struct {
		pending, reminded, want bool
	}{
		{pending: false, reminded: false, want: false},
		{pending: false, reminded: true, want: false},
		{pending: true, reminded: false, want: true},
		{pending: true, reminded: true, want: false},
	} {
		if got := polecatStopShouldBlock(tc.pending, tc.reminded); got != tc.want {
			t.Errorf("polecatStopShouldBlock(pending=%v, reminded=%v) = %v, want %v", tc.pending, tc.reminded, got, tc.want)
		}
	}
}

// A Stop hook fires at every turn end. A polecat that ends a turn while its
// gates run in the background still has unsubmitted commits, and the hook
// used to run gt done for it, closing the bead and tearing the session down
// mid-gates (gt-e3upy). With pending work the hook must block the stop with a
// reminder, and must not run gt done.
func TestRunTapPolecatStopRemindsInsteadOfRunningDone(t *testing.T) {
	// The old auto-done path ran os.Executable() with "done". Under go test that
	// is this test binary, so a regression would re-run this test recursively.
	// Record that it happened before skipping: without the sentinel the skip
	// hides the very regression this test exists for, because "the commit is
	// still unsubmitted" holds whether or not the hook shelled out.
	if len(os.Args) > 1 && os.Args[1] == "done" {
		if sentinel := os.Getenv(polecatStopDoneSentinelEnv); sentinel != "" {
			_ = os.WriteFile(sentinel, []byte(strings.Join(os.Args, " ")), 0644)
		}
		t.Skip("invoked as gt done by a regressed stop hook")
	}
	sentinel := filepath.Join(t.TempDir(), "gt-done-was-run")
	t.Setenv(polecatStopDoneSentinelEnv, sentinel)
	town := t.TempDir()
	if err := os.MkdirAll(filepath.Join(town, "mayor"), 0755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(town, "mayor", "town.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write town.json: %v", err)
	}
	repo := initPolecatStopTestRepo(t)
	writePolecatStopTestFile(t, repo, "gates-running.go", "package main\n")
	runPolecatStopTestGit(t, repo, "add", "gates-running.go")
	runPolecatStopTestGit(t, repo, "commit", "-m", "work whose gates are still running")
	polecatDir := filepath.Join(town, "rigx", "polecats", "px")
	if err := os.MkdirAll(polecatDir, 0755); err != nil {
		t.Fatalf("mkdir polecat dir: %v", err)
	}
	if err := os.Symlink(repo, filepath.Join(polecatDir, "rigx")); err != nil {
		t.Fatalf("symlink clone: %v", err)
	}

	t.Chdir(town)
	t.Setenv("GT_POLECAT", "px")
	t.Setenv("GT_SESSION", "rigx-px")
	t.Setenv("GT_RIG", "rigx")
	t.Setenv("GT_TOWN_ROOT", town)

	run := func(t *testing.T, hookInput string) string {
		t.Helper()
		in, err := os.CreateTemp(t.TempDir(), "stdin")
		if err != nil {
			t.Fatalf("create stdin: %v", err)
		}
		if _, err := in.WriteString(hookInput); err != nil {
			t.Fatalf("write stdin: %v", err)
		}
		if _, err := in.Seek(0, 0); err != nil {
			t.Fatalf("seek stdin: %v", err)
		}
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		oldIn, oldOut := os.Stdin, os.Stdout
		os.Stdin, os.Stdout = in, w
		runErr := runTapPolecatStop(nil, nil)
		os.Stdin, os.Stdout = oldIn, oldOut
		_ = w.Close()
		_ = in.Close()
		out, _ := io.ReadAll(r)
		if runErr != nil {
			t.Fatalf("runTapPolecatStop: %v", runErr)
		}
		return string(out)
	}

	t.Run("first stop is blocked with a reminder", func(t *testing.T) {
		out := run(t, `{"stop_hook_active":false}`)
		var decision struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(out), &decision); err != nil {
			t.Fatalf("stdout is not a Stop hook decision: %v\n%s", err, out)
		}
		if decision.Decision != "block" {
			t.Fatalf("decision = %q, want block", decision.Decision)
		}
		for _, want := range []string{"run gt done now", "keep waiting", "will not be run for you", "unsubmitted commit"} {
			if !strings.Contains(decision.Reason, want) {
				t.Errorf("reason missing %q:\n%s", want, decision.Reason)
			}
		}
	})

	t.Run("stop after a reminder is allowed", func(t *testing.T) {
		if out := run(t, `{"stop_hook_active":true}`); strings.TrimSpace(out) != "" {
			t.Fatalf("stdout = %q, want no decision", out)
		}
	})

	t.Run("gt done was not run", func(t *testing.T) {
		// The sentinel is written by this test binary when it is re-invoked as
		// "done", which is exactly what the old auto-done path did.
		if data, err := os.ReadFile(sentinel); err == nil {
			t.Fatalf("the stop hook shelled out to done: %s", data)
		}
		// gt done submits the branch; the pending commit must still be unsubmitted.
		pending, reason, err := polecatStopPendingWork(repo, polecatStopTestBranch)
		if err != nil {
			t.Fatalf("polecatStopPendingWork: %v", err)
		}
		if !pending {
			t.Fatal("work is no longer pending; something submitted it")
		}
		if !strings.Contains(reason, "unsubmitted commit") {
			t.Fatalf("reason = %q, want unsubmitted commit", reason)
		}
	})
}

func initPolecatStopTestRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	origin := filepath.Join(tmp, "origin.git")

	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runPolecatStopTestGit(t, repo, "init")
	runPolecatStopTestGit(t, repo, "branch", "-M", "main")
	runPolecatStopTestGit(t, repo, "config", "user.email", "test@test.com")
	runPolecatStopTestGit(t, repo, "config", "user.name", "Test User")
	writePolecatStopTestFile(t, repo, "README.md", "# Test\n")
	runPolecatStopTestGit(t, repo, "add", "README.md")
	runPolecatStopTestGit(t, repo, "commit", "-m", "initial")

	runPolecatStopTestGit(t, tmp, "init", "--bare", origin)
	runPolecatStopTestGit(t, repo, "remote", "add", "origin", origin)
	runPolecatStopTestGit(t, repo, "push", "-u", "origin", "main")
	runPolecatStopTestGit(t, repo, "checkout", "-b", polecatStopTestBranch)

	return repo
}

func writePolecatStopTestFile(t *testing.T, repo, rel, contents string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func runPolecatStopTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := append([]string{"-c", "protocol.file.allow=always"}, args...)
	cmd := exec.Command("git", fullArgs...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}
