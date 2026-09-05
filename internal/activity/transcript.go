package activity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file derives when an agent session last WROTE something.
//
// The point of it is what it refuses to do. A session's liveness cannot be read
// from tmux session existence (gt-fy6), and it cannot be read from the
// transcript file's mtime either: measured 2026-09-05, a witness frozen for 9.6
// hours had an mtime age of 0 minutes, which ranked the most frozen session in
// town as the healthiest. An mtime-derived signal fails toward the false green,
// which is worse than having no signal at all.
//
// So the only source used here is the timestamp EMBEDDED in each transcript
// record, which exists because the agent produced that record.

// ErrNoRecords means no transcript record carrying a timestamp could be found.
// Callers must surface this rather than substituting a zero time: an
// unmeasurable activity age and a recent one must never render the same.
var ErrNoRecords = errors.New("no transcript records with a timestamp")

// Record is the newest embedded transcript timestamp found for a session.
type Record struct {
	// Time is the record's own timestamp, in UTC. Never a file mtime.
	Time time.Time
	// File is the transcript that carried it, for auditability.
	File string
}

// tailWindow is how much of a transcript's tail is read first. Transcripts are
// appended in time order, so the newest record is almost always in the last few
// kilobytes; the full-file scan below is the fallback, not the common path.
const tailWindow = 64 * 1024

// maxLineBytes bounds a single JSONL record during the full-file fallback scan.
const maxLineBytes = 16 * 1024 * 1024

// ProjectDirName encodes a working directory the way Claude Code names its
// transcript directory: path separators and underscores both become hyphens,
// so a leading separator becomes a leading hyphen.
func ProjectDirName(workDir string) string {
	name := strings.ReplaceAll(workDir, "/", "-")
	return strings.ReplaceAll(name, "_", "-")
}

// ProjectDir returns the transcript directory for workDir under configDir.
func ProjectDir(configDir, workDir string) string {
	return filepath.Join(configDir, "projects", ProjectDirName(workDir))
}

// FormatAge renders a duration in the same short form the dashboard uses
// ("<1m", "5m", "2h", "1d").
func FormatAge(d time.Duration) string {
	return formatAge(d)
}

// LastRecord returns the newest embedded timestamp across every transcript for
// any of workDirs, searching each of configDirs. Every transcript in each
// directory is read: picking the "latest" file would itself be an mtime
// question, and mtime is exactly what this code exists to avoid.
//
// Both lists are supersets on purpose, because a caller often cannot know which
// config dir or working directory a live agent ended up under. That is safe: a
// transcript directory is named after its own working directory, so a wrong
// guess contributes nothing rather than another agent's records.
//
// Returns ErrNoRecords if no timestamped record was found anywhere.
func LastRecord(configDirs, workDirs []string) (Record, error) {
	var best Record
	seen := make(map[string]bool, len(configDirs)*len(workDirs))

	for _, configDir := range configDirs {
		if configDir == "" {
			continue
		}
		for _, workDir := range workDirs {
			if workDir == "" {
				continue
			}
			best = scanProjectDir(ProjectDir(configDir, workDir), seen, best)
		}
	}

	if best.Time.IsZero() {
		return Record{}, fmt.Errorf("%w for %s", ErrNoRecords, strings.Join(workDirs, ", "))
	}
	return best, nil
}

// scanProjectDir folds the newest record in one transcript directory into best.
func scanProjectDir(dir string, seen map[string]bool, best Record) Record {
	if seen[dir] {
		return best
	}
	seen[dir] = true

	entries, err := os.ReadDir(dir)
	if err != nil {
		return best // A missing or unreadable project dir simply has no records.
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		ts, err := lastTimestampInFile(path)
		if err != nil {
			continue
		}
		if ts.After(best.Time) {
			best = Record{Time: ts, File: path}
		}
	}
	return best
}

// timestamped is the only field of a transcript record this code reads.
type timestamped struct {
	Timestamp string `json:"timestamp"`
}

// lastTimestampInFile returns the newest embedded timestamp in one transcript.
func lastTimestampInFile(path string) (time.Time, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path comes from a directory listing, not user input
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return time.Time{}, err
	}
	size := info.Size()
	if size == 0 {
		return time.Time{}, ErrNoRecords
	}

	// Fast path: the tail almost always carries the newest record.
	window := int64(tailWindow)
	if window > size {
		window = size
	}
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, size-window); err != nil && !errors.Is(err, io.EOF) {
		return time.Time{}, err
	}
	lines := bytes.Split(buf, []byte("\n"))
	if size > window && len(lines) > 0 {
		lines = lines[1:] // The first line is truncated by the window boundary.
	}
	if ts, ok := newestIn(lines); ok {
		return ts, nil
	}

	// Fallback: no timestamp in the tail, so scan the whole file.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return time.Time{}, err
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), maxLineBytes)
	var newest time.Time
	for scanner.Scan() {
		if ts, ok := parseTimestamp(scanner.Bytes()); ok && ts.After(newest) {
			newest = ts
		}
	}
	// A scanner error (an over-long record) truncates the scan but does not
	// invalidate what was already read, so report a found timestamp either way.
	if newest.IsZero() {
		if err := scanner.Err(); err != nil {
			return time.Time{}, err
		}
		return time.Time{}, ErrNoRecords
	}
	return newest, nil
}

func newestIn(lines [][]byte) (time.Time, bool) {
	var newest time.Time
	for _, line := range lines {
		if ts, ok := parseTimestamp(line); ok && ts.After(newest) {
			newest = ts
		}
	}
	return newest, !newest.IsZero()
}

func parseTimestamp(line []byte) (time.Time, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return time.Time{}, false
	}
	var rec timestamped
	if err := json.Unmarshal(line, &rec); err != nil || rec.Timestamp == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
	if err != nil {
		return time.Time{}, false
	}
	return ts.UTC(), true
}
