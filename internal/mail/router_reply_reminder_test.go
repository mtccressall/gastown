package mail

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnqueueReplyReminder_PrescribesReplyNotSend is the regression test for gt-mnnx.
//
// The reminder used to prescribe `gt mail send`, which OPENS A NEW THREAD and so can
// never satisfy the reminder that prescribed it. Measured 30 for 30 across two agents:
// every `gt mail send` created a distinct thread, none matching the inbound one. The
// reminder fires on the INBOUND message's thread, so an agent following the instruction
// exactly guaranteed it would fire again.
//
// This test pins the two properties that make the reminder satisfiable:
//   - it names `gt mail reply`, never `gt mail send`
//   - it carries the MESSAGE ID, which is what `gt mail reply` takes, rather than the
//     sender address, which is what `gt mail send` takes
//
// Against the pre-fix code both assertions fail.
func TestEnqueueReplyReminder_PrescribesReplyNotSend(t *testing.T) {
	townRoot := t.TempDir()
	sessionID := "gt-crew-replyreminder"

	r := &Router{workDir: t.TempDir(), townRoot: townRoot}
	msg := &Message{
		ID:      "gt-wisp-abc123",
		From:    "gastown/witness",
		To:      "deacon/",
		Subject: "a question that wants an answer",
	}

	r.enqueueReplyReminder(msg, sessionID)

	dir := filepath.Join(townRoot, ".runtime", "nudge_queue", sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reply reminder was not enqueued (queue dir unreadable): %v", err)
	}

	var body string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue // skip the queue's lock file
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var q struct {
			Message string `json:"message"`
			Kind    string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &q); err != nil {
			t.Fatalf("unmarshal %s: %v", e.Name(), err)
		}
		if q.Kind == "reply-reminder" {
			body = q.Message
			break
		}
	}
	if body == "" {
		// Carry the denominator: an empty queue and a queue with no matching row
		// otherwise produce the same failure message.
		t.Fatalf("no reply-reminder found among %d queue entries in %s", len(entries), dir)
	}

	if !strings.Contains(body, "gt mail reply") {
		t.Errorf("reminder does not prescribe `gt mail reply`; got: %s", body)
	}
	if strings.Contains(body, "gt mail send "+msg.From) {
		t.Errorf("reminder still prescribes `gt mail send <addr>`, which cannot thread and so cannot clear this reminder; got: %s", body)
	}
	if !strings.Contains(body, msg.ID) {
		t.Errorf("reminder omits the message ID %q, which is the argument `gt mail reply` takes; got: %s", msg.ID, body)
	}
}
