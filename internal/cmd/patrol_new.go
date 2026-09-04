package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/constants"
)

var patrolNewRole string

var patrolNewCmd = &cobra.Command{
	Use:   "new",
	Short: "Create a new patrol wisp with config variables",
	Long: `Create a new patrol wisp for the current role, injecting rig config
variables so the formula has correct settings baked in.

Role is auto-detected from GT_ROLE (set by the daemon). Use --role to override.

For refinery patrols, MQ config variables (run_tests, test_command,
target_branch, etc.) are read from the rig's config.json and settings/config.json and
passed as --var args to the wisp.

Examples:
  gt patrol new                  # Auto-detect role, create patrol
  gt patrol new --role refinery  # Explicitly create refinery patrol`,
	RunE: runPatrolNew,
}

func init() {
	patrolNewCmd.Flags().StringVar(&patrolNewRole, "role", "", "Role override (deacon, witness, refinery)")
}

func runPatrolNew(cmd *cobra.Command, args []string) error {
	// Resolve role
	roleInfo, err := GetRole()
	if err != nil {
		return fmt.Errorf("detecting role: %w", err)
	}

	// Allow --role flag to override; otherwise use the already-parsed role
	// (GetRole already handles GT_ROLE env var internally)
	roleName := string(roleInfo.Role)
	if patrolNewRole != "" {
		roleName = patrolNewRole
	}

	// Build config based on role
	cfg, err := patrolConfigForRole(roleName, roleInfo)
	if err != nil {
		return err
	}

	// Create and hook the wisp
	patrolID, err := autoSpawnPatrol(cfg)
	if err != nil {
		if patrolID != "" {
			// Created but failed to hook
			fmt.Fprintf(os.Stderr, "warning: %s\n", err.Error())
			fmt.Println(patrolID)
			return nil
		}
		return err
	}

	fmt.Println(patrolID)
	return nil
}

// patrolConfigForRole builds the PatrolConfig for a patrol-capable role.
//
// Assignee MUST come from buildAgentIdentity, which is the same function
// gt hook (gt mol status) uses to construct the address it queries. Writing
// the assignee as a separate literal here is what caused gt-7rne: this
// function wrote the Deacon's assignee as "deacon" while buildAgentIdentity
// queried "deacon/", and listAssignedActiveWork matches the assignee exactly,
// with no normalization anywhere on the path. The result was a patrol wisp
// created in HOOKED state that gt hook could never see, so the Deacon read
// its own hook as empty and stood down.
//
// Deriving the written address from the read address makes that drift
// impossible rather than merely corrected. Do not reintroduce a literal here.
func patrolConfigForRole(roleName string, roleInfo RoleInfo) (PatrolConfig, error) {
	role := Role(roleName)

	switch role {
	case RoleDeacon, RoleWitness, RoleRefinery:
	default:
		return PatrolConfig{}, fmt.Errorf("unsupported role for patrol: %q (expected deacon, witness, or refinery)", roleName)
	}

	// The address gt hook will query for this agent. Deriving it here rather
	// than restating it as a literal is the point of this function.
	identity := buildAgentIdentity(RoleInfo{Role: role, Rig: roleInfo.Rig})
	if identity == "" {
		return PatrolConfig{}, fmt.Errorf("cannot determine agent identity for patrol role %q", roleName)
	}

	switch role {
	case RoleDeacon:
		return PatrolConfig{
			RoleName:      "deacon",
			PatrolMolName: constants.MolDeaconPatrol,
			BeadsDir:      roleInfo.TownRoot,
			Assignee:      identity,
		}, nil
	case RoleWitness:
		return PatrolConfig{
			RoleName:      "witness",
			PatrolMolName: constants.MolWitnessPatrol,
			BeadsDir:      roleInfo.TownRoot,
			Assignee:      identity,
		}, nil
	case RoleRefinery:
		return PatrolConfig{
			RoleName:      "refinery",
			PatrolMolName: constants.MolRefineryPatrol,
			BeadsDir:      roleInfo.TownRoot,
			Assignee:      identity,
			ExtraVars:     buildRefineryPatrolVars(roleInfo),
		}, nil
	default:
		return PatrolConfig{}, fmt.Errorf("unsupported role for patrol: %q (expected deacon, witness, or refinery)", roleName)
	}
}
