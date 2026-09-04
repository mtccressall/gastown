package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExtractPatrolRole(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		expected string
	}{
		{
			name:     "deacon patrol",
			title:    "Digest: mol-deacon-patrol",
			expected: "deacon",
		},
		{
			name:     "witness patrol",
			title:    "Digest: mol-witness-patrol",
			expected: "witness",
		},
		{
			name:     "refinery patrol",
			title:    "Digest: mol-refinery-patrol",
			expected: "refinery",
		},
		{
			name:     "wisp digest without patrol suffix",
			title:    "Digest: gt-wisp-abc123",
			expected: "patrol",
		},
		{
			name:     "random title",
			title:    "Some other digest",
			expected: "patrol",
		},
		{
			name:     "empty title",
			title:    "",
			expected: "patrol",
		},
		{
			name:     "just digest prefix",
			title:    "Digest: ",
			expected: "patrol",
		},
		{
			name:     "mol prefix but no patrol suffix",
			title:    "Digest: mol-deacon-other",
			expected: "patrol",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPatrolRole(tt.title)
			if got != tt.expected {
				t.Errorf("extractPatrolRole(%q) = %q, want %q", tt.title, got, tt.expected)
			}
		})
	}
}

func TestPatrolDigestDateFormat(t *testing.T) {
	// Test that PatrolDigest.Date format is YYYY-MM-DD
	digest := PatrolDigest{
		Date:        "2026-01-17",
		TotalCycles: 5,
		ByRole:      map[string]int{"deacon": 2, "witness": 3},
	}

	if digest.Date != "2026-01-17" {
		t.Errorf("Date format incorrect: got %q", digest.Date)
	}

	if digest.TotalCycles != 5 {
		t.Errorf("TotalCycles: got %d, want 5", digest.TotalCycles)
	}

	if digest.ByRole["deacon"] != 2 {
		t.Errorf("ByRole[deacon]: got %d, want 2", digest.ByRole["deacon"])
	}
}

func TestPatrolCycleEntry(t *testing.T) {
	entry := PatrolCycleEntry{
		ID:          "gt-abc123",
		Role:        "deacon",
		Title:       "Digest: mol-deacon-patrol",
		Description: "Test description",
	}

	if entry.ID != "gt-abc123" {
		t.Errorf("ID: got %q, want %q", entry.ID, "gt-abc123")
	}

	if entry.Role != "deacon" {
		t.Errorf("Role: got %q, want %q", entry.Role, "deacon")
	}
}

func TestParseStepResults(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expected      map[string]string
		wantMalformed []string
	}{
		{
			name:     "empty input",
			input:    "",
			expected: map[string]string{},
		},
		{
			name:  "single step",
			input: "heartbeat:OK",
			expected: map[string]string{
				"heartbeat": "OK",
			},
		},
		{
			name:  "multiple steps",
			input: "heartbeat:OK,inbox-check:OK,orphan-cleanup:SKIP",
			expected: map[string]string{
				"heartbeat":      "OK",
				"inbox-check":    "OK",
				"orphan-cleanup": "SKIP",
			},
		},
		{
			name:  "mixed case normalized to upper",
			input: "heartbeat:ok,inbox-check:Skip",
			expected: map[string]string{
				"heartbeat":   "OK",
				"inbox-check": "SKIP",
			},
		},
		{
			name:  "whitespace trimmed",
			input: " heartbeat : OK , inbox-check : OK ",
			expected: map[string]string{
				"heartbeat":   "OK",
				"inbox-check": "OK",
			},
		},
		{
			name:  "trailing comma ignored",
			input: "heartbeat:OK,",
			expected: map[string]string{
				"heartbeat": "OK",
			},
		},
		{
			name:  "entry without a status is reported as malformed",
			input: "heartbeat:OK,inbox-check",
			expected: map[string]string{
				"heartbeat": "OK",
			},
			wantMalformed: []string{"inbox-check"},
		},
		{
			name:  "empty status is reported as malformed, not recorded blank",
			input: "heartbeat:OK,inbox-check:",
			expected: map[string]string{
				"heartbeat": "OK",
			},
			wantMalformed: []string{"inbox-check:"},
		},
		{
			name:  "empty step id is reported as malformed, not an unrecognized empty id",
			input: "heartbeat:OK,:OK",
			expected: map[string]string{
				"heartbeat": "OK",
			},
			wantMalformed: []string{":OK"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, malformed := parseStepResults(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("parseStepResults(%q) returned %d entries, want %d", tt.input, len(got), len(tt.expected))
				return
			}
			for k, v := range tt.expected {
				if got[k] != v {
					t.Errorf("parseStepResults(%q)[%q] = %q, want %q", tt.input, k, got[k], v)
				}
			}
			if !slices.Equal(malformed, tt.wantMalformed) {
				t.Errorf("parseStepResults(%q) malformed = %v, want %v", tt.input, malformed, tt.wantMalformed)
			}
		})
	}
}

func TestBuildStepAudit(t *testing.T) {
	tests := []struct {
		name        string
		formulaName string
		stepsFlag   string
		wantPrefix  string // check prefix of output
		wantSuffix  string // check suffix of output
		wantContain string // check output contains this
	}{
		{
			name:        "deacon patrol with no steps reported",
			formulaName: "mol-deacon-patrol",
			stepsFlag:   "",
			wantPrefix:  "Steps: NOT REPORTED",
			wantContain: "/28)",
		},
		{
			name:        "deacon patrol with all steps OK",
			formulaName: "mol-deacon-patrol",
			stepsFlag:   "heartbeat:OK,inbox-check:OK,orphan-process-cleanup:OK,test-pollution-cleanup:OK,gate-evaluation:OK,dispatch-gated-molecules:OK,check-convoy-completion:OK,resolve-external-deps:OK,fire-notifications:OK,heartbeat-mid:OK,health-scan:OK,dolt-health:OK,zombie-scan:OK,plugin-run:OK,dog-pool-maintenance:OK,dog-health-check:OK,orphan-check:OK,session-gc:OK,wisp-compact:OK,compact-report:OK,costs-digest:OK,observed-vs-concluded:OK,patrol-digest:OK,log-maintenance:OK,patrol-cleanup:OK,context-check:OK,dispatch-ready-work:OK,loop-or-exit:OK",
			wantPrefix:  "Steps:",
			wantSuffix:  "(28/28)",
			wantContain: "heartbeat OK",
		},
		{
			name:        "deacon patrol with some steps skipped",
			formulaName: "mol-deacon-patrol",
			stepsFlag:   "heartbeat:OK,inbox-check:OK,loop-or-exit:OK",
			wantPrefix:  "Steps:",
			wantSuffix:  "(3/28)",
			wantContain: "heartbeat OK",
		},
		{
			name:        "skipped steps shown as SKIP",
			formulaName: "mol-deacon-patrol",
			stepsFlag:   "heartbeat:OK",
			wantContain: "inbox-check SKIP",
		},
		{
			name:        "unrecognized id named in the audit line, not silently dropped",
			formulaName: "mol-deacon-patrol",
			stepsFlag:   "heartbeat:OK,not-a-real-step:OK",
			wantContain: "[1 unrecognized: not-a-real-step]",
		},
		{
			name:        "unrecognized ids do not count toward the score",
			formulaName: "mol-deacon-patrol",
			stepsFlag:   "not-a-real-step:OK,also-not-real:OK",
			wantContain: "(0/28)",
		},
		{
			name:        "steps added to the live formula are graded, not dropped",
			formulaName: "mol-deacon-patrol",
			stepsFlag:   "dispatch-ready-work:OK,observed-vs-concluded:OK",
			wantSuffix:  "(2/28)",
			wantContain: "dispatch-ready-work OK",
		},
		{
			name:        "malformed entry named in the audit line, not only on stderr",
			formulaName: "mol-deacon-patrol",
			stepsFlag:   "heartbeat:OK,inbox-check",
			wantContain: "[1 malformed: inbox-check]",
		},
		{
			name:        "nonexistent formula with no steps",
			formulaName: "mol-nonexistent",
			stepsFlag:   "",
			wantPrefix:  "Steps: NOT REPORTED (formula not found)",
		},
		{
			name:        "nonexistent formula with steps",
			formulaName: "mol-nonexistent",
			stepsFlag:   "heartbeat:OK",
			wantContain: "unvalidated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Empty townRoot/rigName keeps the test hermetic: ResolveFormulaContent
			// skips both override tiers and falls through to the embedded copy.
			got := buildStepAudit(tt.formulaName, "", "", tt.stepsFlag)
			if tt.wantPrefix != "" && !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("buildStepAudit() = %q, want prefix %q", got, tt.wantPrefix)
			}
			if tt.wantSuffix != "" && !strings.HasSuffix(got, tt.wantSuffix) {
				t.Errorf("buildStepAudit() = %q, want suffix %q", got, tt.wantSuffix)
			}
			if tt.wantContain != "" && !strings.Contains(got, tt.wantContain) {
				t.Errorf("buildStepAudit() = %q, want to contain %q", got, tt.wantContain)
			}
		})
	}
}

// TestBuildStepAuditUsesResolvedFormula pins the root of gastown-c1x: the audit
// must grade against the formula the patrol was RENDERED from, not the embedded
// fallback. gt prime resolves rig override, then town override, then embedded
// (formula.ResolveFormulaContent); buildStepAudit used to read only the embedded
// copy, so any step a town added to its own formula was reported by the agent,
// discarded by the audit, and scored as if it had never been claimed.
//
// This is the property, not the instance: it fails for ANY town-only step, so it
// does not go quiet the moment the embedded copy is synced.
func TestBuildStepAuditUsesResolvedFormula(t *testing.T) {
	townRoot := t.TempDir()
	formulaDir := filepath.Join(townRoot, ".beads", "formulas")
	if err := os.MkdirAll(formulaDir, 0o755); err != nil {
		t.Fatalf("creating town formula dir: %v", err)
	}
	override := `formula = "mol-deacon-patrol"
description = "town override"
type = "workflow"

[[steps]]
id = "heartbeat"
title = "Heartbeat"
description = "beat"

[[steps]]
id = "town-only-step"
title = "A step only this town defines"
needs = ["heartbeat"]
description = "the embedded copy has never heard of this"
`
	path := filepath.Join(formulaDir, "mol-deacon-patrol.formula.toml")
	if err := os.WriteFile(path, []byte(override), 0o644); err != nil {
		t.Fatalf("writing town formula: %v", err)
	}

	got := buildStepAudit("mol-deacon-patrol", townRoot, "", "heartbeat:OK,town-only-step:OK")

	if !strings.Contains(got, "town-only-step OK") {
		t.Errorf("buildStepAudit() = %q, want the town-only step graded", got)
	}
	if !strings.HasSuffix(got, "(2/2)") {
		t.Errorf("buildStepAudit() = %q, want suffix %q — the town formula's 2 steps, not the embedded copy's", got, "(2/2)")
	}
	if strings.Contains(got, "unrecognized") {
		t.Errorf("buildStepAudit() = %q, want no unrecognized ids: a town-only step is a real step", got)
	}
}
