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
	"testing"
	"time"
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
