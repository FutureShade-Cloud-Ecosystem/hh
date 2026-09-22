package customerquote

import (
	"strings"
	"testing"
)

// The storage encoding's offline classification matrix (the batch plan's B1
// exit criterion, carried into this batch's unit set) and the M2d membership
// boundary companion: membership is by element, never by substring, and an
// ill-formed stored value loses before any substring luck can win.

func TestEncodeParseRoundTrip(t *testing.T) {
	cases := []TokenState{
		{},
		{Live: strings.Repeat("a", 64)},
		{Dead: []string{strings.Repeat("b", 64)}},
		{Live: strings.Repeat("c", 64), Dead: []string{strings.Repeat("d", 64), strings.Repeat("e", 64)}},
	}
	for _, ts := range cases {
		got, err := ParseTokenState(ts.Encode())
		if err != nil {
			t.Fatalf("parse(%q): %v", ts.Encode(), err)
		}
		if got.Live != ts.Live {
			t.Fatalf("live %q, want %q", got.Live, ts.Live)
		}
		if len(got.Dead) != len(ts.Dead) {
			t.Fatalf("dead len %d, want %d", len(got.Dead), len(ts.Dead))
		}
		for i := range got.Dead {
			if got.Dead[i] != ts.Dead[i] {
				t.Fatalf("dead[%d] %s, want %s", i, got.Dead[i], ts.Dead[i])
			}
		}
	}
	if got := (TokenState{}).Encode(); got != "v1:-:-" {
		t.Fatalf("empty state encodes %q, want v1:-:-", got)
	}
}

func TestParseRejectsIllFormedStoredValues(t *testing.T) {
	h := strings.Repeat("a", 64)
	bad := []string{
		"",                                       // empty
		h,                                        // bare legacy hash
		"v2:" + h + ":-",                         // wrong marker
		"v1:" + h,                                // missing dead separator
		"v1:" + h + ":",                          // empty dead part (not the sentinel)
		"v1:AB" + strings.Repeat("a", 62) + ":-", // uppercase atom
		"v1:" + strings.Repeat("a", 63) + ":-",   // short live atom
		"v1:-:" + strings.Repeat("a", 63),        // short dead atom
		"v1:-:" + h + "," + h,                    // duplicated dead atom
		"v1:-:" + h + ",,",                       // empty list element
		"v1:-:-" + ",",                           // trailing garbage
	}
	for _, stored := range bad {
		if _, err := ParseTokenState(stored); err == nil {
			t.Fatalf("parse(%q) accepted an ill-formed value", stored)
		}
	}
}

// TestM2dSubstringBoundaryIsExactMembership is the M2d companion: a stored
// value whose hex content contains the presented hash as a proper substring
// (a corrupted 128-hex value, or a dead list atom differing in one
// character) must not match.
func TestM2dSubstringBoundaryIsExactMembership(t *testing.T) {
	h1 := strings.Repeat("a", 63) + "b"
	h2 := strings.Repeat("a", 63) + "c" // differs in the last character
	h3 := strings.Repeat("f", 64)

	// Exact element membership.
	ts := TokenState{Live: h3, Dead: []string{h1, h2}}
	if live, dead := ts.Matches(h1); live || !dead {
		t.Fatalf("h1 matched live=%v dead=%v, want dead only", live, dead)
	}
	if live, dead := ts.Matches(h2); live || !dead {
		t.Fatalf("h2 matched live=%v dead=%v, want dead only", live, dead)
	}
	if live, dead := ts.Matches(h3); !live || dead {
		t.Fatalf("h3 matched live=%v dead=%v, want live only", live, dead)
	}
	// One character off: no match at all.
	off := strings.Repeat("a", 62) + "bc"
	if live, dead := ts.Matches(off); live || dead {
		t.Fatalf("a 65th-character variant matched; membership must be exact")
	}
	// The same-quote collision serves: live is checked before dead.
	both := TokenState{Live: h1, Dead: []string{h1}}
	if live, dead := both.Matches(h1); !live {
		t.Fatalf("same-quote collision: live must be checked first, got live=%v dead=%v", live, dead)
	}
}

// TestCorruptedLongHexValueDoesNotWinBySubstring is the stored-value side of
// the boundary: a 128-hex corrupted value containing the presented hash fails
// the grammar, so it matches nothing even though the hash is a substring.
func TestCorruptedLongHexValueDoesNotWinBySubstring(t *testing.T) {
	h := strings.Repeat("1", 64)
	corrupted := h + h
	if _, err := ParseTokenState(corrupted); err == nil {
		t.Fatalf("128-hex corrupted value parsed; it must fail the grammar")
	}
	// And the honest value for the same token does parse.
	if _, err := ParseTokenState((TokenState{Live: h}).Encode()); err != nil {
		t.Fatalf("honest value failed to parse: %v", err)
	}
}

func TestRotationAppendsEveryGeneration(t *testing.T) {
	mint := func(i int) string { return strings.Repeat(string(rune('a'+i)), 64) }
	ts := TokenState{}
	for i := 0; i < 5; i++ {
		ts = ts.InstallLive(mint(i))
		if ts.Live != mint(i) {
			t.Fatalf("generation %d: live %s, want %s", i, ts.Live, mint(i))
		}
		ts = ts.RetireLive()
		if ts.Live != "" {
			t.Fatalf("generation %d: live slot not emptied on retire", i)
		}
		if len(ts.Dead) != i+1 {
			t.Fatalf("generation %d: dead list holds %d atoms, want %d (the single-tombstone mutation M2c kills exactly here)", i, len(ts.Dead), i+1)
		}
	}
	// Dedupe: retiring a hash already dead does not duplicate it.
	ts = ts.InstallLive(mint(2))
	ts = ts.RetireLive()
	count := 0
	for _, d := range ts.Dead {
		if d == mint(2) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("dead list holds %d copies of a twice-retired hash, want 1", count)
	}
}

func TestTokenWellFormed(t *testing.T) {
	raw, _, err := MintToken()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !TokenWellFormed(raw) {
		t.Fatalf("a minted token failed the mint's own shape check: %q", raw)
	}
	for _, bad := range []string{"", "   ", "short", strings.Repeat("x", 42), strings.Repeat("x", 44), "has space in it", "hás+bad/chars"} {
		if TokenWellFormed(bad) {
			t.Fatalf("malformed token %q passed the shape check", bad)
		}
	}
	// Whitespace-trimmed presentation still classifies by shape.
	if !TokenWellFormed("  " + raw + " ") {
		t.Fatalf("trimmed minted token failed the shape check")
	}
}
