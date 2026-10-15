package export

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// audit_test.go, DIB-454 / DIB-405: the audit trail as a native .xlsx.

// auditClock is a fixed instant for the generated-at line and the filename. Compared against nothing
// but the strings these tests assert, so no real clock can arm it (DIB-427).
var auditClock = func() time.Time { return time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC) }

type fakeAuditLister struct {
	rows []audit.Entry
	err  error
	got  audit.Filter
	n    int
}

func (f *fakeAuditLister) List(_ context.Context, flt audit.Filter) ([]audit.Entry, error) {
	f.got = flt
	f.n++
	return f.rows, f.err
}

func auditSvc(rows ...audit.Entry) (*AuditService, *fakeAuditLister) {
	f := &fakeAuditLister{rows: rows}
	return NewAuditService(f, slog.New(slog.NewTextHandler(io.Discard, nil))).WithClock(auditClock), f
}

func firstSheet(t *testing.T, wb *Workbook) *Sheet {
	t.Helper()
	if len(wb.Sheets) == 0 {
		t.Fatal("the workbook has no sheets")
	}
	return wb.Sheets[0]
}

// The columns the ticket asked for, and the two it did not: entity TYPE and entity ID are separate
// because "order/9f3c…" in one cell is not filterable and the id is the join key.
func TestAuditExport_CarriesTheTrailWithATitleBlock(t *testing.T) {
	svc, _ := auditSvc(audit.Entry{
		ID: "a-1", Action: "ORDER_EDIT", EntityType: "order", EntityID: "o-1",
		ActorID: "u-1", ActorRole: "MANAGER", BranchID: "b-1", RequestID: "req-9",
		CreatedAt: "2026-08-11T14:03:00Z",
		Changes:   map[string]any{"before": map[string]any{"qty": 1}, "after": map[string]any{"qty": 2}},
	})
	wb, err := svc.Workbook(context.Background(), AuditQuery{FilterSummary: "Entity: order"})
	if err != nil {
		t.Fatalf("workbook: %v", err)
	}
	sh := firstSheet(t, wb)

	if sh.Title != "Audit trail" {
		t.Fatalf("sheet title = %q", sh.Title)
	}
	if !strings.Contains(sh.Subtitle, "1 entry") || !strings.Contains(sh.Subtitle, "filter: Entity: order") {
		t.Fatalf("subtitle %q must carry the row count and the filter that produced it", sh.Subtitle)
	}
	if !strings.Contains(sh.Subtitle, "generated 2026-08-12 09:30") {
		t.Fatalf("subtitle %q carries no generated-at stamp", sh.Subtitle)
	}
	if len(sh.Rows) != 1 {
		t.Fatalf("%d rows, want 1", len(sh.Rows))
	}
	row := sh.Rows[0]
	if row[0].Kind != KindDate || row[0].When.Format("2006-01-02") != "2026-08-11" {
		t.Fatalf("recorded-at cell %+v, want the date 2026-08-11", row[0])
	}
	if row[1].Text != "ORDER_EDIT" || row[2].Text != "order" || row[3].Text != "o-1" {
		t.Fatalf("action/entity columns wrong: %+v", row[1:4])
	}
	if row[7].Text != `{"qty":1}` || row[8].Text != `{"qty":2}` {
		t.Fatalf("before/after = %q / %q, want the two sides of the payload", row[7].Text, row[8].Text)
	}
}

// A payload that does not carry the house before/after shape is rendered WHOLE in After, and Before
// is BLANK, not "{}", which would claim a prior state that was never recorded.
func TestAuditExport_AnUnshapedPayloadLeavesBeforeBlank(t *testing.T) {
	svc, _ := auditSvc(audit.Entry{
		ID: "a-1", Action: "LOGIN", CreatedAt: "2026-08-11T14:03:00Z",
		Changes: map[string]any{"ip": "10.0.0.2"},
	})
	wb, _ := svc.Workbook(context.Background(), AuditQuery{})
	row := firstSheet(t, wb).Rows[0]
	if row[7].Kind != KindBlank {
		t.Fatalf("Before = %+v, want BLANK, a fabricated prior state is worse than an empty cell", row[7])
	}
	if row[8].Text != `{"ip":"10.0.0.2"}` {
		t.Fatalf("After = %q, want the whole payload", row[8].Text)
	}
}

// An empty payload leaves BOTH sides blank.
func TestAuditExport_AnEmptyPayloadLeavesBothSidesBlank(t *testing.T) {
	svc, _ := auditSvc(audit.Entry{ID: "a-1", Action: "VIEW", CreatedAt: "2026-08-11T14:03:00Z"})
	row := firstSheet(t, mustWorkbook(t, svc)).Rows[0]
	if row[7].Kind != KindBlank || row[8].Kind != KindBlank {
		t.Fatalf("empty payload wrote %+v / %+v, want two blanks", row[7], row[8])
	}
}

// An unparseable timestamp is a BLANK date, never today's. In an evidence file, a fabricated
// recorded-at is worse than a missing one.
func TestAuditExport_AnUnparseableTimestampIsBlankNotToday(t *testing.T) {
	svc, _ := auditSvc(audit.Entry{ID: "a-1", Action: "VIEW", CreatedAt: "whenever"})
	row := firstSheet(t, mustWorkbook(t, svc)).Rows[0]
	if row[0].Kind != KindBlank {
		t.Fatalf("recorded-at = %+v, want BLANK", row[0])
	}
}

// A payload big enough to break the file is capped, and the cap is MARKED so a clipped payload is
// distinguishable from a short one. Excel's own hard limit is 32,767 characters per cell; a file
// that exceeds it does not open at all.
func TestAuditExport_AnEnormousPayloadIsCappedAndMarked(t *testing.T) {
	svc, _ := auditSvc(audit.Entry{
		ID: "a-1", Action: "IMPORT", CreatedAt: "2026-08-11T14:03:00Z",
		Changes: map[string]any{"after": strings.Repeat("x", 50_000)},
	})
	after := firstSheet(t, mustWorkbook(t, svc)).Rows[0][8].Text
	if len(after) > auditCellLimit+8 {
		t.Fatalf("payload cell is %d bytes, want it capped near %d", len(after), auditCellLimit)
	}
	if !strings.HasSuffix(after, "…") {
		t.Fatalf("a truncated payload must say so; got a cell ending %q", after[len(after)-8:])
	}
}

// A multi-byte payload is cut on a RUNE boundary. Half a character is not text, and the writer drops
// invalid runes, so a careless cut would silently change the last visible character.
func TestAuditExport_TruncationCutsOnARuneBoundary(t *testing.T) {
	svc, _ := auditSvc(audit.Entry{
		ID: "a-1", Action: "IMPORT", CreatedAt: "2026-08-11T14:03:00Z",
		Changes: map[string]any{"after": strings.Repeat("é", 5000)},
	})
	after := firstSheet(t, mustWorkbook(t, svc)).Rows[0][8].Text
	for i, r := range after {
		if r == '\ufffd' {
			t.Fatalf("truncation produced an invalid rune at byte %d", i)
		}
	}
}

// The row count on the sheet warns when the read hit its ceiling. An audit extract's most dangerous
// misreading is "this is everything".
func TestAuditExport_SaysSoWhenTheReadHitItsRowLimit(t *testing.T) {
	rows := make([]audit.Entry, 200)
	for i := range rows {
		rows[i] = audit.Entry{ID: "a", Action: "VIEW", CreatedAt: "2026-08-11T14:03:00Z"}
	}
	svc, _ := auditSvc(rows...)
	wb, _ := svc.Workbook(context.Background(), AuditQuery{})
	if sub := firstSheet(t, wb).Subtitle; !strings.Contains(sub, "AT THE 200-ROW LIMIT") {
		t.Fatalf("subtitle %q does not warn that the read is capped; a partial extract that reads as complete is the dangerous case", sub)
	}
}

// An empty trail is a real workbook with headers and no rows, "nothing matched" is an answer, and a
// download that does nothing is indistinguishable from a broken button.
func TestAuditExport_AnEmptyTrailIsStillAWorkbook(t *testing.T) {
	svc, _ := auditSvc()
	wb, err := svc.Workbook(context.Background(), AuditQuery{})
	if err != nil {
		t.Fatalf("empty trail: %v", err)
	}
	sh := firstSheet(t, wb)
	if len(sh.Rows) != 0 || len(sh.Columns) != 10 {
		t.Fatalf("empty export has %d rows / %d columns, want 0 / 10", len(sh.Rows), len(sh.Columns))
	}
	if _, err := wb.Bytes(); err != nil {
		t.Fatalf("the empty workbook does not encode: %v", err)
	}
}

// A FAILED read is an error, never an empty file. An audit export that arrives looking like a clean
// bill of health when the trail could not be read is the worst outcome this route has.
func TestAuditExport_AFailedReadIsAnErrorNotAnEmptyFile(t *testing.T) {
	f := &fakeAuditLister{err: errors.New("boom")}
	svc := NewAuditService(f, slog.New(slog.NewTextHandler(io.Discard, nil))).WithClock(auditClock)
	if _, err := svc.Workbook(context.Background(), AuditQuery{}); err == nil {
		t.Fatal("a failed audit read produced a workbook; an empty audit export must never be a read failure in disguise")
	}
}

// The filter reaches the audit module unchanged, the export must not quietly widen what it reads.
func TestAuditExport_PassesTheFilterThrough(t *testing.T) {
	svc, f := auditSvc()
	want := audit.Filter{EntityType: "order", Action: "ORDER_EDIT", Limit: 50}
	if _, err := svc.Workbook(context.Background(), AuditQuery{Filter: want}); err != nil {
		t.Fatalf("workbook: %v", err)
	}
	if f.got != want {
		t.Fatalf("the audit module was asked for %+v, want %+v", f.got, want)
	}
}

func mustWorkbook(t *testing.T, svc *AuditService) *Workbook {
	t.Helper()
	wb, err := svc.Workbook(context.Background(), AuditQuery{})
	if err != nil {
		t.Fatalf("workbook: %v", err)
	}
	return wb
}

// -- query parsing --------------------------------------------------------------------------------

// The two parameters that flow into ::uuid / ::timestamptz casts are validated HERE, so a typo is a
// 400 rather than a Postgres error surfacing as a 500.
func TestParseAuditFilter_RefusesInputThatWouldBecomeAPostgresError(t *testing.T) {
	bad := []map[string][]string{
		{"actor_id": {"not-a-uuid"}},
		{"since": {"last tuesday"}},
		{"limit": {"-1"}},
		{"limit": {"many"}},
	}
	for _, q := range bad {
		if _, err := ParseAuditFilter(q); err == nil {
			t.Errorf("ParseAuditFilter(%v) accepted input that cannot reach the database", q)
		} else if !IsBadRequest(err) {
			t.Errorf("ParseAuditFilter(%v) → %v, want a client-input error", q, err)
		}
	}
	good := map[string][]string{
		"actor_id": {"9f3c1b2a-0000-4000-9000-000000000001"},
		"since":    {"2026-08-01T00:00:00Z"},
		"limit":    {"50"},
	}
	f, err := ParseAuditFilter(good)
	if err != nil {
		t.Fatalf("ParseAuditFilter(good) = %v", err)
	}
	if f.Limit != 50 || f.ActorID == "" || f.Since == "" {
		t.Fatalf("parsed filter %+v dropped a parameter", f)
	}
}

// The provenance line is stable: the same filter renders the same sentence every time, so two
// exports of one query are byte-identical.
func TestAuditFilterSummary_IsStable(t *testing.T) {
	f := audit.Filter{EntityType: "order", Action: "ORDER_EDIT", ActorID: "u-1", Since: "2026-08-01T00:00:00Z"}
	first := AuditFilterSummary(f)
	for i := 0; i < 20; i++ {
		if got := AuditFilterSummary(f); got != first {
			t.Fatalf("summary changed between calls: %q then %q", first, got)
		}
	}
	if !strings.Contains(first, "Action: ORDER_EDIT") || !strings.Contains(first, "Entity: order") {
		t.Fatalf("summary %q does not name the filter", first)
	}
	if AuditFilterSummary(audit.Filter{}) != "" {
		t.Fatalf("an unfiltered export must carry no filter line, got %q", AuditFilterSummary(audit.Filter{}))
	}
}

// -- the route ------------------------------------------------------------------------------------

// bearerMux wraps one mux so every request carries the kit's staff bearer
// and org header (the guards-swap carry note, HH-3.2 fold: the pass-through
// stub became the kit's real Guards, with a resolved staff authority).
type bearerMux struct {
	mux    *http.ServeMux
	sg     *testkit.StaffGuards
	bearer string
}

func (b bearerMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Header.Set("Authorization", "Bearer "+b.bearer)
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	b.mux.ServeHTTP(w, r)
}

func exportMux(t *testing.T, h *Handler) bearerMux {
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "owner", Tier: 1, Scope: "global"})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	return bearerMux{mux: mux, sg: sg, bearer: sg.Bearer(t)}
}

func TestAuditRoute_AnswersARealXlsxPackage(t *testing.T) {
	svc, _ := auditSvc(audit.Entry{ID: "a-1", Action: "VIEW", CreatedAt: "2026-08-11T14:03:00Z"})
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithAudit(svc).WithClock(auditClock)
	rec := httptest.NewRecorder()
	exportMux(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/exports/audit.xlsx?entity_type=order", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type=%q, want %q", ct, ContentType)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="audit-2026-08-12.xlsx"`) {
		t.Errorf("Content-Disposition=%q", cd)
	}
	if body := rec.Body.Bytes(); len(body) < 2 || body[0] != 'P' || body[1] != 'K' {
		t.Errorf("body is not a ZIP package")
	}
}

func TestAuditRoute_ABadFilterIs400(t *testing.T) {
	svc, _ := auditSvc()
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithAudit(svc).WithClock(auditClock)
	rec := httptest.NewRecorder()
	exportMux(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/exports/audit.xlsx?actor_id=nope", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

// An unwired seam registers NO route. A 404 for a capability this deployment does not have is the
// honest answer; a registered route over a nil seam is a 500 waiting for its first caller.
func TestExportRoutes_AnUnwiredConsumerRegistersNoRoute(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, path := range []string{
		"/api/v1/exports/audit.xlsx",
		"/api/v1/exports/statement.xlsx",
		"/api/v1/exports/transactions.xlsx",
	} {
		rec := httptest.NewRecorder()
		exportMux(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s with no seam wired → %d, want 404", path, rec.Code)
		}
	}
}
