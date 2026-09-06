package mail

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestMessageQueriesIncludeInfra is an AST assertion rather than a behavioural
// one, and that is deliberate.
//
// The defect (gt-mff) was not that a query behaved wrongly. It was that a query
// OMITTED A FLAG, and the omission is invisible in the result: `bd list` hides
// infra beads by default, `gt mail send` writes wisp-backed messages, and wisps
// are infra. So the inbox returned a well-formed, correctly-sorted, empty list
// at rc=0 while 227 messages sat in the store -- 84 of them addressed to the
// Mayor, and among them the Overseer's signed authorizations (gt-uq0g).
// Measured before the fix: 0 rows returned against 73 present for one recipient.
//
// A behavioural test is the wrong instrument here for the same reason it was
// wrong for gt-vkv9 and gt-7rne: it can only exercise the call sites it was
// written against, so it stays green when someone adds a third message query
// next month without the flag. The failure mode is a MISSING site, so the test
// has to quantify over every site, including ones nobody has thought of yet.
//
// Pin the invariant, not the instances.
func TestMessageQueriesIncludeInfra(t *testing.T) {
	const file = "mailbox.go"

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	strLit := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		return strings.Trim(lit.Value, `"`), true
	}

	var checked, offenders int
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}

		var elems []string
		for _, e := range cl.Elts {
			if s, ok := strLit(e); ok {
				elems = append(elems, s)
			}
		}

		// Only bd list invocations that select mail messages are in scope.
		var isList, isMessageQuery, hasInfra bool
		for _, s := range elems {
			switch s {
			case "list":
				isList = true
			case "gt:message":
				isMessageQuery = true
			case "--include-infra":
				hasInfra = true
			}
		}
		if !isList || !isMessageQuery {
			return true
		}

		checked++
		if !hasInfra {
			offenders++
			t.Errorf("%s:%d: a `bd list` query filtering on gt:message omits --include-infra.\n"+
				"Mail messages are wisp-backed (gt-wisp-*), wisps are infra beads, and bd list\n"+
				"hides infra by default -- so this query silently returns an EMPTY list rather\n"+
				"than an error. That is gt-mff. Add \"--include-infra\" to the args slice.",
				file, fset.Position(cl.Pos()).Line)
		}

		return true
	})

	// A scan that matched nothing and a scan that found nothing return the same
	// zero. State the denominator so a vacuous pass is visible, and fail closed
	// if the query shape ever moves out of this file.
	if checked == 0 {
		t.Fatalf("found no `bd list` gt:message queries in %s -- this test verified NOTHING. "+
			"The queries were probably renamed or moved; re-point the test rather than deleting it.", file)
	}
	t.Logf("checked %d gt:message bd-list quer(ies) in %s, %d missing --include-infra", checked, file, offenders)
}
