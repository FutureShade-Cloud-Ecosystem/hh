//go:build integration

// syncscope_ux133_id_shape_integration_test.go: UX-133. Every messaging sync id gate takes the canonical 36 character
// id form only. uuid.Validate also accepts urn:uuid:, braced and 32 hex forms, which a store's Load and existence
// reads then hand to `$n::uuid` and Postgres refuses; the fault would fail a whole push (UX-130's case).
//
// Reach, settled on a real database: the engine's bound key gate takes its id from the ledger's EntityID (written by
// the store or by validOp checked update and delete ids, so canonical) and falls back to payload["id"] only when the
// ledger entity is empty. A create's payload id is checked by no one, so a bound key with an empty entity (not
// minted by the engine today) reaches Load with any string. The store tests below prove the fault at the seam; the
// push tests prove it through the engine.
package messaging

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

func ux133BadIDs() map[string]string {
	u := uuid.NewString()
	return map[string]string{
		"urn":    "urn:uuid:" + u,
		"braced": "{" + u + "}",
		"hex32":  hex32(u),
	}
}

func hex32(u string) string {
	out := make([]byte, 0, 32)
	for i := 0; i < len(u); i++ {
		if u[i] != '-' {
			out = append(out, u[i])
		}
	}
	return string(out)
}

type ux133Store interface {
	sync.Store
	sync.BoundEntityChecker
}

func ux133Stores(f *ux108Fixture) map[string]ux133Store {
	link := func(_ context.Context, sub string) (string, bool) {
		uid, ok := syncSubOf[sub]
		return uid, ok
	}
	repo := NewRepository(f.pool)
	return map[string]ux133Store{
		"messages":     NewMessagesSyncStore(repo, link),
		"acks":         NewAcksSyncStore(repo, link),
		"notify_prefs": NewNotifyPrefsSyncStore(repo, link),
	}
}

// TestUX133StoresAnswerNotFoundForANonCanonicalID: Load and BoundEntityExists answer a miss, never a fault.
func TestUX133StoresAnswerNotFoundForANonCanonicalID(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, _ := ux108Filters()
	for name, st := range ux133Stores(f) {
		for form, id := range ux133BadIDs() {
			if _, visible, err := st.Load(ctx, sam, id); err != nil || visible {
				t.Errorf("%s Load(%s): want a clean miss, got visible=%v err=%v", name, form, visible, err)
			}
			if ok, err := st.BoundEntityExists(ctx, sam, id); err != nil || ok {
				t.Errorf("%s BoundEntityExists(%s): want false and no fault, got %v err=%v", name, form, ok, err)
			}
		}
	}
}

// TestUX133PushOnABoundKeyWithAnEmptyEntityRefusesAlone: through the engine, the payload id fallback.
func TestUX133PushOnABoundKeyWithAnEmptyEntityRefusesAlone(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	_, casey := ux108Filters()
	link := func(_ context.Context, sub string) (string, bool) {
		uid, ok := syncSubOf[sub]
		return uid, ok
	}
	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	repo := NewRepository(f.pool)
	for _, name := range []string{"messages", "acks", "notify_prefs"} {
		var st sync.Store
		switch name {
		case "messages":
			st = NewMessagesSyncStore(repo, link)
		case "acks":
			st = NewAcksSyncStore(repo, link)
		default:
			st = NewNotifyPrefsSyncStore(repo, link)
		}
		if err := engine.RegisterScope(name, st); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	for _, name := range []string{"messages", "acks", "notify_prefs"} {
		info, ok := sync.Lookup(name)
		if !ok {
			t.Fatalf("%s missing from the registry", name)
		}
		for form, id := range ux133BadIDs() {
			key := uuid.NewString()
			// A key bound by another actor with no entity recorded: the gate falls back to the payload id.
			if _, err := f.pool.Exec(ctx,
				`INSERT INTO sync_op_hashes (org_id, tenant_id, scope, client_id, body_hash, entity_id, bound_by)
				 VALUES ($1::uuid, '', $2, $3::uuid, $4, '', 'sub-sam')`,
				casey.OrgID, name, key, "0000000000000000000000000000000000000000000000000000000000000000"); err != nil {
				t.Fatalf("plant the bound key: %v", err)
			}
			t.Cleanup(func() {
				_, _ = f.pool.Exec(context.Background(), `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, key)
			})
			op := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{
				"id": id, "channel_id": ux108Room, "message_id": uuid.NewString(), "body": "ux133", "method": "TAP"}}
			res, err := engine.Push(ctx, info, casey, []apigen.SyncPushOperation{op})
			if err != nil {
				t.Errorf("%s/%s: the push failed as a whole: %v", name, form, err)
				continue
			}
			if res.Results[0].Status != "rejected" || res.Results[0].Error == nil {
				t.Errorf("%s/%s: want the fenced refusal, got %s", name, form, ux108R3Answer(res.Results[0]))
			}
		}
	}
}
