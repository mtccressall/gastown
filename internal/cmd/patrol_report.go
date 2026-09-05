package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/deacon"
	"github.com/steveyegge/gastown/internal/formula"
	"github.com/steveyegge/gastown/internal/style"
)

var (
	patrolReportSummary string
	patrolReportSteps   string
)

var patrolReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Close patrol cycle with summary and start next cycle",
	Long: `Close the current patrol cycle, recording a summary of observations,
then automatically start a new patrol cycle.

This replaces the old squash+new pattern with a single command that:
  1. Closes the current patrol root wisp with the summary
  2. Creates a new patrol wisp for the next cycle

The summary is stored on the patrol root wisp for audit purposes.
The --steps flag records which patrol steps were executed vs skipped,
making shortcutting visible in the ledger.

Examples:
  gt patrol report --summary "All clear, no issues" --steps "heartbeat:OK,inbox-check:OK,health-scan:OK"
  gt patrol report --summary "Dolt latency elevated, filed escalation"`,
	RunE: runPatrolReport,
	// Print operational errors on their own. Without this cobra follows the
	// error with the Flags block, and the block is what the reader sees: the
	// Deacon that filed gastown-9pc read "no active patrol found for deacon"
	// as a malformed-argument problem and went looking at its own quoting.
	// A usage block in place of an error sends the reader to their input
	// rather than to the state.
	SilenceUsage: true,
}

func init() {
	patrolReportCmd.Flags().StringVar(&patrolReportSummary, "summary", "", "Brief summary of patrol observations (required)")
	patrolReportCmd.Flags().StringVar(&patrolReportSteps, "steps", "", "Step audit: comma-separated step:STATUS pairs (e.g., heartbeat:OK,inbox-check:OK)")
	_ = patrolReportCmd.MarkFlagRequired("summary")
}

func runPatrolReport(cmd *cobra.Command, args []string) error {
	// Resolve role
	roleInfo, err := GetRole()
	if err != nil {
		return fmt.Errorf("detecting role: %w", err)
	}

	roleName := string(roleInfo.Role)

	// Build config through the shared builder so the assignee is derived from
	// buildAgentIdentity -- the same function gt hook queries with. This path
	// used to restate the assignee as a literal "deacon", which is what left
	// gt-7rne alive after PR #15 fixed only gt patrol new: report is the
	// ROUTINE minting path (every cycle of every role ends here) while new is
	// the recovery path, so the broken site was the universal one.
	cfg, err := patrolConfigForRole(roleName, roleInfo)
	if err != nil {
		return fmt.Errorf("patrol report: %w", err)
	}

	// Find the active patrol
	patrolID, _, hasPatrol, findErr := findActivePatrol(cfg)
	if findErr != nil {
		return fmt.Errorf("finding active patrol: %w", findErr)
	}
	if !hasPatrol {
		return fmt.Errorf("no active patrol found for %s", cfg.RoleName)
	}

	// Close the current patrol root with the summary
	b := cfg.Beads
	if b == nil {
		b = beads.New(cfg.BeadsDir)
	}

	// Build step audit checklist. Grade against the SAME formula the patrol was
	// rendered from — town and rig overrides win over the embedded copy — or the
	// audit scores the agent against a checklist it was never shown.
	stepAudit := buildStepAudit(cfg.PatrolMolName, roleInfo.TownRoot, roleInfo.Rig, patrolReportSteps)

	// Update the description with the patrol summary and step audit
	desc := fmt.Sprintf("Patrol report: %s\n\n%s", patrolReportSummary, stepAudit)
	if err := b.Update(patrolID, beads.UpdateOptions{
		Description: &desc,
	}); err != nil {
		style.PrintWarning("could not update patrol summary: %v", err)
	}

	// Print the step audit for visibility
	fmt.Println(stepAudit)

	rotated, err := rotatePatrolCycle(b, cfg, patrolID, patrolReportSummary)
	if err != nil {
		return err
	}
	if rotated && cfg.RoleName == "deacon" {
		stampDeaconHeartbeatOnReport(cfg.BeadsDir, patrolReportSummary)
	}
	return nil
}

// patrolSpawnSuccessor is the successor-minting step of a patrol cycle,
// indirected so tests can exercise the mint/close ORDER without a live
// formula catalog. The order is the whole point of gastown-9pc, and a
// happy-path test cannot see it: it passes against either ordering.
var patrolSpawnSuccessor = autoSpawnPatrol

// rotatePatrolCycle ends one patrol cycle and begins the next. It reports
// whether the rotation completed — a partial mint leaves the outgoing cycle
// open, so the caller must not treat that as a finished report.
//
// THE SUCCESSOR IS MINTED BEFORE THE OUTGOING ROOT IS CLOSED, and the order is
// load-bearing (gastown-9pc / town gt-9dwa). Close and mint cannot be made
// atomic, so the question is only which side of the window a death lands on,
// and the two failure modes are NOT symmetric:
//
//	close-then-mint, death in the window -> ZERO hooked patrol wisps. Terminal.
//	  Nothing recovers it, and the role cannot tell it happened: gt hook and the
//	  town-scoped bd query BOTH report empty, truthfully. A respawning agent
//	  reads that, believes it, and stands down — which looks like diligence.
//	mint-then-close, death in the window -> TWO hooked patrol wisps. Self-healing:
//	  the next cycle's burnPreviousPatrolWisps closes the superseded root by
//	  design (patrol_helpers.go, "burned: replaced by new patrol cycle").
//
// So the reordered failure mode is one the system already handles, and the
// original one is the state this workspace's own banner is written about.
//
// Death is not an error branch, so no amount of error handling closes this
// window — only the order does.
func rotatePatrolCycle(b *beads.Beads, cfg PatrolConfig, patrolID, summary string) (bool, error) {
	// Mint the successor FIRST. The outgoing root is still hooked at this
	// point, so it is excluded from the new cycle's burn — otherwise the burn
	// would close it as "burned: replaced by new patrol cycle" and the summary
	// would never reach close_reason.
	newPatrolID, err := patrolSpawnSuccessor(cfg, patrolID)
	if err != nil {
		if newPatrolID != "" {
			// Created but not hooked. The outgoing root is still hooked, so the
			// role keeps a patrol; leave it open rather than closing it and
			// landing in exactly the hookless state this ordering prevents.
			fmt.Fprintf(os.Stderr, "warning: %s\n", err.Error())
			fmt.Printf("New patrol: %s\n", newPatrolID)
			return false, nil
		}
		return false, fmt.Errorf("starting next patrol cycle: %w", err)
	}

	fmt.Printf("%s Started new patrol: %s\n", style.Success.Render("✓"), newPatrolID)

	// The successor exists and is hooked. Now the outgoing cycle can be closed.
	//
	// Close all descendant wisps first (recursive), then the patrol root.
	// Without this, every patrol cycle leaks ~10 orphan wisps into the DB.
	// If descendants can't be closed, abort so patrol retries next cycle (gt-7lx3).
	closed, closeDescErr := forceCloseDescendants(b, patrolID)
	if closeDescErr != nil {
		rollbackSuccessorPatrol(b, cfg, newPatrolID, patrolID)
		return false, fmt.Errorf("closing descendants of patrol %s (closed %d): %w", patrolID, closed, closeDescErr)
	}

	// Close the patrol root
	if err := b.ForceCloseWithReason("patrol cycle complete: "+summary, patrolID); err != nil {
		rollbackSuccessorPatrol(b, cfg, newPatrolID, patrolID)
		return false, fmt.Errorf("closing patrol %s: %w", patrolID, err)
	}

	fmt.Printf("%s Closed patrol %s\n", style.Success.Render("✓"), patrolID)
	return true, nil
}

// rollbackSuccessorPatrol closes a successor that was minted for a cycle whose
// outgoing root then failed to close.
//
// Without it a failed close ends the call with TWO hooked roots, and the retry
// picks the WRONG one: findActivePatrol takes the first active bead from a list
// mergeBeadLists sorts newest-first, which is the successor. The next cycle then
// reports against the successor and burns the outgoing root as superseded, so
// its close_reason reads "burned: replaced by new patrol cycle" instead of the
// summary that cycle actually produced — and close_reason cannot be amended once
// written. The step audit survives in the outgoing root's description, which is
// written before any of this, so what is lost is the close record rather than
// the whole report; it is still a transient failure turning into a permanent
// wrong answer.
//
// Rolling the successor back restores the invariant the retry depends on —
// exactly one hooked patrol root — and it cannot strand the role, because the
// outgoing root is still hooked at this point (its close is what just failed).
// That keeps gt-7lx3's intent intact: abort, and let the next cycle retry the
// same patrol.
func rollbackSuccessorPatrol(b *beads.Beads, cfg PatrolConfig, newPatrolID, patrolID string) {
	if newPatrolID == "" {
		return
	}
	reason := fmt.Sprintf("rolled back: patrol %s could not be closed, so its cycle must be retried", patrolID)
	closeDescendants(b, newPatrolID)
	if err := b.ForceCloseWithReason(reason, newPatrolID); err != nil {
		// Both roots stay hooked. The role is not stranded, but the next cycle
		// will report against the successor, so say so rather than leaving the
		// operator to infer it from a close_reason months later.
		style.PrintWarning("could not roll back successor patrol %s after %s failed to close: %v; "+
			"two patrols are now hooked for %s and the next cycle will report against %s",
			newPatrolID, patrolID, err, cfg.Assignee, newPatrolID)
	}
}

func stampDeaconHeartbeatOnReport(townRoot, summary string) {
	paused, _, err := deacon.IsPaused(townRoot)
	if err != nil {
		style.PrintWarning("not stamping deacon heartbeat: pause state unreadable: %v", err)
		return
	}
	if paused {
		return
	}

	action := "patrol report"
	if summary = strings.TrimSpace(summary); summary != "" {
		action += ": " + summary
	}
	if err := syncDeaconHeartbeatStores(townRoot, action); err != nil {
		style.PrintWarning("could not stamp deacon heartbeat: %v", err)
	}
}

// buildStepAudit builds a step checklist from the formula's steps and the
// reported step results. Format:
//
//	Steps: heartbeat OK | inbox-check OK | orphan-cleanup SKIP | ... (14/25)
//
// If stepsFlag is empty, returns a line indicating the audit was not reported.
//
// The formula is resolved with the same three-tier precedence gt prime uses to
// RENDER the checklist — rig override, then town override, then embedded. Reading
// the embedded copy directly is the root of gastown-c1x: the Deacon executed the
// town's 28-step formula and was graded against the embedded 26-step fallback, so
// the two ids only the town copy defined were dropped from four cycles with no
// warning, no error, rc=0 and nothing on stderr.
//
// Anything reported that the resolved formula does not define, and anything that
// is not a step:STATUS pair at all, is warned about on stderr AND named in the
// audit line. stderr is gone by the next command; the audit line is what lands in
// the ledger, so both belong there.
func buildStepAudit(formulaName, townRoot, rigName string, stepsFlag string) string {
	// Load the formula to get the canonical step list
	content, err := formula.ResolveFormulaContent(formulaName, townRoot, rigName)
	if err != nil {
		if stepsFlag == "" {
			return "Steps: NOT REPORTED (formula not found)"
		}
		// Can't validate without the formula, but still show what was reported
		return fmt.Sprintf("Steps: %s (unvalidated — formula not found)", stepsFlag)
	}

	f, err := formula.Parse(content)
	if err != nil {
		if stepsFlag == "" {
			return "Steps: NOT REPORTED (formula parse error)"
		}
		return fmt.Sprintf("Steps: %s (unvalidated — formula parse error)", stepsFlag)
	}

	allStepIDs := f.GetAllIDs()
	if len(allStepIDs) == 0 {
		return ""
	}

	if stepsFlag == "" {
		return fmt.Sprintf("Steps: NOT REPORTED (?/%d)", len(allStepIDs))
	}

	// Parse the reported step results
	reported, malformed := parseStepResults(stepsFlag)
	for _, entry := range malformed {
		style.PrintWarning("--steps entry %q is not a step:STATUS pair; it was dropped from the audit", entry)
	}

	// Build the audit line: map each formula step to its reported status
	var parts []string
	okCount := 0
	for _, stepID := range allStepIDs {
		status, ok := reported[stepID]
		if !ok {
			status = "SKIP"
		}
		if status == "OK" {
			okCount++
		}
		parts = append(parts, stepID+" "+status)
	}

	audit := fmt.Sprintf("Steps: %s (%d/%d)", strings.Join(parts, " | "), okCount, len(allStepIDs))

	// Report anything the formula does not define, both on stderr and in the
	// audit line itself — the stderr warning is gone by the next command, but
	// the audit line is what lands in the ledger.
	unrecognized := unrecognizedStepIDs(reported, allStepIDs)
	for _, stepID := range unrecognized {
		style.PrintWarning("--steps reported %q, which is not a step in formula %s; it was dropped from the audit", stepID, formulaName)
	}
	if len(unrecognized) > 0 {
		audit += fmt.Sprintf(" [%d unrecognized: %s]", len(unrecognized), strings.Join(unrecognized, ", "))
	}
	if len(malformed) > 0 {
		audit += fmt.Sprintf(" [%d malformed: %s]", len(malformed), strings.Join(malformed, ", "))
	}

	return audit
}

// unrecognizedStepIDs returns the reported step ids that the formula does not
// define, sorted so the warning order is stable across runs.
func unrecognizedStepIDs(reported map[string]string, allStepIDs []string) []string {
	known := make(map[string]bool, len(allStepIDs))
	for _, stepID := range allStepIDs {
		known[stepID] = true
	}
	var unrecognized []string
	for stepID := range reported {
		if !known[stepID] {
			unrecognized = append(unrecognized, stepID)
		}
	}
	sort.Strings(unrecognized)
	return unrecognized
}

// parseStepResults parses a comma-separated string of step:STATUS pairs.
// Returns a map of step ID to uppercase status, plus every entry that did not
// carry both halves, so the caller can report them rather than drop them
// silently.
//
// An entry counts only if it names a step AND carries a status. "inbox-check",
// "inbox-check:" and ":OK" are all reports of nothing: the first has no status
// at all, the second an empty one that would print as a blank in the audit line,
// and the third an empty step id that would be reported as an unrecognized step
// named "". Splitting on ":" alone accepts the last two, which is the same
// silent-drop this change exists to remove (gastown-c1x).
//
// Example input: "heartbeat:OK,inbox-check:OK,orphan-cleanup:SKIP"
func parseStepResults(stepsFlag string) (map[string]string, []string) {
	results := make(map[string]string)
	var malformed []string
	for _, entry := range strings.Split(stepsFlag, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		stepID, status, found := strings.Cut(entry, ":")
		stepID = strings.TrimSpace(stepID)
		status = strings.TrimSpace(status)
		if !found || stepID == "" || status == "" {
			malformed = append(malformed, entry)
			continue
		}
		results[stepID] = strings.ToUpper(status)
	}
	return results, malformed
}
