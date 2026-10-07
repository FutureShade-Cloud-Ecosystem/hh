package main

import "testing"

// The pick ticket carries what the order line was ordered for, so a yard that finds the rest of a
// short line sends the true number and not the reduced one the line now shows.
func TestOrderedWithBackorder(t *testing.T) {
	for _, c := range []struct{ qty, back, wantOrdered, wantBack string }{
		{"3.0000", "2.0000", "5.0000", "2.0000"},
		{"0.3333", "6.6667", "7.0000", "6.6667"},
		{"5.0000", "0.0000", "5.0000", ""},
		{"5", "", "5", ""},
	} {
		o, b := orderedWithBackorder(c.qty, c.back)
		if o != c.wantOrdered || b != c.wantBack {
			t.Errorf("(%q + %q) = %q, %q; want %q, %q", c.qty, c.back, o, b, c.wantOrdered, c.wantBack)
		}
	}
}
