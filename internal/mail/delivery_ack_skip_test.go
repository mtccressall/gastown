package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bdStubPerID installs a fake bd on PATH that serves labels from
// <dir>/<id>.labels, applies label add/remove to that file, and logs every
// invocation to <dir>/bd.log. It returns the log path.
func bdStubPerID(t *testing.T, labelsByID map[string][]string) (dir, logPath string) {
	t.Helper()
	dir = t.TempDir()
	logPath = filepath.Join(dir, "bd.log")
	for id, labels := range labelsByID {
		data := strings.Join(labels, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, id+".labels"), []byte(data), 0644); err != nil {
			t.Fatalf("write labels for %s: %v", id, err)
		}
	}

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$BD_STUB_DIR/bd.log"

if [[ "${1:-}" == "show" ]]; then
  id="${2:-}"
  f="$BD_STUB_DIR/$id.labels"
  printf '[{"id":"%s","labels":[' "$id"
  first=1
  while IFS= read -r label; do
    [[ -z "$label" ]] && continue
    if [[ $first -eq 0 ]]; then printf ','; fi
    first=0
    printf '"%s"' "$label"
  done < "$f"
  printf ']}]'
  exit 0
fi

if [[ "${1:-}" == "label" && "${2:-}" == "add" ]]; then
  f="$BD_STUB_DIR/${3:-}.labels"
  while IFS= read -r existing; do
    [[ "$existing" == "${4:-}" ]] && exit 0
  done < "$f"
  printf '%s\n' "${4:-}" >> "$f"
  exit 0
fi

if [[ "${1:-}" == "label" && "${2:-}" == "remove" ]]; then
  f="$BD_STUB_DIR/${3:-}.labels"
  : > "$f.tmp"
  while IFS= read -r existing; do
    [[ "$existing" == "${4:-}" ]] && continue
    printf '%s\n' "$existing" >> "$f.tmp"
  done < "$f"
  mv "$f.tmp" "$f"
  exit 0
fi

echo "unsupported bd args: $*" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("write bd stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BD_STUB_DIR", dir)
	return dir, logPath
}

func listedMessage(id, assignee string, labels []string) *Message {
	bm := &BeadsMessage{ID: id, Assignee: assignee, Status: "open", Labels: labels}
	bm.ParseLabels()
	return bm.ToMessage()
}

// An unread message stays unread after its delivery is acked, so the inject
// hook hands the same messages to AcknowledgeDeliveries on every prompt. A
// message already fully acked by this recipient must cost no bd call at all;
// otherwise a backlog of a few hundred re-reads exceeds the hook's 30s budget
// (gt-31i2t). Messages that still need a write must keep being acked.
func TestAcknowledgeDeliveriesSkipsMessagesAlreadyAckedByRecipient(t *testing.T) {
	const me = "mayor/"
	acked := []string{"gt:message", DeliveryLabelAckedByPrefix + me, DeliveryLabelAckedAtPrefix + "2026-09-09T13:40:00Z", DeliveryLabelAcked}
	labels := map[string][]string{
		"msg-acked-1":    acked,
		"msg-acked-2":    acked,
		"msg-pending":    {"gt:message", DeliveryLabelPending},
		"msg-residual":   append([]string{DeliveryLabelPending}, acked...),
		"msg-other-ack":  {"gt:message", DeliveryLabelAckedByPrefix + "deacon/", DeliveryLabelAckedAtPrefix + "2026-09-09T13:40:00Z", DeliveryLabelAcked},
		"msg-no-deliver": {"gt:message"},
	}
	dir, logPath := bdStubPerID(t, labels)

	var messages []*Message
	for _, id := range []string{"msg-acked-1", "msg-pending", "msg-acked-2", "msg-residual", "msg-other-ack", "msg-no-deliver"} {
		messages = append(messages, listedMessage(id, me, labels[id]))
	}

	mb := NewMailboxWithBeadsDir(me, dir, "")
	if err := mb.AcknowledgeDeliveries(me, messages); err != nil {
		t.Fatalf("AcknowledgeDeliveries: %v", err)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read log: %v", err)
	}
	log := string(logData)

	for _, id := range []string{"msg-acked-1", "msg-acked-2", "msg-no-deliver"} {
		if strings.Contains(log, " "+id+" ") || strings.Contains(log, " "+id+"\n") {
			t.Errorf("%s needed no write but bd was invoked for it; log:\n%s", id, log)
		}
	}
	for _, id := range []string{"msg-pending", "msg-residual", "msg-other-ack"} {
		if !strings.Contains(log, "show "+id) {
			t.Errorf("%s still needs an ack but was skipped; log:\n%s", id, log)
		}
	}

	// The acks that were still needed must actually land.
	read := func(id string) []string {
		data, err := os.ReadFile(filepath.Join(dir, id+".labels"))
		if err != nil {
			t.Fatalf("read labels %s: %v", id, err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
	if got := read("msg-pending"); !containsDeliveryTestLabel(got, DeliveryLabelAcked) || containsDeliveryTestLabel(got, DeliveryLabelPending) {
		t.Errorf("msg-pending not acked and converged: %v", got)
	}
	if got := read("msg-residual"); containsDeliveryTestLabel(got, DeliveryLabelPending) {
		t.Errorf("msg-residual kept %s: %v", DeliveryLabelPending, got)
	}
	if got := read("msg-other-ack"); !containsDeliveryTestLabel(got, DeliveryLabelAckedByPrefix+me) {
		t.Errorf("msg-other-ack not acked by %s: %v", me, got)
	}
}
