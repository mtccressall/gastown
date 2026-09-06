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
// THE WINDOW MUST NOT SPAN AN OPERATION THAT CHANGES THE REMOTE. `run` and
// `runWithTimeout` drop the memo when they see a `push`, `fetch` or `remote`
// subcommand, so this is enforced rather than merely documented -- a guard that
// only lives in a comment is one refactor away from not existing.
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

// maybeInvalidateRemoteRefCache drops the memo when args carry a git subcommand
// that can change what the remote holds. The subcommand is the first non-flag
// token; anything else leaves the memo alone.
func maybeInvalidateRemoteRefCache(args []string) {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch arg {
		case "push", "fetch", "remote":
			invalidateRemoteRefCache()
		}
		return
	}
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
