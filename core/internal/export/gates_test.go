package export

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// gates_test.go: each export carries exactly the gate of the route it mirrors, plus the caller's
// branch reach. The mirrors are GET /api/v1/finstmt/* (RequireManager, branch fenced by the finstmt
// service) and GET /api/v1/audit (RequireAdmin).

var (
	gateFrontline    = identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	gateBranchMgr    = identity.StaffAuthority{RoleID: "branch_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{"b-1"}}
	gateGlobalMgr    = identity.StaffAuthority{RoleID: "controller", Tier: identity.TierManager, Scope: identity.ScopeGlobal}
	gateAdmin        = identity.StaffAuthority{RoleID: "owner", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
	gateFrontlineBr  = identity.StaffAuthority{RoleID: "counter", Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Branches: []string{"b-1"}}
	gateNoopWorkbook = func() *Workbook {
		wb := New()
		wb.AddSheet("Sheet").AddRow(Str("x"))
		return wb
	}
)

func gateGet(t *testing.T, h *Handler, as identity.StaffAuthority, path string) *httptest.ResponseRecorder {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(as)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func gateHandler() *Handler {
	return NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithClock(auditClock)
}

type gateCase struct {
	name   string
	as     identity.StaffAuthority
	query  string
	want   int
	served bool
}

func runGateCases(t *testing.T, base string, build func() (*Handler, func() bool), cases []gateCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, called := build()
			rec := gateGet(t, h, c.as, base+c.query)
			if rec.Code != c.want {
				t.Fatalf("status %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
			if c.want == http.StatusForbidden {
				// A branch refusal must be the shared 403, byte for byte what the guard itself writes,
				// so a caller cannot tell a reach refusal from a gate refusal.
				shared := httptest.NewRecorder()
				authz.RefuseForbidden(shared)
				if rec.Body.String() != shared.Body.String() {
					t.Fatalf("403 body %q, want the shared 403 body %q", rec.Body.String(), shared.Body.String())
				}
			}
			if called() != c.served {
				t.Fatalf("the finance or audit read ran = %v, want %v: a refused caller must reach no read", called(), c.served)
			}
		})
	}
}

func TestStatementExport_CarriesTheFinanceGateAndTheCallersBranchReach(t *testing.T) {
	build := func() (*Handler, func() bool) {
		f := &fakeStatementReader{wb: gateNoopWorkbook()}
		return gateHandler().WithStatements(f), func() bool { return f.got != (StatementQuery{}) }
	}
	runGateCases(t, "/api/v1/exports/statement.xlsx", build, []gateCase{
		{"frontline refused", gateFrontline, "?report=income-statement&branch_id=b-1", http.StatusForbidden, false},
		{"branch frontline refused", gateFrontlineBr, "?report=income-statement&branch_id=b-1", http.StatusForbidden, false},
		{"out of reach branch refused", gateBranchMgr, "?report=income-statement&branch_id=b-2", http.StatusForbidden, false},
		{"scoped caller naming no branch refused", gateBranchMgr, "?report=income-statement", http.StatusForbidden, false},
		{"scoped caller cannot run the org wide trial balance", gateBranchMgr, "?report=trial-balance&branch_id=b-1", http.StatusForbidden, false},
		{"branch manager served in reach", gateBranchMgr, "?report=income-statement&branch_id=b-1", http.StatusOK, true},
		{"global manager served any branch", gateGlobalMgr, "?report=income-statement&branch_id=b-2", http.StatusOK, true},
		{"global manager served consolidated", gateGlobalMgr, "?report=income-statement", http.StatusOK, true},
		{"global manager served trial balance", gateGlobalMgr, "?report=trial-balance", http.StatusOK, true},
		{"admin served", gateAdmin, "?report=balance-sheet&branch_id=b-9", http.StatusOK, true},
	})
}

func TestTransactionsExport_CarriesTheFinanceGateAndTheCallersBranchReach(t *testing.T) {
	build := func() (*Handler, func() bool) {
		f := &fakeTransactionReader{wb: gateNoopWorkbook()}
		return gateHandler().WithTransactions(f), func() bool { return f.got != (TransactionQuery{}) }
	}
	runGateCases(t, "/api/v1/exports/transactions.xlsx", build, []gateCase{
		{"frontline refused", gateFrontline, "?year=2026&branch_id=b-1", http.StatusForbidden, false},
		{"branch frontline refused", gateFrontlineBr, "?year=2026&branch_id=b-1", http.StatusForbidden, false},
		{"out of reach branch refused", gateBranchMgr, "?year=2026&branch_id=b-2", http.StatusForbidden, false},
		{"scoped caller naming no branch refused", gateBranchMgr, "?year=2026", http.StatusForbidden, false},
		{"branch manager served in reach", gateBranchMgr, "?year=2026&branch_id=b-1", http.StatusOK, true},
		{"global manager served any branch", gateGlobalMgr, "?year=2026&branch_id=b-2", http.StatusOK, true},
		{"global manager served consolidated", gateGlobalMgr, "?year=2026", http.StatusOK, true},
	})
}

func TestAuditExport_CarriesTheAdminGate(t *testing.T) {
	build := func() (*Handler, func() bool) {
		svc, f := auditSvc()
		return gateHandler().WithAudit(svc), func() bool { return f.n > 0 }
	}
	runGateCases(t, "/api/v1/exports/audit.xlsx", build, []gateCase{
		{"frontline refused", gateFrontline, "", http.StatusForbidden, false},
		{"branch manager refused", gateBranchMgr, "?branch_id=b-1", http.StatusForbidden, false},
		{"global manager refused", gateGlobalMgr, "", http.StatusForbidden, false},
		{"admin served", gateAdmin, "", http.StatusOK, true},
		{"admin served any branch", gateAdmin, "?branch_id=b-9", http.StatusOK, true},
	})
}
