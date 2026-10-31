package payments

// received_apply_test.go, the ALLOCATION ARITHMETIC of a received payment, pure and ahead of the
// database: the oldest first walk, the holdback exclusion, the outcome words and the capacity the
// over-refusal reads. The transactional legs (one payment row, one journal, the locks) live in
// received_apply_integration_test.go; this file owns the arithmetic alone so a wrong split is a
// two second failure with no container behind it.
//
// The dates are pinned, never time.Now: a fixture tag built from a real clock in a file that pins
// instants is the DIB-427 shape the testaudit gate refuses.

import (
	"testing"
	"time"
)

var applyTestNow = time.Date(2026, time.August, 26, 9, 0, 0, 0, time.UTC)

// applyRow builds one locked invoice row for the walk, dated relative to the pinned now.
func applyRow(id, no string, issueDays, dueDays int, balance, holdback int64) applyInvoiceRow {
	return applyInvoiceRow{
		InvoiceID: id, InvoiceNo: no, Status: "FINALIZED",
		IssueDate:    applyTestNow.AddDate(0, 0, issueDays*-1),
		DueDate:      applyTestNow.AddDate(0, 0, dueDays*-1),
		BalanceCents: balance, HoldbackCents: holdback,
	}
}

func lineOf(t *testing.T, lines []ApplyLine, id string) ApplyLine {
	t.Helper()
	for _, l := range lines {
		if l.InvoiceID == id {
			return l
		}
	}
	t.Fatalf("no line for invoice %s in %v", id, lines)
	return ApplyLine{}
}

// The server orders the walk, not the caller's list: the ids arrive newest first and the money still
// lands on the oldest invoice first, because a caller's list order is a screen accident and an
// invoice's age is the fact.
func TestAllocateOldestFirst_TheServerOrdersByAgeNotByTheList(t *testing.T) {
	old := applyRow("old", "INV-OLD", 90, 60, 30000, 0)
	mid := applyRow("mid", "INV-MID", 60, 30, 20000, 0)
	fresh := applyRow("new", "INV-NEW", 30, -10, 10000, 0)
	lines, capacity, _ := allocateOldestFirst([]applyInvoiceRow{fresh, mid, old}, 40000, nil)
	if capacity != 60000 {
		t.Fatalf("capacity %d, want the three payables 60000", capacity)
	}
	if len(lines) != 3 || lines[0].InvoiceID != "old" || lines[1].InvoiceID != "mid" || lines[2].InvoiceID != "new" {
		t.Fatalf("the walk must answer oldest first, got %v", lines)
	}
	if lines[0].AppliedCents != 30000 || lines[0].Outcome != ApplyOutcomeCloses || lines[0].BalanceAfterCents != 0 {
		t.Fatalf("the oldest closes: %+v", lines[0])
	}
	if lines[1].AppliedCents != 10000 || lines[1].Outcome != ApplyOutcomePart || lines[1].BalanceAfterCents != 10000 {
		t.Fatalf("the middle takes the rest in part: %+v", lines[1])
	}
	if lines[2].AppliedCents != 0 || lines[2].Outcome != ApplyOutcomeNone || lines[2].BalanceAfterCents != 10000 {
		t.Fatalf("the youngest is untouched: %+v", lines[2])
	}
}

// The due date breaks an issue day tie, then the invoice number, then the id, so two runs of the
// same payment always answer the same order and the ledger keeps one spelling of oldest.
func TestAllocateOldestFirst_TiesBreakDeterministically(t *testing.T) {
	a := applyRow("id-b", "INV-002", 60, 30, 10000, 0) // due the earliest: first
	b := applyRow("id-a", "INV-003", 60, 20, 10000, 0) // later due, higher number: last
	c := applyRow("id-c", "INV-001", 60, 20, 10000, 0) // later due, lowest number: second
	lines, _, _ := allocateOldestFirst([]applyInvoiceRow{a, b, c}, 25000, nil)
	if lines[0].InvoiceID != "id-b" || lines[1].InvoiceID != "id-c" || lines[2].InvoiceID != "id-a" {
		t.Fatalf("want due date then number then id (b, c, a), got %v", lines)
	}
	// And the same due day too: the number, then the id.
	x := applyRow("id-a", "INV-002", 60, 20, 10000, 0)
	y := applyRow("id-z", "INV-001", 60, 20, 10000, 0)
	z := applyRow("id-b", "INV-001", 60, 20, 10000, 0)
	lines, _, _ = allocateOldestFirst([]applyInvoiceRow{x, y, z}, 30000, nil)
	if lines[0].InvoiceID != "id-b" || lines[1].InvoiceID != "id-z" || lines[2].InvoiceID != "id-a" {
		t.Fatalf("want number then id (b, z, a), got %v", lines)
	}
}

// The payable is balance minus holdback, floored at zero: the Bill 60 retained portion is not
// collectable, and an invoice held back in full takes nothing without failing the payment.
func TestAllocateOldestFirst_TheHoldbackIsNotCollectable(t *testing.T) {
	held := applyRow("held", "INV-HELD", 90, 60, 50000, 10000)
	open := applyRow("open", "INV-OPEN", 30, 0, 10000, 0)
	lines, capacity, _ := allocateOldestFirst([]applyInvoiceRow{open, held}, 50000, nil)
	if capacity != 50000 {
		t.Fatalf("capacity %d, want 40000 + 10000", capacity)
	}
	heldLine := lineOf(t, lines, "held")
	if heldLine.AppliedCents != 40000 {
		t.Fatalf("the held invoice takes its payable only, got %d", heldLine.AppliedCents)
	}
	// The settle's own status rule closes an invoice at balance 0, and a held back invoice still
	// carries its retained balance, so it PARTS: the answer must say what the ledger will say.
	if heldLine.Outcome != ApplyOutcomePart || heldLine.BalanceAfterCents != 10000 || heldLine.StaysOpenCents != 0 {
		t.Fatalf("a held back invoice stays part paid: %+v", heldLine)
	}
	if l := lineOf(t, lines, "open"); l.AppliedCents != 10000 || l.Outcome != ApplyOutcomeCloses {
		t.Fatalf("the remainder reaches the next invoice: %+v", l)
	}
}

// A fully held back invoice (payable zero) takes nothing, says NONE, and does not shrink capacity it
// never had; an amount the others cannot take is the caller's over-refusal, not a silent short apply.
func TestAllocateOldestFirst_AFullyHeldBackInvoiceTakesNothing(t *testing.T) {
	held := applyRow("held", "INV-HELD", 90, 60, 8000, 8000)
	open := applyRow("open", "INV-OPEN", 30, 0, 10000, 0)
	lines, capacity, _ := allocateOldestFirst([]applyInvoiceRow{held, open}, 10000, nil)
	if capacity != 10000 {
		t.Fatalf("capacity %d, want the one payable invoice alone", capacity)
	}
	if l := lineOf(t, lines, "held"); l.AppliedCents != 0 || l.Outcome != ApplyOutcomeNone {
		t.Fatalf("a zero payable invoice takes nothing: %+v", l)
	}
	if l := lineOf(t, lines, "open"); l.AppliedCents != 10000 || l.Outcome != ApplyOutcomeCloses {
		t.Fatalf("the payable invoice closes: %+v", l)
	}
}

// stays_open is what the account can still pay after the split (balance after minus holdback): the
// figure the desk reads to answer "what is left on this one".
func TestAllocateOldestFirst_StaysOpenIsThePayableAfter(t *testing.T) {
	inv := applyRow("inv", "INV-1", 60, 30, 30000, 5000)
	lines, _, _ := allocateOldestFirst([]applyInvoiceRow{inv}, 10000, nil)
	l := lines[0]
	if l.BalanceAfterCents != 20000 || l.StaysOpenCents != 15000 {
		t.Fatalf("balance after %d and stays open %d, want 20000 and 15000", l.BalanceAfterCents, l.StaysOpenCents)
	}
	if l.Outcome != ApplyOutcomePart {
		t.Fatalf("a part payment is PART, got %s", l.Outcome)
	}
}
