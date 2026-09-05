package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/channelevents"
)

var (
	emitEventChannel string
	emitEventType    string
	emitEventPayload []string
	emitEventRig     string
)

var moleculeEmitEventCmd = &cobra.Command{
	Use:   "emit-event",
	Short: "Emit a file-based event on a named channel",
	Long: `Emit an event file to a channel directory for subscribers to pick up.

This is the Go counterpart to emit-event.sh. Events are JSON files consumed
by await-event subscribers (e.g., the refinery watching for MERGE_READY events).

CHANNEL SCOPING:
Per-rig channels (refinery, witness) live under a rig:
  ~/gt/events/rigs/<rig>/<channel>/
Town-level channels (mayor) are shared by the whole town:
  ~/gt/events/<channel>/

The rig is taken from --rig, else GT_RIG, else the rig containing the current
directory. Emitting a per-rig event without a rig puts it where that rig's
consumer will not look, so pass --rig when not running from inside a rig.

EVENT FORMAT:
Creates a JSON file at <channel dir>/<timestamp>.event:
  {"type": "...", "channel": "...", "rig": "...", "timestamp": "...", "payload": {...}}

EXAMPLES:
  # Emit a MERGE_READY event for a rig's refinery
  gt mol step emit-event --channel refinery --rig gastown --type MERGE_READY \
    --payload polecat=nux --payload branch=polecat/nux/gt-iw7m

  # Emit a PATROL_WAKE event
  gt mol step emit-event --channel refinery --type PATROL_WAKE \
    --payload source=witness --payload queue_depth=3

  # Emit an MQ_SUBMIT event
  gt mol step emit-event --channel refinery --type MQ_SUBMIT \
    --payload branch=feat/new-feature --payload mr_id=bd-42`,
	RunE: runMoleculeEmitEvent,
}

// EmitEventResult is returned when an event is emitted.
type EmitEventResult struct {
	Path    string `json:"path"`
	Channel string `json:"channel"`
	Rig     string `json:"rig,omitempty"`
	Type    string `json:"type"`
}

func init() {
	moleculeEmitEventCmd.Flags().StringVar(&emitEventChannel, "channel", "",
		"Event channel name (required, e.g., 'refinery')")
	moleculeEmitEventCmd.Flags().StringVar(&emitEventType, "type", "",
		"Event type (required, e.g., 'MERGE_READY')")
	moleculeEmitEventCmd.Flags().StringArrayVar(&emitEventPayload, "payload", nil,
		"Payload key=value pairs (repeatable)")
	moleculeEmitEventCmd.Flags().StringVar(&emitEventRig, "rig", "",
		"Rig owning this channel (default: GT_RIG, else the rig containing the cwd)")
	moleculeEmitEventCmd.Flags().BoolVar(&moleculeJSON, "json", false,
		"Output as JSON")
	_ = moleculeEmitEventCmd.MarkFlagRequired("channel")
	_ = moleculeEmitEventCmd.MarkFlagRequired("type")

	moleculeStepCmd.AddCommand(moleculeEmitEventCmd)
}

func runMoleculeEmitEvent(cmd *cobra.Command, args []string) error {
	// Resolve the town root once and hand it to EmitToTown, so the rig lookup
	// and the event path cannot disagree about which town they are in.
	townRoot := resolveTownRootOrDefault()
	rigName, err := resolveChannelRig(townRoot, emitEventChannel, emitEventRig)
	if err != nil {
		return err
	}
	warnIfChannelUnscoped(emitEventChannel, rigName)

	path, err := channelevents.EmitToTown(townRoot, rigName, emitEventChannel, emitEventType, emitEventPayload)
	if err != nil {
		return err
	}

	if moleculeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(EmitEventResult{
			Path:    path,
			Channel: emitEventChannel,
			Rig:     rigName,
			Type:    emitEventType,
		})
	}

	fmt.Println(path)
	return nil
}
