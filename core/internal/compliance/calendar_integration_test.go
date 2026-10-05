//go:build integration

// calendar_integration_test.go, DIB-211 VERIFY:compliance.
//
// The ledger row flags this module as "built but conspicuously absent from the DIB-211-A sweep bullet
// list". Running the existing evidence fresh confirms the shape of the gap rather than the absence of
// one: integration_test.go proves RegisterDeadline's idempotency and the review-status round trip, and
// stops there. Three things it never touches are shipped behaviour that money or a legal clock depends
// on, so they are covered here:
//
//	Calendar(status)     the deadline registry read behind GET /compliance/calendar, the status filter
//	                     had NO coverage at any level against real SQL.
//	Upcoming(horizon)    proved only by "the row I just wrote appears somewhere in a 90-day window".
//	                     The two facts that matter, the horizon BOUNDARY and the OVERDUE marking that
//	                     drives the Bill 60 Monday triage, were unasserted against the database.
//	the HTTP surface     four routes, all behind requireManager, with zero integration coverage. A
//	                     route wired behind the wrong guard, or behind one that rejects everyone, would
//	                     have passed the whole suite.
//
// Every row this file creates is deleted in t.Cleanup. The older file's fixtures do NOT restore what
// they touch (a NOTICE_DEADLINE row and a review_status row are left behind on every run); that is
// fixed in integration_test.go under the same change.
//
// Run: make reset && go test -tags integration ./internal/compliance/...
package compliance

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// asOfFixed is the clock every test here pins, so "overdue" and "within the horizon" are facts about
// the fixture rather than about the day the suite happens to run.
var asOfFixed = time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)

func fixedSvc(pool *pgxpool.Pool) *Service {
	return NewService(NewRepository(pool)).withClock(func() time.Time { return asOfFixed })
}

// seedDeadline writes one compliance_calendar row directly (job_id NULL avoids the lien_jobs FK) and
// deletes it on cleanup. Returns the row id.
//
// The direct write is deliberate: RegisterDeadline cannot produce a SATISFIED or WAIVED row, and the
// status filter is exactly what needs proving.
func seedDeadline(t *testing.T, pool *pgxpool.Pool, kind, due, status string, ruleDays int) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO compliance_calendar (job_id, deadline_kind, due_date, rule_days, status, notes)
		 VALUES (NULL, $1, $2::date, $3, $4, $5) RETURNING id::text`,
		kind, due, ruleDays, status, "VERIFY:compliance fixture, "+t.Name()).Scan(&id); err != nil {
		t.Fatalf("seed deadline (%s %s %s): %v", kind, due, status, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM compliance_calendar WHERE id = $1`, id)
	})
	return id
}

// -- VERIFY:compliance / 1, the calendar read + its status filter -------------------------------

func TestIntegration_Compliance_Calendar_ListsAndFiltersByStatus(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := fixedSvc(pool)

	open := seedDeadline(t, pool, "HOLDBACK_RELEASE", "2026-07-20", "OPEN", 45)
	satisfied := seedDeadline(t, pool, "LIEN_EXPIRY", "2026-07-21", "SATISFIED", 60)
	waived := seedDeadline(t, pool, "NOTICE_DEADLINE", "2026-07-22", "WAIVED", 45)

	ids := func(items []Deadline) map[string]Deadline {
		out := map[string]Deadline{}
		for _, d := range items {
			out[d.ID] = d
		}
		return out
	}

	// "" = all statuses.
	all, err := svc.Calendar(ctx, "")
	if err != nil {
		t.Fatalf("calendar (all): %v", err)
	}
	got := ids(all)
	for _, id := range []string{open, satisfied, waived} {
		if _, ok := got[id]; !ok {
			t.Fatalf("an unfiltered calendar must return every status; %s is missing", id)
		}
	}
	// The projection is whole, a dropped column here silently blanks the worklist UI.
	row := got[open]
	if row.Kind != "HOLDBACK_RELEASE" || row.DueDate != "2026-07-20" || row.RuleDays != 45 || row.Status != "OPEN" {
		t.Fatalf("deadline projection = %+v, want the seeded kind/due/rule_days/status", row)
	}

	// A status filter narrows to exactly that status.
	openOnly, err := svc.Calendar(ctx, "OPEN")
	if err != nil {
		t.Fatalf("calendar (OPEN): %v", err)
	}
	gotOpen := ids(openOnly)
	if _, ok := gotOpen[open]; !ok {
		t.Fatal("the OPEN filter dropped an OPEN row")
	}
	if _, ok := gotOpen[satisfied]; ok {
		t.Fatal("the OPEN filter returned a SATISFIED row, the filter is not reaching the SQL")
	}
	if _, ok := gotOpen[waived]; ok {
		t.Fatal("the OPEN filter returned a WAIVED row")
	}
	for _, d := range openOnly {
		if d.Status != "OPEN" {
			t.Fatalf("filtered calendar returned status %q", d.Status)
		}
	}

	// A status nobody uses returns an empty set rather than everything, the failure mode where a
	// filter falls through to "no WHERE clause" and a compliance officer reads the wrong worklist.
	none, err := svc.Calendar(ctx, "SATISFIED")
	if err != nil {
		t.Fatalf("calendar (SATISFIED): %v", err)
	}
	if _, ok := ids(none)[open]; ok {
		t.Fatal("the SATISFIED filter returned an OPEN row")
	}
}

// -- VERIFY:compliance / 2, the badge worklist: horizon boundary + OVERDUE marking --------------

// This is the Bill 60 Monday-triage source. Its two load-bearing behaviours are the horizon cut and
// the OVERDUE stamp, and neither was asserted against the database before.
func TestIntegration_Compliance_Upcoming_HorizonBoundaryAndOverdueMarking(t *testing.T) {
	pool := intPool(t)
	svc := fixedSvc(pool)

	// asOf is 2026-07-07.
	overdue := seedDeadline(t, pool, "HOLDBACK_RELEASE", "2026-06-30", "OPEN", 45) // already past
	soon := seedDeadline(t, pool, "LIEN_EXPIRY", "2026-07-15", "OPEN", 45)         // 8 days out
	far := seedDeadline(t, pool, "PROOF_OF_CLAIM", "2026-12-31", "OPEN", 45)       // far beyond any horizon
	// A SATISFIED row inside the horizon must never reach the badge: it is done.
	done := seedDeadline(t, pool, "NOTICE_DEADLINE", "2026-07-10", "SATISFIED", 45)

	byID := func(items []Deadline) map[string]Deadline {
		out := map[string]Deadline{}
		for _, d := range items {
			out[d.ID] = d
		}
		return out
	}

	got := byID(mustUpcoming(t, svc, 30))

	if _, ok := got[far]; ok {
		t.Fatal("a deadline beyond the horizon must not reach the badge worklist")
	}
	if _, ok := got[done]; ok {
		t.Fatal("a SATISFIED deadline must not reach the badge worklist, it is closed")
	}
	if _, ok := got[soon]; !ok {
		t.Fatal("a deadline inside the horizon must reach the badge worklist")
	}
	if d, ok := got[overdue]; !ok {
		t.Fatal("an already-past deadline must still reach the worklist, that is the one that matters most")
	} else if d.Status != "OVERDUE" {
		t.Fatalf("a past-due OPEN deadline must be marked OVERDUE for triage, got %q", d.Status)
	}
	// The in-horizon future row keeps its stored status: only past-due rows are re-stamped.
	if d := got[soon]; d.Status != "OPEN" {
		t.Fatalf("a future in-horizon deadline must stay OPEN, got %q", d.Status)
	}

	// The horizon is a real cut, not decoration: a narrow window drops the 8-day row, a wide one
	// picks up the December one.
	narrow := byID(mustUpcoming(t, svc, 3))
	if _, ok := narrow[soon]; ok {
		t.Fatal("a 3-day horizon must not include a deadline 8 days out")
	}
	if _, ok := narrow[overdue]; !ok {
		t.Fatal("a narrow horizon must still surface an OVERDUE deadline")
	}
	wide := byID(mustUpcoming(t, svc, 365))
	if _, ok := wide[far]; !ok {
		t.Fatal("a 365-day horizon must include a deadline 177 days out")
	}
}

func mustUpcoming(t *testing.T, svc *Service, horizon int) []Deadline {
	t.Helper()
	items, err := svc.Upcoming(context.Background(), horizon)
	if err != nil {
		t.Fatalf("upcoming(%d): %v", horizon, err)
	}
	return items
}

// -- VERIFY:compliance / 3, the HTTP surface and its guards -------------------------------------

// recordingGuard stands in for identity's RequireManager: it records that it ran and can be told to
// refuse, so the test can prove WHICH guard each route is wired behind rather than merely that some
// refusal happens somewhere. (The end-to-end refusal with a real JWT is the gate suite's job; this
// proves the wiring, which is the half a 403 alone cannot distinguish.)
type recordingGuard struct {
	name   string
	ran    []string
	refuse bool
}

func (g *recordingGuard) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g.ran = append(g.ran, g.name+" "+r.Method+" "+r.URL.Path)
		if g.refuse {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func TestIntegration_Compliance_HTTPSurface_ServesAndIsManagerGated(t *testing.T) {
	pool := intPool(t)
	svc := fixedSvc(pool)

	seedDeadline(t, pool, "HOLDBACK_RELEASE", "2026-07-15", "OPEN", 45)

	// Guards-swap carry note (HH-3.2 fold): the recording guard stubs became
	// the kit's real Guards with a resolved authority per leg; the routes are
	// the constructor bindings the registrar pins.
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "payables_receivables", Tier: 2, Scope: "global"})
	mux := http.NewServeMux()
	NewHandler(svc, slog.Default()).RegisterRoutes(mux, sg.Guards)

	// The kit's guards are the real ones: a request with no staff bearer is refused 401 before the
	// authority is ever consulted, so every call carries the bearer the double admits.
	bearer := sg.Bearer(t)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, jsonBody(body))
			r.Header.Set("Content-Type", "application/json")
		}
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("X-Org", testkit.OrgNorthSlug)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	// Every route answers, and answers with real data (not the 501 the module doc still describes as
	// its scaffold state, which is itself worth knowing: the doc is stale, the routes are live).
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/compliance/calendar"},
		{"GET", "/api/v1/compliance/calendar?status=OPEN"},
		{"GET", "/api/v1/compliance/upcoming?horizon_days=30"},
	} {
		w := do(tc.method, tc.path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s = %d, want 200; body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
		var out struct {
			Items []Deadline `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: decode: %v, body=%s", tc.method, tc.path, err, w.Body.String())
		}
		if len(out.Items) == 0 {
			t.Fatalf("%s %s returned an empty item list, the route is not reaching the store", tc.method, tc.path)
		}
	}

	// The review-status read answers the DATA-DRIVEN default for an unknown subject rather than 404,
	// because the M6 UI derives its banner from this endpoint and must never hardcode one.
	subject := freshUUID(t, pool)
	w := do("GET", "/api/v1/compliance/review-status?subject_type=LIEN_JOB&subject_id="+subject, "")
	if w.Code != http.StatusOK {
		t.Fatalf("review-status read = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var rs ReviewStatus
	if err := json.Unmarshal(w.Body.Bytes(), &rs); err != nil {
		t.Fatalf("decode review status: %v", err)
	}
	if rs.State != "DRAFT" || !rs.IsDraft || rs.BannerText == "" {
		t.Fatalf("an unknown subject must answer the default DRAFT banner, got %+v", rs)
	}

	// The upsert persists through the real table and comes back on the read.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM review_status WHERE subject_id = $1::uuid`, subject)
	})
	up := do("PUT", "/api/v1/compliance/review-status",
		fmt.Sprintf(`{"subject_type":"LIEN_JOB","subject_id":%q,"state":"APPROVED","banner_text":"Reviewed"}`, subject))
	if up.Code != http.StatusOK {
		t.Fatalf("review-status upsert = %d, want 200; body=%s", up.Code, up.Body.String())
	}
	after := do("GET", "/api/v1/compliance/review-status?subject_type=LIEN_JOB&subject_id="+subject, "")
	if err := json.Unmarshal(after.Body.Bytes(), &rs); err != nil {
		t.Fatalf("decode review status after upsert: %v", err)
	}
	if rs.State != "APPROVED" || rs.IsDraft || rs.BannerText != "Reviewed" {
		t.Fatalf("upsert did not persist through the route: %+v", rs)
	}

	// -- the wiring itself --------------------------------------------------------------------
	// Guards-swap carry note (HH-3.2 fold): which constructor each compliance route sits behind is the
	// registrar's own source, and the build does NOT pin it (RequireStaff and RequireManager share a
	// signature), so the load-bearing half is behavioral and covers EVERY route: when the resolved
	// authority drops below the manager rung the handler never runs, on all four method and path pairs
	// (the review-status upsert is the consequential one: it flips the shared DRAFT or APPROVED banner),
	// and a call with no bearer at all is refused 401 before any authority is read.
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "global"})
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/compliance/calendar", ""},
		{"GET", "/api/v1/compliance/upcoming?horizon_days=30", ""},
		{"GET", "/api/v1/compliance/review-status?subject_type=LIEN_JOB&subject_id=" + subject, ""},
		{"PUT", "/api/v1/compliance/review-status",
			fmt.Sprintf(`{"subject_type":"LIEN_JOB","subject_id":%q,"state":"APPROVED","banner_text":"Not allowed"}`, subject)},
	} {
		if w := do(tc.method, tc.path, tc.body); w.Code != http.StatusForbidden {
			t.Fatalf("a frontline authority must be refused %s %s, got %d", tc.method, tc.path, w.Code)
		}
	}
	// The refused upsert wrote nothing: the row the manager upsert left is still the APPROVED one.
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "payables_receivables", Tier: 2, Scope: "global"})
	kept := do("GET", "/api/v1/compliance/review-status?subject_type=LIEN_JOB&subject_id="+subject, "")
	if err := json.Unmarshal(kept.Body.Bytes(), &rs); err != nil || rs.BannerText != "Reviewed" {
		t.Fatalf("a refused upsert must leave the banner as the manager set it: %+v err %v", rs, err)
	}
	// No bearer at all: 401 before the authority is consulted (the class that made this test red).
	anon := httptest.NewRequest("GET", "/api/v1/compliance/calendar", nil)
	anon.Header.Set("X-Org", testkit.OrgNorthSlug)
	aw := httptest.NewRecorder()
	mux.ServeHTTP(aw, anon)
	if aw.Code != http.StatusUnauthorized {
		t.Fatalf("a call with no bearer must be refused 401, got %d", aw.Code)
	}
}

func freshUUID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	return id
}

func jsonBody(s string) *strings.Reader { return strings.NewReader(s) }
