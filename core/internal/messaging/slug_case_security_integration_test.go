//go:build integration

// slug_case_security_integration_test.go: the delta review's P1-A legs
// against the migrated + seeded database. The slug column is CITEXT, so
// the unique index folds case; the reserved namespace wall and the system
// provisions must fold and refuse the same way, or an upper cased slug
// squats a system room the next provision would converge on.

package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// displayNameOf resolves a user's display name the way the provisions
// build their handles (a wrong handle would plant a slug nothing
// converges on, and the leg would prove nothing).
func displayNameOf(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID string) string {
	t.Helper()
	var name string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(display_name,'') FROM users WHERE id = $1`, userID).Scan(&name); err != nil {
		t.Fatalf("resolve display name: %v", err)
	}
	return name
}

// The reserved namespaces are closed for every case: the column folds, so
// the wall folds. No room may land.
func TestIntegration_CreateChannel_Reserved_Namespaces_Fold_Case(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	sam := samCaller()

	for _, tc := range []struct {
		family string
		slug   string
	}{
		{"approvals-", strings.ToUpper(approvalChannelSlug(displayNameOf(ctx, t, pool, uAlexMorgan), uAlexMorgan))},
		{"rep-", strings.ToUpper(repDMSlug(displayNameOf(ctx, t, pool, uSamRivera), uSamRivera))},
		{"dm-", strings.ToUpper(dmPairSlug(uAlexMorgan, uCaseyDoyle))},
		{"mixed case dm-", "Dm-" + strings.ToUpper(dmPairSlug(uCaseyDoyle, uSamRivera))[3:]},
	} {
		if _, _, err := svc.CreateChannel(ctx, sam, CreateChannelInput{
			Kind: KindTeam, Name: "Case probe", Slug: tc.slug,
		}); !errors.Is(err, ErrReservedSlug) {
			t.Fatalf("founding the %s namespace in upper case must answer ErrReservedSlug, got %v", tc.family, err)
		}
	}
	// The wall refused before any write: not one probe room landed, in any
	// case (the CITEXT column folds the equality).
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM msg_channels WHERE slug LIKE '%Case probe%'`).Scan(&n); err != nil {
		t.Fatalf("count probe rooms: %v", err)
	}
	if n != 0 {
		t.Fatalf("no probe room may exist, got %d", n)
	}
}

// A caller chosen slug is canonicalized to lower case at the service,
// matching the CITEXT column's own folding: the stored slug is the lower
// case one, and a replay in any case converges on the same room.
func TestIntegration_CreateChannel_Chosen_Slug_Is_Lower_Cased(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	sam := samCaller()

	mixed := fmt.Sprintf("Paving-Crew-%d", time.Now().UnixNano())
	ch, created, err := svc.CreateChannel(ctx, sam, CreateChannelInput{
		Kind: KindTeam, Name: "Canonical probe", Slug: mixed,
	})
	if err != nil || !created {
		t.Fatalf("found room: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, ch.ID)
	})
	if ch.Slug != strings.ToLower(mixed) {
		t.Fatalf("the stored slug must be the lower case canonical, got %q want %q", ch.Slug, strings.ToLower(mixed))
	}
	again, created, err := svc.CreateChannel(ctx, sam, CreateChannelInput{
		Kind: KindTeam, Name: "Canonical probe", Slug: strings.ToUpper(mixed),
	})
	if err != nil || created || again.ID != ch.ID {
		t.Fatalf("a replay in any case must converge on the same room: created=%v id=%s want %s err=%v", created, again.ID, ch.ID, err)
	}
}

// The pair's direct message cannot be denied by a case variant squat: the
// founding of the upper cased pair slug is refused, so the pair's own
// find-or-create opens cleanly.
func TestIntegration_CreateChannel_Upper_Pair_Squat_Does_Not_Deny_The_Pair_DM(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	sam := samCaller()

	upPair := strings.ToUpper(dmPairSlug(uCaseyDoyle, red2ProbeJordan))
	if _, _, err := svc.CreateChannel(ctx, sam, CreateChannelInput{
		Kind: KindTeam, Name: "Pair squat probe", Slug: upPair,
	}); !errors.Is(err, ErrReservedSlug) {
		t.Fatalf("founding the upper cased pair slug must answer ErrReservedSlug, got %v", err)
	}
	casey := Caller{UserID: uCaseyDoyle, RoleCode: "YARD_MANAGER", Scope: "BRANCH",
		Branches: map[string]bool{branchKingston: true}}
	dm, created, err := svc.CreateDM(ctx, casey, red2ProbeJordan)
	if err != nil || !created {
		t.Fatalf("the pair's dm must open over the refused squat: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, dm.ID)
	})
	if dm.Kind != KindDM {
		t.Fatalf("the pair's room must be a dm, got kind=%s", dm.Kind)
	}
}

// red2ProbeJordan is the second seeded staffer with no seeded DM pairing
// (Jordan Blake), the fresh pair the DM legs use.
const red2ProbeJordan = "d7000000-0000-4000-9000-000000000006"

// The approvals provision never adopts a row of the wrong kind: a TEAM
// planted on the approver's own slug (only a hand written row can arrange
// that once the founding walls hold) is refused, the approver is left
// unseated, and once the plant is gone the provision works and is
// idempotent.
func TestIntegration_EnsureApprovalChannel_Refuses_A_Squatted_Row(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	slug := approvalChannelSlug(displayNameOf(ctx, t, pool, uAlexMorgan), uAlexMorgan)
	// An earlier leg in the same package run may already have provisioned
	// the real channel; the plant needs the slug free, and this test's
	// recovery leg re-provisions the real room on the way out.
	_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE slug = $1`, slug)
	var planted string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, tier)
		VALUES ('TEAM', $1, 'Planted approval squat', 'STANDARD')
		RETURNING id::text`, slug).Scan(&planted); err != nil {
		t.Fatalf("plant squat: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, planted)
	})

	if _, err := repo.EnsureApprovalChannel(ctx, uAlexMorgan); !errors.Is(err, ErrChannelSlugTaken) {
		t.Fatalf("the provision must refuse the squatted row in words, got %v", err)
	}
	if _, _, ok := memberRow(ctx, t, pool, planted, uAlexMorgan); ok {
		t.Fatalf("the provision must never seat the approver in a squatted room")
	}

	// The plant removed, the provision is itself again: it creates the
	// APPROVAL room, seats the approver OWNER, and a second call answers
	// the same id.
	_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, planted)
	first, err := repo.EnsureApprovalChannel(ctx, uAlexMorgan)
	if err != nil {
		t.Fatalf("provision after the plant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, first)
	})
	var kind string
	if err := pool.QueryRow(ctx, `SELECT kind FROM msg_channels WHERE id = $1`, first).Scan(&kind); err != nil {
		t.Fatalf("read kind: %v", err)
	}
	if kind != KindApproval {
		t.Fatalf("the provisioned room must be an APPROVAL channel, got %s", kind)
	}
	if role, _, ok := memberRow(ctx, t, pool, first, uAlexMorgan); !ok || role != "OWNER" {
		t.Fatalf("the approver must hold the provisioned room's OWNER row, got role=%q ok=%v", role, ok)
	}
	second, err := repo.EnsureApprovalChannel(ctx, uAlexMorgan)
	if err != nil || second != first {
		t.Fatalf("the provision must be idempotent: first=%s second=%s err=%v", first, second, err)
	}
}

// The rep provision's twin: same refusal over a planted wrong-kind row,
// same recovery once the plant is gone.
func TestIntegration_EnsureRepDMChannel_Refuses_A_Squatted_Row(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	slug := repDMSlug(displayNameOf(ctx, t, pool, uSamRivera), uSamRivera)
	// Same as the approval leg: free the slug for the plant; the recovery
	// leg re-provisions the real channel on the way out.
	_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE slug = $1`, slug)
	var planted string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, tier)
		VALUES ('TEAM', $1, 'Planted rep squat', 'STANDARD')
		RETURNING id::text`, slug).Scan(&planted); err != nil {
		t.Fatalf("plant squat: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, planted)
	})

	if _, err := repo.EnsureRepDMChannel(ctx, uSamRivera); !errors.Is(err, ErrChannelSlugTaken) {
		t.Fatalf("the provision must refuse the squatted row in words, got %v", err)
	}
	if _, _, ok := memberRow(ctx, t, pool, planted, uSamRivera); ok {
		t.Fatalf("the provision must never seat the rep in a squatted room")
	}

	_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, planted)
	first, err := repo.EnsureRepDMChannel(ctx, uSamRivera)
	if err != nil {
		t.Fatalf("provision after the plant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, first)
	})
	if role, _, ok := memberRow(ctx, t, pool, first, uSamRivera); !ok || role != "OWNER" {
		t.Fatalf("the rep must hold the provisioned room's OWNER row, got role=%q ok=%v", role, ok)
	}
	second, err := repo.EnsureRepDMChannel(ctx, uSamRivera)
	if err != nil || second != first {
		t.Fatalf("the provision must be idempotent: first=%s second=%s err=%v", first, second, err)
	}
}
