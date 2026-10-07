package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"testing"
	"time"
)

// Sorting by margin (ascending by default, descending on ask), the filters a manager narrows with
// (branch, salesperson, document date range, cleared only) and a cursor page over the result.

// pageLines is five Brussels lines whose margins rise with the price: 850, 900, 950, 1000, 1050 cents
// per SF against 718, plus one line with no cost (invalid, always last).
func pageLines() []LowMarginSaleLine {
	var out []LowMarginSaleLine
	for i, price := range []int64{950, 850, 1050, 900, 1000} {
		out = append(out, docLine(DocTypeQuote, "q"+strconv.Itoa(i), "l"+strconv.FormatInt(price, 10), price))
	}
	nc := docLine(DocTypeQuote, "qn", "lnocost", 900)
	nc.AvgUnitCostCents, nc.LineCostCents = nil, nil
	return append(out, nc)
}

func ids(rows []LowMarginScanRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.LineID)
	}
	return out
}

func sortedIDs(rows []LowMarginScanRow) []string {
	out := ids(rows)
	sort.Strings(out)
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func window() LowMarginFilter {
	return LowMarginFilter{From: time.Now().AddDate(0, 0, -1), To: time.Now()}
}

func TestLowMarginSort_AscendingIsTheDefaultAndDescendingOnAsk(t *testing.T) {
	svc := scanSvc(&fakeScan{lines: pageLines()})
	for _, c := range []struct {
		sort string
		want []string
	}{
		{"", []string{"l850", "l900", "l950", "l1000", "l1050", "lnocost"}},
		{SortMarginAsc, []string{"l850", "l900", "l950", "l1000", "l1050", "lnocost"}},
		// descending reverses the margins, and the line with no margin still sits last.
		{SortMarginDesc, []string{"l1050", "l1000", "l950", "l900", "l850", "lnocost"}},
	} {
		f := window()
		f.Sort = c.sort
		rows, err := svc.LowMarginScan(context.Background(), f)
		if err != nil || !eq(ids(rows), c.want) {
			t.Errorf("sort %q: %v err %v, want %v", c.sort, ids(rows), err, c.want)
		}
	}
	f := window()
	f.Sort = "price"
	var bre badRequestError
	if _, err := svc.LowMarginScan(context.Background(), f); !errors.As(err, &bre) {
		t.Errorf("an unknown sort must be a bad request, got %v", err)
	}
}

func TestLowMarginFilters_SalespersonAndClearedOnlyAndBranch(t *testing.T) {
	a := docLine(DocTypeQuote, "qa", "a", 850)
	a.SalespersonID = "sp-1"
	b := docLine(DocTypeQuote, "qb", "b", 860)
	b.SalespersonID, b.ClearedByID, b.ClearAuthority = "sp-2", "mgr", "MANAGER_SCOPE"
	stampLine(&b, 25, 1000)
	c := docLine(DocTypeQuote, "qc", "c", 870)
	c.SalespersonID, c.ClearAuthority = "sp-1", "APPROVAL"
	stampLine(&c, 25, 1000)
	scan := &fakeScan{lines: []LowMarginSaleLine{a, b, c}}
	svc := scanSvc(scan)

	f := window()
	f.SalespersonID = "sp-1"
	rows, _ := svc.LowMarginScan(context.Background(), f)
	if !eq(ids(rows), []string{"a", "c"}) || scan.got.SalespersonID != "sp-1" {
		t.Errorf("salesperson filter: %v (feed saw %q)", ids(rows), scan.got.SalespersonID)
	}

	f = window()
	f.ClearedOnly = true
	rows, _ = svc.LowMarginScan(context.Background(), f)
	if !eq(ids(rows), []string{"b", "c"}) {
		t.Errorf("cleared only: %v, want b and c", ids(rows))
	}

	f.SalespersonID = "sp-1"
	rows, _ = svc.LowMarginScan(context.Background(), f)
	if !eq(ids(rows), []string{"c"}) {
		t.Errorf("cleared only and salesperson: %v, want c", ids(rows))
	}

	f = window()
	f.BranchID = "trenton"
	_, _ = svc.LowMarginScan(context.Background(), f)
	if scan.got.BranchID != "trenton" {
		t.Errorf("branch filter not forwarded: %q", scan.got.BranchID)
	}
}

func TestLowMarginPage_CursorWalksEveryRowOnceInBothDirections(t *testing.T) {
	svc := scanSvc(&fakeScan{lines: pageLines()})
	for _, sort := range []string{SortMarginAsc, SortMarginDesc} {
		f := window()
		f.Sort = sort
		all, _ := svc.LowMarginScan(context.Background(), f)
		var walked []string
		cursor, pages := "", 0
		for {
			page, err := svc.LowMarginScanPage(context.Background(), f, cursor, 2)
			if err != nil {
				t.Fatalf("%s page %d: %v", sort, pages, err)
			}
			pages++
			if len(page.Items) > 2 || page.Limit != 2 {
				t.Fatalf("%s page %d: %d items limit %d", sort, pages, len(page.Items), page.Limit)
			}
			walked = append(walked, ids(page.Items)...)
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
			if pages > 10 {
				t.Fatal("cursor never ends")
			}
		}
		if !eq(walked, ids(all)) || pages != 3 {
			t.Errorf("%s: walked %v in %d pages, want %v in 3", sort, walked, pages, ids(all))
		}
	}
}

func TestLowMarginPage_LastFullPageHasNoCursorAndAnEmptyReportIsAnEmptyPage(t *testing.T) {
	svc := scanSvc(&fakeScan{lines: pageLines()[:2]})
	page, err := svc.LowMarginScanPage(context.Background(), window(), "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor != nil {
		t.Errorf("a report that exactly fills the page ends there: %+v err %v", page, err)
	}
	empty, err := scanSvc(&fakeScan{}).LowMarginScanPage(context.Background(), window(), "", 50)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil || empty.Limit != 50 {
		t.Errorf("an empty report must be an empty (non nil) page: %+v err %v", empty, err)
	}
}

func TestLowMarginPage_BadCursorAndBadLimitAreBadRequests(t *testing.T) {
	svc := scanSvc(&fakeScan{lines: pageLines()})
	asc := window()
	first, _ := svc.LowMarginScanPage(context.Background(), asc, "", 2)
	desc := window()
	desc.Sort = SortMarginDesc
	var bre badRequestError
	for name, c := range map[string]struct {
		f      LowMarginFilter
		cursor string
		limit  int
	}{
		"garbage":        {asc, "not-a-cursor", 2},
		"another sort":   {desc, *first.NextCursor, 2},
		"limit over 200": {asc, "", 201},
		"negative limit": {asc, "", -1},
	} {
		if _, err := svc.LowMarginScanPage(context.Background(), c.f, c.cursor, c.limit); !errors.As(err, &bre) {
			t.Errorf("%s: want a bad request, got %v", name, err)
		}
	}
	// A zero limit is the default page of fifty.
	if page, err := svc.LowMarginScanPage(context.Background(), asc, "", 0); err != nil || page.Limit != 50 {
		t.Errorf("zero limit → %+v err %v, want the default 50", page, err)
	}
}

func TestLowMarginScanHandler_PageEnvelopeAndQueryParams(t *testing.T) {
	scan := &fakeScan{lines: pageLines()}
	h := NewHandler(scanSvc(scan), nil)
	get := func(q string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.lowMarginScan(rec, httptest.NewRequest(http.MethodGet, "/api/v1/pricing/reports/low-margin/scan"+q, nil), globalCaller())
		return rec
	}
	rec := get("?limit=2&sort=margin_desc&cleared_only=false&salesperson_id=5d1b3f6e-0000-4000-9000-0000000000aa")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
		Limit      int              `json:"limit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.Limit != 2 || scan.got.SalespersonID == "" {
		t.Fatalf("envelope: %v %s (feed saw %+v)", err, rec.Body.String(), scan.got)
	}
	scan.lines = pageLines() // the salesperson filter above matched nothing in the fixtures
	rec = get("?limit=2&sort=margin_desc")
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 2 || page.Items[0]["line_id"] != "l1050" || page.NextCursor == nil {
		t.Fatalf("first descending page: %s", rec.Body.String())
	}
	rec = get("?limit=2&sort=margin_desc&cursor=" + *page.NextCursor)
	var next struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &next)
	if rec.Code != 200 || len(next.Items) != 2 || next.Items[0]["line_id"] != "l950" {
		t.Fatalf("second descending page: %d %s", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{
		"?sort=price", "?salesperson_id=not-a-uuid", "?cleared_only=maybe", "?limit=0", "?limit=abc", "?limit=201", "?cursor=junk",
	} {
		if rec := get(bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s → %d, want 400", bad, rec.Code)
		}
	}
}
