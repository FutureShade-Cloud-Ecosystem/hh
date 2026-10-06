//go:build integration

// edit_integration_test.go, DIB-56 staff quote builder against a LIVE Postgres.
//
// What the unit tests cannot prove and these can:
//
//	I1  the ratified matrix holds against REAL rows, allowed AND blocked, with the blocked answer
//	    naming the status and the row left byte-identical
//	I2  an edit is reconstructable from the audit trail it actually persisted, including that nothing
//	    was redacted away, and that the rows of one edit share a request id
//	I3  the trail is ATOMIC with the mutation: a recorder that fails AFTER writing its row leaves zero
//	    persisted QUOTE_CHANGED rows, which only holds if it shares the write's transaction (R3)
//	I4  the status/expiry guards run UNDER the quote row lock, not before it
//	I5  the expiry ruling (D2) holds on real rows, including that a NULL valid_until never expires
//
// Run: DATABASE_URL=... go test -tags=integration ./internal/quote/... -count=1 -p 1
package quote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	intTrenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	intKingston = "5d1b3f6e-0000-4000-9000-000000000002"
)

// intPool opens the integration DB. DATABASE_URL set ⇒ a failure must FAIL (CI has a DB configured);
// unset ⇒ skip, so a plain `go test` on a laptop with no database stays green.
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

// anchoredProduct picks a catalogue product whose base UOM is also its price UOM (factor 1), so a
// pricing assertion stays about the quote path rather than becoming fixture archaeology through a
// pieces-per-square-foot conversion (the DIB-378 lesson).
func anchoredProduct(t *testing.T, pool *pgxpool.Pool) (productID, uom string) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT p.id::text, pu.uom_code::text
		  FROM products p
		  JOIN product_uoms pu ON pu.product_id = p.id AND pu.qty_in_base = 1
		 WHERE p.is_active
		 ORDER BY p.sku
		 LIMIT 1`).Scan(&productID, &uom)
	if err != nil {
		t.Skipf("no factor-1 anchored product seeded: %v", err)
	}
	return productID, uom
}

// seedQuote inserts a customer + a quote at `status` in Trenton WITH one line, and registers cleanup of
// everything it touched (including the audit trail the edits will write).
func seedQuote(t *testing.T, pool *pgxpool.Pool, status string, validUntil any) (cust, quoteID, prod, uom string) {
	t.Helper()
	ctx := context.Background()
	prod, uom = anchoredProduct(t, pool)
	tag := fmt.Sprintf("D56-%d", time.Now().UnixNano())

	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type) VALUES ($1,$2,'CONTRACTOR') RETURNING id::text`,
		tag, tag).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	var creator any
	if seedUserID != "" {
		creator = seedUserID
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, valid_until,
		                    subtotal_cents, tax_cents, total_cents, created_by)
		VALUES ($1, $2::uuid, $3::uuid, $4, $5, 2000, 0, 2000, $6) RETURNING id::text`,
		"#Q-"+tag, cust, intTrenton, status, validUntil, creator).Scan(&quoteID); err != nil {
		t.Fatalf("seed quote: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1::uuid, 1, $2::uuid, 'seed line', 2, $3, 2, 1000, $3, 2000)`,
		quoteID, prod, uom); err != nil {
		t.Fatalf("seed quote line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quote_lines WHERE quote_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust, quoteID, prod, uom
}

// The pricing/cost/customer-type doubles are the deterministic ones from edit_test.go (qPricer, qInv,
// qCustType, that file carries no build tag, so it compiles into the integration binary too). Using
// stubs rather than the real pricing service keeps a money assertion about the QUOTE path instead of
// whatever rate the pricing fixtures happen to carry today.

// intSvc composes the real repository + real audit service with deterministic pricing.
func intSvc(t *testing.T, pool *pgxpool.Pool, now time.Time) (*StaffService, *Repository, *audit.Service) {
	t.Helper()
	repo := NewRepository(pool).withNow(func() time.Time { return now })
	aud := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewStaffService(repo).
		WithAudit(aud).
		WithAuditTrail(aud).
		WithPricing(qPricer{}).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		WithActorNamer(repo).
		WithRoleConfig(qTestRoleConfig()).
		WithFloorArbiter(&unitFloorArbiter{}).
		withCaller(qCallerFrom).
		withNow(func() time.Time { return now })
	return svc, repo, aud
}

func intStaff(branch string) identity.StaffContext {
	return identity.StaffContext{
		// Inside Sales, the ladder's Work rung (plan 3.3); the seeded
		// quotes carry the same user as their creator, so the caller owns
		// them.
		UserID: seedUserID, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{branch: true},
	}
}

// seedUserID is resolved once per run from the seeded staff so audit actor_id satisfies its FK.
var seedUserID string

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	if pool, err := pgxpool.New(context.Background(), url); err == nil {
		_ = pool.QueryRow(context.Background(),
			`SELECT id::text FROM users WHERE status='ACTIVE' ORDER BY created_at LIMIT 1`).Scan(&seedUserID)
		pool.Close()
	}
	os.Exit(m.Run())
}

func liveLine(t *testing.T, pool *pgxpool.Pool, quoteID string, lineNo int) QuoteLineSnapshot {
	t.Helper()
	var s QuoteLineSnapshot
	err := pool.QueryRow(context.Background(), fmt.Sprintf(
		`SELECT %s FROM quote_lines WHERE quote_id = $1 AND line_no = $2`, editLineColumns),
		quoteID, lineNo).Scan(&s.LineNo, &s.ProductID, &s.QtyEntered, &s.QtyBase,
		&s.UnitPriceCents, &s.LineTotalCents)
	if err != nil {
		t.Fatalf("read live line: %v", err)
	}
	return s
}

func liveTotals(t *testing.T, pool *pgxpool.Pool, quoteID string) QuoteTotals {
	t.Helper()
	var tot QuoteTotals
	if err := pool.QueryRow(context.Background(),
		`SELECT subtotal_cents, total_cents FROM quotes WHERE id = $1`, quoteID).
		Scan(&tot.SubtotalCents, &tot.TotalCents); err != nil {
		t.Fatalf("read live totals: %v", err)
	}
	return tot
}

func countQuoteChanged(t *testing.T, pool *pgxpool.Pool, quoteID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_log WHERE entity_type='quote' AND entity_id = $1 AND action = $2`,
		quoteID, audit.ActQuoteChanged).Scan(&n); err != nil {
		t.Fatalf("count QUOTE_CHANGED: %v", err)
	}
	return n
}

// - I1: the matrix on real rows -

func TestIntegration_QuoteMatrix_AllowedAndBlockedCells(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	for _, field := range quoteFieldOrder {
		field := field
		t.Run(string(field), func(t *testing.T) {
			for _, status := range allQuoteStatuses {
				status := status
				t.Run(status, func(t *testing.T) {
					_, quoteID, prod, uom := seedQuote(t, pool, status, nil)
					svc, _, _ := intSvc(t, pool, now)
					ctx := qCtxAs(intStaff(intTrenton))

					beforeLine := liveLine(t, pool, quoteID, 1)
					beforeTot := liveTotals(t, pool, quoteID)

					var err error
					switch field {
					case FieldQuoteLineQty:
						_, err = svc.EditLineQty(ctx, quoteID, 1, "5")
					case FieldQuoteLineAdd:
						_, err = svc.AddLine(ctx, quoteID, prod, "added", "3", uom)
					case FieldQuoteLineRemove:
						_, err = svc.RemoveLine(ctx, quoteID, 1)
					case FieldQuoteValidUntil:
						_, err = svc.SetValidUntil(ctx, quoteID, "2027-01-31")
					}

					if ratifiedQuoteMatrix[field][status] {
						if err != nil {
							t.Fatalf("%s in %s: ratified EDITABLE, got %v", field, status, err)
						}
						return
					}
					// Blocked: 409 naming the status, and the row untouched.
					if err == nil {
						t.Fatalf("%s in %s: ratified FROZEN, but the edit succeeded", field, status)
					}
					var ne *NotEditableError
					if !errors.As(err, &ne) {
						t.Fatalf("%s in %s: want *NotEditableError, got %T: %v", field, status, err, err)
					}
					if ne.Status != status {
						t.Errorf("want the refusal to name %q, got %q", status, ne.Status)
					}
					if field != FieldQuoteLineRemove {
						if got := liveLine(t, pool, quoteID, 1); got != beforeLine {
							t.Errorf("a refused edit changed the row: %+v → %+v", beforeLine, got)
						}
					}
					if got := liveTotals(t, pool, quoteID); got != beforeTot {
						t.Errorf("a refused edit moved the totals: %+v → %+v", beforeTot, got)
					}
					if n := countQuoteChanged(t, pool, quoteID); n != 0 {
						t.Errorf("a refused edit wrote %d audit rows; want 0", n)
					}
				})
			}
		})
	}
}

// - I1b: totals fold in and back out -

func TestIntegration_QuoteTotalsRecompute(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, prod, uom := seedQuote(t, pool, "DRAFT", nil)
	svc, _, _ := intSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))

	// qty 2 → 5 at the stub's flat 1000/unit ⇒ 5000
	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err != nil {
		t.Fatalf("qty edit: %v", err)
	}
	if got := liveTotals(t, pool, quoteID); got.SubtotalCents != 5000 {
		t.Errorf("after qty edit subtotal = %d, want 5000", got.SubtotalCents)
	}
	// add a 3-unit line ⇒ +3000
	if _, err := svc.AddLine(ctx, quoteID, prod, "second", "3", uom); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := liveTotals(t, pool, quoteID); got.SubtotalCents != 8000 {
		t.Errorf("after add subtotal = %d, want 8000", got.SubtotalCents)
	}
	// remove line 1 ⇒ back to 3000
	if _, err := svc.RemoveLine(ctx, quoteID, 1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := liveTotals(t, pool, quoteID); got.SubtotalCents != 3000 {
		t.Errorf("after remove subtotal = %d, want 3000", got.SubtotalCents)
	}
	// The header must equal Σ lines, recomputed, never delta-adjusted.
	var sum int64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(line_total_cents),0) FROM quote_lines WHERE quote_id = $1`, quoteID).
		Scan(&sum); err != nil {
		t.Fatalf("sum lines: %v", err)
	}
	if got := liveTotals(t, pool, quoteID); got.SubtotalCents != sum {
		t.Errorf("subtotal %d != Σ lines %d", got.SubtotalCents, sum)
	}
}

// - I2: audit reconstruction -

func TestIntegration_QuoteAuditReconstruction(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	svc, _, _ := intSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err != nil {
		t.Fatalf("qty edit: %v", err)
	}
	hist, err := svc.QuoteEditHistory(ctx, quoteID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist.Changes) == 0 {
		t.Fatal("want change rows from a real edit")
	}
	byField := map[string]QuoteFieldChange{}
	reqIDs := map[string]bool{}
	for _, c := range hist.Changes {
		byField[c.Field] = c
		reqIDs[c.RequestID] = true
		// Nothing may have been redacted away, that is the whole point of the int64/bounded-string rule.
		if c.Before == "[redacted]" || c.After == "[redacted]" {
			t.Errorf("field %q was REDACTED (%q → %q), the trail must stay reconstructable",
				c.Field, c.Before, c.After)
		}
	}
	qty, ok := byField["qty_entered"]
	if !ok {
		t.Fatalf("want a qty_entered row; got %v", byField)
	}
	if qty.Before != "2.0000" || qty.After != "5.0000" {
		t.Errorf("qty_entered %q → %q, want 2.0000 → 5.0000", qty.Before, qty.After)
	}
	// Money must render as plain integer cents, not float notation.
	if lt, ok := byField["line_total_cents"]; ok {
		if strings.Contains(lt.After, ".") || strings.Contains(lt.After, "e") {
			t.Errorf("line_total_cents after = %q, money must render as integer cents", lt.After)
		}
		if lt.After != "5000" {
			t.Errorf("line_total_cents after = %q, want 5000", lt.After)
		}
	} else {
		t.Error("want a line_total_cents row")
	}
	// All rows of ONE edit share a request id, so a multi-column edit correlates into one action.
	if len(reqIDs) != 1 {
		t.Errorf("rows of one edit carry %d distinct request ids; want 1", len(reqIDs))
	}
	if qty.ActorID != seedUserID {
		t.Errorf("actor = %q, want the acting staff id %q", qty.ActorID, seedUserID)
	}
}

// TestIntegration_QuoteAudit_CausalOrder proves the history sorts by audit_log.seq (0078), so the rows
// of one edit and successive edits both read in the order they actually happened.
func TestIntegration_QuoteAudit_CausalOrder(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	svc, _, _ := intSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))

	for _, q := range []string{"4", "6"} {
		if _, err := svc.EditLineQty(ctx, quoteID, 1, q); err != nil {
			t.Fatalf("edit to %s: %v", q, err)
		}
	}
	hist, err := svc.QuoteEditHistory(ctx, quoteID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var qtySteps [][2]string
	for _, c := range hist.Changes {
		if c.Field == "qty_entered" {
			qtySteps = append(qtySteps, [2]string{c.Before, c.After})
		}
	}
	if len(qtySteps) != 2 {
		t.Fatalf("want 2 qty transitions, got %d (%v)", len(qtySteps), qtySteps)
	}
	if qtySteps[0] != [2]string{"2.0000", "4.0000"} || qtySteps[1] != [2]string{"4.0000", "6.0000"} {
		t.Errorf("cross-edit causal order wrong: %v", qtySteps)
	}
	// Within one edit, the emit order (qty → base → total) must be preserved.
	var seenQty, seenTotal int = -1, -1
	first := hist.Changes[0].RequestID
	for i, c := range hist.Changes {
		if c.RequestID != first {
			break
		}
		if c.Field == "qty_entered" && seenQty < 0 {
			seenQty = i
		}
		if c.Field == "total_cents" && seenTotal < 0 {
			seenTotal = i
		}
	}
	if seenQty < 0 || seenTotal < 0 || seenQty > seenTotal {
		t.Errorf("intra-edit order wrong: qty at %d, total at %d", seenQty, seenTotal)
	}
}

// - I3: the trail is atomic with the write (R3) -

// failAfterRealRecorder wraps the real audit service: RecordTx writes the row on the caller's tx (so a
// real QUOTE_CHANGED row genuinely lands INSIDE the edit's transaction) and THEN returns an error. That
// forces the guarded write to roll back, taking the just-written audit row with it. Asserting zero
// persisted rows afterwards is the strongest available proof the trail shares the mutation's
// transaction rather than being a separate best-effort write.
type failAfterRealRecorder struct{ real *audit.Service }

func (f failAfterRealRecorder) Record(ctx context.Context, a audit.SensitiveAccess) error {
	return f.real.Record(ctx, a)
}

func (f failAfterRealRecorder) RecordTx(ctx context.Context, q audit.Execer, a audit.SensitiveAccess) error {
	_ = f.real.RecordTx(ctx, q, a) // write the row on the tx…
	return errors.New("audit write failed after persisting on tx")
}

func TestIntegration_QuoteAudit_RecorderFailureRollsBack(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)

	repo := NewRepository(pool).withNow(func() time.Time { return now })
	aud := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewStaffService(repo).
		WithAudit(failAfterRealRecorder{real: aud}).
		WithPricing(qPricer{}).WithCostBasis(qInv{}).WithCustomerType(qCustType{}).
		withCaller(qCallerFrom).withNow(func() time.Time { return now })

	before := liveLine(t, pool, quoteID, 1)
	if _, err := svc.EditLineQty(qCtxAs(intStaff(intTrenton)), quoteID, 1, "5"); err == nil {
		t.Fatal("want the edit to fail when the recorder fails")
	}
	if got := liveLine(t, pool, quoteID, 1); got != before {
		t.Errorf("a recorder failure must roll the line back: %+v → %+v", before, got)
	}
	if n := countQuoteChanged(t, pool, quoteID); n != 0 {
		t.Errorf("a rolled-back edit left %d persisted QUOTE_CHANGED rows; want 0, the trail is not "+
			"sharing the mutation's transaction", n)
	}
}

// - I4: the guards run UNDER the row lock -

// TestIntegration_QuoteGuardRunsUnderLock proves the status/expiry re-check happens inside the
// transaction that holds the quote row, not before it.
//
// Technique: hold the quote row FOR UPDATE in an outer transaction and flip its status, then run the
// edit concurrently. The edit must BLOCK on the lock, and once the blocker commits the now-frozen
// status must be what it sees, proving it re-read under the lock rather than trusting the advisory
// read from authorizeQuoteEdit.
func TestIntegration_QuoteGuardRunsUnderLock(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	svc, _, _ := intSvc(t, pool, now)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	var st string
	if err := tx.QueryRow(ctx, `SELECT status FROM quotes WHERE id=$1 FOR UPDATE`, quoteID).Scan(&st); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE quotes SET status='ACCEPTED' WHERE id=$1`, quoteID); err != nil {
		t.Fatalf("blocker update: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, e := svc.EditLineQty(qCtxAs(intStaff(intTrenton)), quoteID, 1, "5")
		done <- e
	}()

	// Give the edit time to pass the (unlocked) advisory read and land on the row lock.
	time.Sleep(300 * time.Millisecond)
	select {
	case e := <-done:
		_ = tx.Rollback(ctx)
		t.Fatalf("the edit returned %v while the row was locked, it never took the lock", e)
	default:
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit blocker: %v", err)
	}

	select {
	case e := <-done:
		var ne *NotEditableError
		if !errors.As(e, &ne) {
			t.Fatalf("want *NotEditableError once the blocker committed ACCEPTED, got %T: %v", e, e)
		}
		if ne.Status != "ACCEPTED" {
			t.Errorf("want the refusal to name ACCEPTED (the status committed under the lock), got %q", ne.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the edit never returned after the blocker committed, deadlock")
	}
}

// - I5: expiry on real rows (D2) -

func TestIntegration_QuoteExpiry(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ctx := qCtxAs(intStaff(intTrenton))

	t.Run("NULL valid_until never expires", func(t *testing.T) {
		_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
		svc, _, _ := intSvc(t, pool, now)
		if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err != nil {
			t.Fatalf("D2a: a quote with no expiry must be editable on real rows, got: %v", err)
		}
	})
	t.Run("lapsed quote is refused naming the date", func(t *testing.T) {
		_, quoteID, _, _ := seedQuote(t, pool, "SENT", "2026-06-01")
		svc, _, _ := intSvc(t, pool, now)
		_, err := svc.EditLineQty(ctx, quoteID, 1, "5")
		var qe *QuoteExpiredError
		if !errors.As(err, &qe) {
			t.Fatalf("want *QuoteExpiredError, got %T: %v", err, err)
		}
		if !strings.Contains(qe.Error(), "2026-06-01") {
			t.Errorf("D2b: refusal must name the expiry date; got %q", qe.Error())
		}
		if n := countQuoteChanged(t, pool, quoteID); n != 0 {
			t.Errorf("an expired quote must not be written; got %d audit rows", n)
		}
	})
	t.Run("expires today is still editable", func(t *testing.T) {
		_, quoteID, _, _ := seedQuote(t, pool, "SENT", "2026-07-30")
		svc, _, _ := intSvc(t, pool, now)
		if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err != nil {
			t.Fatalf("a quote valid until today must be editable for the whole of today, got: %v", err)
		}
	})
	t.Run("read of an expired quote offers nothing but still succeeds", func(t *testing.T) {
		_, quoteID, _, _ := seedQuote(t, pool, "SENT", "2026-06-01")
		svc, _, _ := intSvc(t, pool, now)
		view, err := svc.StaffQuoteRead(ctx, quoteID)
		if err != nil {
			t.Fatalf("reading an expired quote must succeed: %v", err)
		}
		if !view.Expired {
			t.Error("want Expired=true")
		}
		if len(view.EditableFields) != 0 {
			t.Errorf("want no editable fields on an expired quote, got %v", view.EditableFields)
		}
	})
}

// - branch scoping + alternate key -

func TestIntegration_QuoteBranchScoping(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	svc, _, _ := intSvc(t, pool, now)

	var quoteNo string
	if err := pool.QueryRow(context.Background(),
		`SELECT quote_no::text FROM quotes WHERE id=$1`, quoteID).Scan(&quoteNo); err != nil {
		t.Fatalf("read quote_no: %v", err)
	}

	t.Run("in-branch by number resolves", func(t *testing.T) {
		if _, err := svc.StaffQuoteRead(qCtxAs(intStaff(intTrenton)), quoteNo); err != nil {
			t.Fatalf("in-branch #Q lookup must resolve: %v", err)
		}
	})
	t.Run("cross-branch by number is 404 not an oracle", func(t *testing.T) {
		_, err := svc.StaffQuoteRead(qCtxAs(intStaff(intKingston)), quoteNo)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("a cross-branch quote_no must answer 404 (no existence oracle), got %v", err)
		}
	})
	t.Run("cross-branch by uuid is 403", func(t *testing.T) {
		_, err := svc.StaffQuoteRead(qCtxAs(intStaff(intKingston)), quoteID)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("a cross-branch uuid deep-link must answer 403, got %v", err)
		}
	})
}

// - creation -

func TestIntegration_QuoteCreate(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	svc, _, _ := intSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))

	var cust string
	createTag := fmt.Sprintf("D56-create-%d", time.Now().UnixNano())
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO customers (account_no, name, customer_type) VALUES ($1,$2,'CONTRACTOR') RETURNING id::text`,
		createTag, createTag).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	view, err := svc.CreateQuote(ctx, QuoteCreate{
		CustomerID: cust, BranchID: intTrenton, ValidUntil: "2026-12-31",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1`, view.ID)
		_, _ = pool.Exec(c, `DELETE FROM quote_lines WHERE quote_id = $1`, view.ID)
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE id = $1`, view.ID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})

	if view.Status != "DRAFT" {
		t.Errorf("a new quote must open at DRAFT, got %q", view.Status)
	}
	// The house document-number convention: '#Q-<n>' from quote_no_seq, matching '#SO-' and '#PRICE-'.
	if !strings.HasPrefix(view.QuoteNo, "#Q-") {
		t.Errorf("quote_no = %q, want the '#Q-' house form", view.QuoteNo)
	}
	if view.ValidUntil != "2026-12-31" {
		t.Errorf("valid_until = %q, want 2026-12-31", view.ValidUntil)
	}
	if view.Expired {
		t.Error("a quote valid until 2026-12-31 is not expired on 2026-07-30")
	}
	// A fresh DRAFT must offer the full edit set.
	if len(view.EditableFields) != len(quoteFieldOrder) {
		t.Errorf("a DRAFT quote must offer every field; got %v", view.EditableFields)
	}
	t.Run("cross-branch creation is refused", func(t *testing.T) {
		_, err := svc.CreateQuote(qCtxAs(intStaff(intKingston)), QuoteCreate{
			CustomerID: cust, BranchID: intTrenton,
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("a quote must not be creatable in an unreachable branch, got %v", err)
		}
	})
}

// pgx is referenced so the import stays honest if the guard test above is edited.
var _ = pgx.ErrNoRows
