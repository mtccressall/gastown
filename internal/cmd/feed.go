package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/krc"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/tui/feed"
	"github.com/steveyegge/gastown/internal/workspace"
	"golang.org/x/term"
)

var (
	feedFollow   bool
	feedLimit    int
	feedSince    string
	feedMol      string
	feedType     string
	feedRig      string
	feedNoFollow bool
	feedWindow   bool
	feedPlain    bool
	feedProblems bool
)

func init() {
	rootCmd.AddCommand(feedCmd)

	feedCmd.Flags().BoolVarP(&feedFollow, "follow", "f", false, "Stream events in real-time (default when no other flags)")
	feedCmd.Flags().BoolVar(&feedNoFollow, "no-follow", false, "Show events once and exit")
	feedCmd.Flags().IntVarP(&feedLimit, "limit", "n", 100, "Maximum number of events to show")
	feedCmd.Flags().StringVar(&feedSince, "since", "", "Show events since duration (e.g., 5m, 1h, 30s)")
	feedCmd.Flags().StringVar(&feedMol, "mol", "", "Filter by molecule/issue ID prefix")
	feedCmd.Flags().StringVar(&feedType, "type", "", "Filter by event type (create, update, delete, comment)")
	feedCmd.Flags().StringVar(&feedRig, "rig", "", "Filter events by rig name")
	feedCmd.Flags().BoolVarP(&feedWindow, "window", "w", false, "Open in dedicated tmux window (creates 'feed' window)")
	feedCmd.Flags().BoolVar(&feedPlain, "plain", false, "Use plain text output (bd activity) instead of TUI")
	feedCmd.Flags().BoolVarP(&feedProblems, "problems", "p", false, "Start in problems view (shows stuck agents)")
}

var feedCmd = &cobra.Command{
	Use:     "feed",
	GroupID: GroupDiag,
	Short:   "Show real-time activity feed of gt events",
	Long: `Display a real-time feed of issue changes and agent activity.

By default, launches an interactive TUI dashboard with:
  - Agent tree (top): Shows all agents organized by role with latest activity
  - Convoy panel (middle): Shows in-progress and recently landed convoys
  - Event stream (bottom): Chronological feed you can scroll through
  - Vim-style navigation: j/k to scroll, tab to switch panels, 1/2/3 for panels, q to quit

Problems View (--problems/-p):
  A problem-first view that surfaces agents needing attention:
  - Detects stuck agents via structured beads data (hook state, timestamps)
  - Shows GUPP violations (hooked work + 30m no progress)
  - Keyboard actions: Enter=attach, n=nudge, h=handoff
  - Press 'p' to toggle between activity and problems view

The feed combines multiple event sources:
  - GT events: Agent activity like patrol, sling, handoff (from .events.jsonl)
  - Beads activity: Issue creates, updates, completions (from bd activity, when available)
  - Convoy status: In-progress and recently-landed convoys (refreshes every 10s)

Use --plain for simple text output (reads .events.jsonl directly).

Tmux Integration:
  Use --window to open the feed in a dedicated tmux window named 'feed'.
  This creates a persistent window you can cycle to with C-b n/p.

Event symbols:
  +  created/bonded    - New issue or molecule created
  →  in_progress       - Work started on an issue
  ✓  completed         - Issue closed or step completed
  ✗  failed            - Step or issue failed
  ⊘  deleted           - Issue removed
  🦉  patrol_started   - Witness began patrol cycle
  ⚡  polecat_nudged   - Worker was nudged
  🎯  sling            - Work was slung to worker
  🤝  handoff          - Session handed off

Agent state symbols (problems view):
  🔥  GUPP violation   - Hooked work + 30m no progress (critical)
  ⚠   STALLED          - Hooked work + 15m no progress
  ●   Working          - Actively producing output
  ○   Idle             - No hooked work
  💀  Zombie           - Dead/crashed session

MQ (Merge Queue) event symbols:
  ⚙  merge_started   - Refinery began processing an MR
  ✓  merged          - MR successfully merged (green)
  ✗  merge_failed    - Merge failed (conflict, tests, etc.) (red)
  ⊘  merge_skipped   - MR skipped (already merged, etc.)

Examples:
  gt feed                       # Launch TUI dashboard
  gt feed --problems            # Start in problems view
  gt feed -p                    # Short flag for problems view
  gt feed --plain               # Plain text output (bd activity)
  gt feed --window              # Open in dedicated tmux window
  gt feed --since 1h            # Events from last hour
  gt feed --rig greenplace      # Use gastown rig's beads`,
	RunE: runFeed,
}

func runFeed(cmd *cobra.Command, args []string) error {
	// Must be in a Gas Town workspace
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace (run from ~/gt or a rig directory)")
	}

	// Build feed arguments for window mode
	bdArgs := buildFeedArgs()

	// Handle --window mode: --rig is forwarded as a CLI flag via buildFeedArgs
	if feedWindow {
		workDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		return runFeedInWindow(workDir, bdArgs)
	}

	// Use TUI by default if running in a terminal and not --plain
	useTUI := !feedPlain && term.IsTerminal(int(os.Stdout.Fd()))

	if useTUI {
		// TUI mode: resolve --rig to a beads directory for BdActivitySource
		workDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		if feedRig != "" {
			candidates := []string{
				fmt.Sprintf("%s/%s/mayor/rig", townRoot, feedRig),
				fmt.Sprintf("%s/%s", townRoot, feedRig),
			}
			found := false
			for _, candidate := range candidates {
				if _, err := os.Stat(candidate + "/.beads"); err == nil {
					workDir = candidate
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("rig '%s' not found or has no .beads directory", feedRig)
			}
		}
		return runFeedTUI(workDir, feedProblems)
	}

	// Plain mode: --rig is a pure event filter via PrintOptions.Rig
	return runFeedDirect(townRoot)
}

// buildFeedArgs builds the feed CLI arguments for window mode.
func buildFeedArgs() []string {
	var args []string

	// Default to follow mode unless --no-follow set
	shouldFollow := !feedNoFollow
	if feedFollow {
		shouldFollow = true
	}

	// Auto-disable follow when stdout is not a TTY (e.g. agents, pipes),
	// unless the user explicitly passed --follow. This prevents agents
	// from blocking on a streaming feed that never terminates.
	if !term.IsTerminal(int(os.Stdout.Fd())) && !feedFollow {
		shouldFollow = false
	}

	if shouldFollow {
		args = append(args, "--follow")
	}

	if feedLimit != 100 {
		args = append(args, "--limit", fmt.Sprintf("%d", feedLimit))
	}

	if feedSince != "" {
		args = append(args, "--since", feedSince)
	}

	if feedMol != "" {
		args = append(args, "--mol", feedMol)
	}

	if feedType != "" {
		args = append(args, "--type", feedType)
	}

	if feedRig != "" {
		args = append(args, "--rig", feedRig)
	}

	return args
}

// runFeedDirect prints events from .events.jsonl to stdout.
// Supports --follow for tailing, and --since/--mol/--type for filtering.
// townRoot is the resolved workspace root (incorporates --rig if set).
func runFeedDirect(townRoot string) error {
	// Determine follow behavior:
	// - Explicit --follow: always follow
	// - Explicit --no-follow: never follow
	// - Non-TTY (pipe/script): no follow unless explicitly requested
	// - Default (TTY, no flags): follow
	shouldFollow := feedFollow
	if !shouldFollow && !feedNoFollow {
		shouldFollow = term.IsTerminal(int(os.Stdout.Fd()))
	}

	opts := feed.PrintOptions{
		Limit:  feedLimit,
		Follow: shouldFollow,
		Since:  feedSince,
		Mol:    feedMol,
		Type:   feedType,
		Rig:    feedRig,
	}

	// DISCLOSE THE RETENTION WINDOW AT THE POINT OF READING (gastown-yu4).
	//
	// .events.jsonl is pruned per row at a per type TTL, so every event type
	// has its own horizon and they differ by an order of magnitude in one
	// file. Nothing in the feed's output said so, and the obvious sanity
	// check -- the first line of the file -- is correct for a 30d type and
	// wrong for a 3d one. Print the horizon of whatever is being shown.
	//
	// Printed at BOTH ends when not following, because head always keeps the
	// first line and tail always keeps the last, so a marker at each end
	// survives any truncation by construction. In follow mode there is no
	// end, so the header is all there can be.
	banner := feedRetentionBanner(townRoot, feedType)
	if banner != "" {
		fmt.Println(banner)
	}

	if err := feed.PrintGtEvents(townRoot, opts); err != nil {
		return err
	}

	if banner != "" && !shouldFollow {
		fmt.Println(banner)
	}
	return nil
}

// feedRetentionBanner describes how far back the events file actually reaches.
//
// Filtered to one type, it names that type's TTL and oldest surviving row.
// Unfiltered, it says plainly that horizons differ per type and points at the
// command that prints the table. It returns "" rather than an error on any
// failure: a feed read must not break because the window could not be measured.
func feedRetentionBanner(townRoot, eventType string) string {
	config, err := krc.LoadConfig(townRoot)
	if err != nil {
		return ""
	}
	w, err := krc.ScanFileWindow(filepath.Join(townRoot, events.EventsFile), config, time.Now())
	if err != nil || !w.Exists || w.FirstRow.IsZero() {
		return ""
	}

	head := fmt.Sprintf("%s (type %q)", w.FirstRow.UTC().Format(time.RFC3339), w.FirstRowType)

	if eventType != "" {
		ttl := krcFormatDuration(config.GetTTL(eventType))
		t := w.Type(eventType)
		if t == nil {
			return style.Dim.Render(fmt.Sprintf(
				"retention: type %q has TTL %s and no surviving rows in .events.jsonl. "+
					"The file's own first row is %s, which is not this type's horizon.",
				eventType, ttl, head))
		}

		oldest := t.Oldest.UTC().Format(time.RFC3339)
		reach := krcFormatDuration(t.Reach)

		// Two different things can set a horizon, and conflating them is the
		// mirror of the error this banner exists to fix: a type under its TTL
		// has had nothing pruned, so its reach is where its data starts and
		// says nothing about the TTL.
		cause := fmt.Sprintf("has TTL %s and has not reached it, so %s is where this "+
			"type's data starts rather than where pruning cut", ttl, oldest)
		if t.TTLBound {
			cause = fmt.Sprintf("has TTL %s and is being pruned at it, so %s is a "+
				"ceiling rather than a start", ttl, oldest)
		}

		// For the head type the file head IS the horizon. Claiming otherwise
		// would be the same false statement pointed the other way.
		contrast := fmt.Sprintf("The file's own first row is %s, which is NOT this "+
			"type's horizon.", head)
		if w.IsHeadType(t) {
			contrast = "This type's oldest row is also the file's first row, so here " +
				"the two agree -- they do not for shorter-lived types."
		}

		return style.Dim.Render(fmt.Sprintf(
			"retention: type %q %s. A %q census reaches back %s. %s",
			eventType, cause, eventType, reach, contrast))
	}

	spread := ""
	if shortest, ok := w.ShortestReach(); ok {
		if longest, ok2 := w.LongestReach(); ok2 {
			spread = fmt.Sprintf(" Horizons here run %s (%s) to %s (%s).",
				krcFormatDuration(shortest.Reach), shortest.EventType,
				krcFormatDuration(longest.Reach), longest.EventType)
		}
	}
	return style.Dim.Render(fmt.Sprintf(
		"retention: .events.jsonl is pruned per row at a per type TTL, so each event type has "+
			"its OWN horizon.%s The file's own first row is %s and is not the horizon for any "+
			"other type. Run 'gt krc window' for the per-type table.",
		spread, head))
}

// runFeedTUI runs the interactive TUI feed.
func runFeedTUI(workDir string, problemsView bool) error {
	// Must be in a Gas Town workspace
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	var sources []feed.EventSource

	// Create event source from bd activity (optional - bd may not have activity command)
	bdSource, err := feed.NewBdActivitySource(workDir)
	if err == nil {
		sources = append(sources, bdSource)
	}

	// Create MQ event source (optional - don't fail if not available)
	mqSource, err := feed.NewMQEventSourceFromWorkDir(workDir)
	if err == nil {
		sources = append(sources, mqSource)
	}

	// Create GT events source (optional - don't fail if not available)
	gtSource, err := feed.NewGtEventsSource(townRoot)
	if err == nil {
		sources = append(sources, gtSource)
	}

	if len(sources) == 0 {
		return fmt.Errorf("no event sources available (check that .events.jsonl exists in %s)", townRoot)
	}

	// Combine all sources
	multiSource := feed.NewMultiSource(sources...)
	defer func() { _ = multiSource.Close() }()

	// Create beads instance for agent health detection
	bd := beads.New(townRoot)

	// Create model and connect event source
	var m *feed.Model
	if problemsView {
		m = feed.NewModelWithProblemsView(bd)
	} else {
		m = feed.NewModel(bd)
	}
	m.SetEventChannel(multiSource.Events())
	m.SetTownRoot(townRoot)

	// Run the TUI
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	return nil
}

// runFeedInWindow opens the feed in a dedicated tmux window.
func runFeedInWindow(workDir string, bdArgs []string) error {
	// Check if we're in tmux
	if !tmux.IsInsideTmux() {
		return fmt.Errorf("--window requires running inside tmux")
	}

	// Get current session from TMUX env var
	// Format: /tmp/tmux-501/default,12345,0 -> we need the session name
	tmuxEnv := os.Getenv("TMUX")
	if tmuxEnv == "" {
		return fmt.Errorf("TMUX environment variable not set")
	}

	t := tmux.NewTmux()

	// Get current session name
	sessionName, err := getCurrentTmuxSession()
	if err != nil {
		return fmt.Errorf("getting current session: %w", err)
	}

	// Build the command to run in the window
	// Use gt feed --plain instead of bd activity (which may not exist)
	gtPath, err := os.Executable()
	if err != nil {
		gtPath = "gt"
	}
	feedWindowCmd := fmt.Sprintf("cd \"%s\" && \"%s\" feed --plain --follow", workDir, gtPath)
	if len(bdArgs) > 0 {
		var filteredArgs []string
		for _, arg := range bdArgs {
			if arg != "--follow" {
				filteredArgs = append(filteredArgs, arg)
			}
		}
		if len(filteredArgs) > 0 {
			feedWindowCmd = fmt.Sprintf("cd \"%s\" && \"%s\" feed --plain --follow %s", workDir, gtPath, strings.Join(filteredArgs, " "))
		}
	}

	// Check if 'feed' window already exists
	windowTarget := sessionName + ":feed"
	exists, err := windowExists(t, sessionName, "feed")
	if err != nil {
		return fmt.Errorf("checking for feed window: %w", err)
	}

	if exists {
		// Window exists - just switch to it
		fmt.Printf("Switching to existing feed window...\n")
		return selectWindow(t, windowTarget)
	}

	// Create new window named 'feed'
	fmt.Printf("Creating feed window in session %s...\n", sessionName)
	if err := createWindow(t, sessionName, "feed", workDir, feedWindowCmd); err != nil {
		return fmt.Errorf("creating feed window: %w", err)
	}

	// Switch to the new window
	return selectWindow(t, windowTarget)
}

// windowExists checks if a window with the given name exists in the session.
// Note: getCurrentTmuxSession is defined in handoff.go
func windowExists(_ *tmux.Tmux, session, windowName string) (bool, error) { // t unused: direct exec for simplicity
	cmd := tmux.BuildCommand("list-windows", "-t", session, "-F", "#{window_name}")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == windowName {
			return true, nil
		}
	}
	return false, nil
}

// createWindow creates a new tmux window with the given name and command.
func createWindow(_ *tmux.Tmux, session, windowName, workDir, command string) error { // t unused: direct exec for simplicity
	args := []string{"new-window", "-t", session, "-n", windowName, "-c", workDir, command}
	cmd := tmux.BuildCommand(args...)
	return cmd.Run()
}

// selectWindow switches to the specified window.
func selectWindow(_ *tmux.Tmux, target string) error { // t unused: direct exec for simplicity
	cmd := tmux.BuildCommand("select-window", "-t", target)
	return cmd.Run()
}
