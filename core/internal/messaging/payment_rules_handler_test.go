package messaging

import (
	"context"
	"encoding/json"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - DIB-462: the prefs endpoints -
//
// The route is staff-gated and SELF-SCOPED: the user id comes from the verified StaffContext, never
// from the path or the body. These tests hold that boundary, because a notification preference that
// one staffer can set for another is not a preference.

func ruleReq(t *testing.T, method, rule, body, asUser string) *http.Request {
	t.Helper()
	target := "/api/v1/messaging/payment-rules"
	var r *http.Request
	if method == http.MethodGet {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target+"/"+rule, strings.NewReader(body))
		r.SetPathValue("rule", rule)
	}
	return r.WithContext(identity.ContextWithStaff(r.Context(),
		identity.StaffContext{UserID: asUser, Status: "ACTIVE"}))
}

// The GET renders every rule, with the untouched ones false, the surface never has to invent the
// half it was not sent.
func TestHandler_GetPaymentRules_ReturnsTheWholeSet(t *testing.T) {
	f := newFakeStore()
	f.optIn(uAlice, RulePaymentCrossBranch)
	h := NewHandler(svcWith(f), nil)

	rr := httptest.NewRecorder()
	h.getPaymentRules(rr, ruleReq(t, http.MethodGet, "", "", uAlice), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var got PaymentRulePrefs
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Rules) != len(PaymentRules) {
		t.Fatalf("every rule must be present, got %v", got.Rules)
	}
	if !got.Rules[RulePaymentCrossBranch] || got.Rules[RulePaymentOnline] || got.Rules[RulePaymentOwnOrder] {
		t.Fatalf("only the stored ON may read as on, got %v", got.Rules)
	}
}

// The PUT round-trips and returns the persisted set.
func TestHandler_PutPaymentRule_TogglesAndEchoesWhatWasStored(t *testing.T) {
	f := newFakeStore()
	h := NewHandler(svcWith(f), nil)

	rr := httptest.NewRecorder()
	h.putPaymentRule(rr, ruleReq(t, http.MethodPut, RulePaymentOnline, `{"enabled":true}`, uAlice), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var got PaymentRulePrefs
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Rules[RulePaymentOnline] {
		t.Fatalf("the toggle must come back on, got %v", got.Rules)
	}

	off := httptest.NewRecorder()
	h.putPaymentRule(off, ruleReq(t, http.MethodPut, RulePaymentOnline, `{"enabled":false}`, uAlice), guards.StaffCaller{})
	if off.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", off.Code, off.Body.String())
	}
	if err := json.Unmarshal(off.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Rules[RulePaymentOnline] {
		t.Fatalf("turning it off must come back off, got %v", got.Rules)
	}
}

// An unknown rule id is a 400 at the boundary, never a Postgres CHECK violation surfacing as a 500.
func TestHandler_PutPaymentRule_UnknownRuleIs400(t *testing.T) {
	h := NewHandler(svcWith(newFakeStore()), nil)
	rr := httptest.NewRecorder()
	h.putPaymentRule(rr, ruleReq(t, http.MethodPut, "EVERYTHING", `{"enabled":true}`, uAlice), guards.StaffCaller{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("an unknown rule want 400, got %d (%s)", rr.Code, rr.Body.String())
	}
}

// No caller → 401, on both verbs. The self-scoping cannot be routed around by omitting identity.
func TestHandler_PaymentRules_RequireAnIdentifiedCaller(t *testing.T) {
	h := NewHandler(svcWith(newFakeStore()), nil)

	get := httptest.NewRecorder()
	h.getPaymentRules(get, httptest.NewRequest(http.MethodGet, "/api/v1/messaging/payment-rules", nil), guards.StaffCaller{})
	if get.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET want 401, got %d", get.Code)
	}

	put := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/messaging/payment-rules/ONLINE",
		strings.NewReader(`{"enabled":true}`))
	req.SetPathValue("rule", RulePaymentOnline)
	h.putPaymentRule(put, req, guards.StaffCaller{})
	if put.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous PUT want 401, got %d", put.Code)
	}
}

// One staffer's write cannot reach another's rules: the id is taken from the session, so a body that
// names somebody else changes nothing about who was written.
func TestHandler_PutPaymentRule_WritesOnlyTheCallersOwnRules(t *testing.T) {
	f := newFakeStore()
	h := NewHandler(svcWith(f), nil)

	rr := httptest.NewRecorder()
	h.putPaymentRule(rr, ruleReq(t, http.MethodPut, RulePaymentOwnOrder,
		`{"enabled":true,"user_id":"`+uBob+`"}`, uAlice), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	bob, err := f.PaymentRulesFor(context.Background(), uBob)
	if err != nil {
		t.Fatalf("read bob: %v", err)
	}
	if len(bob) != 0 {
		t.Fatalf("a body naming another user must not write their rules, got %v", bob)
	}
}
