package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/refinery"
)

type fakeMQPostMergeManager struct {
	mr              *refinery.MergeRequest
	findErr         error
	postMergeErr    error
	postMergeCalled bool
	postMergeMR     *refinery.MergeRequest
}

func (m *fakeMQPostMergeManager) FindMRForPostMerge(string) (*refinery.MergeRequest, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	return m.mr, nil
}

func (m *fakeMQPostMergeManager) PostMergeMR(mr *refinery.MergeRequest) (*refinery.PostMergeResult, error) {
	m.postMergeCalled = true
	m.postMergeMR = mr
	if m.postMergeErr != nil {
		return nil, m.postMergeErr
	}
	return &refinery.PostMergeResult{MR: m.mr, MRClosed: true, SourceIssueClosed: true, SourceIssueID: m.mr.IssueID}, nil
}

type fakeMQPostMergeGit struct {
	verifyErr error
	openPR    bool
	deleteErr error
	remoteTip string
	localHead string
	tipErr    error

	// Squash-proof surface. reachable lists commits that ARE on the target;
	// when non-nil it overrides verifyErr per commit.
	reachable   map[string]bool
	pr          *git.PullRequestInfo
	prErr       error
	patchIDs    map[string]string
	patchIDErr  error
	present     map[string]bool
	fetched     []string
	mergeBases  map[string]string
	lookupCalls []git.PullRequestRef

	verifiedCommits []string
	deletedBranches []string
	deletedHeads    []string
	localDeleted    []string
}

func (g *fakeMQPostMergeGit) VerifyPushedCommitReachableFromPushTarget(_, _, commit string) error {
	g.verifiedCommits = append(g.verifiedCommits, commit)
	if g.reachable != nil {
		if g.reachable[commit] {
			return nil
		}
		return errors.New("not reachable: " + commit)
	}
	return g.verifyErr
}

func (g *fakeMQPostMergeGit) LookupPullRequest(ref git.PullRequestRef) (*git.PullRequestInfo, error) {
	g.lookupCalls = append(g.lookupCalls, ref)
	if g.prErr != nil {
		return nil, g.prErr
	}
	if g.pr == nil {
		return nil, git.ErrPullRequestNotFound
	}
	return g.pr, nil
}

func (g *fakeMQPostMergeGit) HasCommit(commit string) bool {
	if g.present == nil {
		return true
	}
	return g.present[commit]
}

func (g *fakeMQPostMergeGit) FetchCommit(_, commit string) error {
	g.fetched = append(g.fetched, commit)
	if g.present != nil {
		g.present[commit] = true
	}
	return nil
}

func (g *fakeMQPostMergeGit) MergeBase(a, b string) (string, error) {
	if base, ok := g.mergeBases[a+" "+b]; ok {
		return base, nil
	}
	return "merge-base-" + a, nil
}

func (g *fakeMQPostMergeGit) RangePatchID(base, head string) (string, error) {
	if g.patchIDErr != nil {
		return "", g.patchIDErr
	}
	id, ok := g.patchIDs[base+".."+head]
	if !ok {
		return "", errors.New("no patch-id for " + base + ".." + head)
	}
	return id, nil
}

func (g *fakeMQPostMergeGit) HasOpenPullRequest(git.PullRequestRef) bool {
	return g.openPR
}

func (g *fakeMQPostMergeGit) PushRemoteBranchTip(_, _ string) (string, error) {
	return g.remoteTip, g.tipErr
}

func (g *fakeMQPostMergeGit) Rev(string) (string, error) {
	return g.localHead, nil
}

func (g *fakeMQPostMergeGit) DeleteRemoteBranchIfAt(_, branch, expectedHash string) error {
	g.deletedBranches = append(g.deletedBranches, branch)
	g.deletedHeads = append(g.deletedHeads, expectedHash)
	return g.deleteErr
}

func (g *fakeMQPostMergeGit) DeleteBranch(branch string, _ bool) error {
	g.localDeleted = append(g.localDeleted, branch)
	return nil
}

func testMQPostMergeMR() *refinery.MergeRequest {
	return &refinery.MergeRequest{
		ID:           "gt-mr-proof",
		Branch:       "polecat/test/gt-proof",
		Worker:       "polecats/test",
		IssueID:      "gt-proof",
		TargetBranch: "main",
		CommitSHA:    "abc123def456",
	}
}

func TestRunVerifiedMQPostMerge_ProofFailurePreservesRecordsAndBranch(t *testing.T) {
	mgr := &fakeMQPostMergeManager{mr: testMQPostMergeMR()}
	rigGit := &fakeMQPostMergeGit{verifyErr: errors.New("not reachable")}

	_, _, err := runVerifiedMQPostMerge(mgr, t.TempDir(), rigGit, mgr.mr.ID, false)
	if err == nil || !strings.Contains(err.Error(), "merge proof failed") {
		t.Fatalf("runVerifiedMQPostMerge error = %v, want merge proof failure", err)
	}
	if !strings.Contains(err.Error(), mgr.mr.CommitSHA) {
		t.Fatalf("proof error %q does not mention submitted head %s", err, mgr.mr.CommitSHA)
	}
	if mgr.postMergeCalled {
		t.Fatal("PostMerge called after failed proof")
	}
	if len(rigGit.deletedBranches) != 0 {
		t.Fatalf("remote branch deleted after failed proof: %v", rigGit.deletedBranches)
	}
	if len(rigGit.localDeleted) != 0 {
		t.Fatalf("local branch deleted after failed proof: %v", rigGit.localDeleted)
	}
}

func TestRunVerifiedMQPostMerge_VerifiedHeadClosesAndLeaseDeletes(t *testing.T) {
	mgr := &fakeMQPostMergeManager{mr: testMQPostMergeMR()}
	rigGit := &fakeMQPostMergeGit{remoteTip: mgr.mr.CommitSHA, localHead: mgr.mr.CommitSHA}

	_, cleanup, err := runVerifiedMQPostMerge(mgr, t.TempDir(), rigGit, mgr.mr.ID, false)
	if err != nil {
		t.Fatalf("runVerifiedMQPostMerge: %v", err)
	}
	if !mgr.postMergeCalled {
		t.Fatal("PostMerge was not called after successful proof")
	}
	if mgr.postMergeMR != mgr.mr {
		t.Fatal("PostMerge did not use the verified MR snapshot")
	}
	if len(rigGit.verifiedCommits) != 1 || rigGit.verifiedCommits[0] != mgr.mr.CommitSHA {
		t.Fatalf("verified commits = %v, want [%s]", rigGit.verifiedCommits, mgr.mr.CommitSHA)
	}
	if !cleanup.RemoteDeleted || len(rigGit.deletedBranches) != 1 || rigGit.deletedBranches[0] != mgr.mr.Branch {
		t.Fatalf("remote delete = cleanup=%+v branches=%v", cleanup, rigGit.deletedBranches)
	}
	if len(rigGit.deletedHeads) != 1 || rigGit.deletedHeads[0] != mgr.mr.CommitSHA {
		t.Fatalf("deleted heads = %v, want [%s]", rigGit.deletedHeads, mgr.mr.CommitSHA)
	}
	if !cleanup.LocalDeleted || len(rigGit.localDeleted) != 1 || rigGit.localDeleted[0] != mgr.mr.Branch {
		t.Fatalf("local delete = cleanup=%+v local=%v", cleanup, rigGit.localDeleted)
	}
}

func TestRunVerifiedMQPostMerge_SkipBranchDeleteStillRequiresProof(t *testing.T) {
	mgr := &fakeMQPostMergeManager{mr: testMQPostMergeMR()}
	rigGit := &fakeMQPostMergeGit{}

	_, cleanup, err := runVerifiedMQPostMerge(mgr, t.TempDir(), rigGit, mgr.mr.ID, true)
	if err != nil {
		t.Fatalf("runVerifiedMQPostMerge: %v", err)
	}
	if !mgr.postMergeCalled {
		t.Fatal("PostMerge was not called after successful proof")
	}
	if len(rigGit.verifiedCommits) != 1 || rigGit.verifiedCommits[0] != mgr.mr.CommitSHA {
		t.Fatalf("verified commits = %v, want [%s]", rigGit.verifiedCommits, mgr.mr.CommitSHA)
	}
	if !cleanup.Skipped {
		t.Fatalf("cleanup.Skipped = false, cleanup=%+v", cleanup)
	}
	if len(rigGit.deletedBranches) != 0 || len(rigGit.localDeleted) != 0 {
		t.Fatalf("branch deleted despite skip: remote=%v local=%v", rigGit.deletedBranches, rigGit.localDeleted)
	}
}

func TestRunVerifiedMQPostMerge_OpenPRSkipsRemoteDeleteAfterProof(t *testing.T) {
	mgr := &fakeMQPostMergeManager{mr: testMQPostMergeMR()}
	rigGit := &fakeMQPostMergeGit{openPR: true, localHead: mgr.mr.CommitSHA}

	_, cleanup, err := runVerifiedMQPostMerge(mgr, t.TempDir(), rigGit, mgr.mr.ID, false)
	if err != nil {
		t.Fatalf("runVerifiedMQPostMerge: %v", err)
	}
	if !mgr.postMergeCalled {
		t.Fatal("PostMerge was not called after successful proof")
	}
	if !cleanup.OpenPR {
		t.Fatalf("cleanup.OpenPR = false, cleanup=%+v", cleanup)
	}
	if len(rigGit.deletedBranches) != 0 {
		t.Fatalf("remote branch deleted despite open PR: %v", rigGit.deletedBranches)
	}
	if len(rigGit.localDeleted) != 1 || rigGit.localDeleted[0] != mgr.mr.Branch {
		t.Fatalf("local branch cleanup = %v, want [%s]", rigGit.localDeleted, mgr.mr.Branch)
	}
}

func TestRunVerifiedMQPostMerge_LeaseDeleteFailureReturnsAfterPostMerge(t *testing.T) {
	mgr := &fakeMQPostMergeManager{mr: testMQPostMergeMR()}
	rigGit := &fakeMQPostMergeGit{remoteTip: mgr.mr.CommitSHA, deleteErr: errors.New("stale info")}

	_, _, err := runVerifiedMQPostMerge(mgr, t.TempDir(), rigGit, mgr.mr.ID, false)
	if err == nil || !strings.Contains(err.Error(), "remote branch delete") {
		t.Fatalf("runVerifiedMQPostMerge error = %v, want remote branch delete failure", err)
	}
	if !mgr.postMergeCalled {
		t.Fatal("PostMerge was not called after successful proof")
	}
	if len(rigGit.deletedBranches) != 1 || rigGit.deletedBranches[0] != mgr.mr.Branch {
		t.Fatalf("remote delete attempts = %v, want [%s]", rigGit.deletedBranches, mgr.mr.Branch)
	}
	if len(rigGit.deletedHeads) != 1 || rigGit.deletedHeads[0] != mgr.mr.CommitSHA {
		t.Fatalf("delete lease heads = %v, want [%s]", rigGit.deletedHeads, mgr.mr.CommitSHA)
	}
	if len(rigGit.localDeleted) != 0 {
		t.Fatalf("local branch deleted after remote lease failure: %v", rigGit.localDeleted)
	}
}

func TestRunVerifiedMQPostMerge_MissingRemoteBranchIsIdempotentAfterProof(t *testing.T) {
	mgr := &fakeMQPostMergeManager{mr: testMQPostMergeMR()}
	rigGit := &fakeMQPostMergeGit{localHead: mgr.mr.CommitSHA}

	_, cleanup, err := runVerifiedMQPostMerge(mgr, t.TempDir(), rigGit, mgr.mr.ID, false)
	if err != nil {
		t.Fatalf("runVerifiedMQPostMerge: %v", err)
	}
	if !mgr.postMergeCalled {
		t.Fatal("PostMerge was not called after successful proof")
	}
	if !cleanup.AlreadyGone {
		t.Fatalf("cleanup.AlreadyGone = false, cleanup=%+v", cleanup)
	}
	if len(rigGit.deletedBranches) != 0 {
		t.Fatalf("remote branch delete attempted for missing branch: %v", rigGit.deletedBranches)
	}
}

func TestRunVerifiedMQPostMerge_MissingSubmittedHeadFailsClosed(t *testing.T) {
	mr := testMQPostMergeMR()
	mr.CommitSHA = ""
	mgr := &fakeMQPostMergeManager{mr: mr}
	rigGit := &fakeMQPostMergeGit{}

	_, _, err := runVerifiedMQPostMerge(mgr, t.TempDir(), rigGit, mgr.mr.ID, false)
	if err == nil || !strings.Contains(err.Error(), "missing submitted commit_sha") {
		t.Fatalf("runVerifiedMQPostMerge error = %v, want missing submitted head", err)
	}
	if mgr.postMergeCalled {
		t.Fatal("PostMerge called with missing submitted head")
	}
	if len(rigGit.deletedBranches) != 0 {
		t.Fatalf("branch deleted with missing submitted head: %v", rigGit.deletedBranches)
	}
}

func TestRunVerifiedMQPostMerge_SourceTargetBranchFailsClosed(t *testing.T) {
	mr := testMQPostMergeMR()
	mr.Branch = "main"
	mr.TargetBranch = "main"
	mgr := &fakeMQPostMergeManager{mr: mr}
	rigGit := &fakeMQPostMergeGit{}

	_, _, err := runVerifiedMQPostMerge(mgr, t.TempDir(), rigGit, mgr.mr.ID, false)
	if err == nil || !strings.Contains(err.Error(), "matches target branch") {
		t.Fatalf("runVerifiedMQPostMerge error = %v, want source/target failure", err)
	}
	if mgr.postMergeCalled {
		t.Fatal("PostMerge called when source branch matched target")
	}
	if len(rigGit.deletedBranches) != 0 {
		t.Fatalf("branch deleted when source matched target: %v", rigGit.deletedBranches)
	}
}

// squashProofGit builds a fake standing in for a real squash merge: the
// submitted head is NOT reachable from the target (a squash severs ancestry),
// the merge commit IS, and both introduce the same changes.
func squashProofGit(mr *refinery.MergeRequest, mergeCommit string) *fakeMQPostMergeGit {
	return &fakeMQPostMergeGit{
		reachable: map[string]bool{mergeCommit: true},
		pr: &git.PullRequestInfo{
			Number:      21,
			State:       "MERGED",
			HeadSHA:     mr.CommitSHA,
			MergeCommit: mergeCommit,
		},
		mergeBases: map[string]string{mr.CommitSHA + " " + mergeCommit + "^": "base-sha"},
		patchIDs: map[string]string{
			"base-sha.." + mr.CommitSHA:       "patch-same",
			mergeCommit + "^.." + mergeCommit: "patch-same",
		},
		remoteTip: mergeCommit,
		localHead: mr.CommitSHA,
	}
}

// A squash merge is the house style in this repo and ancestry can never prove
// one. Measured: 0 of 12 merged PRs satisfied the ancestry proof. (gastown-2ib)
func TestVerifyMQPostMergeProof_AcceptsSquashMergeByPatchID(t *testing.T) {
	mr := testMQPostMergeMR()
	rigGit := squashProofGit(mr, "merge999")

	if err := verifyMQPostMergeProof(rigGit, mr); err != nil {
		t.Fatalf("squash merge rejected: %v", err)
	}
	if len(rigGit.verifiedCommits) < 2 {
		t.Fatalf("merge commit reachability not checked: %v", rigGit.verifiedCommits)
	}
	// The PR lookup must pin the submitted head, so a rewritten branch cannot
	// be proved landed by a PR that no longer points at what we submitted.
	if len(rigGit.lookupCalls) == 0 || rigGit.lookupCalls[0].HeadSHA != mr.CommitSHA {
		t.Fatalf("PR lookup did not pin submitted head: %+v", rigGit.lookupCalls)
	}
}

// The proof must survive a target that advanced between branch point and merge.
// This is the case tree equality gets wrong: measured over the same 12 PRs, tree
// equality proved 3 -- exactly the 3 whose base had not moved -- and patch-id
// proved 12. A base-invariant proof is the whole point. (gastown-2ib)
func TestVerifyMQPostMergeProof_SquashProofSurvivesMovedBase(t *testing.T) {
	mr := testMQPostMergeMR()
	rigGit := squashProofGit(mr, "merge999")
	// Base moved: the merge commit's parent is not the branch point, so the
	// merge commit's TREE differs from the submitted head's tree. patch-id is
	// unaffected because it compares introduced changes, not end states.
	rigGit.mergeBases = map[string]string{mr.CommitSHA + " merge999^": "old-base"}
	rigGit.patchIDs = map[string]string{
		"old-base.." + mr.CommitSHA: "patch-same",
		"merge999^..merge999":       "patch-same",
	}

	if err := verifyMQPostMergeProof(rigGit, mr); err != nil {
		t.Fatalf("squash proof failed after base moved: %v", err)
	}
}

// The guard this bead explicitly asked to keep: PR state MERGED is never on its
// own sufficient. Different content must still be refused.
func TestVerifyMQPostMergeProof_RejectsMergedPRWithDifferentContent(t *testing.T) {
	mr := testMQPostMergeMR()
	rigGit := squashProofGit(mr, "merge999")
	rigGit.patchIDs = map[string]string{
		"base-sha.." + mr.CommitSHA: "patch-ours",
		"merge999^..merge999":       "patch-theirs",
	}

	err := verifyMQPostMergeProof(rigGit, mr)
	if err == nil {
		t.Fatal("proof accepted a merge commit that introduces different changes")
	}
	if !strings.Contains(err.Error(), "does not introduce the submitted changes") {
		t.Fatalf("error %q does not name the content mismatch", err)
	}
}

// A PR merged into some other branch must not prove a landing on this target.
func TestVerifyMQPostMergeProof_RejectsMergeCommitNotOnTarget(t *testing.T) {
	mr := testMQPostMergeMR()
	rigGit := squashProofGit(mr, "merge999")
	rigGit.reachable = map[string]bool{} // neither head nor merge commit on target

	err := verifyMQPostMergeProof(rigGit, mr)
	if err == nil {
		t.Fatal("proof accepted a merge commit that is not on the target")
	}
	if !strings.Contains(err.Error(), "is not on main") {
		t.Fatalf("error %q does not name the off-target merge commit", err)
	}
}

// An open or closed-unmerged PR proves nothing.
func TestVerifyMQPostMergeProof_RejectsUnmergedPR(t *testing.T) {
	mr := testMQPostMergeMR()
	rigGit := squashProofGit(mr, "merge999")
	rigGit.pr.State = "OPEN"

	err := verifyMQPostMergeProof(rigGit, mr)
	if err == nil {
		t.Fatal("proof accepted an unmerged PR")
	}
	if !strings.Contains(err.Error(), "not MERGED") {
		t.Fatalf("error %q does not name the PR state", err)
	}
}

// Post-merge runs while branches are being deleted, so the submitted head is
// routinely unreachable by ref and must be fetched by SHA to be compared.
func TestVerifyMQPostMergeProof_FetchesCommitsMissingLocally(t *testing.T) {
	mr := testMQPostMergeMR()
	rigGit := squashProofGit(mr, "merge999")
	rigGit.present = map[string]bool{}

	if err := verifyMQPostMergeProof(rigGit, mr); err != nil {
		t.Fatalf("squash proof failed for a deleted source branch: %v", err)
	}
	if len(rigGit.fetched) != 2 {
		t.Fatalf("expected both commits fetched by SHA, got %v", rigGit.fetched)
	}
}

// Ancestry stays the primary proof and must not require a PR lookup at all.
func TestVerifyMQPostMergeProof_AncestryStillProvesWithoutPR(t *testing.T) {
	mr := testMQPostMergeMR()
	rigGit := &fakeMQPostMergeGit{reachable: map[string]bool{mr.CommitSHA: true}}

	if err := verifyMQPostMergeProof(rigGit, mr); err != nil {
		t.Fatalf("ancestry proof rejected: %v", err)
	}
	if len(rigGit.lookupCalls) != 0 {
		t.Fatalf("ancestry proof consulted GitHub: %+v", rigGit.lookupCalls)
	}
}

// A failure must report BOTH proofs, so a refinery reading the error can tell a
// severed-ancestry squash from work that genuinely did not land.
func TestVerifyMQPostMergeProof_FailureNamesBothProofs(t *testing.T) {
	mr := testMQPostMergeMR()
	rigGit := &fakeMQPostMergeGit{verifyErr: errors.New("not reachable")}

	err := verifyMQPostMergeProof(rigGit, mr)
	if err == nil {
		t.Fatal("proof accepted an MR with no ancestry and no PR")
	}
	for _, want := range []string{"does not contain submitted head", "no squash-equivalent merge"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}
