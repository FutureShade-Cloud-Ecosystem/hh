package adapters

// customer.go, assist.CustomerReader over TWO modules: the account lookup lives in
// internal/customer, the money lives in internal/billing.
//
// THAT SPLIT IS THE HONEST SHAPE OF THE DOMAIN, not an accident of wiring. "Which account is this?"
// is a customer-master question; "what does it owe, and what has moved on it?" is an AR question,
// and the AR console owns the arithmetic. Forcing both behind one service would have meant either
// customer importing billing or a third copy of the aging buckets. The seam assist declares
// (ResolveCustomer + Balance + RecentActivity) is written in the language of the QUESTION, so it is
// free to be answered by two modules, and this adapter is the only place that knows it is.
//
// THE FENCE IS THE RESOLVE, AND IT IS THE APP'S OWN FENCE. Which accounts a caller may ask about is
// decided by customer.LookupAccount, using the rule the POS picker already enforces (an account
// homed to a branch you can reach, or an account with no home branch). The money reads then take the
// customer id the resolve returned, they are not separately fenced, because a second, different
// fence on the same question is how an app ends up with two answers to it. Balance and
// RecentActivity are therefore UNREACHABLE without a successful resolve: the command layer calls
// ResolveCustomer first and stops on found=false, and this adapter exposes no other way in.

import (
	"context"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// AccountLookup is what this adapter needs from internal/customer.
type AccountLookup interface {
	LookupAccount(ctx context.Context, reach customer.AccountReach, ref string) (customer.SearchResult, bool, error)
}

// ARReader is what this adapter needs from internal/billing, the AR console's own reads.
type ARReader interface {
	CustomerAging(ctx context.Context, customerID string) (billing.AgingRow, time.Time, bool, error)
	CustomerActivity(ctx context.Context, customerID string, limit int) ([]billing.ARActivity, error)
}

type customerAdapter struct {
	accounts AccountLookup
	ar       ARReader
}

var _ assist.CustomerReader = customerAdapter{}

// NewCustomerReader builds the seam. EITHER dependency missing returns a nil reader, because a
// half-wired customer seam is worse than an unwired one: resolve-without-money would answer
// "customer.balance" with a zero balance for every account, which is a wrong number rather than an
// honest refusal. Both commands behind this seam answer 503 until both halves are present.
func NewCustomerReader(accounts AccountLookup, ar ARReader) assist.CustomerReader {
	if accounts == nil || ar == nil {
		return nil
	}
	return customerAdapter{accounts: accounts, ar: ar}
}

// ResolveCustomer names the account, fenced to what this caller may see.
func (a customerAdapter) ResolveCustomer(ctx context.Context, c assist.Caller, ref string) (assist.CustomerRecord, bool, error) {
	acct, found, err := a.accounts.LookupAccount(ctx, accountReach(c), ref)
	if err != nil {
		return assist.CustomerRecord{}, false, err
	}
	if !found {
		return assist.CustomerRecord{}, false, nil
	}
	return assist.CustomerRecord{CustomerID: acct.ID, Name: acct.Name, AccountNo: acct.AccountNo}, true, nil
}

// Balance is the account's AR position, as of the instant BILLING computed it.
//
// found=false from CustomerAging means the account carries no open balance, an account in perfect
// standing is not in the aging worklist at all. That is a real, good answer and it maps to a ZEROED
// BalanceRecord rather than to an error or an empty state: the command says "… has nothing
// outstanding", which is what a person asking wants to hear.
//
// AsOf comes back from billing rather than from a time.Now() here, so the figure and the instant it
// was aged against come from ONE clock. Stamping a second clock's time onto another's number is how
// an aging answer ends up dated a moment it was not computed for.
func (a customerAdapter) Balance(ctx context.Context, c assist.Caller, customerID string) (assist.BalanceRecord, error) {
	row, asOf, found, err := a.ar.CustomerAging(ctx, customerID)
	if err != nil {
		return assist.BalanceRecord{}, err
	}
	if !found {
		return assist.BalanceRecord{AsOf: asOf}, nil
	}
	// Both numbers are billing's own accessors over billing's own buckets. Nothing is added up here.
	return assist.BalanceRecord{
		OpenCents:    row.OpenCents(),
		OverdueCents: row.OverdueCents(),
		AsOf:         asOf,
	}, nil
}

// RecentActivity is the account's AR ledger, newest first.
//
// An account with nothing on it returns an empty slice and no error, a new account that has never
// been invoiced is an ordinary state.
func (a customerAdapter) RecentActivity(ctx context.Context, c assist.Caller, customerID string, limit int) ([]assist.ActivityRecord, error) {
	rows, err := a.ar.CustomerActivity(ctx, customerID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]assist.ActivityRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, assist.ActivityRecord{
			Kind:      activityKind(r.Kind),
			Reference: r.Reference,
			When:      r.OccurredAt,
			Cents:     r.AmountCents,
		})
	}
	return out, nil
}

// activityKind turns the customer_transactions.txn_type enum into the word a person would use.
//
// Translation belongs HERE and not in billing: the ledger's vocabulary is the ledger's, and a
// module should not have to know that one of its consumers renders to a chat window. An unrecognised
// type is passed through verbatim rather than mapped to "Other", a new txn_type added by a later
// migration should show up as itself in an answer, not be silently swallowed into a bucket that
// makes it look like the assistant understood it.
func activityKind(txnType string) string {
	switch strings.ToUpper(strings.TrimSpace(txnType)) {
	case "INVOICE":
		return "Invoice"
	case "PAYMENT":
		return "Payment"
	case "CREDIT_MEMO":
		return "Credit memo"
	case "ADJUSTMENT":
		return "Adjustment"
	case "NSF":
		return "NSF reversal"
	case "WRITE_OFF":
		return "Write-off"
	default:
		return txnType
	}
}
