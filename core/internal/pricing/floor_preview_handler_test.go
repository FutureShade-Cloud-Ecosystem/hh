package pricing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const (
	hCat     = "ca700000-0000-4000-9000-000000000001"
	hProduct = "a7000000-0000-4000-9000-000000000001"
	hRule    = "b7000000-0000-4000-9000-000000000001"
)

func floorHandler(st *fakeStore) *Handler {
	st.listPrice = &ListPrice{ProductID: hProduct, ListPriceCents: 10000, PriceUOM: "SF"}
	svc := newSvc(st, hCat).WithCandidateSource(fakeCands{[]CandidateProduct{
		{ProductID: hProduct, CategoryID: hCat, SKU: "BLU-60", Name: "Blu 60", BaseUOM: "SF", CostCents: 7000}}})
	return NewHandler(svc, nil)
}

func TestFloorPreviewHandler_AnswersTheWireShape(t *testing.T) {
	h := floorHandler(&fakeStore{})
	body := `{"kind":"rule","rule":{"scope":"TIER_CATEGORY","customer_type":"CONTRACTOR","category_id":"` + hCat +
		`","calc_type":"PCT_OFF_LIST","discount_bps":4000,"price_uom":"SF","priority":10,"detail_text":"","version":0}}`
	rec := httptest.NewRecorder()
	h.floorPreview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/pricing/floor-preview", strings.NewReader(body)), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []struct {
			ProductID         string `json:"product_id"`
			SKU               string `json:"sku"`
			FinalCents        int64  `json:"final_cents"`
			AlreadyUnderFloor bool   `json:"already_under_floor"`
		} `json:"items"`
		Evaluated int  `json:"evaluated"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ProductID != hProduct || got.Items[0].SKU != "BLU-60" || got.Items[0].FinalCents != 6000 || got.Evaluated != 1 {
		t.Fatalf("wrong answer: %s", rec.Body.String())
	}
}

func TestFloorPreviewHandler_RefusesBadInput(t *testing.T) {
	h := floorHandler(&fakeStore{})
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"not json":          {`{`, http.StatusBadRequest},
		"unknown kind":      {`{"kind":"promo"}`, http.StatusBadRequest},
		"bad uuid":          {`{"kind":"rule","id":"nope","rule":{}}`, http.StatusBadRequest},
		"rule rule missing": {`{"kind":"rule"}`, http.StatusBadRequest},
		"floor over target": {`{"kind":"category_policy","category_id":"` + hCat + `","policy":{"margin_target_bps":1000,"margin_floor_bps":2000}}`, http.StatusUnprocessableEntity},
	} {
		rec := httptest.NewRecorder()
		h.floorPreview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/pricing/floor-preview", strings.NewReader(tc.body)), guards.StaffCaller{})
		if rec.Code != tc.want {
			t.Errorf("%s: want %d, got %d (%s)", name, tc.want, rec.Code, rec.Body.String())
		}
	}
}

func TestFloorConfirmationHandler_LogsTheActor(t *testing.T) {
	st := &fakeStore{byID: map[string]Rule{hRule: {ID: hRule}}}
	h := floorHandler(st)
	body := `{"entity_type":"pricing_rule","entity_id":"` + hRule + `","product_ids":["` + hProduct + `"],"ref_entity_type":"category","ref_entity_id":"` + hCat + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pricing/floor-confirmations", strings.NewReader(body))
	req = req.WithContext(identity.ContextWithStaffUserID(req.Context(), "ryan-mgr"))
	rec := httptest.NewRecorder()
	h.floorConfirmation(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(st.audits) != 1 || st.audits[0].UserID != "ryan-mgr" || st.audits[0].RefEntityType != "category" || st.audits[0].RefEntityID != hCat {
		t.Fatalf("the actor and the drill-through reference must reach the log row: %+v", st.audits)
	}
}

func TestFloorConfirmationHandler_RefusesWhatItCannotLog(t *testing.T) {
	st := &fakeStore{byID: map[string]Rule{hRule: {ID: hRule}}}
	h := floorHandler(st)
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"unknown record":  {`{"entity_type":"pricing_rule","entity_id":"` + "b7000000-0000-4000-9000-0000000000ff" + `","product_ids":["` + hProduct + `"]}`, http.StatusNotFound},
		"no products":     {`{"entity_type":"pricing_rule","entity_id":"` + hRule + `","product_ids":[]}`, http.StatusBadRequest},
		"no entity id":    {`{"entity_type":"pricing_rule","product_ids":["` + hProduct + `"]}`, http.StatusBadRequest},
		"bad product id":  {`{"entity_type":"pricing_rule","entity_id":"` + hRule + `","product_ids":["x"]}`, http.StatusBadRequest},
		"bad entity type": {`{"entity_type":"promotion","entity_id":"` + hRule + `","product_ids":["` + hProduct + `"]}`, http.StatusBadRequest},
	} {
		rec := httptest.NewRecorder()
		h.floorConfirmation(rec, httptest.NewRequest(http.MethodPost, "/api/v1/pricing/floor-confirmations", strings.NewReader(tc.body)), guards.StaffCaller{})
		if rec.Code != tc.want {
			t.Errorf("%s: want %d, got %d (%s)", name, tc.want, rec.Code, rec.Body.String())
		}
	}
	if len(st.audits) != 0 {
		t.Errorf("refusals write nothing: %+v", st.audits)
	}
}
