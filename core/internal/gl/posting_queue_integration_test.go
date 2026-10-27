//go:build integration

package gl

// posting_queue_integration_test.go, FW1-02 sub item 3: the Controller read
// of the posting queue. Every posted topic event that is waiting or dead
// lettered lists with its topic, its document, its last error and its
// attempts, the Financial Controller alone sees the list, and the two counts
// the Money Desk's banner reads are there for the desk's managers.

import (
	"context"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

func TestIntegration_PostingQueueListsWaitingAndDeadForTheController(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	controller := identity.StaffContext{UserID: "5d1b3f6e-0000-4000-9000-0000000000aa", RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
	manager := identity.StaffContext{UserID: "5d1b3f6e-0000-4000-9000-0000000000ab", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}

	at := time.Date(2026, time.March, 12, 10, 0, 0, 0, time.UTC)
	plant := func(topic, payload, status string, attempts int, lastErr string) string {
		var id string
		if err := pool.QueryRow(ctx, `
INSERT INTO events_outbox (topic, payload, status, attempts, last_error, occurred_at)
VALUES ($1, $2::jsonb, $3, $4, $5, $6) RETURNING event_id::text`,
			topic, payload, status, attempts, lastErr, at).Scan(&id); err != nil {
			t.Fatalf("plant the outbox row: %v", err)
		}
		return id
	}
	waiting := plant("payment.received", `{"payment_id":"p-fw1-02-q"}`, "PENDING", 2, "gl: no fiscal period for 2027-01")
	dead := plant("ar.invoice.finalized", `{"invoice_id":"i-fw1-02-q"}`, "DEAD", 5, "gl: account \"2200\" is inactive; cannot post")
	// A topic the GL does not own never lists, whatever its state.
	plant("crm.NewPortalContact", `{"x":"1"}`, "DEAD", 5, "no handler registered for topic")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM events_outbox WHERE event_id = ANY($1::uuid[])`, []string{waiting, dead})
	})

	svc := NewService(NewRepository(pool)).
		WithPostingQueue(notify.NewPgOutboxStore(pool)).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return controller, true })

	rows, err := svc.PostingQueue(ctx)
	if err != nil {
		t.Fatalf("the queue read: %v", err)
	}
	var sawWaiting, sawDead bool
	for _, r := range rows {
		switch r.EventID {
		case waiting:
			sawWaiting = true
			if r.Topic != "payment.received" || r.Document != "p-fw1-02-q" || r.Status != "PENDING" ||
				r.Attempts != 2 || r.LastError != "gl: no fiscal period for 2027-01" {
				t.Fatalf("the waiting row: %+v", r)
			}
		case dead:
			sawDead = true
			if r.Topic != "ar.invoice.finalized" || r.Document != "i-fw1-02-q" || r.Status != "DEAD" ||
				r.Attempts != 5 || r.LastError != "gl: account \"2200\" is inactive; cannot post" {
				t.Fatalf("the dead row: %+v", r)
			}
		}
	}
	if !sawWaiting || !sawDead {
		t.Fatalf("the queue did not list both rows (waiting %v, dead %v) of %d rows", sawWaiting, sawDead, len(rows))
	}

	// The counts the banner reads: the same two figures, no documents.
	counts, err := svc.PostingQueueCounts(ctx)
	if err != nil {
		t.Fatalf("the counts read: %v", err)
	}
	if counts.Waiting < 1 || counts.Dead < 1 {
		t.Fatalf("the counts: %+v", counts)
	}

	// The list is the Controller's alone; the counts are not.
	managerSvc := NewService(NewRepository(pool)).
		WithPostingQueue(notify.NewPgOutboxStore(pool)).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return manager, true })
	if _, err := managerSvc.PostingQueue(ctx); err == nil {
		t.Fatalf("a non controller read the posting queue list")
	}
	if _, err := managerSvc.PostingQueueCounts(ctx); err != nil {
		t.Fatalf("the counts must answer a manager: %v", err)
	}
}

func TestIntegration_PostingQueueCounts_SkipsFreshUnattemptedPending(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	controller := identity.StaffContext{UserID: "5d1b3f6e-0000-4000-9000-0000000000aa", RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}

	svc := NewService(NewRepository(pool)).
		WithPostingQueue(notify.NewPgOutboxStore(pool)).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return controller, true })

	before, err := svc.PostingQueueCounts(ctx)
	if err != nil {
		t.Fatalf("baseline counts: %v", err)
	}

	now := time.Now().UTC()
	plantAt := func(topic, payload, status string, attempts int, lastErr string, at time.Time) string {
		var id string
		if err := pool.QueryRow(ctx, `
INSERT INTO events_outbox (topic, payload, status, attempts, last_error, occurred_at)
VALUES ($1, $2::jsonb, $3, $4, $5, $6) RETURNING event_id::text`,
			topic, payload, status, attempts, lastErr, at).Scan(&id); err != nil {
			t.Fatalf("plant outbox row: %v", err)
		}
		return id
	}
	fresh := plantAt("payment.received", `{"payment_id":"p-fw1-02-fresh"}`, "PENDING", 0, "", now)
	retried := plantAt("payment.received", `{"payment_id":"p-fw1-02-retried"}`, "PENDING", 1, "gl: transient", now)
	stale := plantAt("payment.received", `{"payment_id":"p-fw1-02-stale"}`, "PENDING", 0, "", now.Add(-6*time.Minute))
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM events_outbox WHERE event_id = ANY($1::uuid[])`, []string{fresh, retried, stale}); err != nil {
			t.Errorf("cleanup outbox rows: %v", err)
		}
	})

	after, err := svc.PostingQueueCounts(ctx)
	if err != nil {
		t.Fatalf("after counts: %v", err)
	}
	if got := after.Waiting - before.Waiting; got != 2 {
		t.Fatalf("a fresh unattempted PENDING row was counted on the banner: waiting delta = %d, want 2 (only attempts > 0 or age >= 5m)", got)
	}
}

func TestIntegration_PostingQueueCounts_RefusesBranchScopedManager(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	branchManager := identity.StaffContext{
		UserID:   "5d1b3f6e-0000-4000-9000-0000000000ac",
		RoleCode: "BRANCH_MANAGER",
		Tier:     identity.TierManager,
		Scope:    identity.ScopeBranch,
	}

	svc := NewService(NewRepository(pool)).
		WithPostingQueue(notify.NewPgOutboxStore(pool)).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return branchManager, true })

	if _, err := svc.PostingQueueCounts(ctx); err != errForbidden {
		t.Fatalf("a branch scoped manager read the org wide posting queue counts: err = %v, want %v", err, errForbidden)
	}
}


