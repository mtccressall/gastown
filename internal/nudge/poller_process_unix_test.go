//go:build !windows

package nudge

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestParseProcState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stat   string
		want   string
		wantOK bool
	}{
		{
			name:   "ordinary comm",
			stat:   "1221266 (gt) Z 1221265 1221266 0 0 -1 4194560 0 0",
			want:   "Z",
			wantOK: true,
		},
		{
			name:   "sleeping process",
			stat:   "973342 (dolt) S 1 973342 0 0 -1 1077936384 12345 0",
			want:   "S",
			wantOK: true,
		},
		{
			name:   "comm containing a space",
			stat:   "4242 (my odd proc) R 1 4242 0 0 -1 0 0 0",
			want:   "R",
			wantOK: true,
		},
		{
			name:   "comm containing a close paren",
			stat:   "4243 (weird)name) Z 1 4243 0 0 -1 0 0 0",
			want:   "Z",
			wantOK: true,
		},
		{
			name:   "comm containing both a space and parens",
			stat:   "4244 (sh (old) v2) D 1 4244 0 0 -1 0 0 0",
			want:   "D",
			wantOK: true,
		},
		{
			name:   "comm is only parens",
			stat:   "4245 (()) T 1 4245 0",
			want:   "T",
			wantOK: true,
		},
		{
			name:   "trailing newline after state",
			stat:   "4246 (short) Z\n",
			want:   "Z",
			wantOK: true,
		},
		{
			name:   "state is the last field",
			stat:   "4247 (short) I",
			want:   "I",
			wantOK: true,
		},
		{
			name:   "no closing paren",
			stat:   "4248 (truncated",
			wantOK: false,
		},
		{
			name:   "nothing after the comm",
			stat:   "4249 (bare)",
			wantOK: false,
		},
		{
			name:   "only whitespace after the comm",
			stat:   "4250 (bare)  \n",
			wantOK: false,
		},
		{
			name:   "empty input",
			stat:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseProcState(tt.stat)
			if ok != tt.wantOK {
				t.Fatalf("parseProcState(%q) ok = %v, want %v", tt.stat, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("parseProcState(%q) = %q, want %q", tt.stat, got, tt.want)
			}
		})
	}
}

// A naive SplitN on spaces from the left reads field 3 of the raw line, which
// lands inside the comm whenever the comm contains a space. Pin that apart from
// the correct answer so a "simplification" cannot quietly reintroduce it.
func TestParseProcStateIsNotAPlainFieldSplit(t *testing.T) {
	t.Parallel()

	const stat = "4242 (my odd proc) R 1 4242 0"

	got, ok := parseProcState(stat)
	if !ok {
		t.Fatalf("parseProcState(%q) failed", stat)
	}
	if got == "odd" {
		t.Fatalf("parseProcState split on spaces from the left and read the comm, got %q", got)
	}
	if got != "R" {
		t.Errorf("parseProcState(%q) = %q, want %q", stat, got, "R")
	}
}

func TestPollerProcessAliveRejectsNonPositivePids(t *testing.T) {
	t.Parallel()

	for _, pid := range []int{0, -1, -1221266} {
		if pollerProcessAlive(pid) {
			t.Errorf("pollerProcessAlive(%d) = true, want false", pid)
		}
	}
}

func TestPollerProcessAliveAcceptsSelf(t *testing.T) {
	t.Parallel()

	// Positive control: without this, a function that always returns false
	// would pass the zombie test below while breaking every live poller.
	if !pollerProcessAlive(os.Getpid()) {
		t.Errorf("pollerProcessAlive(%d) = false for the running test process", os.Getpid())
	}
}

// The asymmetry IS the bug: a child that has exited but not been reaped still
// answers signal 0, so the old Signal(0)-only check called a corpse alive and
// StartPoller's "already running" guard then blocked its replacement forever.
func TestPollerProcessAliveTreatsAnUnreapedChildAsDead(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("zombie detection reads /proc, which only Linux has")
	}

	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}
	pid := cmd.Process.Pid
	// Never call Wait before the assertions — Wait is what reaps the zombie.
	t.Cleanup(func() { _ = cmd.Wait() })

	deadline := time.Now().Add(10 * time.Second)
	for {
		if state, ok := procState(pid); ok && state == "Z" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child pid %d never reached state Z", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("signal 0 on zombie pid %d returned %v; the premise of this test no longer holds", pid, err)
	}

	if pollerProcessAlive(pid) {
		t.Errorf("pollerProcessAlive(%d) = true for a zombie; signal 0 succeeds on a corpse, the state read must not", pid)
	}
}
