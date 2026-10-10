// demoseed_controller.go, the Financial Controller's world: two journals parked over the maker-checker
// threshold, each with the approval request the GL files when it parks one, so the controller's
// approvals queue holds a journal it decides and a journal that is its own.
//
//	J1  made by the Sales manager login, a prepaid insurance premium over the threshold: the controller
//	    approves it (the request decide route posts the journal in the same transaction).
//	J2  made by the controller itself, an inventory write-down over the threshold: separation of duties
//	    bars the maker from deciding it, so it reads as not the controller's to decide.
//
// THE MAKERS ARE DEMO LOGINS, and the section is conditional on both for the reason quotes.go gives: the
// Sales manager and the controller arrive with the demo populate, after the first apply, so a first
// apply builds nothing here and the second adds the world. A journal with no maker would have created_by
// NULL, which the four eyes rule reads as nobody (gl/journal.go ApproveJournal), and an approval request
// with no requester cannot exist (requested_by is NOT NULL).
//
// WHAT EACH ROW IS, as the real path writes it (gl/journal.go CreateJournal): a MANUAL journal parked as
// DRAFT because its debit total is at or over the org default threshold, its two lines, and the
// JE_OVER_THRESHOLD request identity's FileJEApproval files (the journal id as subject, the debit total
// riding the data column, the detail sentence to the word).
//
// THE ENTRY NUMBER IS MINTED, NEVER INVENTED. entry_no comes from next_entry_no, the counter every
// real journal draws from (0119), inside a CASE that skips the mint when the journal is already there,
// so a repeat apply recognises the row and burns no number. The fiscal period is the clock's month,
// or the latest period on file when the clock's has none: a parked draft posts into a period only when
// the controller approves it, and a missing period must not break the whole apply.
//
// WHAT THIS WORLD DOES NOT CARRY, the quote world's list again: seq stays NULL on the request (the sync
// feed never receives it), and the approval card fan-out and the audit rows the real park writes beside
// the journal are not written. The queue and the ledger read the rows over REST and show them.
package main

import (
	"encoding/json"
	"fmt"
)

// The controller login, by the email the demo populate writes (core/internal/demo populate.go:
// controllerEmail), the join key and not the id for the reason resolvePersonas gives.
const personaController = "financial.controller@hh-demo.invalid"

// controllerPersonaEmails is what resolve.go adds to the personas it looks up.
var controllerPersonaEmails = []string{personaController}

// controllerWorldRows is the number of rows the section writes: per journal the header, its lines and
// its approval request. The quote world's proof adds it to its own expectation, since the second apply
// writes both.
const controllerWorldRows = 8

type journalLineSpec struct {
	Account string // gl_accounts.code
	Debit   int64
	Credit  int64
}

type journalSpec struct {
	Key      string
	Maker    string // the login email that made it
	Branch   int
	Memo     string
	AgeHours int
	Lines    []journalLineSpec
}

// Both totals sit over the org default threshold (500000 cents, 0032's placeholder policy row).
var demoJournals = []journalSpec{
	{Key: "J1", Maker: personaQuoteManager, Branch: 1, AgeHours: 30,
		Memo: "Insurance premium paid ahead for the year (demo)",
		Lines: []journalLineSpec{
			{Account: "1300", Debit: 750_000},
			{Account: "1000", Credit: 750_000},
		}},
	{Key: "J2", Maker: personaController, Branch: 1, AgeHours: 12,
		Memo: "Inventory write-down after the cycle count (demo)",
		Lines: []journalLineSpec{
			{Account: "5200", Debit: 620_000},
			{Account: "1200", Credit: 620_000},
		}},
}

// glYearExpr is the fiscal year of the clock's period, or of the latest period on file.
func glYearExpr(y, m int) sqlExpr {
	return sqlExpr{
		sql: `COALESCE((SELECT year FROM fiscal_periods WHERE year = ?::int AND month = ?::int),
			(SELECT year FROM fiscal_periods ORDER BY year DESC, month DESC LIMIT 1))`,
		args: []any{y, m},
	}
}

// glPeriodExpr is the id of the clock's period, or of the latest period on file.
func glPeriodExpr(y, m int) sqlExpr {
	return sqlExpr{
		sql: `COALESCE((SELECT id FROM fiscal_periods WHERE year = ?::int AND month = ?::int),
			(SELECT id FROM fiscal_periods ORDER BY year DESC, month DESC LIMIT 1))`,
		args: []any{y, m},
	}
}

// glEntryNo mints the journal's entry number from the real counter, and only when the journal is new.
func glEntryNo(journalID string, y, m int) sqlExpr {
	year := glYearExpr(y, m)
	return sqlExpr{
		sql: `CASE WHEN EXISTS (SELECT 1 FROM gl_journals WHERE id = ?::uuid) THEN 0
			ELSE next_entry_no(` + year.sql + `) END`,
		args: append([]any{journalID}, year.args...),
	}
}

// glAccount is the id of an account by its code.
func glAccount(code string) sqlExpr {
	return sqlExpr{sql: `(SELECT id FROM gl_accounts WHERE code = ?::citext)`, args: []any{code}}
}

// controllerWorld is the section's hook. It builds nothing when the controller or a maker is absent.
func (b *builder) controllerWorld() {
	controller := b.in.Personas[personaController]
	if controller == "" {
		return
	}
	y, m := b.in.Now.UTC().Year(), int(b.in.Now.UTC().Month())
	for _, j := range demoJournals {
		if b.in.Personas[j.Maker] == "" {
			return
		}
	}
	for _, j := range demoJournals {
		maker := b.in.Personas[j.Maker]
		journalID := demoID("journal:" + j.Key)
		at := b.ago(j.AgeHours)
		var total int64
		for _, l := range j.Lines {
			total += l.Debit
		}
		add := func(r row) {
			b.add(r)
			b.sum.ControllerRowIDs = append(b.sum.ControllerRowIDs, fmt.Sprint(r.vals[0]))
		}
		add(insert("gl_journals", "(id)",
			"id", journalID,
			"fiscal_year", glYearExpr(y, m),
			"entry_no", glEntryNo(journalID, y, m),
			"period_id", glPeriodExpr(y, m),
			"source", "MANUAL",
			"memo", j.Memo,
			"status", "DRAFT",
			"branch_id", b.branchID(j.Branch),
			"created_by", maker,
			"created_at", at, "updated_at", at,
		))
		for i, l := range j.Lines {
			cols := []any{
				"id", demoID(fmt.Sprintf("journalline:%s:%d", j.Key, i+1)),
				"journal_id", journalID,
				"line_no", i + 1,
				"account_id", glAccount(l.Account),
				"branch_id", b.branchID(j.Branch),
				"created_at", at,
			}
			if l.Debit > 0 {
				cols = append(cols, "debit_cents", l.Debit)
			} else {
				cols = append(cols, "credit_cents", l.Credit)
			}
			add(insert("gl_journal_lines", "(id)", cols...))
		}
		data, _ := json.Marshal(map[string]any{"demo_seed": demoMarker, "amount_cents": total})
		add(insert("permission_approval_requests", "(id)",
			"id", demoID("jerequest:"+j.Key),
			"requested_by", maker,
			"branch_id", b.branchID(j.Branch),
			"policy_type", "JE_OVER_THRESHOLD",
			// identity's FileJEApproval sentence, to the word.
			"detail", fmt.Sprintf("Journal entry at/over the maker-checker threshold (%d cents) awaiting approval", total),
			"subject_ref", journalID,
			"data", string(data),
			"status", "PENDING",
			"requested_at", at,
		))
	}
}
