package warranty

import (
	"math"
	"testing"
)

// threshold_test.go, the ONE comparison the walk change 6 defect classification is allowed to make.
// The rule under test is OVER OR EQUAL at the line total: a defect whose cost reaches the threshold
// becomes a warranty claim, only a cost strictly under it is logged as shrinkage. The tie case (cost
// exactly at the threshold) is the case a later refactor is most likely to flip, so it is named
// first. Money is integer cents end to end, so the edges are exact where a float would round.

func TestCostOverClaimThresholdAtTheTieIsAClaim(t *testing.T) {
	if !CostOverClaimThreshold(5000, 5000) {
		t.Fatal("a cost of exactly 5000 at a threshold of 5000 must be a warranty claim: the rule is over OR EQUAL, and the tie is the case this function exists to settle")
	}
}

func TestCostOverClaimThresholdStraddlesTheLine(t *testing.T) {
	cases := []struct {
		name      string
		cost      int64
		threshold int64
		wantClaim bool
	}{
		{"one cent under is shrinkage", 4999, 5000, false},
		{"one cent over is a claim", 5001, 5000, true},
		{"a zero cost under a real threshold is shrinkage", 0, 5000, false},
		{"a zero threshold classifies everything a claim, a zero cost included", 0, 0, true},
		{"the boards' over example, 56.80 at a 50 threshold", 5680, 5000, true},
		{"the boards' under example, 12.30 at a 50 threshold", 1230, 5000, false},
	}
	for _, c := range cases {
		if got := CostOverClaimThreshold(c.cost, c.threshold); got != c.wantClaim {
			t.Fatalf("%s: CostOverClaimThreshold(%d, %d) = %v, want %v", c.name, c.cost, c.threshold, got, c.wantClaim)
		}
	}
}

func TestCostOverClaimThresholdStaysExactAtBigintEdges(t *testing.T) {
	// Money is BIGINT cents; the comparison must not drift where a float would.
	big := int64(math.MaxInt64)
	if !CostOverClaimThreshold(big, big) {
		t.Fatal("a max-int64 cost at a max-int64 threshold must still answer over-or-equal exactly")
	}
	if CostOverClaimThreshold(big-1, big) {
		t.Fatal("one cent under a max-int64 threshold must still be shrinkage, no overflow to a claim")
	}
}
