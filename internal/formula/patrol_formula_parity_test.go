package formula

import (
	"regexp"
	"strings"
	"testing"
)

// The three agent patrol formulas ship twice: embedded here (the source of truth)
// and provisioned to <town>/.beads/formulas/. gastown-66z is what happens when the
// two drift -- an operator reads one copy and cannot see what the other has.
//
// These tests pin PROPERTIES of the embedded copies, not the current text of them,
// so a future edit that reintroduces the drift fails here rather than in a patrol
// cycle six weeks later.
var (
	// "--steps flag is REQUIRED", with or without backticks around the flag.
	stepsRequired = regexp.MustCompile("`?--steps`? flag is REQUIRED")
	// "ALL 26 steps" and friends: a step count frozen into prose.
	hardcodedStepCount = regexp.MustCompile(`(?i)\ball \d+ steps\b`)
)

var agentPatrolFormulas = []string{
	"mol-deacon-patrol",
	"mol-witness-patrol",
	"mol-refinery-patrol",
}

func loadEmbeddedFormula(t *testing.T, name string) (*Formula, string) {
	t.Helper()
	content, err := formulasFS.ReadFile("formulas/" + name + ".formula.toml")
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	f, err := Parse(content)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	return f, string(content)
}

// liveWispGC matches a `bd mol wisp gc` invocation that an agent would actually run:
// a line whose first token is the command. Prose that quotes the command in order to
// forbid it is deliberately NOT matched -- the town copies suspend the command by
// commenting it out and then explaining why at length, and that explanation must
// survive this check or the check would force the reason to be deleted with the risk.
var liveWispGC = regexp.MustCompile(`(?m)^[ \t]*bd mol wisp gc\b`)

// A patrol formula must not ship an executable `bd mol wisp gc`.
//
// `bd mol wisp gc --closed --force` purges closed wisps AND the notes on them,
// which is where `gt patrol report` stores each cycle's summary. `--age` additionally
// reaps unread mail and hooked work (gt-tfr, still open). Both are suspended in the
// town copies; this test stops the embedded copies from handing the command back to
// every newly provisioned rig.
func TestPatrolFormulasDoNotShipLiveWispGC(t *testing.T) {
	for _, name := range agentPatrolFormulas {
		t.Run(name, func(t *testing.T) {
			f, _ := loadEmbeddedFormula(t, name)
			scanned := 0
			for _, step := range f.Steps {
				scanned++
				if got := liveWispGC.FindString(step.Description); got != "" {
					t.Errorf("step %q contains a live wisp GC invocation: %q\n"+
						"Suspended by gt-tfr/gt-tgf0. Comment it out and keep the explanation.",
						step.ID, strings.TrimSpace(got))
				}
			}
			if scanned == 0 {
				t.Fatalf("scanned 0 steps -- the formula parsed to nothing, so this test proved nothing")
			}
		})
	}
}

// Any patrol formula that tells the agent to run `gt patrol report` must also teach
// `--steps`, and must say it is required.
//
// Without --steps the audit records "Steps: NOT REPORTED" and says so to nobody.
// The guidance existed only in the town copies (gastown-66z), so an agent resolving
// the embedded copy was never told the flag exists.
func TestPatrolFormulasTeachStepsFlag(t *testing.T) {
	for _, name := range agentPatrolFormulas {
		t.Run(name, func(t *testing.T) {
			f, content := loadEmbeddedFormula(t, name)
			if !strings.Contains(content, "gt patrol report") {
				t.Skip("formula does not instruct gt patrol report")
			}
			if !strings.Contains(content, "--steps") {
				t.Fatalf("instructs `gt patrol report` but never names --steps")
			}
			if !stepsRequired.MatchString(content) {
				t.Fatalf("names --steps but does not state that it is REQUIRED")
			}
			// A hardcoded step count in the prose goes stale the moment a step is added,
			// and then the instructions disagree with the formula they describe -- which
			// is gastown-66z one level down. mol-deacon-patrol said "ALL 26 steps" while
			// carrying 28.
			for _, m := range hardcodedStepCount.FindAllStringSubmatch(content, -1) {
				t.Errorf("instruction text hardcodes %q but the formula has %d steps; "+
					"say \"every step\" instead", m[0], len(f.Steps))
			}
			// If the guidance enumerates the step IDs, the enumeration is a second
			// copy of the step list and drifts from the first exactly the way the two
			// FILE copies did. Pin it to the formula.
			if listed := listedStepIDs(content); listed != nil {
				actual := map[string]bool{}
				for _, step := range f.Steps {
					actual[step.ID] = true
				}
				for _, id := range listed {
					if !actual[id] {
						t.Errorf("--steps guidance lists %q, which is not a step in this formula", id)
					}
					delete(actual, id)
				}
				for id := range actual {
					t.Errorf("step %q is missing from the --steps guidance, so an agent reading "+
						"this formula never learns it exists", id)
				}
			}
		})
	}
}

// Every agent patrol formula carries the observed-vs-concluded discipline step.
// The refinery copy was the one that lacked it (gastown-66z), in the embedded copy only.
func TestAgentPatrolFormulasCarryObservedVsConcluded(t *testing.T) {
	for _, name := range agentPatrolFormulas {
		t.Run(name, func(t *testing.T) {
			f, _ := loadEmbeddedFormula(t, name)
			step := requireFormulaStep(t, f, "observed-vs-concluded")
			if len(step.Needs) == 0 {
				t.Fatalf("observed-vs-concluded has no predecessor, so it can run first and grade nothing")
			}
			var dependents int
			for _, other := range f.Steps {
				if containsStepNeed(other, "observed-vs-concluded") {
					dependents++
				}
			}
			if dependents == 0 {
				t.Fatalf("nothing needs observed-vs-concluded, so it is not wired into the chain")
			}
		})
	}
}

// listedStepIDs returns the step IDs enumerated in the --steps guidance block, or nil
// when the formula does not enumerate them.
func listedStepIDs(content string) []string {
	const marker = "The step IDs for this formula are:"
	i := strings.Index(content, marker)
	if i < 0 {
		return nil
	}
	rest := content[i+len(marker):]
	var body []string
	for _, line := range strings.Split(rest, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(body) > 0 {
				break
			}
			continue
		}
		if !strings.HasPrefix(line, "    ") {
			break
		}
		body = append(body, trimmed)
	}
	var ids []string
	for _, part := range strings.Split(strings.Join(body, " "), ",") {
		if part = strings.TrimSpace(part); part != "" {
			ids = append(ids, part)
		}
	}
	return ids
}
