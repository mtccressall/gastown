package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/constants"
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

	// Build config based on role
	var cfg PatrolConfig
	switch roleInfo.Role {
	case RoleDeacon:
		cfg = PatrolConfig{
			RoleName:      "deacon",
			PatrolMolName: constants.MolDeaconPatrol,
			BeadsDir:      roleInfo.TownRoot,
			Assignee:      "deacon",
		}
	case RoleWitness:
		cfg = PatrolConfig{
			RoleName:      "witness",
			PatrolMolName: constants.MolWitnessPatrol,
			BeadsDir:      roleInfo.TownRoot,
			Assignee:      roleInfo.Rig + "/witness",
		}
	case RoleRefinery:
		cfg = PatrolConfig{
			RoleName:      "refinery",
			PatrolMolName: constants.MolRefineryPatrol,
			BeadsDir:      roleInfo.TownRoot,
			Assignee:      roleInfo.Rig + "/refinery",
			ExtraVars:     buildRefineryPatrolVars(roleInfo),
		}
	default:
		return fmt.Errorf("unsupported role for patrol report: %q", roleName)
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

	// Close all descendant wisps first (recursive), then the patrol root.
	// Without this, every patrol cycle leaks ~10 orphan wisps into the DB.
	// If descendants can't be closed, abort so patrol retries next cycle (gt-7lx3).
	closed, closeDescErr := forceCloseDescendants(b, patrolID)
	if closeDescErr != nil {
		return fmt.Errorf("closing descendants of patrol %s (closed %d): %w", patrolID, closed, closeDescErr)
	}

	// Close the patrol root
	if err := b.ForceCloseWithReason("patrol cycle complete: "+patrolReportSummary, patrolID); err != nil {
		return fmt.Errorf("closing patrol %s: %w", patrolID, err)
	}

	fmt.Printf("%s Closed patrol %s\n", style.Success.Render("✓"), patrolID)

	// Start next cycle
	newPatrolID, err := autoSpawnPatrol(cfg)
	if err != nil {
		if newPatrolID != "" {
			fmt.Fprintf(os.Stderr, "warning: %s\n", err.Error())
			fmt.Printf("New patrol: %s\n", newPatrolID)
			return nil
		}
		return fmt.Errorf("starting next patrol cycle: %w", err)
	}

	fmt.Printf("%s Started new patrol: %s\n", style.Success.Render("✓"), newPatrolID)
	if cfg.RoleName == "deacon" {
		stampDeaconHeartbeatOnReport(cfg.BeadsDir, patrolReportSummary)
	}
	return nil
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
