package inventory

// availability_test.go, CP-04 (US-RO-03 support): the read-only availability check.
// DB-free via the fakeStore, mirroring service_test.go.

import (
	"context"
	"testing"
)

func TestCheckAvailability(t *testing.T) {
	const (
		prod   = "prod-1"
		branch = "branch-1"
	)
	cases := []struct {
		name       string
		setup      func(f *fakeStore)
		qtyBase    float64
		wantOK     bool
		wantReason string
	}{
		{
			name: "active with stock is available",
			setup: func(f *fakeStore) {
				f.products[prod] = true
				f.onhand[key(prod, branch)] = 10
			},
			qtyBase: 4, wantOK: true, wantReason: "",
		},
		{
			name: "exact on-hand is available (epsilon-tolerant)",
			setup: func(f *fakeStore) {
				f.products[prod] = true
				f.onhand[key(prod, branch)] = 4
			},
			qtyBase: 4, wantOK: true, wantReason: "",
		},
		{
			name: "insufficient stock is OUT_OF_STOCK",
			setup: func(f *fakeStore) {
				f.products[prod] = true
				f.onhand[key(prod, branch)] = 3
			},
			qtyBase: 4, wantOK: false, wantReason: ReasonOutOfStock,
		},
		{
			name: "inactive product is DISCONTINUED even with stock",
			setup: func(f *fakeStore) {
				f.products[prod] = false
				f.onhand[key(prod, branch)] = 100
			},
			qtyBase: 1, wantOK: false, wantReason: ReasonDiscontinued,
		},
		{
			name:    "missing catalog row is DISCONTINUED",
			setup:   func(_ *fakeStore) {},
			qtyBase: 1, wantOK: false, wantReason: ReasonDiscontinued,
		},
		{
			name: "stock at another branch does not count",
			setup: func(f *fakeStore) {
				f.products[prod] = true
				f.onhand[key(prod, "branch-other")] = 100
			},
			qtyBase: 1, wantOK: false, wantReason: ReasonOutOfStock,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeStore()
			tc.setup(f)
			svc := NewService(NewRepository(nil)).withStore(f)
			ok, reason, err := svc.CheckAvailability(context.Background(), prod, branch, tc.qtyBase)
			if err != nil {
				t.Fatalf("CheckAvailability: %v", err)
			}
			if ok != tc.wantOK || reason != tc.wantReason {
				t.Fatalf("CheckAvailability = (%v, %q), want (%v, %q)", ok, reason, tc.wantOK, tc.wantReason)
			}
		})
	}
}

func TestCheckAvailability_Validation(t *testing.T) {
	svc := NewService(NewRepository(nil)).withStore(newFakeStore())
	if _, _, err := svc.CheckAvailability(context.Background(), "", "b", 1); err == nil {
		t.Fatal("empty product_id must error")
	}
	if _, _, err := svc.CheckAvailability(context.Background(), "p", "b", 0); err == nil {
		t.Fatal("qty_base 0 must error")
	}
}
