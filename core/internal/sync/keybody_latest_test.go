package sync

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// latestKeyStore answers FindApplied the way the D30 stores do: from the
// row's single idempotency column, which holds only the LATEST key. The
// full history fake (fakeStore) hides the defect these legs pin: once a row
// has moved on, an older key no longer dedups in the module.
type latestKeyStore struct{ *fakeStore }

func (s latestKeyStore) FindApplied(_ context.Context, filter ScopeFilter, key string) (RowState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if s.scoped(r, filter) && r.key == key {
			return s.state(r), true, nil
		}
	}
	return RowState{}, false, nil
}

// Create assigns a uuid row id when the payload carries none, as the
// server-assigned create of the real stores does.
func (s latestKeyStore) Create(ctx context.Context, f ScopeFilter, key string, fields map[string]any) (RowState, error) {
	if id, _ := fields["id"].(string); id == "" {
		fields = copyFields(fields)
		fields["id"] = rowID(50)
	}
	return s.fakeStore.Create(ctx, f, key, fields)
}

func latestKeyFixture(t *testing.T) (*Engine, *fakeStore, *memLedger, ScopeInfo) {
	t.Helper()
	e, base, info, _ := newEngineFixture(t, PolicyServerWins)
	e.stores[info.Name] = latestKeyStore{base}
	led := newMemLedger()
	e.WithKeyLedger(led)
	return e, base, led, info
}

// A row that has moved on: create k1, update k2, update k3. Only k3 is in
// the module's single column; k1 and k2 are known to the ledger alone.
func movedOn(t *testing.T) (*Engine, *fakeStore, ScopeInfo) {
	t.Helper()
	e, store, _, info := latestKeyFixture(t)
	res := push(t, e, info,
		apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}},
		apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "b"}},
		apigen.SyncPushOperation{ClientId: key(3), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "c"}},
	)
	for i, r := range res {
		if r.Status != "applied" {
			t.Fatalf("setup op %d: %+v", i, r)
		}
	}
	return e, store, info
}

// P1: a bound key reused with a different body is refused even when the
// module's dedup no longer finds it.
func TestKeyBodyLatestKeyStoreRefusesReuseOfAMovedOnKey(t *testing.T) {
	t.Run("update key", func(t *testing.T) {
		e, store, info := movedOn(t)
		before, _, _ := store.Load(context.Background(), testFilter, rowID(1))
		got := push(t, e, info, apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "EVIL"}})
		wantKeyReused(t, got[0])
		after, _, _ := store.Load(context.Background(), testFilter, rowID(1))
		if after.Seq != before.Seq || after.Data["v"] != "c" {
			t.Fatalf("a refused reuse moved the row: before %+v after %+v", before, after)
		}
	})
	t.Run("create key makes no second row", func(t *testing.T) {
		e, store, info := movedOn(t)
		got := push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(2), "v": "EVIL"}})
		wantKeyReused(t, got[0])
		if n := len(store.rows); n != 1 {
			t.Fatalf("%d rows, want the one the create made", n)
		}
	})
	t.Run("delete key", func(t *testing.T) {
		e, store, _, info := latestKeyFixture(t)
		push(t, e, info,
			apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}},
			apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "b"}},
			apigen.SyncPushOperation{ClientId: key(3), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "c"}})
		got := push(t, e, info, apigen.SyncPushOperation{ClientId: key(2), Op: "delete", Payload: map[string]any{"id": rowID(1)}})
		wantKeyReused(t, got[0])
		if row, _, _ := store.Load(context.Background(), testFilter, rowID(1)); row.Deleted {
			t.Fatalf("a refused reuse deleted the row")
		}
	})
}

// Stored and equal while the module's dedup misses: the key already
// applied, so it must not apply again, and it answers applied from the
// current row (create included, by the entity id the ledger stored).
func TestKeyBodyLatestKeyStoreReplayOfAMovedOnKeyNeverReapplies(t *testing.T) {
	e, store, info := movedOn(t)
	before, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	for name, op := range map[string]apigen.SyncPushOperation{
		"create": {ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}},
		"update": {ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "b"}},
	} {
		got := push(t, e, info, op)[0]
		if got.Status != "applied" || got.Seq == nil || *got.Seq != before.Seq || got.Error != nil {
			t.Errorf("%s replay answered %+v, want applied at the row's current seq %d", name, got, before.Seq)
		}
	}
	after, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	if after.Seq != before.Seq || after.Data["v"] != "c" {
		t.Fatalf("a replay re-applied: before %+v after %+v", before, after)
	}
}

// A create with no client id: the server assigns the row, so the replay can
// only be answered from the entity id the ledger stored at Remember.
func TestKeyBodyLatestKeyStoreServerAssignedCreateReplays(t *testing.T) {
	e, store, _, info := latestKeyFixture(t)
	first := push(t, e, info,
		apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"v": "a"}})[0]
	if first.Status != "applied" {
		t.Fatalf("setup: %+v", first)
	}
	id := store.rows[0].id
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": id, "v": "b"}})
	rows := len(store.rows)
	got := push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"v": "a"}})[0]
	if got.Status != "applied" || got.Seq == nil {
		t.Fatalf("replay answered %+v, want applied", got)
	}
	if len(store.rows) != rows {
		t.Fatalf("the replayed create made a second row: %d rows, want %d", len(store.rows), rows)
	}
}
