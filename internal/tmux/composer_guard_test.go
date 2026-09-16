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

// codex P1 on this change: during generation the last ❯ line is the turn the
// agent is ANSWERING, not a live composer. Reading it as an unsent draft would
// refuse every direct nudge mid-turn, and several direct callers do not queue,
// so the message would be lost rather than delayed.
func TestComposerTypedTextDeclinesOnABusyPane(t *testing.T) {
	const prefix = "❯"
	busy := "\x1b[39m❯ go fix the merge queue\n" +
		"  I'll start by reading the queue state.\n" +
		"  ⏵⏵ bypass permissions · esc to interrupt\n"

	if text, ok := composerTypedText(busy, prefix); ok {
		t.Errorf("claimed a draft on a BUSY pane: %q — that is the submitted turn, not a composer", text)
	}

	// The same pane once the turn ends and the text is genuinely a draft.
	idle := "\x1b[39m❯ go fix the merge queue\n" +
		"  ⏵⏵ bypass permissions\n"
	if _, ok := composerTypedText(idle, prefix); !ok {
		t.Error("declined on an IDLE pane holding typed text — the guard would never fire")
	}
}

// codex P2: an agent whose preset declares no ReadyPromptPrefix has no composer
// this code can find. Falling back to Claude's ❯ scans arbitrary output as
// though it were a composer.
func TestComposerPromptPrefixDoesNotFallBackForAgentsWithoutOne(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix string
		want   bool
	}{
		{"agent with no prompt prefix declines", "", false},
		{"claude's prefix still classifies", DefaultReadyPromptPrefix, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := "\x1b[39m❯ some typed text\n  ⏵⏵ bypass permissions\n"
			_, got := composerTypedText(content, tc.prefix)
			if got != tc.want {
				t.Fatalf("composerTypedText with prefix %q = %v, want %v", tc.prefix, got, tc.want)
			}
		})
	}
}

// The table above passes a prefix in directly, so it CANNOT see the call site
// choosing the wrong one — verified by sabotaging composerPromptPrefixForSession
// back to the fallback and watching the table stay green. This drives the real
// resolver against a real session whose GT_AGENT declares an agent with no
// prompt prefix, which is the case codex P2 is about.
func TestComposerPromptPrefixForSessionDeclinesForPrefixlessAgent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := fmt.Sprintf("gt-composer-prefix-%d", os.Getpid())
	tm := NewTmuxWithSocket(socket)
	t.Cleanup(func() { _, _ = tm.run("kill-server") })

	for _, tc := range []struct {
		session string
		agent   string
		want    string
	}{
		// copilot's preset declares ReadyPromptPrefix "" on purpose: it renders
		// hint text, not a detectable prompt.
		{"prefixless", "copilot", ""},
		{"claudeish", "claude", DefaultReadyPromptPrefix},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			if out, err := tm.run("new-session", "-d", "-s", tc.session, "-e", "GT_AGENT="+tc.agent, "cat"); err != nil {
				t.Skipf("cannot start tmux session: %v (%s)", err, out)
			}
			got := composerPromptPrefixForSession(tm, tc.session)
			if got != tc.want {
				t.Errorf("composerPromptPrefixForSession(GT_AGENT=%s) = %q, want %q", tc.agent, got, tc.want)
			}
		})
	}
}

// codex P1, second round: the first busy check scanned the WHOLE capture for
// "esc to interrupt". That string is also ordinary prose — this town's agents
// write it to each other constantly — so a pane whose scrollback merely
// DISCUSSES the marker would disable the guard and let the draft be submitted.
// The probe would have been defeated by its own subject matter.
func TestComposerTypedTextIgnoresBusyMarkerInScrollback(t *testing.T) {
	const prefix = "❯"

	// Idle pane. The marker appears only as quoted text in the transcript.
	content := "  I was explaining that a busy pane shows esc to interrupt in its status bar.\n" +
		"  That is how WaitForIdle decides.\n" +
		"\x1b[39m❯ dont send this yet\n" +
		"  ⏵⏵ bypass permissions\n"

	text, ok := composerTypedText(content, prefix)
	if !ok {
		t.Fatal("scrollback mentioning the busy marker disabled the guard; a draft would be submitted")
	}
	if text != "dont send this yet" {
		t.Errorf("got %q, want the draft", text)
	}
}

func TestHasBusyIndicatorInStatusArea(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  bool
	}{
		{"marker in the status bar", []string{"output", "❯ x", "⏵⏵ bypass · esc to interrupt"}, true},
		{"marker far up in scrollback", []string{"we discussed esc to interrupt earlier", "a", "b", "c", "❯ draft"}, false},
		{"blank lines do not consume the window", []string{"esc to interrupt", "", "", ""}, true},
		{"no marker", []string{"a", "b", "c"}, false},
		{"empty", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasBusyIndicatorInStatusArea(tc.lines); got != tc.want {
				t.Fatalf("hasBusyIndicatorInStatusArea(%q) = %v, want %v", tc.lines, got, tc.want)
			}
		})
	}
}
