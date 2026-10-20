//go:build integration

// lockorder_sync_integration_test.go: the sync freshness of the money writers the one lock order governs
// (LOCK-ORDER.md, "The legs"). Two kinds of leg, both over the rig of lockorder_integration_test.go:
//
//   - a freshness leg through harness.Fresh: a bounce deletes the payment's allocations and re-opens its
//     invoice, and the payer's device (and the dealer's) must pull both on their next pull;
//   - concurrent legs: a writer that moves a payment's status beside a settle or a reversal, parked in the
//     widened window the other legs use. Neither may deadlock, and afterwards the rows both wrote must
//     reach their feeds at seqs past the cursor a device held before the pair ran.
package payments_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// loOrgID is the org the dealer's staff arm reads the invoices feed under in these legs; the rig stamps it
// on the invoices it wants the staff arm to see (the tenancy stamp itself is SF-3's).
const loOrgID = "3ef10000-0000-4000-8000-0000000000a1"

// - feeds -

type loFeeds struct {
	engine  *sync.Engine
	payInfo sync.ScopeInfo
	invInfo sync.ScopeInfo
	tenant  sync.ScopeFilter
	staff   sync.ScopeFilter
}

func (r *loRig) feeds() *loFeeds {
	r.t.Helper()
	e := sync.NewEngine()
	if err := e.RegisterScope("payments", payments.NewPaymentsSyncStore(r.pool, nil)); err != nil {
		r.t.Fatalf("register payments: %v", err)
	}
	if err := e.RegisterScope("invoices", billing.NewInvoicesSyncStore(r.pool, billing.NewInvoiceWriter(r.pool), nil)); err != nil {
		r.t.Fatalf("register invoices: %v", err)
	}
	pi, _ := sync.Lookup("payments")
	ii, _ := sync.Lookup("invoices")
	return &loFeeds{
		engine: e, payInfo: pi, invInfo: ii,
		tenant: sync.ScopeFilter{OrgID: loOrgID, TenantID: r.custID, Tenant: true, Actor: "lo"},
		staff:  sync.ScopeFilter{OrgID: loOrgID, Actor: "lo"},
	}
}

// stampOrg gives the rig's invoices the org the staff arm reads under.
func (r *loRig) stampOrg() {
	r.t.Helper()
	if _, err := r.obs.Exec(context.Background(),
		`UPDATE invoices SET org_id = $2::uuid WHERE customer_id = $1::uuid AND org_id IS DISTINCT FROM $2::uuid`,
		r.custID, loOrgID); err != nil {
		r.t.Fatalf("stamp org: %v", err)
	}
}

type loPulled struct {
	seq  int64
	data map[string]any
}

// pull follows next_cursor from since and returns the cursor and every item by id.
func (f *loFeeds) pull(t *testing.T, info sync.ScopeInfo, filter sync.ScopeFilter, since int64) (int64, map[string]loPulled) {
	t.Helper()
	out := map[string]loPulled{}
	cursor := since
	for {
		resp, err := f.engine.Pull(context.Background(), info, filter, cursor, 200)
		if err != nil {
			t.Fatalf("pull %s: %v", info.Name, err)
		}
		for _, it := range resp.Items {
			out[it.Id] = loPulled{seq: it.Seq, data: it.Data}
		}
		if resp.NextCursor == nil {
			if resp.LastSeq > cursor {
				cursor = resp.LastSeq
			}
			return cursor, out
		}
		var next int64
		if _, err := fmt.Sscan(*resp.NextCursor, &next); err != nil {
			t.Fatalf("next_cursor %q: %v", *resp.NextCursor, err)
		}
		cursor = next
	}
}

// requireFresh requires the entity in the pull since the cursor, past it, with the wanted status.
func requireFresh(t *testing.T, what string, got map[string]loPulled, cursor int64, id, status string) {
	t.Helper()
	p, ok := got[id]
	if !ok {
		t.Errorf("%s: pull from the device's cursor %d returns %s: false", what, cursor, id)
		return
	}
	if p.seq <= cursor {
		t.Errorf("%s: %s has seq %d, not past the cursor %d", what, id, p.seq, cursor)
	}
	if status != "" && p.data["status"] != status {
		t.Errorf("%s: %s has status %v, want %s", what, id, p.data["status"], status)
	}
}

func (r *loRig) nsfService() *till.Service {
	return till.NewService(till.NewRepository(r.pool))
}

func (r *loRig) staffCtx() context.Context {
	return staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
		UserID: r.userID, Tier: staffcontext.TierLeadership, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE"})
}

// condSleepingInPaymentUpdate: a writer whose payments counter draw is the sync seq trigger's (a status
// update with no draw of its own) shows the update, not the counter's SQL, as its query while it sleeps.
const condSleepingInPaymentUpdate = `wait_event = 'PgSleep' AND query LIKE '%UPDATE payments%'`

// - item 1: allocations after a bounce -

// A bounce deletes the payment's allocations (till/repository.go, in the MarkNSF transaction) and re-opens
// every invoice it paid. The payments feed serves the allocations from a second read, so the payer must
// pull the payment with them gone; the reopened invoice must reach the invoices feed on both arms.
func TestIntegration_Fresh_BounceDropsTheAllocationsAndReopensTheInvoice(t *testing.T) {
	setup := func(t *testing.T) (*loRig, *loFeeds, string, payments.Payment) {
		r := newLORig(t, 4)
		inv := r.invoice("FB", 50_000)
		p := r.payOnce(inv, 50_000)
		r.stampOrg()
		f := r.feeds()
		// The device has synced the settled state: the payment holds its allocation.
		_, got := f.pull(t, f.payInfo, f.tenant, 0)
		if a, _ := got[p.ID].data["allocations"].([]any); len(a) != 1 {
			t.Fatalf("precondition: the synced payment carries %d allocations, want 1", len(a))
		}
		return r, f, inv, p
	}
	bounce := func(r *loRig, p payments.Payment) func(context.Context) (string, error) {
		return func(context.Context) (string, error) {
			_, err := r.nsfService().MarkNSF(r.staffCtx(), till.NSFInput{PaymentID: p.ID, Reason: "freshness leg"})
			return p.ID, err
		}
	}

	t.Run("payments feed: the payment is BOUNCED and its allocations are gone", func(t *testing.T) {
		r, f, _, p := setup(t)
		harness.Fresh(t, harness.FreshCase{
			Engine: f.engine, Scope: f.payInfo, Filter: f.tenant, Write: bounce(r, p),
			Want: func(d map[string]any) error {
				if d["status"] != "BOUNCED" {
					return fmt.Errorf("status is %v, want BOUNCED", d["status"])
				}
				if a, _ := d["allocations"].([]any); len(a) != 0 {
					return fmt.Errorf("the payment still carries %d allocations", len(a))
				}
				return nil
			},
		})
	})

	reopened := func(r *loRig, inv string) func(map[string]any) error {
		return func(d map[string]any) error {
			if d["status"] != "FINALIZED" || d["balance_cents"] != float64(50_000) {
				return fmt.Errorf("invoice %s is %v with balance %v, want FINALIZED with 50000", inv, d["status"], d["balance_cents"])
			}
			return nil
		}
	}
	for _, arm := range []string{"tenant", "staff"} {
		arm := arm
		t.Run("invoices feed, "+arm+" arm: the invoice is reopened", func(t *testing.T) {
			r, f, inv, p := setup(t)
			filter := f.tenant
			if arm == "staff" {
				filter = f.staff
			}
			harness.Fresh(t, harness.FreshCase{
				Engine: f.engine, Scope: f.invInfo, Filter: filter,
				Write: func(ctx context.Context) (string, error) {
					_, err := bounce(r, p)(ctx)
					return inv, err
				},
				Want: reopened(r, inv),
			})
		})
	}
}

// - concurrent legs -

// parkedCond is the barrier for a writer parked in the widened counter window: the status writers draw
// through the trigger, the reversal and the settles through their own counter statement.
func parkedCond(writer string) string {
	switch writer {
	case "match", "deposit":
		return condSleepingInPaymentUpdate
	}
	return condSleepingInSeq
}

// cursors takes the device's cursors before a pair of writers runs.
func (f *loFeeds) cursors(t *testing.T) (pay, inv int64) {
	t.Helper()
	pay, _ = f.pull(t, f.payInfo, f.tenant, 0)
	inv, _ = f.pull(t, f.invInfo, f.tenant, 0)
	return pay, inv
}

// (a) MarkNSF beside a register settle on the SAME invoice. The settle parks holding the invoice before its
// invoices counter row; the bounce queues behind it. Both must answer, and the bounced payment, the
// settle's payment and the invoice must all reach the feeds past the cursor.
func TestIntegration_LockOrder_NSFBesideSettleSameInvoiceReachesTheFeeds(t *testing.T) {
	for _, kind := range []string{"register", "paysettle"} {
		t.Run(kind, func(t *testing.T) {
			loPoolSizes(t, func(t *testing.T, size int32) {
				r := newLORig(t, size)
				inv := r.invoice("SA", 1_000_000)
				p := r.payOnce(inv, 10_000)
				r.stampOrg()
				f := r.feeds()
				payCur, invCur := f.cursors(t)

				r.armAllocWindow()
				setErr := make(chan error, 1)
				go func() { setErr <- r.settle(kind, inv, 500) }()
				r.waitSessions("settle parked holding the invoice, before the invoices counter row", 1, condSleepingInAlloc)
				nsfErr := make(chan error, 1)
				go func() {
					_, err := r.nsfService().MarkNSF(r.staffCtx(), till.NSFInput{PaymentID: p.ID, Reason: "lock order leg"})
					nsfErr <- err
				}()
				r.waitSessions("bounce queued", 1, condLockWaiter)
				loBothAnswered(t, "nsf", loAnswer(t, "nsf", nsfErr), kind, loAnswer(t, kind, setErr))

				_, pays := f.pull(t, f.payInfo, f.tenant, payCur)
				requireFresh(t, "bounced payment", pays, payCur, p.ID, "BOUNCED")
				fresh := 0
				for id, pp := range pays {
					if id != p.ID && pp.seq > payCur {
						fresh++
					}
				}
				if fresh != 1 {
					t.Errorf("the settle's payment reached the feed %d times past the cursor, want 1", fresh)
				}
				_, invs := f.pull(t, f.invInfo, f.tenant, invCur)
				requireFresh(t, "invoice", invs, invCur, inv, "")
			})
		})
	}
}

// loStatementLine seeds one unmatched statement line for the bank match.
func (r *loRig) statementLine(cents int64) string {
	r.t.Helper()
	ctx := context.Background()
	var acct string
	if err := r.obs.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&acct); err != nil {
		r.t.Skipf("no seeded bank account: %v", err)
	}
	day := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	ref := "LO-" + r.suffix
	var id string
	if err := r.obs.QueryRow(ctx, `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1, $2, 'LOCK ORDER MATCH', $3, $4, $5, 'UNMATCHED') RETURNING id::text`,
		acct, day, ref, cents, bankrec.DedupeHash(day, cents, "LOCK ORDER MATCH", ref)).Scan(&id); err != nil {
		r.t.Fatalf("seed statement line: %v", err)
	}
	r.t.Cleanup(func() {
		c := context.Background()
		_, _ = r.obs.Exec(c, `DELETE FROM bank_matches WHERE statement_line_id = $1::uuid`, id)
		_, _ = r.obs.Exec(c, `DELETE FROM bank_statement_lines WHERE id = $1::uuid`, id)
	})
	return id
}

func (r *loRig) match(lineID, paymentID string, cents int64) error {
	_, err := bankrec.NewRepository(r.pool).PersistMatch(context.Background(), bankrec.Match{
		StatementLineID: lineID, TargetType: bankrec.TargetPayment, TargetRefID: paymentID,
		Method: bankrec.MethodManual, AmountCents: cents,
	}, []string{paymentID})
	return err
}

// (b) a bank match beside a reversal of the SAME payment, in both orders. The first writer parks holding the
// payment row and the payments counter; the second queues behind it. Neither may deadlock, and the original
// payment and the reversing entry must reach the payer's feed past the cursor.
func TestIntegration_LockOrder_BankMatchBesideReversalOfTheSamePaymentReachesTheFeed(t *testing.T) {
	for _, first := range []string{"reversal first", "match first"} {
		t.Run(first, func(t *testing.T) {
			loPoolSizes(t, func(t *testing.T, size int32) {
				r := newLORig(t, size)
				inv := r.invoice("SB", 1_000_000)
				p := r.payOnce(inv, 10_000)
				r.stampOrg()
				line := r.statementLine(10_000)
				f := r.feeds()
				payCur, _ := f.cursors(t)

				reverse := func() error {
					_, err := r.repo.ReversePayment(context.Background(), payments.ReverseInput{
						PaymentID: p.ID, Reason: "lock order leg", ActorID: r.userID, IsManager: true}, time.Now())
					return err
				}
				match := func() error { return r.match(line, p.ID, 10_000) }
				a, b := reverse, match
				aName, bName := "reversal", "match"
				if first == "match first" {
					a, b, aName, bName = match, reverse, "match", "reversal"
				}
				r.armSeqWindow("payments")
				aErr := make(chan error, 1)
				go func() { aErr <- a() }()
				r.waitSessions(aName+" parked holding the payments counter row", 1, parkedCond(aName))
				bErr := make(chan error, 1)
				go func() { bErr <- b() }()
				r.waitSessions(bName+" queued behind it", 1, condLockWaiter)
				loBothAnswered(t, aName, loAnswer(t, aName, aErr), bName, loAnswer(t, bName, bErr))

				_, pays := f.pull(t, f.payInfo, f.tenant, payCur)
				requireFresh(t, "the payment", pays, payCur, p.ID, "")
				rows := 0
				for id, pp := range pays {
					if id != p.ID && pp.seq > payCur {
						rows++
					}
				}
				if rows != 1 {
					t.Errorf("the reversing entry reached the feed %d times past the cursor, want 1", rows)
				}
			})
		})
	}
}

// (c) MarkDeposited beside a register settle from the same customer, in both orders. The deposit moves one
// payment's status (the payments counter after the payment row); the settle takes the payments counter, then
// its invoice, then writes a new payment. Neither may deadlock, and both payments reach the feed.
func TestIntegration_LockOrder_DepositBesideSettleSameCustomerReachesTheFeed(t *testing.T) {
	for _, first := range []string{"deposit first", "settle first"} {
		t.Run(first, func(t *testing.T) {
			loPoolSizes(t, func(t *testing.T, size int32) {
				r := newLORig(t, size)
				invA := r.invoice("SC1", 1_000_000)
				invB := r.invoice("SC2", 1_000_000)
				p := r.payOnce(invA, 10_000)
				r.stampOrg()
				f := r.feeds()
				payCur, _ := f.cursors(t)

				deposit := func() error {
					return till.NewRepository(r.pool).MarkDeposited(context.Background(), []string{p.ID}, "lo-line")
				}
				settle := func() error { return r.settle("register", invB, 500) }
				a, b := deposit, settle
				aName, bName := "deposit", "settle"
				if first == "settle first" {
					a, b, aName, bName = settle, deposit, "settle", "deposit"
				}
				r.armSeqWindow("payments")
				aErr := make(chan error, 1)
				go func() { aErr <- a() }()
				r.waitSessions(aName+" parked holding the payments counter row", 1, parkedCond(aName))
				bErr := make(chan error, 1)
				go func() { bErr <- b() }()
				r.waitSessions(bName+" queued behind it", 1, condLockWaiter)
				loBothAnswered(t, aName, loAnswer(t, aName, aErr), bName, loAnswer(t, bName, bErr))

				_, pays := f.pull(t, f.payInfo, f.tenant, payCur)
				requireFresh(t, "the deposited payment", pays, payCur, p.ID, "DEPOSITED")
				fresh := 0
				for id, pp := range pays {
					if id != p.ID && pp.seq > payCur {
						fresh++
					}
				}
				if fresh != 1 {
					t.Errorf("the settle's payment reached the feed %d times past the cursor, want 1", fresh)
				}
			})
		})
	}
}
