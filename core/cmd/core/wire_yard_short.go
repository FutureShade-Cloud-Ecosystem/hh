package main

// wire_yard_short.go, the composition root's two adapters for the yard's short pick: the order
// module's effect (tasks.YardOrderChanges over order.Service) and the billing read the notice needs
// (order.InvoiceStandingReader over the billing repository). Each module declares the port it
// consumes; this file is where one meets the other, and where the refusals are translated into the
// words the yard's surface answers with.

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

// yardOrderAdapter satisfies tasks.YardOrderChanges over order.Service.ApplyYardPicks.
type yardOrderAdapter struct{ svc *order.Service }

func (a yardOrderAdapter) ApplyPicks(ctx context.Context, in tasks.YardPickChange) (tasks.YardPickResult, error) {
	oin := order.YardPickInput{OrderID: in.OrderID, ActorID: in.ActorID}
	for _, l := range in.Lines {
		oin.Lines = append(oin.Lines, order.YardPickLineInput{
			LineNo: l.LineNo, QtyPicked: l.QtyPicked, Reason: l.Reason, Note: l.Note,
		})
	}
	out, err := a.svc.ApplyYardPicks(ctx, oin)
	if err != nil {
		return tasks.YardPickResult{}, yardOrderRefusal(err)
	}
	res := tasks.YardPickResult{Order: tasks.YardOrderFacts(out.Order)}
	for _, l := range out.Lines {
		res.Lines = append(res.Lines, tasks.YardLineEffect(l))
	}
	return res, nil
}

// yardOrderRefusal translates the order module's refusals into the tasks module's vocabulary.
func yardOrderRefusal(err error) error {
	var notEditable *order.NotEditableError
	switch {
	case errors.Is(err, order.ErrAboveOrdered):
		return fmt.Errorf("%w (%v)", tasks.ErrAboveOrdered, err)
	case errors.Is(err, order.ErrNotFound), errors.Is(err, order.ErrLineNotFound):
		return fmt.Errorf("%w: %v", tasks.ErrNotFound, err)
	case errors.Is(err, order.ErrLineInUse), errors.Is(err, order.ErrCreditedAway), errors.As(err, &notEditable):
		return fmt.Errorf("%w: %v", tasks.ErrOrderRefused, err)
	}
	return err
}

// invoiceStandingAdapter satisfies order.InvoiceStandingReader over the billing repository.
type invoiceStandingAdapter struct{ repo *billing.Repository }

func (a invoiceStandingAdapter) InvoiceStanding(ctx context.Context, orderID string) (order.InvoiceStanding, error) {
	no, total, paid, found, err := a.repo.InvoiceStandingForOrder(ctx, orderID)
	if err != nil {
		return order.InvoiceStanding{}, err
	}
	return order.InvoiceStanding{Found: found, InvoiceNo: no, TotalCents: total, PaidCents: paid}, nil
}

// RecordSkids satisfies tasks.YardOrderChanges over order.Service.RecordYardSkids.
func (a yardOrderAdapter) RecordSkids(ctx context.Context, in tasks.YardSkidChange) (tasks.YardSkidResult, error) {
	out, err := a.svc.RecordYardSkids(ctx, order.YardSkidInput{
		OrderID: in.OrderID, ActorID: in.ActorID, SkidCount: in.SkidCount, Reason: in.Reason, Note: in.Note,
	})
	if err != nil {
		return tasks.YardSkidResult{}, yardOrderRefusal(err)
	}
	return tasks.YardSkidResult{
		Order: tasks.YardOrderFacts(out.Order), CountBefore: out.CountBefore, CountAfter: out.CountAfter,
		DepositBeforeCents: out.DepositBeforeCents, DepositAfterCents: out.DepositAfterCents, Changed: out.Changed,
		ChangeID: out.ChangeID,
	}, nil
}

// backorderNoticeAdapter satisfies order.BackorderNotifier over the yard's chat notifier, so the
// order's rep hears that an edit cleared a short line's backorder through the same routing as the
// short pick itself (the order's override, its salesperson, the customer's rep, else the counter).
type backorderNoticeAdapter struct{ chat tasks.ChatNotifier }

func (a backorderNoticeAdapter) BackorderCleared(ctx context.Context, n order.BackorderClearedNotice) error {
	return a.chat.PostTaskEvent(ctx, tasks.TaskEventRef{
		EventKey: n.Key, BranchID: n.Order.BranchID, Topic: "order.backorder_cleared",
		Entity: "order:" + n.Order.OrderID, DeepLink: "/app/orders/" + n.Order.OrderID,
		Summary:  backorderClearedText(n),
		Audience: tasks.AudienceOrderRep, OrderID: n.Order.OrderID,
		CustomerID: n.Order.CustomerID, RepUserID: n.Order.SalespersonID,
	})
}

// backorderClearedText says what the edit did, in the order's own words.
func backorderClearedText(n order.BackorderClearedNotice) string {
	return fmt.Sprintf("Order %s, line %d (%s) was edited to %s %s, which is a new order quantity: the %s %s the yard had picked short and held on backorder is cleared, and the line is billed at the edit's price.",
		n.Order.OrderNo, n.LineNo, n.Description, n.NewQty, n.UOM, n.ClearedQty, n.UOM)
}
