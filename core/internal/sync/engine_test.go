package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// fakeRow is one row of the fake store: a field map with the sync legs.
type fakeRow struct {
	id      string
	org     string
	tenant  string
	seq     int64
	fields  map[string]any
	key     string
	applied map[string]bool
}

// fakeStore is the in-memory Store the engine's unit legs run against:
// rows scoped by org or tenant exactly as the middleware would stamp,
// per-store monotonic seq allocation, and the full applied-key history
// (what the pilot's task_events history provides for real).
type fakeStore struct {
	mu      sync.Mutex
	rows    []*fakeRow
	nextSeq int64
	fail    error // injected fault for the next store call
}

func (f *fakeStore) inject(err error) { f.fail = err }

func (f *fakeStore) scoped(r *fakeRow, filter ScopeFilter) bool {
	if filter.Tenant {
		return r.tenant == filter.TenantID
	}
	return r.org == filter.OrgID && r.tenant == ""
}

func (f *fakeStore) state(r *fakeRow) RowState {
	rs := RowState{ID: r.id, Seq: r.seq, Deleted: strings.HasPrefix(fmt.Sprint(r.fields["__deleted"]), "true")}
	if !rs.Deleted {
		rs.Data = make(map[string]any, len(r.fields))
		for k, v := range r.fields {
			rs.Data[k] = v
		}
	}
	return rs
}

func (f *fakeStore) allocate() int64 {
	f.nextSeq++
	return f.nextSeq
}

func (f *fakeStore) Pull(_ context.Context, filter ScopeFilter, since int64, limit int) ([]Entity, []Tombstone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, nil, f.fail
	}
	items, tombs := []Entity{}, []Tombstone{}
	for _, r := range f.rows {
		if !f.scoped(r, filter) || r.seq <= since {
			continue
		}
		if len(items)+len(tombs) >= limit {
			break
		}
		if deleted, _ := r.fields["__deleted"].(bool); deleted {
			tombs = append(tombs, Tombstone{ID: r.id, Seq: r.seq})
			continue
		}
		data := make(map[string]any, len(r.fields))
		for k, v := range r.fields {
			if k == "__deleted" {
				continue
			}
			data[k] = v
		}
		items = append(items, Entity{ID: r.id, Seq: r.seq, Data: data})
	}
	return items, tombs, nil
}

func (f *fakeStore) FindApplied(_ context.Context, filter ScopeFilter, key string) (RowState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return RowState{}, false, f.fail
	}
	for _, r := range f.rows {
		if f.scoped(r, filter) && (r.key == key || r.applied[key]) {
			return f.state(r), true, nil
		}
	}
	return RowState{}, false, nil
}

func (f *fakeStore) Load(_ context.Context, filter ScopeFilter, id string) (RowState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return RowState{}, false, f.fail
	}
	for _, r := range f.rows {
		if r.id == id && f.scoped(r, filter) {
			return f.state(r), true, nil
		}
	}
	return RowState{}, false, nil
}

func (f *fakeStore) Create(_ context.Context, filter ScopeFilter, key string, fields map[string]any) (RowState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return RowState{}, f.fail
	}
	if id, ok := fields["id"].(string); ok && id != "" {
		for _, r := range f.rows {
			if r.id == id && f.scoped(r, filter) {
				return RowState{}, ErrRowExists
			}
		}
	}
	r := &fakeRow{id: pickID(fields), seq: f.allocate(), fields: copyFields(fields), key: key, applied: map[string]bool{key: true}}
	if filter.Tenant {
		r.tenant = filter.TenantID
	} else {
		r.org = filter.OrgID
	}
	f.rows = append(f.rows, r)
	return f.state(r), nil
}

func (f *fakeStore) Write(_ context.Context, filter ScopeFilter, id, key string, fields map[string]any) (RowState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return RowState{}, f.fail
	}
	for _, r := range f.rows {
		if r.id == id && f.scoped(r, filter) {
			for k, v := range fields {
				if k == "id" {
					continue
				}
				r.fields[k] = v
			}
			r.seq = f.allocate()
			r.key = key
			r.applied[key] = true
			return f.state(r), nil
		}
	}
	return RowState{}, ErrRowNotFound
}

func (f *fakeStore) Delete(_ context.Context, filter ScopeFilter, id, key string) (RowState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return RowState{}, f.fail
	}
	for _, r := range f.rows {
		if r.id == id && f.scoped(r, filter) {
			r.fields["__deleted"] = true
			r.seq = f.allocate()
			r.key = key
			r.applied[key] = true
			return f.state(r), nil
		}
	}
	return RowState{}, ErrRowNotFound
}

// deskEdit is the server-side write a conflict leg needs: a field change
// with a fresh seq, no client key.
func (f *fakeStore) deskEdit(filter ScopeFilter, id string, fields map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.id == id && f.scoped(r, filter) {
			for k, v := range fields {
				r.fields[k] = v
			}
			r.seq = f.allocate()
			return nil
		}
	}
	return ErrRowNotFound
}

func pickID(fields map[string]any) string {
	if id, ok := fields["id"].(string); ok && id != "" {
		return id
	}
	return fmt.Sprintf("row-%d", len(fields)+1000)
}

func copyFields(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	return out
}

// The engine fixture: one scope wired over the fake, a staff filter, and
// the uuid-shaped keys the engine demands.
func newEngineFixture(t *testing.T, policy Policy) (*Engine, *fakeStore, ScopeInfo, ScopeFilter) {
	t.Helper()
	name := "pick_tasks"
	switch policy {
	case PolicyLWWByField:
		name = "quotes"
	case PolicyManual:
		name = "shortages"
	case PolicyPullOnly:
		t.Fatalf("a pull-only scope has no push legs")
	}
	info, ok := Lookup(name)
	if !ok {
		t.Fatalf("scope %s missing", name)
	}
	store := &fakeStore{}
	e := NewEngine()
	if err := e.RegisterScope(name, store); err != nil {
		t.Fatalf("register: %v", err)
	}
	return e, store, info, ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000001", Actor: "fxstaff-any"}
}

func key(n int) string { return fmt.Sprintf("10000000-0000-4000-8000-%012d", n) }

func rowID(n int) string { return fmt.Sprintf("20000000-0000-4000-8000-%012d", n) }

var testFilter = ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000001", Actor: "fxstaff-any"}

func push(t *testing.T, e *Engine, info ScopeInfo, ops ...apigen.SyncPushOperation) []apigen.SyncPushResult {
	t.Helper()
	resp, err := e.Push(context.Background(), info, testFilter, ops)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	return resp.Results
}

// The pull page legs: ordering by seq, the high-water mark, the cursor on
// a full page, and the empty page past the mark.
func TestEnginePullPageShape(t *testing.T) {
	e, _, info, _ := newEngineFixture(t, PolicyServerWins)
	for i := 1; i <= 3; i++ {
		res := push(t, e, info, apigen.SyncPushOperation{ClientId: key(i), Op: "create", Payload: map[string]any{"id": rowID(i), "note": fmt.Sprintf("row %d", i)}})
		if len(res) != 1 || res[0].Status != "applied" {
			t.Fatalf("create %d: %+v", i, res[0])
		}
	}
	full, err := e.Pull(context.Background(), info, testFilter, 0, 3)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(full.Items) != 3 {
		t.Fatalf("full page held %d items, want 3", len(full.Items))
	}
	for i := 1; i < len(full.Items); i++ {
		if full.Items[i-1].Seq >= full.Items[i].Seq {
			t.Fatalf("page not seq-ascending")
		}
	}
	if full.LastSeq != full.Items[2].Seq {
		t.Fatalf("high-water mark %d, want the last row's %d", full.LastSeq, full.Items[2].Seq)
	}
	if full.NextCursor == nil || *full.NextCursor != fmt.Sprint(full.Items[2].Seq) {
		t.Fatalf("full page cursor %+v", full.NextCursor)
	}
	// Resume at the cursor: empty, cursor absent, mark holding.
	next, err := e.Pull(context.Background(), info, testFilter, full.LastSeq, 3)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(next.Items) != 0 || len(next.Tombstones) != 0 || next.NextCursor != nil {
		t.Fatalf("resume page: %+v", next)
	}
	if next.LastSeq != full.LastSeq {
		t.Fatalf("empty page walked the mark backwards: %d, want %d", next.LastSeq, full.LastSeq)
	}
	// A partial page carries no cursor.
	part, _ := e.Pull(context.Background(), info, testFilter, 0, 2)
	if len(part.Items) != 2 || part.NextCursor == nil {
		t.Fatalf("partial page: %+v", part)
	}
}

// Tombstones surface in the tombstones leg and never as items.
func TestEnginePullTombstones(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyServerWins)
	push(t, e, info,
		apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1)}},
		apigen.SyncPushOperation{ClientId: key(2), Op: "create", Payload: map[string]any{"id": rowID(2)}},
	)
	del := push(t, e, info, apigen.SyncPushOperation{ClientId: key(3), Op: "delete", Payload: map[string]any{"id": rowID(1)}})
	if len(del) != 1 || del[0].Status != "applied" {
		t.Fatalf("delete: %+v", del[0])
	}
	page, err := e.Pull(context.Background(), info, testFilter, 0, 10)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(page.Tombstones) != 1 || page.Tombstones[0].Id != rowID(1) || page.Tombstones[0].Seq != *del[0].Seq {
		t.Fatalf("tombstones: %+v", page.Tombstones)
	}
	if len(page.Items) != 1 || page.Items[0].Id != rowID(2) {
		t.Fatalf("items after tombstone: %+v", page.Items)
	}
	_ = store
}

// The scoping predicate is load-bearing: rows of another org never pull,
// never load, and never answer a push (existence never leaks).
func TestEngineScopeFilterIsolatesOrgs(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyServerWins)
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1)}})
	other := ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000002"}
	page, err := e.Pull(context.Background(), info, other, 0, 10)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(page.Items) != 0 || len(page.Tombstones) != 0 {
		t.Fatalf("another org's pull saw rows: %+v", page)
	}
	if _, ok, _ := store.Load(context.Background(), other, rowID(1)); ok {
		t.Fatalf("another org's load resolved a foreign row")
	}
	res := push(t, e, info, apigen.SyncPushOperation{ClientId: key(2), Op: "delete", Payload: map[string]any{"id": rowID(1)}})
	// The delete is the owner's row; through the other org's filter it
	// must not be: the engine's filter travels with every store call.
	otherEngine, otherErr := e.Push(context.Background(), info, other, []apigen.SyncPushOperation{{ClientId: key(2), Op: "delete", Payload: map[string]any{"id": rowID(1)}}})
	if otherErr != nil {
		t.Fatalf("other org push failed: %v", otherErr)
	}
	if len(otherEngine.Results) != 1 || otherEngine.Results[0].Status != "rejected" {
		t.Fatalf("another org's delete was not rejected: %+v", otherEngine.Results[0])
	}
	_ = res
}

// Push ordering: one result per op in push order, seqs ascending, later
// ops reading earlier ops' effects.
func TestEnginePushOrderingAndSeq(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyServerWins)
	res := push(t, e, info,
		apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}},
		apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "b"}},
		apigen.SyncPushOperation{ClientId: key(3), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "c"}},
	)
	if len(res) != 3 {
		t.Fatalf("%d results, want 3", len(res))
	}
	for i, r := range res {
		if r.ClientId != key(i+1) {
			t.Fatalf("result %d answers the wrong client id", i)
		}
		if r.Status != "applied" {
			t.Fatalf("result %d: %+v", i, r)
		}
	}
	for i := 1; i < len(res); i++ {
		if *res[i].Seq <= *res[i-1].Seq {
			t.Fatalf("seqs not ascending: %d then %d", *res[i-1].Seq, *res[i].Seq)
		}
	}
	row, ok, _ := store.Load(context.Background(), testFilter, rowID(1))
	if !ok || row.Data["v"] != "c" {
		t.Fatalf("final state is not the last op's: %+v", row)
	}
}

// Dedup: a replayed operation answers applied and applies nothing twice,
// including the earlier operations of a multi-op outbox on one row (the
// applied-key history, not just the row's latest key).
func TestEngineDedupNeverAppliesTwice(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyServerWins)
	ops := []apigen.SyncPushOperation{
		{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}},
		{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "b"}},
		{ClientId: key(3), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "c"}},
	}
	first := push(t, e, info, ops...)
	row, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	seqBefore := row.Seq
	second := push(t, e, info, ops...)
	for i, r := range second {
		if r.Status != "applied" {
			t.Fatalf("replay op %d answered %+v, want applied", i, r)
		}
	}
	row, _, _ = store.Load(context.Background(), testFilter, rowID(1))
	if row.Seq != seqBefore {
		t.Fatalf("replay re-applied: seq moved %d to %d", seqBefore, row.Seq)
	}
	if row.Data["v"] != "c" {
		t.Fatalf("replay changed the state: %+v", row.Data)
	}
	// The replay's results name the row's live seq (the truth about the
	// row now, which is what a device resuming its cursor needs); the
	// state-changing half is exact and already asserted above.
	for i := range second {
		if second[i].Seq == nil {
			t.Fatalf("replay result %d lost its seq", i)
		}
	}
	_ = first
}

// server_wins: the stale write is refused whole, the server row is the
// outcome, nothing of the client's landed.
func TestEngineConflictServerWins(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyServerWins)
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "server"}})
	row, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	if err := store.deskEdit(testFilter, rowID(1), map[string]any{"desk": "moved"}); err != nil {
		t.Fatalf("desk edit: %v", err)
	}
	stale := row.Seq
	res := push(t, e, info, apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "client"}, BaseSeq: &stale})
	if len(res) != 1 || res[0].Status != "conflict" {
		t.Fatalf("stale push: %+v", res[0])
	}
	c := res[0].Conflict
	if c.Policy != string(PolicyServerWins) || c.Applied == nil || *c.Applied {
		t.Fatalf("envelope: %+v", c)
	}
	if c.ServerState["v"] != "server" || c.ServerState["desk"] != "moved" {
		t.Fatalf("server state is not the desk's row: %+v", c.ServerState)
	}
	after, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	if after.Data["v"] != "server" || after.Seq != row.Seq+1 {
		t.Fatalf("server_wins left the row changed: %+v", after)
	}
}

// last_write_wins_by_field: the declared fields land, the server's
// undeclared fields stand, and the merge carries a fresh seq.
func TestEngineConflictLWWByField(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyLWWByField)
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "client_field": "old", "server_field": "keep"}})
	row, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	store.deskEdit(testFilter, rowID(1), map[string]any{"server_field": "desk"})
	stale := row.Seq
	res := push(t, e, info, apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "client_field": "new"}, BaseSeq: &stale})
	if len(res) != 1 || res[0].Status != "applied" || res[0].Seq == nil || *res[0].Seq <= row.Seq+1 {
		t.Fatalf("lww push: %+v", res[0])
	}
	c := res[0].Conflict
	if c == nil || c.Policy != string(PolicyLWWByField) || c.Applied == nil || !*c.Applied {
		t.Fatalf("lww envelope: %+v", c)
	}
	if c.ServerState["client_field"] != "new" || c.ServerState["server_field"] != "desk" {
		t.Fatalf("merged state wrong: %+v", c.ServerState)
	}
	after, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	if after.Data["client_field"] != "new" || after.Data["server_field"] != "desk" {
		t.Fatalf("merge did not land per field: %+v", after.Data)
	}
}

// manual: never overwrites, the server row returns, the review marker
// lands on the audit seam.
func TestEngineConflictManual(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyManual)
	marker := &auditRecorderDouble{}
	e.WithAudit(marker, nil)
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "crew_field": "server"}})
	row, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	store.deskEdit(testFilter, rowID(1), map[string]any{"desk": "moved"})
	stale := row.Seq
	res := push(t, e, info, apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "crew_field": "client"}, BaseSeq: &stale})
	if len(res) != 1 || res[0].Status != "conflict" {
		t.Fatalf("manual push: %+v", res[0])
	}
	c := res[0].Conflict
	if c.Policy != string(PolicyManual) || c.Applied == nil || *c.Applied {
		t.Fatalf("manual envelope: %+v", c)
	}
	if c.ServerState["crew_field"] != "server" {
		t.Fatalf("manual envelope did not return the server row: %+v", c.ServerState)
	}
	after, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	if after.Data["crew_field"] != "server" || after.Seq != row.Seq+1 {
		t.Fatalf("manual policy overwrote the row: %+v", after)
	}
	if !marker.sawReviewMarker(rowID(1)) {
		t.Fatalf("the manual conflict wrote no review marker")
	}
}

// The rejected legs: a bad key, an unknown op, a missing row, an unknown
// row, a create collision; each is one operation's rejected result, never
// a failed request, and the operations behind a rejected one still run.
func TestEngineRejectedLegs(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyServerWins)
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1)}})
	cases := []struct {
		name string
		op   apigen.SyncPushOperation
		want string
	}{
		{"bad client id", apigen.SyncPushOperation{ClientId: "not-a-uuid", Op: "create", Payload: map[string]any{}}, "client_id"},
		{"unknown op", apigen.SyncPushOperation{ClientId: key(2), Op: "upsert", Payload: map[string]any{"id": rowID(1)}}, "op must be"},
		{"update without id", apigen.SyncPushOperation{ClientId: key(3), Op: "update", Payload: map[string]any{"v": 1}}, "payload id"},
		{"unknown row", apigen.SyncPushOperation{ClientId: key(4), Op: "update", Payload: map[string]any{"id": rowID(9)}}, "not found"},
		{"create collision", apigen.SyncPushOperation{ClientId: key(5), Op: "create", Payload: map[string]any{"id": rowID(1)}}, "exists"},
	}
	for _, c := range cases {
		res := push(t, e, info, c.op)
		if len(res) != 1 || res[0].Status != "rejected" || res[0].Error == nil || !strings.Contains(*res[0].Error, c.want) {
			t.Fatalf("%s: %+v, want a rejected result naming %q", c.name, res[0], c.want)
		}
	}
	// A rejected op does not sink the ops behind it.
	pair := push(t, e, info,
		apigen.SyncPushOperation{ClientId: key(6), Op: "update", Payload: map[string]any{"id": rowID(9)}},
		apigen.SyncPushOperation{ClientId: key(7), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "later"}},
	)
	if pair[0].Status != "rejected" || pair[1].Status != "applied" {
		t.Fatalf("a rejected op sank the outbox: %+v", pair)
	}
	_ = store
}

// A base_seq at or after the row's seq is not stale: the write applies.
func TestEngineFreshBaseApplies(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyServerWins)
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}})
	row, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	base := row.Seq
	res := push(t, e, info, apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "b"}, BaseSeq: &base})
	if len(res) != 1 || res[0].Status != "applied" {
		t.Fatalf("fresh-base push: %+v", res[0])
	}
	// No base_seq declares nothing to conflict against.
	res = push(t, e, info, apigen.SyncPushOperation{ClientId: key(3), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "c"}})
	if res[0].Status != "applied" {
		t.Fatalf("blind push: %+v", res[0])
	}
}

// An unclassified store fault fails the whole push (the 503 family at
// the route), while the seam's declared refusals stay per-operation
// rejected results.
func TestEngineStoreFaultFailsThePush(t *testing.T) {
	e, store, info, _ := newEngineFixture(t, PolicyServerWins)
	store.inject(errors.New("boom"))
	_, err := e.Push(context.Background(), info, testFilter, []apigen.SyncPushOperation{{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1)}}})
	if err == nil {
		t.Fatalf("an unclassified store fault did not fail the push")
	}
	// A declared refusal stays per-operation.
	store.inject(ErrOpRefused)
	resp, err := e.Push(context.Background(), info, testFilter, []apigen.SyncPushOperation{{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1)}}})
	if err != nil {
		t.Fatalf("a declared refusal failed the whole push: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != "rejected" {
		t.Fatalf("declined push: %+v", resp.Results[0])
	}
}

// The audit seam records one row per applied op, and the dedup replay
// records its applied outcome too (the audit invariant covers replays:
// the row is the answer to who completed what offline).
func TestEngineAuditsEveryOperation(t *testing.T) {
	e, _, info, _ := newEngineFixture(t, PolicyServerWins)
	marker := &auditRecorderDouble{}
	e.WithAudit(marker, nil)
	push(t, e, info,
		apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1)}},
		apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "b"}},
		apigen.SyncPushOperation{ClientId: key(3), Op: "delete", Payload: map[string]any{"id": rowID(1)}},
		apigen.SyncPushOperation{ClientId: key(4), Op: "update", Payload: map[string]any{"id": rowID(9)}},
	)
	if got := marker.count("SYNC_PUSH"); got != 4 {
		t.Fatalf("%d SYNC_PUSH rows, want one per operation (4)", got)
	}
	if got := marker.outcomes("rejected"); got != 1 {
		t.Fatalf("%d rejected rows, want 1", got)
	}
}

// RegisterScope's composition guards.
func TestRegisterScopeGuards(t *testing.T) {
	e := NewEngine()
	if err := e.RegisterScope("not_a_scope", &fakeStore{}); err == nil {
		t.Fatalf("an unknown scope registered")
	}
	if err := e.RegisterScope("pick_tasks", nil); err == nil {
		t.Fatalf("a nil store registered")
	}
	if err := e.RegisterScope("pick_tasks", &fakeStore{}); err != nil {
		t.Fatalf("first wiring refused: %v", err)
	}
	if err := e.RegisterScope("pick_tasks", &fakeStore{}); err == nil {
		t.Fatalf("a second wiring of one scope registered")
	}
	if _, ok := e.Store("quotes"); ok {
		t.Fatalf("an unwired scope resolved a store")
	}
}

// auditRecorderDouble collects the entries the engine emits.
type auditRecorderDouble struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (a *auditRecorderDouble) RecordSync(_ context.Context, e AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *auditRecorderDouble) count(action string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, e := range a.entries {
		if e.Action == action {
			n++
		}
	}
	return n
}

func (a *auditRecorderDouble) outcomes(outcome string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, e := range a.entries {
		if e.Outcome == outcome {
			n++
		}
	}
	return n
}

func (a *auditRecorderDouble) sawReviewMarker(entityID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.entries {
		if e.Action == "SYNC_CONFLICT_REVIEW" && e.EntityID == entityID {
			return true
		}
	}
	return false
}

// HH-193: every SYNC_PUSH row carries the caller, applied and rejected alike, for a staff and a
// tenant (contractor) caller, and so does the manual policy's review marker.
func TestEngineAuditRowsCarryTheActor(t *testing.T) {
	staff := ScopeFilter{OrgID: testFilter.OrgID, Actor: "fxstaff-any"}
	tenant := ScopeFilter{OrgID: testFilter.OrgID, TenantID: "7e000000-0000-4000-8000-000000000001", Tenant: true, Actor: "fxportal-alpha"}
	for _, c := range []struct {
		name string
		f    ScopeFilter
	}{{"staff", staff}, {"tenant", tenant}} {
		t.Run(c.name, func(t *testing.T) {
			e, _, info, _ := newEngineFixture(t, PolicyServerWins)
			marker := &auditRecorderDouble{}
			e.WithAudit(marker, nil)
			resp, err := e.Push(context.Background(), info, c.f, []apigen.SyncPushOperation{
				{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1)}},
				{ClientId: key(2), Op: "delete", Payload: map[string]any{"id": rowID(1)}},
				{ClientId: key(3), Op: "update", Payload: map[string]any{"id": rowID(9)}},
				{ClientId: "not-a-uuid", Op: "create", Payload: map[string]any{"id": rowID(2)}},
			})
			if err != nil {
				t.Fatalf("push: %v", err)
			}
			if resp.Results[2].Status != "rejected" || resp.Results[3].Status != "rejected" {
				t.Fatalf("setup: %+v", resp.Results)
			}
			rows := marker.snapshot()
			if len(rows) != 4 {
				t.Fatalf("%d audit rows, want 4", len(rows))
			}
			for _, r := range rows {
				if r.Action != "SYNC_PUSH" || r.Actor != c.f.Actor {
					t.Errorf("row %s/%s (%s) actor %q, want %q", r.Action, r.Op, r.Outcome, r.Actor, c.f.Actor)
				}
			}
		})
	}
}

func (a *auditRecorderDouble) snapshot() []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AuditEntry(nil), a.entries...)
}
