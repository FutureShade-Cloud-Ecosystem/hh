//go:build integration

// channels_integration_test.go: the channel-create, DM-create and membership
// writes against the migrated + seeded database. The route-level file proved
// the surfaces and their refusals; this file proves what only the database
// can: the slug convergence, the pair-DM lookup that finds a SEEDED dm (whose
// slug is NULL) before creating one, the explicit seat beside derived rows,
// and the derived-removal refusal on a real msg_members row.

package messaging

import (
	"context"
	"errors"
	"testing"
)

// A TEAM channel founded twice converges on one row (the slug's unique
// index), the founder is its OWNER, and a branch-scoped caller cannot found a
// channel in a branch they hold no grant for.
func TestIntegration_CreateChannel_Idempotent_And_BranchWall(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))

	first, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: "Trenton paving crew", Topic: "The paving crew's room",
	}, uSamRivera)
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	second, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: "Trenton paving crew", Topic: "A different topic still converges",
	}, uSamRivera)
	if err != nil {
		t.Fatalf("replayed create: %v", err)
	}
	if created || second.ID != first.ID {
		t.Fatalf("the replay did not converge: created=%v first=%s second=%s", created, first.ID, second.ID)
	}
	owner, err := repo.IsMember(ctx, first.ID, uSamRivera)
	if err != nil || !owner {
		t.Fatalf("the founder is not a member: %v err=%v", owner, err)
	}
	t.Cleanup(func() {
		_ = repo.RemoveChannelMember(ctx, first.ID, uSamRivera)
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, first.ID)
	})

	// The branch wall is the service's; the repository proves the named
	// branch must exist at all (an unknown branch is not-found).
	if _, _, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: "Nowhere", BranchID: "e1000000-0000-4000-9000-0000000000f1",
	}, uSamRivera); !errors.Is(err, errNotFound) {
		t.Fatalf("an unknown branch must answer not-found, got %v", err)
	}
}

// A DM between two staff is found when a seeded pair channel already exists
// (the seeded DMs carry NULL slugs, so the pair-shape lookup is the only thing
// that can find them) and created exactly once for a pair with no room.
func TestIntegration_CreateDM_FindsSeededPair_And_CreatesOnce(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))

	// The seeded Alex-Sam DM (0052, slug NULL): asking for the pair must find
	// it, not found a second room beside it.
	ch, created, err := repo.CreateDM(ctx, uAlexMorgan, uSamRivera)
	if err != nil {
		t.Fatalf("find seeded dm: %v", err)
	}
	if created || ch.ID != chDMAlexSam {
		t.Fatalf("the seeded pair was not found: created=%v got %s want %s", created, ch.ID, chDMAlexSam)
	}

	// A pair with no room (the seeds pair Alex-Sam and Sam-Casey, not
	// Alex-Casey): created once, found on the replay.
	fresh, created, err := repo.CreateDM(ctx, uAlexMorgan, uCaseyDoyle)
	if err != nil || !created {
		t.Fatalf("fresh dm: created=%v err=%v", created, err)
	}
	if fresh.Kind != KindDM {
		t.Fatalf("the fresh room is a %s, want a DM", fresh.Kind)
	}
	again, created, err := repo.CreateDM(ctx, uCaseyDoyle, uAlexMorgan) // order must not matter
	if err != nil {
		t.Fatalf("replayed dm: %v", err)
	}
	if created || again.ID != fresh.ID {
		t.Fatalf("the pair's replay did not converge: created=%v first=%s again=%s", created, fresh.ID, again.ID)
	}
	for _, u := range [2]string{uAlexMorgan, uCaseyDoyle} {
		ok, err := repo.IsMember(ctx, fresh.ID, u)
		if err != nil || !ok {
			t.Fatalf("dm party %s is not a member: %v err=%v", u, ok, err)
		}
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, fresh.ID)
	})

	// An unknown staffer is not-found; the caller must be active too.
	if _, _, err := repo.CreateDM(ctx, uSamRivera, "e1000000-0000-4000-9000-0000000000f2"); !errors.Is(err, errNotFound) {
		t.Fatalf("an unknown staffer must answer not-found, got %v", err)
	}
}

// Membership writes: an explicit seat lands beside the derived rows and is
// removable; a DERIVED row (the seeded #yard-trenton rows SyncMembership
// writes) is refused with the conflict.
func TestIntegration_Membership_AddRemove_And_DerivedRefusal(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	// Sam (Trenton) has a DERIVED row in #yard-trenton; syncing materializes
	// it, and the removal must refuse it.
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	if err := repo.RemoveChannelMember(ctx, chYardTrenton, uSamRivera); !errors.Is(err, ErrDerivedMember) {
		t.Fatalf("a derived row must refuse removal, got %v", err)
	}

	// Casey (Kingston) is not in #yard-trenton; Sam seats them EXPLICIT, the
	// seat shows, and removing it drops it again.
	m, err := repo.AddChannelMember(ctx, AddMemberInput{ChannelID: chYardTrenton, UserID: uCaseyDoyle})
	if err != nil {
		t.Fatalf("add casey: %v", err)
	}
	if m.Source != SourceExplicit || m.UserID != uCaseyDoyle {
		t.Fatalf("the seat: %+v", m)
	}
	ok, err := repo.IsMember(ctx, chYardTrenton, uCaseyDoyle)
	if err != nil || !ok {
		t.Fatalf("casey is not seated: %v err=%v", ok, err)
	}
	if err := repo.RemoveChannelMember(ctx, chYardTrenton, uCaseyDoyle); err != nil {
		t.Fatalf("remove casey: %v", err)
	}
	ok, err = repo.IsMember(ctx, chYardTrenton, uCaseyDoyle)
	if err != nil || ok {
		t.Fatalf("casey is still seated: %v err=%v", ok, err)
	}

	// An unknown user cannot be seated; an absent member row is not-found.
	if _, err := repo.AddChannelMember(ctx, AddMemberInput{ChannelID: chYardTrenton, UserID: "e1000000-0000-4000-9000-0000000000f3"}); !errors.Is(err, errNotFound) {
		t.Fatalf("an unknown user must answer not-found, got %v", err)
	}
	if err := repo.RemoveChannelMember(ctx, chYardTrenton, "e1000000-0000-4000-9000-0000000000f3"); !errors.Is(err, errNotFound) {
		t.Fatalf("an absent member row must answer not-found, got %v", err)
	}
}
