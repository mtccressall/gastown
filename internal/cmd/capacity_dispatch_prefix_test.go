package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// newRoutedTown builds a town with a liveop rig whose store serves TWO ID
// prefixes -- the current "liveop-" and the legacy "live-op-" -- which is the
// real shape of this town and the one AcceptsPrefix cannot express.
//
// The rig root carries a redirect rather than the store itself, because that is
// also real (liveop/.beads redirects to mayor/rig/.beads) and it is the reason
// the comparison has to be made on RESOLVED directories rather than on text.
func newRoutedTown(t *testing.T) string {
	t.Helper()
	town := t.TempDir()

	mustMkdir(t, filepath.Join(town, ".beads"))
	mustMkdir(t, filepath.Join(town, "liveop", "mayor", "rig", ".beads"))
	mustMkdir(t, filepath.Join(town, "gastown", "mayor", "rig", ".beads"))
	mustMkdir(t, filepath.Join(town, "liveop", ".beads"))

	mustWrite(t, filepath.Join(town, "liveop", ".beads", "redirect"), "mayor/rig/.beads")

	routes := `{"prefix":"gt-","path":"."}
{"prefix":"hq-","path":"."}
{"prefix":"live-","path":"liveop/mayor/rig"}
{"prefix":"liveop-","path":"liveop/mayor/rig"}
{"prefix":"gastown-","path":"gastown/mayor/rig"}
`
	mustWrite(t, filepath.Join(town, ".beads", "routes.jsonl"), routes)
	return town
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// TestRoutedIntoRig_LegacyPrefixIsAccepted is gt-83kc.
//
// BeadIDPrefix("live-op-agy") is "live", the liveop rig's registered prefix is
// "liveop", and equality refused every legacy bead permanently -- 11 open
// P0/P1 beads at the time this was written, including the top of liveop's
// queue. The route table already knows better; dispatch just never asked it.
func TestRoutedIntoRig_LegacyPrefixIsAccepted(t *testing.T) {
	town := newRoutedTown(t)

	if !routedIntoRig(town, "liveop", "live-op-agy") {
		t.Error("legacy live-op- bead refused; these are undispatchable forever " +
			"and no amount of re-slinging changes it")
	}
	if !routedIntoRig(town, "liveop", "liveop-jn2") {
		t.Error("current liveop- bead refused")
	}
}

// TestRoutedIntoRig_StillRefusesCrossRig is the half that must not regress.
// The guard exists so a polecat is not handed a bead its rig's DB cannot
// resolve (gt-el4). Widening it to consult routes must not widen it to
// everything -- and a check that accepts the legacy bead by accepting
// EVERYTHING would pass the test above on its own.
func TestRoutedIntoRig_StillRefusesCrossRig(t *testing.T) {
	town := newRoutedTown(t)

	for _, tc := range []struct{ rig, bead string }{
		{"liveop", "gastown-mq9"}, // another rig's bead
		{"liveop", "gt-vkv9"},     // a town bead
		{"liveop", "hq-cv-abc"},   // a town convoy
		{"gastown", "live-op-agy"},
		{"gastown", "liveop-jn2"},
	} {
		if routedIntoRig(town, tc.rig, tc.bead) {
			t.Errorf("rig %q accepted %q; the cross-rig guard has been widened to "+
				"everything and gt-el4 is back", tc.rig, tc.bead)
		}
	}
}

// TestRoutedIntoRig_FailsClosedWithoutRoutes: absent routing information is not
// permission. A town with no routes.jsonl must leave the existing refusal in
// place rather than defaulting open.
func TestRoutedIntoRig_FailsClosedWithoutRoutes(t *testing.T) {
	town := t.TempDir()
	mustMkdir(t, filepath.Join(town, ".beads"))
	mustMkdir(t, filepath.Join(town, "liveop", ".beads"))

	if routedIntoRig(town, "liveop", "live-op-agy") {
		t.Error("accepted a bead with no route table to justify it")
	}
	for _, empty := range [][3]string{{"", "liveop", "live-op-agy"}, {town, "", "live-op-agy"}, {town, "liveop", ""}} {
		if routedIntoRig(empty[0], empty[1], empty[2]) {
			t.Errorf("accepted on empty input %v", empty)
		}
	}
}

// TestRoutedIntoRig_RigResolvingToTownStore is the hole codex found in the
// first version of this guard, kept as a regression.
//
// The original compared the dir ResolveBeadsDirForID returned against the rig's
// dir. That function falls back to the directory it was handed when NO route
// matches, so an unroutable bead resolves to the town store -- and a rig whose
// own store also resolves to the town store then compared EQUAL, accepting
// every unroutable bead for that rig and silently undoing gt-el4.
//
// The bug needed two conditions to coincide, so neither the legacy-accept test
// nor the cross-rig test could see it: both use rigs with their own stores.
func TestRoutedIntoRig_RigResolvingToTownStore(t *testing.T) {
	town := t.TempDir()
	mustMkdir(t, filepath.Join(town, ".beads"))
	// A rig whose store REDIRECTS to the town store. A rig with merely no
	// .beads does NOT do this -- ResolveBeadsDir returns <rig>/.beads when there
	// is no redirect file, so the first version of this test never created the
	// condition it was named for and passed against the buggy guard.
	mustMkdir(t, filepath.Join(town, "husk", ".beads"))
	mustWrite(t, filepath.Join(town, "husk", ".beads", "redirect"), "../.beads")
	mustWrite(t, filepath.Join(town, ".beads", "routes.jsonl"),
		`{"prefix":"gt-","path":"."}
{"prefix":"liveop-","path":"liveop/mayor/rig"}
`)

	for _, bead := range []string{
		"totallyunknown-xyz", // no route at all
		"gt-vkv9",            // a town-level route
		"liveop-jn2",         // another rig's bead
		"nodashhere",         // no extractable prefix
	} {
		if routedIntoRig(town, "husk", bead) {
			t.Errorf("accepted %q for a rig that resolves to the town store; "+
				"this is the cross-rig guard being opened, not widened", bead)
		}
	}
}

// TestRoutedIntoRig_TownLevelRouteIsNeverARigAcceptance pins the narrower rule
// on its own: a route whose path is "." describes the town store, and no rig
// should be accepted through it regardless of how that rig resolves.
func TestRoutedIntoRig_TownLevelRouteIsNeverARigAcceptance(t *testing.T) {
	town := newRoutedTown(t)
	for _, bead := range []string{"gt-vkv9", "hq-cv-abc"} {
		if routedIntoRig(town, "liveop", bead) {
			t.Errorf("town-level bead %q accepted for rig liveop", bead)
		}
	}
}
