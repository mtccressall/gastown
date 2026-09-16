package templates

import (
	"regexp"
	"strings"
	"testing"
)

// The town-root CLAUDE.md is the source of record every agent in a town reads.
// gastown-1k7: the hand-maintained copy in this town prescribed
// `... --json > /tmp/all.json` for the anti-duplicate search that agents run
// BEFORE filing a bead. /tmp is shared by every agent on the host, so two
// concurrent patrols race on that one filename and the loser searches another
// rig's store — finds no duplicate, correctly, and files one.
//
// This pins the template so the shipped guidance cannot regrow the pattern.
func TestTownRootCLAUDEmdDoesNotPrescribeSharedTmpWrites(t *testing.T) {
	content := TownRootCLAUDEmd()

	// Writes only. Reading or globbing /tmp (scanning other processes'
	// leftovers) is legitimate, and so is naming the bad pattern in prose.
	writes := []struct {
		what string
		re   *regexp.Regexp
	}{
		{"redirect into a fixed /tmp path", regexp.MustCompile(`>>?[ \t]*/tmp/`)},
		{"tee into a fixed /tmp path", regexp.MustCompile(`\btee\b[^|\n]*[ \t]/tmp/`)},
		{"mkdir of a fixed /tmp path", regexp.MustCompile(`\bmkdir\b[^|\n]*[ \t]/tmp/`)},
	}

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		for _, w := range writes {
			if w.re.MatchString(line) {
				t.Errorf("town-root CLAUDE.md:%d %s\n\t%s", i+1, w.what, strings.TrimSpace(line))
			}
		}
	}
	if len(lines) < 10 {
		t.Fatalf("scanned %d lines — the template did not render, so this scan proved nothing", len(lines))
	}
}

// The negative test above also passes on a template that says nothing at all.
// This is the positive half: the guidance an agent needs must actually be there.
func TestTownRootCLAUDEmdTeachesScopedScratchPaths(t *testing.T) {
	content := TownRootCLAUDEmd()

	for _, want := range []string{
		"## Scratch files",
		"$(mktemp)",
		"${TMPDIR:-/tmp}",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("town-root CLAUDE.md does not carry %q", want)
		}
	}

	// The section is only useful if doctor reports a town that lacks it.
	var found bool
	for _, s := range TownRootRequiredSections() {
		if s.Heading == "## Scratch files" {
			found = true
		}
	}
	if !found {
		t.Error("scratch-file section is in the template but not in TownRootRequiredSections, " +
			"so existing towns are never told they are missing it")
	}
}
