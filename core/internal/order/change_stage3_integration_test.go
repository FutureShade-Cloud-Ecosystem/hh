//go:build integration

// change_stage3_integration_test.go, SCOPE:F10 stage 3 (HH-681): the manager signs and applies, against
// the live database. Each leg needs Postgres because the property under test is a LOCK, a CONSTRAINT or
// a single transaction: tax re-assessed inside the apply, one outbox row, one idempotent write, and the
// refusals that must leave every table exactly as it was.
//
// The shared database holds other tests' rows, so every assertion reads only the order or the change
// this test created.
package order

import (
	"context"
	"errors"
	"sync"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	s3Topic    = "order.change.applied"
	s3Key2     = "22222222-2222-4222-8222-222222222222"
	s3RateBps  = 775 // the demo tenant's standard rate, 7.75 percent
	s3Immaterl = 1000
)

// s3Rate is the tax rate seam over a fixed rate.
type s3Rate struct{ bps int }

func (r s3Rate) StandardRateBps(context.Context, string) (int, error) { return r.bps, nil }

// s3Invoiced answers "a finalized invoice exists" for the money predicate.
type s3Invoiced struct{}

func (s3Invoiced) OrderHasFinalizedInvoice(context.Context, string) (bool, error) { return true, nil }

// s3Service is the change service wired as main.go wires it, plus the tax rate seam when bps > 0.
func s3Service(t *testing.T, pool *pgxpool.Pool, unitCents int64, bps int) *Service {
	t.Helper()
	svc := w5aChangeServiceOn(t, pool, unitCents, true)
	if bps > 0 {
		svc.WithStandardRate(s3Rate{bps: bps})
	}
	return svc
}

func s3Mgr() context.Context {
	return identity.ContextWithStaff(context.Background(), w5aStaff(true))
}

// s3SetMoney puts tax and freight on a seeded order the way a quote conversion leaves them: the header
// carries the tax and the quoted freight, total = subtotal + freight + tax.
func s3SetMoney(t *testing.T, pool *pgxpool.Pool, ord string, taxCents, freightCents int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE orders SET tax_cents = $2, quoted_freight_cents = $3, freight_cents = $3,
		       total_cents = subtotal_cents + $2 + $3 WHERE id = $1`, ord, taxCents, freightCents); err != nil {
		t.Fatalf("set order money: %v", err)
	}
}

func s3Money(t *testing.T, pool *pgxpool.Pool, ord string) (sub, tax, total int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT subtotal_cents, tax_cents, total_cents FROM orders WHERE id = $1`, ord).
		Scan(&sub, &tax, &total); err != nil {
		t.Fatalf("read order money: %v", err)
	}
	return
}

func s3LineTaxSum(t *testing.T, pool *pgxpool.Pool, ord string) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(tax_cents),0) FROM order_lines WHERE order_id = $1`, ord).Scan(&n); err != nil {
		t.Fatalf("read line tax: %v", err)
	}
	return n
}

// s3Outbox counts the sign-and-apply events for one change and registers their cleanup for the order.
func s3Outbox(t *testing.T, pool *pgxpool.Pool, changeID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events_outbox WHERE topic = $1 AND payload->>'change_id' = $2`,
		s3Topic, changeID).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

func s3CleanOutbox(t *testing.T, pool *pgxpool.Pool, ord string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM events_outbox WHERE topic = $1 AND payload->>'order_id' = $2`, s3Topic, ord)
	})
}

// s3Status reads the change row's status straight from Postgres.
func s3Status(t *testing.T, pool *pgxpool.Pool, changeID string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM change_orders WHERE id = $1`, changeID).Scan(&s); err != nil {
		t.Fatalf("read change status: %v", err)
	}
	return s
}

// s3RaiseApproved raises a QTY change on line 1 and walks it to APPROVED (an immaterial delta).
func s3RaiseApproved(t *testing.T, svc *Service, ord, qty string) ChangeOrder {
	t.Helper()
	co, err := w5aRaise(t, svc, s3Mgr(), ord, qty)
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	acked, err := svc.YardAck(s3Mgr(), co.ID, "")
	if err != nil {
		t.Fatalf("yard ack: %v", err)
	}
	if acked.Status != ChangeStatusApproved {
		t.Fatalf("status after ack = %q, want APPROVED", acked.Status)
	}
	return acked
}

// s3RaisePendingFinance raises a material change ($60 a unit, 10 to 20) and acks it to PENDING_FINANCE.
func s3RaisePendingFinance(t *testing.T, svc *Service, ord string) ChangeOrder {
	t.Helper()
	co, err := w5aRaise(t, svc, s3Mgr(), ord, "20")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	acked, err := svc.YardAck(s3Mgr(), co.ID, "")
	if err != nil {
		t.Fatalf("yard ack: %v", err)
	}
	if acked.Status != ChangeStatusPendingFinance {
		t.Fatalf("status after ack = %q, want PENDING_FINANCE", acked.Status)
	}
	return acked
}

// - tax -

// TestIntegration_S3_SignApplyReassessesTaxOnATaxedOrder is the blocker for a real dealer: on main a
// manager can never apply a change to a taxed order (ErrEditTaxAssessed). 10 to 12 units at $10.00
// takes the subtotal to 12000; the standard rate on that is 930 exactly; total = subtotal + tax.
func TestIntegration_S3_SignApplyReassessesTaxOnATaxedOrder(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	s3SetMoney(t, pool, ord, 775, 0)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)

	co := s3RaiseApproved(t, svc, ord, "12")
	out, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, "")
	if err != nil {
		t.Fatalf("sign and apply on a taxed order = %v, want tax re-assessed inside the apply", err)
	}
	if out.Status != ChangeStatusApplied {
		t.Fatalf("status = %q, want APPLIED", out.Status)
	}
	sub, tax, total := s3Money(t, pool, ord)
	if sub != 12000 || tax != 930 || total != 12930 {
		t.Fatalf("order money = subtotal %d tax %d total %d, want 12000 / 930 / 12930 (integer cents, "+
			"tax at 7.75 percent of the new subtotal, total = subtotal + tax)", sub, tax, total)
	}
}

// TestIntegration_S3_PlainApplyReassessesTaxToo: the separate apply route shares the one money path, so
// it stops refusing a taxed order as well. THIS leg fails on the unchanged code for the real reason:
// ApplyChange runs assertEditableMoney, which refuses any order whose tax_cents is not 0.
func TestIntegration_S3_PlainApplyReassessesTaxToo(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	s3SetMoney(t, pool, ord, 775, 0)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)

	co := s3RaiseApproved(t, svc, ord, "12")
	if _, err := svc.ApplyChange(s3Mgr(), co.ID); err != nil {
		t.Fatalf("apply on a taxed order = %v, want tax re-assessed inside the apply", err)
	}
	sub, tax, total := s3Money(t, pool, ord)
	if sub != 12000 || tax != 930 || total != 12930 {
		t.Fatalf("order money = %d / %d / %d, want 12000 / 930 / 12930", sub, tax, total)
	}
}

// TestIntegration_S3_TaxBaseIncludesTheQuotedFreight: the quote builder taxes subtotal plus delivery and
// the order total carries the delivery fee already, so the apply keeps both. Before: base 15000, tax
// 1163 (half up), total 16163. After 10 to 12 units: base 17000, tax 1318, total 12000 + 5000 + 1318.
func TestIntegration_S3_TaxBaseIncludesTheQuotedFreight(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	s3SetMoney(t, pool, ord, 1163, 5000)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)

	co := s3RaiseApproved(t, svc, ord, "12")
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); err != nil {
		t.Fatalf("sign and apply = %v", err)
	}
	sub, tax, total := s3Money(t, pool, ord)
	if sub != 12000 || tax != 1318 || total != 18318 {
		t.Fatalf("order money = subtotal %d tax %d total %d, want 12000 / 1318 / 18318 (the delivery fee "+
			"stays in the total and in the tax base)", sub, tax, total)
	}
}

// TestIntegration_S3_NilRateSeamFailsClosedOnATaxedOrder: no seam, tax on the order, nothing is guessed.
func TestIntegration_S3_NilRateSeamFailsClosedOnATaxedOrder(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	s3SetMoney(t, pool, ord, 775, 0)
	svc := s3Service(t, pool, s3Immaterl, 0)

	co := s3RaiseApproved(t, svc, ord, "12")
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); !errors.Is(err, ErrEditTaxAssessed) {
		t.Fatalf("sign and apply with no rate seam on a taxed order = %v, want ErrEditTaxAssessed", err)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 10 {
		t.Fatalf("line qty = %v after a refused apply, want the original 10", got)
	}
	if s := s3Status(t, pool, co.ID); s != ChangeStatusApproved {
		t.Fatalf("change status = %q after a refused apply, want it still APPROVED", s)
	}
	if n := s3Outbox(t, pool, co.ID); n != 0 {
		t.Fatalf("%d outbox rows after a refused apply, want 0", n)
	}
}

// TestIntegration_S3_UntaxedOrderStaysUntaxed: an order that carries no tax (exempt, or a pre-tax
// draft) is not suddenly taxed by a change; total = subtotal + freight.
func TestIntegration_S3_UntaxedOrderStaysUntaxed(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)

	co := s3RaiseApproved(t, svc, ord, "12")
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); err != nil {
		t.Fatalf("sign and apply = %v", err)
	}
	sub, tax, total := s3Money(t, pool, ord)
	if sub != 12000 || tax != 0 || total != 12000 {
		t.Fatalf("order money = %d / %d / %d, want 12000 / 0 / 12000", sub, tax, total)
	}
}

// TestIntegration_S3_LineTaxStillSumsToTheHeader: an order the tax engine split across its lines keeps
// lines that sum to the header after the apply (the yard pick rule: largest remainder).
func TestIntegration_S3_LineTaxStillSumsToTheHeader(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 5, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		SELECT order_id, 2, product_id, 'second line', qty_entered, uom_entered, qty_base,
		       unit_price_cents, price_uom, line_total_cents FROM order_lines WHERE order_id = $1 AND line_no = 1`, ord); err != nil {
		t.Fatalf("seed second line: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE orders SET subtotal_cents = 10000 WHERE id = $1`, ord); err != nil {
		t.Fatalf("seed subtotal: %v", err)
	}
	s3SetMoney(t, pool, ord, 775, 0)
	if _, err := pool.Exec(context.Background(),
		`UPDATE order_lines SET tax_cents = CASE line_no WHEN 1 THEN 388 ELSE 387 END WHERE order_id = $1`, ord); err != nil {
		t.Fatalf("seed line tax: %v", err)
	}
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)

	co := s3RaiseApproved(t, svc, ord, "10") // line 1: 5 to 10 units, +5000
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); err != nil {
		t.Fatalf("sign and apply = %v", err)
	}
	_, tax, _ := s3Money(t, pool, ord)
	if tax != 1163 {
		t.Fatalf("header tax = %d, want 1163 (15000 at 7.75 percent, half up)", tax)
	}
	if got := s3LineTaxSum(t, pool, ord); got != tax {
		t.Fatalf("line tax sums to %d, header is %d: the lines must always sum to the header", got, tax)
	}
}

// TestIntegration_S3_FinalizedInvoiceStillRefuses: the other halves of the money predicate stay.
func TestIntegration_S3_FinalizedInvoiceStillRefuses(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	s3SetMoney(t, pool, ord, 775, 0)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)
	co := s3RaiseApproved(t, svc, ord, "12")

	svc.WithCancelGuards(s3Invoiced{}, w5aNoSales{})
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); !errors.Is(err, ErrEditBlocked) {
		t.Fatalf("sign and apply on an invoiced order = %v, want ErrEditBlocked", err)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 10 {
		t.Fatalf("line qty = %v, want the original 10", got)
	}
}

// - the ladder -

func TestIntegration_S3_SignApplyFromPendingFinanceSignsAndApplies(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, 6000, s3RateBps)
	co := s3RaisePendingFinance(t, svc, ord)

	out, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, "approved on the phone")
	if err != nil {
		t.Fatalf("sign and apply = %v", err)
	}
	if out.Status != ChangeStatusApplied || out.AppliedAt == "" {
		t.Fatalf("status %q applied_at %q, want APPLIED and stamped", out.Status, out.AppliedAt)
	}
	if out.FinanceApprovedBy != w5aUser || out.FinanceApprovedAt == "" || out.FinanceNote != "approved on the phone" {
		t.Fatalf("finance signature = by %q at %q note %q, want the caller, stamped, with the note",
			out.FinanceApprovedBy, out.FinanceApprovedAt, out.FinanceNote)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 20 {
		t.Fatalf("line qty = %v, want 20", got)
	}
	if n, held, _ := w5aHolds(t, pool, ord); n != 1 || held != 20 {
		t.Fatalf("holds = %d row(s) for %v, want 1 for 20: the reservations resync in the same transaction", n, held)
	}
}

func TestIntegration_S3_SignApplyFromApprovedApplies(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)
	co := s3RaiseApproved(t, svc, ord, "12")

	out, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, "")
	if err != nil || out.Status != ChangeStatusApplied {
		t.Fatalf("sign and apply from APPROVED = %q, %v, want APPLIED", out.Status, err)
	}
	if out.FinanceApprovedBy != "" {
		t.Fatalf("finance_approved_by = %q on a change that never needed a signature, want empty", out.FinanceApprovedBy)
	}
}

func TestIntegration_S3_PendingYardRefusesAndWritesNothing(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)
	co, err := w5aRaise(t, svc, s3Mgr(), ord, "12")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); !errors.Is(err, ErrChangeYardAckRequired) {
		t.Fatalf("sign and apply before the yard ack = %v, want ErrChangeYardAckRequired", err)
	}
	if s := s3Status(t, pool, co.ID); s != ChangeStatusPendingYard {
		t.Fatalf("status = %q, want PENDING_YARD untouched", s)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 10 {
		t.Fatalf("line qty = %v, want the original 10", got)
	}
}

// - authority -

func TestIntegration_S3_NonManagerCannotSignOrApply(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, 6000, s3RateBps)
	co := s3RaisePendingFinance(t, svc, ord)

	frontline := identity.ContextWithStaff(context.Background(), w5aStaff(false))
	if _, err := svc.SignApplyChange(frontline, co.ID, s3Key, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a frontline caller signing = %v, want ErrForbidden", err)
	}
	if s := s3Status(t, pool, co.ID); s != ChangeStatusPendingFinance {
		t.Fatalf("status = %q, want PENDING_FINANCE untouched", s)
	}
}

func TestIntegration_S3_ManagerOutsideTheApproverListCannotSignAMaterialDelta(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, 6000, s3RateBps)
	co := s3RaisePendingFinance(t, svc, ord)

	svc.WithFinanceApprover(w5aApprover{err: errors.New("not a named approver")})
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an unnamed manager signing = %v, want ErrForbidden", err)
	}
	if s := s3Status(t, pool, co.ID); s != ChangeStatusPendingFinance {
		t.Fatalf("status = %q, want PENDING_FINANCE untouched", s)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 10 {
		t.Fatalf("line qty = %v, want the original 10", got)
	}
}

// TestIntegration_S3_ManagerOfAnotherBranchCannotReachTheChange: the branch wall answers not found on
// every read and every decision, never forbidden, so the surface is no existence oracle.
func TestIntegration_S3_ManagerOfAnotherBranchCannotReachTheChange(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, 6000, s3RateBps)
	co := s3RaisePendingFinance(t, svc, ord)

	other := w5aStaff(true)
	other.Branches = map[string]bool{secondBranch: true}
	octx := identity.ContextWithStaff(context.Background(), other)

	if _, err := svc.GetChange(octx, co.ID); !errors.Is(err, ErrChangeNotFound) {
		t.Errorf("read by another branch's manager = %v, want ErrChangeNotFound", err)
	}
	if _, err := svc.SignApplyChange(octx, co.ID, s3Key, ""); !errors.Is(err, ErrChangeNotFound) {
		t.Errorf("sign and apply by another branch's manager = %v, want ErrChangeNotFound", err)
	}
	if _, err := svc.ApplyChange(octx, co.ID); !errors.Is(err, ErrChangeNotFound) {
		t.Errorf("apply by another branch's manager = %v, want ErrChangeNotFound", err)
	}
	if _, err := svc.RejectChange(octx, co.ID, "no"); !errors.Is(err, ErrChangeNotFound) {
		t.Errorf("decline by another branch's manager = %v, want ErrChangeNotFound", err)
	}
	if s := s3Status(t, pool, co.ID); s != ChangeStatusPendingFinance {
		t.Fatalf("status = %q, want PENDING_FINANCE untouched", s)
	}
}

// TestIntegration_S3_DeclineKeepsTheNoteAndClosesTheChange: the manager declines with a note; a declined
// change can never be signed and applied afterwards.
func TestIntegration_S3_DeclineKeepsTheNoteAndClosesTheChange(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, 6000, s3RateBps)
	co := s3RaisePendingFinance(t, svc, ord)

	out, err := svc.RejectChange(s3Mgr(), co.ID, "customer rang back, leave it as it was")
	if err != nil {
		t.Fatalf("decline: %v", err)
	}
	if out.Status != ChangeStatusRejected || out.DecisionNote != "customer rang back, leave it as it was" {
		t.Fatalf("declined change = %q note %q, want REJECTED with the note", out.Status, out.DecisionNote)
	}
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); !errors.Is(err, ErrChangeTerminal) {
		t.Fatalf("sign and apply on a declined change = %v, want ErrChangeTerminal", err)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 10 {
		t.Fatalf("line qty = %v, want the original 10", got)
	}
}

// - idempotency and the single write -

// TestIntegration_S3_ReplayWithTheSameKeyReturnsTheFirstAnswerAndWritesNothing.
func TestIntegration_S3_ReplayWithTheSameKeyReturnsTheFirstAnswerAndWritesNothing(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	s3SetMoney(t, pool, ord, 775, 0)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)
	co := s3RaiseApproved(t, svc, ord, "12")

	first, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, "")
	if err != nil {
		t.Fatalf("first press: %v", err)
	}
	second, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, "")
	if err != nil {
		t.Fatalf("replay with the same key = %v, want the first answer", err)
	}
	if second.ID != first.ID || second.Status != ChangeStatusApplied || second.AppliedAt != first.AppliedAt {
		t.Fatalf("replay = %q applied %q, first = %q applied %q: want the same answer",
			second.Status, second.AppliedAt, first.Status, first.AppliedAt)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 12 {
		t.Fatalf("line qty = %v after a replay, want 12 (applied once)", got)
	}
	if _, tax, _ := s3Money(t, pool, ord); tax != 930 {
		t.Fatalf("tax = %d after a replay, want 930 (assessed once)", tax)
	}
	if n := s3Outbox(t, pool, co.ID); n != 1 {
		t.Fatalf("%d outbox rows after a replay, want exactly 1", n)
	}
}

func TestIntegration_S3_ADifferentKeyOnAnAppliedChangeIsTerminal(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)
	co := s3RaiseApproved(t, svc, ord, "12")
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); err != nil {
		t.Fatalf("first press: %v", err)
	}
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key2, ""); !errors.Is(err, ErrChangeTerminal) {
		t.Fatalf("a new key on an applied change = %v, want ErrChangeTerminal", err)
	}
	if n := s3Outbox(t, pool, co.ID); n != 1 {
		t.Fatalf("%d outbox rows, want exactly 1", n)
	}
}

// TestIntegration_S3_TheSameKeyOnADifferentChangeIsRefused: a key names one action.
func TestIntegration_S3_TheSameKeyOnADifferentChangeIsRefused(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)
	a := s3RaiseApproved(t, svc, ord, "12")
	if _, err := svc.SignApplyChange(s3Mgr(), a.ID, s3Key, ""); err != nil {
		t.Fatalf("first change: %v", err)
	}
	b := s3RaiseApproved(t, svc, ord, "14")
	if _, err := svc.SignApplyChange(s3Mgr(), b.ID, s3Key, ""); !errors.Is(err, ErrChangeKeyReused) {
		t.Fatalf("the same key on a second change = %v, want ErrChangeKeyReused", err)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 12 {
		t.Fatalf("line qty = %v, want 12: the second change must not have applied", got)
	}
	if s := s3Status(t, pool, b.ID); s != ChangeStatusApproved {
		t.Fatalf("second change status = %q, want it still APPROVED", s)
	}
}

// TestIntegration_S3_ConcurrentPressesApplyOnce: a double tap that carries ONE key applies once and
// every press gets an answer; four different keys apply once and the other three are told it is closed.
func TestIntegration_S3_ConcurrentPressesApplyOnce(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sameKey   bool
		wantWins  int
		wantClose int
	}{
		{"one key", true, 4, 0},
		{"four keys", false, 1, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := w5aWidePool(t)
			_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
			s3CleanOutbox(t, pool, ord)
			s3SetMoney(t, pool, ord, 775, 0)
			svc := s3Service(t, pool, s3Immaterl, s3RateBps)
			co := s3RaiseApproved(t, svc, ord, "25")

			keys := []string{
				"aaaaaaaa-0000-4000-8000-000000000001", "aaaaaaaa-0000-4000-8000-000000000002",
				"aaaaaaaa-0000-4000-8000-000000000003", "aaaaaaaa-0000-4000-8000-000000000004",
			}
			const racers = 4
			var wg sync.WaitGroup
			results := make([]error, racers)
			start := make(chan struct{})
			for i := 0; i < racers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					key := keys[0]
					if !tc.sameKey {
						key = keys[i]
					}
					_, results[i] = svc.SignApplyChange(s3Mgr(), co.ID, key, "")
				}(i)
			}
			close(start)
			wg.Wait()

			wins, closed := 0, 0
			for _, err := range results {
				switch {
				case err == nil:
					wins++
				case errors.Is(err, ErrChangeTerminal):
					closed++
				default:
					t.Errorf("unexpected result from a racer: %v", err)
				}
			}
			if wins != tc.wantWins || closed != tc.wantClose {
				t.Fatalf("%d answered, %d told closed, want %d and %d", wins, closed, tc.wantWins, tc.wantClose)
			}
			if got := w5aLineQty(t, pool, ord, 1); got != 25 {
				t.Fatalf("line qty = %v after the race, want 25 (the delta landed more than once)", got)
			}
			if sub, tax, total := s3Money(t, pool, ord); sub != 25000 || tax != 1938 || total != 26938 {
				t.Fatalf("order money = %d / %d / %d, want 25000 / 1938 / 26938", sub, tax, total)
			}
			if n := s3Outbox(t, pool, co.ID); n != 1 {
				t.Fatalf("%d outbox rows after the race, want exactly 1", n)
			}
		})
	}
}

// - outbox and audit -

func TestIntegration_S3_OneOutboxEventCarriesTheSignedAndAppliedChange(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	s3SetMoney(t, pool, ord, 775, 0)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)
	co := s3RaiseApproved(t, svc, ord, "12")
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); err != nil {
		t.Fatalf("sign and apply: %v", err)
	}

	var orderID, branchID, changeNo string
	var subtotal, tax, total, delta int64
	err := pool.QueryRow(context.Background(), `
		SELECT payload->>'order_id', payload->>'branch_id', payload->>'change_no',
		       (payload->>'subtotal_cents')::bigint, (payload->>'tax_cents')::bigint,
		       (payload->>'total_cents')::bigint, (payload->>'delta_cents')::bigint
		  FROM events_outbox WHERE topic = $1 AND payload->>'change_id' = $2`, s3Topic, co.ID).
		Scan(&orderID, &branchID, &changeNo, &subtotal, &tax, &total, &delta)
	if err != nil {
		t.Fatalf("read the outbox event: %v", err)
	}
	if orderID != ord || branchID != trentonBranch || changeNo != co.ChangeNo {
		t.Errorf("event ids = order %q branch %q change %q, want %q / %q / %q",
			orderID, branchID, changeNo, ord, trentonBranch, co.ChangeNo)
	}
	if subtotal != 12000 || tax != 930 || total != 12930 || delta != 2000 {
		t.Errorf("event money = %d / %d / %d / delta %d, want 12000 / 930 / 12930 / 2000 (integer cents "+
			"as written to the order)", subtotal, tax, total, delta)
	}
}

// TestIntegration_S3_TheApplyAuditRowCarriesTheKey: the key rides the allowlisted client_id on the
// order's own audit row (the convention the quote door uses), written on the apply's transaction.
func TestIntegration_S3_TheApplyAuditRowCarriesTheKey(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)
	co := s3RaiseApproved(t, svc, ord, "12")
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, ""); err != nil {
		t.Fatalf("sign and apply: %v", err)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM audit_log
		 WHERE entity_type = 'order' AND entity_id = $1 AND changes->>'field' = 'change_order'
		   AND changes->>'outcome' = 'CHANGE_APPLIED' AND changes->>'client_id' = $2`, ord, s3Key).
		Scan(&n); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d CHANGE_APPLIED audit rows carry the key, want exactly 1", n)
	}
}
