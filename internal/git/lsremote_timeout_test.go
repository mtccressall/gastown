package git

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"
)

// TestLsRemoteCallsAreBounded is an AST assertion rather than a behavioural one,
// and that is deliberate.
//
// The defect (gt-vkv9) was not that a bounded call behaved wrongly -- it was that
// SOME CALL SITE did not use the bound at all, while `runWithTimeout` sat in the
// same file. A behavioural test can only exercise the sites it was written
// against, so it goes green while a seventh unbounded site is added next month.
// This asserts the property over every site in the file, including ones nobody
// has thought of yet.
//
// Same reasoning as the canonical-assignee AST test that closed gt-7rne: pin the
// invariant, not the instances.
func TestLsRemoteCallsAreBounded(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "git.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse git.go: %v", err)
	}

	var unbounded []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Does this call mention "ls-remote" as a literal argument?
		mentionsLsRemote := false
		for _, a := range call.Args {
			if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING &&
				strings.Contains(lit.Value, "ls-remote") {
				mentionsLsRemote = true
			}
		}
		if !mentionsLsRemote {
			return true
		}

		// It is bounded iff the callee is a timeout-carrying form.
		callee := ""
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			callee = fn.Sel.Name
		case *ast.Ident:
			callee = fn.Name
		}
		switch callee {
		case "runWithTimeout", "runWithEnvAndTimeout", "CommandContext":
			return true
		}

		pos := fset.Position(call.Pos())
		unbounded = append(unbounded, callee+" at git.go:"+itoa(pos.Line))
		return true
	})

	if len(unbounded) > 0 {
		t.Errorf("ls-remote invoked without a deadline at %d site(s): %s\n"+
			"A hung remote blocks these forever; the only bound would be the daemon's "+
			"outer 5m dispatch deadline, which names no cause (gt-vkv9). "+
			"Use runWithTimeout(remoteReadTimeout, ...) or exec.CommandContext.",
			len(unbounded), strings.Join(unbounded, ", "))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestRunWithTimeoutActuallyKills is the behavioural half: it establishes that
// the mechanism the AST test points every call site at really does bound a hung
// git process. Without this, the AST test would be pinning call sites to a
// function nobody had shown works.
func TestRunWithTimeoutActuallyKills(t *testing.T) {
	g := &Git{workDir: t.TempDir()}

	start := time.Now()
	// `git ls-remote` against a black-holed address: connect() hangs rather than
	// being refused, which is the shape of the real failure (a remote that stops
	// answering), not a fast error.
	_, err := g.runWithTimeout(1500*time.Millisecond, "ls-remote", "--heads",
		"git://10.255.255.1/nonexistent.git", "main")
	elapsed := time.Since(start)

	if err == nil {
		t.Skip("the unroutable address answered; no hang to bound in this environment")
	}
	if elapsed > 20*time.Second {
		t.Fatalf("runWithTimeout did not bound the call: %v elapsed on a 1.5s deadline", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Logf("bounded in %v but the error was not a timeout: %v", elapsed, err)
		t.Skip("environment failed the call fast (DNS/route), so the deadline was not exercised")
	}
	if !strings.Contains(err.Error(), "remote may be unreachable") {
		t.Errorf("timeout error lost its diagnostic clause; the whole point is that "+
			"the log names a cause. got: %v", err)
	}
}
