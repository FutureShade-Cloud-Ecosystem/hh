package pricing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - HH-419 round 1: THE OTHER PRICING READS THAT CARRY COST -
//
// The price change log (cost_cents rows), a batch's lines (before and after cost) and a vendor's rebate
// ledger (basis_cents on a cost basis program) are dealer-admin reads with no cost check. Each rides the
// same costAllowed the floor preview does: a caller off the cost and margin list gets the cost figures
// absent, never zero. The retail figures and the vendor's accrual are the work itself and still ride.

const (
	doorBatch  = "b7000000-0000-4000-9000-0000000000b1"
	doorVendor = "b7000000-0000-4000-9000-0000000000c1"
)

func asRole(req *http.Request, erpRole string) *http.Request {
	return req.WithContext(identity.ContextWithStaff(req.Context(), identity.StaffContext{UserID: "u-1", RoleCode: erpRole}))
}

func i64(v int64) *int64 { return &v }

func TestCostDoors_ChangeLogDropsCostRowsForACallerOffTheList(t *testing.T) {
	f := newFakeBatchStore()
	f.log = []ChangeLogRow{
		{ID: "l1", ProductID: hProduct, Field: "cost_cents", BeforeCents: i64(7000), AfterCents: i64(7400), CreatedAt: time.Now()},
		{ID: "l2", ProductID: hProduct, Field: "retail_cents", BeforeCents: i64(10000), AfterCents: i64(10500), CreatedAt: time.Now()},
	}
	h := NewHandler(svcWithBatch(f), nil).WithCostRoles(costList{"general_manager": true})
	for role, wantRows := range map[string]int{"HR": 1, "GENERAL_MANAGER": 2} {
		rec := httptest.NewRecorder()
		h.changeLog(rec, asRole(httptest.NewRequest(http.MethodGet, "/api/v1/pricing/change-log", nil), role), guards.StaffCaller{})
		var got []ChangeLogRow
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != wantRows {
			t.Fatalf("%s: want %d rows, got %d (%v): %s", role, wantRows, len(got), err, rec.Body.String())
		}
		if role == "HR" {
			if got[0].Field != "retail_cents" {
				t.Fatalf("a caller off the list keeps the retail row only: %s", rec.Body.String())
			}
			// FLAKE-10: the cost detail is read from the PARSED body. The raw hunt for the bare
			// digits "7400" read the row's created_at nanoseconds as the cost figure: the body
			// carries a clock, a clock spells "7400" in one run in 2,597, and this body carries
			// no cost row at all.
			if leaks := costDoorLeak(rec.Body.String(), "cost_cents", 7000, 7400); len(leaks) != 0 {
				t.Fatalf("a caller off the list keeps the retail row only: %v\n%s", leaks, rec.Body.String())
			}
		}
	}
}

func TestCostDoors_BatchLinesDropCostForACallerOffTheList(t *testing.T) {
	f := newFakeBatchStore()
	f.lines[doorBatch] = []PriceChangeLine{{
		ID: "n1", BatchID: doorBatch, ProductID: hProduct, SKU: "BLU-60",
		BeforeCostCents: i64(7000), AfterCostCents: i64(7400), BeforeRetailCents: i64(10000), AfterRetailCents: i64(10500),
	}}
	h := NewHandler(svcWithBatch(f), nil).WithCostRoles(costList{"general_manager": true})
	read := func(role string) PriceChangeLine {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/pricing/batches/"+doorBatch+"/lines", nil)
		req.SetPathValue("id", doorBatch)
		rec := httptest.NewRecorder()
		h.listBatchLines(rec, asRole(req, role), guards.StaffCaller{})
		var got []PriceChangeLine
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 {
			t.Fatalf("%s: %v %s", role, err, rec.Body.String())
		}
		return got[0]
	}
	hr := read("HR")
	if hr.BeforeCostCents != nil || hr.AfterCostCents != nil {
		t.Fatalf("a caller off the list receives no cost: %+v", hr)
	}
	if hr.BeforeRetailCents == nil || *hr.AfterRetailCents != 10500 {
		t.Fatalf("the retail figures still ride: %+v", hr)
	}
	if gm := read("GENERAL_MANAGER"); gm.BeforeCostCents == nil || *gm.AfterCostCents != 7400 {
		t.Fatalf("a caller on the list keeps cost: %+v", gm)
	}
}

func TestCostDoors_LedgerHoldsBasisOutOfCostBasisRowsForACallerOffTheList(t *testing.T) {
	rs := &fakeRebateStore{inserted: []RebateAccrual{
		{ID: "a1", VendorID: doorVendor, ProductID: hProduct, SourceType: "ORDER", BasisCents: 70000, AccrualCents: 3500, RateNote: "5.00% of cost", Basis: RebatePctOfCost, AccruedAt: time.Now()},
		{ID: "a2", VendorID: doorVendor, ProductID: hProduct, SourceType: "ORDER", BasisCents: 100000, AccrualCents: 2000, RateNote: "2.00% of sale", Basis: RebatePctOfSale, AccruedAt: time.Now()},
	}}
	h := NewHandler(rebateSvc(rs), nil).WithCostRoles(costList{"general_manager": true})
	read := func(role string) []map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/pricing/rebates/vendors/"+doorVendor+"/ledger", nil)
		req.SetPathValue("vendorId", doorVendor)
		rec := httptest.NewRecorder()
		h.vendorLedger(rec, asRole(req, role), guards.StaffCaller{})
		var got struct {
			Rows []map[string]any `json:"rows"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Rows) != 2 {
			t.Fatalf("%s: %v %s", role, err, rec.Body.String())
		}
		return got.Rows
	}
	byID := func(rows []map[string]any, id string) map[string]any {
		for _, r := range rows {
			if r["id"] == id {
				return r
			}
		}
		t.Fatalf("row %s missing", id)
		return nil
	}
	hr := read("HR")
	if _, present := byID(hr, "a1")["basis_cents"]; present {
		t.Fatalf("a cost basis row must not carry basis_cents for a caller off the list: %v", hr)
	}
	if byID(hr, "a2")["basis_cents"] != float64(100000) {
		t.Fatalf("a sale basis row is not cost and keeps its basis: %v", hr)
	}
	if byID(read("GENERAL_MANAGER"), "a1")["basis_cents"] != float64(70000) {
		t.Fatalf("a caller on the list keeps the basis")
	}
}

// - HH-419 round 2: THE RECOMPUTE PATH -
//
// On a percent of cost row, accrual_cents with a rate_note that states the percent gives the cost back
// (accrual divided by the rate), and so does total_cents minus the other rows. For a caller off the cost
// list the cost basis row carries no accrual and no rate note, and the ledger total counts only the rows
// the caller sees. The vendor claim stays whole for a cost reader.
func TestCostDoors_LedgerHoldsAccrualAndRateOutOfCostBasisRowsAndTheTotal(t *testing.T) {
	rs := &fakeRebateStore{inserted: []RebateAccrual{
		{ID: "a1", VendorID: doorVendor, ProductID: hProduct, SourceType: "ORDER", BasisCents: 70000, AccrualCents: 3500, RateNote: "5.00% of cost", Basis: RebatePctOfCost, AccruedAt: time.Now()},
		{ID: "a2", VendorID: doorVendor, ProductID: hProduct, SourceType: "ORDER", BasisCents: 100000, AccrualCents: 2000, RateNote: "2.00% of sale", Basis: RebatePctOfSale, AccruedAt: time.Now()},
	}}
	h := NewHandler(rebateSvc(rs), nil).WithCostRoles(costList{"general_manager": true})
	read := func(role string) (map[string]map[string]any, map[string]any) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/pricing/rebates/vendors/"+doorVendor+"/ledger", nil)
		req.SetPathValue("vendorId", doorVendor)
		rec := httptest.NewRecorder()
		h.vendorLedger(rec, asRole(req, role), guards.StaffCaller{})
		var got struct {
			Rows []map[string]any `json:"rows"`
		}
		var whole map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Rows) != 2 {
			t.Fatalf("%s: %v %s", role, err, rec.Body.String())
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &whole)
		byID := map[string]map[string]any{}
		for _, r := range got.Rows {
			byID[r["id"].(string)] = r
		}
		return byID, whole
	}

	hr, whole := read("HR")
	cost := hr["a1"]
	if _, present := cost["accrual_cents"]; present {
		t.Fatalf("a cost basis row must not carry accrual_cents for a caller off the list: %v", cost)
	}
	if note, present := cost["rate_note"]; present && strings.Contains(note.(string), "%") {
		t.Fatalf("a cost basis row must not state the percent for a caller off the list: %v", cost)
	}
	if hr["a2"]["accrual_cents"] != float64(2000) || hr["a2"]["rate_note"] != "2.00% of sale" {
		t.Fatalf("a sale basis row keeps its accrual and note: %v", hr["a2"])
	}
	if whole["total_cents"] != float64(2000) {
		t.Fatalf("the total counts only the rows the caller sees (2000), got %v", whole["total_cents"])
	}

	gm, wholeGM := read("GENERAL_MANAGER")
	if gm["a1"]["accrual_cents"] != float64(3500) || gm["a1"]["rate_note"] != "5.00% of cost" || wholeGM["total_cents"] != float64(5500) {
		t.Fatalf("a cost reader keeps the whole claim: %v total=%v", gm["a1"], wholeGM["total_cents"])
	}
}
