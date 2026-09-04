package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/tmux"
)

var polecatReleaseForce bool

// gt polecat release exists because a dead polecat could hold the entire pool and
// there was no supported way to let go.
//
// Measured 2026-09-03 on liveop: three polecats with no tmux session held capacity
// at 0 free of 4 while TWELVE sat reusable and idle. vault was hooked to
// liveop-ag4, which reads assignee=None; raider was hooked to liveop-265, which is
// assigned to pipboy — two polecats claiming one piece of work. Every claim was
// contradicted by the beads themselves, and nothing could be dispatched.
//
// The options at the time were `gt polecat nuke`, which destroys the session,
// worktree, branch AND agent bead in order to clear a stale field, or a
// hand-written UPDATE against the data plane. Both are wrong answers to "let go of
// work you no longer own".
//
// This clears exactly two fields and touches nothing else. The worktree, branch
// and agent bead all survive, which is what makes it reversible — and being
// reversible is why it can afford to refuse conservatively.
var polecatReleaseCmd = &cobra.Command{
	Use:   "release <rig>/<polecat>",
	Short: "Release a stopped polecat's stale hook so it stops holding capacity",
	Long: `Clear hook_bead and reset agent_state on a polecat that has stopped.

Unlike nuke, this destroys NOTHING: the worktree, branch and agent bead are left
in place, so the work stays recoverable and the release is undone by re-slinging.

It REFUSES unless all three hold, and names the one that failed:

  1. the tmux session is gone     — a live agent may still be working
  2. the worktree holds nothing   — no modified, untracked (matched by NAME, not
                                    counted), unmerged or stashed files, no
                                    unpushed commits, and the branch is on origin
  3. the hook is genuinely stale  — the hooked bead is terminal or does not point
                                    back at this polecat

Gate 2 is the one that matters, and it is never skippable. A filtered or
count-based worktree check reads clean at exactly the moment it is blindest, which
is how a 295-line security test in this town came within one command of being
destroyed. This uses the same live predicate the recovery path uses.

--force relaxes gates 1 and 3. Nothing relaxes gate 2.`,
	Args: cobra.ExactArgs(1),
	RunE: runPolecatRelease,
}

func runPolecatRelease(cmd *cobra.Command, args []string) error {
	rigName, polecatName, err := parseAddress(args[0])
	if err != nil {
		return err
	}
	mgr, r, err := getPolecatManager(rigName)
	if err != nil {
		return err
	}
	if mgr == nil || r == nil {
		return fmt.Errorf("rig %q not found", rigName)
	}
	info, err := mgr.Get(polecatName)
	if err != nil {
		return fmt.Errorf("polecat %s/%s: %w", rigName, polecatName, err)
	}

	// --- gate 1: the session must be gone ---------------------------------
	sessionNames, sErr := tmux.NewTmux().ListSessions()
	if sErr != nil {
		// Unverifiable. Refuse rather than guess — an indeterminate check must
		// not be recorded as a definite negative (gt-365, gt-28k).
		return fmt.Errorf("cannot list tmux sessions (%v); refusing to release without knowing whether the agent is alive", sErr)
	}
	if session, live := newPolecatSessionSet(sessionNames).lookup(rigName, polecatName); live && !polecatReleaseForce {
		return fmt.Errorf("session %s is still running; stop the agent first, or pass --force if you are certain it is wedged", session)
	}

	// --- gate 2: the worktree must hold nothing ----------------------------
	// Never skippable, --force included.
	if !activeMRGitSafeForWorktree(info.ClonePath) {
		return fmt.Errorf("worktree holds work that is not on origin; release refused\n"+
			"  inspect: git -C %s status --porcelain\n"+
			"  push or commit anything worth keeping first — --force will NOT bypass this",
			info.ClonePath)
	}

	// --- gate 3: the hook must be genuinely stale --------------------------
	bd := beads.New(r.Path)
	agentBeadID := polecatBeadIDForRig(r, rigName, polecatName)
	_, fields, err := bd.GetAgentBead(agentBeadID)
	if err != nil {
		return fmt.Errorf("reading agent bead %s: %w", agentBeadID, err)
	}
	hook := ""
	state := ""
	if fields != nil {
		hook = strings.TrimSpace(fields.HookBead)
		state = strings.TrimSpace(fields.AgentState)
	}
	if hook != "" && !polecatReleaseForce {
		// The question is OWNERSHIP, not completion.
		//
		// The first version of this gate reused hookBeadSafeForCleanup, which
		// answers "is this hooked work finished?" — a neighbouring question, and
		// the wrong one. It refused to release vault because liveop-ag4 was
		// status=open, when ag4's assignee was already nobody: the work had been
		// unhooked and re-slung hours earlier and vault simply had not noticed.
		// Terminality and ownership are different facts, and reusing a predicate
		// because it is nearby is how a check comes to answer confidently about
		// something it was never asked.
		//
		// A hook is stale when the bead no longer points back at this polecat,
		// whether because it is finished, reassigned, or unassigned entirely.
		issue, iErr := bd.Show(hook)
		if iErr != nil {
			return fmt.Errorf("cannot read %s to check whether this hook is stale (%v); refusing\n"+
				"  an unverifiable check must not be treated as permission", hook, iErr)
		}
		if issue == nil {
			return fmt.Errorf("cannot read %s to check whether this hook is stale; refusing", hook)
		}
		owner := strings.TrimSpace(issue.Assignee)
		mine := rigName + "/polecats/" + polecatName
		if owner == mine {
			return fmt.Errorf("%s still names %s as its assignee, so this hook is LIVE, not stale\n"+
				"  unsling the work first, or pass --force to release anyway",
				hook, mine)
		}
		if owner == "" {
			fmt.Printf("  hook %s is stale: the bead has no assignee\n", hook)
		} else {
			fmt.Printf("  hook %s is stale: the bead names %s\n", hook, owner)
		}
	}

	if hook == "" && state != "working" {
		fmt.Printf("  %s/%s already has no hook and state=%q; nothing to release\n", rigName, polecatName, state)
		return nil
	}

	// --- the change: two fields, nothing else ------------------------------
	empty := ""
	idle := "idle"
	if err := bd.UpdateAgentDescriptionFields(agentBeadID, beads.AgentFieldUpdates{
		HookBead:   &empty,
		AgentState: &idle,
	}); err != nil {
		return fmt.Errorf("clearing hook on %s: %w", agentBeadID, err)
	}

	// Read back. A write that reports success without persisting is the defect
	// this whole command exists to clean up after; it must not be the defect the
	// command itself ships.
	// A readback that CANNOT RUN is not a readback that passed. The original
	// version skipped verification on error or nil fields and printed "Released"
	// anyway — reporting success for a write whose persistence is unknown, which
	// is the exact defect this command exists to clean up after. (codex review)
	_, after, vErr := bd.GetAgentBead(agentBeadID)
	if vErr != nil {
		return fmt.Errorf("wrote the clear to %s but could not read it back to verify (%v); treat as NOT released and re-check before relying on it", agentBeadID, vErr)
	}
	if after == nil {
		return fmt.Errorf("wrote the clear to %s but the readback returned no agent bead; treat as NOT released", agentBeadID)
	}
	if strings.TrimSpace(after.HookBead) != "" {
		return fmt.Errorf("wrote the clear but %s still reads hook_bead=%s; NOT released", agentBeadID, after.HookBead)
	}

	fmt.Printf("✓ Released %s/%s\n", rigName, polecatName)
	if hook != "" {
		fmt.Printf("  hook_bead    %s -> (cleared)\n", hook)
	}
	if state != "" {
		fmt.Printf("  agent_state  %s -> idle\n", state)
	}
	fmt.Fprintf(os.Stderr, "  worktree, branch and agent bead untouched; re-sling to put it back to work\n")
	return nil
}

func init() {
	polecatReleaseCmd.Flags().BoolVar(&polecatReleaseForce, "force", false,
		"Relax the session and stale-hook checks. Does NOT relax the worktree check.")
	polecatCmd.AddCommand(polecatReleaseCmd)
}
