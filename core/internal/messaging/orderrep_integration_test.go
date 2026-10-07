//go:build integration

// An order notice against the real rep routing: the rep's DM when there is a rep, a named fallback
// channel with the fallback sentence when there is none.
//
// Run with: DATABASE_URL=... go test -tags integration -count=1 -run OrderRep ./internal/messaging/

package messaging

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIntegration_NotifyOrderRep_RepDMThenCounterFallback(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	var cust string
	if err := pool.QueryRow(ctx, `INSERT INTO customers (account_no, name) VALUES ($1, 'Order rep notice customer') RETURNING id::text`,
		fmt.Sprintf("ORN-%d", time.Now().UnixNano())).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	keyRep := fmt.Sprintf("orn-rep-%d", time.Now().UnixNano())
	keyNone := fmt.Sprintf("orn-none-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM msg_messages WHERE event_key IN ($1, $2)`, keyRep, keyNone)
		_, _ = pool.Exec(c, `DELETE FROM rep_assignments WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})

	// No rep: the notice lands in the named fallback and says why.
	prefix := "No sales rep is on this order, so this came to the counter. "
	res, err := svc.NotifyOrderRep(ctx, OrderRepNotice{
		CustomerID: cust, EventKey: keyNone, Body: "Short pick on order #X.",
		FallbackSlugs: []string{"no-such-channel", "yard-trenton"}, FallbackPrefix: prefix,
	})
	if err != nil || res.Kind != RouteCounter || res.ChannelID != chYardTrenton {
		t.Fatalf("with no rep the notice goes to the first channel that exists: %+v %v", res, err)
	}
	var body string
	if err := pool.QueryRow(ctx, `SELECT body FROM msg_messages WHERE event_key = $1`, keyNone).Scan(&body); err != nil || !strings.HasPrefix(body, prefix) {
		t.Fatalf("the stored message must carry the fallback sentence: %q %v", body, err)
	}

	// A rep: the notice lands in their DM, with no fallback sentence.
	if _, err := repo.UpsertRepAssignment(ctx, RepAssignment{CustomerID: cust, RepUserID: uSamRivera, Source: AssignManual}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	res, err = svc.NotifyOrderRep(ctx, OrderRepNotice{
		CustomerID: cust, EventKey: keyRep, Body: "Short pick on order #X.",
		FallbackSlugs: []string{"yard-trenton"}, FallbackPrefix: prefix,
	})
	if err != nil || res.Kind != RouteRepDM || res.RepUserID != uSamRivera || !res.Inserted {
		t.Fatalf("with a rep the notice goes to their DM: %+v %v", res, err)
	}
	if err := pool.QueryRow(ctx, `SELECT body FROM msg_messages WHERE event_key = $1 AND channel_id = $2`, keyRep, res.ChannelID).Scan(&body); err != nil || body != "Short pick on order #X." {
		t.Fatalf("the rep reads the notice as written: %q %v", body, err)
	}
}
