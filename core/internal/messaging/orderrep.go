package messaging

import (
	"context"
	"fmt"
	"strings"
)

// - A notice about one ORDER, to that order's sales rep -
//
// The rep routing in this package answers an inbound CUSTOMER message (RouteCustomerMessage) and, for
// payments, an opt-in rule (NotifyPaymentRules). A yard change on an order, a short pick or a skid
// count, is neither: it is a fact about one order that the order's rep must hear at once. This is that
// door, built on the same resolution so the question "whose order is this" has one answer.
//
// The order's rep is the most specific of three, in this order:
//
//	a per-order override in rep_assignments   → LevelOrder        (split books)
//	the order's own salesperson (orders.salesperson_id) → LevelSalesperson
//	the customer-level assignment             → LevelCustomer
//
// and nothing at all is RouteNone. NONE writes NOTHING here: the caller falls back to the branch
// counter and says in the message that it did, because only the caller knows its counter channel and
// the message is the caller's to word. An order notice never opens a house queue conversation: that
// queue is for customers waiting on a person, and nobody is waiting here.

// LevelSalesperson is the order's own salesperson answering the routing question: more specific than
// the customer's default rep, less than an explicit per-order override.
const LevelSalesperson = "SALESPERSON"

// RouteNone is an order notice that found no rep and wrote nothing; RouteCounter is one that found
// no rep and was delivered to a fallback channel with the fallback sentence in front.
const (
	RouteNone    = "NONE"
	RouteCounter = "COUNTER"
)

// OrderRepNotice is one notice about one order. OrderRepUserID is the order's own salesperson, passed
// by the caller because the messaging module never reads the order tables. EventKey makes the post
// idempotent across redelivery, exactly like an FR-3 event.
type OrderRepNotice struct {
	CustomerID     string
	OrderID        string
	OrderRepUserID string
	EventKey       string
	Body           string
	Ref            EventRef
	// BodyNoFigures and RoleSeesFigures let the caller hand over two bodies and a role predicate so
	// the choice is made here, once the rep is known (the precedence below is the only copy). Body
	// is the figured words, BodyNoFigures the same words with the figures left out. The figured body
	// goes only to a rep whose own role RoleSeesFigures admits; no predicate, an unreadable role or
	// a refusal sends BodyNoFigures. With no BodyNoFigures the notice names no figure and Body goes
	// as it is.
	BodyNoFigures   string
	RoleSeesFigures func(role string) bool
	// FallbackSlugs are the channels to try, in order, when the order has no rep; empty means write
	// nothing. FallbackPrefix is put in front of Body there, so a reader knows why it arrived.
	FallbackSlugs  []string
	FallbackPrefix string
}

// OrderRepResult says where the notice landed and why. Kind is RouteRepDM or RouteNone.
type OrderRepResult struct {
	Kind      string
	Level     string
	RepUserID string
	ChannelID string
	Inserted  bool
}

// NotifyOrderRep delivers an order notice to the order's rep DM, or reports that there is no rep.
func (s *Service) NotifyOrderRep(ctx context.Context, in OrderRepNotice) (OrderRepResult, error) {
	rs, err := s.reps()
	if err != nil {
		return OrderRepResult{}, err
	}
	if strings.TrimSpace(in.CustomerID) == "" {
		return OrderRepResult{}, ErrNoCustomer
	}
	if strings.TrimSpace(in.Body) == "" {
		return OrderRepResult{}, ErrEmptyBody
	}
	rows, err := rs.RepAssignmentsForCustomer(ctx, in.CustomerID)
	if err != nil {
		return OrderRepResult{}, fmt.Errorf("order rep: assignments: %w", err)
	}
	a, level := resolveAssignment(rows, in.OrderID)
	rep := a.RepUserID
	if level != LevelOrder && strings.TrimSpace(in.OrderRepUserID) != "" {
		rep, level = in.OrderRepUserID, LevelSalesperson
	}
	if rep == "" || level == LevelNone {
		return s.orderNoticeFallback(ctx, in)
	}
	channelID, err := rs.EnsureRepDMChannel(ctx, rep)
	if err != nil {
		return OrderRepResult{}, fmt.Errorf("order rep: ensure dm: %w", err)
	}
	body := s.bodyForRep(ctx, in, rep)
	ref := in.Ref
	ref.Summary = body
	if in.OrderID != "" && ref.Entity == "" {
		ref.Entity = "order:" + in.OrderID
	}
	m, inserted, err := s.postEventToChannelID(ctx, channelID, in.EventKey, body, ref, EventOpts{Priority: PriorityNormal})
	if err != nil {
		return OrderRepResult{}, fmt.Errorf("order rep: post: %w", err)
	}
	_ = m
	return OrderRepResult{Kind: RouteRepDM, Level: level, RepUserID: rep, ChannelID: channelID, Inserted: inserted}, nil
}

// bodyForRep picks the words for the rep the notice goes to: the figured body only for a rep whose
// role the caller's predicate admits, fail closed on every other leg (no predicate, no role found,
// an unreadable role).
func (s *Service) bodyForRep(ctx context.Context, in OrderRepNotice, rep string) string {
	if in.BodyNoFigures == "" {
		return in.Body
	}
	if in.RoleSeesFigures == nil {
		return in.BodyNoFigures
	}
	role, err := s.repo.UserRoleCode(ctx, rep)
	if err != nil || role == "" || !in.RoleSeesFigures(role) {
		return in.BodyNoFigures
	}
	return in.Body
}

// orderNoticeFallback delivers a rep-less order notice to the first reachable fallback channel.
func (s *Service) orderNoticeFallback(ctx context.Context, in OrderRepNotice) (OrderRepResult, error) {
	none := OrderRepResult{Kind: RouteNone, Level: LevelNone}
	body := in.FallbackPrefix + in.Body
	ref := in.Ref
	ref.Summary = body
	if in.OrderID != "" && ref.Entity == "" {
		ref.Entity = "order:" + in.OrderID
	}
	var lastErr error
	for _, slug := range in.FallbackSlugs {
		m, inserted, err := s.PostEventToChannelSlug(ctx, slug, in.EventKey, body, ref, false)
		if err != nil {
			lastErr = err
			continue
		}
		return OrderRepResult{Kind: RouteCounter, Level: LevelNone, ChannelID: m.ChannelID, Inserted: inserted}, nil
	}
	if lastErr != nil {
		return none, fmt.Errorf("order rep: no rep and no reachable fallback: %w", lastErr)
	}
	return none, nil
}
