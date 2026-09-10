package git

import (
	"strings"
	"sync"
)

// A memo for read-only `git ls-remote` answers, scoped to an explicitly opened
// window rather than to the process.
//
// gt-vkv9/gastown-o8q: a single scheduler dispatch pass walks every polecat in
// the town and asks the remote about each one's branch. It does that walk more
// than once -- the plan builds a capacity snapshot, then admission control
// builds another, and `validatePendingBeadForDispatch` can build a third -- so
// the same `ls-remote --heads origin polecat/<name>/<bead>` was measured firing
// three times for one polecat inside one pass. At ~1.1s of spawn and network
// round-trip each, 46-plus of those calls dominated a 300s deadline.
//
// The answers are identical by construction within one pass: the pass takes a
// snapshot of a moving system and every consumer inside it is entitled to the
// same snapshot. Memoizing them removes the repeats without changing what any
// caller sees.
//
// WHY THIS IS OPT-IN AND NOT A PROCESS-LIFETIME CACHE. A cache that is always
// on would serve a stale remote answer to a long-lived process (the daemon, an
// interactive session) with nothing to tell the caller its answer had aged.
// Requiring a caller to open the window makes the staleness boundary a written
// decision at one call site instead of an emergent property of the process.
//
// THE WINDOW MUST NOT SPAN AN OPERATION THAT CHANGES THE REMOTE. Every exec
// path in this package routes through `preExec`, which drops the memo when it
// sees a `push`, `fetch` or `remote` subcommand, so this is enforced rather than
// merely documented -- a guard that only lives in a comment is one refactor away
// from not existing.
//
// This sentence used to name `run` and `runWithTimeout` specifically, and that
// was the defect (gastown-vvq): the package had six exec paths taking
// caller-supplied args and the guard was copied into two of them, so
// `PushWithEnv` -> `runWithEnvAndTimeout` pushed straight through an open
// window. Naming the paths that pass is what let four that did not go unnoticed,
// which is why the guarantee is now stated over the chokepoint and pinned by
// TestEveryGitExecPathCallsPreExec rather than by a list of function names.

// remoteRefEntry is one memoized answer. The sync.Once collapses concurrent
// callers asking the same question onto a single round-trip, which is what the
// now-parallel capacity walk needs.
type remoteRefEntry struct {
	once sync.Once
	out  string
	err  error
}

var (
	remoteRefCacheMu sync.Mutex
	// remoteRefCacheDepth is a refcount, not a boolean: nested windows are
	// legitimate (a dispatch pass opening one around a capacity snapshot that
	// opens its own) and the inner End must not close the outer window.
	remoteRefCacheDepth   int
	remoteRefCacheEntries map[string]*remoteRefEntry
)

// BeginRemoteRefCache opens a memo window for read-only remote ref lookups.
// Every call must be paired with EndRemoteRefCache, normally by defer.
func BeginRemoteRefCache() {
	remoteRefCacheMu.Lock()
	defer remoteRefCacheMu.Unlock()
	remoteRefCacheDepth++
	if remoteRefCacheEntries == nil {
		remoteRefCacheEntries = make(map[string]*remoteRefEntry)
	}
}

// EndRemoteRefCache closes the window opened by the matching BeginRemoteRefCache.
// The memo is discarded when the outermost window closes.
func EndRemoteRefCache() {
	remoteRefCacheMu.Lock()
	defer remoteRefCacheMu.Unlock()
	if remoteRefCacheDepth == 0 {
		return
	}
	remoteRefCacheDepth--
	if remoteRefCacheDepth == 0 {
		remoteRefCacheEntries = nil
	}
}

// remoteRefCacheActive reports whether a window is currently open. Test-facing.
func remoteRefCacheActive() bool {
	remoteRefCacheMu.Lock()
	defer remoteRefCacheMu.Unlock()
	return remoteRefCacheDepth > 0
}

// invalidateRemoteRefCache drops every memoized answer while leaving the window
// open. A no-op when no window is open.
func invalidateRemoteRefCache() {
	remoteRefCacheMu.Lock()
	defer remoteRefCacheMu.Unlock()
	if remoteRefCacheEntries != nil {
		remoteRefCacheEntries = make(map[string]*remoteRefEntry)
	}
}

// remoteMutatingArgs reports whether args carry a git subcommand that can change
// what the remote holds. Anything else leaves the memo alone.
//
// The subcommand is resolved with gitSubcommand, the same parser
// guardUnsafeTownRootMutation uses, rather than by taking the first non-flag
// token. Those two rules disagree on every argument that CARRIES A VALUE:
// `git -C /repo push origin main` has "/repo" as its first non-flag token, so
// the first-token rule reads the subcommand as "/repo" and leaves a stale memo
// standing across a real push. Same for `-c`, `--git-dir`, `--work-tree`,
// `--namespace`, `--config-env` and `--exec-path`.
//
// Latent today -- measured, not assumed: no production caller passes a
// value-carrying global flag to one of this package's run methods alongside a
// push, fetch or remote. It is fixed here anyway because the two guards behind
// preExec must agree on what subcommand they were handed; a chokepoint whose
// two halves parse their input differently is a chokepoint in name only.
//
// This is a PREDICATE rather than an action so preExec can ask the question once
// and act on the answer twice -- dropping the memo before the subprocess and
// again after it, which is what closes the concurrent-read window a
// before-only drop leaves open.
func remoteMutatingArgs(args []string) bool {
	switch cmd, _ := gitSubcommand(args); cmd {
	case "push", "fetch", "remote":
		return true
	}
	return false
}

// lsRemote is the single path every read-only ls-remote query in this package
// goes through, so the memo cannot be bypassed by a new call site spelling the
// subprocess out for itself.
//
// The key includes the repository this Git wrapper points at: two rigs asking
// the same question of the same branch name are asking about different remotes.
//
// ERRORS ARE MEMOIZED TOO, deliberately. A remote that hangs costs
// remoteReadTimeout (60s) per call, and the case this exists to fix asked the
// same question three times in one pass -- caching the failure turns a 180s
// stall into a 60s one. It also keeps the pass self-consistent: three consumers
// of one snapshot should not disagree because the network recovered between two
// of their calls.
func (g *Git) lsRemote(args ...string) (string, error) {
	run := func() (string, error) {
		return g.runWithTimeout(remoteReadTimeout, append([]string{"ls-remote"}, args...)...)
	}

	remoteRefCacheMu.Lock()
	if remoteRefCacheDepth == 0 {
		remoteRefCacheMu.Unlock()
		return run()
	}
	key := g.gitDir + "\x00" + g.workDir + "\x00" + strings.Join(args, "\x00")
	entry := remoteRefCacheEntries[key]
	if entry == nil {
		entry = &remoteRefEntry{}
		remoteRefCacheEntries[key] = entry
	}
	remoteRefCacheMu.Unlock()

	// Outside the lock: the subprocess is the expensive part and holding the
	// mutex across it would serialise the very fan-out this exists to speed up.
	// sync.Once still collapses concurrent callers asking the same question,
	// which is the common case once the fan-out runs in parallel.
	entry.once.Do(func() { entry.out, entry.err = run() })
	return entry.out, entry.err
}
