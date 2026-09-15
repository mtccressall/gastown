package git

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryGitExecPathCallsPreExec is the regression test for gastown-vvq, and
// it is an AST assertion rather than a behavioural one for the reason the defect
// itself demonstrates.
//
// The bug was never that a guarded path behaved wrongly. It was that four of the
// six exec paths in this package DID NOT RUN THE GUARD AT ALL, while two that
// did sat in the same file with a passing test suite beside them. A behavioural
// test can only exercise the paths it was written against, so it stays green
// while a seventh path is added next month with the preamble copied from the
// wrong neighbour -- which is exactly how `runWithEnvAndTimeout`,
// `runMergeCheck`, `runRaw` and `runWithStdin` came to be missing it.
//
// The bead reported ONE missing path. Enumerating by shape rather than by
// caller found four, two of them in merge_proof.go, which the reporting grep
// ('^func (g \*Git) run' in git.go) could not see. A caller grep is
// structurally blind to a bypass, because a bypass is by definition not a
// caller; only enumerating the exec paths themselves finds them.
//
// Same reasoning as TestLsRemoteCallsAreBounded above it and the
// canonical-assignee AST test that closed gt-7rne: pin the invariant, not the
// instances.
func TestEveryGitExecPathCallsPreExec(t *testing.T) {
	paths, err := packageSourceFiles()
	if err != nil {
		t.Fatalf("enumerating package sources: %v", err)
	}

	fset := token.NewFileSet()
	var missing, found []string

	for _, file := range paths {
		f, parseErr := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !hasGitReceiver(fn) || !takesCallerSuppliedArgs(fn) {
				continue
			}
			if !execsGitDirectly(fn) {
				continue
			}
			site := filepath.Base(file) + ":" + itoa(fset.Position(fn.Pos()).Line) + " " + fn.Name.Name
			switch reason := preExecUse(fn); reason {
			case "":
				found = append(found, site)
			default:
				missing = append(missing, site+" ("+reason+")")
			}
		}
	}

	sort.Strings(found)
	sort.Strings(missing)

	// The denominator, and it is the control that makes a zero mean something.
	// If a refactor renames the receiver, moves the exec behind a helper, or
	// changes how args are declared, this scan stops matching and reports the
	// same clean pass as a package where every path is guarded. A scan that
	// matched nothing and a scan that found nothing wrong are indistinguishable
	// without it.
	if len(found)+len(missing) == 0 {
		t.Fatal("found no *Git method that execs git with caller-supplied args; " +
			"this scan is now vacuous. Fix the predicate, do not delete the test.")
	}
	t.Logf("scanned %d exec path(s): %s", len(found)+len(missing), strings.Join(append(append([]string{}, found...), missing...), ", "))

	if len(missing) > 0 {
		t.Errorf("%d of %d git exec path(s) skip preExec: %s\n"+
			"Every path that hands caller-supplied args to git must call g.preExec(args), "+
			"which carries BOTH guardUnsafeTownRootMutation and maybeInvalidateRemoteRefCache. "+
			"Skipping it lets a push run inside an open remoteRefCache window (gastown-vvq): "+
			"the push succeeds, no error is reported, and a later RemoteBranchTip or "+
			"RemoteBranchExists serves the pre-push answer.",
			len(missing), len(found)+len(missing), strings.Join(missing, ", "))
	}
}

// packageSourceFiles lists the non-test Go files in this package. It reads the
// DIRECTORY rather than a hard-coded list so a file added later is covered by
// default -- the two paths this test exists for lived in merge_proof.go, a file
// nobody thought to look in.
func packageSourceFiles() ([]string, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func hasGitReceiver(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == "Git"
}

// takesCallerSuppliedArgs reports whether the method accepts a string slice or a
// string variadic -- the shape that lets a caller name any git subcommand it
// likes, which is what makes the guard load-bearing. A method that builds its
// own fixed argv cannot smuggle a push past the memo.
func takesCallerSuppliedArgs(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, p := range fn.Type.Params.List {
		switch typ := p.Type.(type) {
		case *ast.Ellipsis:
			if ident, ok := typ.Elt.(*ast.Ident); ok && ident.Name == "string" {
				return true
			}
		case *ast.ArrayType:
			if typ.Len != nil {
				continue
			}
			if ident, ok := typ.Elt.(*ast.Ident); ok && ident.Name == "string" {
				return true
			}
		}
	}
	return false
}

// execsGitDirectly reports whether the body starts a `git` subprocess itself
// rather than delegating to another method in this package. Delegating methods
// (runWithEnv -> runWithEnvAndTimeout) are guarded by the path they delegate to,
// and requiring preExec of them would double-invalidate for no benefit.
func execsGitDirectly(fn *ast.FuncDecl) bool {
	git := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "exec" {
			return true
		}
		if sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
			return true
		}
		for _, arg := range call.Args {
			if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"git"` {
				git = true
			}
		}
		return true
	})
	return git
}

// preExecUse returns "" when fn satisfies the whole contract, or a reason it
// does not. BOTH halves are required: preExec drops the memo before the
// subprocess and returns a hook that drops it again afterwards, and a path that
// takes the guard while discarding the hook reopens the concurrent-read window
// (see TestRemoteRefCacheDropIsClosedOnBothSidesOfAMutation). Checking only the
// call would let exactly that through while reporting a clean pass.
//
// The binding is followed by NAME rather than assumed to be called "done", so a
// path that spells it differently still passes and one that never defers it
// still fails.
func preExecUse(fn *ast.FuncDecl) string {
	hook := ""
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) != 2 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "preExec" {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok {
			hook = ident.Name
		}
		return true
	})
	if hook == "" {
		return "does not call g.preExec(args)"
	}
	if hook == "_" {
		return "discards the preExec completion hook"
	}

	deferred := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		d, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		if ident, ok := d.Call.Fun.(*ast.Ident); ok && ident.Name == hook {
			deferred = true
		}
		return true
	})
	if !deferred {
		return "never defers the preExec hook " + hook + "()"
	}
	return ""
}
