package daemon

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// liveop/atom's live failure, 2026-09-26, reproduced as a regression: the dog
// fired between atom's edit and its `git add`, committed atom's new file as
// "WIP: checkpoint (auto)", and atom's own commit then found NOTHING STAGED and
// its message was discarded — while the push reported success. The output read
// "nothing added to commit" and "pushed" in the same breath, and it nearly
// reported to an external reviewer that a rationale was in history when it was
// not (gt-hgwls).
//
// With the checkpoint written OFF the branch, the agent's file stays uncommitted
// and its own commit carries its own message.
func TestTheDogDoesNotSwallowAnAgentsCommitMessage(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := runGitCmd(repo, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	run("init", "-q", ".")
	run("config", "user.name", "atom")
	run("config", "user.email", "atom@t")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "seed")

	// atom writes a new script and has NOT staged it yet.
	if err := os.WriteFile(filepath.Join(repo, "probe.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The dog fires in that window — this is the exact race.
	d := &Daemon{logger: log.New(io.Discard, "", 0)}
	if !d.checkpointWorktree(repo, "liveop", "atom") {
		t.Fatal("no checkpoint written; the fixture no longer exercises the race")
	}

	// atom now commits, as it did.
	run("add", "-A")
	// runGitCmdAs, not runGitCmd: this test process exports GIT_AUTHOR_NAME,
	// which OUTRANKS the repo's user.name — the same precedence that defeated
	// the first version of the PR 68 identity fix. Without it the assertion
	// below reads the harness's identity instead of atom's.
	out, err := runGitCmdAs(repo, "atom", "atom@t", "commit", "-m", "feat(probe): the rationale and test record")
	if err != nil {
		t.Fatalf("atom's own commit FAILED after the dog ran: %v\n%s", err, out)
	}
	if strings.Contains(out, "nothing added to commit") {
		t.Fatalf("the dog consumed atom's staged work; its commit found nothing:\n%s", out)
	}

	// ITS message must be on the branch, carrying ITS file.
	subj := strings.TrimSpace(run("log", "-1", "--format=%s"))
	if subj != "feat(probe): the rationale and test record" {
		t.Errorf("branch tip subject = %q; atom's message was discarded", subj)
	}
	if got := run("log", "-1", "--format=%an"); strings.TrimSpace(got) != "atom" {
		t.Errorf("branch tip author = %q, want atom", strings.TrimSpace(got))
	}
	files := run("show", "--stat", "--format=", "HEAD")
	if !strings.Contains(files, "probe.sh") {
		t.Errorf("atom's file is not in atom's commit:\n%s", files)
	}
	// And no checkpoint commit reached the branch at all.
	if all := run("log", "--format=%s"); strings.Contains(all, checkpointMessage) {
		t.Errorf("a checkpoint landed on the branch:\n%s", all)
	}
}
