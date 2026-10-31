package gl

// export_period_test.go, W3 AX-1 item 2's unit half: the period pick's resolution and the mapping
// refusal's words, against the fake store, no database. The database halves live in
// export_period_integration_test.go.

import (
	"context"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func TestPeriodMonths_ResolvesThePick(t *testing.T) {
	for _, c := range []struct {
		month, quarter int
		want           []int
	}{
		{month: 3, want: []int{3}},
		{month: 12, want: []int{12}},
		{quarter: 1, want: []int{1, 2, 3}},
		{quarter: 2, want: []int{4, 5, 6}},
		{quarter: 3, want: []int{7, 8, 9}},
		{quarter: 4, want: []int{10, 11, 12}},
		{month: 0, want: nil},  // the whole year
		{month: 13, want: nil}, // the bounds are the caller's to refuse; the resolver just narrows
	} {
		got := periodMonths(c.month, c.quarter)
		if len(got) != len(c.want) {
			t.Fatalf("periodMonths(%d, %d) = %v, want %v", c.month, c.quarter, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("periodMonths(%d, %d) = %v, want %v", c.month, c.quarter, got, c.want)
			}
		}
	}
}

func TestExport_AnUnmappedMovementRefusesInWords(t *testing.T) {
	f := newFakeStore()
	f.unmapped = []UnmappedAccount{
		{AccountCode: "1300", AccountName: "Prepaid Expenses"},
		{AccountCode: "2300", AccountName: "Customer Deposits"},
	}
	svc := newTestServiceAs(f, identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1})
	_, err := svc.Export(context.Background(), ExportQuery{Year: 2026, Month: 7, Target: TargetQuickBooksOnline})
	if err == nil {
		t.Fatal("an unmapped movement exported")
	}
	for _, word := range []string{"no mapping", "QuickBooks Online", "1300 Prepaid Expenses", "2300 Customer Deposits"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("the refusal must carry %q, got %q", word, err.Error())
		}
	}
}

func TestExport_ThePickBoundsRefuseInWords(t *testing.T) {
	svc := newTestServiceAs(newFakeStore(), identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1})
	for _, q := range []ExportQuery{
		{Year: 0, Month: 5},
		{Year: 2026, Month: 13},
		{Year: 2026, Month: -1},
		{Year: 2026, Quarter: 5},
		{Year: 2026, Month: 3, Quarter: 1},
		{Year: 2026, Month: 3, Target: "XERO"},
	} {
		if _, err := svc.Export(context.Background(), q); err == nil {
			t.Fatalf("the pick %+v took an export", q)
		}
	}
}
