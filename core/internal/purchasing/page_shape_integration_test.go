//go:build integration

// page_shape_integration_test.go: the purchasing list reads answer the contract's page envelope.
// The incoming read wrote its list under `lines` where PurchasingIncomingLinePage requires `items`,
// and the PO list and the receiving worklist wrote `items: null` when nothing matched, where the
// contract's page is an array. The legs read the served body the way a client does: the envelope
// decoded with unknown keys refused, so a key the contract does not declare fails here.
package purchasing

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// pageEnvelope is the contract's page: items an array, next_cursor a string or null, limit an int.
type pageEnvelope struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"next_cursor"`
	Limit      *int32            `json:"limit"`
}

func servedPage(t *testing.T, serve func(http.ResponseWriter, *http.Request, guards.StaffCaller), target string) (pageEnvelope, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	serve(rec, httptest.NewRequest(http.MethodGet, target, nil), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("%s answered %d: %s", target, rec.Code, rec.Body.String())
	}
	var page pageEnvelope
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&page); err != nil {
		t.Fatalf("%s does not decode against the contract's page: %v (body %s)", target, err, rec.Body.String())
	}
	if page.Items == nil {
		t.Errorf("%s answered items null, want an array (body %s)", target, rec.Body.String())
	}
	return page, rec.Body.String()
}

func TestIntegration_TheIncomingReadAnswersTheContractsPage(t *testing.T) {
	h := newHarness(t)
	hd := NewHandler(h.svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	prod := h.newProduct(t, "page-incoming")

	// Nothing on order: an empty page, not a missing key.
	page, _ := servedPage(t, hd.incoming, "/api/v1/purchasing/incoming?product_id="+prod+"&branch_id="+branchTRT)
	if len(page.Items) != 0 {
		t.Errorf("nothing is on order for the throwaway product, the page carries %d", len(page.Items))
	}

	po := h.orderedPO(t, prod, 12, 100)
	t.Cleanup(func() { h.cleanupPO(t, po.ID) })
	page, body := servedPage(t, hd.incoming, "/api/v1/purchasing/incoming?product_id="+prod+"&branch_id="+branchTRT)
	if len(page.Items) != 1 {
		t.Errorf("the placed order is one incoming line, the page carries %d (body %s)", len(page.Items), body)
	}
}

func TestIntegration_TheOrderListAndTheReceivingWorklistAnswerAnArrayWhenEmpty(t *testing.T) {
	h := newHarness(t)
	hd := NewHandler(h.svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// A branch no order sits at: nothing matches, and the contract's page is still an array.
	servedPage(t, hd.listPOs, "/api/v1/purchasing/pos?branch_id=00000000-0000-4000-8000-000000000999")
	servedPage(t, hd.worklist, "/api/v1/purchasing/receiving?branch_id=00000000-0000-4000-8000-000000000999")
}
