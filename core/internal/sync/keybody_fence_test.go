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

// existsStore is a fencedStore that also answers the bound key gate's
// optional existence read: the row's, not the caller's (no branch or
// driver fence), tombstone aware through the gone set. UX-103 round 3:
// the gate may drop a bound key only when a LIVE entity exists and the
// caller's Load cannot see it; a gone-for-everyone entity keeps the bound
// answer, and a store that cannot answer existence at all keeps it too.
type existsStore struct {
	*fencedStore
	gone map[string]bool
}

func (f *existsStore) BoundEntityExists(_ context.Context, _ ScopeFilter, id string) (bool, error) {
	if f.gone[id] {
		return false, nil
	}
	for _, r := range f.rows {
		if r.id == id {
			return true, nil
		}
	}
	return false, nil
}

// UX-103: the ledger is keyed by org, tenant, scope and key, never by
// branch, so a key bound on one branch reaches the engine's lookup from a
// device of another. The replay must answer like the absent key when the
// store's fence hides the entity: the same body is not "applied" (with or
// without a seq) and another body is not key_reused; the owning branch still
// answers, and a caller whose reach covers both branches is answered as
// before. Round 4 sharpens the shape of the answer: for a hidden LIVE entity
// the refusal is the gate's own, the seam's row not found, taken before the
// plain path (the engine never reaches store.Create for a bound key whose
// entity exists), while an unknown key still takes the plain path and meets
// the store's own words, so the two are compared on status and on the
// nothing-applied effect rather than word for word.
func TestBoundKeyAnswersLikeTheAbsentKeyWhenTheFenceHidesTheEntity(t *testing.T) {
	inner := &fakeStore{}
	fs := &existsStore{fencedStore: &fencedStore{fakeStore: inner, branch: map[string]string{},
		reachFor: func(f ScopeFilter) []string {
			switch f.Actor {
			case "staff-a":
				return []string{"br-a"}
			case "staff-b":
				return []string{"br-b"}
			}
			return nil
		}}, gone: map[string]bool{}}
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

	// UX-103 round 4: a non binder who cannot see the LIVE entity is answered the gate's own
	// refusal, the seam's row not found, before the plain path: the engine never reaches
	// store.Create for a bound key whose entity exists. The absent control keeps the store's own
	// words for an unknown key; the bound key never takes the plain path, so the words differ.
	same := push(fB, op)
	if same.Status != "rejected" || same.Error == nil || *same.Error != ErrRowNotFound.Error() {
		t.Errorf("a branch B replay of a branch A bound key (same body) answers the fenced refusal: got %+v", same)
	}
	other := apigen.SyncPushOperation{ClientId: key(7), Op: "create", Payload: map[string]any{"id": rowID(7), "v": "TAMPERED"}}
	otherBody := push(fB, other)
	if otherBody.Status != "rejected" || otherBody.Error == nil || *otherBody.Error != ErrRowNotFound.Error() {
		t.Errorf("a branch B replay with another body answers the fenced refusal, never key_reused: got %+v", otherBody)
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

// UX-103 round 3: the entity the key applied is GONE for everyone (deleted, tombstoned, hard
// dropped). The gate must keep the bound answer: the replay answers applied with no seq and nothing
// applies twice, for the owner and for a caller the fence holds anyway, and another body is still
// key_reused. Hiding here would send the replay down the plain path, where a gone row can apply
// AGAIN, the second create the gate exists to make impossible.
func TestBoundKeyKeepsTheBoundAnswerWhenTheEntityIsGoneForEveryone(t *testing.T) {
	inner := &fakeStore{}
	fs := &existsStore{fencedStore: &fencedStore{fakeStore: inner, branch: map[string]string{},
		reachFor: func(f ScopeFilter) []string {
			switch f.Actor {
			case "staff-a":
				return []string{"br-a"}
			case "staff-b":
				return []string{"br-b"}
			}
			return nil
		}}, gone: map[string]bool{}}
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

	op := apigen.SyncPushOperation{ClientId: key(17), Op: "create", Payload: map[string]any{"id": rowID(17), "v": "a"}}
	first := push(fA, op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the create applies: %+v", first)
	}
	// Gone for everyone: the row is dropped and the store says so, for every caller at once.
	fs.gone[rowID(17)] = true
	kept := inner.rows[:0]
	for _, r := range inner.rows {
		if r.id != rowID(17) {
			kept = append(kept, r)
		}
	}
	inner.rows = kept

	again := push(fA, op)
	if again.Status != "applied" || again.Seq != nil {
		t.Errorf("the owner's replay over a gone row answers applied with no seq: %+v", again)
	}
	if len(inner.rows) != 0 {
		t.Errorf("the replay applies nothing twice: %d rows, want 0", len(inner.rows))
	}
	other := apigen.SyncPushOperation{ClientId: key(17), Op: "create", Payload: map[string]any{"id": rowID(17), "v": "TAMPERED"}}
	wantKeyReused(t, push(fA, other))

	// A caller the fence holds anyway keeps the bound answer too: without a live row to hide there
	// is no fence answer to give, only the key's own.
	colleague := push(fB, op)
	if colleague.Status != "applied" || colleague.Seq != nil {
		t.Errorf("a branch B replay over a gone row answers applied with no seq, the bound answer: %+v", colleague)
	}
	if len(inner.rows) != 0 {
		t.Errorf("the branch B replay applies nothing twice: %d rows, want 0", len(inner.rows))
	}
	if led.size() != 1 {
		t.Errorf("the replays bind nothing new: %d entries, want 1", led.size())
	}
}

// UX-103 round 3: a store that does not implement the optional existence read keeps the bound
// answer wherever its Load misses, the pre-gate behaviour. The gate fails toward idempotency, never
// toward a second create.
func TestBoundKeyKeepsTheBoundAnswerWhenTheStoreCannotAnswerExistence(t *testing.T) {
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

	op := apigen.SyncPushOperation{ClientId: key(27), Op: "create", Payload: map[string]any{"id": rowID(27), "v": "a"}}
	first := push(fA, op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the create applies: %+v", first)
	}
	again := push(fB, op)
	if again.Status != "applied" || again.Seq != nil {
		t.Errorf("without the existence read a hidden row keeps the bound answer, applied with no seq: %+v", again)
	}
	other := apigen.SyncPushOperation{ClientId: key(27), Op: "create", Payload: map[string]any{"id": rowID(27), "v": "TAMPERED"}}
	wantKeyReused(t, push(fB, other))
	if led.size() != 1 {
		t.Errorf("the replays bind nothing new: %d entries, want 1", led.size())
	}
}

// UX-103 round 4: the BINDER's own replay keeps the bound answer whatever its Load says. A store
// whose Load fences on a branch stamped at create time hides the row from its own creator when the
// order moves branch along with the caller's reach; hiding there would send the binder's replay
// down the plain path, where a create fence reading the order's CURRENT branch applies the key a
// second time (the moved branch regression S2 probed). The bound answer, applied with no seq and
// key_reused for another body, is the idempotent one, and a caller who is NOT the binder who
// cannot see the live row is the gate's own refusal instead.
func TestBoundKeyTheBinderKeepsTheBoundAnswerWhateverItsLoadSays(t *testing.T) {
	inner := &fakeStore{}
	reach := map[string][]string{"staff-a": {"br-a"}, "staff-b": {"br-b"}}
	fs := &existsStore{fencedStore: &fencedStore{fakeStore: inner, branch: map[string]string{},
		reachFor: func(f ScopeFilter) []string { return reach[f.Actor] }}, gone: map[string]bool{}}
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

	op := apigen.SyncPushOperation{ClientId: key(37), Op: "create", Payload: map[string]any{"id": rowID(37), "v": "a"}}
	first := push(fA, op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the create applies: %+v", first)
	}
	// The transfer: the binder's reach moves off the row's branch, so its own Load now hides a
	// row that exists.
	reach["staff-a"] = []string{"br-b"}
	again := push(fA, op)
	if again.Status != "applied" || again.Seq != nil {
		t.Errorf("the binder's replay answers applied with no seq after its reach moved: %+v", again)
	}
	if len(inner.rows) != 1 {
		t.Errorf("the replay applies nothing twice: %d rows, want 1", len(inner.rows))
	}
	other := apigen.SyncPushOperation{ClientId: key(37), Op: "create", Payload: map[string]any{"id": rowID(37), "v": "TAMPERED"}}
	wantKeyReused(t, push(fA, other))

	// A caller who is not the binder, unable to see the live row, is the gate's refusal, and the
	// engine never reaches Create for the bound key.
	colleague := push(fB, op)
	if colleague.Status != "rejected" || colleague.Error == nil || *colleague.Error != ErrRowNotFound.Error() {
		t.Errorf("a non binder's hidden replay is the fenced refusal: %+v", colleague)
	}
	if len(inner.rows) != 1 {
		t.Errorf("the colleague's replay creates nothing: %d rows, want 1", len(inner.rows))
	}
	if led.size() != 1 {
		t.Errorf("the replays bind nothing new: %d entries, want 1", led.size())
	}
}

// UX-103 round 4: a ledger row with NO binder (bound_by NULL, bound before migration 0449, or
// empty, bound by a push that carried no actor) keeps the bound answer for every caller, the
// owner's and a caller the fence hides alike: with the binder unknowable, refusing could answer
// the owner's own replay with a refusal and hiding could apply a second time, so the gate fails
// toward idempotency exactly as it does for a store that cannot answer existence.
func TestBoundKeyAnOldRowWithNoBinderKeepsTheBoundAnswer(t *testing.T) {
	inner := &fakeStore{}
	reach := map[string][]string{"staff-a": {"br-a"}, "staff-b": {"br-b"}}
	fs := &existsStore{fencedStore: &fencedStore{fakeStore: inner, branch: map[string]string{},
		reachFor: func(f ScopeFilter) []string { return reach[f.Actor] }}, gone: map[string]bool{}}
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

	op := apigen.SyncPushOperation{ClientId: key(47), Op: "create", Payload: map[string]any{"id": rowID(47), "v": "a"}}
	first := push(fA, op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the create applies: %+v", first)
	}
	// The pre 0449 shape: the binding stands, the binder is unknown.
	slot := ledgerSlot(fA, "pick_tasks", key(47))
	entry := led.entries[slot]
	led.entries[slot] = LedgerEntry{Hash: entry.Hash, EntityID: entry.EntityID}
	// The moved reach: the row is live, the caller's Load hides it, and the key still keeps its
	// bound answer.
	reach["staff-a"] = []string{"br-b"}
	again := push(fA, op)
	if again.Status != "applied" || again.Seq != nil {
		t.Errorf("a pre 0449 key's replay answers applied with no seq: %+v", again)
	}
	if len(inner.rows) != 1 {
		t.Errorf("the replay applies nothing twice: %d rows, want 1", len(inner.rows))
	}
	other := apigen.SyncPushOperation{ClientId: key(47), Op: "create", Payload: map[string]any{"id": rowID(47), "v": "TAMPERED"}}
	wantKeyReused(t, push(fA, other))

	// A caller the fence hides keeps the bound answer too: with the binder unknowable there is no
	// caller the gate may refuse on the entity's behalf.
	colleague := push(fB, op)
	if colleague.Status != "applied" || colleague.Seq != nil {
		t.Errorf("a hidden caller's replay of a pre 0449 key keeps the bound answer: %+v", colleague)
	}
	if len(inner.rows) != 1 {
		t.Errorf("the hidden caller's replay applies nothing twice: %d rows, want 1", len(inner.rows))
	}
	if led.size() != 1 {
		t.Errorf("the replays bind nothing new: %d entries, want 1", led.size())
	}
}
