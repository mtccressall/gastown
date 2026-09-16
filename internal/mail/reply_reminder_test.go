package mail

import (
	"strings"
	"testing"
)

// The reminder must name an id a mailbox can resolve. msg.ID is an in-memory
// msg- handle that is deliberately never passed to bd create, so quoting it
// prescribes `gt mail reply msg-...`, which resolves to nothing: 0 of 25,532
// rows carry a msg- prefix (gt-mnnx). That is the same failure the reminder was
// rewritten to fix — a command that cannot clear the reminder prescribing it.
func TestReplyReminderTextNamesThePersistedID(t *testing.T) {
	msg := &Message{
		ID:          "msg-deadbeef",
		PersistedID: "gt-wisp-abc123",
		From:        "mayor/",
		Subject:     "a subject",
	}

	got := replyReminderText(msg)

	if !strings.Contains(got, "gt mail reply gt-wisp-abc123") {
		t.Errorf("reminder does not prescribe the persisted id:\n%s", got)
	}
	if strings.Contains(got, "msg-deadbeef") {
		t.Errorf("reminder quotes the in-memory msg- id, which no mailbox holds:\n%s", got)
	}
	if !strings.Contains(got, "NOT `gt mail send`") {
		t.Errorf("reminder lost the send-opens-a-new-thread warning:\n%s", got)
	}
}

// With no persisted id the reminder must not invent one. Telling the agent
// where to find the id is recoverable; naming an id that fails to resolve is
// the defect this test exists for.
func TestReplyReminderTextWithoutPersistedIDQuotesNoID(t *testing.T) {
	msg := &Message{
		ID:      "msg-deadbeef",
		From:    "mayor/",
		Subject: "a subject",
	}

	got := replyReminderText(msg)

	if strings.Contains(got, "msg-deadbeef") {
		t.Errorf("reminder fell back to the in-memory id:\n%s", got)
	}
	if !strings.Contains(got, "gt mail reply <id>") || !strings.Contains(got, "gt mail inbox") {
		t.Errorf("fallback does not tell the agent where to get the id:\n%s", got)
	}
}

func TestParseCreatedBeadID(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want string
	}{
		{"bd create --json", `{"created_at":"2026-09-16T04:38:25Z","id":"gt-wisp-abc123","status":"open","title":"t"}`, "gt-wisp-abc123"},
		{"leading warning line", "warning: beads.role not configured\n{\"id\":\"gt-wisp-xyz\"}", "gt-wisp-xyz"},
		{"human output, no json", "✓ Created issue: gt-wisp-abc — subject", ""},
		{"empty", "", ""},
		{"malformed json", "{not json", ""},
		{"json without id", `{"status":"open"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseCreatedBeadID([]byte(tc.out)); got != tc.want {
				t.Fatalf("parseCreatedBeadID(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}

// A bd that does not know --json must cost the send nothing: the flag only buys
// the assigned id, and the reminder degrades to telling the agent where to find
// it. Anything else is a real failure and must surface (gt-mnnx).
func TestBdRejectedJSONFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unknown flag", &bdError{Stderr: "Error: unknown flag: --json"}, true},
		{"unrelated bd failure", &bdError{Stderr: "Error: database is locked"}, false},
		{"not a bdError", errNotBd{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bdRejectedJSONFlag(tc.err); got != tc.want {
				t.Fatalf("bdRejectedJSONFlag(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

type errNotBd struct{}

func (errNotBd) Error() string { return "some other error" }

func TestWithoutJSONFlag(t *testing.T) {
	// The subject is positional, after "--", and sendToSingle uses that
	// delimiter precisely so a flag-like subject survives. A subject that IS
	// "--json" must therefore survive the fallback too; stripping it would send
	// a different message than the caller wrote (codex P2 on this PR).
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{
			"removes the option",
			[]string{"create", "--labels", "a,b", "--json", "--", "a subject"},
			[]string{"create", "--labels", "a,b", "--", "a subject"},
		},
		{
			"keeps a subject that is exactly --json",
			[]string{"create", "--json", "--", "--json"},
			[]string{"create", "--", "--json"},
		},
		{
			"keeps a subject containing --json",
			[]string{"create", "--json", "--", "subject --json"},
			[]string{"create", "--", "subject --json"},
		},
		{
			"no flag present",
			[]string{"create", "--", "a subject"},
			[]string{"create", "--", "a subject"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := withoutJSONFlag(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
