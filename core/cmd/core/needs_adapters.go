package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

// needs_adapters.go, the NeedsSource adapters for the Home screen's Needs-you queue.
//
// SAME SEAM, SAME OBLIGATION AS THE TILE AND SERIES ADAPTERS. internal/dashboard declares
// NeedsSource and knows nothing about who owns the data; every implementation lives here, over the
// module that does. Scope.BranchIDs / Scope.AllBranches are the caller's GRANTS and every read below
// applies them.
//
// AND THE OBLIGATION THAT IS THIS SEAM'S OWN: a row must be what is waiting on THIS PERSON, not what
// exists. "Approvals waiting" is filtered to requests this caller may actually decide; a queue full
// of things somebody cannot act on is the fastest way to teach them to stop reading their Home.
//
// EVERY ADAPTER MAY FAIL AND MAY BE SLOW. A failure is an ABSENT ROW, not an error, the service
// gives each one its own budget, recovers a panic, logs, and moves on.

// w6bBranches resolves the branches a needs source may ask about. A branch-scoped caller brings
// their own grants; a GLOBAL caller brings AllBranches with an EMPTY list, so the branch set is read
// here rather than invented, a global caller with no rows would otherwise silently get no dispatch
// or counter rows at all.
func w6bBranches(ctx context.Context, pool *pgxpool.Pool, sc dashboard.Scope) []string {
	if !sc.AllBranches {
		return sc.BranchIDs
	}
	if pool == nil {
		return nil
	}
	rows, err := pool.Query(ctx,
		`SELECT id::text FROM locations WHERE loc_type = 'BRANCH' AND is_active ORDER BY code`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil
		}
		out = append(out, id)
	}
	return out
}

// w6bParseStamp reads a ::text timestamp back. A stamp that will not parse yields the ZERO time,
// which the queue reads as "age unknown" and simply does not show, an age is a nicety and a wrong
// one is worse than none.
func w6bParseStamp(s string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02 15:04:05.999999-07", "2006-01-02 15:04:05-07",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// -- identity · approvals waiting ON THIS PERSON ------------------------------------------------
// w6bApprovalsWaiting is re-pointed at the bridge's Approvals rail (the
// sanctioned class: decide-time authority stays the guard's resolution).

// -- identity · approvals filed by THIS PERSON ---------------------------------------------------
// w6bApprovalsFiledPending counts approvals the caller filed that are still pending, scoped to the
// branches the caller reaches. It returns only a count and the oldest filing time, never another
// person's request or its content, so a filer who may not decide still sees the count of their own.
// It sits beside w6bApprovalsWaiting under the same approvals rail, but its own section makes it
// clear at a glance which "you" the card counts: w6bApprovalsWaiting reads what's waiting for the
// caller to decide, w6bApprovalsFiledPending reads what the caller already filed and the system is
// still chewing on.
func w6bApprovalsFiledPending(svc *identity.Approvals) dashboard.NeedsFunc {
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.NeedsMeasure, error) {
		caller, ok := identity.StaffContextFromContext(ctx)
		if !ok {
			return dashboard.NeedsMeasure{}, errors.New("dashboard: filed pending approvals needs a caller")
		}
		reqs, err := svc.ListApprovals(ctx, caller, string(identity.ApprovalPending))
		if err != nil {
			return dashboard.NeedsMeasure{}, err
		}
		m := dashboard.NeedsMeasure{}
		for _, r := range reqs {
			if r.RequestedBy != caller.UserID {
				continue
			}
			m.Count++
			if !r.RequestedAt.IsZero() && (m.Oldest.IsZero() || r.RequestedAt.Before(m.Oldest)) {
				m.Oldest = r.RequestedAt
			}
		}
		return m, nil
	}
}

func w6bApprovalsWaiting(svc *identity.Approvals) dashboard.NeedsFunc {
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.NeedsMeasure, error) {
		caller, ok := identity.StaffContextFromContext(ctx)
		if !ok {
			return dashboard.NeedsMeasure{}, errors.New("dashboard: approvals needs a caller")
		}
		reqs, err := svc.ListApprovals(ctx, caller, string(identity.ApprovalPending))
		if err != nil {
			return dashboard.NeedsMeasure{}, err
		}
		n := 0
		for _, r := range reqs {
			// Four-eyes: your own request is never waiting on you.
			if r.RequestedBy == caller.UserID {
				continue
			}
			// The same decide rule the service enforces, read off the
			// configured service itself (the margin arm's authority is the
			// dealer's configured floor set, D11), including the line-bound
			// rule (P2-C): the queue counts what waits on THIS person and
			// nothing it cannot decide, and a legacy margin card with no
			// line binding is not the branch Sales manager's to decide or
			// count.
			if svc.CanDecideRequest(caller, r) {
				n++
			}
		}
		return dashboard.NeedsMeasure{Count: n}, nil
	}
}

// -- billing · the AR escalation card -----------------------------------------------------------
// The DEALER'S OWN escalation edge (0183), read from the landing rather than re-derived: a second
// opinion about what "late" means is exactly what 0183 exists to prevent. The source is ManagerOnly
// in the dashboard catalog, so this never runs for a caller below that tier.
func w6bAREscalation(svc *billing.ARLandingService) dashboard.NeedsFunc {
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.NeedsMeasure, error) {
		view, err := svc.Landing(ctx, "", sc.Now)
		if err != nil {
			return dashboard.NeedsMeasure{}, err
		}
		oldestDays := 0
		for _, e := range view.Escalation {
			if e.OldestDays > oldestDays {
				oldestDays = e.OldestDays
			}
		}
		m := dashboard.NeedsMeasure{Count: len(view.Escalation)}
		if oldestDays > 0 {
			m.Oldest = sc.Now.AddDate(0, 0, -oldestDays)
		}
		return m, nil
	}
}

// -- dispatch · the load board's own quick cards ------------------------------------------------
// Counts.Unassigned and Counts.Due, from the SAME read the board renders, so a queue row and the
// lane it links to cannot disagree. That is the whole promise countQuickCards was written for.
// The day is the dealer's (HouseDay), never the process zone's, so the evening after the UTC
// rollover still reads today's board.
func w6bLoadBoard(pool *pgxpool.Pool, svc *dispatch.Service, dueToCommit bool) dashboard.NeedsFunc {
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.NeedsMeasure, error) {
		n := 0
		day := dashboard.HouseDayIn(sc.Zone, sc.Now)
		for _, b := range w6bBranches(ctx, pool, sc) {
			view, err := svc.LoadBoard(ctx, b, day)
			if err != nil {
				return dashboard.NeedsMeasure{}, err
			}
			if dueToCommit {
				n += view.Counts.Due
			} else {
				n += view.Counts.Unassigned
			}
		}
		return dashboard.NeedsMeasure{Count: n}, nil
	}
}

// -- pos · the parked-ticket list ---------------------------------------------------------------
func w6bParkedTickets(pool *pgxpool.Pool, svc *pos.Service) dashboard.NeedsFunc {
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.NeedsMeasure, error) {
		m := dashboard.NeedsMeasure{}
		for _, b := range w6bBranches(ctx, pool, sc) {
			tickets, err := svc.ListParked(ctx, b)
			if err != nil {
				return dashboard.NeedsMeasure{}, err
			}
			for _, t := range tickets {
				m.Count++
				if at := w6bParseStamp(t.CreatedAt); !at.IsZero() {
					if m.Oldest.IsZero() || at.Before(m.Oldest) {
						m.Oldest = at
					}
				}
			}
		}
		return m, nil
	}
}

// -- tasks · picks past their accept deadline ---------------------------------------------------
// It WRAPS the shipped picks.stale tile provider rather than writing a second query, so the queue
// row and the tile are the same population by construction. The cost is that it carries no age: the
// tile answers a number, so `stale_after_minutes` never fires for this source until that read
// exposes a timestamp.
func w6bStalePicks(pool *pgxpool.Pool) dashboard.NeedsFunc {
	tile := w6aStalePicks(pool)
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.NeedsMeasure, error) {
		v, err := tile.Measure(ctx, sc)
		if err != nil {
			return dashboard.NeedsMeasure{}, err
		}
		return dashboard.NeedsMeasure{Count: int(v.Number)}, nil
	}
}

// -- tasks · shortages the yard raised and nobody has resolved ----------------------------------
// The count is the tasks module's own (OpenShortageSummary), read through its service with the
// caller's grants as the reach; this file holds no SQL for it.
func w6bOpenShortages(svc *tasks.Service) dashboard.NeedsFunc {
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.NeedsMeasure, error) {
		sum, err := svc.OpenShortageSummary(ctx, tasks.ShortageReach{Global: sc.AllBranches, BranchIDs: sc.BranchIDs})
		if err != nil {
			return dashboard.NeedsMeasure{}, err
		}
		return dashboard.NeedsMeasure{Count: sum.Count, Oldest: sum.Oldest}, nil
	}
}

// -- purchasing · orders that landed short and are still partial --------------------------------
// The purchasing module's own count (ReceivingDifferences), with the caller's grants as the reach.
// A receiving difference is a purchase order received in part with a short line, aged by its
// latest receipt; it leaves the queue on full receipt or close. HH refuses an over-receipt
// (inventory/handler.go, 409) and records no damaged or rejected quantity, so a short partial is
// the only difference HH holds today; price and bill variances belong to the money desk's three way
// match. If receiving later records damaged quantities, they join this queue.
func w6bReceivingDifferences(svc *purchasing.Service) dashboard.NeedsFunc {
	return func(ctx context.Context, sc dashboard.Scope) (dashboard.NeedsMeasure, error) {
		sum, err := svc.ReceivingDifferences(ctx, purchasing.BranchReach{Global: sc.AllBranches, BranchIDs: sc.BranchIDs})
		if err != nil {
			return dashboard.NeedsMeasure{}, err
		}
		return dashboard.NeedsMeasure{Count: sum.Count, Oldest: sum.Oldest}, nil
	}
}
