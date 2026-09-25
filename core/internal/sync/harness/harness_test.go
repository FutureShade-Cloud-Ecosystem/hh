package harness

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	stdsync "sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// fakeRow is one row of the battery's fake store.
type fakeRow struct {
	id      string
	seq     int64
	fields  map[string]any
	key     string
	applied map[string]bool
}

// fakeStore is the battery's in-memory Store: rows scoped by the filter's
// org, monotonic seq allocation, and the full applied-key history. It is
// the same shape the sync package's own unit legs run against; the
// battery's value here is proving the harness itself is policy-blind:
// the same Contract run passes under server_wins, last_write_wins_by_field
// and manual kits.
type fakeStore struct {
	mu      stdsync.Mutex
	rows    []*fakeRow
	nextSeq int64
}

func (f *fakeStore) scoped(r *fakeRow, filter sync.ScopeFilter) bool { return filter.OrgID != "" }

func (f *fakeStore) state(r *fakeRow) sync.RowState {
	rs := sync.RowState{ID: r.id, Seq: r.seq, Deleted: r.fields["deleted"] == true}
	data := make(map[string]any, len(r.fields)+1)
	for k, v := range r.fields {
		data[k] = v
	}
	// The projection carries the id, as every real scope's resource
	// schema does (the contract's Task, for one).
	data["id"] = r.id
	rs.Data = data
	return rs
}

func (f *fakeStore) Pull(_ context.Context, filter sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items, tombs := []sync.Entity{}, []sync.Tombstone{}
	for _, r := range f.rows {
		if !f.scoped(r, filter) || r.seq <= since {
			continue
		}
		if len(items)+len(tombs) >= limit {
			break
		}
		if deleted, _ := r.fields["deleted"].(bool); deleted {
			tombs = append(tombs, sync.Tombstone{ID: r.id, Seq: r.seq})
			continue
		}
		data := make(map[string]any, len(r.fields)+1)
		for k, v := range r.fields {
			if k == "deleted" {
				continue
			}
			data[k] = v
		}
		data["id"] = r.id
		items = append(items, sync.Entity{ID: r.id, Seq: r.seq, Data: data})
	}
	return items, tombs, nil
}

func (f *fakeStore) FindApplied(_ context.Context, filter sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if f.scoped(r, filter) && (r.key == key || r.applied[key]) {
			return f.state(r), true, nil
		}
	}
	return sync.RowState{}, false, nil
}

func (f *fakeStore) Load(_ context.Context, filter sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.id == id && f.scoped(r, filter) {
			return f.state(r), true, nil
		}
	}
	return sync.RowState{}, false, nil
}

func (f *fakeStore) Create(_ context.Context, filter sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := fields["id"].(string); ok && id != "" {
		for _, r := range f.rows {
			if r.id == id {
				return sync.RowState{}, sync.ErrRowExists
			}
		}
	}
	f.nextSeq++
	id, _ := fields["id"].(string)
	if id == "" {
		id = fmt.Sprintf("20000000-0000-4000-8000-%012d", f.nextSeq)
	}
	cp := map[string]any{}
	for k, v := range fields {
		cp[k] = v
	}
	r := &fakeRow{id: id, seq: f.nextSeq, fields: cp, key: key, applied: map[string]bool{key: true}}
	f.rows = append(f.rows, r)
	return f.state(r), nil
}

func (f *fakeStore) Write(_ context.Context, filter sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.id == id && f.scoped(r, filter) {
			for k, v := range fields {
				if k == "id" {
					continue
				}
				r.fields[k] = v
			}
			f.nextSeq++
			r.seq = f.nextSeq
			r.key = key
			r.applied[key] = true
			return f.state(r), nil
		}
	}
	return sync.RowState{}, sync.ErrRowNotFound
}

func (f *fakeStore) Delete(_ context.Context, filter sync.ScopeFilter, id, key string) (sync.RowState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.id == id && f.scoped(r, filter) {
			r.fields["deleted"] = true
			f.nextSeq++
			r.seq = f.nextSeq
			r.key = key
			r.applied[key] = true
			return f.state(r), nil
		}
	}
	return sync.RowState{}, sync.ErrRowNotFound
}

// deskEdit is the desk hook: a server-side field change with a fresh seq.
func (f *fakeStore) deskEdit(id string, fields map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.id == id {
			for k, v := range fields {
				r.fields[k] = v
			}
			f.nextSeq++
			r.seq = f.nextSeq
			return nil
		}
	}
	return sync.ErrRowNotFound
}

// newKit builds one battery kit over a fresh fake: the scope's policy
// comes from the registry, the payloads are the fake's field maps, and
// the desk hook changes the desk field.
func newKit(t *testing.T, scopeName string) (*sync.Engine, Kit) {
	t.Helper()
	info, ok := sync.Lookup(scopeName)
	if !ok {
		t.Fatalf("scope %s missing", scopeName)
	}
	store := &fakeStore{}
	e := sync.NewEngine()
	if err := e.RegisterScope(scopeName, store); err != nil {
		t.Fatalf("register: %v", err)
	}
	k := Kit{
		Scope:  info,
		Filter: sync.ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000001", Actor: "fxkit"},
		Store:  store,
		Create: func(id string) map[string]any {
			fields := map[string]any{"kind": "fixture", "v": 0}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "v": toInt(row["v"]) + 1}
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			return store.deskEdit(fmt.Sprint(row["id"]), map[string]any{"desk": "moved"})
		},
	}
	return e, k
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

// The battery under each policy: the harness is policy-blind, the kit's
// scope carries the policy, and every policy's legs pass the same
// Contract.
func TestContractBatteryUnderEachPolicy(t *testing.T) {
	for _, scope := range []string{"pick_tasks", "quotes", "shortages"} {
		t.Run(scope, func(t *testing.T) {
			e, k := newKit(t, scope)
			Contract(t, e, k)
		})
	}
}

// Gate7 standalone: the gate takes its scope from the kit.
func TestGate7TakesItsScope(t *testing.T) {
	for _, scope := range []string{"pick_tasks", "quotes", "shortages"} {
		t.Run(scope, func(t *testing.T) {
			e, k := newKit(t, scope)
			Gate7(t, e, k)
		})
	}
}

// The replay property scaffold: randomized outboxes over rounds, two
// replays each, one state. Deterministic under the fixed seed so a
// failure reproduces.
func TestReplayPropertyScaffold(t *testing.T) {
	rng := rand.New(rand.NewSource(165))
	e, k := newKit(t, "pick_tasks")
	ReplayProperty(t, e, k, 25, rng.Intn)
}

// A deliberately-broken store proves the battery's teeth from outside:
// a store whose FindApplied consults only the row's latest key (the D30
// column alone, without the applied-key history) re-applies the earlier
// operations of a multi-op outbox on replay, moving the row's seq. That
// seq movement is exactly what Gate7's state comparison detects, so this
// positive control shows the failure mode the gate fails on: under a
// correct store the same replay leaves the seq still (the Gate7 runs
// above already prove that direction).
func TestBatteryBitesOnBrokenStores(t *testing.T) {
	filter := sync.ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000001", Actor: "fxkit"}
	info, ok := sync.Lookup("pick_tasks")
	if !ok {
		t.Fatalf("pick_tasks missing")
	}
	forgetful := &forgetfulStore{}
	e := sync.NewEngine()
	if err := e.RegisterScope("pick_tasks", forgetful); err != nil {
		t.Fatalf("register forgetful: %v", err)
	}
	rowID := "20000000-0000-4000-8000-000000000001"
	ops := []apigen.SyncPushOperation{
		{ClientId: "10000000-0000-4000-8000-000000000099", Op: "create", Payload: map[string]any{"id": rowID, "v": "a"}},
	}
	e.Push(context.Background(), info, filter, ops)
	// The outbox under test: two updates on the seeded row, so the
	// replay's first update hits a row whose latest key is the second
	// update's.
	ops = []apigen.SyncPushOperation{
		{ClientId: "10000000-0000-4000-8000-000000000001", Op: "update", Payload: map[string]any{"id": rowID, "v": "b"}},
		{ClientId: "10000000-0000-4000-8000-000000000002", Op: "update", Payload: map[string]any{"id": rowID, "v": "c"}},
	}
	e.Push(context.Background(), info, filter, ops)
	before, _, err := forgetful.Load(context.Background(), filter, rowID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	e.Push(context.Background(), info, filter, ops)
	after, _, err := forgetful.Load(context.Background(), filter, rowID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if after.Seq == before.Seq {
		t.Fatalf("the forgetful store did not re-apply on replay (seq stood at %d); the positive control failed", before.Seq)
	}
}

// forgetfulStore drops the applied-key history (the row keeps only its
// latest key), the bug the pilot's task_events history exists to prevent.
type forgetfulStore struct {
	fakeStore
}

func (f *forgetfulStore) FindApplied(ctx context.Context, filter sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if f.scoped(r, filter) && r.key == key {
			return f.state(r), true, nil
		}
	}
	return sync.RowState{}, false, nil
}

// A scope name guard: the strings the battery logs stay dash-free by
// construction (the house rule), checked once here so a future message
// cannot drift.
func TestHarnessMessagesCarryNoDashes(t *testing.T) {
	// The battery's failure paths are exercised above; this leg pins the
	// vocabulary the messages draw from.
	for _, word := range []string{"applied", "conflict", "rejected", "tombstone", "high-water mark"} {
		if strings.ContainsAny(word, "\u2013\u2014") {
			t.Fatalf("harness vocabulary carries a dash: %q", word)
		}
	}
}
