// Package customer is the ERP customer/account read service (F0.1). It resolves a
// portal tenant (customers.id == portal_users.customer_id) to the account facts the
// BFF gates and prices on, type, home branch, credit, hold state, balance, and
// exposes the account's contacts (the one-way advisory seed for the first portal
// admin, §4.6.2). Module boundary: this module is the sole reader of customers +
// customer_contacts (migration 0006); it never writes them and never reaches into
// another module's tables. Available-credit/AR balance is COMPOSED with the billing
// module by the BFF, this module owns only the customers/contacts facts.
package customer

import "time"

// Account is the resolved view of a customers row the portal BFF needs: enough to gate
// (OnHold, IsActive), price (CustomerType, BranchID), and surface available credit
// (CreditLimitCents − BalanceCents). Read-only projection; the portal never mutates it.
type Account struct {
	CustomerID       string `json:"customer_id"`
	AccountNo        string `json:"account_no"`
	Name             string `json:"name"`
	CustomerType     string `json:"customer_type"` // CONTRACTOR|RETAIL|BUILDER|GOVERNMENT|INTERNAL
	BranchID         string `json:"branch_id"`     // home_branch_id ("" when unset)
	PaymentTermsDays int    `json:"payment_terms_days"`
	CreditLimitCents int64  `json:"credit_limit_cents"`
	OnHold           bool   `json:"on_hold"`
	TaxExempt        bool   `json:"tax_exempt"`
	// BalanceCents is the account's current AR balance. It is NOT a customers column -
	// it derives from the latest customer_transactions.balance_after (0009), which the
	// billing module owns. The resolver leaves it 0 unless the BFF composes it in.
	BalanceCents int64 `json:"balance_cents"`
	IsActive     bool  `json:"is_active"`
}

// SearchResult is the counter-facing projection the POS customer picker needs: enough
// to identify the account (Name, AccountNo, CustomerType) and show its credit posture
// (PaymentTermsDays, CreditLimitCents, OpenBalanceCents → Available = limit − open).
// OpenBalanceCents is the account's current AR, the latest customer_transactions
// balance_after (0009), the same running-balance basis the POS credit gate reads.
type SearchResult struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	AccountNo        string `json:"account_no"`
	CustomerType     string `json:"customer_type"`
	PaymentTermsDays int    `json:"payment_terms_days"`
	CreditLimitCents int64  `json:"credit_limit_cents"`
	OpenBalanceCents int64  `json:"open_balance_cents"`
}

// Contact is a customer_contacts row. IsPrimary marks the contact the onboarding
// bootstrap turns into the first Owner invite (one-way advisory seed, §4.6.2). Note
// customer_contacts.email is nullable and non-unique, so contacts can never be a live
// mirror of portal_users (whose email is globally unique).
type Contact struct {
	ID         string    `json:"id"`
	CustomerID string    `json:"customer_id"`
	Name       string    `json:"name"`
	Email      string    `json:"email"`
	Phone      string    `json:"phone"`
	Role       string    `json:"role"`
	IsPrimary  bool      `json:"is_primary"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
