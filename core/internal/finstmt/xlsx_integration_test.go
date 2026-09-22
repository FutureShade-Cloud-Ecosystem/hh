//go:build integration

package finstmt

// xlsx_integration_test.go, DIB-454's HTTP surface, driven against the REAL repository.
//
// These assertions need a database, and not because they care what the numbers are: the finstmt
// repository refuses a nil pool (errNoDB), so a handler test without one only ever proves that the
// error path returns 500. What is under test here is the RESPONSE, the content type, the filename,
// and the first two bytes of the body, which only exists when the read succeeds.

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/export"
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// -- the HTTP surface: ?format=xlsx answers with a REAL .xlsx -------------------------------------

// The headline of DIB-454. `?format=xlsx` used to answer application/vnd.ms-excel with a `.xls`
// filename and an XML body; it now answers the OOXML package, which is a ZIP, and a ZIP starts
// "PK", which is the cheapest possible proof that this is not the old file wearing a new name.
func TestIntegration_FinstmtHandler_FormatXLSXAnswersARealXlsxPackage(t *testing.T) {
	for _, format := range []string{"xlsx", "xls", "excel", "XLSX"} {
		rec := statementExportReq(t, "/api/v1/finstmt/trial-balance?year=2026&format="+format)
		if rec.Code != http.StatusOK {
			t.Fatalf("format=%s → %d, want 200 (body: %s)", format, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != export.ContentType {
			t.Errorf("format=%s Content-Type=%q, want %q", format, ct, export.ContentType)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="trial-balance-2026.xlsx"`) {
			t.Errorf("format=%s Content-Disposition=%q, want a .xlsx filename", format, cd)
		}
		body := rec.Body.Bytes()
		if len(body) < 2 || body[0] != 'P' || body[1] != 'K' {
			t.Errorf("format=%s body does not begin with the ZIP magic 'PK', it is not an OOXML package (first bytes: %q)",
				format, string(body[:min(16, len(body))]))
		}
		if strings.Contains(string(body[:min(200, len(body))]), "urn:schemas-microsoft-com:office:spreadsheet") {
			t.Errorf("format=%s still answers SpreadsheetML 2003, the mislabelled format DIB-454 removed", format)
		}
		if rec.Header().Get("Content-Length") == "" {
			t.Errorf("format=%s has no Content-Length; a download with no length shows an indeterminate progress bar", format)
		}
	}
}

// CSV is untouched, same content type, same filename, same bytes it always had.
func TestIntegration_FinstmtHandler_CSVIsUnchanged(t *testing.T) {
	rec := statementExportReq(t, "/api/v1/finstmt/trial-balance?year=2026&format=csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("csv → %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("csv Content-Type=%q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="trial-balance.csv"`) {
		t.Errorf("csv Content-Disposition=%q, want the unchanged .csv name", cd)
	}
	if !strings.HasPrefix(rec.Body.String(), "Trial Balance\n") {
		t.Errorf("csv body no longer opens with its title row: %q", rec.Body.String()[:min(40, rec.Body.Len())])
	}
}

// An unknown format is still a 400, the export must never guess which file someone meant.
func TestIntegration_FinstmtHandler_AnUnknownFormatIsRefused(t *testing.T) {
	rec := statementExportReq(t, "/api/v1/finstmt/trial-balance?year=2026&format=pdf")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("format=pdf → %d, want 400", rec.Code)
	}
}

// statementExportReq drives the real handler with a global caller (past the DIB-391 branch fence)
// over the real repository, so the export path runs end to end: read, project, encode, download.
func statementExportReq(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	svc := NewService(NewRepository(intPool(t))).
		WithClock(exportClock).
		withCaller(func(context.Context) (identity.StaffContext, bool) {
			return identity.StaffContext{
				UserID: "u-ctrl", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: identity.StatusActive,
			}, true
		})
	// Guards-swap carry note (HH-3.2 fold): the pass-through stubs became the
	// kit's real Guards with a resolved staff authority.
	h := NewHandler(svc, slog.New(slog.DiscardHandler))
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "payables_receivables", Tier: 2, Scope: "global"})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}
