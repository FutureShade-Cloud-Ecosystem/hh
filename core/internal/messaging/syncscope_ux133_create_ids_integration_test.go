//go:build integration

// syncscope_ux133_create_ids_integration_test.go: UX-133 round 2. The messages create takes the canonical id form for
// both of its id fields. A member on a free key who pushed a thread_root_id Postgres refuses used to fail the whole
// push with 22P02, again on every retry, so one bad op stuck the outbox; a bad channel_id met the room fence's "row not
// found" instead. The body's shape is now refused for that op alone, and the refusal reads the same for every caller
// and every key state.
package messaging

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

func ux133CreateBadBodies() map[string]map[string]any {
	return map[string]map[string]any{
		"thread_root_urn":  {"channel_id": ux108Room, "body": "ux133r2", "thread_root_id": "urn:uuid:" + uuid.NewString()},
		"thread_root_junk": {"channel_id": ux108Room, "body": "ux133r2", "thread_root_id": "not-a-uuid"},
		"channel_urn":      {"channel_id": "urn:uuid:" + ux108Room, "body": "ux133r2"},
		"channel_junk":     {"channel_id": "not-a-uuid", "body": "ux133r2"},
	}
}

func ux133MessagesEngine(t *testing.T, f *ux108Fixture) (*sync.Engine, sync.ScopeInfo) {
	t.Helper()
	link := func(_ context.Context, sub string) (string, bool) {
		uid, ok := syncSubOf[sub]
		return uid, ok
	}
	repo := NewRepository(f.pool)
	svc := NewService(repo)
	bindPaymentTrailFor(t, f.pool, svc, uSamRivera)
	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("messages", NewMessagesSyncStore(repo, link).WithRoomFence(svc.RoomAdmits)); err != nil {
		t.Fatalf("register messages: %v", err)
	}
	info, ok := sync.Lookup("messages")
	if !ok {
		t.Fatal("messages missing from the registry")
	}
	return engine, info
}

// TestUX133MessagesCreateRefusesABadIDAlone: on a free key, with the production room fence wired, a create whose
// channel_id or thread_root_id is not the canonical form is refused alone; the valid ops in the same batch land,
// including one with an empty thread_root_id.
func TestUX133MessagesCreateRefusesABadIDAlone(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, _ := ux108Filters()
	engine, info := ux133MessagesEngine(t, f)
	for name, bad := range ux133CreateBadBodies() {
		ops := []apigen.SyncPushOperation{
			{ClientId: uuid.NewString(), Op: "create", Payload: map[string]any{"channel_id": ux108Room, "body": "ux133r2 before", "thread_root_id": ""}},
			{ClientId: uuid.NewString(), Op: "create", Payload: bad},
			{ClientId: uuid.NewString(), Op: "create", Payload: map[string]any{"channel_id": ux108Room, "body": "ux133r2 after"}},
		}
		res, err := engine.Push(ctx, info, sam, ops)
		if err != nil {
			t.Errorf("%s: the push failed as a whole: %v", name, err)
			continue
		}
		if len(res.Results) != 3 {
			t.Errorf("%s: want 3 results, got %d", name, len(res.Results))
			continue
		}
		if res.Results[1].Status != "rejected" || res.Results[1].Error == nil {
			t.Errorf("%s: want the bad op rejected, got %s", name, ux108R3Answer(res.Results[1]))
		}
		for _, i := range []int{0, 2} {
			if res.Results[i].Status != "applied" {
				t.Errorf("%s: want op %d applied, got %s", name, i, ux108R3Answer(res.Results[i]))
			}
		}
	}
}

// TestUX133MessagesCreateIDRefusalIsOneAnswer: the refusal names only the body's shape. It reads byte for byte the
// same for a caller who belongs to the channel and one who does not, and under a free key and a spent one.
func TestUX133MessagesCreateIDRefusalIsOneAnswer(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, casey := ux108Filters()
	engine, info := ux133MessagesEngine(t, f)
	answer := func(c sync.ScopeFilter, spent bool, p map[string]any) string {
		key := uuid.NewString()
		if spent {
			// A key another actor bound to a row, as a spent key reads to a different caller.
			if _, err := f.pool.Exec(ctx,
				`INSERT INTO sync_op_hashes (org_id, tenant_id, scope, client_id, body_hash, entity_id, bound_by)
				 VALUES ($1::uuid, '', 'messages', $2::uuid, $3, $4, 'sub-other')`,
				c.OrgID, key, "0000000000000000000000000000000000000000000000000000000000000000", uuid.NewString()); err != nil {
				t.Fatalf("plant the spent key: %v", err)
			}
			t.Cleanup(func() {
				_, _ = f.pool.Exec(context.Background(), `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, key)
			})
		}
		res, err := engine.Push(ctx, info, c, []apigen.SyncPushOperation{{ClientId: key, Op: "create", Payload: p}})
		if err != nil {
			return "FAULT: " + err.Error()
		}
		r := res.Results[0]
		r.ClientId = ""
		b, _ := json.Marshal(r)
		return string(b)
	}
	for name, p := range ux133CreateBadBodies() {
		want := answer(sam, false, p)
		t.Logf("%s: %s", name, want)
		if want == "" || want[0] == 'F' {
			t.Errorf("%s: member on a free key: %s", name, want)
			continue
		}
		for who, c := range map[string]sync.ScopeFilter{"member": sam, "nonmember": casey} {
			for _, spent := range []bool{false, true} {
				if got := answer(c, spent, p); got != want {
					t.Errorf("%s/%s/spent=%v: %s, want %s", name, who, spent, got, want)
				}
			}
		}
	}
}
