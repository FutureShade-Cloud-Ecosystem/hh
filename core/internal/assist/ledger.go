package assist

// ledger.go, the append-only record of every AI exchange.
//
// ONE ROW PER EXCHANGE, WRITTEN AFTER THE PROVIDER ANSWERS, ALWAYS. Including the stub's zeros
// (governance 1: "the stub records zeros but still writes rows"), including an exchange that ended
// in a refusal from the provider, and including one whose answer the user never read because they
// closed the tab. What is being counted is USE, and all four of those are use.
//
// WHAT IS NOT COUNTED: an over-budget refusal. Nothing was sent anywhere and nothing was spent, so
// writing a row would inflate the exchange count with the app's own error messages, and, worse,
// would make the ledger's exchange count disagree with the ledger's token sum in a way that looks
// like a bug in the accounting. A refusal is visible in the app's logs; it is not a transaction.
//
// THE ROLE CODE IS COPIED ONTO THE ROW. Budgets are per role, so a person's August spend must stay
// bucketed under the role they held in August even after a September promotion. The alternative -
// joining to user_roles at report time, silently rewrites history the day anyone changes job.
//
// WRITE FAILURES DO NOT SWALLOW THE ANSWER. If the ledger write fails, the user still gets their
// answer and the failure is logged loudly (service.go). The opposite policy, refuse the answer
// because the accounting failed, makes an availability problem out of a bookkeeping one, and the
// missing row is recoverable from the provider's own billing while a broken assistant is not.

import (
	"context"
	"time"
)

// Entry is one exchange, as written. There is no Update and no Delete anywhere in this package or
// in the database (migration 0154 refuses both with a trigger).
type Entry struct {
	UserID    string
	RoleCode  string
	Provider  string
	Model     string
	TokensIn  int
	TokensOut int
	Kind      string // "chat" today; named so a later consumer lands in the same ledger
	At        time.Time
}

// LedgerStore appends and sums. The interface has no mutating verb other than Append, which is the
// interface-level statement of the same property the trigger enforces in SQL: there is nothing to
// call that could edit history, so no code can be written that does.
type LedgerStore interface {
	Append(ctx context.Context, e Entry) error
	// SpendFor is one user's usage in [from, to). Half-open, matching MonthWindow.
	SpendFor(ctx context.Context, userID string, from, to time.Time) (Spend, error)
	// SpendAll is everyone's usage in the window, for the admin surface. Users with no exchanges
	// are absent from the map rather than present with zeros, the caller knows the user list and
	// zero-filling in the store would make "never used it" indistinguishable from "not a user".
	SpendAll(ctx context.Context, from, to time.Time) (map[string]Spend, error)
}

// KindChat is the only kind this pilot writes.
const KindChat = "chat"
