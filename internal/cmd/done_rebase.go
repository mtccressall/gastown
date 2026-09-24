package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// prMergedForBranch asks the forge whether this branch's PR was merged.
//
// A TARGETED --head QUERY, never a listing: `gh pr list` returns a bounded page
// and an older PR then reads as ABSENT at rc=0, which here would mean silently
// telling a polecat its merged work is not merged. That truncation cost a wrong
// published finding earlier this week at --limit 100 on a busy repo.
func prMergedForBranch(branch string) (bool, error) {
	if branch == "" {
		return false, fmt.Errorf("no branch")
	}
	out, err := exec.Command("gh", "pr", "list", "--head", branch,
		"--state", "merged", "--json", "number,mergedAt", "--limit", "10").Output()
	if err != nil {
		return false, err
	}
	var rows []struct {
		Number   int    `json:"number"`
		MergedAt string `json:"mergedAt"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return false, err
	}
	for _, r := range rows {
		if strings.TrimSpace(r.MergedAt) != "" {
			return true, nil
		}
	}
	return false, nil
}

// rebaseGit is the subset of *git.Git that autoRebaseOnTarget needs. Defined as
// an interface so tests can drive the decision logic without standing up a full
// git repo for every gating case.
type rebaseGit interface {
	Rebase(onto string) error
	AbortRebase() error
}

// alreadyMergedFn reports whether this branch's PR has been merged. It is a
// parameter rather than a call so a test can drive the decision, and because
// the only authority on "was this merged" is the forge.
type alreadyMergedFn func() (bool, error)

// autoRebaseOnTarget rebases the current branch onto base when the branch is
// behind the target. It is a no-op when there is nothing to rebase, when the
// polecat ran the formula's pre-verify step (rebasing again would invalidate
// the gate results that --pre-verified attests to), or when a prior push
// checkpoint exists (rebasing after pushing would require a force-push).
//
// Returns:
//   - rebased: true if a rebase actually ran successfully.
//   - skipReason: non-empty when behind > 0 but the rebase was intentionally
//     skipped. Empty when behind == 0 (no rebase needed) or when rebased == true.
//   - err: rebase failure, after AbortRebase has been attempted to clean up.
//
// gh#3400.
func autoRebaseOnTarget(g rebaseGit, base string, behind int, preVerified, alreadyPushed bool, merged alreadyMergedFn) (rebased bool, skipReason string, err error) {
	if behind <= 0 {
		return false, "", nil
	}
	switch {
	case preVerified:
		return false, "--pre-verified is set", nil
	case alreadyPushed:
		return false, "prior push checkpoint exists", nil
	}

	fmt.Printf("→ Auto-rebasing onto %s (%d commits behind)\n", base, behind)
	if rebaseErr := g.Rebase(base); rebaseErr != nil {
		_ = g.AbortRebase()
		// A squash-merged branch conflicts with a SQUASHED COPY OF ITSELF, and
		// the old advice sent the polecat to resolve conflicts against its own
		// merged work — which cannot be done and did not need doing. Every
		// polecat whose PR is squash-merged reaches this state (gt-jokpn).
		// A TREE COMPARISON CANNOT ANSWER THIS, and the first version of this
		// fix used one. DiffNameOnly runs `git diff --name-only base...head` —
		// TRIPLE dot, i.e. merge-base..head, the branch's OWN changes. A squash
		// creates a new commit, so the branch's commits never enter main's
		// history, the merge base stays at the branch point, and that diff is
		// NEVER empty. Measured on real squash-merged branches: gt-745z0
		// triple=6 two=16, gt-sglq triple=4 two=18. Two-dot does not rescue it
		// either, because main moves after a branch is cut. So the detection
		// could not fire for any squash merge that will ever exist
		// (gastown/refinery on PR 66).
		//
		// The forge is the only authority on whether a PR was merged.
		if merged != nil {
			if ok, mErr := merged(); mErr == nil && ok {
				return false, "already merged (squash)", nil
			}
		}
		return false, "", fmt.Errorf("auto-rebase onto %s failed: %w\n"+
			"Resolve conflicts manually (git fetch origin && git rebase %s), commit the resolution, then rerun gt done.",
			base, rebaseErr, base)
	}
	return true, "", nil
}
