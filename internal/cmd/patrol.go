// Package cmd provides CLI commands for the gt tool.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
)

var (
	// Patrol digest flags
	patrolDigestYesterday bool
	patrolDigestDate      string
	patrolDigestDryRun    bool
	patrolDigestVerbose   bool
)

var patrolCmd = &cobra.Command{
	Use:     "patrol",
	GroupID: GroupDiag,
	Short:   "Patrol digest management",
	Long: `Manage patrol cycle digests.

Patrol cycles (Deacon, Witness, Refinery) create ephemeral per-cycle digests.
This command aggregates them into permanent daily summaries.

Examples:
  gt patrol digest --yesterday  # Aggregate yesterday's patrol digests
  gt patrol digest --dry-run    # Preview what would be aggregated`,
}

var patrolDigestCmd = &cobra.Command{
	Use:   "digest",
	Short: "Aggregate patrol cycle digests into a daily summary bead",
	Long: `Aggregate ephemeral patrol cycle digests into a permanent daily summary.

This command is intended to be run by Deacon patrol (daily) or manually.
It queries patrol digests for a target date, creates a single aggregate
"Patrol Report YYYY-MM-DD" bead, then deletes the source digests.

The resulting digest bead is permanent (synced via git) and provides
an audit trail without per-cycle ephemeral pollution.

Examples:
  gt patrol digest --yesterday   # Digest yesterday's patrols (for daily patrol)
  gt patrol digest --date 2026-01-15
  gt patrol digest --yesterday --dry-run`,
	RunE: runPatrolDigest,
}

func init() {
	patrolCmd.AddCommand(patrolDigestCmd)
	patrolCmd.AddCommand(patrolNewCmd)
	patrolCmd.AddCommand(patrolReportCmd)
	rootCmd.AddCommand(patrolCmd)

	// Patrol digest flags
	patrolDigestCmd.Flags().BoolVar(&patrolDigestYesterday, "yesterday", false, "Digest yesterday's patrol cycles")
	patrolDigestCmd.Flags().StringVar(&patrolDigestDate, "date", "", "Digest patrol cycles for specific date (YYYY-MM-DD)")
	patrolDigestCmd.Flags().BoolVar(&patrolDigestDryRun, "dry-run", false, "Preview what would be created without creating")
	patrolDigestCmd.Flags().BoolVarP(&patrolDigestVerbose, "verbose", "v", false, "Verbose output")
}

// PatrolDigest represents the aggregated daily patrol report.
type PatrolDigest struct {
	Date        string             `json:"date"`
	TotalCycles int                `json:"total_cycles"`
	ByRole      map[string]int     `json:"by_role"` // deacon, witness, refinery
	Cycles      []PatrolCycleEntry `json:"cycles"`
}

// PatrolCycleEntry represents a single patrol cycle in the digest.
type PatrolCycleEntry struct {
	ID          string    `json:"id"`
	Role        string    `json:"role"` // deacon, witness, refinery
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	ClosedAt    time.Time `json:"closed_at,omitempty"`
}

// runPatrolDigest aggregates patrol cycle digests into a daily digest bead.
func runPatrolDigest(cmd *cobra.Command, args []string) error {
	// Determine target date
	var targetDate time.Time

	if patrolDigestDate != "" {
		parsed, err := time.Parse("2006-01-02", patrolDigestDate)
		if err != nil {
			return fmt.Errorf("invalid date format (use YYYY-MM-DD): %w", err)
		}
		targetDate = parsed
	} else if patrolDigestYesterday {
		// Use UTC: Dolt stores timestamps in UTC, so date comparisons
		// must use UTC dates to avoid evening PDT mismatches (gt-ty4).
		targetDate = time.Now().UTC().AddDate(0, 0, -1)
	} else {
		return fmt.Errorf("specify --yesterday or --date YYYY-MM-DD")
	}

	dateStr := targetDate.Format("2006-01-02")

	// Idempotency check: see if digest already exists for this date
	existingID, err := findExistingPatrolDigest(dateStr)
	if err != nil {
		// Non-fatal: continue with creation attempt
		if patrolDigestVerbose {
			fmt.Fprintf(os.Stderr, "[patrol] warning: failed to check existing digest: %v\n", err)
		}
	} else if existingID != "" {
		fmt.Printf("%s Patrol digest already exists for %s (bead: %s)\n",
			style.Dim.Render("○"), dateStr, existingID)
		return nil
	}

	// Query ephemeral patrol digest beads for target date
	cycles, err := queryPatrolDigests(targetDate)
	if err != nil {
		return fmt.Errorf("querying patrol digests: %w", err)
	}

	if len(cycles) == 0 {
		fmt.Printf("%s No patrol digests found for %s\n", style.Dim.Render("○"), dateStr)
		return nil
	}

	// Build digest
	digest := PatrolDigest{
		Date:   dateStr,
		Cycles: cycles,
		ByRole: make(map[string]int),
	}

	for _, c := range cycles {
		digest.TotalCycles++
		digest.ByRole[c.Role]++
	}

	if patrolDigestDryRun {
		fmt.Printf("%s [DRY RUN] Would create Patrol Report %s:\n", style.Bold.Render("📊"), dateStr)
		fmt.Printf("  Total cycles: %d\n", digest.TotalCycles)
		fmt.Printf("  By Role:\n")
		roles := make([]string, 0, len(digest.ByRole))
		for role := range digest.ByRole {
			roles = append(roles, role)
		}
		sort.Strings(roles)
		for _, role := range roles {
			fmt.Printf("    %s: %d cycles\n", role, digest.ByRole[role])
		}
		return nil
	}

	// Create permanent digest bead
	digestID, err := createPatrolDigestBead(digest)
	if err != nil {
		return fmt.Errorf("creating digest bead: %w", err)
	}

	// DELETE ONLY WHAT THE AGGREGATE DEMONSTRABLY CONTAINS. The check is by
	// CONTENT, not by trusting the code above: re-read the bead just written and
	// require every cycle id to appear in it. A deleted wisp cannot be recovered
	// (dolt_ignore), so the failure direction here has to be "keep the sources"
	// (gt-fwzgp).
	deletedCount := 0
	if missing, checkErr := digestMissingCycles(digestID, digest.Cycles); checkErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not verify digest %s carries the cycle bodies (%v); sources KEPT\n", digestID, checkErr)
	} else if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "warning: digest %s is missing %d of %d cycles (first: %s); sources KEPT\n",
			digestID, len(missing), len(digest.Cycles), missing[0])
	} else {
		var deleteErr error
		deletedCount, deleteErr = deletePatrolDigests(targetDate)
		if deleteErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to delete some source digests: %v\n", deleteErr)
		}
	}

	fmt.Printf("%s Created Patrol Report %s (bead: %s)\n", style.Success.Render("✓"), dateStr, digestID)
	fmt.Printf("  Total: %d cycles\n", digest.TotalCycles)
	for role, count := range digest.ByRole {
		fmt.Printf("    %s: %d\n", role, count)
	}
	if deletedCount > 0 {
		fmt.Printf("  Deleted %d source digests\n", deletedCount)
	}

	return nil
}

// queryPatrolDigests queries ephemeral patrol digest beads for a target date.
func queryPatrolDigests(targetDate time.Time) ([]PatrolCycleEntry, error) {
	// Read the CLOSED PATROL WISPS, which is where cycle summaries live now.
	//
	// This used to query --label=digest, and that label is created only by
	// `gt mol squash` (molecule_lifecycle.go). Patrol stopped calling squash when
	// `gt patrol report` replaced the squash-and-new pattern: report closes the
	// patrol root wisp with the summary and opens the next one, so no digest bead
	// is ever produced and this aggregation could never return anything. Measured
	// town-wide: 0 beads carry the digest label in any status (gt-3uty).
	//
	// --include-infra is LOAD-BEARING: wisps are hidden from bd list by default.
	// Measured on the town store, same predicate, same minute:
	//     without --include-infra      1 matching row
	//     with    --include-infra   1256 matching rows
	listCmd := exec.Command("bd", "list",
		"--status=closed",
		"--include-infra",
		"--json",
		"--limit=0", // Get all
	)
	listOutput, err := listCmd.Output()
	if err != nil {
		if patrolDigestVerbose {
			fmt.Fprintf(os.Stderr, "[patrol] bd list failed: %v\n", err)
		}
		return nil, nil
	}

	var issues []struct {
		ID          string    `json:"id"`
		Title       string    `json:"title"`
		Description string    `json:"description"`
		Status      string    `json:"status"`
		CreatedAt   time.Time `json:"created_at"`
		ClosedAt    time.Time `json:"closed_at"`
		Ephemeral   bool      `json:"ephemeral"`
	}

	if err := json.Unmarshal(listOutput, &issues); err != nil {
		return nil, fmt.Errorf("parsing issue list: %w", err)
	}

	// Compare dates in UTC: Dolt stores timestamps in UTC (gt-ty4).
	targetDay := targetDate.UTC().Format("2006-01-02")
	var patrolDigests []PatrolCycleEntry

	for _, issue := range issues {
		// Only process ephemeral patrol digests
		if !issue.Ephemeral {
			continue
		}

		if !isPatrolCycleTitle(issue.Title) {
			continue
		}

		// A cycle belongs to the day it ENDED, not the day it began. Patrol wisps
		// routinely span midnight — and a role with a quiet rig can hold one open
		// for days — so anchoring on CreatedAt files those cycles under a day
		// nobody aggregates again, or under no day at all. Measured on this store:
		// three surviving wisps closed on 2026-09-16 were created on 09-15 and
		// 09-12, and the creation anchor matched none of them (gt-3uty).
		day := issue.ClosedAt
		if day.IsZero() {
			day = issue.CreatedAt
		}
		if day.UTC().Format("2006-01-02") != targetDay {
			continue
		}

		// Extract role from title (e.g., "Digest: mol-deacon-patrol" -> "deacon")
		role := extractPatrolRole(issue.Title)

		patrolDigests = append(patrolDigests, PatrolCycleEntry{
			ID:          issue.ID,
			Role:        role,
			Title:       issue.Title,
			Description: issue.Description,
			CreatedAt:   issue.CreatedAt,
			ClosedAt:    issue.ClosedAt,
		})
	}

	return patrolDigests, nil
}

// isPatrolCycleTitle reports whether a bead title names a patrol cycle record.
//
// Two shapes are accepted because two producers have existed: the legacy squash
// digest ("Digest: mol-deacon-patrol") and the patrol root wisp that replaced it
// ("mol-deacon-patrol"). Accepting both means an aggregation run over a window
// that spans the change does not silently lose half its input.
func isPatrolCycleTitle(title string) bool {
	title = strings.TrimPrefix(title, "Digest: ")
	return strings.HasPrefix(title, "mol-") && strings.HasSuffix(title, "-patrol")
}

// extractPatrolRole extracts the role from a patrol digest title.
// "Digest: mol-deacon-patrol" -> "deacon"
// "Digest: mol-witness-patrol" -> "witness"
// "Digest: gt-wisp-abc123" -> "unknown"
func extractPatrolRole(title string) string {
	// Remove "Digest: " prefix
	title = strings.TrimPrefix(title, "Digest: ")

	// Extract role from "mol-<role>-patrol" or "gt-wisp-<id>"
	if strings.HasPrefix(title, "mol-") && strings.HasSuffix(title, "-patrol") {
		// "mol-deacon-patrol" -> "deacon"
		role := strings.TrimPrefix(title, "mol-")
		role = strings.TrimSuffix(role, "-patrol")
		return role
	}

	// For wisp digests, try to extract from description or return generic
	return "patrol"
}

// digestMissingCycles re-reads the digest bead and returns the ids of cycles it
// does not mention. Verification is by reading back what was WRITTEN rather than
// by trusting what was built, because the consequence of being wrong is the
// permanent loss of every source.
func digestMissingCycles(digestID string, cycles []PatrolCycleEntry) ([]string, error) {
	out, err := exec.Command("bd", "show", digestID, "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("reading back digest %s: %w", digestID, err)
	}
	// bd show --json returns an ARRAY (be-73x); accept either shape.
	var many []struct {
		Description string `json:"description"`
	}
	body := ""
	if err := json.Unmarshal(out, &many); err == nil {
		if len(many) == 0 {
			return nil, fmt.Errorf("digest %s read back empty", digestID)
		}
		body = many[0].Description
	} else {
		var one struct {
			Description string `json:"description"`
		}
		if err2 := json.Unmarshal(out, &one); err2 != nil {
			return nil, fmt.Errorf("parsing digest %s: %w", digestID, err2)
		}
		body = one.Description
	}

	var missing []string
	for _, c := range cycles {
		if !strings.Contains(body, c.ID) {
			missing = append(missing, c.ID)
		}
	}
	return missing, nil
}

// digestPayloadWithoutBodies returns the digest with each cycle's Description
// cleared. See the call site for why: one argv entry cannot carry a day of
// summaries, and the description already does.
func digestPayloadWithoutBodies(digest PatrolDigest) PatrolDigest {
	out := digest
	out.Cycles = make([]PatrolCycleEntry, len(digest.Cycles))
	for i, c := range digest.Cycles {
		c.Description = ""
		out.Cycles[i] = c
	}
	return out
}

// createPatrolDigestBead creates a permanent bead for the daily patrol digest.
func createPatrolDigestBead(digest PatrolDigest) (string, error) {
	// Build description with aggregate data
	var desc strings.Builder
	desc.WriteString(fmt.Sprintf("Daily patrol aggregate for %s.\n\n", digest.Date))
	desc.WriteString(fmt.Sprintf("**Total Cycles:** %d\n\n", digest.TotalCycles))

	if len(digest.ByRole) > 0 {
		desc.WriteString("## By Role\n")
		roles := make([]string, 0, len(digest.ByRole))
		for role := range digest.ByRole {
			roles = append(roles, role)
		}
		sort.Strings(roles)
		for _, role := range roles {
			desc.WriteString(fmt.Sprintf("- %s: %d cycles\n", role, digest.ByRole[role]))
		}
		desc.WriteString("\n")
	}

	// EMBED EVERY CYCLE'S BODY IN THE DESCRIPTION BEFORE ANYTHING IS DELETED.
	//
	// This aggregation deletes its sources, and the sources are wisps, which are
	// in dolt_ignore and therefore never committed — deletion is permanent with
	// no AS OF to recover from. Until this was added the digest kept only counts,
	// so the first successful run in this command's life destroyed 117 cycle
	// summaries and replaced them with 137 bytes of totals (gt-fwzgp).
	desc.WriteString("\n## Cycles\n\n")
	for _, c := range digest.Cycles {
		desc.WriteString(fmt.Sprintf("### %s — %s (%s)\n\n", c.CreatedAt.UTC().Format("15:04Z"), c.Role, c.ID))
		body := strings.TrimSpace(c.Description)
		if body == "" {
			body = "_(no summary recorded on this cycle)_"
		}
		desc.WriteString(body)
		desc.WriteString("\n\n")
	}

	// Build payload JSON with cycle details, WITHOUT each cycle's body.
	//
	// A single argv entry is capped (MAX_ARG_STRLEN, 128KB on Linux) independently
	// of the total, and one day's cycles carry roughly 700 bytes of summary each:
	// 117 of them on the day this was written, which blows the cap on its own and
	// fails as "argument list too long". The bodies are already in the description
	// above, so the payload keeps identity and timing and drops the duplicate
	// prose rather than truncating it (gt-3uty).
	payloadJSON, err := json.Marshal(digestPayloadWithoutBodies(digest))
	if err != nil {
		return "", fmt.Errorf("marshaling digest payload: %w", err)
	}

	// Create the digest bead (NOT ephemeral - this is permanent)
	title := fmt.Sprintf("Patrol Report %s", digest.Date)
	// The description is passed on STDIN, not in argv. A day's aggregate carries
	// every cycle's summary, so as an argument it exceeds ARG_MAX and bd fails
	// with "argument list too long" — which nobody had seen, because until the
	// query above was fixed this function never had any input to aggregate
	// (gt-3uty). 117 cycles on the day this was written.
	bdArgs := []string{
		"create",
		"--type=event",
		"--title=" + title,
		"--event-category=patrol.digest",
		"--event-payload=" + string(payloadJSON),
		"--stdin",
		"--silent",
	}

	bdCmd := exec.Command("bd", bdArgs...)
	bdCmd.Stdin = strings.NewReader(desc.String())
	output, err := bdCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("creating digest bead: %w\nOutput: %s", err, string(output))
	}

	digestID := strings.TrimSpace(string(output))

	// Auto-close the digest (it's an audit record, not work)
	closeCmd := exec.Command("bd", "close", digestID, "--reason=daily patrol digest")
	_ = closeCmd.Run() // Best effort

	return digestID, nil
}

// findExistingPatrolDigest checks if a patrol digest already exists for the given date.
// Returns the bead ID if found, empty string if not found.
func findExistingPatrolDigest(dateStr string) (string, error) {
	expectedTitle := fmt.Sprintf("Patrol Report %s", dateStr)

	// Query event beads with patrol.digest category
	listCmd := exec.Command("bd", "list",
		"--type=event",
		"--json",
		"--limit=50", // Recent events only
	)
	listOutput, err := listCmd.Output()
	if err != nil {
		return "", err
	}

	var events []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}

	if err := json.Unmarshal(listOutput, &events); err != nil {
		return "", err
	}

	for _, evt := range events {
		if evt.Title == expectedTitle {
			return evt.ID, nil
		}
	}

	return "", nil
}

// deletePatrolDigests deletes ephemeral patrol digest beads for a target date.
func deletePatrolDigests(targetDate time.Time) (int, error) {
	// Query patrol digests for the target date
	cycles, err := queryPatrolDigests(targetDate)
	if err != nil {
		return 0, err
	}

	if len(cycles) == 0 {
		return 0, nil
	}

	// Collect IDs to delete
	var idsToDelete []string
	for _, cycle := range cycles {
		idsToDelete = append(idsToDelete, cycle.ID)
	}

	// Delete in batch
	deleteArgs := append([]string{"delete", "--force"}, idsToDelete...)
	deleteCmd := exec.Command("bd", deleteArgs...)
	if err := deleteCmd.Run(); err != nil {
		return 0, fmt.Errorf("deleting patrol digests: %w", err)
	}

	return len(idsToDelete), nil
}
