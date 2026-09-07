package templates

import (
	"regexp"
	"strings"
	"testing"
)

// bdCreateRepoAliasRe matches a prescribed `bd create --repo <bare-name>`, i.e. a
// --repo value that is not a path. That form is the gastown-x9a / be-flg trigger:
// bd resolves a bare name as a path relative to cwd, SILENTLY creates a beads store
// there, files the bead into it, and prints a normal "Created issue:" line, so the
// bead is invisible to every real store.
//
// A path-like value (contains / or ~ or starts with .) keeps bd's native --repo
// semantics against an existing store and is not matched here.
// The trailing quote-or-end-of-line requirement keeps the detector on runnable
// commands: prose that merely mentions the flag ("removes a bd create --repo alias
// from argv") is followed by another word and does not match.
var bdCreateRepoAliasRe = regexp.MustCompile(`(?m)bd\s+create\s+[^\n]*--repo[= ]+([A-Za-z0-9_-]+)\s*(?:["'` + "`" + `]|$)`)

// TestRoleTemplates_NoBareRepoAliasPrescription pins the invariant rather than the
// instance: no role template may hand an agent a bd create --repo <bare-name>
// command. The templates are injected into every crew, deacon, dog, mayor and
// polecat context at session start, so a regression here is pushed to every agent
// in town rather than sitting in a document someone must go and read.
//
// Prose that NAMES the forbidden form (the warning block) is exempt: it is matched
// only when it reads as a runnable command, which requires a value after --repo.
func TestRoleTemplates_NoBareRepoAliasPrescription(t *testing.T) {
	tmpl, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	roles := []string{"crew", "deacon", "dog", "mayor", "polecat", "witness", "refinery", "boot"}
	scanned := 0

	for _, role := range roles {
		out, err := tmpl.RenderRole(role, RoleData{
			Role: role, RigName: "gastown", TownRoot: "/test/town", TownName: "town",
			WorkDir: "/test/town/gastown", DefaultBranch: "main", Polecat: "agate",
			DogName: "rover", IssuePrefix: "gastown", BeadsDir: "/test/town/gastown/.beads",
			MayorSession: "gt-town-mayor", DeaconSession: "gt-town-deacon",
		})
		if err != nil {
			t.Fatalf("RenderRole(%s) error = %v", role, err)
		}
		if len(out) == 0 {
			t.Fatalf("RenderRole(%s) produced no output", role)
		}
		scanned++

		for _, m := range bdCreateRepoAliasRe.FindAllStringSubmatch(out, -1) {
			t.Errorf("role %q prescribes `bd create --repo %s` (bare alias). "+
				"Use `bd -C <townroot>/<rigdir> create ...`, which fails loudly on a bad "+
				"path instead of silently creating a phantom store (gastown-x9a, be-flg). "+
				"Matched: %q", role, m[1], strings.TrimSpace(m[0]))
		}
	}

	// Guard against a vacuous scan: a rename or a render failure must not read as a pass.
	if scanned != len(roles) {
		t.Fatalf("scanned %d role templates, want %d", scanned, len(roles))
	}
}

// TestBdCreateRepoAliasRe_Discriminates is the negative control for the detector
// above. Without it, a regex that matches nothing and a template set that is clean
// return the same pass.
func TestBdCreateRepoAliasRe_Discriminates(t *testing.T) {
	mustMatch := []string{
		"`bd create --repo beads \"...\"`",
		"bd create --repo gastown \"x\"",
		"bd create --repo=hq \"x\"",
		"bd create --title=\"t\" --repo beads",
	}
	mustNotMatch := []string{
		"bd -C /test/town/beadsrig create \"...\"",
		"bd create \"...\"",
		"**Never use `bd create --repo <name>`.**",      // the warning prose
		"bd create --repo ../other/.beads \"x\"",        // path-like: native bd semantics
		"// RewriteBDCreateRepoAlias removes a bd create --repo alias from argv",
	}
	for _, s := range mustMatch {
		if !bdCreateRepoAliasRe.MatchString(s) {
			t.Errorf("detector MISSED a prescription: %q", s)
		}
	}
	for _, s := range mustNotMatch {
		if bdCreateRepoAliasRe.MatchString(s) {
			t.Errorf("detector FALSE POSITIVE on: %q", s)
		}
	}
}
