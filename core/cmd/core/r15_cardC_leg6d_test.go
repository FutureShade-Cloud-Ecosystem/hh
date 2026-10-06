package main

// r15_cardC_leg6d_test.go is crash window leg 6(d), the lost commit acknowledgement (the card
// charge design section 5.3 leg 6), on the r12 rig: the leg owns a RunInTx double whose Commit
// really commits and whose call then returns an error, the exact shape of a commit whose
// acknowledgement the process never saw. Through the packet B wire adapter (the same object the
// ring's compensator holds), the leg pins what the design's table row promises: the compensator
// re-reads the row BOUND and answers the committed sale's transaction (COMMITTED, void nothing,
// refund nothing), and packet C's sweep, run over the row after, moves nothing and calls
// nothing. The row stays BOUND with its payments row.
//
// Red on the packet B tip: the leg does not compile there (no sweep to call), and the brief's
// own red shape line names the compensator's current behavior: the committed arm shipped with
// packet B, so its pins hold before and after packet C. The leg exists to pin them ACROSS the
// sweep's landing; its bite breaks the compensator's committed arm, not the sweep. Gated on
// HH_CORE_TEST_DATABASE_URL like the other composition suites; each run names its own database
// on the command.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// errLostCommitAck is the double's fabricated answer: the commit landed, the acknowledgement
// did not survive.
var errLostCommitAck = errors.New("the commit acknowledgement was lost")

// runInTxLostAckDouble is RunInTx's shape with the lost acknowledgement: Begin, fn, Commit for
// real, and only then the error, so everything fn wrote is committed exactly when the caller is
// told it failed.
func runInTxLostAckDouble(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return errLostCommitAck
}

// TestR15CardCLeg6d_LostCommitAckAnswersTheCommittedSaleAndTheSweepLeavesItAlone runs the
// capture, the committing-but-erring transaction, the compensator and the sweep, in order, on
// one captured leg.
func TestR15CardCLeg6d_LostCommitAckAnswersTheCommittedSaleAndTheSweepLeavesItAlone(t *testing.T) {
	host, seen := newWiringRunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	pool := wiringPool(t)
	key := wiringRailDB(t, pool, map[string]string{"api_key": "r15-cardc-not-a-secret"})
	w := plantWiringCounter(t, pool)
	tops, _, _ := wireFold(t, pool, key, w.branchID)

	ctx := context.Background()

	// The capture, through the rail the wiring closed: one approved charge, a CAPTURED row.
	const amount = int64(4321)
	cp, err := tops.cardRail.Capture(ctx, payment.CaptureInput{
		LegKey: uuidKey(t), SaleKey: uuidKey(t), LegSeq: 1, AmountCents: amount,
		BranchID: w.branchID, TillSessionID: w.sessionID, CardType: "CREDIT",
		AccountToken: "tok_r15_cardc_lostack",
	})
	if err != nil || cp.Kind != payment.CaptureKindCaptured {
		t.Fatalf("the capture = kind %q err %v, want CAPTURED", cp.Kind, err)
	}
	if charges, _ := seen.hits(); charges != 1 {
		t.Fatalf("the capture made %d charge request(s), want one", charges)
	}

	// The sale transaction's double: it writes the payments row, binds the attempt through the
	// wire adapter (the verify read takes the row lock FOR UPDATE, the guarded move rides the
	// same transaction), commits for real, and then reports the lost acknowledgement.
	payNo := "r15cardC-" + uuidKey(t)
	bindErr := runInTxLostAckDouble(ctx, pool, func(tx pgx.Tx) error {
		var payID string
		if err := tx.QueryRow(ctx, `INSERT INTO payments
			  (payment_no, method, amount_cents, branch_id, till_session_id, gateway_ref)
			 VALUES ($1, 'CARD', $2, $3::uuid, $4::uuid, $5) RETURNING id::text`,
			payNo, amount, w.branchID, w.sessionID, cp.Attempt.ProcessorRef).Scan(&payID); err != nil {
			return err
		}
		return tops.cardCharger.BindTx(ctx, tx, till.CardBind{
			AttemptID: cp.Attempt.ID, PaymentID: payID, LegKey: cp.Attempt.LegKey,
			TillSessionID: w.sessionID, AmountCents: amount,
		})
	})
	if !errors.Is(bindErr, errLostCommitAck) {
		t.Fatalf("the double = %v, want the lost acknowledgement after a real commit", bindErr)
	}

	// What the compensator sees is the truth the commit landed: BOUND, so the answer is the
	// committed sale's transaction and no reversal reaches the processor.
	comp, err := tops.cardCharger.Compensate(ctx, cp.Attempt.ID)
	if err != nil {
		t.Fatalf("the compensator: %v", err)
	}
	if comp.Kind != till.CardCompCommitted {
		t.Fatalf("the compensator answered %q, want COMMITTED: the in-request re-read finds the row BOUND", comp.Kind)
	}
	if _, refunds := seen.hits(); refunds != 0 {
		t.Fatalf("the compensator made %d reversal request(s), want none: a committed sale is never voided", refunds)
	}

	// The sweep, run over the row after the lost acknowledgement: the row is BOUND and inside
	// its grace, out of the sweep's listing and out of its hands. It moves nothing and the
	// processor still saw no reversal.
	sres, err := tops.cardRail.SweepCardAttempts(ctx, time.Now)
	if err != nil {
		t.Fatalf("the sweep: %v", err)
	}
	if sres.ToUnknown+sres.Voided+sres.ToVoidUnknown+sres.ToVoidDeclined != 0 || sres.Lost != 0 {
		t.Fatalf("the sweep's pass = %+v, want no move at all over a ring that committed", sres)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM card_charge_attempts WHERE id = $1::uuid`, cp.Attempt.ID).Scan(&status); err != nil {
		t.Fatalf("read the attempt: %v", err)
	}
	if status != "BOUND" {
		t.Fatalf("the attempt row = %s, want BOUND: neither the compensator nor the sweep moved it", status)
	}
	if _, refunds := seen.hits(); refunds != 0 {
		t.Fatalf("after the sweep the host saw %d reversal request(s), want none", refunds)
	}
}
