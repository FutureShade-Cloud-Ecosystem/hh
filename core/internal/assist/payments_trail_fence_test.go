package assist

import (
	"context"
	"errors"
	"testing"
)

// HH-777 round 2: payments.today is the dashboard tile's number and payments.range is the same rollup by
// day. Both are the payment trail's (ruling 87), so the tier floor is not enough: a caller the handler did
// not stamp as holding the trail is refused before any read, the same decision payment.lookup makes.
func TestPaymentsCommands_AreThePaymentTrailAndRefusedWithoutIt(t *testing.T) {
	for code, args := range map[string]Args{
		"payments.today": {},
		"payments.range": {"from": "2026-08-10", "to": "2026-08-16"},
	} {
		rd, d := seededReaders(), seededDeep()
		reg := NewRegistry(deepDeps(rd, d), nil)
		noTrail := manager()
		noTrail.PaymentTrail = false
		if _, err := reg.Run(context.Background(), noTrail, code, args); !errors.Is(err, ErrRefused) {
			t.Fatalf("%s: a caller without the payment trail ran it: %T (%v), want ErrRefused", code, err, err)
		}
		if d.calls != 0 {
			t.Fatalf("%s: the rollup was read for a caller without the trail, the refusal must come first", code)
		}
		if _, err := reg.Run(context.Background(), manager(), code, args); err != nil {
			t.Fatalf("%s: a trail holder still runs it: %v", code, err)
		}
	}
}
