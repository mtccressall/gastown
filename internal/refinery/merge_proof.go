package refinery

import (
	"errors"
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/git"
)

// MergeProofRequest is the merge-request shape a post-merge proof needs. Both
// the `gt mq post-merge` CLI path and the Engineer's own post-merge path build
// one of these so a single proof serves both. Adding a proof mode to one path
// and not the other is the bug this type exists to make unrepresentable.
type MergeProofRequest struct {
	Target    string
	Branch    string
	CommitSHA string
	PRURL     string
	PRNumber  int
}

// MergeProofGit is the git surface a merge proof consumes.
type MergeProofGit interface {
	VerifyPushedCommitReachableFromPushTarget(remote, branch, commit string) error
	LookupPullRequest(ref git.PullRequestRef) (*git.PullRequestInfo, error)
	HasCommit(commit string) bool
	FetchCommit(remote, commit string) error
	MergeBase(a, b string) (string, error)
	RangePatchID(base, head string) (string, error)
}

// VerifyMergeProof proves that the submitted head's content is present on the
// target branch. It accepts either of two proofs.
//
// ANCESTRY (unchanged, tried first): the submitted head is reachable from
// origin/<target>. True for a real merge commit or a fast-forward.
//
// SQUASH EQUIVALENCE: a squash merge builds a NEW single-parent commit and does
// not preserve the submitted head, so ancestry is severed by construction and no
// amount of retrying will satisfy it. Measured on this repo, all 12 merged PRs
// whose commits still resolve: ancestry proved 0 of 12.
//
// The squash proof requires ALL of:
//   - GitHub reports the PR MERGED, and its head SHA is the commit we submitted
//     (LookupPullRequest fails closed on a head that moved, so a rewritten branch
//     cannot be proved landed);
//   - the PR's merge commit is itself reachable from origin/<target>;
//   - the merge commit introduces the same changes as the branch, by patch-id.
//
// Ancestry is not weakened and PR state alone is never sufficient. The guard
// against marking work landed that is not on the target is preserved: the
// content equality is what replaces the commit linkage a squash destroys.
//
// WHY PATCH-ID AND NOT TREE EQUALITY: a tree is squash-invariant but NOT
// base-invariant, so it reports a false negative whenever the target advanced
// between branch point and merge -- the normal case. Measured over the same 12
// PRs, tree equality proved 3 and patch-id proved 12, and the 3 it proved are
// exactly the 3 whose base had not moved.
func VerifyMergeProof(rigGit MergeProofGit, req MergeProofRequest) error {
	if rigGit == nil {
		return fmt.Errorf("git client is missing")
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return fmt.Errorf("missing target branch")
	}
	if source := strings.TrimSpace(req.Branch); source != "" && source == target {
		return fmt.Errorf("source branch %s matches target branch", source)
	}
	commit := strings.TrimSpace(req.CommitSHA)
	if commit == "" {
		return fmt.Errorf("missing submitted commit_sha")
	}

	ancestryErr := rigGit.VerifyPushedCommitReachableFromPushTarget("origin", target, commit)
	if ancestryErr == nil {
		return nil
	}

	squashErr := verifySquashMergeProof(rigGit, req, target, commit)
	if squashErr == nil {
		return nil
	}
	return fmt.Errorf("target %s does not contain submitted head %s: %w; and no squash-equivalent merge: %v", target, commit, ancestryErr, squashErr)
}

func verifySquashMergeProof(rigGit MergeProofGit, req MergeProofRequest, target, commit string) error {
	pr, err := rigGit.LookupPullRequest(git.PullRequestRef{
		URL:     strings.TrimSpace(req.PRURL),
		Number:  req.PRNumber,
		Branch:  strings.TrimSpace(req.Branch),
		HeadSHA: commit,
	})
	if err != nil {
		if errors.Is(err, git.ErrPullRequestNotFound) {
			return fmt.Errorf("no pull request found for submitted head %s", shortProofSHA(commit))
		}
		return fmt.Errorf("pull request lookup: %w", err)
	}
	if !pr.Merged() {
		return fmt.Errorf("PR #%d is %s, not MERGED", pr.Number, pr.State)
	}
	mergeCommit := strings.TrimSpace(pr.MergeCommit)
	if mergeCommit == "" {
		return fmt.Errorf("PR #%d reports MERGED with no merge commit", pr.Number)
	}

	// The merge commit must be on the target. Without this, a PR merged into some
	// other branch would prove a landing on this one.
	if err := rigGit.VerifyPushedCommitReachableFromPushTarget("origin", target, mergeCommit); err != nil {
		return fmt.Errorf("PR #%d merge commit %s is not on %s: %w", pr.Number, shortProofSHA(mergeCommit), target, err)
	}

	// A deleted source branch leaves the submitted head unreachable by ref but
	// still fetchable by SHA. Post-merge runs precisely when branches are being
	// deleted, so this is the ordinary case rather than the exotic one.
	for _, sha := range []string{commit, mergeCommit} {
		if rigGit.HasCommit(sha) {
			continue
		}
		if err := rigGit.FetchCommit("origin", sha); err != nil {
			return fmt.Errorf("fetch %s for content comparison: %w", shortProofSHA(sha), err)
		}
		if !rigGit.HasCommit(sha) {
			return fmt.Errorf("commit %s is not available locally for content comparison", shortProofSHA(sha))
		}
	}

	base, err := rigGit.MergeBase(commit, mergeCommit+"^")
	if err != nil {
		return fmt.Errorf("merge base of %s and %s^: %w", shortProofSHA(commit), shortProofSHA(mergeCommit), err)
	}
	branchPatch, err := rigGit.RangePatchID(base, commit)
	if err != nil {
		return fmt.Errorf("branch patch-id: %w", err)
	}
	mergePatch, err := rigGit.RangePatchID(mergeCommit+"^", mergeCommit)
	if err != nil {
		return fmt.Errorf("merge commit patch-id: %w", err)
	}
	if branchPatch != mergePatch {
		return fmt.Errorf("PR #%d merge commit %s does not introduce the submitted changes (branch patch-id %s, merge patch-id %s)",
			pr.Number, shortProofSHA(mergeCommit), shortProofSHA(branchPatch), shortProofSHA(mergePatch))
	}
	return nil
}

func shortProofSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
