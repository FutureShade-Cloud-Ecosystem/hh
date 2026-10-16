//go:build integration

// channels_admin_gate_integration_test.go: the change 7 channel-create gate
// against the migrated + seeded database. Founding is the admins' pen (the
// role matrix's Admin cell on house-messages, the general manager): a
// frontline caller's founding is refused before any wall runs, and the
// refusal plants no room and no seat. Red first: before the gate the
// frontline founding answered created=true (the recorded red evidence).

package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// A frontline caller founding a TEAM room is refused with the gate's own
// error, and the refusal writes nothing: no room by that name, no seat for
// the refused caller. The same name founded by the admin answers created,
// so the refusal is the gate's and not the name's.
func TestIntegration_CreateChannel_NonAdmin_Is_Refused(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	name := fmt.Sprintf("Frontline probe %d", time.Now().UnixNano())
	if _, _, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
		Kind: KindTeam, Name: name,
	}); !errors.Is(err, ErrNotChannelAdmin) {
		t.Fatalf("frontline founding must answer the admin gate, got %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM msg_channels WHERE name = $1`, name).Scan(&n); err != nil {
		t.Fatalf("count probe rooms: %v", err)
	}
	if n != 0 {
		t.Fatalf("the refused founding planted %d room(s)", n)
	}
	if _, _, ok := memberRow(ctx, t, pool, "00000000-0000-4000-9000-000000000000", uSamRivera); ok {
		t.Fatalf("the refused caller was seated")
	}

	ch, created, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
		Kind: KindTeam, Name: name,
	})
	if err != nil || !created {
		t.Fatalf("admin founding of the same name: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, ch.ID)
	})
	if role, _, ok := memberRow(ctx, t, pool, ch.ID, uAlexMorgan); !ok || role != "OWNER" {
		t.Fatalf("the admin founder must hold the room's OWNER row, got role=%q ok=%v", role, ok)
	}
}

// A branch-scoped non-admin gets the gate on a slug collision too: the gate
// owns the founding's first answer, so the branch wall's collision arm runs
// only for a caller the matrix has already made an admin.
func TestIntegration_CreateChannel_Gate_Owns_The_Branch_Collision_First(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	var depotTeam string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, branch_id, slug, name, tier, auto_scope)
		VALUES ('TEAM', $1, 'gate-depot-team-kingston', 'Gate probe depot team', 'STANDARD', 'BRANCH')
		RETURNING id::text`, branchKingston).Scan(&depotTeam); err != nil {
		t.Fatalf("plant the Kingston depot team room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, depotTeam)
	})

	if _, _, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
		Kind: KindTeam, Name: "gate-depot-team-kingston",
	}); !errors.Is(err, ErrNotChannelAdmin) {
		t.Fatalf("frontline founding on a foreign branch room must answer the admin gate, got %v", err)
	}
	if _, _, ok := memberRow(ctx, t, pool, depotTeam, uSamRivera); ok {
		t.Fatalf("the refused founding must leave the caller a non member")
	}
}
