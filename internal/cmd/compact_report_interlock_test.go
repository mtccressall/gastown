package cmd

import (
	"strings"
	"testing"
)

func reportWith(hidden, deleted, promoted int) *compactReport {
	r := &compactReport{Date: "2026-10-02", Categories: map[string]*categoryStats{}, HiddenWisps: hidden}
	for _, c := range categoryOrder {
		r.Categories[c] = &categoryStats{}
	}
	r.Categories[categoryOrder[0]].Deleted = deleted
	r.Categories[categoryOrder[0]].Promoted = promoted
	r.Categories[categoryOrder[0]].Active = 1 // keep the unrelated zero-patrol note quiet
	return r
}

// Three zeros over a 60k population is indistinguishable from a broken probe,
// and two separate mayor/ sessions raised that exact suspicion days apart because
// the digest did not carry its own explanation. The verdict belongs in the digest.
func TestDigestStatesTheInterlockWhenItActedOnNothing(t *testing.T) {
	got := strings.Join(detectAnomalies(reportWith(60439, 0, 0)), "\n")
	for _, want := range []string{"INERT BY DESIGN", "--include-infra", "60439", "gastown-mq9"} {
		if !strings.Contains(got, want) {
			t.Errorf("digest does not explain its own zeros — missing %q in:\n%s", want, got)
		}
	}
}

// NEGATIVE CONTROL 1: with nothing hidden, the zeros are an ordinary quiet day
// and the note must NOT appear, or every digest cries interlock forever.
func TestDigestIsSilentWhenNothingIsHidden(t *testing.T) {
	if got := strings.Join(detectAnomalies(reportWith(0, 0, 0)), "\n"); strings.Contains(got, "INERT BY DESIGN") {
		t.Errorf("claimed an interlock with 0 hidden wisps:\n%s", got)
	}
}

// NEGATIVE CONTROL 2: once the compactor actually acts, the note must stop — it
// describes a compactor that scanned nothing, not one that found nothing.
func TestDigestIsSilentOnceTheCompactorActs(t *testing.T) {
	if got := strings.Join(detectAnomalies(reportWith(60439, 7, 0)), "\n"); strings.Contains(got, "INERT BY DESIGN") {
		t.Errorf("claimed an interlock while reporting 7 deletions:\n%s", got)
	}
}

// PLACEMENT, NOT PRESENCE — and mayor/ is right that the tests above do not cover
// it. They assert detectAnomalies RETURNS the line; they say nothing about whether
// it reaches the body the mail actually carries. The received bead it measured was
// 209 chars — H2, Summary heading, three-row table, and no "hidden" anywhere — so
// the control it needed had never been in front of it.
//
// This asserts the rendered digest, which is what formatDailyDigest returns and
// what gets mailed. If the line emits only to stdout or JSON, this fails.
func TestTheInterlockLineReachesTheMAILEDDigestBody(t *testing.T) {
	// Run the REAL sequence the command runs: detect, assign, then render.
	// compact_report.go:225 does `report.Anomalies = detectAnomalies(report)`
	// and formatDailyDigest renders report.Anomalies — my first version of this
	// test skipped the assignment and failed, which looked like the fix missing
	// the mail body when it was the test missing a step. mayor/ was right to ask
	// for placement; the first answer the question produced was my own bug.
	r := reportWith(60439, 0, 0)
	r.Anomalies = detectAnomalies(r)
	body := formatDailyDigest(r)

	if !strings.Contains(body, "### Anomalies") {
		t.Fatalf("the digest body has no Anomalies section at all:\n%s", body)
	}
	for _, want := range []string{"INERT BY DESIGN", "60439", "gastown-mq9", "--include-infra"} {
		if !strings.Contains(body, want) {
			t.Errorf("the MAILED body is missing %q — a reader gets the zeros with no explanation:\n%s", want, body)
		}
	}
}

// And the negative control for placement: on an ordinary day the body must NOT
// grow an Anomalies section, or every digest acquires a permanent warning.
func TestTheMAILEDDigestStaysCleanOnAnOrdinaryDay(t *testing.T) {
	r := reportWith(0, 3, 1)
	r.Anomalies = detectAnomalies(r)
	if body := formatDailyDigest(r); strings.Contains(body, "INERT BY DESIGN") {
		t.Errorf("the mailed body claims an interlock on a working compactor:\n%s", body)
	}
}
