//go:build integration

// syncscope_read_fence_integration_test.go: UX-108. The messages scope's pull
// holds two fences: the membership join (the seat) and the room fence
// (admitsRoom: the money rooms follow the payment trail and the branch reach).
// Load spoke both. FindApplied spoke only the seat, so a member the room
// refuses still resolved a key to its row and its seq.
//
// The leg is planted on the room fence, the leg that lives outside the SQL:
// the money room #payments-trenton, Sam (Trenton) holding the trail, Casey
// (Kingston) seated but holding none, a stale seat the room still refuses.
// The scope must answer not found for Casey, exactly as it answers not found
// for a key nobody ever applied.
package messaging

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// ux108Room is the money room the leg plants on: a member_roles list, so the
// room fence follows the payment trail and the branch reach (payments-trenton).
const ux108Room = chPaymentsTrenton

// ux108Fixture is the messages scope over the real database, with the module's
// OWN room fence bound (Service.RoomAdmits, the money room rule) and Sam
// holding the payment trail while Casey holds none.
type ux108Fixture struct {
	pool  *pgxpool.Pool
	store *messagesSyncStore
}

func newUX108Fixture(t *testing.T) *ux108Fixture {
	t.Helper()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	// Sam holds the trail (a platform identity, a live grant, a reader that
	// says yes to him); Casey holds none, so the money room refuses him even
	// though the seat table still carries him.
	bindPaymentTrailFor(t, pool, svc, uSamRivera)
	link := func(_ context.Context, sub string) (string, bool) {
		uid, ok := syncSubOf[sub]
		return uid, ok
	}
	store := NewMessagesSyncStore(repo, link).WithRoomFence(svc.RoomAdmits)
	// Seat both: Sam's seat is what lets him write in the room, Casey's is the
	// stale seat (seated, no trail) the room fence refuses.
	for _, uid := range []string{uSamRivera, uCaseyDoyle} {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO msg_members (channel_id, user_id, role, source) VALUES ($1::uuid, $2::uuid, 'MEMBER', 'EXPLICIT')
			 ON CONFLICT (channel_id, user_id) DO NOTHING`, ux108Room, uid); err != nil {
			t.Fatalf("seat %s in the money room: %v", uid, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(),
				`DELETE FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid`, ux108Room, uid)
		})
	}
	return &ux108Fixture{pool: pool, store: store}
}

// ux108Filters are the two callers' scope filters. The org is a uuid because
// the engine's ledger holds the key under it (the messaging tables carry no
// org column, so the scope itself reads nothing from it).
func ux108Filters() (sync.ScopeFilter, sync.ScopeFilter) {
	sam := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "sub-sam"}
	casey := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "sub-casey"}
	return sam, casey
}

// TestMessagesSyncFindAppliedHoldsTheRoomFence: a key applied in a money room
// the caller may not read answers not found, field for field the answer a key
// nobody ever applied answers. Before the fix the room fence reached Load
// only, and the refused member resolved the key to its row and its seq.
func TestMessagesSyncFindAppliedHoldsTheRoomFence(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, casey := ux108Filters()

	// The room fence is the room's own: Sam is admitted, Casey is refused.
	if !admitsRoom(ctx, f.store.fence, uSamRivera, ux108Room) {
		t.Fatalf("the fixture is wrong: the trail holder is refused the money room")
	}
	if admitsRoom(ctx, f.store.fence, uCaseyDoyle, ux108Room) {
		t.Fatalf("the fixture is wrong: the member without the trail is admitted the money room")
	}

	key := uuid.NewString()
	rs, err := f.store.Create(ctx, sam, key, map[string]any{"channel_id": ux108Room, "body": "ux108: a money room post"})
	if err != nil {
		t.Fatalf("the trail holder posts in the room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE id = $1::uuid`, rs.ID)
	})

	// The key's owner keeps the row: applied, with its seq.
	row, found, err := f.store.FindApplied(ctx, sam, key)
	if err != nil || !found {
		t.Fatalf("the poster finds his own key: found %v err %v", found, err)
	}
	if row.Seq != rs.Seq {
		t.Errorf("the poster's key answers with the row's seq: got %v want %v", row.Seq, rs.Seq)
	}

	// The refused member resolves nothing: the answer a key nobody applied
	// gives, field for field.
	absent := uuid.NewString()
	want, wantFound, err := f.store.FindApplied(ctx, casey, absent)
	if err != nil || wantFound {
		t.Fatalf("the absent key answers not found: found %v err %v", wantFound, err)
	}
	got, gotFound, err := f.store.FindApplied(ctx, casey, key)
	if err != nil {
		t.Fatalf("the refused member's lookup failed: %v", err)
	}
	if gotFound {
		t.Fatalf("a member the room refuses resolves the key: %+v (the room's own answer is not found)", got)
	}
	if got.ID != want.ID || got.Seq != want.Seq || got.Deleted != want.Deleted || got.Data != nil {
		t.Errorf("the refused member's answer differs from the absent key's answer: got %+v want %+v", got, want)
	}
}

// TestMessagesSyncEngineReplayOfARoomKeyCarriesNothingOfTheRow: the real
// engine over the real key ledger. Sam applies a post in the money room; a
// replay of that key by a member the room refuses carries nothing of the
// row: not its seq, nothing of it. Sam's own replay still answers applied
// with the row's seq, and nothing ran twice.
//
// The ledger row is planted the way a key bound before migration 0449 reads
// back: bound_by NULL, its binder unknowable. The engine's gate (a key whose
// binder is known and is not the replaying caller) reads bound_by and skips
// such a row entirely, so the scope's own reads are the only fence left
// holding the room. A key bound after the migration is refused by that gate
// with the absent key's own refusal, which the leg below also proves.
func TestMessagesSyncEngineReplayOfARoomKeyCarriesNothingOfTheRow(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, casey := ux108Filters()

	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("messages", f.store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("messages")
	if !ok {
		t.Fatal("messages missing from the registry")
	}
	key := uuid.NewString()
	payload := map[string]any{"channel_id": ux108Room, "body": "ux108: a money room post"}
	op := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: payload}

	res, err := engine.Push(ctx, info, sam, []apigen.SyncPushOperation{op})
	if err != nil || res.Results[0].Status != "applied" || res.Results[0].Seq == nil {
		t.Fatalf("Sam applies his post: %+v %v", res.Results[0], err)
	}
	first := res.Results[0]
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM msg_messages WHERE idempotency_key = $1::uuid`, key)
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, key)
	})

	// The pre 0449 shape: the ledger row knows no binder.
	if _, err := f.pool.Exec(ctx, `UPDATE sync_op_hashes SET bound_by = NULL WHERE client_id = $1::uuid`, key); err != nil {
		t.Fatalf("plant the pre 0449 ledger row: %v", err)
	}

	res, err = engine.Push(ctx, info, casey, []apigen.SyncPushOperation{op})
	if err != nil {
		t.Fatalf("Casey's replay failed the push: %v", err)
	}
	replay := res.Results[0]
	if replay.Status != "applied" || replay.Seq != nil {
		t.Errorf("a replay by a member the room refuses carries nothing of the row: status %q seq %v error %v",
			replay.Status, replay.Seq, replay.Error)
	}
	if replay.Conflict != nil {
		t.Errorf("a replay by a member the room refuses carries no conflict: %+v", replay.Conflict)
	}

	// Another body under the same key: the engine's own answer (the scope is
	// not consulted on the hash path), and it carries nothing of the row.
	other := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{
		"channel_id": ux108Room, "body": "ux108: another body"}}
	res, err = engine.Push(ctx, info, casey, []apigen.SyncPushOperation{other})
	if err != nil {
		t.Fatalf("Casey's other body failed the push: %v", err)
	}
	if res.Results[0].Status != "rejected" || res.Results[0].Seq != nil {
		t.Errorf("another body under the same key is refused and carries nothing of the row: %+v", res.Results[0])
	}

	// The key's owner keeps the bound answer, with the row's seq.
	res, err = engine.Push(ctx, info, sam, []apigen.SyncPushOperation{op})
	if err != nil || res.Results[0].Status != "applied" || res.Results[0].Seq == nil || *res.Results[0].Seq != *first.Seq {
		t.Errorf("the poster's own replay still answers applied with the row's seq: %+v %v", res.Results[0], err)
	}
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM msg_messages WHERE idempotency_key = $1::uuid`, key).Scan(&n); err != nil || n != 1 {
		t.Errorf("the replay ran nothing twice: %d messages under the key, %v", n, err)
	}
}
