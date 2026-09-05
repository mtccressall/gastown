package cmd

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/steveyegge/gastown/internal/nudge"
)

// stubStartNudgePoller swaps in a recording stub for the duration of a test.
func stubStartNudgePoller(t *testing.T) *[][2]string {
	t.Helper()
	var calls [][2]string
	orig := startNudgePoller
	startNudgePoller = func(townRoot, session string) (int, error) {
		calls = append(calls, [2]string{townRoot, session})
		return 4242, nil
	}
	t.Cleanup(func() { startNudgePoller = orig })
	return &calls
}

// writeStalePollerPid plants a PID file naming a process that cannot be
// running, which is the observed liveop-refinery state: pid file left behind
// on disk, process GONE, session still live.
func writeStalePollerPid(t *testing.T, townRoot, session string) string {
	t.Helper()
	dir := filepath.Join(townRoot, ".runtime", "nudge_poller")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, session+".pid")
	if err := os.WriteFile(path, []byte("999999999"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestEnqueueAndEnsurePoller_RecoversDeadPoller is the regression test for
// gastown-ku3: a queued nudge for a session whose poller is dead must trigger
// a poller start, or the message sits in the queue until it expires.
func TestEnqueueAndEnsurePoller_RecoversDeadPoller(t *testing.T) {
	townRoot := t.TempDir()
	session := "gt-liveop-refinery"
	writeStalePollerPid(t, townRoot, session)
	calls := stubStartNudgePoller(t)

	if err := enqueueAndEnsurePoller(townRoot, session, nudge.QueuedNudge{
		Sender:  "deacon",
		Message: "hello",
	}); err != nil {
		t.Fatalf("enqueueAndEnsurePoller: %v", err)
	}

	if n := nudge.QueueLen(townRoot, session); n != 1 {
		t.Errorf("QueueLen = %d, want 1", n)
	}
	if len(*calls) != 1 {
		t.Fatalf("startNudgePoller called %d times, want 1", len(*calls))
	}
	if got, want := (*calls)[0], [2]string{townRoot, session}; got != want {
		t.Errorf("startNudgePoller args = %v, want %v", got, want)
	}
}

// A poller that is genuinely alive must not be disturbed: StartPoller is
// idempotent, so the call still happens, and it must report the live PID
// rather than spawning a second poller.
func TestEnqueueAndEnsurePoller_LivePollerNotReplaced(t *testing.T) {
	townRoot := t.TempDir()
	session := "gt-gastown-witness"

	dir := filepath.Join(townRoot, ".runtime", "nudge_poller")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(dir, session+".pid")
	myPid := os.Getpid()
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(myPid)), 0644); err != nil {
		t.Fatal(err)
	}

	// Real StartPoller here: the assertion is that it early-returns the live
	// PID without launching anything.
	if err := enqueueAndEnsurePoller(townRoot, session, nudge.QueuedNudge{
		Sender:  "deacon",
		Message: "hello",
	}); err != nil {
		t.Fatalf("enqueueAndEnsurePoller: %v", err)
	}

	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("reading pid file: %v", err)
	}
	if got := string(data); got != strconv.Itoa(myPid) {
		t.Errorf("pid file = %q, want %q (live poller was replaced)", got, strconv.Itoa(myPid))
	}
}

// A failed Enqueue leaves nothing in the queue, so there is nothing for a
// poller to drain and no reason to start one. The caller falls back to
// immediate delivery on that path.
func TestEnqueueAndEnsurePoller_NoPollerWhenEnqueueFails(t *testing.T) {
	townRoot := t.TempDir()
	session := "gt-crew-test"
	calls := stubStartNudgePoller(t)

	// Make the queue directory un-creatable by planting a file where the
	// queue root must be a directory.
	runtimeDir := filepath.Join(townRoot, ".runtime")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "nudge_queue"), []byte("not a dir"), 0644); err != nil {
		t.Fatal(err)
	}

	err := enqueueAndEnsurePoller(townRoot, session, nudge.QueuedNudge{
		Sender:  "deacon",
		Message: "hello",
	})
	if err == nil {
		t.Fatal("enqueueAndEnsurePoller succeeded, want enqueue failure")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Logf("enqueue failed with: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("startNudgePoller called %d times after failed enqueue, want 0", len(*calls))
	}
}

// TestWaitIdleEnqueuesAllEnsurePoller guards the shape of the defect, not just
// one instance of it. gastown-ku3 was a single wait-idle branch that enqueued
// without starting a poller; two of the three branches were already like that,
// and nothing flagged it. This asserts the wait-idle case reaches the queue
// only through enqueueAndEnsurePoller.
//
// The queue-mode branch is the positive control: it enqueues directly on
// purpose (--mode=queue means "leave it queued"), and finding that call proves
// the walker can see a bare nudge.Enqueue at all. Without it a walker that
// matched nothing would report the same clean pass as a walker that found
// nothing wrong.
func TestWaitIdleEnqueuesAllEnsurePoller(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "nudge.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing nudge.go: %v", err)
	}

	var deliver *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "deliverNudge" {
			deliver = fn
			break
		}
	}
	if deliver == nil {
		t.Fatal("deliverNudge not found in nudge.go")
	}

	waitIdle := findModeCase(t, deliver, "NudgeModeWaitIdle")
	queueMode := findModeCase(t, deliver, "NudgeModeQueue")

	// Positive control first: if this is 0, the matcher is broken and every
	// other count below is meaningless.
	if got := countCalls(queueMode, "nudge", "Enqueue"); got != 1 {
		t.Fatalf("control: queue-mode branch has %d nudge.Enqueue calls, want 1 "+
			"(matcher cannot see bare Enqueue calls; the wait-idle counts below prove nothing)", got)
	}

	if got := countCalls(waitIdle, "nudge", "Enqueue"); got != 0 {
		t.Errorf("wait-idle branch has %d bare nudge.Enqueue calls, want 0 — "+
			"use enqueueAndEnsurePoller so a dead poller is restarted (gastown-ku3)", got)
	}
	if got := countCalls(waitIdle, "", "enqueueAndEnsurePoller"); got != 3 {
		t.Errorf("wait-idle branch has %d enqueueAndEnsurePoller calls, want 3 "+
			"(no-prompt-detection, unverified-submit, busy-timeout)", got)
	}
}

// findModeCase returns the switch case clause guarded by the named mode const.
func findModeCase(t *testing.T, fn *ast.FuncDecl, modeConst string) ast.Node {
	t.Helper()
	var found ast.Node
	ast.Inspect(fn, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			if ident, ok := expr.(*ast.Ident); ok && ident.Name == modeConst {
				found = clause
				return false
			}
		}
		return true
	})
	if found == nil {
		t.Fatalf("no case clause for %s in deliverNudge", modeConst)
	}
	return found
}

// countCalls counts calls to pkg.name within n. An empty pkg matches a plain
// function call by name.
func countCalls(n ast.Node, pkg, name string) int {
	count := 0
	ast.Inspect(n, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			ident, ok := fun.X.(*ast.Ident)
			if ok && pkg != "" && ident.Name == pkg && fun.Sel.Name == name {
				count++
			}
		case *ast.Ident:
			if pkg == "" && fun.Name == name {
				count++
			}
		}
		return true
	})
	return count
}
