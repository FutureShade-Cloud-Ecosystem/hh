//go:build integration

// system_post_slug_integration_test.go: a system post by slug resolves
// only a system row. The lookup behind every post and route by slug
// (the consumer's yard, house and payments posts, the task notifier's
// counter, yard and #office, the escalations mirror, the portal bridge)
// reads rows carrying the auto_scope marker no caller founded room ever
// carries, so a caller room that squats a name the system derives from
// branch facts (the counter family, sales-<city>, was the one the
// reservation sweep missed) can never receive a system post, whatever
// family a later sweep also misses. The families sales- and drivers- are
// reserved beside yard- as the first wall.

package messaging

import (
	"context"
	"errors"
	"testing"
)

const branchNewR4 = "a9840000-0000-4000-8000-0000000000b1"

// A branch added as a locations row with none of the 0076 rooms seeded
// (any branch beyond the two demo ones) and a caller shaped room on the
// counter slug: the counter notice must not land in it. It falls through
// to the yard room (absent too) and then #office, as it does when nobody
// founded the squat.
func TestIntegration_SystemPost_By_Slug_Never_Lands_In_A_Caller_Room(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	if _, err := pool.Exec(ctx, `
		INSERT INTO locations (id, branch_id, loc_type, code, name)
		VALUES ($1, $1, 'BRANCH', 'NWBR4', 'New branch round 4')`, branchNewR4); err != nil {
		t.Fatalf("add the new branch: %v", err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM msg_messages WHERE event_key LIKE 'r4-%'`)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_messages WHERE event_key LIKE 'r4-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE branch_id = $1`, branchNewR4)
		_, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, branchNewR4)
	})

	// The caller shaped room, exactly as the founding route used to write
	// it for the counter family (TEAM, the branch, auto_scope NULL).
	var plant string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, branch_id, slug, name, tier)
		VALUES ('TEAM', $1, 'sales-nwbr4', 'Sales nwbr4', 'STANDARD')
		RETURNING id::text`, branchNewR4).Scan(&plant); err != nil {
		t.Fatalf("plant the counter shaped room: %v", err)
	}

	yard, err := svc.YardSlugForBranch(ctx, branchNewR4)
	if err != nil || yard != "yard-nwbr4" {
		t.Fatalf("the new branch's yard slug: %q (%v)", yard, err)
	}

	// The counter notifier's own call, then the fallback chain it feeds.
	if _, _, err := svc.PostEventToChannelSlug(ctx, "sales-nwbr4", "r4-counter-direct", "counter notice", EventRef{}, false); err == nil {
		t.Fatalf("a post by slug landed in a caller shaped room")
	}
	res, err := svc.orderNoticeFallback(ctx, OrderRepNotice{
		EventKey: "r4-counter-chain", Body: "Order is STAGED",
		FallbackSlugs: []string{"sales-nwbr4", yard, "office"},
	})
	if err != nil {
		t.Fatalf("fallback chain: %v", err)
	}
	var landed string
	if err := pool.QueryRow(ctx,
		`SELECT channel_id::text FROM msg_messages WHERE event_key = 'r4-counter-chain'`).Scan(&landed); err != nil {
		t.Fatalf("find where the notice landed (kind %s): %v", res.Kind, err)
	}
	if landed == plant {
		t.Fatalf("the counter notice landed in the caller room")
	}
	if landed != chOffice {
		t.Fatalf("the notice must fall through to #office, landed in %s", landed)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM msg_messages WHERE channel_id = $1`, plant).Scan(&n); err != nil {
		t.Fatalf("count the plant's messages: %v", err)
	}
	if n != 0 {
		t.Fatalf("the caller room holds %d system message(s)", n)
	}

	// The wall is structural, not a family list: any caller shaped slug.
	var other string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, branch_id, slug, name, tier)
		VALUES ('TEAM', $1, 'r4-any-family-nwbr4', 'Any family', 'STANDARD')
		RETURNING id::text`, branchNewR4).Scan(&other); err != nil {
		t.Fatalf("plant the second room: %v", err)
	}
	if _, _, err := svc.PostEventToChannelSlug(ctx, "r4-any-family-nwbr4", "r4-any", "x", EventRef{}, false); err == nil {
		t.Fatalf("a post by slug landed in a caller room of an unlisted family")
	}
	if _, err := repo.ChannelIDBySlug(ctx, "r4-any-family-nwbr4"); !errors.Is(err, errNotFound) {
		t.Fatalf("the slug lookup must answer not found for a caller room, got %v", err)
	}
}

// Every slug the system posts into today carries the marker on a seeded
// database, so the structural wall costs the seeded rooms nothing.
func TestIntegration_SystemPost_By_Slug_Still_Resolves_Every_Seeded_System_Room(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	for _, slug := range []string{
		"escalations", "office", "portal-inbox", "payments", "house-unassigned", "finance", "alerts-all",
		"yard-trenton", "yard-kingston", "sales-trenton", "sales-kingston", "drivers-trenton", "drivers-kingston",
		"house-trenton", "house-kingston", "payments-trenton", "payments-kingston",
	} {
		if _, err := repo.ChannelIDBySlug(ctx, slug); err != nil {
			t.Errorf("the seeded system room %q must resolve by slug: %v", slug, err)
		}
	}
}

// The reservation, first wall: sales- and drivers- beside yard-, any case,
// chosen or derived from the name.
func TestIntegration_CreateChannel_Counter_And_Drivers_Families_Are_Reserved(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool))
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `
			DELETE FROM msg_channels
			WHERE slug ILIKE 'sales-0-mine' OR slug ILIKE 'drivers-0-mine'`)
	})
	for _, f := range []struct {
		what string
		in   CreateChannelInput
	}{
		{"sales chosen lower", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "sales-0-mine", BranchID: branchTrenton}},
		{"sales chosen upper", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "SALES-0-MINE", BranchID: branchTrenton}},
		{"sales name derived", CreateChannelInput{Kind: KindTopic, Name: "Sales 0 mine"}},
		{"drivers chosen lower", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "drivers-0-mine", BranchID: branchTrenton}},
		{"drivers chosen mixed", CreateChannelInput{Kind: KindTeam, Name: "x", Slug: "DrIvErS-0-Mine", BranchID: branchTrenton}},
		{"drivers name derived", CreateChannelInput{Kind: KindTopic, Name: "Drivers 0 mine"}},
	} {
		if _, _, err := svc.CreateChannel(ctx, adminCaller(), f.in); !errors.Is(err, ErrReservedSlug) {
			t.Fatalf("%s: founding must be refused as a reserved system family, got %v", f.what, err)
		}
	}
}
