package sync

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// fencedStore wraps the fake store the way a fenced scope's store behaves
// for the engine's unit legs: rows sit on branches, a caller reaches only
// the branches its authority grants, and an out of reach row is the absent
// row for Load and FindApplied while a create the fence hides is refused
// with the module's own words. The store seam is the fence's only door,
// which is the shape the tasks module's pick and weights stores present.
type fencedStore struct {
	*fakeStore
	branch   map[string]string          // row id to the branch it sits on
	reachFor func(ScopeFilter) []string // the caller's granted branches; nil is GLOBAL
}

func (f *fencedStore) reaches(filter ScopeFilter, branch string) bool {
	granted := f.reachFor(filter)
	if granted == nil {
		return true
	}
	for _, b := range granted {
		if b == branch {
			return true
		}
	}
	return false
}

func (f *fencedStore) FindApplied(ctx context.Context, filter ScopeFilter, key string) (RowState, bool, error) {
	rs, found, err := f.fakeStore.FindApplied(ctx, filter, key)
	if err != nil || !found {
		return rs, found, err
	}
	if !f.reaches(filter, f.branch[rs.ID]) {
		return RowState{}, false, nil
	}
	return rs, true, nil
}

func (f *fencedStore) Load(ctx context.Context, filter ScopeFilter, id string) (RowState, bool, error) {
	rs, found, err := f.fakeStore.Load(ctx, filter, id)
	if err != nil || !found {
		return rs, found, err
	}
	if !f.reaches(filter, f.branch[id]) {
		return RowState{}, false, nil
	}
	return rs, true, nil
}

func (f *fencedStore) Create(ctx context.Context, filter ScopeFilter, key string, fields map[string]any) (RowState, error) {
	if !f.reaches(filter, "br-a") {
		return RowState{}, fmt.Errorf("%w: tasks: unknown order", ErrOpRefused)
	}
	rs, err := f.fakeStore.Create(ctx, filter, key, fields)
	if err == nil {
		f.branch[rs.ID] = "br-a"
	}
	return rs, err
}

// UX-103: the ledger is keyed by org, tenant, scope and key, never by
// branch, so a key bound on one branch reaches the engine's lookup from a
// device of another. The replay must answer like the absent key when the
// store's fence hides the entity: the same body is not "applied" (with or
// without a seq) and another body is not key_reused, both getting exactly
// what an unknown key gets; the owning branch still answers, and a caller
// whose reach covers both branches is answered as before.
func TestBoundKeyAnswersLikeTheAbsentKeyWhenTheFenceHidesTheEntity(t *testing.T) {
	inner := &fakeStore{}
	fs := &fencedStore{fakeStore: inner, branch: map[string]string{},
		reachFor: func(f ScopeFilter) []string {
			switch f.Actor {
			case "staff-a":
				return []string{"br-a"}
			case "staff-b":
				return []string{"br-b"}
			}
			return nil
		}}
	e := NewEngine()
	if err := e.RegisterScope("pick_tasks", fs); err != nil {
		t.Fatalf("register: %v", err)
	}
	led := newMemLedger()
	e.WithKeyLedger(led)
	info, ok := Lookup("pick_tasks")
	if !ok {
		t.Fatal("pick_tasks missing from the registry")
	}
	push := func(f ScopeFilter, op apigen.SyncPushOperation) apigen.SyncPushResult {
		t.Helper()
		resp, err := e.Push(context.Background(), info, f, []apigen.SyncPushOperation{op})
		if err != nil {
			t.Fatalf("push: %v", err)
		}
		return resp.Results[0]
	}
	fA := ScopeFilter{OrgID: testFilter.OrgID, Actor: "staff-a"}
	fB := ScopeFilter{OrgID: testFilter.OrgID, Actor: "staff-b"}
	fG := ScopeFilter{OrgID: testFilter.OrgID, Actor: "staff-g"}

	op := apigen.SyncPushOperation{ClientId: key(7), Op: "create", Payload: map[string]any{"id": rowID(7), "v": "a"}}
	first := push(fA, op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the branch A create applies: %+v", first)
	}
	// The absent key's answer for the fenced caller is the pattern: the
	// store's create refusal, the module's own words.
	absent := push(fB, apigen.SyncPushOperation{ClientId: key(8), Op: "create", Payload: map[string]any{"id": rowID(9), "v": "a"}})
	if absent.Status != "rejected" || absent.Error == nil || !strings.Contains(*absent.Error, "unknown order") {
		t.Fatalf("setup: the absent key's answer for the fenced caller: %+v", absent)
	}

	same := push(fB, op)
	if same.Status != absent.Status || same.Error == nil || *same.Error != *absent.Error {
		t.Errorf("a branch B replay of a branch A bound key (same body) answers like the absent key: got %+v want %+v", same, absent)
	}
	other := apigen.SyncPushOperation{ClientId: key(7), Op: "create", Payload: map[string]any{"id": rowID(7), "v": "TAMPERED"}}
	otherBody := push(fB, other)
	if otherBody.Status != absent.Status || otherBody.Error == nil || *otherBody.Error != *absent.Error {
		t.Errorf("a branch B replay with another body answers like the absent key, never key_reused: got %+v want %+v", otherBody, absent)
	}

	again := push(fA, op)
	if again.Status != "applied" || again.Seq == nil || *again.Seq != *first.Seq {
		t.Errorf("the owning branch's replay still answers applied with the row's seq: %+v", again)
	}
	wantKeyReused(t, push(fA, other))

	world := push(fG, op)
	if world.Status != "applied" || world.Seq == nil {
		t.Errorf("a caller whose reach covers both branches is answered as before: %+v", world)
	}
	if led.size() != 1 {
		t.Errorf("the refused replays bind nothing new: %d entries, want 1", led.size())
	}
}
