package payments

// counter_receipt.go, DIB-510: recording a payment that has ALREADY BEEN TAKEN.
//
// - WHAT THIS IS NOT -
// It is not a charge. Nothing here talks to a gateway, claims a payment_intent or produces a
// gateway_ref. The DIB-415 intent machinery guards the window between "we asked a processor for
// money" and "we learned whether it moved"; a cheque handed across a counter has no such window -
// the money is already in the drawer and the only question is whether we wrote it down twice.
//
// - THE GUARANTEE THIS FILE OWES -
// A REPEATED "RECORD PAYMENT" MUST NOT DOUBLE-POST. The register's gold action is the kind of button
// that gets pressed twice: it is at the bottom of a panel, it is the end of a phone call, and the
// response is a re-render rather than a page change. So the guard is the DATABASE's, not a flag in
// this process:
//
//   0028 gave `payments` an idempotency_key column and a PARTIAL UNIQUE INDEX over it. The insert
//   below is ON CONFLICT DO NOTHING against that index. The second request inserts nothing, reads the
//   payment the first one wrote, and returns it with Replayed=true, having applied no allocation,
//   moved no balance and emitted no event, because it never reaches the code that does those things.
//
// That is a stronger promise than "we checked first": a read-then-write loses under concurrency, and
// two counter terminals ringing the same cheque is exactly the concurrency that happens.
//
// - WHY THE SETTLE IS THE SHIPPED SETTLE -
// The allocation, the balance move, the subledger row and the payment.received outbox emit all come
// from applyAllocations, the same function PayWithIntent calls, extracted rather than copied. A
// second implementation of "apply money to an invoice" is how a ledger acquires two answers to one
// question. The only thing this path does differently is the header row (it carries a key and a
// reference and no gateway) and the attribution: a counter receipt stamps WHO took it and WHO applied
// it, which a portal payment cannot.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// CounterTenders are the methods a receivable can actually be settled with at a desk. It is the 0010
// CHECK vocabulary minus OTHER, an unnamed tender on an AR receipt is a reconciliation problem
// somebody inherits, and the four named ones cover every way money reaches this business.
//
// "Charge account" is deliberately NOT here. Charging the account is what CREATED this invoice;
// offering it as a way to pay the invoice would let an operator settle a debt with itself. The
// register offers Apply credit for the case that control was reaching for.
var CounterTenders = []string{"EFT", "CHEQUE", "CARD", "CASH"}

// ReceiptRequest is one recorded receipt against one or more open invoices.
type ReceiptRequest struct {
	CustomerID     string
	Method         string
	Reference      string // cheque number / EFT trace / terminal ref, recorded, never invented
	IdempotencyKey string
	ReceivedBy     string // the staff caller; also the allocator (0043 allocated_by)
	BranchID       string // the drawer's branch when known; "" falls back to the invoice's
	Allocations    []Allocation
}

// ReceiptOutcome is the settle's answer. Replayed=true means THIS REQUEST POSTED NOTHING.
type ReceiptOutcome struct {
	Payment  Payment
	Replayed bool
	// Reference and ReceivedBy are what the stored payment says (the reference as recorded, the
	// staff member who took it), also on a replay. The pool entry leaves both empty on a replay,
	// as it always did; the transaction entry fills them.
	Reference  string
	ReceivedBy string
}

// RecordReceipt writes one already-taken payment against open invoices, atomically and idempotently.
func (r *Repository) RecordReceipt(ctx context.Context, in ReceiptRequest) (ReceiptOutcome, error) {
	// THE REQUEST IS VALIDATED BEFORE THE POOL IS CONSULTED. A malformed receipt is malformed whether
	// or not this process has a database behind it, and answering "database not configured" to somebody
	// who sent an invented tender sends them looking at infrastructure for a typo.
	if err := validateReceipt(in); err != nil {
		return ReceiptOutcome{}, err
	}
	if r.pool == nil {
		return ReceiptOutcome{}, errNoDB
	}

	// The replay fast path, BEFORE any lock. A double-click that arrives after the first commit never
	// touches an invoice row at all.
	if p, ok, err := r.receiptByKey(ctx, in.IdempotencyKey); err != nil {
		return ReceiptOutcome{}, err
	} else if ok {
		return ReceiptOutcome{Payment: p, Replayed: true}, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ReceiptOutcome{}, fmt.Errorf("receipt: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	p, claimed, err := settleReceipt(ctx, tx, in)
	if err != nil {
		return ReceiptOutcome{}, err
	}
	if !claimed {
		// Somebody else claimed the key between the fast path and here. Roll back, nothing has been
		// applied, and answer with the payment that actually exists.
		_ = tx.Rollback(ctx)
		if existing, ok, readErr := r.receiptByKey(ctx, in.IdempotencyKey); readErr != nil {
			return ReceiptOutcome{}, readErr
		} else if ok {
			return ReceiptOutcome{Payment: existing, Replayed: true}, nil
		}
		return ReceiptOutcome{}, fmt.Errorf("payments: receipt key %q was claimed and then vanished", in.IdempotencyKey)
	}
	if err := tx.Commit(ctx); err != nil {
		return ReceiptOutcome{}, fmt.Errorf("receipt: commit: %w", err)
	}
	return ReceiptOutcome{Payment: p, Reference: in.Reference, ReceivedBy: in.ReceivedBy}, nil
}

// RecordReceiptTx is RecordReceipt on the CALLER'S transaction (QD-5): the same validation, the same
// replay guard, the same settle, and no begin, no commit and no rollback of its own, so the receipt
// commits or rolls back with whatever the caller wrote before it (the order, the invoice) and holds
// no connection but the caller's. Everything it reads, it reads on that transaction, because a
// receipt against an invoice raised a moment ago in the same transaction cannot be seen by the pool.
//
// The key is checked FIRST, on the transaction, so a repeated key answers the stored payment
// (Replayed) having touched nothing; the answer carries the stored reference and the receiver, and
// no allocations (the replay's reader is the counter door, which needs the receipt, not the split).
// Nothing in here calls out of the process: the four counter tenders are money already in the drawer.
func (r *Repository) RecordReceiptTx(ctx context.Context, tx pgx.Tx, in ReceiptRequest) (ReceiptOutcome, error) {
	if err := validateReceipt(in); err != nil {
		return ReceiptOutcome{}, err
	}
	if p, ref, by, ok, err := receiptByKeyOn(ctx, tx, in.IdempotencyKey); err != nil {
		return ReceiptOutcome{}, err
	} else if ok {
		return ReceiptOutcome{Payment: p, Replayed: true, Reference: ref, ReceivedBy: by}, nil
	}
	p, claimed, err := settleReceipt(ctx, tx, in)
	if err != nil {
		return ReceiptOutcome{}, err
	}
	if !claimed {
		// A concurrent writer committed this key while this statement waited on it: the row is
		// visible to this statement's snapshot now, so answer it.
		if existing, ref, by, ok, readErr := receiptByKeyOn(ctx, tx, in.IdempotencyKey); readErr != nil {
			return ReceiptOutcome{}, readErr
		} else if ok {
			return ReceiptOutcome{Payment: existing, Replayed: true, Reference: ref, ReceivedBy: by}, nil
		}
		return ReceiptOutcome{}, fmt.Errorf("payments: receipt key %q was claimed and then vanished", in.IdempotencyKey)
	}
	return ReceiptOutcome{Payment: p, Reference: in.Reference, ReceivedBy: in.ReceivedBy}, nil
}

// validateReceipt is the request check both entries make before any database is consulted.
func validateReceipt(in ReceiptRequest) error {
	if in.IdempotencyKey == "" {
		// Refused rather than defaulted. A receipt with no key is a receipt with no double-post guard,
		// and silently accepting one would make the guarantee above conditional on the client
		// remembering, which is the same as not having it.
		return httpx.BadRequestf("recording a payment needs an idempotency key")
	}
	if !validTender(in.Method) {
		return httpx.BadRequestf("tender must be one of %s", strings.Join(CounterTenders, ", "))
	}
	if len(in.Allocations) == 0 {
		return httpx.BadRequestf("a payment must be applied to at least one invoice")
	}
	return nil
}

// settleReceipt is the one settle both entries share, on a transaction the caller owns: lock and total
// the invoices, claim the key with ON CONFLICT DO NOTHING (the whole guard, see the file header) and
// apply the allocations. claimed is false when the key already existed, and then nothing was applied.
func settleReceipt(ctx context.Context, tx pgx.Tx, in ReceiptRequest) (p Payment, claimed bool, err error) {
	total, invBranch, err := lockAndTotal(ctx, tx, in.CustomerID, in.Allocations)
	if err != nil {
		return Payment{}, false, err
	}
	branchID := in.BranchID
	if branchID == "" {
		branchID = invBranch
	}

	var pCustomerID *string
	var receivedBy any
	if in.ReceivedBy != "" {
		receivedBy = in.ReceivedBy
	}
	// ON CONFLICT DO NOTHING against ux_payments_idempotency_key (0028). RETURNING yields no row when
	// the key was already used, which is the whole guard, see the file header. The row also stamps
	// the 0213 sync legs (the payments feed seq, the org off the branch's location), the settle's
	// own posture: a counter receipt FOR a customer's account is that payer's history.
	row := tx.QueryRow(ctx, paymentsFeedSeqCTE+`
INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id,
                       gateway_ref, received_by, idempotency_key, metadata, org_id, seq)
		 VALUES (nextval('payment_no_seq')::text, $1, $2, $3, 'RECEIVED', $4, '', $5::uuid, $6,
		         jsonb_build_object('reference', $7::text, 'source', 'AR_REGISTER'),
		         (SELECT org_id FROM locations WHERE id = $4::uuid),
		         (SELECT next_value FROM pseq))
		 ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING
		 RETURNING id::text, payment_no::text, customer_id::text, method, amount_cents, status, received_at`,
		in.CustomerID, in.Method, total, branchID, receivedBy, in.IdempotencyKey, in.Reference)
	err = row.Scan(&p.ID, &p.PaymentNo, &pCustomerID, &p.Method, &p.AmountCents, &p.Status, &p.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, false, nil
	}
	if err != nil {
		return Payment{}, false, fmt.Errorf("receipt: insert payment: %w", err)
	}
	if pCustomerID != nil {
		p.CustomerID = *pCustomerID
	}

	if err := applyAllocations(ctx, tx, &p, in.CustomerID, in.Method, "", branchID, in.Allocations, in.ReceivedBy); err != nil {
		return Payment{}, false, err
	}
	return p, true, nil
}

// PayableCentsFor is the ceiling a Record Payment may offer for one invoice: balance − holdback,
// floored at zero. Exported so the READ that draws the control and the WRITE that honours it use one
// definition, a screen whose maximum disagreed with the server's would refuse a payment the operator
// had already accepted from a customer.
func (r *Repository) PayableCentsFor(ctx context.Context, invoiceID string) (int64, string, error) {
	if r.pool == nil {
		return 0, "", errNoDB
	}
	var balance, holdback int64
	var customerID string
	err := r.pool.QueryRow(ctx,
		`SELECT balance_cents, holdback_cents, customer_id::text FROM invoices WHERE id = $1::uuid`,
		invoiceID).Scan(&balance, &holdback, &customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, "", ErrNotFound
		}
		return 0, "", fmt.Errorf("receipt: payable: %w", err)
	}
	payable := balance - holdback
	if payable < 0 {
		payable = 0
	}
	return payable, customerID, nil
}

// receiptByKey reads a settled receipt by its one-time key (the replay answer), with its allocations.
func (r *Repository) receiptByKey(ctx context.Context, key string) (Payment, bool, error) {
	var p Payment
	var customerID *string
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, payment_no::text, customer_id::text, method, amount_cents, status, received_at
		   FROM payments WHERE idempotency_key = $1`, key).
		Scan(&p.ID, &p.PaymentNo, &customerID, &p.Method, &p.AmountCents, &p.Status, &p.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, false, nil
	}
	if err != nil {
		return Payment{}, false, fmt.Errorf("receipt: read by key: %w", err)
	}
	if customerID != nil {
		p.CustomerID = *customerID
	}
	byPayment, err := r.allocationsByPayment(ctx, []string{p.ID})
	if err != nil {
		return Payment{}, false, err
	}
	p.Allocations = byPayment[p.ID]
	if p.Allocations == nil {
		p.Allocations = []Allocation{}
	}
	return p, true, nil
}

// receiptByKeyOn reads a settled receipt by its key on any querier (the caller's transaction), with
// the reference and the receiver as stored. It carries no allocations.
func receiptByKeyOn(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, key string) (p Payment, reference, receivedBy string, ok bool, err error) {
	var customerID *string
	err = q.QueryRow(ctx,
		`SELECT id::text, payment_no::text, customer_id::text, method, amount_cents, status, received_at,
		        COALESCE(metadata->>'reference', ''), COALESCE(received_by::text, '')
		   FROM payments WHERE idempotency_key = $1`, key).
		Scan(&p.ID, &p.PaymentNo, &customerID, &p.Method, &p.AmountCents, &p.Status, &p.ReceivedAt, &reference, &receivedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, "", "", false, nil
	}
	if err != nil {
		return Payment{}, "", "", false, fmt.Errorf("receipt: read by key: %w", err)
	}
	if customerID != nil {
		p.CustomerID = *customerID
	}
	p.Allocations = []Allocation{}
	return p, reference, receivedBy, true, nil
}

func validTender(m string) bool {
	for _, t := range CounterTenders {
		if t == m {
			return true
		}
	}
	return false
}
