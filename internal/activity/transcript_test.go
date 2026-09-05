package activity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTranscript creates a transcript file whose records carry the given
// embedded timestamps, then stamps the file's mtime independently. The two are
// deliberately decoupled: mtime must never influence the result.
func writeTranscript(t *testing.T, dir, name string, mtime time.Time, stamps ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	for _, ts := range stamps {
		fmt.Fprintf(&b, "{\"type\":\"assistant\",\"timestamp\":%q}\n", ts)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	return path
}

func TestProjectDirName(t *testing.T) {
	tests := []struct {
		workDir string
		want    string
	}{
		{"/home/u/gt/gastown/witness", "-home-u-gt-gastown-witness"},
		{"/home/u/gt/my_rig/witness", "-home-u-gt-my-rig-witness"},
		{"relative/path", "relative-path"},
	}
	for _, tc := range tests {
		if got := ProjectDirName(tc.workDir); got != tc.want {
			t.Errorf("ProjectDirName(%q) = %q, want %q", tc.workDir, got, tc.want)
		}
	}
}

// TestLastRecordIgnoresMtime is the regression test for gastown-7k9 / gt-4vt7.
//
// The frozen gastown witness had a transcript mtime age of 0 minutes against a
// 9.6 hour gap in its actual records. A last-activity signal derived from mtime
// would have ranked the most frozen session in town as the healthiest, so this
// asserts the newest EMBEDDED timestamp wins even when mtime says otherwise.
func TestLastRecordIgnoresMtime(t *testing.T) {
	configDir := t.TempDir()
	workDir := "/town/gastown/witness"
	dir := ProjectDir(configDir, workDir)

	// The frozen session: records are 9.6h old, but the file was touched now.
	frozen := time.Now().UTC().Add(-576 * time.Minute).Truncate(time.Second)
	writeTranscript(t, dir, "frozen.jsonl", time.Now(), frozen.Format(time.RFC3339))

	rec, err := LastRecord([]string{configDir}, []string{workDir})
	if err != nil {
		t.Fatalf("LastRecord: %v", err)
	}
	if !rec.Time.Equal(frozen) {
		t.Errorf("LastRecord returned %s, want the embedded %s (mtime must not win)", rec.Time, frozen)
	}
}

// TestLastRecordPicksNewestAcrossFiles covers the multi-transcript case: a
// session directory holds one file per session, and the newest record can live
// in a file whose mtime is not the newest.
func TestLastRecordPicksNewestAcrossFiles(t *testing.T) {
	configDir := t.TempDir()
	workDir := "/town/liveop/refinery/rig"
	dir := ProjectDir(configDir, workDir)

	newest := time.Date(2026, 9, 5, 1, 48, 20, 0, time.UTC)
	older := time.Date(2026, 9, 4, 15, 45, 11, 0, time.UTC)

	// The file carrying the NEWEST record has the OLDEST mtime.
	writeTranscript(t, dir, "a.jsonl", time.Now().Add(-72*time.Hour), newest.Format(time.RFC3339))
	writeTranscript(t, dir, "b.jsonl", time.Now(), older.Format(time.RFC3339))

	rec, err := LastRecord([]string{configDir}, []string{workDir})
	if err != nil {
		t.Fatalf("LastRecord: %v", err)
	}
	if !rec.Time.Equal(newest) {
		t.Errorf("LastRecord = %s, want %s", rec.Time, newest)
	}
	if filepath.Base(rec.File) != "a.jsonl" {
		t.Errorf("Record.File = %q, want a.jsonl", rec.File)
	}
}

// TestLastRecordSearchesAllConfigDirs covers agents whose account resolves to a
// non-default CLAUDE_CONFIG_DIR.
func TestLastRecordSearchesAllConfigDirs(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	workDir := "/town/gastown/refinery/rig"

	older := time.Date(2026, 9, 4, 15, 45, 11, 0, time.UTC)
	newer := time.Date(2026, 9, 5, 1, 52, 43, 0, time.UTC)
	writeTranscript(t, ProjectDir(first, workDir), "a.jsonl", time.Time{}, older.Format(time.RFC3339))
	writeTranscript(t, ProjectDir(second, workDir), "b.jsonl", time.Time{}, newer.Format(time.RFC3339))

	rec, err := LastRecord([]string{first, second, "", first}, []string{workDir})
	if err != nil {
		t.Fatalf("LastRecord: %v", err)
	}
	if !rec.Time.Equal(newer) {
		t.Errorf("LastRecord = %s, want %s", rec.Time, newer)
	}
}

// TestLastRecordNoRecords asserts an unmeasurable age is reported as an error
// and never as a zero (which a caller would render as "just now" or "1970").
func TestLastRecordNoRecords(t *testing.T) {
	configDir := t.TempDir()
	workDir := "/town/gastown/witness"
	dir := ProjectDir(configDir, workDir)

	// A present-but-useless transcript: no record carries a timestamp.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.jsonl"), []byte("{\"type\":\"summary\"}\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := LastRecord([]string{configDir}, []string{workDir}); !errors.Is(err, ErrNoRecords) {
		t.Errorf("LastRecord error = %v, want ErrNoRecords", err)
	}
}

func TestLastRecordMissingDir(t *testing.T) {
	if _, err := LastRecord([]string{t.TempDir()}, []string{"/town/nope/witness"}); !errors.Is(err, ErrNoRecords) {
		t.Errorf("LastRecord error = %v, want ErrNoRecords", err)
	}
}

// TestLastRecordFallsBackToFullScan covers a transcript whose tail window holds
// no timestamped record: the newest one is far enough back that only the
// full-file scan can find it.
func TestLastRecordFallsBackToFullScan(t *testing.T) {
	configDir := t.TempDir()
	workDir := "/town/gastown/witness"
	dir := ProjectDir(configDir, workDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	want := time.Date(2026, 9, 5, 1, 48, 20, 0, time.UTC)
	var b strings.Builder
	fmt.Fprintf(&b, "{\"type\":\"assistant\",\"timestamp\":%q}\n", want.Format(time.RFC3339))
	// Pad past the tail window with records that carry no timestamp.
	padding := strings.Repeat("x", 512)
	for b.Len() < tailWindow*2 {
		fmt.Fprintf(&b, "{\"type\":\"summary\",\"pad\":%q}\n", padding)
	}
	if err := os.WriteFile(filepath.Join(dir, "t.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	rec, err := LastRecord([]string{configDir}, []string{workDir})
	if err != nil {
		t.Fatalf("LastRecord: %v", err)
	}
	if !rec.Time.Equal(want) {
		t.Errorf("LastRecord = %s, want %s", rec.Time, want)
	}
}

// TestLastRecordSkipsMalformedLines asserts a corrupt record does not abort the
// scan, since a truncated write is normal at the head of a live transcript.
func TestLastRecordSkipsMalformedLines(t *testing.T) {
	configDir := t.TempDir()
	workDir := "/town/gastown/witness"
	dir := ProjectDir(configDir, workDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	want := time.Date(2026, 9, 5, 1, 48, 20, 0, time.UTC)
	body := "not json at all\n" +
		"{\"timestamp\":\"nonsense\"}\n" +
		fmt.Sprintf("{\"timestamp\":%q}\n", want.Format(time.RFC3339)) +
		"{\"type\":\"attachment\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "t.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	rec, err := LastRecord([]string{configDir}, []string{workDir})
	if err != nil {
		t.Fatalf("LastRecord: %v", err)
	}
	if !rec.Time.Equal(want) {
		t.Errorf("LastRecord = %s, want %s", rec.Time, want)
	}
}

func TestFormatAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "<1m"},
		{5 * time.Minute, "5m"},
		{576 * time.Minute, "9h"},
		{50 * time.Hour, "2d"},
	}
	for _, tc := range tests {
		if got := FormatAge(tc.d); got != tc.want {
			t.Errorf("FormatAge(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// TestLastRecordSearchesAllWorkDirs covers a live session whose real working
// directory differs from the one derived from the rig layout: the refinery
// falls back to mayor/rig when worktree repair fails, while leaving the
// refinery directory in place for the derived path to find.
func TestLastRecordSearchesAllWorkDirs(t *testing.T) {
	configDir := t.TempDir()
	actual := "/town/gastown/mayor/rig"
	derived := "/town/gastown/refinery/rig"

	want := time.Date(2026, 9, 5, 1, 52, 43, 0, time.UTC)
	writeTranscript(t, ProjectDir(configDir, actual), "a.jsonl", time.Time{}, want.Format(time.RFC3339))

	rec, err := LastRecord([]string{configDir}, []string{actual, derived})
	if err != nil {
		t.Fatalf("LastRecord: %v", err)
	}
	if !rec.Time.Equal(want) {
		t.Errorf("LastRecord = %s, want %s", rec.Time, want)
	}

	// The derived path alone finds nothing, which is the bug this guards.
	if _, err := LastRecord([]string{configDir}, []string{derived}); !errors.Is(err, ErrNoRecords) {
		t.Errorf("derived-only LastRecord error = %v, want ErrNoRecords", err)
	}
}
