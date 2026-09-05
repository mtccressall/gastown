package beads

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// bdMetricsNotice is the courtesy message the installed bd writes to stderr on
// its first run. gastown strips HOME from the subprocess environment
// (filterBeadsEnv), so bd can never persist metrics.notice_shown and EVERY
// invocation is a first run.
const bdMetricsNotice = `Thanks for using bd! Quick heads-up: bd shares anonymous usage metrics -
   just which commands get run (plus the bd version and OS platform), never your
   issues, paths, remotes, identity, or anything you type.`

// installStubBD writes an executable "bd" shell script into a fresh temp dir and
// puts that dir at the front of PATH for the duration of the test. The script
// answers the --allow-stale support probe ("bd --allow-stale version") on stdout
// so the probe result is deterministic, and runs body for every other invocation.
func installStubBD(t *testing.T, body string) string {
	t.Helper()

	stubDir := t.TempDir()
	script := `#!/bin/sh
for a in "$@"; do
  if [ "$a" = "version" ]; then
    echo "bd stub 1.2.2"
    exit 0
  fi
done
` + body
	stubPath := filepath.Join(stubDir, "bd")
	if err := os.WriteFile(stubPath, []byte(script), 0755); err != nil {
		t.Fatalf("write bd stub: %v", err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stubDir
}

// TestRunQuietCommandWithStderrNoticeSucceeds is the regression test for
// gt-945n / gastown-0l3. run() used to treat "empty stdout + non-empty stderr"
// as a failure regardless of exit code, so bd's first-run metrics notice turned
// every successful --quiet command into a manufactured error.
//
// This exercises the path that actually broke: exit 0, nothing on stdout,
// something on stderr. A not-found test alone passes in BOTH the broken and the
// fixed state, which is why the original defect went unnoticed.
func TestRunQuietCommandWithStderrNoticeSucceeds(t *testing.T) {
	installStubBD(t, `cat >&2 <<'NOTICE'
`+bdMetricsNotice+`
NOTICE
exit 0
`)

	b := New(t.TempDir())

	out, err := b.Run("init", "--prefix", "tq", "--quiet")
	if err != nil {
		t.Fatalf("bd exiting 0 with empty stdout and a stderr notice must succeed, got error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty stdout, got %q", out)
	}
}

// TestRunWithRoutingQuietCommandWithStderrNoticeSucceeds covers the second copy
// of the same heuristic. A caller-grep on run() misses this site entirely, and a
// fix applied to only one of the two leaves the other live.
func TestRunWithRoutingQuietCommandWithStderrNoticeSucceeds(t *testing.T) {
	installStubBD(t, `cat >&2 <<'NOTICE'
`+bdMetricsNotice+`
NOTICE
exit 0
`)

	b := New(t.TempDir())

	out, err := b.runWithRouting("update", "gt-abc", "--status", "closed", "--quiet")
	if err != nil {
		t.Fatalf("runWithRouting: bd exiting 0 with empty stdout and a stderr notice must succeed, got error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty stdout, got %q", out)
	}
}

// TestRunNotFoundStillMapsToErrNotFound pins the half of the original fix that
// was never the problem: wrapError's "no issue found" text match. It works off
// stderr text rather than stream shape, so deleting the heuristic must not
// weaken genuine not-found detection.
func TestRunNotFoundStillMapsToErrNotFound(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stderr string
	}{
		{"no issue found", "Error: no issue found with ID gt-zzzzzz"},
		{"not found", "Error: issue not found: gt-zzzzzz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installStubBD(t, `echo '`+tc.stderr+`' >&2
exit 1
`)

			b := New(t.TempDir())

			if _, err := b.Run("show", "gt-zzzzzz", "--json"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("expected ErrNotFound, got %v", err)
			}
		})
	}
}

// TestRunPropagatesNonZeroExit confirms deletion of the heuristic did not make
// run() permissive: a real failure still surfaces, and it surfaces with bd's own
// stderr rather than the synthetic "command produced no output".
func TestRunPropagatesNonZeroExit(t *testing.T) {
	installStubBD(t, `echo 'Error: dolt server unreachable' >&2
exit 1
`)

	b := New(t.TempDir())

	_, err := b.Run("list", "--json")
	if err == nil {
		t.Fatal("expected an error when bd exits non-zero")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("a transport failure must not be reported as not-found: %v", err)
	}
}
