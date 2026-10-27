//go:build integration

package billing

// credit_memo_customer_wall_integration_test.go: THE CUSTOMER WALL ON THE CREDIT ROUTE (UX-89).
//
// The credit route held the customer to the caller's ORG and never to the caller's REACH, and it
// handed the customer id to the customer module's own read unparsed. Two legs say what the house
// rule says, both against the REAL seams (the customer module's repository and the branches
// module's), so neither is a fixed map agreeing with itself:
//
//   - THE REACH: a branch scoped manager granted Trenton names a customer homed at Kingston, in the
//     same org. Every other customer door walls that with the house rule: an account homed at a
//     branch the caller cannot reach answers 404 "customer not found", the same answer an id that
//     is no account gets, so the door is no existence oracle for another branch's accounts.
//   - THE MALFORMED ID: a customer id that is not a uuid reaches the customer module's read as an
//     uncastable literal and answers 500, a database error for what is really "no such account".
//     The id is parsed BEFORE the seam reads it, so it answers as an account that is not there.
//
// The unknown id is the same answer in the same words, byte for byte, or the door is an oracle:
// that is the UX-89 walk's own rule, and this file holds the same line.

import (
	"context"
	"log/slog"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// cmWallRoute builds the route over the REAL seams: the branches module's org read and the
// customer module's own home-branch read.
func cmWallRoute(t *testing.T) *CreditMemoService {
	t.Helper()
	w := NewInvoiceWriter(intPool(t))
	return NewCreditMemoService(w, slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: creditMemoRules}).
		WithThreshold(creditMemoFixedThreshold{cents: 0}).
		WithBranchOrg(branches.NewRepository(w.pool)).
		WithCustomerHome(customer.NewRepository(w.pool))
}

// cmHouseAnswerIs reports whether err is THE customer answer, in the house's words: an account the
// caller cannot reach, an account of another org and an id that is no account all say this and
// nothing else.
func cmHouseAnswerIs(err error) bool {
	return err != nil && err.Error() == "customer not found"
}

// TestIntegration_CreditMemoRoute_ACustomerTheCallerCannotReachAnswersTheHouseNotFound is the
// reach line: a customer homed at a branch the caller does not reach is not a customer this caller
// may credit, and the answer is the one an id that is no account gets.
func TestIntegration_CreditMemoRoute_ACustomerTheCallerCannotReachAnswersTheHouseNotFound(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	// The customer is homed at Kingston: the same org as the caller's Trenton, a branch the caller
	// does not reach.
	cust := seedCustomerAt(t, pool, tag, kingstonBranch)
	svc := cmWallRoute(t)

	_, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: trentonBranch,
		AmountCents: 10_000, Reason: "Goodwill",
	}, cmTrentonOnlyCaller())
	if err == nil {
		t.Fatal("fw1-12a r3 red: a customer homed at a branch the caller cannot reach was credited")
	}
	if !cmHouseAnswerIs(err) {
		t.Fatalf("fw1-12a r3 red: a customer the caller cannot reach answered %q, want the house's %q", err.Error(), "customer not found")
	}
	var memos int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND customer_id = $1`, cust).Scan(&memos); err != nil {
		t.Fatalf("count the credit memos: %v", err)
	}
	if memos != 0 {
		t.Fatalf("the refused credit wrote %d documents, want none", memos)
	}
}

// TestIntegration_CreditMemoRoute_ACustomerOfAnotherOrgAnswersTheHouseNotFound is the org line:
// an account of another org answers the same way an account of no org at all does, and the same
// way an id that is no account does.
func TestIntegration_CreditMemoRoute_ACustomerOfAnotherOrgAnswersTheHouseNotFound(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomerAt(t, pool, tag, cmSouthBranchID)
	svc := cmWallRoute(t)

	_, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: trentonBranch,
		AmountCents: 10_000, Reason: "Goodwill",
	}, cmNorthGlobalCaller())
	if err == nil {
		t.Fatal("fw1-12a r3 red: a customer of another org was credited")
	}
	if !cmHouseAnswerIs(err) {
		t.Fatalf("fw1-12a r3 red: a customer of another org answered %q, want the house's %q", err.Error(), "customer not found")
	}
}

// TestIntegration_CreditMemoRoute_AMalformedCustomerIdAnswersTheHouseNotFound is the parse line: the
// id is parsed before the seam reads it, so a malformed id answers as an account that is not there
// and never as a database error.
func TestIntegration_CreditMemoRoute_AMalformedCustomerIdAnswersTheHouseNotFound(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	plantCreditMemoOrgs(t, pool)
	svc := cmWallRoute(t)

	_, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID: "not-a-uuid", BranchID: trentonBranch,
		AmountCents: 10_000, Reason: "Goodwill",
	}, cmTrentonOnlyCaller())
	if err == nil {
		t.Fatal("fw1-12a r3 red: a malformed customer id was credited")
	}
	if !cmHouseAnswerIs(err) {
		t.Fatalf("fw1-12a r3 red: a malformed customer id answered %q, want the house's %q", err.Error(), "customer not found")
	}
}
