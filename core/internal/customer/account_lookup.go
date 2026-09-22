package customer

// account_lookup.go, resolve ONE account from what a person typed, inside the caller's reach.
//
// WHY NOT SearchAccounts. The picker's search answers "show me the accounts matching these two
// letters" and returns a LIST, fenced to ONE branch the caller named (it is a POS surface, and the
// POS always knows which till it is standing at). A lookup is the other shape: a person says a name
// or an account number and expects ONE account back, and there is no till, the fence has to be the
// caller's whole set of grants, not a branch they were asked to nominate.
//
// THE FENCE IS SEARCH'S OWN RULE, GENERALISED FROM ONE BRANCH TO A SET, and nothing more:
//
//	an account homed to a branch the caller can reach   → visible
//	an account with NO home branch                      → visible (company-wide by construction;
//	                                                       the WALK-IN house account is the canonical
//	                                                       one, and hiding these would break every
//	                                                       branch for the accounts they all share)
//	anything else                                       → not found
//
// It is stated as "not found" rather than "forbidden" deliberately, and that differs from
// SearchAccounts on purpose. A search returns a list, so "no results" and "not allowed" are
// genuinely different answers and the second must not hide behind the first (search_scope.go says
// exactly this). A LOOKUP returns one thing or nothing, and telling a Trenton user that a Kingston
// account exists but is not theirs hands over the fact the fence was built to withhold, the same
// argument internal/order makes for a cross-branch order number.
//
// THE SAME EXCLUSIONS AS THE PICKER. The walk-in house account is filtered out here as it is there:
// it is not a chargeable account, it has no meaningful balance, and an assistant reporting one would
// be answering a question about a placeholder.

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// AccountReach is a caller's branch authority projected onto a read: every branch, or a set of
// granted ones. It is the multi-branch form of SearchScope, and like it, it is built from the
// guard-resolved caller and passed IN, a read must not be able to re-derive an authority the
// request guard already decided.
//
// Global false with no BranchIDs means "no branch grants at all". That resolves to the company-wide
// (unhomed) accounts only, and never to everything.
type AccountReach struct {
	Global    bool
	BranchIDs []string
}

// branchFilter renders the reach for the `($2::uuid[] IS NULL OR …)` predicate. nil → SQL NULL →
// no fence. An empty (non-nil) array is NOT nil: `= ANY('{}')` is false for every row, which leaves
// only the IS NULL arm, the unhomed accounts, which is the correct answer for a caller with no
// grants, and is the opposite of what passing nil would do.
func (r AccountReach) branchFilter() any {
	if r.Global {
		return nil
	}
	if r.BranchIDs == nil {
		return []string{}
	}
	return r.BranchIDs
}

// ErrLookupNotConfigured marks a store that cannot fence in SQL. It is returned rather than falling
// back to an unfenced read for the reason SearchAccounts gives: SearchResult carries no home branch,
// so a service-side filter has nothing to filter on, and "best effort" would mean leaking.
var ErrLookupNotConfigured = errors.New("customer: scoped account lookup not configured")

// accountLookupStore is the optional persistence seam for this read, a separate interface reached
// by type assertion, exactly as scopedAccountSearcher is, and for the same reason: accountStore is
// implemented outside this package and must not be widened.
type accountLookupStore interface {
	LookupAccountScoped(ctx context.Context, ref string, reach AccountReach) (SearchResult, bool, error)
}

// LookupAccount resolves one account from a reference, within the caller's reach.
//
// found=false is an ANSWER: no account by that name or number that this caller can see. An error
// means something failed.
func (s *Service) LookupAccount(ctx context.Context, reach AccountReach, ref string) (SearchResult, bool, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return SearchResult{}, false, nil
	}
	ls, ok := s.store.(accountLookupStore)
	if !ok {
		return SearchResult{}, false, ErrLookupNotConfigured
	}
	return ls.LookupAccountScoped(ctx, ref, reach)
}

// LookupAccountScoped is the pgx read behind LookupAccount.
//
// THE ORDER OF THE ATTEMPTS IS THE DESIGN, most specific first:
//
//  1. an exact account number, CITEXT, so case is already irrelevant. This is what is printed on
//     an invoice and what a person reading one out loud will say.
//  2. a name prefix, then a name substring, "northgate" and then "landscaping", ordered by name so
//     the same question gets the same answer on every call. A bare LIMIT 1 with no ORDER BY is a
//     different account on different days.
//
// The fence is applied to EVERY attempt, not just the fuzzy one: an exact account number belonging
// to another branch must not resolve, or the account-number space becomes the existence oracle the
// fence was built to close.
func (r *Repository) LookupAccountScoped(ctx context.Context, ref string, reach AccountReach) (SearchResult, bool, error) {
	if r.pool == nil {
		return SearchResult{}, false, errNoDB
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return SearchResult{}, false, nil
	}
	// The projection and the exclusions are the picker's, so the two surfaces cannot disagree about
	// what an account IS or about which accounts are chargeable.
	const cols = `c.id::text, c.name, c.account_no::text, c.customer_type,
	              c.payment_terms_days, c.credit_limit_cents,
	              COALESCE((SELECT t.balance_after FROM customer_transactions t
	                         WHERE t.customer_id = c.id ORDER BY t.occurred_at DESC LIMIT 1), 0)`
	const fence = `c.is_active
	           AND c.account_no <> 'WALK-IN'
	           AND c.name NOT ILIKE 'walk-in%'
	           AND ($2::uuid[] IS NULL OR c.home_branch_id = ANY($2::uuid[]) OR c.home_branch_id IS NULL)`

	scan := func(sql string) (SearchResult, bool, error) {
		var s SearchResult
		err := r.pool.QueryRow(ctx, sql, ref, reach.branchFilter()).Scan(
			&s.ID, &s.Name, &s.AccountNo, &s.CustomerType,
			&s.PaymentTermsDays, &s.CreditLimitCents, &s.OpenBalanceCents)
		switch {
		case err == nil:
			return s, true, nil
		case errors.Is(err, pgx.ErrNoRows):
			return SearchResult{}, false, nil
		default:
			return SearchResult{}, false, err
		}
	}

	if s, ok, err := scan(`SELECT ` + cols + ` FROM customers c
	                        WHERE ` + fence + ` AND c.account_no = $1::citext`); ok || err != nil {
		return s, ok, err
	}
	if s, ok, err := scan(`SELECT ` + cols + ` FROM customers c
	                        WHERE ` + fence + ` AND c.name ILIKE $1 || '%'
	                        ORDER BY c.name LIMIT 1`); ok || err != nil {
		return s, ok, err
	}
	return scan(`SELECT ` + cols + ` FROM customers c
	              WHERE ` + fence + ` AND c.name ILIKE '%' || $1 || '%'
	              ORDER BY c.name LIMIT 1`)
}
