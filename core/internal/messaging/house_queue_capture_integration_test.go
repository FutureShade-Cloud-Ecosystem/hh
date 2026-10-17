//go:build integration

// house_queue_capture_integration_test.go: the branch seeded families
// (the house queues, the payments channels and the yard rooms) are
// system rooms whose names are derived from branch facts, and nothing a
// caller founds may capture them. Two walls, each proven on its own:
// the families are reserved against caller founding exactly as dm- and
// rep- are (any case, chosen or derived from the name), and the
// branch's house channel is picked by what makes it one (the branch's
// derived house name held by a system row, auto_scope set), never by a
// slug pattern, so even a hand planted caller shaped row inside the
// family is never picked.

package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// The reservation: no caller founded room may enter the branch seeded
// families, whatever the case and whether the slug was chosen or
// derived from the name. Nothing lands.
func TestIntegration_CreateChannel_Branch_System_Families_Are_Reserved(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool))

	// Rerun safe on a shared database: whatever a founding managed to
	// write (the red phase of this leg writes them all) is swept, so a
	// later pass counts only its own refusals.
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `
			DELETE FROM msg_channels
			WHERE slug ILIKE 'house-0-mine' OR slug ILIKE 'payments-0-mine' OR slug ILIKE 'yard-0-mine'`)
	})

	foundings := []struct {
		what string
		in   CreateChannelInput
	}{
		{"house chosen lower", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "house-0-mine", BranchID: branchTrenton}},
		{"house chosen upper", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "HOUSE-0-MINE", BranchID: branchTrenton}},
		{"house chosen mixed", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "HoUsE-0-Mine", BranchID: branchTrenton}},
		{"house name derived", CreateChannelInput{Kind: KindTeam, Name: "House 0 mine"}},
		{"payments branch lower", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "payments-0-mine", BranchID: branchTrenton}},
		{"payments branch upper", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "PAYMENTS-0-MINE", BranchID: branchTrenton}},
		{"payments name derived", CreateChannelInput{Kind: KindTeam, Name: "Payments 0 mine"}},
		{"payments floor upper", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "PAYMENTS"}},
		{"yard chosen lower", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "yard-0-mine", BranchID: branchTrenton}},
		{"yard chosen upper", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "YARD-0-MINE", BranchID: branchTrenton}},
		{"yard name derived", CreateChannelInput{Kind: KindTeam, Name: "Yard 0 mine"}},
	}
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)
	for _, f := range foundings {
		if _, _, err := svc.CreateChannel(ctx, adminCaller(), f.in); !errors.Is(err, ErrReservedSlug) {
			t.Fatalf("%s: founding must be refused as a reserved system family, got %v", f.what, err)
		}
	}

	// Nothing landed: the three probe slugs exist in no case form (the
	// bare payments floor is the seeded GLOBAL room and stays).
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM msg_channels
		WHERE slug ILIKE 'house-0-mine' OR slug ILIKE 'payments-0-mine' OR slug ILIKE 'yard-0-mine'`).
		Scan(&n); err != nil {
		t.Fatalf("count squat rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("a refused founding landed %d row(s) in the families", n)
	}
}

// The pick: a branch's house channel is resolved by what makes it one,
// never by a slug pattern. The squat shape the reviewer drove (a caller
// founded house- room that sorts before the branch's own) is planted by
// hand here, exactly as the founding route would have written it
// (auto_scope NULL), because the reservation closes the route itself;
// the pick must ignore it all the same, and the router's post by slug
// must land in the branch's real house room.
func TestIntegration_BranchHouseSlug_Never_Picks_A_Caller_Shaped_Room(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	var houseTrentonID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM msg_channels WHERE slug = 'house-trenton'`).Scan(&houseTrentonID); err != nil {
		t.Fatalf("resolve the seeded Trenton house room: %v", err)
	}

	slug, err := repo.BranchHouseSlug(ctx, branchTrenton)
	if err != nil || slug != "house-trenton" {
		t.Fatalf("Trenton must resolve to house-trenton, got %q (%v)", slug, err)
	}

	var plant string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, branch_id, slug, name, tier)
		VALUES ('TEAM', $1, 'house-0-mine', 'House 0 mine', 'STANDARD')
		RETURNING id::text`, branchTrenton).Scan(&plant); err != nil {
		t.Fatalf("plant the squat shaped row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, plant)
	})

	if slug, err = repo.BranchHouseSlug(ctx, branchTrenton); err != nil || slug != "house-trenton" {
		t.Fatalf("the branch's house queue must stay the system room beside the planted row, got %q (%v)", slug, err)
	}
	if slug, err = repo.BranchHouseSlug(ctx, branchKingston); err != nil || slug != "house-kingston" {
		t.Fatalf("Kingston must resolve to house-kingston, got %q (%v)", slug, err)
	}

	// The router's own second call: a post by the resolved slug lands in
	// the branch's real house room, never the plant.
	key := fmt.Sprintf("house-capture-probe-%d", time.Now().UnixNano())
	m, inserted, err := svc.PostEventToChannelSlug(ctx, "house-trenton", key,
		"Capture probe: this must land in the seeded house room.", EventRef{}, false)
	if err != nil || !inserted {
		t.Fatalf("post to the resolved house slug: inserted=%v err=%v", inserted, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_messages WHERE id = $1`, m.ID)
	})
	if m.ChannelID != houseTrentonID {
		t.Fatalf("the routed event must land in the seeded house room %s, got %s", houseTrentonID, m.ChannelID)
	}
}
