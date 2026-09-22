package assist

// limits.go, the monthly budget resolver, and the boundary that enforces it.
//
// THE RESOLUTION RULE, IN ONE FUNCTION (Resolve), because a rule implemented twice is a rule that
// will eventually be two rules:
//
//	a per-user override, if one exists      → that number, source "user"
//	otherwise the role default, if one      → that number, source "role"
//	otherwise the code default              → that number, source "default"
//
// WHOLE-OR-ABSENT. A user override REPLACES the role budget entirely; there is no merge, no
// "inherit the role's number and add", no partial override. This is the same rule migration 0140
// established for dashboard layouts and it is worth restating why it matters here: the admin screen
// has to render one honest sentence per person ("500,000, their own" / "300,000, from
// SALES_MANAGER"), and reset-to-role has to be a DELETE that cannot leave a fragment behind. Both
// follow directly from whole-or-absent and neither is achievable with a merge.
//
// SOURCE IS RETURNED, NOT INFERRED. The admin surface must never work out where a number came from
// by comparing it to the role default, two people can legitimately have an override equal to their
// role's number, and inferring would tell them it was inherited and then silently change it when
// the role default moved.
//
// THE BOUNDARY IS `spent >= budget`, AND THAT CHOICE IS DELIBERATE. A budget of 100,000 means you
// may spend up to 100,000; once you have, you are done. The alternative (`>`) lets a user at
// exactly their limit start one more exchange, which reads as an off-by-one to anyone reconciling
// the ledger against the budget and is impossible to explain in a sentence. TestBudget_Boundary
// pins it, and flipping the operator kills that test by name (mutation M2 in NOTES-FIX-431.md).

import (
	"context"
	"time"
)

// DefaultBudget is the monthly token allowance for a role with no row in assist_role_limits.
//
// It is the SMALL number on purpose. A role added by a later migration must not silently receive
// the largest allowance in the table, and it must not silently receive zero either, zero would
// switch the assistant off for a group of people with no administrator having decided that. A small
// working allowance is the only default that is wrong in a way somebody notices and can fix.
const DefaultBudget = 60000

// Budget is a resolved allowance and where it came from.
type Budget struct {
	Tokens int    `json:"tokens"`
	Source string `json:"source"` // "user" | "role" | "default"
}

// Budget sources, as strings on the wire so the admin surface renders them without a mapping table.
const (
	SourceUser    = "user"
	SourceRole    = "role"
	SourceDefault = "default"
)

// Resolve applies the whole-or-absent rule. The two pointers are "an override exists" and "a role
// default exists"; nil means absent. Pointers rather than (int, bool) pairs because the store reads
// them from nullable rows and a zero budget is a LEGAL, meaningful value ("no AI for this role") -
// with (int, bool) the two would be one keystroke apart at every call site.
func Resolve(userOverride, roleDefault *int) Budget {
	if userOverride != nil {
		return Budget{Tokens: *userOverride, Source: SourceUser}
	}
	if roleDefault != nil {
		return Budget{Tokens: *roleDefault, Source: SourceRole}
	}
	return Budget{Tokens: DefaultBudget, Source: SourceDefault}
}

// Spend is what a person has used in a period: the token total AND the number of exchanges.
//
// EXCHANGES ARE COUNTED SEPARATELY AND THAT IS THE POINT OF THE PILOT. The default provider is the
// stub, which costs zero tokens, so a spend report that only summed tokens would read as "nobody
// used it" for the entire pilot. Exchanges answer the question the client actually asked, is
// anyone using this, and tokens answer the one they will ask next.
type Spend struct {
	Exchanges int `json:"exchanges"`
	TokensIn  int `json:"tokens_in"`
	TokensOut int `json:"tokens_out"`
}

// Total is the number compared against the budget: everything the exchange cost, both directions.
//
// Input tokens are counted because they are billed. A budget that only counted output would let a
// user paste a novel into the chat for free, which is both the cheapest way to run up a bill and
// the easiest one to do by accident.
func (s Spend) Total() int { return s.TokensIn + s.TokensOut }

// UserSpend is one person's row on the admin surface.
type UserSpend struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	RoleCode    string `json:"role_code"`
	Spend
	Budget Budget `json:"budget"`
}

// Remaining is what is left of the allowance, floored at zero. Negative remaining is possible in
// the data (one exchange can cross the line) and is reported as zero: "you are 400 tokens over" is
// not a number anyone acts on, and it invites the reading that the overage is owed.
func (u UserSpend) Remaining() int {
	if r := u.Budget.Tokens - u.Total(); r > 0 {
		return r
	}
	return 0
}

// OverBudget is the enforcement predicate, in one place, used by both the service and the surfaces
// so a user is never told they have room and then refused.
func OverBudget(spent Spend, b Budget) bool { return spent.Total() >= b.Tokens }

// MonthWindow returns the half-open [start, end) of the calendar month containing t, in t's own
// location.
//
// A CALENDAR MONTH, NOT A ROLLING 30 DAYS. Budgets are a thing an administrator explains to a
// person, and "it resets on the 1st" is explainable while "you have used 87% of a window that ends
// next Tuesday" is not. Half-open so an exchange at 23:59:59.999 on the last day belongs to the
// month it happened in and to exactly one month.
func MonthWindow(t time.Time) (time.Time, time.Time) {
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	return start, start.AddDate(0, 1, 0)
}

// LimitStore reads and writes the two budget tables (migration 0155).
//
// The reads return pointers so "absent" survives the trip: see Resolve. SetUserBudget/
// ClearUserBudget are the whole of the override lifecycle, there is no partial update, because
// there is no partial override.
type LimitStore interface {
	RoleBudget(ctx context.Context, roleCode string) (*int, error)
	UserBudget(ctx context.Context, userID string) (*int, error)
	RoleBudgets(ctx context.Context) (map[string]int, error)
	UserBudgets(ctx context.Context) (map[string]int, error)
	SetRoleBudget(ctx context.Context, roleCode string, tokens int, actorID string) error
	SetUserBudget(ctx context.Context, userID string, tokens int, actorID string) error
	ClearUserBudget(ctx context.Context, userID string) error
}

// BudgetFor resolves one caller's allowance from a store.
func BudgetFor(ctx context.Context, st LimitStore, c Caller) (Budget, error) {
	if st == nil {
		return Budget{Tokens: DefaultBudget, Source: SourceDefault}, nil
	}
	uo, err := st.UserBudget(ctx, c.UserID)
	if err != nil {
		return Budget{}, err
	}
	rd, err := st.RoleBudget(ctx, c.RoleCode)
	if err != nil {
		return Budget{}, err
	}
	return Resolve(uo, rd), nil
}
