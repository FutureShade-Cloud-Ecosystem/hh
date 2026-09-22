package inventory

// dib452_browse_paging_test.go, DIB-452: the browse read pages, so the catalogue's availability
// column can cover every row it renders.
//
// THE DEFECT THIS ENCODES
// -----------------------
// The catalogue list is unpaged; the availability read behind its "Available" column clamped itself
// to 200 rows and had no way to ask for the next 200. Past the 200th matching product the extra rows
// had no bucket to join to, so they rendered an em dash, and an em dash in that column means "we do
// not know yet", not "we stopped asking". The screen made a claim about the data that was really a
// claim about the read.
//
// The cap is not the bug and raising it is not the fix (see Browse's comment: three correlated
// lateral aggregates per row, plus a facet count per facet re-running the same predicate). The bug
// is that there was no page two.
//
// WHAT LIVES WHERE. This file is the DB-free half: the clamp, the has-more arithmetic, and the
// handler's grade of ?offset=. The claims that are really claims about Postgres, that page N+1 does
// not repeat or skip a row of page N, and that the cap still holds against real SQL, are in
// dib452_browse_paging_integration_test.go, because a paging test that never runs the query is a
// test of arithmetic somebody already agreed with.
//
// MUTATION TARGETS. Each dies on a specific plausible edit:
//
//	off-by-one in HasMore (<= for <)              -> TestBrowseHasMore_TheBoundaryPage
//	drop the offset clamp / cap in browsePage     -> TestBrowsePage_ClampsToOnePageAndNonNegative
//	let a bad ?offset= default to 0 silently      -> TestBrowseHandler_BadOffsetIs400
//	require ?offset= (empty read as an error)     -> TestBrowseHandler_NoOffsetIsTheFirstPage

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// - the clamp -

func TestBrowsePage_ClampsToOnePageAndNonNegative(t *testing.T) {
	for _, c := range []struct {
		name                  string
		limit, offset         int
		wantLimit, wantOffset int
	}{
		// The pre-existing clamp, unchanged by this ticket on purpose: out of range falls back to the
		// default page size, NOT to the maximum. Every caller that shipped before DIB-452 has been
		// getting 60 for a nonsense limit, and a paging ticket is not the place to re-tune that.
		{"unspecified", 0, 0, browseDefaultLimit, 0},
		{"negative limit", -5, 0, browseDefaultLimit, 0},
		{"over the cap", browseMaxLimit + 1, 0, browseDefaultLimit, 0},
		{"absurd", 100000, 0, browseDefaultLimit, 0},
		// A limit AT the cap is honoured, this is the one the catalogue asks for, and the whole
		// point of the ticket is that it can now ask for it repeatedly.
		{"at the cap", browseMaxLimit, 0, browseMaxLimit, 0},
		{"under the cap", 25, 0, 25, 0},
		// Offsets.
		{"second page", browseMaxLimit, browseMaxLimit, browseMaxLimit, browseMaxLimit},
		{"deep page", 50, 4321, 50, 4321},
		{"negative offset floors at zero", 50, -1, 50, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			gotLimit, gotOffset := browsePage(c.limit, c.offset)
			if gotLimit != c.wantLimit || gotOffset != c.wantOffset {
				t.Fatalf("browsePage(%d, %d) = (%d, %d), want (%d, %d)",
					c.limit, c.offset, gotLimit, gotOffset, c.wantLimit, c.wantOffset)
			}
		})
	}
}

func TestBrowsePage_CapIsStillTwoHundred(t *testing.T) {
	// The cap is load-bearing for the frontend's request-count property: the catalogue sizes its
	// pages to it, and "one batched request per page" is only bounded if the page is. If this
	// number moves, the pager's arithmetic and the DIB-439 measurement move with it, so it is
	// asserted rather than merely written down.
	if browseMaxLimit != 200 {
		t.Fatalf("browseMaxLimit = %d, want 200, the catalogue's STOCK_READ_LIMIT and the export's "+
			"stockReadLimit both mirror this; move all three together or not at all", browseMaxLimit)
	}
	if got, _ := browsePage(browseMaxLimit+1, 0); got > browseMaxLimit {
		t.Fatalf("a limit over the cap came back as %d, one call must never answer for more than %d rows",
			got, browseMaxLimit)
	}
}

// - the boundary: where an off-by-one would live -

func TestBrowseHasMore_TheBoundaryPage(t *testing.T) {
	// The whole reason HasMore is a method on the result rather than a subtraction at each call
	// site: this comparison is where a pager stops one page early (losing the tail, the exact
	// symptom DIB-452 reports) or loops forever (asking for a page past the end, in a loop).
	rows := func(n int) []BrowseRow { return make([]BrowseRow, n) }
	for _, c := range []struct {
		name          string
		offset, total int
		got           int
		want          bool
	}{
		{"a full first page with more behind it", 0, 292, 200, true},
		// THE BOUNDARY. 200 of 292 read: 92 to go. Off by one in either direction and the last 92
		// rows are the em dash this ticket exists to remove.
		{"the second page finishes the set", 200, 292, 92, false},
		{"a set that divides exactly, the last full page", 0, 400, 200, true},
		{"…and the page that completes it", 200, 400, 200, false},
		{"one page holds everything", 0, 32, 32, false},
		{"nothing matched at all", 0, 0, 0, false},
		// Defensive: a page that came back short of what the total implied (a row deleted between
		// the two queries) must still terminate rather than spin.
		{"a short page still says there is more", 0, 292, 199, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := BrowseResult{Rows: rows(c.got), Total: c.total, Offset: c.offset, Limit: 200}
			if got := res.HasMore(); got != c.want {
				t.Fatalf("HasMore() = %v, want %v (offset %d + %d rows of %d total)",
					got, c.want, c.offset, c.got, c.total)
			}
		})
	}
}

// - the handler's grade of ?offset= -

// browseHandler builds the handler over a nil pool. Every assertion here is about what happens
// BEFORE the query, a graded request never reaches the repository, and one that passes the grade
// fails later at the (absent) database, which is exactly how these two cases are told apart.
func browseHandler() *Handler {
	return NewHandler(NewService(NewRepository(nil)),
		slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
}

func TestBrowseHandler_BadOffsetIs400(t *testing.T) {
	h := browseHandler()
	for _, bad := range []string{
		"-1",    // backwards
		"-200",  // backwards by a page
		"two",   // words
		"1e3",   // scientific notation, which Atoi does not take
		"200.5", // a fraction of a row
		// An ABSENT offset is deliberately not in this list: it is the first page, not an error.
		// TestBrowseHandler_NoOffsetIsTheFirstPage owns that case.
	} {
		rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.browse(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/browse?offset="+urlQ(bad))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("offset=%q: want 400, got %d: %s, reading a bad offset as 0 hands the caller "+
				"page ONE while it believes it is merging page three, which is silent data loss "+
				"wearing a 200", bad, rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), "offset") {
			t.Fatalf("offset=%q: the 400 must name the field, got %s", bad, rec.Body)
		}
	}
}

func TestBrowseHandler_NoOffsetIsTheFirstPage(t *testing.T) {
	// Empty is never an error. An absent offset is the first page, which is every caller that
	// existed before this ticket, so grading it as required would have broken all of them.
	h := browseHandler()
	for name, url := range map[string]string{
		"omitted":         "/api/v1/inventory/browse",
		"present-empty":   "/api/v1/inventory/browse?offset=",
		"whitespace-only": "/api/v1/inventory/browse?offset=%20%20",
		"explicit zero":   "/api/v1/inventory/browse?offset=0",
	} {
		rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.browse(w, r, guards.StaffCaller{}) }, url)
		// The nil pool means this cannot reach 200; what matters is that it got PAST the grade, a
		// 400 here would mean the first page had become a bad request.
		if rec.Code == http.StatusBadRequest {
			t.Fatalf("%s: an absent offset is the first page, not a bad request: %s", name, rec.Body)
		}
	}
}

func TestOffsetParam_AcceptsAPageBoundary(t *testing.T) {
	// The value the catalogue's pager actually sends, one page in. Trivial-looking, and it is the
	// case that breaks if someone "tidies" the grade into a positive-only check.
	h := browseHandler()
	rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.browse(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/browse?limit=200&offset=200")
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("offset=200 is the second page and must be accepted: %s", rec.Body)
	}
}
