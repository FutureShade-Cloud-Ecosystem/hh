//go:build integration

// write_integration_test.go, M5 / A-5. Proves the customer WRITE path validates + normalizes
// a Canadian billing address end-to-end against the real pgx repository: a valid lowercase/
// no-space postal persists canonicalized (K8V 5P7), a US ZIP / bad province is rejected 400,
// and the round-trip re-read confirms the stored value is canonical.
// Run: DATABASE_URL=… go test -tags integration ./internal/customer/... -run TestIntegration_CustomerWrite
package customer

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

func TestIntegration_CustomerWrite_NormalizesAndPersists(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	acctNo := "DIB-M5-" + randSuffix()
	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo:    acctNo,
		Name:         "M5 Localization Test Co",
		CustomerType: "CONTRACTOR",
		BillingAddress: map[string]any{
			"line1": "1 Yard Rd", "city": "Trenton", "province": "on", "postal": "k8v5p7",
		},
	})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, acct.CustomerID)
	})

	// Re-read the JSONB straight from the table: it must be canonical.
	var province, postal string
	if err := pool.QueryRow(ctx,
		`SELECT billing_address->>'province', billing_address->>'postal' FROM customers WHERE id = $1`,
		acct.CustomerID).Scan(&province, &postal); err != nil {
		t.Fatalf("re-read billing_address: %v", err)
	}
	if province != "ON" || postal != "K8V 5P7" {
		t.Fatalf("stored address not normalized: province=%q postal=%q", province, postal)
	}

	// Update to a valid QC address; it must re-normalize.
	if _, err := svc.UpdateBillingAddress(ctx, acct.CustomerID,
		map[string]any{"province": "qc", "postal": "h2x 1y4"}); err != nil {
		t.Fatalf("UpdateBillingAddress: %v", err)
	}
	_ = pool.QueryRow(ctx,
		`SELECT billing_address->>'province', billing_address->>'postal' FROM customers WHERE id = $1`,
		acct.CustomerID).Scan(&province, &postal)
	if province != "QC" || postal != "H2X 1Y4" {
		t.Fatalf("updated address not normalized: province=%q postal=%q", province, postal)
	}
}

func TestIntegration_CustomerWrite_RejectsBadAddress(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo)

	isBadReq := func(err error) bool {
		var bre httpx.BadRequestError
		return errors.As(err, &bre)
	}

	// A US ZIP must be rejected and nothing inserted.
	_, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo: "DIB-M5-BAD-" + randSuffix(), Name: "Bad ZIP Co",
		BillingAddress: map[string]any{"province": "ON", "postal": "90210"},
	})
	if err == nil || !isBadReq(err) {
		t.Fatalf("US ZIP should be rejected 400, got %v", err)
	}
	// A bad province must be rejected.
	_, err = svc.CreateCustomer(ctx, CreateInput{
		AccountNo: "DIB-M5-BAD2-" + randSuffix(), Name: "Bad Province Co",
		BillingAddress: map[string]any{"province": "NY", "postal": "K8V 5P7"},
	})
	if err == nil || !isBadReq(err) {
		t.Fatalf("province NY should be rejected 400, got %v", err)
	}
}

// randSuffix keeps parallel/re-run inserts from colliding on account_no (UNIQUE).
func randSuffix() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
