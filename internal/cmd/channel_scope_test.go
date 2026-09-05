package cmd

import (
	"testing"

	"github.com/steveyegge/gastown/internal/channelevents"
)

func TestResolveChannelRigPrecedence(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown", "liveop")

	t.Run("explicit flag wins over env", func(t *testing.T) {
		t.Setenv("GT_RIG", "liveop")
		got, err := resolveChannelRig(townRoot, "refinery", "gastown")
		if err != nil {
			t.Fatal(err)
		}
		if got != "gastown" {
			t.Errorf("rig = %q, want gastown", got)
		}
	})

	t.Run("env used when no flag", func(t *testing.T) {
		t.Setenv("GT_RIG", "liveop")
		got, err := resolveChannelRig(townRoot, "refinery", "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "liveop" {
			t.Errorf("rig = %q, want liveop", got)
		}
	})

	t.Run("town scope when nothing resolves", func(t *testing.T) {
		t.Setenv("GT_RIG", "")
		// The cwd is the package directory, which is not inside townRoot, so
		// cwd inference cannot match a registered rig.
		got, err := resolveChannelRig(townRoot, "refinery", "")
		if err != nil {
			t.Fatal(err)
		}
		if got != channelevents.TownScope {
			t.Errorf("rig = %q, want town scope", got)
		}
	})

	t.Run("unsafe names are rejected loudly", func(t *testing.T) {
		t.Setenv("GT_RIG", "")
		if _, err := resolveChannelRig(townRoot, "refinery", "../etc"); err == nil {
			t.Error("accepted a traversal in --rig")
		}
		t.Setenv("GT_RIG", "../etc")
		if _, err := resolveChannelRig(townRoot, "refinery", ""); err == nil {
			t.Error("accepted a traversal in GT_RIG")
		}
	})
}

// TestUnregisteredRigFromEnvIsHonored documents a deliberate asymmetry: an
// explicit --rig or GT_RIG is taken at its word, while a cwd-derived name must
// be a registered rig. Standing in a directory is weak evidence of intent;
// naming the rig is not, and requiring registration for the explicit form would
// break rig setup, where the channel is used before the rig is registered.
func TestUnregisteredRigFromEnvIsHonored(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown")
	t.Setenv("GT_RIG", "brandnew")

	got, err := resolveChannelRig(townRoot, "refinery", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "brandnew" {
		t.Errorf("rig = %q, want brandnew", got)
	}
}

// TestTownScopedChannelIgnoresAmbientRig is the regression test for the codex
// P1 on this change. The resolver used to return whatever rig was ambient for
// EVERY channel, so `emit-event --channel mayor` from inside a rig — or with
// GT_RIG set — wrote to events/rigs/<rig>/mayor while the mayor watches
// events/mayor. The wake is then lost with no error at either end, which is
// the same silent-misdelivery failure as the bug this change fixes, pointed
// the other way.
func TestTownScopedChannelIgnoresAmbientRig(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown")
	t.Setenv("GT_RIG", "gastown")

	got, err := resolveChannelRig(townRoot, "mayor", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != channelevents.TownScope {
		t.Errorf("mayor channel resolved to rig %q; the mayor watches the town-scoped directory", got)
	}
}

// TestTownScopedChannelRejectsExplicitRig pins that the contradiction is
// refused rather than ignored. A silently dropped flag is indistinguishable
// from a flag that worked.
func TestTownScopedChannelRejectsExplicitRig(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown")
	t.Setenv("GT_RIG", "")

	if _, err := resolveChannelRig(townRoot, "mayor", "gastown"); err == nil {
		t.Error("--rig on a town-scoped channel was accepted silently")
	}
}

// TestUnclassifiedChannelKeepsTownScope pins the compatibility guarantee: this
// change reroutes ONLY the channels named in channelScopes. A custom channel
// that exists today keeps events/<channel> even when GT_RIG is set or the
// command runs inside a rig — otherwise shipping this would move its events on
// upgrade, and an emitter and consumer running in different contexts would
// strand with no error at either end.
func TestUnclassifiedChannelKeepsTownScope(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown")
	t.Setenv("GT_RIG", "gastown")

	got, err := resolveChannelRig(townRoot, "some-ad-hoc-channel", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != channelevents.TownScope {
		t.Errorf("unclassified channel resolved to rig %q; it must keep its pre-scoping path", got)
	}
}

// TestUnclassifiedChannelScopesOnExplicitRig pins the opt-in: naming a rig is
// how a custom channel becomes rig-scoped.
func TestUnclassifiedChannelScopesOnExplicitRig(t *testing.T) {
	townRoot := t.TempDir()
	writeRigsConfig(t, townRoot, "gastown")
	t.Setenv("GT_RIG", "")

	got, err := resolveChannelRig(townRoot, "some-ad-hoc-channel", "gastown")
	if err != nil {
		t.Fatal(err)
	}
	if got != "gastown" {
		t.Errorf("explicit --rig on a custom channel = %q, want gastown", got)
	}
}
