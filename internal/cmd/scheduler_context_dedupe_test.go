package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gastown-o8q: beadsSearchDirs returns DIRECTORIES, and several of them resolve
// to the same STORE — a rig's own `.beads` is a redirect to its mayor rig's, so
// `<rig>` and `<rig>/mayor/rig` are two directories over one database.
// listAllSlingContextRecords queried per directory, so the identical `bd list`
// ran once per alias. Measured in a live dispatch pass: 7 byte-identical
// invocations, each a subprocess and a Dolt round-trip, against a 300s deadline
// the pass was already blowing.
//
// The results were deduplicated by resolved store all along, so the extra passes
// never changed the answer — which is exactly why nothing surfaced the cost.
//
// This asserts the store is queried ONCE per resolved database while the answer
// stays identical. Both halves matter: dropping directories from the walk would
// also reduce the query count, and would lose beads.
func TestListAllSlingContextRecordsQueriesEachStoreOnce(t *testing.T) {
	townRoot := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "bd.log")

	// A rig whose own .beads redirects to its mayor rig's — the real layout, and
	// the one that produced the duplicate queries.
	rigDir := filepath.Join(townRoot, "rig")
	mayorRigDir := filepath.Join(rigDir, "mayor", "rig")
	for _, dir := range []string{
		filepath.Join(townRoot, "mayor"),
		filepath.Join(townRoot, ".beads"),
		filepath.Join(rigDir, ".beads"),
		filepath.Join(mayorRigDir, ".beads"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(rigDir, ".beads", "redirect"),
		[]byte("mayor/rig/.beads\n"), 0o644); err != nil {
		t.Fatalf("write redirect: %v", err)
	}

	// The fake bd records the store each invocation was pointed at, and returns
	// one sling context per store so a dropped store is visible as a missing
	// record rather than only as a smaller query count.
	installFakeBD(t, `#!/bin/sh
echo "$BEADS_DIR|$*" >> `+logPath+`
case "$BEADS_DIR" in
  */rig/mayor/rig/.beads) printf '[{"id":"rig-ctx","status":"open","description":""}]\n' ;;
  *) printf '[{"id":"town-ctx","status":"open","description":""}]\n' ;;
esac
exit 0
`)

	records, err := listAllSlingContextRecords(townRoot)
	if err != nil {
		t.Fatalf("listAllSlingContextRecords: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read bd log: %v", err)
	}
	// Count only the sling-context query. bd also runs a one-off `--allow-stale
	// version` capability probe per PROCESS, which is not per-store work and
	// counting it would make this assertion measure the wrong thing.
	counts := map[string]int{}
	total := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		store, args, ok := strings.Cut(line, "|")
		if !ok || !strings.Contains(args, "gt:sling-context") {
			continue
		}
		counts[store]++
		total++
	}

	// Denominator: a fixture that never invoked bd would satisfy every
	// "queried at most once" assertion below while proving nothing.
	if total == 0 {
		t.Fatal("fake bd was never invoked; this test is measuring nothing")
	}
	if len(counts) < 2 {
		t.Fatalf("only %d distinct store(s) queried (%v); the fixture is not exercising "+
			"the multi-store walk this test exists for", len(counts), counts)
	}

	for store, n := range counts {
		if n != 1 {
			t.Errorf("store %s queried %d times, want 1: two directories resolving to one "+
				"database are still costing two subprocesses (gastown-o8q)", store, n)
		}
	}

	// The answer must be unchanged. Deduplicating DIRECTORIES must not drop a
	// STORE, which is the way this optimisation could go wrong silently.
	found := map[string]bool{}
	for _, r := range records {
		found[r.issue.ID] = true
	}
	for _, want := range []string{"town-ctx", "rig-ctx"} {
		if !found[want] {
			t.Errorf("sling context %q missing from the walk; dedupe dropped a store, "+
				"got %v", want, found)
		}
	}
}
