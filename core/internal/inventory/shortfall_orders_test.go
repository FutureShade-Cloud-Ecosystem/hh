package inventory

// shortfall_orders_test.go, the sales order numbers on the shortfall row (change 5, slice S3).
//
// The row is short because somebody sold the stock, and until now the worklist never named who was
// waiting: a buyer reading "short by 40" could not see the order they were about to hold up. These
// tests pin the seam read onto the rows: the numbers of the open sales orders waiting on the
// product at the row's branch, oldest first, at most three of them, with the count of the rest
// beside them. DB-free, like the on-order and draft pointer legs; the owning module's SQL is
// proved against a live database in internal/order's integration legs.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// fakeShortOrders is the order module's side of the seam.
type fakeShortOrders struct {
	rows        []ShortOrder
	err         error
	gotBranch   string
	gotProducts []string
}

func (f *fakeShortOrders) OpenOrdersFor(_ context.Context, branchID string, productIDs []string) ([]ShortOrder, error) {
	f.gotBranch, f.gotProducts = branchID, productIDs
	return f.rows, f.err
}

// shortOrdersFor builds the seam's reply in its own order, oldest first: the reply is what the
// owning module's ORDER BY hands back, so the fixture keeps that order and the row must keep it too.
func shortOrdersFor(productID, branchID string, nos ...string) []ShortOrder {
	rows := make([]ShortOrder, 0, len(nos))
	for _, no := range nos {
		rows = append(rows, ShortOrder{ProductID: productID, BranchID: branchID, OrderID: no, OrderNo: no})
	}
	return rows
}

// TestShortfallRows_NamesTheOpenSalesOrdersTheyHoldUp is the slice's own question: four open sales
// orders waiting on the short product, oldest first, and the row names the oldest three plus a
// count of the rest.
func TestShortfallRows_NamesTheOpenSalesOrdersTheyHoldUp(t *testing.T) {
	svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	svc.WithOnOrderReader(&fakeOnOrder{})
	svc.WithShortOrderReader(&fakeShortOrders{rows: shortOrdersFor(blu60, trt470,
		"#SO-TRT-1001", "#SO-TRT-1002", "#SO-TRT-1003", "#SO-TRT-1004")})

	wl, err := svc.ShortfallWorklist(context.Background(), BranchReach{Global: true}, trt470, 200)
	if err != nil {
		t.Fatalf("worklist: %v", err)
	}
	if len(wl.Rows) != 1 {
		t.Fatalf("want the one short row, got %d", len(wl.Rows))
	}
	got := wl.Rows[0]
	if len(got.ShortOrderNos) != 3 ||
		got.ShortOrderNos[0] != "#SO-TRT-1001" ||
		got.ShortOrderNos[2] != "#SO-TRT-1003" {
		t.Errorf("short_order_nos = %v, want the oldest three in the seam's order", got.ShortOrderNos)
	}
	if got.ShortOrderMore != 1 {
		t.Errorf("short_order_more = %d, want 1 (the fourth order waits behind the three named)", got.ShortOrderMore)
	}
}

// TestShortfallRows_TheWindowAndTheCountAreHonestAtTheEdges, three named and none left over is
// zero, not one; two named is two, padded with nothing.
func TestShortfallRows_TheWindowAndTheCountAreHonestAtTheEdges(t *testing.T) {
	for _, tc := range []struct {
		nos  []string
		want []string
		more int
	}{
		{nos: []string{"#SO-1", "#SO-2"}, want: []string{"#SO-1", "#SO-2"}, more: 0},
		{nos: []string{"#SO-1", "#SO-2", "#SO-3"}, want: []string{"#SO-1", "#SO-2", "#SO-3"}, more: 0},
	} {
		svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
		svc.WithShortOrderReader(&fakeShortOrders{rows: shortOrdersFor(blu60, trt470, tc.nos...)})
		wl, err := svc.ShortfallWorklist(context.Background(), BranchReach{Global: true}, trt470, 200)
		if err != nil || len(wl.Rows) != 1 {
			t.Fatalf("worklist: %d rows, err %v", len(wl.Rows), err)
		}
		if got := wl.Rows[0]; len(got.ShortOrderNos) != len(tc.want) || got.ShortOrderMore != tc.more {
			t.Errorf("nos %v = %v (more %d), want %v (more %d)", tc.nos, got.ShortOrderNos, got.ShortOrderMore, tc.want, tc.more)
		}
	}
}

// TestShortfallRows_OrdersAreMatchedPerBranch, an order waiting at Kingston is not Trenton's
// shortfall, and a branch outside the caller's reach never lands on a row either.
func TestShortfallRows_OrdersAreMatchedPerBranch(t *testing.T) {
	kgn := "5d1b3f6e-0000-4000-9000-000000000002"
	trtLine, kgnLine := blu60Line(), blu60Line()
	kgnLine.BranchID, kgnLine.BranchCode = kgn, "KGN"
	svc, _ := newWorklistSvc([]ShortfallRow{trtLine, kgnLine})
	// The seam answers a cross-branch question with both yards' orders; only Kingston's line waits
	// there, and only Trenton's on the Trenton row.
	svc.WithShortOrderReader(&fakeShortOrders{rows: shortOrdersFor(blu60, trt470, "#SO-TRT-1001")})

	wl, err := svc.ShortfallWorklist(context.Background(), BranchReach{Global: true}, "", 200)
	if err != nil {
		t.Fatalf("worklist: %v", err)
	}
	for _, r := range wl.Rows {
		nos := r.ShortOrderNos
		if r.BranchID == trt470 && (len(nos) != 1 || nos[0] != "#SO-TRT-1001") {
			t.Errorf("the Trenton row names %v, want the Trenton order", nos)
		}
		if r.BranchID == kgn && len(nos) != 0 {
			t.Errorf("the Kingston row names %v with no Kingston order waiting", nos)
		}
	}
}

// TestShortfallRows_TheSeamIsAskedAboutTheBranchTheWorklistShows, the question carries the scope,
// and names the rows' products rather than every product in the catalogue.
func TestShortfallRows_TheSeamIsAskedAboutTheBranchTheWorklistShows(t *testing.T) {
	svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	seam := &fakeShortOrders{}
	svc.WithShortOrderReader(seam)

	if _, err := svc.ShortfallWorklist(context.Background(), BranchReach{Global: true}, trt470, 200); err != nil {
		t.Fatalf("worklist: %v", err)
	}
	if seam.gotBranch != trt470 {
		t.Fatalf("the seam was asked about branch %q, the worklist is showing %q", seam.gotBranch, trt470)
	}
	if len(seam.gotProducts) != 1 || seam.gotProducts[0] != blu60 {
		t.Errorf("the seam was asked about products %v, want the rows' products", seam.gotProducts)
	}

	if _, err := svc.ShortfallWorklist(context.Background(), BranchReach{Global: true}, "", 200); err != nil {
		t.Fatalf("worklist: %v", err)
	}
	if seam.gotBranch != "" {
		t.Fatalf("an all-branch worklist asked the seam about branch %q", seam.gotBranch)
	}
}

// TestShortfallRows_AnUnboundSeamOmitsTheFields, the honest unwired posture: the worklist is right
// without the numbers (they label the row, they do not grade it), so an unbound seam leaves the
// fields off rather than writing an empty list that reads as "nobody is waiting" on the strength of
// a measurement nobody made.
func TestShortfallRows_AnUnboundSeamOmitsTheFields(t *testing.T) {
	svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	wl, err := svc.ShortfallWorklist(context.Background(), BranchReach{Global: true}, trt470, 200)
	if err != nil || len(wl.Rows) != 1 {
		t.Fatalf("worklist: %d rows, err %v", len(wl.Rows), err)
	}
	b, _ := json.Marshal(wl.Rows[0])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"short_order_nos", "short_order_more"} {
		if _, ok := m[key]; ok {
			t.Errorf("no seam is bound and the row carries %s: %s", key, b)
		}
	}
}

// TestShortfallRows_ABoundSeamThatFailsIsAnError, the draft pointer's rule: a seam that answered
// "I am broken" must not be laundered into a silent nothing, which would read as "no order waits".
func TestShortfallRows_ABoundSeamThatFailsIsAnError(t *testing.T) {
	svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	svc.WithShortOrderReader(&fakeShortOrders{err: errors.New("orders: connection refused")})
	if _, err := svc.ShortfallWorklist(context.Background(), BranchReach{Global: true}, trt470, 200); err == nil {
		t.Error("a failing orders seam produced a worklist instead of an error")
	}
}

// TestShortfallRows_TheNumbersRideTheContractsRow serves the read through the handler and decodes
// the body against the generated contract type with unknown fields refused, so the numbers are
// contract fields, not a Go struct's private extra.
func TestShortfallRows_TheNumbersRideTheContractsRow(t *testing.T) {
	line := blu60Line()
	svc, _ := newWorklistSvc([]ShortfallRow{line})
	svc.WithOnOrderReader(&fakeOnOrder{})
	svc.WithShortOrderReader(&fakeShortOrders{rows: shortOrdersFor(blu60, trt470,
		"#SO-TRT-1001", "#SO-TRT-1002", "#SO-TRT-1003", "#SO-TRT-1004")})
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec := httptest.NewRecorder()
	h.shortfall(rec, globalShortfallReq("/api/v1/inventory/shortfall?branch_id="+trt470), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", rec.Code, rec.Body.String())
	}
	var page apigen.InventoryShortfallPage
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&page); err != nil {
		t.Fatalf("the body does not decode against the generated page type: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("the page carries %d rows, want one", len(page.Items))
	}
	row := page.Items[0]
	if row.ShortOrderNos == nil || len(*row.ShortOrderNos) != 3 || (*row.ShortOrderNos)[0] != "#SO-TRT-1001" {
		t.Errorf("the contract row names %v, want the oldest three", row.ShortOrderNos)
	}
	if row.ShortOrderMore == nil || *row.ShortOrderMore != 1 {
		t.Errorf("the contract row counts %v left over, want 1", row.ShortOrderMore)
	}

	// A row with nothing waiting and the seam unbound alike leave both fields byte-absent, the
	// draft pointer's optionality.
	svc2, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	svc2.WithShortOrderReader(&fakeShortOrders{})
	h2 := NewHandler(svc2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec2 := httptest.NewRecorder()
	h2.shortfall(rec2, globalShortfallReq("/api/v1/inventory/shortfall?branch_id="+trt470), guards.StaffCaller{})
	var raw map[string]any
	_ = json.Unmarshal(rec2.Body.Bytes(), &raw)
	items, _ := raw["items"].([]any)
	first, _ := items[0].(map[string]any)
	for _, key := range []string{"short_order_nos", "short_order_more"} {
		if _, ok := first[key]; ok {
			t.Errorf("nothing waits on the row and the body carries %s: %s", key, rec2.Body.String())
		}
	}
}
