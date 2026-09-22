package portal

// dib479_me_capabilities_test.go, GET /me carries the guard's OWN capability computation.
//
// The claim under test is not "the /me response has a capabilities object". It is that the object
// is the SAME VALUE the request guards used to decide this very request, not a parallel derivation
// that happens to agree today. The test therefore never writes an expected capability set by hand
// for the equality check: it captures what a guard sees, through a guard, and compares.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// capsSeenByAGuard runs a request through the SAME wrapper every real route is registered with
// (h.require) and returns the capability value the wrapper computed for the caller. This is the
// authority side of the comparison, whatever the gate believed, verbatim.
func capsSeenByAGuard(t *testing.T, h *Handler, token string) Capabilities {
	t.Helper()
	var seen Capabilities
	var reached bool
	probe := h.require(func(c Capabilities) bool {
		seen, reached = c, true
		return true // the probe gates on nothing; it exists only to observe
	}, func(http.ResponseWriter, *http.Request) {})

	r := httptest.NewRequest("GET", "/probe", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	probe(httptest.NewRecorder(), r)
	if !reached {
		t.Fatal("the guard never ran, the probe token did not authenticate")
	}
	return seen
}

// meReq drives GET /me with the PortalContext the bearer-split adapter
// resolves (the guards-swap carry note, HH-3.2 fold: the HS256 roundtrip
// the pin used is the superseded issuer world).
func meReq(svc *Service, store *fakeStore) *http.Request {
	caller, err := svc.ResolveTenantCaller(context.Background(), platformSub, "c1")
	if err != nil {
		panic("me fixture must resolve a caller: " + err.Error())
	}
	r := httptest.NewRequest("GET", "/api/portal/v1/me", nil)
	return r.WithContext(context.WithValue(r.Context(), callerKey, caller))
}

// TestMe_CapabilitiesEqualTheGuardsOwnComputation, for EVERY role the portal can issue, the
// capabilities served by GET /me are byte-for-byte the ones the guard resolved.
func TestMe_CapabilitiesEqualTheGuardsOwnComputation(t *testing.T) {
	roles := []string{
		string(RoleAccountAdmin), string(RoleBuyer),
		string(RoleBuyerNoPay), string(RoleAPFinance), string(RoleFieldCrew),
	}
	for _, role := range roles {
		store := newFakeStore()
		store.addUser(User{ID: "u1", CustomerID: "c1", Email: "me@acme.test", Name: "Sam",
			Role: role, Status: "active"})
		svc := NewService(store, testSecret)
		h := NewHandler(svc, nil)

		w := httptest.NewRecorder()
		h.getMe(w, meReq(svc, store))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: GET /me = %d (%s)", role, w.Code, w.Body.String())
		}

		var got MeResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: decode /me: %v", role, err)
		}
		// The guard's own computation is the identity package's carried
		// CapabilitiesFor over the CURRENT role.
		var want Capabilities = CapabilitiesFor(role)
		if got.Capabilities != want {
			t.Errorf("%s: /me capabilities %+v != the guard's own %+v", role, got.Capabilities, want)
		}
	}
}

// The capability set follows the CURRENT role in the database, not the role frozen in the token -
// the same H-4 property every guard has. A user demoted after their token was minted is told the
// truth by /me on their very next request.
func TestMe_CapabilitiesFollowTheCurrentRoleNotTheToken(t *testing.T) {
	store := newFakeStore()
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "me@acme.test",
		Role: string(RoleAccountAdmin), Status: "active"})
	svc := NewService(store, testSecret)
	h := NewHandler(svc, nil)

	// Demoted to field crew a moment later.
	u, _ := store.GetUser(t.Context(), "u1")
	u.Role = string(RoleFieldCrew)
	store.addUser(u)

	w := httptest.NewRecorder()
	h.getMe(w, meReq(svc, store))

	var got MeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Capabilities.ManageUsers || got.Capabilities.PayInvoices {
		t.Fatalf("a demoted admin must not still be told they may manage users or pay: %+v", got.Capabilities)
	}
	if got.Capabilities != CapabilitiesFor(string(RoleFieldCrew)) {
		t.Fatalf("capabilities must track the CURRENT role: %+v", got.Capabilities)
	}
}

// The extension is BACKWARD COMPATIBLE: every field the old GET /me returned is still at the top
// level of the object (the user struct is embedded, not nested), so the shipped portal keeps
// working unchanged.
func TestMe_StillCarriesTheUserFieldsAtTheTopLevel(t *testing.T) {
	store := newFakeStore()
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "me@acme.test", Name: "Sam",
		Role: string(RoleBuyer), Status: "active",
		NotificationPrefs: json.RawMessage(`{"email":true}`),
		CreatedAt:         time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC()})
	svc := NewService(store, testSecret)
	h := NewHandler(svc, nil)
	w := httptest.NewRecorder()
	h.getMe(w, meReq(svc, store))

	var flat map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &flat); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"id", "customer_id", "email", "name", "role", "status",
		"notification_prefs", "created_at", "updated_at", "capabilities"} {
		if _, ok := flat[key]; !ok {
			t.Errorf("GET /me lost the %q key, the capabilities extension must be additive", key)
		}
	}
	if _, leaked := flat["password_hash"]; leaked {
		t.Error("password_hash must never be serialized")
	}
}
