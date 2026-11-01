//go:build integration

package billing

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// TestIntegration_JA4_ByJobRoutes drives the three by job reads through the handler: the aging with
// its job filter, the statement by job, the job statement as JSON and as HTML, and the refusals (a
// malformed job id is a 400 in words, another customer's job a 404).
func TestIntegration_JA4_ByJobRoutes(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	h := NewMonthEndHandler(NewCreditService(repo), NewStatementService(repo), slog.Default())
	direct := func(fn func(http.ResponseWriter, *http.Request, guards.StaffCaller)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { fn(w, r, guards.StaffCaller{}) }
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/ar/aging-by-job", direct(h.agingByJob))
	mux.HandleFunc("GET /api/v1/ar/customers/{customerID}/statement-by-job", direct(h.statementByJob))
	mux.HandleFunc("GET /api/v1/ar/customers/{customerID}/jobs/{jobID}/statement", direct(h.jobStatement))
	f := jaSeed(t, pool)
	other := jaSeed(t, pool, "-o")
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, adminReq(http.MethodGet, target, ""))
		return rec
	}

	rec := get("/api/v1/ar/aging-by-job?job_id=" + f.jobA)
	var aging struct {
		Items []AgingJobRow `json:"items"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &aging) != nil || len(aging.Items) != 1 || aging.Items[0].TotalCents != 30000 {
		t.Fatalf("aging-by-job: %d %s", rec.Code, rec.Body.String())
	}
	if rec = get("/api/v1/ar/aging-by-job?job_id=not-a-job"); rec.Code != 400 {
		t.Fatalf("a malformed job id: %d, want 400", rec.Code)
	}

	rec = get("/api/v1/ar/customers/" + f.customer + "/statement-by-job")
	var st JobStatement
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &st) != nil || len(st.Sections) != 3 || st.ClosingCents != f.totalCents {
		t.Fatalf("statement-by-job: %d %s", rec.Code, rec.Body.String())
	}

	rec = get("/api/v1/ar/customers/" + f.customer + "/jobs/" + f.jobB + "/statement")
	var js JobStatement
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &js) != nil || len(js.Sections) != 1 || js.ClosingCents != 20000 || js.Contact.Source != "job" {
		t.Fatalf("job statement: %d %s", rec.Code, rec.Body.String())
	}
	rec = get("/api/v1/ar/customers/" + f.customer + "/jobs/" + f.jobB + "/statement?format=html")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") || !strings.Contains(rec.Body.String(), "Job B-") {
		t.Fatalf("job statement html: %d %s", rec.Code, rec.Body.String())
	}
	if rec = get("/api/v1/ar/customers/" + f.customer + "/jobs/" + other.jobA + "/statement"); rec.Code != 404 {
		t.Fatalf("another customer's job: %d, want 404", rec.Code)
	}
	if rec = get("/api/v1/ar/customers/" + f.customer + "/jobs/nope/statement"); rec.Code != 400 {
		t.Fatalf("a malformed job id on the statement: %d, want 400", rec.Code)
	}
}
