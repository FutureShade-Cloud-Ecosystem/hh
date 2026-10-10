package billing

import (
	"context"
	"errors"
	"testing"
)

type stubTrail struct {
	held bool
	err  error
}

func (s stubTrail) HoldsPaymentTrail(context.Context, string, string) (bool, error) {
	return s.held, s.err
}

func TestLatePaymentTrail_UnboundWithholds(t *testing.T) {
	var l LatePaymentTrail
	held, err := l.HoldsPaymentTrail(context.Background(), "u", "o")
	if held || err != nil {
		t.Fatalf("an unbound reader withholds without an error, got %v %v", held, err)
	}
}

func TestLatePaymentTrail_BoundAnswersAndPassesErrors(t *testing.T) {
	var l LatePaymentTrail
	l.Bind(stubTrail{held: true})
	if held, err := l.HoldsPaymentTrail(context.Background(), "u", "o"); !held || err != nil {
		t.Fatalf("a bound reader's yes passes through, got %v %v", held, err)
	}
	boom := errors.New("roles unreadable")
	l.Bind(stubTrail{err: boom})
	if held, err := l.HoldsPaymentTrail(context.Background(), "u", "o"); held || !errors.Is(err, boom) {
		t.Fatalf("a bound reader's error passes through and withholds, got %v %v", held, err)
	}
}
