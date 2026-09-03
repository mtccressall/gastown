package daemon

import (
	"os"
	"strings"
	"testing"
)

func readDaemonSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("daemon.go")
	if err != nil {
		t.Fatalf("reading daemon.go: %v", err)
	}
	return string(b)
}

func countOccurrences(haystack, needle string) int {
	return strings.Count(haystack, needle)
}
