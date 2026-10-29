//go:build integration

// ap_payable_payee_integration_test.go, W2-1d item 3: a landed cost names its
// freight carrier or customs broker as a vendor, and the aging labels the
// payable with that vendor's name. The PO reference is the fallback for old
// rows only. Run with:
//
//	DATABASE_URL=... go test -tags integration ./internal/landedcost/... -p 1
package landedcost

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// payeeCarrierVendorNo is the test's own carrier vendor. The name is what the
// aging must show once the landed cost names its payee.
const payeeCarrierVendorNo = "V-W21D-CARRIER"
const payeeCarrierName = "Cairnbrook Freight Lines"

func TestIntegration_PayeeVendorLabelsThePayable(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()

	var payeeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO vendors (vendor_no, name, payment_terms_days, is_edi)
		VALUES ($1, $2, 30, false)
		ON CONFLICT (vendor_no) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text`, payeeCarrierVendorNo, payeeCarrierName).Scan(&payeeID); err != nil {
		t.Fatalf("seed the payee vendor: %v", err)
	}

	cap := &fakeCapitalizer{lines: []ReceivedLine{
		{ProductID: prodA, QtyBase: 4, ValueCents: 30000, WeightKg: 2},
	}}
	gl := &fakeGLPoster{}
	svc := newSvc(pool, cap, gl)

	// A landed cost that names its carrier: the aging labels the payable with
	// the carrier's name.
	named, err := svc.Create(ctx, CreateInput{
		POID: landedPO, PORef: "LC-IT-PAYEE-1", BranchID: trentonBranch, Currency: "CAD",
		FreightCents: 45000, AllocationBasis: "weight",
		PayeeVendorID: payeeID,
	})
	if err != nil {
		t.Fatalf("create named landed cost: %v", err)
	}
	if err := svc.Apply(ctx, named); err != nil {
		t.Fatalf("apply named landed cost: %v", err)
	}

	// An old row names no payee: the PO reference is the fallback.
	legacy, err := svc.Create(ctx, CreateInput{
		POID: landedPO, PORef: "LC-IT-PAYEE-OLD", BranchID: trentonBranch, Currency: "CAD",
		FreightCents: 45000, AllocationBasis: "weight",
	})
	if err != nil {
		t.Fatalf("create legacy landed cost: %v", err)
	}
	if err := svc.Apply(ctx, legacy); err != nil {
		t.Fatalf("apply legacy landed cost: %v", err)
	}

	payables, err := svc.OpenPayablesAsOf(ctx, time.Now().UTC().Add(time.Second), nil, true)
	if err != nil {
		t.Fatalf("open payables: %v", err)
	}
	byID := map[string]OpenPayable{}
	for _, p := range payables {
		byID[p.ID] = p
	}

	namedPayable, ok := byID[named]
	if !ok {
		t.Fatalf("the named landed cost's payable is missing from the aging read (%d payables)", len(payables))
	}
	if namedPayable.PayeeLabel != payeeCarrierName {
		t.Errorf("the aging labels the payable %q, want the payee vendor's name %q", namedPayable.PayeeLabel, payeeCarrierName)
	}
	if namedPayable.PayeeVendorID != payeeID {
		t.Errorf("the payable names payee vendor %q, want %q", namedPayable.PayeeVendorID, payeeID)
	}

	legacyPayable, ok := byID[legacy]
	if !ok {
		t.Fatalf("the legacy landed cost's payable is missing from the aging read")
	}
	if legacyPayable.PayeeLabel != "Freight Carrier (LC-IT-PAYEE-OLD)" {
		t.Errorf("the old row's label %q, want the PO reference fallback %q", legacyPayable.PayeeLabel, "Freight Carrier (LC-IT-PAYEE-OLD)")
	}

	// A payee that names no vendor is a clean client error, never a 500.
	_, err = svc.Create(ctx, CreateInput{
		POID: landedPO, PORef: "LC-IT-PAYEE-BAD", BranchID: trentonBranch, Currency: "CAD",
		FreightCents: 45000, AllocationBasis: "weight",
		PayeeVendorID: "00000000-0000-4000-8000-00000000d1ce",
	})
	if err == nil {
		t.Fatalf("create with an unknown payee vendor succeeded, want a clean client error")
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != http.StatusBadRequest {
		t.Fatalf("unknown payee vendor error = %v, want a 400 client error", err)
	}

	// A payee that is not even uuid-shaped is the same clean refusal.
	_, err = svc.Create(ctx, CreateInput{
		POID: landedPO, PORef: "LC-IT-PAYEE-JUNK", BranchID: trentonBranch, Currency: "CAD",
		FreightCents: 45000, AllocationBasis: "weight",
		PayeeVendorID: "not-a-vendor",
	})
	if err == nil {
		t.Fatalf("create with a junk payee vendor succeeded, want a clean client error")
	}
	if !errors.As(err, &ce) || ce.status != http.StatusBadRequest {
		t.Fatalf("junk payee vendor error = %v, want a 400 client error", err)
	}
}

// TestIntegration_SettlePaysThePayableOnItsTerms (W2-1d item 4): the settle
// route pays a posted landed cost's payable in full, the aging reads it paid,
// a re-press pays nothing twice, and an unsettled payable still reads open.
func TestIntegration_SettlePaysThePayableOnItsTerms(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()

	var payeeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO vendors (vendor_no, name, payment_terms_days, is_edi)
		VALUES ($1, $2, 30, false)
		ON CONFLICT (vendor_no) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text`, payeeCarrierVendorNo, payeeCarrierName).Scan(&payeeID); err != nil {
		t.Fatalf("seed the payee vendor: %v", err)
	}

	cap := &fakeCapitalizer{lines: []ReceivedLine{
		{ProductID: prodA, QtyBase: 4, ValueCents: 30000, WeightKg: 2},
	}}
	gl := &fakeGLPoster{}
	svc := newSvc(pool, cap, gl)

	id, err := svc.Create(ctx, CreateInput{
		POID: landedPO, PORef: "LC-IT-SETTLE-1", BranchID: trentonBranch, Currency: "CAD",
		FreightCents: 60000, AllocationBasis: "weight",
		PayeeVendorID: payeeID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Apply(ctx, id); err != nil {
		t.Fatalf("apply: %v", err)
	}

	readOpen := func(asOf time.Time) OpenPayable {
		t.Helper()
		payables, err := svc.OpenPayablesAsOf(ctx, asOf, nil, true)
		if err != nil {
			t.Fatalf("open payables: %v", err)
		}
		for _, p := range payables {
			if p.ID == id {
				return p
			}
		}
		t.Fatalf("the payable disappeared from the aging read")
		return OpenPayable{}
	}

	before := time.Now().UTC().Add(time.Second)
	open := readOpen(before)
	if open.PaidCadCents != 0 || open.TotalCadCents != 60000 {
		t.Fatalf("before the settle the payable reads total %d paid %d, want 60000/0", open.TotalCadCents, open.PaidCadCents)
	}

	if err := svc.Settle(ctx, id, SettleInput{PaidAt: &before}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	// A re-press is a no-op, not a second payment.
	if err := svc.Settle(ctx, id, SettleInput{PaidAt: &before}); err != nil {
		t.Fatalf("re-settle: %v", err)
	}

	paid := readOpen(before.Add(time.Second))
	if paid.PaidCadCents != 60000 {
		t.Errorf("after the settle the payable reads paid %d, want 60000", paid.PaidCadCents)
	}

	// The as-of read cuts the payment at the settlement instant: before it the
	// payable is open, after it the payable is gone from the aging.
	payables, err := svc.OpenPayablesAsOf(ctx, before.Add(-time.Second), nil, true)
	if err != nil {
		t.Fatalf("open payables as of before the settle: %v", err)
	}
	for _, p := range payables {
		if p.ID == id {
			t.Errorf("the payable still reads open %d cents as of before its settlement", p.TotalCadCents-p.PaidCadCents)
		}
	}

	// The settlement journal is balanced and relieves GL 2100.
	j := gl.journals["landedcost.settle:"+id]
	if j.IdempotencyKey == "" {
		t.Fatalf("no settlement journal posted under the landedcost.settle key")
	}
	if gl.debit("landedcost.settle:"+id, "2100") != 60000 || gl.credit("landedcost.settle:"+id, "2100") != 0 {
		t.Errorf("the settlement journal does not debit 2100 by the charge")
	}
	if gl.credit("landedcost.settle:"+id, "1000") != 60000 {
		t.Errorf("the settlement journal does not credit 1000 by the charge")
	}

	// Settling a DRAFT refuses with a conflict, and a VOIDED payable is not payable.
	draftID, err := svc.Create(ctx, CreateInput{
		POID: landedPO, PORef: "LC-IT-SETTLE-DRAFT", BranchID: trentonBranch, Currency: "CAD",
		FreightCents: 60000, AllocationBasis: "weight",
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if err := svc.Settle(ctx, draftID, SettleInput{PaidAt: &before}); err == nil {
		t.Errorf("settling a DRAFT landed cost succeeded, want a conflict")
	}
}

// TestIntegration_VoidAfterSettleRefusedAnd2100TieHolds (W3 P2-1 leg 1, S6's
// probe): apply a landed cost, settle it, then attempt to void it. Void must
// refuse in words ("this landed cost is settled; record the carrier's credit
// instead"), post no reversing journal, and keep GL 2100 tied to the open
// payable (zero on both sides).
func TestIntegration_VoidAfterSettleRefusedAnd2100TieHolds(t *testing.T) {
	pool := intPool(t)
	seedPO(t, pool)
	ctx := context.Background()

	cap := &fakeCapitalizer{lines: []ReceivedLine{
		{ProductID: prodA, QtyBase: 4, ValueCents: 30000, WeightKg: 2},
	}}
	gl := &fakeGLPoster{}
	svc := newSvc(pool, cap, gl)

	id, err := svc.Create(ctx, CreateInput{
		POID: landedPO, PORef: "LC-S6-VOID-AFTER-SETTLE", BranchID: trentonBranch, Currency: "CAD",
		FreightCents: 60000, AllocationBasis: "weight",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Apply(ctx, id); err != nil {
		t.Fatalf("apply: %v", err)
	}
	at := time.Now().UTC().Add(time.Second)
	if err := svc.Settle(ctx, id, SettleInput{PaidAt: &at}); err != nil {
		t.Fatalf("settle: %v", err)
	}

	voidErr := svc.Void(ctx, id)
	var net2100, net1000, net1200 int64
	for _, j := range gl.journals {
		var d, c int64
		for _, l := range j.Lines {
			d += l.DebitCents
			c += l.CreditCents
			switch l.AccountCode {
			case "2100":
				net2100 += l.CreditCents - l.DebitCents
			case "1000":
				net1000 += l.DebitCents - l.CreditCents
			case "1200":
				net1200 += l.DebitCents - l.CreditCents
			}
		}
		if d != c {
			t.Errorf("journal %s is unbalanced: debit %d credit %d", j.IdempotencyKey, d, c)
		}
	}

	if voidErr == nil {
		t.Fatalf("void after settle succeeded; GL 2100 nets %d cents, GL 1000 nets %d cents (want 409 refusal)", net2100, net1000)
	}
	if got := statusOf(voidErr); got != http.StatusConflict {
		t.Errorf("void after settle status = %d (%v), want 409", got, voidErr)
	}
	const wantWords = "this landed cost is settled; record the carrier's credit instead"
	if !strings.Contains(voidErr.Error(), wantWords) {
		t.Errorf("void after settle error = %q, want words %q", voidErr.Error(), wantWords)
	}
	if _, exists := gl.journals["landedcost.void:"+id]; exists {
		t.Errorf("a refused void posted a reversing journal under landedcost.void:%s", id)
	}

	// The 2100 tie holds: GL 2100 net credit is 0 and the open payable on the
	// aging is 0 (60000 total less 60000 paid).
	payables, err := svc.OpenPayablesAsOf(ctx, at.Add(time.Second), nil, true)
	if err != nil {
		t.Fatalf("open payables: %v", err)
	}
	var openCents int64
	for _, p := range payables {
		if p.ID == id {
			openCents += p.TotalCadCents - p.PaidCadCents
		}
	}
	if net2100 != 0 || openCents != 0 || net2100 != openCents {
		t.Errorf("2100 tie broken after refused void: GL 2100 net credit = %d, open payable = %d, want 0/0", net2100, openCents)
	}
	if net1200 != 60000 || cap.caps[prodA] != 60000 {
		t.Errorf("inventory tie after refused void: GL 1200 = %d, subledger = %d, want 60000/60000", net1200, cap.caps[prodA])
	}
}

// TestIntegration_VoidRacingSettleExactlyOneWinsAndBooksTie (W3 P2-1 leg 2): a
// Void and a Settle race on the same applied landed cost after both have passed
// their pre-transaction read. Exactly one must win under the FOR UPDATE row
// lock, the other must refuse with 409, and the books must tie in both race
// orders (Settle winning the lock first, and Void winning the lock first).
func TestIntegration_VoidRacingSettleExactlyOneWinsAndBooksTie(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	helper := intPool(t)
	seedPO(t, helper)

	for _, settleFirst := range []bool{true, false} {
		name := "void_wins_lock"
		if settleFirst {
			name = "settle_wins_lock"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			appName := "lc_p21_race_" + name
			cfg, err := pgxpool.ParseConfig(url)
			if err != nil {
				t.Fatalf("parse url: %v", err)
			}
			cfg.MaxConns = 4
			cfg.ConnConfig.RuntimeParams["application_name"] = appName
			scnPool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatalf("scenario pool: %v", err)
			}
			defer scnPool.Close()

			cap := &fakeCapitalizer{lines: []ReceivedLine{
				{ProductID: prodA, QtyBase: 4, ValueCents: 30000, WeightKg: 2},
			}}
			gl := &fakeGLPoster{}
			svc := newSvc(scnPool, cap, gl)

			id, err := svc.Create(ctx, CreateInput{
				POID: landedPO, PORef: "LC-S6-RACE-" + name, BranchID: trentonBranch, Currency: "CAD",
				FreightCents: 60000, AllocationBasis: "weight",
			})
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if err := svc.Apply(ctx, id); err != nil {
				t.Fatalf("apply: %v", err)
			}

			// Hold the row FOR UPDATE on the helper pool so both callers pass
			// guardBranch (plain SELECT) and park on LockForUpdateTx inside their
			// transactions.
			btx, err := helper.Begin(ctx)
			if err != nil {
				t.Fatalf("blocker begin: %v", err)
			}
			defer btx.Rollback(ctx)
			if _, err := btx.Exec(ctx, `SELECT 1 FROM landed_costs WHERE id = $1 FOR UPDATE`, id); err != nil {
				t.Fatalf("blocker lock: %v", err)
			}

			waitParked := func(want int) {
				t.Helper()
				for until := time.Now().Add(3 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
					var parked int
					if err := helper.QueryRow(ctx, `
						SELECT count(*) FROM pg_stat_activity
						 WHERE application_name = $1 AND wait_event_type = 'Lock'`, appName).Scan(&parked); err != nil {
						t.Fatalf("pg_stat_activity: %v", err)
					}
					if parked >= want {
						return
					}
				}
				t.Fatalf("timed out waiting for %d caller(s) to park on the row lock", want)
			}

			at := time.Now().UTC().Add(time.Second)
			var settleErr, voidErr error
			var wg sync.WaitGroup
			wg.Add(2)
			runSettle := func() {
				defer wg.Done()
				settleErr = svc.Settle(ctx, id, SettleInput{PaidAt: &at})
			}
			runVoid := func() {
				defer wg.Done()
				voidErr = svc.Void(ctx, id)
			}

			if settleFirst {
				go runSettle()
				waitParked(1)
				go runVoid()
				waitParked(2)
			} else {
				go runVoid()
				waitParked(1)
				go runSettle()
				waitParked(2)
			}

			// Release the blocker so the queued waiters run back-to-back.
			_ = btx.Rollback(ctx)
			wg.Wait()

			if (settleErr == nil) == (voidErr == nil) {
				t.Fatalf("race %s: settleErr=%v voidErr=%v, want exactly one winner", name, settleErr, voidErr)
			}
			if settleFirst {
				if settleErr != nil {
					t.Errorf("settle_wins_lock: settle failed: %v", settleErr)
				}
				if statusOf(voidErr) != http.StatusConflict || !strings.Contains(voidErr.Error(), "this landed cost is settled; record the carrier's credit instead") {
					t.Errorf("settle_wins_lock: voidErr = %v, want 409 settled refusal", voidErr)
				}
			} else {
				if voidErr != nil {
					t.Errorf("void_wins_lock: void failed: %v", voidErr)
				}
				if statusOf(settleErr) != http.StatusConflict {
					t.Errorf("void_wins_lock: settleErr = %v, want 409 conflict", settleErr)
				}
			}

			var net2100, net1200 int64
			for _, j := range gl.journals {
				var d, c int64
				for _, l := range j.Lines {
					d += l.DebitCents
					c += l.CreditCents
					switch l.AccountCode {
					case "2100":
						net2100 += l.CreditCents - l.DebitCents
					case "1200":
						net1200 += l.DebitCents - l.CreditCents
					}
				}
				if d != c {
					t.Errorf("journal %s unbalanced: debit %d credit %d", j.IdempotencyKey, d, c)
				}
			}

			payables, err := svc.OpenPayablesAsOf(ctx, at.Add(time.Second), nil, true)
			if err != nil {
				t.Fatalf("open payables: %v", err)
			}
			var openCents int64
			for _, p := range payables {
				if p.ID == id {
					openCents += p.TotalCadCents - p.PaidCadCents
				}
			}
			if net2100 != 0 || openCents != 0 {
				t.Errorf("race %s: 2100 tie broken: GL 2100 net credit = %d, open payable = %d, want 0/0", name, net2100, openCents)
			}
			if net1200 != cap.caps[prodA] {
				t.Errorf("race %s: inventory tie broken: GL 1200 = %d, subledger = %d", name, net1200, cap.caps[prodA])
			}
		})
	}
}
