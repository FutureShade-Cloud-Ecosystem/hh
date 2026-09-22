package pricing

// override_authority.go, DIB-420. The client's `has_approved_override` is a REQUEST. The server decides.
//
// - WHAT WAS WRONG -
// `POST /api/v1/pricing/resolve` is behind readGuard (any ACTIVE staff, Sales+). Its body carried
// `has_approved_override`, which went straight into ResolveInput and straight into the A-20 margin gate,
// whose first line is `if hasApprovedOverride { return nil }`. A junior salesperson could clear the
// margin floor by typing one boolean into a request body. The contrast was the tell: identity's OWN
// handler derives the same fact from the CALLER (identity/handler.go:263), never from the body.
//
// - THE RULING -
// The flag no longer *is* the authority; it is the request for one. When a price lands below the floor
// AND the caller asked for an override, the server looks the authority up for ITSELF, from the caller
// identity the auth guard resolved (ResolveInput.UserID, which is stamped by staffUserID(r), the
// spoofable X-User-Id header was removed in C-1). Two sources are accepted, in this order:
//
//	APPROVAL      , an APPROVED margin override on the DIB-360 approvals substrate for this
//	                 requester + branch. This is the primary source: it is the specific, dated,
//	                 per-branch artefact somebody with authority created, and it is what the
//	                 request→approve→proceed loop was built to produce.
//	MANAGER_SCOPE , the caller is a Tier-1/Tier-2 role whose scope covers the branch being priced.
//	                 Accepted because a manager IS the approver: requiring one to file an approval
//	                 request with themselves is a loop that gets worked around at the counter, and a
//	                 control people route around is worse than one with an honest, recorded door.
//
// Anything else, including a caller with a manager role but no scope over THAT branch, is refused,
// and the refusal says the override was requested and not honoured rather than pretending it was never
// asked for. Silence here is how a salesperson learns to keep clicking.
//
// - FAIL CLOSED -
// No authority wired (the resolver-only fakes, an incompletely wired deployment) and a lookup that
// ERRORS both refuse. The alternative, treating "I could not check" as "it is fine", makes an
// approvals outage into a pricing hole, and an approvals outage is exactly when somebody is most
// motivated to try. The cost of failing closed is that a genuine override waits for a manager on the
// floor, which is what happened before the approvals queue existed at all.
//
// - WHY A SEAM AND NOT A CALL -
// internal/pricing does not import internal/identity for this. It declares what it needs; the
// composition root adapts identity.Service + the caller's StaffContext to it. Same shape as
// tasks.OrderReader, messagingprovider.ContactSource and purchasing.StockReceiver.

import (
	"context"
)

// Override authority sources. These are recorded on the Breakdown and on the audit row, so
// "how many below-floor prices went out last month, and on whose authority" is a GROUP BY.
const (
	// AuthorityApproval, an APPROVED margin override exists on the approvals substrate.
	AuthorityApproval = "APPROVAL"
	// AuthorityManagerScope, the caller holds manager scope over the branch being priced.
	AuthorityManagerScope = "MANAGER_SCOPE"
)

// OverrideGrant is the server's answer to "may this caller price below the floor here?".
// Source is one of the constants above when Granted; empty otherwise.
type OverrideGrant struct {
	Granted bool
	Source  string
}

// OverrideAuthority is the consumer-declared seam over the server-side authority check.
//
// SEAM CONTRACT:
//   - userID is the caller resolved by the auth guard. An implementation MUST NOT accept a
//     caller id from anywhere else, and MUST NOT treat an empty userID as authorised.
//   - branchID is the branch being priced. An implementation MUST confirm the caller's authority
//     covers THAT branch; "is a manager somewhere" is not authority here.
//   - An error means "could not determine", and the resolver refuses. It never means "yes".
type OverrideAuthority interface {
	MarginOverrideAuthority(ctx context.Context, userID, branchID string) (OverrideGrant, error)
}

// WithOverrideAuthority wires the DIB-420 server-side authority. Nil (the default) means NO override
// request is ever honoured, fail closed, see the header.
func (s *Service) WithOverrideAuthority(a OverrideAuthority) *Service { s.authority = a; return s }

// overrideGrant resolves the caller's authority for one below-floor resolve. It is only reached when
// the price is ALREADY below the floor and the caller asked for an override, so the lookup costs
// nothing on the overwhelming majority of resolves that price above the floor.
func (s *Service) overrideGrant(ctx context.Context, in ResolveInput) OverrideGrant {
	if !in.OverrideRequested {
		return OverrideGrant{}
	}
	if s.authority == nil {
		// Unwired. The request is refused, loudly in the log, silently to the caller (a caller who
		// learns WHY they were refused learns which lever to pull).
		if s.log != nil {
			s.log.Warn("pricing: margin override requested but no authority is wired, refused (DIB-420)",
				"user_id", in.UserID, "branch_id", in.BranchID)
		}
		return OverrideGrant{}
	}
	g, err := s.authority.MarginOverrideAuthority(ctx, in.UserID, in.BranchID)
	if err != nil {
		if s.log != nil {
			s.log.Error("pricing: margin override authority lookup failed, refused (DIB-420)",
				"user_id", in.UserID, "branch_id", in.BranchID, "err", err)
		}
		return OverrideGrant{}
	}
	if !g.Granted {
		return OverrideGrant{}
	}
	// A grant with no named source is not a grant. An adapter that returns {true, ""} is telling us
	// it does not know why it said yes, and an unattributable below-floor price is the thing this
	// whole file exists to prevent.
	if g.Source != AuthorityApproval && g.Source != AuthorityManagerScope {
		if s.log != nil {
			s.log.Error("pricing: override authority returned an unnamed source, refused (DIB-420)",
				"source", g.Source, "user_id", in.UserID)
		}
		return OverrideGrant{}
	}
	return g
}
