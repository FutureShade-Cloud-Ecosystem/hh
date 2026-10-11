//go:build integration

// sale_lookup_like_integration_test.go, the past-sale search's text term against real Postgres.
//
// The term is bound, never interpolated, but a bound LIKE pattern is still a pattern: on main a
// caller's '%' and '_' act as wildcards inside it, and a '\' is consumed as the pattern's own
// escape character (Postgres's default), so a search for the literal text "Slab\2" matched
// "Slab2" and NOT "Slab\2". Each leg plants a customer whose NAME carries the term literally and
// a decoy customer whose name differs only in the character a wildcard would have stood in for,
// rings one cash sale per customer, and asks for the term: the search must find the literal sale,
// must not find the decoy's, and every row it returns must contain the term literally in one of
// the fields it matches (order number, invoice number or customer name).
//
//	Run: DATABASE_URL=... go test -tags integration ./internal/pos/ -run TestIntegration_LookupSales_TextIsLiteral -count=1
package pos

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// likeFixSale plants a customer with the given name and rings one cash sale for it, returning the
// order id. Customers carry a nonce account number so repeated runs do not collide; the sales are
// left in place, the package's existing posture for its fixture rings.
func likeFixSale(t *testing.T, pool *pgxpool.Pool, svc *Service, ctx context.Context,
	productID, branchID, sessionID, name string) string {
	t.Helper()
	var custID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'RETAIL', $3::uuid, 0) RETURNING id::text`,
		fmt.Sprintf("LKX%d", time.Now().UnixNano()), name, branchID).Scan(&custID); err != nil {
		t.Fatalf("seed customer %q: %v", name, err)
	}
	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, TillSessionID: sessionID, CustomerID: custID,
		SaleType: SaleCollectNow, Tender: TenderCash,
		Lines: []LineInput{{ProductID: productID, QtyEntered: 1, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("fixture sale for %q: %v", name, err)
	}
	return out.OrderID
}

// lookupTermIsLiteral runs one search and holds it to the literal-only contract: the sale whose
// customer name contains the term literally is found, the decoy's is not, and no returned row is
// free of the term in every field the search matches.
func lookupTermIsLiteral(t *testing.T, svc *Service, ctx context.Context, branchID, term, wantOrderID, decoyOrderID string) {
	t.Helper()
	got, err := svc.LookupSales(ctx, SaleLookupQuery{BranchID: branchID, Q: term, Limit: 100})
	if err != nil {
		t.Fatalf("lookup %q: %v", term, err)
	}
	var haveWant, haveDecoy bool
	for _, s := range got {
		if s.OrderID == wantOrderID {
			haveWant = true
		}
		if s.OrderID == decoyOrderID {
			haveDecoy = true
		}
		if !strings.Contains(s.OrderNo, term) && !strings.Contains(s.InvoiceNo, term) &&
			!strings.Contains(s.CustomerName, term) {
			t.Fatalf("search %q returned a row containing none of it: order=%s invoice=%s name=%q",
				term, s.OrderNo, s.InvoiceNo, s.CustomerName)
		}
	}
	if !haveWant {
		t.Fatalf("search %q did not find the sale whose customer name contains it literally", term)
	}
	if haveDecoy {
		t.Fatalf("search %q matched a row the term fits only as a wildcard, not as text", term)
	}
}

// TestIntegration_LookupSales_TextIsLiteral, the three metacharacter classes a bound pattern can
// misread: % and _ (wildcards on main) and the escape character itself.
func TestIntegration_LookupSales_TextIsLiteral(t *testing.T) {
	pool := intPool(t)
	svc := realPosSvc(t, pool, stubCharger{})
	productID, branchID := aPaver(t, pool)
	sessionID := openTill(t, pool, branchID)
	ctx := globalCtx(t, pool)

	pctLit := likeFixSale(t, pool, svc, ctx, productID, branchID, sessionID, "Half Price 50% Co")
	pctDec := likeFixSale(t, pool, svc, ctx, productID, branchID, sessionID, "Half Price 501 Co")
	undLit := likeFixSale(t, pool, svc, ctx, productID, branchID, sessionID, "Grade A_1 Co")
	undDec := likeFixSale(t, pool, svc, ctx, productID, branchID, sessionID, "Grade AB1 Co")
	bslLit := likeFixSale(t, pool, svc, ctx, productID, branchID, sessionID, `Slab\2 Co`)
	bslDec := likeFixSale(t, pool, svc, ctx, productID, branchID, sessionID, "Slab2 Co")

	t.Run("a percent sign in the term is text, not any-run", func(t *testing.T) {
		lookupTermIsLiteral(t, svc, ctx, branchID, "50%", pctLit, pctDec)
	})
	t.Run("an underscore in the term is text, not any-one", func(t *testing.T) {
		lookupTermIsLiteral(t, svc, ctx, branchID, "A_1", undLit, undDec)
	})
	t.Run("the escape character in the term is text too", func(t *testing.T) {
		lookupTermIsLiteral(t, svc, ctx, branchID, `Slab\2`, bslLit, bslDec)
	})
}
