package lien

import (
	"strings"
	"testing"
	"time"
)

// RT-3 fix, item 1: the triage stops pointing a job linked to a customer job at the old release route.
func TestRT3Fix_TriageOfALinkedJobNamesTheControllersRelease(t *testing.T) {
	now := time.Now()
	entries := []HoldbackEntry{
		{JobID: "linked", EntryType: "RETENTION", AmountCents: 10000, CreatedAt: now.Add(-50 * 24 * time.Hour)},
		{JobID: "plain", EntryType: "RETENTION", AmountCents: 10000, CreatedAt: now.Add(-50 * 24 * time.Hour)},
	}
	facts := map[string]jobFacts{
		"linked": {JobNo: "L", Direction: dirReceivable, Linked: true},
		"plain":  {JobNo: "P", Direction: dirReceivable},
	}
	rows := buildAging(entries, facts, now)
	for _, r := range rows {
		if r.JobNo == "L" && r.ReleaseEligible {
			t.Errorf("a linked job is not eligible for the old release rule")
		}
		if r.JobNo == "P" && !r.ReleaseEligible {
			t.Errorf("an unlinked receivable keeps the old rule")
		}
	}
	for _, it := range buildTriage(rows) {
		if it.JobNo == "L" && !strings.Contains(it.Note, "Controller") {
			t.Errorf("a linked job's note must name the Controller's release, got %q", it.Note)
		}
		if it.JobNo == "P" && !strings.Contains(it.Note, "eligible to release") {
			t.Errorf("an unlinked job keeps its note, got %q", it.Note)
		}
	}
}
