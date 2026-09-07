package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeRetentionFixture builds an events file whose head is a 30d type 20 days
// old while the newest 3d type reaches back only 2 days -- the interleaving a
// per-row prune leaves behind.
func writeRetentionFixture(t *testing.T, townRoot string) {
	t.Helper()
	now := time.Now()
	rows := []struct {
		ago time.Duration
		typ string
	}{
		{20 * 24 * time.Hour, "mail"},
		{71 * time.Hour, "nudge"},
		{1 * time.Hour, "nudge"},
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(fmt.Sprintf(`{"ts":%q,"type":%q,"actor":"test"}`+"\n",
			now.Add(-r.ago).UTC().Format(time.RFC3339), r.typ))
	}
	if err := os.WriteFile(filepath.Join(townRoot, ".events.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
}

// TestFeedRetentionBanner_FilteredAndPruned: filtered to a type the pruner is
// actively cutting, the banner must name that type's TTL and its own oldest
// surviving row, and must say the file's first row is NOT that type's horizon.
func TestFeedRetentionBanner_FilteredAndPruned(t *testing.T) {
	town := t.TempDir()
	writeRetentionFixture(t, town)

	got := feedRetentionBanner(town, "nudge")
	if got == "" {
		t.Fatal("banner is empty for a filtered read")
	}
	for _, want := range []string{
		`"nudge"`,
		"TTL 3d",
		"is being pruned at it", // the horizon is a ceiling, not a start
		"reaches back 2d23h",    // the type's own reach, not the head's
		"NOT this type's horizon",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("banner missing %q:\n%s", want, got)
		}
	}
	// The head must appear only as something the reader is told to disregard.
	if !strings.Contains(got, "first row is") {
		t.Errorf("banner does not state the file head at all:\n%s", got)
	}
}

// TestFeedRetentionBanner_FilteredUnderTTL: a type that has NOT reached its TTL
// has had nothing pruned, so its reach is where its data starts. Reporting that
// as a pruning ceiling is the same error pointed the other way.
func TestFeedRetentionBanner_FilteredUnderTTL(t *testing.T) {
	town := t.TempDir()
	writeRetentionFixture(t, town)

	got := feedRetentionBanner(town, "mail")
	if !strings.Contains(got, "has not reached it") {
		t.Errorf("banner claims a pruning ceiling for a type under its TTL:\n%s", got)
	}
	if strings.Contains(got, "is being pruned at it") {
		t.Errorf("banner says a 30d type 20d old is being pruned:\n%s", got)
	}
}

// TestFeedRetentionBanner_HeadType: for the one type whose oldest row IS the
// file's first row, the head is a correct horizon. The banner must not tell
// that reader to disregard it.
func TestFeedRetentionBanner_HeadType(t *testing.T) {
	town := t.TempDir()
	writeRetentionFixture(t, town)

	got := feedRetentionBanner(town, "mail")
	if strings.Contains(got, "NOT this type's horizon") {
		t.Errorf("banner disclaims the file head for the head type itself:\n%s", got)
	}
	if !strings.Contains(got, "the two agree") {
		t.Errorf("banner does not say head and horizon coincide here:\n%s", got)
	}
}

// TestFeedRetentionBanner_Unfiltered: with no type filter the banner cannot name
// one horizon, so it must say horizons differ per type and point at the command
// that prints them.
func TestFeedRetentionBanner_Unfiltered(t *testing.T) {
	town := t.TempDir()
	writeRetentionFixture(t, town)

	got := feedRetentionBanner(town, "")
	if got == "" {
		t.Fatal("banner is empty for an unfiltered read")
	}
	for _, want := range []string{"per type TTL", "OWN horizon", "gt krc window"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner missing %q:\n%s", want, got)
		}
	}
	// The spread is what makes the warning concrete rather than boilerplate.
	if !strings.Contains(got, "nudge") || !strings.Contains(got, "mail") {
		t.Errorf("banner does not name the shortest and longest horizons:\n%s", got)
	}
}

// TestFeedRetentionBanner_TypeWithNoRows: a type absent from the file is an
// absence of ROWS, not of the type. The banner must still state its TTL rather
// than going silent, because silence here reads as "no retention limit".
func TestFeedRetentionBanner_TypeWithNoRows(t *testing.T) {
	town := t.TempDir()
	writeRetentionFixture(t, town)

	got := feedRetentionBanner(town, "sling")
	if !strings.Contains(got, "sling") || !strings.Contains(got, "TTL 14d") {
		t.Errorf("banner for an unrepresented type = %q, want its TTL named", got)
	}
	if !strings.Contains(got, "no surviving rows") {
		t.Errorf("banner does not say the type has no rows:\n%s", got)
	}
}

// TestFeedRetentionBanner_NoEventsFile: a feed read must not break because the
// window could not be measured.
func TestFeedRetentionBanner_NoEventsFile(t *testing.T) {
	if got := feedRetentionBanner(t.TempDir(), ""); got != "" {
		t.Errorf("banner = %q for a town with no events file, want empty", got)
	}
}
