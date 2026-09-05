package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeCommit(t *testing.T, dir, name, body, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", msg)
	g := NewGit(dir)
	sha, err := g.Rev("HEAD")
	if err != nil {
		t.Fatalf("rev HEAD: %v", err)
	}
	return sha
}

// This is the real-git counterpart to the fake-driven proof tests. It builds an
// actual squash merge onto a target that MOVED after the branch point -- the
// case that decides between the two candidate proofs -- and asserts what each
// one reports. Measured on the gastown repo itself, over all 12 merged PRs whose
// commits still resolve: ancestry proved 0, tree equality proved 3, patch-id
// proved 12; the 3 tree equality proved are exactly the 3 whose base had not
// moved. This test pins that finding in code. (gastown-2ib)
func TestRangePatchID_ProvesSquashMergeAcrossMovedBase(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	base, err := g.Rev("HEAD")
	if err != nil {
		t.Fatalf("rev base: %v", err)
	}
	// The default branch name is configurable (init.defaultBranch), so capture
	// it rather than assuming master or main.
	target := strings.TrimSpace(gitRun(t, dir, "rev-parse", "--abbrev-ref", "HEAD"))

	// Branch: two commits, so the squash is a genuine multi-commit collapse.
	gitRun(t, dir, "checkout", "-b", "feature")
	writeCommit(t, dir, "feature.txt", "one\n", "feat: first")
	head := writeCommit(t, dir, "feature.txt", "one\ntwo\n", "feat: second")

	// Target moves independently after the branch point. This is what breaks
	// tree equality and what patch-id must survive.
	gitRun(t, dir, "checkout", target)
	writeCommit(t, dir, "unrelated.txt", "moved\n", "chore: target moved")

	// Squash the branch onto the moved target, as GitHub's squash button does.
	gitRun(t, dir, "merge", "--squash", "feature")
	mergeCommit := writeCommit(t, dir, "feature.txt", "one\ntwo\n", "squash: feature (#1)")

	// Ancestry: severed by construction.
	reachable, err := g.IsAncestor(head, mergeCommit)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if reachable {
		t.Fatal("submitted head is an ancestor of the squash commit; scenario is not a squash")
	}

	// Tree equality: the proof this bead originally prescribed. It is
	// squash-invariant but NOT base-invariant, so a moved target defeats it.
	headTree, err := g.Rev(head + "^{tree}")
	if err != nil {
		t.Fatalf("rev head tree: %v", err)
	}
	mergeTree, err := g.Rev(mergeCommit + "^{tree}")
	if err != nil {
		t.Fatalf("rev merge tree: %v", err)
	}
	if headTree == mergeTree {
		t.Fatal("trees are equal; the target did not actually move, so this test cannot discriminate")
	}

	// Range patch-id: base-invariant, and proves the landing.
	mergeBase, err := g.MergeBase(head, mergeCommit+"^")
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if mergeBase != base {
		t.Fatalf("merge base = %s, want branch point %s", mergeBase, base)
	}
	branchPatch, err := g.RangePatchID(mergeBase, head)
	if err != nil {
		t.Fatalf("RangePatchID branch: %v", err)
	}
	mergePatch, err := g.RangePatchID(mergeCommit+"^", mergeCommit)
	if err != nil {
		t.Fatalf("RangePatchID merge: %v", err)
	}
	if branchPatch != mergePatch {
		t.Fatalf("range patch-id did not prove the squash: branch %s, merge %s", branchPatch, mergePatch)
	}

	// The single-commit form must NOT be substituted: a squash of N commits is
	// one combined diff matching no individual commit. Measured on the gastown
	// repo, this is where PRs 12, 17 and 19 fail. (gastown-2ib)
	singlePatch, err := g.RangePatchID(head+"^", head)
	if err != nil {
		t.Fatalf("RangePatchID single: %v", err)
	}
	if singlePatch == mergePatch {
		t.Fatal("single-commit patch-id matched a multi-commit squash; the range form is no longer load-bearing")
	}
}

// Different content must not compare equal, or the proof proves nothing.
func TestRangePatchID_DiffersForDifferentChanges(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	base, err := g.Rev("HEAD")
	if err != nil {
		t.Fatalf("rev base: %v", err)
	}
	mine := writeCommit(t, dir, "a.txt", "mine\n", "mine")
	gitRun(t, dir, "checkout", "-b", "other", base)
	theirs := writeCommit(t, dir, "a.txt", "theirs\n", "theirs")

	minePatch, err := g.RangePatchID(base, mine)
	if err != nil {
		t.Fatalf("RangePatchID mine: %v", err)
	}
	theirsPatch, err := g.RangePatchID(base, theirs)
	if err != nil {
		t.Fatalf("RangePatchID theirs: %v", err)
	}
	if minePatch == theirsPatch {
		t.Fatal("different changes produced the same patch-id")
	}
}

// An empty diff must be an error, not an id. Two empty ranges comparing equal
// would prove a landing that never happened.
func TestRangePatchID_EmptyDiffIsAnError(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	head, err := g.Rev("HEAD")
	if err != nil {
		t.Fatalf("rev HEAD: %v", err)
	}
	if _, err := g.RangePatchID(head, head); err == nil {
		t.Fatal("empty diff returned a patch-id")
	}
}

func TestHasCommit(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	head, err := g.Rev("HEAD")
	if err != nil {
		t.Fatalf("rev HEAD: %v", err)
	}
	if !g.HasCommit(head) {
		t.Fatal("HasCommit false for a commit that exists")
	}
	if g.HasCommit("0123456789012345678901234567890123456789") {
		t.Fatal("HasCommit true for a commit that does not exist")
	}
	if g.HasCommit("") {
		t.Fatal("HasCommit true for an empty sha")
	}
}
