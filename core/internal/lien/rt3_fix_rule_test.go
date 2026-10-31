package lien

import (
	"testing"
	"time"
)

func d(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &t
}

// The preservation period starts at the latest date the job carries that can start it.
func TestRT3Fix_PreservationStartIsTheLatestOfTheDates(t *testing.T) {
	cases := []struct {
		name            string
		pub, perf, sup  *time.Time
		wantBasis, date string
	}{
		{"publication latest", d("2001-03-10"), d("2001-02-01"), d("2001-02-20"), BasisPublication, "2001-03-10"},
		{"performance latest", d("2001-01-10"), d("2001-02-01"), d("2001-01-20"), BasisSubstantialPerformance, "2001-02-01"},
		{"last supply latest", nil, d("2001-02-01"), d("2001-02-20"), BasisCompletion, "2001-02-20"},
		{"only performance", nil, d("2001-02-01"), nil, BasisSubstantialPerformance, "2001-02-01"},
		{"none", nil, nil, nil, BasisNone, ""},
	}
	for _, c := range cases {
		basis, date := preservationStart(c.pub, c.perf, c.sup)
		if basis != c.wantBasis || date != c.date {
			t.Errorf("%s: got %s %q, want %s %q", c.name, basis, date, c.wantBasis, c.date)
		}
	}
}
