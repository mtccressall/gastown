package mail

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The sender label must be normalised the same way the assignee is. They carried
// opposite conventions, which made CLAUDE.md's mail-parity check impossible to
// pass for a polecat: one bound name is used as an assignee value in one conjunct
// and a from-label value in the other (gt-xo2jz).
func TestFromLabelIsCanonicalLikeTheAssignee(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// The case that broke the parity check.
		{"liveop/polecats/atom", "from:liveop/atom"},
		{"gastown/polecats/obsidian", "from:gastown/obsidian"},
		{"gastown/crew/max", "from:gastown/max"},
		// Already-canonical senders must be untouched, or every non-polecat
		// correspondent's history stops matching.
		{"liveop/atom", "from:liveop/atom"},
		{"gastown/refinery", "from:gastown/refinery"},
		{"mayor/", "from:mayor/"},
		{"deacon/", "from:deacon/"},
		{"overseer", "from:overseer"},
	} {
		if got := fromLabel(tc.in); got != tc.want {
			t.Errorf("fromLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// THE CALL-SITE GUARD, and it is a source assertion on purpose.
//
// The fix had to touch FOUR sites, and I first believed there were three because
// a `head` truncated my own grep. A unit test on fromLabel cannot catch a fifth
// site appending a raw label later — that is the helper-not-the-call-site gap that
// has cost several PRs here. This asserts the invariant directly: nothing in this
// package builds a from: label without normalising.
func TestNoRawFromLabelConstructionRemains(t *testing.T) {
	b, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	// Matches `"from:"+something` other than inside fromLabel's own definition.
	raw := regexp.MustCompile(`"from:"\s*\+\s*msg\.`)
	body := string(b)
	if n := len(raw.FindAllString(body, -1)); n != 0 {
		t.Errorf("%d raw from: label construction(s) remain; use fromLabel() so the sender is normalised", n)
	}
	if !strings.Contains(body, "fromLabel(msg.From)") {
		t.Error("no call to fromLabel found — the normalisation is not wired in")
	}
}
