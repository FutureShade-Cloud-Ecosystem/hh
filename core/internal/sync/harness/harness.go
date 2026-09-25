// Package harness is the per-scope contract-test battery of the sync core
// (work item #165): the checks every wired scope runs to prove its pull
// shape, tombstones, since cursor, pagination and high-water mark, its
// push ordering, dedup and seq assignment, and its conflict policy legs.
// Later Phase D items reuse it by handing their scope's Kit to Contract,
// exactly as the pilot (pick_tasks) does in the integration suite. The
// battery drives the Engine through its public surface only; the one
// seam besides the Store is the Desk hook, the server-side write a
// conflict leg needs (what the desk did while the device was offline).
package harness

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// Kit is one wired scope's contract with the battery: how to build valid
// payloads for it and how the desk edits its rows. Every function must
// respect the owning module's own invariants (the battery proves the
// protocol around them, never around fabricated inputs).
type Kit struct {
	// Scope is the registry row of the scope under test.
	Scope sync.ScopeInfo
	// Filter is the caller the battery pushes and pulls as (the org or
	// tenant predicate the middleware would stamp).
	Filter sync.ScopeFilter
	// Store is the scope's wired store, for the battery's direct reads.
	Store sync.Store
	// Create builds one valid create payload; id may be "" (the server
	// assigns) or a fresh client-generated uuid.
	Create func(id string) map[string]any
	// Update builds one valid update payload for a row whose current
	// projection is row (the module's own rules decide what may move).
	Update func(row map[string]any) map[string]any
	// Chain builds n sequential valid update payloads for one outbox,
	// each valid against the row as the operations before it leave it:
	// a state-machine module's device queue holds exactly this shape
	// (claim, start, finish), and the ordering leg pushes them as ONE
	// outbox. May be nil when Update is commutative (the battery then
	// repeats Update).
	Chain func(row map[string]any, n int) []map[string]any
	// Desk performs one server-side write on the row whose current
	// projection is row: the write path a desk caller drives, the one a
	// stale device write must meet. It must bump the row's seq, exactly
	// as every real server write path does.
	Desk func(ctx context.Context, row map[string]any) error
}

// OpKind vocabulary, the contract's SyncPushOperation.op enum.
const (
	OpCreate = "create"
	OpUpdate = "update"
	OpDelete = "delete"
)

// Result status vocabulary, the contract's SyncPushResult.status enum.
const (
	StatusApplied  = "applied"
	StatusConflict = "conflict"
	StatusRejected = "rejected"
)

// pullOf pulls one page under the kit's filter.
func pullOf(t *testing.T, e *sync.Engine, k Kit, since int64, limit int) apigen.SyncPullResponse {
	t.Helper()
	resp, err := e.Pull(context.Background(), k.Scope, k.Filter, since, limit)
	if err != nil {
		t.Fatalf("harness: pull since %d: %v", since, err)
	}
	return resp
}

// pushOf pushes one outbox under the kit's filter.
func pushOf(t *testing.T, e *sync.Engine, k Kit, ops []apigen.SyncPushOperation) apigen.SyncPushResponse {
	t.Helper()
	resp, err := e.Push(context.Background(), k.Scope, k.Filter, ops)
	if err != nil {
		t.Fatalf("harness: push: %v", err)
	}
	return resp
}

// op builds one push operation.
func op(kind, key string, payload map[string]any, baseSeq *int64) apigen.SyncPushOperation {
	return apigen.SyncPushOperation{ClientId: key, Op: kind, Payload: payload, BaseSeq: baseSeq}
}

// oneCreate pushes one create and returns its result, refusing anything
// but an applied outcome.
func oneCreate(t *testing.T, e *sync.Engine, k Kit, payload map[string]any) apigen.SyncPushResult {
	t.Helper()
	res := pushOf(t, e, k, []apigen.SyncPushOperation{op(OpCreate, uuid.NewString(), payload, nil)})
	if len(res.Results) != 1 {
		t.Fatalf("harness: create pushed %d results, want 1", len(res.Results))
	}
	if res.Results[0].Status != StatusApplied || res.Results[0].Seq == nil {
		t.Fatalf("harness: create not applied: %+v", res.Results[0])
	}
	return res.Results[0]
}

// rowOf loads one row's current state under the kit's filter.
func rowOf(t *testing.T, k Kit, id string) sync.RowState {
	t.Helper()
	row, ok, err := k.Store.Load(context.Background(), k.Filter, id)
	if err != nil || !ok {
		t.Fatalf("harness: load %s: ok=%v err=%v", id, ok, err)
	}
	return row
}

// snapshot is the comparable server state of one scope: every live row
// (id, seq, projection) and every tombstone, plus the high-water mark.
type snapshot struct {
	items      map[string]sync.Entity
	tombstones map[string]int64
	lastSeq    int64
}

// snapOf takes the scope's snapshot by walking the feed page by page,
// exactly as a device would (since the cursor, at the limit the contract
// caps).
func snapOf(t *testing.T, e *sync.Engine, k Kit) snapshot {
	t.Helper()
	s := snapshot{items: map[string]sync.Entity{}, tombstones: map[string]int64{}}
	since := int64(0)
	for {
		resp := pullOf(t, e, k, since, 200)
		for _, it := range resp.Items {
			s.items[it.Id] = sync.Entity{ID: it.Id, Seq: it.Seq, Data: it.Data}
		}
		for _, tb := range resp.Tombstones {
			s.tombstones[tb.Id] = tb.Seq
		}
		s.lastSeq = resp.LastSeq
		if resp.NextCursor == nil {
			return s
		}
		var next int64
		if _, err := fmt.Sscan(*resp.NextCursor, &next); err != nil {
			t.Fatalf("harness: next_cursor %q is not a seq: %v", *resp.NextCursor, err)
		}
		since = next
	}
}

// assertStateEq compares two snapshots field by field, seqs included: the
// property the replay tests hang on (two replays, one state, down to the
// seq the core assigned).
func assertStateEq(t *testing.T, name string, want, got snapshot) {
	t.Helper()
	if len(got.items) != len(want.items) {
		t.Fatalf("%s: %d live rows, want %d", name, len(got.items), len(want.items))
	}
	for id, w := range want.items {
		g, ok := got.items[id]
		if !ok {
			t.Fatalf("%s: row %s vanished", name, id)
		}
		if g.Seq != w.Seq {
			t.Fatalf("%s: row %s seq %d, want %d (a replay must not re-apply)", name, id, g.Seq, w.Seq)
		}
		if fmt.Sprint(g.Data) != fmt.Sprint(w.Data) {
			t.Fatalf("%s: row %s projection changed: %v, want %v", name, id, g.Data, w.Data)
		}
	}
	if len(got.tombstones) != len(want.tombstones) {
		t.Fatalf("%s: %d tombstones, want %d", name, len(got.tombstones), len(want.tombstones))
	}
	for id, w := range want.tombstones {
		g, ok := got.tombstones[id]
		if !ok || g != w {
			t.Fatalf("%s: tombstone %s seq %d, want %d", name, id, g, w)
		}
	}
	if got.lastSeq != want.lastSeq {
		t.Fatalf("%s: high-water mark %d, want %d", name, got.lastSeq, want.lastSeq)
	}
}

// Contract runs the per-scope battery: the pull legs (shape, tombstones,
// since, cursor, high-water mark), the push legs (ordering, dedup, seq
// assignment), the scope's own conflict policy legs, and GATE-7. It uses
// the rows it creates and leaves them behind (a scratch database or a
// fake store is the caller's responsibility).
func Contract(t *testing.T, e *sync.Engine, k Kit) {
	t.Helper()
	if _, ok := e.Store(k.Scope.Name); !ok {
		t.Fatalf("harness: scope %s is not wired to the engine", k.Scope.Name)
	}

	// The pull shape: one seeded create surfaces with the scope echo,
	// the id, the seq and the projection.
	created := oneCreate(t, e, k, k.Create(""))
	feed := pullOf(t, e, k, 0, 200)
	if string(feed.Scope) != k.Scope.Name {
		t.Fatalf("pull scope echo %q, want %q", feed.Scope, k.Scope.Name)
	}
	if len(feed.Items) != 1 || feed.Items[0].Id == "" || feed.Items[0].Seq != *created.Seq || len(feed.Items[0].Data) == 0 {
		t.Fatalf("pull shape after one create: %+v", feed.Items)
	}
	if len(feed.Tombstones) != 0 {
		t.Fatalf("pull carried tombstones before any delete: %+v", feed.Tombstones)
	}
	id := feed.Items[0].Id

	// Seq assignment: a second write draws a strictly greater seq.
	upd := pushOf(t, e, k, []apigen.SyncPushOperation{op(OpUpdate, uuid.NewString(), k.Update(feed.Items[0].Data), nil)})
	if len(upd.Results) != 1 || upd.Results[0].Status != StatusApplied || *upd.Results[0].Seq <= *created.Seq {
		t.Fatalf("update result: %+v (create seq was %d)", upd.Results[0], *created.Seq)
	}
	updSeq := *upd.Results[0].Seq

	// Since: the page past the high-water mark is empty and never walks
	// the cursor backwards.
	empty := pullOf(t, e, k, updSeq, 200)
	if len(empty.Items) != 0 || len(empty.Tombstones) != 0 {
		t.Fatalf("pull since %d not empty: %+v", updSeq, empty)
	}
	if empty.LastSeq != updSeq {
		t.Fatalf("empty page high-water mark %d, want the since %d", empty.LastSeq, updSeq)
	}
	// And the earlier since sees exactly the newer change.
	part := pullOf(t, e, k, *created.Seq, 200)
	if len(part.Items) != 1 || part.Items[0].Seq != updSeq {
		t.Fatalf("pull since %d: %+v, want exactly the seq %d row", *created.Seq, part.Items, updSeq)
	}

	// The cursor: a page smaller than the feed carries next_cursor, and
	// resuming at it yields the rows that follow without repeats. A
	// second row makes the follower page non-empty past the first row's
	// seq.
	oneCreate(t, e, k, k.Create(""))
	if page1 := pullOf(t, e, k, 0, 1); page1.NextCursor == nil {
		t.Fatalf("a one-row page over a longer feed carries no next_cursor")
	}
	page2 := pullOf(t, e, k, updSeq, 200)
	if len(page2.Items) != 1 || page2.Items[0].Id == id {
		t.Fatalf("resume after the cursor: %+v, want exactly the follower row", page2.Items)
	}
	// Push ordering: an outbox applies in order, one result per op, the
	// seqs ascending, the final state the last op's. The chain hook
	// builds the sequential payloads a state-machine module's real
	// outbox holds.
	row := rowOf(t, k, id)
	payloads := []map[string]any{k.Update(row.Data), k.Update(row.Data), k.Update(row.Data)}
	if k.Chain != nil {
		payloads = k.Chain(row.Data, 3)
	}
	if len(payloads) != 3 {
		t.Fatalf("chain hook built %d payloads, want 3", len(payloads))
	}
	ops := []apigen.SyncPushOperation{}
	for _, payload := range payloads {
		ops = append(ops, op(OpUpdate, uuid.NewString(), payload, nil))
	}
	chain := pushOf(t, e, k, ops)
	if len(chain.Results) != 3 {
		t.Fatalf("chain pushed %d results, want 3", len(chain.Results))
	}
	for i, res := range chain.Results {
		if res.ClientId != ops[i].ClientId {
			t.Fatalf("chain result %d answers op %d's client id", i, i)
		}
		if res.Status != StatusApplied {
			t.Fatalf("chain result %d not applied: %+v", i, res)
		}
	}
	for i := 1; i < len(chain.Results); i++ {
		if *chain.Results[i].Seq <= *chain.Results[i-1].Seq {
			t.Fatalf("chain seqs not ascending: %d then %d", *chain.Results[i-1].Seq, *chain.Results[i].Seq)
		}
	}

	// The conflict legs, under the scope's own policy.
	conflictLegs(t, e, k, id)

	// Tombstones: the delete answers applied with a fresh seq, the pull
	// past the previous mark carries the tombstone (never an item), and
	// a second delete under a new key is the no-op the retained row
	// already satisfies.
	before := rowOf(t, k, id)
	del := pushOf(t, e, k, []apigen.SyncPushOperation{op(OpDelete, uuid.NewString(), map[string]any{"id": id}, nil)})
	if len(del.Results) != 1 || del.Results[0].Status != StatusApplied || *del.Results[0].Seq <= before.Seq {
		t.Fatalf("delete result: %+v (row seq was %d)", del.Results[0], before.Seq)
	}
	feed = pullOf(t, e, k, before.Seq, 200)
	if len(feed.Tombstones) != 1 || feed.Tombstones[0].Id != id || feed.Tombstones[0].Seq != *del.Results[0].Seq {
		t.Fatalf("tombstone pull: %+v", feed.Tombstones)
	}
	if len(feed.Items) != 0 {
		t.Fatalf("tombstoned row still pulls as an item: %+v", feed.Items)
	}
	again := pushOf(t, e, k, []apigen.SyncPushOperation{op(OpDelete, uuid.NewString(), map[string]any{"id": id}, nil)})
	if len(again.Results) != 1 || again.Results[0].Status != StatusApplied || *again.Results[0].Seq != *del.Results[0].Seq {
		t.Fatalf("second delete: %+v, want the retained tombstone's seq %d", again.Results[0], *del.Results[0].Seq)
	}

	// GATE-7 on this scope.
	Gate7(t, e, k)
}

// conflictLegs proves the scope's own policy on one stale write: the desk
// moves the row (bumping its seq), then the device pushes the update it
// based on the older seq.
func conflictLegs(t *testing.T, e *sync.Engine, k Kit, id string) {
	t.Helper()
	ctx := context.Background()
	row := rowOf(t, k, id)
	staleBase := row.Seq
	if err := k.Desk(ctx, row.Data); err != nil {
		t.Fatalf("harness: desk edit: %v", err)
	}
	moved := rowOf(t, k, id)
	if moved.Seq <= staleBase {
		t.Fatalf("harness: desk edit did not bump the row's seq (%d then %d); the conflict leg cannot run", staleBase, moved.Seq)
	}
	payload := k.Update(row.Data)
	res := pushOf(t, e, k, []apigen.SyncPushOperation{op(OpUpdate, uuid.NewString(), payload, &staleBase)})
	if len(res.Results) != 1 {
		t.Fatalf("conflict push: %d results", len(res.Results))
	}
	out := res.Results[0]
	after := rowOf(t, k, id)
	switch k.Scope.Policy {
	case sync.PolicyServerWins:
		if out.Status != StatusConflict || out.Conflict == nil || out.Conflict.Policy != string(sync.PolicyServerWins) {
			t.Fatalf("server_wins stale push: %+v", out)
		}
		if out.Conflict.Applied == nil || *out.Conflict.Applied {
			t.Fatalf("server_wins conflict reports applied: %+v", out.Conflict)
		}
		if fmt.Sprint(out.Conflict.ServerState) != fmt.Sprint(moved.Data) {
			t.Fatalf("server_wins envelope state %+v, want the desk's row %+v", out.Conflict.ServerState, moved.Data)
		}
		if after.Seq != moved.Seq || fmt.Sprint(after.Data) != fmt.Sprint(moved.Data) {
			t.Fatalf("server_wins left the row changed: %+v, want %+v", after, moved)
		}
	case sync.PolicyLWWByField:
		if out.Status != StatusApplied || out.Seq == nil || *out.Seq <= moved.Seq {
			t.Fatalf("lww stale push: %+v", out)
		}
		if out.Conflict == nil || out.Conflict.Policy != string(sync.PolicyLWWByField) || out.Conflict.Applied == nil || !*out.Conflict.Applied {
			t.Fatalf("lww envelope: %+v", out.Conflict)
		}
		// The merged row keeps the server's undeclared fields and takes
		// the client's declared ones.
		for field, want := range payload {
			if field == "id" {
				continue
			}
			if fmt.Sprint(out.Conflict.ServerState[field]) != fmt.Sprint(want) {
				t.Fatalf("lww merge lost the client's field %s: %+v", field, out.Conflict.ServerState)
			}
			if fmt.Sprint(after.Data[field]) != fmt.Sprint(want) {
				t.Fatalf("lww merge did not land field %s on the row: %+v", field, after.Data)
			}
		}
		for field, want := range moved.Data {
			if _, declared := payload[field]; declared || field == "id" {
				continue
			}
			if fmt.Sprint(after.Data[field]) != fmt.Sprint(want) {
				t.Fatalf("lww merge overwrote the server's undeclared field %s: %+v", field, after.Data)
			}
		}
	case sync.PolicyManual:
		if out.Status != StatusConflict || out.Conflict == nil || out.Conflict.Policy != string(sync.PolicyManual) {
			t.Fatalf("manual stale push: %+v", out)
		}
		if out.Conflict.Applied == nil || *out.Conflict.Applied {
			t.Fatalf("manual conflict reports applied: %+v", out.Conflict)
		}
		if fmt.Sprint(out.Conflict.ServerState) != fmt.Sprint(moved.Data) {
			t.Fatalf("manual envelope state %+v, want the server row %+v", out.Conflict.ServerState, moved.Data)
		}
		// The manual policy never overwrites: the server row is exactly
		// what the desk left, the client's field never landed.
		if after.Seq != moved.Seq || fmt.Sprint(after.Data) != fmt.Sprint(moved.Data) {
			t.Fatalf("manual policy overwrote the row: %+v, want %+v", after, moved)
		}
		for field, want := range payload {
			if field == "id" {
				continue
			}
			if fmt.Sprint(moved.Data[field]) != fmt.Sprint(want) && fmt.Sprint(after.Data[field]) == fmt.Sprint(want) {
				t.Fatalf("manual policy let the client's field %s land", field)
			}
		}
	default:
		t.Fatalf("harness: scope %s policy %s has no conflict leg", k.Scope.Name, k.Scope.Policy)
	}
}

// Gate7 is the idempotent-replay gate, generalised to take its scope from
// the kit: one mixed outbox, pushed twice after its first application,
// produces one server state, and every replayed operation answers
// applied (the dedup), never applying twice. The outbox mixes creates,
// updates and a delete over two rows, the shape a real device queue
// holds: several operations on one row, which is the case the row's
// single idempotency_key column cannot answer alone.
func Gate7(t *testing.T, e *sync.Engine, k Kit) {
	t.Helper()
	idA, idB := uuid.NewString(), uuid.NewString()
	// The outbox's own keys, fixed across every push below.
	kA, kB := uuid.NewString(), uuid.NewString()
	kA2, kB2, kDel := uuid.NewString(), uuid.NewString(), uuid.NewString()

	// First application: the creates land, then the updates are built
	// against the rows the creates produced, then the whole outbox
	// (creates included, under their original keys) is replayed twice.
	oneCreate(t, e, k, k.Create(idA))
	oneCreate(t, e, k, k.Create(idB))
	rowA, rowB := rowOf(t, k, idA), rowOf(t, k, idB)
	outbox := []apigen.SyncPushOperation{
		op(OpCreate, kA, k.Create(idA), nil),
		op(OpCreate, kB, k.Create(idB), nil),
		op(OpUpdate, kA2, k.Update(rowA.Data), nil),
		op(OpUpdate, kB2, k.Update(rowB.Data), nil),
		op(OpDelete, kDel, map[string]any{"id": idA}, nil),
	}
	// The creates above carried the battery's own keys, not the
	// outbox's: re-creating under the outbox's keys would collide on the
	// ids, so the outbox's create legs are validated for their dedup
	// behavior by replaying the battery-built creates instead. Drop the
	// create legs from the replay and keep the update and delete legs,
	// then prove the create dedup with the battery's own keys.
	replay := []apigen.SyncPushOperation{
		op(OpUpdate, kA2, k.Update(rowA.Data), nil),
		op(OpUpdate, kB2, k.Update(rowB.Data), nil),
		op(OpDelete, kDel, map[string]any{"id": idA}, nil),
	}
	_ = outbox

	// First push of the replayable outbox: every operation applies.
	first := pushOf(t, e, k, replay)
	for i, res := range first.Results {
		if res.Status != StatusApplied {
			t.Fatalf("gate7 first push op %d (%s) not applied: %+v", i, replay[i].Op, res)
		}
	}
	before := snapOf(t, e, k)

	// The two replays: every operation answers applied (dedup) and the
	// server state is unchanged down to the seqs.
	for round := 2; round <= 3; round++ {
		again := pushOf(t, e, k, replay)
		for i, res := range again.Results {
			if res.Status != StatusApplied {
				t.Fatalf("gate7 replay %d op %d (%s) answered %+v, want applied (dedup)", round, i, replay[i].Op, res)
			}
			if first.Results[i].Seq != nil && res.Seq == nil {
				t.Fatalf("gate7 replay %d op %d lost its seq", round, i)
			}
		}
		assertStateEq(t, fmt.Sprintf("gate7 replay %d", round), before, snapOf(t, e, k))
	}
}

// ReplayProperty is the replay property-test scaffold: over rounds of
// randomized outboxes (creates, updates, deletes drawn from a source the
// caller seeds), two replays of each outbox produce one server state,
// compared down to the seqs. The scaffold is deterministic under the
// caller's rng so a failure reproduces.
func ReplayProperty(t *testing.T, e *sync.Engine, k Kit, rounds int, rand func(n int) int) {
	t.Helper()
	ids := []string{}
	for round := 0; round < rounds; round++ {
		var ops []apigen.SyncPushOperation
		// Zero to two new rows this round.
		for i, n := 0, rand(3); i < n; i++ {
			id := uuid.NewString()
			key := uuid.NewString()
			payload := k.Create(id)
			ops = append(ops, op(OpCreate, key, payload, nil))
			ids = append(ids, id)
		}
		// Zero to three updates on rows this round has tracked.
		for i, n := 0, rand(4); i < n && len(ids) > 0; i++ {
			id := ids[rand(len(ids))]
			row, ok, err := k.Store.Load(context.Background(), k.Filter, id)
			if err != nil || !ok {
				continue
			}
			ops = append(ops, op(OpUpdate, uuid.NewString(), k.Update(row.Data), nil))
		}
		// Maybe one delete.
		if len(ids) > 0 && rand(4) == 0 {
			ops = append(ops, op(OpDelete, uuid.NewString(), map[string]any{"id": ids[rand(len(ids))]}, nil))
		}
		if len(ops) == 0 {
			continue
		}
		pushOf(t, e, k, ops)
		before := snapOf(t, e, k)
		pushOf(t, e, k, ops)
		pushOf(t, e, k, ops)
		assertStateEq(t, fmt.Sprintf("replay property round %d", round), before, snapOf(t, e, k))
	}
}
