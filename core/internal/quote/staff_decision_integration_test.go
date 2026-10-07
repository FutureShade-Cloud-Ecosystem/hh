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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	decKeyA = "7c1d8f2e-0000-4000-8000-00000000a001"
	decKeyB = "7c1d8f2e-0000-4000-8000-00000000a002"
)

func i64p(v int64) *int64 { return &v }

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
		Channel: "Phone", Note: "Dana called back, wants it Friday", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000), IdempotencyKey: decKeyA,
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
	in := StaffAcceptIn{Channel: "in_person", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)}

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
		for _, door := range []string{"accept", "reject"} {
			t.Run(c.name+"/"+door, func(t *testing.T) {
				_, quoteID, _, _ := seedQuote(t, pool, c.status, c.validUntil)
				before := decAuditCount(t, pool, quoteID)
				var err error
				if door == "accept" {
					_, err = svc.AcceptOnBehalf(ctx, quoteID, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)})
				} else {
					_, err = svc.RejectOnBehalf(ctx, quoteID, StaffRejectIn{Channel: "phone", ReasonCode: "price"})
				}
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
	in := StaffAcceptIn{Channel: "text", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000), IdempotencyKey: decKeyA}

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
	if _, err := svc.RejectOnBehalf(ctx, quoteID, StaffRejectIn{Channel: "text", ReasonCode: "price", IdempotencyKey: decKeyA}); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Errorf("same key on the other door: want ErrIdempotencyKeyReused, got %v", err)
	}

	// Without a key a second accept is simply refused: staff re-asserting is a mistake worth saying.
	_, q2, _, _ := seedQuote(t, pool, "SENT", nil)
	if _, err := svc.AcceptOnBehalf(ctx, q2, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)}); err != nil {
		t.Fatalf("unkeyed first: %v", err)
	}
	if _, err := svc.AcceptOnBehalf(ctx, q2, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)}); err == nil {
		t.Error("an unkeyed second accept must be refused")
	}
	_ = decKeyB
}

func TestIntegration_StaffAccept_AuthzAndBranch(t *testing.T) {
	pool := intPool(t)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	svc := shareSvc(t, pool, decisionNow())
	in := StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)}

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

	var total int64
	if err := pool.QueryRow(context.Background(), `SELECT total_cents FROM quotes WHERE id=$1`, quoteID).Scan(&total); err != nil {
		t.Fatalf("read total: %v", err)
	}
	if _, err := svc.AcceptOnBehalf(ctx, quoteID, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(total)}); err != nil {
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

func TestIntegration_StaffReject_SentBecomesLostWithItsRecord(t *testing.T) {
	pool := intPool(t)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	svc := shareSvc(t, pool, decisionNow())
	ctx := decisionCaller("OUTSIDE_SALES", intTrenton)

	res, err := svc.RejectOnBehalf(ctx, quoteID, StaffRejectIn{
		Channel: "email", ReasonCode: "Competitor", Note: "went with the other yard", IdempotencyKey: decKeyA,
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if res.Status != "LOST" || res.PreviousStatus != "SENT" || res.Channel != "email" || !res.OnBehalf || res.Replayed {
		t.Errorf("result: %+v", res)
	}
	if got := decStatus(t, pool, quoteID); got != "LOST" {
		t.Fatalf("status is %q, want LOST", got)
	}
	var purpose, reason, note, before, after string
	var onBehalf bool
	if err := pool.QueryRow(context.Background(), `
		SELECT changes->>'purpose', changes->>'decline_reason', COALESCE(changes->>'note',''),
		       changes->>'before', changes->>'after', (changes->>'on_behalf')::boolean
		  FROM audit_log WHERE entity_type='quote' AND entity_id=$1 AND action=$2`,
		quoteID, audit.ActQuoteRejected).Scan(&purpose, &reason, &note, &before, &after, &onBehalf); err != nil {
		t.Fatalf("the reject must leave exactly one QUOTE_REJECTED row: %v", err)
	}
	if purpose != "STAFF_QUOTE_DECISION" || reason != "competitor" || note != "went with the other yard" ||
		before != "SENT" || after != "LOST" || !onBehalf {
		t.Errorf("audit row: purpose=%q reason=%q note=%q %s->%s on_behalf=%v", purpose, reason, note, before, after, onBehalf)
	}
	hist, err := svc.QuoteEditHistory(ctx, quoteID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	found := false
	for _, c := range hist.Changes {
		if c.Field == "status" && c.Before == "SENT" && c.After == "LOST" && c.ActorID != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("the change history must show status SENT to LOST by the staff member: %+v", hist.Changes)
	}
	// The staff read says why and how it was declined, from the decision's own row.
	view, err := svc.StaffQuoteRead(ctx, quoteID)
	if err != nil {
		t.Fatalf("staff read: %v", err)
	}
	if view.Declined == nil || view.Declined.ReasonCode != "competitor" || view.Declined.Channel != "email" ||
		view.Declined.Note != "went with the other yard" || view.Declined.DeclinedAt.IsZero() {
		t.Errorf("the lost quote must carry its decline: %+v", view.Declined)
	}
}

// A quote nobody sent can be marked lost too: the customer said no to what was read out, and an empty
// draft is no obstacle to saying no.
func TestIntegration_StaffReject_DraftAllowed(t *testing.T) {
	pool := intPool(t)
	_, quoteID, _, _ := seedQuote(t, pool, "DRAFT", nil)
	svc := shareSvc(t, pool, decisionNow())
	res, err := svc.RejectOnBehalf(decisionCaller("INSIDE_SALES", intTrenton), quoteID,
		StaffRejectIn{Channel: "phone", ReasonCode: "price"})
	if err != nil || res.PreviousStatus != "DRAFT" || decStatus(t, pool, quoteID) != "LOST" {
		t.Fatalf("a DRAFT must be markable lost: %+v %v", res, err)
	}
}

func TestIntegration_StaffReject_IdempotentByKey(t *testing.T) {
	pool := intPool(t)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	svc := shareSvc(t, pool, decisionNow())
	ctx := decisionCaller("INSIDE_SALES", intTrenton)
	in := StaffRejectIn{Channel: "text", ReasonCode: "timing", IdempotencyKey: decKeyB}

	first, err := svc.RejectOnBehalf(ctx, quoteID, in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	rows := decAuditCount(t, pool, quoteID)
	again, err := svc.RejectOnBehalf(ctx, quoteID, in)
	if err != nil || !again.Replayed || again.Status != "LOST" || !again.DecidedAt.Equal(first.DecidedAt) {
		t.Fatalf("a retry under the same key must answer the first outcome: %+v %v", again, err)
	}
	if decAuditCount(t, pool, quoteID) != rows {
		t.Error("a replay must write nothing")
	}
	// The key belongs to one decision: the other door, or another channel, is refused.
	if _, err := svc.AcceptOnBehalf(ctx, quoteID, StaffAcceptIn{Channel: "text", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000), IdempotencyKey: decKeyB}); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Errorf("same key on the accept door: want ErrIdempotencyKeyReused, got %v", err)
	}
	other := in
	other.Channel = "phone"
	if _, err := svc.RejectOnBehalf(ctx, quoteID, other); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Errorf("same key, another channel: want ErrIdempotencyKeyReused, got %v", err)
	}
}

func TestIntegration_StaffReject_AuthzAndBranch(t *testing.T) {
	pool := intPool(t)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	svc := shareSvc(t, pool, decisionNow())
	in := StaffRejectIn{Channel: "phone", ReasonCode: "price"}

	var notSelling *NotSellingRoleError
	if _, err := svc.RejectOnBehalf(decisionCaller("YARD_TEAM", intTrenton), quoteID, in); !errors.As(err, &notSelling) {
		t.Errorf("a yard team member must be refused as a non seller, got %v", err)
	}
	if _, err := svc.RejectOnBehalf(decisionCaller("INSIDE_SALES", intKingston), quoteID, in); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
		t.Errorf("a seller in another branch must be refused flat, got %v", err)
	}
	if _, err := svc.RejectOnBehalf(context.Background(), quoteID, in); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("no caller: want ErrUnauthorized, got %v", err)
	}
	if got := decStatus(t, pool, quoteID); got != "SENT" {
		t.Errorf("refused callers moved the quote to %q", got)
	}
	for _, role := range []string{"INSIDE_SALES", "OUTSIDE_SALES", "SALES_MANAGER", "BRANCH_MANAGER", "GENERAL_MANAGER", "FINANCIAL_CONTROLLER"} {
		_, q, _, _ := seedQuote(t, pool, "SENT", nil)
		if _, err := svc.RejectOnBehalf(decisionCaller(role, intTrenton), q, in); err != nil {
			t.Errorf("%s must be able to reject on the customer's behalf: %v", role, err)
		}
	}
}

// decisionReserver records the soft hold releases a reject asks for, and can fail them.
type decisionReserver struct {
	releases []string
	fail     error
}

func (d *decisionReserver) ReserveTx(context.Context, pgx.Tx, StockReserveInput) (StockReserveResult, error) {
	return StockReserveResult{}, nil
}

func (d *decisionReserver) ReleaseBySourceTx(_ context.Context, _ pgx.Tx, sourceType, ref, status string) (int, error) {
	if d.fail != nil {
		return 0, d.fail
	}
	d.releases = append(d.releases, sourceType+"/"+ref+"/"+status)
	return 1, nil
}

// A lost quote is no longer interest in that stock: the soft holds are released RELEASED on the same
// transaction (reserve.go's lifecycle), and a release that fails rolls the reject back with it.
func TestIntegration_StaffReject_ReleasesSoftHoldsOnTheSameTransaction(t *testing.T) {
	pool := intPool(t)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	ctx := decisionCaller("INSIDE_SALES", intTrenton)
	in := StaffRejectIn{Channel: "phone", ReasonCode: "price"}

	failing := &decisionReserver{fail: errors.New("stock down")}
	if _, err := shareSvc(t, pool, decisionNow()).WithStockReserver(failing).RejectOnBehalf(ctx, quoteID, in); err == nil {
		t.Fatal("a failed hold release must fail the reject")
	}
	if got := decStatus(t, pool, quoteID); got != "SENT" {
		t.Fatalf("a failed release must roll the status back, it is %q", got)
	}
	if n := decAuditCount(t, pool, quoteID); n != 0 {
		t.Errorf("a failed release must leave no audit row, found %d", n)
	}

	ok := &decisionReserver{}
	if _, err := shareSvc(t, pool, decisionNow()).WithStockReserver(ok).RejectOnBehalf(ctx, quoteID, in); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if len(ok.releases) != 1 || ok.releases[0] != ResSourceQuote+"/"+quoteID+"/"+ResReleased {
		t.Errorf("the soft holds must be released as RELEASED for this quote: %v", ok.releases)
	}

	// An accept keeps the holds: the quote is still going to become an order.
	_, q2, _, _ := seedQuote(t, pool, "SENT", nil)
	keep := &decisionReserver{}
	if _, err := shareSvc(t, pool, decisionNow()).WithStockReserver(keep).AcceptOnBehalf(ctx, q2,
		StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if len(keep.releases) != 0 {
		t.Errorf("an accept must not release the holds: %v", keep.releases)
	}
}

// - follow-ups stop -

type followUpCounts struct {
	enabled              bool
	why                  string
	sent, cancelled, tot int
}

func followUpState(t *testing.T, pool *pgxpool.Pool, quoteID string) followUpCounts {
	t.Helper()
	var f followUpCounts
	if err := pool.QueryRow(context.Background(),
		`SELECT enabled, COALESCE(disabled_why,'') FROM quote_followup_plans WHERE quote_id=$1`, quoteID).
		Scan(&f.enabled, &f.why); err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE status='SENT'), count(*) FILTER (WHERE status='CANCELLED'), count(*)
		  FROM quote_followup_events WHERE quote_id=$1`, quoteID).Scan(&f.sent, &f.cancelled, &f.tot); err != nil {
		t.Fatalf("count steps: %v", err)
	}
	return f
}

// A quote with a follow-up cadence stops being followed up the moment staff record the customer's
// answer, not at the next sweep: the steps not yet taken are written CANCELLED with the reason, the
// plan is turned off, and a step already sent stays as sent. The same transaction as the status move.
func TestIntegration_StaffDecision_StopsFollowUps(t *testing.T) {
	pool := intPool(t)
	for _, door := range []string{"accept", "reject"} {
		t.Run(door, func(t *testing.T) {
			_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), `DELETE FROM quote_followup_events WHERE quote_id = $1`, quoteID)
				_, _ = pool.Exec(context.Background(), `DELETE FROM quote_followup_plans WHERE quote_id = $1`, quoteID)
			})
			started := decisionNow().AddDate(0, 0, -4)
			if _, err := NewFollowUpRepository(pool).EnsurePlan(context.Background(), quoteID, ChannelEmail, []int{3, 7, 14}, started); err != nil {
				t.Fatalf("plan: %v", err)
			}
			if _, err := pool.Exec(context.Background(), `
				INSERT INTO quote_followup_events (quote_id, step_day, channel, status, scheduled_for)
				VALUES ($1, 3, $2, 'SENT', $3)`, quoteID, ChannelEmail, started.AddDate(0, 0, 3)); err != nil {
				t.Fatalf("seed a sent step: %v", err)
			}
			svc := shareSvc(t, pool, decisionNow())
			ctx := decisionCaller("INSIDE_SALES", intTrenton)

			var err error
			if door == "accept" {
				_, err = svc.AcceptOnBehalf(ctx, quoteID, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000), IdempotencyKey: decKeyA})
			} else {
				_, err = svc.RejectOnBehalf(ctx, quoteID, StaffRejectIn{Channel: "phone", ReasonCode: "price", IdempotencyKey: decKeyA})
			}
			if err != nil {
				t.Fatalf("%s: %v", door, err)
			}
			f := followUpState(t, pool, quoteID)
			if f.enabled || f.why == "" {
				t.Errorf("the plan must be turned off with a reason: enabled=%v why=%q", f.enabled, f.why)
			}
			if f.sent != 1 || f.cancelled != 2 || f.tot != 3 {
				t.Errorf("the sent step stays and the two untaken steps are cancelled: %+v", f)
			}
		})
	}
}

// A quote that never had a cadence is no obstacle, and a refused decision stops nothing.
func TestIntegration_StaffDecision_FollowUpsLeftAloneWhenNothingMoves(t *testing.T) {
	pool := intPool(t)
	svc := shareSvc(t, pool, decisionNow())
	ctx := decisionCaller("INSIDE_SALES", intTrenton)

	_, bare, _, _ := seedQuote(t, pool, "SENT", nil)
	if _, err := svc.AcceptOnBehalf(ctx, bare, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)}); err != nil {
		t.Fatalf("a quote with no plan: %v", err)
	}

	_, lost, _, _ := seedQuote(t, pool, "LOST", nil)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM quote_followup_events WHERE quote_id = $1`, lost)
		_, _ = pool.Exec(context.Background(), `DELETE FROM quote_followup_plans WHERE quote_id = $1`, lost)
	})
	if _, err := NewFollowUpRepository(pool).EnsurePlan(context.Background(), lost, ChannelEmail, []int{3}, decisionNow()); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := svc.AcceptOnBehalf(ctx, lost, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)}); err == nil {
		t.Fatal("a lost quote must refuse")
	}
	if f := followUpState(t, pool, lost); !f.enabled || f.tot != 0 {
		t.Errorf("a refused decision must leave the plan alone: %+v", f)
	}
}

// The offer is the write path's own answer: the view says can_accept / can_reject exactly when the
// door would take it, for every role and every state, so a button that appears is a button that works.
func TestIntegration_StaffDecision_OfferMatchesTheWritePath(t *testing.T) {
	pool := intPool(t)
	svc := shareSvc(t, pool, decisionNow())
	roles := []string{"INSIDE_SALES", "OUTSIDE_SALES", "SALES_MANAGER", "BRANCH_MANAGER", "GENERAL_MANAGER",
		"FINANCIAL_CONTROLLER", "YARD_TEAM", "DRIVERS", "YARD_MANAGER", "HR", "PROCUREMENT_MANAGER"}
	states := []struct {
		name, status string
		validUntil   any
		empty        bool
	}{
		{"draft", "DRAFT", nil, false}, {"empty draft", "DRAFT", nil, true}, {"sent", "SENT", nil, false},
		{"sent and expired", "SENT", "2026-07-01", false}, {"accepted", "ACCEPTED", nil, false},
		{"converted", "CONVERTED", nil, false}, {"lost", "LOST", nil, false}, {"expired", "EXPIRED", nil, false},
	}
	for _, st := range states {
		for _, role := range roles {
			mk := func() string {
				_, q, _, _ := seedQuote(t, pool, st.status, st.validUntil)
				if st.empty {
					if _, err := pool.Exec(context.Background(), `DELETE FROM quote_lines WHERE quote_id=$1`, q); err != nil {
						t.Fatalf("empty: %v", err)
					}
				}
				return q
			}
			ctx := decisionCaller(role, intTrenton)
			view, err := svc.StaffQuoteRead(ctx, mk())
			if err != nil {
				t.Fatalf("%s/%s read: %v", st.name, role, err)
			}
			_, acceptErr := svc.AcceptOnBehalf(ctx, mk(), StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)})
			_, rejectErr := svc.RejectOnBehalf(ctx, mk(), StaffRejectIn{Channel: "phone", ReasonCode: "price"})
			if view.CanAccept != (acceptErr == nil) {
				t.Errorf("%s as %s: can_accept=%v but the door says %v", st.name, role, view.CanAccept, acceptErr)
			}
			if view.CanReject != (rejectErr == nil) {
				t.Errorf("%s as %s: can_reject=%v but the door says %v", st.name, role, view.CanReject, rejectErr)
			}
		}
	}
}

// - the total the customer was read -

func decAcceptAmount(t *testing.T, pool *pgxpool.Pool, quoteID string) (int64, bool) {
	t.Helper()
	var amt *int64
	err := pool.QueryRow(context.Background(), `
		SELECT (changes->>'amount_cents')::bigint FROM audit_log
		 WHERE entity_type='quote' AND entity_id=$1 AND action=$2`, quoteID, audit.ActQuoteAccepted).Scan(&amt)
	if err != nil || amt == nil {
		return 0, false
	}
	return *amt, true
}

// The quote moved after the salesperson read it to the customer: the accept is refused in words, names
// both totals, writes nothing, and works once the salesperson reads the new total.
func TestIntegration_StaffAccept_RefusesWhenTheTotalMoved(t *testing.T) {
	pool := intPool(t)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	svc := shareSvc(t, pool, decisionNow())
	ctx := decisionCaller("INSIDE_SALES", intTrenton)

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	var now int64
	if err := pool.QueryRow(context.Background(), `SELECT total_cents FROM quotes WHERE id=$1`, quoteID).Scan(&now); err != nil {
		t.Fatalf("read total: %v", err)
	}
	if now == 2000 {
		t.Fatal("fixture: the edit must move the total")
	}
	before := decAuditCount(t, pool, quoteID)

	_, err := svc.AcceptOnBehalf(ctx, quoteID, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)})
	var refused *DecisionRefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("a stale total must be refused in words, got %v", err)
	}
	for _, want := range []string{"$20.00", "$"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the totals (%q): %v", want, err)
		}
	}
	if decStatus(t, pool, quoteID) != "SENT" || decAuditCount(t, pool, quoteID) != before {
		t.Error("a refused accept must change nothing")
	}

	res, err := svc.AcceptOnBehalf(ctx, quoteID, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(now)})
	if err != nil {
		t.Fatalf("accept at the current total: %v", err)
	}
	if res.TotalCents != now {
		t.Errorf("the result must carry the total accepted: %+v", res)
	}
	if amt, ok := decAcceptAmount(t, pool, quoteID); !ok || amt != now {
		t.Errorf("the audit row must carry amount_cents %d, got %d (present %v)", now, amt, ok)
	}
}

// Raced: an edit and an accept at the same moment. Every run ends one of two ways and never a third:
// the accept lands at the total the customer was read, or it is refused and the quote is untouched.
func TestIntegration_StaffAccept_RacedAgainstAnEdit(t *testing.T) {
	pool := intPool(t)
	svc := shareSvc(t, pool, decisionNow())
	ctx := decisionCaller("INSIDE_SALES", intTrenton)
	landed, refusedRuns := 0, 0
	for i := 0; i < 16; i++ {
		_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
		start := make(chan struct{})
		var aerr, eerr error
		done := make(chan struct{}, 2)
		go func() {
			<-start
			_, aerr = svc.AcceptOnBehalf(ctx, quoteID, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, ExpectedTotalCents: i64p(2000)})
			done <- struct{}{}
		}()
		go func() {
			<-start
			_, eerr = svc.EditLineQty(ctx, quoteID, 1, "7")
			done <- struct{}{}
		}()
		close(start)
		<-done
		<-done
		var total int64
		if err := pool.QueryRow(context.Background(), `SELECT total_cents FROM quotes WHERE id=$1`, quoteID).Scan(&total); err != nil {
			t.Fatalf("read total: %v", err)
		}
		switch {
		case aerr == nil:
			landed++
			amt, ok := decAcceptAmount(t, pool, quoteID)
			if decStatus(t, pool, quoteID) != "ACCEPTED" || total != 2000 || !ok || amt != 2000 || eerr == nil {
				t.Fatalf("run %d: an accept that landed must be at the read total with the edit refused: total=%d amount=%d/%v edit=%v", i, total, amt, ok, eerr)
			}
		default:
			refusedRuns++
			if decStatus(t, pool, quoteID) != "SENT" || eerr != nil || total == 2000 {
				t.Fatalf("run %d: a refused accept must leave the edited quote SENT: status=%s total=%d edit=%v accept=%v", i, decStatus(t, pool, quoteID), total, eerr, aerr)
			}
			if _, ok := decAcceptAmount(t, pool, quoteID); ok {
				t.Fatalf("run %d: a refused accept left an acceptance row", i)
			}
		}
	}
	t.Logf("16 races: %d accepts landed, %d refused", landed, refusedRuns)
}
