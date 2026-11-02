package gl

// export_period_p3_test.go, W3 AX-2 item 2: S7's P3s on 719 (item 0.18), the unit halves. An
// unknown account code in a mapping set is refused in words, not a 500 (P3-1); the GP-parity
// banner carries the quarter a pick narrowed to (P3-2's file header half); and the mapped draft
// body's printed totals are asserted, an unequal pair refusing the file (P3-4). The database
// halves live in export_qbo_legs_integration_test.go and the whole-year leg's own fix (P3-3) is in
// export_whole_year_integration_test.go.

import (
	"context"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestSetAccountMapping_AnUnknownHHAccountRefusesInWords is P3-1: the mapping points an HH GL
// account at a package's code, so an account code the chart does not hold is a refusal in words at
// the seam, never the storage layer's foreign-key 500.
func TestSetAccountMapping_AnUnknownHHAccountRefusesInWords(t *testing.T) {
	f := newFakeStore()
	f.accounts["1000"] = Account{Code: "1000", Name: "Cash Bank", AccountType: "ASSET", NormalBalance: NormalDebit, IsActive: true}
	svc := newTestServiceAs(f, identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1})

	_, err := svc.SetAccountMapping(context.Background(), TargetQuickBooksOnline, "9999", "9990")
	if err == nil {
		t.Fatal("an unknown account code took a mapping")
	}
	if !strings.Contains(err.Error(), "no HH account 9999") {
		t.Fatalf("the refusal must name the account in words, got %q", err.Error())
	}
	var br badRequestError
	if !errors.As(err, &br) {
		t.Fatalf("the refusal must be the 400 shape, got %T %q", err, err.Error())
	}

	// A code the chart holds still maps.
	if _, err := svc.SetAccountMapping(context.Background(), TargetQuickBooksOnline, "1000", "1010"); err != nil {
		t.Fatalf("a known account code must map: %v", err)
	}
}

// TestExport_TheGPParityBannerCarriesTheQuarter is P3-2's file header half: the draft's own banner
// names the pick it rendered, so a quarter's file says its quarter instead of a month 0 that names
// no month. (The batch row's own quarter word needs its column, which is a migration; AX-2 adds
// no migration, so that half stays with the lead.)
func TestExport_TheGPParityBannerCarriesTheQuarter(t *testing.T) {
	f := newFakeStore()
	f.exportRows = []ExportRow{
		{EntryNo: 41, PostedAt: "2096-04-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 11100, Memo: "q"},
		{EntryNo: 41, PostedAt: "2096-04-10", AccountCode: "4000", AccountName: "Sales", CreditCents: 11100, Memo: "q"},
	}
	svc := newTestServiceAs(f, identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1})

	res, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Quarter: 2})
	if err != nil {
		t.Fatalf("the quarter's GP-parity draft must export: %v", err)
	}
	if !strings.Contains(res.Content, "quarter=2") {
		t.Fatalf("the banner must name the quarter the pick narrowed to, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "fiscal_year=2096") {
		t.Fatalf("the banner must name the year, got:\n%s", res.Content)
	}
}
