package cmd

import (
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/daemon"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func capture(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// THE BEHAVIOURAL TEST mayor/ made a condition of the off-branch design: a
// polecat dies with uncommitted work, and a FRESH READER must be TOLD the
// checkpoint exists without being told to look for it.
//
// Moving the checkpoint off the branch takes it out of git log, git status and
// every ordinary diff. That is better hygiene and strictly worse recovery
// unless something a successor already runs says the ref is there — the ruling
// Henry made on #429, that retained bytes a person cannot reach are not draft
// recovery, applied to us (gt-hgwls).
func TestAFreshSessionIsToldAboutAnOffBranchCheckpointWithoutLookingForIt(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", ".")
	gitIn(t, repo, "config", "user.name", "Marc Cressall")
	gitIn(t, repo, "config", "user.email", "mtccressall@gmail.com")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "seed")
	tipBefore := gitIn(t, repo, "rev-parse", "HEAD")

	// The polecat's uncommitted work, including a file git has never seen.
	if err := os.WriteFile(filepath.Join(repo, "handler.go"), []byte("package x // real work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The dog fires, then the polecat dies.
	d := daemon.NewForTest(log.New(io.Discard, "", 0))
	if !d.CheckpointWorktreeForTest(repo, "liveop", "atom") {
		t.Fatal("no checkpoint was written; the fixture no longer exercises the path")
	}

	// NOTHING about the branch changed — that is the point of the design.
	if got := gitIn(t, repo, "rev-parse", "HEAD"); got != tipBefore {
		t.Errorf("the branch tip MOVED: %s -> %s", tipBefore[:12], got[:12])
	}
	if got := gitIn(t, repo, "log", "--format=%s", "-1"); got == "WIP: checkpoint (auto)" {
		t.Error("the checkpoint landed on the branch")
	}
	// And the agent's own index was not staged underneath it.
	if got := gitIn(t, repo, "diff", "--cached", "--name-only"); got != "" {
		t.Errorf("the checkpoint staged the agent's index: %q", got)
	}

	// NOW THE PART THAT MATTERS: a fresh session runs prime and must be told.
	out := capture(t, func() {
		outputUncommittedCheckpointRef(RoleContext{Role: RolePolecat, WorkDir: repo})
	})

	branch := gitIn(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	ref := checkpointRefPrefix + branch
	for _, want := range []string{ref, "handler.go", "git checkout " + ref + " -- ."} {
		if !strings.Contains(out, want) {
			t.Errorf("a fresh reader is NOT told %q. Got:\n%s", want, out)
		}
	}
}

// The negative control: with nothing checkpointed, prime must stay silent.
// Without this, a section that always prints would pass the test above while
// telling every session about a checkpoint that does not exist.
func TestPrimeSaysNothingWhenThereIsNoOffBranchCheckpoint(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", ".")
	gitIn(t, repo, "config", "user.name", "t")
	gitIn(t, repo, "config", "user.email", "t@t")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "seed")

	out := capture(t, func() {
		outputUncommittedCheckpointRef(RoleContext{Role: RolePolecat, WorkDir: repo})
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("prime announced a checkpoint that does not exist:\n%s", out)
	}
}
