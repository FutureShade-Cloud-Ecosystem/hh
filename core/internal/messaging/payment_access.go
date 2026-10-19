package messaging

import "context"

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

// RoomAdmits says whether a seated user may read a room. It is true for every room but a money room
// (a room carrying a member_roles list). Skeleton: the money room fence lands with the fix.
func (s *Service) RoomAdmits(ctx context.Context, userID, channelID string) bool { return true }
