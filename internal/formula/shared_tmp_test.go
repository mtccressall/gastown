package formula

import (
	"regexp"
	"strings"
	"testing"
)

// sharedTmpWrites matches a prescription that WRITES to a literal path under
// /tmp. Reads of the shared directory stay legal — scanning other processes'
// leftovers (`for pidfile in /tmp/dolt-test-server-*.pid`) is a real job, and
// so is naming the bad pattern in prose. Only the write is the defect.
var sharedTmpWrites = []struct {
	what string
	re   *regexp.Regexp
}{
	{"redirect into a fixed /tmp path", regexp.MustCompile(`>>?[ \t]*/tmp/`)},
	{"tee into a fixed /tmp path", regexp.MustCompile(`\btee\b[^|\n]*[ \t]/tmp/`)},
	{"mkdir of a fixed /tmp path", regexp.MustCompile(`\bmkdir\b[^|\n]*[ \t]/tmp/`)},
	{"mktemp forced into a fixed /tmp path", regexp.MustCompile(`\bmktemp\b[^|\n]*[ \t]/tmp/`)},
}

// TestFormulasDoNotWriteToSharedTmpPaths pins gastown-1k7.
//
// /tmp is shared by every agent on the host and Gas Town runs patrols
// concurrently by design — mol-pr-feedback-patrol's own docs say "run one
// patrol instance per rig". A formula that writes to a fixed /tmp filename is
// therefore a race between instances, and the loser reads the winner's file
// believing it read its own: rc=0, nothing on stderr, a result that is neither
// empty nor malformed. mol-pr-feedback-patrol then CREATES BEADS AND SLINGS
// POLECATS from what it read, so the failure direction is a write against
// another rig's PRs, not a missing answer.
//
// The invariant, rather than the instances: no shipped formula names a fixed
// /tmp path as a write target. Scope scratch to the agent (GT_ROLE), to the
// process, or to mktemp.
func TestFormulasDoNotWriteToSharedTmpPaths(t *testing.T) {
	entries, err := formulasFS.ReadDir("formulas")
	if err != nil {
		t.Fatalf("reading embedded formulas: %v", err)
	}

	scanned := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".formula.toml") {
			continue
		}
		content, err := formulasFS.ReadFile("formulas/" + entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		scanned++

		for lineNo, line := range strings.Split(string(content), "\n") {
			for _, pattern := range sharedTmpWrites {
				if pattern.re.MatchString(line) {
					t.Errorf("%s:%d %s\n\t%s\n\tUse a path scoped to this agent, e.g.\n"+
						"\t${TMPDIR:-/tmp}/gt-<what>/$(printf '%%s' \"${GT_ROLE:-unknown}\" | tr -c 'A-Za-z0-9._-' '-')",
						entry.Name(), lineNo+1, pattern.what, strings.TrimSpace(line))
				}
			}
		}
	}

	// A scan that matched nothing and a scan that found nothing return the same
	// zero. Carry the denominator so a vacuous pass is visible.
	if scanned == 0 {
		t.Fatal("scanned 0 formulas — the embed glob or the suffix filter is wrong, not the formulas")
	}
	t.Logf("scanned %d formulas", scanned)
}

func loadFormulaFile(t *testing.T, name string) *Formula {
	t.Helper()
	content, err := formulasFS.ReadFile("formulas/" + name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	f, err := Parse(content)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	return f
}

// TestPRFeedbackPatrolScopesScratchToTheAgent is the positive half: the
// invariant test above passes on a formula that writes nowhere at all, so it
// cannot tell "scoped correctly" from "stopped working". This asserts the
// replacement is actually present and actually private.
func TestPRFeedbackPatrolScopesScratchToTheAgent(t *testing.T) {
	f := loadFormulaFile(t, "mol-pr-feedback-patrol.formula.toml")

	// Every step that touches scratch must derive the directory itself; the
	// steps run in separate shells, so an unset PATROL_DIR silently resolves
	// to the filesystem root.
	for _, id := range []string{"list-open-prs", "check-review-status", "check-ci-status", "dispatch-work", "loop-or-exit"} {
		step := requireFormulaStep(t, f, id)
		if !strings.Contains(step.Description, `PATROL_DIR="${TMPDIR:-/tmp}/gt-pr-feedback-patrol/`) {
			t.Errorf("step %s does not derive PATROL_DIR — it cannot rely on another step's shell", id)
		}
		if !strings.Contains(step.Description, "GT_ROLE") {
			t.Errorf("step %s derives a scratch path that is not scoped to the agent", id)
		}
	}

	fetch := requireFormulaStep(t, f, "list-open-prs")
	if !strings.Contains(fetch.Description, `test("/" + $repo + "/pull/")`) {
		t.Error("list-open-prs must assert the fetched PR set belongs to {{repo}}: " +
			"a clobbered census has a plausible row count and does not name its source")
	}

	cleanup := requireFormulaStep(t, f, "loop-or-exit")
	if !strings.Contains(cleanup.Description, `rm -rf "$PATROL_DIR"`) {
		t.Error("loop-or-exit must remove only the directory this patrol owns")
	}
}
