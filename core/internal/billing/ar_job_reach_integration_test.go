//go:build integration

// ar_job_reach_integration_test.go, W3 0.62 (FROM-PRINCIPAL 254 and 258): the job statement and the by
// job reads stay inside the caller's reach.
//
//   - a job statement is refused, in words, to a caller who cannot reach every branch the job's
//     documents sit at, JSON and HTML alike, and gives the whole statement to one who can;

// by job, though one of its invoices sits in reach: the money stays on the lines the caller sees.
package billing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const jobReachWords = "this job has documents at a branch you cannot see; ask someone who can reach every branch on it"

// reachCtx is a branch scoped manager granted exactly the named branches.
func reachCtx(branches ...string) context.Context {
	granted := map[string]bool{}
	for _, b := range branches {
		granted[b] = true
	}
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Status: "ACTIVE", Branches: granted,
	})
}

// jobReachWorld is one customer with a job homed at Trenton whose invoices sit at both branches
// (30000 at Trenton, 40000 at Kingston), and a job homed at Kingston with one invoice at Trenton.
type jobReachWorld struct {
	customer string
	split    string // homed at Trenton, invoices at both
	away     string // homed at Kingston, its one invoice at Trenton
}

func newJobReachWorld(t *testing.T) jobReachWorld {
	t.Helper()
	pool := intPool(t)
	n := ns(t)
	w := jobReachWorld{customer: seedCustomer(t, pool, n+"-JR")}
	w.split = jaSeedJob(t, pool, w.customer, "S-"+n)
	w.away = jaSeedJob(t, pool, w.customer, "K-"+n)
	if _, err := pool.Exec(context.Background(), `UPDATE customer_jobs SET branch_id = $2::uuid WHERE id = $1::uuid`, w.away, kingstonBranch); err != nil {
		t.Fatalf("home the away job at Kingston: %v", err)
	}
	issue := time.Now().AddDate(0, 0, -45).Format("2006-01-02")
	sa := seedInvoice(t, pool, w.customer, n, invSpec{no: "JR-SA", status: "FINALIZED", branch: trentonBranch, issue: issue, total: 30000, balance: 30000, finalized: true})
	sb := seedInvoice(t, pool, w.customer, n, invSpec{no: "JR-SB", status: "FINALIZED", branch: kingstonBranch, issue: issue, total: 40000, balance: 40000, finalized: true})
	aw := seedInvoice(t, pool, w.customer, n, invSpec{no: "JR-AW", status: "FINALIZED", branch: trentonBranch, issue: issue, total: 5000, balance: 5000, finalized: true})
	jaNameInvoice(t, pool, sa, w.split)
	jaNameInvoice(t, pool, sb, w.split)
	jaNameInvoice(t, pool, aw, w.away)
	return w
}

func jobReachMux(t *testing.T) http.Handler {
	t.Helper()
	repo := NewRepository(intPool(t))
	h := NewMonthEndHandler(NewCreditService(repo), NewStatementService(repo), slog.Default())
	direct := func(fn func(http.ResponseWriter, *http.Request, guards.StaffCaller)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { fn(w, r, guards.StaffCaller{}) }
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/ar/aging-by-job", direct(h.agingByJob))
	mux.HandleFunc("GET /api/v1/ar/customers/{customerID}/statement-by-job", direct(h.statementByJob))
	mux.HandleFunc("GET /api/v1/ar/customers/{customerID}/jobs/{jobID}/statement", direct(h.jobStatement))
	return mux
}

func jobReachGet(mux http.Handler, ctx context.Context, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx))
	return rec
}

// TestIntegration_W3P062_JobStatementRefusedWhenBranchesOutOfReach: item 1, JSON and HTML.
func TestIntegration_W3P062_JobStatementRefusedWhenBranchesOutOfReach(t *testing.T) {
	w := newJobReachWorld(t)
	mux := jobReachMux(t)
	base := "/api/v1/ar/customers/" + w.customer + "/jobs/" + w.split + "/statement"

	for _, format := range []string{"", "?format=html"} {
		rec := jobReachGet(mux, reachCtx(trentonBranch), base+format)
		body := rec.Body.String()
		if rec.Code != http.StatusForbidden || !strings.Contains(body, jobReachWords) {
			t.Fatalf("[%q] a manager reaching one of the job's two branches: %d %q, want 403 with the words", format, rec.Code, body)
		}
		for _, leak := range []string{"30000", "300.00", "40000", "400.00", "JR-SA", "JR-SB", "Subtotal"} {
			if strings.Contains(body, leak) {
				t.Fatalf("[%q] the refusal carries %q: %q", format, leak, body)
			}
		}
	}

	for name, ctx := range map[string]context.Context{"both branches": reachCtx(trentonBranch, kingstonBranch), "global": AllBranchesContext()} {
		rec := jobReachGet(mux, ctx, base)
		var st JobStatement
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &st) != nil || st.ClosingCents != 70000 || len(st.Sections) != 1 || len(st.Sections[0].Lines) != 2 {
			t.Fatalf("[%s] the whole statement: %d %s, want 70000 over two lines", name, rec.Code, rec.Body.String())
		}
		html := jobReachGet(mux, ctx, base+"?format=html")
		if html.Code != http.StatusOK || !strings.Contains(html.Body.String(), "$700.00") {
			t.Fatalf("[%s] the whole statement as HTML: %d %s", name, html.Code, html.Body.String())
		}
	}
}

// AllBranchesContext is a context with no stamped caller, which reads as the whole book.
func AllBranchesContext() context.Context { return context.Background() }
