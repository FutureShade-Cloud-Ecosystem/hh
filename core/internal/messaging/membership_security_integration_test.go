//go:build integration

// membership_security_integration_test.go: the security legs of the
// membership writes against the migrated + seeded database, the reviewer's
// own repro as the red first evidence: a direct message's membership is
// fixed at its two parties, membership management belongs to the owner and
// the roles the role matrix gives Admin on house-messages, a room the
// branch model derives refuses a target the model would not derive in, and
// a DERIVED row is never rewritten to EXPLICIT by an add.

package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// uatInsideSalesID resolves the UAT inside sales persona's user id: the
// ACTIVE, INSIDE_SALES holder of a Trenton grant alone (0077 seeds the
// account by email), a target who reaches the Trenton branch without
// holding a seat in the yard room until the test seats one. The id is
// generated per database (0077 resolves the row by email), so no constant
// can name it; a missing persona is a setup fault, not a skip.
func uatInsideSalesID(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = 'scrubbed-0009@example.invalid' AND status = 'ACTIVE'`).
		Scan(&id); err != nil {
		t.Fatalf("resolve the uat inside sales persona: %v", err)
	}
	return id
}

// The reviewer's exact sequence: the general manager opens a DM with the
// yard manager, then tries to seat the sales manager in it; the sales
// manager (were they seated) removing a party or the owner. Every leg must
// refuse, and the room must keep exactly its two parties.
func TestIntegration_Membership_DM_Is_Fixed_At_Two_Parties(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	dm, created, err := svc.CreateDM(ctx, gm, uCaseyDoyle)
	if err != nil || !created {
		t.Fatalf("open dm: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, dm.ID)
	})

	// No one seats a third party in a direct message, not even the room's
	// own opener (its OWNER row).
	if _, err := svc.AddMember(ctx, gm, AddMemberInput{ChannelID: dm.ID, UserID: uSamRivera}); !errors.Is(err, ErrMembershipFixed) {
		t.Fatalf("a third party into a dm must refuse, got %v", err)
	}
	if ok, _ := repo.IsMember(ctx, dm.ID, uSamRivera); ok {
		t.Fatalf("the third party was seated")
	}

	// And nobody removes either party: the MEMBER party and the OWNER row
	// alike are the room's fixed shape.
	for _, party := range []string{uCaseyDoyle, uAlexMorgan} {
		if err := svc.RemoveMember(ctx, gm, dm.ID, party); !errors.Is(err, ErrMembershipFixed) {
			t.Fatalf("removing party %s from a dm must refuse, got %v", party, err)
		}
	}
	for _, party := range []string{uCaseyDoyle, uAlexMorgan} {
		if ok, _ := repo.IsMember(ctx, dm.ID, party); !ok {
			t.Fatalf("the dm lost a party")
		}
	}
}

// Membership management belongs to the room's owner and to the roles the
// role matrix gives Admin on house-messages (the general manager, the one
// admin cell default.json carries): a plain member neither seats nor drops,
// the owner does, and the admin does.
func TestIntegration_Membership_Managed_By_Owner_Or_Admin(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	sam := samCaller()
	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	// The composition root publishes the decided matrix for the wall; the
	// test process has no root, so the leg publishes it (and restores the
	// previous value after).
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)
	room, _, err := svc.CreateChannel(ctx, sam, CreateChannelInput{
		Kind: KindTeam, Name: fmt.Sprintf("Owner wall probe %d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("found room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, room.ID)
	})

	// The owner seats a member.
	if _, err := svc.AddMember(ctx, sam, AddMemberInput{ChannelID: room.ID, UserID: uCaseyDoyle}); err != nil {
		t.Fatalf("owner seats casey: %v", err)
	}

	// A plain member (Casey, not the owner, not an admin) manages nobody.
	casey := Caller{UserID: uCaseyDoyle, RoleCode: "YARD_MANAGER", Scope: "BRANCH",
		Branches: map[string]bool{branchKingston: true}}
	if _, err := svc.AddMember(ctx, casey, AddMemberInput{ChannelID: room.ID, UserID: uAlexMorgan}); !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("a plain member seating someone must refuse, got %v", err)
	}
	if err := svc.RemoveMember(ctx, casey, room.ID, uSamRivera); !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("a plain member removing the owner must refuse, got %v", err)
	}

	// The admin cell (general_manager) manages the room once a member: the
	// assertMember wall still runs first, so an admin outside the room
	// manages nothing (no one adds themselves to a room they are not in).
	if _, err := svc.AddMember(ctx, gm, AddMemberInput{ChannelID: room.ID, UserID: uSamRivera}); !errors.Is(err, ErrNotMember) {
		t.Fatalf("an admin outside the room must meet the member wall, got %v", err)
	}
	if _, err := svc.AddMember(ctx, sam, AddMemberInput{ChannelID: room.ID, UserID: uAlexMorgan}); err != nil {
		t.Fatalf("owner seats the gm: %v", err)
	}
	if err := svc.RemoveMember(ctx, gm, room.ID, uCaseyDoyle); err != nil {
		t.Fatalf("admin removes a plain member: %v", err)
	}
	if _, err := svc.AddMember(ctx, gm, AddMemberInput{ChannelID: room.ID, UserID: uCaseyDoyle}); err != nil {
		t.Fatalf("admin seats the member back: %v", err)
	}

	// A removal never drops the last OWNER: the founder removing
	// themselves is refused, the room keeps its owner.
	if err := svc.RemoveMember(ctx, sam, room.ID, uSamRivera); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("the last owner removing themselves must refuse, got %v", err)
	}
	if role, _, ok := memberRow(ctx, t, pool, room.ID, uSamRivera); !ok || role != "OWNER" {
		t.Fatalf("the room lost its owner, got role=%q ok=%v", role, ok)
	}
}

// A room whose membership the branch model derives (#yard-trenton,
// auto_scope BRANCH) refuses a target the model would not derive in: the
// Kingston yard manager cannot be seated in the Trenton yard. A target who
// reaches the branch can be. And an add over a DERIVED row answers the row
// as it stands, never rewriting it to EXPLICIT.
func TestIntegration_Membership_Branch_Derived_Rooms_Refuse_Foreign_Targets(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	// The composition root publishes the decided matrix for the wall; the
	// test process has no root, so the leg publishes it (and restores the
	// previous value after).
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)
	// The general manager reaches every branch, so SyncMembership derives
	// them into the yard room: the member wall passes and the target wall
	// is what these legs prove.
	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatalf("sync gm: %v", err)
	}

	// Casey (Kingston only) is not derivable into #yard-trenton.
	if _, err := svc.AddMember(ctx, gm, AddMemberInput{ChannelID: chYardTrenton, UserID: uCaseyDoyle}); !errors.Is(err, ErrTargetNotInBranch) {
		t.Fatalf("a Kingston target in the Trenton yard must refuse, got %v", err)
	}
	if ok, _ := repo.IsMember(ctx, chYardTrenton, uCaseyDoyle); ok {
		t.Fatalf("the foreign target was seated")
	}

	// Sam (Trenton) holds a DERIVED row there; an add over it answers the
	// row as it stands and never rewrites it to EXPLICIT (a rewritten row
	// would become hand-removable, contradicting the DERIVED refusal).
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	m, err := svc.AddMember(ctx, gm, AddMemberInput{ChannelID: chYardTrenton, UserID: uSamRivera})
	if err != nil {
		t.Fatalf("add over a derived row: %v", err)
	}
	if m.Source != SourceDerived {
		t.Fatalf("a derived row must be answered as it stand, got source=%s", m.Source)
	}
	if err := repo.RemoveChannelMember(ctx, chYardTrenton, uSamRivera); !errors.Is(err, ErrDerivedMember) {
		t.Fatalf("the derived row must still refuse removal, got %v", err)
	}

	// A target who reaches the branch (the UAT inside sales persona,
	// granted Trenton) is seatable by the admin, and removable again.
	uat := uatInsideSalesID(ctx, t, pool)
	seated := false
	t.Cleanup(func() {
		if seated {
			_, _ = pool.Exec(ctx,
				`DELETE FROM msg_members WHERE channel_id = $1 AND user_id = $2 AND source = 'EXPLICIT'`,
				chYardTrenton, uat)
		}
	})
	if _, err := svc.AddMember(ctx, gm, AddMemberInput{ChannelID: chYardTrenton, UserID: uat}); err != nil {
		t.Fatalf("a Trenton target in the Trenton yard: %v", err)
	}
	seated = true
	if err := svc.RemoveMember(ctx, gm, chYardTrenton, uat); err != nil {
		t.Fatalf("admin removes the explicit seat: %v", err)
	}
	seated = false
}
