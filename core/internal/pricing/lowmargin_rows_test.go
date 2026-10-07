package pricing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// What a manager needs on each row to scan a quote or order for a below floor price: who sold it,
// the margin against the floor and the target, whether the line was cleared below the floor and by
// whom, and how the whole document sits, so a below floor line inside a healthy quote reads as such.

// stampLine puts a clearing stamp on a line: cleared at discPct percent off, at marginBps.
func stampLine(ln *LowMarginSaleLine, discPct int, marginBps int64) {
	ln.ClearStamped, ln.ClearStampDiscPct, ln.ClearStampMarginBps = true, &discPct, &marginBps
}

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
	cleared := docLine(DocTypeQuote, "q1", "cleared", 850) // 1558 bps, below the 2000 floor
	cleared.SalespersonID, cleared.SalespersonLabel = "u-sales", "Outside Sales, Trenton"
	stampLine(&cleared, 25, 1000)
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
	if c.MarginBps != 1558 || c.FloorBps != 2000 || c.TargetBps != 3300 {
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
	stampLine(&ln, 25, 1000)
	ln.ClearedByLabel, ln.ClearAuthority = "Sales Manager, Trenton", "APPROVAL"
	h := NewHandler(scanSvc(&fakeScan{lines: []LowMarginSaleLine{ln}}), nil)
	rec := httptest.NewRecorder()
	h.lowMarginScan(rec, httptest.NewRequest(http.MethodGet, "/api/v1/pricing/reports/low-margin/scan", nil), globalCaller())
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("%v %s", err, rec.Body.String())
	}
	r := page.Items[0]
	for k, want := range map[string]any{
		"salesperson": "Outside Sales, Trenton", "cleared_below_floor": true,
		"cleared_by": "Sales Manager, Trenton", "clear_authority": "APPROVAL",
		"margin_bps": float64(1558), "floor_bps": float64(2000), "target_bps": float64(3300),
		"doc_margin_bps": float64(1558), // (34000-28700)*1e4/34000, the line itself agrees
	} {
		if r[k] != want {
			t.Errorf("%s = %v, want %v (row %v)", k, r[k], want, r)
		}
	}
}

// The stamp stands for the discount it cleared and anything shallower; cleared_below_floor is true only
// when the line is below its floor NOW and a stamp covers it. Every stamped row carries the stamp's own
// figures beside the current ones, whatever the verdict.
func TestLowMarginRow_TheStampCoversTheDiscountItClearedAndShallowerOnly(t *testing.T) {
	cost := p64(175)
	mk := func(id string, gross int64, disc int) LowMarginSaleLine {
		ln := brusselsLine(id, gross, cost)
		ln.DocType, ln.DocID, ln.DocNo, ln.DiscPct = DocTypeQuote, "q-"+id, "Q-"+id, disc
		ln.ClearedByID, ln.ClearedByLabel, ln.ClearAuthority = "u-mgr", "Sales Manager, Trenton", "MANAGER_SCOPE"
		return ln
	}
	same := mk("same", 1000, 25) // net 750: below the 2000 floor, at the very discount the stamp cleared
	stampLine(&same, 25, 1000)
	shallower := mk("shallower", 1000, 15) // net 850: still below the floor, shallower than cleared
	stampLine(&shallower, 25, 1000)
	deeper := mk("deeper", 1000, 30) // net 700: below the floor, deeper than the stamp cleared
	stampLine(&deeper, 25, 1000)
	recovered := mk("recovered", 1000, 5) // net 950: margin back above the floor, below target (warn)
	stampLine(&recovered, 25, 1000)
	unstamped := mk("unstamped", 1000, 25)

	rows := scanOnce(t, same, shallower, deeper, recovered, unstamped)
	want := map[string]bool{"same": true, "shallower": true, "deeper": false, "recovered": false, "unstamped": false}
	for id, cleared := range want {
		r, ok := rowByLine(rows, id)
		if !ok {
			t.Fatalf("%s: not a row: %+v", id, rows)
		}
		if r.ClearedBelowFloor != cleared {
			t.Errorf("%s: cleared_below_floor = %v at level %q margin %d, want %v", id, r.ClearedBelowFloor, r.Level, r.MarginBps, cleared)
		}
	}
	rec, _ := rowByLine(rows, "recovered")
	if rec.Level != "warn" {
		t.Fatalf("the recovered line is no longer below its floor: level %q", rec.Level)
	}
	if rec.ClearDiscPct == nil || *rec.ClearDiscPct != 25 || rec.ClearMarginBps == nil || *rec.ClearMarginBps != 1000 || rec.ClearedBy != "Sales Manager, Trenton" {
		t.Errorf("a stamped row carries the stamp's own figures whatever the verdict: %+v", rec)
	}
	if u, _ := rowByLine(rows, "unstamped"); u.ClearDiscPct != nil || u.ClearMarginBps != nil {
		t.Errorf("an unstamped row carries no stamp figures: %+v", u)
	}

	f := window()
	f.ClearedOnly = true
	only, err := scanSvc(&fakeScan{lines: []LowMarginSaleLine{same, shallower, deeper, recovered, unstamped}}).LowMarginScan(context.Background(), f)
	if err != nil || !eq(sortedIDs(only), []string{"same", "shallower"}) {
		t.Errorf("cleared_only returns the covered lines below the floor now: %v err %v", ids(only), err)
	}
}

// A stamp with no discount figure cannot be shown to cover anything.
func TestLowMarginRow_AStampWithNoDiscountFigureCoversNothing(t *testing.T) {
	ln := docLine(DocTypeQuote, "q1", "bare", 850)
	ln.ClearStamped = true
	rows := scanOnce(t, ln)
	if len(rows) != 1 || rows[0].ClearedBelowFloor {
		t.Errorf("a stamp that names no discount covers no line: %+v", rows)
	}
}

// The row's margin and the document's agree for a one line document: both come off the line's own
// total and cost, rounded once, not one off a rounded unit price.
func TestLowMarginRow_OneLineDocumentRowAndDocumentMarginAgree(t *testing.T) {
	ln := docLine(DocTypeQuote, "q1", "one", 999)
	ln.DiscPct = 33
	ln.LineTotalCents = (999*40*67 + 50) / 100 // 26773, the quote's own net line total
	rows := scanOnce(t, ln)
	if len(rows) != 1 || rows[0].DocMarginBps == nil {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].MarginBps != *rows[0].DocMarginBps {
		t.Errorf("a one line document: row margin %d, document margin %d", rows[0].MarginBps, *rows[0].DocMarginBps)
	}
	if rows[0].MarginBps != -719 {
		t.Errorf("margin %d, want -719 ((26773-28700)*1e4/26773)", rows[0].MarginBps)
	}
}
