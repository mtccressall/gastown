package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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
			got, _ := composerLineFrom(tc.content, p)
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
	got, _ := composerLineFrom(content, "❯")
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

// TestComposerDetailJoinsWrappedComposer is the regression test for the wrapped
// composer, run against a REAL tmux because the defect and the fix both live in
// a capture-pane flag rather than in Go.
//
// Without -J tmux emits a soft-wrapped composer as several physical lines and the
// recorded text stops at the pane width — in the code whose purpose is preserving
// that text, with nothing marking the omission. RED before the -J: the captured
// detail holds only the first physical line.
func TestComposerDetailJoinsWrappedComposer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	const prefix = "❯"
	// Longer than the 30-column pane, so tmux must wrap it.
	const typed = "ABCDEFGHIJ0123456789abcdefghijKLMNOPQRSTUVWXYZ"

	socket := fmt.Sprintf("gt-composer-join-%d", os.Getpid())
	session := "composer-join"
	tm := NewTmuxWithSocket(socket)
	if out, err := tm.run("new-session", "-d", "-s", session, "-x", "30", "-y", "12", "cat"); err != nil {
		t.Skipf("cannot start tmux session: %v (%s)", err, out)
	}
	t.Cleanup(func() { _, _ = tm.run("kill-server") })

	if _, err := tm.run("send-keys", "-t", session, prefix+" "+typed); err != nil {
		t.Fatalf("send-keys: %v", err)
	}
	time.Sleep(400 * time.Millisecond)

	got, _ := tm.composerDetail(session, prefix)
	if !strings.Contains(got, typed) {
		t.Errorf("composerDetail lost the wrapped continuation.\n got: %q\nwant it to contain: %q", got, typed)
	}
}

// TestComposerLineFromFlagsCaptureEdge pins the second half of the acceptance: when
// the composer is the first captured line, earlier text may have scrolled out of
// range, and the reading must be reported as possibly incomplete rather than
// returned as a silently short string.
func TestComposerLineFromFlagsCaptureEdge(t *testing.T) {
	const prefix = "❯"
	atEdgeContent := prefix + " text at the very top of the capture\nsome later line\n"
	if _, atEdge := composerLineFrom(atEdgeContent, prefix); !atEdge {
		t.Error("composer on the first captured line should report atCaptureEdge=true")
	}
	notAtEdge := "an earlier line\n" + prefix + " composer text\n"
	if _, atEdge := composerLineFrom(notAtEdge, prefix); atEdge {
		t.Error("composer with a line above it must not report atCaptureEdge")
	}
}
