//go:build integration

package main

// rt2_fix2_statement_integration_test.go, the RT-2 fix round 2, item 2: a statement for a past date keeps
// its holdback line, from the same ledger sum the aging reads, whatever the invoice did after the date.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

// statementAt is the leg's customer's statement closing balance as of the end.
func (l *rt2Leg) statementAt(end time.Time) int64 {
	l.t.Helper()
	lines, err := billing.NewRepository(l.pool).StatementLines(context.Background(), l.cust, end, billing.BranchReach{Global: true})
	if err != nil {
		l.t.Fatalf("statement lines: %v", err)
	}
	if len(lines) == 0 {
		return 0
	}
	return lines[len(lines)-1].BalanceRun
}

// Probe B: a credit taken in full this month leaves last month's statement closing at the net.
func TestRT2Fix2_APastStatementKeepsItsHoldbackLineWhenTheInvoiceLaterCloses(t *testing.T) {
	l := rt2Setup(t, "f2stmt")
	lastEnd := rt2LastMonthEnd()
	l.newJob(true)
	doc := l.invoice(100_000, 13_000)
	l.ageInvoice(doc.InvoiceID, 45)
	before := l.datedAt(lastEnd)
	eq(t, "last month's statement closes at the net", l.statementAt(lastEnd), int64(103_000))

	memo := l.creditMemo(doc.InvoiceID, 0, "rt2fix2-full")
	if _, err := l.applyMemo(memo.InvoiceID, doc.InvoiceID, 113_000, "rt2fix2-full-apply"); err != nil {
		t.Fatalf("apply the memo: %v", err)
	}
	eq(t, "the invoice closed", l.invoiceRow(doc.InvoiceID).status, "PAID")
	l.columnIsLedger(doc.InvoiceID, "after the full credit")
	after := l.datedAt(lastEnd)
	eq(t, "last month's statement closing after the invoice closed", l.statementAt(lastEnd), int64(103_000))
	eq(t, "ARBalanceCents", after.ar-before.ar, int64(0))
	eq(t, "the aging", after.aging-before.aging, int64(0))
}

// A write off this month leaves last month's statement closing at the net, and so does its reversal.
func TestRT2Fix2_AWriteOffThisMonthLeavesLastMonthsStatementAlone(t *testing.T) {
	l := rt2Setup(t, "f2stwo")
	lastEnd := rt2LastMonthEnd()
	l.newJob(true)
	doc := l.invoice(100_000, 13_000)
	l.ageInvoice(doc.InvoiceID, 45)
	eq(t, "last month's statement closes at the net", l.statementAt(lastEnd), int64(103_000))
	res := l.writeOffNow(doc.InvoiceID, "rt2fix2-stwo")
	eq(t, "after the write off", l.statementAt(lastEnd), int64(103_000))
	if _, err := l.writeOffRepo().ReverseWriteOff(context.Background(), billing.ReverseWriteOffInput{
		InvoiceID: doc.InvoiceID, WriteOffID: res.WriteOffID, Reason: "undo", OrgID: rt2FixOrg,
	}, time.Now()); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	eq(t, "after the reversal", l.statementAt(lastEnd), int64(103_000))
}
