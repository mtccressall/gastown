package tmux

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrSubmitNotVerified reports that a nudge payload was typed, but the
// transport could not prove it left the target composer.
var ErrSubmitNotVerified = errors.New("submit not verified: message stranded in composer")

// ErrComposerHasText is returned INSTEAD OF TYPING when the target's composer
// already holds text somebody typed and has not submitted. Delivery types the
// nudge into that same composer and presses Enter, which submits the human's
// unsent draft joined to our notification — reproduced on both delivery paths
// (gt-sglq). Callers treat this like ErrSubmitNotVerified and QUEUE: a late
// nudge is recoverable, a submitted draft is not.
var ErrComposerHasText = errors.New("composer holds unsubmitted text: refusing to type")

// composerTypedText reports text a person typed into the composer, and
// distinguishes it from the DIM PLACEHOLDER an idle pane shows.
//
// The placeholder is rendered with SGR 2 and is not input: clearing or typing
// over it destroys nothing. Typed text carries no dim attribute. That is the
// only separation available — the words themselves cannot be trusted, because
// placeholders are role-appropriate ("continue patrol") and a human has typed
// exactly that string in this town.
//
// It returns ("", false) when there is no composer line, when the composer is
// empty, when every content rune is dim, and when the prompt prefix is empty.
// Every one of those is a REFUSAL TO CLAIM typed text, because the caller acts
// on true by withholding delivery and on false by typing.
func composerTypedText(escContent, promptPrefix string) (string, bool) {
	if promptPrefix == "" {
		return "", false
	}
	plain, dim := stripAnsiTrackDim(escContent)
	// A BUSY PANE HAS NO LIVE COMPOSER, AND ITS LAST PROMPT LINE IS THE TURN THE
	// AGENT IS CURRENTLY ANSWERING (codex P1 on this change). Reading that as an
	// unsent draft would refuse every direct nudge during an active turn, and
	// several direct callers do not queue. Declining here keeps the guard scoped
	// to the defect it is for: WaitForIdle calling a DRAFTED composer idle.
	for _, line := range strings.Split(string(plain), "\n") {
		if hasBusyIndicator(line) {
			return "", false
		}
	}
	lines, dims := splitRunesAndDim(plain, dim)
	for i := len(lines) - 1; i >= 0; i-- {
		if !matchesPromptPrefix(string(lines[i]), promptPrefix) {
			continue
		}
		content, contentDim := composerContent(lines[i], dims[i], promptPrefix)
		if len(content) == 0 || allDim(contentDim) {
			return "", false
		}
		return strings.TrimSpace(string(content)), true
	}
	return "", false
}

// composerHoldsTypedText captures the target and applies composerTypedText.
//
// The -J matters for the same reason it does in composerDetail: without it a
// soft-wrapped composer arrives as several physical lines and the text we
// report is truncated at the pane width. A capture ERROR returns false, so a
// broken probe cannot silence every nudge in the town; a capture that SUCCEEDS
// and shows typed text refuses delivery. Those two failure directions are
// deliberately different.
func (t *Tmux) composerHoldsTypedText(target, promptPrefix string) (string, bool) {
	content, err := t.run("capture-pane", "-p", "-e", "-J", "-t", target, "-S", "-25")
	if err != nil {
		return "", false
	}
	return composerTypedText(content, promptPrefix)
}

type submitProbe int

const (
	probeUnknown submitProbe = iota
	probeTurnStarted
	probeComposerCleared
	probeStranded
	probeComposerDirty
)

func (p submitProbe) String() string {
	switch p {
	case probeTurnStarted:
		return "turn-started"
	case probeComposerCleared:
		return "composer-cleared"
	case probeStranded:
		return "stranded"
	case probeComposerDirty:
		return "composer-dirty"
	default:
		return "unknown"
	}
}

const (
	submitProbeAttempts  = 3
	submitProbeInterval  = 700 * time.Millisecond
	submitNeedleMaxRunes = 32
	minStrandPrefixRunes = 8
)

func submitNeedle(message string) string {
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > submitNeedleMaxRunes {
			return string(runes[:submitNeedleMaxRunes])
		}
		return line
	}
	return ""
}

func applySGR(params string, dim bool) bool {
	if params == "" {
		return false
	}
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "", "0":
			dim = false
		case "2":
			dim = true
		case "22":
			dim = false
		case "38", "48", "58":
			if i+1 >= len(fields) {
				continue
			}
			switch fields[i+1] {
			case "5":
				i += 2
			case "2":
				i += 4
			}
		}
	}
	return dim
}

func stripAnsiTrackDim(s string) ([]rune, []bool) {
	var plain []rune
	var dim []bool
	curDim := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if i+1 < len(s) && s[i+1] == '[' {
				j := i + 2
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j >= len(s) {
					break
				}
				if s[j] == 'm' {
					curDim = applySGR(s[i+2:j], curDim)
				}
				i = j + 1
				continue
			}
			if i+1 < len(s) && s[i+1] == ']' {
				j := i + 2
				for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
					j++
				}
				if j >= len(s) {
					break
				}
				if s[j] == 0x1b {
					j++
				}
				i = j + 1
				continue
			}
			i += 2
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		plain = append(plain, r)
		dim = append(dim, curDim)
		i += size
	}
	return plain, dim
}

func runeIndex(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func allDim(flags []bool) bool {
	if len(flags) == 0 {
		return false
	}
	for _, flag := range flags {
		if !flag {
			return false
		}
	}
	return true
}

func splitRunesAndDim(plain []rune, dim []bool) ([][]rune, [][]bool) {
	var lines [][]rune
	var lineDims [][]bool
	start := 0
	for i := 0; i <= len(plain); i++ {
		if i == len(plain) || plain[i] == '\n' {
			lines = append(lines, plain[start:i])
			lineDims = append(lineDims, dim[start:i])
			start = i + 1
		}
	}
	return lines, lineDims
}

func trimRunesAndDim(runes []rune, dim []bool) ([]rune, []bool) {
	isSpace := func(r rune) bool { return r == ' ' || r == '\t' || r == '\u00a0' }
	for len(runes) > 0 && isSpace(runes[0]) {
		runes = runes[1:]
		dim = dim[1:]
	}
	for len(runes) > 0 && isSpace(runes[len(runes)-1]) {
		runes = runes[:len(runes)-1]
		dim = dim[:len(dim)-1]
	}
	return runes, dim
}

func composerContent(line []rune, dim []bool, promptPrefix string) ([]rune, []bool) {
	prefix := []rune(strings.TrimSpace(strings.ReplaceAll(promptPrefix, "\u00a0", " ")))
	if len(prefix) == 0 {
		return nil, nil
	}
	idx := runeIndex(line, prefix)
	if idx < 0 {
		idx = runeIndex(line, prefix[:1])
	}
	if idx < 0 {
		return nil, nil
	}
	after := line[idx+len(prefix):]
	afterDim := dim[idx+len(prefix):]
	return trimRunesAndDim(after, afterDim)
}

func analyzeComposerLine(line []rune, dim []bool, needle, promptPrefix string) submitProbe {
	needleRunes := []rune(needle)
	if idx := runeIndex(line, needleRunes); idx >= 0 {
		if allDim(dim[idx : idx+len(needleRunes)]) {
			return probeComposerCleared
		}
		return probeStranded
	}

	content, contentDim := composerContent(line, dim, promptPrefix)
	if len(content) == 0 {
		return probeComposerCleared
	}
	if allDim(contentDim) {
		return probeComposerCleared
	}
	if len(content) >= minStrandPrefixRunes && strings.HasPrefix(needle, string(content)) {
		return probeStranded
	}
	return probeComposerDirty
}

func analyzeSubmission(escContent, needle, promptPrefix string) submitProbe {
	if needle == "" || promptPrefix == "" {
		return probeUnknown
	}
	plain, dim := stripAnsiTrackDim(escContent)
	lines, lineDims := splitRunesAndDim(plain, dim)

	for i := len(lines) - 1; i >= 0; i-- {
		if matchesPromptPrefix(string(lines[i]), promptPrefix) {
			return analyzeComposerLine(lines[i], lineDims[i], needle, promptPrefix)
		}
	}

	for _, line := range lines {
		if hasBusyIndicator(string(line)) {
			return probeTurnStarted
		}
	}
	return probeUnknown
}

// composerLineFrom returns the plain text of the composer line, or "" if none is
// found. It reuses the same stripping and prompt-matching that analyzeSubmission
// does, so it sees exactly the line the probe classified.
//
// gt-rd87: when the probe reports probeComposerDirty the nudge's typed payload has
// been left APPENDED to whatever was already in the composer, and on this town's
// rules that may be a human's unsent instruction (gt-sglq). We deliberately do NOT
// clear it — the cost of destroying unsent text is unknown and clearing is exactly
// what CLAUDE.md forbids doing blind. What we can do is WRITE THE TEXT OUT, which
// converts a silent ambiguity into a recorded one: the concatenated line reaches the
// caller in the error, so it is recoverable from the nudge's output instead of
// existing only in a pane nobody will attribute later.
// It returns the line and atCaptureEdge, which reports that the composer was the
// FIRST line of the captured range. The capture starts a fixed number of lines
// back, so a composer at index 0 may have had earlier content scrolled out of
// range, and the text returned is then a floor rather than the whole composer.
// The caller must say so: a short string that looks complete is the false-complete
// shape this town keeps paying for, and here the captured text is what someone
// later relies on to decide whether a human typed something.
func composerLineFrom(escContent, promptPrefix string) (string, bool) {
	if promptPrefix == "" {
		return "", false
	}
	plain, dim := stripAnsiTrackDim(escContent)
	lines, _ := splitRunesAndDim(plain, dim)
	for i := len(lines) - 1; i >= 0; i-- {
		if matchesPromptPrefix(string(lines[i]), promptPrefix) {
			return strings.TrimSpace(string(lines[i])), i == 0
		}
	}
	return "", false
}

// composerDetail captures the target's composer as ONE logical line.
//
// The -J is load-bearing and is why this is a separate method from the probe
// capture. Without it tmux emits a soft-wrapped composer as several PHYSICAL
// lines, composerLineFrom returns only the one carrying the prompt, and the
// recorded text is silently truncated at the pane width — in the code whose
// reason for existing is preserving that text. Verified against a real tmux at
// 30 columns: "-p -e" splits a 40-character composer mid-word, "-p -e -J"
// returns it whole (gt-rd87, codex P2 on PR #50).
//
// The probe capture in probeSubmission deliberately does NOT take -J: it feeds
// classification, not recovery, and joining lines there would change what
// analyzeSubmission sees.
func (t *Tmux) composerDetail(target, promptPrefix string) (string, bool) {
	content, err := t.run("capture-pane", "-p", "-e", "-J", "-t", target, "-S", "-25")
	if err != nil {
		return "", false
	}
	return composerLineFrom(content, promptPrefix)
}

func (t *Tmux) probeSubmission(target, needle, promptPrefix string) submitProbe {
	content, err := t.run("capture-pane", "-p", "-e", "-t", target, "-S", "-25")
	if err != nil {
		return probeUnknown
	}
	return analyzeSubmission(content, needle, promptPrefix)
}

func (t *Tmux) pollSubmission(target, needle, promptPrefix string, attempts int) submitProbe {
	last := probeUnknown
	stranded := false
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(submitProbeInterval)
		}
		probe := t.probeSubmission(target, needle, promptPrefix)
		switch probe {
		case probeTurnStarted, probeComposerCleared:
			return probe
		case probeComposerDirty:
			return probe
		case probeStranded:
			stranded = true
		}
		last = probe
	}
	if stranded {
		return probeStranded
	}
	return last
}

func (t *Tmux) submitComposer(target, message, promptPrefix string) error {
	enterErr := t.sendEnterVerified(target)
	needle := submitNeedle(message)
	if needle == "" {
		return enterErr
	}

	switch t.pollSubmission(target, needle, promptPrefix, submitProbeAttempts) {
	case probeTurnStarted, probeComposerCleared:
		return nil
	case probeUnknown:
		return enterErr
	case probeComposerDirty:
		// gt-rd87: capture what is actually in the composer before returning. The
		// nudge payload is now concatenated with pre-existing text and nothing
		// distinguishes the two halves in the pane, so this error is the only
		// record of what was there.
		detail := ""
		if line, atEdge := t.composerDetail(target, promptPrefix); line != "" {
			detail = fmt.Sprintf(" [composer now reads: %q]", line)
			if atEdge {
				detail += " [WARNING: the composer was the first captured line, so earlier text may have scrolled out of range and this reading may be incomplete]"
			}
		}
		return fmt.Errorf("%w (composer contains other text after Enter; nudge payload was NOT cleared and is appended to it)%s", ErrSubmitNotVerified, detail)
	case probeStranded:
		return t.recoverStrandedComposer(target, message, needle, promptPrefix)
	default:
		return enterErr
	}
}

func (t *Tmux) recoverStrandedComposer(target, message, needle, promptPrefix string) error {
	if _, err := t.run("send-keys", "-t", target, "C-j"); err != nil {
		return fmt.Errorf("%w (C-j reset failed: %v)", ErrSubmitNotVerified, err)
	}
	time.Sleep(500 * time.Millisecond)

	switch probe := t.probeSubmission(target, needle, promptPrefix); probe {
	case probeTurnStarted:
		return nil
	case probeComposerCleared:
		if err := t.sendMessageToTarget(target, message); err != nil {
			return fmt.Errorf("%w (retype failed: %v)", ErrSubmitNotVerified, err)
		}
		time.Sleep(adaptiveTextDelay(len(message)))
		_ = t.sendEnterVerified(target)
	case probeStranded, probeComposerDirty, probeUnknown:
		return fmt.Errorf("%w (composer state after C-j: %s)", ErrSubmitNotVerified, probe)
	}

	switch probe := t.pollSubmission(target, needle, promptPrefix, submitProbeAttempts); probe {
	case probeTurnStarted, probeComposerCleared:
		return nil
	default:
		return fmt.Errorf("nudge submit to %q: %w (final state: %s)", target, ErrSubmitNotVerified, probe)
	}
}
