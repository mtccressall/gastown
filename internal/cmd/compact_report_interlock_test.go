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
