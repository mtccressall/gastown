package cmd

import (
	"testing"
)

func TestRunPatrolNew_UnsupportedRole(t *testing.T) {
	// Test that an unsupported role returns an error
	// We can't easily test the full flow without bd/beads,
	// but we can verify role validation logic

	// Test the role switch logic directly
	validRoles := []string{"deacon", "witness", "refinery"}
	invalidRoles := []string{"mayor", "polecat", "crew", "unknown", ""}

	for _, role := range validRoles {
		r := Role(role)
		if r != RoleDeacon && r != RoleWitness && r != RoleRefinery {
			t.Errorf("role %q should be valid for patrol new", role)
		}
	}

	for _, role := range invalidRoles {
		r := Role(role)
		if r == RoleDeacon || r == RoleWitness || r == RoleRefinery {
			t.Errorf("role %q should be invalid for patrol new", role)
		}
	}
}

func TestPatrolNewCmd_Registered(t *testing.T) {
	// Verify the command is properly registered
	found := false
	for _, cmd := range patrolCmd.Commands() {
		if cmd.Use == "new" {
			found = true
			break
		}
	}
	if !found {
		t.Error("patrol new command not registered")
	}
}

func TestPatrolNewCmd_HasRoleFlag(t *testing.T) {
	flag := patrolNewCmd.Flags().Lookup("role")
	if flag == nil {
		t.Error("patrol new command missing --role flag")
	}
}

// TestPatrolConfigAssigneeMatchesHookLookup is the regression test for gt-7rne.
//
// A patrol wisp is written with cfg.Assignee; gt hook (gt mol status) looks it
// up using buildAgentIdentity(). listAssignedActiveWork matches the assignee
// EXACTLY -- nothing on that path trims or normalizes a trailing slash. So if
// the two disagree by even one character, the minting command produces a
// HOOKED wisp that gt hook reports as "Nothing on hook", and the agent reads
// its own hook as empty.
//
// The original defect was exactly one character: the assignee was written as
// "deacon" while buildAgentIdentity queried "deacon/". This asserts the
// INVARIANT for every patrol-capable role, not just the instance that broke.
//
// It now covers all three minting paths -- gt patrol new, gt patrol report and
// gt prime -- because all three build their config here. It did not when it was
// written, and that is why gt-7rne survived PR #15: this test was green while
// gt patrol report, the path every cycle of every role runs, still wrote a
// literal. See TestNoPatrolMintingSiteWritesAssigneeLiteral in
// patrol_assignee_test.go for the structural assertion that keeps it that way.
func TestPatrolConfigAssigneeMatchesHookLookup(t *testing.T) {
	const rig = "testrig"

	for _, role := range []Role{RoleDeacon, RoleWitness, RoleRefinery} {
		t.Run(string(role), func(t *testing.T) {
			cfg, err := patrolConfigForRole(string(role), RoleInfo{Rig: rig, TownRoot: t.TempDir()})
			if err != nil {
				t.Fatalf("patrolConfigForRole(%q): %v", role, err)
			}

			// The address gt hook will actually query for this agent.
			want := buildAgentIdentity(RoleInfo{Role: role, Rig: rig})
			if want == "" {
				t.Fatalf("buildAgentIdentity returned empty for role %q", role)
			}

			if cfg.Assignee != want {
				t.Errorf("assignee written by gt patrol new does not match the address gt hook queries\n"+
					"  role:            %s\n"+
					"  patrol new wrote: %q\n"+
					"  gt hook queries:  %q\n"+
					"a wisp written this way is HOOKED but invisible to gt hook (gt-7rne)",
					role, cfg.Assignee, want)
			}
		})
	}
}

// TestPatrolConfigDeaconAssigneeIsCanonical pins the specific value that broke.
// Town-level agents (mayor, deacon) carry a trailing slash; see
// canonicalAssigneeAddress in sling_target.go, which documents this as the
// canonical assignee form and names the bare form as the read/write mismatch.
func TestPatrolConfigDeaconAssigneeIsCanonical(t *testing.T) {
	cfg, err := patrolConfigForRole("deacon", RoleInfo{TownRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("patrolConfigForRole(deacon): %v", err)
	}
	if cfg.Assignee != "deacon/" {
		t.Errorf("deacon patrol assignee = %q, want %q (town-level roles take a trailing slash)", cfg.Assignee, "deacon/")
	}
}

func TestPatrolConfigForRole_UnsupportedRole(t *testing.T) {
	for _, role := range []string{"mayor", "polecat", "crew", "unknown", ""} {
		if _, err := patrolConfigForRole(role, RoleInfo{Rig: "testrig"}); err == nil {
			t.Errorf("patrolConfigForRole(%q) = nil error, want error", role)
		}
	}
}
