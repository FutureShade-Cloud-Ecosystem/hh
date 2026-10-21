package customer

// account_position.go, the dealer's account position policy on the staff customer reads (UX-97).
//
// The credit limit and the open balance are the account position. The dealer decides per branch who
// sees it (ar_invoice_policy.account_position_visibility: MANAGER_UP, ALL_STAFF or HIDDEN), and the
// receivables register already enforces that by byte absence. The two staff reads in this module,
// the picker's search and the by-id read, carried the figures to every staff caller regardless, so
// a clerk the register refuses could read the same numbers one screen over.
//
// The decision is billing's (billing.AccountPositionGate, the same function the register calls), and
// this module reaches it through the PositionGate seam because billing already imports this module.
// UNBOUND IS REFUSED: a Service wired without a gate withholds the figures from every staff caller,
// the same fail-closed posture the gate itself takes on an unreadable policy.
//
// The refusal is byte absence at the marshal layer, never 0: the wire types below carry the figures
// as omitted-when-nil pointers, so a refused response has no key for the figure at all and a client
// cannot mistake "not allowed to see" for "owes nothing".

import "context"

// PositionGate answers whether the staff caller on the context may receive the account position of
// an account homed to branchID ("" asks about the dealer's default policy).
type PositionGate interface {
	AccountPositionAllowed(ctx context.Context, branchID string) bool
}

// WithPositionGate binds the account position policy. Returns the Service for chaining.
func (s *Service) WithPositionGate(g PositionGate) *Service { s.position = g; return s }

// PositionAllowed asks the gate; false when none is bound.
func (s *Service) PositionAllowed(ctx context.Context, branchID string) bool {
	if s.position == nil {
		return false
	}
	return s.position.AccountPositionAllowed(ctx, branchID)
}

// SearchResultWire is the staff search row on the wire. The two position figures are pointers that
// are nil, and so absent, when the caller is refused the account position.
type SearchResultWire struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	AccountNo        string `json:"account_no"`
	CustomerType     string `json:"customer_type"`
	PaymentTermsDays int    `json:"payment_terms_days"`
	CreditLimitCents *int64 `json:"credit_limit_cents,omitempty"`
	OpenBalanceCents *int64 `json:"open_balance_cents,omitempty"`
	// AvailableCreditCents is the core's own headroom (limit minus open, never below zero), so no
	// client subtracts it. Absent with the other two.
	AvailableCreditCents *int64 `json:"available_credit_cents,omitempty"`
}

// AccountWire is the by-id read on the wire. It embeds Account, whose two position fields are
// shadowed by the shallower pointer fields below (encoding/json takes the shallowest name), so the
// figures are absent from the response unless the gate admits the caller.
type AccountWire struct {
	Account
	CreditLimitCents *int64 `json:"credit_limit_cents,omitempty"`
	BalanceCents     *int64 `json:"balance_cents,omitempty"`
	// AvailableCreditCents is the core's own headroom (limit minus balance, never below zero), so no
	// client subtracts it. Absent with the other two.
	AvailableCreditCents *int64 `json:"available_credit_cents,omitempty"`
}

// availableCredit is limit minus open, floored at zero: the same arithmetic as the register's
// account position card (billing.AccountPosition), so every screen quotes one figure.
func availableCredit(limit, open int64) int64 {
	if a := limit - open; a > 0 {
		return a
	}
	return 0
}

// SearchForWire shapes search rows under the policy. branchID is the branch the request names: the
// search carries no per account home branch, so the branch the caller acts for decides.
func (s *Service) SearchForWire(ctx context.Context, rows []SearchResult, branchID string) []SearchResultWire {
	allowed := s.PositionAllowed(ctx, branchID)
	out := make([]SearchResultWire, 0, len(rows))
	for _, r := range rows {
		w := SearchResultWire{ID: r.ID, Name: r.Name, AccountNo: r.AccountNo,
			CustomerType: r.CustomerType, PaymentTermsDays: r.PaymentTermsDays}
		if allowed {
			limit, open := r.CreditLimitCents, r.OpenBalanceCents
			avail := availableCredit(limit, open)
			w.CreditLimitCents, w.OpenBalanceCents, w.AvailableCreditCents = &limit, &open, &avail
		}
		out = append(out, w)
	}
	return out
}

// AccountForWire shapes the by-id read under the policy of the account's own home branch.
func (s *Service) AccountForWire(ctx context.Context, a Account) AccountWire {
	w := AccountWire{Account: a}
	if s.PositionAllowed(ctx, a.BranchID) {
		limit, bal := a.CreditLimitCents, a.BalanceCents
		avail := availableCredit(limit, bal)
		w.CreditLimitCents, w.BalanceCents, w.AvailableCreditCents = &limit, &bal, &avail
	}
	return w
}
