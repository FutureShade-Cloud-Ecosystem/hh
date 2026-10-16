//go:build integration

// group_rooms_integration_test.go: slice S2's group room surface against the
// migrated + seeded database, over the real handler, service and repository
// (the route harness of the untagged security tests over the real pool, the
// membership integration tests' database). The change 7 rulings 3 and 4: any
// staffer starts a named group with any set of staffers, any member adds any
// staffer by hand from any branch, any member renames, a system line records
// the rename and the add, and a direct message stays a pair (the fixed
// membership refusal is never weakened). Red first: every leg ran against the
// tree before the group code existed, the two new routes answering 404.

package messaging

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// groupRoutesAs wires the real handler over the real service and repository
// for ONE caller: the bearer resolves to linkedUser with the authority given,
// so each actor of a leg holds its own mux (the untagged harness's
// newAdminRoutesAs over the integration pool).
func groupRoutesAs(t *testing.T, linkedUser string, authority identity.StaffAuthority) (*http.ServeMux, string) {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(authority)
	h := NewHandler(NewService(NewRepository(intPool(t))), nil).
		WithUserLinker(func(_ context.Context, _ string) (string, bool) { return linkedUser, true })
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	return mux, sg.Bearer(t)
}

var (
	groupFrontline = identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Branches: []string{branchTrenton, branchKingston}}
	groupManager   = identity.StaffAuthority{RoleID: "general_manager", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
)

// groupCreate posts the group create body and decodes the channel answer.
func groupCreate(t *testing.T, mux *http.ServeMux, bearer, body string) (map[string]any, *int) {
	t.Helper()
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/groups", []byte(body))
	if rec.Code != http.StatusCreated {
		return nil, &rec.Code
	}
	var ch map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode group create answer: %v", err)
	}
	return ch, nil
}

// groupSystemLines answers the group's EVENT lines (the system lines the add
// and the rename write).
func groupSystemLines(t *testing.T, channelID string) []string {
	t.Helper()
	rows, err := intPool(t).Query(context.Background(),
		`SELECT body FROM msg_messages WHERE channel_id = $1 AND kind = 'EVENT' ORDER BY created_at ASC`,
		channelID)
	if err != nil {
		t.Fatalf("read the group's system lines: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan system line: %v", err)
		}
		out = append(out, body)
	}
	return out
}

// groupListContains answers whether the caller's channel list carries the
// room, read through the repository's plain SELECT: the handler's list
// resyncs the caller's derived membership, and this file must leave no row
// a later test's fresh-seat assumption stands on.
func groupListContains(t *testing.T, userID, channelID string) bool {
	t.Helper()
	chans, err := NewRepository(intPool(t)).ListChannelsForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("list channels for %s: %v", userID, err)
	}
	for _, ch := range chans {
		if ch.ID == channelID {
			if ch.Kind != "GROUP" {
				t.Fatalf("the room lists with kind %q, want GROUP", ch.Kind)
			}
			return true
		}
	}
	return false
}

// The founding leg: a staffer starts a named group with a set of staffers; the
// room is a GROUP the founder owns, and the reads list it to its members only.
func TestIntegration_Group_Create_WithName_ListedToMembersOnly(t *testing.T) {
	ctx := context.Background()
	samMux, samBearer := groupRoutesAs(t, uSamRivera, groupFrontline)
	outsider := uatInsideSalesID(ctx, t, intPool(t))

	ch, failed := groupCreate(t, samMux, samBearer,
		`{"name":"Patio crew","member_ids":["`+uCaseyDoyle+`"]}`)
	if failed != nil {
		t.Fatalf("create group: %d (the group route is not served)", *failed)
	}
	id, _ := ch["id"].(string)
	if id == "" || ch["kind"] != "GROUP" || ch["name"] != "Patio crew" {
		t.Fatalf("the created room is not the named group: %v", ch)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, id)
	})

	// The founder owns the room; the named staffer holds an explicit seat.
	var ownerRole, memberRole string
	if err := intPool(t).QueryRow(ctx,
		`SELECT role FROM msg_members WHERE channel_id = $1 AND user_id = $2`, id, uSamRivera).Scan(&ownerRole); err != nil {
		t.Fatalf("the founder holds no seat: %v", err)
	}
	if err := intPool(t).QueryRow(ctx,
		`SELECT role FROM msg_members WHERE channel_id = $1 AND user_id = $2`, id, uCaseyDoyle).Scan(&memberRole); err != nil {
		t.Fatalf("the named staffer holds no seat: %v", err)
	}
	if ownerRole != "OWNER" || memberRole != "MEMBER" {
		t.Fatalf("seats: owner=%s member=%s", ownerRole, memberRole)
	}

	// The reads treat the group like a channel the caller is in: listed to
	// both members, never to a staffer outside it.
	if !groupListContains(t, uSamRivera, id) || !groupListContains(t, uCaseyDoyle, id) {
		t.Fatalf("a member's channel list misses the group")
	}
	if groupListContains(t, outsider, id) {
		t.Fatalf("an outsider's channel list carries the group")
	}
}

// The add leg: any member adds any staffer by hand, across branches (a group
// spans branches), and the fresh seat writes the system line naming who was
// added and by whom.
func TestIntegration_Group_AnyMemberAdds_AcrossBranches_SystemLine(t *testing.T) {
	ctx := context.Background()
	samMux, samBearer := groupRoutesAs(t, uSamRivera, groupFrontline)
	caseyMux, caseyBearer := groupRoutesAs(t, uCaseyDoyle, groupFrontline)

	ch, failed := groupCreate(t, samMux, samBearer,
		`{"name":"Patio crew","member_ids":["`+uCaseyDoyle+`"]}`)
	if failed != nil {
		t.Fatalf("create group: %d (the group route is not served)", *failed)
	}
	id, _ := ch["id"].(string)
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, id)
	})

	// Casey (a Kingston yard manager, a plain MEMBER of the group Sam
	// founded in no branch) seats Alex (GLOBAL) by hand: any member, any
	// branch, no owner and no admin in the room.
	rec := adminDo(t, caseyMux, caseyBearer, http.MethodPost,
		"/api/v1/messaging/channels/"+id+"/members", []byte(`{"user_id":"`+uAlexMorgan+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("a plain member's cross branch add: %d %s", rec.Code, rec.Body.String())
	}
	if !groupListContains(t, uAlexMorgan, id) {
		t.Fatalf("the added staffer's channel list misses the group")
	}

	// The system line records who was added and by whom, in the room.
	words := displayNameOf(ctx, t, intPool(t), uAlexMorgan) + " " + displayNameOf(ctx, t, intPool(t), uCaseyDoyle)
	found := false
	for _, line := range groupSystemLines(t, id) {
		if strings.Contains(line, displayNameOf(ctx, t, intPool(t), uAlexMorgan)) && strings.Contains(line, displayNameOf(ctx, t, intPool(t), uCaseyDoyle)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no system line carries both the added staffer and the adder (%s)", words)
	}
}

// The rename leg: any member renames the group, the new name reaches every
// member's read, and a system line records who renamed it and to what.
func TestIntegration_Group_AnyMemberRenames_SystemLine(t *testing.T) {
	ctx := context.Background()
	samMux, samBearer := groupRoutesAs(t, uSamRivera, groupFrontline)
	caseyMux, caseyBearer := groupRoutesAs(t, uCaseyDoyle, groupFrontline)

	ch, failed := groupCreate(t, samMux, samBearer,
		`{"name":"Patio crew","member_ids":["`+uCaseyDoyle+`"]}`)
	if failed != nil {
		t.Fatalf("create group: %d (the group route is not served)", *failed)
	}
	id, _ := ch["id"].(string)
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, id)
	})

	rec := adminDo(t, caseyMux, caseyBearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+id, []byte(`{"name":"Patio crew and drivers"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("a plain member's rename: %d %s", rec.Code, rec.Body.String())
	}
	var renamed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &renamed); err != nil || renamed["name"] != "Patio crew and drivers" {
		t.Fatalf("the rename answer does not carry the new name: %v", renamed)
	}
	if !groupListContains(t, uSamRivera, id) {
		t.Fatalf("the founder's channel list misses the renamed group")
	}
	// The name shows to every member: the other member's list carries the
	// new name, not the founding one.
	var name string
	if err := intPool(t).QueryRow(ctx, `SELECT name FROM msg_channels WHERE id = $1`, id).Scan(&name); err != nil || name != "Patio crew and drivers" {
		t.Fatalf("the room's name did not change: %q err=%v", name, err)
	}

	// The system line records who renamed the group and to what.
	found := false
	for _, line := range groupSystemLines(t, id) {
		if strings.Contains(line, displayNameOf(ctx, t, intPool(t), uCaseyDoyle)) && strings.Contains(line, "Patio crew and drivers") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no system line records the rename")
	}
}

// The walls: a staffer who holds no seat renames, adds and reads nothing; a
// direct message's membership stays fixed at its two parties; an inactive
// staffer and a non staffer id are refused.
func TestIntegration_Group_Walls_NonMember_DMFixed_Inactive_NonStaffer(t *testing.T) {
	ctx := context.Background()
	samMux, samBearer := groupRoutesAs(t, uSamRivera, groupFrontline)
	outsider := uatInsideSalesID(ctx, t, intPool(t))
	outMux, outBearer := groupRoutesAs(t, outsider, groupFrontline)

	ch, failed := groupCreate(t, samMux, samBearer, `{"name":"Patio crew"}`)
	if failed != nil {
		t.Fatalf("create group: %d (the group route is not served)", *failed)
	}
	id, _ := ch["id"].(string)
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, id)
	})

	// A non member manages and reads nothing (403, the channel-scoping wall).
	if rec := adminDo(t, outMux, outBearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+id, []byte(`{"name":"Not yours"}`)); rec.Code != http.StatusForbidden {
		t.Fatalf("a non member's rename: %d %s", rec.Code, rec.Body.String())
	}
	if rec := adminDo(t, outMux, outBearer, http.MethodPost,
		"/api/v1/messaging/channels/"+id+"/members", []byte(`{"user_id":"`+outsider+`"}`)); rec.Code != http.StatusForbidden {
		t.Fatalf("a non member's add: %d %s", rec.Code, rec.Body.String())
	}
	if rec := adminDo(t, outMux, outBearer, http.MethodGet,
		"/api/v1/messaging/channels/"+id+"/messages", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("a non member's message read: %d %s", rec.Code, rec.Body.String())
	}

	// A direct message stays a pair: the member add refuses with the fixed
	// membership conflict, whatever the group arm taught the seam.
	dmRec := adminDo(t, samMux, samBearer, http.MethodPost, "/api/v1/messaging/dms",
		[]byte(`{"user_id":"`+uCaseyDoyle+`"}`))
	if dmRec.Code != http.StatusOK && dmRec.Code != http.StatusCreated {
		t.Fatalf("open dm: %d %s", dmRec.Code, dmRec.Body.String())
	}
	var dm struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(dmRec.Body.Bytes(), &dm); err != nil || dm.ID == "" {
		t.Fatalf("decode dm: %v", err)
	}
	if rec := adminDo(t, samMux, samBearer, http.MethodPost,
		"/api/v1/messaging/channels/"+dm.ID+"/members", []byte(`{"user_id":"`+uAlexMorgan+`"}`)); rec.Code != http.StatusConflict {
		t.Fatalf("a third party into a dm: %d %s", rec.Code, rec.Body.String())
	}

	// An inactive staffer can be named but never seated: refused on the
	// create's set and on the add.
	inactive := insertInactiveStaffer(t)
	t.Cleanup(func() { _, _ = intPool(t).Exec(ctx, `DELETE FROM users WHERE id = $1`, inactive) })
	if rec := adminDo(t, samMux, samBearer, http.MethodPost, "/api/v1/messaging/groups",
		[]byte(`{"name":"Shift crew","member_ids":["`+inactive+`"]}`)); rec.Code != http.StatusForbidden {
		t.Fatalf("an inactive staffer on the create's set: %d %s", rec.Code, rec.Body.String())
	}
	if rec := adminDo(t, samMux, samBearer, http.MethodPost,
		"/api/v1/messaging/channels/"+id+"/members", []byte(`{"user_id":"`+inactive+`"}`)); rec.Code != http.StatusForbidden {
		t.Fatalf("an inactive staffer on the add: %d %s", rec.Code, rec.Body.String())
	}

	// An id that names no staffer at all is not found.
	if rec := adminDo(t, samMux, samBearer, http.MethodPost, "/api/v1/messaging/groups",
		[]byte(`{"name":"Ghost crew","member_ids":["99000000-0000-4000-9000-000000000001"]}`)); rec.Code != http.StatusNotFound {
		t.Fatalf("a non staffer id on the create's set: %d %s", rec.Code, rec.Body.String())
	}
	if rec := adminDo(t, samMux, samBearer, http.MethodPost,
		"/api/v1/messaging/channels/"+id+"/members", []byte(`{"user_id":"99000000-0000-4000-9000-000000000001"}`)); rec.Code != http.StatusNotFound {
		t.Fatalf("a non staffer id on the add: %d %s", rec.Code, rec.Body.String())
	}
}

// insertInactiveStaffer plants one suspended staffer row of the test's own and
// names it (the seats' active-staff wall is what the leg proves; the row is
// the test's own, removed with the test).
func insertInactiveStaffer(t *testing.T) string {
	t.Helper()
	id := "d7000000-0000-4000-9000-00000000f1a1"
	_, err := intPool(t).Exec(context.Background(),
		`INSERT INTO users (id, email, display_name, status, password_hash)
		 VALUES ($1, 'group-inactive-test@example.invalid', 'Inactive Test Hand', 'SUSPENDED', 'x')
		 ON CONFLICT (id) DO UPDATE SET status = 'SUSPENDED'`, id)
	if err != nil {
		t.Fatalf("plant the inactive staffer: %v", err)
	}
	return id
}
