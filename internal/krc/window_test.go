package krc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeWindowFixture builds a JSONL event file whose rows interleave exactly
// the way a per-row prune leaves them: a long-TTL row at the head, and every
// short-TTL type reaching back only as far as its own TTL allows.
func writeWindowFixture(t *testing.T, path string, now time.Time) {
	t.Helper()
	rows := []struct {
		ago   time.Duration
		typ   string
		extra string
	}{
		// FILE HEAD: a 30d type, 29 days old. This is what `head -1` returns
		// and what a reader mistakes for the file's horizon.
		{29 * 24 * time.Hour, "mail", ""},
		{18 * 24 * time.Hour, "mail", ""},
		{6 * 24 * time.Hour, "sling", ""},
		// A 3d type sitting on its TTL: nothing older survives, and the
		// pruner is what set that.
		{71 * time.Hour, "nudge", ""},
		{1 * time.Hour, "nudge", ""},
		{30 * time.Minute, "nudge", ""},
		// A glob-matched 1d type, also on its TTL.
		{23*time.Hour + 30*time.Minute, "patrol_started", ""},
		{2 * time.Hour, "patrol_complete", ""},
	}

	var b strings.Builder
	for _, r := range rows {
		ts := now.Add(-r.ago).UTC().Format(time.RFC3339)
		b.WriteString(fmt.Sprintf(`{"ts":%q,"type":%q,"actor":"test"}`+"\n", ts, r.typ))
	}
	// Rows the scan cannot read: malformed JSON, and a valid row with an
	// unparsable timestamp. Both must be counted, never silently dropped.
	b.WriteString("{not json\n")
	b.WriteString(`{"ts":"not-a-timestamp","type":"nudge"}` + "\n")
	b.WriteString("\n") // blank lines are not rows

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
}

// TestScanFileWindow_HeadIsNotTheHorizon is the acceptance test for gastown-yu4:
// the file's own first timestamp and a 3d type's real horizon must disagree by
// an order of magnitude, and the scan must report both.
func TestScanFileWindow_HeadIsNotTheHorizon(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, ".events.jsonl")
	writeWindowFixture(t, path, now)

	w, err := ScanFileWindow(path, DefaultConfig(), now)
	if err != nil {
		t.Fatalf("ScanFileWindow: %v", err)
	}

	if !w.Exists {
		t.Fatal("Exists = false, want true")
	}

	// The head is file order, not minimum timestamp: it is what `head -1` gives.
	if got, want := w.FirstRowType, "mail"; got != want {
		t.Errorf("FirstRowType = %q, want %q", got, want)
	}
	if got, want := w.FirstRowAge, 29*24*time.Hour; got != want {
		t.Errorf("FirstRowAge = %v, want %v", got, want)
	}

	nudge := w.Type("nudge")
	if nudge == nil {
		t.Fatal("Type(\"nudge\") = nil, want a window")
	}
	if got, want := nudge.Reach, 71*time.Hour; got != want {
		t.Errorf("nudge Reach = %v, want %v", got, want)
	}
	if got, want := nudge.TTL, 3*24*time.Hour; got != want {
		t.Errorf("nudge TTL = %v, want %v", got, want)
	}
	if got, want := nudge.Count, 3; got != want {
		t.Errorf("nudge Count = %d, want %d", got, want)
	}

	// THE WHOLE FINDING, asserted: reading the file head as the nudge horizon
	// overstates it by 10x. If a future change made the per-type oldest fall
	// back to the file head, this is the assertion that fails.
	if nudge.Oldest.Equal(w.FirstRow) {
		t.Fatal("nudge Oldest equals the file head; the per-type horizon is not being measured")
	}
	if ratio := float64(w.FirstRowAge) / float64(nudge.Reach); ratio < 9 {
		t.Errorf("head/nudge horizon ratio = %.1fx, want >= 9x for this fixture", ratio)
	}
}

// TestScanFileWindow_TTLBound: REACH alone cannot say WHY a horizon is short.
// A type sitting on its TTL is being cut by the pruner; a type well under it
// has had nothing pruned and its reach is only where its data starts, which
// neither confirms nor refutes the TTL beside it.
func TestScanFileWindow_TTLBound(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), ".events.jsonl")
	writeWindowFixture(t, path, now)

	w, err := ScanFileWindow(path, DefaultConfig(), now)
	if err != nil {
		t.Fatalf("ScanFileWindow: %v", err)
	}

	for _, tc := range []struct {
		typ  string
		want bool
	}{
		{"nudge", true},            // reach 71h against a 3d TTL
		{"patrol_started", true},   // reach 23h30m against a 1d TTL
		{"mail", false},            // reach 29d against a 30d TTL
		{"sling", false},           // reach 6d against a 14d TTL
		{"patrol_complete", false}, // reach 2h against a 1d TTL
	} {
		tw := w.Type(tc.typ)
		if tw == nil {
			t.Fatalf("Type(%q) = nil", tc.typ)
		}
		if tw.TTLBound != tc.want {
			t.Errorf("%s TTLBound = %v, want %v (reach %v, TTL %v)",
				tc.typ, tw.TTLBound, tc.want, tw.Reach, tw.TTL)
		}
	}
}

// TestScanFileWindow_IsHeadType: for exactly one type the file head IS the
// horizon. Telling that type's reader to disregard the head would be the same
// false claim pointed the other way.
func TestScanFileWindow_IsHeadType(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), ".events.jsonl")
	writeWindowFixture(t, path, now)

	w, err := ScanFileWindow(path, DefaultConfig(), now)
	if err != nil {
		t.Fatalf("ScanFileWindow: %v", err)
	}

	if !w.IsHeadType(w.Type("mail")) {
		t.Error("IsHeadType(mail) = false; mail's oldest row IS the file head")
	}
	for _, typ := range []string{"nudge", "sling", "patrol_started"} {
		if w.IsHeadType(w.Type(typ)) {
			t.Errorf("IsHeadType(%s) = true, want false", typ)
		}
	}
	if w.IsHeadType(nil) {
		t.Error("IsHeadType(nil) = true")
	}
}

func TestScanFileWindow_GlobTTLAndSortOrder(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), ".events.jsonl")
	writeWindowFixture(t, path, now)

	w, err := ScanFileWindow(path, DefaultConfig(), now)
	if err != nil {
		t.Fatalf("ScanFileWindow: %v", err)
	}

	// patrol_* is a glob pattern in DefaultConfig; the scan must resolve it
	// per row rather than falling through to DefaultTTL.
	for _, typ := range []string{"patrol_started", "patrol_complete"} {
		tw := w.Type(typ)
		if tw == nil {
			t.Fatalf("Type(%q) = nil", typ)
		}
		if got, want := tw.TTL, 24*time.Hour; got != want {
			t.Errorf("%s TTL = %v, want %v (glob patrol_*)", typ, got, want)
		}
	}

	// Shortest horizon first: it is the one most likely to be over-read, and
	// the one a head-truncated table must not lose.
	for i := 1; i < len(w.Types); i++ {
		if w.Types[i-1].Reach > w.Types[i].Reach {
			t.Fatalf("Types not sorted by reach ascending: %v then %v",
				w.Types[i-1], w.Types[i])
		}
	}
	shortest, ok := w.ShortestReach()
	if !ok || shortest.EventType != "patrol_complete" {
		t.Errorf("ShortestReach = %+v, want patrol_complete", shortest)
	}
	longest, ok := w.LongestReach()
	if !ok || longest.EventType != "mail" {
		t.Errorf("LongestReach = %+v, want mail", longest)
	}
}

// TestScanFileWindow_UnparsableCounted guards the vacuous-scan failure: a small
// types table over a big file must say how many rows it could not read.
func TestScanFileWindow_UnparsableCounted(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), ".events.jsonl")
	writeWindowFixture(t, path, now)

	w, err := ScanFileWindow(path, DefaultConfig(), now)
	if err != nil {
		t.Fatalf("ScanFileWindow: %v", err)
	}
	if got, want := w.Unparsable, 2; got != want {
		t.Errorf("Unparsable = %d, want %d", got, want)
	}
	if got, want := w.RowCount, 10; got != want {
		t.Errorf("RowCount = %d, want %d (blank lines are not rows)", got, want)
	}
	var counted int
	for _, tw := range w.Types {
		counted += tw.Count
	}
	if counted+w.Unparsable != w.RowCount {
		t.Errorf("per-type counts %d + unparsable %d != RowCount %d",
			counted, w.Unparsable, w.RowCount)
	}
}

// TestScanFileWindow_MissingFile: an absent file must be distinguishable from a
// file with no rows. Both are "empty" and only one is a fault.
func TestScanFileWindow_MissingFile(t *testing.T) {
	now := time.Now()
	w, err := ScanFileWindow(filepath.Join(t.TempDir(), "nope.jsonl"), DefaultConfig(), now)
	if err != nil {
		t.Fatalf("ScanFileWindow on missing file: %v", err)
	}
	if w.Exists {
		t.Error("Exists = true for a missing file")
	}
	if w.Type("nudge") != nil {
		t.Error("Type() returned a window from a missing file")
	}
	if _, ok := w.ShortestReach(); ok {
		t.Error("ShortestReach ok = true with no types")
	}
}

func TestScanFileWindow_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".events.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := ScanFileWindow(path, DefaultConfig(), time.Now())
	if err != nil {
		t.Fatalf("ScanFileWindow: %v", err)
	}
	if !w.Exists {
		t.Error("Exists = false for an existing empty file")
	}
	if w.RowCount != 0 || !w.FirstRow.IsZero() {
		t.Errorf("RowCount = %d, FirstRow = %v; want 0 and zero time", w.RowCount, w.FirstRow)
	}
}

// TestGetWindows_ReportsBothFiles: both pruned files are reported even when one
// is absent, so a caller cannot mistake a missing file for an empty one.
func TestGetWindows_ReportsBothFiles(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	town := t.TempDir()
	writeWindowFixture(t, filepath.Join(town, ".events.jsonl"), now)

	windows, err := GetWindows(town, DefaultConfig(), now)
	if err != nil {
		t.Fatalf("GetWindows: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("GetWindows returned %d files, want 2", len(windows))
	}
	if !strings.HasSuffix(windows[0].Path, ".events.jsonl") || !windows[0].Exists {
		t.Errorf("first window = %s exists=%v, want the events file present",
			windows[0].Path, windows[0].Exists)
	}
	if !strings.HasSuffix(windows[1].Path, ".feed.jsonl") || windows[1].Exists {
		t.Errorf("second window = %s exists=%v, want the feed file absent",
			windows[1].Path, windows[1].Exists)
	}
}
