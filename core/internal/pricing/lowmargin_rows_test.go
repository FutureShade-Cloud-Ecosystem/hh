package pricing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// What a manager needs on each row to scan a quote or order for a below floor price: who sold it,
// the margin against the floor and the target, whether the line was cleared below the floor and by
// whom, and how the whole document sits, so a below floor line inside a healthy quote reads as such.

const rowsCost = 175 // cents per PC, 718 per SF once converted

// docLine is a 40 SF Brussels line of a document: 164 PC at 175 cents is 28700 of cost, and a line
// total of unit*40.
func docLine(docType, doc, line string, unit int64) LowMarginSaleLine {
	ln := brusselsLine(line, unit, p64(rowsCost))
	ln.DocType, ln.DocID, ln.DocNo = docType, doc, "N-"+doc
	ln.LineTotalCents = unit * 40
	ln.LineCostCents = p64(28700)
	return ln
}

func scanOnce(t *testing.T, lines ...LowMarginSaleLine) []LowMarginScanRow {
	t.Helper()
	rows, err := scanSvc(&fakeScan{lines: lines}).LowMarginScan(context.Background(),
		LowMarginFilter{From: time.Now().AddDate(0, 0, -1), To: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestLowMarginRow_CarriesSalespersonAndTheClearing(t *testing.T) {
	cleared := docLine(DocTypeQuote, "q1", "cleared", 850) // 1552 bps, below the 2000 floor
	cleared.SalespersonID, cleared.SalespersonLabel = "u-sales", "Outside Sales, Trenton"
	cleared.ClearedBelowFloor = true
	cleared.ClearedByID, cleared.ClearedByLabel, cleared.ClearAuthority = "u-mgr", "Sales Manager, Trenton", "MANAGER_SCOPE"
	plain := docLine(DocTypeQuote, "q2", "plain", 850)
	plain.SalespersonID, plain.SalespersonLabel = "u-sales", "Outside Sales, Trenton"

	rows := scanOnce(t, cleared, plain)
	c, ok := rowByLine(rows, "cleared")
	if !ok {
		t.Fatalf("cleared line missing: %+v", rows)
	}
	if c.Salesperson != "Outside Sales, Trenton" || c.SalespersonID != "u-sales" {
		t.Errorf("salesperson not carried: %q / %q", c.Salesperson, c.SalespersonID)
	}
	if !c.ClearedBelowFloor || c.ClearedBy != "Sales Manager, Trenton" || c.ClearedByID != "u-mgr" || c.ClearAuthority != "MANAGER_SCOPE" {
		t.Errorf("clearing not carried: %+v", c)
	}
	if c.MarginBps != 1552 || c.FloorBps != 2000 || c.TargetBps != 3300 {
		t.Errorf("margin, floor, target: %d %d %d", c.MarginBps, c.FloorBps, c.TargetBps)
	}
	p, _ := rowByLine(rows, "plain")
	if p.ClearedBelowFloor || p.ClearedBy != "" || p.ClearedByID != "" || p.ClearAuthority != "" {
		t.Errorf("a line nobody cleared must carry no clearing: %+v", p)
	}
}

// A below floor line inside a healthy quote: the quote's other line is priced well, so the document's
// own margin is healthy while the line itself is critical.
func TestLowMarginRow_DocumentMarginShowsAHealthyQuoteAroundABelowFloorLine(t *testing.T) {
	low := docLine(DocTypeQuote, "q1", "low", 850)    // revenue 34000, cost 28700
	high := docLine(DocTypeQuote, "q1", "high", 2400) // revenue 96000, cost 28700 (healthy, omitted from rows)
	rows := scanOnce(t, low, high)
	if len(rows) != 1 || rows[0].LineID != "low" {
		t.Fatalf("only the below floor line is a row: %+v", rows)
	}
	// (34000+96000 - 57400) * 10000 / 130000 = 5584 bps
	if rows[0].DocMarginBps == nil || *rows[0].DocMarginBps != 5584 {
		t.Errorf("document margin = %v, want 5584", rows[0].DocMarginBps)
	}
	if rows[0].Level != "critical" {
		t.Errorf("the line itself is still critical: %q", rows[0].Level)
	}
}

// A document with a line whose cost is unknown has no honest overall margin: it is absent, never a
// plausible number computed off the costed lines alone.
func TestLowMarginRow_DocumentMarginAbsentWhenACostIsMissing(t *testing.T) {
	low := docLine(DocTypeQuote, "q1", "low", 850)
	nocost := docLine(DocTypeQuote, "q1", "nocost", 900)
	nocost.AvgUnitCostCents, nocost.LineCostCents = nil, nil
	for _, r := range scanOnce(t, low, nocost) {
		if r.DocMarginBps != nil {
			t.Errorf("row %s: document margin %d with an uncosted line in the document, want none", r.LineID, *r.DocMarginBps)
		}
	}
}

func TestLowMarginRow_JSONNames(t *testing.T) {
	ln := docLine(DocTypeOrder, "o1", "l", 850)
	ln.SalespersonLabel = "Outside Sales, Trenton"
	ln.ClearedBelowFloor, ln.ClearedByLabel, ln.ClearAuthority = true, "Sales Manager, Trenton", "APPROVAL"
	h := NewHandler(scanSvc(&fakeScan{lines: []LowMarginSaleLine{ln}}), nil)
	rec := httptest.NewRecorder()
	h.lowMarginScan(rec, httptest.NewRequest(http.MethodGet, "/api/v1/pricing/reports/low-margin/scan", nil), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("%v %s", err, rec.Body.String())
	}
	r := rows[0]
	for k, want := range map[string]any{
		"salesperson": "Outside Sales, Trenton", "cleared_below_floor": true,
		"cleared_by": "Sales Manager, Trenton", "clear_authority": "APPROVAL",
		"margin_bps": float64(1552), "floor_bps": float64(2000), "target_bps": float64(3300),
		"doc_margin_bps": float64(1558), // (34000-28700)*1e4/34000, the line itself is 1552 on the rounded 718
	} {
		if r[k] != want {
			t.Errorf("%s = %v, want %v (row %v)", k, r[k], want, r)
		}
	}
}
