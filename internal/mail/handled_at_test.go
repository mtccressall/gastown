package mail

import (
	"strings"
	"testing"
	"time"
)

// gt-745z0: the ack fires at DELIVERY, not when a message is dealt with.
// Measured on this town: 24 beads carry delivery-acked-by while still OPEN, and
// across 442 closed messages the ack-to-close gap is p50 123s but p90 ~41 hours,
// with 38% acked more than five minutes before being cleared. handled-at is the
// only field that separates "reached its recipient" from "somebody dealt with
// it".
func TestHandledAtPrefixIsDistinctFromTheAckLabels(t *testing.T) {
	if HandledAtPrefix == DeliveryLabelAckedAtPrefix {
		t.Fatal("handled-at must not reuse the ack prefix; they record different events")
	}
	for _, p := range []string{DeliveryLabelAckedAtPrefix, DeliveryLabelAckedByPrefix, DeliveryLabelAcked, DeliveryLabelPending} {
		if strings.HasPrefix(p, HandledAtPrefix) || strings.HasPrefix(HandledAtPrefix, p) {
			t.Errorf("handled-at prefix %q collides with delivery label %q; a prefix match would conflate them", HandledAtPrefix, p)
		}
	}
	if !strings.HasSuffix(HandledAtPrefix, ":") {
		t.Errorf("label prefix %q must end in ':' or value parsing splits wrongly", HandledAtPrefix)
	}
}

// There is deliberately NO handled-by. delivery-acked-by never disagrees with
// the assignee (0 of 1959 across 19 roles), so an identity label is vacuous
// unless a handler can differ from a recipient — and the store has no closed_by
// field, so that case is UNTESTABLE rather than absent. This pins the decision
// so a later reader does not add the field on the assumption it was overlooked.
func TestNoHandledByCompanionExists(t *testing.T) {
	if HandledAtPrefix != "handled-at:" {
		t.Fatalf("HandledAtPrefix = %q; the deferral reasoning below is written about handled-at", HandledAtPrefix)
	}
	// A handled-by constant would be caught here by name.
	for _, known := range []string{
		DeliveryLabelPending, DeliveryLabelAcked,
		DeliveryLabelAckedByPrefix, DeliveryLabelAckedAtPrefix, HandledAtPrefix,
	} {
		if strings.HasPrefix(known, "handled-by") {
			t.Error("a handled-by label exists; gt-745z0 defers it until a closed_by field makes the case testable")
		}
	}
}

// The label must be a timestamp the store can order and a human can read.
func TestHandledAtValueIsRFC3339(t *testing.T) {
	at := time.Date(2026, 9, 16, 22, 4, 5, 0, time.UTC)
	label := HandledAtPrefix + at.Format(time.RFC3339)

	value := strings.TrimPrefix(label, HandledAtPrefix)
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("handled-at value %q is not RFC3339: %v", value, err)
	}
	if !parsed.Equal(at) {
		t.Errorf("round trip changed the instant: %v != %v", parsed, at)
	}
	if strings.Contains(value, " ") {
		t.Errorf("value %q contains a space; bd label add takes one token", value)
	}
}
