// Package channelevents provides file-based event emission for named channels.
//
// Channel events are JSON files consumed by await-event subscribers (e.g., the
// refinery watching for MERGE_READY events). This is distinct from the activity
// feed events in the events package (~/gt/.events.jsonl).
//
// # Channel scoping
//
// A channel directory is either RIG-SCOPED or TOWN-SCOPED:
//
//	~/gt/events/rigs/<rig>/<channel>/   rig-scoped   (refinery, witness)
//	~/gt/events/<channel>/             town-scoped  (mayor)
//
// Roles that exist once per rig (refinery, witness) MUST be rig-scoped. Every
// rig runs its own refinery, and await-event is documented single-consumer: two
// refineries watching one directory with --cleanup means one rig consumes and
// deletes the other's events. That is not hypothetical — gastown/refinery ate
// and deleted six of liveop/refinery's MQ_SUBMIT events on 2026-09-02 (gt-a3qs).
//
// Roles that exist once per TOWN (mayor) are town-scoped: the mayor is a single
// consumer by construction, and rig-scoping its channel would scatter its events
// across directories it does not watch.
//
// ChannelDir is the single builder for both cases. Emitters and consumers must
// agree on the path, so every site — emit, await, and the daemon's spawn gate —
// resolves through it rather than joining the path itself. A site that builds
// the path by hand is how the three consumers drifted apart in the first place.
//
// # Writes are suppressed inside a test binary
//
// Emission is gated on testmode.WritesSuppressed (gastown-rv6). Emitters
// resolve the town root from the caller's cwd, and agents run `make test`
// inside worktrees under the town, so fixture traffic landed in the
// PRODUCTION channel directories: measured on this town, 43 of the 70 files
// in events/refinery were MQ_SUBMIT events carrying the payload
// "test message" from internal/cmd's nudge fixtures.
//
// A channel event is worse than a stray feed row because it is ACTED ON
// rather than read — a pending MQ_SUBMIT wakes a refinery, which then burns
// a patrol cycle on an empty queue.
//
// The gate sits at the writer rather than at the call sites for the same
// reason it does in internal/events and internal/townlog: GT_TEST_NUDGE_LOG
// already gated the nudge TRANSPORT and the emit rode the ungated path
// beside it, so a per-call-site fix is the shape that gets half applied.
// Tests that exercise emission itself opt back in with
// t.Setenv(testmode.EnvSuppress, "0").
package channelevents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/steveyegge/gastown/internal/testmode"
	"github.com/steveyegge/gastown/internal/workspace"
)

// ValidChannelName restricts channel names to safe characters (no path traversal).
var ValidChannelName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// EnvSuppress disables all channel event emission when set to anything but
// "0". It is set automatically inside a test binary; see package testmode.
const EnvSuppress = testmode.EnvSuppress

// ValidRigName restricts rig names to safe characters (no path traversal).
// A rig name reaches ChannelDir from a --rig flag and from GT_RIG, so it is
// attacker-adjacent in the same way a channel name is and gets the same guard.
var ValidRigName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// TownScope is the rig value for a town-level channel (one consumer for the
// whole town, e.g. the mayor). It is a named constant so a call site reads as a
// deliberate choice rather than as a forgotten argument.
const TownScope = ""

// RigsDirName is the subdirectory under events/ holding rig-scoped channels.
const RigsDirName = "rigs"

// LegacyDirName is the subdirectory under events/ where `gt events migrate`
// archives pre-scoping events it cannot attribute to a rig.
const LegacyDirName = "legacy"

// reservedTownChannels are names a town-scoped channel may not use, because
// events/<channel> would collide with the rig-scoping and archive subtrees.
// Rejecting them is loud; letting one through would silently merge a channel
// with every rig's events.
var reservedTownChannels = map[string]bool{
	RigsDirName:   true,
	LegacyDirName: true,
}

// emitSeq is an atomic counter to ensure unique event filenames even when
// time.Now().UnixNano() has low resolution.
var emitSeq atomic.Uint64

// ChannelDir returns the event directory for a channel, and is the only place
// that path is constructed.
//
// rig == "" yields the town-scoped directory (events/<channel>); a non-empty rig
// yields the rig-scoped one (events/rigs/<rig>/<channel>). It validates both
// names and does not touch the filesystem.
func ChannelDir(townRoot, rig, channel string) (string, error) {
	if !ValidChannelName.MatchString(channel) {
		return "", fmt.Errorf("invalid channel name %q: must match [a-zA-Z0-9_-]", channel)
	}
	if rig == "" {
		if reservedTownChannels[channel] {
			return "", fmt.Errorf("channel name %q is reserved for the events/ layout: "+
				"a town-scoped channel cannot use it", channel)
		}
		return filepath.Join(townRoot, "events", channel), nil
	}
	if !ValidRigName.MatchString(rig) {
		return "", fmt.Errorf("invalid rig name %q: must match [a-zA-Z0-9_-]", rig)
	}
	return filepath.Join(townRoot, "events", RigsDirName, rig, channel), nil
}

// LegacyChannelDir returns the archive directory `gt events migrate` moves
// unattributable pre-scoping events into. Nothing consumes it; it exists so a
// drain never has to delete an event that might belong to another rig.
func LegacyChannelDir(townRoot, channel string) (string, error) {
	if !ValidChannelName.MatchString(channel) {
		return "", fmt.Errorf("invalid channel name %q: must match [a-zA-Z0-9_-]", channel)
	}
	return filepath.Join(townRoot, "events", LegacyDirName, channel), nil
}

// EventRig reads the rig an event declares, from its top-level "rig" field or a
// "rig" key in its payload. It returns "" when the rig is unset, unsafe, or the
// file cannot be read or parsed.
//
// Attribution comes from the event's CONTENTS and never from its location, so a
// caller cannot accidentally claim an event by virtue of where it found it —
// which is the whole failure this package's scoping exists to prevent.
func EventRig(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var event struct {
		Rig     string            `json:"rig"`
		Payload map[string]string `json:"payload"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return ""
	}
	rig := event.Rig
	if rig == "" {
		rig = event.Payload["rig"]
	}
	if !ValidRigName.MatchString(rig) {
		return ""
	}
	return rig
}

// MoveEvent relocates an event file, creating the destination directory as
// needed. It never replaces an existing destination.
//
// The destination is claimed with O_EXCL rather than checked with Stat and then
// written. A Stat-then-rename is a check-then-act race, and it is a reachable
// one: `gt events migrate` is a manual command while the daemon delivers
// attributed legacy events on every heartbeat, so both can hold the same source
// file at once. On Unix os.Rename would then silently replace the destination —
// destroying an event, which is the one outcome every path here is built to
// avoid.
//
// The failure mode under a lost race is a duplicate, never a loss: the loser of
// the O_EXCL create leaves the source alone and reports the collision, and a
// crash between the create and the unlink leaves the event in both places. A
// duplicate wake-up costs a patrol cycle; a lost one costs a merge.
func MoveEvent(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("destination already exists: %s", dst)
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("closing %s: %w", dst, err)
	}

	// The destination now holds the event. A source that another process
	// already removed is not an error — events are immutable, so both copies
	// were identical and the move is complete either way.
	if err := os.Remove(src); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s after copying it to %s: %w", src, dst, err)
	}
	return nil
}

// Emit creates an event file in the channel directory, resolving the town
// root from the current working directory. Pass rig "" for a town-scoped channel.
func Emit(rig, channel, eventType string, payloadPairs []string) (string, error) {
	townRoot, err := workspace.FindFromCwd()
	if err != nil || townRoot == "" {
		home, _ := os.UserHomeDir()
		townRoot = filepath.Join(home, "gt")
	}
	return EmitToTown(townRoot, rig, channel, eventType, payloadPairs)
}

// EmitToTown creates an event file using an explicit town root.
// Used by internal callers that already know the town root.
//
// rig is required for per-rig roles (refinery, witness) and must be "" for
// town-level ones (mayor). It is a positional parameter rather than an optional
// one so that adding a channel forces the caller to decide which it is.
func EmitToTown(townRoot, rig, channel, eventType string, payloadPairs []string) (string, error) {
	eventDir, err := ChannelDir(townRoot, rig, channel)
	if err != nil {
		return "", err
	}
	return emitToDir(eventDir, rig, channel, eventType, payloadPairs)
}

// emitToDir creates the channel directory and writes an event file into it.
//
// This is the single chokepoint every emission passes through, so it carries
// the test gate. Creating the directory belongs here too, behind the gate:
// both callers used to mkdir before delegating, so a gate on the write alone
// would still leave a suppressed test run creating channel directories in the
// production town.
//
// A suppressed emission is not an error — it returns an empty path and a nil
// error, the same way internal/events drops a suppressed write. Callers here
// treat emission as best-effort and discard both values.
func emitToDir(eventDir, rig, channel, eventType string, payloadPairs []string) (string, error) {
	if !ValidChannelName.MatchString(channel) {
		return "", fmt.Errorf("invalid channel name %q: must match [a-zA-Z0-9_-]", channel)
	}

	if testmode.WritesSuppressed() {
		return "", nil
	}

	if err := os.MkdirAll(eventDir, 0755); err != nil {
		return "", fmt.Errorf("creating event directory: %w", err)
	}

	payload := make(map[string]string)
	for _, pair := range payloadPairs {
		key, val, found := strings.Cut(pair, "=")
		if found {
			payload[key] = val
		}
	}

	now := time.Now()
	event := map[string]interface{}{
		"type":      eventType,
		"channel":   channel,
		"timestamp": now.Format(time.RFC3339),
		"payload":   payload,
	}
	// Record the rig on the event itself, not only in its path. The six events
	// lost in gt-a3qs could not be attributed from their contents at all; the
	// rig had to be recovered by correlating timestamps against a second log.
	// A file that gets moved or copied should still say whose it is.
	if rig != "" {
		event["rig"] = rig
	}

	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling event: %w", err)
	}

	seq := emitSeq.Add(1)
	eventFile := filepath.Join(eventDir, fmt.Sprintf("%d-%d-%d.event", now.UnixNano(), seq, os.Getpid()))
	if err := os.WriteFile(eventFile, data, 0644); err != nil {
		return "", fmt.Errorf("writing event file: %w", err)
	}

	return eventFile, nil
}
