//go:build integration

// Integration tests run the real pgx Repository against the live Postgres the compose stack
// provisions (migrations 0003 branch roots + 0017 addresses + 0014 default_branch_id). These
// are the US-M7-06 acceptance tests the scope reconciliation (A-1) named as MISSING:
// TestBranches_BothResolve_200 and TestDefaultBranchResolves.
//
// Run with: go test -tags=integration ./internal/branches -run 'TestBranches_BothResolve_200|TestDefaultBranchResolves'
package branches

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// passthrough stands in for the identity RequireStaff guard so the handler test exercises the
// real HTTP path (JSON shape + 200) without wiring the full auth stack.
func passthrough(next http.HandlerFunc) http.HandlerFunc { return next }

// TestBranches_BothResolve_200, US-M7-06: GET /api/v1/branches returns 200 with BOTH seeded
// branches (Trenton/TRT + Kingston/KGN), each carrying a non-placeholder address (0017).
func TestBranches_BothResolve_200(t *testing.T) {
	pool := intPool(t)
	// Guards-swap carry note (HH-3.2 fold): the passthrough stub became the
	// kit's Guards; the branch fence is the assertion, the caller a resolved
	// global authority.
	h := NewHandler(NewService(NewRepository(pool)), nil)
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "general_manager", Tier: 1, Scope: "global"})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/branches", nil)
	req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	svc := NewService(NewRepository(pool))
	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("branch list returned %d branches, want exactly 2 (TRT + KGN)", len(list))
	}
	byCode := map[string]Branch{}
	for _, b := range list {
		byCode[b.Code] = b
	}
	for _, code := range []string{"TRT", "KGN"} {
		b, ok := byCode[code]
		if !ok {
			t.Fatalf("branch %s missing from list: %+v", code, list)
		}
		if b.Name == "" {
			t.Fatalf("branch %s has empty name", code)
		}
		// 0017 loads a real street address; the 0003 placeholder line1 was "TBD, confirm with Dibbits".
		if b.Address.City == "" || b.Address.Line1 == "" || b.Address.Postal == "" {
			t.Fatalf("branch %s has a placeholder/empty address: %+v", code, b.Address)
		}
	}
}

// TestDefaultBranchResolves, US-M7-06: a default branch is resolvable, and it points at one
// of the two seeded branches (Trenton by seed, migration 0014). The list flags it is_default.
func TestDefaultBranchResolves(t *testing.T) {
	pool := intPool(t)
	svc := NewService(NewRepository(pool))
	ctx := context.Background()

	id, err := svc.DefaultBranchID(ctx)
	if err != nil {
		t.Fatalf("DefaultBranchID: %v", err)
	}
	if id == "" {
		t.Fatal("no default branch configured (system_settings.default_branch_id absent)")
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var defaults, matched int
	for _, b := range list {
		if b.IsDefault {
			defaults++
		}
		if b.ID == id {
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("default_branch_id %s does not match exactly one seeded branch (matched %d)", id, matched)
	}
	if defaults != 1 {
		t.Fatalf("expected exactly one branch flagged is_default, got %d", defaults)
	}
}
