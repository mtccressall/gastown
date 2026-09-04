package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The daemon's recovery heartbeat must never call the health path that runs the
// beads read probe.
//
// That probe issues beadsProbeSamples sequential bd reads, each bounded at 30s,
// so it can block its caller for up to 150 SECONDS. It was called synchronously
// from ensureDoltServerRunning, meaning a degraded store would stall every
// subsequent agent recovery check for minutes — and a degraded store is the only
// condition under which the probe reports anything interesting. The diagnostic
// became the outage it was built to observe.
//
// Asserted against SOURCE because the cost is a property of the CALL PATH, which
// no unit test of either function can see. The PR that introduced it passed build,
// tests and a failure-set diff; all three are blind to this by construction.
func TestDaemonHeartbeatDoesNotRunTheBeadsProbe(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(".", "daemon.go"))
	if err != nil {
		t.Fatalf("reading daemon.go: %v", err)
	}
	text := string(src)

	// The slow variant, called as a bare word rather than the Fast one.
	slow := regexp.MustCompile(`doltserver\.GetHealthMetrics\(`)
	if slow.MatchString(text) {
		t.Error("daemon.go calls doltserver.GetHealthMetrics, which runs the " +
			"5x30s beads probe on the recovery heartbeat; use GetHealthMetricsFast")
	}

	if !strings.Contains(text, "doltserver.GetHealthMetricsFast(") {
		t.Error("daemon.go no longer calls GetHealthMetricsFast; if the health " +
			"snapshot moved, re-point this test rather than deleting it")
	}

	// And the probe must not be reachable directly either.
	if strings.Contains(text, "MeasureBeadsReadLatency") {
		t.Error("daemon.go calls MeasureBeadsReadLatency directly on a heartbeat path")
	}
}
