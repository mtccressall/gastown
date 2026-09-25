package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/steveyegge/gastown/internal/style"
)

// checkpointRefPrefix must match internal/daemon's checkpointRef. The daemon
// writes the ref and cannot import this package (internal/cmd imports
// internal/daemon), so the prefix is duplicated and pinned by a test.
const checkpointRefPrefix = "refs/checkpoints/"

// outputUncommittedCheckpointRef tells a fresh session that the checkpoint dog
// saved uncommitted work OFF the branch, and how to get it back.
//
// THIS IS THE HALF THAT MAKES THE REF DESIGN A SAFETY NET RATHER THAN A TIDIER
// PLACE TO LOSE THINGS. Moving the checkpoint off the branch removed it from
// `git log`, from the branch, and from every diff a person would normally read
// — so nothing distinguishes PRESERVED from LOST unless a surface somebody
// already looks at says the ref is there. `gt prime` is that surface: it is
// what a respawning session runs before anything else (gt-hgwls, and the
// ruling Henry made on #429 that retained bytes a person cannot reach are not
// draft recovery).
//
// It is deliberately INDEPENDENT of the JSON session checkpoint above it: that
// one records what a session was doing, this one records uncommitted FILES, and
// either can exist without the other.
func outputUncommittedCheckpointRef(ctx RoleContext) {
	if ctx.Role != RolePolecat && ctx.Role != RoleCrew {
		return
	}
	git := func(args ...string) (string, bool) {
		cmd := exec.Command("git", args...)
		cmd.Dir = ctx.WorkDir
		out, err := cmd.Output()
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(out)), true
	}

	branch, ok := git("rev-parse", "--abbrev-ref", "HEAD")
	if !ok || branch == "" || branch == "HEAD" {
		return
	}
	ref := checkpointRefPrefix + branch
	if _, ok := git("rev-parse", "--verify", "--quiet", ref); !ok {
		return
	}
	// Only speak up if the checkpoint actually differs from the branch tip;
	// an identical tree means the work was committed normally and there is
	// nothing to recover.
	files, ok := git("diff", "--name-only", "HEAD", ref)
	if !ok || files == "" {
		return
	}

	names := strings.Split(files, "\n")
	fmt.Println()
	fmt.Printf("%s\n\n", style.Bold.Render("## 💾 Uncommitted work was checkpointed OFF-BRANCH"))
	fmt.Printf("The checkpoint dog saved %d uncommitted file(s) to `%s`.\n", len(names), ref)
	fmt.Printf("This is NOT on your branch and will NOT appear in `git log` or `git status`.\n\n")
	for i, n := range names {
		if i >= 10 {
			fmt.Printf("  ... and %d more\n", len(names)-10)
			break
		}
		fmt.Printf("  %s\n", n)
	}
	fmt.Printf("\nRecover all of it with:\n\n    git checkout %s -- .\n\n", ref)
	fmt.Printf("Inspect it first with:  git show %s\n", ref)
}
