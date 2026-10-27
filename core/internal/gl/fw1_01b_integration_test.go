//go:build integration

// fw1_01b_integration_test.go, FW1-01b's red first legs on the real migrated
// and seeded database: a system journal takes its DOCUMENT's date, not the
// processing day. FW1-01 gave that to the invoice and the deposit builders;
// every other builder still dated its journal with the clock, so a document
// taken on the last day of a month and consumed after midnight lands in the
// NEXT month's books.
//
// The three legs are the ones the brief names: the receipt, a till session
// closed after midnight on its business day, and a return. Each plants a
// document dated in the last month of the year band and consumes it with the
// clock standing in the next month, so "the journal posts in the document's
// month" fails on the processing day and passes on the document's own date.
//
// ISOLATION. Fiscal year 2087, outside the seeded 2026 periods and every band
// the package's other integration tests use; ensurePeriods scrubs the year
// and its journals when the leg ends.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_01B ./internal/gl/
package gl

import (
	"context"
	"fmt"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1bYear is the fiscal year band these legs post in: outside the seeded 2026
// periods and outside every band the package's other integration tests use.
const fw1bYear = 2087

// fw1bLastDay is the last day of the month the documents are dated in, in the
// house zone's afternoon (18:00Z is 13:00 in America/Toronto in January).
var fw1bLastDay = time.Date(fw1bYear, 1, 31, 18, 0, 0, 0, time.UTC)

// fw1bAfterMidnight is the processing instant: the outbox row is picked up
// after midnight, so the clock stands in the NEXT month (05:00Z is 00:00 in
// America/Toronto).
var fw1bAfterMidnight = time.Date(fw1bYear, 2, 1, 5, 0, 0, 0, time.UTC)

// fw1bBranch is one seeded location: the payment's branch and the journal's branch.
func fw1bBranch(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM locations LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	return id
}

// fw1bUUID is a fresh identifier for the rows the legs plant: gl_journals
// .source_ref_id is a uuid column, so a session or return id must be one.
func fw1bUUID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatalf("mint an id: %v", err)
	}
	return id
}

// fw1bPlantPayment plants one RECEIVED payment row dated receivedAt: the
// receipt document the receipt leg posts from.
func fw1bPlantPayment(t *testing.T, pool *pgxpool.Pool, receivedAt time.Time) (paymentID, branchID string) {
	t.Helper()
	ctx := context.Background()
	branchID = fw1bBranch(t, pool)
	suffix := time.Now().UnixNano()
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, method, amount_cents, status, received_at, branch_id)
		 VALUES ($1, 'CASH', 4520, 'RECEIVED', $2, $3) RETURNING id::text`,
		fmt.Sprintf("FW1B-%d", suffix), receivedAt, branchID).Scan(&paymentID); err != nil {
		t.Fatalf("plant the payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1`, paymentID)
	})
	return paymentID, branchID
}

// seedPaymentReader mirrors the till's PaymentForGL seam (till/recon_seam.go):
// the payment's own finance facts, and now the payment's received_at, which is
// the receipt document's date.
type seedPaymentReader struct{ pool *pgxpool.Pool }

func (r seedPaymentReader) PaymentForGL(ctx context.Context, paymentID string) (PaymentFacts, error) {
	var f PaymentFacts
	err := r.pool.QueryRow(ctx,
		`SELECT branch_id::text, COALESCE(customer_id::text, ''), method, status, amount_cents, received_at
		 FROM payments WHERE id = $1`, paymentID,
	).Scan(&f.BranchID, &f.CustomerID, &f.Method, &f.Status, &f.AmountCents, &f.ReceivedAt)
	if err != nil {
		return PaymentFacts{}, err
	}
	return f, nil
}

// fw1bJournal reads a journal's period year and month and its posted_at.
func fw1bJournal(t *testing.T, pool *pgxpool.Pool, key string) (year, month int, postedAt *time.Time) {
	t.Helper()
	var y int
	var m int
	var posted *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT p.year, p.month, j.posted_at
		  FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
		 WHERE j.idempotency_key = $1`, key).Scan(&y, &m, &posted); err != nil {
		t.Fatalf("read the journal %q: %v", key, err)
	}
	return y, m, posted
}

// TestIntegration_FW1_01B_ReceiptTakesThePaymentsOwnDate is the receipt leg:
// a receipt taken on the last day of the month, in the store, consumed after
// midnight, must post in the month the receipt was taken, on the receipt's own
// date. Today the journal lands on the processing day, in the next month.
func TestIntegration_FW1_01B_ReceiptTakesThePaymentsOwnDate(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, fw1bYear)

	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, fw1bAfterMidnight).
		WithPaymentReader(seedPaymentReader{pool: pool})
	paymentID, _ := fw1bPlantPayment(t, pool, fw1bLastDay)

	if err := svc.consume(ctx, ev("payment.received", map[string]any{"payment_id": paymentID})); err != nil {
		t.Fatalf("the receipt consume: %v", err)
	}
	year, month, posted := fw1bJournal(t, pool, "payment.received:"+paymentID)
	if year != fw1bYear || month != 1 {
		t.Fatalf("the receipt journal posts in %d-%02d, want %d-01 (the month the receipt was taken)", year, month, fw1bYear)
	}
	if posted == nil || !posted.UTC().Equal(fw1bLastDay) {
		t.Fatalf("the receipt journal carries posted_at %v, want the payment's received_at %v", posted, fw1bLastDay)
	}
}

// TestIntegration_FW1_01B_TillSessionTakesItsBusinessDay is the till leg: a
// session whose business day is the last day of the month, closed after
// midnight, must post its over/short in the session's own business day. Today
// the journal lands on the processing day, in the next month.
func TestIntegration_FW1_01B_TillSessionTakesItsBusinessDay(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, fw1bYear)

	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, fw1bAfterMidnight)
	branch := fw1bBranch(t, pool)
	sessionID := fw1bUUID(t, pool)

	// The session's business day: the drawer's own trading day, the last day of
	// the month, opened in the store's afternoon.
	if err := svc.consume(ctx, ev("till.session.completed", map[string]any{
		"session_id":       sessionID,
		"branch_id":        branch,
		"over_short_cents": -500,
		"occurred_at":      fw1bLastDay,
	})); err != nil {
		t.Fatalf("the till session consume: %v", err)
	}
	year, month, posted := fw1bJournal(t, pool, "till.session.completed:"+sessionID)
	if year != fw1bYear || month != 1 {
		t.Fatalf("the till session journal posts in %d-%02d, want %d-01 (the session's business day)", year, month, fw1bYear)
	}
	if posted == nil || !posted.UTC().Equal(fw1bLastDay) {
		t.Fatalf("the till session journal carries posted_at %v, want the session's business day %v", posted, fw1bLastDay)
	}
}

// TestIntegration_FW1_01B_ReturnTakesTheReturnsOwnDate is the return leg: a
// return taken on the last day of the month, consumed after midnight, must post
// its refund in the month the return was taken. Today the journal lands on the
// processing day, in the next month.
func TestIntegration_FW1_01B_ReturnTakesTheReturnsOwnDate(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, fw1bYear)

	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, fw1bAfterMidnight)
	branch := fw1bBranch(t, pool)
	returnID := fw1bUUID(t, pool)

	if err := svc.consume(ctx, ev("pos.return.committed", map[string]any{
		"return_id":            returnID,
		"order_id":             fw1bUUID(t, pool),
		"invoice_id":           fw1bUUID(t, pool),
		"refund_method":        "CASH",
		"subtotal_cents":       4000,
		"tax_cents":            520,
		"total_cents":          4520,
		"fee_cents":            0,
		"applied_cents":        0,
		"invoice_offset_cents": 0,
		"branch_id":            branch,
		"occurred_at":          fw1bLastDay,
	})); err != nil {
		t.Fatalf("the return consume: %v", err)
	}
	year, month, posted := fw1bJournal(t, pool, "pos.return.committed:"+returnID)
	if year != fw1bYear || month != 1 {
		t.Fatalf("the return journal posts in %d-%02d, want %d-01 (the month the return was taken)", year, month, fw1bYear)
	}
	if posted == nil || !posted.UTC().Equal(fw1bLastDay) {
		t.Fatalf("the return journal carries posted_at %v, want the return's own date %v", posted, fw1bLastDay)
	}
}
