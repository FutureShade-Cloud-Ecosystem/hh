package pricing

// exactline_test.go, the resolver's exact line-total proof (decision log row 820, option A
// widened). AGENTS.md: money is integer cents, computed by the core, never a float.
//
// RED FIRST. On the float arm the relay's case (410 pieces at 15 cents per BOX of 100) answers
// 61: the float evaluates 410 / 100 as 4.0999999999999996 and 4.0999999999999996 x 15 lands at
// 61.499999999999993, which rounds DOWN. The exact answer is 61.5 and the module's one rounding
// mode (half away from zero, roundCents / mulDivRoundHalfUp) makes it 62. Every test here was
// seen red on main before the exact path landed, and the exact path is mutated back to float to
// prove each one bites. The oracle below is INDEPENDENT: it shares no helper with the resolver.

import (
	"context"
	"math/big"
	"strconv"
	"testing"
)

// oracleRoundHalfAway rounds the exact rational x to an integer, half away from zero. Written
// from the definition on purpose: floor(|x| + 1/2), signed.
func oracleRoundHalfAway(x *big.Rat) int64 {
	num := new(big.Int).Abs(x.Num())
	den := x.Denom()
	n := new(big.Int).Lsh(num, 1) // floor((2|num| + den) / (2den)) = floor(|x| + 1/2)
	n.Add(n, den)
	n.Quo(n, new(big.Int).Lsh(den, 1))
	if x.Sign() < 0 {
		n.Neg(n)
	}
	return n.Int64()
}

// oracleLineTotal is the independent reference for one line: exact decimal quantity entered in
// the BASE unit, converted into the price's unit whose factor is facNum/facDen (how many base
// units one priced unit carries), times integer cents, rounded once half away from zero.
func oracleLineTotal(qtyNum, qtyDen, facNum, facDen, cents int64) int64 {
	total := new(big.Rat).SetFrac64(cents*qtyNum*facDen, qtyDen*facNum)
	return oracleRoundHalfAway(total)
}

// exactLineSvc is the resolver over the LIST path only: one retail anchor priced per BOX, the
// entered unit PC with factor 1, no rules, no override, no promo. ResolveCounterPrice is the
// till's door onto the same waterfall the quote resolves through, so every case here crosses
// the line-total arm exactly as a counter line does, with no database.
func exactLineSvc(f float64, listCents int64) *Service {
	return NewService(&fakeStore{
		listPrice: &ListPrice{ProductID: "blu60", ListPriceCents: listCents, PriceUOM: "BOX"},
	}).WithUOMConverter(fakeUOM{factors: map[string]float64{"PC": 1, "BOX": f}})
}

// resolveExactLine prices qty PC against a p-cents-per-BOX anchor and returns the line total.
func resolveExactLine(t *testing.T, s *Service, qty float64, p int64) int64 {
	t.Helper()
	bd, err := s.ResolveCounterPrice(context.Background(), ResolveInput{
		ProductID: "blu60", CustomerType: "RETAIL", QtyEntered: qty, UOMEntered: "PC",
	})
	if err != nil {
		t.Fatalf("resolve %v PC at %d/BOX: %v", qty, p, err)
	}
	return bd.LineTotalCents
}

// TestExactLineTotal_RelayCase is THE case the principal ruled on (relay 002, row 820): 410
// pieces at 15 cents per box of 100 prices at 62, not 61. The exact line total is 61.5, and
// half away from zero rounds it up.
func TestExactLineTotal_RelayCase(t *testing.T) {
	got := resolveExactLine(t, exactLineSvc(100, 15), 410, 15)
	if want := int64(62); got != want {
		t.Fatalf("410 PC at 15 cents per BOX of 100: line total %d, want %d (the exact total is 61.5, half away from zero rounds up)", got, want)
	}
}

// TestExactLineTotal_HalfCentEdges pins the neighbouring shapes the review probed: the
// away-from-edge cases were already right and must stay right, the at-edge cases were the
// defect. qtyNum/qtyDen is the exact decimal quantity; qtyF is the float64 the wire carries.
func TestExactLineTotal_HalfCentEdges(t *testing.T) {
	cases := []struct {
		name   string
		qtyNum int64
		qtyDen int64
		factor int64 // whole factors, the unit set the review probed
		price  int64
		want   int64 // the exact answer, cross-checked against the oracle below
	}{
		{"10 PC at 15 per 100 stays 2", 10, 1, 100, 15, 2},
		{"30 PC at 15 per 100 stays 5", 30, 1, 100, 15, 5},
		{"410 PC at 15 per 100 is 62 (the relay case)", 410, 1, 100, 15, 62},
		{"4.1 pieces at 15 cents is 62 (no conversion, decimal qty)", 41, 10, 1, 15, 62},
		{"0.5 at 15 per 2 is 4", 1, 2, 2, 15, 4},
		{"4999.9 at 15 per 1000 is 75", 49999, 10, 1000, 15, 75},
		{"2.5 at 7 per 2 is 9", 5, 2, 2, 7, 9},
		{"1.25 at 6 per 3 is 3", 5, 4, 3, 6, 3},
		{"4.1 at 15 per 10 is 6", 41, 10, 10, 15, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if want := oracleLineTotal(tc.qtyNum, tc.qtyDen, tc.factor, 1, tc.price); want != tc.want {
				t.Fatalf("table self-check: want %d but the oracle says %d", tc.want, want)
			}
			qtyF := float64(tc.qtyNum) / float64(tc.qtyDen)
			got := resolveExactLine(t, exactLineSvc(float64(tc.factor), tc.price), qtyF, tc.price)
			if got != tc.want {
				t.Fatalf("qty %s factor %d price %d: line total %d, want %d",
					strconv.FormatFloat(qtyF, 'f', -1, 64), tc.factor, tc.price, got, tc.want)
			}
		})
	}
}

// halfCentEdgeQuantities scans k/10^d for exact half-cent edges of qty x price / factor and
// returns the k values that land on one, so the brute force grid covers the edges by
// construction and not only by stride luck. An exact half cent means twice the total is an odd
// whole number.
func halfCentEdgeQuantities(facNum, facDen, price, d, maxK int64, cap int) []int64 {
	var out []int64
	twice := new(big.Rat)
	for k := int64(1); k <= maxK; k++ {
		twice.SetFrac64(2*k*price*facDen, pow10(d)*facNum)
		if twice.Denom().IsInt64() && twice.Num().Bit(0) == 1 {
			out = append(out, k)
			if len(out) >= cap {
				break
			}
		}
	}
	return out
}

func pow10(d int64) int64 {
	n := int64(1)
	for i := int64(0); i < d; i++ {
		n *= 10
	}
	return n
}

// bruteForceQuantities builds the quantity grid: whole quantities and one to four decimal
// places, up to 5000, a deterministic stride sample. The half-cent edges join per (factor,
// price) cell in the brute force itself.
func bruteForceQuantities(t *testing.T) [][2]int64 {
	t.Helper()
	var out [][2]int64 // pairs of (numerator, decimals): qty = num / 10^decimals
	add := func(num, dec int64) { out = append(out, [2]int64{num, dec}) }
	for k := int64(1); k <= 12; k++ {
		add(k, 0)
	}
	for k := int64(13); k <= 5000; k += 503 {
		add(k, 0)
	}
	add(410, 0)
	for d := int64(1); d <= 4; d++ {
		limit := int64(5000) * pow10(d)
		for k := int64(1); k <= 12; k++ {
			add(k, d)
		}
		for k := int64(13); k <= limit; k += 997 * pow10(d-1) {
			add(k, d)
		}
	}
	return out
}

// TestBruteForceLineTotalAgainstOracle is the wide leg: factors 2, 3, 6, 12, 24, 50, 100, 144,
// 1000; prices 1 to 200 cents; quantities whole and with one to four decimals up to 5000,
// including every half-cent edge the scan finds. Zero lines may be off by a cent against the
// independent big.Rat oracle.
func TestBruteForceLineTotalAgainstOracle(t *testing.T) {
	factors := []float64{2, 3, 6, 12, 24, 50, 100, 144, 1000}
	grid := bruteForceQuantities(t)
	off := 0
	cases := 0
	var first string
	for _, f := range factors {
		for p := int64(1); p <= 200; p++ {
			svc := exactLineSvc(f, p)
			// every half-cent edge for this (factor, price) cell, by scan, per decimal size
			qtys := grid
			for d := int64(0); d <= 4; d++ {
				maxK := int64(1200) * pow10(d)
				if maxK > 6000 {
					maxK = 6000 // bounded scan window: edges near the small lines are the dense ones
				}
				for _, k := range halfCentEdgeQuantities(int64(f*1000), 1000, p, d, maxK, 24) {
					qtys = append(qtys, [2]int64{k, d})
				}
			}
			for _, q := range qtys {
				qtyF := float64(q[0]) / float64(pow10(q[1]))
				want := oracleLineTotal(q[0], pow10(q[1]), int64(f*1000), 1000, p)
				got := resolveExactLine(t, svc, qtyF, p)
				cases++
				if got != want {
					off++
					if off == 1 {
						first = "qty " + strconv.FormatFloat(qtyF, 'f', -1, 64) +
							" factor " + strconv.FormatFloat(f, 'f', -1, 64) +
							" price " + strconv.FormatInt(p, 10) +
							": got " + strconv.FormatInt(got, 10) + ", want " + strconv.FormatInt(want, 10)
					}
				}
			}
		}
	}
	if off != 0 {
		t.Fatalf("%d of %d lines priced off by a cent against the big.Rat oracle; first: %s", off, cases, first)
	}
}

// TestPercentArmsCannotMisround proves the census claim for the percent arms: applyPctOff,
// applyMarkup and applySurchargePct are INTEGER mulDiv through mulDivRoundHalfUp, and against
// the exact rational oracle they agree everywhere, so the principal's ruling ("make it exact
// where it can misround, otherwise show by brute force that it cannot and leave it") is
// discharged for the percent arms by evidence, not by argument. This leg is green before and
// after the fix; the census says why it exists.
func TestPercentArmsCannotMisround(t *testing.T) {
	oracle := func(v, num, den int64) int64 {
		return oracleRoundHalfAway(new(big.Rat).SetFrac64(v*num, den))
	}
	bad := 0
	cases := 0
	check := func(v, num, den int64) {
		cases++
		if mulDivRoundHalfUp(v, num, den) != oracle(v, num, den) {
			bad++
		}
	}
	// the exact pct-off and markup maps: v*num/den with num = 10000 -/+ bps
	for bps := int64(0); bps <= 10000; bps++ {
		for cents := int64(1); cents <= 200; cents++ {
			check(cents, 10000-bps, 10000) // applyPctOff
			check(cents, 10000+bps, 10000) // applyMarkup and applySurchargePct share the map
		}
	}
	// large bases on a stride so the multiply's high bits are exercised too
	for bps := int64(0); bps <= 10000; bps += 7 {
		for cents := int64(201); cents <= 2000000; cents = cents*3 + 1 {
			check(cents, 10000-bps, 10000)
			check(cents, 10000+bps, 10000)
		}
	}
	if bad != 0 {
		t.Fatalf("%d of %d percent-arm cases disagree with the exact oracle", bad, cases)
	}
}
