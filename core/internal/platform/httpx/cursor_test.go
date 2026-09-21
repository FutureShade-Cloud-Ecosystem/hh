package httpx

// cursor_test.go pins the cursor pagination family against the contract's
// convention (api/CONVENTIONS.md, Pagination): items, next_cursor, limit;
// never offset, never total. Both halves of every conditional rule: a valid
// cursor round-trips, a malformed one is refused; limit clamps at both
// ends; next_cursor is null exactly when no further page exists.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// parseOf runs ParseCursorPage over a request with the given query string.
func parseOf(t *testing.T, rawQuery string) (CursorRequest, error) {
	t.Helper()
	r := httptest.NewRequest("GET", "/list?"+rawQuery, nil)
	req, err := ParseCursorPage(r)
	if err != nil {
		return CursorRequest{}, err
	}
	return req, nil
}

// TestCursorRoundTrip: EncodeCursor then ParseCursorPage returns the key.
func TestCursorRoundTrip(t *testing.T) {
	for _, key := range []string{"42", "0007", "f81d4fae-7dec-11d0-a765-00a0c91e6bf6", "128/f81d4fae-7dec-11d0-a765-00a0c91e6bf6", "seq:99:uuid"} {
		c := EncodeCursor(key)
		if c == "" || strings.ContainsAny(c, "+/=") {
			t.Fatalf("EncodeCursor(%q) = %q: must be non-empty URL-safe base64", key, c)
		}
		req, err := parseOf(t, "cursor="+c)
		if err != nil {
			t.Fatalf("round trip of %q: %v", key, err)
		}
		if req.Cursor != key {
			t.Fatalf("round trip: got cursor %q, want %q", req.Cursor, key)
		}
	}
}

// TestCursorFirstPage: no cursor param (and an empty one) is the first page,
// not an error.
func TestCursorFirstPage(t *testing.T) {
	for _, q := range []string{"", "limit=10", "cursor="} {
		req, err := parseOf(t, q)
		if err != nil {
			t.Fatalf("query %q: first page must parse clean: %v", q, err)
		}
		if req.Cursor != "" {
			t.Fatalf("query %q: cursor %q on a first page, want empty", q, req.Cursor)
		}
	}
}

// TestCursorMalformedRefused: a present-but-malformed cursor is a 400-class
// BadRequestError, never a silent restart from the top of the list.
func TestCursorMalformedRefused(t *testing.T) {
	for _, bad := range []string{
		"0", "1", "42", // a bare hand-crafted key
		"!!!!", "a b c", "_", // not base64url
		"c3RvcA",              // valid base64url of "stop", but not a minted token
		EncodeCursor("k")[:5], // a truncated minted token
	} {
		if _, err := parseOf(t, "cursor="+strings.ReplaceAll(bad, " ", "%20")); err == nil {
			t.Fatalf("cursor %q: malformed cursor must be refused", bad)
		} else if _, ok := err.(BadRequestError); !ok {
			t.Fatalf("cursor %q: refusal must be BadRequestError, got %T", bad, err)
		}
	}
}

// TestCursorLimitClamp, both halves: absent/invalid/negative reads as the
// default; a value above the cap reads as the cap; valid values in range
// pass through.
func TestCursorLimitClamp(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", DefaultPageLimit},
		{"limit=", DefaultPageLimit},
		{"limit=abc", DefaultPageLimit},
		{"limit=0", DefaultPageLimit},
		{"limit=-5", DefaultPageLimit},
		{"limit=1", 1},
		{"limit=50", 50},
		{"limit=200", MaxPageLimit},
		{"limit=201", MaxPageLimit},
		{"limit=100000", MaxPageLimit},
	}
	for _, c := range cases {
		req, err := parseOf(t, c.raw)
		if err != nil {
			t.Fatalf("query %q: %v", c.raw, err)
		}
		if req.Limit != c.want {
			t.Errorf("query %q: limit = %d, want %d", c.raw, req.Limit, c.want)
		}
	}
}

// TestWriteCursorPage pins the envelope: three keys only, null next_cursor
// when the page is last, a decodable cursor when it is not, [] for a nil
// slice, and the limit echo.
func TestWriteCursorPage(t *testing.T) {
	type row struct {
		ID string `json:"id"`
	}
	// Last page: nextKey empty.
	rec := httptest.NewRecorder()
	WriteCursorPage(rec, []row{{ID: "a"}}, "", 50)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("page is not JSON: %v", err)
	}
	if _, ok := body["total"]; ok {
		t.Fatal("the cursor page must never echo a total")
	}
	if _, ok := body["offset"]; ok {
		t.Fatal("the cursor page must never echo an offset")
	}
	if body["next_cursor"] != nil {
		t.Fatalf("last page: next_cursor = %v, want null", body["next_cursor"])
	}
	if body["limit"] != float64(50) {
		t.Fatalf("limit echo = %v, want 50", body["limit"])
	}
	// A page with more behind it: next_cursor decodes back to the key.
	rec = httptest.NewRecorder()
	WriteCursorPage(rec, []row{{ID: "a"}, {ID: "b"}}, "128", 50)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("page is not JSON: %v", err)
	}
	next, ok := body["next_cursor"].(string)
	if !ok || next == "" {
		t.Fatalf("next_cursor = %v, want a minted cursor", body["next_cursor"])
	}
	req, err := parseOf(t, "cursor="+next)
	if err != nil || req.Cursor != "128" {
		t.Fatalf("next_cursor must round-trip to the last row's key: %q %v", req.Cursor, err)
	}
	// An empty page writes [], never null.
	rec = httptest.NewRecorder()
	WriteCursorPage[row](rec, nil, "", 1)
	if strings.TrimSpace(rec.Body.String()) != `{"items":[],"next_cursor":null,"limit":1}` {
		t.Fatalf("empty page body = %s", rec.Body.String())
	}
}
