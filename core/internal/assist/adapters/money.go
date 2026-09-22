package adapters

// money.go, DIB-478. The three money seams: today's rollup, the historical range, and the AR desk.
//
// ONE RULE ABOVE ALL OTHERS IN THIS FILE: NOTHING HERE ADDS UP MONEY. Every figure below is carried
// through from the module that owns it. The rollup owns today; the reporting template owns a
// historical day; the credit service owns an aging bucket. An adapter that summed anything would be
// a second definition of a number that already has one, and the first time the two disagreed in
// front of a customer nobody would believe either.

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/reporting"
)

// -- payments today: the rollup, and only the rollup --------------------------------------------

// RollupTodayReader is what this adapter needs from internal/messaging: the payments-today read the
// dashboard tile already uses, with the caller's branch reach passed in.
type RollupTodayReader interface {
	PaymentsTodayFor(ctx context.Context, branchIDs []string, allBranches bool, at time.Time) (messaging.PaymentsToday, error)
}

type rollupAdapter struct{ svc RollupTodayReader }

var _ assist.RollupReader = rollupAdapter{}

// NewRollupReader builds the seam. A nil service returns a nil reader, the assist package reads
// that as an unwired seam and answers "not switched on in this system yet" rather than panicking.
func NewRollupReader(svc RollupTodayReader) assist.RollupReader {
	if svc == nil {
		return nil
	}
	return rollupAdapter{svc: svc}
}

// PaymentsToday reads the ROOT of the day's rollup as the asking user.
//
// THE BRANCH REACH IS THE CALLER'S AND IS ENFORCED BY MESSAGING, not here, its own doc says in as
// many words that a consumer "cannot enforce branch scoping on a number it did not compute". This
// adapter's only job is to hand over the reach in the shape that read wants: the granted branch ids,
// or the global flag. Never both, and never an empty list standing in for "everything".
func (a rollupAdapter) PaymentsToday(ctx context.Context, c assist.Caller, at time.Time) (assist.DayTotal, error) {
	out, err := a.svc.PaymentsTodayFor(ctx, branchIDsOf(c), c.Global, at)
	if err != nil {
		return assist.DayTotal{}, err
	}
	return assist.DayTotal{Day: out.Day, Count: out.Count, Cents: out.Cents, Channels: out.Channels}, nil
}

// branchIDsOf renders the caller's grants for a read that takes a list plus a global flag.
//
// A GLOBAL caller passes NO list, the flag is the authority and a list beside it invites a reader
// to wonder which wins. A scoped caller with no grants passes an EMPTY list, which the read renders
// as a filter matching nothing. Never nil-meaning-everything: that inversion is the exact thing
// branch scoping exists to prevent.
func branchIDsOf(c assist.Caller) []string {
	if c.Global {
		return nil
	}
	if c.BranchIDs == nil {
		return []string{}
	}
	return c.BranchIDs
}

// -- payments over a range: the reporting module's own cash-receipts template -------------------

// ReportRunner is what this adapter needs from internal/reporting.
//
// The reporting service derives its OWN branch fence from the request guard's caller (authorizeBranch)
// and refuses a branch outside it. That is why this seam takes no reach: a second fence over the same
// question would be a second answer to it, and the module that owns the report owns its fence.
type ReportRunner interface {
	Run(ctx context.Context, code string, in map[string]string) (reporting.Result, error)
}

type paymentRangeAdapter struct{ svc ReportRunner }

var _ assist.PaymentRangeReader = paymentRangeAdapter{}

// NewPaymentRangeReader builds the seam.
func NewPaymentRangeReader(svc ReportRunner) assist.PaymentRangeReader {
	if svc == nil {
		return nil
	}
	return paymentRangeAdapter{svc: svc}
}

// holdStatusActive is the credit_holds status this seam asks for. A LIVE hold is the only one that
// answers "who is stopped right now"; a released one is history and belongs on the credit desk's own
// screen, where the audit trail beside it makes sense of it.
const holdStatusActive = "ACTIVE"

// paymentsByDayTemplate is the reporting code this seam runs, and the three parameter names it binds.
//
// ALL FOUR ARE A CONTRACT WITH A SEEDED ROW, not with a Go type, so nothing in the compiler checks
// them. A wrong code or a wrong parameter name is a 400 from reporting at the moment a user asks -
// "unknown report parameter", and every unit test in this package would still pass, because a fake
// ReportRunner accepts whatever map it is handed. That is not a hypothetical: this seam shipped with
// "from"/"to" (the words the assist query struct uses) against a template that declares
// "from_date"/"to_date", and `payments.by_day` was dead on the live server until a walkthrough asked
// it a question. TestPaymentsByDayBindsTheTemplatesOwnParameterNames now reads the parameter names
// out of reporting's OWN catalogue and fails if these drift from it again.
const (
	paymentsByDayTemplate = "payments_by_day"
	paramFromDate         = "from_date"
	paramToDate           = "to_date"
	paramBranchID         = "branch_id"
)

// PaymentsByDay runs the cash-receipts template and reads its rows BY COLUMN KEY.
//
// BY KEY, NOT BY POSITION. reporting.Result.Rows is [][]any and the column order is the template's
// business; an adapter that indexed [0], [1], [2] would silently start reading the surcharge as the
// amount the day somebody added a column. The Columns slice is the template's own description of its
// own shape, so it is what the indices come from.
func (a paymentRangeAdapter) PaymentsByDay(ctx context.Context, c assist.Caller, q assist.PaymentRangeQuery) ([]assist.PaymentDay, error) {
	in := map[string]string{paramFromDate: q.From, paramToDate: q.To}
	if strings.TrimSpace(q.BranchID) != "" {
		in[paramBranchID] = strings.TrimSpace(q.BranchID)
	}
	res, err := a.svc.Run(ctx, paymentsByDayTemplate, in)
	if err != nil {
		return nil, err
	}
	day := columnIndex(res.Columns, "day")
	count := columnIndex(res.Columns, "payment_count")
	cents := columnIndex(res.Columns, "amount_cents")
	out := make([]assist.PaymentDay, 0, len(res.Rows))
	for _, row := range res.Rows {
		out = append(out, assist.PaymentDay{
			Day:   cellString(row, day),
			Count: int(cellInt(row, count)),
			Cents: cellInt(row, cents),
		})
	}
	return out, nil
}

func columnIndex(cols []reporting.Column, key string) int {
	for i, c := range cols {
		if c.Name == key {
			return i
		}
	}
	return -1
}

// cellString and cellInt are total: an absent column or an unexpected type reads as the zero value
// rather than panicking. A report that gained or lost a column must degrade to a missing field on
// one chat answer, not to a 500 in an assistant.
func cellString(row []any, i int) string {
	if i < 0 || i >= len(row) || row[i] == nil {
		return ""
	}
	if s, ok := row[i].(string); ok {
		return s
	}
	return ""
}

func cellInt(row []any, i int) int64 {
	if i < 0 || i >= len(row) || row[i] == nil {
		return 0
	}
	switch v := row[i].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// -- AR: aging, and who is stopped --------------------------------------------------------------

// CreditDeskReader is what this adapter needs from internal/billing's credit service, the AR
// console's own two reads.
type CreditDeskReader interface {
	Aging(ctx context.Context, asOf time.Time, branchID string) ([]billing.AgingRow, error)
	ListHolds(ctx context.Context, status string) ([]billing.CreditHold, error)
}

type arAdapter struct{ svc CreditDeskReader }

var _ assist.ARReader = arAdapter{}

// NewARReader builds the seam.
func NewARReader(svc CreditDeskReader) assist.ARReader {
	if svc == nil {
		return nil
	}
	return arAdapter{svc: svc}
}

// Aging fences the credit desk's worklist to the caller's branches.
//
// billing.Aging takes ONE branch id ("" = every branch), because the month-end view is opened for one
// branch at a time. A caller with three grants therefore asks three times and the answers are
// CONCATENATED, which is a filter, not arithmetic: no bucket is recomputed, no total is folded, and
// every row is exactly the row billing produced.
//
// A GLOBAL caller asks once with "". A scoped caller with NO grants asks not at all and gets nothing
// , never everything.
func (a arAdapter) Aging(ctx context.Context, c assist.Caller, asOf time.Time) ([]assist.AgingAccount, error) {
	var rows []billing.AgingRow
	switch {
	case c.Global:
		r, err := a.svc.Aging(ctx, asOf, "")
		if err != nil {
			return nil, err
		}
		rows = r
	default:
		for _, b := range c.BranchIDs {
			r, err := a.svc.Aging(ctx, asOf, b)
			if err != nil {
				return nil, err
			}
			rows = append(rows, r...)
		}
	}
	out := make([]assist.AgingAccount, 0, len(rows))
	for _, r := range rows {
		out = append(out, assist.AgingAccount{
			CustomerID:   r.CustomerID,
			CustomerName: r.CustomerName,
			AccountNo:    r.AccountNo,
			BranchID:     r.BranchID,
			CurrentCents: r.CurrentCents,
			Days30Cents:  r.Days30Cents,
			Days60Cents:  r.Days60Cents,
			Days90Cents:  r.Days90PlusCents,
			TotalCents:   r.TotalCents,
			OnHold:       r.OnHold,
			HoldKind:     r.HoldKind,
		})
	}
	// BIGGEST DEBT FIRST. The question behind "show me the aging" is always "who do I chase", and a
	// list in whatever order the branches came back in answers it only by accident.
	sort.SliceStable(out, func(i, j int) bool { return out[i].TotalCents > out[j].TotalCents })
	return out, nil
}

// Holds lists the ACTIVE credit holds the caller may see, and says whether it had to narrow.
//
// THE FENCE HERE IS THE HONEST-BUT-IMPERFECT ONE, AND IT SAYS SO. credit_holds carries no branch of
// its own, a hold is placed on an ACCOUNT, and an account trades wherever it trades, so there is no
// branch column to filter on. Rather than hand a branch-scoped user every hold in the company (a
// widening) or refuse the question entirely (unhelpful, and the credit desk's own screen does not
// refuse it), this narrows through the accounts that appear in the caller's OWN aging reach and
// reports scoped=true so the command can say what it did.
//
// The known cost is stated where it will be read (commands_domain.go, ar.holds): an account on hold
// with no outstanding balance at the caller's branches is not listed for a scoped caller. A GLOBAL
// caller sees every hold and scoped is false.
func (a arAdapter) Holds(ctx context.Context, c assist.Caller) ([]assist.HoldRecord, bool, error) {
	holds, err := a.svc.ListHolds(ctx, holdStatusActive)
	if err != nil {
		return nil, false, err
	}
	var allowed map[string]bool
	if !c.Global {
		allowed = map[string]bool{}
		for _, b := range c.BranchIDs {
			rows, err := a.svc.Aging(ctx, time.Now(), b)
			if err != nil {
				return nil, true, err
			}
			for _, r := range rows {
				allowed[r.CustomerID] = true
			}
		}
	}
	out := make([]assist.HoldRecord, 0, len(holds))
	for _, h := range holds {
		if allowed != nil && !allowed[h.CustomerID] {
			continue
		}
		rec := assist.HoldRecord{
			CustomerID:     h.CustomerID,
			CustomerName:   h.CustomerName,
			Status:         h.Status,
			Kind:           h.Kind,
			AutoPlaced:     h.AutoPlaced,
			Reason:         h.Reason,
			OverrideReason: h.OverrideReason,
			PlacedAt:       h.CreatedAt,
		}
		if h.OverrideUntil != nil {
			rec.OverrideUntil = *h.OverrideUntil
		}
		out = append(out, rec)
	}
	return out, !c.Global, nil
}
