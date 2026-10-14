package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// CreditHoldNotice is the words and the keys of a credit hold notice for sales: which customer, in
// which branch, under which event key. It carries no amount, no reason and no figure of any kind.
type CreditHoldNotice struct {
	CustomerID   string
	CustomerName string // used only when the customer record has no name
	BranchID     string
	EventKey     string
	DeepLink     string
}

// CreditHoldNoticeResult says where the notice landed. OwnerChannelID is empty when the customer has
// no sales owner on file; HouseSlug is the branch's house room (or the global floor).
type CreditHoldNoticeResult struct {
	OwnerChannelID string
	HouseChannelID string
	HouseSlug      string
}

// PostCreditHoldNotice tells sales that a customer is on credit hold, in the rooms sales already
// holds and not in the money rooms. A credit hold is a fact sales must act on ("take payment before
// release"), so it reaches (1) the customer's sales owner, in their rep DM, and (2) the branch's
// house room, which every user granted the branch (Inside Sales, Outside Sales, Sales Manager,
// Branch Manager) is seated in. The money rooms (#payments, #finance) seat the money desk only
// (migration 0390), so a sales role would never see the finance copy. The words name the customer
// and the action and never an amount or the reason the hold was placed.
//
// Both posts are idempotent on the event key, so an outbox redelivery writes nothing twice.
func (s *Service) PostCreditHoldNotice(ctx context.Context, in CreditHoldNotice) (CreditHoldNoticeResult, error) {
	rs, err := s.reps()
	if err != nil {
		return CreditHoldNoticeResult{}, err
	}
	name := strings.TrimSpace(in.CustomerName)
	branch := in.BranchID
	if strings.TrimSpace(in.CustomerID) != "" {
		recName, home, err := rs.CustomerFacts(ctx, in.CustomerID)
		if err != nil && !errors.Is(err, errNotFound) {
			return CreditHoldNoticeResult{}, fmt.Errorf("credit hold notice: customer: %w", err)
		}
		if err == nil {
			if recName != "" {
				name = recName
			}
			if branch == "" {
				branch = home
			}
		}
	}
	if name == "" {
		name = "A customer"
	}
	body := name + " is on credit hold: take payment before release."
	ref := EventRef{Topic: TopicCreditHold, DeepLink: in.DeepLink, Summary: body}
	if in.CustomerID != "" {
		ref.Entity = "customer:" + in.CustomerID
	}

	var res CreditHoldNoticeResult
	if in.CustomerID != "" {
		owner, err := s.NotifyOrderRep(ctx, OrderRepNotice{
			CustomerID: in.CustomerID, EventKey: in.EventKey + ":sales-owner", Body: body, Ref: ref,
		})
		if err != nil {
			return res, fmt.Errorf("credit hold notice: owner: %w", err)
		}
		res.OwnerChannelID = owner.ChannelID
	}
	slug, err := rs.BranchHouseSlug(ctx, branch)
	if err != nil {
		return res, fmt.Errorf("credit hold notice: house room: %w", err)
	}
	m, _, err := s.PostEventToChannelSlug(ctx, slug, in.EventKey+":sales-house", body, ref, false)
	if err != nil {
		return res, fmt.Errorf("credit hold notice: house post: %w", err)
	}
	res.HouseSlug, res.HouseChannelID = slug, m.ChannelID
	return res, nil
}
