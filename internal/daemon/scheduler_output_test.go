package daemon

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestSchedulerOutputTail_EmptyIsExplicit pins the property the whole fix rests
// on being readable: "the pass printed nothing" must not render as the empty
// string, because that is byte-identical to a caller that dropped the output --
// the exact confusion gt-vkv9 was.
func TestSchedulerOutputTail_EmptyIsExplicit(t *testing.T) {
	got := schedulerOutputTail(nil)
	if got == "" {
		t.Fatal("empty output rendered as the empty string; a dropped output and " +
			"a silent pass are then indistinguishable in daemon.log")
	}
	if got != "<no output>" {
		t.Fatalf("schedulerOutputTail(nil) = %q, want %q", got, "<no output>")
	}
}

func TestSchedulerOutputTail_ShortOutputPassesThrough(t *testing.T) {
	if got := schedulerOutputTail([]byte("Dispatching liveop-cer")); got != "Dispatching liveop-cer" {
		t.Fatalf("got %q, want the output verbatim", got)
	}
}

// TestSchedulerOutputTail_KeepsTailNotHead is the one that matters for
// diagnosis: a timed-out pass is interesting for the LAST thing it printed,
// which is where it stopped. A head-truncating implementation passes a length
// check and answers the wrong question.
func TestSchedulerOutputTail_KeepsTailNotHead(t *testing.T) {
	big := strings.Repeat("A", 8000) + "STOPPED-HERE"
	got := schedulerOutputTail([]byte(big))

	if !strings.Contains(got, "STOPPED-HERE") {
		t.Error("truncated output dropped the tail; the last line before the kill " +
			"is the only part that says where the pass stopped")
	}
	if strings.Contains(got, "<no output>") {
		t.Error("non-empty output rendered as <no output>")
	}
	if len(got) > 4096+128 {
		t.Errorf("rendered %d bytes; the bound is meant to keep a runaway pass "+
			"out of daemon.log", len(got))
	}
	// The reader must be able to tell this was cut down, and from what.
	if !strings.Contains(got, "of 8012 bytes") {
		t.Errorf("truncation notice missing or wrong; got prefix %q", got[:min(80, len(got))])
	}
}

// TestSchedulerOutputTail_TruncatesOnRuneBoundary covers the case the byte
// bound creates: the cut lands at an arbitrary offset, and scheduler output is
// not ASCII -- its dispatch lines render as "Dispatching <bead> \u2192 <rig>",
// three bytes per arrow. A naive byte slice puts invalid UTF-8 into daemon.log.
//
// The body is ALL multibyte runes on purpose. An earlier version of this test
// used realistic mixed ASCII/arrow lines and PASSED against the naive slice:
// the 4096-byte cut happened to land in the ASCII run of every line, so it
// never exercised the defect it was named for. Padding is swept across the
// rune width so the cut lands on each byte of a 3-byte rune in turn.
func TestSchedulerOutputTail_TruncatesOnRuneBoundary(t *testing.T) {
	const marker = "END-OF-PASS"
	for pad := 0; pad < 3; pad++ {
		out := []byte(strings.Repeat("x", pad) + strings.Repeat("\u2192", 3000) + marker)
		if len(out) <= 4096 {
			t.Fatalf("pad=%d: test input is %d bytes, too short to truncate", pad, len(out))
		}

		got := schedulerOutputTail(out)
		if !utf8.ValidString(got) {
			t.Errorf("pad=%d: rendered invalid UTF-8; daemon.log would carry a broken rune", pad)
		}
		if !strings.HasSuffix(got, marker) {
			t.Errorf("pad=%d: boundary walk ate the tail, which is the part that says "+
				"where the pass stopped: %.40q", pad, got)
		}
	}
}

// TestCombinedOutputRetainsPartialOutputOnDeadline establishes the PREMISE of
// the fix rather than the fix itself.
//
// dispatchQueuedWork logs `out` on the DeadlineExceeded branch. That is only
// worth doing if exec.CommandContext + CombinedOutput actually returns what the
// child printed before the deadline killed it. If Go discarded it, the new log
// line would print "<no output>" on every timeout forever and would look like a
// working fix while telling nobody anything.
//
// So: run a child that prints, then hangs past a short deadline, and assert the
// print survives the kill.
//
// Note this test takes ~2s rather than the 300ms deadline, and that is not a
// flaw in it: `sh` forks the sleeper, which inherits the write end of the pipe
// CombinedOutput reads, so Wait cannot return until the grandchild exits. That
// is worth knowing about but it is NOT what gt-vkv9 is -- in production the
// timeout and success log lines are spaced identically (~8m30s, the daemon's
// tick), so the real dispatch pass does return at its deadline.
func TestCombinedOutputRetainsPartialOutputOnDeadline(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, sh, "-c", "echo dispatching-liveop-cer; sleep 2")
	out, _ := cmd.CombinedOutput()

	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("child was not killed by the deadline (ctx.Err() = %v); the test did not exercise the branch it claims to", ctx.Err())
	}
	if !strings.Contains(string(out), "dispatching-liveop-cer") {
		t.Fatalf("partial output lost on deadline kill: %q\n"+
			"dispatchQueuedWork's timeout branch would have nothing to log", out)
	}
}
