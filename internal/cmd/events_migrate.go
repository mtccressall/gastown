package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/channelevents"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	eventsMigrateDryRun bool
	eventsMigrateJSON   bool
)

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Inspect and maintain channel event directories",
}

var eventsMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Move pre-scoping channel events out of the shared directory",
	Long: `Drain the unscoped events/<channel>/ directories left over from before
channels were rig-scoped.

Per-rig channels (refinery, witness) used to share one directory per ROLE across
every rig, so one rig's consumer could read — and with --cleanup delete — another
rig's events. Channels are now scoped as events/rigs/<rig>/<channel>/. This
command drains what the shared directories still hold.

Each leftover event goes to one of two places, and NOTHING IS DELETED:

  rig identifiable   -> events/rigs/<rig>/<channel>/     (delivered)
  rig unknown        -> events/legacy/<channel>/         (archived)

An event is attributed only from its own "rig" field or a "rig" in its payload,
and only when that rig is registered in the town. Pre-scoping MQ_SUBMIT events
carry no rig at all; those are archived rather than guessed at, because handing
an event to the wrong rig is the bug this scoping exists to fix, and copying it
to every rig would wake all of them on work that is not theirs.

Archived events stay readable and can be moved back by hand. Deleting them is a
separate, deliberate act — deleting an event that might belong to a live rig is
exactly the theft this command exists to avoid.

Town-level channels (mayor) are not touched: their directory is already correct.

EXAMPLES:
  gt events migrate --dry-run    # report what would move
  gt events migrate              # move it`,
	RunE: runEventsMigrate,
}

func init() {
	eventsMigrateCmd.Flags().BoolVar(&eventsMigrateDryRun, "dry-run", false,
		"Report what would move without moving anything")
	eventsMigrateCmd.Flags().BoolVar(&eventsMigrateJSON, "json", false,
		"Output as JSON")
	eventsCmd.AddCommand(eventsMigrateCmd)
	rootCmd.AddCommand(eventsCmd)
}

// EventsMigrateMove is one planned or completed relocation.
type EventsMigrateMove struct {
	Channel string `json:"channel"`
	From    string `json:"from"`
	To      string `json:"to"`
	Rig     string `json:"rig,omitempty"` // empty means archived, not delivered
}

// EventsMigrateResult reports what the migration did.
//
// Delivered and Archived are reported separately rather than as one total:
// a run that archives everything and one that delivers everything are very
// different outcomes, and a single count cannot tell them apart.
type EventsMigrateResult struct {
	DryRun    string              `json:"mode"`
	Scanned   int                 `json:"scanned"`
	Delivered int                 `json:"delivered"`
	Archived  int                 `json:"archived"`
	Moves     []EventsMigrateMove `json:"moves"`
	Errors    []string            `json:"errors,omitempty"`
}

func runEventsMigrate(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return err
	}

	result, err := collectEventsMigrate(townRoot, eventsMigrateDryRun)
	if err != nil {
		return err
	}

	if eventsMigrateJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(result); encErr != nil {
			return encErr
		}
	} else {
		printEventsMigrateResult(result)
	}
	// Exit non-zero on any failure in BOTH output modes. A --json caller is
	// automation, which is exactly the consumer that acts on a status code
	// without a human reading the errors array.
	if len(result.Errors) > 0 {
		return fmt.Errorf("%d event(s) could not be migrated", len(result.Errors))
	}
	return nil
}

// collectEventsMigrate plans and (unless dryRun) performs the migration.
// Separated from the cobra wrapper so the behaviour is testable without a cwd.
func collectEventsMigrate(townRoot string, dryRun bool) (*EventsMigrateResult, error) {
	result := &EventsMigrateResult{DryRun: "applied"}
	if dryRun {
		result.DryRun = "dry-run"
	}

	// Only per-rig channels can be mis-scoped. Sorted so output is stable.
	channels := make([]string, 0, len(channelScopes))
	for ch, scope := range channelScopes {
		if scope == scopeRig {
			channels = append(channels, ch)
		}
	}
	sort.Strings(channels)

	for _, channel := range channels {
		srcDir, dirErr := channelevents.ChannelDir(townRoot, channelevents.TownScope, channel)
		if dirErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", channel, dirErr))
			continue
		}
		entries, readErr := os.ReadDir(srcDir)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue // no shared directory for this channel: nothing to drain
			}
			// A permission or I/O error is NOT an empty directory. Swallowing it
			// would report a clean drain while leaving every legacy event in
			// place — the false empty this whole change exists to stop.
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", srcDir, readErr))
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".event") {
				continue
			}
			result.Scanned++
			src := filepath.Join(srcDir, entry.Name())

			rig := attributeEventRig(townRoot, src)
			var dstDir string
			if rig != "" {
				dstDir, dirErr = channelevents.ChannelDir(townRoot, rig, channel)
			} else {
				dstDir, dirErr = channelevents.LegacyChannelDir(townRoot, channel)
			}
			if dirErr != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", src, dirErr))
				continue
			}
			dst := filepath.Join(dstDir, entry.Name())

			// Count and record only what actually moved. Incrementing up front
			// makes a permission error or a destination collision report as a
			// completed move — a job claiming success on partial completion,
			// which is the shape that hid a backup layer that had never once
			// run. In dry-run nothing can fail, so the plan is the result.
			if !dryRun {
				if err := channelevents.MoveEvent(src, dst); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", src, err))
					continue
				}
			}

			result.Moves = append(result.Moves, EventsMigrateMove{
				Channel: channel, From: src, To: dst, Rig: rig,
			})
			if rig != "" {
				result.Delivered++
			} else {
				result.Archived++
			}
		}
	}

	return result, nil
}

func printEventsMigrateResult(result *EventsMigrateResult) {
	if result.Scanned == 0 {
		fmt.Printf("%s No pre-scoping events found. Nothing to migrate.\n", style.Dim.Render("✓"))
		return
	}
	verb := "Moved"
	if result.DryRun == "dry-run" {
		verb = "Would move"
	}
	fmt.Printf("%s %s %d of %d event(s): %d delivered to a rig, %d archived as unattributable.\n",
		style.Bold.Render("→"), verb, result.Delivered+result.Archived, result.Scanned,
		result.Delivered, result.Archived)
	for _, m := range result.Moves {
		dest := "archive"
		if m.Rig != "" {
			dest = "rig " + m.Rig
		}
		fmt.Printf("  %s %s -> %s\n", style.Dim.Render(m.Channel), filepath.Base(m.From), dest)
	}
	if result.Archived > 0 {
		fmt.Printf("\n%s Archived events are kept, not deleted. They carry no rig, so delivering\n"+
			"  them would risk waking the wrong rig. Review them before removing anything.\n",
			style.Dim.Render("note:"))
	}
	for _, e := range result.Errors {
		fmt.Printf("%s %s\n", style.Dim.Render("⚠"), e)
	}
}

// attributeEventRig returns the rig an event belongs to, or "" when it cannot
// be established or names a rig this town does not have.
//
// The rig comes from the event's own contents via channelevents.EventRig — it
// is never inferred from the surroundings. The six events lost in gt-a3qs were
// attributed to liveop only afterwards, by correlating timestamps against a
// separate log, and a heuristic that good is still not good enough to route on.
func attributeEventRig(townRoot, path string) string {
	rig := channelevents.EventRig(path)
	if rig == "" {
		return ""
	}
	// A rig that is not registered cannot be delivered to; archive instead.
	if !isRegisteredRig(townRoot, rig) {
		return ""
	}
	return rig
}
