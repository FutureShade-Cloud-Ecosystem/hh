package inventory

// dib443_lookup_test.go, DIB-443: the stock lookup's HTTP contract for a product with no history,
// and for an id that is not a product id at all.
//
// THE DEFECT THIS ENCODES
// -----------------------
// A tester on staging looked up a product and got "internal error", with "No stock balance rows for
// this product." underneath it. Two separate faults met on that screen; this file owns the server
// half of both.
//
//  1. product_id is a `uuid` column. Anything that is not a UUID, a SKU pasted out of the
//     catalogue, an id copied with a trailing space, reached pgx as an uncastable literal and came
//     back as an untyped driver error, which writeErr can only call "internal error": HTTP 500 and a
//     line in the error log, for a typo. It is now a 400 that names what the field wants.
//
//  2. The 500 mattered beyond its own rudeness, because the surface reads the status: if a typo and
//     a real outage and an empty product all arrive as 500-or-empty, no client can tell them apart.
//     So the other half of this file pins the NORMAL answer: a product that has never moved stock is
//     200 with an empty list, and stays 200.
//
// MUTATION TARGETS. Each of these fails on a specific plausible edit rather than on "something
// changed":
//
//	drop the uuid.Parse grade in productIDParam  -> TestStockReads_NonUUIDProductIDIs400 (500 again)
//	drop the TrimSpace                           -> TestStockReads_TrimsSurroundingWhitespace
//	map the store error to 200/400 instead of 500 -> TestStockReads_RealFailureIsStill500
//	return the nil slice instead of []           -> TestStockReads_NoHistoryIsEmptyListNotNull
//
// The last one is not pedantry: `{"on_hand":null}` and `{"on_hand":[]}` are different claims to a
// consumer that does not write `?? []`, and "the field was absent" is exactly the ambiguity this
// ticket is about.

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubReads answers the two read seams with canned data and records what the handler passed down,
// which is how the trim/canonicalisation assertions see through to SQL. Everything else comes from
// the package's DB-free fakeStore.
type stubReads struct {
	*fakeStore
	onHand     []OnHand
	moves      []StockMove
	err        error
	gotProduct string
	gotBranch  string
}

func (s *stubReads) OnHandFor(_ context.Context, productID string) ([]OnHand, error) {
	s.gotProduct = productID
	return s.onHand, s.err
}

func (s *stubReads) RecentMoves(_ context.Context, productID, branchID string, _ int) ([]StockMove, error) {
	s.gotProduct, s.gotBranch = productID, branchID
	return s.moves, s.err
}

func newLookupHandler(st *stubReads) *Handler {
	return NewHandler(NewService(NewRepository(nil)).withStore(st),
		slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
}

// get drives one read handler end to end and hands back the recorder.
func get(t *testing.T, h http.HandlerFunc, url string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec
}

const (
	noHistoryProduct = "11111111-2222-3333-4444-555555555555"
	someBranch       = "5d1b3f6e-0000-4000-9000-000000000001"
)

// - the normal answer: no history is not an error -

func TestStockReads_NoHistoryIsEmptyListNotNull(t *testing.T) {
	// The store answers exactly as the real one does for a product nothing has ever moved: no rows,
	// no error.
	st := &stubReads{fakeStore: newFakeStore()}
	h := newLookupHandler(st)

	for _, c := range []struct {
		name, url, field string
		fn               http.HandlerFunc
	}{
		{"on-hand", "/api/v1/inventory/on-hand?product_id=" + noHistoryProduct, "on_hand", func(w http.ResponseWriter, r *http.Request) { h.onHand(w, r, guards.StaffCaller{}) }},
		{"moves", "/api/v1/inventory/moves?product_id=" + noHistoryProduct, "moves", func(w http.ResponseWriter, r *http.Request) { h.moves(w, r, guards.StaffCaller{}) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := get(t, c.fn, c.url)
			if rec.Code != http.StatusOK {
				t.Fatalf("a product with no stock history must answer 200, got %d: %s", rec.Code, rec.Body)
			}
			// The raw body, not a decode into []T, a decode cannot tell null from [].
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatalf("body is not JSON: %v (%s)", err, rec.Body)
			}
			if got := strings.TrimSpace(string(raw[c.field])); got != "[]" {
				t.Fatalf("%s = %s, want [], a null here is a different claim to a client that "+
					"does not defend itself, and 'field absent' is the exact ambiguity DIB-443 is about", c.field, got)
			}
		})
	}
}

// - the typo: 400, not 500 -

func TestStockReads_NonUUIDProductIDIs400(t *testing.T) {
	st := &stubReads{fakeStore: newFakeStore()}
	h := newLookupHandler(st)

	// Every one of these is something a person actually types or pastes into that box.
	for _, bad := range []string{
		"DIB443-NOSTOCK",                        // the SKU, copied off the catalogue
		"abc",                                   // a fragment
		"11111111-2222-3333-4444-5555555555",    // a UUID short two characters
		"11111111-2222-3333-4444-555555555555x", // a UUID with a stray character
		"'; DROP TABLE stock_moves; --",         // and the one that must never reach a cast
	} {
		t.Run(bad, func(t *testing.T) {
			for _, c := range []struct {
				name string
				url  string
				fn   http.HandlerFunc
			}{
				{"on-hand", "/api/v1/inventory/on-hand?product_id=" + urlQ(bad), func(w http.ResponseWriter, r *http.Request) { h.onHand(w, r, guards.StaffCaller{}) }},
				{"moves", "/api/v1/inventory/moves?product_id=" + urlQ(bad), func(w http.ResponseWriter, r *http.Request) { h.moves(w, r, guards.StaffCaller{}) }},
			} {
				rec := get(t, c.fn, c.url)
				if rec.Code == http.StatusInternalServerError {
					t.Fatalf("%s: a non-UUID product_id still answers 500, that is DIB-443: the "+
						"user's typo is reported as the server breaking, and the surface cannot "+
						"tell it from a real outage", c.name)
				}
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("%s: want 400 for %q, got %d: %s", c.name, bad, rec.Code, rec.Body)
				}
				if !strings.Contains(rec.Body.String(), "product_id") {
					t.Fatalf("%s: the 400 must say which field is wrong, got %s", c.name, rec.Body)
				}
			}
			// And nothing reached the store: an unparseable id is refused before SQL, which is
			// what makes the injection case above uninteresting rather than merely escaped.
			if st.gotProduct != "" {
				t.Fatalf("a rejected product_id was still passed to the store as %q", st.gotProduct)
			}
		})
	}
}

func TestStockReads_MissingProductIDIs400(t *testing.T) {
	h := newLookupHandler(&stubReads{fakeStore: newFakeStore()})
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"on-hand": get(t, func(w http.ResponseWriter, r *http.Request) { h.onHand(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/on-hand"),
		"moves":   get(t, func(w http.ResponseWriter, r *http.Request) { h.moves(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/moves"),
		// A whitespace-only value is a missing one wearing a disguise.
		"on-hand blank": get(t, func(w http.ResponseWriter, r *http.Request) { h.onHand(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/on-hand?product_id=%20%20"),
	} {
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400 for a missing product_id, got %d: %s", name, rec.Code, rec.Body)
		}
	}
}

func TestStockReads_NonUUIDBranchIDIs400(t *testing.T) {
	st := &stubReads{fakeStore: newFakeStore()}
	h := newLookupHandler(st)
	rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.moves(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/moves?product_id="+noHistoryProduct+"&branch_id=Kelowna")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("branch_id casts to uuid in the same query; a branch NAME must be 400, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "branch_id") {
		t.Fatalf("the 400 must name branch_id, got %s", rec.Body)
	}
}

// - whitespace: the paste that used to 500 -

func TestStockReads_TrimsSurroundingWhitespace(t *testing.T) {
	// Copying a UUID out of a table or a chat message brings whitespace with it more often than not.
	// Before the fix that paste was indistinguishable from a broken server.
	//
	// THE PADDING MATTERS, and the first version of this test got it wrong. uuid.Parse switches on
	// LENGTH: at 36+2 it assumes the braced spelling and strips the first and last characters
	// WITHOUT checking they are braces (uuid.go:82, `case 36 + 2: s = s[1:]`). A symmetrically
	// padded " <uuid>\n" is 38 characters, so it parses by accident even with no trimming at all,
	// and a test built on that case survives the mutation that deletes TrimSpace. These are the
	// one-sided pastes, 37 characters, where the trim is the only thing doing the work.
	st := &stubReads{fakeStore: newFakeStore()}
	h := newLookupHandler(st)

	for _, padded := range []string{
		"%20" + noHistoryProduct,         // a leading space (37)
		noHistoryProduct + "%20",         // a trailing space (37)
		noHistoryProduct + "%0A",         // a trailing newline, from a copied table cell (37)
		"%20" + noHistoryProduct + "%0A", // and both, which the length switch would have rescued (38)
	} {
		st.gotProduct = ""
		rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.onHand(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/on-hand?product_id="+padded)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: a pasted id with surrounding whitespace must be accepted, got %d: %s", padded, rec.Code, rec.Body)
		}
		if st.gotProduct != noHistoryProduct {
			t.Fatalf("%q: the store received %q; the handler must pass the trimmed, canonical id",
				padded, st.gotProduct)
		}
	}

	st.gotProduct, st.gotBranch = "", ""
	rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.moves(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/moves?product_id="+noHistoryProduct+"&branch_id="+someBranch+"%20")
	if rec.Code != http.StatusOK {
		t.Fatalf("a pasted branch_id with whitespace must be accepted, got %d: %s", rec.Code, rec.Body)
	}
	if st.gotBranch != someBranch {
		t.Fatalf("the store received branch %q, want the trimmed %q", st.gotBranch, someBranch)
	}
}

func TestStockReads_UUIDCanonicalisedBeforeSQL(t *testing.T) {
	// uuid.Parse accepts spellings Postgres does not (urn:) and ones it does (braces). One shape
	// reaches SQL either way, so the query plan and the logs never depend on how it was typed.
	st := &stubReads{fakeStore: newFakeStore()}
	h := newLookupHandler(st)
	for _, spelling := range []string{
		"urn:uuid:" + noHistoryProduct,
		"{" + noHistoryProduct + "}",
		strings.ToUpper(noHistoryProduct),
	} {
		st.gotProduct = ""
		rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.onHand(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/on-hand?product_id="+urlQ(spelling))
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: want 200, got %d: %s", spelling, rec.Code, rec.Body)
		}
		if st.gotProduct != noHistoryProduct {
			t.Fatalf("%q reached the store as %q, want the canonical %q", spelling, st.gotProduct, noHistoryProduct)
		}
	}
}

// - the error path that must SURVIVE the fix -

func TestStockReads_RealFailureIsStill500(t *testing.T) {
	// The point of DIB-443 is not "stop returning 500". It is "stop returning 500 for things that
	// are not failures". A store that genuinely fails must still say so, loudly, with a status the
	// surface can tell apart from an empty product, otherwise the fix has simply moved the lie.
	st := &stubReads{fakeStore: newFakeStore(), err: errors.New("connection reset by peer")}
	h := newLookupHandler(st)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"on-hand": get(t, func(w http.ResponseWriter, r *http.Request) { h.onHand(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/on-hand?product_id="+noHistoryProduct),
		"moves":   get(t, func(w http.ResponseWriter, r *http.Request) { h.moves(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/moves?product_id="+noHistoryProduct),
	} {
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: a real store failure must stay 500, got %d: %s, an outage rendered as "+
				"'no stock' is the same bug pointing the other way", name, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "connection reset") {
			t.Fatalf("%s: the driver error leaked to the client: %s", name, rec.Body)
		}
	}
}

// urlQ percent-encodes a query value (the tests build URLs by hand for readability).
func urlQ(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '~':
			b.WriteRune(r)
		default:
			for _, c := range []byte(string(r)) {
				b.WriteString("%")
				const hex = "0123456789ABCDEF"
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0x0f])
			}
		}
	}
	return b.String()
}
