package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
)

func TestGetTTL(t *testing.T) {
	ttls := defaultTTLs

	tests := []struct {
		wispType string
		want     time.Duration
	}{
		{"heartbeat", 6 * time.Hour},
		{"ping", 6 * time.Hour},
		{"patrol", 24 * time.Hour},
		{"gc_report", 24 * time.Hour},
		{"error", 7 * 24 * time.Hour},
		{"recovery", 7 * 24 * time.Hour},
		{"escalation", 7 * 24 * time.Hour},
		{"default", 24 * time.Hour},
		{"", 24 * time.Hour},        // empty falls back to default
		{"unknown", 24 * time.Hour}, // unknown falls back to default
	}

	for _, tc := range tests {
		t.Run(tc.wispType, func(t *testing.T) {
			got := getTTL(ttls, tc.wispType)
			if got != tc.want {
				t.Errorf("getTTL(%q) = %v, want %v", tc.wispType, got, tc.want)
			}
		})
	}
}

func TestWispAge(t *testing.T) {
	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		updatedAt string
		wantAge   time.Duration
		wantErr   bool
	}{
		{
			name:      "RFC3339",
			updatedAt: "2026-02-07T06:00:00Z",
			wantAge:   6 * time.Hour,
		},
		{
			name:      "one day old",
			updatedAt: "2026-02-06T12:00:00Z",
			wantAge:   24 * time.Hour,
		},
		{
			name:      "invalid",
			updatedAt: "not-a-date",
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &compactIssue{
				Issue: beads.Issue{UpdatedAt: tc.updatedAt},
			}
			got, err := wispAge(w, now)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantAge {
				t.Errorf("wispAge = %v, want %v", got, tc.wantAge)
			}
		})
	}
}

func TestHasKeepLabel(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   bool
	}{
		{"no labels", nil, false},
		{"other labels", []string{"bug", "urgent"}, false},
		{"keep label", []string{"keep"}, true},
		{"gt:keep label", []string{"bug", "gt:keep"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &compactIssue{
				Issue: beads.Issue{Labels: tc.labels},
			}
			if got := hasKeepLabel(w); got != tc.want {
				t.Errorf("hasKeepLabel = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasComments(t *testing.T) {
	tests := []struct {
		name  string
		count int
		want  bool
	}{
		{"no comments", 0, false},
		{"has comments", 3, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &compactIssue{CommentCount: tc.count}
			if got := hasComments(w); got != tc.want {
				t.Errorf("hasComments = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsReferenced(t *testing.T) {
	tests := []struct {
		name    string
		depCnt  int
		deptCnt int
		want    bool
	}{
		{"no refs", 0, 0, false},
		{"has dependents", 0, 1, true},
		{"has dependencies", 1, 0, true},
		{"both", 2, 3, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &compactIssue{
				Issue: beads.Issue{
					DependencyCount: tc.depCnt,
					DependentCount:  tc.deptCnt,
				},
			}
			if got := isReferenced(w); got != tc.want {
				t.Errorf("isReferenced = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompactTruncate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"short ASCII", "short", 10, "short"},
		{"exact length", "exactly10!", 10, "exactly10!"},
		{"ASCII too long", "this is too long", 10, "this is..."},
		{"short maxLen", "ab", 3, "ab"},
		{"maxLen 3", "abcdef", 3, "abc"},
		// Multi-byte UTF-8: emoji is 1 rune, not 4 bytes
		{"emoji within limit", "🤝 HANDOFF", 10, "🤝 HANDOFF"},
		{"emoji truncated", "🤝 HANDOFF: Routine cycle for witness", 15, "🤝 HANDOFF: R..."},
		// CJK characters: each is 1 rune, 3 bytes
		{"CJK within limit", "日本語テスト", 10, "日本語テスト"},
		{"CJK truncated", "日本語テストデータ", 6, "日本語..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := compactTruncate(tc.s, tc.maxLen); got != tc.want {
				t.Errorf("compactTruncate(%q, %d) = %q, want %q", tc.s, tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestExtractJSONArray(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			"clean JSON array",
			`[{"id":"test"}]`,
			`[{"id":"test"}]`,
		},
		{
			"warning prefix before JSON",
			"Warning: no route found for prefix \"gt-\"\n[{\"id\":\"test\"}]",
			`[{"id":"test"}]`,
		},
		{
			"unicode warning prefix",
			"⚠ Warning: something with 🤝 emoji\n[{\"id\":\"test\"}]",
			`[{"id":"test"}]`,
		},
		{
			"no array in data",
			"just some text without json",
			"just some text without json",
		},
		{
			"empty data",
			"",
			"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(extractJSONArray([]byte(tc.data)))
			if got != tc.want {
				t.Errorf("extractJSONArray(%q) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

func TestLoadTTLConfigDefaults(t *testing.T) {
	// With empty town root, should return defaults
	ttls := loadTTLConfig("", "")

	if ttls["heartbeat"] != 6*time.Hour {
		t.Errorf("heartbeat TTL = %v, want 6h", ttls["heartbeat"])
	}
	if ttls["patrol"] != 24*time.Hour {
		t.Errorf("patrol TTL = %v, want 24h", ttls["patrol"])
	}
	if ttls["error"] != 7*24*time.Hour {
		t.Errorf("error TTL = %v, want 168h", ttls["error"])
	}
}

func TestLoadTTLConfigWithRoleDefaults(t *testing.T) {
	// With empty town root, should return hardcoded defaults
	ttls := loadTTLConfigWithRole("", "")

	for k, want := range defaultTTLs {
		if got := ttls[k]; got != want {
			t.Errorf("loadTTLConfigWithRole TTLs[%q] = %v, want %v", k, got, want)
		}
	}
}

func TestLoadTTLConfigWithRoleSkipsInvalidPaths(t *testing.T) {
	// With nonexistent paths, rig bead lookup should gracefully skip
	ttls := loadTTLConfigWithRole("/nonexistent/town", "myrig")

	// Should still have defaults even though lookups failed
	if ttls["patrol"] != defaultTTLs["patrol"] {
		t.Errorf("patrol TTL = %v, want %v", ttls["patrol"], defaultTTLs["patrol"])
	}
	if ttls["error"] != defaultTTLs["error"] {
		t.Errorf("error TTL = %v, want %v", ttls["error"], defaultTTLs["error"])
	}
}

func TestCleanOrphanedWispDepsUsesTypedTargets(t *testing.T) {
	data, err := os.ReadFile("compact.go")
	if err != nil {
		t.Fatalf("read compact.go: %v", err)
	}
	body := compactSourceBetween(t, string(data), "func cleanOrphanedWispDeps(", "// listWisps")
	if strings.Contains(body, "depends_on_id") {
		t.Fatalf("cleanOrphanedWispDeps should not use legacy depends_on_id:\n%s", body)
	}
	for _, want := range []string{
		"depends_on_wisp_id IS NOT NULL AND NOT EXISTS",
		"wisps WHERE id = wisp_dependencies.depends_on_wisp_id",
		"depends_on_issue_id IS NOT NULL AND NOT EXISTS",
		"issues WHERE id = wisp_dependencies.depends_on_issue_id",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("cleanOrphanedWispDeps missing %q:\n%s", want, body)
		}
	}
}

func compactSourceBetween(t *testing.T, source, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(source, startMarker)
	if start == -1 {
		t.Fatalf("could not find %q", startMarker)
	}
	end := strings.Index(source[start:], endMarker)
	if end == -1 {
		t.Fatalf("could not find %q after %q", endMarker, startMarker)
	}
	return source[start : start+end]
}

// writeTestTown creates a town root with a rigs.json listing the given rigs,
// plus a .beads directory per rig so redirect resolution has something to land
// on. Returns the town root.
func writeTestTown(t *testing.T, rigNames ...string) string {
	t.Helper()

	townRoot := t.TempDir()
	rigs := make(map[string]config.RigEntry, len(rigNames))
	for _, name := range rigNames {
		rigs[name] = config.RigEntry{GitURL: "https://example.invalid/" + name + ".git"}
		if err := os.MkdirAll(filepath.Join(townRoot, name, ".beads"), 0o755); err != nil {
			t.Fatalf("creating rig %s: %v", name, err)
		}
	}
	if err := config.SaveRigsConfig(constants.MayorRigsPath(townRoot), &config.RigsConfig{Version: 1, Rigs: rigs}); err != nil {
		t.Fatalf("saving rigs config: %v", err)
	}
	return townRoot
}

// An unknown --rig used to be accepted silently and report "0 wisps scanned",
// making a wrong scope indistinguishable from an empty store (gt-kei9).
func TestResolveCompactScopeRejectsUnknownRig(t *testing.T) {
	t.Setenv("GT_RIG", "")
	townRoot := writeTestTown(t, "gastown", "liveop")

	for _, name := range []string{"nonsense", "gt", "town"} {
		t.Run(name, func(t *testing.T) {
			scope, err := resolveCompactScope(townRoot, townRoot, name)
			if err == nil {
				t.Fatalf("resolveCompactScope(%q) = %+v, want error", name, scope)
			}
			// The error must name the rejected value and the rigs that do exist,
			// so the caller can act on it without a second command.
			for _, want := range []string{name, "gastown", "liveop"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestResolveCompactScopeAcceptsKnownRig(t *testing.T) {
	t.Setenv("GT_RIG", "")
	townRoot := writeTestTown(t, "gastown", "liveop")

	scope, err := resolveCompactScope(townRoot, townRoot, "liveop")
	if err != nil {
		t.Fatalf("resolveCompactScope(liveop): %v", err)
	}
	if scope.Name != "liveop" {
		t.Errorf("scope.Name = %q, want liveop", scope.Name)
	}
	if scope.Source != "--rig" {
		t.Errorf("scope.Source = %q, want --rig", scope.Source)
	}
	// The scan must be scoped to the named rig's store, not to the working
	// directory. Before this, --rig only selected TTLs.
	wantBeads := filepath.Join(townRoot, "liveop", ".beads")
	if scope.BeadsDir != wantBeads {
		t.Errorf("scope.BeadsDir = %q, want %q", scope.BeadsDir, wantBeads)
	}
	if scope.WorkDir != filepath.Join(townRoot, "liveop") {
		t.Errorf("scope.WorkDir = %q, want the rig path", scope.WorkDir)
	}
}

// GT_RIG reaches the same code path as --rig, so it gets the same validation
// and the error has to say which one supplied the bad name.
func TestResolveCompactScopeValidatesGTRig(t *testing.T) {
	townRoot := writeTestTown(t, "gastown")
	t.Setenv("GT_RIG", "nonsense")

	_, err := resolveCompactScope(townRoot, townRoot, "")
	if err == nil {
		t.Fatal("resolveCompactScope with GT_RIG=nonsense returned nil error")
	}
	if !strings.Contains(err.Error(), "GT_RIG") {
		t.Errorf("error %q does not name GT_RIG as the source", err)
	}
}

func TestResolveCompactScopeFallsBackToCwd(t *testing.T) {
	t.Setenv("GT_RIG", "")
	townRoot := writeTestTown(t, "gastown")
	workDir := filepath.Join(townRoot, "gastown")

	scope, err := resolveCompactScope(workDir, townRoot, "")
	if err != nil {
		t.Fatalf("resolveCompactScope with no rig: %v", err)
	}
	if scope.Name != "cwd" || scope.Source != "cwd" {
		t.Errorf("scope = {Name:%q Source:%q}, want both \"cwd\"", scope.Name, scope.Source)
	}
	if scope.WorkDir != workDir {
		t.Errorf("scope.WorkDir = %q, want %q", scope.WorkDir, workDir)
	}
}

// Outside a workspace there is no rigs.json to validate against, so a rig name
// must fail rather than be accepted unchecked.
func TestResolveCompactScopeRequiresTownRootForRig(t *testing.T) {
	t.Setenv("GT_RIG", "")

	_, err := resolveCompactScope(t.TempDir(), "", "gastown")
	if err == nil {
		t.Fatal("resolveCompactScope with no town root returned nil error")
	}
}

// The scan query omits --include-infra, so it cannot see wisps. The shortfall
// probe must use the flag, or it measures the same blind spot twice and
// reports a reassuring zero.
func TestCountHiddenWispsQueryIncludesInfra(t *testing.T) {
	data, err := os.ReadFile("compact.go")
	if err != nil {
		t.Fatalf("read compact.go: %v", err)
	}
	src := string(data)

	idx := strings.Index(src, "func countHiddenWisps(")
	if idx < 0 {
		t.Fatal("countHiddenWisps not found in compact.go")
	}
	end := strings.Index(src[idx:], "\n}\n")
	if end < 0 {
		t.Fatal("could not delimit countHiddenWisps body")
	}
	body := src[idx : idx+end]
	if !strings.Contains(body, `"--include-infra"`) {
		t.Error("countHiddenWisps does not pass --include-infra; it would report the same zero the scan does")
	}
}

func TestHasPatrolReport(t *testing.T) {
	// `gt patrol report` writes "Patrol report: <summary>\n\n<step audit>" into
	// the wisp description and then closes the wisp. Nothing else keeps the
	// step audit, so this arm of the proven-value predicate is the only thing
	// standing between that record and deleteWisp.
	tests := []struct {
		name string
		desc string
		want bool
	}{
		{"empty", "", false},
		{"patrol report", "Patrol report: routine cycle\n\nheartbeat: OK", true},
		{"leading whitespace", "\n  Patrol report: cycle 12", true},
		{"rendered formula text", "# mol-deacon-patrol\n\nStep 1: heartbeat", false},
		{"mentions the phrase later", "Cycle notes\n\nPatrol report: not at the head", false},
		{"different prefix", "Patrol summary: routine cycle", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &compactIssue{Issue: beads.Issue{Description: tc.desc}}
			if got := hasPatrolReport(w); got != tc.want {
				t.Errorf("hasPatrolReport(%q) = %v, want %v", tc.desc, got, tc.want)
			}
		})
	}
}

// TestPatrolReportPrefixMatchesWriter pins the reader against the writer. If
// patrol_report.go's format string changes, the guard stops matching and the
// records it protects become deletable again, silently — the shape gastown-mq9
// exists to prevent.
func TestPatrolReportPrefixMatchesWriter(t *testing.T) {
	written := fmt.Sprintf("Patrol report: %s\n\n%s", "summary text", "step audit")
	w := &compactIssue{Issue: beads.Issue{Description: written}}
	if !hasPatrolReport(w) {
		t.Fatalf("hasPatrolReport rejected the exact description patrol_report.go writes: %q", written)
	}
}

// TestCompactVerdict exercises the decision path itself, including the case
// gastown-mq9 is about: a closed patrol wisp, long past TTL, carrying no
// comments, no keep label and no dependencies — whose description is the only
// surviving copy of that patrol cycle's summary and step audit.
func TestCompactVerdict(t *testing.T) {
	const ttl = 24 * time.Hour
	patrolDesc := "Patrol report: routine cycle\n\nheartbeat: OK | inbox-check: OK"

	tests := []struct {
		name       string
		w          *compactIssue
		age        time.Duration
		want       compactVerdictKind
		wantReason string
	}{
		{
			name:       "closed patrol wisp past TTL is never deleted",
			w:          &compactIssue{Issue: beads.Issue{Status: "closed", Description: patrolDesc}},
			age:        30 * 24 * time.Hour,
			want:       verdictPromote,
			wantReason: "proven value",
		},
		{
			name: "closed patrol wisp past TTL survives even with zero metadata",
			w: &compactIssue{
				Issue:        beads.Issue{Status: "closed", Description: patrolDesc},
				CommentCount: 0,
			},
			age:        365 * 24 * time.Hour,
			want:       verdictPromote,
			wantReason: "proven value",
		},
		{
			name:       "closed wisp past TTL with no record is deleted",
			w:          &compactIssue{Issue: beads.Issue{Status: "closed", Description: "heartbeat"}},
			age:        30 * 24 * time.Hour,
			want:       verdictDelete,
			wantReason: "TTL expired",
		},
		{
			name:       "closed wisp within TTL is skipped",
			w:          &compactIssue{Issue: beads.Issue{Status: "closed"}},
			age:        time.Hour,
			want:       verdictSkip,
			wantReason: "within TTL",
		},
		{
			name:       "commented wisp is promoted regardless of age",
			w:          &compactIssue{Issue: beads.Issue{Status: "closed"}, CommentCount: 2},
			age:        time.Hour,
			want:       verdictPromote,
			wantReason: "proven value",
		},
		{
			name:       "keep label is promoted",
			w:          &compactIssue{Issue: beads.Issue{Status: "closed", Labels: []string{"gt:keep"}}},
			age:        30 * 24 * time.Hour,
			want:       verdictPromote,
			wantReason: "proven value",
		},
		{
			name:       "molecule step past TTL is deleted, not promoted",
			w:          &compactIssue{Issue: beads.Issue{Status: "open", Parent: "gt-wisp-root"}, CommentCount: 5},
			age:        30 * 24 * time.Hour,
			want:       verdictDelete,
			wantReason: "molecule step past TTL",
		},
		{
			name:       "stuck in_progress past TTL is promoted",
			w:          &compactIssue{Issue: beads.Issue{Status: "in_progress"}},
			age:        30 * 24 * time.Hour,
			want:       verdictPromote,
			wantReason: "stuck in_progress past TTL",
		},
		{
			name:       "open past TTL is promoted",
			w:          &compactIssue{Issue: beads.Issue{Status: "open"}},
			age:        30 * 24 * time.Hour,
			want:       verdictPromote,
			wantReason: "open past TTL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := compactVerdict(tc.w, tc.age, ttl)
			if got != tc.want || reason != tc.wantReason {
				t.Errorf("compactVerdict = (%v, %q), want (%v, %q)", got, reason, tc.want, tc.wantReason)
			}
		})
	}
}
