package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/channelevents"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

// channelScope says whether a channel's consumer exists once per rig or once
// per town. THE CHANNEL DECIDES, not the caller's location: a consumer watches
// one directory, and if an emitter picks a different one the wake is lost with
// no error anywhere.
type channelScope int

const (
	// scopeUnknown is a channel this table does not classify. It keeps the
	// pre-scoping town-wide path, so this change cannot silently reroute a
	// channel nobody here knew about. Scoping one requires an explicit --rig,
	// or an entry in channelScopes.
	scopeUnknown channelScope = iota
	// scopeRig is a channel with one consumer per rig. Sharing one directory
	// between rigs is gt-a3qs.
	scopeRig
	// scopeTown is a channel with a single town-wide consumer. Rig-scoping one
	// scatters its events into directories nothing watches.
	scopeTown
)

// channelScopes classifies the channels this town actually runs. Add a channel
// here when you add its consumer — this table is the one place the two halves
// are declared together, and it is also the complete list of channels whose
// routing this change alters. Anything absent keeps its old path.
var channelScopes = map[string]channelScope{
	"refinery": scopeRig,  // one refinery per rig
	"witness":  scopeRig,  // one witness per rig
	"mayor":    scopeTown, // one mayor for the whole town
}

func scopeOf(channel string) channelScope {
	return channelScopes[channel]
}

// resolveChannelRig determines which rig's channel directory a command should
// use.
//
// A town-scoped channel is always town-scoped; an explicit --rig on one is a
// contradiction and is rejected rather than ignored, because a silently
// dropped flag looks exactly like a flag that worked. For every other channel
// the rig comes from, in descending order of explicitness:
//
//	--rig flag  >  GT_RIG env  >  the rig containing the cwd  >  town scope
//
// That ladder applies to channels classified scopeRig. An unclassified channel
// keeps town scope unless --rig names one explicitly, so shipping this cannot
// reroute a channel that is not in the table.
//
// A cwd-derived name counts only if it is a registered rig, so the mayor and
// deacon directories resolve to town scope rather than to a rig named "mayor".
//
// Falling back to town scope is the safe direction for a rig channel: every
// emitter is rig-scoped, so the town-scoped directory is one nothing writes to.
// The consumer sees no events and falls back to its timeout rather than reading
// — or deleting — another rig's. warnIfChannelUnscoped makes that visible
// instead of leaving it as a silent empty.
func resolveChannelRig(townRoot, channel, explicit string) (string, error) {
	if scopeOf(channel) == scopeTown {
		if explicit != "" {
			return "", fmt.Errorf("channel %q is town-scoped (a single town-wide consumer); "+
				"--rig %s would write where nothing watches", channel, explicit)
		}
		return channelevents.TownScope, nil
	}
	// An unclassified channel keeps the path it has always had. Inferring a rig
	// for it from ambient GT_RIG or the cwd would move an existing channel's
	// events the moment this binary ships, and if its emitter and consumer run
	// in different contexts they would strand with no error at either end —
	// which is this change's own failure mode, aimed at a channel nobody
	// reviewed. Scoping one is opt-in via --rig or channelScopes.
	if scopeOf(channel) == scopeUnknown && explicit == "" {
		return channelevents.TownScope, nil
	}
	if explicit != "" {
		if !channelevents.ValidRigName.MatchString(explicit) {
			return "", fmt.Errorf("invalid rig name %q: must match [a-zA-Z0-9_-]", explicit)
		}
		return explicit, nil
	}
	if env := strings.TrimSpace(os.Getenv("GT_RIG")); env != "" {
		if !channelevents.ValidRigName.MatchString(env) {
			return "", fmt.Errorf("invalid rig name %q in GT_RIG: must match [a-zA-Z0-9_-]", env)
		}
		return env, nil
	}
	if townRoot == "" {
		return channelevents.TownScope, nil
	}
	name, err := inferRigFromCwd(townRoot)
	if err != nil || name == "" {
		return channelevents.TownScope, nil
	}
	if !channelevents.ValidRigName.MatchString(name) || !isRegisteredRig(townRoot, name) {
		return channelevents.TownScope, nil
	}
	return name, nil
}

// resolveTownRootOrDefault finds the town root from the cwd, falling back to
// ~/gt. It mirrors the fallback inside channelevents.Emit so a caller that
// resolves the root itself lands in the same place.
func resolveTownRootOrDefault() string {
	townRoot, err := workspace.FindFromCwd()
	if err != nil || townRoot == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "gt")
	}
	return townRoot
}

// isRegisteredRig reports whether name appears in the town's rig registry.
// A missing or unreadable registry yields false, which resolves the caller to
// town scope — see resolveChannelRig for why that direction is the safe one.
func isRegisteredRig(townRoot, name string) bool {
	rigsConfig, err := config.LoadRigsConfig(filepath.Join(townRoot, "mayor", "rigs.json"))
	if err != nil || rigsConfig == nil {
		return false
	}
	_, ok := rigsConfig.Rigs[name]
	return ok
}

// warnIfChannelUnscoped prints a warning when a per-rig channel resolved to town
// scope. Without it the symptom is an event wait that simply never fires, which
// is indistinguishable from a genuinely quiet queue — the failure this town keeps
// paying for. Writes to stderr so --json output stays parseable.
func warnIfChannelUnscoped(channel, rig string) {
	if rig != channelevents.TownScope || scopeOf(channel) != scopeRig {
		return
	}
	fmt.Fprintf(os.Stderr, "%s channel %q is rig-scoped but no rig resolved "+
		"(pass --rig, set GT_RIG, or run from inside a rig). Watching the town-scoped "+
		"directory, which no emitter writes to — expect no events.\n",
		style.Dim.Render("⚠"), channel)
}
