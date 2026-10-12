package likeutil

import "testing"

// TestEscapeLikePattern_Literal pins the LIKE-metacharacter escaping every search's ILIKE arm
// relies on: %, _ and \ from a user term must be escaped so that under `ESCAPE '\'` they match
// literally instead of acting as wildcards.
func TestEscapeLikePattern_Literal(t *testing.T) {
	cases := map[string]string{
		`abc`: `abc`,
		`a%b`: `a\%b`,
		`a_b`: `a\_b`,
		`a\b`: `a\\b`,
		`%_`:  `\%\_`,
		// backslash must be escaped first so an escaped wildcard is not double-processed.
		`\%`: `\\\%`,
	}
	for in, want := range cases {
		if got := EscapeLikePattern(in); got != want {
			t.Errorf("EscapeLikePattern(%q) = %q, want %q", in, got, want)
		}
	}
}
