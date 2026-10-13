package exchange

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// The tenant leg: a contractor signs in to FB Console, presents the same
// {jwt, org} with class "tenant", and gets a tenant class token whose
// `tenant` claim is the live portal row's own account.

const fxContractorUser = "console_contractor.1"

// tenantJWT is a Console JWT for the contractor account on a session whose
// provider is the given one.
func (f *fx) tenantJWT(provider string) string {
	f.console.sessions = sessionsJSON(sessionJSON("sess_t", f.now.Add(-10*time.Minute), provider))
	return f.jwt(fxContractorUser, "sess_t", f.now.Add(10*time.Minute),
		accountJSON(fxContractorUser, fxContractorEm, true, true))
}

func (f *fx) postClass(jwt, org, class string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"jwt": jwt, "org": org, "class": class})
	return f.postRaw(string(body), "198.51.100.7:5555")
}

func (f *fx) postTenant(jwt string) *httptest.ResponseRecorder {
	return f.postClass(jwt, fxOrgSlug, "tenant")
}

func tenantClaims(t *testing.T, tok string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTenantExchangeMintsATenantTokenBoundToTheLivePortalRow(t *testing.T) {
	f := newFx(t)
	rec := f.postTenant(f.tenantJWT("magic-url"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	a := decodeAnswer(t, rec)
	c := tenantClaims(t, a.Token)
	if c["aud"] != fxTenantAud || c["tenant"] != fxTenantID || c["sub"] != fxContractorUser {
		t.Fatalf("claims = %v", c)
	}
	if a.User.UserID != fxContractorUser || a.User.RoleCode != identity.PortalRoleBuyer || a.User.RoleName != "Buyer" || a.User.Scope != "TENANT" || a.User.Tier != 0 {
		t.Fatalf("user block = %+v", a.User)
	}
	if len(f.audit.rows) != 1 {
		t.Fatalf("audit rows = %d", len(f.audit.rows))
	}
	row := f.audit.rows[0]
	if row.ActorID != fxPortalRow || row.EntityID != fxPortalRow || row.Purpose != "TENANT_SIGN_IN" || row.Extra["actor_subject"] != fxContractorUser {
		t.Fatalf("audit row = %+v", row)
	}
	if got := strings.Join(f.order, ","); !strings.HasSuffix(got, "audit,mint") {
		t.Fatalf("order = %s, want the audit row before the token", got)
	}
	// The real verifier and gate: the token classifies as tenant, the org
	// comes from the tenant Team and the tenant from the claim.
	pub, _ := identity.NewSessionKeySet(&f.key.PublicKey, nil, nil)
	verifier, err := identity.NewVerifier(identity.Config{KeySource: pub, Issuer: fxIssuer, StaffAudience: fxAudience, TenantAudience: fxTenantAud})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := authz.NewGate(verifier, f.members, f.orgs)
	if err != nil {
		t.Fatal(err)
	}
	var scope authz.TenantScope
	probe := gate.TenantScoped(func(w http.ResponseWriter, r *http.Request, s authz.TenantScope) { scope = s })
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+a.Token)
	probe.ServeHTTP(httptest.NewRecorder(), req)
	if scope.TenantID != fxTenantID || scope.PlatformUserID != fxContractorUser || scope.OrgSlug != fxOrgSlug {
		t.Fatalf("scope = %+v", scope)
	}
}

func TestTenantExchangeWindowIsBoundedByTheTenantCeilingAndSignInAge(t *testing.T) {
	f := newFx(t)
	f.cfg.TokenTTL = 100 * time.Hour
	a := decodeAnswer(t, f.postTenant(f.tenantJWT("google")))
	iat, exp := tokenWindow(t, a.Token)
	if exp-iat != int64((12*time.Hour)/time.Second)-600 {
		// the Console session is ten minutes old, so the sign in age left is the ceiling less ten minutes
		t.Fatalf("window = %ds", exp-iat)
	}
	f = newFx(t)
	f.console.sessions = sessionsJSON(sessionJSON("sess_t", f.now.Add(-13*time.Hour), "google"))
	j := f.jwt(fxContractorUser, "sess_t", f.now.Add(10*time.Minute), accountJSON(fxContractorUser, fxContractorEm, true, true))
	f.assertRefusal(f.postTenant(j), http.StatusUnauthorized, bodyU)
}

func TestTenantClassIsRefusedWhenTheLegIsNotConfiguredOrNamedBadly(t *testing.T) {
	f := newFx(t)
	f.cfg.Tenant = nil
	f.assertRefusal(f.postTenant(f.tenantJWT("magic-url")), http.StatusForbidden, bodyF)
	if f.console.accountHit != 0 {
		t.Error("an unconfigured tenant leg reached the console")
	}

	g := newFx(t)
	for _, class := range []string{"Tenant", "machine", "staff2", " tenant"} {
		rec := g.postClass(g.tenantJWT("magic-url"), fxOrgSlug, class)
		g.assertRefusal(rec, http.StatusBadRequest, nil)
	}
	if g.console.accountHit != 0 {
		t.Error("a bad class reached the console")
	}
	// An explicit staff class is the staff exchange.
	g = newFx(t)
	if rec := g.postClass(g.goodJWT(), fxOrgSlug, "staff"); rec.Code != http.StatusOK {
		t.Fatalf("explicit staff: %d %s", rec.Code, rec.Body)
	}
}

func TestTenantExchangeIgnoresTheStaffAllowListsAndStaffTeams(t *testing.T) {
	f := newFx(t)
	// The contractor's domain is on no staff list and the staff Team read is
	// never consulted for the tenant class.
	f.members.set(false)
	if rec := f.postTenant(f.tenantJWT("magic-url")); rec.Code != http.StatusOK {
		t.Fatalf("tenant: %d %s", rec.Code, rec.Body)
	}
	if f.members.calls != 0 {
		t.Fatalf("staff membership read %d times", f.members.calls)
	}
}

func TestTenantStaffTeamAloneDoesNotOpenTheTenantLegAndTheReverse(t *testing.T) {
	f := newFx(t)
	f.members.tenantOrgs = nil
	f.assertRefusal(f.postTenant(f.tenantJWT("magic-url")), http.StatusForbidden, bodyF)

	g := newFx(t)
	g.members.set(false)
	g.assertRefusal(g.post(g.goodJWT(), fxOrgSlug), http.StatusForbidden, bodyF)
}

func TestTenantMembershipRefusals(t *testing.T) {
	cases := map[string]struct {
		orgs   []string
		err    error
		status int
		body   []byte
	}{
		"no tenant team":       {nil, nil, 403, bodyF},
		"another org":          {[]string{"other"}, nil, 403, bodyF},
		"two orgs":             {[]string{fxOrgSlug, "other"}, nil, 403, bodyF},
		"read failed":          {nil, errBoom, 503, bodyV},
		"requested org absent": {[]string{"zzz"}, nil, 403, bodyF},
	}
	for name, c := range cases {
		f := newFx(t)
		f.members.tenantOrgs, f.members.tenantErr = c.orgs, c.err
		f.assertRefusal(f.postTenant(f.tenantJWT("magic-url")), c.status, c.body)
		if f.portal.calls != 0 {
			t.Errorf("%s: the portal was read before membership passed", name)
		}
	}
}

func TestTenantOrgAnchorRefusals(t *testing.T) {
	f := newFx(t)
	f.orgs.ok = false
	f.assertRefusal(f.postTenant(f.tenantJWT("magic-url")), http.StatusForbidden, bodyF)
	g := newFx(t)
	g.orgs.err = errBoom
	g.assertRefusal(g.postTenant(g.tenantJWT("magic-url")), http.StatusServiceUnavailable, bodyV)
}

func TestTenantPortalRowRefusals(t *testing.T) {
	cases := map[string]struct {
		mut    func(p *fakePortal)
		status int
		body   []byte
	}{
		"no row":        {func(p *fakePortal) { p.ok = false }, 403, bodyF},
		"inactive row":  {func(p *fakePortal) { p.ok, p.err = false, identity.ErrPortalUserInactive }, 403, bodyF},
		"read failed":   {func(p *fakePortal) { p.ok, p.err = false, errBoom }, 503, bodyV},
		"email differs": {func(p *fakePortal) { p.link.Email = "someone.else@contractor.example" }, 403, bodyF},
		"no tenant":     {func(p *fakePortal) { p.link.TenantID = "" }, 403, bodyF},
		"no row id":     {func(p *fakePortal) { p.link.PortalUserID = "" }, 403, bodyF},
	}
	for name, c := range cases {
		f := newFx(t)
		c.mut(f.portal)
		rec := f.postTenant(f.tenantJWT("magic-url"))
		f.assertRefusal(rec, c.status, c.body)
		if t.Failed() {
			t.Fatalf("case %q", name)
		}
	}
	// A row email in another case is the same account.
	f := newFx(t)
	f.portal.link.Email = strings.ToUpper(fxContractorEm)
	if rec := f.postTenant(f.tenantJWT("magic-url")); rec.Code != http.StatusOK {
		t.Fatalf("case folded email: %d %s", rec.Code, rec.Body)
	}
}

func TestTenantAccountGatesStillApply(t *testing.T) {
	f := newFx(t)
	j := f.jwt(fxContractorUser, "sess_t", f.now.Add(10*time.Minute), accountJSON(fxContractorUser, fxContractorEm, false, true))
	f.assertRefusal(f.postTenant(j), http.StatusForbidden, bodyF)
	g := newFx(t)
	j = g.jwt(fxContractorUser, "sess_t", g.now.Add(10*time.Minute), accountJSON(fxContractorUser, fxContractorEm, true, false))
	g.assertRefusal(g.postTenant(j), http.StatusForbidden, bodyF)
	// Replay: the same Console JWT is spent once, across classes.
	h := newFx(t)
	j = h.tenantJWT("magic-url")
	if rec := h.postTenant(j); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	if rec := h.postTenant(j); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay: %d", rec.Code)
	}
}

func TestTenantSignInMethodsAreTheClosedTenantTable(t *testing.T) {
	for provider, want := range map[string]int{
		"magic-url": 200, "google": 200,
		"email": 403, "microsoft": 403, "": 403, "github": 403,
	} {
		f := newFx(t)
		// The dealer's staff set must not decide the tenant leg.
		f.methods.set = []string{"email_password"}
		rec := f.postTenant(f.tenantJWT(provider))
		if rec.Code != want {
			t.Errorf("provider %q: %d, want %d", provider, rec.Code, want)
		}
		if want == 403 && len(f.methods.calls) != 0 {
			t.Errorf("provider %q read the dealer set", provider)
		}
	}
}

func TestTenantCodesAreDistinctAndListed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range AllCodes() {
		if seen[c] {
			t.Errorf("duplicate code %s", c)
		}
		seen[c] = true
	}
	for _, c := range []string{CodeClassBad, CodeTenantOff, CodeTenantNoRow, CodeTenantInactive, CodeTenantEmail, CodeTenantRowError} {
		if !seen[c] {
			t.Errorf("code %s not listed", c)
		}
	}
}

func TestNewRefusesAHalfConfiguredTenantLeg(t *testing.T) {
	base := newFx(t).cfg
	for name, leg := range map[string]*TenantLeg{
		"no portal": {Minter: base.Minter.(TenantMinter)},
		"no minter": {Portal: &fakePortal{}},
	} {
		c := base
		c.Tenant = leg
		if _, err := New(c); err == nil {
			t.Errorf("%s: constructed", name)
		}
	}
	_ = context.Background()
}
