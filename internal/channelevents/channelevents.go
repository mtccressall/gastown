// Package channelevents provides file-based event emission for named channels.
//
// Channel events are JSON files written to ~/gt/events/<channel>/*.event
// and consumed by await-event subscribers (e.g., the refinery watching for
// MERGE_READY events). This is distinct from the activity feed events in
// the events package (~/gt/.events.jsonl).
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

// emitSeq is an atomic counter to ensure unique event filenames even when
// time.Now().UnixNano() has low resolution.
var emitSeq atomic.Uint64

// Emit creates an event file in the channel directory, resolving the town
// root from the current working directory.
func Emit(channel, eventType string, payloadPairs []string) (string, error) {
	if !ValidChannelName.MatchString(channel) {
		return "", fmt.Errorf("invalid channel name %q: must match [a-zA-Z0-9_-]", channel)
	}

	townRoot, err := workspace.FindFromCwd()
	if err != nil || townRoot == "" {
		home, _ := os.UserHomeDir()
		townRoot = filepath.Join(home, "gt")
	}
	return emitToDir(filepath.Join(townRoot, "events", channel), channel, eventType, payloadPairs)
}

// EmitToTown creates an event file using an explicit town root.
// Used by internal callers that already know the town root.
func EmitToTown(townRoot, channel, eventType string, payloadPairs []string) (string, error) {
	if !ValidChannelName.MatchString(channel) {
		return "", fmt.Errorf("invalid channel name %q: must match [a-zA-Z0-9_-]", channel)
	}

	return emitToDir(filepath.Join(townRoot, "events", channel), channel, eventType, payloadPairs)
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
func emitToDir(eventDir, channel, eventType string, payloadPairs []string) (string, error) {
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
