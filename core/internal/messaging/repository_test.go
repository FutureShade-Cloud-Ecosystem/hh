package messaging

import (
	"errors"
	"testing"
	"time"
)

// The page cursor must carry BOTH created_at and id so pagination never splits a same-timestamp
// group across a boundary (the MAJOR keyset bug). These DB-free tests pin the encode/decode
// contract that the SQL tuple predicate `(created_at, id) < (cursor_ts, cursor_id)` relies on.

func TestCursor_RoundTrip(t *testing.T) {
	ts := time.Date(2026, 7, 13, 10, 30, 0, 123456789, time.UTC)
	m := Message{ID: "8f14e45f-ceea-467d-9a1c-000000000001", CreatedAt: ts}
	cur := encodeCursor(m)

	gotTS, gotID, err := decodeCursor(cur)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gotTS.Equal(ts) {
		t.Errorf("timestamp not preserved: want %v got %v", ts, gotTS)
	}
	if gotID != m.ID {
		t.Errorf("id not preserved: want %q got %q", m.ID, gotID)
	}
}

// Two messages sharing a created_at must yield DISTINCT cursors, that is the whole point of adding
// the id tiebreaker. Pre-fix both encoded to the same timestamp string and the second row was lost.
func TestCursor_DistinctOnTimestampCollision(t *testing.T) {
	ts := time.Date(2026, 7, 13, 10, 30, 0, 0, time.UTC)
	a := encodeCursor(Message{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", CreatedAt: ts})
	b := encodeCursor(Message{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", CreatedAt: ts})
	if a == b {
		t.Fatalf("same-timestamp rows must produce distinct cursors, both = %q", a)
	}
}

// A legacy timestamp-only cursor (minted before this fix) must still decode, defaulting the id to
// the tuple upper bound so the boundary keeps the old created_at-only semantics.
func TestCursor_LegacyTimestampOnly(t *testing.T) {
	ts := time.Date(2026, 7, 13, 10, 30, 0, 0, time.UTC)
	gotTS, gotID, err := decodeCursor(ts.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("legacy decode: %v", err)
	}
	if !gotTS.Equal(ts) {
		t.Errorf("legacy timestamp not preserved: %v", gotTS)
	}
	if gotID != "ffffffff-ffff-ffff-ffff-ffffffffffff" {
		t.Errorf("legacy cursor should default id to max UUID, got %q", gotID)
	}
}

func TestCursor_BadInputErrors(t *testing.T) {
	if _, _, err := decodeCursor("not-a-timestamp|x"); err == nil {
		t.Fatal("a malformed cursor must error, not silently pass")
	}
}

// A malformed cursor must fail as ErrBadCursor (client fault → 400), NOT a bare parse error that the
// handler would treat as a 500. Pre-fix decodeCursor returned the raw time.Parse error, so
// errors.Is(err, ErrBadCursor) was false and ListMessages surfaced a 500. This pins the sentinel for
// every garbage/invalid shape.
func TestCursor_BadInputIsErrBadCursor(t *testing.T) {
	for _, bad := range []string{
		"garbage",                       // not a timestamp at all
		"not-a-timestamp|x",             // has separator, still unparseable ts
		"|" + "8f14e45f-ceea-467d-9a1c", // empty timestamp half
		"2026-13-99T99:99:99Z|x",        // structurally timestamp-ish but out of range
		"' OR 1=1 --",                   // injection-shaped junk
	} {
		_, _, err := decodeCursor(bad)
		if err == nil {
			t.Fatalf("cursor %q must error", bad)
		}
		if !errors.Is(err, ErrBadCursor) {
			t.Fatalf("cursor %q: want errors.Is ErrBadCursor, got %v", bad, err)
		}
	}
}

// A valid cursor must still decode cleanly (no ErrBadCursor false-positive) so pagination keeps
// working, the fix must not break the happy path.
func TestCursor_ValidNotFlaggedBad(t *testing.T) {
	ts := time.Date(2026, 7, 13, 10, 30, 0, 123456789, time.UTC)
	cur := encodeCursor(Message{ID: "8f14e45f-ceea-467d-9a1c-000000000001", CreatedAt: ts})
	if _, _, err := decodeCursor(cur); err != nil {
		t.Fatalf("valid cursor must decode, got %v", err)
	}
}

// REGRESSION (MAJOR): a cursor whose timestamp half parses but whose id half is NOT a UUID must be
// rejected as ErrBadCursor at decode time. Pre-fix decodeCursor validated only the timestamp and
// passed the raw id straight into the `$4::uuid` bind, so Postgres failed the cast and ListMessages
// wrapped it as a plain "list messages" error → HTTP 500 instead of a 400 client fault. Without the
// uuid.Parse(idStr) guard this returns nil and the test fails.
func TestCursor_NonUUIDIdIsErrBadCursor(t *testing.T) {
	validTS := time.Date(2026, 7, 13, 10, 30, 0, 123456789, time.UTC).Format(time.RFC3339Nano)
	for _, bad := range []string{
		validTS + "|not-a-uuid",                           // clearly not a UUID
		validTS + "|8f14e45f-ceea-467d-9a1c",              // truncated UUID (would still fail the ::uuid cast)
		validTS + "|' OR 1=1 --",                          // injection-shaped id half
		validTS + "|ffffffff-ffff-ffff-ffff-fffffffffffg", // right shape, invalid hex char
	} {
		_, _, err := decodeCursor(bad)
		if err == nil {
			t.Fatalf("cursor %q: non-UUID id half must error (was passed to $4::uuid → 500)", bad)
		}
		if !errors.Is(err, ErrBadCursor) {
			t.Fatalf("cursor %q: want errors.Is ErrBadCursor (→ 400), got %v", bad, err)
		}
	}
}
