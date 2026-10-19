package messaging

import (
	"context"
	"errors"
)

// payment_access.go is the payment fence's consumer side: the two facts the targeted payment notices
// (and the money rooms) need about a person before a payment's amount, customer and order reach them.
//
//   - the payment trail: the same reads ruling 87 holds to money desk WORK. The composition root binds
//     the role gate's own admission to those routes (cmd/core paymentTrailReader), so the answer is
//     the gate's and moves with the document; messaging names no role and no level for it;
//   - the branch reach: the branches the person is granted, or every branch for a global person, read
//     from the same identity facts SyncMembership qualifies seats with.
//
// Unbound is closed: with no PaymentAccess bound nobody holds the trail, so a targeted notice goes to
// no one, the same posture as billing.LatePaymentTrail.

// PaymentAccess says whether a person holds the payment trail reads. It is declared here, by its
// consumer, and billing.PaymentTrailReader satisfies it without messaging importing billing.
type PaymentAccess interface {
	HoldsPaymentTrail(ctx context.Context, platformUserID, orgID string) (bool, error)
}

// PaymentRecipient is what the fence needs to know about one staffer: the platform identity the role
// gate answers for, the orgs they hold a grant in, and the branches they reach.
type PaymentRecipient struct {
	PlatformUserID string
	OrgIDs         []string
	Global         bool
	BranchIDs      []string
}

// paymentRecipientStore is the persistence seam for PaymentRecipient, discovered by type assertion off
// the base store like every other seam in this module.
type paymentRecipientStore interface {
	// PaymentRecipient reads one staffer's platform identity, grant orgs and branch reach. A staffer
	// with no platform identity returns the zero value: the gate cannot answer for them.
	PaymentRecipient(ctx context.Context, userID string) (PaymentRecipient, error)
}

// WithPaymentAccess binds the payment trail reader. Unbound keeps every payment notice withheld.
func (s *Service) WithPaymentAccess(a PaymentAccess) *Service { s.payAccess = a; return s }

// recipientHoldsTrail asks the bound reader whether the staffer holds the payment trail in any org they
// are granted in. Unbound, no identity, no org and an unreadable answer are all no: the fence fails
// closed. The error is returned beside the no so a caller can say why.
func (s *Service) recipientHoldsTrail(ctx context.Context, rec PaymentRecipient) (bool, error) {
	if s.payAccess == nil || rec.PlatformUserID == "" {
		return false, nil
	}
	for _, org := range rec.OrgIDs {
		held, err := s.payAccess.HoldsPaymentTrail(ctx, rec.PlatformUserID, org)
		if err != nil {
			return false, err
		}
		if held {
			return true, nil
		}
	}
	return false, nil
}

// reachesAny reports whether the staffer's branch reach covers at least one of the branches. A global
// reach covers all of them, including none. A branch scoped reach covers nothing when no branch is
// known: a payment nobody can place is not a branch scoped staffer's to hear.
func (rec PaymentRecipient) reachesAny(branches []string) bool {
	if rec.Global {
		return true
	}
	for _, b := range branches {
		if b == "" {
			continue
		}
		for _, mine := range rec.BranchIDs {
			if mine == b {
				return true
			}
		}
	}
	return false
}

func (s *Service) paymentRecipients() (paymentRecipientStore, bool) {
	ps, ok := s.repo.(paymentRecipientStore)
	return ps, ok
}

// MayHearPayment says whether a staffer may be told about a payment: they hold the payment trail and
// their reach covers the branch the money was taken at or the branch that owns the order (the cross
// branch rule exists for the order's end). An error is returned beside the no.
func (s *Service) mayHearPayment(ctx context.Context, userID string, f PaymentFacts) (bool, error) {
	ps, ok := s.paymentRecipients()
	if !ok {
		return false, nil
	}
	rec, err := ps.PaymentRecipient(ctx, userID)
	if err != nil {
		return false, err
	}
	if !rec.reachesAny([]string{f.TakenBranchID, f.OrderBranchID}) {
		return false, nil
	}
	return s.recipientHoldsTrail(ctx, rec)
}

// RoomAdmits says whether a seated user may read a room. It is true for every room but a money room
// (a room carrying a member_roles list: #payments, #payments-<branch>, #finance). A money room follows
// the payment trail reader and, when it names a branch, the branch reach, whatever the seat table says:
// the seat list was pinned to the trail roles when it was written and the reader is what moves.
// Any doubt closes the room.
func (s *Service) RoomAdmits(ctx context.Context, userID, channelID string) bool {
	facts, err := s.repo.channelFacts(ctx, channelID)
	if err != nil {
		// An unknown room has nothing to fence; any other read failure on a room we cannot classify
		// closes it.
		return errors.Is(err, errNotFound)
	}
	if len(facts.MemberRoles) == 0 {
		return true
	}
	ps, ok := s.paymentRecipients()
	if !ok {
		return false
	}
	rec, err := ps.PaymentRecipient(ctx, userID)
	if err != nil {
		return false
	}
	if facts.BranchID != "" && !rec.reachesAny([]string{facts.BranchID}) {
		return false
	}
	held, err := s.recipientHoldsTrail(ctx, rec)
	return err == nil && held
}

// admits is RoomAdmits with a per-call memo, for the list reads that meet one room many times.
func (s *Service) admits(ctx context.Context, memo map[string]bool, userID, channelID string) bool {
	if v, ok := memo[channelID]; ok {
		return v
	}
	v := s.RoomAdmits(ctx, userID, channelID)
	memo[channelID] = v
	return v
}
