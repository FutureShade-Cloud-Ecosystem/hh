//go:build integration

// Integration tests run the real pgx Repository + the deterministic RUN-PAY stub against the Postgres
// that `make reset` provisions, exercising migrations 0027+0028 (I1), end-to-end idempotency (I2),
// card via the stub with card_type round-trip (I3), the derived over/short close (I4), the conditional
// manager gate with real lifecycle (I5), and the in-caller-tx rollback (I6). Run with:
//
//	make reset && go test -tags integration ./internal/till/...
//
// No live processor call anywhere, the CARD path uses payment.RunPaymentsStub (no network I/O).
package till

import (
	"context"
	"errors"
	"os"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// stubCharger wraps the deterministic RunPaymentsStub as a till.CardCharger (no live call, no
// config row, no attempt table): it mirrors the capture seam's shape over the stub processor,
// returning a stable captured ref for any positive amount + token. The bind arm accepts the facts
// it is handed (the real adapter guards them against the attempt row, which this stub does not
// keep), and the compensation arm answers a clean reversal.
type stubCharger struct{ proc payment.Processor }

func (s stubCharger) Capture(ctx context.Context, in CardChargeInput) (CardCapture, error) {
	if in.ReadOnly {
		return CardCapture{}, ErrCardLegUnattempted
	}
	res, err := s.proc.Charge(ctx, map[string]string{"api_key": "stub", "currency": "CAD"}, "MID-TEST", in.AmountCents, in.AccountToken)
	if err != nil {
		return CardCapture{}, err
	}
	if res.Status != "captured" {
		return CardCapture{}, ErrCardDeclined
	}
	return CardCapture{Kind: CardCaptureCaptured, AttemptID: "att-stub-" + in.LegKey, ProcessorRef: res.ProcessorRef}, nil
}

func (s stubCharger) BindTx(_ context.Context, _ pgx.Tx, b CardBind) error {
	if b.AttemptID == "" || b.PaymentID == "" {
		return errors.New("stub charger: the bind arrived without its attempt or payment id")
	}
	return nil
}

func (s stubCharger) Compensate(_ context.Context, attemptID string) (CardCompensation, error) {
	return CardCompensation{Kind: CardCompVoided, AttemptID: attemptID}, nil
}

func realSvc(pool *pgxpool.Pool) *Service {
	return NewService(NewRepository(pool)).
		WithCardCharger(stubCharger{proc: payment.RunPaymentsStub{}}).
		withCaller(testCallerFrom)
}

// seedBranchUser returns a seeded BRANCH location id + an ACTIVE user id (skips if the DB is unseeded).
func seedBranchUser(t *testing.T, pool *pgxpool.Pool) (branchID, userID string) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH location: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	return branchID, userID
}

func globalCtx(userID string) context.Context {
	return ctxWith(context.Background(), identity.StaffContext{
		UserID: userID, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})
}

func frontlineCtx(userID, branchID string) context.Context {
	return ctxWith(context.Background(), identity.StaffContext{
		UserID: userID, Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Status: "ACTIVE",
		Branches: map[string]bool{branchID: true},
	})
}

func managerCtx(userID, branchID string) context.Context {
	return ctxWith(context.Background(), identity.StaffContext{
		UserID: userID, Tier: identity.TierManager, Scope: identity.ScopeBranch, Status: "ACTIVE",
		Branches: map[string]bool{branchID: true},
	})
}

// I1, the 0027 + 0028 schema is live (the columns A-03 writes incl. the two consumed 0027 columns).
func TestIntegration_Schema(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	cols := []struct{ table, col string }{
		{"till_sessions", "counted_by"}, {"till_sessions", "completed_by"},
		{"till_sessions", "closing_float_cents"}, {"till_sessions", "override_notes"}, {"till_sessions", "blind_close"},
		{"payments", "idempotency_key"}, {"payments", "offline_captured"}, {"payments", "signature_ref"},
		{"payments", "card_type"}, // RUN-PAY's 0027 column A-03 writes
	}
	for _, c := range cols {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`,
			c.table, c.col).Scan(&n); err != nil {
			t.Fatalf("schema query %s.%s: %v", c.table, c.col, err)
		}
		if n != 1 {
			t.Fatalf("missing column %s.%s", c.table, c.col)
		}
	}
	for _, tbl := range []string{"till_tender_totals", "till_denomination_counts", "till_movements", "till_policy"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name = $1`, tbl).Scan(&n); err != nil {
			t.Fatalf("schema table %s: %v", tbl, err)
		}
		if n != 1 {
			t.Fatalf("missing table %s", tbl)
		}
	}
	// The status CHECK includes 'PENDING' (RUN-PAY 0027) and card_type carries the 4-value CHECK.
	var def string
	if err := pool.QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'payments_status_check'`).Scan(&def); err != nil {
		t.Fatalf("status check: %v", err)
	}
	if !contains(def, "PENDING") {
		t.Fatalf("payments status CHECK missing PENDING: %s", def)
	}
}

// I2, open → TakeTender CASH (real pgx tx) → replay same key → same id, exactly one row.
func TestIntegration_CashIdempotency(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "ITEST", FloatCents: 20000})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	key := "itest-key-" + sid
	first := takeInTx(t, pool, svc, cctx, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, IdempotencyKey: key})
	second := takeInTx(t, pool, svc, cctx, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, IdempotencyKey: key})
	if first != second {
		t.Fatalf("replay returned a different id: %s != %s", first, second)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("idempotent replay wrote %d rows, want 1", n)
	}
}

// I3, the card two-half: CaptureCard on the pool (the stub's captured ref), then TakeTender in a
// real pgx tx binding that capture → gateway_ref + card_type RECEIVED on the real 0027 column.
func TestIntegration_CardViaStub(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "ITEST-CARD", FloatCents: 0})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	key := "itest-card-" + sid
	cap, err := svc.CaptureCard(cctx, CardChargeInput{
		TillSessionID: sid, BranchID: branchID, AmountCents: 12000,
		LegKey: key, SaleKey: key, LegSeq: 1, AccountToken: "tok_itest", CardType: CardCredit,
	})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	pid := takeInTx(t, pool, svc, cctx, TenderInput{TillSessionID: sid, Method: MethodCard, AmountCents: 12000,
		PaymentToken: "tok_itest", CardType: CardCredit, IdempotencyKey: key, Capture: cap})

	var status, cardType, gatewayRef string
	if err := pool.QueryRow(ctx, `SELECT status, card_type, gateway_ref FROM payments WHERE id = $1`, pid).Scan(&status, &cardType, &gatewayRef); err != nil {
		t.Fatalf("read card payment: %v", err)
	}
	if status != PaymentReceived || cardType != CardCredit || gatewayRef == "" || gatewayRef != cap.ProcessorRef {
		t.Fatalf("card row wrong: status=%s card_type=%s ref=%q (captured %q)", status, cardType, gatewayRef, cap.ProcessorRef)
	}
}

// I4, movements + count + complete with an over/short → derived Z-report + over_short_cents snapshot.
func TestIntegration_ReconciliationSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "ITEST-RECON", FloatCents: 20000})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	_ = takeInTx(t, pool, svc, cctx, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 60000, IdempotencyKey: "itest-recon-" + sid})
	if err := svc.PaidOut(cctx, MovementInput{TillSessionID: sid, MovementType: MovementPaidOut, Method: MethodCash, AmountCents: 50000, Reason: "drop"}); err != nil {
		t.Fatalf("paidout: %v", err)
	}
	if err := svc.CountTill(cctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 29950}}, ClosingFloatCents: 20000}); err != nil {
		t.Fatalf("count: %v", err)
	}
	z, err := svc.ZReportFor(cctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	if z.Cash.ExpectedCents != 30000 || z.Cash.OverShortCents != -50 {
		t.Fatalf("derived over/short wrong: %+v", z.Cash)
	}
	// A manager closes the over-threshold variance with a note → CLOSED + snapshot persisted.
	if err := svc.CompleteTill(managerCtx(userID, branchID), CompleteInput{TillSessionID: sid, OverrideNotes: "recount confirmed"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	var overShort int64
	var status string
	if err := pool.QueryRow(ctx, `SELECT status, over_short_cents FROM till_sessions WHERE id = $1`, sid).Scan(&status, &overShort); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if status != StatusClosed || overShort != -50 {
		t.Fatalf("snapshot wrong: status=%s over_short=%d", status, overShort)
	}
}

// I4b, the cash refund path end to end: RefundTender books the REFUNDED payments row PLUS the cash
// PAID_OUT movement (both real rows, one tx), so the Z report's per-tender CASH expected and the
// cash summary's expected BOTH drop by the refund and agree with each other; a drawer counted
// exactly right reads variance 0 on both lines.
func TestIntegration_CashRefund_PerTenderCashAgreesWithSummary(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "ITEST-REFUND", FloatCents: 20000})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	takeInTx(t, pool, svc, cctx, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 60000, IdempotencyKey: "itest-refund-sale-" + sid})
	refundInTx(t, pool, svc, cctx, RefundInput{TillSessionID: sid, Method: MethodCash, AmountCents: 5000,
		BranchID: branchID, Reference: "ret-itest", IdempotencyKey: "itest-refund-" + sid})

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM till_movements WHERE till_session_id = $1 AND movement_type = $2`, sid, MovementPaidOut).Scan(&n); err != nil {
		t.Fatalf("count movements: %v", err)
	}
	if n != 1 {
		t.Fatalf("the refund booked %d PAID_OUT movements, want 1", n)
	}

	z, err := svc.ZReportFor(cctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	if z.Cash.ExpectedCents != 75000 { // 20000 + 60000 - 5000
		t.Fatalf("summary expected cash = 75000 after the refund, got %d", z.Cash.ExpectedCents)
	}
	var cash TenderTotal
	for _, tt := range z.PerTender {
		if tt.Method == MethodCash {
			cash = tt
		}
	}
	if cash.Method != MethodCash {
		t.Fatalf("no per-tender CASH line in %+v", z.PerTender)
	}
	if cash.ExpectedCents != z.Cash.ExpectedCents {
		t.Fatalf("per-tender CASH expected %d disagrees with the summary's %d after the refund: one answer, one expected cash", cash.ExpectedCents, z.Cash.ExpectedCents)
	}

	if err := svc.CountTill(cctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 75000}}}); err != nil {
		t.Fatalf("count: %v", err)
	}
	z, err = svc.ZReportFor(cctx, sid)
	if err != nil {
		t.Fatalf("zreport after count: %v", err)
	}
	if z.Cash.OverShortCents != 0 {
		t.Fatalf("summary over/short = 0, got %d", z.Cash.OverShortCents)
	}
	for _, tt := range z.PerTender {
		if tt.Method == MethodCash && tt.VarianceCents != 0 {
			t.Fatalf("a perfectly counted drawer shows per-tender CASH variance %d, want 0", tt.VarianceCents)
		}
	}
}

// I4c, the cheque movement surface on the real movement table: a cheque lodgement books against its
// OWN method (0028's vocabulary: PAID_IN/PAID_OUT/DROP/LODGEMENT x CASH/CHEQUE/CARD/EFT/OTHER), so
// the counted drawer reads CHEQUE expected 0 and variance 0, and the session detail read's cheque
// row states the same expected the Z report derives.
func TestIntegration_ChequeMovement_InChequeExpected(t *testing.T) {
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "ITEST-CHQ", FloatCents: 20000})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	// A cheque lodgement names its slip (FW1-13 round 3): the drawer lodges the
	// cheque it took in, so the lodgement carries that payment's id.
	paymentID := takeInTx(t, pool, svc, cctx, TenderInput{TillSessionID: sid, Method: MethodCheque, AmountCents: 8000, IdempotencyKey: "itest-chq-sale-" + sid})
	if err := svc.Lodge(cctx, MovementInput{TillSessionID: sid, MovementType: MovementLodgement, Method: MethodCheque, AmountCents: 8000, Reference: "bag-itest-chq", PaymentIDs: []string{paymentID}}); err != nil {
		t.Fatalf("cheque lodge: %v", err)
	}
	if err := svc.CountTill(cctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{
		{Method: MethodCash, ActualCents: 20000}, {Method: MethodCheque, ActualCents: 0},
	}}); err != nil {
		t.Fatalf("count: %v", err)
	}

	z, err := svc.ZReportFor(cctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	var cheque TenderTotal
	for _, tt := range z.PerTender {
		if tt.Method == MethodCheque {
			cheque = tt
		}
	}
	if cheque.Method != MethodCheque {
		t.Fatalf("no per-tender CHEQUE line in %+v", z.PerTender)
	}
	if cheque.ExpectedCents != 0 || cheque.VarianceCents != 0 {
		t.Fatalf("cheque expected 0 with the lodgement out and variance 0 on a correctly counted drawer, got expected %d variance %d", cheque.ExpectedCents, cheque.VarianceCents)
	}

	detail, err := svc.GetSession(cctx, sid)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	for _, tt := range detail.Tenders {
		if tt.Method == MethodCheque && (tt.ExpectedCents != cheque.ExpectedCents || tt.VarianceCents != 0) {
			t.Fatalf("detail read cheque expected %d variance %d disagrees with the Z report's expected %d", tt.ExpectedCents, tt.VarianceCents, cheque.ExpectedCents)
		}
	}
}

// I4d, one truth for CASH expected across both reads: the session detail's tenders row states the
// derived drawer expected the Z report states, not the stored payments-sum snapshot (the round 2
// review's probe: detail 60000, actual 75000, variance +15000 against the Z report's 75000 and 0).
func TestIntegration_SessionDetailCashAgreesWithZReport(t *testing.T) {
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "ITEST-DETAIL", FloatCents: 20000})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	takeInTx(t, pool, svc, cctx, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 60000, IdempotencyKey: "itest-detail-sale-" + sid})
	if err := svc.PaidOut(cctx, MovementInput{TillSessionID: sid, MovementType: MovementPaidOut, Method: MethodCash, AmountCents: 5000, Reason: "drop"}); err != nil {
		t.Fatalf("paid out: %v", err)
	}
	if err := svc.CountTill(cctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 75000}}}); err != nil {
		t.Fatalf("count: %v", err)
	}

	z, err := svc.ZReportFor(cctx, sid)
	if err != nil {
		t.Fatalf("zreport: %v", err)
	}
	detail, err := svc.GetSession(cctx, sid)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	var detailCash TenderTotal
	found := false
	for _, tt := range detail.Tenders {
		if tt.Method == MethodCash {
			detailCash = tt
			found = true
		}
	}
	if !found {
		t.Fatalf("no tenders row for CASH in the detail read: %+v", detail.Tenders)
	}
	if detailCash.ExpectedCents != z.Cash.ExpectedCents {
		t.Fatalf("detail read cash expected %d disagrees with the Z report's %d", detailCash.ExpectedCents, z.Cash.ExpectedCents)
	}
	if detailCash.VarianceCents != 0 {
		t.Fatalf("detail read cash variance %d, want 0 on a correctly counted drawer", detailCash.VarianceCents)
	}
}

// I5, over-threshold complete as a frontline token → 403; as a manager with a note → CLOSED.
func TestIntegration_ConditionalManagerGate(t *testing.T) {
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)

	// DIB-384: the variance is derived from the branch's ACTUAL policy, never assumed.
	//
	// This test used to count short by a flat 50 cents and rely on the threshold being 0, the value
	// Repository.Policy returns when the branch has no till_policy row. The seed now plants a realistic
	// row per branch (variance_threshold_cents = 300, i.e. $3.00), so on a genuinely fresh DB a 50-cent
	// variance is UNDER threshold, the frontline close is correctly allowed, and the test failed asserting
	// it would be blocked. The gate was never broken; the test was reading an ambient default.
	//
	// Note this is the opposite of the DIB-378 class it was filed as: the config is not missing from the
	// fresh seed, it is PRESENT there and absent from the assumption the test was written against.
	policy, err := svc.Policy(globalCtx(userID), branchID)
	if err != nil {
		t.Fatalf("read till policy: %v", err)
	}
	const floatCents = 100000 // comfortably above any plausible threshold so the short below stays positive
	shortBy := policy.VarianceThresholdCents + 50
	if shortBy >= floatCents {
		t.Skipf("branch threshold %d is too large to exceed against a %d float", policy.VarianceThresholdCents, floatCents)
	}

	sid, err := svc.OpenTill(globalCtx(userID), OpenTillInput{BranchID: branchID, Station: "ITEST-GATE", FloatCents: floatCents})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	// Counted short by threshold + 50 → provably OVER the gate, whatever the branch policy says.
	if err := svc.CountTill(frontlineCtx(userID, branchID), CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: floatCents - shortBy}}}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if err := svc.CompleteTill(frontlineCtx(userID, branchID), CompleteInput{TillSessionID: sid}); err == nil {
		t.Fatal("frontline over-threshold close should be blocked")
	}
	if err := svc.CompleteTill(managerCtx(userID, branchID), CompleteInput{TillSessionID: sid, OverrideNotes: "ok"}); err != nil {
		t.Fatalf("manager close: %v", err)
	}
}

// I6, a TakeTender inside a tx that the caller rolls back leaves NO payments row (capture-before-stock
// atomicity); a committed one persists.
func TestIntegration_InCallerTxRollback(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "ITEST-TX", FloatCents: 0})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	key := "itest-rollback-" + sid
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := svc.TakeTender(cctx, tx, TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 4500, IdempotencyKey: key}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("take: %v", err)
	}
	// The "later step" fails → the caller rolls back the whole tx.
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("rolled-back tender left %d payment rows, want 0", n)
	}
}

// I7 (B2 regression), a residual COUNTED drawer from a prior shift (the "finish the close next
// morning" SOP) must NOT block opening today's session on the same (branch, station). Pre-0063 the
// uq_till_open_per_drawer index covered every NON-CLOSED status, so OpenTill over a COUNTED drawer hit
// the unique index → errConflict → 409, and attach-recovery only re-finds status='OPEN', so it was
// unrecoverable. 0063 narrows the index to status='OPEN'. Also asserts the OPEN → re-attach path stays green.
func TestIntegration_CountedDrawerDoesNotBlockOpen(t *testing.T) {
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)
	const station = "ITEST-COUNTED"

	// Yesterday: open the drawer and blind-count it, deferring the close to next morning → COUNTED.
	sid1, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: station, FloatCents: 20000})
	if err != nil {
		t.Fatalf("open #1: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid1) })
	if err := svc.CountTill(cctx, CountInput{TillSessionID: sid1, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 20000}}}); err != nil {
		t.Fatalf("count #1: %v", err)
	}

	// Today: opening the SAME drawer must succeed with a NEW open session, not 409.
	sid2, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: station, FloatCents: 20000})
	if err != nil {
		t.Fatalf("open over a COUNTED drawer must succeed (0063 narrowed the index to OPEN), got: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid2) })
	if sid2 == sid1 {
		t.Fatalf("open over a COUNTED drawer must create a NEW session, got the COUNTED one back: %s", sid2)
	}

	// Re-attach still holds: opening the now-OPEN drawer again returns the same OPEN session.
	sid3, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: station, FloatCents: 20000})
	if err != nil {
		t.Fatalf("re-open of an OPEN drawer should re-attach, got: %v", err)
	}
	if sid3 != sid2 {
		t.Fatalf("re-open of an OPEN drawer must re-attach to %s, got %s", sid2, sid3)
	}
}

// -- integration helpers ---------------------------------------------------------

func takeInTx(t *testing.T, pool *pgxpool.Pool, svc *Service, ctx context.Context, in TenderInput) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := svc.TakeTender(ctx, tx, in)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("TakeTender: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return res.PaymentID
}

// refundInTx mirrors takeInTx for the refund seam: the REFUNDED payments row + the PAID_OUT
// movement commit together inside the caller's tx.
func refundInTx(t *testing.T, pool *pgxpool.Pool, svc *Service, ctx context.Context, in RefundInput) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := svc.RefundTender(ctx, tx, in)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("RefundTender: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return res.PaymentID
}

func cleanupSession(pool *pgxpool.Pool, sessionID string) {
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE till_session_id = $1)`, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM payments WHERE till_session_id = $1`, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM till_movements WHERE till_session_id = $1`, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM till_tender_totals WHERE till_session_id = $1`, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM till_denomination_counts WHERE till_session_id = $1`, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM till_sessions WHERE id = $1`, sessionID)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
