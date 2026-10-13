package sync

import (
	"context"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// refusingStore refuses Create or Write inside the module, after the
// engine has decided the operation may apply: the module's own invariant
// (ErrOpRefused) or a create collision (ErrRowExists).
type refusingStore struct {
	*fakeStore
	createErr error
	writeErr  error
}

func (s *refusingStore) Create(ctx context.Context, f ScopeFilter, key string, fields map[string]any) (RowState, error) {
	if s.createErr != nil {
		return RowState{}, s.createErr
	}
	return s.fakeStore.Create(ctx, f, key, fields)
}

func (s *refusingStore) Write(ctx context.Context, f ScopeFilter, id, key string, fields map[string]any) (RowState, error) {
	if s.writeErr != nil {
		return RowState{}, s.writeErr
	}
	return s.fakeStore.Write(ctx, f, id, key, fields)
}

func refusalFixture(t *testing.T) (*Engine, *refusingStore, *memLedger, ScopeInfo) {
	t.Helper()
	e, base, info, _ := newEngineFixture(t, PolicyServerWins)
	rs := &refusingStore{fakeStore: base}
	e.stores[info.Name] = rs
	led := newMemLedger()
	e.WithKeyLedger(led)
	return e, rs, led, info
}

// Store before apply: the ledger is written only after the module applied.
// A module that refuses a create leaves the ledger empty and the key free,
// so the corrected retry under the same key (a different body) lands.
func TestKeyBodyModuleRefusedCreateLeavesTheKeyFree(t *testing.T) {
	for name, refusal := range map[string]error{
		"op refused": fmt.Errorf("%w: window closed", ErrOpRefused),
		"row exists": ErrRowExists,
	} {
		t.Run(name, func(t *testing.T) {
			e, store, led, info := refusalFixture(t)
			store.createErr = refusal
			bad := apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "bad"}}
			if r := push(t, e, info, bad)[0]; r.Status != "rejected" || r.Error == nil {
				t.Fatalf("setup, want a module refusal: %+v", r)
			}
			if led.size() != 0 {
				t.Fatalf("a module refused create stored %d hashes, want none", led.size())
			}
			store.createErr = nil
			good := bad
			good.Payload = map[string]any{"id": rowID(1), "v": "corrected"}
			if r := push(t, e, info, good)[0]; r.Status != "applied" {
				t.Fatalf("the corrected retry under the free key answered %+v, want applied", r)
			}
			if led.size() != 1 {
				t.Fatalf("%d hashes after the corrected retry, want 1", led.size())
			}
		})
	}
}

// The same for an update the module refuses inside Write.
func TestKeyBodyModuleRefusedUpdateLeavesTheKeyFree(t *testing.T) {
	e, store, led, info := refusalFixture(t)
	push(t, e, info, apigen.SyncPushOperation{ClientId: key(1), Op: "create", Payload: map[string]any{"id": rowID(1), "v": "a"}})
	before := led.size()
	store.writeErr = fmt.Errorf("%w: locked", ErrOpRefused)
	bad := apigen.SyncPushOperation{ClientId: key(2), Op: "update", Payload: map[string]any{"id": rowID(1), "v": "bad"}}
	if r := push(t, e, info, bad)[0]; r.Status != "rejected" || r.Error == nil {
		t.Fatalf("setup, want a module refusal: %+v", r)
	}
	if led.size() != before {
		t.Fatalf("a module refused update stored a hash (%d, was %d)", led.size(), before)
	}
	store.writeErr = nil
	good := bad
	good.Payload = map[string]any{"id": rowID(1), "v": "corrected"}
	if r := push(t, e, info, good)[0]; r.Status != "applied" {
		t.Fatalf("the corrected retry under the free key answered %+v, want applied", r)
	}
	row, _, _ := store.Load(context.Background(), testFilter, rowID(1))
	if row.Data["v"] != "corrected" {
		t.Fatalf("the corrected retry did not land: %+v", row)
	}
}
