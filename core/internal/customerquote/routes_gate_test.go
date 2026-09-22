package customerquote

// The registration's gate tests (HH-3.6 C5). The x-role equality machinery
// is Lane B's and does not exist in this tree, so the registered routes'
// gates are pinned here instead, at the module: the public leg's properties
// (bare, X-Org and a bogus bearer identical; a valid tenant credential
// never widens the token; no org resolution path runs), the send route's
// staff gate (the admission matrix and the org fence on the data), and the
// send event on the delivery seam (D34: the no-provider stub, one handoff
// per committed send, none on a refusal).

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// gateBench is the bench variant whose org resolver is the kit double, so
// the org-resolution reach of one request is measurable as a call count
// (the Phase B zero-repository-reach pattern: the resolver call count is
// the middleware-leg stand-in for the anchor-read statement count).
type gateBench struct {
	*bench
	resolver *testkit.ResolverDouble
	members  *testkit.MembershipDouble
}

func gateBenchFor(t *testing.T) *gateBench {
	t.Helper()
	if sharedPool == nil {
		t.Skip(dbEnvVar + " is not set: the gate tests need the throwaway database with migrations applied")
	}
	g := &gateBench{bench: &bench{pool: sharedPool, kit: testkit.NewHarness(t)}}
	g.kit.WireFixtureOrgs(t)
	g.resolver = g.kit.Resolver
	g.members = g.kit.Membership
	gate, err := authz.NewGate(g.kit.Verifier, g.kit.Membership, g.resolver)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	staff, err := identity.NewStaffAuthorityResolver(g.pool)
	if err != nil {
		t.Fatalf("staff resolver: %v", err)
	}
	portal, err := identity.NewPortalRoleResolver(g.pool)
	if err != nil {
		t.Fatalf("portal resolver: %v", err)
	}
	gd, err := guards.NewGuards(gate, staff, portal, nil)
	if err != nil {
		t.Fatalf("guards: %v", err)
	}
	store, err := NewStore(g.pool)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	g.seam = newPgxAuditSeam(g.pool)
	g.keys = &SigningKeys{KeyID: "fx-accept-key", Key: bytes.Repeat([]byte{0x51}, 32)}
	g.svc, err = NewService(store, g.seam, g.keys, Config{Now: g.kit.Clock.Now})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	g.mux = http.NewServeMux()
	RegisterRoutes(g.mux, gd, g.svc)
	return g
}

// S3 on both public operations: bare, X-Org and a well-formed but
// irrelevant bearer answer identically on the non-volatile fields, and a
// VALID tenant bearer for another tenant never widens what the token
// grants: the answer stays byte-for-byte the bare one, no more.
func TestS3PublicLegIgnoresHeadersAndCredentials(t *testing.T) {
	g := gateBenchFor(t)
	horizon := g.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := g.createQuote(t, fixtureCreate(horizon))
	sent := g.sendQuote(t, created.ID)

	// The view: warm (the first view advances sent to viewed), then the
	// three header variants plus the real foreign tenant bearer, byte for
	// byte identical.
	warm := g.publicView(t, sent.MagicLinkToken)
	bare := g.publicView(t, sent.MagicLinkToken)
	withOrg := doReq(t, g.mux, http.MethodGet, g.viewURL(sent.MagicLinkToken), "",
		map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug}, nil)
	withBogus := doReq(t, g.mux, http.MethodGet, g.viewURL(sent.MagicLinkToken), "definitely-not-a-bearer", nil, nil)
	withTenant := doReq(t, g.mux, http.MethodGet, g.viewURL(sent.MagicLinkToken),
		g.kit.Minter.TenantToken(t, testkit.SubPortalAdminBeta, testkit.TenantBetaID), nil, nil)
	for leg, rec := range map[string]*httptest.ResponseRecorder{
		"bare": bare, "X-Org": withOrg, "bogus bearer": withBogus, "valid tenant bearer": withTenant,
	} {
		if rec.Code != warm.Code || rec.Body.String() != warm.Body.String() {
			t.Fatalf("public view with %s differs from the bare answer: %d vs %d", leg, rec.Code, warm.Code)
		}
	}

	// The accept: three fresh quotes, one per variant, identical fixtures;
	// the answers agree on every non-volatile field (the name and every
	// cents field), and none of the variants changes the outcome.
	acceptLeg := func(name string, bearer string, headers map[string]string) AcceptanceResult {
		created := g.createQuote(t, fixtureCreate(horizon))
		sent := g.sendQuote(t, created.ID)
		rec := doReq(t, g.mux, http.MethodPost, g.acceptURL(sent.MagicLinkToken), bearer, headers,
			AcceptRequest{TypedName: "Fixture Homeowner One", Accepted: true})
		assertStatus(t, "accept "+name, rec, http.StatusOK)
		var result AcceptanceResult
		decode(t, rec, &result)
		if result.QuoteID != created.ID {
			t.Fatalf("accept %s recorded against %s, want %s", name, result.QuoteID, created.ID)
		}
		return result
	}
	base := acceptLeg("bare", "", nil)
	withOrgAccept := acceptLeg("X-Org", "", map[string]string{authz.HeaderXOrg: testkit.OrgNorthSlug})
	withBogusAccept := acceptLeg("bogus bearer", "definitely-not-a-bearer", nil)
	for name, result := range map[string]AcceptanceResult{"X-Org": withOrgAccept, "bogus bearer": withBogusAccept} {
		if result.TypedName != base.TypedName || result.MaterialsCents != base.MaterialsCents ||
			result.LabourCents != base.LabourCents || result.OverheadCents != base.OverheadCents ||
			result.TotalCents != base.TotalCents {
			t.Fatalf("accept with %s changed the agreed totals or name: %+v vs %+v", name, result, base)
		}
	}
}

// S3's zero-repository-reach leg: a public view and a public accept run
// with zero org anchor reads and zero membership calls, and still succeed.
// The doubles' call counts are the measurable form of "no org resolution
// path" (ADR-0020's public leg); if the public pair ever gains a scoped
// wrapper, the counts move and this pin fails.
func TestS3PublicLegResolvesNoOrg(t *testing.T) {
	g := gateBenchFor(t)
	horizon := g.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := g.createQuote(t, fixtureCreate(horizon))
	sent := g.sendQuote(t, created.ID)

	memberBefore, tenantsBefore := g.members.Counts()
	resolverBefore := g.resolver.Calls()
	rec := g.publicView(t, sent.MagicLinkToken)
	assertStatus(t, "public view", rec, http.StatusOK)
	rec = g.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "public accept", rec, http.StatusOK)
	memberAfter, tenantsAfter := g.members.Counts()
	if memberAfter != memberBefore || tenantsAfter != tenantsBefore {
		t.Fatalf("the public leg reached the membership source: member %d->%d tenants %d->%d",
			memberBefore, memberAfter, tenantsBefore, tenantsAfter)
	}
	if got := g.resolver.Calls(); got != resolverBefore {
		t.Fatalf("the public leg performed an org anchor read: %d -> %d", resolverBefore, got)
	}
}

// The send route's staff gate: the admission matrix through the shipped
// Guards, plus the org fence on the data (the dealer desk's authority ends
// at its org).
func TestSendRouteStaffGate(t *testing.T) {
	g := gateBenchFor(t)
	horizon := g.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := g.createQuote(t, fixtureCreate(horizon))
	target := "/api/v1/customer-quotes/" + created.ID + "/send"

	leg := func(name string, bearer string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		return doReq(t, g.mux, http.MethodPost, target, bearer, headers, struct{}{})
	}

	// No credential: the 401 family with the shared body.
	rec := leg("no bearer", "", staffHeaders(testkit.OrgNorthSlug))
	assertStatus(t, "no bearer", rec, http.StatusUnauthorized)
	// A malformed bearer: the same 401, same body (no client can tell which
	// check failed).
	rec = leg("bogus bearer", "definitely-not-a-bearer", staffHeaders(testkit.OrgNorthSlug))
	if rec.Code != http.StatusUnauthorized || rec.Body.String() != string(authz.BodyUnauthorized) {
		t.Fatalf("bogus bearer: %d %s, want the shared 401 body", rec.Code, rec.Body.String())
	}
	// A verified tenant bearer is authenticated but the wrong class for the
	// staff leg: the RBAC-403, not the 401 family (the credential verified;
	// the gate refused it).
	rec = leg("tenant bearer", g.kit.Minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID),
		staffHeaders(testkit.OrgNorthSlug))
	assertStatus(t, "tenant bearer", rec, http.StatusForbidden)
	// A valid staff bearer without the org selection: the 400 shape family.
	rec = leg("no X-Org", g.kit.Minter.StaffToken(t, testkit.SubInsideSales), nil)
	assertStatus(t, "no X-Org", rec, http.StatusBadRequest)
	// Membership in the wrong org only: refused at the org gate (SubGeneralMgr
	// holds membership and grants in the north; the south has neither).
	rec = leg("south org", g.kit.Minter.StaffToken(t, testkit.SubGeneralMgr), staffHeaders(testkit.OrgSouthSlug))
	assertStatus(t, "south org", rec, http.StatusForbidden)
	// Membership without an active grant: the RBAC-403 (SubRevoked's grant
	// row is tombstoned).
	rec = leg("revoked grant", g.kit.Minter.StaffToken(t, testkit.SubRevoked), staffHeaders(testkit.OrgNorthSlug))
	assertStatus(t, "revoked grant", rec, http.StatusForbidden)
	// The green leg: an active grant in the selected org, any tier (the
	// dealer send family is requireStaff).
	rec = leg("inside sales", g.kit.Minter.StaffToken(t, testkit.SubInsideSales), staffHeaders(testkit.OrgNorthSlug))
	assertStatus(t, "inside sales sends", rec, http.StatusOK)
	var out SendResult
	decode(t, rec, &out)
	if out.MagicLinkToken == "" || out.Status != StatusSent {
		t.Fatalf("the staff send did not mint a token and move the quote to sent")
	}

	// The org fence on the data: a south-org quote is invisible to the
	// north desk (404, the tenant-leg sentence), and a south-org staff
	// caller with a south grant sends it. SubTwoOrg holds grants in both
	// orgs but membership in the north only; the sweep moves its membership
	// to the south, the wrong-one-of-two shape reversed.
	southQuote := g.createQuoteAs(t, benchTenantGamma, fixtureCreate(horizon))
	southTarget := "/api/v1/customer-quotes/" + southQuote.ID + "/send"
	rec = doReq(t, g.mux, http.MethodPost, southTarget,
		g.kit.Minter.StaffToken(t, testkit.SubInsideSales), staffHeaders(testkit.OrgNorthSlug), struct{}{})
	e := assertStatus(t, "north desk sends a south quote", rec, http.StatusNotFound)
	if e.Error != SentenceQuoteGone {
		t.Fatalf("cross-org send refusal %q, want the quote-gone sentence", e.Error)
	}
	g.members.SetMember(testkit.SubTwoOrg, testkit.OrgNorthSlug, false)
	g.members.SetMember(testkit.SubTwoOrg, testkit.OrgSouthSlug, true)
	rec = doReq(t, g.mux, http.MethodPost, southTarget,
		g.kit.Minter.StaffToken(t, testkit.SubTwoOrg), staffHeaders(testkit.OrgSouthSlug), struct{}{})
	assertStatus(t, "south desk sends the south quote", rec, http.StatusOK)
}

// recordingMailer records the send events handed to the delivery seam.
type recordingMailer struct {
	mu    sync.Mutex
	links []SentLink
}

func (r *recordingMailer) SendQuoteLink(_ context.Context, link SentLink) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.links = append(r.links, link)
	return nil
}

// The send event on the delivery seam (D34): exactly one handoff per
// committed send, carrying the quote, the scopes, the customer and the raw
// token the response also returns; nothing handed over on a refused send;
// and the default NoProviderMailer is the no-provider stub, never an error.
func TestSendEventOnTheMailerSeam(t *testing.T) {
	b := benchFor(t)
	recorder := &recordingMailer{}
	b.svc.mail = recorder
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)

	// A refused send (a gate failure) hands nothing over.
	noName := fixtureCreate(horizon)
	noName.Customer = CustomerContact{Name: " "}
	created := b.createQuote(t, noName)
	if _, err := b.svc.Send(context.Background(), benchTenantAlpha, created.ID); err == nil {
		t.Fatalf("the nameless send unexpectedly succeeded")
	}
	if len(recorder.links) != 0 {
		t.Fatalf("a refused send handed %d links to the delivery seam", len(recorder.links))
	}

	// A green send hands exactly one, with the committed facts.
	if _, err := b.svc.Update(context.Background(), benchTenantAlpha, created.ID,
		UpdateInput{Customer: &CustomerContact{Name: "Fixture Homeowner One"}}); err != nil {
		t.Fatalf("patch the name: %v", err)
	}
	sent := b.sendQuote(t, created.ID)
	if len(recorder.links) != 1 {
		t.Fatalf("a committed send handed %d links, want exactly 1", len(recorder.links))
	}
	link := recorder.links[0]
	if link.QuoteID != created.ID || link.Token != sent.MagicLinkToken || link.Token == "" {
		t.Fatalf("the send event misnames the quote or the token: %+v", link)
	}
	if link.OrgID != testkit.OrgNorthID || link.TenantID != testkit.TenantAlphaID {
		t.Fatalf("the send event misnames the scopes: %+v", link)
	}
	if link.CustomerName != "Fixture Homeowner One" || link.ValidUntil == nil || link.SentAt.IsZero() {
		t.Fatalf("the send event misses the customer or the bounds: %+v", link)
	}

	// The default stub (D34: the core holds no mail server) never errors.
	var stub Mailer = NoProviderMailer{}
	if err := stub.SendQuoteLink(context.Background(), link); err != nil {
		t.Fatalf("the no-provider stub errored: %v", err)
	}
}

// S1's remaining legs, beside T1's replay pin: the view counters keep
// advancing on replayed views after the acceptance (the standing record
// stays readable and observed), and a replayed accept after a decline
// answers 409 naming the standing state with no second acceptance row.
// There is no decline operation in the module (the public surface carries
// view and accept only), so the declined state is planted directly.
func TestS1ReplayLegsDeclineAndViewCounters(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	sent := b.sendQuote(t, created.ID)
	rec := b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "accept", rec, http.StatusOK)

	// Replay the view: still 200, status accepted, acceptable false, and
	// the counters advance.
	b.publicView(t, sent.MagicLinkToken)
	countAfterFirst := b.scalarInt(t, `SELECT view_count FROM customer_quote WHERE id = $1`, created.ID)
	rec = b.publicView(t, sent.MagicLinkToken)
	assertStatus(t, "replayed view", rec, http.StatusOK)
	var view PublicView
	decode(t, rec, &view)
	if view.Status != StatusAccepted || view.Acceptable {
		t.Fatalf("replayed view status %s acceptable %v", view.Status, view.Acceptable)
	}
	if got := b.scalarInt(t, `SELECT view_count FROM customer_quote WHERE id = $1`, created.ID); got != countAfterFirst+1 {
		t.Fatalf("the replayed view did not advance the counter: %d then %d", countAfterFirst, got)
	}

	// The declined family: a second quote, declined by plant, refuses the
	// replayed accept in the 409 family with no row.
	declined := b.createQuote(t, fixtureCreate(horizon))
	declinedSent := b.sendQuote(t, declined.ID)
	if _, err := b.pool.Exec(ctx,
		`UPDATE customer_quote SET status = 'declined' WHERE id = $1`, declined.ID); err != nil {
		t.Fatalf("plant the decline: %v", err)
	}
	rec = b.publicAccept(t, declinedSent.MagicLinkToken, "Fixture Homeowner One", true)
	e := assertStatus(t, "replay accept after decline", rec, http.StatusConflict)
	if e.Error != SentenceAlreadyResponded {
		t.Fatalf("declined replay refusal %q, want the already-responded sentence", e.Error)
	}
	if got := b.scalarInt(t, `SELECT count(*) FROM quote_acceptance WHERE customer_quote_id = $1`, declined.ID); got != 0 {
		t.Fatalf("the declined replay created %d acceptance rows", got)
	}
}
