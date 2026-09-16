package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeMayorRigClone builds a rig whose mayor/rig is a non-bare --single-branch
// clone: the shape gt actually provisions, and the shape BareRepoRefspecCheck
// cannot see.
func makeMayorRigClone(t *testing.T, townRoot, rigName string) string {
	t.Helper()

	src := filepath.Join(t.TempDir(), "src")
	mustGit(t, "", "init", src)
	mustGit(t, src, "config", "user.email", "test@test.com")
	mustGit(t, src, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustGit(t, src, "add", ".")
	mustGit(t, src, "commit", "-m", "initial")
	mustGit(t, src, "branch", "-M", "main")
	mustGit(t, src, "branch", "sidebranch")

	mayorRig := filepath.Join(townRoot, rigName, "mayor", "rig")
	if err := os.MkdirAll(filepath.Dir(mayorRig), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustGit(t, "", "clone", "--single-branch", "--branch", "main", src, mayorRig)
	return mayorRig
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestMayorRigRefspecCheck_DetectsRestrictedRefspec(t *testing.T) {
	townRoot := t.TempDir()
	makeMayorRigClone(t, townRoot, "testrig")

	check := NewMayorRigRefspecCheck()
	ctx := &CheckContext{TownRoot: townRoot, RigName: "testrig"}

	result := check.Run(ctx)
	if result.Status != StatusWarning {
		t.Fatalf("expected StatusWarning on a --single-branch mayor rig, got %v: %s", result.Status, result.Message)
	}
}

func TestMayorRigRefspecCheck_FixWidensRefspec(t *testing.T) {
	townRoot := t.TempDir()
	mayorRig := makeMayorRigClone(t, townRoot, "testrig")

	check := NewMayorRigRefspecCheck()
	ctx := &CheckContext{TownRoot: townRoot, RigName: "testrig"}

	if err := check.Fix(ctx); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if result := check.Run(ctx); result.Status != StatusOK {
		t.Fatalf("expected StatusOK after Fix, got %v: %s", result.Status, result.Message)
	}

	// Behavioural: the rig must now be able to resolve a non-default branch.
	mustGit(t, mayorRig, "fetch", "origin")
	cmd := exec.Command("git", "-C", mayorRig, "rev-parse", "--verify", "refs/remotes/origin/sidebranch")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("origin/sidebranch still unresolvable after Fix: %v\n%s", err, out)
	}
}

// The documented hand repair adds the wildcard alongside the restricted line.
// Such a rig is healthy and the check must not report it broken — reading only
// the first value of remote.origin.fetch would.
func TestMayorRigRefspecCheck_AcceptsAdditiveHandRepair(t *testing.T) {
	townRoot := t.TempDir()
	mayorRig := makeMayorRigClone(t, townRoot, "testrig")
	mustGit(t, mayorRig, "config", "--add", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")

	check := NewMayorRigRefspecCheck()
	ctx := &CheckContext{TownRoot: townRoot, RigName: "testrig"}

	if result := check.Run(ctx); result.Status != StatusOK {
		t.Fatalf("expected StatusOK on an additively repaired rig, got %v: %s", result.Status, result.Message)
	}
}

// Same repair, wildcard written FIRST. This is the ordering that discriminates:
// `git config --get` returns only the LAST value, so a check reading it would
// see the restricted line alone and report a healthy rig as broken.
func TestMayorRigRefspecCheck_AcceptsWildcardFirstOrdering(t *testing.T) {
	townRoot := t.TempDir()
	mayorRig := makeMayorRigClone(t, townRoot, "testrig")
	mustGit(t, mayorRig, "config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	mustGit(t, mayorRig, "config", "--add", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")

	check := NewMayorRigRefspecCheck()
	ctx := &CheckContext{TownRoot: townRoot, RigName: "testrig"}

	if result := check.Run(ctx); result.Status != StatusOK {
		t.Fatalf("expected StatusOK when the wildcard is not the last refspec, got %v: %s", result.Status, result.Message)
	}
}

// Fix must be idempotent: a second run must not stack duplicate refspecs.
func TestMayorRigRefspecCheck_FixIsIdempotent(t *testing.T) {
	townRoot := t.TempDir()
	mayorRig := makeMayorRigClone(t, townRoot, "testrig")

	check := NewMayorRigRefspecCheck()
	ctx := &CheckContext{TownRoot: townRoot, RigName: "testrig"}

	for i := 0; i < 3; i++ {
		if err := check.Fix(ctx); err != nil {
			t.Fatalf("Fix run %d: %v", i, err)
		}
	}

	out, err := exec.Command("git", "-C", mayorRig, "config", "--get-all", "remote.origin.fetch").Output()
	if err != nil {
		t.Fatalf("reading refspecs: %v", err)
	}
	wildcards := strings.Count(string(out), "refs/heads/*")
	if wildcards != 1 {
		t.Fatalf("expected exactly 1 wildcard refspec after repeated Fix, got %d:\n%s", wildcards, out)
	}
}

// A wildcard whose DESTINATION is not refs/remotes/origin/* leaves origin/<branch>
// unresolvable — the exact state this check exists to detect. A substring test for
// "refs/heads/*" would call it healthy.
func TestMayorRigRefspecCheck_RejectsNonOriginWildcard(t *testing.T) {
	townRoot := t.TempDir()
	mayorRig := makeMayorRigClone(t, townRoot, "testrig")
	mustGit(t, mayorRig, "config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")

	check := NewMayorRigRefspecCheck()
	ctx := &CheckContext{TownRoot: townRoot, RigName: "testrig"}

	if result := check.Run(ctx); result.Status != StatusWarning {
		t.Fatalf("expected StatusWarning for a wildcard that does not populate origin/*, got %v: %s", result.Status, result.Message)
	}
}

// A non-forced wildcard still populates the tracking refs and is healthy.
func TestMayorRigRefspecCheck_AcceptsNonForcedWildcard(t *testing.T) {
	townRoot := t.TempDir()
	mayorRig := makeMayorRigClone(t, townRoot, "testrig")
	mustGit(t, mayorRig, "config", "--replace-all", "remote.origin.fetch", "refs/heads/*:refs/remotes/origin/*")

	check := NewMayorRigRefspecCheck()
	ctx := &CheckContext{TownRoot: townRoot, RigName: "testrig"}

	if result := check.Run(ctx); result.Status != StatusOK {
		t.Fatalf("expected StatusOK for a non-forced origin wildcard, got %v: %s", result.Status, result.Message)
	}
}

func TestMayorRigRefspecCheck_NoMayorRigIsOK(t *testing.T) {
	townRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(townRoot, "testrig"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	check := NewMayorRigRefspecCheck()
	ctx := &CheckContext{TownRoot: townRoot, RigName: "testrig"}

	if result := check.Run(ctx); result.Status != StatusOK {
		t.Fatalf("expected StatusOK when there is no mayor rig, got %v: %s", result.Status, result.Message)
	}
}
