package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTownBeads(t *testing.T, townRoot, metadata string) {
	t.Helper()
	beads := filepath.Join(townRoot, ".beads")
	if err := os.MkdirAll(beads, 0o755); err != nil {
		t.Fatal(err)
	}
	if metadata != "" {
		if err := os.WriteFile(filepath.Join(beads, "metadata.json"), []byte(metadata), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func makeDoltRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".dolt"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The live shape of this town: metadata names database "dolt", so bd's
// server-mode resolution lands on .beads/dolt — a real Dolt repository holding
// none of the town's work — while the daemon serves .dolt-data. That is the
// state that produced gt-zpnz's silent zeros.
func TestDecoyDataDirIsReportedWhenBdWouldServeIt(t *testing.T) {
	town := t.TempDir()
	writeTownBeads(t, town, `{"database":"dolt","dolt_mode":"embedded","dolt_database":"gt"}`)
	makeDoltRepo(t, filepath.Join(town, ".beads", "dolt"))
	makeDoltRepo(t, filepath.Join(town, ".dolt-data"))

	res := NewDoltDecoyDataDirCheck().Run(&CheckContext{TownRoot: town})

	if res.Status != StatusWarning {
		t.Fatalf("status = %v, want StatusWarning — the decoy is servable and unreported", res.Status)
	}
}

// The safe configuration: metadata points bd at the same directory the daemon
// serves, so a bd start-class command would serve the real rows.
func TestNoWarningWhenBdResolvesTheDaemonDataDir(t *testing.T) {
	town := t.TempDir()
	writeTownBeads(t, town, `{"database":"dolt","dolt_data_dir":"`+filepath.Join(town, ".dolt-data")+`"}`)
	makeDoltRepo(t, filepath.Join(town, ".dolt-data"))

	res := NewDoltDecoyDataDirCheck().Run(&CheckContext{TownRoot: town})

	if res.Status != StatusOK {
		t.Fatalf("status = %v, want StatusOK — bd and the daemon agree here: %s", res.Status, res.Message)
	}
}

// NEGATIVE CONTROL. A resolution that points somewhere harmless must NOT warn,
// or the check would fire on every town and the warning would mean nothing.
// Without this, a check hardcoded to "always warn" passes the first test.
func TestNoWarningWhenTheResolvedDirIsNotADoltRepo(t *testing.T) {
	town := t.TempDir()
	writeTownBeads(t, town, `{"database":"dolt"}`)
	// .beads/dolt deliberately absent — nothing to serve.
	makeDoltRepo(t, filepath.Join(town, ".dolt-data"))

	res := NewDoltDecoyDataDirCheck().Run(&CheckContext{TownRoot: town})

	if res.Status != StatusOK {
		t.Fatalf("status = %v, want StatusOK — there is no repository at the resolved path: %s", res.Status, res.Message)
	}
}

// bd falls back to <beadsDir>/dolt when metadata is unreadable, which is the
// branch the recurring metadata.json deletions would take.
func TestUnreadableMetadataFallsBackToTheDecoyPath(t *testing.T) {
	town := t.TempDir()
	writeTownBeads(t, town, "") // no metadata.json at all
	makeDoltRepo(t, filepath.Join(town, ".beads", "dolt"))
	makeDoltRepo(t, filepath.Join(town, ".dolt-data"))

	res := NewDoltDecoyDataDirCheck().Run(&CheckContext{TownRoot: town})

	if res.Status != StatusWarning {
		t.Fatalf("status = %v, want StatusWarning — the missing-metadata fallback resolves to the decoy too", res.Status)
	}
}
