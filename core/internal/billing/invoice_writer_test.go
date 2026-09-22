package billing

// invoice_writer_test.go, the POS-PARITY tests (DIB-422 acceptance).
//
// These are unit tests and they hold the half of the parity claim that does not need a database: the
// TAX. The other half, the GL shape and the AR subledger, is held by
// invoice_writer_integration_test.go, because a journal only exists once a row does.
//
// Why parity is tested rather than asserted in a comment: the file header of invoice_writer.go makes
// five guarantees (G1..G5) about two producers writing one table. Guarantees written in comments rot
// silently. G2 is the one a future change is most likely to break, somebody swaps the rounding on
// one side, or the allocation, and every counter sale and every order invoice quietly disagree by a
// cent on the same lines. So it is spelled as executable arithmetic here, against the POS ring's OWN
// exported assessor, not against a re-typed copy of its formula.

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/moneyutil"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
)

// parityLines is one basket, expressed once, used by both producers in every parity test below.
//
// It is deliberately chosen to DISCRIMINATE header-level rounding from per-line rounding: the three
// 50¢ lines each sit exactly on the half-cent (50 × 13% = 6.5¢), so rounding them individually
// half-up gains 0.5¢ apiece and the basket totals 2¢ more per-line than it does at the header. A
// basket of round numbers would let a producer round wherever it liked and still pass, which is
// exactly the test that fails to notice the bug it was written for. The assertion at the end of
// TestInvoiceWriter_TaxAndGLParityWithPOSRing FAILS if this property is ever lost.
//
// (Realistic, too: 50¢ is a paver clip, and hardscape orders carry dozens of them.)
var parityLines = []int64{12_345, 7, 99_999, 250_00, 1, 50, 50, 50}

// TestInvoiceWriter_TaxAndGLParityWithPOSRing is the NAMED acceptance test for DIB-422: the same
// lines produce the same tax and the same GL-shaping numbers through both producers.
//
// It compares against pos.FlatHSTAssessor, the ring's actual shipped default, so that if the ring's
// tax behaviour ever changes, THIS test fails and forces the second producer to be changed with it
// (or forces the divergence to be stated deliberately). That is the point: parity is a thing the
// build checks, not a thing a reviewer remembers.
func TestInvoiceWriter_TaxAndGLParityWithPOSRing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mine := FlatHSTAssessor{}
	theirs := pos.FlatHSTAssessor{}

	// -- 1. the header tax agrees, across the awkward cases and not merely the round ones --
	subtotals := []int64{0, 1, 3, 7, 49, 50, 51, 99, 100, 12_345, 250_00, 99_999, 1_000_000, 362_352}
	for _, sub := range subtotals {
		got, err := mine.AssessSale(ctx, TaxQuoteInput{SubtotalCents: sub, ReasonCode: TaxStdON13})
		if err != nil {
			t.Fatalf("billing assessor at %d: %v", sub, err)
		}
		want, err := theirs.AssessSale(ctx, pos.TaxQuoteInput{SubtotalCents: sub, ReasonCode: pos.TaxStdON13})
		if err != nil {
			t.Fatalf("pos assessor at %d: %v", sub, err)
		}
		if got.TaxCents != want.TaxCents {
			t.Errorf("header tax disagrees at subtotal %d: billing=%d pos=%d", sub, got.TaxCents, want.TaxCents)
		}
		if got.RateBps != want.RateBps {
			t.Errorf("rate disagrees at subtotal %d: billing=%dbps pos=%dbps", sub, got.RateBps, want.RateBps)
		}
	}

	// -- 2. the reason code is the same STRING on both sides --
	// A reason code is a wire value that lands in a DB column and in a tax return. Two producers
	// spelling it differently would split the tax report in half without any test noticing.
	if TaxStdON13 != pos.TaxStdON13 {
		t.Errorf("the standard tax reason code must be one string: billing=%q pos=%q", TaxStdON13, pos.TaxStdON13)
	}

	// -- 3. the PER-LINE split agrees, and sums back to the header --
	// Both producers split the HEADER tax over line totals with the same largest-remainder function.
	// The invariant that matters to an auditor is that the split is exhaustive: Σ per-line = header.
	var subtotal int64
	for _, l := range parityLines {
		subtotal += l
	}
	hdr, err := mine.AssessSale(ctx, TaxQuoteInput{SubtotalCents: subtotal, ReasonCode: TaxStdON13})
	if err != nil {
		t.Fatal(err)
	}
	split := moneyutil.AllocateLargestRemainder(hdr.TaxCents, parityLines)
	var sum int64
	for _, s := range split {
		sum += s
	}
	if sum != hdr.TaxCents {
		t.Errorf("the per-line tax split must be exhaustive: Σ=%d header=%d", sum, hdr.TaxCents)
	}
	if len(split) != len(parityLines) {
		t.Fatalf("the split must produce one number per line: got %d for %d lines", len(split), len(parityLines))
	}

	// -- 4. header-level rounding, NOT per-line rounding --
	// This is the specific bug parity exists to prevent. Rounding 13% on each line and summing gives a
	// different answer from rounding once at the header, and the two producers must make the SAME
	// choice. Both round once, at the header.
	var perLineRounded int64
	for _, l := range parityLines {
		perLineRounded += (l*flatHSTRateBps + 5000) / 10000
	}
	if perLineRounded == hdr.TaxCents {
		// NOT a skip. A basket that cannot tell the two rounding modes apart makes this whole test
		// vacuous, and a vacuously-passing parity test is worse than none: it reports safety it is not
		// checking. If someone edits parityLines into round numbers, this fails and says why.
		t.Fatalf("parityLines no longer discriminates header vs per-line rounding (both %d), "+
			"this test is vacuous until the basket is sharpened", hdr.TaxCents)
	}
	if hdr.TaxCents != (subtotal*flatHSTRateBps+5000)/10000 {
		t.Errorf("tax must be rounded ONCE at the header: got %d want %d",
			hdr.TaxCents, (subtotal*flatHSTRateBps+5000)/10000)
	}
}

// TestInvoiceWriter_SeamsDefaultSafely: a mis-wired composition root must degrade to the shipped
// behaviour, never to a nil dereference on the first invoice of the day.
func TestInvoiceWriter_SeamsDefaultSafely(t *testing.T) {
	t.Parallel()
	w := NewInvoiceWriter(nil)
	if _, ok := w.assessor().(FlatHSTAssessor); !ok {
		t.Error("a fresh writer must assess flat 13%")
	}
	w.WithTaxAssessor(nil)
	if _, ok := w.assessor().(FlatHSTAssessor); !ok {
		t.Error("binding a nil assessor must be a no-op, not a nil seam")
	}
	// An unbound order source is a NAMED error, not a panic: the operator gets "not wired", which is
	// actionable, rather than a 500 that means nothing.
	if _, err := w.InvoiceOrder(context.Background(), "some-order"); err != ErrOrderSourceUnwired {
		t.Errorf("an unbound order source must report itself; got %v", err)
	}
	if _, err := w.InvoiceOrderGroups(context.Background(), "o", []string{"g"}, "ON_ALL_COMPLETE"); err != ErrOrderSourceUnwired {
		t.Errorf("an unbound order source must report itself on the group path too; got %v", err)
	}
}

// TestMultiShipKey_IsSetIdentityNotListIdentity, the idempotency key must treat the SAME groups in a
// different order as one document, and DIFFERENT subsets of one order as different documents.
//
// This is not fussiness. Under 0126's ON_DEMAND_SUBSET a phased job is billed in slices; if the key
// were order-sensitive, a retry whose group list came back from the database in a different sort
// order would mint a SECOND invoice for work already billed, and the customer would be charged twice
// for one delivery.
func TestMultiShipKey_IsSetIdentityNotListIdentity(t *testing.T) {
	t.Parallel()
	a := multiShipKey("ord-1", []string{"g3", "g1", "g2"})
	b := multiShipKey("ord-1", []string{"g1", "g2", "g3"})
	if a != b {
		t.Errorf("the same group SET must key identically regardless of order:\n  %s\n  %s", a, b)
	}
	if c := multiShipKey("ord-1", []string{"g1", "g2"}); c == a {
		t.Error("a strict subset must NOT collide with the full set, that would skip billing a group")
	}
	if d := multiShipKey("ord-2", []string{"g1", "g2", "g3"}); d == a {
		t.Error("the same group ids under a different order must not collide")
	}
	// The empty set is still a stable key (the whole-order path uses its own prefix, so no collision).
	if multiShipKey("ord-1", nil) == "order-invoice:ord-1" {
		t.Error("the group-invoice and whole-order key spaces must not overlap")
	}
}

// TestCreditMemo_InputGuards, the sign is billing's business, and a memo without a retry key is
// refused. Money leaving the company is the one place a duplicate is a real loss.
func TestCreditMemo_InputGuards(t *testing.T) {
	t.Parallel()
	w := NewInvoiceWriter(nil)
	ctx := context.Background()

	if _, err := w.IssueCreditMemo(ctx, CreditMemoInput{AmountCents: 100}); err == nil {
		t.Error("a credit memo without an idempotency key must be refused")
	}
	// A negative amount is a caller that has guessed at billing's convention. Refused loudly so the
	// guess is corrected rather than silently double-negated into a positive invoice.
	if _, err := w.IssueCreditMemo(ctx, CreditMemoInput{AmountCents: -100, IdempotencyKey: "k"}); err == nil {
		t.Error("a negative credit-memo amount must be refused, the sign belongs to billing")
	}
}

// compile-time assertions that the seams this package declares are the shapes the union will bind.
var (
	_ TaxAssessor      = FlatHSTAssessor{}
	_ TaxAssessor      = (*recordingAssessor)(nil)
	_ CreditMemoPoster = (*recordingPoster)(nil)
)

// recordingAssessor is a controllable tax seam for the integration tests.
type recordingAssessor struct {
	taxCents int64
	calls    []TaxQuoteInput
}

func (a *recordingAssessor) AssessSale(_ context.Context, in TaxQuoteInput) (TaxQuoteResult, error) {
	a.calls = append(a.calls, in)
	return TaxQuoteResult{TaxCents: a.taxCents, RateBps: 1300}, nil
}

// recordingPoster captures the reversing entry so its SIDES can be asserted.
type recordingPoster struct {
	journals []CreditMemoJournal
	err      error
}

func (p *recordingPoster) PostCreditMemoJournal(_ context.Context, in CreditMemoJournal) (string, error) {
	p.journals = append(p.journals, in)
	if p.err != nil {
		return "", p.err
	}
	return "jrnl-" + in.CreditMemoID, nil
}

// TestCreditMemo_ReversingEntrySwapsSidesNotSigns is the named test the credit_memo.go header points
// at, and it exists because a mutation SURVIVED without it.
//
// The side swap used to live in main.go's adapter. Flipping AR there from credited to debited, the
// exact negate-instead-of-swap bug the whole design exists to prevent, killed nothing, because no
// test in any module can reach a composition-root adapter. Arithmetic no test can see is arithmetic
// with no owner, so it moved here.
func TestCreditMemo_ReversingEntrySwapsSidesNotSigns(t *testing.T) {
	t.Parallel()
	j := CreditMemoJournal{ARCents: 1_412_50, RevenueCents: 1_250_00, TaxCents: 162_50}
	legs := j.ReversingLegs()

	byRole := map[string]JournalLeg{}
	var drTotal, crTotal int64
	for _, l := range legs {
		byRole[l.Role] = l
		drTotal += l.DebitCents
		crTotal += l.CreditCents
		// EVERY leg is a positive magnitude on exactly one side. gl_journal_lines CHECKs
		// (debit_cents > 0 XOR credit_cents > 0), so a negative or two-sided leg is not storable.
		if l.DebitCents < 0 || l.CreditCents < 0 {
			t.Errorf("%s: a ledger leg is never negative; got DR %d CR %d", l.Role, l.DebitCents, l.CreditCents)
		}
		if (l.DebitCents == 0) == (l.CreditCents == 0) {
			t.Errorf("%s: exactly one side must carry the amount; got DR %d CR %d", l.Role, l.DebitCents, l.CreditCents)
		}
	}

	// THE SWAP. An invoice does DR ar / CR revenue / CR tax; the reversal does the opposite sides
	// with the same magnitudes.
	if got := byRole[RoleRevenue]; got.DebitCents != 1_250_00 || got.CreditCents != 0 {
		t.Errorf("revenue must be DEBITED on a reversal (an invoice credits it); got DR %d CR %d",
			got.DebitCents, got.CreditCents)
	}
	if got := byRole[RoleTax]; got.DebitCents != 162_50 || got.CreditCents != 0 {
		t.Errorf("tax must be DEBITED on a reversal; got DR %d CR %d", got.DebitCents, got.CreditCents)
	}
	if got := byRole[RoleAR]; got.CreditCents != 1_412_50 || got.DebitCents != 0 {
		t.Errorf("AR must be CREDITED on a reversal (an invoice debits it); got DR %d CR %d",
			got.DebitCents, got.CreditCents)
	}
	if drTotal != crTotal {
		t.Errorf("the reversing journal must balance: DR %d ≠ CR %d", drTotal, crTotal)
	}

	// Roles, never account codes: the chart belongs to gl and is branch-overridable, so a hard-coded
	// '4000' in billing would be a second, stale copy of a configurable fact.
	for _, l := range legs {
		switch l.Role {
		case RoleAR, RoleRevenue, RoleTax:
		default:
			t.Errorf("unknown journal role %q, billing names roles, gl owns the codes", l.Role)
		}
	}

	// A zero-tax memo emits NO tax leg, rather than a zero one that the CHECK would refuse.
	for _, l := range (CreditMemoJournal{ARCents: 500, RevenueCents: 500}).ReversingLegs() {
		if l.Role == RoleTax {
			t.Error("a zero-tax memo must not emit a zero tax leg, gl_journal_lines refuses it")
		}
	}
}
