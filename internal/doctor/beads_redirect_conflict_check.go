package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BeadsRedirectConflictCheck finds .beads directories that hold BOTH a redirect
// and the identity/database files that belong only in the redirect's target.
//
// The two .beads layouts are alternatives, not a union. rig/manager.go InitBeads
// picks exactly one and returns:
//
//	adopted rig (mayor/rig/.beads exists) -> .beads/{redirect}                  and nothing else
//	standalone rig                        -> .beads/{metadata.json, config.yaml, dolt/}
//
// A directory holding both is not a third layout; it is one layout laid over the
// other, and it makes bd read the two halves from different files. The redirect
// resolves the DIRECTORY while the local metadata.json supplies dolt_database, so
// a stale local metadata.json silently binds bd to a different database while
// every path bd prints stays correct:
//
//	.beads/{redirect}                                  bd info -> .../mayor/rig/.beads/dolt  248 issues
//	.beads/{redirect, metadata.json dolt_database=gt}  bd info -> .../mayor/rig/.beads/dolt    0 issues
//
// One file apart, identical Database: line, no error and no warning on either
// side. That is why "check bd info to confirm you are on the right store" cannot
// catch this: bd info reports which object was addressed, never whether the data
// was reached. Only a content signal separates them, which is what this check
// reports (gastown-ojk, and gt-irl for the same signature in the town store).
//
// beads.SetupRedirect already refuses to leave identity files beside a redirect
// it creates (see cleanBeadsRuntimeFiles) — but that runs for WORKTREES only, so
// nothing has ever inspected a rig root.
type BeadsRedirectConflictCheck struct {
	BaseCheck
}

// NewBeadsRedirectConflictCheck creates a new beads redirect conflict check.
func NewBeadsRedirectConflictCheck() *BeadsRedirectConflictCheck {
	return &BeadsRedirectConflictCheck{
		BaseCheck: BaseCheck{
			CheckName:        "beads-redirect-conflict",
			CheckDescription: "Check that no .beads/redirect sits beside its own database identity files",
			CheckCategory:    CategoryRig,
		},
	}
}

// redirectConflictEntries are the identity/database artifacts that bind bd to a
// database. Tracked docs (formulas/, README.md, PRIME.md, .gitignore, backup/)
// and the passive issues.jsonl export are deliberately absent: they carry no
// identity, and SetupRedirect preserves them beside a redirect on purpose.
var redirectConflictEntries = []string{
	"metadata.json", // supplies dolt_database — the proven cause
	"config.yaml",   // supplies prefix/config; cleaned by SetupRedirect for the same reason
	"dolt",          // a local database
	"embeddeddolt",  // a local embedded database
	"issues.db",     // a local SQLite-era database
}

// redirectConflict is one .beads directory holding a redirect plus identity files.
type redirectConflict struct {
	beadsDir string   // the offending .beads directory
	target   string   // resolved redirect target
	entries  []string // conflicting entries found, in redirectConflictEntries order
	localDB  string   // dolt_database declared by the LOCAL metadata.json, if any
	targetDB string   // dolt_database declared by the TARGET's metadata.json, if any
}

// diverges reports whether the local metadata.json names a different database
// than the redirect target does. That is the case proven to return a silent
// zero, so it is reported as an error rather than a warning.
func (rc redirectConflict) diverges() bool {
	return rc.localDB != "" && rc.targetDB != "" && rc.localDB != rc.targetDB
}

// Run scans every rig root and worktree .beads directory for the conflict.
func (c *BeadsRedirectConflictCheck) Run(ctx *CheckContext) *CheckResult {
	rigDirs, err := findRigDirs(ctx.TownRoot)
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("Could not scan rigs: %v", err),
		}
	}

	// Scan rig roots as well as worktrees. getWorktreePaths covers crew/,
	// polecats/ and refinery/rig but never the rig root, which is exactly where
	// this state has gone unnoticed.
	var scanned int
	var conflicts []redirectConflict
	for _, rigDir := range rigDirs {
		paths := append([]string{rigDir}, getWorktreePaths(rigDir)...)
		paths = append(paths, filepath.Join(rigDir, "witness"))
		for _, p := range paths {
			beadsDir := filepath.Join(p, ".beads")
			target, ok := resolveRedirectTargetPath(p)
			if !ok {
				continue
			}
			scanned++
			var found []string
			for _, name := range redirectConflictEntries {
				if _, err := os.Lstat(filepath.Join(beadsDir, name)); err == nil {
					found = append(found, name)
				}
			}
			if len(found) == 0 {
				continue
			}
			conflicts = append(conflicts, redirectConflict{
				beadsDir: beadsDir,
				target:   target,
				entries:  found,
				localDB:  metadataDoltDatabaseAt(beadsDir),
				targetDB: metadataDoltDatabaseAt(target),
			})
		}
	}

	if scanned == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No beads redirects to check",
		}
	}

	if len(conflicts) == 0 {
		// State the denominator: a clean zero from a scan that found nothing and
		// a clean zero from a scan that matched nothing look identical otherwise.
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: fmt.Sprintf("No redirect/identity conflicts (%d redirect(s) scanned)", scanned),
		}
	}

	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].beadsDir < conflicts[j].beadsDir })

	status := StatusWarning
	var details []string
	for _, rc := range conflicts {
		rel, err := filepath.Rel(ctx.TownRoot, rc.beadsDir)
		if err != nil {
			rel = rc.beadsDir
		}
		line := fmt.Sprintf("%s holds a redirect plus %s", rel, strings.Join(rc.entries, ", "))
		if rc.diverges() {
			status = StatusError
			line += fmt.Sprintf(" — local metadata.json names database %q but the redirect target uses %q, so bd reads an empty store and reports it as zero", rc.localDB, rc.targetDB)
		}
		details = append(details, line)
	}

	return &CheckResult{
		Name:   c.Name(),
		Status: status,
		Message: fmt.Sprintf("%d of %d redirect(s) sit beside their own identity files",
			len(conflicts), scanned),
		Details: details,
		FixHint: "Identity files belong only in the redirect TARGET. Not auto-fixed: removing anything under .beads/ is Marc-reserved (gt-tfr). " +
			"Confirm with 'bd info' from the offending directory and from its target — a matching Database: line with different Issue Counts is this defect — then have Marc move the stray metadata.json/config.yaml/dolt dirs aside.",
	}
}

// resolveRedirectTargetPath returns the resolved target of workDir/.beads/redirect.
// It deliberately does NOT follow redirect chains: this check is about what sits
// beside each individual redirect file, not about where the chain terminates.
func resolveRedirectTargetPath(workDir string) (string, bool) {
	target := readRedirectTarget(workDir)
	if target == "" {
		return "", false
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), true
	}
	return filepath.Clean(filepath.Join(workDir, target)), true
}

// metadataDoltDatabaseAt reads dolt_database from the metadata.json in exactly
// this directory. It must not resolve redirects: beads.DatabaseNameFromMetadata
// does, and would therefore return the TARGET's database name for both sides of
// the comparison, which is the one answer that cannot detect the divergence.
func metadataDoltDatabaseAt(beadsDir string) string {
	data, err := os.ReadFile(filepath.Join(beadsDir, "metadata.json")) //nolint:gosec // G304: path is constructed internally
	if err != nil {
		return ""
	}
	var meta struct {
		DoltDatabase string `json:"dolt_database"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	return meta.DoltDatabase
}
