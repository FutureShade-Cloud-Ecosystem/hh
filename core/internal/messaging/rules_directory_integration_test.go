//go:build integration

// rules_directory_integration_test.go: change 7 ruling 7 directory + join + leave
// and item 3b's membership-rules read and write (auto_scope, auto_role,
// visibility) against the migrated + seeded database. Owner:
// internal/messaging.
//
// Every leg here is the database's own verdict on the migration the ruling
// demands: a public directory lists PUBLIC channels and hides PRIVATE ones,
// staffer self-joins a public channel and is refused on a private one, a
// staffer leaves an EXPLICIT seat and is refused with 409 on a DERIVED
// seat ("Joined by rule, you stay"), and an admin flips the membership
// rule and the next sync reconciles the derived seats. Red first.

package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// - Channel directory -

// TestIntegration_Directory_Lists_Public_Hides_Private proves the change 7
// ruling 7 directory: GET /api/v1/messaging/directory answers the PUBLIC
// rooms and hides the PRIVATE ones; every entry carries the caller's
// membership state (joined, membership_source, member_count).
func TestIntegration_Directory_Lists_Public_Hides_Private(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	// Create a fresh public and a fresh private room the seed does not
	// already provide (the names are unique to this test).
	pubName := fmt.Sprintf("directory public %d", time.Now().UnixNano())
	privName := fmt.Sprintf("directory private %d", time.Now().UnixNano()+1)

	pub, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: pubName, Slug: pubName,
	}, uSamRivera)
	if err != nil || !created {
		t.Fatalf("create public: created=%v err=%v", created, err)
	}
	priv, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: privName, Slug: privName,
		Visibility: VisibilityPrivate,
	}, uSamRivera)
	if err != nil || !created {
		t.Fatalf("create private: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx,
			`DELETE FROM msg_channels WHERE id IN ($1, $2)`, pub.ID, priv.ID)
	})

	sam := samCaller()
	dir, err := svc.ListDirectory(ctx, sam)
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}

	if _, ok := dir[pub.ID]; !ok {
		t.Fatalf("the directory must carry the public room, got keys %v", keysOf(dir))
	}
	if _, ok := dir[priv.ID]; ok {
		t.Fatalf("the directory must hide the private room")
	}

	pubEntry := dir[pub.ID]
	if !pubEntry.Joined {
		t.Fatalf("the founder must read as joined")
	}
	if pubEntry.MembershipSource != SourceExplicit {
		t.Fatalf("the founder's source must be EXPLICIT, got %q", pubEntry.MembershipSource)
	}
	if pubEntry.MemberCount != 1 {
		t.Fatalf("a fresh room's member count must be 1 (the founder), got %d", pubEntry.MemberCount)
	}
}

// TestIntegration_Directory_Branch_Scoped proves the change 7 ruling 7
// branch reach: a branch-scoped caller (Sam, granted Trenton) sees only
// rooms they reach.
func TestIntegration_Directory_Branch_Scoped(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	// A room with no branch is GLOBAL-visible.
	gname := fmt.Sprintf("directory no branch %d", time.Now().UnixNano())
	g, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: gname, Slug: gname,
	}, uSamRivera)
	if err != nil || !created {
		t.Fatalf("create no-branch: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, g.ID)
	})

	dir, err := svc.ListDirectory(ctx, samCaller())
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	if _, ok := dir[g.ID]; !ok {
		t.Fatalf("a no-branch room must answer for every caller")
	}

	// The seeded branch rooms are the directory's branch-scoped reality.
	// Sam reaches Trenton only; the Trenton yard must show, the Kingston
	// yard must not.
	if _, ok := dir[chYardTrenton]; !ok {
		t.Fatalf("Sam must see the Trenton yard")
	}
	if _, ok := dir[chYardKingston]; ok {
		t.Fatalf("Sam must NOT see the Kingston yard")
	}
}

// - Self join and leave -

// TestIntegration_Join_Public_Team_Succeeds proves the change 7 ruling 7
// join: a staffer who reaches the channel's branch (Trenton) may
// self-join a PUBLIC TEAM room.
func TestIntegration_Join_Public_Team_Succeeds(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	sam := samCaller()
	name := fmt.Sprintf("join public %d", time.Now().UnixNano())
	ch, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: name, Slug: name, BranchID: branchTrenton,
	}, sam.UserID)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, ch.ID)
	})

	// Casey (Kingston) does not reach Trenton; the join is refused.
	casey := Caller{UserID: uCaseyDoyle, RoleCode: "PURCHASING", Scope: "BRANCH", Branches: map[string]bool{branchKingston: true}}
	if _, err := svc.JoinChannel(ctx, casey, ch.ID); err == nil {
		t.Fatalf("a caller outside the branch must refuse")
	}

	// Alex (GLOBAL) reaches every branch and may join.
	alex := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	m, err := svc.JoinChannel(ctx, alex, ch.ID)
	if err != nil {
		t.Fatalf("alex join: %v", err)
	}
	if m.Source != SourceExplicit {
		t.Fatalf("a self-join must seat EXPLICIT, got %q", m.Source)
	}
	if m.Role != RoleMember {
		t.Fatalf("a self-join must seat MEMBER, got %q", m.Role)
	}

	// A second join is idempotent (already seated).
	if _, err := svc.JoinChannel(ctx, alex, ch.ID); err != nil {
		t.Fatalf("a re-join must be idempotent, got %v", err)
	}
}

// TestIntegration_Join_Private_Refuses proves the change 7 ruling 7 wall:
// a PRIVATE room hides from the directory and refuses a self-join
// (a private room is admin-or-owner hand-add only).
func TestIntegration_Join_Private_Refuses(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	sam := samCaller()
	name := fmt.Sprintf("join private %d", time.Now().UnixNano())
	ch, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: name, Slug: name,
		Visibility: VisibilityPrivate,
	}, sam.UserID)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, ch.ID)
	})

	// A caller is refused on a private room: the directory hides it, the
	// join is refused in shape ErrPrivateRoom.
	alex := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	if _, err := svc.JoinChannel(ctx, alex, ch.ID); err == nil {
		t.Fatalf("a private room must refuse self-join")
	}
}

// TestIntegration_Leave_Explicit_Refuses_On_Derived proves the change 7
// ruling 7 leave: a caller's DERIVED row is the role and branch model's
// and is never hand-removable (ErrDerivedMember, 409).
func TestIntegration_Leave_Explicit_Refuses_On_Derived(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	sam := samCaller()
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	// The branch model's yard room is seeded (chYardTrenton); the caller
	// has a DERIVED seat.
	if err := repo.SyncMembership(ctx, sam.UserID); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	if ok, _ := repo.IsMember(ctx, chYardTrenton, sam.UserID); !ok {
		t.Fatalf("Sam must be a derived member of the Trenton yard")
	}

	if err := svc.LeaveChannel(ctx, sam, chYardTrenton); !errors.Is(err, ErrDerivedMember) {
		t.Fatalf("a derived row must refuse leave, got %v", err)
	}
	if ok, _ := repo.IsMember(ctx, chYardTrenton, sam.UserID); !ok {
		t.Fatalf("the derived row must stay after a refused leave")
	}

	// A fresh caller who is not yet in any seeded room hand-builds a new
	// room and joins it: an EXPLICIT MEMBER row can leave, the wall only
	// refuses DERIVED rows. The seed's DM and office rooms would derive a
	// caller in, so the test cannot reuse them; a brand-new PUBLIC room
	// (no auto_scope, no branch) is the cleanest leave probe.
	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	name := fmt.Sprintf("leave-probe %d", time.Now().UnixNano())
	probe, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: name, Slug: name,
	}, sam.UserID)
	if err != nil || !created {
		t.Fatalf("create probe: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, probe.ID)
	})

	if _, err := svc.JoinChannel(ctx, gm, probe.ID); err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := svc.LeaveChannel(ctx, gm, probe.ID); err != nil {
		t.Fatalf("the explicit seat must allow leave, got %v", err)
	}
}

// TestIntegration_Leave_Last_Owner_Refuses proves the change 7 ruling 7
// last-owner wall: a removal must never drop a room's last OWNER.
func TestIntegration_Leave_Last_Owner_Refuses(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	sam := samCaller()
	name := fmt.Sprintf("last-owner %d", time.Now().UnixNano())
	ch, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: name, Slug: name,
	}, sam.UserID)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, ch.ID)
	})

	if err := svc.LeaveChannel(ctx, sam, ch.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("the last OWNER must refuse leave, got %v", err)
	}
	if ok, _ := repo.IsMember(ctx, ch.ID, sam.UserID); !ok {
		t.Fatalf("the OWNER row must stay after the refused leave")
	}
}

// TestIntegration_Leave_DM_Refuses proves the change 7 ruling 7 DM
// wall: a direct message's two parties are fixed, neither can leave.
func TestIntegration_Leave_DM_Refuses(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	alex := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	dm, _, err := svc.CreateDM(ctx, alex, uSamRivera)
	if err != nil {
		t.Fatalf("open dm: %v", err)
	}

	if err := svc.LeaveChannel(ctx, alex, dm.ID); !errors.Is(err, ErrMembershipFixed) {
		t.Fatalf("a DM must refuse leave, got %v", err)
	}
}

// - Membership rule (item 3b) -

// TestIntegration_SetMembershipRule_Reconcile_Derived proves the change 7
// ruling 7 rule write: an admin flips a room's auto_role to a specific
// role, and the next SyncMembership drops the seats that no longer qualify.
func TestIntegration_SetMembershipRule_Reconcile_Derived(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	sam := samCaller()
	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	// A new GLOBAL TEAM room whose auto_role narrows to INSIDE_SALES.
	name := fmt.Sprintf("rule reconcile %d", time.Now().UnixNano())
	ch, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: name, Slug: name,
		AutoScope: AutoScopeGlobal,
		AutoRole:  AutoRoleAny,
	}, gm.UserID)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, ch.ID)
	})

	// Sam (SALES_MANAGER) is derived today; the rule narrows to
	// INSIDE_SALES and the next sync must drop Sam.
	if _, err := svc.UpdateMembershipRule(ctx, gm, ch.ID, MembershipRule{
		AutoScope:  AutoScopeGlobal,
		AutoRole:   "INSIDE_SALES",
		Visibility: VisibilityPublic,
	}); err != nil {
		t.Fatalf("set rule: %v", err)
	}
	if err := repo.SyncMembership(ctx, sam.UserID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if ok, _ := repo.IsMember(ctx, ch.ID, sam.UserID); ok {
		t.Fatalf("a SALES_MANAGER must be pruned when the rule narrows to INSIDE_SALES")
	}
}

// TestIntegration_GetMembershipRule_Reads_The_Row proves the read
// side: the rule read carries auto_scope, auto_role, visibility.
func TestIntegration_GetMembershipRule_Reads_The_Row(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	name := fmt.Sprintf("rule read %d", time.Now().UnixNano())
	ch, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: name, Slug: name,
		AutoScope: AutoScopeBranch, BranchID: branchTrenton, AutoRole: AutoRoleAny,
	}, gm.UserID)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, ch.ID)
	})

	rule, err := svc.GetMembershipRule(ctx, gm, ch.ID)
	if err != nil {
		t.Fatalf("get rule: %v", err)
	}
	if rule.AutoScope != AutoScopeBranch {
		t.Fatalf("auto_scope must be BRANCH, got %q", rule.AutoScope)
	}
	if rule.Visibility != VisibilityPublic {
		t.Fatalf("visibility must be PUBLIC, got %q", rule.Visibility)
	}
}

// - Members list -

// TestIntegration_ListExplicitMembers_Carries_Reason proves the read
// side of the hand-added audit: the channel's EXPLICIT members list
// returns each row's added_reason and added_by alongside the user id
// and display name.
func TestIntegration_ListExplicitMembers_Carries_Reason(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	sam := samCaller()
	name := fmt.Sprintf("members list %d", time.Now().UnixNano())
	ch, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: name, Slug: name,
	}, sam.UserID)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, ch.ID)
	})

	// Sam seats Casey by hand with a reason; the audit pair lands.
	reason := "added by hand: Trenton swap, paving crew"
	if _, err := svc.AddMember(ctx, sam, AddMemberInput{
		ChannelID: ch.ID, UserID: uCaseyDoyle, Role: RoleMember,
		AddedReason: reason, AddedBy: sam.UserID,
	}); err != nil {
		t.Fatalf("add casey: %v", err)
	}

	members, err := svc.ListExplicitMembers(ctx, sam, ch.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	var foundCasey *ChannelMemberEntry
	for i, m := range members {
		if m.UserID == uCaseyDoyle {
			foundCasey = &members[i]
		}
	}
	if foundCasey == nil {
		t.Fatalf("Casey must be in the explicit members list")
	}
	if foundCasey.AddedReason != reason {
		t.Fatalf("added_reason must carry the hand-add reason, got %q", foundCasey.AddedReason)
	}
	if foundCasey.AddedBy != sam.UserID {
		t.Fatalf("added_by must name the manager who added them, got %q", foundCasey.AddedBy)
	}
}

// TestIntegration_Directory_Derived_Seat_Reads_As_Joined proves the
// directory's joined state for a derived row reads as joined with
// source DERIVED.
func TestIntegration_Directory_Derived_Seat_Reads_As_Joined(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	if err := repo.SyncMembership(ctx, gm.UserID); err != nil {
		t.Fatalf("sync gm: %v", err)
	}

	// The seeded #office (chOffice) is GLOBAL: GM is derived.
	dir, err := svc.ListDirectory(ctx, gm)
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	entry, ok := dir[chOffice]
	if !ok {
		t.Fatalf("the office room must show for the GM")
	}
	if !entry.Joined {
		t.Fatalf("the GM must read as joined in the office")
	}
	if entry.MembershipSource != SourceDerived {
		t.Fatalf("the GM's seat in the office must be DERIVED, got %q", entry.MembershipSource)
	}
}

// keysOf returns the keys of a map[string]DirectoryEntry for diagnostics.
func keysOf(m map[string]DirectoryEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
