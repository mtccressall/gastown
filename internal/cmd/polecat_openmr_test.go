package cmd

import "testing"

// A recorded MR id used to pin its polecat unconditionally, with the blocker
// string literally reading "status=unknown" — the code recorded that it did not
// know, then blocked anyway. An MR written once and later merged held its worker
// forever.
//
// Measured 2026-09-03 on liveop: `gt refinery ready --all` reported an EMPTY
// queue while polecats sat idle-pr-open. Freeing them surfaced real work that the
// phantom had been masking — guzzle held 362 unpushed insertions of planning
// code, which is exactly the work that must never be reused away.
//
// The three states must stay distinct. In particular NIL is not EMPTY: a failed
// lookup must not read as "the queue is empty, so everything is closed".
func TestOpenMRSetStatus(t *testing.T) {
	cases := []struct {
		name string
		set  openMRSet
		mr   string
		want mrStatus
	}{
		{
			name: "lookup never ran — unknown, so the caller keeps blocking",
			set:  openMRSet{},
			mr:   "gt-abc",
			want: mrStatusUnknown,
		},
		{
			name: "lookup failed, ids nil — unknown, NOT closed",
			// The dangerous confusion: nil must never be read as an empty queue.
			set:  openMRSet{ids: nil, loaded: false},
			mr:   "gt-abc",
			want: mrStatusUnknown,
		},
		{
			name: "queue genuinely empty — the MR is closed and must not block",
			set:  openMRSet{ids: map[string]bool{}, loaded: true},
			mr:   "gt-abc",
			want: mrStatusClosed,
		},
		{
			name: "MR present — still open, keep blocking",
			set:  openMRSet{ids: map[string]bool{"gt-abc": true}, loaded: true},
			mr:   "gt-abc",
			want: mrStatusOpen,
		},
		{
			name: "different MR open — this one is closed",
			set:  openMRSet{ids: map[string]bool{"gt-other": true}, loaded: true},
			mr:   "gt-abc",
			want: mrStatusClosed,
		},
		{
			name: "surrounding whitespace must still match",
			set:  openMRSet{ids: map[string]bool{"gt-abc": true}, loaded: true},
			mr:   "  gt-abc  ",
			want: mrStatusOpen,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.set.statusOf(tc.mr); got != tc.want {
				t.Fatalf("statusOf(%q) = %v, want %v", tc.mr, got, tc.want)
			}
		})
	}
}
