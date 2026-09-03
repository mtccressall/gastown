package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// repoWithEnv builds a throwaway repository root that declares a dotenv deny
// rule and DELIBERATELY CONTAINS NO .env FILE.
//
// The absence is the point. The first version of this fixture wrote a .env,
// because the code under test asked whether one existed. Both were wrong in
// the same direction, so the tests passed and the guard missed the incident it
// was written for: brahmin's worktree has no .env at any depth and brahmin
// still froze on the prompt for hours. A fixture built from the code's own
// assumption cannot falsify that assumption.
func repoWithEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatalf("creating .claude: %v", err)
	}
	settings := `{"permissions":{"deny":["Read(./.env)","Read(./.env.*)","Bash(rm -rf *)"]}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(settings), 0600); err != nil {
		t.Fatalf("writing settings.json: %v", err)
	}
	for _, sub := range []string{"src", "components", "services"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			t.Fatalf("creating %s: %v", sub, err)
		}
	}
	return dir
}

func TestDenyRuleCoversSecrets(t *testing.T) {
	// The load-bearing case: a repo that declares the rule and has NO .env
	// file. This is brahmin's exact shape, and the guard must fire.
	withRule := repoWithEnv(t)
	if _, err := os.Stat(filepath.Join(withRule, ".env")); !os.IsNotExist(err) {
		t.Fatal("fixture must NOT contain a .env — the absence is what it tests")
	}

	write := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(body), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		return dir
	}

	tests := []struct {
		name string
		dir  string
		want bool
	}{
		{"declares a dotenv deny rule, no .env on disk", withRule, true},
		{"anchored form", write(t, `{"permissions":{"deny":["Read(**/.env)"]}}`), true},
		{"secrets rule", write(t, `{"permissions":{"deny":["Read(secrets/**)"]}}`), true},
		{"deny rules exist but none for secrets", write(t, `{"permissions":{"deny":["Bash(rm -rf *)"]}}`), false},
		{"no deny block at all", write(t, `{"permissions":{"allow":["Bash(*)"]}}`), false},
		// A Bash rule mentioning .env is not the predictive Read check that
		// raises the modal, so it must not arm the guard.
		{"a Bash rule mentioning env is not a Read deny", write(t, `{"permissions":{"deny":["Bash(cat .env)"]}}`), false},
		{"unreadable settings fails open", t.TempDir(), false},
		{"malformed json fails open", write(t, `{not json`), false},
		{"empty path", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := denyRuleCoversSecrets(tt.dir); got != tt.want {
				t.Errorf("denyRuleCoversSecrets(%q) = %v, want %v", tt.dir, got, tt.want)
			}
		})
	}
}

func TestExcludesEnv(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"grep --exclude", "grep -rn foo . --exclude=.env", true},
		{"grep --exclude quoted", "grep -rn foo . --exclude='.env*'", true},
		{"grep --exclude double quoted", `grep -rn foo . --exclude=".env*"`, true},
		{"grep --exclude glob", "grep -rn foo . --exclude=*.env", true},
		{"exclude-dir", "grep -rn foo . --exclude-dir=.env.d", true},
		{"ripgrep negated glob", "rg -u foo . -g '!.env'", true},
		{"no exclusion", "grep -rn foo .", false},
		{"excludes something else", "grep -rn foo . --exclude-dir=node_modules", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := excludesEnv(tt.command); got != tt.want {
				t.Errorf("excludesEnv(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestIsRecursiveGrep(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"grep -r", "grep -r foo .", true},
		{"grep -rn combined", "grep -rn foo .", true},
		{"grep -R", "grep -R foo .", true},
		{"grep --recursive", "grep --recursive foo .", true},
		{"absolute path to grep", "/usr/bin/grep -rn foo .", true},
		{"egrep -r", "egrep -r foo .", true},
		{"non-recursive grep", "grep -n foo file.txt", false},
		{"not grep at all", "cat file.txt", false},
		{"long flag that merely contains r", "grep --color foo file.txt", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRecursiveGrep(splitCommand(tt.command)); got != tt.want {
				t.Errorf("isRecursiveGrep(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestIsUnrestrictedRipgrep(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"rg -u", "rg -u foo .", true},
		{"rg -uu", "rg -uu foo .", true},
		{"rg --no-ignore", "rg --no-ignore foo .", true},
		{"rg --hidden", "rg --hidden foo .", true},
		// Plain rg honours .gitignore and skips hidden files, so it never
		// reaches .env and must not be blocked.
		{"plain rg", "rg foo .", false},
		{"rg with harmless flags", "rg -n --color never foo .", false},
		{"not rg", "grep -r foo .", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUnrestrictedRipgrep(splitCommand(tt.command)); got != tt.want {
				t.Errorf("isUnrestrictedRipgrep(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestSearchRootIsRepoRoot(t *testing.T) {
	dir := repoWithEnv(t)

	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"bare dot", "grep -rn foo .", true},
		{"dot slash", "grep -rn foo ./", true},
		// The pattern LOOKS like a path and is not one. Treating it as a
		// scope would let the exact command this guard exists to stop
		// through, so existence is what decides, not the slash.
		{"pattern containing a slash, root search", "grep -rn foo/bar .", true},
		{"scoped to one real subtree", "grep -rn foo src/", false},
		{"scoped to several real subtrees", "grep -rn foo src/ components/ services/", false},
		{"dot plus a real subtree", "grep -rn foo . src/", false},
		{"no path argument at all", "grep -rn foo", false},
		{"quoted dot", `grep -rn foo "."`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := searchRootIsRepoRoot(splitCommand(tt.command), dir); got != tt.want {
				t.Errorf("searchRootIsRepoRoot(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestIsFindExecGrep(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"find exec grep", "find . -type f -exec grep -l foo {} ;", true},
		{"find piped to xargs grep", "find . -type f | xargs grep foo", true},
		{"find alone", "find . -name *.ts", false},
		{"grep alone", "grep -rn foo .", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFindExecGrep(splitCommand(tt.command)); got != tt.want {
				t.Errorf("isFindExecGrep(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestHookInputCwd(t *testing.T) {
	// The .env check has to run against the AGENT's directory. If the guard
	// read its own cwd instead, it would answer a question about the wrong
	// repository -- allowing the sweep in a repo that has a .env, or
	// blocking one in a repo that does not.
	got := hookInputCwd([]byte(`{"tool_input":{"command":"grep -rn foo ."},"cwd":"/some/polecat/worktree"}`))
	if got != "/some/polecat/worktree" {
		t.Errorf("hookInputCwd() = %q, want the cwd from the hook payload", got)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if got := hookInputCwd([]byte(`{"tool_input":{"command":"grep -rn foo ."}}`)); got != wd {
		t.Errorf("hookInputCwd() with no cwd field = %q, want the process cwd %q", got, wd)
	}
}

// TestBroadSearchGuardDecision exercises the whole predicate chain the way
// runTapGuardBroadSearch composes it, because the individual matchers being
// right is not the same claim as the guard blocking the right commands.
func TestBroadSearchGuardDecision(t *testing.T) {
	withEnv := repoWithEnv(t)
	withoutEnv := t.TempDir()
	for _, sub := range []string{"src"} {
		if err := os.MkdirAll(filepath.Join(withoutEnv, sub), 0755); err != nil {
			t.Fatalf("creating %s: %v", sub, err)
		}
	}

	blocks := func(command, dir string) bool {
		if excludesEnv(command) {
			return false
		}
		fields := splitCommand(command)
		if !isRecursiveGrep(fields) && !isUnrestrictedRipgrep(fields) && !isFindExecGrep(fields) {
			return false
		}
		if !searchRootIsRepoRoot(fields, dir) {
			return false
		}
		return denyRuleCoversSecrets(dir)
	}

	tests := []struct {
		name    string
		command string
		dir     string
		want    bool
	}{
		// The command that actually froze nuka and brahmin.
		// brahmin's exact command, in a repo shaped exactly like brahmin's:
		// the deny rule is declared and no .env exists anywhere.
		{"repo-root recursive grep where the deny rule is declared", "grep -rn 'ResearchHub' .", withEnv, true},
		{"find piped to xargs grep at the root", "find . -type f | xargs grep ResearchHub", withEnv, true},
		{"unrestricted rg at the root", "rg -u ResearchHub .", withEnv, true},

		// Everything a polecat legitimately needs stays allowed.
		{"scoped search", "grep -rn 'ResearchHub' src/", withEnv, false},
		{"root search that excludes .env", "grep -rn 'ResearchHub' . --exclude='.env*'", withEnv, false},
		{"plain rg, which skips hidden files anyway", "rg ResearchHub .", withEnv, false},
		{"non-recursive grep", "grep -n ResearchHub package.json", withEnv, false},
		{"not a search", "npm test", withEnv, false},

		// No deny rule means no prompt to prevent, so the guard must stay
		// out of the way entirely.
		{"repo-root recursive grep where no deny rule is declared", "grep -rn 'ResearchHub' .", withoutEnv, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := blocks(tt.command, tt.dir); got != tt.want {
				t.Errorf("blocks(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}
