package gl

// tax_words_test.go, the memo on a tax leg names the dealer's tax in the dealer's word (item 3b): "HST"
// for an Ontario dealer exactly as before (and unwired), "Sales tax" for a California one, the neutral
// "Tax" when the jurisdiction could not be read. The amounts and accounts are untouched.

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/taxwords"
)

type glWords struct {
	w   string
	err error
}

func (g glWords) TaxLabel(context.Context) (string, error) { return g.w, g.err }

func TestTaxLegMemoFollowsTheDealersWord(t *testing.T) {
	for _, c := range []struct {
		name string
		src  taxwords.Source
		want string
	}{
		{"unwired", nil, "HST on NSF fee"},
		{"ontario", glWords{w: "HST"}, "HST on NSF fee"},
		{"california", glWords{w: "Sales tax"}, "Sales tax on NSF fee"},
		{"failed read", glWords{err: errors.New("down")}, "Tax on NSF fee"},
	} {
		f := newFakeStore()
		svc := newTestService(f)
		if c.src != nil {
			svc.WithTaxLabel(c.src)
		}
		if err := svc.consume(context.Background(), ev("payment.bounced", bounceTaxableFee())); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		l, ok := leg(f.lastInserted(), "2200")
		if !ok || l.Memo != c.want {
			t.Errorf("%s: memo %q, want %q", c.name, l.Memo, c.want)
		}
		wantLeg(t, f.lastInserted(), "2200", 0, 518) // the amount does not move with the word
	}
}
