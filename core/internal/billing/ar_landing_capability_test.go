package billing

// ar_landing_capability_test.go, THE MUTATION TEST for the Pro-tier boundary.
//
// The rule this file exists to enforce is the house's oldest one: a gated figure is BYTE-ABSENT
// from the response, never CSS-hidden and never sent-and-hidden. So these tests do not ask the
// service politely whether a caller is allowed; they take the RESPONSE BODY and assert that the
// advanced payload's byte strings do not appear in it.
//
// The mutation is a real one, run twice over the same code path with one thing changed at a time:
//
//   1. same caller, dealer flipped PRO → BASE   (the plan boundary)
//   2. same PRO dealer, caller flipped manager → frontline  (the role boundary)
//
// Each mutation must flip the bytes. A test that only proved the PRO case would pass against a
// build that always serves the payload, and a test that only proved the BASE case would pass
// against a build that never serves it, so both directions are asserted every time.

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// advancedByteStrings are the strings that must not survive the gate. They are literal JSON keys
// rather than struct field names on purpose: what ships to a browser is bytes, and a rename that
// kept the data but changed the field name would be a different bug, not a fixed one.
var advancedByteStrings = []string{
	`"ar_advanced"`,
	`"engagement"`,
	`"next_action"`,
	`"delivered_never_opened"`,
	`"last_open_at"`,
	`"reminders_enabled"`,
}

func landingMux(f *fakeARStore) *http.ServeMux {
	mux := http.NewServeMux()
	// Guards-swap carry note (HH-3.2 fold): the pass-through guard stubs
	// became a direct adapter over the guarded handler signature. The route
	// guard is the coarse first line and is NOT what is under test - the
	// point is that the SERVICE withholds the payload from a caller the
	// route would have let in; the public track routes register beside it.
	landing := NewARLandingService(f).WithClock(func() time.Time { return arDay(2026, 8, 27) })
	reminders := NewARReminderService(f, landing, nil, "https://dibbits.example")
	h := NewARLandingHandler(landing, reminders, slog.Default())
	direct := func(fn func(http.ResponseWriter, *http.Request, guards.StaffCaller)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { fn(w, r, guards.StaffCaller{}) }
	}
	mux.HandleFunc("GET /api/v1/ar/landing", direct(h.landingRead))
	mux.HandleFunc("GET /api/v1/ar/landing/policy", direct(h.policyRead))
	mux.HandleFunc("PUT /api/v1/ar/landing/policy", direct(h.policyWrite))
	mux.HandleFunc("POST /api/v1/ar/reminders", direct(h.sendReminder))
	mux.HandleFunc("GET /api/public/v1/ar/track/{token}/open", h.trackOpen)
	mux.HandleFunc("GET /api/public/v1/ar/track/{token}/click", h.trackClick)
	return mux
}

func landingReq(tier identity.Tier, role string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ar/landing", nil)
	return r.WithContext(identity.ContextWithStaff(r.Context(), identity.StaffContext{
		UserID: "u1", RoleCode: role, Tier: tier, Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}))
}

func bookForCapability(asOf time.Time) []OpenInvoice {
	return []OpenInvoice{
		{InvoiceID: "i1", CustomerID: "c1", CustomerName: "Rood Landscaping", AccountNo: "A-1",
			BalanceCents: 900_00, DueDate: asOf.AddDate(0, 0, -120)},
	}
}

func capabilityStore(tier string) *fakeARStore {
	f := newFakeARStore()
	f.policy.Tier = tier
	f.invoices = bookForCapability(arDay(2026, 8, 27))
	// A last-open timestamp is present so the control case exercises the omitempty field too: an
	// absence assertion over a field that is never populated proves nothing.
	opened := arDay(2026, 7, 1)
	f.engage = map[string]EngagementInput{"c1": {
		CustomerID: "c1", APContactTagged: true, DeliveredNeverOpened: 4, LastOpenAt: &opened,
	}}
	return f
}

// TestProTierPayload_IsServedToAnEntitledManager is the control. Without it, every absence
// assertion below could be satisfied by a build that serves nothing to anybody.
func TestProTierPayload_IsServedToAnEntitledManager(t *testing.T) {
	w := httptest.NewRecorder()
	landingMux(capabilityStore(TierPro)).ServeHTTP(w, landingReq(identity.TierManager, "PAYABLES_RECEIVABLES"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d (%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, s := range advancedByteStrings {
		if !strings.Contains(body, s) {
			t.Fatalf("a PRO manager did not receive %s, the control case must pass or every absence below is vacuous", s)
		}
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	caps, _ := view["capabilities"].(map[string]any)
	if caps[CapARAdvanced] != true {
		t.Fatalf("capabilities = %v, want %s true", caps, CapARAdvanced)
	}
}

// TestBaseTierPayload_IsByteAbsent, MUTATION 1: the dealer's plan.
func TestBaseTierPayload_IsByteAbsent(t *testing.T) {
	w := httptest.NewRecorder()
	landingMux(capabilityStore(TierBase)).ServeHTTP(w, landingReq(identity.TierManager, "PAYABLES_RECEIVABLES"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d (%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, s := range advancedByteStrings {
		if strings.Contains(body, s) {
			t.Fatalf("a BASE-tier response carries %s, the payload was sent and the client was trusted to hide it.\n%s", s, body)
		}
	}
	// The basic half must still be there, or the gate has taken the product with it.
	for _, s := range []string{`"cards"`, `"escalation_queue"`, `"customer_name"`} {
		if !strings.Contains(body, s) {
			t.Fatalf("the BASE response lost %s, basic AR ships native and the gate must not eat it", s)
		}
	}
	var view map[string]any
	_ = json.Unmarshal([]byte(body), &view)
	caps, _ := view["capabilities"].(map[string]any)
	if caps[CapARAdvanced] != false {
		t.Fatalf("a BASE response must state the capability as false so ONE surface can render it, got %v", caps)
	}
}

// TestBaseTierRole_IsByteAbsent, MUTATION 2: the caller's authority, dealer left on PRO.
func TestBaseTierRole_IsByteAbsent(t *testing.T) {
	w := httptest.NewRecorder()
	landingMux(capabilityStore(TierPro)).ServeHTTP(w, landingReq(identity.TierFrontline, "INSIDE_SALES"))
	body := w.Body.String()
	for _, s := range advancedByteStrings {
		if strings.Contains(body, s) {
			t.Fatalf("a frontline caller on a PRO dealer received %s\n%s", s, body)
		}
	}
}

// TestAnonymousCallerGetsNothingAdvanced, fail closed. An unauthenticated context must never
// resolve the capability, whatever the dealer's plan says.
func TestAnonymousCallerGetsNothingAdvanced(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ar/landing", nil)
	landingMux(capabilityStore(TierPro)).ServeHTTP(w, r)
	for _, s := range advancedByteStrings {
		if strings.Contains(w.Body.String(), s) {
			t.Fatalf("an anonymous caller received %s", s)
		}
	}
}

// TestReminderSend_IsRefusedOnBaseTier: the gate is on the POWER, not only on the payload. A
// base-tier caller who hand-crafts the POST is refused, a gate that only omits fields from a GET
// gates the display and not the act.
func TestReminderSend_IsRefusedOnBaseTier(t *testing.T) {
	f := capabilityStore(TierBase)
	f.apContact = &APContact{CustomerID: "c1", Email: "ap@rood.example"}
	f.ctxRow = ReminderContext{CustomerID: "c1", InvoiceNo: "INV-1", DueDate: arDay(2026, 5, 1)}
	mux := landingMux(f)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/ar/reminders",
		strings.NewReader(`{"customer_id":"c1","channel":"EMAIL"}`))
	r = r.WithContext(identity.ContextWithStaff(r.Context(), identity.StaffContext{
		UserID: "u1", RoleCode: "PAYABLES_RECEIVABLES", Tier: identity.TierManager,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a BASE-tier send = %d (%s), want 403", w.Code, w.Body.String())
	}
	if len(f.sends) != 0 {
		t.Fatalf("%d rows were written by a refused send", len(f.sends))
	}
}

// TestPolicyWrite_RefusesCrossedEdgesThroughTheRoute, the 422-in-words posture reaches the wire.
func TestPolicyWrite_RefusesCrossedEdgesThroughTheRoute(t *testing.T) {
	mux := landingMux(capabilityStore(TierPro))
	r := httptest.NewRequest(http.MethodPut, "/api/v1/ar/landing/policy",
		strings.NewReader(`{"aging_warn_days":95,"aging_escalate_days":90}`))
	r = r.WithContext(identity.ContextWithStaff(r.Context(), identity.StaffContext{
		UserID: "u1", RoleCode: identity.RoleSuperAdmin, Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("crossed edges = %d (%s), want 422", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "95") {
		t.Fatalf("the refusal must quote what was typed: %s", w.Body.String())
	}
}

// TestTrackingEndpointsAreConstant: a public endpoint that answered differently for a known and an
// unknown token would be an oracle for enumerating sends.
func TestTrackingEndpointsAreConstant(t *testing.T) {
	f := capabilityStore(TierPro)
	f.apContact = &APContact{CustomerID: "c1", Email: "ap@rood.example"}
	f.ctxRow = ReminderContext{CustomerID: "c1", InvoiceNo: "INV-1", DueDate: arDay(2026, 5, 1)}
	mux := landingMux(f)

	known := httptest.NewRecorder()
	mux.ServeHTTP(known, httptest.NewRequest(http.MethodGet, "/api/public/v1/ar/track/aaaaaaaaaaaaaaaa/open", nil))
	unknown := httptest.NewRecorder()
	mux.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/api/public/v1/ar/track/bbbbbbbbbbbbbbbb/open", nil))

	if known.Code != unknown.Code || known.Body.String() != unknown.Body.String() {
		t.Fatal("the open pixel must be identical for a known and an unknown token")
	}
	if ct := known.Header().Get("Content-Type"); ct != "image/gif" {
		t.Fatalf("open pixel content-type = %q", ct)
	}
	click := httptest.NewRecorder()
	mux.ServeHTTP(click, httptest.NewRequest(http.MethodGet, "/api/public/v1/ar/track/cccccccccccccccc/click", nil))
	if click.Code != http.StatusNoContent {
		t.Fatalf("click = %d, want 204, this endpoint must never redirect (open-redirect)", click.Code)
	}
	if loc := click.Header().Get("Location"); loc != "" {
		t.Fatalf("the click endpoint set Location=%q, that is an open redirect on a trusted domain", loc)
	}
}
