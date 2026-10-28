//go:build integration

// ar_empty_list_integration_test.go, W2-4: the till's list answers are empty JSON arrays, never
// null. On a real core with no bounced payment, GET /api/v1/ar/bounced answered {"items": null}
// and the Money Desk's receivables area crashed mapping over the null (Cannot read properties of
// null (reading 'map')); the web suites never saw it because the stub always answers an array.
// The sweep over the till module's other list-carrying endpoints found the same nil slice shape
// on the credit hold list (items, the desk maps over it too), the session detail (tenders,
// movements and slip_candidates, arrays the contract requires) and the Z report (per_tender,
// which the Counter Till's close screen maps over unguarded). The payment and invoice history
// reads already initialize their slices and are pinned here by no change.
package till

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func newArEmptyFixture(t *testing.T, pool *pgxpool.Pool, linkedUserID string) *arEmptyFixture {
	t.Helper()
	f := &arEmptyFixture{
		mux:     http.NewServeMux(),
		harness: testkit.NewHarness(t),
		subject: map[string]identity.StaffAuthority{},
	}
	f.harness.Resolver.Set(testkit.OrgNorthSlug, testkit.OrgNorthID)
	// The production caller seam: the service reads the StaffContext the guard stamps onto the
	// request (NewService's default), never a test caller function. Every subject links to one
	// seeded users.id, the column the drawer writes name (opened_by, counted_by).
	svc := NewService(NewRepository(pool))
	h := NewHandler(svc, nil).WithUserLinker(func(_ context.Context, _ string) (string, bool) {
		return linkedUserID, true
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
	return f.do(t, sub, http.MethodGet, path, nil)
}

// post bodies a JSON request, so a leg can drive a write through the route the way the app does.
func (f *arEmptyFixture) post(t *testing.T, sub, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, sub, http.MethodPost, path, body)
}

func (f *arEmptyFixture) do(t *testing.T, sub, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+f.harness.Minter.StaffToken(t, sub))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

// openEmptyDrawer opens a drawer with a zero float through the route, on the production caller
// path, and returns its id: the drawer these legs read is one no money ever moved in.
func (f *arEmptyFixture) openEmptyDrawer(t *testing.T, branchID string) string {
	t.Helper()
	rec := f.post(t, "ar-empty-bm", "/api/v1/till/sessions",
		map[string]any{"branch_id": branchID, "station": "AR-EMPTY", "float_cents": 0})
	if rec.Code != http.StatusCreated {
		t.Fatalf("open drawer: got %d %s, want 201", rec.Code, rec.Body.String())
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == "" {
		t.Fatalf("open drawer: decode %s: %v", rec.Body.String(), err)
	}
	return out.ID
}

// assertArrayField fails unless the named field is present and exactly the empty JSON array.
// null and [] are different JSON values, and a render that maps over the field crashes on null.
func assertArrayField(t *testing.T, rec *httptest.ResponseRecorder, what, field string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: got %d %s, want 200", what, rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode %s: %v", what, rec.Body.String(), err)
	}
	raw, ok := body[field]
	if !ok {
		t.Fatalf("%s: no %s key in %s", what, field, rec.Body.String())
	}
	if got := strings.TrimSpace(string(raw)); got != "[]" {
		t.Fatalf("%s: %s is %s, want the empty array [] not null (a render maps over it)", what, field, got)
	}
}

func assertArEmptyItems(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	assertArrayField(t, rec, what, "items")
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
	branchID, userID := seedBranchUser(t, pool)
	f := newArEmptyFixture(t, pool, userID)

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

// The session detail read on a drawer that has taken no money and seen no movement: tenders,
// movements and slip_candidates are contract arrays, and the Counter Till's close flow reads them.
func TestIntegration_SessionDetailWithAnEmptyDrawerAnswersArrays(t *testing.T) {
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	f := newArEmptyFixture(t, pool, userID)
	f.person("ar-empty-bm", identity.StaffAuthority{RoleID: "branch_manager",
		Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{branchID}})

	sid := f.openEmptyDrawer(t, branchID)
	t.Cleanup(func() { cleanupSession(pool, sid) })

	rec := f.get(t, "ar-empty-bm", "/api/v1/till/sessions/"+sid)
	assertArrayField(t, rec, "the session detail's tenders on an empty drawer", "tenders")
	assertArrayField(t, rec, "the session detail's movements on an empty drawer", "movements")
	assertArrayField(t, rec, "the session detail's slip candidates on an empty drawer", "slip_candidates")
}

// The Z report of a drawer that has taken no money: per_tender is a contract array and the
// Counter Till's close screen maps over it with no null guard, so null there is the desk's
// crash on the till's own close.
func TestIntegration_ZReportWithAnEmptyDrawerAnswersArrays(t *testing.T) {
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	f := newArEmptyFixture(t, pool, userID)
	f.person("ar-empty-bm", identity.StaffAuthority{RoleID: "branch_manager",
		Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{branchID}})

	sid := f.openEmptyDrawer(t, branchID)
	t.Cleanup(func() { cleanupSession(pool, sid) })

	rec := f.get(t, "ar-empty-bm", "/api/v1/till/sessions/"+sid+"/zreport")
	assertArrayField(t, rec, "the Z report's per_tender on an empty drawer", "per_tender")
	assertArrayField(t, rec, "the Z report's movements on an empty drawer", "movements")
}

func TestIntegration_CreditHoldsWithNoHoldsAnswerAnEmptyArray(t *testing.T) {
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	f := newArEmptyFixture(t, pool, userID)

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
