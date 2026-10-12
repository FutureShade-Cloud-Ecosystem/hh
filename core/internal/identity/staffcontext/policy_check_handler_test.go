package staffcontext

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The policy check route (identityCheckPolicy) over the real Guards, the real stamp adapter and the
// fake store: below the floor with no approved override is the 403 with the block, an approved
// override clears it, a branch outside the caller's grants is refused, a bad body is a 400, and no
// bearer is the gate's refusal.
func TestPolicyCheck_Route(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	bearer := sg.Bearer(t)

	const linkedID = "11111111-1111-1111-1111-111111111111"
	link := func(_ context.Context, _ string) (string, bool) { return linkedID, true }
	f := &fakeStore{overrides: map[string]bool{}, unusedOverrides: map[string]bool{}}
	h := NewApprovalsHandler(NewApprovals(f), link)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	do := func(body string, withBearer bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/staff/policy/check", strings.NewReader(body))
		if withBearer {
			r.Header.Set("Authorization", "Bearer "+bearer)
			r.Header.Set("X-Org", testkit.OrgNorthSlug)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, r)
		return rr
	}

	// Any staff member may ask: a front-line caller granted the Trenton branch.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "branch", Branches: []string{trt}})

	// Above the floor: 200 allowed, margin 2000 bps, no override.
	rr := do(`{"price_cents":10000,"cost_cents":8000,"branch_id":"`+trt+`"}`, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("above floor = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}
	var ok struct {
		Allowed   bool  `json:"allowed"`
		MarginBps int64 `json:"margin_bps"`
		Override  bool  `json:"override"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &ok); err != nil || !ok.Allowed || ok.MarginBps != 2000 || ok.Override {
		t.Fatalf("above floor body = %s (err %v), want allowed, 2000 bps, no override", rr.Body.String(), err)
	}

	// Below the floor, no override: 403 with the block under the block key and a sentence in error.
	rr = do(`{"price_cents":10000,"cost_cents":9000,"branch_id":"`+trt+`"}`, true)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("below floor = %d, want 403 (body %s)", rr.Code, rr.Body.String())
	}
	var blocked struct {
		Error string `json:"error"`
		Block struct {
			PolicyType string            `json:"policy_type"`
			BranchID   string            `json:"branch_id"`
			Detail     string            `json:"detail"`
			Data       map[string]string `json:"data"`
		} `json:"block"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &blocked); err != nil {
		t.Fatalf("below floor body is not JSON: %v (%s)", err, rr.Body.String())
	}
	if blocked.Block.PolicyType != string(PolicyMargin) || blocked.Block.BranchID != trt || blocked.Block.Data["margin_bps"] != "1000" || blocked.Block.Data["floor_bps"] != "1200" {
		t.Fatalf("block = %+v, want the margin policy block for the branch with 1000 and 1200 bps", blocked.Block)
	}
	if blocked.Error == "" || blocked.Error != blocked.Block.Detail {
		t.Fatalf("error = %q, want the block's own sentence %q", blocked.Error, blocked.Block.Detail)
	}

	// An approved override for this caller and branch clears the block: 200 allowed with override true.
	// An approved override the line write already used up is not standing: the check counts only an unused one.
	f.overrides[linkedID+"|"+trt+"|"+string(PolicyMargin)] = true
	if rr = do(`{"price_cents":10000,"cost_cents":9000,"branch_id":"`+trt+`"}`, true); rr.Code != http.StatusForbidden {
		t.Fatalf("used up override = %d, want 403 (body %s)", rr.Code, rr.Body.String())
	}
	f.unusedOverrides[linkedID+"|"+trt+"|"+string(PolicyMargin)] = true
	rr = do(`{"price_cents":10000,"cost_cents":9000,"branch_id":"`+trt+`"}`, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("below floor with override = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &ok); err != nil || !ok.Allowed || !ok.Override || ok.MarginBps != 1000 {
		t.Fatalf("override body = %s (err %v), want allowed, override, 1000 bps", rr.Body.String(), err)
	}

	// A branch outside the caller's grants is refused, override or not.
	rr = do(`{"price_cents":10000,"cost_cents":8000,"branch_id":"`+kgn+`"}`, true)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("foreign branch = %d, want 403 (body %s)", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"block"`) {
		t.Fatalf("foreign branch carries a block: %s", rr.Body.String())
	}

	// A failed override lookup is the 503, never a guess either way.
	f.overrideErr = errors.New("lookup down")
	if rr = do(`{"price_cents":10000,"cost_cents":8000,"branch_id":"`+trt+`"}`, true); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("lookup failure = %d, want 503 (body %s)", rr.Code, rr.Body.String())
	}
	f.overrideErr = nil

	// A global caller reaches any branch.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "general_manager", Tier: 1, Scope: "global"})
	if rr = do(`{"price_cents":10000,"cost_cents":8000,"branch_id":"`+kgn+`"}`, true); rr.Code != http.StatusOK {
		t.Fatalf("global caller = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}

	// A bad body is a 400, and so is a missing branch (the contract requires it).
	for _, body := range []string{`not json`, `{"price_cents":-1,"cost_cents":1,"branch_id":"` + trt + `"}`, `{"price_cents":1,"cost_cents":9000000000000000,"branch_id":"` + trt + `"}`, `{"price_cents":1,"cost_cents":1}`, `{"price_cents":1,"cost_cents":1,"branch_id":""}`} {
		if rr = do(body, true); rr.Code != http.StatusBadRequest {
			t.Fatalf("body %q = %d, want 400 (body %s)", body, rr.Code, rr.Body.String())
		}
	}

	// No bearer: the gate's refusal, before the handler.
	if rr = do(`{"price_cents":1,"cost_cents":1,"branch_id":"`+trt+`"}`, false); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401 (body %s)", rr.Code, rr.Body.String())
	}
}
