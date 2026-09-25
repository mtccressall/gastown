package daemon

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/constants"
	gtgit "github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/util"
)

const (
	defaultCheckpointDogInterval = 10 * time.Minute
)

// CheckpointDogConfig holds configuration for the checkpoint_dog patrol.
type CheckpointDogConfig struct {
	// Enabled controls whether the checkpoint dog runs.
	Enabled bool `json:"enabled"`

	// IntervalStr is how often to run, as a string (e.g., "10m").
	IntervalStr string `json:"interval,omitempty"`
}

// checkpointDogInterval returns the configured interval, or the default (10m).
func checkpointDogInterval(config *DaemonPatrolConfig) time.Duration {
	if config != nil && config.Patrols != nil && config.Patrols.CheckpointDog != nil {
		if config.Patrols.CheckpointDog.IntervalStr != "" {
			if d, err := time.ParseDuration(config.Patrols.CheckpointDog.IntervalStr); err == nil && d > 0 {
				return d
			}
		}
	}
	return defaultCheckpointDogInterval
}

// runCheckpointDog auto-commits WIP changes in active polecat worktrees.
// This protects against data loss when sessions crash or hit context limits.
//
// ## ZFC Exemption
// The checkpoint dog executes git operations directly (same pattern as
// compactor_dog's SQL operations). The daemon pours a molecule for
// observability, then runs git commands via exec.Command.
func (d *Daemon) runCheckpointDog() {
	if !d.isPatrolActive("checkpoint_dog") {
		return
	}

	d.logger.Printf("checkpoint_dog: starting cycle")

	mol := d.pourDogMolecule(constants.MolDogCheckpoint, nil)
	defer mol.close()

	rigs := d.getKnownRigs()
	totalScanned := 0
	totalCheckpointed := 0

	for _, rigName := range rigs {
		scanned, checkpointed := d.checkpointRigPolecats(rigName)
		totalScanned += scanned
		totalCheckpointed += checkpointed
	}

	mol.closeStep("scan")
	mol.closeStep("checkpoint")

	d.logger.Printf("checkpoint_dog: cycle complete — scanned %d worktrees, checkpointed %d",
		totalScanned, totalCheckpointed)
	mol.closeStep("report")
}

// checkpointRigPolecats checkpoints dirty polecat worktrees in a single rig.
// Returns (scanned, checkpointed) counts.
func (d *Daemon) checkpointRigPolecats(rigName string) (int, int) {
	polecatsDir := filepath.Join(d.config.TownRoot, rigName, "polecats")
	polecats, err := listPolecatWorktrees(polecatsDir)
	if err != nil {
		return 0, 0
	}

	scanned := 0
	checkpointed := 0

	for _, polecatName := range polecats {
		scanned++

		// Check if tmux session is alive — only checkpoint active sessions.
		// Dead sessions can't benefit from checkpoints.
		sessionName := session.PolecatSessionName(session.PrefixFor(rigName), polecatName)
		alive, err := d.tmux.HasSession(sessionName)
		if err != nil {
			d.logger.Printf("checkpoint_dog: error checking session %s: %v", sessionName, err)
			continue
		}
		if !alive {
			continue
		}

		// Polecat layout: prefer <polecatsDir>/<name>/<rigName>/ (the new
		// nested layout where the outer <name>/ dir is a container with
		// per-polecat scaffolding and the inner dir is the actual git
		// worktree). Fall back to <polecatsDir>/<name>/ for the legacy
		// flat layout still supported by polecat.Manager. Both candidates
		// must contain `.git` — never fall back to a parent dir, since
		// the original bug here was exactly that: an empty <name>/
		// container caused git to walk up to the top-level workspace's
		// .git and commit "WIP: checkpoint (auto)" on the workspace's
		// branch (usually main) instead of the polecat's branch.
		// (gt-checkpoint-workdir fix.)
		workDir := resolveCheckpointWorkDir(polecatsDir, polecatName, rigName)
		if workDir == "" {
			continue // Neither layout has a usable .git — skip silently.
		}
		if d.checkpointWorktree(workDir, rigName, polecatName) {
			checkpointed++
		}
	}

	return scanned, checkpointed
}

// checkpointWorktree creates a WIP checkpoint commit for a single worktree.
// Returns true if a checkpoint was created.
func (d *Daemon) checkpointWorktree(workDir, rigName, polecatName string) bool {
	// Check git status (exclude runtime dirs from consideration)
	statusOut, err := runGitCmd(workDir, "status", "--porcelain")
	if err != nil {
		d.logger.Printf("checkpoint_dog: git status failed in %s/%s: %v", rigName, polecatName, err)
		return false
	}
	if strings.TrimSpace(statusOut) == "" {
		return false // Clean worktree
	}

	// Stage everything
	branch, err := runGitCmd(workDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || strings.TrimSpace(branch) == "" || strings.TrimSpace(branch) == "HEAD" {
		// Detached or unreadable: a checkpoint keyed on a branch name would be
		// unfindable, and an unfindable checkpoint is not a safety net.
		d.logger.Printf("checkpoint_dog: no branch in %s/%s, skipping checkpoint", rigName, polecatName)
		return false
	}
	branch = strings.TrimSpace(branch)

	commit, ok := d.writeCheckpointToRef(workDir, branch, rigName, polecatName)
	if !ok {
		return false
	}
	// SAY WHERE IT WENT. A checkpoint nobody can find is not draft recovery —
	// the ref is invisible in `git log` and in the branch, so this line is the
	// only thing standing between "preserved" and "indistinguishable from lost".
	d.logger.Printf("checkpoint_dog: checkpoint %s saved OFF-BRANCH for %s/%s at %s — recover with: git -C <worktree> checkout %s -- .",
		commit[:min(len(commit), 12)], rigName, polecatName, checkpointRef(branch), checkpointRef(branch))
	d.logger.Printf("checkpoint_dog: created WIP checkpoint in %s/%s", rigName, polecatName)
	return true
}

// checkpointRef is where a worktree's auto-checkpoint lives: OFF the branch.
//
// A checkpoint commit ON the branch is permanent, travels with any push, and
// cannot be collapsed afterwards — measured on one live review candidate,
// 28 checkpoints among 100 commits, in 26 separate runs. That last number is
// the one that matters: 24 were SINGLETONS, because this dog fires every 10
// minutes and the agent commits real work in between, so checkpoints are
// isolated BY CONSTRUCTION. Every adjacency-based remedy therefore reaches
// ~2 of 28, and dropping them and replaying FAILS with conflicts because the
// replay depends on intermediate state (gt-hgwls).
//
// So the checkpoint is not put on the branch at all. refs/checkpoints/<branch>
// holds a commit object that no branch references: it is never pushed, never
// reviewed, never needs collapsing, and no history is ever rewritten.
const checkpointMessage = "WIP: checkpoint (auto)"

func checkpointRef(branch string) string { return "refs/checkpoints/" + branch }

// writeCheckpointToRef builds the checkpoint in a TEMPORARY index and stores it
// as a commit object under refs/checkpoints/<branch>.
//
// Nothing on the branch moves, and — unlike the version this replaces — the
// agent's OWN INDEX is not touched either. The old path ran `git add -A`
// against the real index, so a checkpoint silently staged the agent's work
// underneath it. Untracked files are still captured, which `git stash create`
// would have dropped.
func (d *Daemon) writeCheckpointToRef(workDir, branch, rigName, polecatName string) (string, bool) {
	idx := filepath.Join(workDir, ".git", "gt-checkpoint-index")
	_ = os.Remove(idx)
	defer func() { _ = os.Remove(idx) }()
	idxEnv := []string{"GIT_INDEX_FILE=" + idx}

	run := func(args ...string) (string, bool) {
		out, err := runGitCmdRawEnv(workDir, idxEnv, args...)
		if err != nil {
			d.logger.Printf("checkpoint_dog: git %s failed in %s/%s: %v", args[0], rigName, polecatName, err)
			return "", false
		}
		return strings.TrimSpace(out), true
	}

	if _, ok := run("read-tree", "HEAD"); !ok {
		return "", false
	}
	if _, ok := run("add", "-A"); !ok {
		return "", false
	}

	stagedOut, err := runGitCmdRawEnv(workDir, idxEnv, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		d.logger.Printf("checkpoint_dog: git diff --cached failed in %s/%s: %v", rigName, polecatName, err)
		return "", false
	}
	for _, pathspec := range gtgit.RuntimeArtifactPathspecs(splitNullSeparatedPaths(stagedOut)) {
		if _, ok := run("reset", "HEAD", "--", pathspec); !ok {
			return "", false
		}
	}
	if delOut, err := runGitCmdRawEnv(workDir, idxEnv, "diff", "--cached", "--name-only", "--diff-filter=D"); err == nil {
		for _, f := range strings.Split(strings.TrimSpace(delOut), "\n") {
			if f != "" {
				_, _ = run("reset", "HEAD", "--", f)
			}
		}
	}

	tree, ok := run("write-tree")
	if !ok {
		return "", false
	}
	headTree, err := runGitCmd(workDir, "rev-parse", "HEAD^{tree}")
	if err == nil && strings.TrimSpace(headTree) == tree {
		return "", false // nothing but excluded runtime noise
	}

	// commit-tree, not commit: this produces a commit object that NO branch
	// points at. The identity is the polecat's, for the same reason the branch
	// version needed it — the object carries an author either way.
	name, email := checkpointCommitIdentity(rigName, polecatName)
	commit, err := runGitCmdAs(workDir, name, email, "commit-tree", tree, "-p", "HEAD", "-m", checkpointMessage)
	if err != nil {
		d.logger.Printf("checkpoint_dog: git commit-tree failed in %s/%s: %v", rigName, polecatName, err)
		return "", false
	}
	commit = strings.TrimSpace(commit)
	if _, err := runGitCmd(workDir, "update-ref", checkpointRef(branch), commit); err != nil {
		d.logger.Printf("checkpoint_dog: update-ref failed in %s/%s: %v", rigName, polecatName, err)
		return "", false
	}
	return commit, true
}

// checkpointCommitIdentity returns the git author for an auto-checkpoint.
//
// MIRRORS internal/cmd/commit.go's identityToEmail, which is the source of
// truth for agent git identity — "gastown/crew/jack" becomes
// "gastown.crew.jack@gastown.local". It is duplicated rather than imported
// because internal/cmd imports internal/daemon, so the daemon cannot import
// back without a cycle. checkpoint_dog_identity_test.go pins the format so the
// two cannot drift silently.
func checkpointCommitIdentity(rigName, polecatName string) (name, email string) {
	name = rigName + "/polecats/" + polecatName
	email = strings.ReplaceAll(name, "/", ".") + "@gastown.local"
	return name, email
}

// isGitWorktree reports whether the given directory is the root of a git
// worktree (has its own `.git` file or directory). Used to guard checkpoint
// commits against the "wrong-dir" failure mode where git operations in a
// non-worktree directory walk up the filesystem tree and commit on the
// parent workspace's branch.
func isGitWorktree(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// resolveCheckpointWorkDir picks the actual git-worktree directory for a
// polecat, supporting both the new nested layout (polecats/<name>/<rigName>/)
// and the legacy flat layout (polecats/<name>/) that polecat.Manager still
// recognizes for backward compatibility. Returns "" if neither candidate is
// a git worktree, in which case the caller MUST skip the polecat — never
// fall back to a parent directory, since git would walk up to the top-level
// workspace's .git and commit on the wrong branch (this is the bug this
// helper exists to prevent).
func resolveCheckpointWorkDir(polecatsDir, polecatName, rigName string) string {
	nested := filepath.Join(polecatsDir, polecatName, rigName)
	if isGitWorktree(nested) {
		return nested
	}
	flat := filepath.Join(polecatsDir, polecatName)
	if isGitWorktree(flat) {
		return flat
	}
	return ""
}

// runGitCmd executes a git command in the given directory and returns stdout.
func runGitCmd(workDir string, args ...string) (string, error) {
	out, err := runGitCmdRaw(workDir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// runGitCmdAs runs git with an explicit author AND committer.
//
// `-c user.name=` IS NOT ENOUGH, and a test caught that rather than a review:
// GIT_AUTHOR_NAME outranks config, gastown itself exports it (handoff.go:923),
// and the daemon inherits whatever its own environment carries. The first
// version of this fix passed -c and produced
//
//	deacon <liveop.polecats.atom@gastown.local>
//
// the email applied, the NAME taken from an inherited GIT_AUTHOR_NAME. Setting
// all four variables puts the identity at the precedence level that wins.
func runGitCmdAs(workDir, name, email string, args ...string) (string, error) {
	return runGitCmdRawEnv(workDir, []string{
		"GIT_AUTHOR_NAME=" + name,
		"GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name,
		"GIT_COMMITTER_EMAIL=" + email,
	}, args...)
}

func runGitCmdRaw(workDir string, args ...string) (string, error) {
	return runGitCmdRawEnv(workDir, nil, args...)
}

func runGitCmdRawEnv(workDir string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workDir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	util.SetDetachedProcessGroup(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("%s: %s", err, errMsg)
		}
		return "", err
	}

	return stdout.String(), nil
}

func splitNullSeparatedPaths(out string) []string {
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\x00")
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			paths = append(paths, part)
		}
	}
	return paths
}
