//go:build integration

// channels_security_integration_test.go: the security legs of the
// channel-create surface against the migrated + seeded database, the
// reviewer's own repro as the red first evidence: founding a channel whose
// name or slug matches an existing room must never hand that room to a
// caller who is not already allowed in, never seat anyone as owner of an
// existing channel, and a direct message must only ever be reached through
// the direct message find or create for its two parties.

package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// samCaller is the Sales Manager's caller projection: branch scoped,
// granted Trenton only (no Kingston grant), the exact authority the
// reviewer's probe drove.
func samCaller() Caller {
	return Caller{
		UserID:   uSamRivera,
		RoleCode: "SALES_MANAGER",
		Scope:    "BRANCH",
		Branches: map[string]bool{branchTrenton: true},
	}
}

// adminCaller is the general manager's caller projection: GLOBAL scope, the
// one role the decided matrix gives Admin on house-messages. Founding a
// channel is the admins' pen (change 7, ruling A), so the founding walls
// below drive an admin and the walls themselves (reserved slugs, collisions)
// sit behind that gate.
func adminCaller() Caller {
	return Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
}

// derivedRowUnchanged reads one membership row and reports whether it is
// absent, or present as the DERIVED member the role and branch model owns:
// the assertion the collision legs make for a caller the GLOBAL rooms derive
// in (a refused founding must never seat anyone over their derived row).
func derivedRowUnchanged(ctx context.Context, t *testing.T, pool *pgxpool.Pool, channelID, userID string) bool {
	t.Helper()
	role, source, ok := memberRow(ctx, t, pool, channelID, userID)
	if !ok {
		return true
	}
	return source == SourceDerived && role != "OWNER"
}

// adminBranchCaller is the branch scoped admin projection: the same role,
// narrowed to Trenton. The matrix seats the general manager alone today and
// GLOBAL with it, so no live authority produces this shape; the projection
// keeps the branch wall's own legs honest for any branch scoped admin a
// later matrix seats, proving the wall still reads the room's own branch.
func adminBranchCaller() Caller {
	return Caller{
		UserID:   uAlexMorgan,
		RoleCode: "GENERAL_MANAGER",
		Scope:    "BRANCH",
		Branches: map[string]bool{branchTrenton: true},
	}
}

// memberRow reads one membership row's role and source, the probe's own
// read: the seat the founding wrote (or must not have written) is the fact.
func memberRow(ctx context.Context, t *testing.T, pool *pgxpool.Pool, channelID, userID string) (role, source string, ok bool) {
	t.Helper()
	err := pool.QueryRow(ctx,
		`SELECT role, source FROM msg_members WHERE channel_id = $1 AND user_id = $2`,
		channelID, userID).Scan(&role, &source)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false
	}
	if err != nil {
		t.Fatalf("read member row: %v", err)
	}
	return role, source, true
}

// Founding a seeded GLOBAL room's name as an already derived member answers
// the room (created false) and writes NOTHING: the caller's row stays the
// derived one, never an EXPLICIT OWNER seat. Founding is the admins' pen
// (change 7, ruling A), so the leg drives the general manager, a derived
// member of #office like any ACTIVE staffer.
func TestIntegration_CreateChannel_Collision_AnswersWithoutSeating(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	// Sync first, so the caller's membership state is deterministic on the
	// shared gate database: Alex is a derived member of the GLOBAL #office.
	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatalf("sync alex: %v", err)
	}
	if _, _, ok := memberRow(ctx, t, pool, chOffice, uAlexMorgan); !ok {
		t.Fatalf("setup: the admin must hold a derived row in #office after sync")
	}

	ch, created, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
		Kind: KindTeam, Name: "office",
	})
	if err != nil {
		t.Fatalf("founding a room the caller is already in must answer it, got %v", err)
	}
	if created || ch.ID != chOffice {
		t.Fatalf("the collision must answer the existing room: created=%v id=%s want %s", created, ch.ID, chOffice)
	}
	role, source, ok := memberRow(ctx, t, pool, chOffice, uAlexMorgan)
	if !ok {
		t.Fatalf("the caller's derived row vanished")
	}
	if source != SourceDerived || role == "OWNER" {
		t.Fatalf("the founding must not seat the caller over their derived row, got role=%s source=%s", role, source)
	}
}

// Founding a name whose slug collides with a branch scoped room the caller
// holds no grant for is refused by the branch wall reading the EXISTING
// row's branch (the caller cannot dodge the wall by omitting branch_id),
// and the caller is left a non member. The leg drives the branch scoped
// admin projection: the branch wall sits behind the admin gate, and it is
// the wall these legs prove.
func TestIntegration_CreateChannel_Collision_BranchWall_Runs_On_The_Existing_Row(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	// A TEAM room with auto_scope BRANCH on Kingston, in a family outside
	// the reserved system namespaces (every seeded branch room lives in a
	// reserved family now, so only a hand written row can arrange it). The
	// request names no branch; the wall must read the room's own.
	var depotTeam string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, branch_id, slug, name, tier, auto_scope)
		VALUES ('TEAM', $1, 'depot-team-kingston', 'Hand planted depot team', 'STANDARD', 'BRANCH')
		RETURNING id::text`, branchKingston).Scan(&depotTeam); err != nil {
		t.Fatalf("plant the Kingston depot team room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, depotTeam)
	})
	if _, _, err := svc.CreateChannel(ctx, adminBranchCaller(), CreateChannelInput{
		Kind: KindTeam, Name: "depot-team-kingston",
	}); !errors.Is(err, ErrNotInBranch) {
		t.Fatalf("founding the Kingston depot room from Trenton must answer the branch wall, got %v", err)
	}
	if _, _, ok := memberRow(ctx, t, pool, depotTeam, uAlexMorgan); ok {
		t.Fatalf("the refused founding must leave the caller a non member")
	}

	// The reserved branch families answer their own wall first: the
	// payments, yard, sales and drivers namespaces are closed to caller founding (any
	// case, chosen or derived), so a founding that names one is refused
	// as reserved before any collision is consulted, and the caller is
	// left a non member of the seeded rooms.
	for _, name := range []string{"Payments Kingston", "Yard Kingston", "Sales Kingston", "Drivers Kingston"} {
		if _, _, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
			Kind: KindTeam, Name: name,
		}); !errors.Is(err, ErrReservedSlug) {
			t.Fatalf("founding %q must be refused as a reserved system family, got %v", name, err)
		}
	}
	// A GLOBAL admin is a derived member of the seeded rooms; the refused
	// founding must never seat anyone over their derived row.
	if !derivedRowUnchanged(ctx, t, pool, chPaymentsKingston, uAlexMorgan) {
		t.Fatalf("the refused founding must leave the payments room's derived row untouched")
	}
	if !derivedRowUnchanged(ctx, t, pool, chYardKingston, uAlexMorgan) {
		t.Fatalf("the refused founding must leave the yard room's derived row untouched")
	}

	// A BRANCH kind room, another kind than the request, on a slug outside
	// the reserved families (only a hand written row can arrange that
	// today: the seeded BRANCH rooms all live in the reserved yard
	// family): the collision is refused in words whatever the caller's
	// reach.
	var handBranch string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, branch_id, slug, name, tier)
		VALUES ('BRANCH', $1, 'depot-kingston', 'Hand planted depot room', 'STANDARD')
		RETURNING id::text`, branchKingston).Scan(&handBranch); err != nil {
		t.Fatalf("plant hand branch room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, handBranch)
	})
	if _, _, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
		Kind: KindTeam, Name: "depot-kingston",
	}); !errors.Is(err, ErrChannelSlugTaken) {
		t.Fatalf("founding a BRANCH room's slug must answer the collision, got %v", err)
	}
	if _, _, ok := memberRow(ctx, t, pool, handBranch, uAlexMorgan); ok {
		t.Fatalf("the refused founding must leave the caller a non member")
	}
}

// A direct message is reached only through its own find or create: founding
// a TEAM channel on a DM's slug is refused for a party (a member of the
// room) and for a stranger alike, and the room keeps exactly its two
// parties.
func TestIntegration_CreateChannel_Never_Reaches_ADM_By_Slug(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	// The seeds pair Alex-Sam and Sam-Casey; Alex-Casey has no room, so the
	// pair's DM is created fresh here with its deterministic slug.
	gmCaller := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	dm, created, err := svc.CreateDM(ctx, gmCaller, uCaseyDoyle)
	if err != nil || !created {
		t.Fatalf("open dm: created=%v err=%v", created, err)
	}
	if dm.Slug == "" {
		t.Fatalf("setup: the fresh dm must carry its pair slug")
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, dm.ID)
	})

	// The pair's own slug sits in the reserved dm- namespace, so founding on
	// it is refused by the prefix wall, party or stranger alike. Founding is
	// the admins' pen, so the leg drives the general manager.
	for _, in := range []CreateChannelInput{
		{Kind: KindTeam, Slug: dm.Slug, Name: "Not a dm"},
		{Kind: KindTeam, Slug: dm.Slug, Name: "Still not a dm"},
	} {
		if _, _, err := svc.CreateChannel(ctx, adminCaller(), in); !errors.Is(err, ErrReservedSlug) {
			t.Fatalf("founding the dm namespace must be refused, got %v", err)
		}
	}

	// The kind backstop: a DM holding a slug OUTSIDE the reserved namespaces
	// (only a hand written row can arrange that today) is still never
	// answered by the channel create, not even to a party of the room. Two
	// hand rooms, so the admin is a PARTY of one (its OWNER) and a STRANGER
	// to the other.
	handSlug := fmt.Sprintf("pair-room-%d", time.Now().UnixNano())
	var handDM string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, tier)
		VALUES ('DM', $1, 'Hand planted pair room', 'STANDARD')
		RETURNING id::text`, handSlug).Scan(&handDM); err != nil {
		t.Fatalf("plant hand dm: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, handDM)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'OWNER', 'EXPLICIT'), ($1, $3, 'MEMBER', 'EXPLICIT')`,
		handDM, uAlexMorgan, uCaseyDoyle); err != nil {
		t.Fatalf("plant hand dm parties: %v", err)
	}
	if _, _, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
		Kind: KindTeam, Slug: handSlug, Name: "Not a dm",
	}); !errors.Is(err, ErrChannelSlugTaken) {
		t.Fatalf("a party founding a dm's slug outside the namespace must answer the collision, got %v", err)
	}
	strangerSlug := fmt.Sprintf("pair-room-%d", time.Now().UnixNano())
	var strangerDM string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, tier)
		VALUES ('DM', $1, 'Hand planted stranger pair room', 'STANDARD')
		RETURNING id::text`, strangerSlug).Scan(&strangerDM); err != nil {
		t.Fatalf("plant stranger dm: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, strangerDM)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'OWNER', 'EXPLICIT'), ($1, $3, 'MEMBER', 'EXPLICIT')`,
		strangerDM, uCaseyDoyle, uSamRivera); err != nil {
		t.Fatalf("plant stranger dm parties: %v", err)
	}
	if _, _, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
		Kind: KindTeam, Slug: strangerSlug, Name: "Not a dm either",
	}); !errors.Is(err, ErrChannelSlugTaken) {
		t.Fatalf("a stranger founding a dm's slug outside the namespace must answer the collision, got %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM msg_members WHERE channel_id = $1`, dm.ID).Scan(&n); err != nil {
		t.Fatalf("count dm parties: %v", err)
	}
	if n != 2 {
		t.Fatalf("the dm must keep exactly its two parties, got %d", n)
	}
}

// Caller chosen slugs may not squat the system namespaces (dm-, rep-,
// approvals-): a room founded there would capture a later system provision
// that converges on the same slug.
func TestIntegration_CreateChannel_Refuses_Reserved_Slug_Namespaces(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)
	slug := fmt.Sprintf("dm-probe-%d", time.Now().UnixNano())

	if _, _, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
		Kind: KindTeam, Slug: slug, Name: "Probe",
	}); !errors.Is(err, ErrReservedSlug) {
		t.Fatalf("a dm- prefixed slug must be refused, got %v", err)
	}
	for _, prefix := range []string{"rep-", "approvals-"} {
		if _, _, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
			Kind: KindTeam, Slug: prefix + slug, Name: "Probe",
		}); !errors.Is(err, ErrReservedSlug) {
			t.Fatalf("a %s prefixed slug must be refused, got %v", prefix, err)
		}
	}
	// The prefix wall holds for the DERIVED slug too (a name that sanitizes
	// into the namespace), not only a caller chosen one.
	if _, _, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
		Kind: KindTeam, Name: "DM guidelines " + slug,
	}); !errors.Is(err, ErrReservedSlug) {
		t.Fatalf("a name deriving into the dm- namespace must be refused, got %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM msg_channels WHERE slug LIKE $1`,
		"%"+slug+"%").Scan(&n); err != nil {
		t.Fatalf("count probe rooms: %v", err)
	}
	if n != 0 {
		t.Fatalf("no probe room may exist, got %d", n)
	}
}

// The idempotent replay survives the walls: the founder re-founding their
// own room is answered (created false), the same room, still a member. The
// founder is the admin (founding is the admins' pen).
func TestIntegration_CreateChannel_Replay_Answers_The_Founder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)
	name := fmt.Sprintf("Security replay room %d", time.Now().UnixNano())

	first, created, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
		Kind: KindTeam, Name: name,
	})
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, first.ID)
	})
	second, created, err := svc.CreateChannel(ctx, adminCaller(), CreateChannelInput{
		Kind: KindTeam, Name: name, Topic: "A different topic still converges",
	})
	if err != nil {
		t.Fatalf("replayed create: %v", err)
	}
	if created || second.ID != first.ID {
		t.Fatalf("the replay did not converge: created=%v first=%s second=%s", created, first.ID, second.ID)
	}
	if role, _, ok := memberRow(ctx, t, pool, first.ID, uAlexMorgan); !ok || role != "OWNER" {
		t.Fatalf("the founder must hold the room's OWNER row, got role=%q ok=%v", role, ok)
	}
}
