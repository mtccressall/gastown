package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitpkg "github.com/steveyegge/gastown/internal/git"
)

// notMerged is the default forge answer: this branch's PR was not merged.
func notMerged() (int, bool, error) { return 0, false, nil }

// wasMerged stands in for a squash-merged PR.
func wasMerged() (int, bool, error) { return 49, true, nil }

// fakeRebaseGit lets us drive autoRebaseOnTarget without a real git repo for
// the gating-decision tests.
type fakeRebaseGit struct {
	rebaseErr   error
	rebaseCalls int
	abortCalls  int
}

func (f *fakeRebaseGit) Rebase(onto string) error {
	f.rebaseCalls++
	return f.rebaseErr
}

func (f *fakeRebaseGit) AbortRebase() error {
	f.abortCalls++
	return nil
}

// TestAutoRebaseOnTarget_GatingDecisions verifies the skip/rebase decision
// matrix (gh#3400). The behavior under test is the *decision*, not the actual
// git mechanics — those are exercised separately below against a real repo.
func TestAutoRebaseOnTarget_GatingDecisions(t *testing.T) {
	tests := []struct {
		name          string
		behind        int
		preVerified   bool
		alreadyPushed bool
		wantRebased   bool
		wantSkip      string
		wantCalls     int
	}{
		{
			name:        "not behind: no-op",
			behind:      0,
			wantRebased: false,
			wantSkip:    "",
			wantCalls:   0,
		},
		{
			name:        "behind by 1: rebase runs",
			behind:      1,
			wantRebased: true,
			wantSkip:    "",
			wantCalls:   1,
		},
		{
			name:        "behind by 5: rebase runs",
			behind:      5,
			wantRebased: true,
			wantSkip:    "",
			wantCalls:   1,
		},
		{
			name:        "pre-verified: skip even when behind",
			behind:      3,
			preVerified: true,
			wantRebased: false,
			wantSkip:    "--pre-verified is set",
			wantCalls:   0,
		},
		{
			name:          "already pushed: skip to avoid divergence",
			behind:        3,
			alreadyPushed: true,
			wantRebased:   false,
			wantSkip:      "prior push checkpoint exists",
			wantCalls:     0,
		},
		{
			name:          "pre-verified takes precedence over already-pushed",
			behind:        3,
			preVerified:   true,
			alreadyPushed: true,
			wantRebased:   false,
			wantSkip:      "--pre-verified is set",
			wantCalls:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeRebaseGit{}
			rebased, skipReason, err := autoRebaseOnTarget(fake, "origin/main", tt.behind, tt.preVerified, tt.alreadyPushed, notMerged)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rebased != tt.wantRebased {
				t.Errorf("rebased = %v, want %v", rebased, tt.wantRebased)
			}
			if skipReason != tt.wantSkip {
				t.Errorf("skipReason = %q, want %q", skipReason, tt.wantSkip)
			}
			if fake.rebaseCalls != tt.wantCalls {
				t.Errorf("rebase calls = %d, want %d", fake.rebaseCalls, tt.wantCalls)
			}
			if fake.abortCalls != 0 {
				t.Errorf("abort calls = %d on success path, want 0", fake.abortCalls)
			}
		})
	}
}

// TestAutoRebaseOnTarget_ConflictAborts verifies that a rebase failure causes
// AbortRebase to fire and the returned error includes remediation guidance.
func TestAutoRebaseOnTarget_ConflictAborts(t *testing.T) {
	// diffFiles must name foo.txt: this fixture already claims a conflict IN
	// foo.txt, so the trees necessarily differ. Without it the fake describes an
	// impossible state — a content conflict between identical trees — which is
	// precisely the squash-merge signature, and autoRebaseOnTarget now
	// classifies it as such (gt-jokpn).
	fake := &fakeRebaseGit{
		rebaseErr: errors.New("CONFLICT (content): merge conflict in foo.txt"),
	}

	rebased, skipReason, err := autoRebaseOnTarget(fake, "origin/main", 1, false, false, notMerged)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if rebased {
		t.Error("rebased should be false on conflict")
	}
	if skipReason != "" {
		t.Errorf("skipReason should be empty on conflict, got %q", skipReason)
	}
	if fake.rebaseCalls != 1 {
		t.Errorf("expected 1 rebase call, got %d", fake.rebaseCalls)
	}
	if fake.abortCalls != 1 {
		t.Errorf("expected AbortRebase to fire on conflict, got %d calls", fake.abortCalls)
	}
	// Remediation guidance is part of the contract — agents read this message.
	msg := err.Error()
	if !strings.Contains(msg, "auto-rebase onto origin/main failed") {
		t.Errorf("error missing context: %q", msg)
	}
	if !strings.Contains(msg, "git rebase origin/main") {
		t.Errorf("error missing remediation hint: %q", msg)
	}
	if !strings.Contains(msg, "rerun gt done") {
		t.Errorf("error missing rerun hint: %q", msg)
	}
}

// TestAutoRebaseOnTarget_RealRepoSuccess exercises the rebase against a real
// git working tree to confirm the wiring (Rebase call) actually replays the
// branch onto a moved base. (gh#3400, scenario (a) from the bead notes.)
func TestAutoRebaseOnTarget_RealRepoSuccess(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	testRunGit(t, tmp, "init", "--initial-branch", "main", repo)
	testRunGit(t, repo, "config", "user.email", "test@test.com")
	testRunGit(t, repo, "config", "user.name", "Test")

	// Initial commit on main.
	writeRepoFile(t, repo, "README.md", "# initial\n")
	testRunGit(t, repo, "add", ".")
	testRunGit(t, repo, "commit", "-m", "initial")

	// Branch off and add a polecat commit on a non-conflicting file.
	testRunGit(t, repo, "checkout", "-b", "feature")
	writeRepoFile(t, repo, "feature.txt", "feature work\n")
	testRunGit(t, repo, "add", ".")
	testRunGit(t, repo, "commit", "-m", "feature")

	// Move main forward independently — also non-conflicting with feature.
	testRunGit(t, repo, "checkout", "main")
	writeRepoFile(t, repo, "main-new.txt", "new on main\n")
	testRunGit(t, repo, "add", ".")
	testRunGit(t, repo, "commit", "-m", "advance main")

	testRunGit(t, repo, "checkout", "feature")

	g := gitpkg.NewGit(repo)
	rebased, skipReason, err := autoRebaseOnTarget(g, "main", 1, false, false, notMerged)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rebased {
		t.Fatalf("expected rebased=true, got false (skip=%q)", skipReason)
	}

	// After rebase, both files must be present and the feature commit must sit
	// on top of the advance-main commit.
	if _, statErr := os.Stat(filepath.Join(repo, "main-new.txt")); statErr != nil {
		t.Errorf("main-new.txt missing after rebase: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "feature.txt")); statErr != nil {
		t.Errorf("feature.txt missing after rebase: %v", statErr)
	}
}

// TestAutoRebaseOnTarget_RealRepoConflictAborts exercises the conflict path
// against a real git working tree: feature and main both touch the same file,
// rebase fails with a CONFLICT, and AbortRebase must restore the working tree
// so the polecat can address the conflict manually. (gh#3400, scenario (b).)
func TestAutoRebaseOnTarget_RealRepoConflictAborts(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	testRunGit(t, tmp, "init", "--initial-branch", "main", repo)
	testRunGit(t, repo, "config", "user.email", "test@test.com")
	testRunGit(t, repo, "config", "user.name", "Test")

	writeRepoFile(t, repo, "shared.txt", "v0\n")
	testRunGit(t, repo, "add", ".")
	testRunGit(t, repo, "commit", "-m", "initial")

	// Feature changes shared.txt to v1.
	testRunGit(t, repo, "checkout", "-b", "feature")
	writeRepoFile(t, repo, "shared.txt", "v1-feature\n")
	testRunGit(t, repo, "add", ".")
	testRunGit(t, repo, "commit", "-m", "feature edit")

	// Main changes shared.txt to a different value — guaranteed conflict.
	testRunGit(t, repo, "checkout", "main")
	writeRepoFile(t, repo, "shared.txt", "v1-main\n")
	testRunGit(t, repo, "add", ".")
	testRunGit(t, repo, "commit", "-m", "main edit")

	testRunGit(t, repo, "checkout", "feature")

	g := gitpkg.NewGit(repo)
	rebased, skipReason, err := autoRebaseOnTarget(g, "main", 1, false, false, notMerged)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if rebased {
		t.Error("rebased should be false on conflict")
	}
	if skipReason != "" {
		t.Errorf("skipReason should be empty on conflict, got %q", skipReason)
	}

	// AbortRebase is required to leave the working tree in a clean state. After
	// abort, the rebase-merge dir must be gone — otherwise the polecat is stuck
	// in a half-rebased state.
	if _, statErr := os.Stat(filepath.Join(repo, ".git", "rebase-merge")); !os.IsNotExist(statErr) {
		t.Errorf(".git/rebase-merge should not exist after abort (stat err: %v)", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".git", "rebase-apply")); !os.IsNotExist(statErr) {
		t.Errorf(".git/rebase-apply should not exist after abort (stat err: %v)", statErr)
	}
}

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// THE CONFLICT FALLBACK, which is retained for the multi-commit case: the
// pre-rebase query says not-merged, the rebase then conflicts, and a second
// query finds the merge that landed in between. Without the fallback the
// polecat would get "resolve conflicts manually" for work that had just landed.
func TestAutoRebaseFallsBackToTheForgeAfterAConflict(t *testing.T) {
	fake := &fakeRebaseGit{rebaseErr: errors.New("could not apply abc123... the commit")}

	calls := 0
	mergedLate := func() (int, bool, error) {
		calls++
		if calls == 1 {
			return 0, false, nil // pre-rebase: not merged yet
		}
		return 49, true, nil // landed while we were rebasing
	}

	rebased, _, err := autoRebaseOnTarget(fake, "origin/main", 3, false, false, mergedLate)

	if err == nil {
		t.Fatal("the conflict fallback did not report the merge, so runDone would continue into push")
	}
	if !strings.Contains(err.Error(), "PR #49") {
		t.Errorf("error does not name the merging PR: %v", err)
	}
	if rebased {
		t.Error("rebased = true for work that already landed")
	}
	if fake.abortCalls != 1 {
		t.Errorf("abortCalls = %d, want 1 — a failed rebase must still be cleaned up", fake.abortCalls)
	}
	if calls != 2 {
		t.Errorf("forge consulted %d time(s), want 2 — before the rebase and again after the conflict", calls)
	}
}

// NEGATIVE CONTROL. A real conflict — trees still differ — must keep the
// original error and its advice. Without this, a function that always claimed
// "already merged" would pass the test above.
func TestAutoRebaseKeepsConflictAdviceWhenTreesStillDiffer(t *testing.T) {
	fake := &fakeRebaseGit{
		rebaseErr: errors.New("could not apply abc123... the commit"),
	}

	_, _, err := autoRebaseOnTarget(fake, "origin/main", 3, false, false, notMerged)

	if err == nil {
		t.Fatal("no error for a genuine conflict — the polecat loses the advice it needs")
	}
	if !strings.Contains(err.Error(), "Resolve conflicts manually") {
		t.Errorf("error lost the conflict advice: %v", err)
	}
}

// A failure to REACH THE FORGE must not be read as "merged". Fail toward the
// ordinary advice: wrongly claiming a merge would have the polecat walk away
// from work that never landed.
func TestAutoRebaseTreatsForgeFailureAsNotMerged(t *testing.T) {
	fake := &fakeRebaseGit{
		rebaseErr: errors.New("could not apply abc123... the commit"),
	}

	forgeDown := func() (int, bool, error) { return 0, false, errors.New("gh: could not reach api.github.com") }
	_, _, err := autoRebaseOnTarget(fake, "origin/main", 3, false, false, forgeDown)

	if err == nil {
		t.Fatal("an unreachable forge was treated as proof the work had merged")
	}
}

// A merged PR at a DIFFERENT revision is not this work. The live case:
// polecat/deacon/gt-mnnx+reply-reminder had PR 49 merged at e68f0088 and was
// then pushed to 2f9e7ed1 — the stranded commit. Matching the branch NAME alone
// would tell that polecat its unmerged work had landed.
func TestAutoRebaseDoesNotTrustAMergeAtAnotherRevision(t *testing.T) {
	fake := &fakeRebaseGit{rebaseErr: errors.New("could not apply abc123")}

	mergedElsewhere := func() (int, bool, error) { return 0, false, nil } // revision did not match

	_, _, err := autoRebaseOnTarget(fake, "origin/main", 3, false, false, mergedElsewhere)

	if err == nil {
		t.Fatal("no error: a branch whose merged PR was at another revision was treated as merged")
	}
	if !strings.Contains(err.Error(), "Resolve conflicts manually") {
		t.Errorf("lost the ordinary conflict advice: %v", err)
	}
}

// THE LIVE STRANDING CASE, driven directly. polecat/deacon/gt-mnnx+reply-reminder
// had PR 49 merged at e68f0088; the branch was then pushed to 2f9e7ed1, which
// never merged. Matching the branch NAME alone would tell a polecat sitting on
// 2f9e7ed1 that its work had landed.
func TestMergedPRAtRevisionRequiresTheRevisionToMatch(t *testing.T) {
	rows := []mergedPRRow{{Number: 49, MergedAt: "2026-09-16T05:08:08Z", HeadRefOid: "e68f0088"}}

	if pr, ok := mergedPRAtRevision(rows, "e68f0088"); !ok || pr != 49 {
		t.Errorf("the merged revision was not recognised: pr=%d ok=%v", pr, ok)
	}
	if pr, ok := mergedPRAtRevision(rows, "2f9e7ed1"); ok {
		t.Errorf("the STRANDED revision was reported as merged by PR %d — this is the gt-mnnx sequence", pr)
	}
}

// An unmerged row must never satisfy it, whatever its head.
func TestMergedPRAtRevisionIgnoresUnmergedRows(t *testing.T) {
	rows := []mergedPRRow{{Number: 70, MergedAt: "", HeadRefOid: "deadbeef"}}
	if _, ok := mergedPRAtRevision(rows, "deadbeef"); ok {
		t.Error("an unmerged PR was treated as a merge")
	}
}

// THE CLEAN-REBASE PATH, which the first two rounds never exercised. A
// SINGLE-COMMIT squash-merged branch rebases successfully — git skips the
// already-applied patch — so a check that only runs after a conflict is never
// consulted, and runDone proceeds into push and MR creation, recreating the
// branch deleted at merge. Reproduced from scratch before this test was written
// (gastown/refinery, PR 66 round 3).
func TestAutoRebaseRefusesAMergedBranchEvenWhenTheRebaseWouldSucceed(t *testing.T) {
	fake := &fakeRebaseGit{} // Rebase returns nil: a CLEAN rebase

	rebased, _, err := autoRebaseOnTarget(fake, "origin/main", 2, false, false, wasMerged)

	if err == nil {
		t.Fatal("a clean rebase of an already-merged branch returned no error, so runDone would push and enqueue landed work")
	}
	if rebased {
		t.Error("rebased = true for work that already landed")
	}
	if fake.rebaseCalls != 0 {
		t.Errorf("rebase ran %d time(s) — the forge must be asked BEFORE rebasing, or the clean path skips the check", fake.rebaseCalls)
	}
	if !strings.Contains(err.Error(), "PR #49") {
		t.Errorf("error does not name the merging PR: %v", err)
	}
}

// NEGATIVE CONTROL: an unmerged branch must still rebase normally, or the check
// above would pass for a function that refuses everything.
func TestAutoRebaseStillRebasesAnUnmergedBranch(t *testing.T) {
	fake := &fakeRebaseGit{}

	rebased, skipReason, err := autoRebaseOnTarget(fake, "origin/main", 2, false, false, notMerged)

	if err != nil || !rebased || skipReason != "" {
		t.Fatalf("an unmerged branch was not rebased: rebased=%v skip=%q err=%v", rebased, skipReason, err)
	}
	if fake.rebaseCalls != 1 {
		t.Errorf("rebaseCalls = %d, want 1", fake.rebaseCalls)
	}
}
