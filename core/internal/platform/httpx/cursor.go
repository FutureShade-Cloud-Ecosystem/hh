// cursor.go holds the cursor pagination family every list endpoint of the
// contract uses (api/CONVENTIONS.md, Pagination): a list request carries
// `cursor` and `limit`, and answers a page of `items`, `next_cursor` and
// `limit`; never `offset`, never `total`. It replaces the ERP's offset
// pagination family (Pagination, ParsePagination, the offset page generic
// and its WritePage helper), which is not carried: the contract's list
// envelopes carry no offset echo and no total echo, and the two shapes must
// never coexist behind one door.
package httpx

import (
	"encoding/base64"
	"net/http"
	"strconv"
)

const (
	// DefaultPageLimit is the page size applied when ?limit is absent or
	// invalid (the contract's Limit parameter default).
	DefaultPageLimit = 50
	// MaxPageLimit caps ?limit so one request can't pull an unbounded result
	// set (the contract's Limit parameter maximum).
	MaxPageLimit = 200
)

// cursorVersion prefixes every minted cursor so a decode can tell a token
// this package minted from anything a client hand-crafted. It is not
// secrecy: the cursor stays opaque to the client by contract, not by
// cryptography, and the prefix only keeps malformed or forged values from
// being read as a resume key they never were.
const cursorVersion = "v1:"

// CursorRequest is the parsed list window: the decoded resume key (empty on
// the first page) and the clamped limit.
type CursorRequest struct {
	// Cursor is the stable-ordering key of the last row of the previous
	// page, decoded; the first page asks with no cursor and gets "".
	Cursor string
	// Limit is the page size clamped to [1, MaxPageLimit].
	Limit int
}

// ParseCursorPage reads `cursor` and `limit` from the query string per the
// contract's pagination convention. Limit defaults to DefaultPageLimit and
// clamps to [1, MaxPageLimit] (an invalid value reads as the default, the
// same fail-quiet clamp the offset family had). A cursor that is present but
// malformed (not the opaque shape EncodeCursor mints) is a BadRequestError,
// never silently treated as the first page: a client resuming from a
// corrupted token must learn that, because silently restarting from the top
// of a list repeats rows the client already acted on.
func ParseCursorPage(r *http.Request) (CursorRequest, error) {
	q := r.URL.Query()
	limit := DefaultPageLimit
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	cursor := ""
	if v := q.Get("cursor"); v != "" {
		key, err := decodeCursor(v)
		if err != nil {
			return CursorRequest{}, BadRequestError{Msg: "cursor is malformed"}
		}
		cursor = key
	}
	return CursorRequest{Cursor: cursor, Limit: limit}, nil
}

// EncodeCursor mints the opaque `next_cursor` value from a page's last
// stable-ordering key. Round-trips with ParseCursorPage.
//
// THE STABLE-ORDERING CONTRACT, stated once for every converting list: the
// key must be the list's stable ordering key under the query's filters (the
// contract's per-scope monotonic seq where one exists, otherwise a
// deterministic composite such as the sort column plus a unique
// tie-breaker). A cursor minted on an unstable order (a non-deterministic
// sort, a key that repeats across rows) silently drops or repeats rows, and
// for an AR register or a statement that is a wrong number a person acts
// on. Every endpoint that converts records its ordering basis beside its
// conversion.
func EncodeCursor(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursorVersion + key))
}

// decodeCursor inverts EncodeCursor and refuses anything it did not mint.
func decodeCursor(raw string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	s := string(b)
	if len(s) < len(cursorVersion) || s[:len(cursorVersion)] != cursorVersion {
		return "", BadRequestError{Msg: "cursor is malformed"}
	}
	return s[len(cursorVersion):], nil
}

// cursorEnvelope is the wire shape of every cursor-paginated list response
// (the contract's `<Prefix><Thing>Page` schemas all carry exactly these
// three fields). next_cursor is a nullable string: null when the page is the
// last one the caller can fetch, the minted cursor of the next page
// otherwise.
type cursorEnvelope[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
	Limit      int     `json:"limit"`
}

// WriteCursorPage writes a cursor page envelope with 200. nextKey is the
// stable-ordering key of the last row of THIS page when a further page
// exists, and "" when this page is the last (next_cursor then serializes as
// null). The caller decides which case holds: fetch limit+1 rows, and when
// limit+1 came back a next page exists, so drop the extra row and pass its
// key. A nil slice serializes as [] (never null), so a client never has to
// special-case an empty page.
func WriteCursorPage[T any](w http.ResponseWriter, items []T, nextKey string, limit int) {
	if items == nil {
		items = []T{}
	}
	var next *string
	if nextKey != "" {
		s := EncodeCursor(nextKey)
		next = &s
	}
	WriteJSON(w, http.StatusOK, cursorEnvelope[T]{Items: items, NextCursor: next, Limit: limit})
}
