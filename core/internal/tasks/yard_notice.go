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
// the skid deposit, whose owed and due figures are carried on the facts. figures false leaves the
// figures out and keeps the words ("owed back on the invoice", "needs to come down"), the body a
// channel whose readers are not all on the cost and margin list may carry: a figure never reaches
// an off list role in words either (ruling 91).
func standingSentence(f YardOrderFacts, deposit, figures bool) string {
	switch {
	case f.PaidCents > 0 && f.OwedBackCents > 0:
		if !figures {
			return fmt.Sprintf(" The order is already paid: money was taken on invoice %s, and some is now owed back to the customer.", f.InvoiceNo)
		}
		return fmt.Sprintf(" The order is already paid: %s was taken on invoice %s, and %s is now owed back to the customer.",
			dollars(f.PaidCents), f.InvoiceNo, dollars(f.OwedBackCents))
	case f.PaidCents > 0 && f.DueCents > 0:
		if !figures {
			return fmt.Sprintf(" The order is already paid: money was taken on invoice %s, and some is now due from the customer.", f.InvoiceNo)
		}
		return fmt.Sprintf(" The order is already paid: %s was taken on invoice %s, and %s is now due from the customer.",
			dollars(f.PaidCents), f.InvoiceNo, dollars(f.DueCents))
	case f.PaidCents > 0:
		if !figures {
			return fmt.Sprintf(" The order is already paid: money was taken on invoice %s, and nothing further is owed either way.", f.InvoiceNo)
		}
		return fmt.Sprintf(" The order is already paid: %s was taken on invoice %s, and nothing further is owed either way.",
			dollars(f.PaidCents), f.InvoiceNo)
	case f.InvoiceNo != "" && !deposit && f.InvoicedCents != f.TotalAfterCents:
		if !figures {
			return fmt.Sprintf(" Invoice %s no longer matches the order total and needs to come down.", f.InvoiceNo)
		}
		return fmt.Sprintf(" Invoice %s was raised for %s and now needs to come down to %s.",
			f.InvoiceNo, dollars(f.InvoicedCents), dollars(f.TotalAfterCents))
	}
	return ""
}

// shortPickMessage is the body for one changed or still-short line. figures false leaves the
// order's figures out and keeps the words, the body a channel whose readers are not all on the
// cost and margin list may carry.
func shortPickMessage(f YardOrderFacts, e YardLineEffect, figures bool) string {
	head := fmt.Sprintf("order %s, line %d (%s)", f.OrderNo, e.LineNo, e.Description)
	if backordered, _ := strconv.ParseFloat(e.QtyBackordered, 64); backordered <= 0 {
		total := fmt.Sprintf("The order total is %s.", dollars(f.TotalAfterCents))
		if !figures {
			total = "The order total changed."
		}
		return fmt.Sprintf("Pick restored on %s: now picked in full, %s %s of %s %s ordered. %s%s",
			head, e.QtyPicked, e.UOM, e.QtyOrdered, e.UOM, total, standingSentence(f, false, figures))
	}
	was := ""
	if f.TotalBeforeCents != f.TotalAfterCents && figures {
		was = fmt.Sprintf(" (was %s)", dollars(f.TotalBeforeCents))
	}
	reason := ""
	if e.Reason != "" {
		reason = " " + reasonSentence(e.Reason, e.Note)
	}
	total := fmt.Sprintf("The order total is now %s", dollars(f.TotalAfterCents))
	if !figures {
		total = "The order total changed"
	}
	return fmt.Sprintf("Short pick on %s: ordered %s %s, picked %s %s, %s %s on backorder.%s %s%s.%s",
		head, e.QtyOrdered, e.UOM, e.QtyPicked, e.UOM, e.QtyBackordered, e.UOM,
		reason, total, was, standingSentence(f, false, figures))
}

// skidMessage is the body for a changed skid count. figures false leaves the deposit and invoice
// figures out and keeps the words (the count, "the skid deposit changed", "owed back on the
// invoice"), the body a channel whose readers are not all on the cost and margin list may carry.
func skidMessage(in YardSkidChange, r YardSkidResult, figures bool) string {
	f := r.Order
	var what string
	if r.CountBefore == nil {
		what = fmt.Sprintf("Skid count on order %s set to %d skids.", f.OrderNo, r.CountAfter)
	} else {
		what = fmt.Sprintf("Skid count on order %s changed from %d to %d skids.", f.OrderNo, *r.CountBefore, r.CountAfter)
	}
	s := what + " " + reasonSentence(in.Reason, in.Note)
	if r.DepositBeforeCents > 0 || r.DepositAfterCents > 0 {
		if figures {
			s += fmt.Sprintf(" The skid deposit is now %s (was %s).", dollars(r.DepositAfterCents), dollars(r.DepositBeforeCents))
		} else {
			s += " The skid deposit changed."
		}
	}
	return s + standingSentence(f, true, figures)
}

// notifyOrderRep posts one notice addressed to the order's rep, in two bodies: the summary with
// its figures, for the rep's own copy and any channel whose readers are all on the cost and
// margin list, and the same words with the figures left out, for every other channel the routing
// may reach (ruling 91: a figure never reaches an off list role, in words either). Best effort by
// the module's doctrine: a messaging blip is logged and never fails a yard write.
func (s *Service) notifyOrderRep(ctx context.Context, t Task, f YardOrderFacts, key, topic, summary, summaryNoFigures string) bool {
	if s.notifier == nil {
		return false
	}
	ref := TaskEventRef{
		EventKey:         key,
		BranchID:         firstNonEmpty(f.BranchID, t.BranchID),
		Topic:            topic,
		Entity:           "order:" + t.OrderID,
		DeepLink:         "/app/orders/" + t.OrderID,
		Summary:          summary,
		SummaryNoFigures: summaryNoFigures,
		Audience:         AudienceOrderRep,
		OrderID:          t.OrderID,
		CustomerID:       f.CustomerID,
		RepUserID:        f.SalespersonID,
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
	for _, e := range res.Lines {
		backordered, _ := strconv.ParseFloat(e.QtyBackordered, 64)
		if !e.Changed && backordered <= 0 {
			continue
		}
		key := fmt.Sprintf("yard.short:%s:%d:%d", t.OrderID, e.LineNo, e.Revision)
		s.notifyOrderRep(ctx, t, res.Order, key, "yard.short_pick",
			shortPickMessage(res.Order, e, true), shortPickMessage(res.Order, e, false))
	}
}
