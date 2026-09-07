package git

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const restrictedRefspecMarker = "refs/heads/*"

// initRefspecSourceRepo builds a source repo with a default branch plus a
// second branch, so tests can ask whether a clone can resolve a branch other
// than the one it was cloned at.
func initRefspecSourceRepo(t *testing.T) string {
	t.Helper()
	src := initTestRepo(t)

	// Normalize the default branch name; `git init` honours init.defaultBranch.
	run(t, src, "branch", "-M", "main")
	run(t, src, "branch", "sidebranch")
	return src
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func fetchRefspecs(t *testing.T, repo string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "config", "--get-all", "remote.origin.fetch")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("reading remote.origin.fetch in %s: %v", repo, err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

func assertWildcardRefspec(t *testing.T, repo string) {
	t.Helper()
	specs := fetchRefspecs(t, repo)
	for _, s := range specs {
		if strings.Contains(s, restrictedRefspecMarker) {
			return
		}
	}
	t.Fatalf("remote.origin.fetch in %s has no wildcard refspec; got %q", repo, specs)
}

// TestNonBareCloneGetsWildcardRefspec is the gastown-rxl acceptance test. It
// asserts on the MAYOR RIG shape — a non-bare --single-branch clone — because a
// test that only checks the bare repo passes both before and after the fix.
func TestNonBareCloneGetsWildcardRefspec(t *testing.T) {
	src := initRefspecSourceRepo(t)

	cases := map[string]func(g *Git, dest string) error{
		"Clone": func(g *Git, dest string) error {
			return g.Clone(src, dest)
		},
		"CloneBranch": func(g *Git, dest string) error {
			return g.CloneBranch(src, dest, "main")
		},
		"CloneWithReference": func(g *Git, dest string) error {
			return g.CloneWithReference(src, dest, src)
		},
		"CloneBranchWithReference": func(g *Git, dest string) error {
			return g.CloneBranchWithReference(src, dest, "main", src)
		},
		"CloneBranchPartial": func(g *Git, dest string) error {
			return g.CloneBranchPartial(src, dest, "main", "blob:none")
		},
	}

	for name, clone := range cases {
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "rig")
			if err := clone(NewGit(t.TempDir()), dest); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			assertWildcardRefspec(t, dest)
		})
	}
}

// TestNonBareCloneCanResolveOtherBranches is the behavioural half: the config
// value only matters because it decides whether `git fetch` can ever create a
// tracking ref for a branch other than the cloned one. Without the fix the
// fetch below exits 0, prints nothing, and origin/sidebranch never appears.
func TestNonBareCloneCanResolveOtherBranches(t *testing.T) {
	src := initRefspecSourceRepo(t)
	dest := filepath.Join(t.TempDir(), "rig")

	if err := NewGit(t.TempDir()).CloneBranch(src, dest, "main"); err != nil {
		t.Fatalf("CloneBranch: %v", err)
	}

	// A plain `git fetch origin` is what any caller runs; it must now widen.
	run(t, dest, "fetch", "origin")

	cmd := exec.Command("git", "-C", dest, "rev-parse", "--verify", "refs/remotes/origin/sidebranch")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("origin/sidebranch does not resolve after fetch: %v\n%s", err, out)
	}
}

// TestNonBareCloneRefspecFixDoesNotShallowPartialClones pins the reason the
// non-bare path sets the config without fetching. CloneBranchPartial takes no
// --depth, so a `git fetch --depth 1` would convert a full-history clone into a
// shallow one — breaking every merge-base query the wider refspec exists to
// enable.
func TestNonBareCloneRefspecFixDoesNotShallowPartialClones(t *testing.T) {
	src := initRefspecSourceRepo(t)
	dest := filepath.Join(t.TempDir(), "rig")

	if err := NewGit(t.TempDir()).CloneBranchPartial(src, dest, "main", "blob:none"); err != nil {
		t.Fatalf("CloneBranchPartial: %v", err)
	}

	if got := run(t, dest, "rev-parse", "--is-shallow-repository"); got != "false" {
		t.Fatalf("partial clone was made shallow by refspec configuration: is-shallow=%s", got)
	}
}

// TestBareCloneStillGetsWildcardRefspec guards the pre-existing behaviour the
// refactor moved: bare clones must keep both the config and the ref-creating
// fetch (gastown#286).
func TestBareCloneStillGetsWildcardRefspec(t *testing.T) {
	src := initRefspecSourceRepo(t)
	dest := filepath.Join(t.TempDir(), ".repo.git")

	if err := NewGit(t.TempDir()).CloneBare(src, dest); err != nil {
		t.Fatalf("CloneBare: %v", err)
	}

	assertWildcardRefspec(t, dest)

	cmd := exec.Command("git", "--git-dir", dest, "rev-parse", "--verify", "refs/remotes/origin/main")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bare clone lost origin/main: %v\n%s", err, out)
	}
}
