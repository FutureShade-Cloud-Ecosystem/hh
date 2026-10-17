//go:build integration

// ar_fence_standing_integration_test.go, the security review's P2-3: the counter's standing is the account's whole position.
package billing

import (
	"testing"
	"time"
)

// P2-3: the counter's standing is the account's whole position, whatever branch the caller works.
func TestARFenceReview_TheCountersStandingIsAccountWide(t *testing.T) {
	w := newARFenceWorld(t)
	svc := NewCreditService(NewRepository(w.pool)).WithClock(func() time.Time { return d510Now })
	row, _, found, err := svc.CustomerAging(arTrentonOnly(), w.kingOnly)
	if err != nil {
		t.Fatalf("customer aging: %v", err)
	}
	if !found || row.TotalCents != 50000 || !row.OnHold {
		t.Fatalf("the account standing must read the whole position (found %v total %d on hold %v), not clear", found, row.TotalCents, row.OnHold)
	}
	row, _, found, err = svc.CustomerAging(arTrentonOnly(), w.dual)
	if err != nil || !found || row.TotalCents != 80000 {
		t.Fatalf("a shared account's standing totals every branch (found %v total %d err %v)", found, row.TotalCents, err)
	}
}
