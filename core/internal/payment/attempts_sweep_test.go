package payment

// attempts_sweep_test.go: the crash window legs 6(a) and 6(b) (the card charge design section
// 5.3 leg 6) as service level units with an injected clock. Each leg plants one attempt row in
// the state the crash window leaves behind, backdated past the grace constant, and pins what one
// sweep pass does to it: the guarded move, the processor calls (counted, by kind), and the
// escalation posts (recorded, by key). A second pass pins the once-per-row rule: a row the sweep
// already closed is never examined again, so no processor call and no post can repeat.
//
// The legs run on a real Postgres named on the command (DATABASE_URL, migration 0361 applied);
// absent the variable they skip, so the default battery stays green without a database.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sweepPools opens the leg's store (vault backed, so the reversal's config read decrypts) over
// the database the command names.
func sweepPools(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("no sweep leg database: %v", err)
	}
	t.Cleanup(pool.Close)
	v, err := NewVault(testVaultKey)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	return NewStore(pool, v), pool
}

// sweepFx is one till session, one rail config row, and the rows the leg plants on it, all
// removed at cleanup. The rail row is the leg's own (its merchant id is minted per fixture), so
// a shared database never loses another suite's config and the sweep's reversal reads this leg's
// own creds.
type sweepFx struct {
	t                     *testing.T
	store                 *Store
	pool                  *pgxpool.Pool
	branch, user, session string
	merchant              string
	planted               int
}

func newSweepFx(t *testing.T) *sweepFx {
	t.Helper()
	store, pool := sweepPools(t)
	ctx := context.Background()
	fx := &sweepFx{t: t, store: store, pool: pool, merchant: "MID-SWEEP-" + uuid.NewString()[:8]}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations ORDER BY id LIMIT 1`).Scan(&fx.branch); err != nil {
		t.Skipf("no seeded location: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&fx.user); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO till_sessions (branch_id, station, status, opened_by)
	    VALUES ($1::uuid, $2, 'OPEN', $3::uuid) RETURNING id::text`,
		fx.branch, "sweep-"+uuid.NewString()[:8], fx.user).Scan(&fx.session); err != nil {
		t.Fatalf("till session fixture: %v", err)
	}
	cleanRail := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM payment_config WHERE merchant_id = $1`, fx.merchant)
	}
	cleanRail()
	t.Cleanup(cleanRail)
	if err := store.Configure(ctx, "run_payments", fx.merchant, "CAD", "", 0, false,
		map[string]string{"api_key": "sweep-leg-not-a-secret"}, ""); err != nil {
		t.Fatalf("configure the leg's rail: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM card_charge_attempts WHERE till_session_id = $1::uuid`, fx.session)
		_, _ = pool.Exec(ctx, `DELETE FROM till_sessions WHERE id = $1::uuid`, fx.session)
	})
	return fx
}

// plant writes a CHARGING row through the store's claim, then forces it to the crash window's
// status with the ref that status's CHECKs demand and backdates the row's last transition to at,
// bypassing the store exactly the way the process death the sweep closes would leave it.
func (fx *sweepFx) plant(status, ref string, at time.Time) Attempt {
	fx.t.Helper()
	// One amount per planted row: the till guard blocks a second unresolved attempt for the
	// same amount at the same session, which is exactly what the legs plant side by side.
	fx.planted++
	in := ClaimInput{LegKey: uuid.NewString(), SaleKey: uuid.NewString(), LegSeq: 1,
		AmountCents: int64(2500 + 100*fx.planted),
		BranchID:    fx.branch, TillSessionID: fx.session, CardType: "CREDIT", Processor: "run_payments",
		CreatedBy: fx.user}
	a, created, err := fx.store.Claim(context.Background(), in)
	if err != nil || !created {
		fx.t.Fatalf("plant claim: created=%v err=%v", created, err)
	}
	if _, err := fx.pool.Exec(context.Background(),
		`UPDATE card_charge_attempts SET status = $2, processor_ref = $3, updated_at = $4 WHERE id = $1::uuid`,
		a.ID, status, ref, at); err != nil {
		fx.t.Fatalf("plant %s: %v", status, err)
	}
	return a
}

// read re-reads one attempt row whole.
func (fx *sweepFx) read(id string) Attempt {
	fx.t.Helper()
	a, err := scanAttempt(fx.pool.QueryRow(context.Background(),
		`SELECT `+attemptColumns+` FROM card_charge_attempts WHERE id = $1::uuid`, id))
	if err != nil {
		fx.t.Fatalf("read the attempt: %v", err)
	}
	return a
}

// sweepProc is the stub with counters, and with fault knobs the unhappy reversal arms set.
type sweepProc struct {
	RunPaymentsStub
	mu                      sync.Mutex
	charges, voids, refunds int
	voidErr, refundErr      error
}

func (p *sweepProc) counts() (int, int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.charges, p.voids, p.refunds
}

func (p *sweepProc) Charge(ctx context.Context, creds map[string]string, mid string, amount int64, token string) (ChargeResult, error) {
	p.mu.Lock()
	p.charges++
	p.mu.Unlock()
	return p.RunPaymentsStub.Charge(ctx, creds, mid, amount, token)
}

func (p *sweepProc) Void(ctx context.Context, creds map[string]string, mid, ref string, amount int64) (RefundResult, error) {
	p.mu.Lock()
	p.voids++
	p.mu.Unlock()
	if p.voidErr != nil {
		return RefundResult{}, p.voidErr
	}
	return p.RunPaymentsStub.Void(ctx, creds, mid, ref, amount)
}

func (p *sweepProc) Refund(ctx context.Context, creds map[string]string, mid, ref string, amount int64) (RefundResult, error) {
	p.mu.Lock()
	p.refunds++
	p.mu.Unlock()
	if p.refundErr != nil {
		return RefundResult{}, p.refundErr
	}
	return p.RunPaymentsStub.Refund(ctx, creds, mid, ref, amount)
}

// sweepAlerts records the escalation posts the sweep makes through the seam.
type sweepAlerts struct {
	mu   sync.Mutex
	keys []string
}

func (a *sweepAlerts) record(_ context.Context, key, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys = append(a.keys, key)
	return nil
}

func (a *sweepAlerts) got() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.keys...)
}

// swapAlertRecorder points the seam at a recorder for the test's span and restores the seam
// after, so a leg pins its own posts and nobody else's.
func swapAlertRecorder(t *testing.T) *sweepAlerts {
	t.Helper()
	rec := &sweepAlerts{}
	prev := AlertFunc
	AlertFunc = rec.record
	t.Cleanup(func() { AlertFunc = prev })
	return rec
}

// TestSweepLeg6a_CapturedPastGraceVoidsOnceThenNeverAgain is crash window leg 6(a)'s happy arm:
// a CAPTURED unbound row past grace (the process died after the capture stamp, before the sale
// committed) is examined once, voided at the processor once (one void, no capture, no refund
// fallback needed), and stamped VOIDED with both refs. No escalation posts: the money is back,
// nobody has to look. The second pass examines nothing and calls nothing.
func TestSweepLeg6a_CapturedPastGraceVoidsOnceThenNeverAgain(t *testing.T) {
	fx := newSweepFx(t)
	base := time.Now().Add(-time.Hour)
	clock := func() time.Time { return base }
	proc := &sweepProc{}
	svc := NewService(fx.store, proc)
	rec := swapAlertRecorder(t)

	a := fx.plant(AttemptCaptured, "ref-sweep-6a", base.Add(-2*AttemptGrace))

	res, err := svc.SweepCardAttempts(context.Background(), clock)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if res.Examined != 1 || res.Voided != 1 || res.ToUnknown != 0 || res.Lost != 0 {
		t.Fatalf("first pass = %+v, want the one captured row examined and voided", res)
	}
	row := fx.read(a.ID)
	if row.Status != AttemptVoided || row.ReversalKind != ReversalVoid || row.ReversalRef == "" {
		t.Fatalf("row after the first pass = %s kind %q reversal ref %q, want VOIDED by VOID with both refs",
			row.Status, row.ReversalKind, row.ReversalRef)
	}
	charges, voids, refunds := proc.counts()
	if charges != 0 || voids != 1 || refunds != 0 {
		t.Fatalf("the first pass made %d charge(s), %d void(s), %d refund(s), want one void of the capture and nothing else", charges, voids, refunds)
	}
	if keys := rec.got(); len(keys) != 0 {
		t.Fatalf("the first pass posted %d escalation(s) %v, want none on VOIDED", len(keys), keys)
	}

	res2, err := svc.SweepCardAttempts(context.Background(), clock)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if res2.Examined != 0 || res2.Voided != 0 || res2.ToUnknown != 0 {
		t.Fatalf("second pass = %+v, want nothing left to examine: the row is VOIDED", res2)
	}
	if c, v, r := proc.counts(); c != 0 || v != 1 || r != 0 {
		t.Fatalf("after the second pass the processor saw %d charge(s), %d void(s), %d refund(s), want the first pass's one void and nothing more", c, v, r)
	}
	if keys := rec.got(); len(keys) != 0 {
		t.Fatalf("the second pass posted %d escalation(s), want none", len(keys))
	}
}

// TestSweepLeg6a_CapturedPastGraceUnhappyArmsBecomeOperatorItems is crash window leg 6(a)'s
// unhappy arms: a capture whose void loses its answer lands VOID_UNKNOWN, a capture whose void
// and refund fallback are both declined lands VOID_DECLINED, and each posts exactly one
// escalation keyed card-attempt:<id>:<status>, the operator item. A second pass posts nothing.
func TestSweepLeg6a_CapturedPastGraceUnhappyArmsBecomeOperatorItems(t *testing.T) {
	fx := newSweepFx(t)
	base := time.Now().Add(-time.Hour)
	clock := func() time.Time { return base }
	rec := swapAlertRecorder(t)

	// The lost void answer: the sweep's reversal cannot tell whether the void happened.
	lostProc := &sweepProc{voidErr: fmt.Errorf("%w: the void answer was lost", ErrProcessorOutcomeUnknown)}
	lostSvc := NewService(fx.store, lostProc)
	lost := fx.plant(AttemptCaptured, "ref-sweep-6a-lost", base.Add(-2*AttemptGrace))
	res, err := lostSvc.SweepCardAttempts(context.Background(), clock)
	if err != nil {
		t.Fatalf("the lost answer pass: %v", err)
	}
	if res.ToVoidUnknown != 1 || res.Voided != 0 {
		t.Fatalf("the lost answer pass = %+v, want the row on VOID_UNKNOWN", res)
	}
	if row := fx.read(lost.ID); row.Status != AttemptVoidUnknown {
		t.Fatalf("the lost answer row = %s, want VOID_UNKNOWN", row.Status)
	}
	if c, v, r := lostProc.counts(); c != 0 || v != 1 || r != 0 {
		t.Fatalf("the lost answer pass made %d charge(s), %d void(s), %d refund(s), want one void and no blind retry", c, v, r)
	}
	want := "card-attempt:" + lost.ID + ":" + AttemptVoidUnknown
	if keys := rec.got(); len(keys) != 1 || keys[0] != want {
		t.Fatalf("the lost answer pass posted %v, want exactly %s", keys, want)
	}

	// Both reversals declined: the customer's money is held, a person has to look.
	declProc := &sweepProc{
		voidErr:   &DeclineError{RespCode: "57", RespText: "void not permitted"},
		refundErr: &DeclineError{RespCode: "57", RespText: "refund not permitted"},
	}
	declSvc := NewService(fx.store, declProc)
	declined := fx.plant(AttemptCaptured, "ref-sweep-6a-declined", base.Add(-2*AttemptGrace))
	res, err = declSvc.SweepCardAttempts(context.Background(), clock)
	if err != nil {
		t.Fatalf("the declined pass: %v", err)
	}
	if res.ToVoidDeclined != 1 || res.Voided != 0 {
		t.Fatalf("the declined pass = %+v, want the row on VOID_DECLINED", res)
	}
	if row := fx.read(declined.ID); row.Status != AttemptVoidDeclined {
		t.Fatalf("the declined row = %s, want VOID_DECLINED", row.Status)
	}
	if c, v, r := declProc.counts(); c != 0 || v != 1 || r != 1 {
		t.Fatalf("the declined pass made %d charge(s), %d void(s), %d refund(s), want the void and its refund fallback", c, v, r)
	}
	want = "card-attempt:" + declined.ID + ":" + AttemptVoidDeclined
	if keys := rec.got(); len(keys) != 2 || keys[1] != want {
		t.Fatalf("the passes posted %v, want the lost answer's key then exactly %s", keys, want)
	}

	// The second pass re-examines neither row and posts nothing more.
	res, err = lostSvc.SweepCardAttempts(context.Background(), clock)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if res.Examined != 0 || res.ToVoidUnknown != 0 {
		t.Fatalf("second pass = %+v, want nothing left to examine", res)
	}
	if keys := rec.got(); len(keys) != 2 {
		t.Fatalf("after the second pass %d escalation(s) are posted %v, want the first pass's two and no more", len(keys), keys)
	}
}

// TestSweepLeg6b_ChargingPastGraceMovesToUnknownWithoutACall is crash window leg 6(b): a CHARGING
// row past grace (the process died between the claim and the stamp; there is no ref to void and
// no lookup to ask) moves to UNKNOWN with NO processor call, and posts exactly one escalation
// keyed card-attempt:<id>:UNKNOWN. A repeat pass examines nothing and posts nothing.
func TestSweepLeg6b_ChargingPastGraceMovesToUnknownWithoutACall(t *testing.T) {
	fx := newSweepFx(t)
	base := time.Now().Add(-time.Hour)
	clock := func() time.Time { return base }
	proc := &sweepProc{}
	svc := NewService(fx.store, proc)
	rec := swapAlertRecorder(t)

	a := fx.plant(AttemptCharging, "", base.Add(-2*AttemptGrace))

	res, err := svc.SweepCardAttempts(context.Background(), clock)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if res.Examined != 1 || res.ToUnknown != 1 || res.Voided != 0 {
		t.Fatalf("first pass = %+v, want the charging row examined and moved to UNKNOWN", res)
	}
	if row := fx.read(a.ID); row.Status != AttemptUnknown {
		t.Fatalf("row after the first pass = %s, want UNKNOWN", row.Status)
	}
	charges, voids, refunds := proc.counts()
	if charges != 0 || voids != 0 || refunds != 0 {
		t.Fatalf("the first pass made %d charge(s), %d void(s), %d refund(s), want none: there is no ref to void and no lookup", charges, voids, refunds)
	}
	want := "card-attempt:" + a.ID + ":" + AttemptUnknown
	if keys := rec.got(); len(keys) != 1 || keys[0] != want {
		t.Fatalf("the first pass posted %v, want exactly %s", keys, want)
	}

	res2, err := svc.SweepCardAttempts(context.Background(), clock)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if res2.Examined != 0 || res2.ToUnknown != 0 {
		t.Fatalf("second pass = %+v, want nothing left to examine: the row is UNKNOWN", res2)
	}
	if keys := rec.got(); len(keys) != 1 {
		t.Fatalf("after the second pass %d escalation(s) are posted, want the first pass's one and no more: a repeat move to the same state posts nothing", len(keys))
	}
}

// TestSweepLeg6b_WithinGraceRowIsUntouched pins the grace's other side: a CHARGING row whose last
// transition is still inside AttemptGrace is not the sweep's yet, whatever its age in wall time
// the pass's clock does not confirm. The row stays CHARGING, no call, no post.
func TestSweepLeg6b_WithinGraceRowIsUntouched(t *testing.T) {
	fx := newSweepFx(t)
	base := time.Now().Add(-time.Hour)
	clock := func() time.Time { return base }
	proc := &sweepProc{}
	svc := NewService(fx.store, proc)
	rec := swapAlertRecorder(t)

	a := fx.plant(AttemptCharging, "", base.Add(-time.Minute))

	res, err := svc.SweepCardAttempts(context.Background(), clock)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if res.Examined != 0 || res.ToUnknown != 0 {
		t.Fatalf("pass = %+v, want the in grace row untouched", res)
	}
	if row := fx.read(a.ID); row.Status != AttemptCharging {
		t.Fatalf("row after the pass = %s, want CHARGING: the sweep must never race a live ring", row.Status)
	}
	if c, v, r := proc.counts(); c != 0 || v != 0 || r != 0 {
		t.Fatalf("the pass made %d charge(s), %d void(s), %d refund(s), want none", c, v, r)
	}
	if keys := rec.got(); len(keys) != 0 {
		t.Fatalf("the pass posted %v, want no escalation for a row inside its grace", keys)
	}
}
