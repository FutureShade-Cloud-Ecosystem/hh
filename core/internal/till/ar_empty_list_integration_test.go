//go:build integration

// ar_empty_list_integration_test.go, W2-4: the AR list reads the Money Desk maps over answer an
// empty JSON array, never null. On a real core with no bounced payment, GET /api/v1/ar/bounced
// answered {"items": null} and the desk's receivables area crashed mapping over the null
// (Cannot read properties of null (reading 'map')); the web suites never saw it because the
// stub always answers an array. The route, as a manager, must answer items: [].
package till

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// arEmptyAuthorities resolves each test subject to the authority it was planted with, the same
// shape the route binding tests use. Authority always resolves through the stub, never from the
// token (D3).
type arEmptyAuthorities struct {
	bySubject map[string]identity.StaffAuthority
}

func (s arEmptyAuthorities) ResolveStaff(_ context.Context, subject, _ string) (identity.StaffAuthority, bool, error) {
	a, ok := s.bySubject[subject]
	return a, ok, nil
}

type arEmptyNoPortal struct{}

func (arEmptyNoPortal) PortalRole(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

type arEmptyKnob struct{}

func (arEmptyKnob) DriverSessionHours(context.Context, string) (int, error) { return 12, nil }

// arEmptyFixture serves the real till routes over the REAL repository on the gate database,
// behind the real guards: the production path from the bearer to the JSON body, with only the
// membership, resolver and authority sources doubled (they are DB free by design).
type arEmptyFixture struct {
	mux     *http.ServeMux
	harness *testkit.Harness
	subject map[string]identity.StaffAuthority
}

func newArEmptyFixture(t *testing.T, pool *pgxpool.Pool) *arEmptyFixture {
	t.Helper()
	f := &arEmptyFixture{
		mux:     http.NewServeMux(),
		harness: testkit.NewHarness(t),
		subject: map[string]identity.StaffAuthority{},
	}
	f.harness.Resolver.Set(testkit.OrgNorthSlug, testkit.OrgNorthID)
	// The production caller seam: the service reads the StaffContext the guard stamps onto the
	// request (NewService's default), never a test caller function.
	svc := NewService(NewRepository(pool))
	h := NewHandler(svc, nil).WithUserLinker(func(_ context.Context, _ string) (string, bool) {
		return "ar-empty-user", true
	})
	g, err := guards.NewGuards(f.harness.Gate, arEmptyAuthorities{f.subject}, arEmptyNoPortal{}, arEmptyKnob{})
	if err != nil {
		t.Fatalf("guards: %v", err)
	}
	h.RegisterRoutes(f.mux, g)
	return f
}

func (f *arEmptyFixture) person(sub string, a identity.StaffAuthority) {
	f.subject[sub] = a
	f.harness.Membership.SetMember(sub, testkit.OrgNorthSlug, true)
}

func (f *arEmptyFixture) get(t *testing.T, sub, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+f.harness.Minter.StaffToken(t, sub))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

// assertArEmptyItems fails unless the body carries items as the empty JSON array. null and []
// are different JSON values, and the desk's render maps over items, so only [] is acceptable.
func assertArEmptyItems(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: got %d %s, want 200", what, rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode %s: %v", what, rec.Body.String(), err)
	}
	raw, ok := body["items"]
	if !ok {
		t.Fatalf("%s: no items key in %s", what, rec.Body.String())
	}
	if got := strings.TrimSpace(string(raw)); got != "[]" {
		t.Fatalf("%s: items is %s, want the empty array [] not null (the desk's render maps over it)", what, got)
	}
}

// arEmptyCount reads one count off the gate database, for the shared database guard below.
func arEmptyCount(t *testing.T, pool *pgxpool.Pool, q string) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(t.Context(), q).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestIntegration_BouncedQueueWithNoBouncesAnswersAnEmptyArray(t *testing.T) {
	pool := intPool(t)
	branchID, _ := seedBranchUser(t, pool)
	f := newArEmptyFixture(t, pool)

	// The Money Desk's payables and receivables role: tier 2, global scope, the caller the
	// receivables area signs in as. This is the caller whose read crashed the desk.
	f.person("ar-empty-pr", identity.StaffAuthority{RoleID: "payables_receivables",
		Tier: identity.TierManager, Scope: identity.ScopeGlobal})
	// A branch manager reads the same route through the service's branch filter path.
	f.person("ar-empty-bm", identity.StaffAuthority{RoleID: "branch_manager",
		Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{branchID}})

	// The exact ref search answers the empty shape whatever else the database carries: this ref
	// matches no payment, so the queue is empty for any caller on any database.
	assertArEmptyItems(t, f.get(t, "ar-empty-pr", "/api/v1/ar/bounced?gateway_ref=ar-empty-none"),
		"the bounced queue by an unknown gateway_ref")
	assertArEmptyItems(t, f.get(t, "ar-empty-bm", "/api/v1/ar/bounced?gateway_ref=ar-empty-none"),
		"the bounced queue by an unknown gateway_ref as a branch manager")

	// The desk's own call, no filter, on a database with no bounced payment. On a shared gate
	// database an earlier package's leftover bounce would make the list legitimately non empty,
	// so the leg stands only when the table holds none (a fresh seeded database holds none).
	if n := arEmptyCount(t, pool, `SELECT count(*) FROM payments WHERE status = 'BOUNCED'`); n != 0 {
		t.Logf("skipping the unfiltered legs: the shared database carries %d bounced payments", n)
	} else {
		assertArEmptyItems(t, f.get(t, "ar-empty-pr", "/api/v1/ar/bounced"),
			"the bounced queue as the payables and receivables manager")
		assertArEmptyItems(t, f.get(t, "ar-empty-bm", "/api/v1/ar/bounced"),
			"the bounced queue as a branch manager")
	}
}

func TestIntegration_CreditHoldsWithNoHoldsAnswerAnEmptyArray(t *testing.T) {
	pool := intPool(t)
	branchID, _ := seedBranchUser(t, pool)
	f := newArEmptyFixture(t, pool)

	f.person("ar-empty-pr", identity.StaffAuthority{RoleID: "payables_receivables",
		Tier: identity.TierManager, Scope: identity.ScopeGlobal})
	f.person("ar-empty-bm", identity.StaffAuthority{RoleID: "branch_manager",
		Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{branchID}})

	// The same shared database guard as the bounced queue above: the unfiltered legs stand only
	// when the hold table is empty, which a fresh seeded database is.
	if n := arEmptyCount(t, pool, `SELECT count(*) FROM credit_holds`); n != 0 {
		t.Logf("skipping the unfiltered legs: the shared database carries %d credit holds", n)
	} else {
		assertArEmptyItems(t, f.get(t, "ar-empty-pr", "/api/v1/ar/credit-holds"),
			"the credit hold list as the payables and receivables manager")
		assertArEmptyItems(t, f.get(t, "ar-empty-bm", "/api/v1/ar/credit-holds"),
			"the credit hold list as a branch manager")
		assertArEmptyItems(t, f.get(t, "ar-empty-pr", "/api/v1/ar/credit-holds?status=ACTIVE"),
			"the credit hold list filtered to ACTIVE")
	}
}
