package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/steveyegge/gastown/internal/util"
)

// MergeBase returns the best common ancestor of two commits.
func (g *Git) MergeBase(a, b string) (string, error) {
	return g.run("merge-base", a, b)
}

// HasCommit reports whether the commit object is present in the local object store.
func (g *Git) HasCommit(commit string) bool {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return false
	}
	_, err := g.run("cat-file", "-e", commit+"^{commit}")
	return err == nil
}

// FetchCommit fetches a single commit by SHA from remote. Used to make a commit
// diffable when the branch that carried it has already been deleted.
func (g *Git) FetchCommit(remote, commit string) error {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return fmt.Errorf("fetch commit: empty commit for remote %s", remote)
	}
	_, err := g.run("fetch", "--no-tags", g.pushTarget(remote), commit)
	return err
}

// RangePatchID returns the patch-id of the combined diff from base to head.
//
// The range form is required, not incidental. A squash merge collapses every
// commit on the branch into one combined diff, so its patch-id matches the
// patch-id of the WHOLE RANGE and matches no individual commit on the branch.
// Measured on this repo: comparing single commits agrees with the squash on
// 1-commit branches and disagrees on every multi-commit branch (PRs 12, 17, 19),
// while the range form agrees on all 12 merged PRs.
//
// patch-id --stable normalizes context and line numbers, so the id is invariant
// under rebase and under a moving base. That is what makes it a usable merge
// proof where tree equality is not: a tree is base-dependent and reports a false
// negative whenever the target advanced between branch point and merge.
func (g *Git) RangePatchID(base, head string) (string, error) {
	base = strings.TrimSpace(base)
	head = strings.TrimSpace(head)
	if base == "" || head == "" {
		return "", fmt.Errorf("patch-id: empty range %q..%q", base, head)
	}
	diff, err := g.runRaw("diff", base, head)
	if err != nil {
		return "", fmt.Errorf("patch-id: diff %s..%s: %w", shortSHA(base), shortSHA(head), err)
	}
	if len(bytes.TrimSpace(diff)) == 0 {
		// An empty diff has no patch-id. Report it as such rather than letting
		// two empty ids compare equal and prove a landing that never happened.
		return "", fmt.Errorf("patch-id: empty diff for %s..%s", shortSHA(base), shortSHA(head))
	}
	out, err := g.runWithStdin(diff, "patch-id", "--stable")
	if err != nil {
		return "", fmt.Errorf("patch-id: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("patch-id: no id for %s..%s", shortSHA(base), shortSHA(head))
	}
	return fields[0], nil
}

// runRaw executes a git command and returns raw stdout bytes. Diff output is
// binary-ish and must not go through the trimming that run() applies.
func (g *Git) runRaw(args ...string) ([]byte, error) {
	if err := g.guardUnsafeTownRootMutation(args); err != nil {
		return nil, err
	}
	if g.gitDir != "" {
		args = append([]string{"--git-dir=" + g.gitDir}, args...)
	}
	cmd := exec.Command("git", args...)
	util.SetDetachedProcessGroup(cmd)
	if g.workDir != "" {
		cmd.Dir = g.workDir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return stdout.Bytes(), nil
}

// runWithStdin executes a git command feeding input on stdin.
func (g *Git) runWithStdin(input []byte, args ...string) ([]byte, error) {
	if err := g.guardUnsafeTownRootMutation(args); err != nil {
		return nil, err
	}
	if g.gitDir != "" {
		args = append([]string{"--git-dir=" + g.gitDir}, args...)
	}
	cmd := exec.Command("git", args...)
	util.SetDetachedProcessGroup(cmd)
	if g.workDir != "" {
		cmd.Dir = g.workDir
	}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return stdout.Bytes(), nil
}
