//go:build integration

// hh194_register_export_integration_test.go, HH-194: the receivables CSV export carries every row of
// the register it was cut from, not the first page of it.
//
// The register read is capped at 200 rows a page. The export is promised to carry the same filter
// and the same figures as the register read, so a dealer with more open invoices than one page holds
// must get all of them. Every fact is planted inside one customer and torn down by the fixture, and
// every read is fenced by that customer so nothing else in the database can move a number here.
//
// Run: go test -tags integration ./internal/billing/ -run HH194
package billing

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

const (
	hh194Overdue = 300 // due in the past, so on the OVERDUE chip
	hh194Future  = 150 // due well ahead, open but not overdue
)

// plantHH194 inserts more open invoices than one register page can hold, in one statement.
func plantHH194(t *testing.T, f *d510Fixture) {
	t.Helper()
	due := func(offset int) string { return d510Now.AddDate(0, 0, offset).Format("2006-01-02") }
	for _, g := range []struct {
		label  string
		n      int
		offset int
	}{{"LATE", hh194Overdue, -10}, {"AHEAD", hh194Future, 40}} {
		if _, err := f.pool.Exec(context.Background(),
			`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
			 SELECT $1 || '-' || $2 || '-' || lpad(n::text, 5, '0'), $3::uuid, $4::uuid, 'FINALIZED',
			        ($5::date - 30), $5::date, 1000 + n, 0, 1000 + n, 1000 + n, $5::date - 30, 'INVOICE'
			   FROM generate_series(1, $6::int) AS n`,
			f.tag, g.label, f.customerID, trentonBranch, due(g.offset), g.n); err != nil {
			t.Fatalf("plant %s invoices: %v", g.label, err)
		}
	}
}

// exportRows runs the real export handler and returns the invoice numbers in the order written.
func exportRows(t *testing.T, f *d510Fixture, query url.Values) []string {
	t.Helper()
	svc := f.service(d510Manager())
	h := NewRegisterHandler(svc, NewRepository(f.pool), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ar/register/export?"+query.Encode(), nil)
	rec := httptest.NewRecorder()
	h.export(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("export status %d: %s", rec.Code, rec.Body.String())
	}
	recs, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("export is not valid csv: %v", err)
	}
	if len(recs) == 0 || recs[0][0] != "Invoice" {
		t.Fatalf("export has no header row")
	}
	var out []string
	for _, r := range recs[1:] {
		if r[0] == "Invoice" {
			t.Fatalf("header repeated in the body")
		}
		out = append(out, r[0])
	}
	return out
}

// registerOrderFor pages the register read itself, the reference the export is held to.
func registerOrderFor(t *testing.T, f *d510Fixture, rf RegisterFilter) ([]string, int) {
	t.Helper()
	svc := f.service(d510Manager())
	var all []string
	total := 0
	for off := 0; ; off += 200 {
		rf.Limit, rf.Offset = 200, off
		page, err := svc.Register(context.Background(), rf)
		if err != nil {
			t.Fatalf("register read: %v", err)
		}
		total = page.Total
		for _, v := range page.Items {
			all = append(all, v.InvoiceNo)
		}
		if len(page.Items) == 0 || len(all) >= page.Total {
			return all, total
		}
	}
}

func assertExportMatchesRegister(t *testing.T, f *d510Fixture, query url.Values, rf RegisterFilter, want int) {
	t.Helper()
	ref, total := registerOrderFor(t, f, rf)
	if total != want {
		t.Fatalf("register total = %d, want %d (fixture is wrong)", total, want)
	}
	got := exportRows(t, f, query)
	if len(got) != total {
		t.Fatalf("export wrote %d rows, the register total is %d", len(got), total)
	}
	seen := map[string]bool{}
	for _, no := range got {
		if seen[no] {
			t.Fatalf("export repeats invoice %s", no)
		}
		seen[no] = true
	}
	for _, no := range ref {
		if !seen[no] {
			t.Fatalf("export is missing invoice %s", no)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(ref) {
		t.Fatalf("export order differs from the register read order")
	}
}

func TestHH194_ExportCarriesEveryRowOfTheRegister(t *testing.T) {
	f := newD510Fixture(t)
	plantHH194(t, f)
	assertExportMatchesRegister(t, f,
		url.Values{"customer_id": {f.customerID}},
		RegisterFilter{CustomerID: f.customerID},
		hh194Overdue+hh194Future)
}

func TestHH194_FilteredExportCarriesEveryFilteredRow(t *testing.T) {
	f := newD510Fixture(t)
	plantHH194(t, f)
	assertExportMatchesRegister(t, f,
		url.Values{"customer_id": {f.customerID}, "chip": {"overdue"}},
		RegisterFilter{CustomerID: f.customerID, Chip: StatusOverdue},
		hh194Overdue)
}
