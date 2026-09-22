//go:build integration

// DIB-462 integration tests. These prove the half of the feature that lives in SQL and CANNOT be
// proven with a double:
//
//	the FACTS query really does resolve a payment to its order, that order's branch and its customer,
//	  through payment_allocations → invoices → orders on real rows;
//	"nobody took it at a counter" really is what the till_session_id / received_by columns say;
//	the fan-out really does return only ENABLED rows for ACTIVE users;
//	the rule rows really are absent by default, so a freshly-migrated database notifies NOBODY.
//
// Every fixture restores exactly what it created (DIB-396 discipline), so the package stays green
// under -shuffle=on and beside a live server draining the outbox.
//
// Run: cd backend && DATABASE_URL=... go test -tags integration ./internal/messaging/... -v
package messaging

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A seeded order at TRENTON and its customer (cmd/seed/dibbits_seed.sql).
const (
	itOrderTrenton   = "df000000-0000-4000-9000-0000000000d1"
	itCustContractor = "df000000-0000-4000-9000-000000000001"
)

// seedPayment writes one payment (and optionally its allocation to an invoice on the seeded order),
// returning its id. atCounter stamps received_by, which is what makes it NOT online.
func seedPayment(t *testing.T, pool *pgxpool.Pool, branchID string, cents int64, atCounter bool, allocateToOrder bool) string {
	t.Helper()
	ctx := context.Background()
	var receivedBy any
	if atCounter {
		receivedBy = uSamRivera
	}
	var paymentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, received_by, gateway_ref)
		VALUES ('PMT-IT-' || substr(gen_random_uuid()::text, 1, 8), $1::uuid, 'EFT', $2, 'RECEIVED', $3::uuid, $4::uuid, 'itest')
		RETURNING id::text`, itCustContractor, cents, branchID, receivedBy).Scan(&paymentID); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_allocations WHERE payment_id = $1::uuid`, paymentID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, paymentID)
	})

	if allocateToOrder {
		var invoiceID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents)
			VALUES ('INV-IT-' || substr(gen_random_uuid()::text, 1, 8), $1::uuid, $2::uuid, $3::uuid, 'FINALIZED',
			        CURRENT_DATE + 30, $4, 0, $4, $4)
			RETURNING id::text`, itCustContractor, itOrderTrenton, branchTrenton, cents).Scan(&invoiceID); err != nil {
			t.Fatalf("seed invoice: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM payment_allocations WHERE invoice_id = $1::uuid`, invoiceID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM invoices WHERE id = $1::uuid`, invoiceID)
		})
		if _, err := pool.Exec(ctx, `
			INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents) VALUES ($1::uuid, $2::uuid, $3)`,
			paymentID, invoiceID, cents); err != nil {
			t.Fatalf("seed allocation: %v", err)
		}
	}
	return paymentID
}

// clearRules removes the rule rows a fixture wrote, and asserts nothing else, because the table is
// meant to be empty on a fresh database.
func clearRules(t *testing.T, pool *pgxpool.Pool, userIDs ...string) {
	t.Helper()
	wipe := func() {
		for _, u := range userIDs {
			_, _ = pool.Exec(context.Background(), `DELETE FROM msg_payment_notify_rules WHERE user_id = $1::uuid`, u)
		}
	}
	wipe()
	t.Cleanup(wipe)
}

// - THE DEFAULT, IN SQL -

// A freshly-migrated database notifies NOBODY. 0157 seeds no rows on purpose, and this is the
// assertion that says so out loud: every rule's recipient list is empty until a person opts in.
func TestIntegration_PaymentRules_AreOffForEveryoneUntilSomebodyOptsIn(t *testing.T) {
	pool := intPool(t)
	clearRules(t, pool, uSamRivera, uAlexMorgan)
	repo := NewRepository(pool)
	ctx := context.Background()

	for _, rule := range PaymentRules {
		users, err := repo.UsersWithPaymentRule(ctx, rule)
		if err != nil {
			t.Fatalf("recipients for %s: %v", rule, err)
		}
		if len(users) != 0 {
			t.Fatalf("rule %s must have NO recipients before anybody opts in, got %v", rule, users)
		}
	}

	stored, err := repo.PaymentRulesFor(ctx, uSamRivera)
	if err != nil {
		t.Fatalf("rules for user: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("a user who has never visited the prefs surface has no rows, got %v", stored)
	}
}

// The toggle round-trips, an explicit OFF is stored (not deleted), and an OFF row is not a recipient.
func TestIntegration_PaymentRules_ToggleRoundTripsAndOffIsStored(t *testing.T) {
	pool := intPool(t)
	clearRules(t, pool, uSamRivera)
	repo := NewRepository(pool)
	ctx := context.Background()

	if _, err := repo.SetPaymentRule(ctx, uSamRivera, RulePaymentOnline, true); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	users, err := repo.UsersWithPaymentRule(ctx, RulePaymentOnline)
	if err != nil {
		t.Fatalf("recipients: %v", err)
	}
	if len(users) != 1 || users[0] != uSamRivera {
		t.Fatalf("the opted-in user must be the ONLY recipient, got %v", users)
	}

	stored, err := repo.SetPaymentRule(ctx, uSamRivera, RulePaymentOnline, false)
	if err != nil {
		t.Fatalf("opt back out: %v", err)
	}
	if v, ok := stored[RulePaymentOnline]; !ok || v {
		t.Fatalf("an opt-out must be STORED as false, not deleted: got %v (present=%v)", v, ok)
	}
	after, err := repo.UsersWithPaymentRule(ctx, RulePaymentOnline)
	if err != nil {
		t.Fatalf("recipients after opt-out: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("a stored OFF must notify nobody, got %v", after)
	}
}

// - THE FACTS QUERY, ON REAL ROWS -

// The ONLINE fact is the columns, not a guess: a payment with no till session and no received_by is
// remote; stamping a receiving staffer makes the same row a counter payment.
func TestIntegration_PaymentFacts_OnlineIsTheAbsenceOfAHuman(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	remote := seedPayment(t, pool, branchTrenton, 124000, false, false)
	facts, err := repo.PaymentNotifyFacts(ctx, remote)
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if !facts.Found {
		t.Fatal("a payment that exists must resolve")
	}
	if facts.TakenAtCounter {
		t.Fatal("no till session and no received_by means nobody took it at a counter")
	}
	if !matchesOnline(facts) {
		t.Fatal("that is the ONLINE case")
	}

	counter := seedPayment(t, pool, branchTrenton, 5000, true, false)
	cFacts, err := repo.PaymentNotifyFacts(ctx, counter)
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if !cFacts.TakenAtCounter || matchesOnline(cFacts) {
		t.Fatal("a payment with a receiving staffer is NOT online")
	}
}

// The order is resolved through the real allocation chain, and with it the order's branch, which is
// what makes the cross-branch matcher able to say anything at all.
func TestIntegration_PaymentFacts_ResolveTheOrderThroughAllocations(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	// Taken at KINGSTON, allocated to an invoice on a TRENTON order.
	crossID := seedPayment(t, pool, branchKingston, 124000, true, true)
	facts, err := repo.PaymentNotifyFacts(ctx, crossID)
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if facts.OrderID != itOrderTrenton {
		t.Fatalf("the payment must resolve to its order through payment_allocations → invoices, got %q", facts.OrderID)
	}
	if facts.OrderBranchID != branchTrenton || facts.TakenBranchID != branchKingston {
		t.Fatalf("both branches must resolve: taken=%q order=%q", facts.TakenBranchID, facts.OrderBranchID)
	}
	if !facts.OnStatement {
		t.Fatal("a payment allocated to an invoice landed on the account")
	}
	if facts.CustomerName == "" || facts.OrderNo == "" || facts.TakenBranchName == "" || facts.OrderBranch == "" {
		t.Fatalf("the notice needs names, not ids, to be readable: %+v", facts)
	}
	// SHORT branch names. The legal name is "Dibbits Landscape Supply, Kingston"; a notice that
	// spells that out twice in one sentence buries the two words the reader needs. The city half is
	// the same part of the same string the 0136 house-slug convention derives from.
	if facts.TakenBranchName != "Kingston" || facts.OrderBranch != "Trenton" {
		t.Fatalf("branch names must read as cities, got taken=%q order=%q", facts.TakenBranchName, facts.OrderBranch)
	}
	notice := renderPaymentNotice(facts, []string{RulePaymentCrossBranch})
	if !strings.Contains(notice, "Taken at Kingston; the order is Trenton's.") {
		t.Fatalf("the sentence a person reads: %q", notice)
	}
	if !matchesCrossBranch(facts) {
		t.Fatal("Kingston taking money on a Trenton order IS the cross-branch case, on real rows")
	}

	// The same shape at the SAME branch must not match, the negative on real data, which is where a
	// mutated matcher would otherwise hide.
	sameID := seedPayment(t, pool, branchTrenton, 5000, true, true)
	same, err := repo.PaymentNotifyFacts(ctx, sameID)
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if matchesCrossBranch(same) {
		t.Fatal("a Trenton payment on a Trenton order is not cross-branch")
	}
}

// A payment with no allocation has no order, and the matchers say so rather than inventing one.
func TestIntegration_PaymentFacts_NoAllocationMeansNoOrder(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)

	id := seedPayment(t, pool, branchKingston, 7500, false, false)
	facts, err := repo.PaymentNotifyFacts(context.Background(), id)
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if facts.OrderID != "" || facts.OnStatement {
		t.Fatalf("an unallocated payment has no order and is not on a statement: %+v", facts)
	}
	if matchesCrossBranch(facts) {
		t.Fatal("with no order there is no branch to differ from")
	}
	if matchesOwnOrder(facts, uSamRivera, uSamRivera) {
		t.Fatal("with no order there is nothing to own")
	}
	if !matchesOnline(facts) {
		t.Fatal("it is still an online payment")
	}
}

// An unknown payment id is Found=false and NO error, an event about a payment this database cannot
// see must not put the outbox row into a retry loop over a fact that will never arrive.
func TestIntegration_PaymentFacts_UnknownPaymentIsNotAnError(t *testing.T) {
	pool := intPool(t)
	facts, err := NewRepository(pool).PaymentNotifyFacts(context.Background(),
		"00000000-0000-4000-9000-0000000000ff")
	if err != nil {
		t.Fatalf("an unknown payment must not error: %v", err)
	}
	if facts.Found {
		t.Fatal("an unknown payment must not report as found")
	}
}

// - END TO END, THROUGH THE REAL CONSUMER -

// One cross-branch payment, one opted-in user, real rows on both sides: the rollup lands in the
// payments channel AND the notice lands in that user's DM, and in nobody else's.
func TestIntegration_PaymentRules_CrossBranchNoticeLandsInTheOptedInUsersDM(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton, chPaymentsGlobal)
	clearRules(t, pool, uSamRivera, uAlexMorgan)
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo)

	if _, err := repo.SetPaymentRule(ctx, uSamRivera, RulePaymentCrossBranch, true); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	paymentID := seedPayment(t, pool, branchKingston, 124000, true, true)
	eventKey := "itest-rule-" + paymentID
	dmID, err := repo.EnsureRepDMChannel(ctx, uSamRivera)
	if err != nil {
		t.Fatalf("ensure dm: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM msg_messages WHERE event_key = $1`, paymentNoticeKey(eventKey, uSamRivera))
	})

	res, err := svc.NotifyPaymentRules(ctx, eventKey, paymentID, EventRef{Topic: TopicPaymentReceived})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(res.Notices) != 1 || res.Notices[0].UserID != uSamRivera {
		t.Fatalf("exactly one recipient expected, got %+v", res.Notices)
	}

	var body string
	if err := pool.QueryRow(ctx, `
		SELECT body FROM msg_messages WHERE channel_id = $1::uuid AND event_key = $2`,
		dmID, paymentNoticeKey(eventKey, uSamRivera)).Scan(&body); err != nil {
		t.Fatalf("read the delivered notice: %v", err)
	}
	if !strings.Contains(body, "$1,240.00") || !strings.Contains(body, "Taken at") {
		t.Fatalf("the notice must state the money and the reason, got %q", body)
	}

	// Nobody else. The GM has not opted into anything.
	var others int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM msg_messages WHERE event_key = $1`,
		paymentNoticeKey(eventKey, uAlexMorgan)).Scan(&others); err != nil {
		t.Fatalf("count other notices: %v", err)
	}
	if others != 0 {
		t.Fatalf("a user who opted into nothing must receive nothing, got %d", others)
	}

	// Redelivery is a no-op: the same key, the same one row.
	if _, err := svc.NotifyPaymentRules(ctx, eventKey, paymentID, EventRef{Topic: TopicPaymentReceived}); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM msg_messages WHERE event_key = $1`,
		paymentNoticeKey(eventKey, uSamRivera)).Scan(&n); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if n != 1 {
		t.Fatalf("redelivery must not duplicate the notice, got %d rows", n)
	}
}

// The whole point, in one assertion on real rows: with every rule ON for a user, a MATCHING payment
// still produces exactly the DIB-449 rollup, one day row in the channel, the payment as its reply.
// The notice is additive; it does not stand in for the day line and it does not suppress it.
func TestIntegration_PaymentRules_DoNotDisturbTheRollup(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton)
	clearRules(t, pool, uSamRivera)
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo)
	for _, rule := range PaymentRules {
		if _, err := repo.SetPaymentRule(ctx, uSamRivera, rule, true); err != nil {
			t.Fatalf("opt in %s: %v", rule, err)
		}
	}
	paymentID := seedPayment(t, pool, branchKingston, 124000, false, true)
	eventKey := "itest-undisturbed-" + paymentID
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE event_key LIKE $1`,
			"pay-rule:"+eventKey+":%")
	})

	body, ref := render(TopicPaymentReceived, eventPayload{AmountText: dollars(124000)})
	ref.Topic = TopicPaymentReceived
	if _, err := svc.PostRoutineEvent(ctx, "payments-trenton", eventKey, body, ref, 124000, testDay); err != nil {
		t.Fatalf("post routine: %v", err)
	}
	if _, err := svc.NotifyPaymentRules(ctx, eventKey, paymentID, ref); err != nil {
		t.Fatalf("notify: %v", err)
	}

	if n := rollupRoots(t, pool, chPaymentsTrenton); n != 1 {
		t.Fatalf("the day must still be ONE row, got %d", n)
	}
	_, line, _, _, replies := rollupRow(t, pool, chPaymentsTrenton)
	if replies != 1 {
		t.Fatalf("the payment must still be a thread reply, got %d", replies)
	}
	if want := dayLine(1, "$1,240.00"); line != want {
		t.Fatalf("the day's line = %q, want %q", line, want)
	}
}
