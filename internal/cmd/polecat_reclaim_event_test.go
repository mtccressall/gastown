package cmd

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/polecat"
)

// A reclaim DELETES a worktree. Before this, it announced itself on stdout only,
// so the removal left no durable trace: an investigation into a vanished polecat
// was lost because the operator's own output filter dropped the reclaim lines
// before anyone read them (gt-kpiwr). The payload has to carry enough to answer
// "what went, from where, and why" without the terminal.
func TestPolecatReclaimedPayloadCarriesWhatWentAndWhy(t *testing.T) {
	p := events.PolecatReclaimedPayload("liveop", "chrome",
		"/home/x/gt/liveop/polecats/chrome", "worktree metadata missing")

	for _, k := range []string{"rig", "polecat", "path", "reason"} {
		if _, ok := p[k]; !ok {
			t.Errorf("payload is missing %q — the feed cannot answer what was removed or why", k)
		}
	}
	if p["polecat"] != "chrome" {
		t.Errorf("polecat = %v, want chrome", p["polecat"])
	}
	if !strings.Contains(p["reason"].(string), "metadata missing") {
		t.Errorf("reason lost the verification failure: %v", p["reason"])
	}
}

// The event type must be distinct from spawn and kill. A reclaim that reported
// itself as either would be indistinguishable from ordinary pool churn, which is
// the state gt-kpiwr was filed from: a directory gone with no event naming it.
func TestPolecatReclaimedTypeIsDistinct(t *testing.T) {
	if events.TypePolecatReclaimed == "" {
		t.Fatal("TypePolecatReclaimed is empty")
	}
	for name, other := range map[string]string{
		"spawn":         events.TypeSpawn,
		"kill":          events.TypeKill,
		"session_death": events.TypeSessionDeath,
	} {
		if events.TypePolecatReclaimed == other {
			t.Errorf("reclaim shares the %q event type — it would read as ordinary churn", name)
		}
	}
}

// fakeReclaimMgr drives the reclaim decision without a rig on disk.
type fakeReclaimMgr struct {
	list       []*polecat.Polecat
	reclaimErr error
	reclaimed  []string
}

func (f *fakeReclaimMgr) List() ([]*polecat.Polecat, error) { return f.list, nil }
func (f *fakeReclaimMgr) ReclaimBrokenIdlePolecat(name string) error {
	if f.reclaimErr != nil {
		return f.reclaimErr
	}
	f.reclaimed = append(f.reclaimed, name)
	return nil
}

// THE WIRING TEST. A reclaim deletes a worktree, and the payload tests above pass
// whether or not anything is ever emitted — deleting the emit call outright left
// them all green. This drives the function and asserts the announcement actually
// happens, which is the only assertion that would have preserved the evidence
// gt-kpiwr lost.
func TestReclaimAnnouncesTheDeletion(t *testing.T) {
	mgr := &fakeReclaimMgr{list: []*polecat.Polecat{{
		Name:      "chrome",
		State:     polecat.StateIdle,
		Issue:     "",
		ClonePath: "/nonexistent/gt/liveop/polecats/chrome",
	}}}

	var got []string
	reclaimed, err := reclaimBrokenIdlePolecatForSling(mgr, "liveop",
		func(rig, name, path, reason string) {
			got = append(got, rig+"/"+name+" path="+path+" reason="+reason)
		})
	if err != nil {
		t.Fatalf("reclaim returned an error: %v", err)
	}
	if !reclaimed {
		t.Fatal("a structurally broken idle polecat was not reclaimed; the fixture no longer exercises the path")
	}
	if len(got) != 1 {
		t.Fatalf("emit called %d times, want 1 — a worktree was deleted with no record", len(got))
	}
	if !strings.Contains(got[0], "liveop/chrome") || !strings.Contains(got[0], "path=/nonexistent") {
		t.Errorf("announcement does not identify what was removed: %q", got[0])
	}
}

// NEGATIVE CONTROL: nothing eligible means nothing announced, or the test above
// would pass for a function that emits unconditionally.
func TestReclaimAnnouncesNothingWhenNothingIsEligible(t *testing.T) {
	mgr := &fakeReclaimMgr{list: []*polecat.Polecat{{
		Name:      "synth",
		State:     polecat.StateIdle,
		Issue:     "liveop-ia7a", // has work: never a reclaim candidate
		ClonePath: "/nonexistent/gt/liveop/polecats/synth",
	}}}

	var calls int
	reclaimed, err := reclaimBrokenIdlePolecatForSling(mgr, "liveop",
		func(string, string, string, string) { calls++ })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reclaimed || calls != 0 {
		t.Fatalf("reclaimed=%v emit calls=%d — a polecat holding work was treated as reclaimable", reclaimed, calls)
	}
}
