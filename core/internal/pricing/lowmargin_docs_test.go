package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// The low margin report covers quotes as well as orders. A document type filter (quotes, orders,
// both) picks the kinds, and a quote line is graded exactly as an order line is, at the price the
// quote itself holds: the stored unit price taken net of the line's discount percent, because a
// below floor price reaches a quote through that discount and the stored unit price is the engine's
// gross figure.

func quoteLine(id string, gross int64, discPct int, cost *int64) LowMarginSaleLine {
	ln := brusselsLine(id, gross, cost)
	ln.DocType, ln.DocID, ln.DocNo = DocTypeQuote, "q-"+id, "Q-"+id
	ln.DiscPct = discPct
	return ln
}

func TestLowMarginScan_DocTypeFilterReachesTheFeed(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ScanDocsBoth}, {ScanDocsBoth, ScanDocsBoth},
		{ScanDocsQuotes, ScanDocsQuotes}, {ScanDocsOrders, ScanDocsOrders},
	} {
		scan := &fakeScan{}
		_, err := scanSvc(scan).LowMarginScan(context.Background(),
			LowMarginFilter{From: time.Now().AddDate(0, 0, -1), To: time.Now(), DocType: c.in})
		if err != nil {
			t.Fatalf("doc type %q: %v", c.in, err)
		}
		if scan.got.DocType != c.want {
			t.Errorf("doc type %q reached the feed as %q, want %q", c.in, scan.got.DocType, c.want)
		}
	}
}

func TestLowMarginScan_UnknownDocTypeIsABadRequest(t *testing.T) {
	_, err := scanSvc(&fakeScan{}).LowMarginScan(context.Background(),
		LowMarginFilter{From: time.Now().AddDate(0, 0, -1), To: time.Now(), DocType: "invoices"})
	var bre badRequestError
	if !errors.As(err, &bre) {
		t.Fatalf("an unknown doc type must be a bad request (400), got %v", err)
	}
}

// A quote line is graded at its own net price, and a row says which kind of document it came from.
func TestLowMarginScan_QuoteLineGradedAtTheQuotesOwnNetPrice(t *testing.T) {
	cost := p64(175) // 718 cents per SF once converted
	scan := &fakeScan{lines: []LowMarginSaleLine{
		quoteLine("qdisc", 1000, 15, cost), // net (1000*85+50)/100 = 850: margin 1552 bps, critical
		quoteLine("qfull", 1000, 0, cost),  // 1000 at no discount: margin 2820 bps, warn
		brusselsLine("ord", 850, cost),     // the order side, critical as before
	}}
	rows, err := scanSvc(scan).LowMarginScan(context.Background(),
		LowMarginFilter{From: time.Now().AddDate(0, 0, -1), To: time.Now(), DocType: ScanDocsBoth})
	if err != nil {
		t.Fatal(err)
	}
	disc, ok := rowByLine(rows, "qdisc")
	if !ok {
		t.Fatalf("the discounted quote line is missing: %+v", rows)
	}
	if disc.DocType != DocTypeQuote || disc.DocNo != "Q-qdisc" {
		t.Errorf("row must name its document: type %q no %q", disc.DocType, disc.DocNo)
	}
	if disc.UnitPriceCents != 850 || disc.MarginBps != 1552 || disc.Level != "critical" {
		t.Errorf("a quote line is graded at its net price: price %d margin %d level %q, want 850 / 1552 / critical",
			disc.UnitPriceCents, disc.MarginBps, disc.Level)
	}
	full, ok := rowByLine(rows, "qfull")
	if !ok || full.Level != "warn" || full.MarginBps != 2820 {
		t.Errorf("an undiscounted quote line at 1000: %+v ok=%v, want warn at 2820 bps", full, ok)
	}
	if o, ok := rowByLine(rows, "ord"); !ok || o.DocType != DocTypeOrder {
		t.Errorf("the order line must still ride the same report as an order: %+v ok=%v", o, ok)
	}
}

func TestLowMarginScan_QuotesOnlyAndOrdersOnly(t *testing.T) {
	cost := p64(175)
	scan := &fakeScan{lines: []LowMarginSaleLine{quoteLine("q", 850, 0, cost), brusselsLine("o", 850, cost)}}
	svc := scanSvc(scan)
	window := LowMarginFilter{From: time.Now().AddDate(0, 0, -1), To: time.Now()}
	for _, c := range []struct{ docType, want string }{{ScanDocsQuotes, "q"}, {ScanDocsOrders, "o"}} {
		f := window
		f.DocType = c.docType
		rows, err := svc.LowMarginScan(context.Background(), f)
		if err != nil || len(rows) != 1 || rows[0].LineID != c.want {
			t.Errorf("%s: rows %+v err %v, want only line %q", c.docType, rows, err, c.want)
		}
	}
}

func TestLowMarginScanHandler_DocTypeParam(t *testing.T) {
	scan := &fakeScan{lines: []LowMarginSaleLine{quoteLine("q", 850, 0, p64(175))}}
	h := NewHandler(scanSvc(scan), nil)
	get := func(url string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.lowMarginScan(rec, httptest.NewRequest(http.MethodGet, url, nil), guards.StaffCaller{})
		return rec
	}
	if rec := get("/api/v1/pricing/reports/low-margin/scan?doc_type=quotes"); rec.Code != http.StatusOK {
		t.Fatalf("doc_type=quotes → %d (%s)", rec.Code, rec.Body.String())
	} else {
		var rows []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0]["doc_type"] != "quote" {
			t.Errorf("want one quote row carrying doc_type quote, got %s (%v)", rec.Body.String(), err)
		}
	}
	if scan.got.DocType != ScanDocsQuotes {
		t.Errorf("doc_type not threaded: %q", scan.got.DocType)
	}
	if rec := get("/api/v1/pricing/reports/low-margin/scan?doc_type=invoices"); rec.Code != http.StatusBadRequest {
		t.Errorf("doc_type=invoices → %d, want 400", rec.Code)
	}
}
