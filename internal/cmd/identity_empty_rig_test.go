package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildAgentIdentityRejectsEmptyComponents is the regression test for
// gt-s4pw at the function that actually produced the bad address.
//
// patrol_assignee_test.go already pins the three PATROL minting paths. It
// cannot see this, because it exercises patrolConfigForRole -- the one caller
// that had a hand-written guard. buildAgentIdentity has twelve production call
// sites, and the two hottest (gt mail send with no explicit recipient, and
// every gt handoff) are nowhere near patrol. So a green patrol suite was never
// evidence about this function; it only ever covered the path that already
// worked.
//
// The trap the concatenation set: an empty Rig yields "/witness", "/refinery",
// "/polecats/nux" -- NON-EMPTY and well formed, so every caller's `== ""`
// check passes and hands on an address no agent can ever hold. Ten of the
// twelve call sites were already checking, and all ten were checking an
// operand that could not be empty.
func TestBuildAgentIdentityRejectsEmptyComponents(t *testing.T) {
	cases := []struct {
		name string
		ctx  RoleContext
	}{
		{"witness/empty-rig", RoleContext{Role: RoleWitness}},
		{"refinery/empty-rig", RoleContext{Role: RoleRefinery}},
		{"polecat/empty-rig", RoleContext{Role: RolePolecat, Polecat: "nux"}},
		{"polecat/empty-name", RoleContext{Role: RolePolecat, Rig: "gastown"}},
		{"crew/empty-rig", RoleContext{Role: RoleCrew, Polecat: "jack"}},
		{"crew/empty-name", RoleContext{Role: RoleCrew, Rig: "gastown"}},
		{"dog/empty-name", RoleContext{Role: RoleDog}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildAgentIdentity(tc.ctx); got != "" {
				t.Fatalf("buildAgentIdentity(%+v) = %q, want \"\"; %q is non-empty, so every "+
					"caller's `== \"\"` guard passes it through as a real address (gt-s4pw)",
					tc.ctx, got, got)
			}
		})
	}
}

// TestBuildAgentIdentityWellFormed is the positive control. Without it the test
// above passes on a function that returns "" for everything.
func TestBuildAgentIdentityWellFormed(t *testing.T) {
	cases := []struct {
		ctx  RoleContext
		want string
	}{
		{RoleContext{Role: RoleMayor}, "mayor/"},
		{RoleContext{Role: RoleDeacon}, "deacon/"},
		{RoleContext{Role: RoleBoot}, "deacon/boot"},
		{RoleContext{Role: RoleWitness, Rig: "gastown"}, "gastown/witness"},
		{RoleContext{Role: RoleRefinery, Rig: "gastown"}, "gastown/refinery"},
		{RoleContext{Role: RolePolecat, Rig: "gastown", Polecat: "opal"}, "gastown/polecats/opal"},
		{RoleContext{Role: RoleCrew, Rig: "gastown", Polecat: "jack"}, "gastown/crew/jack"},
		{RoleContext{Role: RoleDog, Polecat: "boot"}, "deacon/dogs/boot"},
	}
	for _, tc := range cases {
		if got := buildAgentIdentity(tc.ctx); got != tc.want {
			t.Errorf("buildAgentIdentity(%+v) = %q, want %q", tc.ctx, got, tc.want)
		}
	}
}

// TestEveryBuildAgentIdentityCallSiteGuards pins the PROPERTY rather than the
// instances, so it catches the call site added next month.
//
// The value-based tests above are blind to a new caller that ignores "". This
// walks every production call of buildAgentIdentity and asserts the result is
// either checked against "" or returned to a caller that must check it.
func TestEveryBuildAgentIdentityCallSiteGuards(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	scanned, callSites := 0, 0

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if !strings.Contains(string(src), "buildAgentIdentity(") {
			continue
		}
		scanned++
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		// Walk every statement LIST, not just each function body's top level.
		// Most of these calls sit inside an if-block, so a FuncDecl-only walk
		// silently checks about a third of them and reports a healthy count.
		ast.Inspect(file, func(n ast.Node) bool {
			var list []ast.Stmt
			switch b := n.(type) {
			case *ast.BlockStmt:
				list = b.List
			case *ast.CaseClause:
				list = b.Body
			case *ast.CommClause:
				list = b.Body
			default:
				return true
			}
			for i, stmt := range list {
				assigned := identityAssignedBy(stmt)
				if assigned == "" {
					// A bare `return buildAgentIdentity(ctx)` hands the empty
					// string to the caller, which is the guarded contract.
					continue
				}
				callSites++
				if i+1 >= len(list) || !guardsEmpty(list[i+1], assigned) {
					pos := fset.Position(stmt.Pos())
					t.Errorf("%s: %s = buildAgentIdentity(...) is not followed by a check "+
						"for the empty string. buildAgentIdentity returns \"\" for a rig-scoped "+
						"role with no rig (gt-s4pw); an unguarded identity is used as an "+
						"assignee that matches nothing, or as an unscoped query that matches "+
						"everything.", pos, assigned)
				}
			}
			return true
		})
	}

	// Assert the scan found something. A rename would otherwise make this pass
	// vacuously, which is the failure mode this town keeps recording.
	if scanned == 0 || callSites == 0 {
		t.Fatalf("scanned %d files and found %d assigned call sites of buildAgentIdentity; "+
			"expected several. The test matched nothing and proved nothing.", scanned, callSites)
	}
	t.Logf("scanned %d files, checked %d assigned call sites", scanned, callSites)
}

// identityAssignedBy returns the identifier a statement assigns
// buildAgentIdentity's result to, or "" if the statement is not such an
// assignment.
func identityAssignedBy(stmt ast.Stmt) string {
	as, ok := stmt.(*ast.AssignStmt)
	if !ok || len(as.Rhs) != 1 || len(as.Lhs) != 1 {
		return ""
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok {
		return ""
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "buildAgentIdentity" {
		return ""
	}
	lhs, ok := as.Lhs[0].(*ast.Ident)
	if !ok {
		return ""
	}
	return lhs.Name
}

// guardsEmpty reports whether stmt is `if <name> == "" { ... }`.
func guardsEmpty(stmt ast.Stmt, name string) bool {
	ifs, ok := stmt.(*ast.IfStmt)
	if !ok {
		return false
	}
	bin, ok := ifs.Cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return false
	}
	x, ok := bin.X.(*ast.Ident)
	if !ok || x.Name != name {
		return false
	}
	lit, ok := bin.Y.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING && (lit.Value == `""` || lit.Value == "``")
}
