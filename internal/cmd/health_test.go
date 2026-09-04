package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBeadsStoreMode(t *testing.T) {
	tests := []struct {
		name         string
		contents     string
		writeFile    bool
		wantMode     string
		wantDatabase string
	}{
		{
			name:         "embedded",
			contents:     `{"backend":"dolt","dolt_mode":"embedded","dolt_database":"gt"}`,
			writeFile:    true,
			wantMode:     "embedded",
			wantDatabase: "gt",
		},
		{
			name:         "server",
			contents:     `{"backend":"dolt","dolt_mode":"server","dolt_database":"liveop"}`,
			writeFile:    true,
			wantMode:     "server",
			wantDatabase: "liveop",
		},
		{
			// A store with no metadata.json cannot be identified. Reporting an
			// empty mode is correct; guessing "server" would put it back in the
			// class of stores the Databases section claims to cover.
			name:      "missing metadata",
			writeFile: false,
		},
		{
			name:      "unparseable metadata",
			contents:  "not json",
			writeFile: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.writeFile {
				if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(tc.contents), 0o600); err != nil {
					t.Fatalf("writing metadata.json: %v", err)
				}
			}

			mode, database := readBeadsStoreMode(dir)
			if mode != tc.wantMode {
				t.Errorf("mode = %q, want %q", mode, tc.wantMode)
			}
			if database != tc.wantDatabase {
				t.Errorf("database = %q, want %q", database, tc.wantDatabase)
			}
		})
	}
}

// An uncounted store must never render as a store holding zero issues. That
// conflation is the defect this section exists to fix (gt-kei9): a true
// statement about the wrong target reads exactly like a healthy empty one.
func TestPrintBeadsStoresDistinguishesUncountedFromEmpty(t *testing.T) {
	empty := renderBeadsStores(t, &HealthReport{
		Server:      &ServerHealth{Running: true, Port: 3307},
		BeadsStores: []BeadsStoreHealth{{Scope: "town", Path: "/t/.beads", Mode: "server", UsesServer: true, Counted: true}},
	})
	uncounted := renderBeadsStores(t, &HealthReport{
		Server:      &ServerHealth{Running: true, Port: 3307},
		BeadsStores: []BeadsStoreHealth{{Scope: "town", Path: "/t/.beads", Mode: "server", UsesServer: true, Error: "bd stats: connection refused"}},
	})

	if empty == uncounted {
		t.Fatal("an empty store and an uncountable one render identically")
	}
	if !strings.Contains(empty, "0 issues") {
		t.Errorf("counted-but-empty store does not report a count:\n%s", empty)
	}
	if strings.Contains(uncounted, "0 issues") {
		t.Errorf("uncountable store reports a count of zero:\n%s", uncounted)
	}
	if !strings.Contains(uncounted, "connection refused") {
		t.Errorf("uncountable store does not surface the error:\n%s", uncounted)
	}
}

// A store bd opens in embedded mode is not served by the Dolt server, so the
// Databases section says nothing about it. The report has to say so.
func TestPrintBeadsStoresFlagsOffServerStores(t *testing.T) {
	offServer := renderBeadsStores(t, &HealthReport{
		Server: &ServerHealth{Running: true, Port: 3307},
		BeadsStores: []BeadsStoreHealth{
			{Scope: "town", Path: "/t/.beads", Mode: "embedded", Database: "gt", Counted: true, TotalIssues: 365, OpenIssues: 236},
			{Scope: "liveop", Path: "/t/liveop/.beads", Mode: "server", Database: "liveop", UsesServer: true, Counted: true, TotalIssues: 164},
		},
	})

	if !strings.Contains(offServer, "NOT served by the Dolt server") {
		t.Errorf("embedded store is not flagged as off-server:\n%s", offServer)
	}
	if !strings.Contains(offServer, "1 of 2") {
		t.Errorf("off-server count is not reported:\n%s", offServer)
	}

	allOnServer := renderBeadsStores(t, &HealthReport{
		Server: &ServerHealth{Running: true, Port: 3307},
		BeadsStores: []BeadsStoreHealth{
			{Scope: "town", Path: "/t/.beads", Mode: "server", UsesServer: true, Counted: true},
		},
	})
	if strings.Contains(allOnServer, "NOT served by the Dolt server") {
		t.Errorf("warning fired with every store on the server:\n%s", allOnServer)
	}
}

// renderBeadsStores captures printBeadsStores' stdout.
func renderBeadsStores(t *testing.T, r *HealthReport) string {
	t.Helper()

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = write

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := read.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()

	printBeadsStores(r)

	os.Stdout = orig
	if err := write.Close(); err != nil {
		t.Fatalf("closing pipe: %v", err)
	}
	out := <-done
	if err := read.Close(); err != nil {
		t.Fatalf("closing pipe reader: %v", err)
	}
	return out
}
