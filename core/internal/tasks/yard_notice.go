package tasks

// yard_notice.go, the words and the routing intent of the notice a yard change sends to the order's
// sales rep. tasks declares WHO must hear (AudienceOrderRep, with the order, the customer and the
// order's own salesperson on the ref); the composition root maps that to the rep's DM through the
// messaging module's rep routing, and falls back to the branch counter, saying so, when the order
// has no rep. This file builds the message, it never decides a channel.

import (
	"context"
	"fmt"
	"strconv"
)

// reasonPhrase is the sentence form of a reason code.
func reasonPhrase(code string) string {
	switch code {
	case ShortReasonCondensed:
		return "Skids condensed"
	case ShortReasonDamaged:
		return "Damaged"
	case ShortReasonNotInYard:
		return "Not in the yard"
	case ShortReasonCountCorrection:
		return "Count corrected"
	case ShortReasonOther:
		return "Other"
	}
	return code
}

// dollars renders integer cents as $D.CC.
func dollars(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

// reasonSentence is "Reason: <phrase>[, <note>]."
func reasonSentence(code, note string) string {
	s := "Reason: " + reasonPhrase(code)
	if note != "" {
		s += ", " + note
	}
	return s + "."
}

// standingSentence says what the order's money now means: nothing, an invoice that needs bringing
// down, or money owed back or due on an order that has been paid. depositOnly keeps the sentence to
// the skid deposit, whose owed and due figures are carried on the facts.
func standingSentence(f YardOrderFacts, deposit bool) string {
	switch {
	case f.PaidCents > 0 && f.OwedBackCents > 0:
		return fmt.Sprintf(" The order is already paid: %s was taken on invoice %s, and %s is now owed back to the customer.",
			dollars(f.PaidCents), f.InvoiceNo, dollars(f.OwedBackCents))
	case f.PaidCents > 0 && f.DueCents > 0:
		return fmt.Sprintf(" The order is already paid: %s was taken on invoice %s, and %s is now due from the customer.",
			dollars(f.PaidCents), f.InvoiceNo, dollars(f.DueCents))
	case f.PaidCents > 0:
		return fmt.Sprintf(" The order is already paid: %s was taken on invoice %s, and nothing further is owed either way.",
			dollars(f.PaidCents), f.InvoiceNo)
	case f.InvoiceNo != "" && !deposit && f.InvoicedCents != f.TotalAfterCents:
		return fmt.Sprintf(" Invoice %s was raised for %s and now needs to come down to %s.",
			f.InvoiceNo, dollars(f.InvoicedCents), dollars(f.TotalAfterCents))
	}
	return ""
}

// shortPickMessage is the body for one changed or still-short line.
func shortPickMessage(f YardOrderFacts, e YardLineEffect, reason, note string) string {
	head := fmt.Sprintf("order %s, line %d (%s)", f.OrderNo, e.LineNo, e.Description)
	if backordered, _ := strconv.ParseFloat(e.QtyBackordered, 64); backordered <= 0 {
		return fmt.Sprintf("Pick restored on %s: now picked in full, %s %s of %s %s ordered. The order total is %s.%s",
			head, e.QtyPicked, e.UOM, e.QtyOrdered, e.UOM, dollars(f.TotalAfterCents), standingSentence(f, false))
	}
	return fmt.Sprintf("Short pick on %s: ordered %s %s, picked %s %s, %s %s on backorder. %s The order total is now %s (was %s).%s",
		head, e.QtyOrdered, e.UOM, e.QtyPicked, e.UOM, e.QtyBackordered, e.UOM,
		reasonSentence(reason, note), dollars(f.TotalAfterCents), dollars(f.TotalBeforeCents), standingSentence(f, false))
}

// skidMessage is the body for a changed skid count.
func skidMessage(in YardSkidChange, r YardSkidResult) string {
	f := r.Order
	var what string
	if r.CountBefore == nil {
		what = fmt.Sprintf("Skid count on order %s set to %d skids.", f.OrderNo, r.CountAfter)
	} else {
		what = fmt.Sprintf("Skid count on order %s changed from %d to %d skids.", f.OrderNo, *r.CountBefore, r.CountAfter)
	}
	s := what + " " + reasonSentence(in.Reason, in.Note)
	if r.DepositBeforeCents > 0 || r.DepositAfterCents > 0 {
		s += fmt.Sprintf(" The skid deposit is now %s (was %s).", dollars(r.DepositAfterCents), dollars(r.DepositBeforeCents))
	}
	return s + standingSentence(f, true)
}

// notifyOrderRep posts one notice addressed to the order's rep. Best effort by the module's
// doctrine: a messaging blip is logged and never fails a yard write.
func (s *Service) notifyOrderRep(ctx context.Context, t Task, f YardOrderFacts, key, topic, summary string) bool {
	if s.notifier == nil {
		return false
	}
	ref := TaskEventRef{
		EventKey:   key,
		BranchID:   firstNonEmpty(f.BranchID, t.BranchID),
		Topic:      topic,
		Entity:     "order:" + t.OrderID,
		DeepLink:   "/app/orders/" + t.OrderID,
		Summary:    summary,
		Audience:   AudienceOrderRep,
		OrderID:    t.OrderID,
		CustomerID: f.CustomerID,
		RepUserID:  f.SalespersonID,
	}
	if err := s.notifier.PostTaskEvent(ctx, ref); err != nil {
		if s.log != nil {
			s.log.Warn("tasks: order rep notice failed", "task", t.ID, "order", t.OrderID, "key", key, "err", err)
		}
		return false
	}
	return true
}

// noticeShortPicks tells the rep about every line that changed or is still short. A line still short
// and unchanged is sent again under the key its last change carried (the revision), which is what
// lets a notice that was lost be delivered when the yard repeats the record, and a delivered one
// deduplicate. A full pick that changed nothing is not news.
func (s *Service) noticeShortPicks(ctx context.Context, taskID string, lines []PickedLineInput, res YardPickResult) {
	if s.notifier == nil || len(res.Lines) == 0 {
		return
	}
	t, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return
	}
	byLine := make(map[int]PickedLineInput, len(lines))
	for _, l := range lines {
		byLine[l.OrderLineNo] = l
	}
	for _, e := range res.Lines {
		backordered, _ := strconv.ParseFloat(e.QtyBackordered, 64)
		if !e.Changed && backordered <= 0 {
			continue
		}
		l := byLine[e.LineNo]
		key := fmt.Sprintf("yard.short:%s:%d:%d", taskID, e.LineNo, e.Revision)
		s.notifyOrderRep(ctx, t, res.Order, key, "yard.short_pick", shortPickMessage(res.Order, e, l.ShortReason, l.ShortNote))
	}
}
