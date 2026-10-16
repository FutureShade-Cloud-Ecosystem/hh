package messaging

// channels_admin_route_test.go: the channel-create, DM-create and membership
// routes at the route level, through the real Handler on a real mux behind the
// testkit guards. Red first per route and per refusal: before the routes were
// registered the mux answered 404 (405 beside the channels GET pattern) on
// every leg, the recorded red evidence in the item report; each refusal leg
// asserts its own status (400/403/404/409), so a refusal that stops being
// written fails here rather than drifting back into the catch-all.
//
// The double is HTTP-only: adminStore embeds the suite's fakeStore and adds
// the channel/DM/member seams, so the service under the routes runs its real
// validation against scripted rows.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The route legs' fixed world: a UUID caller (the repository binds caller ids
// into uuid columns), a second staffer, the Kingston branch root and the
// seeded Trenton yard channel id (the integration-tagged suite holds its own
// copies of the seeded ids under the build tag).
const (
	rtCaller   = "d7000000-0000-4000-9000-0000000000aa"
	rtBobby    = "d7000000-0000-4000-9000-0000000000bb"
	rtDana     = "d7000000-0000-4000-9000-0000000000cc"
	rtUnknown  = "d7000000-0000-4000-9000-0000000000ff"
	rtKingston = "5d1b3f6e-0000-4000-9000-000000000002"
	rtYardTrt  = "a5000000-0000-4000-9000-000000000001"
)

// adminStore is the route double: the suite's fakeStore (membership, channels,
// slugs) plus the channel/DM/member seams the new routes drive. activeUsers
// models the staff rows the repository validates against (the caller and the
// second staffer are ACTIVE; rtUnknown is nobody).
type adminStore struct {
	*fakeStore
	createdChannels []Channel
	dmChannels      map[string]Channel // sorted "a|b" pair -> channel
	rooms           map[string]Channel // slug -> the room that holds it
	addedMembers    []string           // "channelID|userID"
	removedMembers  []string           // "channelID|userID"
	derivedMembers  map[string]bool    // "channelID|userID" -> removal refuses
	activeUsers     map[string]bool
	channelSeq      int
}

func newAdminStore() *adminStore {
	return &adminStore{
		fakeStore:      newFakeStore(),
		dmChannels:     map[string]Channel{},
		rooms:          map[string]Channel{},
		derivedMembers: map[string]bool{},
		activeUsers: map[string]bool{
			rtCaller: true,
			rtBobby:  true,
		},
	}
}

// plantRoom seeds the double with a room the tests did not found through
// the routes (a seeded room, a DM), the shape the repository would hold.
// The first member named takes the room's OWNER row.
func (f *adminStore) plantRoom(c Channel, memberIDs ...string) {
	f.rooms[c.Slug] = c
	f.exists[c.ID] = true
	f.facts[c.ID] = channelFactsRow{Kind: c.Kind, BranchID: c.BranchID}
	for i, id := range memberIDs {
		f.setMember(c.ID, id)
		f.memberSources[c.ID+"|"+id] = SourceExplicit
		if i == 0 {
			f.memberRoles[c.ID+"|"+id] = "OWNER"
		} else {
			f.memberRoles[c.ID+"|"+id] = "MEMBER"
		}
	}
}

func (f *adminStore) CreateChannel(_ context.Context, in CreateChannelInput, ownerID string) (Channel, bool, error) {
	if in.Slug == "" {
		in.Slug = sanitizeSlugPart(in.Name)
	}
	// The convergence the repository's slug index carries: a slug that
	// already holds a room answers that room (created false), and the
	// double, like the repository, writes nothing on that arm.
	if c, ok := f.rooms[in.Slug]; ok {
		return c, false, nil
	}
	// A UUID id: the handler validates {id} at the boundary, so the double
	// must mint what the repository would.
	f.channelSeq++
	c := Channel{ID: fmt.Sprintf("e1000000-0000-4000-9000-%012d", f.channelSeq), Kind: in.Kind,
		BranchID: in.BranchID, Slug: in.Slug, Name: in.Name, Topic: in.Topic, Tier: TierStandard}
	f.createdChannels = append(f.createdChannels, c)
	f.rooms[in.Slug] = c
	f.slugs[in.Slug] = c.ID
	f.setMember(c.ID, ownerID)
	f.memberRoles[c.ID+"|"+ownerID] = "OWNER"
	f.memberSources[c.ID+"|"+ownerID] = SourceExplicit
	f.facts[c.ID] = channelFactsRow{Kind: in.Kind, BranchID: in.BranchID}
	return c, true, nil
}

func dmPairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func (f *adminStore) CreateDM(_ context.Context, aUserID, bUserID string) (Channel, bool, error) {
	if !f.activeUsers[aUserID] || !f.activeUsers[bUserID] {
		return Channel{}, false, errNotFound
	}
	if c, ok := f.dmChannels[dmPairKey(aUserID, bUserID)]; ok {
		return c, false, nil
	}
	// A UUID id: the handler validates {id} at the boundary, so the double
	// must mint what the repository would.
	f.channelSeq++
	c := Channel{ID: fmt.Sprintf("e3000000-0000-4000-9000-%012d", f.channelSeq), Kind: KindDM,
		Name: "DM", Slug: dmPairSlug(aUserID, bUserID), Tier: TierStandard}
	f.dmChannels[dmPairKey(aUserID, bUserID)] = c
	if c.Slug != "" {
		f.rooms[c.Slug] = c
	}
	f.setMember(c.ID, aUserID)
	f.setMember(c.ID, bUserID)
	f.memberRoles[c.ID+"|"+aUserID] = "OWNER"
	f.memberRoles[c.ID+"|"+bUserID] = "MEMBER"
	f.memberSources[c.ID+"|"+aUserID] = SourceExplicit
	f.memberSources[c.ID+"|"+bUserID] = SourceExplicit
	f.facts[c.ID] = channelFactsRow{Kind: KindDM}
	return c, true, nil
}

func (f *adminStore) AddChannelMember(_ context.Context, in AddMemberInput) (ChannelMember, error) {
	f.addedMembers = append(f.addedMembers, in.ChannelID+"|"+in.UserID)
	f.setMember(in.ChannelID, in.UserID)
	role := in.Role
	if role == "" {
		role = RoleMember
	}
	if src, ok := f.memberSources[in.ChannelID+"|"+in.UserID]; ok {
		return ChannelMember{ChannelID: in.ChannelID, UserID: in.UserID, Role: f.memberRoles[in.ChannelID+"|"+in.UserID], Source: src, AddedReason: in.AddedReason, AddedBy: in.AddedBy}, nil
	}
	f.memberSources[in.ChannelID+"|"+in.UserID] = SourceExplicit
	f.memberRoles[in.ChannelID+"|"+in.UserID] = role
	return ChannelMember{ChannelID: in.ChannelID, UserID: in.UserID, Role: role, Source: SourceExplicit, AddedReason: in.AddedReason, AddedBy: in.AddedBy}, nil
}

func (f *adminStore) RemoveChannelMember(_ context.Context, channelID, userID string) error {
	if f.derivedMembers[channelID+"|"+userID] {
		return ErrDerivedMember
	}
	// The last-owner guard the repository's delete predicate holds: an
	// OWNER row that is the room's only one survives every removal.
	if f.memberRoles[channelID+"|"+userID] == "OWNER" {
		owners := 0
		for uid := range f.members[channelID] {
			if f.memberRoles[channelID+"|"+uid] == "OWNER" {
				owners++
			}
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	f.removedMembers = append(f.removedMembers, channelID+"|"+userID)
	delete(f.members[channelID], userID)
	delete(f.memberRoles, channelID+"|"+userID)
	delete(f.memberSources, channelID+"|"+userID)
	return nil
}

// SetChannelVisibility satisfies the store seam for the new settings-panel
// route. The visibility-mutate path is the change 7 ruling 7 hand on the
// room's settings panel; the fake records the new value and returns the
// channel it would answer.
func (f *adminStore) SetChannelVisibility(_ context.Context, channelID, visibility string) (Channel, error) {
	if visibility != VisibilityPublic && visibility != VisibilityPrivate {
		return Channel{}, ErrBadVisibility
	}
	c, ok := f.rooms[channelID]
	if !ok {
		return Channel{}, errNotFound
	}
	c.Visibility = visibility
	f.rooms[channelID] = c
	return c, nil
}

// ArchiveChannel satisfies the store seam for the channel-delete route. The
// second-archive refusal the repository answers carries through here, so
// the route test that exercises a re-delete sees ErrChannelArchived in the
// same shape the live route hands back.
func (f *adminStore) ArchiveChannel(_ context.Context, channelID string) error {
	c, ok := f.rooms[channelID]
	if !ok {
		return errNotFound
	}
	if c.IsArchived {
		return ErrChannelArchived
	}
	c.IsArchived = true
	f.rooms[channelID] = c
	return nil
}

// memberSource / memberRole / channelFacts / userHoldsAnyRole / userReachesBranch
// satisfy the membership walls' private seams for the new methods.

func (f *adminStore) memberRole(_ context.Context, channelID, userID string) (string, error) {
	return f.memberRoles[channelID+"|"+userID], nil
}

func (f *adminStore) memberSource(_ context.Context, channelID, userID string) (string, error) {
	return f.memberSources[channelID+"|"+userID], nil
}

func (f *adminStore) channelFacts(_ context.Context, channelID string) (channelFactsRow, error) {
	return f.facts[channelID], nil
}

func (f *adminStore) userHoldsAnyRole(_ context.Context, _ string, _ []string) (bool, error) {
	return true, nil
}

func (f *adminStore) userReachesBranch(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

// ListDirectory satisfies the new store seam for the directory route
// (rule 7's "Channel directory + self join and leave"). The fake walks
// the rooms map and skips private/archived/system kinds the live SQL's
// WHERE clause also excludes.
func (f *adminStore) ListDirectory(_ context.Context, userID string, isGlobal bool, branches map[string]bool) (map[string]DirectoryEntry, error) {
	if isGlobal {
		branches = map[string]bool{}
	}
	out := make(map[string]DirectoryEntry)
	for id, ch := range f.rooms {
		if ch.IsArchived {
			continue
		}
		if ch.Kind != KindTeam && ch.Kind != KindTopic && ch.Kind != KindBranch {
			continue
		}
		if ch.Visibility != VisibilityPublic {
			continue
		}
		if ch.BranchID != "" && !isGlobal && !branches[ch.BranchID] {
			continue
		}
		source := f.memberSources[id+"|"+userID]
		var memberCount int
		for uid := range f.members[id] {
			if f.memberRoles[id+"|"+uid] == "OWNER" || f.memberRoles[id+"|"+uid] == "MEMBER" || source != "" {
				memberCount++
			}
		}
		out[id] = DirectoryEntry{
			ChannelID:        id,
			Kind:             ch.Kind,
			BranchID:         ch.BranchID,
			Name:             ch.Name,
			Topic:            ch.Topic,
			Slug:             ch.Slug,
			Joined:           source != "",
			MembershipSource: source,
			MemberCount:      memberCount,
		}
	}
	return out, nil
}

// ListChannelMembers satisfies the membership-rule read route. The fake
// walks members and answers each row's audit fields.
func (f *adminStore) ListChannelMembers(_ context.Context, channelID string) ([]ChannelMemberEntry, error) {
	var out []ChannelMemberEntry
	for uid, isMember := range f.members[channelID] {
		if !isMember {
			continue
		}
		role := f.memberRoles[channelID+"|"+uid]
		source := f.memberSources[channelID+"|"+uid]
		out = append(out, ChannelMemberEntry{
			UserID:      uid,
			DisplayName: "",
			Role:        role,
			Source:      source,
			AddedReason: "",
			AddedBy:     "",
		})
	}
	return out, nil
}

// GetChannelRule reads the room's rule fields from the fake's facts map.
func (f *adminStore) GetChannelRule(_ context.Context, channelID string) (MembershipRule, error) {
	c, ok := f.rooms[channelID]
	if !ok {
		return MembershipRule{}, errNotFound
	}
	return MembershipRule{
		AutoScope:  "",
		AutoRole:   "",
		Visibility: c.Visibility,
	}, nil
}

// SetChannelRule records the new rule on the fake's room; the live SQL
// writes the row in a transaction.
func (f *adminStore) SetChannelRule(_ context.Context, channelID string, rule MembershipRule) error {
	c, ok := f.rooms[channelID]
	if !ok {
		return errNotFound
	}
	c.Visibility = rule.Visibility
	f.rooms[channelID] = c
	return nil
}

// ListChannelRules satisfies the collection read for GET /api/v1/messaging/membership-rules.
// The fake mirrors the live predicate's branch reach and visibility filter.
func (f *adminStore) ListChannelRules(_ context.Context, _ string, isGlobal bool, _ map[string]bool) ([]MembershipRuleEntry, error) {
	if isGlobal {
		// noop
	}
	var out []MembershipRuleEntry
	for id, ch := range f.rooms {
		if ch.IsArchived {
			continue
		}
		if ch.Kind != KindTeam && ch.Kind != KindTopic && ch.Kind != KindBranch {
			continue
		}
		if ch.Visibility != VisibilityPublic {
			continue
		}
		out = append(out, MembershipRuleEntry{
			ChannelID:  id,
			Name:       ch.Name,
			Kind:       ch.Kind,
			BranchID:   ch.BranchID,
			AutoScope:  "",
			AutoRole:   "",
			Visibility: ch.Visibility,
		})
	}
	return out, nil
}

// newAdminRoutes wires the real Handler over the double behind the testkit
// guards, with the linker resolving the bearer to rtCaller (a branch-scoped
// frontline caller holding Trenton: every role works House Messages, the
// general manager administers it, the roleconfig document says so).
func newAdminRoutes(t *testing.T, fs *adminStore) (*http.ServeMux, string) {
	return newAdminRoutesAs(t, fs, rtCaller, identity.StaffAuthority{
		RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
	})
}

// newAdminRoutesAs is newAdminRoutes with the linked caller and the authority
// the guards resolve chosen by the test: the membership walls read the
// caller's role, so the legs need the general manager's authority as easily
// as the frontline caller's.
func newAdminRoutesAs(t *testing.T, fs *adminStore, linkedUser string, authority identity.StaffAuthority) (*http.ServeMux, string) {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(authority)
	h := NewHandler(&Service{repo: fs}, nil).
		WithUserLinker(func(_ context.Context, _ string) (string, bool) { return linkedUser, true })
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	return mux, sg.Bearer(t)
}

// adminDo sends one request with the staff bearer and X-Org.
func adminDo(t *testing.T, mux *http.ServeMux, bearer, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// The happy legs: a TEAM channel is created (201 the first time, 200 on the
// idempotent replay), a DM between two staff is found-or-created once, a
// member is added and an explicit member is removed.
func TestRoute_CreateChannel_And_Idempotent_Replay(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newAdminRoutes(t, fs)
	body := []byte(`{"kind":"TEAM","name":"Paving crew","topic":"The paving crew's room"}`)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel: %d %s", rec.Code, rec.Body.String())
	}
	rec = adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("replayed create: %d %s, want 200 on the found channel", rec.Code, rec.Body.String())
	}
	if len(fs.createdChannels) != 1 {
		t.Fatalf("the replay created a second channel: %+v", fs.createdChannels)
	}
}

func TestRoute_CreateDM_FoundOrCreatedOnce(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newAdminRoutes(t, fs)
	body := []byte(`{"user_id":"` + rtBobby + `"}`)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/dms", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dm: %d %s", rec.Code, rec.Body.String())
	}
	rec = adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/dms", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("replayed dm: %d %s, want 200 on the found channel", rec.Code, rec.Body.String())
	}
	if len(fs.dmChannels) != 1 {
		t.Fatalf("the replay created a second dm: %+v", fs.dmChannels)
	}
}

func TestRoute_AddAndRemoveMember(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels", []byte(`{"kind":"TEAM","name":"Paving crew"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel: %d %s", rec.Code, rec.Body.String())
	}
	ch := fs.createdChannels[0].ID
	rec = adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels/"+ch+"/members", []byte(`{"user_id":"`+rtBobby+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("add member: %d %s", rec.Code, rec.Body.String())
	}
	rec = adminDo(t, mux, bearer, http.MethodDelete, "/api/v1/messaging/channels/"+ch+"/members/"+rtBobby, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove member: %d %s", rec.Code, rec.Body.String())
	}
}

// The refusal legs, each pinned to its own status.

// A kind outside TEAM/TOPIC (the creatable kinds) is a client fault.
func TestRoute_CreateChannel_Refuses_Structural_Kinds(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newAdminRoutes(t, fs)
	for _, kind := range []string{"BRANCH", "DM", "APPROVAL"} {
		rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels",
			[]byte(`{"kind":"`+kind+`","name":"X"}`))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("create %s channel: %d %s, want 400", kind, rec.Code, rec.Body.String())
		}
	}
}

// A branch the caller does not hold is the branch wall the module's reads
// already carry: 403, never a channel created above the wall.
func TestRoute_CreateChannel_Refuses_Behind_The_Branch_Wall(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels",
		[]byte(`{"kind":"TEAM","name":"Kingston crew","branch_id":"`+rtKingston+`"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("create behind the branch wall: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if len(fs.createdChannels) != 0 {
		t.Fatalf("a channel was created behind the wall: %+v", fs.createdChannels)
	}
}

// A DM needs another staff member: naming the caller's own id is a client
// fault.
func TestRoute_CreateDM_Refuses_Self(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/dms", []byte(`{"user_id":"`+rtCaller+`"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dm to self: %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// A DM naming a user who is not an active staffer is refused, not invented.
func TestRoute_CreateDM_Refuses_Unknown_User(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/dms", []byte(`{"user_id":"`+rtUnknown+`"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("dm to unknown user: %d %s, want 404", rec.Code, rec.Body.String())
	}
}

// A caller who is not a member of the channel cannot add or remove members:
// the module's existing read wall (assertMember), 403 here.
func TestRoute_Membership_Refuses_NonMember(t *testing.T) {
	fs := newAdminStore()
	fs.exists[rtYardTrt] = true // the channel exists, but the caller is not a member
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels/"+rtYardTrt+"/members",
		[]byte(`{"user_id":"`+rtBobby+`"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member add: %d %s, want 403", rec.Code, rec.Body.String())
	}
	rec = adminDo(t, mux, bearer, http.MethodDelete, "/api/v1/messaging/channels/"+rtYardTrt+"/members/"+rtBobby, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member remove: %d %s, want 403", rec.Code, rec.Body.String())
	}
}

// Removing a DERIVED member is a conflict with the role and branch model that
// owns that membership, not a permission failure. The caller holds the room's
// OWNER row (the membership walls' pen), so the conflict is what answers.
func TestRoute_RemoveMember_Refuses_Derived_Member(t *testing.T) {
	fs := newAdminStore()
	fs.plantRoom(Channel{ID: rtYardTrt, Kind: KindBranch, Slug: "yard-trenton", Name: "#yard-trenton",
		BranchID: "5d1b3f6e-0000-4000-9000-000000000001", Tier: TierStandard}, rtCaller, rtBobby)
	fs.derivedMembers[rtYardTrt+"|"+rtBobby] = true
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodDelete, "/api/v1/messaging/channels/"+rtYardTrt+"/members/"+rtBobby, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("remove derived member: %d %s, want 409", rec.Code, rec.Body.String())
	}
}
