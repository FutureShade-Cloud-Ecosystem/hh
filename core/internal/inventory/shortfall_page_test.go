package inventory

// shortfall_page_test.go, the shortfall read against its contract and the draft pointer on its rows.
//
// The contract (InventoryShortfallPage) requires `items` and `on_order_degraded`; the read used to
// write the list under `shortfall`. The leg decodes the served body against the generated type with
// unknown fields refused, so a key the contract does not declare fails here, not in an app.

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

// fakeDraftReader is the purchasing side of the draft pointer seam.
type fakeDraftReader struct {
	rows        []DraftPO
	err         error
	gotBranch   string
	gotProducts []string
}

func (f *fakeDraftReader) OpenDraftsFor(_ context.Context, branchID string, productIDs []string) ([]DraftPO, error) {
	f.gotBranch, f.gotProducts = branchID, productIDs
	return f.rows, f.err
}

func TestShortfallRead_AnswersTheContractsPage(t *testing.T) {
	line := blu60Line()
	svc, _ := newWorklistSvc([]ShortfallRow{line})
	svc.WithOnOrderReader(&fakeOnOrder{})
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec := httptest.NewRecorder()
	h.shortfall(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inventory/shortfall?branch_id="+trt470, nil), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("the read answered %d: %s", rec.Code, rec.Body.String())
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &keys); err != nil {
		t.Fatalf("the body is not an object: %v", err)
	}
	for _, required := range []string{"items", "on_order_degraded"} {
		if _, ok := keys[required]; !ok {
			t.Errorf("the contract's required key %q is missing from the body %s", required, rec.Body.String())
		}
	}
	if _, ok := keys["shortfall"]; ok {
		t.Errorf("the list is still written under the old key `shortfall`: %s", rec.Body.String())
	}
	var page apigen.InventoryShortfallPage
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&page); err != nil {
		t.Fatalf("the body does not decode against the generated page type: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ProductId != blu60 {
		t.Errorf("the page carries %+v, want the one short row", page.Items)
	}
}

func TestShortfallRead_AnEmptyListIsAnEmptyArray(t *testing.T) {
	svc, _ := newWorklistSvc(nil)
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	h.shortfall(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inventory/shortfall", nil), guards.StaffCaller{})
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &keys)
	if got := string(keys["items"]); got != "[]" {
		t.Errorf("nothing short must read items [], got %q", got)
	}
}

func TestShortfallRows_CarryTheDraftThatHasNotBeenPlaced(t *testing.T) {
	line := blu60Line()
	svc, _ := newWorklistSvc([]ShortfallRow{line})
	svc.WithOnOrderReader(&fakeOnOrder{})
	drafts := &fakeDraftReader{rows: []DraftPO{{ProductID: blu60, BranchID: trt470, POID: "d1", PONo: "PO-TRT-142"}}}
	svc.WithDraftPOReader(drafts)

	wl, err := svc.ShortfallWorklist(context.Background(), trt470, 200)
	if err != nil {
		t.Fatalf("worklist: %v", err)
	}
	// A draft never covers a shortfall: the row stays, with the draft pointed at.
	if len(wl.Rows) != 1 {
		t.Fatalf("the draft took the row off the worklist: %+v", wl.Rows)
	}
	if got := wl.Rows[0]; got.DraftPOID != "d1" || got.DraftPONo != "PO-TRT-142" {
		t.Errorf("the row points at draft %q (%q), want d1 (PO-TRT-142)", got.DraftPOID, got.DraftPONo)
	}
	if drafts.gotBranch != trt470 || len(drafts.gotProducts) != 1 || drafts.gotProducts[0] != blu60 {
		t.Errorf("the draft read was asked for branch %q products %v", drafts.gotBranch, drafts.gotProducts)
	}

	b, _ := json.Marshal(wl.Rows[0])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["draft_po_id"] != "d1" || m["draft_po_no"] != "PO-TRT-142" {
		t.Errorf("the row serialises its draft as %v / %v, want draft_po_id and draft_po_no", m["draft_po_id"], m["draft_po_no"])
	}
}

func TestShortfallRows_WithNoDraftOmitTheFields(t *testing.T) {
	svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	svc.WithDraftPOReader(&fakeDraftReader{})
	wl, err := svc.ShortfallWorklist(context.Background(), trt470, 200)
	if err != nil || len(wl.Rows) != 1 {
		t.Fatalf("worklist: %d rows, err %v", len(wl.Rows), err)
	}
	b, _ := json.Marshal(wl.Rows[0])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["draft_po_id"]; ok {
		t.Errorf("a row with no draft carries draft_po_id: %s", b)
	}
	if _, ok := m["draft_po_no"]; ok {
		t.Errorf("a row with no draft carries draft_po_no: %s", b)
	}
}

func TestShortfallRows_AFailingDraftReadIsAnErrorNotASilentGap(t *testing.T) {
	svc, _ := newWorklistSvc([]ShortfallRow{blu60Line()})
	svc.WithDraftPOReader(&fakeDraftReader{err: errors.New("purchasing down")})
	if _, err := svc.ShortfallWorklist(context.Background(), trt470, 200); err == nil {
		t.Error("a draft read that failed was swallowed, the buyer would draft a second order for a row that already has one")
	}
}
