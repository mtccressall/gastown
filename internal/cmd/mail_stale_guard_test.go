package cmd

import (
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/mail"
)

// gt-745z0: --stale is the ONE archive path that selects by pattern rather than
// by explicit id, so the protection that stops an agent clearing an inbox by
// looping a listing has to live in the command. AGE IS NOT EVIDENCE THAT A
// MESSAGE WAS DEALT WITH: this town has already lost a correction that was
// archived unread.
func TestStaleSelectionSeparatesReadFromUnread(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	msgs := []staleMessage{
		{Message: &mail.Message{ID: "gt-wisp-read1", Read: true, Timestamp: old}},
		{Message: &mail.Message{ID: "gt-wisp-unread", Read: false, Timestamp: old}},
		{Message: &mail.Message{ID: "gt-wisp-read2", Read: true, Timestamp: old}},
	}

	// THE FUNCTION THE COMMAND CALLS, not a copy of its logic.
	readable, unread := partitionStaleByRead(msgs)

	if len(unread) != 1 || unread[0].Message.ID != "gt-wisp-unread" {
		t.Fatalf("unread set = %v, want exactly the unread message", unread)
	}
	if len(readable) != 2 {
		t.Fatalf("readable set has %d, want the two read messages", len(readable))
	}
	for _, s := range readable {
		if !s.Message.Read {
			t.Errorf("%s would be archived by --stale despite being unread", s.Message.ID)
		}
	}
}

// A stale set that is entirely unread must leave NOTHING to archive, rather
// than falling through to a bulk close.
func TestStaleAllUnreadArchivesNothing(t *testing.T) {
	old := time.Now().Add(-72 * time.Hour)
	msgs := []staleMessage{
		{Message: &mail.Message{ID: "gt-wisp-a", Read: false, Timestamp: old}},
		{Message: &mail.Message{ID: "gt-wisp-b", Read: false, Timestamp: old}},
	}

	readable, unread := partitionStaleByRead(msgs)
	if len(unread) != 2 {
		t.Fatalf("unread = %d, want both", len(unread))
	}
	if len(readable) != 0 {
		t.Fatalf("readable = %d, want 0; an all-unread stale set must archive nothing", len(readable))
	}
}
