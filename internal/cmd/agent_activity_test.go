package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/activity"
	"github.com/steveyegge/gastown/internal/constants"
)

func writeTestTranscript(t *testing.T, configDir, workDir string, stamp time.Time) {
	t.Helper()
	dir := activity.ProjectDir(configDir, workDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	line := fmt.Sprintf("{\"type\":\"assistant\",\"timestamp\":%q}\n", stamp.Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestResolveAgentActivityReportsWriteDerivedAge(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	workDir := "/town/gastown/witness"
	last := time.Date(2026, 9, 4, 15, 44, 45, 0, time.UTC)
	now := time.Date(2026, 9, 5, 1, 24, 45, 0, time.UTC) // 9h40m later
	writeTestTranscript(t, configDir, workDir, last)

	got := resolveAgentActivity(activityQuery{TownRoot: t.TempDir(), WorkDir: workDir, Provider: "claude"}, now)

	if got.Source != "transcript" {
		t.Errorf("Source = %q, want transcript", got.Source)
	}
	if got.LastActivity != "2026-09-04T15:44:45Z" {
		t.Errorf("LastActivity = %q, want 2026-09-04T15:44:45Z", got.LastActivity)
	}
	if got.AgeSeconds == nil || *got.AgeSeconds != 34800 {
		t.Errorf("AgeSeconds = %v, want 34800", got.AgeSeconds)
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want empty", got.Error)
	}
}

// TestResolveAgentActivityUnavailableIsExplicit asserts a failure to measure is
// distinguishable from a recent write. Silently reporting a zero age would make
// a systematic failure to measure look identical to a healthy agent.
func TestResolveAgentActivityUnavailableIsExplicit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	got := resolveAgentActivity(activityQuery{TownRoot: t.TempDir(), WorkDir: "/town/gastown/witness", Provider: "claude"}, time.Now())

	if got.Source != "unavailable" {
		t.Errorf("Source = %q, want unavailable", got.Source)
	}
	if got.AgeSeconds != nil {
		t.Errorf("AgeSeconds = %v, want nil", *got.AgeSeconds)
	}
	if got.LastActivity != "" {
		t.Errorf("LastActivity = %q, want empty", got.LastActivity)
	}
	if got.Error == "" {
		t.Error("Error is empty; an unavailable source must say why")
	}
}

// TestResolveAgentActivityClampsClockSkew guards against a negative age
// rendering as a huge unsigned duration downstream.
func TestResolveAgentActivityClampsClockSkew(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	workDir := "/town/gastown/refinery/rig"
	future := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)
	writeTestTranscript(t, configDir, workDir, future)

	got := resolveAgentActivity(activityQuery{TownRoot: t.TempDir(), WorkDir: workDir}, future.Add(-time.Hour))
	if got.AgeSeconds == nil || *got.AgeSeconds != 0 {
		t.Errorf("AgeSeconds = %v, want 0", got.AgeSeconds)
	}
}

// TestStatusOutputCarriesActivityFields locks the JSON contract the Deacon's
// health-scan step reads. Before gastown-7k9 neither payload carried a single
// time-bearing field, so a frozen agent and a healthy one serialised alike.
func TestStatusOutputCarriesActivityFields(t *testing.T) {
	age := int64(34800)
	act := AgentActivity{
		LastActivity: "2026-09-04T15:44:45Z",
		AgeSeconds:   &age,
		Source:       "transcript",
	}

	witnessJSON, err := json.Marshal(WitnessStatusOutput{
		Running: true, RigName: "gastown", Session: "gastown-witness",
		AgentActivity: act, MonitoredPolecats: []string{"opal"},
	})
	if err != nil {
		t.Fatalf("marshal witness: %v", err)
	}
	refineryJSON, err := json.Marshal(RefineryStatusOutput{
		Running: true, RigName: "gastown", Session: "gastown-refinery",
		AgentActivity: act, QueueLength: 3,
	})
	if err != nil {
		t.Fatalf("marshal refinery: %v", err)
	}

	for name, payload := range map[string]string{
		"witness":  string(witnessJSON),
		"refinery": string(refineryJSON),
	} {
		for _, field := range []string{
			`"last_activity":"2026-09-04T15:44:45Z"`,
			`"last_activity_age_seconds":34800`,
			`"last_activity_source":"transcript"`,
		} {
			if !strings.Contains(payload, field) {
				t.Errorf("%s status JSON missing %s\ngot: %s", name, field, payload)
			}
		}
	}
}

// TestStatusOutputUnavailableSerialises asserts the unavailable case still
// carries a source and a reason rather than silently omitting everything.
func TestStatusOutputUnavailableSerialises(t *testing.T) {
	payload, err := json.Marshal(RefineryStatusOutput{
		Running: false, RigName: "beadsrig",
		AgentActivity: AgentActivity{Source: "unavailable", Error: "no transcript records found"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(payload)
	if strings.Contains(got, `"last_activity"`) {
		t.Errorf("unavailable payload should omit last_activity, got: %s", got)
	}
	for _, want := range []string{`"last_activity_source":"unavailable"`, `"last_activity_error":`} {
		if !strings.Contains(got, want) {
			t.Errorf("payload missing %s\ngot: %s", want, got)
		}
	}
}

// TestClaudeConfigDirsIncludesEveryAccount is the regression test for the
// rotation gap: gt quota rotate sets a new CLAUDE_CONFIG_DIR on the AGENT's
// tmux session, never on the shell running the status command, so resolving
// only the default account reports a rotated-but-healthy agent as stale.
func TestClaudeConfigDirsIncludesEveryAccount(t *testing.T) {
	townRoot := t.TempDir()
	ambient := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", ambient)

	work := filepath.Join(townRoot, "acct-work")
	personal := filepath.Join(townRoot, "acct-personal")
	accountsPath := constants.MayorAccountsPath(townRoot)
	if err := os.MkdirAll(filepath.Dir(accountsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := fmt.Sprintf(`{"version":1,"default":"work","accounts":{`+
		`"work":{"email":"w@example.com","config_dir":%q},`+
		`"personal":{"email":"p@example.com","config_dir":%q}}}`, work, personal)
	if err := os.WriteFile(accountsPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write accounts: %v", err)
	}

	got := claudeConfigDirsFor(townRoot, "")
	for _, want := range []string{work, personal, ambient} {
		if !slices.Contains(got, want) {
			t.Errorf("claudeConfigDirsFor missing %q; got %v", want, got)
		}
	}
}

// TestResolveAgentActivityFindsRotatedAccountTranscript is the end-to-end form:
// the agent wrote into a NON-default account directory, and the status command
// must still find it.
func TestResolveAgentActivityFindsRotatedAccountTranscript(t *testing.T) {
	townRoot := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // ambient dir holds nothing

	rotated := filepath.Join(townRoot, "acct-personal")
	accountsPath := constants.MayorAccountsPath(townRoot)
	if err := os.MkdirAll(filepath.Dir(accountsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := fmt.Sprintf(`{"version":1,"default":"work","accounts":{`+
		`"work":{"email":"w@example.com","config_dir":%q},`+
		`"personal":{"email":"p@example.com","config_dir":%q}}}`,
		filepath.Join(townRoot, "acct-work"), rotated)
	if err := os.WriteFile(accountsPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write accounts: %v", err)
	}

	workDir := "/town/gastown/witness"
	last := time.Date(2026, 9, 5, 1, 48, 20, 0, time.UTC)
	writeTestTranscript(t, rotated, workDir, last)

	got := resolveAgentActivity(activityQuery{TownRoot: townRoot, WorkDir: workDir}, last.Add(5*time.Minute))
	if got.Source != "transcript" {
		t.Fatalf("Source = %q (%s), want transcript", got.Source, got.Error)
	}
	if got.AgeSeconds == nil || *got.AgeSeconds != 300 {
		t.Errorf("AgeSeconds = %v, want 300", got.AgeSeconds)
	}
}

// TestResolveAgentActivityNamesUnsupportedProvider asserts a runtime this cannot
// read is reported as its own state. A codex or generic agent that is perfectly
// healthy must not serialise identically to a Claude agent that stopped writing,
// or the Deacon's health scan learns nothing from either.
func TestResolveAgentActivityNamesUnsupportedProvider(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	got := resolveAgentActivity(activityQuery{TownRoot: t.TempDir(), WorkDir: "/town/gastown/witness", Provider: "codex"}, time.Now())

	if got.Source != "unsupported" {
		t.Errorf("Source = %q, want unsupported", got.Source)
	}
	if !strings.Contains(got.Error, "codex") {
		t.Errorf("Error = %q, want it to name the provider", got.Error)
	}
	if got.AgeSeconds != nil {
		t.Errorf("AgeSeconds = %v, want nil", *got.AgeSeconds)
	}
}

// TestAgentWorkDirsFallsBackToDerived covers the stopped-session case, where
// tmux can report nothing and the derived path is all there is.
func TestAgentWorkDirsFallsBackToDerived(t *testing.T) {
	got := agentWorkDirs("", "/town/gastown/witness")
	if len(got) != 1 || got[0] != "/town/gastown/witness" {
		t.Errorf("agentWorkDirs = %v, want [/town/gastown/witness]", got)
	}
	if got := agentWorkDirs("no-such-session-gastown-7k9", "/town/gastown/witness"); len(got) != 1 {
		t.Errorf("agentWorkDirs for a dead session = %v, want just the derived path", got)
	}
}

// TestResolveAgentActivityRefusesSharedWorkDir is the false-green guard. The
// refinery falls back to the mayor's worktree when its own is missing, and
// Claude Code files transcripts by working directory, so a busy mayor's records
// sit in the same directory as the refinery's. Reporting them would make a
// frozen refinery read fresh, which is the exact failure this whole change
// exists to prevent.
func TestResolveAgentActivityRefusesSharedWorkDir(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	shared := "/town/gastown/mayor/rig"
	// The mayor is busy: a very recent record sits in the shared directory.
	writeTestTranscript(t, configDir, shared, time.Now().UTC())

	got := resolveAgentActivity(activityQuery{
		TownRoot:   t.TempDir(),
		WorkDir:    shared,
		SharedDirs: []string{shared},
	}, time.Now())

	if got.Source != "ambiguous" {
		t.Fatalf("Source = %q (%s), want ambiguous", got.Source, got.Error)
	}
	if got.AgeSeconds != nil {
		t.Errorf("AgeSeconds = %v, want nil: the mayor's writes are not the refinery's", *got.AgeSeconds)
	}
	if !strings.Contains(got.Error, shared) {
		t.Errorf("Error = %q, want it to name the shared directory", got.Error)
	}
}

// TestResolveAgentActivityAllowsUnsharedWorkDir is the negative control: the
// guard must not fire when the refinery is in its own worktree.
func TestResolveAgentActivityAllowsUnsharedWorkDir(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	own := "/town/gastown/refinery/rig"
	last := time.Date(2026, 9, 5, 1, 52, 43, 0, time.UTC)
	writeTestTranscript(t, configDir, own, last)

	got := resolveAgentActivity(activityQuery{
		TownRoot:   t.TempDir(),
		WorkDir:    own,
		SharedDirs: []string{"/town/gastown/mayor/rig"},
	}, last.Add(time.Minute))

	if got.Source != "transcript" {
		t.Fatalf("Source = %q (%s), want transcript", got.Source, got.Error)
	}
	if got.AgeSeconds == nil || *got.AgeSeconds != 60 {
		t.Errorf("AgeSeconds = %v, want 60", got.AgeSeconds)
	}
}
