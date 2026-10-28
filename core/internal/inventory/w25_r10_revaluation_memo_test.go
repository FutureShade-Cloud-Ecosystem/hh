package inventory

// w25_r10_revaluation_memo_test.go, W2-5 round 10 (finance wave 2), S5's P3-6: the issue's
// fallback cost path books a REVALUATION, and the journal says so.
//
// An issue that took the provisional fallback cost revalues every unit still on hand from the old
// average to the fallback. The difference between the row's exact change and the movement's
// booked value is booked as a journal in the move's own transaction, and before this round that
// journal's memo said "rounding": a Controller reading the dollars under a rounding memo could
// not tell a revaluation of the stock from the rounding of a cent, and the two want different
// answers (a revaluation is a price the books learned late; a rounding is an arithmetic cent).

import (
	"context"
	"strings"
	"testing"
)

// TestW25R10_TheIssueFallbackCostBooksARevaluationMemo is the leg: an issue with no cost row
// takes the provisional fallback cost, the whole remaining stock is revalued to it, and the
// journal that books the difference says revaluation.
func TestW25R10_TheIssueFallbackCostBooksARevaluationMemo(t *testing.T) {
	f := newFakeStore()
	f.onhand[key("p1", "br1")] = 10 // ten on hand at no cost at all
	f.productCost["p1"] = 1200      // the provisional fallback cost, $12.00
	gl := &fakeGLPoster{}
	rules := fakeRules{m: map[string]string{
		"order.fulfilled|inventory":    "1200",
		"inventory.adjusted|inventory": "1200",
		"inventory.adjusted|expense":   "5200",
	}}
	s := NewService(nil).withStore(f).WithGLPoster(gl).WithRuleResolver(rules)

	if err := s.FulfilOrderLinesTx(context.Background(), nil, "order-1", "br1", []string{"p1"}, []float64{1}); err != nil {
		t.Fatalf("the fulfilment: %v", err)
	}

	var memo string
	var cents int64
	for _, j := range gl.journals {
		if strings.Contains(j.Memo, "stock issue") {
			memo = j.Memo
			for _, l := range j.Lines {
				if l.AccountCode == "5200" {
					cents += l.DebitCents - l.CreditCents
				}
			}
		}
	}
	if memo == "" {
		t.Fatalf("the issue booked no journal at all: %+v", gl.journals)
	}
	if cents == 0 {
		t.Fatalf("the issue's journal books no revaluation: %+v", gl.journals)
	}
	if !strings.Contains(memo, "revaluation") {
		t.Fatalf("the issue's journal memo %q does not say revaluation: the fallback cost path revalues the stock and the memo must say so", memo)
	}
	t.Logf("the issue's fallback cost booked a revaluation of %d cents under the memo %q", cents, memo)
}
