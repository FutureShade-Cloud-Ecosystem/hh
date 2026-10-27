//go:build integration

// split_tender_integration_test.go, DIB-165 against real Postgres.
//
// Two things can only be proved here and not in the unit suite:
//
//  1. the DEFERRED CONSTRAINT TRIGGER from migration 0100. The Go-side checkTenderSum refuses a bad
//     split before the tx opens; this file bypasses the service entirely and writes leg rows straight
//     through psql-equivalent SQL, which is exactly the path a future writer (or a person with a
//     database console) would take. If the trigger is not doing its job, that path succeeds.
//  2. that a real split sale composes real orders/invoices/payments/allocations that reconcile.
//
// Run: DATABASE_URL=... go test -tags integration -count=1 ./internal/pos/...
package pos

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// timeNowDate is today's ISO date, the shape the lookup's from/to bounds take.
//
// IT READS THE HOUSE ZONE. The previous body was `time.Now().UTC()`, and the header here used to
// argue at length that UTC was deliberate because it MODELLED THE QUERY: the lookup bounded on
// `ps.created_at >= $N::date`, a bare `::date` cast resolves in the SESSION's timezone, and that is
// UTC in this stack, so the day to ask for was the day the DATABASE would file the sale under.
// That argument was correct and is now void, because THE THING IT MODELLED IS GONE. The same header
// named the boundary "a separate, LIVE question, recorded rather than changed here"; it has since
// been changed. LookupSales no longer casts anything: saleLookupBounds (sale_lookup.go:176-194)
// resolves both bounds in Go against houseZone = "America/Toronto" (sale_lookup.go:138) and binds
// them as instants, half-open, at sale_lookup.go:273 and :278.
//
// THE DEFECT THIS LEAVES BEHIND, WHICH IS WHY THE BODY MOVED WITH IT. The helper and the query used
// to be wrong in the SAME direction, so they agreed and this subtest passed. Corrected on one side
// only, they disagree for exactly the four hours between 20:00 Toronto and midnight, when UTC has
// already rolled over: `date bounds include the same-day sale` (:346) rings a sale at 21:00, asks
// for 21:00's UTC date, tomorrow, and the lookup answers with tomorrow's window, which begins four
// hours in the future and cannot contain the row the test just wrote. The subtest fails, and it
// fails on the clock rather than on the code, which is the worst shape a suite failure takes: green
// all afternoon, red on an evening gate run, and nothing in the diff to blame.
//
// The whole point of the corrected boundary is that the evening sale belongs to the day the yard was
// open, TestSaleLookupBounds_TheEveningSaleBelongsToTheDayTheYardWasOpen (sale_lookup_day_test.go)
// is the unit-level statement of it, so a test asking "does the lookup find a sale I rang today"
// must ask for the day THE YARD calls today. houseLocation() is this package's own resolver and is
// the one the query itself uses, so the helper cannot drift from the boundary again without the two
// moving together.
//
// NO `TZ=` AND NO HOST CALENDAR. Reading the host's local zone is what made this test fail every
// evening on a UTC-7 machine before UTC was adopted; the house zone is neither the host's nor
// Greenwich's, and it is read from tzdata at call time exactly as production reads it, so the suite
// stays timezone-independent in the sense gl/businessday.go established.
//
// Revert cost: one expression. `time.Now().In(houseLocation())` back to `time.Now().UTC()` restores
// the old helper, and the subtest goes back to passing twenty hours a day.
func timeNowDate() string { return time.Now().In(houseLocation()).Format("2006-01-02") }

// TestIntegration_SplitTender_Commits rings a real two-leg split and reconciles every downstream row.
func TestIntegration_SplitTender_Commits(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realPosSvc(t, pool, stubCharger{})
	productID, branchID := aPaver(t, pool)
	sessionID := openTill(t, pool, branchID)
	sctx := globalCtx(t, pool)

	// Price the line first so the split can be expressed against the REAL total. Ringing a guessed
	// total would make this test a coin flip on the seeded price list.
	probe := SaleInput{
		IdempotencyKey: key(), BranchID: branchID, TillSessionID: sessionID, CustomerID: walkInID(t, pool),
		SaleType: SaleCollectNow, Tender: TenderCash,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 10, UOMEntered: "SF"}},
	}
	priced, err := svc.Sale(sctx, probe)
	if err != nil {
		t.Fatalf("probe sale: %v", err)
	}
	total := priced.TotalCents
	if total < 3 {
		t.Fatalf("probe total %d is too small to split, the seeded price list changed", total)
	}
	cash := total / 3
	card := total - cash

	in := SaleInput{
		IdempotencyKey: key(), BranchID: branchID, TillSessionID: sessionID, CustomerID: walkInID(t, pool),
		SaleType: SaleCollectNow,
		Lines:    []LineInput{{ProductID: productID, QtyEntered: 10, UOMEntered: "SF"}},
		Tenders: []TenderLegInput{
			{Method: TenderCash, AmountCents: cash},
			{Method: TenderCard, AmountCents: card, PaymentToken: "tok_stub"},
		},
	}
	out, err := svc.Sale(sctx, in)
	if err != nil {
		t.Fatalf("split sale: %v", err)
	}
	if !out.SplitTender || len(out.Tenders) != 2 {
		t.Fatalf("response must carry the legs: %+v", out)
	}

	// The persisted legs sum to the order total, the same assertion the trigger makes, read back.
	var legSum, orderTotal int64
	var legs int
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(st.amount_cents),0), count(*), max(o.total_cents)
		  FROM pos_sale ps
		  JOIN orders o ON o.id = ps.order_id
		  JOIN pos_sale_tenders st ON st.pos_sale_id = ps.id
		 WHERE ps.order_id = $1`, out.OrderID).Scan(&legSum, &legs, &orderTotal); err != nil {
		t.Fatalf("read legs: %v", err)
	}
	if legs != 2 || legSum != orderTotal {
		t.Fatalf("legs=%d sum=%d order total=%d, a committed split must settle its sale exactly", legs, legSum, orderTotal)
	}

	// Two DISTINCT payments rows, one per leg, each attached to the drawer session.
	var payments int
	if err := pool.QueryRow(ctx, `
		SELECT count(DISTINCT st.payment_id) FROM pos_sale ps
		  JOIN pos_sale_tenders st ON st.pos_sale_id = ps.id
		 WHERE ps.order_id = $1 AND st.payment_id IS NOT NULL`, out.OrderID).Scan(&payments); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if payments != 2 {
		t.Fatalf("want 2 distinct payments rows (one per captured leg), got %d, legs sharing a row means "+
			"the derived idempotency keys collapsed", payments)
	}

	// Allocations reconcile the invoice to zero.
	var allocated, invBalance int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(pa.amount_cents),0), max(i.balance_cents)
		  FROM invoices i LEFT JOIN payment_allocations pa ON pa.invoice_id = i.id
		 WHERE i.id = $1`, out.InvoiceID).Scan(&allocated, &invBalance); err != nil {
		t.Fatalf("read allocations: %v", err)
	}
	if allocated != orderTotal || invBalance != 0 {
		t.Fatalf("allocations=%d balance=%d, want the invoice fully settled at %d", allocated, invBalance, orderTotal)
	}

	// The reopen view carries the legs back out.
	view, err := svc.GetSale(sctx, out.OrderID)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !view.SplitTender || len(view.Tenders) != 2 {
		t.Fatalf("the reopened sale must show how it was actually settled: %+v", view)
	}
	if view.Tenders[0].Seq != 1 || view.Tenders[1].Seq != 2 {
		t.Fatalf("legs must come back in submission order: %+v", view.Tenders)
	}
}

// TestIntegration_SplitTender_DeferredSumGuard is the DB-level half of the split-tender guard.
//
// MUTATION TARGET: the `IF v_sum <> v_total THEN RAISE` arm of pos_sale_tenders_sum_guard()
// (migration 0100), or the CONSTRAINT TRIGGER that invokes it. Weaken either and the under/over
// tender commits below succeed.
//
// It writes leg rows DIRECTLY, not through the service: the point of a database constraint is that it
// binds writers the service does not know about. Every case runs in its own transaction which is
// rolled back, so the test leaves the database exactly as it found it, and because the trigger is
// DEFERRABLE INITIALLY DEFERRED, "does it fire" is genuinely a question about COMMIT, which is why
// each case commits rather than merely inserting.
func TestIntegration_SplitTender_DeferredSumGuard(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realPosSvc(t, pool, stubCharger{})
	productID, branchID := aPaver(t, pool)
	sessionID := openTill(t, pool, branchID)
	sctx := globalCtx(t, pool)

	// A committed single-tender sale gives us a real pos_sale + orders pair to hang legs off.
	base, err := svc.Sale(sctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, TillSessionID: sessionID, CustomerID: walkInID(t, pool),
		SaleType: SaleCollectNow, Tender: TenderCash,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("base sale: %v", err)
	}
	var posSaleID string
	var total int64
	if err := pool.QueryRow(ctx,
		`SELECT ps.id::text, o.total_cents FROM pos_sale ps JOIN orders o ON o.id = ps.order_id
		  WHERE ps.order_id = $1`, base.OrderID).Scan(&posSaleID, &total); err != nil {
		t.Fatalf("read base sale: %v", err)
	}

	// insertLegs writes the given amounts as legs in ONE transaction and reports the commit error.
	insertLegs := func(t *testing.T, amounts ...int64) error {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		for i, amt := range amounts {
			if _, err := tx.Exec(ctx,
				`INSERT INTO pos_sale_tenders (pos_sale_id, seq, method, amount_cents) VALUES ($1,$2,$3,$4)`,
				posSaleID, i+1, TenderCash, amt); err != nil {
				return err // a row-level CHECK (e.g. amount > 0) fires here, before COMMIT
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		// Committed, undo it so the next case starts clean and the DB is left as found.
		if _, derr := pool.Exec(ctx, `DELETE FROM pos_sale_tenders WHERE pos_sale_id = $1`, posSaleID); derr != nil {
			t.Fatalf("cleanup: %v", derr)
		}
		return nil
	}

	t.Run("under-tender is refused at COMMIT", func(t *testing.T) {
		err := insertLegs(t, total/2, total-total/2-1)
		if err == nil {
			t.Fatal("legs summing to less than the sale total must not commit")
		}
		if !strings.Contains(err.Error(), "sum to") {
			t.Fatalf("want the sum-guard message, got %v", err)
		}
	})

	t.Run("over-tender is refused at COMMIT", func(t *testing.T) {
		err := insertLegs(t, total, 1)
		if err == nil {
			t.Fatal("legs summing to more than the sale total must not commit")
		}
		if !strings.Contains(err.Error(), "sum to") {
			t.Fatalf("want the sum-guard message, got %v", err)
		}
	})

	t.Run("a single leg short of the total is refused", func(t *testing.T) {
		// The partial-write case: one leg inserted and the tx committed before the rest arrive. Under a
		// NON-deferred trigger this would be indistinguishable from a legitimate mid-transaction state;
		// deferring is what makes it checkable at exactly the right moment.
		if err := insertLegs(t, total-1); err == nil {
			t.Fatal("a lone leg that does not settle the sale must not commit")
		}
	})

	t.Run("an exact split commits", func(t *testing.T) {
		// The positive arm: without it, a trigger that refuses EVERY leg write would pass the three
		// refusal cases above. It also exercises the "no legs is not an under-tender" arm, because the
		// cleanup inside insertLegs removes every leg again, a guard that refused that would make a
		// split irreversible.
		if err := insertLegs(t, total/2, total-total/2); err != nil {
			t.Fatalf("legs that settle the sale exactly must commit: %v", err)
		}
		var remaining int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pos_sale_tenders WHERE pos_sale_id = $1`,
			posSaleID).Scan(&remaining); err != nil {
			t.Fatalf("verify cleanup: %v", err)
		}
		if remaining != 0 {
			t.Fatalf("the fixture must restore what it touched, %d legs left behind", remaining)
		}
	})

	t.Run("two ACCOUNT legs are refused", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		for i, amt := range []int64{total / 2, total - total/2} {
			_, err = tx.Exec(ctx,
				`INSERT INTO pos_sale_tenders (pos_sale_id, seq, method, amount_cents) VALUES ($1,$2,'ACCOUNT',$3)`,
				posSaleID, i+1, amt)
			if err != nil {
				break
			}
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err == nil {
			// Defensive cleanup BEFORE the failure. If the constraint is ever weakened (or is being
			// mutation-tested), the commit above succeeds and these rows survive the rolled-back tx -
			// leaving duplicates that the constraint itself then cannot be recreated over. A fixture
			// has to restore what it touched even on the path where the thing it asserts is broken.
			_, _ = pool.Exec(context.Background(), `DELETE FROM pos_sale_tenders WHERE pos_sale_id = $1`, posSaleID)
			t.Fatal("two ACCOUNT legs would post the remainder to AR twice, the partial unique index must refuse")
		}
	})
}

// TestIntegration_LookupSales_BranchFence proves the past-sale search fences at the SQL level: a
// scoped caller's results contain only its own branch, and an out-of-scope branch is a 403 rather
// than an empty list (which would be indistinguishable from "no sales that day").
func TestIntegration_LookupSales_BranchFence(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realPosSvc(t, pool, stubCharger{})
	productID, branchID := aPaver(t, pool)
	sessionID := openTill(t, pool, branchID)

	sale, err := svc.Sale(globalCtx(t, pool), SaleInput{
		IdempotencyKey: key(), BranchID: branchID, TillSessionID: sessionID, CustomerID: walkInID(t, pool),
		SaleType: SaleCollectNow, Tender: TenderCash,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 3, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("fixture sale: %v", err)
	}

	// A second branch to be excluded by.
	var otherBranch string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'KGN' AND id <> $1 LIMIT 1`, branchID).Scan(&otherBranch); err != nil {
		if err == pgx.ErrNoRows {
			t.Skip("only one branch seeded, nothing to fence against")
		}
		t.Fatalf("second branch: %v", err)
	}

	scoped := staffCtx(t, pool, identity.ScopeBranch, branchID)
	t.Run("own branch finds the sale", func(t *testing.T) {
		got, err := svc.LookupSales(scoped, SaleLookupQuery{BranchID: branchID, Q: sale.OrderNo})
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		found := false
		for _, s := range got {
			if s.OrderID == sale.OrderID {
				found = true
			}
			if s.BranchID != branchID {
				t.Fatalf("a fenced search returned another branch's sale: %+v", s)
			}
		}
		if !found {
			t.Fatalf("the sale just rung must be findable by its order number %q", sale.OrderNo)
		}
	})

	t.Run("till-session narrowing", func(t *testing.T) {
		got, err := svc.LookupSales(scoped, SaleLookupQuery{BranchID: branchID, TillSessionID: sessionID})
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		for _, s := range got {
			if s.TillSessionID != sessionID {
				t.Fatalf("session filter leaked another drawer's sale: %+v", s)
			}
		}
	})

	t.Run("another branch is forbidden, not empty", func(t *testing.T) {
		_, err := svc.LookupSales(scoped, SaleLookupQuery{BranchID: otherBranch})
		if err == nil {
			t.Fatal("a scoped caller must not search another branch")
		}
		if st, ok := ClientStatus(err); !ok || st != 403 {
			t.Fatalf("want 403, got status=%d (%v)", st, err)
		}
	})

	t.Run("date bounds include the same-day sale", func(t *testing.T) {
		today := timeNowDate()
		got, err := svc.LookupSales(scoped, SaleLookupQuery{BranchID: branchID, From: today, To: today, Q: sale.OrderNo})
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("an inclusive to-date must include a sale rung later the same day")
		}
	})
}
