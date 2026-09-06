package daemon

import (
	"bytes"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installFakeGT puts a `gt` on PATH that records each invocation and then holds
// the child open for holdSeconds, so a test can observe a pass that is still
// running. It writes the log line BEFORE sleeping: a test that waits for the log
// is waiting for "the child started", not for "the child finished".
//
// holdSeconds is a shell literal rather than a time.Duration on purpose. The
// first version of this helper formatted a Duration into `sleep 2se-3`, which
// sh rejects -- so the child exited immediately and both tests below passed
// while asserting nothing about a running pass.
func installFakeGT(t *testing.T, logPath, holdSeconds string) {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake gt bin: %v", err)
	}
	script := "#!/bin/sh\necho \"$*\" >> " + logPath + "\nsleep " + holdSeconds + "\nexit 0\n"
	gtPath := filepath.Join(binDir, "gt")
	if err := os.WriteFile(gtPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gt: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Positive control on the fixture itself: prove the script the tests depend
	// on actually runs and holds. A fixture that fails fast makes every
	// "still in flight" assertion below vacuously true.
	probeScript := "#!/bin/sh\nsleep " + holdSeconds + "\nexit 0\n"
	probePath := filepath.Join(binDir, "gt-probe")
	if err := os.WriteFile(probePath, []byte(probeScript), 0o755); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	start := time.Now()
	cmd := exec.Command(probePath)
	if err := cmd.Run(); err != nil {
		t.Fatalf("fake gt script does not run in this shell: %v", err)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatalf("fake gt returned in %v; it is not holding the child open, so the "+
			"in-flight assertions below would be vacuous", time.Since(start))
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read %s: %v", path, err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// gastown-o8q: dispatchQueuedWork was called INLINE from heartbeat(), so the
// heartbeat could not finish until the dispatch pass did. With passes routinely
// burning their whole 5-minute deadline, the town's recovery cadence -- dead
// session restart, GUPP checks, orphaned work -- inflated from 3 minutes to 8.7.
// That is the safety net being gated on the throughput of something it does not
// depend on.
//
// The assertion is that the call RETURNS while the child is still running. It is
// timing-based, which is normally a smell, but the property under test is
// literally "does not block", and the margin is two orders of magnitude.
func TestDispatchQueuedWorkAsyncDoesNotBlockTheHeartbeat(t *testing.T) {
	townRoot := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "gt.log")
	installFakeGT(t, logPath, "2")

	d := &Daemon{
		config: &Config{TownRoot: townRoot},
		logger: log.New(io.Discard, "", 0),
	}

	start := time.Now()
	d.dispatchQueuedWorkAsync()
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("dispatchQueuedWorkAsync blocked for %v while the pass ran; the heartbeat "+
			"is still gated on dispatch latency (gastown-o8q)", elapsed)
	}
	if !d.dispatchInFlight.Load() {
		t.Fatal("in-flight flag not set while a pass is running; the guard cannot work")
	}

	// Positive control: the child really was launched. Without this the timing
	// assertion above is satisfied just as well by a function that does nothing.
	deadline := time.Now().Add(5 * time.Second)
	for countLines(t, logPath) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if countLines(t, logPath) == 0 {
		t.Fatal("no dispatch pass was launched; this test is measuring an empty function")
	}
}

// The guard exists because the pass can outlive the heartbeat interval. Without
// it a 5-minute pass against a 3-minute heartbeat leaves two children racing for
// the scheduler's exclusive flock at all times, and the loser's work is waste.
func TestDispatchQueuedWorkAsyncSkipsWhileAPassIsRunning(t *testing.T) {
	townRoot := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "gt.log")
	installFakeGT(t, logPath, "2")

	var logs bytes.Buffer
	d := &Daemon{
		config: &Config{TownRoot: townRoot},
		logger: log.New(&logs, "", 0),
	}

	d.dispatchQueuedWorkAsync()
	// Wait until the first child has actually started, so the second call is
	// definitely racing a live pass rather than an unstarted goroutine.
	deadline := time.Now().Add(5 * time.Second)
	for countLines(t, logPath) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if countLines(t, logPath) != 1 {
		t.Fatalf("first pass did not start exactly once (log has %d lines)", countLines(t, logPath))
	}

	d.dispatchQueuedWorkAsync()
	d.dispatchQueuedWorkAsync()

	if n := countLines(t, logPath); n != 1 {
		t.Fatalf("%d dispatch passes launched while one was in flight, want 1", n)
	}
	if !strings.Contains(logs.String(), "previous pass still running") {
		t.Errorf("skipped pass was not logged; a silent skip is indistinguishable from "+
			"a pass that ran and found nothing. log: %q", logs.String())
	}

	// And the guard must clear, or dispatch stops forever after the first pass.
	deadline = time.Now().Add(10 * time.Second)
	for d.dispatchInFlight.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if d.dispatchInFlight.Load() {
		t.Fatal("in-flight flag never cleared; dispatch would be wedged shut permanently")
	}

	d.dispatchQueuedWorkAsync()
	deadline = time.Now().Add(5 * time.Second)
	for countLines(t, logPath) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := countLines(t, logPath); n != 2 {
		t.Fatalf("after the pass finished, a new dispatch launched %d total passes, want 2", n)
	}
}
