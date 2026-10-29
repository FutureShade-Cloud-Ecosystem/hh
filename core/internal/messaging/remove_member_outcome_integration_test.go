//go:build integration

// remove_member_outcome_integration_test.go: what a remove of one member
// answers when the member row is already gone (UX-111).
//
// The defect this file pins: RemoveChannelMember read the member row's
// source off the pool, OUTSIDE its transaction, and answered ErrLastOwner
// for any delete of its that matched no row. A remove that raced another
// remove of the same member read the row alive, then deleted nothing (the
// first remove had already dropped it), and the zero-row delete was taken
// for the last-owner refusal: the second caller got a 409, "a channel must
// keep its owner", about a room whose owner was intact. The row was a
// MEMBER row the whole time, so the answer was never the truth about it.
//
// The shape this file pins after the fix: the member row is read INSIDE the
// transaction, under the channel's lock. Under that lock nothing can remove
// the row between the read and the delete (every member path in this module
// takes the channel first), so the two answers stop lying: a read that
// finds no row is "already removed", the route's not-found, and a delete
// that matches no row after the read saw the row can only be the room's
// last OWNER.
package messaging

import (
	"context"
	"errors"
	"testing"
	"time"
)

// UX-111: two concurrent removes of one member of one group room. The
// member is seated at the top of every round, so each round has exactly one
// row to take: one remove lands, and the other finds the row already gone.
//
// The bar: no round answers ErrLastOwner. The row is a MEMBER row, never an
// owner row, so "a channel must keep its owner" is never the truth about
// it. Before the fix the racing loser answered ErrLastOwner whenever its
// read of the row ran before the winner's commit and its delete ran after
// it, which is the window the race opens. A deadlock (40P01) and any other
// error fail everywhere; a lock timeout (55P03) or a cancellation (57014)
// follows the same rule as the member_lock_order legs: a failure outside
// the evening guard, counted and logged under it, because the guard's
// pinned database answers those from a timer libfaketime moves (ci.yml's
// evening guard step: statement_timeout, lock_timeout and the deadlock
// detector ride that timer).
func TestIntegration_TwoRemoves_OneMember_NeverAnswersLastOwner(t *testing.T) {
	ctx := context.Background()
	pool := ux110r2Pool(t)
	repo := NewRepository(pool)
	group := ux110r2GroupRoom(t, pool, repo, "two-removes")

	const rounds = 250
	var tally ux110r2Tally
	var lastOwner int
	var landed int
	var alreadyGone int
	for round := 0; round < rounds; round++ {
		if _, _, err := repo.SeatGroupMember(ctx, group, uSamRivera, "the owner", "the guest"); err != nil {
			t.Fatalf("seat the member before round %d: %v", round, err)
		}

		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			results <- repo.RemoveChannelMember(ctx, group, uSamRivera)
		}()
		go func() {
			<-start
			results <- repo.RemoveChannelMember(ctx, group, uSamRivera)
		}()
		close(start)
		for i := 0; i < 2; i++ {
			select {
			case err := <-results:
				switch {
				case err == nil:
					landed++
				case errors.Is(err, errNotFound):
					alreadyGone++
				case errors.Is(err, ErrLastOwner):
					lastOwner++
				default:
					tally.add(err)
					if ux110r2Kind(err) == "other" {
						t.Errorf("round %d: a remove must answer removed, already gone or a counted timeout, got %v", round, err)
					} else if ux110r2Kind(err) == "40P01" {
						t.Errorf("round %d: a remove deadlocked: %v", round, err)
					} else if !ux110r2UnderGuard() {
						t.Errorf("round %d: a remove answered %s: %v", round, ux110r2Kind(err), err)
					} else {
						t.Logf("round %d: a remove answered %s under the evening guard, counted and not failed: %v",
							round, ux110r2Kind(err), err)
					}
				}
			case <-time.After(ux110r2Settle):
				t.Fatalf("round %d: a remove never answered", round)
			}
		}
	}

	t.Logf("two removes of one member of one group room: %d rounds, ErrLastOwner %d, removed %d, already gone %d, 40P01 %d, 55P03 %d, 57014 %d, other %d",
		rounds, lastOwner, landed, alreadyGone, tally.deadlocks, tally.timeouts, tally.canceled, tally.others)
	for i, s := range tally.samples {
		t.Logf("other error %d: %s", i+1, s)
	}
	if lastOwner != 0 {
		t.Errorf("a second remove of one member must never answer ErrLastOwner: %d of %d rounds did", lastOwner, rounds)
	}
	if tally.deadlocks != 0 || tally.others != 0 {
		t.Errorf("the pair must answer only removed or already gone: 40P01 %d, other %d", tally.deadlocks, tally.others)
	}
	ux110r2CheckTimeoutKinds(t, &tally, rounds, "a remove of one member must not wait out a lock timeout")
	if !ux110r2UnderGuard() && (landed != rounds || alreadyGone != rounds) {
		t.Errorf("one remove of the pair lands and one answers already gone: landed %d, already gone %d, across %d rounds",
			landed, alreadyGone, rounds)
	}
}

// UX-111: the outcomes of one remove, one at a time, on a real room.
//
// A real last-owner removal still answers ErrLastOwner: the room's founder
// is its only OWNER row, and the refusal leaves that row seated. A seated
// MEMBER row removes cleanly. Removing it again answers the route's
// not-found, not a last-owner conflict: the remove that landed did the
// work, and a row that is already gone says nothing about owners. A member
// who was never seated answers the same not-found, which is the answer the
// web already handles (its refusal words carry a sentence for a 404, and
// the remove flow reloads the roster whatever the answer says).
func TestIntegration_RemoveMember_Outcomes_LastOwnerRefused_AlreadyGoneNotFound(t *testing.T) {
	ctx := context.Background()
	pool := ux110r2Pool(t)
	repo := NewRepository(pool)
	group := ux110r2GroupRoom(t, pool, repo, "outcomes")

	// The founder is the room's only OWNER row: removing them is the real
	// last-owner case, and the refusal leaves them seated.
	if err := repo.RemoveChannelMember(ctx, group, uAlexMorgan); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("removing the room's only owner must answer ErrLastOwner, got %v", err)
	}
	var owners int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM msg_members WHERE channel_id = $1::uuid AND role = 'OWNER'`,
		group).Scan(&owners); err != nil {
		t.Fatalf("count the owners after the refusal: %v", err)
	}
	if owners != 1 {
		t.Fatalf("the refused last-owner removal left %d owner rows, want 1", owners)
	}

	// A seated MEMBER row removes cleanly, and removing it again answers
	// the route's not-found: already removed, never a last-owner conflict.
	if _, _, err := repo.SeatGroupMember(ctx, group, uSamRivera, "the owner", "the guest"); err != nil {
		t.Fatalf("seat the member: %v", err)
	}
	if err := repo.RemoveChannelMember(ctx, group, uSamRivera); err != nil {
		t.Fatalf("remove the member: %v", err)
	}
	if err := repo.RemoveChannelMember(ctx, group, uSamRivera); !errors.Is(err, errNotFound) {
		t.Fatalf("removing the member again must answer not-found, got %v", err)
	}

	// A member who was never seated answers the same not-found.
	if err := repo.RemoveChannelMember(ctx, group, uCaseyDoyle); !errors.Is(err, errNotFound) {
		t.Fatalf("removing a member who was never seated must answer not-found, got %v", err)
	}
}
