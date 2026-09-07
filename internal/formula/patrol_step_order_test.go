package formula

import (
	"slices"
	"sort"
	"testing"
)

// TestPatrolFormulasSequenceEveryStep pins the invariant, not the instance.
//
// A patrol formula is read by an agent as a top-to-bottom checklist but executed
// as a dependency graph, and a step nothing sequences is silently exempt from
// both readings: it becomes runnable at the very start and is never required
// before the cycle ends.
//
// gastown-c1x added dispatch-ready-work to mol-deacon-patrol by copying it
// verbatim from the town's live formula, where it carries no needs at all. It
// parsed, sorted and rendered fine while sitting second in the execution order —
// beside heartbeat, twenty-six steps from where the file puts it — and
// loop-or-exit could complete without it ever running. A test that only checked
// the step existed would have passed, which is exactly how the live formula has
// carried the same hole.
//
// Two properties, both cheap:
//
//	one root      — exactly one step is ready before anything has run
//	no orphans    — the final step transitively needs every other step
//
// Declaration order is deliberately NOT asserted: these formulas contain genuine
// parallel branches whose topological order legally differs from file order.
func TestPatrolFormulasSequenceEveryStep(t *testing.T) {
	// The ratchet is empty and stays empty. mol-deacon-patrol's three orphans —
	// the dolt-health -> zombie-scan -> plugin-run chain that dead-ended below
	// health-scan — were sequenced into loop-or-exit for gastown-7ip, so all
	// three names came off this list. Nothing may be added back: a new orphan is
	// the defect this test exists to catch, not an entry to record here.
	knownOrphans := map[string][]string{}

	for _, name := range []string{"mol-deacon-patrol", "mol-witness-patrol", "mol-refinery-patrol"} {
		t.Run(name, func(t *testing.T) {
			content, err := GetEmbeddedFormulaContent(name)
			if err != nil {
				t.Fatalf("GetEmbeddedFormulaContent(%s): %v", name, err)
			}
			f, err := Parse(content)
			if err != nil {
				t.Fatalf("Parse(%s): %v", name, err)
			}
			if len(f.Steps) == 0 {
				t.Fatalf("%s declares no steps", name)
			}
			if _, err := f.TopologicalSort(); err != nil {
				t.Fatalf("TopologicalSort(%s): %v", name, err)
			}

			if ready := f.ReadySteps(map[string]bool{}); len(ready) != 1 {
				t.Errorf("%s has %d steps ready before any step runs, want exactly 1: %v", name, len(ready), ready)
			}

			needs := make(map[string][]string, len(f.Steps))
			for _, step := range f.Steps {
				needs[step.ID] = step.Needs
			}
			final := f.Steps[len(f.Steps)-1].ID

			reached := map[string]bool{final: true}
			queue := []string{final}
			for len(queue) > 0 {
				id := queue[0]
				queue = queue[1:]
				for _, dep := range needs[id] {
					if !reached[dep] {
						reached[dep] = true
						queue = append(queue, dep)
					}
				}
			}

			var orphans []string
			for _, step := range f.Steps {
				if !reached[step.ID] {
					orphans = append(orphans, step.ID)
				}
			}
			sort.Strings(orphans)
			want := knownOrphans[name]
			if !slices.Equal(orphans, want) {
				t.Errorf("%s: final step %q does not transitively need %v, want %v — an unlisted step there can be skipped without the cycle noticing (see gastown-7ip)", name, final, orphans, want)
			}
		})
	}
}
