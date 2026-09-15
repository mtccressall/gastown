package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/workspace"
)

var tapPolecatStopCmd = &cobra.Command{
	Use:   "polecat-stop-check",
	Short: "Remind a polecat to run gt done when it stops with pending work",
	Long: `Safety net for the "idle polecat" problem: polecats that finish work
but forget to call gt done.

This command is designed to run from a Claude Code Stop hook. It checks:
1. Whether this is a polecat session (GT_POLECAT env var)
2. Whether gt done has already run (heartbeat state is "exiting" or "idle")
3. Whether the polecat has commits, stashes, or non-runtime dirty work

If the polecat has pending work that wasn't submitted, this command blocks
the stop once and tells the polecat to run gt done if its work is finished.
It never runs gt done itself: Claude Code fires Stop at the end of every
turn, not only when a session ends, so a polecat that ends a turn to wait
for background tests or gates also has "pending work". Running gt done for
it closed the bead and tore the session down mid-gates (gt-e3upy).

When the stop was already blocked once (stop_hook_active in the hook
input), the stop is allowed, so a polecat that is waiting is not looped.

Output: nothing when the stop is allowed (not a polecat, already done, nothing
pending, or already reminded). When blocked, a Stop hook decision on stdout:
  {"decision":"block","reason":"<reminder for the polecat>"}
Always exits 0.`,
	RunE:         runTapPolecatStop,
	SilenceUsage: true,
}

func init() {
	tapCmd.AddCommand(tapPolecatStopCmd)
}

func runTapPolecatStop(cmd *cobra.Command, args []string) error {
	// Only applies to polecats
	polecatName := os.Getenv("GT_POLECAT")
	if polecatName == "" {
		return nil // Not a polecat session — nothing to do
	}

	sessionName := os.Getenv("GT_SESSION")
	if sessionName == "" {
		return nil // No session tracking — can't check state
	}

	// Find town root for heartbeat check
	townRoot, _, _ := workspace.FindFromCwdWithFallback()
	if townRoot == "" {
		townRoot = os.Getenv("GT_TOWN_ROOT")
	}
	if townRoot == "" {
		return nil // Can't find workspace — exit quietly
	}

	// Check heartbeat state: if already "exiting" or "idle", gt done already ran
	hb := polecat.ReadSessionHeartbeat(townRoot, sessionName)
	if hb != nil {
		state := hb.EffectiveState()
		if state == polecat.HeartbeatExiting || state == polecat.HeartbeatIdle {
			return nil // gt done already ran or polecat is idle — nothing to do
		}
	}

	// Check if the polecat is on a feature branch with work to submit.
	rigName := os.Getenv("GT_RIG")
	if rigName == "" {
		return nil
	}

	// Reconstruct polecat worktree path
	polecatDir := filepath.Join(townRoot, rigName, "polecats", polecatName)
	// Try the nested clone layout first (polecats/<name>/<rig>/)
	cloneDir := filepath.Join(polecatDir, rigName)
	if _, err := os.Stat(filepath.Join(cloneDir, ".git")); err != nil {
		// Fall back to flat layout
		cloneDir = polecatDir
		if _, err := os.Stat(filepath.Join(cloneDir, ".git")); err != nil {
			return nil // No git repo found — exit quietly
		}
	}

	// Check current branch — skip if on main/master
	branchCmd := exec.Command("git", "-C", cloneDir, "rev-parse", "--abbrev-ref", "HEAD")
	branchOut, err := branchCmd.Output()
	if err != nil {
		return nil // Can't determine branch — exit quietly
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "main" || branch == "master" || branch == "HEAD" {
		return nil // On default branch — nothing to submit
	}

	pending, reason, err := polecatStopPendingWork(cloneDir, branch)
	if err != nil || !pending {
		return nil // Can't check, or no work to submit — don't block session stop
	}

	input, _ := io.ReadAll(os.Stdin)
	if !polecatStopShouldBlock(pending, stopHookActive(input)) {
		return nil // Already reminded this stop — let the polecat wait
	}

	out, err := json.Marshal(map[string]string{
		"decision": "block",
		"reason":   polecatStopReminder(polecatName, branch, reason),
	})
	if err != nil {
		return nil
	}
	fmt.Println(string(out))
	return nil
}

// stopHookActive reports whether Claude Code is already continuing because a
// Stop hook blocked the previous stop. Unparseable input reads as false.
func stopHookActive(input []byte) bool {
	var payload struct {
		StopHookActive bool `json:"stop_hook_active"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return false
	}
	return payload.StopHookActive
}

// polecatStopShouldBlock decides whether a stop with pending work is blocked
// with a reminder. There is deliberately no outcome that runs gt done: a stop
// is a turn end, not proof the polecat is finished (gt-e3upy).
func polecatStopShouldBlock(pending, alreadyReminded bool) bool {
	return pending && !alreadyReminded
}

func polecatStopReminder(polecatName, branch, reason string) string {
	return fmt.Sprintf(`Polecat %s has pending work on branch %s (%s) and gt done has not run.
If your work is finished and its gates have passed, run gt done now.
If you are waiting on background tests, gates or reviews, keep waiting and do not run gt done yet; you may end your turn.
gt done will not be run for you.
`, polecatName, branch, reason)
}

func polecatStopPendingWork(cloneDir, branch string) (bool, string, error) {
	g := git.NewGit(cloneDir)
	workStatus, err := g.CheckUncommittedWork()
	if err != nil {
		return false, "", err
	}

	if workStatus.HasUncommittedChanges && !workStatus.CleanExcludingRuntime() {
		return true, fmt.Sprintf("%d non-runtime dirty file(s)", len(workStatus.NonRuntimePaths())), nil
	}
	if workStatus.StashCount > 0 {
		return true, fmt.Sprintf("%d branch stash(es)", workStatus.StashCount), nil
	}

	targetStatus, err := g.BranchTargetStatus(branch, "origin", nil)
	if err != nil {
		return false, "", err
	}
	if !targetStatus.Preserved && targetStatus.UnpreservedPatchCount > 0 {
		return true, fmt.Sprintf("%d unsubmitted commit(s)", targetStatus.UnpreservedPatchCount), nil
	}

	return false, "", nil
}
