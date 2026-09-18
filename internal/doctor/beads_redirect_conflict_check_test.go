package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newConflictRig builds <town>/<rig> with a canonical store at mayor/rig/.beads
// declaring dolt_database=<rig>, and a rig-root .beads/redirect pointing at it.
// This is the layout rig/manager.go InitBeads produces for an adopted rig.
func newConflictRig(t *testing.T, rigName string) (townRoot, rigDir string) {
	t.Helper()
	townRoot = t.TempDir()
	rigDir = filepath.Join(townRoot, rigName)

	// findRigDirs requires a .git dir or a mayor/rig subdir to treat this as a rig.
	if err := os.MkdirAll(filepath.Join(rigDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	mayorBeads := filepath.Join(rigDir, "mayor", "rig", ".beads")
	if err := os.MkdirAll(filepath.Join(mayorBeads, "dolt"), 0755); err != nil {
		t.Fatal(err)
	}
	writeMetadataDB(t, mayorBeads, rigName)

	rigBeads := filepath.Join(rigDir, ".beads")
	if err := os.MkdirAll(rigBeads, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigBeads, "redirect"), []byte("mayor/rig/.beads\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return townRoot, rigDir
}

func writeMetadataDB(t *testing.T, beadsDir, db string) {
	t.Helper()
	body := `{"backend":"dolt","database":"dolt","dolt_mode":"server","dolt_database":"` + db + `"}`
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func runConflictCheck(t *testing.T, townRoot string) *CheckResult {
	t.Helper()
	return NewBeadsRedirectConflictCheck().Run(&CheckContext{TownRoot: townRoot})
}

// The control: the adopted-rig layout is a redirect and nothing else. This is
// the state beadsrig and liveop are in, and it must stay OK — a check that
// flags every redirect would be indistinguishable from one that works.
func TestBeadsRedirectConflictCheck_RedirectOnlyIsClean(t *testing.T) {
	townRoot, _ := newConflictRig(t, "myrig")

	result := runConflictCheck(t, townRoot)

	if result.Status != StatusOK {
		t.Fatalf("redirect-only rig should be OK, got %v: %s\n%s",
			result.Status, result.Message, strings.Join(result.Details, "\n"))
	}
	// The denominator must be visible: a scan that matched nothing and a scan
	// that found nothing otherwise print the same clean zero.
	if !strings.Contains(result.Message, "1 redirect(s) scanned") {
		t.Errorf("expected the scanned count in the message, got %q", result.Message)
	}
}

// The motivating case (gastown-ojk): a local metadata.json naming a DIFFERENT
// database silently binds bd to an empty store while every path it prints stays
// correct. Proven divergence is an error, not a warning.
func TestBeadsRedirectConflictCheck_DivergentMetadataIsError(t *testing.T) {
	townRoot, rigDir := newConflictRig(t, "myrig")
	// dolt_database "gt" is the TOWN database — the exact value found on the
	// gastown rig root, which holds no myrig- rows and so reports zero.
	writeMetadataDB(t, filepath.Join(rigDir, ".beads"), "gt")

	result := runConflictCheck(t, townRoot)

	if result.Status != StatusError {
		t.Fatalf("divergent dolt_database should be StatusError, got %v: %s",
			result.Status, result.Message)
	}
	joined := strings.Join(result.Details, "\n")
	for _, want := range []string{"metadata.json", `"gt"`, `"myrig"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("details should name %s, got:\n%s", want, joined)
		}
	}
	if !strings.Contains(result.Message, "1 of 1") {
		t.Errorf("expected conflicts-of-scanned in the message, got %q", result.Message)
	}
}

// A local metadata.json agreeing with the target is still a layout violation
// (it is the file that goes stale) but it is not today's silent zero, so it
// must not be reported at the same severity.
func TestBeadsRedirectConflictCheck_MatchingMetadataIsWarningNotError(t *testing.T) {
	townRoot, rigDir := newConflictRig(t, "myrig")
	writeMetadataDB(t, filepath.Join(rigDir, ".beads"), "myrig")

	result := runConflictCheck(t, townRoot)

	if result.Status != StatusWarning {
		t.Fatalf("matching dolt_database should be StatusWarning, got %v: %s",
			result.Status, result.Message)
	}
	if strings.Contains(strings.Join(result.Details, "\n"), "reports it as zero") {
		t.Error("must not claim a silent zero when the database names agree")
	}
}

// A local database directory beside a redirect is the same layout collision
// even with no metadata.json to compare.
func TestBeadsRedirectConflictCheck_LocalStoreDirIsFlagged(t *testing.T) {
	townRoot, rigDir := newConflictRig(t, "myrig")
	if err := os.MkdirAll(filepath.Join(rigDir, ".beads", "embeddeddolt", "gt"), 0755); err != nil {
		t.Fatal(err)
	}

	result := runConflictCheck(t, townRoot)

	if result.Status != StatusWarning {
		t.Fatalf("embeddeddolt beside a redirect should warn, got %v: %s",
			result.Status, result.Message)
	}
	if !strings.Contains(strings.Join(result.Details, "\n"), "embeddeddolt") {
		t.Errorf("details should name embeddeddolt, got %v", result.Details)
	}
}

// Tracked docs and the passive issues.jsonl export live beside a redirect by
// design — SetupRedirect preserves them. 65 of this town's 66 redirect
// directories look exactly like this, so a false positive here would bury the
// one real hit.
func TestBeadsRedirectConflictCheck_TrackedDocsAreNotConflicts(t *testing.T) {
	townRoot, rigDir := newConflictRig(t, "myrig")
	beadsDir := filepath.Join(rigDir, ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "formulas"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(beadsDir, "backup"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "PRIME.md", ".gitignore", "issues.jsonl", ".jsonl.lock"} {
		if err := os.WriteFile(filepath.Join(beadsDir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result := runConflictCheck(t, townRoot)

	if result.Status != StatusOK {
		t.Fatalf("tracked docs beside a redirect must stay OK, got %v: %s\n%s",
			result.Status, result.Message, strings.Join(result.Details, "\n"))
	}
}

// The standalone-rig layout — identity files and NO redirect — is the other
// legitimate half of the either/or and must never be flagged.
func TestBeadsRedirectConflictCheck_StandaloneRigLayoutIsClean(t *testing.T) {
	townRoot := t.TempDir()
	rigDir := filepath.Join(townRoot, "solo")
	beadsDir := filepath.Join(rigDir, ".beads")
	if err := os.MkdirAll(filepath.Join(rigDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(beadsDir, "dolt"), 0755); err != nil {
		t.Fatal(err)
	}
	writeMetadataDB(t, beadsDir, "solo")
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("issue-prefix: so\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result := runConflictCheck(t, townRoot)

	if result.Status != StatusOK {
		t.Fatalf("standalone rig layout must stay OK, got %v: %s\n%s",
			result.Status, result.Message, strings.Join(result.Details, "\n"))
	}
	if !strings.Contains(result.Message, "No beads redirects to check") {
		t.Errorf("expected the no-redirects message, got %q", result.Message)
	}
}

// Worktrees and the witness agent root carry redirects too. getWorktreePaths
// omits witness/, so this pins the path that would otherwise go unscanned.
func TestBeadsRedirectConflictCheck_ScansWorktreesAndWitness(t *testing.T) {
	townRoot, rigDir := newConflictRig(t, "myrig")

	for _, wt := range []string{
		filepath.Join(rigDir, "crew", "worker1"),
		filepath.Join(rigDir, "refinery", "rig"),
		filepath.Join(rigDir, "witness"),
	} {
		beadsDir := filepath.Join(wt, ".beads")
		if err := os.MkdirAll(beadsDir, 0755); err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(wt, filepath.Join(rigDir, "mayor", "rig", ".beads"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(beadsDir, "redirect"), []byte(rel+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Poison only the witness — the path no existing check walks.
	writeMetadataDB(t, filepath.Join(rigDir, "witness", ".beads"), "gt")

	result := runConflictCheck(t, townRoot)

	if result.Status != StatusError {
		t.Fatalf("witness conflict should be StatusError, got %v: %s",
			result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "1 of 4") {
		t.Errorf("expected 1 of 4 redirects scanned, got %q", result.Message)
	}
	if !strings.Contains(strings.Join(result.Details, "\n"), filepath.Join("witness", ".beads")) {
		t.Errorf("details should name the witness beads dir, got %v", result.Details)
	}
}

// metadataDoltDatabaseAt must read the file in the directory it is handed.
// beads.DatabaseNameFromMetadata resolves redirects and would return the
// target's name for BOTH sides of the comparison — the one answer that makes
// the divergence undetectable. This pins the reason for the local helper.
func TestMetadataDoltDatabaseAt_DoesNotFollowRedirect(t *testing.T) {
	_, rigDir := newConflictRig(t, "myrig")
	rigBeads := filepath.Join(rigDir, ".beads")
	writeMetadataDB(t, rigBeads, "gt")

	if got := metadataDoltDatabaseAt(rigBeads); got != "gt" {
		t.Errorf("local metadata should read %q, got %q", "gt", got)
	}
	target := filepath.Join(rigDir, "mayor", "rig", ".beads")
	if got := metadataDoltDatabaseAt(target); got != "myrig" {
		t.Errorf("target metadata should read %q, got %q", "myrig", got)
	}
}
