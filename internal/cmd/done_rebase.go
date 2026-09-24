package cmd

import (
	"fmt"
)

// rebaseGit is the subset of *git.Git that autoRebaseOnTarget needs. Defined as
// an interface so tests can drive the decision logic without standing up a full
// git repo for every gating case.
type rebaseGit interface {
	Rebase(onto string) error
	AbortRebase() error
	// DiffNameOnly reports the files differing between two refs. An empty
	// result means the trees are identical, which is how a squash merge is
	// detected here — see alreadyLandedAsSquash.
	DiffNameOnly(base, head string) ([]string, error)
}

// alreadyLandedAsSquash reports whether this branch's work is already in base,
// by comparing TREES rather than ancestry.
//
// ANCESTRY CANNOT BE THIS TEST, and that is the whole reason the function
// exists. A squash merge replays the branch as ONE NEW COMMIT on base, so the
// reviewed head is not an ancestor of base afterwards even though every line of
// it has landed. Verified three times in one week on this town's PRs. Patch-id
// (git cherry) is no better: a branch cut before another PR touched the same
// file carries different hunk context, so identical content produces a
// different patch-id (gt-u76h).
//
// An empty diff between base and HEAD means the branch introduces nothing base
// does not already have. That is sufficient, not necessary: if other work
// landed on base in the meantime the trees differ and this returns false, which
// is the safe direction — the caller falls back to the ordinary conflict
// advice rather than wrongly claiming the work is merged.
func alreadyLandedAsSquash(g rebaseGit, base string) bool {
	changed, err := g.DiffNameOnly(base, "HEAD")
	if err != nil {
		return false
	}
	return len(changed) == 0
}

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
func autoRebaseOnTarget(g rebaseGit, base string, behind int, preVerified, alreadyPushed bool) (rebased bool, skipReason string, err error) {
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
		if alreadyLandedAsSquash(g, base) {
			return false, "already merged (squash)", nil
		}
		return false, "", fmt.Errorf("auto-rebase onto %s failed: %w\n"+
			"Resolve conflicts manually (git fetch origin && git rebase %s), commit the resolution, then rerun gt done.",
			base, rebaseErr, base)
	}
	return true, "", nil
}
