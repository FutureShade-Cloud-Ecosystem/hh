//go:build integration

// team_branch_seat_integration_test.go: ruling 89. A TEAM channel founded
// naming a branch takes the branch rule when its owner seats someone, the
// same as a room the branch model derives (auto_scope BRANCH): a staffer
// who does not reach that branch is refused in the shared body's words. A
// TEAM founded without a branch still seats anyone the owner may reach.

package messaging

import (
	"context"
	"errors"
	"testing"
)

func TestIntegration_TeamNamingABranch_Takes_The_Branch_Rule_On_Seat(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	var trenton string
	if err := pool.QueryRow(ctx,
		`SELECT branch_id::text FROM msg_channels WHERE id = $1`, chYardTrenton).Scan(&trenton); err != nil {
		t.Fatalf("resolve the Trenton branch: %v", err)
	}

	found := func(slug string, branch string) Channel {
		t.Helper()
		ch, created, err := svc.CreateChannel(ctx, gm, CreateChannelInput{
			Kind: KindTeam, Name: slug, Slug: slug, BranchID: branch,
		})
		if err != nil || !created {
			t.Fatalf("found %s: created=%v err=%v", slug, created, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, ch.ID)
		})
		return ch
	}

	trentonTeam := found("ruling-89-trenton-team", trenton)

	// A Kingston only staffer is refused, in the shared body's words, and
	// is not seated.
	if _, err := svc.AddMember(ctx, gm, trentonTeam.ID, uCaseyDoyle); !errors.Is(err, ErrTargetNotInBranch) {
		t.Fatalf("a Kingston target in a Trenton TEAM must refuse, got %v", err)
	}
	if ok, _ := repo.IsMember(ctx, trentonTeam.ID, uCaseyDoyle); ok {
		t.Fatalf("the foreign target was seated")
	}

	// A Trenton staffer is still seatable by the owner.
	uat := uatInsideSalesID(ctx, t, pool)
	if _, err := svc.AddMember(ctx, gm, trentonTeam.ID, uat); err != nil {
		t.Fatalf("a Trenton target in a Trenton TEAM: %v", err)
	}

	// A TEAM that really spans branches is founded without naming one, and
	// seats anyone the owner may reach (unchanged).
	spanning := found("ruling-89-spanning-team", "")
	if _, err := svc.AddMember(ctx, gm, spanning.ID, uCaseyDoyle); err != nil {
		t.Fatalf("a TEAM without a branch must seat a Kingston staffer: %v", err)
	}
}
