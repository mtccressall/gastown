package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/health"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	healthJSON bool
)

// beadsStoreCountTimeout bounds how long gt health waits for one store's issue
// count. Shorter than bd's own 60s subprocess timeout on purpose: an unanswered
// count is reported, and the rest of the report still prints.
const beadsStoreCountTimeout = 15 * time.Second

// HealthReport is the machine-readable output of gt health --json.
type HealthReport struct {
	Timestamp string           `json:"timestamp"`
	Server    *ServerHealth    `json:"server"`
	Databases []DatabaseHealth `json:"databases"`
	// BeadsStores reports the stores bd actually reads, which are not
	// necessarily the databases served by Server. See checkBeadsStores.
	BeadsStores []BeadsStoreHealth `json:"beads_stores"`
	Pollution   []PollutionRecord  `json:"pollution,omitempty"`
	Backups     *BackupHealth      `json:"backups"`
	Processes   *ProcessHealth     `json:"processes"`
	Orphans     []OrphanDB         `json:"orphans,omitempty"`
}

// BeadsStoreHealth describes one beads store as bd resolves it: where it is,
// what mode it is opened in, and how many issues it actually holds.
//
// The Databases section above reports on the Dolt server. bd does not
// necessarily use that server — when metadata.json says dolt_mode "embedded",
// bd opens a local store instead, and every count the server reports is a true
// statement about a database the town does not run on (gt-kei9). This section
// exists so the two can be told apart without reading source.
type BeadsStoreHealth struct {
	// Scope is "town" or a rig name.
	Scope string `json:"scope"`
	// Path is the resolved .beads directory, after following any redirect.
	Path string `json:"path"`
	// Mode is dolt_mode from the store's metadata.json: "embedded", "server",
	// or "" when metadata.json is absent or unreadable.
	Mode string `json:"mode,omitempty"`
	// Database is dolt_database from metadata.json.
	Database string `json:"database,omitempty"`
	// UsesServer reports whether this store is served by the Dolt server in the
	// Server section. When false, the Databases counts say nothing about it.
	UsesServer bool `json:"uses_server"`
	// Counted is false when the counts below could not be measured. Without it,
	// an unreachable store and an empty one both report zero.
	Counted      bool   `json:"counted"`
	TotalIssues  int    `json:"total_issues"`
	OpenIssues   int    `json:"open_issues"`
	ClosedIssues int    `json:"closed_issues"`
	Error        string `json:"error,omitempty"`
}

type ServerHealth struct {
	Running            bool    `json:"running"`
	PID                int     `json:"pid,omitempty"`
	Port               int     `json:"port,omitempty"`
	LatencyMs          int64   `json:"latency_ms,omitempty"`
	Connections        int     `json:"connections,omitempty"`
	MaxConnections     int     `json:"max_connections,omitempty"`
	DiskUsageBytes     int64   `json:"disk_usage_bytes,omitempty"`
	DiskUsageHuman     string  `json:"disk_usage_human,omitempty"`
	LastCommitAgeSec   float64 `json:"last_commit_age_seconds,omitempty"`
	LastCommitDB       string  `json:"last_commit_db,omitempty"`
}

type DatabaseHealth struct {
	Name       string `json:"name"`
	Issues     int    `json:"issues"`
	OpenIssues int    `json:"open_issues"`
	Wisps      int    `json:"wisps"`
	OpenWisps  int    `json:"open_wisps"`
	Commits    int    `json:"commits"`
}

type PollutionRecord struct {
	Database string `json:"database"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Pattern  string `json:"pattern"`
}

type BackupHealth struct {
	DoltFreshness  string `json:"dolt_freshness,omitempty"`
	DoltAgeSeconds int    `json:"dolt_age_seconds,omitempty"`
	DoltStale      bool   `json:"dolt_stale"`
	JSONLFreshness string `json:"jsonl_freshness,omitempty"`
	JSONLAgeSeconds int   `json:"jsonl_age_seconds,omitempty"`
	JSONLStale     bool   `json:"jsonl_stale"`
}

type ProcessHealth struct {
	ZombieCount int   `json:"zombie_count"`
	ZombiePIDs  []int `json:"zombie_pids,omitempty"`
}

type OrphanDB struct {
	Name string `json:"name"`
	Size string `json:"size,omitempty"`
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Show comprehensive system health",
	Long: `Display a comprehensive health report for the Gas Town data plane.

Sections:
  1. Dolt Server: status, PID, port, latency
  2. Databases: per-DB counts of issues, wisps, commits, ON THE DOLT SERVER
  3. Beads stores: the stores bd actually reads, and their issue counts
  4. Pollution: scan for known test/garbage patterns
  5. Backups: Dolt filesystem and JSONL git freshness
  6. Processes: zombie dolt servers
  7. Orphan DBs: databases not referenced by any rig

Sections 2 and 3 are different targets and can disagree. A store whose
metadata.json says dolt_mode "embedded" is not served by the Dolt server, so
the server's counts for it say nothing about what bd reads.

Use --json for machine-readable output.`,
	RunE: runHealth,
}

func init() {
	healthCmd.Flags().BoolVar(&healthJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(healthCmd)
}

func runHealth(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	report := &HealthReport{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// 1. Dolt Server
	report.Server = checkServerHealth(townRoot)

	// 2. Databases (only if server is running)
	if report.Server.Running {
		report.Databases = checkDatabaseHealth(report.Server.Port)
	}

	// 3. Beads stores — the stores bd actually reads, which the Databases
	// section above does not necessarily cover.
	report.BeadsStores = checkBeadsStores(townRoot)

	// 4. Pollution scan
	if report.Server.Running {
		report.Pollution = checkPollution(report.Server.Port)
	}

	// 5. Backups
	report.Backups = checkBackupHealth(townRoot)

	// 6. Processes
	report.Processes = checkProcessHealth(report.Server.Port)

	// 7. Orphans
	report.Orphans = checkOrphanDBs(townRoot)

	if healthJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	printHealthReport(report)
	return nil
}

func checkServerHealth(townRoot string) *ServerHealth {
	sh := &ServerHealth{}

	running, pid, err := doltserver.IsRunning(townRoot)
	if err != nil || !running {
		sh.Running = false
		return sh
	}

	sh.Running = true
	sh.PID = pid

	state, err := doltserver.LoadState(townRoot)
	if err == nil {
		sh.Port = state.Port
	}

	metrics := doltserver.GetHealthMetrics(townRoot)
	sh.LatencyMs = metrics.QueryLatency.Milliseconds()
	sh.Connections = metrics.Connections
	sh.MaxConnections = metrics.MaxConnections
	sh.DiskUsageBytes = metrics.DiskUsageBytes
	sh.DiskUsageHuman = metrics.DiskUsageHuman
	if metrics.LastCommitAge > 0 {
		sh.LastCommitAgeSec = metrics.LastCommitAge.Seconds()
		sh.LastCommitDB = metrics.LastCommitDB
	}

	return sh
}

// checkBeadsStores reports on the stores bd actually reads: the town store and
// each registered rig's store.
//
// gt health's Databases section reports on the Dolt server. Whether bd uses
// that server is a per-store property recorded in each store's metadata.json,
// and when it says "embedded" bd opens a local store instead. On a town in that
// state every server-side count is a true statement about a database nothing
// reads, which is how "gt: 0 issues" sat next to a store holding hundreds of
// open ones for days (gt-kei9). Counting the stores directly is the only way
// the report can be checked against what bd returns.
//
// A store that cannot be counted is marked Counted=false with the error, never
// reported as zero.
func checkBeadsStores(townRoot string) []BeadsStoreHealth {
	type target struct {
		scope    string
		workDir  string
		beadsDir string
	}

	targets := []target{{
		scope:    "town",
		workDir:  townRoot,
		beadsDir: beads.ResolveBeadsDir(townRoot),
	}}

	if rigsConfig, err := config.LoadRigsConfig(constants.MayorRigsPath(townRoot)); err == nil {
		names := make([]string, 0, len(rigsConfig.Rigs))
		for name := range rigsConfig.Rigs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			rigPath := filepath.Join(townRoot, name)
			targets = append(targets, target{
				scope:    name,
				workDir:  rigPath,
				beadsDir: beads.ResolveBeadsDir(rigPath),
			})
		}
	}

	results := make([]BeadsStoreHealth, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			bsh := BeadsStoreHealth{Scope: t.scope, Path: t.beadsDir}
			bsh.Mode, bsh.Database = readBeadsStoreMode(t.beadsDir)
			bsh.UsesServer = bsh.Mode == "server"

			// A hung store must not hang the diagnostic. bd's own subprocess
			// timeout is 60s, which is longer than anyone will wait during the
			// incident this section exists to diagnose, so the count is given a
			// shorter deadline of its own and a miss is reported as one.
			type countResult struct {
				out []byte
				err error
			}
			done := make(chan countResult, 1)
			go func() {
				out, err := beads.NewWithBeadsDir(t.workDir, t.beadsDir).Run("stats", "--json")
				done <- countResult{out: out, err: err}
			}()

			var res countResult
			select {
			case res = <-done:
			case <-time.After(beadsStoreCountTimeout):
				bsh.Error = fmt.Sprintf("bd stats did not answer within %s", beadsStoreCountTimeout)
				results[i] = bsh
				return
			}

			if res.err != nil {
				bsh.Error = res.err.Error()
				results[i] = bsh
				return
			}

			var stats struct {
				Summary struct {
					TotalIssues  int `json:"total_issues"`
					OpenIssues   int `json:"open_issues"`
					ClosedIssues int `json:"closed_issues"`
				} `json:"summary"`
			}
			if err := json.Unmarshal(res.out, &stats); err != nil {
				bsh.Error = fmt.Sprintf("parsing bd stats: %v", err)
				results[i] = bsh
				return
			}

			bsh.Counted = true
			bsh.TotalIssues = stats.Summary.TotalIssues
			bsh.OpenIssues = stats.Summary.OpenIssues
			bsh.ClosedIssues = stats.Summary.ClosedIssues
			results[i] = bsh
		}(i, t)
	}
	wg.Wait()

	return results
}

// readBeadsStoreMode reads dolt_mode and dolt_database from a store's
// metadata.json. Returns empty strings when the file is absent or unreadable,
// which is itself reportable: without metadata.json bd cannot identify the
// database, and the caller shows "unknown" rather than assuming a mode.
func readBeadsStoreMode(beadsDir string) (mode, database string) {
	data, err := os.ReadFile(filepath.Join(beadsDir, "metadata.json")) //nolint:gosec // G304: path is constructed internally
	if err != nil {
		return "", ""
	}
	var metadata struct {
		DoltMode     string `json:"dolt_mode"`
		DoltDatabase string `json:"dolt_database"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", ""
	}
	return metadata.DoltMode, metadata.DoltDatabase
}

func checkDatabaseHealth(port int) []DatabaseHealth {
	productionDBs := []string{"hq", "gt", "mo"}
	var results []DatabaseHealth

	for _, dbName := range productionDBs {
		dh := DatabaseHealth{Name: dbName}

		// wa-d6f: socket-first DSN (TCP fallback) to avoid TIME_WAIT churn
		// from short-lived gt-CLI calls into Dolt.
		dsn := buildDoltDSN("root", port, dbName, dsnOpts{
			ParseTime:   true,
			Timeout:     "5s",
			ReadTimeout: "10s",
		})
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			results = append(results, dh)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		// Issue counts
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues").Scan(&dh.Issues)
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues WHERE status IN ('open','in_progress')").Scan(&dh.OpenIssues)

		// Wisp counts
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM wisps").Scan(&dh.Wisps)
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM wisps WHERE status IN ('open','hooked','in_progress')").Scan(&dh.OpenWisps)

		// Commit count
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM dolt_log").Scan(&dh.Commits)

		cancel()
		db.Close()
		results = append(results, dh)
	}

	return results
}

func checkPollution(port int) []PollutionRecord {
	productionDBs := []string{"hq", "gt", "mo"}
	var records []PollutionRecord

	// Known pollution patterns to check in the issues table.
	type check struct {
		where   string
		pattern string
	}
	checks := []check{
		{"title LIKE '--%'", "--help artifacts"},
		{"title LIKE 'Usage: %'", "CLI usage output"},
		{"id LIKE 'offlinebrew-%'", "offlinebrew test prefix"},
		{"id LIKE '%-wisp-%' AND (ephemeral IS NULL OR ephemeral = false)", "non-ephemeral wisp ID in issues table"},
		{"title LIKE 'Test Issue%'", "test issue title"},
		{"id LIKE 'test%'", "test ID prefix"},
	}

	for _, dbName := range productionDBs {
		// wa-d6f: socket-first DSN (TCP fallback) — same rationale as above.
		dsn := buildDoltDSN("root", port, dbName, dsnOpts{
			ParseTime:   true,
			Timeout:     "5s",
			ReadTimeout: "10s",
		})
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		for _, c := range checks {
			query := fmt.Sprintf("SELECT id, COALESCE(title,'') FROM issues WHERE (%s) AND status != 'closed' LIMIT 10", c.where)
			rows, err := db.QueryContext(ctx, query)
			if err != nil {
				continue
			}
			for rows.Next() {
				var id, title string
				if err := rows.Scan(&id, &title); err != nil {
					continue
				}
				records = append(records, PollutionRecord{
					Database: dbName,
					ID:       id,
					Title:    title,
					Pattern:  c.pattern,
				})
			}
			rows.Close()
		}

		cancel()
		db.Close()
	}

	return records
}

func checkBackupHealth(townRoot string) *BackupHealth {
	bh := &BackupHealth{}

	// Dolt filesystem backup freshness.
	backupDir := filepath.Join(townRoot, ".dolt-backup")
	if _, err := os.Stat(backupDir); err == nil {
		newest := findNewestFile(backupDir)
		if !newest.IsZero() {
			age := time.Since(newest)
			bh.DoltAgeSeconds = int(age.Seconds())
			bh.DoltFreshness = age.Round(time.Second).String()
			bh.DoltStale = age > 30*time.Minute
		}
	}

	// JSONL git backup freshness.
	homeDir, err := os.UserHomeDir()
	if err == nil {
		gitRepo := filepath.Join(homeDir, ".dolt-archive", "git")
		if _, err := os.Stat(filepath.Join(gitRepo, ".git")); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "git", "-C", gitRepo, "log", "-1", "--format=%ci")
			output, err := cmd.Output()
			if err == nil {
				commitTimeStr := strings.TrimSpace(string(output))
				if commitTime, err := time.Parse("2006-01-02 15:04:05 -0700", commitTimeStr); err == nil {
					age := time.Since(commitTime)
					bh.JSONLAgeSeconds = int(age.Seconds())
					bh.JSONLFreshness = age.Round(time.Second).String()
					bh.JSONLStale = age > 30*time.Minute
				}
			}
		}
	}

	return bh
}

// checkProcessHealth finds zombie Dolt servers (not on the expected port).
// Uses lsof-based port discovery instead of pgrep/ps string matching (ZFC fix: gt-fj87).
func checkProcessHealth(expectedPort int) *ProcessHealth {
	result := health.FindZombieServers([]int{expectedPort})
	return &ProcessHealth{
		ZombieCount: result.Count,
		ZombiePIDs:  result.PIDs,
	}
}

func checkOrphanDBs(townRoot string) []OrphanDB {
	orphans, err := doltserver.FindOrphanedDatabases(townRoot)
	if err != nil {
		return nil
	}

	var results []OrphanDB
	for _, o := range orphans {
		results = append(results, OrphanDB{
			Name: o.Name,
			Size: formatBytes(o.SizeBytes),
		})
	}
	return results
}

func printHealthReport(r *HealthReport) {
	// 1. Server
	fmt.Printf("\n%s Dolt Server\n", style.Bold.Render("●"))
	if r.Server.Running {
		fmt.Printf("  Status: %s (PID %d)\n", style.Bold.Render("running"), r.Server.PID)
		if r.Server.Port > 0 {
			fmt.Printf("  Port: %d\n", r.Server.Port)
		}
		fmt.Printf("  Latency: %dms\n", r.Server.LatencyMs)
		fmt.Printf("  Connections: %d / %d\n", r.Server.Connections, r.Server.MaxConnections)
		if r.Server.DiskUsageHuman != "" {
			fmt.Printf("  Disk: %s\n", r.Server.DiskUsageHuman)
		}
	} else {
		fmt.Printf("  Status: %s\n", style.Dim.Render("not running"))
	}

	// 2. Databases
	if len(r.Databases) > 0 {
		header := "Databases"
		if r.Server != nil && r.Server.Port > 0 {
			header = fmt.Sprintf("Databases (Dolt server on port %d)", r.Server.Port)
		}
		fmt.Printf("\n%s %s\n", style.Bold.Render("●"), header)
		for _, db := range r.Databases {
			fmt.Printf("  %s: %d issues (%d open), %d wisps (%d open), %d commits\n",
				style.Bold.Render(db.Name), db.Issues, db.OpenIssues,
				db.Wisps, db.OpenWisps, db.Commits)
		}
	}

	// 3. Beads stores
	printBeadsStores(r)

	// 4. Pollution
	fmt.Printf("\n%s Pollution\n", style.Bold.Render("●"))
	if len(r.Pollution) == 0 {
		fmt.Printf("  %s No pollution detected\n", style.Bold.Render("✓"))
	} else {
		fmt.Printf("  %s %d suspicious record(s):\n", style.Bold.Render("!"), len(r.Pollution))
		for _, p := range r.Pollution {
			fmt.Printf("    %s/%s: %q (%s)\n", p.Database, p.ID, p.Title, p.Pattern)
		}
	}

	// 5. Backups
	fmt.Printf("\n%s Backups\n", style.Bold.Render("●"))
	if r.Backups.DoltFreshness != "" {
		icon := style.Bold.Render("✓")
		if r.Backups.DoltStale {
			icon = style.Bold.Render("!")
		}
		fmt.Printf("  %s Dolt filesystem: %s ago\n", icon, r.Backups.DoltFreshness)
	} else {
		fmt.Printf("  %s Dolt filesystem: not found\n", style.Dim.Render("○"))
	}
	if r.Backups.JSONLFreshness != "" {
		icon := style.Bold.Render("✓")
		if r.Backups.JSONLStale {
			icon = style.Bold.Render("!")
		}
		fmt.Printf("  %s JSONL git: %s ago\n", icon, r.Backups.JSONLFreshness)
	} else {
		fmt.Printf("  %s JSONL git: not found\n", style.Dim.Render("○"))
	}

	// 6. Processes
	fmt.Printf("\n%s Processes\n", style.Bold.Render("●"))
	if r.Processes.ZombieCount == 0 {
		fmt.Printf("  %s No zombie processes\n", style.Bold.Render("✓"))
	} else {
		fmt.Printf("  %s %d zombie(s): %v\n", style.Bold.Render("!"),
			r.Processes.ZombieCount, r.Processes.ZombiePIDs)
	}

	// 7. Orphans
	fmt.Printf("\n%s Orphan DBs\n", style.Bold.Render("●"))
	if len(r.Orphans) == 0 {
		fmt.Printf("  %s None\n", style.Bold.Render("✓"))
	} else {
		for _, o := range r.Orphans {
			fmt.Printf("  %s %s (%s)\n", style.Bold.Render("!"), o.Name, o.Size)
		}
	}

	fmt.Println()
}

// printBeadsStores renders the stores bd reads, and says plainly when a store
// is not served by the Dolt server the Databases section reports on.
func printBeadsStores(r *HealthReport) {
	fmt.Printf("\n%s Beads stores (what bd actually reads)\n", style.Bold.Render("●"))
	if len(r.BeadsStores) == 0 {
		fmt.Printf("  %s No beads stores resolved\n", style.Warning.Render("!"))
		return
	}

	offServer := 0
	for _, b := range r.BeadsStores {
		mode := b.Mode
		if mode == "" {
			mode = "unknown"
		}
		fmt.Printf("  %s: %s\n", style.Bold.Render(b.Scope), b.Path)
		if b.Database != "" {
			fmt.Printf("    mode: %s, database: %s\n", mode, b.Database)
		} else {
			fmt.Printf("    mode: %s\n", mode)
		}
		if b.Counted {
			fmt.Printf("    %d issues (%d open, %d closed)\n", b.TotalIssues, b.OpenIssues, b.ClosedIssues)
		} else {
			fmt.Printf("    %s counts UNAVAILABLE: %s\n", style.Warning.Render("!"), b.Error)
		}
		if !b.UsesServer {
			offServer++
		}
	}

	if offServer > 0 && r.Server != nil && r.Server.Running {
		fmt.Printf("\n  %s %d of %d store(s) are NOT served by the Dolt server on port %d.\n",
			style.Warning.Render("!"), offServer, len(r.BeadsStores), r.Server.Port)
		fmt.Printf("  The Databases section above reports on that server, so its counts\n")
		fmt.Printf("  are not a measurement of those stores. See gt-kei9.\n")
	}
}
