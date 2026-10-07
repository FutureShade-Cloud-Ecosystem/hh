//go:build integration

// staff_decision_integration_test.go, a salesperson marks a quote accepted or lost on the customer's
// behalf, after the customer said so by phone, text, email or in person.
package quote

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	decKeyA = "7c1d8f2e-0000-4000-8000-00000000a001"
	decKeyB = "7c1d8f2e-0000-4000-8000-00000000a002"
)

func decisionNow() time.Time { return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC) }

func decisionCaller(role, branch string) context.Context {
	c := intStaff(branch)
	c.RoleCode = role
	return qCtxAs(c)
}

func decStatus(t *testing.T, pool *pgxpool.Pool, quoteID string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM quotes WHERE id=$1`, quoteID).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}

func decAuditCount(t *testing.T, pool *pgxpool.Pool, quoteID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE entity_type='quote' AND entity_id=$1`, quoteID).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

func TestIntegration_StaffAccept_SentBecomesAcceptedWithItsRecord(t *testing.T) {
	pool := intPool(t)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	svc := shareSvc(t, pool, decisionNow())
	ctx := decisionCaller("INSIDE_SALES", intTrenton)

	res, err := svc.AcceptOnBehalf(ctx, quoteID, StaffAcceptIn{
		Channel: "Phone", Note: "Dana called back, wants it Friday", QuantitiesConfirmed: true, IdempotencyKey: decKeyA,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if res.Status != "ACCEPTED" || res.PreviousStatus != "SENT" || res.Channel != "phone" || !res.OnBehalf || res.Replayed {
		t.Errorf("result: %+v", res)
	}
	if got := decStatus(t, pool, quoteID); got != "ACCEPTED" {
		t.Fatalf("status is %q, want ACCEPTED", got)
	}

	// The audit row says it was staff, for the customer, how, and what staff attested.
	var purpose, channel, note, clientID, before, after, actor string
	var onBehalf, qtyOK bool
	if err := pool.QueryRow(context.Background(), `
		SELECT changes->>'purpose', changes->>'channel', COALESCE(changes->>'note',''), changes->>'client_id',
		       changes->>'before', changes->>'after', (changes->>'on_behalf')::boolean,
		       (changes->>'quantities_confirmed')::boolean, actor_id::text
		  FROM audit_log WHERE entity_type='quote' AND entity_id=$1 AND action=$2`,
		quoteID, audit.ActQuoteAccepted).
		Scan(&purpose, &channel, &note, &clientID, &before, &after, &onBehalf, &qtyOK, &actor); err != nil {
		t.Fatalf("the accept must leave exactly one QUOTE_ACCEPTED row: %v", err)
	}
	if purpose != "STAFF_QUOTE_DECISION" || channel != "phone" || !onBehalf || !qtyOK || clientID != decKeyA ||
		before != "SENT" || after != "ACCEPTED" || actor == "" {
		t.Errorf("audit row: purpose=%q channel=%q on_behalf=%v qty=%v key=%q %s->%s actor=%q",
			purpose, channel, onBehalf, qtyOK, clientID, before, after, actor)
	}
	if note != "Dana called back, wants it Friday" {
		t.Errorf("the note must ride the audit row, got %q", note)
	}

	// The quote's own change history shows the move and who made it.
	hist, err := svc.QuoteEditHistory(ctx, quoteID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	found := false
	for _, c := range hist.Changes {
		if c.Field == "status" && c.Before == "SENT" && c.After == "ACCEPTED" && c.ActorID != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("the change history must show status SENT to ACCEPTED by the staff member: %+v", hist.Changes)
	}
}

// A quote nobody has sent yet may be accepted by staff: the customer agreed to what the salesperson
// read out, and there is no link to click. It needs at least one line, so an empty draft is never
// "accepted".
func TestIntegration_StaffAccept_DraftWithLinesYesEmptyNo(t *testing.T) {
	pool := intPool(t)
	svc := shareSvc(t, pool, decisionNow())
	ctx := decisionCaller("INSIDE_SALES", intTrenton)
	in := StaffAcceptIn{Channel: "in_person", QuantitiesConfirmed: true}

	_, withLines, _, _ := seedQuote(t, pool, "DRAFT", nil)
	res, err := svc.AcceptOnBehalf(ctx, withLines, in)
	if err != nil {
		t.Fatalf("a DRAFT with a line: %v", err)
	}
	if res.PreviousStatus != "DRAFT" || decStatus(t, pool, withLines) != "ACCEPTED" {
		t.Errorf("DRAFT must accept and say it was a draft: %+v", res)
	}

	_, empty, _, _ := seedQuote(t, pool, "DRAFT", nil)
	if _, err := pool.Exec(context.Background(), `DELETE FROM quote_lines WHERE quote_id=$1`, empty); err != nil {
		t.Fatalf("empty the quote: %v", err)
	}
	var refused *DecisionRefusedError
	if _, err := svc.AcceptOnBehalf(ctx, empty, in); !errors.As(err, &refused) || !strings.Contains(err.Error(), "no lines") {
		t.Fatalf("an empty draft must refuse in words naming the missing lines, got %v", err)
	}
	if got := decStatus(t, pool, empty); got != "DRAFT" {
		t.Errorf("the refused empty draft moved to %q", got)
	}
}

// Every refusal is in words, names the state, and leaves the quote and its trail exactly as they were.
func TestIntegration_StaffDecision_RefusalsAreInWordsAndWriteNothing(t *testing.T) {
	pool := intPool(t)
	svc := shareSvc(t, pool, decisionNow())
	ctx := decisionCaller("INSIDE_SALES", intTrenton)

	cases := []struct {
		name, status string
		validUntil   any
		wantWords    string
	}{
		{"expired by date", "SENT", "2026-07-01", "expired"},
		{"already accepted", "ACCEPTED", nil, "already accepted"},
		{"converted", "CONVERTED", nil, "converted"},
		{"lost", "LOST", nil, "lost"},
		{"expired status", "EXPIRED", nil, "expired"},
	}
	for _, c := range cases {
		for _, door := range []string{"accept"} {
			t.Run(c.name+"/"+door, func(t *testing.T) {
				_, quoteID, _, _ := seedQuote(t, pool, c.status, c.validUntil)
				before := decAuditCount(t, pool, quoteID)
				_, err := svc.AcceptOnBehalf(ctx, quoteID, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true})
				if err == nil {
					t.Fatal("want a refusal")
				}
				if !strings.Contains(strings.ToLower(err.Error()), c.wantWords) {
					t.Errorf("the refusal must say it in words (%q): %v", c.wantWords, err)
				}
				if got := decStatus(t, pool, quoteID); got != c.status {
					t.Errorf("status moved to %q", got)
				}
				if decAuditCount(t, pool, quoteID) != before {
					t.Error("a refusal must write no audit row")
				}
			})
		}
	}
}

func TestIntegration_StaffAccept_IdempotentByKey(t *testing.T) {
	pool := intPool(t)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	svc := shareSvc(t, pool, decisionNow())
	ctx := decisionCaller("INSIDE_SALES", intTrenton)
	in := StaffAcceptIn{Channel: "text", QuantitiesConfirmed: true, IdempotencyKey: decKeyA}

	first, err := svc.AcceptOnBehalf(ctx, quoteID, in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	rows := decAuditCount(t, pool, quoteID)

	again, err := svc.AcceptOnBehalf(ctx, quoteID, in)
	if err != nil {
		t.Fatalf("a retry under the same key must answer the first outcome, got %v", err)
	}
	if !again.Replayed || again.Status != "ACCEPTED" || again.PreviousStatus != "SENT" || !again.DecidedAt.Equal(first.DecidedAt) {
		t.Errorf("replay: %+v vs first %+v", again, first)
	}
	if decAuditCount(t, pool, quoteID) != rows {
		t.Error("a replay must write nothing")
	}

	// The retry still answers the first outcome after the quote has moved on.
	if _, err := pool.Exec(context.Background(), `UPDATE quotes SET status='CONVERTED' WHERE id=$1`, quoteID); err != nil {
		t.Fatalf("move on: %v", err)
	}
	if again, err = svc.AcceptOnBehalf(ctx, quoteID, in); err != nil || !again.Replayed {
		t.Fatalf("a retry after the quote moved on must still replay: %+v %v", again, err)
	}

	// The same key for a different statement is refused, never replayed.
	other := in
	other.Channel = "email"
	if _, err := svc.AcceptOnBehalf(ctx, quoteID, other); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Errorf("same key, another channel: want ErrIdempotencyKeyReused, got %v", err)
	}

	// Without a key a second accept is simply refused: staff re-asserting is a mistake worth saying.
	_, q2, _, _ := seedQuote(t, pool, "SENT", nil)
	if _, err := svc.AcceptOnBehalf(ctx, q2, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true}); err != nil {
		t.Fatalf("unkeyed first: %v", err)
	}
	if _, err := svc.AcceptOnBehalf(ctx, q2, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true}); err == nil {
		t.Error("an unkeyed second accept must be refused")
	}
	_ = decKeyB
}

func TestIntegration_StaffAccept_AuthzAndBranch(t *testing.T) {
	pool := intPool(t)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	svc := shareSvc(t, pool, decisionNow())
	in := StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true}

	var notSelling *NotSellingRoleError
	if _, err := svc.AcceptOnBehalf(decisionCaller("YARD_TEAM", intTrenton), quoteID, in); !errors.As(err, &notSelling) {
		t.Errorf("a yard team member must be refused as a non seller, got %v", err)
	}
	if _, err := svc.AcceptOnBehalf(decisionCaller("INSIDE_SALES", intKingston), quoteID, in); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
		t.Errorf("a seller in another branch must be refused flat, got %v", err)
	}
	if _, err := svc.AcceptOnBehalf(context.Background(), quoteID, in); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("no caller: want ErrUnauthorized, got %v", err)
	}
	if got := decStatus(t, pool, quoteID); got != "SENT" {
		t.Errorf("refused callers moved the quote to %q", got)
	}
	// Every selling role admitted by the convert predicate may do it, each on its own quote.
	for _, role := range []string{"INSIDE_SALES", "OUTSIDE_SALES", "SALES_MANAGER", "BRANCH_MANAGER", "GENERAL_MANAGER", "FINANCIAL_CONTROLLER"} {
		_, q, _, _ := seedQuote(t, pool, "SENT", nil)
		if _, err := svc.AcceptOnBehalf(decisionCaller(role, intTrenton), q, in); err != nil {
			t.Errorf("%s must be able to accept on the customer's behalf: %v", role, err)
		}
	}
}

// What staff accepts, staff can convert: the accepted quote is an ordinary ACCEPTED quote to the
// conversion.
func TestIntegration_StaffAccept_ThenConvert(t *testing.T) {
	pool := intPool(t)
	seller := qd1User(t, pool, "seller")
	cust, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "SENT", seller, true)
	setCreditLimit(t, pool, cust, 1_000_00)
	cleanupConvertedOrders(t, pool, quoteID)
	svc := qd1Svc(t, pool, decisionNow())
	ctx := qd1WithRole(qCtxAs(intStaff(intTrenton)), "INSIDE_SALES", intTrenton, seller)

	if _, err := svc.AcceptOnBehalf(ctx, quoteID, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	res, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert after a staff accept: %v", err)
	}
	if res.OrderID == "" {
		t.Error("the conversion must mint an order")
	}
}
