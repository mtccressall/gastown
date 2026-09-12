package tmux

import (
	"strings"
	"testing"
)

// TestComposerLineFrom is the regression test for gt-rd87 hazard 1.
//
// When the submit probe reports probeComposerDirty, the nudge's typed payload has been
// left APPENDED to whatever was already in the composer — possibly a human's unsent
// instruction. We do not clear it: destroying unsent text has unknown cost and is what
// CLAUDE.md forbids doing blind. Instead the concatenated line must reach the caller so
// the text is recoverable rather than existing only in a pane nobody can attribute.
//
// This pins that composerLineFrom finds the composer line the probe classified.
func TestComposerLineFrom(t *testing.T) {
	const prefix = "❯"

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "dirty composer carries both halves",
			content: "some scrollback\n❯ unblock brahmin HEALTH_CHECK: are you alive\n  ⏵⏵ bypass permissions\n",
			want:    "❯ unblock brahmin HEALTH_CHECK: are you alive",
		},
		{
			name:    "takes the LAST prompt line, not an earlier one in scrollback",
			content: "❯ an older submitted turn\nmiddle\n❯ the live composer\n",
			want:    "❯ the live composer",
		},
		{
			name:    "no prompt line yields empty, not a false capture",
			content: "just output\nno composer here\n",
			want:    "",
		},
		{
			name:    "empty prompt prefix yields empty rather than matching everything",
			content: "❯ something",
			want:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := prefix
			if tc.name == "empty prompt prefix yields empty rather than matching everything" {
				p = ""
			}
			got := composerLineFrom(tc.content, p)
			if got != tc.want {
				t.Errorf("composerLineFrom() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestComposerLineFrom_StripsANSI verifies the capture reports the TEXT a human would
// read rather than the escape-laden pane bytes — a dim placeholder and typed text must
// both come back as plain text, since the caller puts this in an error message.
func TestComposerLineFrom_StripsANSI(t *testing.T) {
	content := "\x1b[2m❯ dim placeholder text\x1b[0m\n"
	got := composerLineFrom(content, "❯")
	if got == "" {
		t.Fatalf("composerLineFrom returned empty for an ANSI-wrapped composer line")
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("composerLineFrom leaked ANSI escapes: %q", got)
	}
	if !strings.Contains(got, "dim placeholder text") {
		t.Errorf("composerLineFrom lost the text: %q", got)
	}
}
