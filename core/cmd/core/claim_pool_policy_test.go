//go:build integration

package main

// claim_pool_policy_test.go pins the sms claim store's send policy (site 23). The worker's sms
// dispatcher drains through a second Outbox built over the claim pool, and the quiet hours and the
// rate gate live on that Outbox's SendPolicy: if buildClaimStores stopped attaching the policy, the
// dispatcher would claim and send at any hour. The leg builds the claim stores through the
// production buildClaimStores with a real SendPolicy whose clock the leg moves: at 03:00 in the
// house zone the window is closed and a due row must stay PENDING with attempts 0 (the gate runs
// before the first claim), then at noon the window is open and the same row drains.
//
// Like the other claim pool legs it back-dates its own row by a year, claims one row per batch, and
// needs a migrated database with no foreign PENDING sms_outbox row older than that (it would be
// claimed instead). Skips without DATABASE_URL.

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messagingprovider"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

func TestClaimPool_SmsClaimStoreKeepsTheSendPolicy(t *testing.T) {
	url := claimLegURL(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	shared := claimLegPool(t, url, claimLegPoolSize)
	helper := claimLegPool(t, url, 2)

	loc, err := messagingprovider.LoadZone("")
	if err != nil {
		t.Fatalf("house zone: %v", err)
	}
	now := time.Date(2031, time.March, 12, 3, 0, 0, 0, loc) // quiet hours: the window is 08:00 to 20:00
	policy := messagingprovider.NewSendPolicy().WithClock(func() time.Time { return now })

	var eventID string
	if err := helper.QueryRow(ctx, `INSERT INTO sms_outbox (topic, payload, occurred_at)
		VALUES ($1, $2::jsonb, now() - interval '1 year') RETURNING event_id::text`,
		messagingprovider.TopicARNotice,
		`{"invoice_id":"00000000-0000-4000-8000-0000000000aa","customer_id":"00000000-0000-4000-8000-0000000000bb"}`).Scan(&eventID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = helper.Exec(context.Background(), `DELETE FROM sms_outbox WHERE event_id = $1::uuid`, eventID)
	})

	cs, err := buildClaimStores(ctx, shared, policy, log)
	if err != nil {
		t.Fatalf("buildClaimStores: %v", err)
	}
	t.Cleanup(cs.Close)
	d := notify.NewDispatcher(&gatedStore{inner: cs.sms, claimed: make(chan struct{})}, log)
	resolver := messagingprovider.NewConsentResolver(messagingprovider.NewPrefsStore(shared), log)
	messagingprovider.NewConsumer(notify.NewLogNotifier(log), resolver, log).RegisterHandlers(d)

	state := func(p *pgxpool.Pool) (string, int) {
		var status string
		var attempts int
		if err := p.QueryRow(ctx, `SELECT status, attempts FROM sms_outbox WHERE event_id = $1::uuid`, eventID).Scan(&status, &attempts); err != nil {
			t.Fatalf("read row: %v", err)
		}
		return status, attempts
	}

	if n := d.DrainOnce(ctx); n != 0 {
		t.Fatalf("closed window (03:00): the drain handled %d rows, want 0: the claim store lost its send policy", n)
	}
	if status, attempts := state(helper); status != "PENDING" || attempts != 0 {
		t.Fatalf("closed window (03:00): row is %s with %d attempts, want PENDING and 0: the claim store lost its send policy", status, attempts)
	}

	now = time.Date(2031, time.March, 12, 12, 0, 0, 0, loc) // the window is open
	if n := d.DrainOnce(ctx); n != 1 {
		t.Fatalf("open window (12:00): the drain handled %d rows, want 1", n)
	}
	if status, _ := state(helper); status != "PUBLISHED" {
		t.Errorf("open window (12:00): row is %s, want PUBLISHED", status)
	}
}
