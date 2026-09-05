package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/suggest"
	"github.com/steveyegge/gastown/internal/wisp"
)

var (
	compactDryRun  bool
	compactVerbose bool
	compactJSON    bool
	compactRig     string
)

// Default TTLs per wisp type (from design doc WISP-COMPACTION-POLICY.md).
var defaultTTLs = map[string]time.Duration{
	"heartbeat":  6 * time.Hour,
	"ping":       6 * time.Hour,
	"patrol":     24 * time.Hour,
	"gc_report":  24 * time.Hour,
	"recovery":   7 * 24 * time.Hour,
	"error":      7 * 24 * time.Hour,
	"escalation": 7 * 24 * time.Hour,
	"default":    24 * time.Hour,
}

// compactResult tracks what happened to each wisp during compaction.
type compactResult struct {
	// Scope and Store name the store this run actually scanned. Without them a
	// "0 wisps scanned" line is unattributable: a wrong scope and a genuinely
	// empty store print the same report (gt-kei9 / gastown-c62).
	Scope            string          `json:"scope"`
	Store            string          `json:"store"`
	Promoted         []compactAction `json:"promoted"`
	Deleted          []compactAction `json:"deleted"`
	Skipped          int             `json:"skipped"`            // wisps still within TTL
	OrphanedWispDeps int             `json:"orphaned_wisp_deps"` // stale wisp_dependencies removed
	// HiddenWisps counts ephemeral rows that exist in the scanned store but are
	// invisible to the scan query, which omits --include-infra. Only measured
	// when the scan itself came back empty. Reported, never acted on: this run
	// deletes and promotes exactly what it can see, and nothing else.
	HiddenWisps      int      `json:"hidden_wisps"`
	HiddenWispsError string   `json:"hidden_wisps_error,omitempty"`
	Errors           []string `json:"errors,omitempty"`
}

type compactAction struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Reason   string `json:"reason"`
	WispType string `json:"wisp_type,omitempty"`
}

var compactCmd = &cobra.Command{
	Use:     "compact",
	GroupID: GroupWork,
	Short:   "Compact expired wisps (TTL-based cleanup)",
	Long: `Apply TTL-based compaction policy to ephemeral wisps.

For non-closed wisps past TTL: promotes to permanent beads (something is stuck).
For closed wisps past TTL: deletes them (Dolt AS OF preserves history).
Wisps with comments, keep labels, or a "Patrol report:" description are always
promoted: a patrol wisp's description is the only copy of that cycle's summary
and step audit.

TTLs by wisp type:
  heartbeat, ping:              6h
  patrol, gc_report:            24h
  recovery, error, escalation:  7d
  default (untyped):            24h

Examples:
  gt compact              # Run compaction
  gt compact --dry-run    # Preview what would happen
  gt compact --verbose    # Show each wisp decision
  gt compact --json       # Machine-readable output
  gt compact --rig liveop # Compact the liveop rig's store

Scope: --rig names the rig whose store is scanned; without it, $GT_RIG is used,
and without that the store the working directory routes to. An unknown rig name
is an error, never a silent no-op. Every run names the store it scanned.`,
	RunE: runCompact,
}

func init() {
	compactCmd.Flags().BoolVar(&compactDryRun, "dry-run", false, "Preview compaction without making changes")
	compactCmd.Flags().BoolVarP(&compactVerbose, "verbose", "v", false, "Show each wisp decision")
	compactCmd.Flags().BoolVar(&compactJSON, "json", false, "Output results as JSON")
	compactCmd.Flags().StringVar(&compactRig, "rig", "", "Compact a specific rig's store (default: $GT_RIG, else the store the working directory routes to)")

	rootCmd.AddCommand(compactCmd)
}

// loadTTLConfig loads TTL configuration with layered precedence:
//
//	rig config (wisp layer + bead labels) > hardcoded defaults
func loadTTLConfig(townRoot, rigName string) map[string]time.Duration {
	return loadTTLConfigWithRole(townRoot, rigName)
}

// loadTTLConfigWithRole is the testable version of loadTTLConfig.
func loadTTLConfigWithRole(townRoot, rigName string) map[string]time.Duration {
	// Layer 1: Hardcoded defaults (lowest precedence)
	ttls := make(map[string]time.Duration)
	for k, v := range defaultTTLs {
		ttls[k] = v
	}

	if townRoot == "" {
		return ttls
	}

	// Layer 2: Rig config - wisp layer (middle precedence)
	if rigName != "" {
		cfg := wisp.NewConfig(townRoot, rigName)
		raw := cfg.Get("wisp_ttl")
		if raw != nil {
			// wisp_ttl is stored as map[string]interface{} in JSON config
			if ttlMap, ok := raw.(map[string]interface{}); ok {
				for wispType, val := range ttlMap {
					if s, ok := val.(string); ok {
						if d, err := time.ParseDuration(s); err == nil {
							ttls[wispType] = d
						}
					}
				}
			}
		}

		// Layer 2b: Rig identity bead labels (wisp_ttl_*:value)
		applyRigBeadTTLOverrides(ttls, townRoot, rigName)
	}

	return ttls
}

// applyRigBeadTTLOverrides reads wisp_ttl_* labels from the rig identity bead
// and applies them as overrides.
func applyRigBeadTTLOverrides(ttls map[string]time.Duration, townRoot, rigName string) {
	beadsDir := beads.ResolveBeadsDir(townRoot)
	bd := beads.NewWithBeadsDir(townRoot, beadsDir)

	rigBeadID := beads.RigBeadIDWithPrefix("gt", rigName)
	issue, err := bd.Show(rigBeadID)
	if err != nil {
		return
	}

	for _, label := range issue.Labels {
		colonIdx := strings.Index(label, ":")
		if colonIdx == -1 {
			continue
		}
		key := strings.ToLower(label[:colonIdx])
		value := strings.TrimSpace(label[colonIdx+1:])

		if wispType, ok := beads.ParseWispTTLKey(key); ok {
			if dur, err := time.ParseDuration(value); err == nil {
				ttls[wispType] = dur
			}
		}
	}
}

// getTTL returns the TTL for a wisp based on its wisp_type field.
// Falls back to "default" if wisp_type is empty or unknown.
func getTTL(ttls map[string]time.Duration, wispType string) time.Duration {
	if wispType == "" {
		wispType = "default"
	}
	if d, ok := ttls[wispType]; ok {
		return d
	}
	return ttls["default"]
}

// compactIssue is the extended issue struct that includes fields from bd list --json.
type compactIssue struct {
	beads.Issue
	CommentCount int    `json:"comment_count"`
	WispType     string `json:"wisp_type,omitempty"`
}

// compactScope names the store a compaction run operates on, and how that
// store was chosen.
type compactScope struct {
	// Name is the rig name, or "cwd" when no rig was named and the run falls
	// back to whatever the working directory routes to.
	Name string
	// Source describes where Name came from, for error messages: "--rig",
	// "GT_RIG" or "cwd".
	Source string
	// WorkDir is the directory bd runs in.
	WorkDir string
	// BeadsDir is the resolved .beads directory that will be scanned.
	BeadsDir string
}

// resolveCompactScope decides which store this run scans, and refuses to run
// against a rig name that does not exist.
//
// An unknown --rig used to be accepted silently: the name was consulted only
// for TTL configuration, the scan still ran against the working directory, and
// the run reported "0 wisps scanned". A wrong scope and an empty store were
// indistinguishable, which is why an inert compactor went unnoticed for days
// (gt-kei9). A name that does not resolve to a store is now an error, and a
// name that does resolve scopes the scan to that rig's store rather than only
// its TTLs.
func resolveCompactScope(workDir, townRoot, flagRig string) (*compactScope, error) {
	rigName, source := flagRig, "--rig"
	if rigName == "" {
		rigName, source = os.Getenv("GT_RIG"), "GT_RIG"
	}

	if rigName == "" {
		return &compactScope{
			Name:     "cwd",
			Source:   "cwd",
			WorkDir:  workDir,
			BeadsDir: beads.ResolveBeadsDir(workDir),
		}, nil
	}

	if townRoot == "" {
		return nil, fmt.Errorf("%s=%s given, but %s is not inside a Gas Town workspace so the rig cannot be resolved",
			source, rigName, workDir)
	}

	rigsConfig, err := config.LoadRigsConfig(constants.MayorRigsPath(townRoot))
	if err != nil {
		return nil, fmt.Errorf("loading rigs config to validate %s=%s: %w", source, rigName, err)
	}

	if _, ok := rigsConfig.Rigs[rigName]; !ok {
		known := make([]string, 0, len(rigsConfig.Rigs))
		for name := range rigsConfig.Rigs {
			known = append(known, name)
		}
		sort.Strings(known)
		msg := suggest.FormatSuggestion("rig", rigName, suggest.FindSimilar(rigName, known, 3), "")
		return nil, fmt.Errorf("%s=%s: %s\n\n  Known rigs: %s",
			source, rigName, msg, strings.Join(known, ", "))
	}

	rigPath := filepath.Join(townRoot, rigName)
	return &compactScope{
		Name:     rigName,
		Source:   source,
		WorkDir:  rigPath,
		BeadsDir: beads.ResolveBeadsDir(rigPath),
	}, nil
}

func runCompact(cmd *cobra.Command, args []string) error {
	now := time.Now().UTC()

	// Resolve working directory and town root
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working dir: %w", err)
	}

	townRoot := beads.FindTownRoot(workDir)

	scope, err := resolveCompactScope(workDir, townRoot, compactRig)
	if err != nil {
		return err
	}

	rigName := scope.Name
	if scope.Source == "cwd" {
		rigName = ""
	}

	// Load TTL config
	ttls := loadTTLConfig(townRoot, rigName)

	// Query all ephemeral (wisp) issues via bd list, against the scoped store.
	bd := beads.NewWithBeadsDir(scope.WorkDir, scope.BeadsDir)
	allWisps, err := listWisps(bd)
	if err != nil {
		return fmt.Errorf("listing wisps in %s: %w", scope.BeadsDir, err)
	}

	if !compactJSON && !compactDryRun {
		fmt.Printf("Compacting %d wisps in %s (%s)...\n", len(allWisps), scope.BeadsDir, scope.Name)
	}

	result := &compactResult{Scope: scope.Name, Store: scope.BeadsDir}

	// An empty scan is the exact shape this bug family keeps producing, so it
	// gets a positive control rather than a clean report. See countHiddenWisps.
	if len(allWisps) == 0 {
		if hidden, herr := countHiddenWisps(bd); herr != nil {
			result.HiddenWispsError = herr.Error()
		} else {
			result.HiddenWisps = hidden
		}
	}

	for _, w := range allWisps {
		age, err := wispAge(w, now)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", w.ID, err))
			continue
		}

		ttl := getTTL(ttls, w.WispType)

		switch verdict, reason := compactVerdict(w, age, ttl); verdict {
		case verdictPromote:
			promoteWisp(bd, w, reason, result)
		case verdictDelete:
			deleteWisp(bd, w, reason, result)
		default:
			result.Skipped++
			if compactVerbose && !compactJSON {
				fmt.Printf("  skip  %s %s (age: %s, ttl: %s)\n",
					w.ID, compactTruncate(w.Title, 40), age.Round(time.Minute), ttl)
			}
		}
	}

	// Clean up orphaned wisp_dependencies left behind by deleted wisps.
	// When bd delete removes a wisp, it doesn't cascade-delete dependency
	// records in wisp_dependencies that reference the deleted wisp. Over many
	// compaction cycles these accumulate as dangling refs. We sweep them here.
	if !compactDryRun {
		cleanOrphanedWispDeps(bd, result)
	}

	// Output results
	if compactJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	printCompactSummary(result)
	return nil
}

// compactVerdictKind is what a compaction run decides to do with one wisp.
type compactVerdictKind int

const (
	verdictSkip compactVerdictKind = iota
	verdictPromote
	verdictDelete
)

// compactVerdict is the whole promote/delete/skip decision for one wisp.
//
// It is a pure function on purpose. The decision used to be inline in
// runCompact, so the only testable pieces were the individual predicates —
// and a predicate test passes whether or not the decision path calls it. That
// is the shape that let gt-7rne ship next to a green suite pinning a function
// the broken path never invoked. Test the verdict, not the predicate.
func compactVerdict(w *compactIssue, age, ttl time.Duration) (compactVerdictKind, string) {
	// Molecule step wisps (those with a Parent) should never be promoted.
	// They are subordinate steps of a molecule and should be deleted when
	// past TTL, not elevated to permanent beads. This prevents patrol
	// molecule steps from polluting the issues table.
	isMoleculeStep := w.Parent != ""

	if (hasComments(w) || hasKeepLabel(w) || hasPatrolReport(w)) && !isMoleculeStep {
		return verdictPromote, "proven value"
	}

	if age <= ttl {
		return verdictSkip, "within TTL"
	}

	if w.Status == "closed" {
		return verdictDelete, "TTL expired"
	}

	// Non-closed and past TTL: something is stuck, so the wisp becomes a
	// permanent bead rather than disappearing — except molecule steps, which
	// are deleted so they do not pollute the issues table.
	if isMoleculeStep {
		return verdictDelete, "molecule step past TTL"
	}
	if w.Status == "in_progress" {
		return verdictPromote, "stuck in_progress past TTL"
	}
	return verdictPromote, "open past TTL"
}

// cleanOrphanedWispDeps removes wisp_dependencies rows where either side no
// longer exists in the wisps table. This happens when bd delete removes a wisp
// but leaves behind its dependency records (bd delete has no cascade logic for
// the wisp-level tables). Runs as a post-compact sweep.
func cleanOrphanedWispDeps(bd *beads.Beads, result *compactResult) {
	columns, err := bd.Run("sql", "--csv", "SHOW COLUMNS FROM wisp_dependencies")
	if err != nil || !strings.Contains(string(columns), "\ndepends_on_wisp_id,") || !strings.Contains(string(columns), "\ndepends_on_issue_id,") {
		return
	}
	const q = `DELETE FROM wisp_dependencies WHERE ` +
		`NOT EXISTS (SELECT 1 FROM wisps WHERE id = wisp_dependencies.issue_id) ` +
		`OR (depends_on_wisp_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM wisps WHERE id = wisp_dependencies.depends_on_wisp_id)) ` +
		`OR (depends_on_issue_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM issues WHERE id = wisp_dependencies.depends_on_issue_id))`
	out, err := bd.Run("sql", q)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("orphaned wisp_deps cleanup: %v", err))
		return
	}
	// bd sql reports "OK, N rows affected" for non-SELECT statements.
	// Parse the count if present; a non-zero result means refs were cleaned.
	var n int
	if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(out)), "OK, %d rows affected", &n); scanErr == nil {
		result.OrphanedWispDeps = n
	}
}

// listWisps queries all ephemeral issues from the database.
// Returns extended issue structs with comment_count and wisp_type.
//
// The query deliberately omits --include-infra, so it sees no wisps at all and
// this compactor is inert. Do NOT add the flag as a one-line fix (gastown-mq9).
// Adding it arms deleteWisp and promoteWisp over every wisp in the store on the
// very next run — a mass delete, which CLAUDE.md reserves — and it makes the
// compactor a second path to a deletion that the standing `bd mol wisp gc
// --closed --force` suspension in the town patrol formulas currently blocks.
// The compactor is not covered by that suspension only because it cannot see
// wisps; widening the scan silently lifts a protection left in place on purpose.
//
// Enabling it needs, in this order:
//  1. Somewhere durable for per-cycle patrol summaries to live (gt-tgf0's
//     aggregation), or at minimum the proven-value guard for them that
//     hasPatrolReport now provides — landed, so this step is satisfied.
//  2. A decision on whether the first enabled run is forced to --dry-run,
//     gated behind an explicit --confirm, or bounded by a max-deletions cap.
//     Ages in every store are far past every TTL in defaultTTLs, so the first
//     armed run deletes essentially everything closed at once.
//  3. Confirmation that the TTL policy is still what we want before it takes
//     effect on thousands of accumulated wisps.
//
// Steps 2 and 3 are Marc's or the Deacon's call, not a code change.
func listWisps(bd *beads.Beads) ([]*compactIssue, error) {
	// Use bd list --json --all to get wisps in all statuses, unlimited
	out, err := bd.Run("list", "--json", "--all", "-n", "0")
	if err != nil {
		return nil, err
	}

	// Strip any non-JSON prefix (warnings, notices) that bd may emit to
	// stdout before the JSON array. Without this, unicode characters like
	// emoji in wisp subjects can trigger "invalid character looking for
	// beginning of value" errors when a warning line contains non-ASCII.
	out = extractJSONArray(out)

	var allIssues []*compactIssue
	if err := json.Unmarshal(out, &allIssues); err != nil {
		return nil, fmt.Errorf("parsing issue list: %w", err)
	}

	// Filter to ephemeral only
	var wisps []*compactIssue
	for _, issue := range allIssues {
		if issue.Ephemeral {
			wisps = append(wisps, issue)
		}
	}

	return wisps, nil
}

// countHiddenWisps reports how many ephemeral rows the scan query cannot see.
//
// listWisps runs "bd list --all", which omits --include-infra. Wisps are infra
// and hidden by that filter, so the scan can return zero rows from a store
// holding thousands of them, and the run prints a clean "0 wisps scanned". The
// shortfall leaves no trace in the output, which is how an inert compactor sat
// unnoticed next to a wisp count that kept climbing (gt-kei9).
//
// This measures the shortfall and does nothing about it. Widening the scan
// query would silently arm deletion and promotion over every wisp in the store,
// which is not a change to make as a side effect of a reporting fix.
func countHiddenWisps(bd *beads.Beads) (int, error) {
	out, err := bd.Run("list", "--json", "--all", "--include-infra", "-n", "0")
	if err != nil {
		return 0, err
	}

	var allIssues []*compactIssue
	if err := json.Unmarshal(extractJSONArray(out), &allIssues); err != nil {
		return 0, fmt.Errorf("parsing issue list: %w", err)
	}

	hidden := 0
	for _, issue := range allIssues {
		if issue.Ephemeral {
			hidden++
		}
	}
	return hidden, nil
}

// extractJSONArray finds the first '[' byte in data and returns from that
// point onward. This strips any non-JSON prefix (warning messages, notices)
// that a subprocess may emit to stdout before the actual JSON payload.
// Returns the original data unchanged if no '[' is found.
func extractJSONArray(data []byte) []byte {
	idx := bytes.IndexByte(data, '[')
	if idx < 0 {
		return data
	}
	return data[idx:]
}

// promoteWisp makes a wisp permanent by setting --persistent and adding a comment.
func promoteWisp(bd *beads.Beads, w *compactIssue, reason string, result *compactResult) {
	action := compactAction{ID: w.ID, Title: w.Title, Reason: reason, WispType: w.WispType}

	if compactDryRun {
		result.Promoted = append(result.Promoted, action)
		if !compactJSON {
			fmt.Printf("  %s promote %s %s (%s)\n",
				style.Dim.Render("[dry-run]"), w.ID, compactTruncate(w.Title, 40), reason)
		}
		return
	}

	// bd update --persistent sets ephemeral=false
	_, err := bd.Run("update", w.ID, "--persistent")
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("promote %s: %v", w.ID, err))
		return
	}

	// Add comment noting the promotion
	_, _ = bd.Run("comments", "add", w.ID, fmt.Sprintf("Promoted from Level 0: %s", reason))

	result.Promoted = append(result.Promoted, action)

	if compactVerbose && !compactJSON {
		fmt.Printf("  %s %s %s (%s)\n",
			style.Success.Render("promote"), w.ID, compactTruncate(w.Title, 40), reason)
	}
}

// deleteWisp removes a closed wisp that has expired past its TTL.
func deleteWisp(bd *beads.Beads, w *compactIssue, reason string, result *compactResult) {
	action := compactAction{ID: w.ID, Title: w.Title, Reason: reason, WispType: w.WispType}

	if compactDryRun {
		result.Deleted = append(result.Deleted, action)
		if !compactJSON {
			fmt.Printf("  %s delete  %s %s (%s)\n",
				style.Dim.Render("[dry-run]"), w.ID, compactTruncate(w.Title, 40), reason)
		}
		return
	}

	// bd delete --force (safe: Dolt AS OF preserves history)
	_, err := bd.Run("delete", w.ID, "--force")
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("delete %s: %v", w.ID, err))
		return
	}

	result.Deleted = append(result.Deleted, action)

	if compactVerbose && !compactJSON {
		fmt.Printf("  %s %s %s (%s)\n",
			style.Warning.Render("delete "), w.ID, compactTruncate(w.Title, 40), reason)
	}
}

func printCompactSummary(result *compactResult) {
	promoted := len(result.Promoted)
	deleted := len(result.Deleted)
	total := promoted + deleted + result.Skipped

	if compactDryRun {
		fmt.Printf("\n%s Dry run complete: %d wisps scanned\n",
			style.Dim.Render("ℹ"), total)
	} else {
		fmt.Printf("\n%s Compaction complete\n", style.Success.Render("✓"))
	}

	fmt.Printf("  Scope:    %s\n", result.Scope)
	fmt.Printf("  Store:    %s\n", result.Store)
	fmt.Printf("  Promoted: %d\n", promoted)
	fmt.Printf("  Deleted:  %d\n", deleted)
	fmt.Printf("  Skipped:  %d (within TTL)\n", result.Skipped)
	if result.OrphanedWispDeps > 0 {
		fmt.Printf("  Cleaned:  %d orphaned wisp dependency ref(s)\n", result.OrphanedWispDeps)
	}

	if result.HiddenWispsError != "" {
		fmt.Printf("\n%s Scanned 0 wisps and could not check whether that is real: %s\n",
			style.Warning.Render("⚠"), result.HiddenWispsError)
	} else if result.HiddenWisps > 0 {
		fmt.Printf("\n%s Scanned 0 wisps, but this store holds %d ephemeral row(s).\n",
			style.Warning.Render("⚠"), result.HiddenWisps)
		fmt.Printf("  They are invisible to the scan query, which omits --include-infra,\n")
		fmt.Printf("  so this run compacted nothing and the report above is not evidence\n")
		fmt.Printf("  that there was nothing to compact. See gt-kei9.\n")
		fmt.Printf("  Reproduce: bd list --json --all --include-infra -n 0\n")
	}

	if len(result.Errors) > 0 {
		fmt.Printf("\n%s %d errors:\n", style.Warning.Render("⚠"), len(result.Errors))
		for _, e := range result.Errors {
			fmt.Printf("  - %s\n", e)
		}
	}

	// Show promotions if any
	if promoted > 0 && !compactDryRun {
		fmt.Printf("\nPromotions:\n")
		for _, p := range result.Promoted {
			fmt.Printf("  %s: %s (%s)\n", p.ID, compactTruncate(p.Title, 50), p.Reason)
		}
	}
}

// compactTruncate shortens a string to maxLen runes, adding "..." if truncated.
// Uses rune count instead of byte length so multi-byte UTF-8 characters
// (emoji, CJK, etc.) are never split mid-sequence.
func compactTruncate(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string([]rune(s)[:maxLen])
	}
	return string([]rune(s)[:maxLen-3]) + "..."
}

// hasComments checks the comment_count on the compactIssue.
func hasComments(w *compactIssue) bool {
	return w.CommentCount > 0
}

// patrolReportPrefix is what patrol_report.go writes at the head of a patrol
// wisp's description when a cycle closes with a summary. Keep it in step with
// the format string there.
const patrolReportPrefix = "Patrol report:"

// hasPatrolReport reports whether this wisp's description carries a patrol
// cycle summary, which makes it the only copy of that record.
//
// `gt patrol report` writes the cycle summary and step audit into the wisp's
// DESCRIPTION and then closes the wisp (patrol_report.go). Nothing else keeps
// that text: the close reason holds the summary but not the step audit, and no
// aggregation reads it yet (gt-tgf0). So a closed patrol wisp past TTL is a
// deletion candidate whose description is the record.
//
// The rest of the proven-value predicate cannot see this. Comments, keep
// labels and dependency counts are all metadata; a patrol wisp typically
// carries none of them. Measured against the town store 2026-09-05 with
// `bd -C ~/gt list --json --all --include-infra -n 0`: of 8344 rows, 7829
// ephemeral, 237 carry a "Patrol report:" description and 232 of those are
// closed with comment_count 0 — unprotected by every other arm of the
// predicate, and the exact record class the standing `bd mol wisp gc --closed`
// suspension exists to preserve. All 237 are molecule ROOTS (parent empty), so
// the !isMoleculeStep arm of the promote branch does not exclude them.
func hasPatrolReport(w *compactIssue) bool {
	return strings.HasPrefix(strings.TrimSpace(w.Description), patrolReportPrefix)
}

// isReferenced checks dependency counts.
func isReferenced(w *compactIssue) bool {
	return w.DependentCount > 0 || w.DependencyCount > 0
}

// hasKeepLabel checks for keep labels.
func hasKeepLabel(w *compactIssue) bool {
	for _, label := range w.Labels {
		if label == "keep" || label == "gt:keep" {
			return true
		}
	}
	return false
}

// wispAge returns the age of a compactIssue.
func wispAge(w *compactIssue, now time.Time) (time.Duration, error) {
	ts := w.UpdatedAt
	if ts == "" {
		ts = w.CreatedAt
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05Z", ts)
		if err != nil {
			return 0, fmt.Errorf("parsing timestamp %q: %w", ts, err)
		}
	}
	return now.Sub(t), nil
}
