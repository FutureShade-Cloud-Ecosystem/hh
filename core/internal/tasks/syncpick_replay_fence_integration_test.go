//go:build integration

// syncpick_replay_fence_integration_test.go, UX-103: the pick door inside the engine's whole flow, the
// key ledger answering like the absent key when the store's fence hides the entity. The ledger is keyed
// by org, tenant, scope and key, never by branch, so a key bound on a branch A operation reaches the
// lookup from a branch B device: the same body is not "applied" and another body is not key_reused, both
// answering exactly what an unknown key gets; the owning branch still answers, its replay with the row's
// seq, and its tampered body is still refused. (Round 4: for a hidden LIVE entity the refusal now comes
// from the engine's own gate, the seam's row not found, before the plain path; for this update shape the
// plain path answers the same words, so the pins stand.)
//
// Run with DATABASE_URL set to a throwaway migrated database; the legs skip politely without one.
package tasks

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

func TestPickDoorReplayFence_ABranchBReplayOfABoundKeyAnswersLikeTheAbsentKey(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	aCaller := scopedTo(d.user, d.branchA)
	bCaller := scopedTo(uuid.NewString(), d.branchB)
	// Each caller rides its own scope filter actor, as the middleware stamps one subject per
	// device: the ledger records who bound the key (UX-103 round 4), so the two callers must not
	// share one actor or the binder rule would answer the branch B replay as the binder's own.
	aF := hhsync.ScopeFilter{OrgID: d.org, Actor: "pick-door-actor-a"}
	bF := hhsync.ScopeFilter{OrgID: d.org, Actor: "pick-door-actor-b"}
	key := uuid.NewString()
	op := recordOp(key, id, pickLine(1))
	first := pushAsFilter(d, aF, aCaller, "pick_tasks", op)[0]
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the branch A record applies: %+v %v", first, first.Error)
	}
	// The absent key's answer for the branch B device is the pattern to hold: an unknown key on the same
	// shape is rejected with the row's words and writes nothing.
	absent := pushAsFilter(d, bF, bCaller, "pick_tasks", recordOp(uuid.NewString(), id, pickLine(1)))[0]
	if absent.Status != "rejected" || absent.Error == nil {
		t.Fatalf("setup: the absent key's answer for the branch B device: %+v", absent)
	}
	// The bound key from the branch B device, the same body: not "applied", the absent key's answer.
	same := pushAsFilter(d, bF, bCaller, "pick_tasks", op)[0]
	if same.Status != absent.Status || same.Error == nil || *same.Error != *absent.Error {
		t.Errorf("a branch B replay of a branch A bound key answers like the absent key: got %+v want %+v", same, absent)
	}
	// Another body: not key_reused either, the same absent answer.
	other := pickLine(1)
	other["qty_picked"] = "9"
	otherBody := pushAsFilter(d, bF, bCaller, "pick_tasks", recordOp(key, id, other))[0]
	if otherBody.Status != absent.Status || otherBody.Error == nil || *otherBody.Error != *absent.Error {
		t.Errorf("a branch B replay with another body answers like the absent key, never key_reused: got %+v want %+v", otherBody, absent)
	}
	if rows := d.lines(id); len(rows) != 1 || rows[0].picked != "7.0000" {
		t.Errorf("the branch B replays change nothing: %+v", rows)
	}
	// The owning branch still answers, its replay with the row's seq, a tampered body refused.
	again := pushAsFilter(d, aF, aCaller, "pick_tasks", op)[0]
	if again.Status != "applied" || again.Seq == nil || *again.Seq != *first.Seq {
		t.Errorf("the owning branch's replay still answers applied with the row's seq: %+v", again)
	}
	reused := pushAsFilter(d, aF, aCaller, "pick_tasks", recordOp(key, id, other))[0]
	if reused.Status != "rejected" || reused.Error == nil || !strings.Contains(*reused.Error, "key_reused") {
		t.Errorf("the owning branch's tampered replay is still key_reused: %+v", reused)
	}
}
