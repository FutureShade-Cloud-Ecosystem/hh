//go:build integration

// d110_pickupcard_integration_test.go, D-110, proved against a real Postgres.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run D110 ./internal/dispatch/
//
// THE RULING. "The 'customer pickups' quick card counts OPEN PICKUPS SPLIT BY STAGE (awaiting pick /
// ready to collect), not live codes."
//
// WHAT ONLY A DATABASE CAN PROVE, AND IS THEREFORE PROVED HERE. The acceptance the item is written
// against is not "the numbers look right", it is that **the card's two numbers match the pick
// queue's population exactly**. That is a statement about two different SQL reads agreeing, and the
// only way to make it is to run both against the same rows and compare the answers. A fake would be
// asserting that two hand-written fixtures agree, which is the very failure the card had.
//
// FIXTURES RESTORE WHAT THEY TOUCH. Orders come from seedPickupOrder (whose cleanup is the suite's);
// the pick tasks this file writes are deleted on cleanup. No whole-DB invariant is transiently broken
// (no stock moves, no payment_config), so no DIB-396 advisory lease is taken.

package dispatch

import (
	"context"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// d110PickState grades a population off the REAL tasks table, mirroring the composition root's
// adapter (main.go's dispatchPickStateAdapter) including its one judgement: STAGED and DONE both mean
// the material was pulled. It is written here rather than imported because dispatch does not import
// tasks, which is the whole reason the seam exists.
type d110PickState struct{ pool *pgxpool.Pool }

func (a d110PickState) OrderPickState(ctx context.Context, orderID string) (PickCompletion, error) {
	m, err := a.OrderPickStates(ctx, []string{orderID})
	if err != nil {
		return PickCompletion{}, err
	}
	return m[orderID], nil
}

func (a d110PickState) OrderPickStates(ctx context.Context, orderIDs []string) (map[string]PickCompletion, error) {
	out := map[string]PickCompletion{}
	if len(orderIDs) == 0 {
		return out, nil
	}
	rows, err := a.pool.Query(ctx, `
SELECT DISTINCT ON (order_id) order_id::text, status
  FROM tasks
 WHERE order_id = ANY($1::uuid[]) AND task_type = 'PICK' AND status <> 'CANCELLED'
 ORDER BY order_id, created_at DESC`, orderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		out[id] = PickCompletion{Exists: true, Staged: status == "STAGED" || status == "DONE", Status: status}
	}
	return out, rows.Err()
}

// seedPickTask puts a PICK task on an order at a status and cleans it up.
func seedPickTask(t *testing.T, pool *pgxpool.Pool, orderID, branchID, status string) {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `
INSERT INTO tasks (task_type, order_id, branch_id, priority, status, source)
VALUES ('PICK', $1::uuid, $2::uuid, 'NORMAL', $3, 'MANUAL') RETURNING id::text`,
		orderID, branchID, status).Scan(&id); err != nil {
		t.Fatalf("seed pick task (%s): %v", status, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM tasks WHERE id = $1::uuid`, id) })
}

func d110Service(t *testing.T, pool *pgxpool.Pool) *Service {
	t.Helper()
	return NewService(NewRepository(pool)).
		WithPickState(d110PickState{pool: pool}).
		withCaller(func(context.Context) (identity.StaffContext, bool) {
			return identity.StaffContext{
				UserID: "u-d110", RoleCode: "LOGISTICS_MANAGER", Tier: identity.TierManager,
				Scope: identity.ScopeGlobal, Status: identity.StatusActive,
			}, true
		})
}

// TestD110_TheCardsTwoNumbersAreTheQueuesPopulationExactly is the item's acceptance, as a test.
//
// It builds three open pickups at one branch, one nobody has touched, one being picked, one staged -
// then asserts THREE things that have to be true together:
//
//  1. the split is right: two awaiting pick, one ready to collect;
//  2. the card's total is the sum, because the setter computes it (SetPickupCounts);
//  3. the total is EXACTLY the size of the pick queue's own pickup population, the same predicate,
//     read through the feed rather than the card.
//
// (3) is the one that matters. (1) and (2) can both be true of a card counting the wrong population,
// which is precisely what the shipped card did: it counted live rack-tag CODES, so a pickup nobody
// had pulled, having no code, was invisible to it.
func TestD110_TheCardsTwoNumbersAreTheQueuesPopulationExactly(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := d110Service(t, pool)

	// A branch of its own would be ideal, but branches are seeded and shared; instead the test
	// measures the DELTA it causes, so whatever else the branch already holds cannot perturb it.
	beforeAwaiting, beforeReady, _, err := svc.PendingPickupCounts(ctx, branchKGN)
	if err != nil {
		t.Fatalf("baseline count: %v", err)
	}

	untouched := seedPickupOrder(t, pool, branchKGN, "WILL_CALL", "CONFIRMED", inThreeDays())
	seedPickLines(t, pool, untouched, 1)

	beingPicked := seedPickupOrder(t, pool, branchKGN, "PICKUP", "CONFIRMED", inThreeDays())
	seedPickLines(t, pool, beingPicked, 1)
	seedPickTask(t, pool, beingPicked, branchKGN, "IN_PROGRESS")

	staged := seedPickupOrder(t, pool, branchKGN, "WILL_CALL", "READY", inThreeDays())
	seedPickLines(t, pool, staged, 1)
	seedPickTask(t, pool, staged, branchKGN, "STAGED")

	awaiting, ready, capped, err := svc.PendingPickupCounts(ctx, branchKGN)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if capped {
		t.Fatalf("three pickups must not hit the population cap")
	}

	// 1 · THE SPLIT. A pick that is IN_PROGRESS is a promise, not a pallet, it belongs on the
	// awaiting side, exactly as the rack-tag gate treats it.
	if got := awaiting - beforeAwaiting; got != 2 {
		t.Fatalf("awaiting pick moved by %d, want 2 (untouched + in-progress)", got)
	}
	if got := ready - beforeReady; got != 1 {
		t.Fatalf("ready to collect moved by %d, want 1 (the staged one)", got)
	}

	// 2 · THE CARD'S TOTAL IS THE SUM, and it is computed by the setter rather than passed in, so the
	// card and its split are arithmetically incapable of disagreeing.
	var counts QuickCounts
	counts.SetPickupCounts(awaiting, ready, capped)
	if counts.Pickups != awaiting+ready {
		t.Fatalf("the card must be the sum of its split: %d != %d + %d", counts.Pickups, awaiting, ready)
	}

	// 3 · THE POPULATION IS THE QUEUE'S. The feed's pickup half is read through a completely different
	// path, PickFeedPendingPickups, building whole tickets, and the card's total must equal the
	// number of pickup tickets it produces for the same branch. This is the assertion the old card
	// could never have passed.
	feed := pullFeedFor(t, pool)
	tickets, _, err := feed.PendingTickets(ctx, branchKGN, 200, 0)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	inQueue := 0
	for _, tk := range tickets {
		if tk.Fulfillment == "PICKUP" || tk.Fulfillment == "WILL_CALL" {
			inQueue++
		}
	}
	if counts.Pickups != inQueue {
		t.Fatalf(`the card and the queue disagree about how many customer pickups there are.
This is the defect D-110 names: the card must be counted FROM the pick-queue population, not from a
second population that happens to be similar.
  card:  %d   (awaiting %d + ready %d)
  queue: %d pickup tickets`, counts.Pickups, awaiting, ready, inQueue)
	}
}

// TestD110_AnUnpulledPickupIsVisibleWithNoRackTagAnywhere is the defect, stated as its own test.
//
// The card it replaces counted `will_call_pickups` rows. The mint gate refuses to create one until a
// pick is STAGED, so a pickup that nobody has pulled has no row and was worth zero to the old card -
// while being exactly the pickup a yard lead needs in their face. Nothing in this test mints
// anything, and the count must still see it.
func TestD110_AnUnpulledPickupIsVisibleWithNoRackTagAnywhere(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := d110Service(t, pool)

	before, _, _, err := svc.PendingPickupCounts(ctx, branchKGN)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	order := seedPickupOrder(t, pool, branchKGN, "WILL_CALL", "CONFIRMED", inThreeDays())
	seedPickLines(t, pool, order, 1)

	var codes int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM will_call_pickups WHERE order_id = $1::uuid`, order).Scan(&codes); err != nil {
		t.Fatalf("count codes: %v", err)
	}
	if codes != 0 {
		t.Fatalf("this fixture must mint no rack tag, that is the whole point (%d found)", codes)
	}

	after, _, _, err := svc.PendingPickupCounts(ctx, branchKGN)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if after-before != 1 {
		t.Fatalf(`a pickup with no rack tag must still be counted as awaiting pick (moved by %d, want 1).
The old card counted live codes, so this pickup, the one most in need of attention, was worth zero.`,
			after-before)
	}
}

// TestD110_TheSplitDegradesRatherThanDisappearsWhenTheYardCannotBeAsked pins the honest failure.
//
// "Ready to collect" is a fact in the tasks module, reached through PickCompletionReader. Unwired -
// a deployment that has not attached the yard, the card must still report the POPULATION, because
// that is the number a yard lead actually needs, and must report all of it as AWAITING PICK, which is
// the pessimistic reading: a yard nobody has wired has staged nothing.
//
// The alternative, a zero, would hide the pickups entirely, which is the failure the ruling is about.
func TestD110_TheSplitDegradesRatherThanDisappearsWhenTheYardCannotBeAsked(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	order := seedPickupOrder(t, pool, branchKGN, "WILL_CALL", "READY", inThreeDays())
	seedPickLines(t, pool, order, 1)
	seedPickTask(t, pool, order, branchKGN, "STAGED")

	wired := d110Service(t, pool)
	_, readyWired, _, err := wired.PendingPickupCounts(ctx, branchKGN)
	if err != nil {
		t.Fatalf("wired count: %v", err)
	}
	if readyWired < 1 {
		t.Fatalf("with the yard wired, a staged pickup must read as ready to collect (got %d)", readyWired)
	}

	unwired := NewService(NewRepository(pool)).
		withCaller(func(context.Context) (identity.StaffContext, bool) {
			return identity.StaffContext{
				UserID: "u-d110", RoleCode: "LOGISTICS_MANAGER", Tier: identity.TierManager,
				Scope: identity.ScopeGlobal, Status: identity.StatusActive,
			}, true
		})
	awaiting, ready, _, err := unwired.PendingPickupCounts(ctx, branchKGN)
	if err != nil {
		t.Fatalf("unwired count: %v", err)
	}
	if ready != 0 {
		t.Fatalf("an unwired yard cannot claim anything is staged (got %d ready)", ready)
	}
	if awaiting < 1 {
		t.Fatalf(`an unwired yard must still report the POPULATION as awaiting pick, not zero.
A zero would hide every customer pickup, which is the defect the ruling is about (got %d).`, awaiting)
	}
}
