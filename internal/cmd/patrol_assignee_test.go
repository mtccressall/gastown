package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// patrolMintingSites are the three commands that create a HOOKED patrol wisp:
//
//	gt patrol new     recovery, run when a patrol is lost
//	gt patrol report  routine, run at the end of every cycle by every role
//	gt prime          session start, auto-bonds when no patrol is active
//
// All three write PatrolConfig.Assignee onto the wisp and read that same field
// back through findActivePatrol, so they must agree with each other AND with
// buildAgentIdentity, which is what gt hook queries with.
var patrolMintingSites = []string{
	"patrol_new.go",
	"patrol_report.go",
	"prime_molecule.go",
}

// TestPatrolConfigRejectsEmptyRig is the regression test for gt-s4pw.
//
// buildAgentIdentity CONCATENATES for the rig-scoped roles, so an empty Rig
// yields "/witness" and "/refinery" -- non-empty, so an `identity == ""` check
// waves them straight through, and permanently hooked to an address no agent
// can hold. This fired on 2026-09-02, minting gt-wisp-8etdh ("/refinery") and
// gt-wisp-ejd8r ("/witness") two seconds apart; both are still hooked and
// unworkable. Refusing to mint is the only outcome an agent can recover from.
func TestPatrolConfigRejectsEmptyRig(t *testing.T) {
	for _, role := range []Role{RoleWitness, RoleRefinery} {
		t.Run(string(role), func(t *testing.T) {
			cfg, err := patrolConfigForRole(string(role), RoleInfo{Rig: "", TownRoot: t.TempDir()})
			if err == nil {
				t.Fatalf("patrolConfigForRole(%q) with an empty rig returned assignee %q and no error; "+
					"that wisp is hooked to an address no agent holds (gt-s4pw)", role, cfg.Assignee)
			}
			if strings.HasPrefix(cfg.Assignee, "/") {
				t.Errorf("assignee %q leaked out of the guard", cfg.Assignee)
			}
		})
	}
}

// TestNoPatrolMintingSiteWritesAssigneeLiteral is the structural half, and it
// is the half that would have caught gt-7rne surviving PR #15.
//
// The behavioural test above can only see the assignee of a config it was
// handed; it is blind to a SECOND construction site that never calls
// patrolConfigForRole. That is exactly what happened: patrol_new.go was fixed
// and tested while patrol_report.go and prime_molecule.go kept building
// PatrolConfig by hand, and the test suite went green.
//
// So assert the property no single-site test can: outside patrolConfigForRole,
// nothing assigns PatrolConfig.Assignee at all.
func TestNoPatrolMintingSiteWritesAssigneeLiteral(t *testing.T) {
	fset := token.NewFileSet()

	for _, file := range patrolMintingSites {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}

		// Locate patrolConfigForRole so its own (correct) assignments are exempt.
		var allowed []*ast.FuncDecl
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "patrolConfigForRole" {
				allowed = append(allowed, fn)
			}
		}
		inAllowed := func(pos token.Pos) bool {
			for _, fn := range allowed {
				if pos >= fn.Pos() && pos <= fn.End() {
					return true
				}
			}
			return false
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			// Assignee: "..." inside a PatrolConfig{} literal.
			case *ast.KeyValueExpr:
				key, ok := node.Key.(*ast.Ident)
				if ok && key.Name == "Assignee" && !inAllowed(node.Pos()) {
					t.Errorf("%s:%d sets PatrolConfig.Assignee outside patrolConfigForRole. "+
						"Every patrol minting path must derive the assignee from buildAgentIdentity, "+
						"which is the address gt hook queries; restating it here is gt-7rne.",
						file, fset.Position(node.Pos()).Line)
				}
			// cfg.Assignee = ...
			case *ast.AssignStmt:
				for _, lhs := range node.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if ok && sel.Sel.Name == "Assignee" && !inAllowed(node.Pos()) {
						t.Errorf("%s:%d assigns .Assignee outside patrolConfigForRole (gt-7rne)",
							file, fset.Position(node.Pos()).Line)
					}
				}
			}
			return true
		})
	}
}

// TestPatrolMintingSitesUseSharedBuilder is the positive control for the test
// above: a scan that matched nothing and a scan that found nothing return the
// same zero. If a file is renamed, the AST walk above passes vacuously.
func TestPatrolMintingSitesUseSharedBuilder(t *testing.T) {
	fset := token.NewFileSet()

	for _, file := range patrolMintingSites {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v (is %s still a patrol minting site?)", file, err, file)
		}

		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "patrolConfigForRole" {
				found = true
			}
			return true
		})

		if !found {
			t.Errorf("%s never calls patrolConfigForRole; either it stopped minting patrols "+
				"(remove it from patrolMintingSites) or it builds the assignee itself (gt-7rne)", file)
		}
	}
}
