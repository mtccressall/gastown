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

// THE WIRING TEST. gastown/refinery's instance five: extracting the predicate so
// a test can drive it protects the LOGIC and not the WIRING — a test on
// partitionStaleByRead passes whether or not the command still calls it. The
// assertion that matters is that nothing unread is ever handed to delete, and it
// can only be made where delete is visible.
func TestArchiveStaleSetNeverDeletesUnread(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	msgs := []staleMessage{
		{Message: &mail.Message{ID: "gt-wisp-read", Read: true, Timestamp: old}},
		{Message: &mail.Message{ID: "gt-wisp-unread", Read: false, Timestamp: old}},
	}

	var deleted []string
	del := func(id string) error {
		deleted = append(deleted, id)
		return nil
	}

	if err := archiveStaleSet(msgs, del); err != nil {
		t.Fatalf("archiveStaleSet: %v", err)
	}

	for _, id := range deleted {
		if id == "gt-wisp-unread" {
			t.Errorf("an UNREAD stale message was handed to delete: %v", deleted)
		}
	}
	if len(deleted) != 1 || deleted[0] != "gt-wisp-read" {
		t.Fatalf("deleted = %v, want exactly the read message", deleted)
	}
}

// An all-unread stale set must hand NOTHING to delete, rather than falling
// through to a bulk close — the failure this town has already paid for.
func TestArchiveStaleSetAllUnreadDeletesNothing(t *testing.T) {
	old := time.Now().Add(-72 * time.Hour)
	msgs := []staleMessage{
		{Message: &mail.Message{ID: "gt-wisp-a", Read: false, Timestamp: old}},
		{Message: &mail.Message{ID: "gt-wisp-b", Read: false, Timestamp: old}},
	}

	called := 0
	del := func(string) error { called++; return nil }

	if err := archiveStaleSet(msgs, del); err != nil {
		t.Fatalf("archiveStaleSet: %v", err)
	}
	if called != 0 {
		t.Fatalf("delete was called %d times on an all-unread set; want 0", called)
	}
}
