//go:build integration

// credit_hold_notice_integration_test.go: a credit hold is a notice for sales, not money room
// content. The money rooms seat the money desk only, so the hold must reach the customer's sales
// owner and the branch's sales roles in the rooms they already hold, naming no amount.

package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

func TestIntegration_CreditHold_ReachesSalesWithNoAmounts(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	cons := NewConsumer(svc, nil)

	stamp := time.Now().UnixNano()
	name := fmt.Sprintf("Summit Ridge Builders %d", stamp)
	var cust string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, home_branch_id) VALUES ($1, $2, $3) RETURNING id::text`,
		fmt.Sprintf("CHN-%d", stamp), name, branchTrenton).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if _, err := repo.UpsertRepAssignment(ctx, RepAssignment{CustomerID: cust, RepUserID: uSamRivera, Source: AssignManual}); err != nil {
		t.Fatalf("assign the sales owner: %v", err)
	}
	eventID := fmt.Sprintf("credit-hold-%d", stamp)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM msg_messages WHERE event_key LIKE $1`, eventID+"%")
		_, _ = pool.Exec(c, `DELETE FROM rep_assignments WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})

	// The four sales personas, each granted Trenton and synced as a sign in syncs them.
	personas := []string{"INSIDE_SALES", "OUTSIDE_SALES", "SALES_MANAGER", "BRANCH_MANAGER"}
	ids := map[string]string{}
	for i, role := range personas {
		id := plantMoneyProbe(t, ctx, pool, fmt.Sprintf("credit-hold-%d-%d@fixture.test", stamp, i), role)
		ids[role] = id
		if err := repo.SyncMembership(ctx, id); err != nil {
			t.Fatalf("sync %s: %v", role, err)
		}
	}

	// payment.bounced is the raw event the ledger emits; 1,250.00 must not appear anywhere.
	payload, _ := json.Marshal(rawPayload{PaymentID: "p-" + eventID, CustomerID: cust, BranchID: branchTrenton, AmountCents: 125000})
	if err := cons.handleRaw(ctx, notify.Event{EventID: eventID, Topic: RawPaymentBounced, Payload: payload}); err != nil {
		t.Fatalf("route the credit hold: %v", err)
	}

	var house string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM msg_channels WHERE slug = 'house-trenton'`).Scan(&house); err != nil {
		t.Fatalf("the house room: %v", err)
	}
	for _, role := range personas {
		page, err := svc.GetMessages(ctx, Caller{UserID: ids[role], RoleCode: role, Scope: "GLOBAL"}, house, "", 100)
		if err != nil {
			t.Fatalf("%s reads the house room: %v", role, err)
		}
		found := false
		for _, m := range page.Messages {
			if !strings.Contains(m.Body, name) {
				continue
			}
			found = true
			assertNoMoney(t, role, name, m)
		}
		if !found {
			t.Errorf("%s did not receive the credit hold notice in #house-trenton", role)
		}
	}

	// The sales owner also gets it in their rep DM.
	var dm string
	if err := pool.QueryRow(ctx, `
		SELECT m.channel_id::text FROM msg_messages m WHERE m.event_key = $1`, eventID+":sales-owner").Scan(&dm); err != nil {
		t.Fatalf("the sales owner's notice: %v", err)
	}
	page, err := svc.GetMessages(ctx, Caller{UserID: uSamRivera, RoleCode: "SALES_MANAGER", Scope: "BRANCH"}, dm, "", 100)
	if err != nil {
		t.Fatalf("the owner reads the rep DM: %v", err)
	}
	for _, m := range page.Messages {
		if strings.Contains(m.Body, name) {
			assertNoMoney(t, "owner", name, m)
			return
		}
	}
	t.Errorf("the sales owner's DM does not carry the notice")
}

// assertNoMoney lives in credit_hold_notice_money_test.go, untagged, so its unit leg runs in the
// untagged suite. It reads the notice's OWN words: the body is the customer's name and a fixed
// sentence, and the seed puts the clock's nanoseconds in the name, so the name is taken out of
// the haystack before the hunt (FLAKE-10).
