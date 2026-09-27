package mail

import "testing"

// The inbox header prints Mailbox.Identity(), and every assignee query uses it.
// This pins the normalisation that makes those two the same thing: a polecat's
// ADDRESS is "<rig>/polecats/<name>" and the assignee field stores "<rig>/<name>".
//
// Printing the un-normalised address made the header disagree with the store, and
// CLAUDE.md prescribes an EQUALITY predicate for "your mail" — so a reader who fed
// it the name the header printed got a clean zero at rc=0. Measured 198 gt:message
// beads at "liveop/atom" against 0 at "liveop/polecats/atom" (gt-xo2jz).
func TestAddressToIdentityNormalisesRoleSubpaths(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// The case that produced the false empty.
		{"liveop/polecats/atom", "liveop/atom"},
		{"gastown/polecats/Toast", "gastown/Toast"},
		// crew and the legacy singular form normalise the same way.
		{"gastown/crew/max", "gastown/max"},
		{"gastown/polecat/opal", "gastown/opal"},
		// Already-canonical forms must be untouched, or the fix would break
		// every non-polecat mailbox.
		{"liveop/atom", "liveop/atom"},
		{"gastown/refinery", "gastown/refinery"},
		{"gastown/witness", "gastown/witness"},
		// Town singletons keep their trailing slash.
		{"mayor/", "mayor/"},
		{"deacon/", "deacon/"},
		{"overseer", "overseer"},
	} {
		if got := AddressToIdentity(tc.in); got != tc.want {
			t.Errorf("AddressToIdentity(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The identity a mailbox queries by must be the canonical form, because that is
// what the header now prints and what a reader will copy into a bd query.
func TestMailboxIdentityIsCanonicalForAPolecatAddress(t *testing.T) {
	m := NewMailboxFromAddress("liveop/polecats/atom", t.TempDir())
	if got := m.Identity(); got != "liveop/atom" {
		t.Errorf("Mailbox.Identity() = %q, want liveop/atom — the header would print a name no assignee holds", got)
	}
}
