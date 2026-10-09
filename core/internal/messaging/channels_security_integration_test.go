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
// derived one, never an EXPLICIT OWNER seat.
func TestIntegration_CreateChannel_Collision_AnswersWithoutSeating(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	// Sync first, so the caller's membership state is deterministic on the
	// shared gate database: Sam is a derived member of the GLOBAL #finance.
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	if _, _, ok := memberRow(ctx, t, pool, chFinance, uSamRivera); !ok {
		t.Fatalf("setup: sam must hold a derived row in #finance after sync")
	}

	ch, created, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
		Kind: KindTeam, Name: "finance",
	})
	if err != nil {
		t.Fatalf("founding a room the caller is already in must answer it, got %v", err)
	}
	if created || ch.ID != chFinance {
		t.Fatalf("the collision must answer the existing room: created=%v id=%s want %s", created, ch.ID, chFinance)
	}
	role, source, ok := memberRow(ctx, t, pool, chFinance, uSamRivera)
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
// and the caller is left a non member.
func TestIntegration_CreateChannel_Collision_BranchWall_Runs_On_The_Existing_Row(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	// Sync so the refusal is the wall's, not the absence of a derived row.
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}

	// payments-kingston: TEAM, auto_scope BRANCH, granted Kingston. The
	// request names no branch; the wall must read the room's own.
	if _, _, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
		Kind: KindTeam, Name: "Payments Kingston",
	}); !errors.Is(err, ErrNotInBranch) {
		t.Fatalf("founding the Kingston payments room from Trenton must answer the branch wall, got %v", err)
	}
	if _, _, ok := memberRow(ctx, t, pool, chPaymentsKingston, uSamRivera); ok {
		t.Fatalf("the refused founding must leave the caller a non member")
	}

	// yard-kingston: a BRANCH kind room, another kind than the request: the
	// collision is refused in words whatever the caller's reach.
	if _, _, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
		Kind: KindTeam, Name: "Yard Kingston",
	}); !errors.Is(err, ErrChannelSlugTaken) {
		t.Fatalf("founding a BRANCH room's slug must answer the collision, got %v", err)
	}
	if _, _, ok := memberRow(ctx, t, pool, chYardKingston, uSamRivera); ok {
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
	// it is refused by the prefix wall, party or stranger alike.
	caseyCaller := Caller{UserID: uCaseyDoyle, RoleCode: "YARD_MANAGER", Scope: "BRANCH",
		Branches: map[string]bool{branchKingston: true}}
	for _, caller := range []Caller{caseyCaller, samCaller()} {
		if _, _, err := svc.CreateChannel(ctx, caller, CreateChannelInput{
			Kind: KindTeam, Slug: dm.Slug, Name: "Not a dm",
		}); !errors.Is(err, ErrReservedSlug) {
			t.Fatalf("founding the dm namespace must be refused, got %v", err)
		}
	}

	// The kind backstop: a DM holding a slug OUTSIDE the reserved namespaces
	// (only a hand written row can arrange that today) is still never
	// answered by the channel create, not even to a party.
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
	if _, _, err := svc.CreateChannel(ctx, caseyCaller, CreateChannelInput{
		Kind: KindTeam, Slug: handSlug, Name: "Not a dm",
	}); !errors.Is(err, ErrChannelSlugTaken) {
		t.Fatalf("a party founding a dm's slug outside the namespace must answer the collision, got %v", err)
	}
	if _, _, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
		Kind: KindTeam, Slug: handSlug, Name: "Not a dm",
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
	slug := fmt.Sprintf("dm-probe-%d", time.Now().UnixNano())

	if _, _, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
		Kind: KindTeam, Slug: slug, Name: "Probe",
	}); !errors.Is(err, ErrReservedSlug) {
		t.Fatalf("a dm- prefixed slug must be refused, got %v", err)
	}
	for _, prefix := range []string{"rep-", "approvals-"} {
		if _, _, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
			Kind: KindTeam, Slug: prefix + slug, Name: "Probe",
		}); !errors.Is(err, ErrReservedSlug) {
			t.Fatalf("a %s prefixed slug must be refused, got %v", prefix, err)
		}
	}
	// The prefix wall holds for the DERIVED slug too (a name that sanitizes
	// into the namespace), not only a caller chosen one.
	if _, _, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
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
// own room is answered (created false), the same room, still a member.
func TestIntegration_CreateChannel_Replay_Answers_The_Founder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	name := fmt.Sprintf("Security replay room %d", time.Now().UnixNano())

	first, created, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
		Kind: KindTeam, Name: name,
	})
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, first.ID)
	})
	second, created, err := svc.CreateChannel(ctx, samCaller(), CreateChannelInput{
		Kind: KindTeam, Name: name, Topic: "A different topic still converges",
	})
	if err != nil {
		t.Fatalf("replayed create: %v", err)
	}
	if created || second.ID != first.ID {
		t.Fatalf("the replay did not converge: created=%v first=%s second=%s", created, first.ID, second.ID)
	}
	if role, _, ok := memberRow(ctx, t, pool, first.ID, uSamRivera); !ok || role != "OWNER" {
		t.Fatalf("the founder must hold the room's OWNER row, got role=%q ok=%v", role, ok)
	}
}
