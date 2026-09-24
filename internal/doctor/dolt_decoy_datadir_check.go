package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
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

// bdReportedStore asks bd which store it actually reads.
//
// THIS REPLACES A MODEL OF bd's RESOLUTION, AND THE MODEL WAS WRONG WHERE IT
// RAN. The first version of this check reimplemented projectDoltDirPath and
// concluded this town's bd resolves <townRoot>/.beads/dolt. It does not: bd
// info reports .beads/embeddeddolt with 44,988 issues, while .beads/dolt is a
// separate 4.7M repo. Both are real Dolt directories, so a "is it a repo" test
// cannot tell them apart. For a check that exists to catch stores reading zero,
// a wrong resolution model gives a confident wrong answer about exactly the
// question it is asked (gastown/refinery on PR 65).
//
// bd info prints the resolved path and cannot drift from bd, because it IS bd.
func bdReportedStore(townRoot string) (string, error) {
	cmd := exec.Command("bd", "info")
	cmd.Dir = townRoot
	// PIN THE ROUTING, because cmd.Dir does NOT win. bd honours BEADS_DIR and
	// BEADS_DB from the inherited environment even with cmd.Dir at the town
	// root, so an operator with BEADS_DB=<townRoot>/.beads/dolt exported would
	// have this check bless the DECOY as the real store and report the actual
	// 44,988-issue store as unaccounted — exactly inverted, on the one question
	// it exists to answer. Measured with this invocation shape
	// (gastown/refinery, PR 65 round 2):
	//
	//   baseline                       .beads/embeddeddolt   44,988 issues
	//   BEADS_DB=<townRoot>/.beads/dolt .beads/dolt          <- the decoy
	//   BEADS_DIR=<rig>/.beads          <rig>/mayor/rig/.beads/dolt
	//
	// BuildPinnedBDEnv strips inherited target selectors first, which is why it
	// is the in-tree answer rather than appending one more variable.
	cmd.Env = pinnedBDEnv(os.Environ(), townRoot)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Database:"); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("bd info printed no Database: line")
}

// pinnedBDEnv is the environment for the bd subprocess, extracted so a test can
// assert that an inherited selector cannot survive into it.
func pinnedBDEnv(base []string, townRoot string) []string {
	return beads.BuildPinnedBDEnv(base, filepath.Join(townRoot, ".beads"))
}

// isDoltRepo reports whether dir is a real Dolt repository rather than a stub.
func isDoltRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".dolt"))
	return err == nil && info.IsDir()
}

// candidateStores are the places a Dolt repository turns up in a town. This is
// an ENUMERATION, not a prediction: the check reports which real repositories
// exist and which of them nothing accounts for, rather than claiming to know
// what bd would serve if somebody started a server.
func candidateStores(townRoot string) []string {
	return []string{
		filepath.Join(townRoot, ".beads", "dolt"),
		filepath.Join(townRoot, ".beads", "embeddeddolt"),
		filepath.Join(townRoot, ".dolt-data"),
	}
}

// unaccountedStores returns the real Dolt repositories that are neither the
// store bd reads nor the daemon's data dir, plus how many were scanned.
//
// isRepo is injected so a test drives THIS function rather than a copy of its
// logic, and so the scan can be exercised without Dolt directories on disk.
func unaccountedStores(store, daemonDir string, candidates []string, isRepo func(string) bool) (unaccounted []string, scanned int) {
	for _, dir := range candidates {
		if !isRepo(dir) {
			continue
		}
		scanned++
		if filepath.Clean(dir) == filepath.Clean(store) || filepath.Clean(dir) == filepath.Clean(daemonDir) {
			continue
		}
		unaccounted = append(unaccounted, dir)
	}
	return unaccounted, scanned
}

func (c *DoltDecoyDataDirCheck) Run(ctx *CheckContext) *CheckResult {
	beadsDir := filepath.Join(ctx.TownRoot, ".beads")
	if _, err := os.Stat(beadsDir); err != nil {
		return &CheckResult{Name: c.Name(), Status: StatusOK, Message: "No town .beads directory to check"}
	}

	store, err := bdReportedStore(ctx.TownRoot)
	if err != nil {
		// Without bd's own answer this check has no ground truth, and guessing
		// one is what the previous version did wrong. Say so instead.
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("Could not ask bd which store it reads: %v", err),
			FixHint: "Run `bd info` from the town root; this check needs its Database: line as ground truth.",
		}
	}
	daemonDir := filepath.Join(ctx.TownRoot, ".dolt-data")

	unaccounted, scanned := unaccountedStores(store, daemonDir, candidateStores(ctx.TownRoot), isDoltRepo)

	if len(unaccounted) == 0 {
		// State the denominator: a clean zero from a scan that found nothing and
		// one from a scan that looked nowhere are otherwise identical.
		return &CheckResult{
			Name:   c.Name(),
			Status: StatusOK,
			Message: fmt.Sprintf("Every Dolt repository is accounted for (%d scanned; bd reads %s)",
				scanned, store),
		}
	}

	var details []string
	for _, dir := range unaccounted {
		rel, relErr := filepath.Rel(ctx.TownRoot, dir)
		if relErr != nil {
			rel = dir
		}
		details = append(details, fmt.Sprintf("%s is a real Dolt repository that is neither the store bd reads (%s) nor the daemon's data dir (%s)", rel, store, daemonDir))
	}
	details = append(details,
		"A bd start-class command run from the town root resolves its data dir from the beads dir, so an unaccounted repository beside it can be served on the configured port",
		"While it is served, every server-mode store reads rc=0 with 0 rows and 0 bytes on stderr — the silent zero of gt-zpnz, which blinded three rig merge queues on 2026-09-09",
		"gt's own paths are NOT affected: the daemon and `gt dolt start` both resolve .dolt-data explicitly")

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d of %d Dolt repositories are unaccounted for", len(unaccounted), scanned),
		Details: details,
		FixHint: "Do NOT delete it — removing a Dolt directory is Marc-reserved (gt-tfr) and it may hold history. " +
			"Confirm with `bd info` which store bd reads, and prefer `gt dolt start`, which resolves .dolt-data explicitly.",
	}
}
