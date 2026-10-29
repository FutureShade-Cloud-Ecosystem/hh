//go:build integration

// write_off_integration_test.go, W3 item 0.10's WRITE OFFS, proved against the real schema and the
// real GL consumer.
//
// A manager writes off an invoice's remaining balance, with a reason, under a small balance limit
// that is a branch setting (the org default is 2500 cents, $25.00). The write off posts DR a bad
// debt expense account and CR 1100 through the GL module's seam, so the aging and GL 1100 move by
// the same cents. Over the limit is refused for a manager and allowed for the controller. A write
// off is reversible once, with a reason: the reversal restores the invoice and the books, and a
// second reversal is refused.
//
// Run: go test -tags integration ./internal/billing/ -run W3_WriteOff
package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

// writeOffWorld is one test owned account, the write off service over it, and the real GL consumer
// to drain the outbox with.
type writeOffWorld struct {
	pool *pgxpool.Pool
	repo *Repository
	svc  *WriteOffService
	cust string
	tag  string
	org  string
	t    *testing.T
	ctx  context.Context
}

func newWriteOffWorld(t *testing.T) *writeOffWorld {
	t.Helper()
	pool := intPool(t)
	repo := NewRepository(pool)
	w := &writeOffWorld{pool: pool, repo: repo, svc: NewWriteOffService(repo), tag: "W3WO-" + ns(t), org: "44444444-4444-4444-8444-444444444444", t: t, ctx: context.Background()}
	w.cust = seedCustomer(t, pool, w.tag)
	// The door's scope is a real org row (the key discipline the credit door's 0448 set).
	w.org = "44444444-4444-4444-8444-444444444444"
	if _, err := pool.Exec(w.ctx, `INSERT INTO org (id, slug, name) VALUES ($1::uuid, $2, $3) ON CONFLICT (id) DO NOTHING`,
		w.org, "w3wo-"+w.org[:8], "W3 Write Off Org"); err != nil {
		t.Fatalf("plant the org: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id = $1::uuid`, w.org)
	})
	return w
}

// invoice plants one open invoice and returns its id.
func (w *writeOffWorld) invoice(label string, total int64) string {
	w.t.Helper()
	return seedInvoice(w.t, w.pool, w.cust, w.tag, invSpec{
		no: label, status: "FINALIZED", branch: trentonBranch,
		issue: time.Now().AddDate(0, 0, -20).Format("2006-01-02"),
		total: total, balance: total, finalized: true,
	})
}

// gl wires the real GL consumer the way the app wires it and drains until the named journal posts.
func (w *writeOffWorld) drainPosted(key string) {
	w.t.Helper()
	glSvc := gl.NewService(gl.NewRepository(w.pool)).
		WithInvoiceReader(w.repo).
		WithPaymentReader(till.NewRepository(w.pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(w.pool), nil)
	glSvc.RegisterPostingHandlers(d)
	for i := 0; i < 200; i++ {
		d.DrainOnce(w.ctx)
		var posted int
		if err := w.pool.QueryRow(w.ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`, key).
			Scan(&posted); err != nil {
			w.t.Fatalf("watch the journal: %v", err)
		}
		if posted > 0 {
			return
		}
	}
	w.t.Fatalf("the journal on key %s never posted", key)
}

// journalLegs reads one posted journal's debit total, credit total and its movement on one account
// code.
func (w *writeOffWorld) journalLegs(key, accountCode string) (debits, credits, accountMove int64) {
	w.t.Helper()
	err := w.pool.QueryRow(w.ctx, `
		SELECT COALESCE(SUM(l.debit_cents), 0), COALESCE(SUM(l.credit_cents), 0),
		       COALESCE(SUM(CASE WHEN a.code = $2::text
		                         THEN COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)
		                         ELSE 0 END), 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1::text AND j.status = 'POSTED'`, key, accountCode).
		Scan(&debits, &credits, &accountMove)
	if err != nil {
		w.t.Fatalf("read the journal on %s: %v", key, err)
	}
	return debits, credits, accountMove
}

// writeOffRows counts the invoice's own WRITE_OFF subledger rows and sums them.
func (w *writeOffWorld) writeOffSum(invoiceID string) int64 {
	w.t.Helper()
	var sum int64
	if err := w.pool.QueryRow(w.ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND txn_type = 'WRITE_OFF'
		   AND source_type = 'invoice' AND source_ref_id = $2::uuid`, w.cust, invoiceID).Scan(&sum); err != nil {
		w.t.Fatalf("read the write off rows: %v", err)
	}
	return sum
}

// RED LEG 3: a write off under the default limit moves the aging and GL 1100 by the same cents:
// DR bad debt expense, CR 1100, the subledger row against the invoice, the balance gone.
func TestIntegration_W3_WriteOff_UnderTheDefaultLimitTiesAgingAndGL(t *testing.T) {
	w := newWriteOffWorld(t)
	inv := w.invoice("WO-SMALL", 2000)

	// The default is the org default row's 2500 ($25.00): the read says so before any act.
	pol, err := w.repo.CreditPolicy(w.ctx, trentonBranch)
	if err != nil {
		t.Fatalf("read the credit policy: %v", err)
	}
	if pol.WriteOffLimitCents != 2500 {
		t.Fatalf("the default write off limit is 2500 cents, got %d", pol.WriteOffLimitCents)
	}

	agingBefore := agingSum(t, w.repo, time.Now().UTC().Add(1*time.Hour))
	res, err := w.svc.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: inv, Reason: "small balance, uncollectible", ActorID: "w3-manager",
		IdempotencyKey: w.tag + "-small", OrgID: w.org,
	}, true)
	if err != nil {
		t.Fatalf("the manager writes off the 2000 balance: %v", err)
	}
	if res.AmountCents != 2000 {
		t.Fatalf("the write off took the whole remaining balance (2000), got %d", res.AmountCents)
	}
	if b, s := w3InvoiceState(t, w.pool, inv); b != 0 || s != "PAID" {
		t.Fatalf("the invoice is settled at zero, got %d %s", b, s)
	}
	if got := w.writeOffSum(inv); got != -2000 {
		t.Fatalf("the account's write off row is -2000, got %d", got)
	}

	// The journal through the GL module's seam: DR the expense, CR 1100, balanced, one per act.
	w.drainPosted("ar.invoice.write_off:" + res.WriteOffID)
	debits, credits, expense := w.journalLegs("ar.invoice.write_off:"+res.WriteOffID, "5400")
	if debits != 2000 || credits != 2000 || expense != 2000 {
		t.Fatalf("the write off journal is DR 5400 2000 (D 2000 C 2000), got D %d C %d 5400 %d", debits, credits, expense)
	}
	if _, _, ar := w.journalLegs("ar.invoice.write_off:"+res.WriteOffID, "1100"); ar != -2000 {
		t.Fatalf("the write off journal credits 1100 by 2000, got %d", ar)
	}

	// The tie: the aging and GL 1100 moved by the same 2000.
	if agingSum(t, w.repo, time.Now().UTC().Add(1*time.Hour))-agingBefore != -2000 {
		t.Fatalf("the aging moved -2000 with the write off, got %d", agingSum(t, w.repo, time.Now().UTC().Add(1*time.Hour))-agingBefore)
	}
}

// RED LEG 4: over the limit is refused for a manager and allowed for the controller.
func TestIntegration_W3_WriteOff_OverTheLimitNeedsTheController(t *testing.T) {
	w := newWriteOffWorld(t)
	inv := w.invoice("WO-BIG", 3000) // over the 2500 default

	_, err := w.svc.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: inv, Reason: "beyond my desk", ActorID: "w3-manager",
		IdempotencyKey: w.tag + "-big-mgr", OrgID: w.org,
	}, true)
	var over *WriteOffOverLimitError
	if !asWriteOffOverLimit(err, &over) {
		t.Fatalf("a manager over the limit is refused in words, got %T %v", err, err)
	}
	if over.LimitCents != 2500 {
		t.Fatalf("the refusal names the branch's limit 2500, got %d", over.LimitCents)
	}
	if b, _ := w3InvoiceState(t, w.pool, inv); b != 3000 {
		t.Fatalf("the refused write off moved nothing, balance %d", b)
	}

	res, err := w.svc.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: inv, Reason: "controller approved", ActorID: "w3-controller",
		IdempotencyKey: w.tag + "-big-ctl", OrgID: w.org, IsController: true,
	}, true)
	if err != nil {
		t.Fatalf("the controller writes off over the limit: %v", err)
	}
	if res.AmountCents != 3000 {
		t.Fatalf("the controller's write off took the whole balance (3000), got %d", res.AmountCents)
	}
	w.drainPosted("ar.invoice.write_off:" + res.WriteOffID)
	if debits, credits, _ := w.journalLegs("ar.invoice.write_off:"+res.WriteOffID, "5400"); debits != 3000 || credits != 3000 {
		t.Fatalf("the journal is balanced at 3000, got D %d C %d", debits, credits)
	}
}

// The limit is a BRANCH setting: a branch row of its own overrides the org default, both for the
// refusal and for the controller's authority.
func TestIntegration_W3_WriteOff_TheLimitIsABranchSetting(t *testing.T) {
	w := newWriteOffWorld(t)
	// The branch row overrides the org default with 500.
	if _, err := w.pool.Exec(w.ctx, `
		INSERT INTO ar_credit_policy (branch_id, write_off_limit_cents) VALUES ($1::uuid, 500)`,
		trentonBranch); err != nil {
		t.Fatalf("plant the branch's policy row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM ar_credit_policy WHERE branch_id = $1::uuid`, trentonBranch)
	})
	pol, err := w.repo.CreditPolicy(w.ctx, trentonBranch)
	if err != nil {
		t.Fatalf("read the branch's policy: %v", err)
	}
	if pol.WriteOffLimitCents != 500 {
		t.Fatalf("the branch's own limit is 500, got %d", pol.WriteOffLimitCents)
	}

	inv := w.invoice("WO-BRANCH", 2000)
	_, err = w.svc.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: inv, Reason: "over the branch's own limit", ActorID: "w3-manager",
		IdempotencyKey: w.tag + "-branch-mgr", OrgID: w.org,
	}, true)
	var over *WriteOffOverLimitError
	if !asWriteOffOverLimit(err, &over) || over.LimitCents != 500 {
		t.Fatalf("the manager is refused on the branch's own 500, got %T %v", err, err)
	}
	res, err := w.svc.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: inv, Reason: "controller, branch limit notwithstanding", ActorID: "w3-controller",
		IdempotencyKey: w.tag + "-branch-ctl", OrgID: w.org, IsController: true,
	}, true)
	if err != nil {
		t.Fatalf("the controller writes off over the branch's limit: %v", err)
	}
	if res.AmountCents != 2000 {
		t.Fatalf("the controller's write off took the whole balance, got %d", res.AmountCents)
	}
}

// RED LEG 5: a reversal restores the invoice, the subledger and the books, once; a second reversal
// is refused.
func TestIntegration_W3_WriteOff_ReversalRestoresBothAndIsRefusedTwice(t *testing.T) {
	w := newWriteOffWorld(t)
	inv := w.invoice("WO-REV", 3000)
	agingBefore := agingSum(t, w.repo, time.Now().UTC().Add(1*time.Hour))

	res, err := w.svc.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: inv, Reason: "controller approved", ActorID: "w3-controller",
		IdempotencyKey: w.tag + "-rev-go", OrgID: w.org, IsController: true,
	}, true)
	if err != nil {
		t.Fatalf("the write off: %v", err)
	}
	w.drainPosted("ar.invoice.write_off:" + res.WriteOffID)
	if agingSum(t, w.repo, time.Now().UTC().Add(1*time.Hour))-agingBefore != -3000 {
		t.Fatalf("the write off moved the aging -3000 first, got %d", agingSum(t, w.repo, time.Now().UTC().Add(1*time.Hour))-agingBefore)
	}

	rev, err := w.svc.Reverse(w.ctx, ReverseWriteOffInput{
		InvoiceID: inv, WriteOffID: res.WriteOffID, Reason: "the customer paid after all",
		ActorID: "w3-controller", OrgID: w.org,
	}, true)
	if err != nil {
		t.Fatalf("the reversal: %v", err)
	}
	if rev.RestoredCents != 3000 || rev.InvoiceBalance != 3000 {
		t.Fatalf("the reversal restored 3000, got %d and balance %d", rev.RestoredCents, rev.InvoiceBalance)
	}
	if b, s := w3InvoiceState(t, w.pool, inv); b != 3000 || s != "FINALIZED" {
		t.Fatalf("the invoice is open again as it was, got %d %s", b, s)
	}
	if got := w.writeOffSum(inv); got != 0 {
		t.Fatalf("the write off and its reversal net to zero on the account, got %d", got)
	}
	w.drainPosted("ar.invoice.write_off_reversed:" + res.WriteOffID)
	if debits, credits, ar := w.journalLegs("ar.invoice.write_off_reversed:"+res.WriteOffID, "1100"); debits != 3000 || credits != 3000 || ar != 3000 {
		t.Fatalf("the reversal journal is DR 1100 3000 (D 3000 C 3000), got D %d C %d 1100 %d", debits, credits, ar)
	}
	if _, _, expense := w.journalLegs("ar.invoice.write_off_reversed:"+res.WriteOffID, "5400"); expense != -3000 {
		t.Fatalf("the reversal journal credits the expense back by 3000, got %d", expense)
	}
	if agingSum(t, w.repo, time.Now().UTC().Add(1*time.Hour))-agingBefore != 0 {
		t.Fatalf("the reversal restores the aging to its start, got %d",
			agingSum(t, w.repo, time.Now().UTC().Add(1*time.Hour))-agingBefore)
	}

	_, err = w.svc.Reverse(w.ctx, ReverseWriteOffInput{
		InvoiceID: inv, WriteOffID: res.WriteOffID, Reason: "a second bite",
		ActorID: "w3-controller", OrgID: w.org,
	}, true)
	if !isWriteOffNotReversible(err) {
		t.Fatalf("a second reversal is refused, got %T %v", err, err)
	}
}

// The door's other refusals and its replay: a reason is required, a draft and a paid invoice are
// not writable off, another branch's invoice is not found, and the same key with the same body
// answers the first result and writes nothing.
func TestIntegration_W3_WriteOff_RefusalsAndReplay(t *testing.T) {
	w := newWriteOffWorld(t)
	inv := w.invoice("WO-REF", 2000)

	if _, err := w.svc.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: inv, Reason: "   ", ActorID: "w3-manager",
		IdempotencyKey: w.tag + "-noreason", OrgID: w.org,
	}, true); !isWriteOffReasonRequired(err) {
		t.Fatalf("a reason is required, got %T %v", err, err)
	}

	// A replay under the same key, same body: the first result again, nothing written twice.
	first, err := w.svc.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: inv, Reason: "small balance", ActorID: "w3-manager",
		IdempotencyKey: w.tag + "-replay", OrgID: w.org,
	}, true)
	if err != nil {
		t.Fatalf("the write off: %v", err)
	}
	again, err := w.svc.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: inv, Reason: "small balance", ActorID: "w3-manager",
		IdempotencyKey: w.tag + "-replay", OrgID: w.org,
	}, true)
	if err != nil {
		t.Fatalf("the same key replays: %v", err)
	}
	if !again.Replayed || again.WriteOffID != first.WriteOffID {
		t.Fatalf("the replay answers the first act, got %+v", again)
	}
	if got := w.writeOffSum(inv); got != -2000 {
		t.Fatalf("the replay wrote nothing: the account carries one write off, got %d", got)
	}

	// Nothing left to write off: the invoice is settled.
	if _, err := w.svc.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: inv, Reason: "nothing left", ActorID: "w3-manager",
		IdempotencyKey: w.tag + "-spent", OrgID: w.org, IsController: true,
	}, true); err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("a settled invoice is not writable off, got %T %v", err, err)
	}

	// Another branch's invoice is not found: no existence leaks across the wall.
	other := w.invoice("WO-OTHER", 2000)
	fenced := w.svc
	_, err = fenced.WriteOff(w.ctx, WriteOffInput{
		InvoiceID: other, Reason: "out of reach", ActorID: "w3-manager",
		IdempotencyKey: w.tag + "-fenced", OrgID: w.org,
		CanAccessBranch: func(string) bool { return false },
	}, true)
	if !isNoSuchInvoice(err) {
		t.Fatalf("an invoice outside the reach is not found, got %T %v", err, err)
	}
}

// asWriteOffOverLimit, isWriteOffNotReversible, isWriteOffReasonRequired and isNoSuchInvoice are
// the legs' error shorthands.
func asWriteOffOverLimit(err error, dst **WriteOffOverLimitError) bool {
	return errors.As(err, dst)
}

func isWriteOffNotReversible(err error) bool {
	return errors.Is(err, ErrWriteOffNotReversible)
}

func isWriteOffReasonRequired(err error) bool {
	return errors.Is(err, ErrWriteOffReasonRequired)
}

func isNoSuchInvoice(err error) bool {
	var no NoSuchInvoiceError
	return errors.As(err, &no)
}
