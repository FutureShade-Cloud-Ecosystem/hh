package taxwords

import (
	"context"
	"errors"
	"testing"
)

func TestForOntarioIsHSTExactlyAndEveryOtherIsSalesTax(t *testing.T) {
	for _, j := range []string{"ON", "on", " ON ", ""} {
		if got := For(j); got != "HST" {
			t.Errorf("For(%q) = %q, want HST", j, got)
		}
	}
	for _, j := range []string{"CA", "TX", "BC", "NY"} {
		if got := For(j); got != "Sales tax" {
			t.Errorf("For(%q) = %q, want Sales tax", j, got)
		}
	}
}

type src struct {
	w   string
	err error
}

func (s src) TaxLabel(context.Context) (string, error) { return s.w, s.err }

func TestResolve(t *testing.T) {
	ctx := context.Background()
	if w, err := Resolve(ctx, nil); w != "HST" || err != nil {
		t.Errorf("nil source: %q %v", w, err)
	}
	if w, err := Resolve(ctx, src{w: "Sales tax"}); w != "Sales tax" || err != nil {
		t.Errorf("source: %q %v", w, err)
	}
	if w, err := Resolve(ctx, src{err: errors.New("down")}); w != "Tax" || err == nil {
		t.Errorf("failed read must be the neutral word and the error: %q %v", w, err)
	}
}
