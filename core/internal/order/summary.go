package order

// summary.go, the caller-scoped read of ONE order by its VISIBLE number.
//
// WHY THIS IS NOT OrderRead. OrderRead (service.go) answers "what may I do to this order right
// now": status, legal actions, editable fields, lines and money, keyed by an order ID with the
// order_no accepted as an alternate. It is the lifecycle panel's read and it is correctly shaped
// for a screen that is about to write.
//
// This one answers a different question, "where is #SO-4?", asked by someone holding a piece of
// paper with a number on it, who wants one sentence back. It is deliberately smaller: no legal
// actions (nothing is about to be written), no lines (a headline is the answer), and it resolves the
// number to a customer name and a branch name because those are the two facts that make the answer
// recognisable as the right order.
//
// THE AUTHORITY IS IDENTICAL TO OrderRead's, and that is the property worth stating: this read can
// never surface an order the lifecycle panel would refuse. It uses the SAME ResolveOrderID with the
// SAME branch scope (B2-4: a cross-branch #SO- number resolves to nothing, so the number space is
// not an existence oracle), and it re-checks the owning branch after loading, because ResolveOrderID
// passes a UUID through unresolved by design.
//
// ONE DELIBERATE DIFFERENCE IN THE FAILURE SHAPE. OrderRead distinguishes "no such order" (404) from
// "not your branch" (403), which is right for a deep link a user navigated to. This read collapses
// both into found=false, because its consumer states the answer in words: "I can't find an order
// numbered SO-4" is true in both cases, and saying "that order exists but is not yours" would hand
// back exactly the fact the branch-scoped resolve was written to withhold.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// isNotFound / isNoRowsErr are local spellings so the cascade above reads as intent rather than as
// three copies of an errors.Is.
func isNotFound(err error) bool  { return errors.Is(err, ErrNotFound) }
func isNoRowsErr(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// ReadScope is a caller's branch authority projected onto a read: the same split
// callerBranchScope() computes from a StaffContext, as a value a composition-root adapter can build
// and pass in.
//
// It is NOT an identity.StaffContext on purpose. A read that received one could re-derive authority;
// this one can only be told what it is. Global true means every branch and BranchIDs is ignored;
// Global false with no BranchIDs means no branches at all, which resolves to not-found rather than
// to everything.
type ReadScope struct {
	Global    bool
	BranchIDs []string
}

// ScopeFor projects a guard-resolved staff caller onto a ReadScope. It exists so the mapping lives
// HERE, next to the read it fences, rather than being re-implemented by every adapter, and so the
// test that pins it to callerBranchScope's behaviour has one thing to pin.
func ScopeFor(global bool, branchIDs []string) ReadScope {
	if global {
		return ReadScope{Global: true}
	}
	return ReadScope{BranchIDs: append([]string(nil), branchIDs...)}
}

// Summary is one order's headline facts, everything needed to say "SO-4 is picking, Northgate
// Landscaping, $5,123.00" and nothing else.
type Summary struct {
	OrderID      string    `json:"order_id"`
	OrderNo      string    `json:"order_no"`
	Status       string    `json:"status"`
	CustomerName string    `json:"customer_name"`
	BranchID     string    `json:"branch_id"`
	BranchName   string    `json:"branch_name"`
	TotalCents   int64     `json:"total_cents"`
	PlacedAt     time.Time `json:"placed_at"`
	LineCount    int       `json:"line_count"`
}

// summaryStore is the optional persistence seam for this read.
//
// A separate interface reached by type assertion, exactly as editStore is (edit.go): orderStore is
// implemented outside this package (internal/portal composes the Service with its own fake), so
// widening it would break a module this change does not own. *Repository implements it, so the real
// deployment always takes the real path.
type summaryStore interface {
	OrderSummary(ctx context.Context, orderID string) (Summary, error)
}

// SummaryByNumber resolves a visible order number within the caller's reach and returns its
// headline facts.
//
// found=false is an ANSWER, "there is no order by that number that you can see", and covers a
// number that does not exist, a number in another branch, and a caller with no branch grants at all.
// An error means something actually failed.
func (s *Service) SummaryByNumber(ctx context.Context, scope ReadScope, orderNo string) (Summary, bool, error) {
	orderNo = strings.TrimSpace(orderNo)
	if orderNo == "" {
		return Summary{}, false, nil
	}
	ss, ok := s.store.(summaryStore)
	if !ok {
		return Summary{}, false, ErrEditNotConfigured
	}
	// B2-4: the number space is branch-scoped at RESOLUTION time, so a cross-branch #SO- answers
	// not-found rather than confirming the order exists.
	orderID, err := s.store.ResolveOrderID(ctx, orderNo, scope.Global, scope.BranchIDs)
	if err != nil {
		if isNotFound(err) {
			return Summary{}, false, nil
		}
		return Summary{}, false, err
	}
	sum, err := ss.OrderSummary(ctx, orderID)
	if err != nil {
		if isNotFound(err) {
			return Summary{}, false, nil
		}
		return Summary{}, false, err
	}
	// THE SECOND CHECK IS NOT REDUNDANT. ResolveOrderID passes a UUID through WITHOUT a round trip
	// (that is its documented contract, a cross-branch deep link is meant to 403 downstream), so
	// without this a caller who typed an order's id instead of its number would read any branch's
	// order. Collapsed to not-found here for the reason in the file header.
	if !scope.reaches(sum.BranchID) {
		return Summary{}, false, nil
	}
	return sum, true, nil
}

// reaches mirrors identity.StaffContext.CanAccessBranch on the projected scope.
func (r ReadScope) reaches(branchID string) bool {
	if r.Global {
		return true
	}
	for _, id := range r.BranchIDs {
		if id == branchID {
			return true
		}
	}
	return false
}

// OrderSummary reads one order's headline facts in a single round trip.
//
// The line count is a correlated subquery rather than a JOIN + GROUP BY: joining order_lines to the
// header would fan the header out per line, and the totals would then have to be de-duplicated by
// hand, the classic fan-out that produces a number nobody can reproduce.
//
// A malformed (non-UUID) id short-circuits to ErrNotFound before the query, since orders.id is a
// uuid column and a garbage id would otherwise raise SQLSTATE 22P02 → an internal error for what is
// really "no such order" (mirrors OrderHeader / OrderBranch).
func (r *Repository) OrderSummary(ctx context.Context, orderID string) (Summary, error) {
	if r.pool == nil {
		return Summary{}, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return Summary{}, ErrNotFound
	}
	var s Summary
	err := r.pool.QueryRow(ctx, `
		SELECT o.id::text, o.order_no::text, o.status,
		       COALESCE(c.name, ''),
		       COALESCE(o.branch_id::text, ''), COALESCE(l.name, ''),
		       o.total_cents, o.created_at,
		       (SELECT count(*)::int FROM order_lines ol WHERE ol.order_id = o.id)
		  FROM orders o
		  LEFT JOIN customers c ON c.id = o.customer_id
		  LEFT JOIN locations l ON l.id = o.branch_id
		 WHERE o.id = $1::uuid`, orderID).
		Scan(&s.OrderID, &s.OrderNo, &s.Status, &s.CustomerName,
			&s.BranchID, &s.BranchName, &s.TotalCents, &s.PlacedAt, &s.LineCount)
	if err != nil {
		if isNoRowsErr(err) {
			return Summary{}, ErrNotFound
		}
		return Summary{}, err
	}
	return s, nil
}
