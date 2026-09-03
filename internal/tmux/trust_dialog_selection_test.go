package tmux

import "testing"

// gt-ma1: AcceptWorkspaceTrustDialog pressed Enter unconditionally, on the
// stated assumption that "option 1 is pre-selected" and accepts. On Claude's
// dialog "No, exit" is FIRST and carries the cursor, so the acceptor DECLINED
// and killed the session it was meant to admit — silently, and named for the
// opposite of what it did.
//
// The captured pane that proved it (gt-2t9s):
//
//	Quick safety check: Is this a project you created or one you trust?
//	> No, exit
//	  Yes, I trust this folder
//	  Enter to confirm - Esc to cancel
func TestTrustDialogSelection(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    trustSelection
	}{
		{
			name: "the gt-ma1 case: cursor on No, exit",
			content: "Quick safety check: Is this a project you created or one you trust?\n" +
				"> No, exit\n  Yes, I trust this folder\n  Enter to confirm - Esc to cancel",
			want: trustSelectionDecline,
		},
		{
			name: "cursor on Yes",
			content: "Quick safety check\n  No, exit\n> Yes, I trust this folder\n",
			want: trustSelectionAccept,
		},
		{
			name:    "alternate cursor glyph",
			content: "Quick safety check\n❯ No, exit\n  Yes, I trust this folder\n",
			want:    trustSelectionDecline,
		},
		{
			// The important one. An unreadable layout must NOT be guessed at:
			// a stalled agent is visible and recoverable, a declined one is dead
			// and silent. The caller returns an error rather than sending Enter.
			name:    "no cursor marker at all",
			content: "Quick safety check\n  No, exit\n  Yes, I trust this folder\n",
			want:    trustSelectionUnknown,
		},
		{
			name:    "empty pane",
			content: "",
			want:    trustSelectionUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := trustDialogSelection(tc.content); got != tc.want {
				t.Fatalf("trustDialogSelection() = %v, want %v", got, tc.want)
			}
		})
	}
}
