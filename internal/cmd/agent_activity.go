package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/activity"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/util"
)

// AgentActivity is the write-derived liveness signal reported by the witness
// and refinery status commands.
//
// It exists because "running" is derived from tmux session existence and so
// reports true for a frozen agent (gt-fy6). mol-deacon-patrol's health table
// asks the Deacon to read "recent activity", and before this field there was
// no time-bearing field in either command's output to read it from.
//
// Source is always populated, including when the age could not be measured, so
// that a failure to measure is never rendered the same as a recent write.
type AgentActivity struct {
	// LastActivity is an RFC3339 UTC timestamp the agent itself wrote.
	LastActivity string `json:"last_activity,omitempty"`
	// AgeSeconds is how long ago that write happened, at report time.
	AgeSeconds *int64 `json:"last_activity_age_seconds,omitempty"`
	// Source names where the timestamp came from: "transcript" when measured,
	// "unsupported" when this agent provider writes no readable transcript,
	// "ambiguous" when the agent shares a working directory with another role,
	// or "unavailable" when a Claude agent has written nothing findable.
	Source string `json:"last_activity_source"`
	// Error explains a non-"transcript" source. Never empty in that case.
	Error string `json:"last_activity_error,omitempty"`
}

// claudeConfigDirsFor returns every Claude Code config directory the agent
// running in sessionName may have written transcripts into.
//
// The agent's config dir is NOT this process's. `gt quota rotate` sets a new
// CLAUDE_CONFIG_DIR on the target tmux session (executor.go:220) and leaves the
// shell running `gt ... status` untouched, so resolving only the default
// account would miss every write a rotated agent made and report a healthy
// agent as stale. Searching a superset is safe: a transcript directory is named
// after the agent's own working directory, so another agent's records can never
// be picked up here.
func claudeConfigDirsFor(townRoot, sessionName string) []string {
	var dirs []string

	// Authoritative for a live session: what the session itself carries.
	if sessionName != "" {
		if dir, err := tmux.NewTmux().GetEnvironment(sessionName, "CLAUDE_CONFIG_DIR"); err == nil && dir != "" {
			dirs = append(dirs, util.ExpandHome(dir))
		}
	}

	// Every configured account, for a session tmux cannot answer for (stopped,
	// or rotated to an account this shell knows nothing about).
	if townRoot != "" {
		if acctCfg, err := config.LoadAccountsConfig(constants.MayorAccountsPath(townRoot)); err == nil {
			for _, acct := range acctCfg.Accounts {
				if acct.ConfigDir != "" {
					dirs = append(dirs, util.ExpandHome(acct.ConfigDir))
				}
			}
		}
	}

	// ClaudeConfigDir already honours CLAUDE_CONFIG_DIR, falling back to ~/.claude.
	if ambient, err := config.ClaudeConfigDir(); err == nil && ambient != "" {
		dirs = append(dirs, ambient)
	}
	return dirs
}

// activityQuery describes the agent whose last write is being measured.
type activityQuery struct {
	// TownRoot is the town containing the rig, used to enumerate accounts.
	TownRoot string
	// SessionName is the agent's tmux session. It is the authority on the
	// config dir and working directory the agent is actually running under,
	// both of which `gt ... start` overrides can move. May be empty.
	SessionName string
	// WorkDir is the working directory derived from the rig layout.
	WorkDir string
	// Provider is the agent runtime ("claude", "codex", "generic"); empty
	// means claude.
	Provider string
	// SharedDirs are working directories another role also runs in. Claude Code
	// files transcripts by working directory, so records under one of these
	// cannot be attributed to this agent and must not be reported as its
	// activity. Reporting nothing is correct there; reporting the neighbour's
	// writes would make a frozen agent read fresh.
	SharedDirs []string
}

// resolveAgentActivity reports when the queried agent last wrote a transcript
// record.
//
// DO NOT reimplement this on file mtime. Measured 2026-09-05, a witness frozen
// for 9.6 hours had a transcript mtime age of 0 minutes, which ranked the most
// frozen session in town as the healthiest. See gt-4vt7 / gastown-7k9.
func resolveAgentActivity(q activityQuery, now time.Time) AgentActivity {
	// Only Claude Code's transcript layout is read today. Say so explicitly
	// rather than reporting a bare "unavailable": a provider this cannot measure
	// and an agent that has stopped writing must not look the same to the Deacon.
	if q.Provider != "" && !strings.EqualFold(q.Provider, "claude") {
		return AgentActivity{
			Source: "unsupported",
			Error: fmt.Sprintf("agent provider %q writes no Claude Code transcript; "+
				"activity is not measurable for this agent (gastown-7k9)", q.Provider),
		}
	}

	workDirs := agentWorkDirs(q.SessionName, q.WorkDir)
	if shared := firstShared(workDirs, q.SharedDirs); shared != "" {
		return AgentActivity{
			Source: "ambiguous",
			Error: fmt.Sprintf("this agent is running in %s, which another role also runs in; "+
				"Claude Code files transcripts by working directory, so records there cannot "+
				"be attributed to either agent (gastown-7k9)", shared),
		}
	}

	rec, err := activity.LastRecord(claudeConfigDirsFor(q.TownRoot, q.SessionName), workDirs)
	if err != nil {
		reason := err.Error()
		if errors.Is(err, activity.ErrNoRecords) {
			reason = fmt.Sprintf("no transcript records found for %s", strings.Join(workDirs, ", "))
		}
		return AgentActivity{Source: "unavailable", Error: reason}
	}

	age := int64(now.Sub(rec.Time).Seconds())
	if age < 0 {
		age = 0
	}
	return AgentActivity{
		LastActivity: rec.Time.Format(time.RFC3339),
		AgeSeconds:   &age,
		Source:       "transcript",
	}
}

// firstShared returns the first work dir that is also another role's, or "".
func firstShared(workDirs, sharedDirs []string) string {
	for _, w := range workDirs {
		for _, s := range sharedDirs {
			if s != "" && filepath.Clean(w) == filepath.Clean(s) {
				return s
			}
		}
	}
	return ""
}

// printAgentActivity renders the activity line for human-readable status output.
func printAgentActivity(a AgentActivity) {
	if a.Source != "transcript" || a.AgeSeconds == nil {
		fmt.Printf("  Last activity: %s\n", style.Dim.Render("unknown ("+a.Error+")"))
		return
	}
	age := time.Duration(*a.AgeSeconds) * time.Second
	fmt.Printf("  Last activity: %s (%s ago)\n", a.LastActivity, activity.FormatAge(age))
}

// agentProvider returns the agent runtime the AGENT is actually running under
// ("claude", "codex", "generic"). An empty result means claude.
//
// The live session wins over the role's configured agent, because `gt witness
// start --agent <name>` records the override in the session's GT_AGENT and
// leaves the role config untouched. Reading only the role config would call a
// codex override "claude" and a claude override "codex", and both readings
// misreport the resulting activity signal.
func agentProvider(role, townRoot, rigPath, sessionName string) string {
	if sessionName != "" {
		if name, err := tmux.NewTmux().GetEnvironment(sessionName, "GT_AGENT"); err == nil && name != "" {
			if rc := config.ResolveAgentConfigByName(name, townRoot, rigPath); rc != nil {
				return rc.Provider
			}
		}
	}
	if rc := config.ResolveRoleAgentConfig(role, townRoot, rigPath); rc != nil {
		return rc.Provider
	}
	return ""
}

// agentWorkDirs returns every working directory the agent's transcripts may be
// filed under: the directory the live session actually reports, plus the one
// derived from the rig layout.
//
// Both are needed. A session's real cwd is authoritative but unavailable once
// it stops; the derived path can be wrong for a live session, because the
// refinery falls back to mayor/rig when worktree repair fails while leaving the
// refinery directory in place, so the derived path names a directory the agent
// is not in. Searching both cannot produce a false fresh reading: a transcript
// directory is named after its own working directory, so an extra path can only
// contribute records written from that path.
func agentWorkDirs(sessionName, derived string) []string {
	var dirs []string
	if sessionName != "" {
		if cwd, err := tmux.NewTmux().GetPaneWorkDir(sessionName); err == nil && cwd != "" && cwd != derived {
			dirs = append(dirs, cwd)
		}
	}
	if derived != "" {
		dirs = append(dirs, derived)
	}
	return dirs
}

// townRootForRig returns the town root containing a rig path.
func townRootForRig(rigPath string) string {
	if rigPath == "" {
		return ""
	}
	return filepath.Dir(rigPath)
}
