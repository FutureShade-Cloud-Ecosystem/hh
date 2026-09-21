package customerquote

// The named acceptance tests and their companions, run against a throwaway
// database with the frozen migration set applied, named by
// HH_CQ_TEST_DATABASE_URL. Absent the variable every test skips politely;
// the run report sets it and asserts zero skips. T1: a homeowner accepts a
// customer quote with a token and no account. T2: a revised quote's old
// token answers 410, never 404. The companions cover the classification
// cells the reviews pinned (F-3), the sweep legs this batch could carry
// cheaply (S1 replay, S2 cross-quote, S3 X-Org irrelevance, the S4
// malformed and expired families) and the S5 artifact verification leg.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const dbEnvVar = "HH_CQ_TEST_DATABASE_URL"

var sharedPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv(dbEnvVar)
	if dsn == "" {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	pool, _, err := testkit.NewCountingPool(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "customerquote accept: counting pool:", err)
		os.Exit(1)
	}
	sharedPool = pool
	db := testkit.PgxPool(pool)
	fail := func(step string, err error) {
		fmt.Fprintf(os.Stderr, "customerquote accept: %s: %v\n", step, err)
		pool.Close()
		os.Exit(1)
	}
	if err := testkit.VerifyBaseline(ctx, db); err != nil {
		fail("baseline", err)
	}
	if err := testkit.Build(ctx, db); err != nil {
		fail("fixture build", err)
	}
	code := m.Run()
	// The module's own rows: children before parents, and the audit rows the
	// seam double wrote, keyed by the pinned entity type.
	for _, stmt := range []string{
		`DELETE FROM audit_log WHERE entity_type = 'quote_acceptance'`,
		`DELETE FROM quote_acceptance`,
		`DELETE FROM customer_quote WHERE tenant_id IN ('` + testkit.TenantAlphaID + `', '` + testkit.TenantBetaID + `', '` + testkit.TenantGammaID + `')`,
		`DELETE FROM attachment WHERE appwrite_file_id = 'FXFILE'`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			fail("module cleanup", err)
		}
	}
	if err := testkit.Cleanup(ctx, db); err != nil {
		fail("fixture cleanup", err)
	}
	counts, err := testkit.FixtureCounts(ctx, db)
	if err != nil {
		fail("fixture counts after cleanup", err)
	}
	zero := map[string]int{}
	for name := range testkit.ExpectedCounts() {
		zero[name] = 0
	}
	for name, n := range counts {
		if n != zero[name] {
			fail("fixture residue", fmt.Errorf("table %s: %d rows remain", name, n))
		}
	}
	// The module's tables must be empty too.
	for _, table := range []string{"customer_quote", "quote_acceptance"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			fail("module residue", fmt.Errorf("%s holds %d rows", table, n))
		}
	}
	pool.Close()
	os.Exit(code)
}

// bench is one test's wiring: the fixture database through the counting
// pool, a fresh harness (fresh keys and JWK Set server per test), the real
// resolvers, the shipped Guards, the seam double and the module's routes.
type bench struct {
	pool *pgxpool.Pool
	kit  *testkit.Harness
	mux  *http.ServeMux
	seam *pgxAuditSeam
	keys *SigningKeys
	svc  *Service
}

func benchFor(t *testing.T) *bench {
	t.Helper()
	if sharedPool == nil {
		t.Skip(dbEnvVar + " is not set: the acceptance tests need the throwaway database with migrations applied")
	}
	b := &bench{pool: sharedPool, kit: testkit.NewHarness(t)}
	b.kit.WireFixtureOrgs(t)
	orgs, err := authz.NewPgxOrgResolver(b.pool)
	if err != nil {
		t.Fatalf("org resolver: %v", err)
	}
	gate, err := authz.NewGate(b.kit.Verifier, b.kit.Membership, orgs)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	staff, err := identity.NewStaffAuthorityResolver(b.pool)
	if err != nil {
		t.Fatalf("staff resolver: %v", err)
	}
	portal, err := identity.NewPortalRoleResolver(b.pool)
	if err != nil {
		t.Fatalf("portal resolver: %v", err)
	}
	g, err := guards.NewGuards(gate, staff, portal, nil)
	if err != nil {
		t.Fatalf("guards: %v", err)
	}
	store, err := NewStore(b.pool)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	b.seam = newPgxAuditSeam(b.pool)
	b.keys = &SigningKeys{KeyID: "fx-accept-key", Key: bytes.Repeat([]byte{0x51}, 32)}
	b.svc, err = NewService(store, b.seam, b.keys, Config{Now: b.kit.Clock.Now})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	b.mux = http.NewServeMux()
	RegisterRoutes(b.mux, g, b.svc)
	return b
}

// request helpers

func doReq(t *testing.T, mux *http.ServeMux, method, target, bearer string, headers map[string]string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rd = bytes.NewReader(payload)
	}
	req := httptest.NewRequest(method, target, rd)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type errorBody struct {
	Error string `json:"error"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response (%d): %v: %s", rec.Code, err, rec.Body.String())
	}
}

func assertStatus(t *testing.T, leg string, rec *httptest.ResponseRecorder, want int) errorBody {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("%s: status %d, want %d, body %s", leg, rec.Code, want, rec.Body.String())
	}
	var e errorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return e
}

// fixture quote inputs: two priced selection/commodity lines whose supplier
// horizon sits a week out, so the default requested window caps to it.

func fixtureLines(horizon time.Time) []Line {
	return []Line{
		{
			ID: "fxl1", Name: "Fixture Deck Board", Qty: "120", UOM: "EA", UnitCostCents: 2400,
			PriceSource: PriceSourceERP, PriceValidUntil: &horizon, Presentation: PresentationSelection,
			SKU: "FX-SKU-0001", Description: "Fixture deck board selection", ImageURL: "/fx/deck.png",
		},
		{
			ID: "fxl2", Name: "Fixture Joist Hanger", Qty: "40", UOM: "EA", UnitCostCents: 310,
			PriceSource: PriceSourceQuoted, PriceValidUntil: &horizon, Presentation: PresentationCommodity,
			SKU: "FX-SKU-0002", IsSpecialOrder: false,
		},
	}
}

func fixtureCreate(horizon time.Time) CreateInput {
	return CreateInput{
		Customer:      CustomerContact{Name: "Fixture Homeowner One"},
		Lines:         fixtureLines(horizon),
		LabourLines:   []LabourLine{{ID: "fxlab1", Description: "Fixture crew", RateType: "flat", RateCents: 125000}},
		OverheadLines: []OverheadLine{{ID: "fxovh1", Label: "Fixture permit", AmountType: "flat", Value: 7500}},
	}
}

func (b *bench) createQuote(t *testing.T, in CreateInput) TenantQuote {
	t.Helper()
	rec := doReq(t, b.mux, http.MethodPost, "/api/v1/tenant/customer-quotes",
		b.kit.Minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID), nil, in)
	assertStatus(t, "create", rec, http.StatusCreated)
	var out TenantQuote
	decode(t, rec, &out)
	return out
}

func (b *bench) sendQuote(t *testing.T, id string) SendResult {
	t.Helper()
	rec := doReq(t, b.mux, http.MethodPost, "/api/v1/tenant/customer-quotes/"+id+"/send",
		b.kit.Minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID), nil, struct{}{})
	assertStatus(t, "send", rec, http.StatusOK)
	var out SendResult
	decode(t, rec, &out)
	return out
}

func (b *bench) acceptURL(token string) string {
	return "/api/public/v1/customer-quotes/" + token + "/accept"
}

func (b *bench) viewURL(token string) string {
	return "/api/public/v1/customer-quotes/" + token
}

func (b *bench) publicAccept(t *testing.T, token, name string, accepted bool) *httptest.ResponseRecorder {
	t.Helper()
	return doReq(t, b.mux, http.MethodPost, b.acceptURL(token), "", nil,
		AcceptRequest{TypedName: name, Accepted: accepted})
}

func (b *bench) publicView(t *testing.T, token string) *httptest.ResponseRecorder {
	t.Helper()
	return doReq(t, b.mux, http.MethodGet, b.viewURL(token), "", nil, nil)
}

func (b *bench) scalarInt(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := b.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("scalar: %v", err)
	}
	return n
}

func (b *bench) scalarStr(t *testing.T, sql string, args ...any) string {
	t.Helper()
	var s string
	if err := b.pool.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		t.Fatalf("scalar: %v", err)
	}
	return s
}

// T1: a homeowner accepts a customer quote with a token and no account.
func TestT1HomeownerAcceptsWithTokenAndNoAccount(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	sent := b.sendQuote(t, created.ID)
	if sent.MagicLinkToken == "" || sent.Status != StatusSent {
		t.Fatalf("send did not return a raw token with status sent")
	}

	// The identity tables' zero-delta assertion (assert 4): counts before.
	usersBefore := b.scalarInt(t, `SELECT count(*) FROM users`)
	portalBefore := b.scalarInt(t, `SELECT count(*) FROM portal_users`)
	tenantsBefore := b.scalarInt(t, `SELECT count(*) FROM customers`)

	// Act: no Authorization header, no cookie, no identity material.
	rec := b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "accept with token only", rec, http.StatusOK)
	var result AcceptanceResult
	decode(t, rec, &result)

	// Assert 1: the result restates the agreed totals, computed by the core.
	wantTotals, err := ComputeTotals(fixtureLines(horizon), defaultMarkupBps,
		fixtureCreate(horizon).LabourLines, fixtureCreate(horizon).OverheadLines)
	if err != nil {
		t.Fatalf("totals: %v", err)
	}
	if result.TotalCents != wantTotals.GrandCents || result.MaterialsCents != wantTotals.MaterialsCents {
		t.Fatalf("result totals %+v, want grand %d materials %d", result, wantTotals.GrandCents, wantTotals.MaterialsCents)
	}

	// Assert 2: the quote's status is accepted.
	if status := b.scalarStr(t, `SELECT status FROM customer_quote WHERE id = $1`, created.ID); status != string(StatusAccepted) {
		t.Fatalf("quote status %s, want accepted", status)
	}

	// Assert 3: the acceptance row carries the typed name, the affirmative,
	// the served consent constant and the server-computed total.
	var signedName string
	var affirmative bool
	var consent string
	var total int64
	if err := b.pool.QueryRow(ctx,
		`SELECT signed_name, affirmative, consent_text, accepted_total_cents FROM quote_acceptance WHERE id = $1`,
		result.AcceptanceID).Scan(&signedName, &affirmative, &consent, &total); err != nil {
		t.Fatalf("acceptance row: %v", err)
	}
	if signedName != "Fixture Homeowner One" || !affirmative || consent != ConsentText || total != wantTotals.GrandCents {
		t.Fatalf("acceptance row: name %q affirmative %v consent-ok %v total %d", signedName, affirmative, consent == ConsentText, total)
	}

	// Assert 4: zero writes to any identity table (D23).
	if got := b.scalarInt(t, `SELECT count(*) FROM users`); got != usersBefore {
		t.Fatalf("users table moved from %d to %d", usersBefore, got)
	}
	if got := b.scalarInt(t, `SELECT count(*) FROM portal_users`); got != portalBefore {
		t.Fatalf("portal_users table moved from %d to %d", portalBefore, got)
	}
	if got := b.scalarInt(t, `SELECT count(*) FROM customers`); got != tenantsBefore {
		t.Fatalf("customers table moved from %d to %d", tenantsBefore, got)
	}

	// Assert 5: the audit row's actorship is the link, never a person.
	var acting *string
	var refType, tokenHashStored string
	if err := b.pool.QueryRow(ctx,
		`SELECT acting_platform_user_id::text, changes->>'ref_type', changes->>'token_hash'
		 FROM audit_log WHERE entity_type = $1 AND entity_id = $2::uuid AND action = $3`,
		AuditEntityType, result.AcceptanceID, AuditAction).Scan(&acting, &refType, &tokenHashStored); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if acting != nil && *acting != "" {
		t.Fatalf("audit row names an acting user; actorship must be the link")
	}
	if refType != RefTypeShareToken {
		t.Fatalf("audit ref_type %q, want %q", refType, RefTypeShareToken)
	}
	if decoded, err := separatorDecodeHex(tokenHashStored); err != nil || decoded != HashToken(sent.MagicLinkToken) {
		t.Fatalf("audit token hash does not name the accepting token")
	}

	// Assert 6: the audit row carries the artifact facts, and the independent
	// verification over the row and the audit row succeeds.
	var hashStored, sigStored, alg, keyID string
	if err := b.pool.QueryRow(ctx,
		`SELECT changes->>'artifact_hash', changes->>'artifact_sig', changes->>'alg', changes->>'key_id'
		 FROM audit_log WHERE entity_type = $1 AND entity_id = $2::uuid AND action = $3`,
		AuditEntityType, result.AcceptanceID, AuditAction).Scan(&hashStored, &sigStored, &alg, &keyID); err != nil {
		t.Fatalf("audit artifact fields: %v", err)
	}
	if alg != ArtifactAlg || keyID != b.keys.KeyID {
		t.Fatalf("audit alg %q key id %q", alg, keyID)
	}
	// The stored values survived the chokepoint verbatim: the separator
	// encoding means no seven-digit run, so no "[redacted]".
	if strings.Contains(hashStored, "[redacted]") || strings.Contains(sigStored, "[redacted]") {
		t.Fatalf("the artifact signature was corrupted by the audit redaction chokepoint")
	}
	verification, err := b.svc.VerifyAcceptance(ctx, result.AcceptanceID)
	if err != nil || !verification.OK {
		t.Fatalf("independent verification failed: %+v %v", verification, err)
	}

	// S1 replay: a repeat accept answers 409 naming the standing state, and
	// no second acceptance row exists.
	rec = b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	e := assertStatus(t, "replay accept", rec, http.StatusConflict)
	if e.Error != SentenceAlreadyResponded {
		t.Fatalf("replay body %q, want the already-responded sentence", e.Error)
	}
	if got := b.scalarInt(t, `SELECT count(*) FROM quote_acceptance WHERE customer_quote_id = $1`, created.ID); got != 1 {
		t.Fatalf("replay created a second acceptance row (%d rows)", got)
	}
	verification, err = b.svc.VerifyAcceptance(ctx, result.AcceptanceID)
	if err != nil || !verification.OK {
		t.Fatalf("verification after replay failed: %+v %v", verification, err)
	}

	// The view still serves: status accepted, acceptable false.
	rec = b.publicView(t, sent.MagicLinkToken)
	assertStatus(t, "view after accept", rec, http.StatusOK)
	var view PublicView
	decode(t, rec, &view)
	if view.Status != StatusAccepted || view.Acceptable {
		t.Fatalf("post-accept view status %s acceptable %v", view.Status, view.Acceptable)
	}

	// S3: X-Org (and an irrelevant Authorization header) have zero effect on
	// the public leg: two warm views, byte for byte identical.
	warm := b.publicView(t, sent.MagicLinkToken)
	bare := b.publicView(t, sent.MagicLinkToken)
	withOrg := doReq(t, b.mux, http.MethodGet, b.viewURL(sent.MagicLinkToken), "", map[string]string{"X-Org": "fixture-north"}, nil)
	withAuth := doReq(t, b.mux, http.MethodGet, b.viewURL(sent.MagicLinkToken), "definitely-not-a-bearer", nil, nil)
	if bare.Code != warm.Code || withOrg.Code != warm.Code || withAuth.Code != warm.Code {
		t.Fatalf("public view codes differ across header variants")
	}
	if bare.Body.String() != warm.Body.String() || withOrg.Body.String() != warm.Body.String() || withAuth.Body.String() != warm.Body.String() {
		t.Fatalf("X-Org or Authorization changed the public view body")
	}
	// The view carries the allowlist: no org or tenant id, no token state.
	body := bare.Body.String()
	if strings.Contains(body, testkit.OrgNorthID) || strings.Contains(body, testkit.TenantAlphaID) {
		t.Fatalf("public view leaks org or tenant ids")
	}
	if strings.Contains(body, "v1:") || strings.Contains(body, sent.MagicLinkToken) {
		t.Fatalf("public view leaks token state")
	}
}

// T2: a revised quote's old token is dead with 410, not 404, across every
// generation (the append-only dead list; the single-tombstone mutation M2c
// kills exactly at the third generation, and the demonstrated run is in the
// lane's evidence).
func TestT2RevisedQuoteOldTokenAnswers410Never404(t *testing.T) {
	b := benchFor(t)
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	first := b.sendQuote(t, created.ID)

	// Revise: the token rotates the moment the intent to change forms.
	rec := doReq(t, b.mux, http.MethodPost, "/api/v1/tenant/customer-quotes/"+created.ID+"/revise",
		b.kit.Minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID), nil, struct{}{})
	assertStatus(t, "revise", rec, http.StatusOK)
	var revised TenantQuote
	decode(t, rec, &revised)

	// Assert 3: the status is draft and the live slot is empty.
	if revised.Status != StatusDraft || revised.HasLiveToken {
		t.Fatalf("after revise: status %s has-live %v, want draft with an empty live slot", revised.Status, revised.HasLiveToken)
	}
	stored := b.scalarStr(t, `SELECT share_token_hash FROM customer_quote WHERE id = $1`, created.ID)
	state, err := ParseTokenState(stored)
	if err != nil {
		t.Fatalf("stored token value after revise: %v", err)
	}
	if state.Live != "" {
		t.Fatalf("live slot after revise holds %s, want empty", state.Live)
	}
	if !state.ContainsDead(HashToken(first.MagicLinkToken)) {
		t.Fatalf("the first token's hash is not in the dead list after revise")
	}

	// Asserts 1 and 2: the old token answers 410 on both operations, never
	// 404, with the dead-link body.
	viewRec := b.publicView(t, first.MagicLinkToken)
	if e := assertStatus(t, "old token view", viewRec, http.StatusGone); e.Error != SentenceLinkNoLongerValid {
		t.Fatalf("old token view body %q", e.Error)
	}
	acceptRec := b.publicAccept(t, first.MagicLinkToken, "Fixture Homeowner One", true)
	if e := assertStatus(t, "old token accept", acceptRec, http.StatusGone); e.Error != SentenceLinkNoLongerValid {
		t.Fatalf("old token accept body %q", e.Error)
	}

	// Assert 4: the flow continues. A fresh send mints a fresh token; the
	// first stays dead; the fresh token accepts; its replay answers 409; the
	// fresh token's view stays live-or-state per its own state.
	second := b.sendQuote(t, created.ID)
	if second.MagicLinkToken == "" || second.MagicLinkToken == first.MagicLinkToken {
		t.Fatalf("the second send did not mint a fresh token")
	}
	if rec := b.publicView(t, first.MagicLinkToken); rec.Code != http.StatusGone {
		t.Fatalf("first token after second send: %d, want 410", rec.Code)
	}
	rec = b.publicAccept(t, second.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "accept with second token", rec, http.StatusOK)
	var result AcceptanceResult
	decode(t, rec, &result)
	if rec := b.publicAccept(t, second.MagicLinkToken, "Fixture Homeowner One", true); rec.Code != http.StatusConflict {
		t.Fatalf("second token replay: %d, want 409", rec.Code)
	}
	if rec := b.publicView(t, second.MagicLinkToken); rec.Code != http.StatusOK {
		t.Fatalf("second token view after acceptance: %d, want 200", rec.Code)
	}

	// Assert 5: a third generation, on a fresh quote whose flow does not
	// accept (an accepted quote is never revisable, so the generations case
	// drives its own row). Revise and send once more; every retired token,
	// all generations, answers 410 while the newest is live. In this flow
	// the second send displaces nothing (revise already emptied the live
	// slot), and the second revise retires the second generation beside the
	// first: the exact point a single-tombstone design loses the first
	// generation (the M2c kill).
	thirdQuote := b.createQuote(t, fixtureCreate(horizon))
	gen1 := b.sendQuote(t, thirdQuote.ID)
	rec = doReq(t, b.mux, http.MethodPost, "/api/v1/tenant/customer-quotes/"+thirdQuote.ID+"/revise",
		b.kit.Minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID), nil, struct{}{})
	assertStatus(t, "fresh quote revise one", rec, http.StatusOK)
	gen2 := b.sendQuote(t, thirdQuote.ID)
	rec = doReq(t, b.mux, http.MethodPost, "/api/v1/tenant/customer-quotes/"+thirdQuote.ID+"/revise",
		b.kit.Minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID), nil, struct{}{})
	assertStatus(t, "fresh quote revise two", rec, http.StatusOK)
	gen3 := b.sendQuote(t, thirdQuote.ID)
	for i, tok := range []string{gen1.MagicLinkToken, gen2.MagicLinkToken} {
		if rec := b.publicView(t, tok); rec.Code != http.StatusGone {
			t.Fatalf("retired generation %d answers %d on view, want 410 (the single-tombstone mutation dies here)", i+1, rec.Code)
		}
		if rec := b.publicAccept(t, tok, "Fixture Homeowner One", true); rec.Code != http.StatusGone {
			t.Fatalf("retired generation %d answers %d on accept, want 410", i+1, rec.Code)
		}
	}
	if rec := b.publicView(t, gen3.MagicLinkToken); rec.Code != http.StatusOK {
		t.Fatalf("the live third generation answers %d on view, want 200", rec.Code)
	}
	rec = b.publicAccept(t, gen3.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "accept with the live third generation", rec, http.StatusOK)
}
