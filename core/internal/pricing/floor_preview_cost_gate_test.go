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

// - HH-419: THE FLOOR PREVIEW SERVES MARGIN AND COST ONLY TO THE COST VISIBILITY LIST -
//
// The preview is the pricing editors' warning, gated by the editors list. A margin is a cost in
// disguise (final price and margin name the cost), so the figures that carry one ride only to a
// caller on the cost and margin list. The names, the unit prices and the under-the-floor flags are the
// warning itself and ride to every editor. Absent is not zero: an unlisted caller receives the margin
// legs byte-absent.

// costList is a CostRoles double: the canonical roles it names are on the list.
type costList map[string]bool

func (c costList) SeesCostAndMargin(role string) (bool, error) { return c[role], nil }

const floorRuleBody = `{"kind":"rule","rule":{"scope":"TIER_CATEGORY","customer_type":"CONTRACTOR","category_id":"` + hCat +
	`","calc_type":"PCT_OFF_LIST","discount_bps":4000,"price_uom":"SF","priority":10,"detail_text":"","version":0}}`

// previewAs runs the preview as the ERP role code (empty for an unstamped request).
func previewAs(t *testing.T, h *Handler, erpRole string) (map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pricing/floor-preview", strings.NewReader(floorRuleBody))
	if erpRole != "" {
		req = req.WithContext(identity.ContextWithStaff(req.Context(), identity.StaffContext{UserID: "u-1", RoleCode: erpRole}))
	}
	rec := httptest.NewRecorder()
	h.floorPreview(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Items) != 1 {
		t.Fatalf("one item expected: %v %s", err, rec.Body.String())
	}
	return got.Items[0], rec.Body.String()
}

var marginLegs = []string{"margin_bps", "floor_bps", "previous_margin_bps"}

// MUTATION TARGET: marshal the margin legs for every caller and this dies.
func TestFloorPreviewCostGate_CallerOffTheListGetsTheWarningWithoutTheMargin(t *testing.T) {
	h := floorHandler(&fakeStore{}).WithCostRoles(costList{"general_manager": true})
	item, body := previewAs(t, h, "SALES_MANAGER") // an editor, but not on this dealer's cost list
	for _, k := range marginLegs {
		if _, present := item[k]; present {
			t.Fatalf("%s must be byte-absent for a caller off the cost list: %s", k, body)
		}
	}
	if item["sku"] != "BLU-60" || item["final_cents"] != float64(6000) {
		t.Fatalf("the warning itself (who, at what price) still rides: %s", body)
	}
	if _, ok := item["already_under_floor"]; !ok {
		t.Fatalf("the under the floor flag is the warning and still rides: %s", body)
	}
	if strings.Contains(body, ":7000") {
		t.Fatalf("the cost figure must not appear anywhere in the answer: %s", body)
	}
}

func TestFloorPreviewCostGate_CallerOnTheListKeepsTheMargin(t *testing.T) {
	h := floorHandler(&fakeStore{}).WithCostRoles(costList{"general_manager": true})
	item, body := previewAs(t, h, "GENERAL_MANAGER")
	for _, k := range marginLegs {
		if _, present := item[k]; !present {
			t.Fatalf("%s must ride for a caller on the cost list: %s", k, body)
		}
	}
}

// Fail closed on every leg but a resolved yes.
func TestFloorPreviewCostGate_FailsClosed(t *testing.T) {
	for name, tc := range map[string]struct {
		h    *Handler
		role string
	}{
		"rule not wired":        {floorHandler(&fakeStore{}), "GENERAL_MANAGER"},
		"unstamped request":     {floorHandler(&fakeStore{}).WithCostRoles(costList{"general_manager": true}), ""},
		"role the bridge lacks": {floorHandler(&fakeStore{}).WithCostRoles(costList{"general_manager": true}), "NO_SUCH_ROLE"},
	} {
		item, body := previewAs(t, tc.h, tc.role)
		for _, k := range marginLegs {
			if _, present := item[k]; present {
				t.Errorf("%s: %s must be absent: %s", name, k, body)
			}
		}
	}
}
