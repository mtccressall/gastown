// Package testmode reports whether the running binary is a `go test` binary,
// so that activity-log writers can drop their writes instead of appending to
// whatever town the working directory happens to resolve to.
//
// The problem it solves (gastown-adj): agents run `make test` inside their own
// worktrees. Writers such as internal/events and internal/townlog resolve the
// town root from the cwd, so fixture traffic was appended to the PRODUCTION
// records — signed with the running agent's identity. Measured on this town: at
// least 59 phantom events in .events.jsonl (40 nudge, 15 session_death, 4 mail)
// across three agent identities and at least 21 hours, plus 31 phantom
// `hello dog` nudge lines in logs/town.log. Both feed counts are floors: they
// come from filters built out of fixtures somebody happened to notice, which is
// why this gates the emitting writer rather than filtering the output.
//
// A polecat was given a direct instruction to stop sending test nudges it had
// never sent; every reader was reasoning correctly from records that described
// deliveries which never happened.
//
// Command-level hooks such as GT_TEST_NUDGE_LOG gate the TRANSPORT and leave
// the RECORD of a delivery behind, which is the artifact everyone reads. The
// gate therefore belongs at the writers, not at the call sites: the leak is not
// confined to one command — nudge fixtures in internal/cmd, the hq-cv-abc mail
// fixture in synthesis_test.go and the myr/mycat session_death fixtures in
// internal/daemon all arrive by different routes, and a per-call-site fix is
// the kind that gets half applied.
package testmode

import (
	"os"
	"testing"
)

// EnvSuppress suppresses activity-log writes when set to anything but "0".
//
// It is set automatically inside a test binary. Tests that exercise a writer
// itself opt back in with t.Setenv(testmode.EnvSuppress, "0").
const EnvSuppress = "GT_SUPPRESS_EVENTS"

func init() {
	// Publish the decision into the environment rather than consulting
	// testing.Testing() at each write, so that a real gt binary exec'd by a
	// test inherits it too: such a subprocess is not itself a test binary and
	// would otherwise resolve a town root of its own and write.
	if testing.Testing() && os.Getenv(EnvSuppress) == "" {
		_ = os.Setenv(EnvSuppress, "1")
	}
}

// WritesSuppressed reports whether activity-log writes should be dropped.
func WritesSuppressed() bool {
	v := os.Getenv(EnvSuppress)
	return v != "" && v != "0"
}
