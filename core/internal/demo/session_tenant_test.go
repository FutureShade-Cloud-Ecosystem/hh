//go:build demo

package demo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// fakeTenantMinter records the (subject, account) pairs it is asked to
// sign, and shares the event log with the audit fake.
type fakeTenantMinter struct {
	pairs  [][2]string
	token  string
	err    error
	events *[]string
}

func (f *fakeTenantMinter) MintTenant(subject, tenantID string) (string, error) {
	f.pairs = append(f.pairs, [2]string{subject, tenantID})
	if f.events != nil {
		*f.events = append(*f.events, "mint-tenant")
	}
	if f.err != nil {
		return "", f.err
	}
	return f.token, nil
}

// fakePortal answers a fixed portal role, presence and error, and records
// every question's platform user id and tenant id.
type fakePortal struct {
	role  string
	ok    bool
	err   error
	calls [][2]string
}

func (f *fakePortal) PortalRole(_ context.Context, platformUserID, tenantID string) (string, bool, error) {
	f.calls = append(f.calls, [2]string{platformUserID, tenantID})
	return f.role, f.ok, f.err
}

// newTenantMint builds the module over fakes with the tenant seams wired
// and the portal answering the given role.
func newTenantMint(t *testing.T, role string, ok bool, perr error) (*Mint, *fakeTenantMinter, *fakeMinter, *fakePortal, *fakeStaff, *fakeAudit, *[]string) {
	t.Helper()
	var events []string
	staffMinter := &fakeMinter{token: "staff-token", events: &events}
	tenantMinter := &fakeTenantMinter{token: "tenant-token", events: &events}
	portal := &fakePortal{role: role, ok: ok, err: perr}
	staff := &fakeStaff{auth: salesAuthority(), ok: true}
	aud := &fakeAudit{events: &events}
	m, err := NewMint(MintDeps{
		Minter: staffMinter, TenantMinter: tenantMinter, Portal: portal,
		Orgs: &fakeOrgs{id: "org-demo-1", ok: true}, Staff: staff, Audit: aud,
	})
	if err != nil {
		t.Fatalf("NewMint: %v", err)
	}
	return m, tenantMinter, staffMinter, portal, staff, aud, &events
}

// The five contractor flows each mint a tenant class token for their own
// portal user and the one Summit Ridge Builders account, after the live
// portal grant is asked, with the audit row before the token and nothing
// from the staff resolver or the staff minter.
func TestTenantSessionMintsATenantTokenAfterTheLiveGrant(t *testing.T) {
	for _, a := range flows.All() {
		if a.Class() != flows.ClassTenant {
			continue
		}
		t.Run(a.Flow, func(t *testing.T) {
			m, tm, sm, portal, staff, aud, events := newTenantMint(t, a.Role, true, nil)
			rsp := postSession(t, m, `{"flow":"`+a.Flow+`"}`)
			if rsp.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 (body: %s)", rsp.Code, rsp.Body.String())
			}
			if len(tm.pairs) != 1 || tm.pairs[0] != [2]string{a.PlatformUserID, a.Tenant} {
				t.Fatalf("tenant minter asked for %v, want the flow's portal user and account", tm.pairs)
			}
			if len(sm.subjects) != 0 || len(staff.calls) != 0 {
				t.Fatalf("a contractor flow touched the staff path: minted %v, resolved %v", sm.subjects, staff.calls)
			}
			if len(portal.calls) != 1 || portal.calls[0] != [2]string{a.PlatformUserID, a.Tenant} {
				t.Fatalf("portal asked %v, want one question for the flow's user in its account", portal.calls)
			}
			if strings.Join(*events, ",") != "audit,mint-tenant" {
				t.Fatalf("event order %v, want the audit row before the token", *events)
			}
			if len(aud.rows) != 1 || aud.rows[0].ActorID != a.PlatformUserID {
				t.Fatalf("audit rows %+v", aud.rows)
			}
			var got map[string]any
			if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil {
				t.Fatalf("body does not decode: %v", err)
			}
			if got["token"] != "tenant-token" || got["flow"] != a.Flow || got["class"] != "tenant" {
				t.Fatalf("response = %v", got)
			}
			user, _ := got["user"].(map[string]any)
			if user["user_id"] != a.PlatformUserID || user["role_code"] != a.Role || user["role_name"] != a.Label || user["email"] != a.Email {
				t.Fatalf("user block = %v", user)
			}
			if _, has := user["tier"]; has {
				t.Fatalf("a contractor user block carries a tier: %v", user)
			}
			if _, has := user["scope"]; has {
				t.Fatalf("a contractor user block carries a staff scope: %v", user)
			}
		})
	}
}

// A staff session answers class staff, and the listing carries the class.
func TestStaffSessionAndListingCarryTheClass(t *testing.T) {
	m, _, _ := newTestMint(t, &fakeMinter{token: "t"}, nil)
	rsp := postSession(t, m, `{"flow":"sales"}`)
	var got map[string]any
	if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil || got["class"] != "staff" {
		t.Fatalf("staff response = %v, %v", got, err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/demo/flows", nil)
	lr := httptest.NewRecorder()
	m.Flows(lr, req)
	var list flowList
	if err := json.Unmarshal(lr.Body.Bytes(), &list); err != nil {
		t.Fatalf("listing: %v", err)
	}
	all := flows.All()
	for i, row := range list.Flows {
		if row.Class != all[i].Class() {
			t.Fatalf("row %d class = %q, want %q", i, row.Class, all[i].Class())
		}
	}
	if list.Flows[0].Class != "staff" || list.Flows[len(list.Flows)-1].Class != "tenant" {
		t.Fatalf("classes at the ends: %q %q", list.Flows[0].Class, list.Flows[len(list.Flows)-1].Class)
	}
}

// Every refusal edge of a tenant mint: no token, no audit row where the
// check precedes the row, and the portal's live word is the decider.
func TestTenantSessionRefusals(t *testing.T) {
	cases := []struct {
		name   string
		role   string
		ok     bool
		perr   error
		status int
		word   string
	}{
		{"no portal grant", "", false, nil, http.StatusForbidden, "no portal grant"},
		{"another role than the flow's", "account_admin", true, nil, http.StatusForbidden, "holds account_admin"},
		{"inactive portal user", "", false, identity.ErrPortalUserInactive, http.StatusForbidden, "not active"},
		{"portal read fails", "", false, errors.New("db down"), http.StatusInternalServerError, "could not be resolved"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, tm, _, _, _, aud, _ := newTenantMint(t, c.role, c.ok, c.perr)
			rsp := postSession(t, m, `{"flow":"contractor-buyer"}`)
			if rsp.Code != c.status {
				t.Fatalf("status %d, want %d (body %s)", rsp.Code, c.status, rsp.Body.String())
			}
			if !strings.Contains(rsp.Body.String(), c.word) {
				t.Fatalf("body %q does not name %q", rsp.Body.String(), c.word)
			}
			if len(tm.pairs) != 0 || len(aud.rows) != 0 {
				t.Fatalf("a refused mint left a token request %v or audit rows %v", tm.pairs, aud.rows)
			}
			if strings.Contains(rsp.Body.String(), "tenant-token") {
				t.Fatal("a token was served on a refusal")
			}
		})
	}
}

// A tenant flow on a module wired without the tenant seams fails closed
// and mints nothing, and a table flow is the only thing a request can name.
func TestTenantSessionFailsClosedWhenUnwired(t *testing.T) {
	m, _, _ := newTestMint(t, &fakeMinter{token: "t"}, nil)
	rsp := postSession(t, m, `{"flow":"contractor-buyer"}`)
	if rsp.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 when the tenant seams are not wired", rsp.Code)
	}
	if strings.Contains(rsp.Body.String(), `"token"`) {
		t.Fatal("a token was served by an unwired tenant mint")
	}
}

// The tenant token mint failing, or the audit row failing, serves no token.
func TestTenantSessionMintAndAuditFailures(t *testing.T) {
	m, tm, _, _, _, _, _ := newTenantMint(t, "buyer", true, nil)
	tm.err = errors.New("sign failed")
	if rsp := postSession(t, m, `{"flow":"contractor-buyer"}`); rsp.Code != http.StatusInternalServerError || strings.Contains(rsp.Body.String(), "tenant-token") {
		t.Fatalf("mint failure: status %d body %s", rsp.Code, rsp.Body.String())
	}
	m2, tm2, _, _, _, aud2, _ := newTenantMint(t, "buyer", true, nil)
	aud2.err = errors.New("audit down")
	if rsp := postSession(t, m2, `{"flow":"contractor-buyer"}`); rsp.Code != http.StatusInternalServerError {
		t.Fatalf("audit failure: status %d", rsp.Code)
	}
	if len(tm2.pairs) != 0 {
		t.Fatal("a token was requested after the audit row failed")
	}
}

// A subject, role, tenant or class in the body is refused: the request
// carries a flow name and nothing else, for a contractor flow as for staff.
func TestTenantSessionTakesNoSubjectFromTheRequest(t *testing.T) {
	for _, body := range []string{
		`{"flow":"contractor-buyer","tenant":"x"}`,
		`{"flow":"contractor-buyer","sub":"x"}`,
		`{"flow":"contractor-buyer","role":"account_admin"}`,
		`{"flow":"contractor-buyer","email":"a@b.c"}`,
	} {
		m, tm, _, _, _, _, _ := newTenantMint(t, "buyer", true, nil)
		if rsp := postSession(t, m, body); rsp.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", body, rsp.Code)
		}
		if len(tm.pairs) != 0 {
			t.Fatalf("%s: a token was requested", body)
		}
	}
}
