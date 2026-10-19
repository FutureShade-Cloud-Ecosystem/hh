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
			assertNoMoney(t, role, m)
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
			assertNoMoney(t, "owner", m)
			return
		}
	}
	t.Errorf("the sales owner's DM does not carry the notice")
}

// assertNoMoney fails when a notice carries an amount in its words or a money field in its ref.
func assertNoMoney(t *testing.T, who string, m Message) {
	t.Helper()
	if strings.ContainsAny(m.Body, "$") || strings.Contains(m.Body, "1,250") || strings.Contains(m.Body, "125000") {
		t.Errorf("%s: the notice words carry an amount: %q", who, m.Body)
	}
	var ref map[string]any
	if err := json.Unmarshal(m.Ref, &ref); err != nil {
		t.Errorf("%s: ref: %v", who, err)
		return
	}
	for k, v := range ref {
		if strings.Contains(k, "amount") || strings.Contains(k, "cents") || strings.Contains(fmt.Sprint(v), "1,250") {
			t.Errorf("%s: the notice ref carries a money field %q=%v", who, k, v)
		}
	}
}
