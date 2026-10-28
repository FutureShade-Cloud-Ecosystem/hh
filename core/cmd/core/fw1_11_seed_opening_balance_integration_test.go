//go:build integration

// fw1_11_seed_opening_balance_integration_test.go, FW1-11 round 4: the seed's DATED TIME BOMB, ruled.
//
// WHAT THE BOMB IS. The seed plants two invoices and no journal of its own, so both are history
// from before HH and both carry the opening balance marker (FROM-PRINCIPAL 180, ruled again here):
// an opening balance's HST is not a period's collection. What made it a bomb was the DATE. The
// settled invoice INV-2026-0001 is dated thirty days before the seed's own day, so on the 31st of a
// 31-day month that date is the FIRST of the SAME month: the invoice lands in the walk month, and an
// unmarked invoice with no journal behind it would put its HST into that month's collected with no
// 2200 to match, so the month's reconcile would show a variance and the walk's HST line would go
// red for nobody's commit.
//
// THE LEG proves the ruling at a 31st, on the walk's own world (the Ontario Dibbits seed):
//
//	1. THE RULE, read off the live rows: every invoice the seed plants WITHOUT a journal of its own
//	   (no finalize journal behind it and no queued finalize event) carries the marker, and every
//	   invoice WITH one (the demo CP03 pair from 0055, whose repair event is queued) does not.
//	2. THE 31ST: the seed applied with ITS OWN CLOCK PINNED to the 31st of a 31-day month, so the
//	   settled invoice lands in that month. The month's reconcile ties at zero variance and the
//	   month's opening balances line names both seeded documents.
//	3. THE CONTROL: with the marker taken off that one settled invoice, the same month shows a
//	   variance of exactly its HST. That is what the marker is there to prevent, and it is the
//	   number the walk would have shown on the 31st.
//
// EVERY DATE IN THIS FILE IS FIXED (the pinned 31st is 2026-01-31) and no real clock is read here,
// so the leg cannot age into a dated time bomb (internal/testaudit's tripwire).
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run 'FW111' ./cmd/core/

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
)

// The pinned 31st: the last day of a 31-day month, so the seed's own "thirty days before" lands on
// the first of that same month, the day the bomb goes off.
const (
	fw111PinnedDay = "2026-01-31"
	fw111PinnedY   = 2026
	fw111PinnedM   = 1
	// The settled invoice, dated thirty days before the pinned day, is the first of the pinned month.
	fw111SettledInvoiceNo = "INV-2026-0001"
	fw111SettledIssueDay  = "2026-01-01"
)

// TestIntegration_FW111_TheSeedMarksEveryJournalLessDocumentItPlants is the walk's leg on the
// seed's opening balances, pinned to a 31st.
func TestIntegration_FW111_TheSeedMarksEveryJournalLessDocumentItPlants(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "fw111seed")

	// The walk's own world, with the SEED'S CLOCK PINNED. The seed reads its own day from the
	// database's clock, and the day it reads is the DEALER's day, the house's own SQL spelling
	// (now() AT TIME ZONE 'America/Toronto')::date, which is the very thing that dates the
	// documents, so the pin is applied to the seed's own text: the dealer's day expression becomes
	// the pinned 31st. Nothing else in the seed is dated from the clock in a way the tax tie-out
	// reads.
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	pinned := strings.ReplaceAll(string(body), "(now() AT TIME ZONE 'America/Toronto')::date", "DATE '"+fw111PinnedDay+"'")
	pinned = strings.ReplaceAll(pinned, "CURRENT_DATE", "DATE '"+fw111PinnedDay+"'")
	if pinned == string(body) {
		t.Fatal("the seed names no day to pin: the pinned seed would be the unpinned seed and the leg would prove nothing")
	}
	if _, err := pool.Exec(ctx, pinned); err != nil {
		t.Fatalf("applying the pinned seed: %v", err)
	}
	// The seed runs before any org exists; the branches get one, exactly as the walk's fixture does.
	const (
		orgID   = "88a20000-0000-4000-8000-0000000000f2"
		orgSlug = "fw1-11-seed"
	)
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'FW1 11 Seed Org') ON CONFLICT (id) DO NOTHING`, orgID, orgSlug)
	mustExec(t, ctx, pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, orgID)

	// -- 1. THE RULE, on the live rows ------------------------------------------------------
	//
	// A document has a journal of its own when a finalize journal stands behind it, or when its
	// finalize event is on the queue for the dispatcher to post (0222's repair). The seed plants no
	// journals and no events, so every invoice IT plants is in the journal-less population and must
	// carry the marker. The demo CP03 pair is planted by 0055 with its repair event queued, so it is
	// not in that population and must not be marked.
	//
	// The population is the ACCRUAL documents (doc_type = 'INVOICE'), the same population 0222's
	// repair covers. A credit memo is not an accrual: it reduces an existing journal through its
	// own poster and never carries an ar.invoice.finalized journal, so "no finalize journal" does
	// not describe it, and the seed plants none.
	type invoiceRow struct {
		no      string
		issue   string
		tax     int64
		marked  bool
		journal bool
	}
	rows, err := pool.Query(ctx, `
		SELECT i.invoice_no,
		       to_char(i.issue_date, 'YYYY-MM-DD'),
		       i.tax_cents,
		       (i.metadata @> '{"opening_balance": true}'::jsonb),
		       EXISTS (SELECT 1 FROM gl_journals j
		                WHERE j.idempotency_key = 'ar.invoice.finalized:' || i.id::text)
		       OR EXISTS (SELECT 1 FROM events_outbox e
		                  WHERE e.topic = 'ar.invoice.finalized'
		                    AND e.payload->>'invoice_id' = i.id::text
		                    AND e.status <> 'DEAD')
		  FROM invoices i
		 WHERE i.doc_type = 'INVOICE'
		   AND i.status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
		 ORDER BY i.invoice_no`)
	if err != nil {
		t.Fatalf("reading the seeded invoices: %v", err)
	}
	defer rows.Close()
	var seen []invoiceRow
	for rows.Next() {
		var r invoiceRow
		if err := rows.Scan(&r.no, &r.issue, &r.tax, &r.marked, &r.journal); err != nil {
			t.Fatalf("scan the seeded invoice: %v", err)
		}
		seen = append(seen, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the seeded invoices: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("the pinned seed planted no invoice at all, so the leg proves nothing")
	}
	var markedCount, journalLessCount, cp03 int
	for _, r := range seen {
		if strings.HasPrefix(r.no, "INV-DEMO-CP03") {
			cp03++
		}
		if r.journal {
			if r.marked {
				t.Errorf("FW1-11 the seed's opening balances: %s carries a journal of its own and is marked as an opening balance, want it unmarked (a document with a journal behind it is this period's business)", r.no)
			}
			continue
		}
		journalLessCount++
		if !r.marked {
			t.Errorf("FW1-11 the seed's opening balances: %s is planted without a journal of its own and carries no opening balance marker, want it marked (the seed plants history from before HH, and an opening balance's HST is not a period's collection)", r.no)
			continue
		}
		markedCount++
	}
	if journalLessCount == 0 {
		t.Fatal("every invoice on the seeded world has a journal of its own, so the seed's opening balance population is empty and the leg proves nothing")
	}
	if cp03 != 2 {
		t.Errorf("FW1-11 the seed's opening balances: the demo CP03 pair is %d rows, want the 2 of 0055 (they carry their own repair event, so they stay unmarked)", cp03)
	}

	// -- 2. THE 31ST: the settled invoice lands in the pinned month --------------------------
	var settledIssue string
	if err := pool.QueryRow(ctx,
		`SELECT to_char(issue_date, 'YYYY-MM-DD') FROM invoices WHERE invoice_no = $1`, fw111SettledInvoiceNo).Scan(&settledIssue); err != nil {
		t.Fatalf("reading the settled invoice's issue day: %v", err)
	}
	if settledIssue != fw111SettledIssueDay {
		t.Fatalf("FW1-11 the seed's dated time bomb: the settled invoice is dated %s, want %s (thirty days before the pinned 31st is the first of that same month, the day the bomb goes off)",
			settledIssue, fw111SettledIssueDay)
	}

	glSvc := gl.NewService(gl.NewRepository(pool).WithZone(newDealerZone(pool)))
	svc := tax.NewService(tax.NewRepository(pool)).
		WithSalesHSTs(billing.NewRepository(pool)).
		WithReturnHSTs(pos.NewRepository(pool)).
		WithBillITCs(purchasing.NewRepository(pool)).
		WithGLBalances(glBalanceAdapter{svc: glSvc})
	rep, err := svc.ReconcileReport(ctx, fw111PinnedY, fw111PinnedM, "")
	if err != nil {
		t.Fatalf("the HST reconcile for the pinned month: %v", err)
	}
	if rep.VarianceCents != 0 || !rep.TiedOut {
		t.Errorf("FW1-11 the seed's dated time bomb: the reconcile for %04d-%02d does not tie: GL 2200 payable %d against %d collected, a variance of %d, want 0 (the settled invoice dated the first of that month is an opening balance, not that month's collection)",
			fw111PinnedY, fw111PinnedM, rep.GLPayableCents, rep.CollectedCents, rep.VarianceCents)
	}
	if rep.CollectedCents != 0 {
		t.Errorf("FW1-11 the seed's dated time bomb: the pinned month collected %d, want 0 (both seeded documents are opening balances)", rep.CollectedCents)
	}
	if rep.OpeningBalanceCount != markedCount {
		t.Errorf("FW1-11 the seed's dated time bomb: the pinned month's opening balances line names %d documents, want the %d the seed planted without a journal", rep.OpeningBalanceCount, markedCount)
	}

	// -- 3. THE CONTROL: the marker is what makes that month tie ------------------------------
	//
	// Take the marker off the settled invoice, exactly as the seed had it before this ruling, and
	// the same month shows a variance of exactly that invoice's HST. That is the number the walk
	// would have shown on the 31st, and it is why the marker is the ruling and not a convenience.
	if _, err := pool.Exec(ctx,
		`UPDATE invoices SET metadata = metadata - 'opening_balance' WHERE invoice_no = $1`,
		fw111SettledInvoiceNo); err != nil {
		t.Fatalf("take the marker off the settled invoice: %v", err)
	}
	control, err := svc.ReconcileReport(ctx, fw111PinnedY, fw111PinnedM, "")
	if err != nil {
		t.Fatalf("the HST reconcile for the pinned month without the marker: %v", err)
	}
	var settledTax int64
	if err := pool.QueryRow(ctx,
		`SELECT tax_cents FROM invoices WHERE invoice_no = $1`, fw111SettledInvoiceNo).Scan(&settledTax); err != nil {
		t.Fatalf("read the settled invoice's tax: %v", err)
	}
	if control.VarianceCents != -settledTax {
		t.Errorf("FW1-11 the seed's dated time bomb: without the marker the pinned month shows a variance of %d, want %d (the settled invoice's HST counted as that month's collection with no 2200 behind it)",
			control.VarianceCents, -settledTax)
	}
	if control.OpeningBalanceCount != markedCount-1 {
		t.Errorf("FW1-11 the seed's dated time bomb: without the marker the opening balances line names %d documents, want %d", control.OpeningBalanceCount, markedCount-1)
	}
}
