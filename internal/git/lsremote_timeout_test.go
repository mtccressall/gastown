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
//
// gastown-o8q moved the literal into `lsRemote`, the canonical read path that
// carries the pass-scoped memo. That is exactly the change that would have made
// this scan VACUOUS -- the call sites in git.go stopped mentioning "ls-remote",
// so a scan of git.go alone would find zero sites and report zero unbounded
// ones, which is the same green as a passing test. The scan now covers both
// files AND asserts it found sites at all.
func TestLsRemoteCallsAreBounded(t *testing.T) {
	fset := token.NewFileSet()

	var unbounded []string
	scanned := 0

	for _, file := range []string{"git.go", "remote_cache.go"} {
		f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}

		// The literal now sits inside a composite literal that is an argument to
		// the bounded call, so "is it a direct argument of a bounded call" no
		// longer describes the shape. Collect the SOURCE RANGE of every bounded
		// call, then ask whether each literal falls inside one. That is robust
		// to how the argument list happens to be built, and it is a predicate
		// with no hidden state to get wrong.
		var boundedLo, boundedHi []token.Pos
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee := ""
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				callee = fn.Sel.Name
			case *ast.Ident:
				callee = fn.Name
			}
			switch callee {
			case "runWithTimeout", "runWithEnvAndTimeout", "CommandContext":
				boundedLo = append(boundedLo, call.Pos())
				boundedHi = append(boundedHi, call.End())
			}
			return true
		})

		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || !strings.Contains(lit.Value, "ls-remote") {
				return true
			}
			scanned++
			for i := range boundedLo {
				if lit.Pos() >= boundedLo[i] && lit.End() <= boundedHi[i] {
					return true
				}
			}
			unbounded = append(unbounded, file+":"+itoa(fset.Position(lit.Pos()).Line))
			return true
		})
	}

	// The denominator. Without it a refactor that renames or relocates the
	// literal turns this whole test into a scan that matched nothing, and a
	// scan that matched nothing is indistinguishable from a scan that found
	// nothing wrong. It has already fired once, on the gastown-o8q refactor
	// that moved the literal into remote_cache.go.
	if scanned == 0 {
		t.Fatal("no ls-remote literal found in git.go or remote_cache.go; this scan " +
			"is now vacuous. Point it at wherever the literal moved.")
	}

	if len(unbounded) > 0 {
		t.Errorf("ls-remote invoked without a deadline at %d of %d site(s): %s\n"+
			"A hung remote blocks these forever; the only bound would be the daemon's "+
			"outer 5m dispatch deadline, which names no cause (gt-vkv9). "+
			"Use runWithTimeout(remoteReadTimeout, ...) or exec.CommandContext.",
			len(unbounded), scanned, strings.Join(unbounded, ", "))
	}
}

// TestLsRemoteReadsRouteThroughCanonicalHelper is the other half of the same
// invariant, and it is the one gastown-o8q needs: a read that spells the
// subprocess out for itself is bounded but NOT memoized, so it silently
// reintroduces the repeat round-trips the memo exists to remove.
//
// A bypass is by definition not a caller, so grepping for callers of lsRemote
// cannot find one. This asserts the shape of the construction instead.
func TestLsRemoteReadsRouteThroughCanonicalHelper(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "git.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse git.go: %v", err)
	}

	var direct []string
	routed := 0
	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BasicLit:
			if node.Kind == token.STRING && strings.Contains(node.Value, "ls-remote") {
				direct = append(direct, "git.go:"+itoa(fset.Position(node.Pos()).Line))
			}
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "lsRemote" {
				routed++
			}
		}
		return true
	})

	// Positive control: if git.go stopped calling lsRemote entirely, the
	// zero-direct-invocations result above would be true and meaningless.
	if routed == 0 {
		t.Fatal("git.go calls lsRemote zero times; either the reads moved or this " +
			"test is asserting an absence over an empty population")
	}

	if len(direct) > 0 {
		t.Errorf("git.go invokes ls-remote directly at %d site(s) (%d routed correctly): %s\n"+
			"Read-only ls-remote must go through (*Git).lsRemote so a scheduler pass "+
			"memoizes it (gastown-o8q). A direct invocation is bounded but repeats the "+
			"network round-trip once per caller.",
			len(direct), routed, strings.Join(direct, ", "))
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
