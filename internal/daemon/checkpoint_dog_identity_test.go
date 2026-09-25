package daemon

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The auto-checkpoint commits code the AGENT wrote. Without an explicit author
// git uses the worktree's ambient identity, which measured 34 of 34 "Marc
// Cressall" on live-op — a human credited for agent-written production code,
// permanently, by a background job (gt-kb3ry).
func TestCheckpointCommitIdentityNamesThePolecatNotTheAmbientUser(t *testing.T) {
	name, email := checkpointCommitIdentity("liveop", "atom")

	if name != "liveop/polecats/atom" {
		t.Errorf("name = %q, want liveop/polecats/atom", name)
	}
	// Format pinned against internal/cmd/commit.go's identityToEmail, which
	// cannot be imported here (internal/cmd imports internal/daemon). If that
	// convention changes, this test is what catches the divergence.
	if email != "liveop.polecats.atom@gastown.local" {
		t.Errorf("email = %q, want liveop.polecats.atom@gastown.local", email)
	}
}

// A human name must never appear, whatever the ambient git config holds.
func TestCheckpointCommitIdentityIsDerivedOnlyFromTheBranchNamespace(t *testing.T) {
	for _, tc := range []struct{ rig, pc string }{
		{"gastown", "emerald"}, {"beadsrig", "cheedo"}, {"steward", "x"},
	} {
		name, email := checkpointCommitIdentity(tc.rig, tc.pc)
		if name == "" || email == "" {
			t.Fatalf("empty identity for %s/%s", tc.rig, tc.pc)
		}
		if name != tc.rig+"/polecats/"+tc.pc {
			t.Errorf("name = %q, want %s/polecats/%s", name, tc.rig, tc.pc)
		}
	}
}

// THE WIRING TEST, against a real repository. The unit test above drives the
// helper; deleting the -c override at the CALL SITE left it green, which is the
// same gap this town has caught on three of my PRs. Only running the function
// and reading the resulting commit proves the author is applied.
func TestCheckpointWorktreeCommitsUnderThePolecatIdentity(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := runGitCmd(repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-q", ".")
	// An ambient identity a HUMAN would have: the defect is that this is what
	// the checkpoint inherited.
	run("config", "user.name", "Marc Cressall")
	run("config", "user.email", "mtccressall@gmail.com")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "seed")

	// Uncommitted agent work, exactly what the checkpoint dog sweeps up.
	if err := os.WriteFile(filepath.Join(repo, "agent.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{logger: log.New(io.Discard, "", 0)}
	if !d.checkpointWorktree(repo, "liveop", "atom") {
		t.Fatal("checkpointWorktree reported no checkpoint; the fixture no longer exercises the path")
	}

	author, err := runGitCmd(repo, "log", "-1", "--format=%an <%ae>")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(author)
	if got != "liveop/polecats/atom <liveop.polecats.atom@gastown.local>" {
		t.Errorf("checkpoint author = %q, want the polecat — a human is credited for agent-written code", got)
	}
	if strings.Contains(got, "Marc") {
		t.Errorf("the ambient HUMAN identity survived into the commit: %q", got)
	}
}
