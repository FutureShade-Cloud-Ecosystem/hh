package testkit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// The kit's own proof set: the minter produces tokens the B2 verifier
// accepts over the served JWKS URL, every negative shape the sweeps need
// fails closed with the right verification class and one 401 body, the
// G4 middleware halves behave as pinned, the erroring doubles are 503s,
// the provisioning gap is exactly one anchor read, and the database parts
// build and clean up count-exact against a migrated scratch database.
// The sweeps themselves are B5's; these tests prove the kit.

func TestStaffPositiveLegReachesHandlerWithScope(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Staff(rec.Handler())

	rsp := Do(t, handler, http.MethodGet, "/api/v1/orders/"+OrderAlphaDraftID,
		h.Minter.StaffToken(t, SubInsideSales), OrgNorthSlug)
	AssertStatus(t, "staff positive", rsp, http.StatusOK)
	rec.AssertStaffReach(t, "staff positive", SubInsideSales, OrgNorthID)
}

func TestStaffAuthorityJunkChangesNothing(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Staff(rec.Handler())

	rsp := Do(t, handler, http.MethodGet, "/api/v1/orders/"+OrderAlphaDraftID,
		h.Minter.StaffTokenWithAuthorityJunk(t, SubInsideSales), OrgNorthSlug)
	AssertStatus(t, "authority junk", rsp, http.StatusOK)
	rec.AssertStaffReach(t, "authority junk", SubInsideSales, OrgNorthID)
}

func TestECAlgorithmPositiveLeg(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Staff(rec.Handler())

	rsp := Do(t, handler, http.MethodGet, "/api/v1/orders/"+OrderAlphaDraftID,
		h.Minter.ECStaffToken(t, SubSalesManager), OrgNorthSlug)
	AssertStatus(t, "es256 positive", rsp, http.StatusOK)
	rec.AssertStaffReach(t, "es256 positive", SubSalesManager, OrgNorthID)
}

func TestTenantPositiveLegReachesHandlerWithScope(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Tenant(rec.Handler())

	rsp := Do(t, handler, http.MethodGet, "/api/v1/tenant/orders",
		h.Minter.TenantToken(t, SubPortalAdminAlpha, TenantAlphaID))
	AssertStatus(t, "tenant positive", rsp, http.StatusOK)
	rec.AssertTenantReach(t, "tenant positive", SubPortalAdminAlpha, OrgNorthID, TenantAlphaID)
}

func TestSyncAcceptsBothClasses(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Sync(rec.Handler())

	rsp := Do(t, handler, http.MethodPost, "/api/v1/sync/orders",
		h.Minter.StaffToken(t, SubGeneralMgr), OrgNorthSlug)
	AssertStatus(t, "sync staff", rsp, http.StatusOK)
	rec.AssertStaffReach(t, "sync staff", SubGeneralMgr, OrgNorthID)

	rec.Reset()
	rsp = Do(t, handler, http.MethodPost, "/api/v1/sync/orders",
		h.Minter.TenantToken(t, SubPortalCrewAlpha, TenantAlphaID))
	AssertStatus(t, "sync tenant", rsp, http.StatusOK)
	rec.AssertTenantReach(t, "sync tenant", SubPortalCrewAlpha, OrgNorthID, TenantAlphaID)
}

func TestCrossBearerIsForbiddenAndNeverReaches(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	staffRec := NewRecorder()
	tenantRec := NewRecorder()
	staffRoute := h.Gate.Staff(staffRec.Handler())
	tenantRoute := h.Gate.Tenant(tenantRec.Handler())

	rsp := Do(t, staffRoute, http.MethodGet, "/api/v1/orders",
		h.Minter.TenantToken(t, SubPortalAdminAlpha, TenantAlphaID), OrgNorthSlug)
	AssertStatus(t, "tenant on staff route", rsp, http.StatusForbidden)
	staffRec.AssertNotReached(t, "tenant on staff route")

	rsp = Do(t, tenantRoute, http.MethodGet, "/api/v1/tenant/orders",
		h.Minter.StaffToken(t, SubInsideSales))
	AssertStatus(t, "staff on tenant route", rsp, http.StatusForbidden)
	tenantRec.AssertNotReached(t, "staff on tenant route")
}

// negativeLeg pairs one minted negative shape with the verification class
// the B2 verifier must answer: every one of them is a who-you-are or
// shape failure (401 with one body), never nil, never operational.
type negativeLeg struct {
	name  string
	token string
	class identity.ErrorClass
}

func negativeLegs(t *testing.T, m *Minter) []negativeLeg {
	t.Helper()
	sub := SubInsideSales
	tenant := TenantAlphaID
	legs := []negativeLeg{
		{"expired staff", m.ExpiredToken(t, StaffClass, sub, tenant), identity.ClassCredential},
		{"expired tenant", m.ExpiredToken(t, TenantClass, SubPortalAdminAlpha, tenant), identity.ClassCredential},
		{"wrong key", m.WrongKeyToken(t, StaffClass, sub, tenant), identity.ClassCredential},
		{"unknown kid", m.UnknownKidToken(t, StaffClass, sub, tenant), identity.ClassCredential},
		{"alg confusion", m.ConfusionToken(t, StaffClass, sub, tenant), identity.ClassCredential},
		{"dual audience", m.DualAudienceToken(t, sub, tenant), identity.ClassCredential},
		{"foreign audience", m.ForeignAudienceToken(t, sub), identity.ClassCredential},
		{"tenant without tenant claim", m.TenantWithoutTenantClaim(t, SubPortalAdminAlpha), identity.ClassMalformed},
	}
	for name, tok := range m.MissingClaimTokens(t, sub) {
		class := identity.ClassMalformed
		if name == "foreign-iss" {
			class = identity.ClassCredential
		}
		legs = append(legs, negativeLeg{name: name, token: tok, class: class})
	}
	return legs
}

func TestNegativeTokensFailClosedAtTheVerifier(t *testing.T) {
	h := NewHarness(t)
	for _, leg := range negativeLegs(t, h.Minter) {
		_, err := h.Verifier.Verify(context.Background(), leg.token)
		if err == nil {
			t.Fatalf("leg %s: verified, want refusal", leg.name)
		}
		var ve *identity.VerifyError
		if !asVerifyError(err, &ve) {
			t.Fatalf("leg %s: error %T, want *identity.VerifyError", leg.name, err)
		}
		if ve.Class() != leg.class {
			t.Fatalf("leg %s: class %d, want %d (%v)", leg.name, ve.Class(), leg.class, err)
		}
	}
}

func TestNegativeTokensAnswerOneUnauthorizedBody(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Staff(rec.Handler())

	var responses []*httptest.ResponseRecorder
	legs := negativeLegs(t, h.Minter)
	for _, leg := range legs {
		rsp := Do(t, handler, http.MethodGet, "/api/v1/orders", leg.token, OrgNorthSlug)
		AssertStatus(t, leg.name, rsp, http.StatusUnauthorized)
		responses = append(responses, rsp)
	}
	// The no-token and garbage variants join the same family.
	rsp := Do(t, handler, http.MethodGet, "/api/v1/orders", "")
	AssertStatus(t, "no token", rsp, http.StatusUnauthorized)
	responses = append(responses, rsp)
	for i, g := range h.Minter.GarbageBearers(t) {
		rsp := Do(t, handler, http.MethodGet, "/api/v1/orders", g)
		AssertStatus(t, garbageName(i), rsp, http.StatusUnauthorized)
		responses = append(responses, rsp)
	}
	AssertOneBodyAcross(t, "401 family", responses)
	rec.AssertNotReached(t, "every 401 leg")
}

func garbageName(i int) string {
	names := []string{"not a jwt", "truncated", "random bytes", "dots"}
	if i < len(names) {
		return names[i]
	}
	return "garbage"
}

func TestRotationTurnsRetiredKidIntoUnknownKid(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Staff(rec.Handler())

	tok := h.Minter.RetiredKidToken(t, StaffClass, SubInsideSales, TenantAlphaID)
	rsp := Do(t, handler, http.MethodGet, "/api/v1/orders", tok, OrgNorthSlug)
	AssertStatus(t, "retired kid before rotation", rsp, http.StatusOK)
	rec.AssertStaffReach(t, "retired kid before rotation", SubInsideSales, OrgNorthID)

	h.Server.Rotate()
	h.Clock.Advance(verifierKeyCacheTTL + time.Millisecond)
	rec.Reset()
	rsp = Do(t, handler, http.MethodGet, "/api/v1/orders", tok, OrgNorthSlug)
	AssertStatus(t, "retired kid after rotation", rsp, http.StatusUnauthorized)
	rec.AssertNotReached(t, "retired kid after rotation")
}

func TestG4Halves(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	staffRec := NewRecorder()
	tenantRec := NewRecorder()
	staffRoute := h.Gate.Staff(staffRec.Handler())
	tenantRoute := h.Gate.Tenant(tenantRec.Handler())

	// SW-6a: staff bearer without X-Org refused (400, the pinned shape
	// family), with the header admitted.
	rsp := Do(t, staffRoute, http.MethodGet, "/api/v1/orders", h.Minter.StaffToken(t, SubInsideSales))
	AssertStatus(t, "missing x-org", rsp, http.StatusBadRequest)
	staffRec.AssertNotReached(t, "missing x-org")

	rsp = Do(t, staffRoute, http.MethodGet, "/api/v1/orders",
		h.Minter.StaffToken(t, SubInsideSales), OrgNorthSlug, OrgSouthSlug)
	AssertStatus(t, "repeated x-org", rsp, http.StatusBadRequest)
	staffRec.AssertNotReached(t, "repeated x-org")

	rsp = Do(t, staffRoute, http.MethodGet, "/api/v1/orders",
		h.Minter.StaffToken(t, SubInsideSales), "Not_A_Slug")
	AssertStatus(t, "malformed x-org", rsp, http.StatusBadRequest)
	staffRec.AssertNotReached(t, "malformed x-org")

	rsp = Do(t, staffRoute, http.MethodGet, "/api/v1/orders",
		h.Minter.StaffToken(t, SubInsideSales), OrgNorthSlug)
	AssertStatus(t, "x-org present", rsp, http.StatusOK)
	staffRec.AssertStaffReach(t, "x-org present", SubInsideSales, OrgNorthID)

	// SW-6b: tenant bearer carrying X-Org refused for both sub-halves:
	// naming its own org and naming another org.
	tok := h.Minter.TenantToken(t, SubPortalAdminAlpha, TenantAlphaID)
	rsp = Do(t, tenantRoute, http.MethodGet, "/api/v1/tenant/orders", tok, OrgNorthSlug)
	AssertStatus(t, "tenant carries own org", rsp, http.StatusBadRequest)
	tenantRec.AssertNotReached(t, "tenant carries own org")

	rsp = Do(t, tenantRoute, http.MethodGet, "/api/v1/tenant/orders", tok, OrgSouthSlug)
	AssertStatus(t, "tenant carries other org", rsp, http.StatusBadRequest)
	tenantRec.AssertNotReached(t, "tenant carries other org")

	rsp = Do(t, tenantRoute, http.MethodGet, "/api/v1/tenant/orders", tok)
	AssertStatus(t, "tenant without header", rsp, http.StatusOK)
	tenantRec.AssertTenantReach(t, "tenant without header", SubPortalAdminAlpha, OrgNorthID, TenantAlphaID)
}

func TestWrongOrgRefusesWithZeroAnchorReads(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Staff(rec.Handler())

	// Both wrong-org shapes: membership absent for a well-formed slug,
	// and the two-org principal presenting the wrong one of its two
	// (grant rows exist in both orgs, Team membership in the north only).
	rsp := Do(t, handler, http.MethodGet, "/api/v1/orders",
		h.Minter.StaffToken(t, SubInsideSales), OrgSouthSlug)
	AssertStatus(t, "membership absent", rsp, http.StatusForbidden)
	rec.AssertNotReached(t, "membership absent")
	if calls := h.Resolver.Calls(); calls != 0 {
		t.Fatalf("membership absent: %d anchor reads, want 0", calls)
	}

	rsp = Do(t, handler, http.MethodGet, "/api/v1/orders",
		h.Minter.StaffToken(t, SubTwoOrg), OrgSouthSlug)
	AssertStatus(t, "two org principal, wrong one of two", rsp, http.StatusForbidden)
	rec.AssertNotReached(t, "two org principal, wrong one of two")
	if calls := h.Resolver.Calls(); calls != 0 {
		t.Fatalf("two org principal: %d anchor reads, want 0", calls)
	}

	// Positive control: the same principal under the granted org admits.
	rsp = Do(t, handler, http.MethodGet, "/api/v1/orders",
		h.Minter.StaffToken(t, SubTwoOrg), OrgNorthSlug)
	AssertStatus(t, "two org principal, granted org", rsp, http.StatusOK)
	rec.AssertStaffReach(t, "two org principal, granted org", SubTwoOrg, OrgNorthID)
}

func TestProvisioningGapIsExactlyOneAnchorRead(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	h.Membership.SetMember(SubInsideSales, OrgSouthSlug, true)
	h.Resolver.Missing(OrgSouthSlug) // membership passes, anchor row absent
	rec := NewRecorder()
	handler := h.Gate.Staff(rec.Handler())

	before := h.Resolver.Calls()
	rsp := Do(t, handler, http.MethodGet, "/api/v1/orders",
		h.Minter.StaffToken(t, SubInsideSales), OrgSouthSlug)
	AssertStatus(t, "provisioning gap", rsp, http.StatusForbidden)
	rec.AssertNotReached(t, "provisioning gap")
	if reads := h.Resolver.Calls() - before; reads != 1 {
		t.Fatalf("provisioning gap: %d anchor reads, want exactly 1", reads)
	}
}

func TestErroringDoublesAreUnavailable(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Staff(rec.Handler())

	h.Membership.MemberError(ErrDependencyDown)
	rsp := Do(t, handler, http.MethodGet, "/api/v1/orders",
		h.Minter.StaffToken(t, SubInsideSales), OrgNorthSlug)
	AssertStatus(t, "membership source down", rsp, http.StatusServiceUnavailable)
	rec.AssertNotReached(t, "membership source down")

	h.Membership.MemberError(nil)
	h.Resolver.Error(ErrDependencyDown)
	rsp = Do(t, handler, http.MethodGet, "/api/v1/orders",
		h.Minter.StaffToken(t, SubInsideSales), OrgNorthSlug)
	AssertStatus(t, "resolver down", rsp, http.StatusServiceUnavailable)
	rec.AssertNotReached(t, "resolver down")
}

func TestFailingJWKSEndpointIsUnavailable(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Staff(rec.Handler())

	h.Server.Fail()
	h.Clock.Advance(verifierKeyCacheTTL + time.Millisecond)
	rsp := Do(t, handler, http.MethodGet, "/api/v1/orders",
		h.Minter.StaffToken(t, SubInsideSales), OrgNorthSlug)
	AssertStatus(t, "jwks endpoint down", rsp, http.StatusServiceUnavailable)
	rec.AssertNotReached(t, "jwks endpoint down")
}

func TestIssuedInFutureRefuses(t *testing.T) {
	h := NewHarness(t)
	h.WireFixtureOrgs(t)
	rec := NewRecorder()
	handler := h.Gate.Staff(rec.Handler())

	future := h.Clock.Now().Add(10 * time.Minute)
	tok := h.Minter.Mint(t, StaffClass, SubInsideSales, TokenShape{IssuedAt: &future})
	rsp := Do(t, handler, http.MethodGet, "/api/v1/orders", tok, OrgNorthSlug)
	AssertStatus(t, "issued in the future", rsp, http.StatusUnauthorized)
	rec.AssertNotReached(t, "issued in the future")
}

// The database-backed proofs. They run when KIT_TEST_DATABASE_URL points
// at a scratch database with migrations applied, and skip politely
// otherwise; the run report records skips, the F8 discipline.
func TestDatabaseFixturesBuildAndCleanUp(t *testing.T) {
	url := os.Getenv("KIT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("KIT_TEST_DATABASE_URL is not set: the database fixture proofs need a scratch database with migrations applied")
	}
	ctx := context.Background()
	pool, counter, err := NewCountingPool(ctx, url)
	if err != nil {
		t.Fatalf("counting pool: %v", err)
	}
	defer pool.Close()
	db := PgxPool(pool)

	if err := VerifyBaseline(ctx, db); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if err := Build(ctx, db); err != nil {
		t.Fatalf("build: %v", err)
	}
	// Idempotency: a second run is a no-op.
	if err := Build(ctx, db); err != nil {
		t.Fatalf("build again: %v", err)
	}
	counts, err := FixtureCounts(ctx, db)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	AssertCountsEqual(t, "after build", counts, ExpectedCounts())

	if err := Cleanup(ctx, db); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	counts, err = FixtureCounts(ctx, db)
	if err != nil {
		t.Fatalf("counts after cleanup: %v", err)
	}
	zero := make(map[string]int, len(ExpectedCounts()))
	for k := range ExpectedCounts() {
		zero[k] = 0
	}
	AssertCountsEqual(t, "after cleanup", counts, zero)

	// The pool tracer counts statements: proof the counting form itself
	// counts, so a zero in a sweep means something.
	counter.Reset()
	if _, err := pool.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("select: %v", err)
	}
	counter.AssertCount(t, "one select", 1)
}

func asVerifyError(err error, target **identity.VerifyError) bool {
	return errors.As(err, target)
}
