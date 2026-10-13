package main

// compfixture_test.go is the composition fixtures of the Phase C
// integration test migration: the parity and x-role suites drive the
// fold's registerRoutes(r, getenv) so the census covers the FULL served
// surface, exactly as the binary wires it (the A6 review's integration
// steps and the C5 recipe's F-1). The construction needs the identity
// environment the serve role reads (issuer, audiences, JWKS URL, database,
// the Appwrite membership endpoint) plus the customer-quote signing keys
// the audit re-point made a spawn requirement; every value here is an
// invented fixture value served from doubles the test controls. The pool
// behind the map is a throwaway scratch database with the frozen migration
// set applied and the kit's synthetic fixture rows built, named by
// HH_CORE_TEST_DATABASE_URL (the tree's gate-variable pattern); absent the
// variable these suites skip politely, and the acceptance run sets it and
// asserts zero skips.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// coreDBEnvVar gates the composition suites on the scratch database, the
// same spelling discipline the tree's module suites use (KIT_, HH_B3_,
// HH_B4_, HH_B5_, HH_CQ_ before it).
const coreDBEnvVar = "HH_CORE_TEST_DATABASE_URL"

// fixtureAcceptanceKeyID names the fixture signing key for the
// customer-quote artifact schedule; a code-shaped value under the pinned
// key-id shape rule.
const fixtureAcceptanceKeyID = "fx-accept-sign-key"

// xrNoPortalRowSub is the platform subject that HOLDS a tenant team in the
// membership double but has NO portal_users row in the fixture database:
// the no-row leg (membership passes, authority absent), which every tenant
// route class refuses with the shared 403 (FROM-PRINCIPAL 10).
const xrNoPortalRowSub = "fxportal-norow-alpha"

// appwriteFake serves the Appwrite membership listing shape over invented
// fixture principals: staff subjects hold the one org-<slug> team
// membership, portal subjects hold exactly one ten-<slug> team,
// and an unknown subject holds nothing. It answers the one endpoint the
// membership source reads; no real directory is ever contacted.
type appwriteFake struct {
	mu     sync.Mutex
	teams  map[string][]string // platform subject -> team ids
	server *httptest.Server
}

func newAppwriteFake(t *testing.T) *appwriteFake {
	t.Helper()
	f := &appwriteFake{teams: make(map[string][]string)}
	mux := http.NewServeMux()
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/users/")
		sub, ok := strings.CutSuffix(rest, "/memberships")
		if !ok || sub == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.mu.Lock()
		teams := append([]string(nil), f.teams[sub]...)
		f.mu.Unlock()
		members := make([]map[string]string, 0, len(teams))
		for _, team := range teams {
			members = append(members, map[string]string{"teamId": team, "userId": sub})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"total": len(members), "memberships": members})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *appwriteFake) setTeam(sub string, teams ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teams[sub] = teams
}

// wireFixtureTeams points the membership double at the fixture database's
// org facts: the staff principals hold the north org team, the portal
// principals hold exactly one tenant team each (alpha and beta in the
// north org, gamma in the south), and the no-row subject holds a team
// with no portal row behind it.
func (f *appwriteFake) wireFixtureTeams() {
	orgNorth := "org-" + testkit.OrgNorthSlug
	for _, sub := range []string{
		testkit.SubInsideSales, testkit.SubSalesManager, testkit.SubGeneralMgr,
		testkit.SubTwoOrg, testkit.SubRevoked, testkit.SubInert, testkit.SubDriver, testkit.SubSuspended,
	} {
		f.setTeam(sub, orgNorth)
	}
	f.setTeam(testkit.SubPortalAdminAlpha, "ten-"+testkit.OrgNorthSlug)
	f.setTeam(testkit.SubPortalCrewAlpha, "ten-"+testkit.OrgNorthSlug)
	f.setTeam(testkit.SubPortalBuyerNoPayAlpha, "ten-"+testkit.OrgNorthSlug)
	f.setTeam(testkit.SubPortalAdminBeta, "ten-"+testkit.OrgNorthSlug)
	f.setTeam(testkit.SubPortalCrewBeta, "ten-"+testkit.OrgNorthSlug)
	f.setTeam(testkit.SubPortalBuyerBeta, "ten-"+testkit.OrgNorthSlug)
	f.setTeam(testkit.SubPortalAdminGamma, "ten-"+testkit.OrgSouthSlug)
	f.setTeam(testkit.SubPortalApGamma, "ten-"+testkit.OrgSouthSlug)
	f.setTeam(xrNoPortalRowSub, "ten-"+testkit.OrgNorthSlug)
}

// compEnv is the identity environment map the composition suites hand
// registerRoutes: the kit's issuer and audiences against the kit's served
// JWK Set, the membership double's endpoint, the gate database, and the
// acceptance signing key the fixture generates at runtime (never
// persisted, never printed).
type compEnv struct {
	keys     *testkit.Keys
	minter   *testkit.Minter
	keySrv   *testkit.KeyServer
	appwrite *appwriteFake
	dsn      string
	values   map[string]string
	getenv   func(string) string
}

// set adds or overrides one environment value the composition reads:
// the tests that flip a switch use it.
func (e *compEnv) set(k, v string) {
	e.values[k] = v
}

// fixturePool opens a pool on the fixture database, for the tests that
// plant fixture rows beside the kit's own; the caller closes it.
func (e *compEnv) fixturePool() *pgxpool.Pool {
	pool, err := pgxpool.New(context.Background(), e.dsn)
	if err != nil {
		panic("compfixture: fixture pool: " + err.Error())
	}
	return pool
}

// newCompEnv builds the environment and the fixture rows. It skips the
// test politely when the gate variable is unset.
func newCompEnv(t *testing.T) *compEnv {
	t.Helper()
	dsn := os.Getenv(coreDBEnvVar)
	if dsn == "" {
		t.Skip(coreDBEnvVar + " is not set: the composition suites need the scratch database with the frozen migrations applied")
	}
	// Validate refuses an unset APP_ENV (HH-198); the composition suites are dev wiring.
	t.Setenv("APP_ENV", "dev")
	ctx := context.Background()
	fixPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("fixture pool: %v", err)
	}
	db := testkit.PgxPool(fixPool)
	if err := testkit.VerifyBaseline(ctx, db); err != nil {
		fixPool.Close()
		t.Fatalf("fixture baseline: %v", err)
	}
	if err := testkit.Build(ctx, db); err != nil {
		fixPool.Close()
		t.Fatalf("fixture build: %v", err)
	}
	fixPool.Close()

	keys := testkit.NewKeys(t)
	env := &compEnv{
		keys:     keys,
		minter:   &testkit.Minter{Keys: keys, Clock: testkit.NewClock()},
		keySrv:   testkit.NewKeyServer(t, keys),
		appwrite: newAppwriteFake(t),
		dsn:      dsn,
	}
	env.appwrite.wireFixtureTeams()

	// The acceptance signing key: 32 CSPRNG bytes, hex, generated per run
	// exactly the way the kit generates every other key it holds.
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate acceptance key: %v", err)
	}
	values := map[string]string{
		"CORE_TOKEN_ISSUER":      testkit.KitIssuer,
		"CORE_STAFF_AUDIENCE":    testkit.KitStaffAud,
		"CORE_TENANT_AUDIENCE":   testkit.KitTenantAud,
		"CORE_JWKS_URL":          env.keySrv.URL(),
		"CORE_DATABASE_URL":      dsn,
		"CORE_APPWRITE_ENDPOINT": env.appwrite.server.URL,
		"CORE_APPWRITE_PROJECT":  "fx-appwrite-project",
		"CORE_APPWRITE_API_KEY":  "fx-appwrite-key-placeholder",
		customerquote.EnvKeyID:   fixtureAcceptanceKeyID,
		customerquote.EnvKey:     hex.EncodeToString(rawKey),
	}
	consoleSessionValues(t, env.minter, values)
	env.values = values
	env.getenv = func(k string) string { return env.values[k] }
	return env
}

// registeredSurface wires the FULL served surface over the environment:
// the one composition point the binary itself uses, driving a recording
// registrar so the registered patterns are enumerable. The returned graph
// owns a pool, closed with the test.
func registeredSurface(t *testing.T, env *compEnv) (*recordingRegistrar, *appGraph) {
	t.Helper()
	rec := &recordingRegistrar{inner: http.NewServeMux()}
	graph, err := registerRoutes(rec, env.getenv)
	if err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	t.Cleanup(graph.pool.Close)
	return rec, graph
}

// compPlanter builds a customer-quote service over the same environment
// for planting classification tokens through the module's own seams
// (create, send, revise), the same discipline the module's acceptance
// bench uses; the service shares the pool the served graph holds.
func compPlanter(t *testing.T, env *compEnv, pool *pgxpool.Pool) *customerquote.Service {
	t.Helper()
	keys, err := customerquote.LoadSigningKeys(env.getenv)
	if err != nil {
		t.Fatalf("planter keys: %v", err)
	}
	store, err := customerquote.NewStore(pool)
	if err != nil {
		t.Fatalf("planter store: %v", err)
	}
	svc, err := customerquote.NewService(store, cqAuditSeam{svc: audit.NewService(audit.NewRepository(pool), nil)}, keys, customerquote.Config{})
	if err != nil {
		t.Fatalf("planter service: %v", err)
	}
	return svc
}

// cqClassificationTokens plants the customer-quote public pair's three
// token states through the module's seams: one dead token (sent, then
// revised: the rotation retires it into the dead list), and two live
// tokens on fresh quotes (one for the view's live cell, one for the
// accept's, because an acceptance flips the quote's state and would end
// the second cell's live window). The planted rows and any acceptance
// rows the probes write are removed with the test.
type cqClassificationTokens struct {
	absent     string
	dead       string
	liveView   string
	liveAccept string
}

// xrAbsentToken is a well-formed, never-minted raw token: 43 characters of
// the mint's alphabet, so it classifies as unknown rather than malformed
// and the pair answers the never-existed sentence.
const xrAbsentToken = "fxAbsentNeverMintedTokenWellFormed43CharX"

func plantClassificationTokens(t *testing.T, env *compEnv, pool *pgxpool.Pool) *cqClassificationTokens {
	t.Helper()
	svc := compPlanter(t, env, pool)
	scope := customerquote.Scope{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID}
	// mk creates and sends one quote and returns its id and the raw token
	// (the send response is the ONLY place a raw token ever appears).
	mk := func(name string) (string, string) {
		t.Helper()
		horizon := time.Now().Add(24 * time.Hour)
		quote, err := svc.Create(context.Background(), scope, customerquote.CreateInput{
			Customer: customerquote.CustomerContact{Name: name},
			Lines: []customerquote.Line{{
				ID: "fxl1", Name: "Fixture Deck Board", Qty: "10", UOM: "EA", UnitCostCents: 2400,
				PriceSource: customerquote.PriceSourceERP, PriceValidUntil: &horizon, Presentation: customerquote.PresentationSelection,
			}},
		})
		if err != nil {
			t.Fatalf("plant create: %v", err)
		}
		sent, err := svc.Send(context.Background(), scope, quote.ID)
		if err != nil {
			t.Fatalf("plant send: %v", err)
		}
		return quote.ID, sent.MagicLinkToken
	}
	deadID, deadRaw := mk("Fixture Homeowner Dead")
	if _, err := svc.Revise(context.Background(), scope, deadID); err != nil {
		t.Fatalf("plant revise: %v", err)
	}
	_, liveView := mk("Fixture Homeowner View")
	_, liveAccept := mk("Fixture Homeowner Accept")
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit_log WHERE entity_type = 'quote_acceptance'`,
			`DELETE FROM quote_acceptance`,
			`DELETE FROM customer_quote WHERE tenant_id = '` + testkit.TenantAlphaID + `'`,
		} {
			if _, err := pool.Exec(ctx, stmt); err != nil {
				t.Fatalf("classification cleanup: %v", err)
			}
		}
	})
	return &cqClassificationTokens{absent: xrAbsentToken, dead: deadRaw, liveView: liveView, liveAccept: liveAccept}
}
