package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DoltDecoyDataDirCheck reports a Dolt repository that bd's SERVER-mode
// resolution would serve instead of the town's real data.
//
// THE FAILURE IT PREVENTS IS A SILENT, WELL-FORMED ZERO (gt-zpnz). When a Dolt
// server on the configured port serves a directory holding none of the town's
// work, every rig's `bd list` returns rc=0, empty stdout and ZERO bytes on
// stderr. Measured the same minute during the 2026-09-09 incident: three
// server-mode stores read 0 rows while the server held 204, 46 and 7 issues.
// Nothing in that output says the store was not reached — which is why the
// merge queues went blind rather than erroring.
//
// WHY IT CANNOT BE CAUGHT BY THE EXISTING IMPOSTER CHECK. CheckPortConflict
// finds a dolt process ALREADY LISTENING whose data dir differs from the town's.
// That is detection after the fact and only while the process lives. This check
// looks at the directory that makes it possible, so it fires BEFORE anyone
// starts a server, including on a quiet town where no imposter exists yet.
//
// THE MECHANISM, read from bd's source (doltserver.ResolveDoltDir ->
// projectDoltDirPath): bd never passes a data directory to dolt. It computes one
// from the beads dir and launches with cmd.Dir set to it, so dolt serves
// whatever databases live there. For this town that resolution lands on
// <townRoot>/.beads/dolt — a real Dolt repository, 4.6M, holding none of the
// town's work — while the actual data is embedded at .beads/embeddeddolt and the
// daemon's server data is .dolt-data. Both gt paths were cleared: the daemon
// (daemon/dolt.go) and `gt dolt start` (cmd/dolt.go) both resolve
// <townRoot>/.dolt-data explicitly. So every occurrence has a bd start-class
// caller — `bd dolt start`, `bd init` in shared-server mode, or a shared-server
// config apply — run from the town, usually by somebody mid-incident following
// on-screen advice to start a server.
type DoltDecoyDataDirCheck struct {
	BaseCheck
}

// NewDoltDecoyDataDirCheck creates a new decoy data directory check.
func NewDoltDecoyDataDirCheck() *DoltDecoyDataDirCheck {
	return &DoltDecoyDataDirCheck{
		BaseCheck: BaseCheck{
			CheckName:        "dolt-decoy-datadir",
			CheckDescription: "Check that bd's server-mode resolution cannot serve a directory holding none of the town's work",
			CheckCategory:    CategoryInfrastructure,
		},
	}
}

// bdServerModeDoltDir reproduces bd's projectDoltDirPath for a beads dir.
//
// Deliberately a REIMPLEMENTATION rather than a call into bd: bd is a separate,
// module-proxy-built binary whose source is not in this tree, so there is
// nothing to call. That makes this check a model of another program's
// behaviour, and it can drift — which is why it reports what it resolved and
// why, rather than asserting bd will do the same.
func bdServerModeDoltDir(beadsDir string) (string, string) {
	if env := os.Getenv("BEADS_DOLT_DATA_DIR"); env != "" {
		return env, "BEADS_DOLT_DATA_DIR"
	}

	metaPath := filepath.Join(beadsDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		// No readable metadata: bd falls back to <beadsDir>/dolt.
		return filepath.Join(beadsDir, "dolt"), "fallback (metadata.json unreadable)"
	}

	var meta struct {
		Database    string `json:"database"`
		DoltDataDir string `json:"dolt_data_dir"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return filepath.Join(beadsDir, "dolt"), "fallback (metadata.json unparseable)"
	}
	if meta.DoltDataDir != "" {
		return meta.DoltDataDir, "metadata.json dolt_data_dir"
	}
	if meta.Database != "" {
		return filepath.Join(beadsDir, meta.Database), "metadata.json database"
	}
	return filepath.Join(beadsDir, "dolt"), "fallback (no database key)"
}

// isDoltRepo reports whether dir is a real Dolt repository rather than an empty
// stub. A stub serves nothing and is not the hazard; a populated repo is.
func isDoltRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".dolt"))
	return err == nil && info.IsDir()
}

func (c *DoltDecoyDataDirCheck) Run(ctx *CheckContext) *CheckResult {
	beadsDir := filepath.Join(ctx.TownRoot, ".beads")
	if _, err := os.Stat(beadsDir); err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No town .beads directory to check",
		}
	}

	resolved, why := bdServerModeDoltDir(beadsDir)
	realDataDir := filepath.Join(ctx.TownRoot, ".dolt-data")

	// The safe case: bd's resolution and the daemon's data dir agree, so a bd
	// start-class command would serve the same rows the daemon does.
	if filepath.Clean(resolved) == filepath.Clean(realDataDir) {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: fmt.Sprintf("bd server-mode resolution matches the daemon data dir (%s, via %s)", realDataDir, why),
		}
	}

	if !isDoltRepo(resolved) {
		// It resolves elsewhere, but there is no repository there to serve.
		// Report it rather than passing silently: the directory can appear later.
		return &CheckResult{
			Name:   c.Name(),
			Status: StatusOK,
			Message: fmt.Sprintf("bd would resolve %s (via %s), which is not a Dolt repository — nothing to serve",
				resolved, why),
		}
	}

	rel, err := filepath.Rel(ctx.TownRoot, resolved)
	if err != nil {
		rel = resolved
	}

	return &CheckResult{
		Name:   c.Name(),
		Status: StatusWarning,
		Message: fmt.Sprintf("bd server-mode would serve %s, NOT the daemon's %s",
			rel, filepath.Base(realDataDir)),
		Details: []string{
			fmt.Sprintf("resolved via %s", why),
			fmt.Sprintf("%s is a real Dolt repository, so a bd start-class command run from the town root would serve it on the configured port", rel),
			"While it is served, every server-mode store reads rc=0 with 0 rows and 0 bytes on stderr — the silent zero of gt-zpnz, which blinded three rig merge queues on 2026-09-09",
			"gt's own paths are NOT affected: the daemon and `gt dolt start` both resolve .dolt-data explicitly",
		},
		FixHint: "Do NOT delete it — removing a Dolt directory is Marc-reserved (gt-tfr) and this one may hold history. " +
			"Until bd refuses to serve a data dir that disagrees with the town's, treat `bd dolt start` and `bd init` from the town root as unsafe and use `gt dolt start`, which resolves .dolt-data. " +
			"If a server is already serving it, `gt dolt kill-imposters` reports and clears it.",
	}
}
