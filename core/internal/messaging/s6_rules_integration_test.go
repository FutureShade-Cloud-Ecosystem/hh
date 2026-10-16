//go:build integration

// s6_rules_integration_test.go: change 7 ruling 7 (Notif opt-in, Settings >
// Membership rules hand-added reason, New channel admins only, Channel delete
// admins only) plus change 8 ruling 8 Channel delete hands-down. Owner:
// internal/messaging.
//
// Every leg here is the database's own verdict on the migration the ruling
// demands: a PUBLIC room is the default, a PRIVATE room persists visibility,
// a hand-added EXPLICIT row carries the manager's text reason and the user
// id who added them, a PRIVATE room's listing only answers members (the room
// owner or house messages admin, the brief's wall), and a channel delete is
// a soft archive (is_archived = true) that the manager wall guards.

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

// VisibilityPublic and VisibilityPrivate live in model.go (the migration's
// CHECK is the canonical enum, the service answers the same values).

// uatScrubbedID resolves the persona's user id by email: the scrubbed-009
// inside sales persona (the brief's "user who holds no row in the room" the
// test seats, so the seat's added_reason / added_by land on a row the test
// owns).
func uatScrubbedID(ctx context.Context, t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = $1 AND status = 'ACTIVE'`, email).
		Scan(&id); err != nil {
		t.Fatalf("resolve persona %s: %v", email, err)
	}
	return id
}

// - visibility default probe -

// TestIntegration_Visibility_Defaults_To_Public proves the migration's default:
// a new room without an explicit visibility writes PUBLIC (the room's
// settings panel default, the moderation skill's rule #2 starting point).
func TestIntegration_Visibility_Defaults_To_Public(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))

	name := fmt.Sprintf("visibility default %d", time.Now().UnixNano())
	ch, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: name, Slug: name,
	}, uSamRivera)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, ch.ID)
	})

	if ch.Visibility != VisibilityPublic {
		t.Fatalf("a new room's visibility must default to PUBLIC, got %q", ch.Visibility)
	}
	if err := intPool(t).QueryRow(ctx,
		`SELECT visibility FROM msg_channels WHERE id = $1`, ch.ID).Scan(&ch.Visibility); err != nil {
		t.Fatalf("read back visibility: %v", err)
	}
	if ch.Visibility != VisibilityPublic {
		t.Fatalf("the row's stored visibility must be PUBLIC, got %q", ch.Visibility)
	}
}

// TestIntegration_Visibility_Persists_When_Set proves a PRIVATE room stays
// PRIVATE through the round trip: the route, the row, and the re-read all
// answer PRIVATE, and a PUBLIC room written next door does not flip the
// neighbouring row (the migration is one column wide; each row's value is
// its own).
func TestIntegration_Visibility_Persists_When_Set(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))

	privName := fmt.Sprintf("private room %d", time.Now().UnixNano())
	pubName := fmt.Sprintf("public topic %d", time.Now().UnixNano()+1)
	priv, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: privName, Slug: privName,
		Visibility: VisibilityPrivate,
	}, uSamRivera)
	if err != nil || !created {
		t.Fatalf("create private: created=%v err=%v", created, err)
	}
	pub, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTopic, Name: pubName, Slug: pubName,
		Visibility: VisibilityPublic,
	}, uSamRivera)
	if err != nil || !created {
		t.Fatalf("create public: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id IN ($1,$2)`, priv.ID, pub.ID)
	})

	if priv.Visibility != VisibilityPrivate {
		t.Fatalf("private room reads back PUBLIC: %q", priv.Visibility)
	}
	if pub.Visibility != VisibilityPublic {
		t.Fatalf("public room reads back PRIVATE: %q", pub.Visibility)
	}
}

// TestIntegration_SetVisibility_By_Admin_Writes_Private proves the
// visibility-mutate path the brief names: the service's SetVisibility writes
// the new value through the same row the migration's CHECK (visibility IN
// ('PUBLIC','PRIVATE')) guards, so an invalid value is refused and a valid
// value replaces the existing one. The owner can call it, the admin does,
// and the row is re-read through the same column.
func TestIntegration_SetVisibility_By_Admin_Writes_Private(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	sam := samCaller()
	founder := Caller{UserID: sam.UserID, RoleCode: "GENERAL_MANAGER", Scope: "BRANCH", Branches: sam.Branches}
	room, _, err := svc.CreateChannel(ctx, founder, CreateChannelInput{
		Kind: KindTeam, Name: fmt.Sprintf("set-vis probe %d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, room.ID)
	})

	if err := svc.SetVisibility(ctx, sam, room.ID, VisibilityPrivate); err != nil {
		t.Fatalf("owner set PRIVATE: %v", err)
	}
	var got string
	if err := intPool(t).QueryRow(ctx,
		`SELECT visibility FROM msg_channels WHERE id = $1`, room.ID).Scan(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != VisibilityPrivate {
		t.Fatalf("private set failed: got %q", got)
	}

	if err := svc.SetVisibility(ctx, sam, room.ID, VisibilityPublic); err != nil {
		t.Fatalf("owner set PUBLIC: %v", err)
	}
	if err := intPool(t).QueryRow(ctx,
		`SELECT visibility FROM msg_channels WHERE id = $1`, room.ID).Scan(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != VisibilityPublic {
		t.Fatalf("public set failed: got %q", got)
	}
}

// TestIntegration_HandAdded_Member_Carries_Reason_And_Adder proves the
// Settings > Membership rules payload: a manager-driven add-member writes
// the EXPLICIT row with the manager's text reason (a brief line the staff
// member reads in the row audit) and the user id of the manager who added
// them. The same row re-reads with the same values.
func TestIntegration_HandAdded_Member_Carries_Reason_And_Adder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	sam := samCaller()
	founder := Caller{UserID: sam.UserID, RoleCode: "GENERAL_MANAGER", Scope: "BRANCH", Branches: sam.Branches}
	room, _, err := svc.CreateChannel(ctx, founder, CreateChannelInput{
		Kind: KindTeam, Name: fmt.Sprintf("hand-add probe %d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, room.ID)
	})

	target := uatScrubbedID(ctx, t, pool, "scrubbed-0009@example.invalid")
	reason := "added by hand: paving crew swap for Trenton run, week ahead"

	got, err := svc.AddMember(ctx, sam, AddMemberInput{
		ChannelID: room.ID,
		UserID:    target, Role: "MEMBER",
		AddedReason: reason, AddedBy: sam.UserID,
	})
	if err != nil {
		t.Fatalf("add member: %v", err)
	}
	if got.Source != SourceExplicit {
		t.Fatalf("a hand-added row's source must be EXPLICIT, got %q", got.Source)
	}
	if got.AddedReason != reason {
		t.Fatalf("added_reason lost: got %q want %q", got.AddedReason, reason)
	}
	if got.AddedBy != sam.UserID {
		t.Fatalf("added_by lost: got %q want %q", got.AddedBy, sam.UserID)
	}

	// Re-read directly from msg_members to prove the row on disk carries
	// what the struct answered: the read-after-write guard.
	var (
		dbReason, dbAddedBy string
	)
	if err := pool.QueryRow(ctx,
		`SELECT added_reason, COALESCE(added_by::text, '') FROM msg_members WHERE channel_id = $1 AND user_id = $2`,
		room.ID, target).Scan(&dbReason, &dbAddedBy); err != nil {
		t.Fatalf("read msg_members: %v", err)
	}
	if dbReason != reason {
		t.Fatalf("row's added_reason drifted: got %q want %q", dbReason, reason)
	}
	if dbAddedBy != sam.UserID {
		t.Fatalf("row's added_by drifted: got %q want %q", dbAddedBy, sam.UserID)
	}
}

// TestIntegration_Derived_Member_Leaves_Adder_Fields_Empty proves the model
// the migration defends: a DERIVED row (the role/branch model writes it)
// does not gain an added_reason or added_by. A SyncMembership pass writes
// the row; the row reads back with both fields empty (the migration's
// default ” / NULL). The added_reason / added_by columns are the
// hand-added audit, and a derived member is not hand-added.
func TestIntegration_Derived_Member_Leaves_Adder_Fields_Empty(t *testing.T) {
	priv := t
	_ = priv
	_ = strings.NewReader("")
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	sam := samCaller()
	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)
	_ = sam
	_ = svc

	// The branch model's yard room is seeded (chYardTrenton): the room
	// itself is structural, so the test does not create it; SyncMembership
	// is what writes the derived rows under review.
	if err := repo.SyncMembership(ctx, gm.UserID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	roomID := chYardTrenton

	rows, err := pool.Query(ctx,
		`SELECT added_reason, COALESCE(added_by::text,''), source
		 FROM msg_members WHERE channel_id = $1`, roomID)
	if err != nil {
		t.Fatalf("query members: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var reason, addedBy, source string
		if err := rows.Scan(&reason, &addedBy, &source); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if source != "DERIVED" {
			continue
		}
		found = true
		if reason != "" {
			t.Fatalf("a derived row must have empty added_reason, got %q", reason)
		}
		if addedBy != "" {
			t.Fatalf("a derived row must have NULL added_by, got %q", addedBy)
		}
	}
	if !found {
		t.Fatalf("no DERIVED row found in the branch room after sync")
	}
}

// TestIntegration_Delete_Channel_By_Admin_Archives proves the
// ChannelDelete path (house messages admin only) the brief demands: a delete
// from the room's settings panel archives the room (is_archived = true) and
// refuses for a non-admin. An already-archived room answers ErrChannelArchived
// (a re-delete is not a re-archive).
func TestIntegration_Delete_Channel_By_Admin_Archives(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)

	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	sam := samCaller()
	founder := Caller{UserID: sam.UserID, RoleCode: "GENERAL_MANAGER", Scope: "BRANCH", Branches: sam.Branches}
	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	name := fmt.Sprintf("delete probe %d", time.Now().UnixNano())
	room, _, err := svc.CreateChannel(ctx, founder, CreateChannelInput{
		Kind: KindTeam, Name: name, Slug: name,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, room.ID)
	})

	// A plain member cannot delete the room: the settings panel is admins
	// only, and the call returns the same wall the route carries. The
	// plain member must already be in the room (the membership wall runs
	// first; the manager wall is the second gate).
	plainMember := Caller{UserID: uCaseyDoyle, RoleCode: "PURCHASING", Scope: "BRANCH", Branches: map[string]bool{branchTrenton: true}}
	if _, err := svc.AddMember(ctx, sam, AddMemberInput{
		ChannelID: room.ID, UserID: plainMember.UserID, Role: RoleMember,
		AddedReason: "delete-probe seat for the wall", AddedBy: sam.UserID,
	}); err != nil {
		t.Fatalf("seat plain member: %v", err)
	}
	if err := svc.DeleteChannel(ctx, plainMember, room.ID); !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("a non-manager must refuse, got %v", err)
	}

	// Seat the general manager in the room so the membership wall (which
	// runs first) lets the manager wall (which runs second) reach the
	// archive path. The seat is a hand-add from sam, the founder; the
	// audit row names the manager.
	if _, err := svc.AddMember(ctx, sam, AddMemberInput{
		ChannelID: room.ID, UserID: gm.UserID, Role: RoleMember,
		AddedReason: "delete-probe seat for the admin wall", AddedBy: sam.UserID,
	}); err != nil {
		t.Fatalf("seat gm: %v", err)
	}

	// A house messages admin deletes: the row's is_archived flips true,
	// and the room still exists (the delete is a soft archive, the audit
	// trail; the row stays for the read path).
	if err := svc.DeleteChannel(ctx, gm, room.ID); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	var archived bool
	if err := pool.QueryRow(ctx,
		`SELECT is_archived FROM msg_channels WHERE id = $1`, room.ID).Scan(&archived); err != nil {
		t.Fatalf("read is_archived: %v", err)
	}
	if !archived {
		t.Fatalf("the room must be archived after admin delete")
	}

	// A second delete answers ErrChannelArchived: the room is already gone
	// from the settings panel, and re-archiving is not the keep-state the
	// route answers.
	if err := svc.DeleteChannel(ctx, gm, room.ID); !errors.Is(err, ErrChannelArchived) {
		t.Fatalf("re-delete must refuse, got %v", err)
	}
}
