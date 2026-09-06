package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestSpawnPolecatForSling_ClearsRespawnCounterOnSuccess pins gt-iiih's second
// defect: the respawn counter was never cleared when a spawn SUCCEEDED, so a
// bead that demonstrably works stayed at the cap and its next dispatch was
// blocked. Measured 2026-09-06: liveop-vjq spawned successfully 34 seconds
// after its counter reached 3, and the counter was still 3 afterwards.
//
// This is an AST test rather than a behavioural one on purpose.
// SpawnPolecatForSling does rig-config, git and polecat-manager I/O that a unit
// test cannot reach, and the unit tests on ResetBeadRespawnCount itself passed
// both before and after the fix -- a green test beside a live defect, because
// they exercise the function the broken path never called.
//
// The property pinned is structural and is what actually makes the class
// unrepresentable: the reset must be DEFERRED, so that it covers every success
// return including one added later. There are currently two (idle-polecat reuse
// and fresh allocation); a per-return call would leave them free to drift and
// would not cover a third.
func TestSpawnPolecatForSling_ClearsRespawnCounterOnSuccess(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "polecat_spawn.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing polecat_spawn.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "SpawnPolecatForSling" {
			fn = fd
			break
		}
	}
	if fn == nil {
		t.Fatal("SpawnPolecatForSling not found; this test is vacuous — fix the test, not the code")
	}

	// Positive control on the population: the defer only earns its keep if there
	// really is more than one success return to cover. If this ever drops to one,
	// the comment above is stale and should be revisited rather than ignored.
	successReturns := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 2 {
			return true
		}
		// A success return yields a non-nil composite literal and a nil error.
		if unary, ok := ret.Results[0].(*ast.UnaryExpr); ok {
			if _, isComposite := unary.X.(*ast.CompositeLit); isComposite {
				successReturns++
			}
		}
		return true
	})
	if successReturns < 2 {
		t.Errorf("expected >=2 success returns in SpawnPolecatForSling, found %d; "+
			"if the function was refactored to a single exit, re-check that the deferred "+
			"reset is still the right shape", successReturns)
	}

	// The actual invariant: a deferred call that reaches ResetBeadRespawnCount.
	foundDeferredReset := false
	ast.Inspect(fn, func(n ast.Node) bool {
		def, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		ast.Inspect(def, func(inner ast.Node) bool {
			sel, ok := inner.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "ResetBeadRespawnCount" {
				foundDeferredReset = true
			}
			return true
		})
		return true
	})

	if !foundDeferredReset {
		t.Errorf("SpawnPolecatForSling has no DEFERRED call to ResetBeadRespawnCount.\n"+
			"A successful spawn must clear the respawn counter (gt-iiih), and it must do so "+
			"in a defer so that all %d success returns are covered by construction. "+
			"Adding the call before each return re-opens the drift this test exists to prevent.",
			successReturns)
	}
}

// TestSpawnPolecatForSling_ResetIsGuardedOnSuccess checks the deferred reset does
// not fire on the error paths. Clearing the counter after a FAILED spawn would
// disable the limiter entirely, which is the opposite defect and a worse one.
func TestSpawnPolecatForSling_ResetIsGuardedOnSuccess(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "polecat_spawn.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing polecat_spawn.go: %v", err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "SpawnPolecatForSling" {
			fn = fd
		}
	}
	if fn == nil {
		t.Fatal("SpawnPolecatForSling not found; this test is vacuous")
	}

	src := ""
	ast.Inspect(fn, func(n ast.Node) bool {
		if def, ok := n.(*ast.DeferStmt); ok {
			var sb strings.Builder
			ast.Inspect(def, func(inner ast.Node) bool {
				switch v := inner.(type) {
				case *ast.Ident:
					sb.WriteString(v.Name + " ")
				case *ast.SelectorExpr:
					sb.WriteString(v.Sel.Name + " ")
				}
				return true
			})
			if strings.Contains(sb.String(), "ResetBeadRespawnCount") {
				src = sb.String()
			}
		}
		return true
	})

	if src == "" {
		t.Fatal("no deferred ResetBeadRespawnCount found (covered by the sibling test)")
	}
	// The guard must reference the error result; otherwise it fires on failures too.
	if !strings.Contains(src, "err") {
		t.Errorf("deferred reset does not reference the error result, so it would clear the "+
			"counter on FAILED spawns and disable the limiter. Guard body idents: %s", src)
	}
}
