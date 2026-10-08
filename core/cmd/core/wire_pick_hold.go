package main

// wire_pick_hold.go, item 9.3 (the row 1047 deposits work): the two adapters
// that map billing's pay before release facts onto the seams its consumers
// declare. The order module's picking hold wants the whole facts block; the
// dispatch module's will call doors want held and owed only. Both read the
// same repository method, so the two doors and the hold cannot disagree about
// the money.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
)

// pickHoldAdapter satisfies order.PickHoldReader over the billing repository.
type pickHoldAdapter struct{ repo *billing.Repository }

func (a pickHoldAdapter) PickHoldFacts(ctx context.Context, orderID string) (order.PickHoldFacts, error) {
	held, owed, paid, total, err := a.repo.PickHoldForOrder(ctx, orderID)
	if err != nil {
		return order.PickHoldFacts{}, err
	}
	out := order.PickHoldFacts{Held: held, OrderTotalCents: total, PaidCents: paid, OwedCents: owed}
	if held {
		out.Words = order.PickHoldWords
	}
	if paid > 0 && owed > 0 {
		out.InProcessWords = order.PickInProcessWords
	}
	return out, nil
}

// dispatchPickHoldAdapter satisfies dispatch.PickHoldGate over the same read.
type dispatchPickHoldAdapter struct{ repo *billing.Repository }

func (a dispatchPickHoldAdapter) HeldFromPicking(ctx context.Context, orderID string) (bool, int64, error) {
	held, owed, _, _, err := a.repo.PickHoldForOrder(ctx, orderID)
	return held, owed, err
}
