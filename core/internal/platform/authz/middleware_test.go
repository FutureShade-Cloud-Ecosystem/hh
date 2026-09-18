package authz

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// captureResponse is one recorded response.
type captureResponse struct {
	status int
	body   string
}

// capture runs one request through the handler and records the response.
func capture(h http.Handler, headers map[string]string) *captureResponse {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return &captureResponse{status: rec.Code, body: rec.Body.String()}
}

func mustContext() context.Context { return context.Background() }

// flipSource is a MembershipSource whose answers flip between calls
// without a clock of its own, for the D28 window test through the real
// cache.
type flipSource struct {
	member bool
	orgs   []string
}

func (s *flipSource) OrgMember(context.Context, string, string) (bool, error) {
	return s.member, nil
}

func (s *flipSource) TenantOrgs(context.Context, string) ([]string, error) {
	return s.orgs, nil
}

// TestStaffHappyPath proves the success leg end to end: verification,
// membership, slug resolution, stamped scope, handler reach, and the
// resolver running exactly once on the success path only.
func TestStaffHappyPath(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	h := f.gate.StaffScoped(func(w http.ResponseWriter, r *http.Request, scope StaffScope) {
		rec.mu.Lock()
		rec.reached++
		rec.staff = scope
		rec.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	})

	resp := capture(h, map[string]string{
		"Authorization": "Bearer " + f.staffToken(t, fixtureSubject),
		HeaderXOrg:      fixtureOrgSlug,
	})
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.status, resp.body)
	}
	reached, staff, tenant := rec.result()
	if reached != 1 {
		t.Fatalf("handler reached %d times", reached)
	}
	if staff.PlatformUserID != fixtureSubject || staff.OrgSlug != fixtureOrgSlug || staff.OrgID != fixtureOrgID {
		t.Fatalf("scope = %+v", staff)
	}
	if tenant != (TenantScope{}) {
		t.Fatalf("tenant scope stamped on a staff request: %+v", tenant)
	}
	memberCalls, orgsCalls := f.members.counts()
	if memberCalls != 1 || orgsCalls != 0 {
		t.Fatalf("membership calls = %d, tenant-orgs calls = %d", memberCalls, orgsCalls)
	}
	if f.orgs.count() != 1 {
		t.Fatalf("resolver ran %d times, want exactly once on the success path", f.orgs.count())
	}
}

// The anonymous and identity-failure variants, asserted identical: no
// token, a scheme defect, a garbage token, an empty bearer, an expired
// token and the alg-confusion token all answer 401 with byte-identical
// bodies, and none of them touches membership or the resolver.
func TestStaffIdentityFailuresIdentical(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	h := f.gate.Staff(rec.handler())

	good := f.staffToken(t, fixtureSubject)
	variants := []struct {
		name    string
		headers map[string]string
	}{
		{"no token", map[string]string{HeaderXOrg: fixtureOrgSlug}},
		{"scheme defect", map[string]string{"Authorization": "Basic " + good, HeaderXOrg: fixtureOrgSlug}},
		{"garbage token", map[string]string{"Authorization": "Bearer not.a.jwt", HeaderXOrg: fixtureOrgSlug}},
		{"empty bearer", map[string]string{"Authorization": "Bearer ", HeaderXOrg: fixtureOrgSlug}},
		{"expired token", map[string]string{"Authorization": "Bearer " + f.expiredStaffToken(t, fixtureSubject), HeaderXOrg: fixtureOrgSlug}},
		{"alg confusion", map[string]string{"Authorization": "Bearer " + f.confusionToken(t, fixtureSubject), HeaderXOrg: fixtureOrgSlug}},
	}

	var reference string
	for _, v := range variants {
		resp := capture(h, v.headers)
		if resp.status != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", v.name, resp.status)
		}
		if resp.body != string(BodyUnauthorized) {
			t.Fatalf("%s: body = %s", v.name, resp.body)
		}
		if strings.Contains(resp.body, good) {
			t.Fatalf("%s: body echoes token content", v.name)
		}
		if reference == "" {
			reference = resp.body
		} else if resp.body != reference {
			t.Fatalf("%s: 401 body differs across identity failures", v.name)
		}
		if reached, _, _ := rec.result(); reached != 0 {
			t.Fatalf("%s: handler reached", v.name)
		}
		if m, o := f.members.counts(); m != 0 || o != 0 {
			t.Fatalf("%s: membership touched (%d, %d)", v.name, m, o)
		}
		if f.orgs.count() != 0 {
			t.Fatalf("%s: resolver touched", v.name)
		}
	}
}

// G4 staff half (SW-6a), both directions of the conditional: no X-Org is
// refused 400 with zero platform and database calls, and the same bearer
// with the header present and correct is admitted.
func TestStaffRequiresXOrg(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	h := f.gate.Staff(rec.handler())
	auth := "Bearer " + f.staffToken(t, fixtureSubject)

	resp := capture(h, map[string]string{"Authorization": auth})
	if resp.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.status)
	}
	if resp.body != string(bodyMissingOrgHeader) {
		t.Fatalf("body = %s", resp.body)
	}
	if m, o := f.members.counts(); m != 0 || o != 0 {
		t.Fatalf("membership touched on a shape refusal (%d, %d)", m, o)
	}
	if f.orgs.count() != 0 {
		t.Fatalf("resolver touched on a shape refusal")
	}
	if reached, _, _ := rec.result(); reached != 0 {
		t.Fatalf("handler reached on a shape refusal")
	}

	ok := capture(h, map[string]string{"Authorization": auth, HeaderXOrg: fixtureOrgSlug})
	if ok.status != http.StatusOK {
		t.Fatalf("positive control: status = %d, body %s", ok.status, ok.body)
	}
}

// Shape refusals on the header itself: a malformed or empty slug answers
// 400 before any platform or database call, distinct from the org-refusal
// 403 family.
func TestStaffOrgHeaderShape(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	h := f.gate.Staff(rec.handler())
	auth := "Bearer " + f.staffToken(t, fixtureSubject)

	for name, slug := range map[string]string{
		"uppercase slug": "Not-A-Slug",
		"empty slug":     "",
		"trailing junk":  fixtureOrgSlug + "!",
	} {
		resp := capture(h, map[string]string{"Authorization": auth, HeaderXOrg: slug})
		if resp.status != http.StatusBadRequest {
			t.Fatalf("%s: status %d", name, resp.status)
		}
		if m, _ := f.members.counts(); m != 0 {
			t.Fatalf("%s: membership touched", name)
		}
		if f.orgs.count() != 0 {
			t.Fatalf("%s: resolver touched", name)
		}
		if reached, _, _ := rec.result(); reached != 0 {
			t.Fatalf("%s: handler reached", name)
		}
	}
}

// A repeated X-Org header is a shape failure on both route classes
// (ADR-0020 addendum: sent exactly once), refused before any platform or
// database call. capture cannot build it (Header.Set holds one value), so
// the request is built directly.
func TestRepeatedOrgHeaderRefused(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}

	staff := f.gate.Staff(rec.handler())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	req.Header.Add("Authorization", "Bearer "+f.staffToken(t, fixtureSubject))
	req.Header.Add(HeaderXOrg, fixtureOrgSlug)
	req.Header.Add(HeaderXOrg, fixtureOtherSlug)
	srec := httptest.NewRecorder()
	staff.ServeHTTP(srec, req)
	if srec.Code != http.StatusBadRequest {
		t.Fatalf("staff repeated header: status %d", srec.Code)
	}
	if got := srec.Body.String(); got != string(bodyRepeatedOrgHeader) {
		t.Fatalf("staff repeated header: body = %s", got)
	}
	if m, _ := f.members.counts(); m != 0 {
		t.Fatalf("staff repeated header: membership touched")
	}
	if f.orgs.count() != 0 {
		t.Fatalf("staff repeated header: resolver touched")
	}
	if reached, _, _ := rec.result(); reached != 0 {
		t.Fatalf("staff repeated header: handler reached")
	}

	tenant := f.gate.Tenant(rec.handler())
	req = httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	req.Header.Add("Authorization", "Bearer "+f.tenantToken(t, fixtureSubject, fixtureTenant))
	req.Header.Add(HeaderXOrg, fixtureOrgSlug)
	req.Header.Add(HeaderXOrg, fixtureOrgSlug)
	trec := httptest.NewRecorder()
	tenant.ServeHTTP(trec, req)
	if trec.Code != http.StatusBadRequest {
		t.Fatalf("tenant repeated header: status %d", trec.Code)
	}
	if got := trec.Body.String(); got != string(bodyOrgHeaderNotAllowed) {
		t.Fatalf("tenant repeated header: body = %s", got)
	}
	if m, o := f.members.counts(); m != 0 || o != 0 {
		t.Fatalf("tenant repeated header: platform touched (%d, %d)", m, o)
	}
	if f.orgs.count() != 0 {
		t.Fatalf("tenant repeated header: resolver touched")
	}
	if reached, _, _ := rec.result(); reached != 0 {
		t.Fatalf("tenant repeated header: handler reached")
	}
}

// The staff 403 legs and the byte-identity that kills the directory
// oracle: absent membership, unknown slug and a revoked membership past
// the cache window all answer with the same body (ADR-0020, refusal
// statuses).
func TestStaffOrgRefusalBodiesIdentical(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	h := f.gate.Staff(rec.handler())
	auth := "Bearer " + f.staffToken(t, fixtureSubject)

	// Leg 1: shape-valid slug, no membership.
	f.member = false
	wrongOrg := capture(h, map[string]string{"Authorization": auth, HeaderXOrg: fixtureOtherSlug})
	if wrongOrg.status != http.StatusForbidden {
		t.Fatalf("wrong org: status %d", wrongOrg.status)
	}
	if f.orgs.count() != 0 {
		t.Fatalf("resolver touched on a membership refusal")
	}

	// Leg 2: membership present, slug unknown to the anchor (the
	// provisioning-gap leg; it is the one refusal that reaches the
	// anchor read).
	f.member = true
	f.slugOK = map[string]string{}
	unknownSlug := capture(h, map[string]string{"Authorization": auth, HeaderXOrg: "ghostorg"})
	if unknownSlug.status != http.StatusForbidden {
		t.Fatalf("unknown slug: status %d", unknownSlug.status)
	}
	f.slugOK = map[string]string{fixtureOrgSlug: fixtureOrgID, fixtureOtherSlug: fixtureOtherID}

	// Leg 3: membership revoked on the platform, cache window passed.
	src := &flipSource{member: true}
	cached := newCachedMembership(src, time.Minute, 0, f.clock.Now)
	gate, err := NewGate(f.verifier, cached, f.orgs)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	revokedAuth := "Bearer " + f.staffToken(t, "platform-user-2")
	within := capture(gate.Staff(rec.handler()), map[string]string{
		"Authorization": revokedAuth, HeaderXOrg: fixtureOrgSlug,
	})
	if within.status != http.StatusOK {
		t.Fatalf("revoked membership refused inside the window: %d", within.status)
	}
	src.member = false
	f.clock.advance(2 * time.Minute)
	revoked := capture(gate.Staff(rec.handler()), map[string]string{
		"Authorization": revokedAuth, HeaderXOrg: fixtureOrgSlug,
	})
	if revoked.status != http.StatusForbidden {
		t.Fatalf("revoked membership admitted past the window: %d", revoked.status)
	}

	if wrongOrg.body != unknownSlug.body || wrongOrg.body != revoked.body {
		t.Fatalf("403 bodies differ across legs: %q %q %q", wrongOrg.body, unknownSlug.body, revoked.body)
	}
	if !bytes.Equal([]byte(wrongOrg.body), BodyForbidden) {
		t.Fatalf("403 body is not the pinned body: %s", wrongOrg.body)
	}
	if reached, _, _ := rec.result(); reached != 1 {
		t.Fatalf("handler reached %d times, want only the admitted in-window call", reached)
	}
}

// D28's cache window in isolation, both directions: a membership revoked
// between calls still admits within the window (bounded staleness), and
// refuses once the window has passed, with the refusal executing no
// resolver call.
func TestStaffRevokedMembershipWindow(t *testing.T) {
	f := newFixture(t)
	src := &flipSource{member: true}
	cached := newCachedMembership(src, time.Minute, 0, f.clock.Now)
	gate, err := NewGate(f.verifier, cached, f.orgs)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	rec := &recorder{}
	h := gate.Staff(rec.handler())
	headers := map[string]string{
		"Authorization": "Bearer " + f.staffToken(t, fixtureSubject),
		HeaderXOrg:      fixtureOrgSlug,
	}

	if resp := capture(h, headers); resp.status != http.StatusOK {
		t.Fatalf("first call: status %d", resp.status)
	}

	// Revoke on the platform. Within the window the cached answer is
	// stale and admits: the named, bounded, D28-justified staleness.
	src.member = false
	within := capture(h, headers)
	if within.status != http.StatusOK {
		t.Fatalf("within window: status %d, want admission (bounded staleness)", within.status)
	}

	// Past the window, per-request resolution sees the revocation.
	beforeRefusal := f.orgs.count()
	f.clock.advance(2 * time.Minute)
	past := capture(h, headers)
	if past.status != http.StatusForbidden {
		t.Fatalf("past window: status %d, want refusal", past.status)
	}
	if past.body != string(BodyForbidden) {
		t.Fatalf("past-window body = %s", past.body)
	}
	if f.orgs.count() != beforeRefusal {
		t.Fatalf("resolver ran on the refused request: %d then %d", beforeRefusal, f.orgs.count())
	}
	if reached, _, _ := rec.result(); reached != 2 {
		t.Fatalf("handler reached %d times, want the two admitted calls", reached)
	}
}

// Operational failures are explicit and fail closed: a membership source
// error and a resolver error answer 503, never a silent admission and
// never a 403 that would misreport an outage as a policy refusal.
func TestStaffOperationalFailures(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	auth := "Bearer " + f.staffToken(t, fixtureSubject)

	f.members.member = func(string, string) (bool, error) { return false, errors.New("platform down") }
	h := f.gate.Staff(rec.handler())
	resp := capture(h, map[string]string{"Authorization": auth, HeaderXOrg: fixtureOrgSlug})
	if resp.status != http.StatusServiceUnavailable {
		t.Fatalf("membership outage: status %d", resp.status)
	}

	f.members.member = func(string, string) (bool, error) { return true, nil }
	f.orgs.resolve = func(string) (string, bool, error) { return "", false, errors.New("anchor read failed") }
	resp = capture(h, map[string]string{"Authorization": auth, HeaderXOrg: fixtureOrgSlug})
	if resp.status != http.StatusServiceUnavailable {
		t.Fatalf("resolver outage: status %d", resp.status)
	}
	if reached, _, _ := rec.result(); reached != 0 {
		t.Fatalf("handler reached on an outage")
	}
}

// The tenant leg success: no X-Org, org from the Team behind the token,
// tenant from the token, scope stamped.
func TestTenantHappyPath(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	h := f.gate.TenantScoped(func(w http.ResponseWriter, r *http.Request, scope TenantScope) {
		rec.mu.Lock()
		rec.reached++
		rec.tenant = scope
		rec.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	})

	resp := capture(h, map[string]string{
		"Authorization": "Bearer " + f.tenantToken(t, fixtureSubject, fixtureTenant),
	})
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.status, resp.body)
	}
	reached, staff, tenant := rec.result()
	if reached != 1 {
		t.Fatalf("handler reached %d times", reached)
	}
	if staff != (StaffScope{}) {
		t.Fatalf("staff scope stamped on a tenant request: %+v", staff)
	}
	if tenant.OrgSlug != fixtureOrgSlug || tenant.OrgID != fixtureOrgID {
		t.Fatalf("org not resolved from the team: %+v", tenant)
	}
	if tenant.TenantID != fixtureTenant {
		t.Fatalf("tenant not stamped from the token: %+v", tenant)
	}
	if tenant.PlatformUserID != fixtureSubject {
		t.Fatalf("subject not carried: %+v", tenant)
	}
	memberCalls, orgsCalls := f.members.counts()
	if memberCalls != 0 || orgsCalls != 1 {
		t.Fatalf("membership calls = %d, tenant-orgs calls = %d", memberCalls, orgsCalls)
	}
}

// G4 tenant half (SW-6b), both sub-halves and the positive control: a
// tenant bearer carrying X-Org is refused 400 whether the header names
// the token's own org or a different one, and the same bearer without
// the header is admitted.
func TestTenantRejectsXOrg(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	h := f.gate.Tenant(rec.handler())
	auth := "Bearer " + f.tenantToken(t, fixtureSubject, fixtureTenant)

	for name, val := range map[string]string{
		"own org":     fixtureOrgSlug,
		"other org":   fixtureOtherSlug,
		"empty value": "",
	} {
		resp := capture(h, map[string]string{"Authorization": auth, HeaderXOrg: val})
		if resp.status != http.StatusBadRequest {
			t.Fatalf("%s: status %d", name, resp.status)
		}
		if resp.body != string(bodyOrgHeaderNotAllowed) {
			t.Fatalf("%s: body = %s", name, resp.body)
		}
		if val != "" && strings.Contains(resp.body, val) {
			t.Fatalf("%s: body echoes the header value", name)
		}
		if m, o := f.members.counts(); m != 0 || o != 0 {
			t.Fatalf("%s: platform touched before the shape check (%d, %d)", name, m, o)
		}
		if f.orgs.count() != 0 {
			t.Fatalf("%s: resolver touched", name)
		}
		if reached, _, _ := rec.result(); reached != 0 {
			t.Fatalf("%s: handler reached", name)
		}
	}

	ok := capture(h, map[string]string{"Authorization": auth})
	if ok.status != http.StatusOK {
		t.Fatalf("positive control: status %d, body %s", ok.status, ok.body)
	}
}

// Tenant org failures fail closed with the same body as the staff 403
// family: no tenant team, teams across two orgs, and a team org with no
// anchor row.
func TestTenantOrgFailuresClosed(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	h := f.gate.Tenant(rec.handler())
	auth := "Bearer " + f.tenantToken(t, fixtureSubject, fixtureTenant)

	f.orgList = nil
	noTeam := capture(h, map[string]string{"Authorization": auth})
	if noTeam.status != http.StatusForbidden || noTeam.body != string(BodyForbidden) {
		t.Fatalf("no team: status %d body %s", noTeam.status, noTeam.body)
	}

	f.orgList = []string{fixtureOrgSlug, fixtureOtherSlug}
	twoOrgs := capture(h, map[string]string{"Authorization": auth})
	if twoOrgs.status != http.StatusForbidden || twoOrgs.body != string(BodyForbidden) {
		t.Fatalf("two orgs: status %d body %s", twoOrgs.status, twoOrgs.body)
	}

	f.orgList = []string{"ghostorg"}
	ghost := capture(h, map[string]string{"Authorization": auth})
	if ghost.status != http.StatusForbidden || ghost.body != string(BodyForbidden) {
		t.Fatalf("ghost org: status %d body %s", ghost.status, ghost.body)
	}
	if reached, _, _ := rec.result(); reached != 0 {
		t.Fatalf("handler reached on an org failure")
	}
	if f.orgs.count() != 1 {
		t.Fatalf("resolver calls = %d, want exactly the ghost-org success-path leg", f.orgs.count())
	}
}

// SW-1b, both halves: a valid tenant bearer on a staff operation answers
// 403, and a valid staff bearer on a tenant operation answers 403;
// neither reaches the handler, neither is 401, and neither touches the
// platform or the resolver.
func TestCrossBearerRefused(t *testing.T) {
	f := newFixture(t)
	staffRec := &recorder{}
	tenantRec := &recorder{}
	staffRoute := f.gate.Staff(staffRec.handler())
	tenantRoute := f.gate.Tenant(tenantRec.handler())

	resp := capture(staffRoute, map[string]string{
		"Authorization": "Bearer " + f.tenantToken(t, fixtureSubject, fixtureTenant),
		HeaderXOrg:      fixtureOrgSlug,
	})
	if resp.status != http.StatusForbidden {
		t.Fatalf("tenant on staff route: status %d", resp.status)
	}
	if resp.body != string(BodyForbidden) {
		t.Fatalf("cross-bearer body = %s", resp.body)
	}

	resp = capture(tenantRoute, map[string]string{
		"Authorization": "Bearer " + f.staffToken(t, fixtureSubject),
	})
	if resp.status != http.StatusForbidden {
		t.Fatalf("staff on tenant route: status %d", resp.status)
	}

	if reached, _, _ := staffRec.result(); reached != 0 {
		t.Fatalf("staff handler reached")
	}
	if reached, _, _ := tenantRec.result(); reached != 0 {
		t.Fatalf("tenant handler reached")
	}
	if m, o := f.members.counts(); m != 0 || o != 0 {
		t.Fatalf("platform touched on a cross-bearer refusal (%d, %d)", m, o)
	}
	if f.orgs.count() != 0 {
		t.Fatalf("resolver touched on a cross-bearer refusal")
	}
}

// ADR-0020 leg 2, the named non-rule, tested as an absence of effect: a
// public request behaves identically with and without X-Org, and with a
// garbage credential, and no scope is ever stamped.
func TestPublicXOrgIgnored(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	h := f.gate.Public(rec.handler())

	base := capture(h, nil)
	withOrg := capture(h, map[string]string{HeaderXOrg: fixtureOrgSlug})
	withBoth := capture(h, map[string]string{HeaderXOrg: fixtureOtherSlug, "Authorization": "Bearer garbage"})

	for name, resp := range map[string]*captureResponse{"plain": base, "with x-org": withOrg, "with both": withBoth} {
		if resp.status != http.StatusOK || resp.body != `{"ok":true}` {
			t.Fatalf("%s: status %d body %s", name, resp.status, resp.body)
		}
	}
	reached, staff, tenant := rec.result()
	if reached != 3 {
		t.Fatalf("handler reached %d times", reached)
	}
	if staff != (StaffScope{}) || tenant != (TenantScope{}) {
		t.Fatalf("public request carries scope: %+v %+v", staff, tenant)
	}
	if m, o := f.members.counts(); m != 0 || o != 0 {
		t.Fatalf("platform touched on a public request")
	}
	if f.orgs.count() != 0 {
		t.Fatalf("resolver touched on a public request")
	}
}

// The sync surface middleware half (finding F7): both halves of G4 under
// either bearer, exactly as on the single-class surfaces, and anonymous
// still 401.
func TestSyncBothBearers(t *testing.T) {
	f := newFixture(t)
	rec := &recorder{}
	h := f.gate.Sync(rec.handler())
	staffAuth := "Bearer " + f.staffToken(t, fixtureSubject)
	tenantAuth := "Bearer " + f.tenantToken(t, fixtureSubject, fixtureTenant)

	resp := capture(h, map[string]string{"Authorization": staffAuth})
	if resp.status != http.StatusBadRequest {
		t.Fatalf("sync staff without x-org: status %d", resp.status)
	}
	resp = capture(h, map[string]string{"Authorization": staffAuth, HeaderXOrg: fixtureOrgSlug})
	if resp.status != http.StatusOK {
		t.Fatalf("sync staff with x-org: status %d body %s", resp.status, resp.body)
	}

	resp = capture(h, map[string]string{"Authorization": tenantAuth, HeaderXOrg: fixtureOrgSlug})
	if resp.status != http.StatusBadRequest {
		t.Fatalf("sync tenant with x-org: status %d", resp.status)
	}
	resp = capture(h, map[string]string{"Authorization": tenantAuth})
	if resp.status != http.StatusOK {
		t.Fatalf("sync tenant without x-org: status %d body %s", resp.status, resp.body)
	}

	resp = capture(h, nil)
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("sync anonymous: status %d", resp.status)
	}
	if reached, _, _ := rec.result(); reached != 2 {
		t.Fatalf("handler reached %d times, want the two admitted calls", reached)
	}
}

// The acceptance property behind the tracer: on every refusal leg the
// middleware executes no core-database statement. The OrgResolver is the
// only database seam the middleware holds, so its call count is the
// statement count. A slug no org carries is refused at the membership
// check (the platform has no team org:<slug> for it), so the anchor read
// never runs; the membership-passed, anchor-missing provisioning gap is
// the one refusal that reaches the anchor, and it is covered with its own
// assertions in TestStaffOrgRefusalBodiesIdentical.
func TestRefusalsExecuteNoDatabaseStatements(t *testing.T) {
	legs := map[string]struct {
		fixture func(*fixture)
		gate    func(*fixture, http.Handler) http.Handler
		headers func(t *testing.T, f *fixture) map[string]string
	}{
		"anonymous": {nil, func(f *fixture, h http.Handler) http.Handler { return f.gate.Staff(h) }, func(t *testing.T, f *fixture) map[string]string {
			return nil
		}},
		"garbage bearer": {nil, func(f *fixture, h http.Handler) http.Handler { return f.gate.Staff(h) }, func(t *testing.T, f *fixture) map[string]string {
			return map[string]string{"Authorization": "Bearer junk.junk.junk", HeaderXOrg: fixtureOrgSlug}
		}},
		"staff missing x-org": {nil, func(f *fixture, h http.Handler) http.Handler { return f.gate.Staff(h) }, func(t *testing.T, f *fixture) map[string]string {
			return map[string]string{"Authorization": "Bearer " + f.staffToken(t, fixtureSubject)}
		}},
		"staff wrong org": {func(f *fixture) { f.member = false }, func(f *fixture, h http.Handler) http.Handler { return f.gate.Staff(h) }, func(t *testing.T, f *fixture) map[string]string {
			return map[string]string{
				"Authorization": "Bearer " + f.staffToken(t, fixtureSubject),
				HeaderXOrg:      fixtureOtherSlug,
			}
		}},
		"staff ghost slug": {func(f *fixture) { f.member = false }, func(f *fixture, h http.Handler) http.Handler { return f.gate.Staff(h) }, func(t *testing.T, f *fixture) map[string]string {
			return map[string]string{
				"Authorization": "Bearer " + f.staffToken(t, fixtureSubject),
				HeaderXOrg:      "ghostorg",
			}
		}},
		"tenant with x-org": {nil, func(f *fixture, h http.Handler) http.Handler { return f.gate.Tenant(h) }, func(t *testing.T, f *fixture) map[string]string {
			return map[string]string{
				"Authorization": "Bearer " + f.tenantToken(t, fixtureSubject, fixtureTenant),
				HeaderXOrg:      fixtureOrgSlug,
			}
		}},
		"tenant no team": {func(f *fixture) { f.orgList = nil }, func(f *fixture, h http.Handler) http.Handler { return f.gate.Tenant(h) }, func(t *testing.T, f *fixture) map[string]string {
			return map[string]string{
				"Authorization": "Bearer " + f.tenantToken(t, fixtureSubject, fixtureTenant),
			}
		}},
	}
	for name, leg := range legs {
		f := newFixture(t)
		if leg.fixture != nil {
			leg.fixture(f)
		}
		rec := &recorder{}
		h := leg.gate(f, rec.handler())
		resp := capture(h, leg.headers(t, f))
		if resp.status < 400 {
			t.Fatalf("%s: expected a refusal, got %d", name, resp.status)
		}
		if f.orgs.count() != 0 {
			t.Fatalf("%s: resolver ran %d times on a refused request", name, f.orgs.count())
		}
		if reached, _, _ := rec.result(); reached != 0 {
			t.Fatalf("%s: handler reached on a refused request", name)
		}
	}
}

// A scope forged upstream is never trusted: the middleware overwrites it
// with verified values.
func TestForgedScopeOverwritten(t *testing.T) {
	f := newFixture(t)
	var seen StaffScope
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = StaffScopeFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := f.gate.Staff(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	req = req.WithContext(WithStaffScope(mustContext(), StaffScope{
		PlatformUserID: "forged-user",
		OrgSlug:        "forged",
		OrgID:          "forged-id",
	}))
	req.Header.Set("Authorization", "Bearer "+f.staffToken(t, fixtureSubject))
	req.Header.Set(HeaderXOrg, fixtureOrgSlug)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d", recorder.Code)
	}
	if seen.PlatformUserID != fixtureSubject || seen.OrgID != fixtureOrgID {
		t.Fatalf("forged scope survived: %+v", seen)
	}
}

func TestNewGateValidation(t *testing.T) {
	f := newFixture(t)
	if _, err := NewGate(nil, f.members, f.orgs); err == nil {
		t.Fatalf("nil verifier accepted")
	}
	if _, err := NewGate(f.verifier, nil, f.orgs); err == nil {
		t.Fatalf("nil membership accepted")
	}
	if _, err := NewGate(f.verifier, f.members, nil); err == nil {
		t.Fatalf("nil resolver accepted")
	}
}

func TestBearerTokenScheme(t *testing.T) {
	good := "tok.enc.sig"
	for name, header := range map[string]string{
		"lowercase scheme": "bearer " + good,
		"uppercase scheme": "BEARER " + good,
		"mixed scheme":     "BeArEr " + good,
	} {
		if got, ok := bearerToken(header); !ok || got != good {
			t.Fatalf("%s: got %q ok=%v", name, got, ok)
		}
	}
	for name, header := range map[string]string{
		"no space":   "bearertok",
		"wrong kind": "Basic dXNlcjpwYXNz",
		"empty":      "",
		"two tokens": "bearer tok enc",
	} {
		if _, ok := bearerToken(header); ok {
			t.Fatalf("%s: accepted", name)
		}
	}
}
