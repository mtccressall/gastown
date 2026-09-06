package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// TestCloseDescendantsSeesWispChildren is a behavioural test, not a source-text
// one, and the distinction is the whole point.
//
// The defect (gt-qh0g) was that closeDescendantsImpl listed a molecule's children
// with no --include-infra. Molecule steps are wisps, bd hides infra beads by
// default, so the listing returned ZERO children for a molecule that had open
// ones. The function then took its len(children)==0 early return and reported
// (0, nil) -- nothing closed, NO ERROR -- while the caller went on to close the
// parent. Every step was orphaned under a closed root, unreachable by anything.
//
// Measured on the live town before the fix, one closed dog-molecule root:
//
//	bd list --parent <root> --status=all                  -> 0 children
//	bd list --parent <root> --status=all --include-infra  -> 3 (2 OPEN)
//
// That ran at roughly 800-960 permanent open wisps per day.
//
// A source-text assertion ("the file contains --include-infra") would pass while
// the option sat unplumbed in ListOptions and never reached the bd argv. This
// drives the real path through a fake bd and asserts on BOTH halves that had to
// work: the flag reaches the command line, AND the wisp child actually gets
// closed. Either half alone is a green test beside a live bug.
func TestCloseDescendantsSeesWispChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script command stubs not supported on Windows")
	}

	binDir := t.TempDir()
	argsLog := filepath.Join(t.TempDir(), "bd-args.log")

	// The stub returns a wisp child ONLY when --include-infra is present, which is
	// exactly how the real bd behaves. That is what makes this test able to fail:
	// without the flag the child is invisible and nothing is closed.
	bdScript := `#!/bin/sh
printf '%s\n' "$*" >> "$BD_ARGS_LOG"
case "$*" in
  # The ROOT's child listing, and only when --include-infra is present. Real bd
  # hides wisps without that flag, which is the whole defect.
  *list*--parent=gt-wisp-root*)
    case "$*" in
      *--include-infra*)
        printf '[{"id":"gt-wisp-step1","title":"Report findings and return to kennel","status":"open","issue_type":"task","ephemeral":true}]\n'
        ;;
      *)
        printf '[]\n'
        ;;
    esac
    ;;
  # The step has no children of its own. closeDescendantsImpl RECURSES, so a stub
  # that returned the same child for every parent would recurse forever -- which
  # is exactly what the first version of this test did.
  *list*)
    printf '[]\n'
    ;;
  *)
    printf 'ok\n'
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(bdScript), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BD_ARGS_LOG", argsLog)
	beads.ResetBdAllowStaleCacheForTest()
	t.Cleanup(beads.ResetBdAllowStaleCacheForTest)

	closed := closeDescendants(beads.New(t.TempDir()), "gt-wisp-root")

	argsBytes, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("read bd args: %v", err)
	}
	args := string(argsBytes)

	// Half 1: the flag has to reach the argv. This is what was missing.
	if !strings.Contains(args, "--include-infra") {
		t.Errorf("closeDescendants listed children without --include-infra.\n"+
			"Molecule steps are wisps and bd hides infra by default, so this listing\n"+
			"returns zero children and the function reports (0, nil) while the caller\n"+
			"closes the parent, orphaning every step (gt-qh0g).\nbd args were:\n%s", args)
	}

	// Half 2: the wisp child has to actually be closed. A flag that reaches the
	// argv but whose result is dropped looks identical to the bug from outside.
	if closed != 1 {
		t.Errorf("closeDescendants closed %d children, want 1 -- the wisp step was "+
			"listed but not closed", closed)
	}
	if !strings.Contains(args, "gt-wisp-step1") {
		t.Errorf("bd was never asked to close the wisp child gt-wisp-step1.\nbd args were:\n%s", args)
	}
}
