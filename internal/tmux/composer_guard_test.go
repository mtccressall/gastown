package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
const (
	rule   = "───────────────────────────────"
	footer = "  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents"
)

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

// THE WIRING TEST. The tables above are pure-function and stay GREEN when the
// guard is deleted from NudgeSessionWithOpts entirely — verified by removing it
// — so on their own they certify a fix connected to nothing. This runs the real
// delivery path against a real tmux and asserts the property that matters: WITH
// A DRAFT IN THE COMPOSER, NOTHING IS TYPED.
//
// The pane RENDERS the frame rather than having it typed in, because a composer
// is the second line from the bottom and anything typed into a bare shell lands
// last. What is under test here is the wiring — that delivery consults the guard
// and returns before sendMessageToTarget — not tmux's own rendering, which the
// fixtures above cover with text captured from live panes.
//
// The socket is unique to this test and cleanup kills only that server, never
// the town's (gt-lgt4: a test that signalled a whole session took this machine
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
	// Written to a file and cat'd: sh's printf does not portably grok \x
	// escapes, and a half-rendered frame would make the refusal below vacuous.
	frame := "transcript output\n" + rule + "\n\x1b[39m❯\u00a0" + draft + "\n" + rule + "\n" + footer + "\n"
	framePath := filepath.Join(t.TempDir(), "frame")
	if err := os.WriteFile(framePath, []byte(frame), 0o600); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if out, err := tm.run("new-session", "-d", "-s", session, "-x", "120", "-y", "12",
		"sh", "-c", "cat "+framePath+"; sleep 120"); err != nil {
		t.Skipf("cannot start tmux session: %v (%s)", err, out)
	}
	t.Cleanup(func() { _, _ = tm.run("kill-server") })
	time.Sleep(500 * time.Millisecond)

	// Guard the guard: if the frame did not render, the refusal below would be
	// vacuous and this test would pass for the wrong reason.
	if pre, err := tm.run("capture-pane", "-p", "-J", "-t", session); err != nil || !strings.Contains(pre, draft) {
		t.Skipf("pane did not render the frame (err %v):\n%s", err, pre)
	}

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

// Fixtures transcribed from REAL panes on this host, 2026-09-16, because the
// two previous designs of this guard were argued rather than measured and both
// were wrong. The layout is rule / composer / rule / footer, and it is the SAME
// while the agent is generating — the busy marker joins the footer line instead
// of replacing the composer box.
const (
	paneIdleWithDraft = "  some transcript output\n" +
		"───────────────────────────────\n" +
		"\x1b[39m❯ dont send this yet\n" +
		"───────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\n"

	paneIdlePlaceholder = "  some transcript output\n" +
		"───────────────────────────────\n" +
		"\x1b[39m❯ \x1b[2mcontinue patrol\x1b[0m\n" +
		"───────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\n"

	// Captured from hq-deacon mid-turn: the composer box is still there.
	paneBusyWithDraft = "✶ Cultivating… (1d 5h 32m · ↓ 547.5k tokens)\n" +
		"  ⎿  Tip: Use /clear to start fresh\n" +
		"───────────────────────────────\n" +
		"\x1b[39m❯ typed while it was thinking\n" +
		"───────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · ← for ag…\n"

	// A ❯ in the transcript must never be read as the composer.
	paneScrollbackPromptOnly = "❯ a turn submitted ten minutes ago\n" +
		"  the agent answered it at length\n" +
		"  and mentioned esc to interrupt while doing so\n" +
		"───────────────────────────────\n" +
		"\x1b[39m❯ \x1b[2mcontinue patrol\x1b[0m\n" +
		"───────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\n"
)

func TestComposerTypedTextAgainstRealPaneLayouts(t *testing.T) {
	// The real prefix carries a trailing space; using a bare glyph here would
	// test a prefix no caller passes.
	const prefix = DefaultReadyPromptPrefix
	for _, tc := range []struct {
		name     string
		pane     string
		wantText string
		wantOK   bool
	}{
		{"idle pane holding a draft", paneIdleWithDraft, "dont send this yet", true},
		{"idle pane showing the dim placeholder", paneIdlePlaceholder, "", false},
		{
			// The earlier design DECLINED here, which was backwards: text typed
			// during generation is queued input and is exactly the draft the
			// next Enter would submit.
			"BUSY pane holding a draft is still protected", paneBusyWithDraft, "typed while it was thinking", true,
		},
		{
			// Footer anchoring is what makes this safe: the scrollback ❯ is not
			// adjacent to the footer, so it is never a candidate.
			"submitted turn in scrollback is not a draft", paneScrollbackPromptOnly, "", false,
		},
		{
			// SEPARATES FOOTER ANCHORING FROM "take the last ❯". A pane with no
			// composer frame — a plain shell, or Claude before its TUI paints —
			// has prompt characters and no composer. The scan version calls this
			// a draft and refuses every nudge to that session forever; anchoring
			// declines and delivery behaves as it always did.
			"no composer frame at all", "$ ls\n❯ not a composer, just output\n$ \n", "", false,
		},
		{
			"empty composer", "out\n" + rule + "\n❯\n" + rule + "\n" + footer + "\n", "", false,
		},
		{
			// Placeholder text differs per agent, so nothing may key on the words.
			"another agent's placeholder", "out\n" + rule + "\n\x1b[39m❯\u00a0\x1b[2mkeep patrolling\x1b[0m\n" + rule + "\n" + footer + "\n", "", false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := composerTypedText(tc.pane, prefix)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (text %q)", ok, tc.wantOK, got)
			}
			if got != tc.wantText {
				t.Errorf("text = %q, want %q", got, tc.wantText)
			}
		})
	}
}

// A pane that does not have the expected shape must DECLINE rather than guess:
// delivery then behaves as it always did.
func TestComposerLineIndexByFooterDeclinesOnUnknownShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  int
	}{
		{"no footer", []string{"❯ text", "more output"}, -1},
		{"footer but no rule above", []string{"❯ text", "  ⏵⏵ bypass permissions on"}, -1},
		{"empty", nil, -1},
		{"well formed", []string{"out", "──────", "❯ draft", "──────", "  ⏵⏵ bypass permissions on"}, 2},
		{"trailing blank lines do not hide the footer", []string{"──────", "❯ draft", "──────", "  ⏵⏵ bypass permissions on", "", ""}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := composerLineIndexByFooter(tc.lines); got != tc.want {
				t.Fatalf("composerLineIndexByFooter = %d, want %d", got, tc.want)
			}
		})
	}
}

// codex P1, third round: the guard knows only Claude's frame. This pins that as
// a STATED scope rather than an accident of a footer that happens never to
// match, and records that those agents are unprotected — the honest status,
// since I have no Codex or Gemini pane to measure and the two previous designs
// failed exactly by describing a TUI I had not captured.
func TestComposerGuardScopeIsClaudeOnlyAndSaysSo(t *testing.T) {
	if composerLayoutSupported(DefaultReadyPromptPrefix) != true {
		t.Fatal("claude's layout must be supported or the guard protects nobody")
	}
	for _, prefix := range []string{"› ", "> ", ""} {
		if composerLayoutSupported(prefix) {
			t.Errorf("claimed support for prefix %q whose frame has never been captured", prefix)
		}
		// And the decline must be total: no classification from a Claude-shaped
		// frame that happens to be rendered by another agent.
		if text, ok := composerTypedText(paneIdleWithDraft, prefix); ok {
			t.Errorf("prefix %q classified a draft %q despite unsupported layout", prefix, text)
		}
	}
}
