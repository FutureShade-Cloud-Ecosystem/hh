//go:build integration

package reporting

// surface_integration_test.go, the parts of DIB-95 that are not a single report's arithmetic: the
// print artefact and its print_queue row, CSV export parity, the branch fence, saved views, and the
// conformance between the seeded definitions (0143) and the compiled catalog.

import (
	"context"
	"encoding/csv"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// seededSalesByDayTitle is the name migration 0143 plants for sales_by_day.
// Both title assertions below pin THIS string, not the human title
// "Sales by Day": the seed's names are scrubbed, so the human title is
// planted nowhere, and an assertion the seed cannot satisfy fails on
// every freshly migrated database (which is how this read on main
// before the pin).
const seededSalesByDayTitle = "Scrubname0027 by Scrubname0009"

// TestIntegration_Catalog_MatchesSeededTemplates, the two halves of a report definition must agree in
// BOTH directions, and the parameter schema must line up with the query's bind count.
//
// This is the test that catches the realistic maintenance failure: a template added to 0143 and not to
// catalog.go (a report in the nav that 404s), or a parameter added to the SQL and not to param_schema
// (a bind that is never supplied, so the query errors on every run).
func TestIntegration_Catalog_MatchesSeededTemplates(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	rows, err := pool.Query(ctx, `SELECT code, jsonb_array_length(param_schema) FROM report_templates WHERE is_active`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seeded := map[string]int{}
	for rows.Next() {
		var code string
		var params int
		if err := rows.Scan(&code, &params); err != nil {
			t.Fatal(err)
		}
		seeded[code] = params
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	for code, paramCount := range seeded {
		entry, ok := catalog[code]
		if !ok {
			t.Errorf("report_templates has %q but the catalog has no query for it, it would appear in the "+
				"nav and 404 when clicked", code)
			continue
		}
		if got := maxPlaceholder(entry.sql); got != paramCount {
			t.Errorf("template %s declares %d parameters in 0143 but its SQL binds $1..$%d, the two must "+
				"agree, in order, because the array index IS the bind position", code, paramCount, got)
		}
	}
	for code := range catalog {
		if _, ok := seeded[code]; !ok {
			t.Errorf("the catalog has a query for %q but no active report_templates row, it is invisible "+
				"to every surface", code)
		}
	}
}

// TestIntegration_Print_FreezesArtefactAndEnqueuesREPORTJob, the print path, end to end.
//
// The three things a printed report has to be: an ARTEFACT that keeps saying what it said, a
// print_queue row of the new REPORT doc type (0143's widening, following 0105's STATEMENT precedent),
// and the two LINKED so neither can exist alone.
func TestIntegration_Print_FreezesArtefactAndEnqueuesREPORTJob(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	ctx := context.Background()
	from, to := window()

	cust := f.customer("acme")
	f.invoicePaid(cust, trentonBranch, "", from, 10_000, 500)
	f.invoicePaid(cust, trentonBranch, "", from, 25_000, 1_250)

	params := map[string]string{"from_date": from, "to_date": to, "branch_id": trentonBranch}
	run, err := svc.Print(ctx, "sales_by_day", params, "")
	if err != nil {
		t.Fatal(err)
	}
	f.del(`DELETE FROM report_runs WHERE id=$1::uuid`, run.ID)
	f.del(`UPDATE report_runs SET print_job_id = NULL WHERE id=$1::uuid`, run.ID)
	f.del(`DELETE FROM print_queue WHERE id=$1::uuid`, run.PrintJobID)

	if run.PrintJobID == "" {
		t.Fatal("printing produced an artefact with no print job, nothing would ever come out of a printer")
	}
	// The print_queue row is the new doc type, targeted at a browser, queued, and pointing at the run.
	var docType, target, status, refID, branchID string
	if err := pool.QueryRow(ctx,
		`SELECT doc_type, render_target, status, ref_id::text, branch_id::text FROM print_queue WHERE id=$1::uuid`,
		run.PrintJobID).Scan(&docType, &target, &status, &refID, &branchID); err != nil {
		t.Fatal(err)
	}
	if docType != PrintDocType {
		t.Errorf("print job doc_type = %q, want %q", docType, PrintDocType)
	}
	if target != PrintRenderTarget {
		t.Errorf("print job render_target = %q, want %q", target, PrintRenderTarget)
	}
	if status != "QUEUED" {
		t.Errorf("print job status = %q, want QUEUED, enqueuing is not printing", status)
	}
	if refID != run.ID {
		t.Errorf("print job ref_id = %q, want the report_runs id %q", refID, run.ID)
	}
	if branchID != trentonBranch {
		t.Errorf("print job branch = %q, want the report's branch", branchID)
	}

	// The artefact carries the rendered document and the frozen totals.
	stored, err := svc.RunArtefact(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.HTML, "<table>") || !strings.Contains(stored.HTML, seededSalesByDayTitle) {
		t.Error("the stored artefact does not contain a rendered report")
	}
	if !strings.Contains(stored.HTML, "367.50") {
		t.Error("the stored artefact does not carry the day's total as money, it is not a document, it is a shell")
	}
	if got, ok := stored.Totals["total_cents"].(float64); !ok || int64(got) != 36_750 {
		t.Errorf("frozen totals[total_cents] = %#v, want 36750", stored.Totals["total_cents"])
	}
	if stored.Params["branch_id"] != trentonBranch || stored.Params["from_date"] != from {
		t.Errorf("frozen params = %v, an artefact that does not say what it is a report OF cannot be "+
			"reproduced or defended", stored.Params)
	}
	if stored.RowCount != 1 {
		t.Errorf("frozen row_count = %d, want 1", stored.RowCount)
	}
}

// TestIntegration_Print_ConsolidatedReportRefusesWithoutABranch, print_queue.branch_id is NOT NULL
// because paper comes out of a printer that lives somewhere. A consolidated report must be told where.
func TestIntegration_Print_ConsolidatedReportRefusesWithoutABranch(t *testing.T) {
	pool := intPool(t)
	svc := globalSvc(t, pool)
	from, to := window()

	_, err := svc.Print(context.Background(), "sales_by_branch",
		map[string]string{"from_date": from, "to_date": to}, "")
	if err == nil {
		t.Fatal("a consolidated report printed with no branch named")
	}
	if !isBadRequest(err) {
		t.Errorf("error is %v, want a 400 that tells the operator what to do", err)
	}
	if !strings.Contains(err.Error(), "branch") {
		t.Errorf("the message does not mention the branch: %q", err.Error())
	}

	// Naming one works.
	run, err := svc.Print(context.Background(), "sales_by_branch",
		map[string]string{"from_date": from, "to_date": to}, kingstonBranch)
	if err != nil {
		t.Fatalf("printing with an explicit branch failed: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `UPDATE report_runs SET print_job_id=NULL WHERE id=$1::uuid`, run.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM print_queue WHERE id=$1::uuid`, run.PrintJobID)
		_, _ = pool.Exec(ctx, `DELETE FROM report_runs WHERE id=$1::uuid`, run.ID)
	})
}

// TestIntegration_Export_CSVCarriesTheSameNumbersAsTheRun, the export parity AC against real data.
func TestIntegration_Export_CSVCarriesTheSameNumbersAsTheRun(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	cust := f.customer("acme")
	f.invoicePaid(cust, trentonBranch, "", from, 10_000, 500)
	f.invoicePaid(cust, trentonBranch, "", addDays(from, 1), 25_000, 1_250)

	params := map[string]string{"from_date": from, "to_date": to, "branch_id": trentonBranch}
	res, body, err := svc.Export(context.Background(), "sales_by_day", params)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("the export is not parseable CSV: %v", err)
	}
	// 3 title-block rows + header + 2 data rows + totals.
	if len(recs) != 7 {
		t.Fatalf("csv has %d records, want 7: %v", len(recs), recs)
	}
	if recs[0][0] != seededSalesByDayTitle {
		t.Errorf("csv title = %q", recs[0][0])
	}
	if !strings.Contains(recs[1][1], "branch_id="+trentonBranch) {
		t.Errorf("the csv parameter row does not name the branch: %q", recs[1][1])
	}
	// The totals row must equal the Result's totals, formatted.
	totalIdx := -1
	for i, c := range res.Columns {
		if c.Name == "total_cents" {
			totalIdx = i
		}
	}
	if recs[6][totalIdx] != "367.50" {
		t.Errorf("csv total = %q, want 367.50 (105.00 + 262.50)", recs[6][totalIdx])
	}
	if got, _ := res.Totals["total_cents"].(int64); got != 36_750 {
		t.Errorf("the run's own total is %d cents, the CSV and the screen disagree", got)
	}
}

// TestIntegration_CommissionPrintAndExport, the parity requirement for commissions: the SAME artefact
// substrate, the SAME REPORT doc type, and a printed document that carries the accrual caveat.
func TestIntegration_CommissionPrintAndExport(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	ctx := context.Background()
	from, to := window()

	rep, _ := twoUsers(t, pool)
	cust := f.customer("acme")
	f.repAssignment(cust, "", rep)
	prod := f.product("PAV1", "Paver", f.category("pavers"))
	ord := f.order(cust, trentonBranch, from)
	inv := f.invoicePaid(cust, trentonBranch, ord, from, 20_000, 0)
	f.invoiceLine(inv, prod, 1, 10, 2_000, 20_000)
	f.commissionRule(rep, BasisRevenue, 500, "", "", from)

	frozen, err := svc.ComputeAndFreeze(ctx, rep, from, to, trentonBranch)
	if err != nil {
		t.Fatal(err)
	}
	f.del(`DELETE FROM commission_run_lines WHERE run_id=$1::uuid`, frozen.RunID)
	f.del(`DELETE FROM commission_runs WHERE id=$1::uuid`, frozen.RunID)

	// Export.
	period, body, err := svc.ExportCommissionRun(ctx, frozen.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if period.CommissionCents != 1_000 {
		t.Errorf("exported period commission = %d, want 1000", period.CommissionCents)
	}
	if !strings.Contains(string(body), "10.00") {
		t.Error("the commission CSV does not carry the commission as a plain decimal")
	}

	// Print.
	run, err := svc.PrintCommissionRun(ctx, frozen.RunID, "")
	if err != nil {
		t.Fatal(err)
	}
	f.del(`DELETE FROM report_runs WHERE id=$1::uuid`, run.ID)
	f.del(`UPDATE report_runs SET print_job_id=NULL WHERE id=$1::uuid`, run.ID)
	f.del(`DELETE FROM print_queue WHERE id=$1::uuid`, run.PrintJobID)

	if run.Kind != RunKindCommission {
		t.Errorf("commission artefact kind = %q, want %q", run.Kind, RunKindCommission)
	}
	var docType string
	if err := pool.QueryRow(ctx, `SELECT doc_type FROM print_queue WHERE id=$1::uuid`, run.PrintJobID).
		Scan(&docType); err != nil {
		t.Fatal(err)
	}
	if docType != PrintDocType {
		t.Errorf("commission print job doc_type = %q, want %q, one substrate, one doc type", docType, PrintDocType)
	}
	stored, err := svc.RunArtefact(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.HTML, "not posted") {
		t.Error("the PRINTED commission statement does not carry the accrual caveat, a printout is the " +
			"thing most likely to be read by someone who never saw the screen")
	}
}

// TestIntegration_BranchFence_ScopedCallerIsHeldToTheirBranch, the DIB-391 fence, three ways.
func TestIntegration_BranchFence_ScopedCallerIsHeldToTheirBranch(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	from, to := window()
	kingston := scopedSvc(t, pool, kingstonBranch)

	// 1 · their own branch: allowed.
	if _, err := kingston.Run(ctx, "sales_by_day", map[string]string{
		"from_date": from, "to_date": to, "branch_id": kingstonBranch}); err != nil {
		t.Errorf("a Kingston manager was refused their OWN branch: %v", err)
	}
	// 2 · another branch: refused.
	_, err := kingston.Run(ctx, "sales_by_day", map[string]string{
		"from_date": from, "to_date": to, "branch_id": trentonBranch})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("a Kingston manager read Trenton's sales (err=%v)", err)
	}
	// 3 · consolidated (no branch named): refused. This is the case that is easy to miss, because
	// "no parameter" looks like "no filter" rather than "the whole company".
	_, err = kingston.Run(ctx, "sales_by_day", map[string]string{"from_date": from, "to_date": to})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("a scoped manager read the CONSOLIDATED books by simply omitting branch_id (err=%v)", err)
	}
	// And a report with no branch dimension at all is consolidated by construction.
	_, err = kingston.Run(ctx, "sales_by_branch", map[string]string{"from_date": from, "to_date": to})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("a scoped manager read the all-branch sales report (err=%v)", err)
	}
}

// TestIntegration_UnknownTemplateCodeIs404, including a code that exists in neither half.
func TestIntegration_UnknownTemplateCodeIs404(t *testing.T) {
	pool := intPool(t)
	svc := globalSvc(t, pool)
	for _, code := range []string{"no_such_report", "", "sales_by_day; DROP TABLE invoices"} {
		if _, err := svc.Run(context.Background(), code, map[string]string{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("running %q gave %v, want ErrNotFound", code, err)
		}
	}
}

// TestIntegration_SavedView_RoundTripsAndRevalidatesOnRun, a saved view stores PARAMETERS, and the
// validation lives on the run path, so a stored view can never be a way past the allowlist.
func TestIntegration_SavedView_RoundTripsAndRevalidatesOnRun(t *testing.T) {
	pool := intPool(t)
	svc := globalSvc(t, pool)
	ctx := context.Background()
	from, to := window()

	v, err := svc.SaveView(ctx, "sales_by_day", "Trenton, this window",
		map[string]string{"from_date": from, "to_date": to, "branch_id": trentonBranch}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM report_saved_views WHERE id=$1::uuid`, v.ID)
	})

	views, err := svc.SavedViews(ctx, "sales_by_day")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, got := range views {
		if got.ID == v.ID {
			found = true
			if got.Params["branch_id"] != trentonBranch {
				t.Errorf("saved params round-tripped as %v", got.Params)
			}
		}
	}
	if !found {
		t.Fatal("the saved view is not in the caller's list")
	}

	// Re-saving under the same name UPDATES rather than creating a second indistinguishable row.
	v2, err := svc.SaveView(ctx, "sales_by_day", "Trenton, this window",
		map[string]string{"from_date": from, "to_date": to, "branch_id": kingstonBranch}, false)
	if err != nil {
		t.Fatal(err)
	}
	if v2.ID != v.ID {
		t.Errorf("re-saving under the same name created a second view (%s then %s)", v.ID, v2.ID)
	}

	// A view that could never run is refused AT SAVE TIME, otherwise it is a button that always errors.
	if _, err := svc.SaveView(ctx, "sales_by_day", "bad", map[string]string{"nonsense": "1"}, false); err == nil {
		t.Error("a saved view with an unknown parameter was accepted")
	}
	if _, err := svc.SaveView(ctx, "sales_by_day", "bad", map[string]string{
		"from_date": from, "to_date": to, "branch_id": "' OR 1=1--"}, false); err == nil {
		t.Error("a saved view carrying an injection payload was accepted")
	}
}

// TestIntegration_InjectionThroughTheRealQueryPath, the unit test proves bindParams refuses; this
// proves nothing gets past it on the way to Postgres, against the real database, on a real template.
func TestIntegration_InjectionThroughTheRealQueryPath(t *testing.T) {
	pool := intPool(t)
	svc := globalSvc(t, pool)
	ctx := context.Background()
	from, to := window()

	payloads := []string{
		"' OR 1=1--",
		"00000000-0000-0000-0000-000000000000'; DROP TABLE report_templates;--",
		"x' UNION SELECT password_hash, '', '', '', '' FROM users--",
	}
	for _, p := range payloads {
		if _, err := svc.Run(ctx, "sales_by_day", map[string]string{
			"from_date": from, "to_date": to, "branch_id": p}); err == nil {
			t.Errorf("payload %q reached the query path and RAN", p)
		} else if !isBadRequest(err) {
			t.Errorf("payload %q produced %v, want a 400", p, err)
		}
	}
	// The table the payload tried to drop is still there, and the catalogue still resolves.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM report_templates`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("report_templates is gone or unreadable after the injection attempts (n=%d, err=%v)", n, err)
	}
}

// TestIntegration_CategoriesListsTheWholeCatalogue, the nav a person actually sees.
func TestIntegration_CategoriesListsTheWholeCatalogue(t *testing.T) {
	pool := intPool(t)
	svc := globalSvc(t, pool)

	cats, err := svc.Categories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, c := range cats {
		if c.Label == "" {
			t.Errorf("category %q has no label", c.Code)
		}
		for _, tmpl := range c.Templates {
			total++
			if len(tmpl.Columns) == 0 {
				t.Errorf("template %s reached the surface with no columns", tmpl.Code)
			}
			if tmpl.LegacyName == "" {
				t.Errorf("template %s has no legacy name, the person migrating cannot find their report", tmpl.Code)
			}
		}
	}
	if total != len(catalog) {
		t.Errorf("the catalogue surfaced %d templates but the compiled catalog has %d", total, len(catalog))
	}
}

// TestIntegration_TemplateJSONNeverCarriesItsSQL, the query text must not reach a browser. It is not a
// secret in the cryptographic sense; it is a thing that ends up in screenshots, bug reports and support
// tickets, and from there into somebody's suggestion box as a feature request for "custom SQL".
func TestIntegration_TemplateJSONNeverCarriesItsSQL(t *testing.T) {
	pool := intPool(t)
	svc := globalSvc(t, pool)
	tmpl, err := svc.Template(context.Background(), "sales_by_day")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.sqlText() == "" {
		t.Fatal("the template resolved with no bound query, it could not be run")
	}
	blob := mustJSON(t, tmpl)
	for _, fragment := range []string{"SELECT", "FROM invoices", "$1"} {
		if strings.Contains(blob, fragment) {
			t.Errorf("the serialized template leaks its query (found %q): %s", fragment, blob)
		}
	}
}

// TestIntegration_Audit_ExportAndPrintAreActuallyRecorded, a regression test for a bug that shipped
// silently for about an hour and would have shipped for a wave.
//
// recordAccess deliberately swallows its errors: refusing to hand a manager their margin report because
// the audit table is busy would be the wrong trade. The cost of that decision is that a MALFORMED write
// is also swallowed, and the first version of this code put the template CODE in
// audit_log.entity_id, which is a UUID column. Every insert failed with SQLSTATE 22P02, the reports
// were served exactly as expected, a warning went to a log nobody was reading, and the audit trail was
// empty. Nothing on any screen said so.
//
// So this test uses the REAL audit service against the REAL table and reads the row back. A fake
// Recorder would have passed the whole time.
func TestIntegration_Audit_ExportAndPrintAreActuallyRecorded(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	ctx := context.Background()
	from, to := window()

	recorder := audit.NewService(audit.NewRepository(pool), slog.Default())
	svc := globalSvc(t, pool).WithAudit(recorder)

	cust := f.customer("acme")
	f.invoicePaid(cust, trentonBranch, "", from, 10_000, 500)
	params := map[string]string{"from_date": from, "to_date": to, "branch_id": trentonBranch}

	countRows := func(action string) int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE action = $1 AND changes->>'purpose' = 'sales_by_day'`,
			action).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	beforeExport, beforePrint := countRows(ActReportExport), countRows(ActReportPrint)

	if _, _, err := svc.Export(ctx, "sales_by_day", params); err != nil {
		t.Fatal(err)
	}
	if got := countRows(ActReportExport); got != beforeExport+1 {
		t.Errorf("an export wrote %d audit rows (was %d, now %d), the sensitive-access trail is EMPTY, "+
			"and because recordAccess swallows errors nothing on any screen would have said so",
			got-beforeExport, beforeExport, got)
	}

	run, err := svc.Print(ctx, "sales_by_day", params, "")
	if err != nil {
		t.Fatal(err)
	}
	f.del(`DELETE FROM report_runs WHERE id=$1::uuid`, run.ID)
	f.del(`UPDATE report_runs SET print_job_id=NULL WHERE id=$1::uuid`, run.ID)
	f.del(`DELETE FROM print_queue WHERE id=$1::uuid`, run.PrintJobID)

	if got := countRows(ActReportPrint); got != beforePrint+1 {
		t.Errorf("a print wrote %d audit rows (was %d, now %d)", got-beforePrint, beforePrint, got)
	}

	// The print row points at the ARTEFACT, carries the branch as a first-class column, and keeps the
	// date range intact through the redaction boundary.
	var entityType, entityID, branchID, reason string
	if err := pool.QueryRow(ctx, `
SELECT entity_type, COALESCE(entity_id::text,''), COALESCE(branch_id::text,''), COALESCE(changes->>'reason','')
  FROM audit_log
 WHERE action = $1 AND entity_id = $2::uuid`, ActReportPrint, run.ID).
		Scan(&entityType, &entityID, &branchID, &reason); err != nil {
		t.Fatalf("no audit row points at the printed artefact %s: %v", run.ID, err)
	}
	if entityType != "report_run" {
		t.Errorf("entity_type = %q, want report_run", entityType)
	}
	if branchID != trentonBranch {
		t.Errorf("branch_id = %q, want the report's branch, it is a column, not a redacted string", branchID)
	}
	if !strings.Contains(reason, from) || !strings.Contains(reason, to) {
		t.Errorf("reason = %q, want both dates, audit's redactor eats any value with a 7+ digit run, "+
			"which is why the branch uuid is NOT in this field", reason)
	}
}
