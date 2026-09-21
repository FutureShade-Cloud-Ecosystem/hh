package moneyutil

import "testing"

func sum(xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}

func TestAllocate_SumsExactly(t *testing.T) {
	cases := []struct {
		total   int64
		weights []int64
	}{
		{1300, []int64{10000}},
		{1300, []int64{3333, 3333, 3334}},
		{100, []int64{1, 1, 1}}, // 34/33/33 (largest remainders take the extra penny)
		{101, []int64{1, 1, 1}}, // 34/34/33
		{999, []int64{50, 30, 20}},
		{7, []int64{1, 1, 1, 1, 1, 1, 1, 1}},
		{0, []int64{5, 5}},
		{500, []int64{0, 0, 0}}, // no basis → even spread
	}
	for _, c := range cases {
		got := AllocateLargestRemainder(c.total, c.weights)
		if len(got) != len(c.weights) {
			t.Fatalf("len(%v) = %d, want %d", got, len(got), len(c.weights))
		}
		if s := sum(got); s != c.total {
			t.Fatalf("Allocate(%d,%v) = %v sums to %d, want %d", c.total, c.weights, got, s, c.total)
		}
	}
}

func TestAllocate_LargestRemainderWins(t *testing.T) {
	// total 100 over weights 1/1/1: floors are 33/33/33 (=99); the leftover penny goes to the lowest index.
	got := AllocateLargestRemainder(100, []int64{1, 1, 1})
	want := []int64{34, 33, 33}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestAllocate_ProportionalSplit(t *testing.T) {
	// 999 over 50/30/20 → 500/300/199 (49.95→500 by rounding, sums to 999 exactly).
	got := AllocateLargestRemainder(999, []int64{50, 30, 20})
	if sum(got) != 999 {
		t.Fatalf("sum %d", sum(got))
	}
	if got[0] < got[1] || got[1] < got[2] {
		t.Fatalf("allocation not monotone with weights: %v", got)
	}
}

func TestAllocate_NegativeTotalReverses(t *testing.T) {
	pos := AllocateLargestRemainder(1300, []int64{3333, 3333, 3334})
	neg := AllocateLargestRemainder(-1300, []int64{3333, 3333, 3334})
	if sum(neg) != -1300 {
		t.Fatalf("negative sum %d", sum(neg))
	}
	for i := range pos {
		if neg[i] != -pos[i] {
			t.Fatalf("reversal not mirror: +%v vs %v", pos, neg)
		}
	}
}

func TestAllocate_Empty(t *testing.T) {
	if got := AllocateLargestRemainder(100, nil); got != nil {
		t.Fatalf("nil weights → nil, got %v", got)
	}
}
