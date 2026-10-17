package main

// demoseed_controller_plan_test.go, the controller world's promises asserted on the PLAN, without a
// database. The database half (the controller decides through the real routes) is
// demoworld_controller_test.go.

import (
	"fmt"
	"strings"
	"testing"
)

func controllerRows(rows []row, sum planSummary) map[string][]map[string]any {
	ids := map[string]bool{}
	for _, id := range sum.ControllerRowIDs {
		ids[id] = true
	}
	out := map[string][]map[string]any{}
	for _, r := range rows {
		if ids[fmt.Sprint(r.vals[0])] {
			out[r.table] = append(out[r.table], fieldMap(r))
		}
	}
	return out
}

func TestControllerPlan_NeedsTheControllerAndBothMakers(t *testing.T) {
	for name, drop := range map[string][]string{
		"no controller": {personaController},
		"no manager":    {personaQuoteManager},
		"none":          {personaController, personaQuoteManager},
	} {
		in := testInput(t)
		for _, p := range drop {
			delete(in.Personas, p)
		}
		_, sum, err := buildPlan(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(sum.ControllerRowIDs) != 0 {
			t.Errorf("%s: the controller world built %d rows with a login missing", name, len(sum.ControllerRowIDs))
		}
	}
}

func TestControllerPlan_TwoParkedJournalsOverTheThresholdWithTheirRequests(t *testing.T) {
	in := testInput(t)
	rows, sum, err := buildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.ControllerRowIDs) != controllerWorldRows {
		t.Fatalf("the world writes %d rows, want %d", len(sum.ControllerRowIDs), controllerWorldRows)
	}
	c := controllerRows(rows, sum)
	if len(c["gl_journals"]) != 2 || len(c["gl_journal_lines"]) != 4 || len(c["permission_approval_requests"]) != 2 {
		t.Fatalf("the world holds %d journals, %d lines, %d requests, want 2, 4, 2",
			len(c["gl_journals"]), len(c["gl_journal_lines"]), len(c["permission_approval_requests"]))
	}
	controller, manager := in.Personas[personaController], in.Personas[personaQuoteManager]
	makers := map[string]bool{}
	for _, j := range c["gl_journals"] {
		if j["status"] != "DRAFT" || j["source"] != "MANUAL" {
			t.Errorf("journal %v is %v from %v, want a MANUAL draft", j["id"], j["status"], j["source"])
		}
		if j["branch_id"] != in.Branches[1].ID {
			t.Errorf("journal %v is at %v, want Trenton (the second branch by code)", j["id"], j["branch_id"])
		}
		makers[fmt.Sprint(j["created_by"])] = true
	}
	if !makers[controller] || !makers[manager] || len(makers) != 2 {
		t.Errorf("journal makers %v, want the controller (its own) and the Sales manager", makers)
	}
	// Each journal balances and sits over the org default threshold, which is what parks it.
	const threshold = 500_000
	totals := map[string]int64{}
	credits := map[string]int64{}
	for _, l := range c["gl_journal_lines"] {
		jid := fmt.Sprint(l["journal_id"])
		if d, ok := l["debit_cents"].(int64); ok {
			totals[jid] += d
		}
		if cr, ok := l["credit_cents"].(int64); ok {
			credits[jid] += cr
		}
		_, deb := l["debit_cents"]
		_, cre := l["credit_cents"]
		if deb == cre {
			t.Errorf("line %v carries both or neither of debit and credit", l["id"])
		}
	}
	for jid, total := range totals {
		if total != credits[jid] {
			t.Errorf("journal %s does not balance: debit %d credit %d", jid, total, credits[jid])
		}
		if total < threshold {
			t.Errorf("journal %s totals %d, under the %d threshold, so it would have posted", jid, total, threshold)
		}
	}
	// Each request is the one identity's FileJEApproval files: the journal as subject, the maker as
	// requester, the debit total in the sentence and in data.
	for _, r := range c["permission_approval_requests"] {
		if r["policy_type"] != "JE_OVER_THRESHOLD" || r["status"] != "PENDING" {
			t.Errorf("request %v is %v %v, want a pending JE_OVER_THRESHOLD", r["id"], r["policy_type"], r["status"])
		}
		subject := fmt.Sprint(r["subject_ref"])
		var journal map[string]any
		for _, j := range c["gl_journals"] {
			if j["id"] == subject {
				journal = j
			}
		}
		if journal == nil {
			t.Fatalf("request %v names journal %s, which the world does not hold", r["id"], subject)
		}
		if r["requested_by"] != journal["created_by"] {
			t.Errorf("request %v is filed by %v, the journal's maker is %v", r["id"], r["requested_by"], journal["created_by"])
		}
		want := fmt.Sprintf("(%d cents)", totals[subject])
		if !strings.Contains(fmt.Sprint(r["detail"]), want) {
			t.Errorf("request %v reads %q, want the debit total %s in the sentence", r["id"], r["detail"], want)
		}
	}
}

// The entry number is minted from the real counter only for a journal that is not there yet, so a
// repeat apply burns no number (the CASE guards the mint).
func TestControllerPlan_TheEntryNumberIsMintedOnlyForANewJournal(t *testing.T) {
	rows, sum, err := buildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	ids := map[string]bool{}
	for _, id := range sum.ControllerRowIDs {
		ids[id] = true
	}
	for _, r := range rows {
		if r.table != "gl_journals" || !ids[fmt.Sprint(r.vals[0])] {
			continue
		}
		seen++
		stmt := r.sql()
		if !strings.Contains(stmt, "CASE WHEN EXISTS (SELECT 1 FROM gl_journals WHERE id = $") || !strings.Contains(stmt, "next_entry_no(") {
			t.Errorf("the journal insert does not guard the mint:\n%s", stmt)
		}
		if got, want := len(r.args()), strings.Count(stmt, "$")-strings.Count(stmt, "$$"); got < 1 || want < 1 {
			t.Errorf("args %d, placeholders %d", got, want)
		}
	}
	if seen != 2 {
		t.Fatalf("saw %d journal inserts, want 2", seen)
	}
}
