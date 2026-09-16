package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The guard decides whether a nudge is typed into a live composer. Both
// directions cost something and they are not symmetric:
//
//	false when text IS typed  -> a human's unsent draft is submitted (gt-sglq)
//	true when it is NOT       -> nudges queue forever, town-wide
//
// So the placeholder cases below are as load-bearing as the typed ones. The
// separation is SGR 2 and nothing else: placeholder text is role-appropriate
// ("continue patrol") and a human has typed exactly that string in this town,
// so the words cannot be used to tell them apart.
func TestComposerTypedText(t *testing.T) {
	const prefix = "❯"

	tests := []struct {
		name     string
		content  string
		wantText string
		wantOK   bool
	}{
		{
			name:     "typed text carries no dim attribute",
			content:  "scrollback\n\x1b[39m❯ unblock brahmin\n  ⏵⏵ bypass permissions\n",
			wantText: "unblock brahmin",
			wantOK:   true,
		},
		{
			name:    "dim placeholder is NOT typed text",
			content: "scrollback\n\x1b[39m❯ \x1b[2mcontinue patrol\x1b[0m\n  ⏵⏵ bypass permissions\n",
			wantOK:  false,
		},
		{
			// The placeholder text differs per agent, so nothing may key on the
			// words. This is the same line with different content and must also
			// read as empty.
			name:    "a different agent's placeholder is also not typed text",
			content: "\x1b[39m❯ \x1b[2mkeep patrolling\x1b[0m\n  ⏵⏵ bypass permissions\n",
			wantOK:  false,
		},
		{
			name:    "empty composer",
			content: "some output\n❯\n  ⏵⏵ bypass permissions\n",
			wantOK:  false,
		},
		{
			name:    "no composer line at all",
			content: "just output\nnothing here\n",
			wantOK:  false,
		},
		{
			// Every ❯ in the scrollback marks a past user turn. Taking an earlier
			// one would report text that was submitted long ago as unsent.
			name:     "takes the LAST prompt line, not scrollback",
			content:  "❯ an older submitted turn\nmiddle\n\x1b[39m❯ live draft\n",
			wantText: "live draft",
			wantOK:   true,
		},
		{
			name:    "last prompt line is a placeholder even when scrollback has typed turns",
			content: "❯ an older submitted turn\nmiddle\n\x1b[39m❯ \x1b[2mcontinue patrol\x1b[0m\n",
			wantOK:  false,
		},
		{
			// An empty prefix would otherwise match every line and report the
			// pane's last line as a draft.
			name:    "empty prompt prefix claims nothing",
			content: "❯ something typed",
			wantOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := prefix
			if tc.name == "empty prompt prefix claims nothing" {
				p = ""
			}
			gotText, gotOK := composerTypedText(tc.content, p)
			if gotOK != tc.wantOK {
				t.Fatalf("composerTypedText() ok = %v, want %v (text %q)", gotOK, tc.wantOK, gotText)
			}
			if tc.wantOK && gotText != tc.wantText {
				t.Errorf("composerTypedText() text = %q, want %q", gotText, tc.wantText)
			}
			if !tc.wantOK && gotText != "" {
				t.Errorf("composerTypedText() returned %q alongside ok=false", gotText)
			}
		})
	}
}

// The error must carry the text it refused to type over. A refusal that does
// not say what it saw leaves the draft existing only in a pane nobody will
// attribute later, which is the recoverability half of gt-rd87.
func TestComposerHasTextErrorIsIdentifiableAndCarriesTheDraft(t *testing.T) {
	wrapped := fmt.Errorf("%w: %s holds %q", ErrComposerHasText, "hq-mayor", "make CI checks required on main")

	if !errors.Is(wrapped, ErrComposerHasText) {
		t.Fatal("wrapped error is not identifiable with errors.Is, so callers cannot queue on it")
	}
	if errors.Is(wrapped, ErrSubmitNotVerified) {
		t.Fatal("must not be confused with a stranded OWN message: different cause, same remedy")
	}
	if !strings.Contains(wrapped.Error(), "make CI checks required on main") {
		t.Errorf("refusal does not carry the draft it protected: %v", wrapped)
	}
}

// THE WIRING TEST. The table above is pure-function and stays GREEN when the
// guard is deleted from NudgeSessionWithOpts entirely — verified by removing it
// — so on its own it certifies a fix that is not connected to anything. This
// runs the real delivery path against a real tmux and asserts the property that
// matters: WITH A DRAFT IN THE COMPOSER, NOTHING IS TYPED.
//
// The socket is unique to this test and cleanup kills only that server, never
// the town's (gt-lgt4: a test that signals a whole session took this machine
// down four times in one morning).
func TestNudgeRefusesToTypeIntoADraftedComposer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	const draft = "unblock brahmin before the merge"
	const nudgeText = "NUDGETEXTTHATMUSTNOTBETYPED"

	socket := fmt.Sprintf("gt-composer-guard-%d", os.Getpid())
	session := "composer-guard"
	tm := NewTmuxWithSocket(socket)
	if out, err := tm.run("new-session", "-d", "-s", session, "-x", "120", "-y", "12", "cat"); err != nil {
		t.Skipf("cannot start tmux session: %v (%s)", err, out)
	}
	t.Cleanup(func() { _, _ = tm.run("kill-server") })

	// A human's unsent draft: typed, never submitted, and NOT dim.
	if _, err := tm.run("send-keys", "-t", session, DefaultReadyPromptPrefix+" "+draft); err != nil {
		t.Fatalf("send-keys: %v", err)
	}
	time.Sleep(400 * time.Millisecond)

	err := tm.NudgeSessionWithOpts(session, nudgeText, NudgeOpts{})

	if !errors.Is(err, ErrComposerHasText) {
		t.Errorf("delivery did not refuse: err = %v, want ErrComposerHasText", err)
	}

	// The load-bearing assertion. An error return proves what the function
	// SAID; only the pane proves what it DID.
	after, capErr := tm.run("capture-pane", "-p", "-J", "-t", session)
	if capErr != nil {
		t.Fatalf("capture-pane: %v", capErr)
	}
	if strings.Contains(after, nudgeText) {
		t.Errorf("nudge text was typed into a composer holding a draft:\n%s", after)
	}
	if !strings.Contains(after, draft) {
		t.Errorf("the draft did not survive delivery, which is the loss this guards:\n%s", after)
	}
}
