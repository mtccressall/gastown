package krc

// Retention window reporting.
//
// pruneFile is a PER ROW filter: it drops a row when now.Sub(ts) exceeds the
// TTL for THAT ROW's type. It is not a prefix cut. So after a prune pass the
// surviving rows of different types interleave exactly as they did before, and
// THE FILE'S HEAD IS UNTOUCHED -- the first line in the file is still whatever
// long-TTL row the file started with.
//
// A reader who sanity checks the window the obvious way, by taking the first
// line of the file, is therefore told the feed reaches back to the file start.
// That answer is correct for a 30d type and wrong by an order of magnitude for
// a 3d one, and nothing in the file, the tooling, or an error message says so.
//
// This file makes the real per-type horizon measurable, so a census cannot
// silently span mismatched horizons.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/steveyegge/gastown/internal/events"
)

// TypeWindow is the real retention horizon of a single event type in one file.
//
// Reach is the age of the OLDEST SURVIVING ROW of this type, which is the
// furthest back any census of this type can see. It is bounded above by TTL,
// and is shorter than TTL whenever the type simply has not been emitted for
// that long.
type TypeWindow struct {
	EventType string        `json:"event_type"`
	TTL       time.Duration `json:"ttl"`
	Count     int           `json:"count"`
	Oldest    time.Time     `json:"oldest"`
	Newest    time.Time     `json:"newest"`
	Reach     time.Duration `json:"reach"`

	// TTLBound says whether PRUNING is what limits this type's reach.
	//
	// Reach alone cannot answer that, and reading it as though it could is the
	// mirror of the error this file exists to fix. A type whose reach is well
	// under its TTL has had nothing pruned yet: its horizon is simply where its
	// data starts, and a longer TTL is neither confirmed nor refuted by it.
	// Only a TTL-bound type is one the pruner is actively cutting.
	//
	// The prune interval is the slack: a row is dropped when its age exceeds
	// the TTL, and the pruner runs periodically, so the oldest surviving row
	// sits within one interval of the TTL rather than exactly on it.
	TTLBound bool `json:"ttl_bound"`
}

// FileWindow is the per-type retention report for one JSONL event file.
type FileWindow struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Size     int64  `json:"size"`
	RowCount int    `json:"row_count"`

	// FirstRow is the first parsable row in FILE ORDER -- what `head -1` gives
	// a reader, and the number this report exists to contradict. It is NOT the
	// horizon of any type but its own.
	FirstRow     time.Time     `json:"first_row"`
	FirstRowType string        `json:"first_row_type"`
	FirstRowAge  time.Duration `json:"first_row_age"`

	// Types is sorted by Reach ascending: the shortest horizon, which is the
	// one most likely to mislead, is printed first.
	Types []TypeWindow `json:"types"`

	// Unparsable counts rows the scan could not read a type/timestamp from.
	// Reported so a small Types table cannot silently stand for a big file.
	Unparsable int       `json:"unparsable"`
	MeasuredAt time.Time `json:"measured_at"`
}

// Type returns the window for one event type, or nil if the file holds no
// surviving rows of it. A nil result is an absence of ROWS, never an absence
// of the type from the TTL table -- use Config.GetTTL for that.
func (w *FileWindow) Type(eventType string) *TypeWindow {
	for i := range w.Types {
		if w.Types[i].EventType == eventType {
			return &w.Types[i]
		}
	}
	return nil
}

// IsHeadType reports whether this type's oldest surviving row IS the file's
// first row. For that one type the file head is a correct horizon, so a caller
// must not tell the reader to disregard it.
func (w *FileWindow) IsHeadType(t *TypeWindow) bool {
	return t != nil && !w.FirstRow.IsZero() && t.Oldest.Equal(w.FirstRow)
}

// ShortestReach returns the smallest per-type reach in the file.
func (w *FileWindow) ShortestReach() (TypeWindow, bool) {
	if len(w.Types) == 0 {
		return TypeWindow{}, false
	}
	return w.Types[0], true
}

// LongestReach returns the largest per-type reach in the file.
func (w *FileWindow) LongestReach() (TypeWindow, bool) {
	if len(w.Types) == 0 {
		return TypeWindow{}, false
	}
	return w.Types[len(w.Types)-1], true
}

// ScanFileWindow reads one JSONL event file and reports the real retention
// horizon of every event type in it.
func ScanFileWindow(filePath string, config *Config, now time.Time) (*FileWindow, error) {
	w := &FileWindow{Path: filePath, MeasuredAt: now}

	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return w, nil
	}
	if err != nil {
		return nil, err
	}
	w.Exists = true
	w.Size = info.Size()

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	byType := make(map[string]*TypeWindow)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		w.RowCount++

		var row struct {
			Timestamp string `json:"ts"`
			Type      string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			w.Unparsable++
			continue
		}
		ts, err := time.Parse(time.RFC3339, row.Timestamp)
		if err != nil {
			w.Unparsable++
			continue
		}

		// FILE ORDER, not minimum timestamp: this is the value a reader gets
		// from `head -1`, and reproducing that exactly is the point.
		if w.FirstRow.IsZero() {
			w.FirstRow = ts
			w.FirstRowType = row.Type
		}

		tw := byType[row.Type]
		if tw == nil {
			tw = &TypeWindow{EventType: row.Type, TTL: config.GetTTL(row.Type), Oldest: ts, Newest: ts}
			byType[row.Type] = tw
		}
		tw.Count++
		if ts.Before(tw.Oldest) {
			tw.Oldest = ts
		}
		if ts.After(tw.Newest) {
			tw.Newest = ts
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning %s: %w", filePath, err)
	}

	if !w.FirstRow.IsZero() {
		w.FirstRowAge = now.Sub(w.FirstRow)
	}

	for _, tw := range byType {
		tw.Reach = now.Sub(tw.Oldest)
		tw.TTLBound = tw.Reach+config.PruneInterval >= tw.TTL
		w.Types = append(w.Types, *tw)
	}
	// Shortest horizon first: that is the one a reader is most likely to
	// over-read, and the one a head-truncated table must not lose.
	sort.Slice(w.Types, func(i, j int) bool {
		if w.Types[i].Reach != w.Types[j].Reach {
			return w.Types[i].Reach < w.Types[j].Reach
		}
		return w.Types[i].EventType < w.Types[j].EventType
	})

	return w, nil
}

// GetWindows reports the retention window of both pruned files, events first.
// Both are returned even when absent, so a caller cannot mistake a missing
// file for a file with no rows.
func GetWindows(townRoot string, config *Config, now time.Time) ([]*FileWindow, error) {
	paths := []string{
		filepath.Join(townRoot, events.EventsFile),
		filepath.Join(townRoot, ".feed.jsonl"),
	}
	var out []*FileWindow
	for _, p := range paths {
		w, err := ScanFileWindow(p, config, now)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}
