//go:build integration

// reactions_mentions_integration_test.go: slice S3's mentions, reactions and
// seen-by against the migrated + seeded database, over the real handler,
// service and repository (the route harness of the group room tests over the
// real pool). The change 7 rulings at stake: ruling 5, the mentions view lists
// the messages naming the caller or the caller's role, the badge follows the
// stored mentions, and the old e-mail handle vocabulary keeps working; ruling
// 6, a reaction is none on EVENT, approval or other system messages. Red
// first: before the slice's code every leg failed, the three new routes
// answering 404 and the mention legs finding no stored mention and no badge.
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/google/uuid"
)

// s3RoutesAs wires the real handler over the real service and repository for
// ONE caller (the group room harness's shape over this slice's authority).
func s3RoutesAs(t *testing.T, linkedUser string, authority identity.StaffAuthority) (*http.ServeMux, string) {
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
	s3Frontline = identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Branches: []string{branchTrenton, branchKingston}}
	s3Manager   = identity.StaffAuthority{RoleID: "general_manager", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
	s3YardMgr   = identity.StaffAuthority{RoleID: "yard_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{branchKingston}}
)

// s3Posted carries the message ids a leg owes the cleanup (the suite shares one
// seeded database: a leg leaves no row a later leg's counts stand on).
var s3Posted []string

func s3CleanupPosted(t *testing.T) {
	t.Cleanup(func() {
		ctx := context.Background()
		for _, id := range s3Posted {
			_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_messages WHERE id = $1`, id)
		}
		s3Posted = nil
	})
}

func s3TrackPosted(id string) { s3Posted = append(s3Posted, id) }

// s3DisplayName resolves one staffer's display name (the mention the composer
// would author), s3EmailLocal their e-mail handle (the vocabulary the 0152
// badge heuristic answered to), and s3HoldsRole answers whether a staffer
// holds a role code.
func s3DisplayName(ctx context.Context, t *testing.T, userID string) string {
	t.Helper()
	var name string
	if err := intPool(t).QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1`, userID).Scan(&name); err != nil {
		t.Fatalf("resolve display name: %v", err)
	}
	return name
}

func s3EmailLocal(ctx context.Context, t *testing.T, userID string) string {
	t.Helper()
	var local string
	if err := intPool(t).QueryRow(ctx, `SELECT lower(split_part(email::text, '@', 1)) FROM users WHERE id = $1`, userID).Scan(&local); err != nil {
		t.Fatalf("resolve email local: %v", err)
	}
	return local
}

func s3HoldsRole(ctx context.Context, t *testing.T, userID, code string) bool {
	t.Helper()
	var holds bool
	if err := intPool(t).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id WHERE ur.user_id = $1 AND ro.code = $2)`,
		userID, code).Scan(&holds); err != nil {
		t.Fatalf("resolve role hold: %v", err)
	}
	return holds
}

// s3Post seats the caller in the room (the derived sync the hub runs on list)
// and posts through the service, returning the message id.
func s3Post(ctx context.Context, t *testing.T, repo *Repository, svc *Service, caller Caller, channelID, body string) string {
	t.Helper()
	if err := repo.SyncMembership(ctx, caller.UserID); err != nil {
		t.Fatalf("sync membership: %v", err)
	}
	m, err := svc.PostMessage(ctx, caller, channelID, body, "")
	if err != nil {
		t.Fatalf("post %q: %v", body, err)
	}
	s3TrackPosted(m.ID)
	return m.ID
}

// s3ResetReads moves every named member's cursor in the room past everything
// the seed holds, so a leg's badge counts only what the leg itself posts.
func s3ResetReads(ctx context.Context, t *testing.T, repo *Repository, channelID string, users ...string) {
	t.Helper()
	for _, u := range users {
		if err := repo.SyncMembership(ctx, u); err != nil {
			t.Fatalf("sync %s: %v", u, err)
		}
		if err := repo.MarkRead(ctx, channelID, u, time.Now()); err != nil {
			t.Fatalf("mark read %s: %v", u, err)
		}
	}
	time.Sleep(5 * time.Millisecond)
}

// s3Unread reads the effective badge row (the view the badge endpoint serves)
// for one member and room: the folded unread count, the mode, the flag.
func s3Unread(ctx context.Context, t *testing.T, userID, channelID string) (int, string, bool) {
	t.Helper()
	var unread int
	var mode string
	var mentioned bool
	err := intPool(t).QueryRow(ctx, `
		SELECT unread_count, mode, mentioned
		FROM msg_unread_effective WHERE user_id = $1 AND channel_id = $2`,
		userID, channelID).Scan(&unread, &mode, &mentioned)
	if err != nil {
		t.Fatalf("read unread row for %s: %v", userID, err)
	}
	return unread, mode, mentioned
}

// s3MentionRow answers whether the (message, target) mention row exists, by
// person or by role.
func s3MentionRow(ctx context.Context, t *testing.T, messageID, userID, roleCode string) bool {
	t.Helper()
	var ok bool
	var err error
	if userID != "" {
		err = intPool(t).QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM msg_mentions WHERE message_id = $1 AND user_id = $2)`,
			messageID, userID).Scan(&ok)
	} else {
		err = intPool(t).QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM msg_mentions WHERE message_id = $1 AND role_code = $2)`,
			messageID, roleCode).Scan(&ok)
	}
	if err != nil {
		t.Fatalf("read mention row: %v", err)
	}
	return ok
}

// s3MentionsViaRoute walks the mentions view over HTTP for one caller and
// answers whether the page carries the message.
func s3MentionsViaRoute(t *testing.T, linkedUser string, authority identity.StaffAuthority, messageID string) bool {
	t.Helper()
	mux, bearer := s3RoutesAs(t, linkedUser, authority)
	rec := adminDo(t, mux, bearer, http.MethodGet, "/api/v1/messaging/mentions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the mentions view for %s: %d %s (the route is not served)", linkedUser, rec.Code, rec.Body.String())
	}
	var page struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode mentions page: %v", err)
	}
	for _, m := range page.Messages {
		if m.ID == messageID {
			return true
		}
	}
	return false
}

// Ruling 5, the person arm: a post naming a member by display name stores the
// mention, rings the badge, and lists in the named member's view and nobody
// else's; the e-mail handle keeps working beside the display name; the
// MENTIONS notify mode folds to exactly the mention posts.
func TestIntegration_Mentions_Person_Badge_View_And_Email_Handle(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)
	alex := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	s3CleanupPosted(t)

	samName := s3DisplayName(ctx, t, uSamRivera)
	samLocal := s3EmailLocal(ctx, t, uSamRivera)

	// One post naming Sam by display name, one by the e-mail handle, one plain.
	s3ResetReads(ctx, t, repo, chOffice, uSamRivera, uAlexMorgan)
	byName := s3Post(ctx, t, repo, svc, alex, chOffice, fmt.Sprintf("@%s can you take this one?", samName))
	byHandle := s3Post(ctx, t, repo, svc, alex, chOffice, fmt.Sprintf("@%s the pallet count is short", samLocal))
	plain := s3Post(ctx, t, repo, svc, alex, chOffice, "no one in particular needs to read this")

	if !s3MentionRow(ctx, t, byName, uSamRivera, "") {
		t.Fatal("the display name mention stored no person row for Sam")
	}
	if !s3MentionRow(ctx, t, byHandle, uSamRivera, "") {
		t.Fatal("the e-mail handle mention stored no person row for Sam")
	}
	if s3MentionRow(ctx, t, plain, uSamRivera, "") {
		t.Fatal("a plain post stored a mention row")
	}

	// The badge: the flag rides the mentions, the count is all three posts,
	// and the author's own post never mentions the author.
	unread, _, samMentioned := s3Unread(ctx, t, uSamRivera, chOffice)
	if unread != 3 || !samMentioned {
		t.Fatalf("Sam's badge after three posts naming him twice: unread=%d mentioned=%v", unread, samMentioned)
	}
	if _, _, alexMentioned := s3Unread(ctx, t, uAlexMorgan, chOffice); alexMentioned {
		t.Fatal("Alex was mentioned by his own post")
	}

	// The MENTIONS mode folds to exactly the two mention posts.
	if _, err := repo.UpsertNotificationPrefs(ctx, applyMode(DefaultNotificationPrefs(chOffice, uSamRivera), ModeMentions)); err != nil {
		t.Fatalf("set mentions mode: %v", err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_notification_prefs WHERE channel_id = $1 AND user_id = $2`, chOffice, uSamRivera)
	})
	unread, mode, _ := s3Unread(ctx, t, uSamRivera, chOffice)
	if mode != ModeMentions || unread != 2 {
		t.Fatalf("in MENTIONS mode the fold is exactly the mention posts: unread=%d mode=%s", unread, mode)
	}

	// The mentions view: Sam's carries the named posts and not the plain one;
	// Alex's carries neither.
	if !s3MentionsViaRoute(t, uSamRivera, s3Frontline, byHandle) {
		t.Fatal("Sam's mentions view is missing the e-mail handle post")
	}
	if !s3MentionsViaRoute(t, uSamRivera, s3Frontline, byName) {
		t.Fatal("Sam's mentions view is missing the display name post")
	}
	if s3MentionsViaRoute(t, uSamRivera, s3Frontline, plain) {
		t.Fatal("Sam's mentions view lists a post that names nobody")
	}
	if s3MentionsViaRoute(t, uAlexMorgan, s3Manager, byName) {
		t.Fatal("Alex's mentions view lists a post that names Sam")
	}
}

// Ruling 5, the role arm: a post naming a role reaches the members who hold
// it (badge and view) and not the members who hold something else.
func TestIntegration_Mentions_Role_Badge_And_View(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)
	alex := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	s3CleanupPosted(t)

	if !s3HoldsRole(ctx, t, uCaseyDoyle, "YARD_MANAGER") {
		t.Fatal("fixture: Casey does not hold YARD_MANAGER, the leg's world is not the seed's")
	}
	if s3HoldsRole(ctx, t, uSamRivera, "YARD_MANAGER") {
		t.Fatal("fixture: Sam holds YARD_MANAGER, the not-named arm cannot run on this seed")
	}

	s3ResetReads(ctx, t, repo, chOffice, uSamRivera, uAlexMorgan, uCaseyDoyle)
	rolePost := s3Post(ctx, t, repo, svc, alex, chOffice, "@YARD_MANAGER the Kingston dock is backed up")
	plain := s3Post(ctx, t, repo, svc, alex, chOffice, "no role is named in this post")

	if !s3MentionRow(ctx, t, rolePost, "", "YARD_MANAGER") {
		t.Fatal("the role mention stored no role row")
	}
	if s3MentionRow(ctx, t, plain, "", "YARD_MANAGER") {
		t.Fatal("a plain post stored a role mention row")
	}

	// Casey holds the role: badge and view carry the post.
	if err := repo.SyncMembership(ctx, uCaseyDoyle); err != nil {
		t.Fatalf("sync casey: %v", err)
	}
	_, _, caseyMentioned := s3Unread(ctx, t, uCaseyDoyle, chOffice)
	if !caseyMentioned {
		t.Fatal("the yard manager's badge did not ring on a role mention")
	}
	if !s3MentionsViaRoute(t, uCaseyDoyle, s3YardMgr, rolePost) {
		t.Fatal("the yard manager's mentions view is missing the role post")
	}
	// Sam holds another role: the badge stays flat and the view stays empty of it.
	raw, _, samMentioned := s3Unread(ctx, t, uSamRivera, chOffice)
	if samMentioned {
		t.Fatal("Sam's badge rang on a role he does not hold")
	}
	if raw != 2 {
		t.Fatalf("Sam's raw unread should count both posts, got %d", raw)
	}
	if s3MentionsViaRoute(t, uSamRivera, s3Frontline, rolePost) {
		t.Fatal("Sam's mentions view lists a role post for a role he does not hold")
	}
}

// Ruling 6: a reaction seats on a person's TEXT message, its count and mine
// ride the read model, the remove drops exactly one, and EVENT and APPROVAL
// system messages refuse at the route with 409 and never grow a row.
func TestIntegration_Reactions_Counts_Mine_And_System_Refusals(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)
	sam := Caller{UserID: uSamRivera, RoleCode: "SALES_MANAGER", Scope: "BRANCH"}
	s3CleanupPosted(t)

	s3ResetReads(ctx, t, repo, chOffice, uSamRivera, uAlexMorgan)
	post := s3Post(ctx, t, repo, svc, sam, chOffice, "pallet is on the yard, the count is 12")

	samMux, samBearer := s3RoutesAs(t, uSamRivera, s3Frontline)
	alexMux, alexBearer := s3RoutesAs(t, uAlexMorgan, s3Manager)
	target := "/api/v1/messaging/channels/" + chOffice + "/messages/" + post + "/reactions"

	// Two members react with the same emoji; each reads count 2, mine true.
	// The first member's replay lands once (the PUT is idempotent).
	for i, actor := range []struct {
		mux    *http.ServeMux
		bearer string
	}{{samMux, samBearer}, {alexMux, alexBearer}} {
		rec := adminDo(t, actor.mux, actor.bearer, http.MethodPut, target, []byte(`{"emoji":"👍"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("put reaction: %d %s (the reactions route is not served)", rec.Code, rec.Body.String())
		}
		if i == 0 {
			// The idempotent replay before the second member joins: still one.
			rec = adminDo(t, actor.mux, actor.bearer, http.MethodPut, target, []byte(`{"emoji":"👍"}`))
			if rec.Code != http.StatusOK {
				t.Fatalf("the replayed PUT: %d %s", rec.Code, rec.Body.String())
			}
		}
		var out struct {
			Reactions []struct {
				Emoji string `json:"emoji"`
				Count int    `json:"count"`
				Mine  bool   `json:"mine"`
			} `json:"reactions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode reaction answer: %v", err)
		}
		if len(out.Reactions) != 1 || out.Reactions[0].Emoji != "👍" || out.Reactions[0].Count != 2 || !out.Reactions[0].Mine {
			t.Fatalf("the reaction summary is not count 2, mine true: %+v", out.Reactions)
		}
	}

	// The stream read model carries the chip, and the remove drops exactly
	// one. The stream walks over HTTP (the read model the client sees).
	streamReactions := func(mux *http.ServeMux, bearer, messageID string) []struct {
		Emoji string `json:"emoji"`
		Count int    `json:"count"`
		Mine  bool   `json:"mine"`
	} {
		rec := adminDo(t, mux, bearer, http.MethodGet, "/api/v1/messaging/channels/"+chOffice+"/messages?limit=5", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("read stream: %d %s", rec.Code, rec.Body.String())
		}
		var page struct {
			Messages []struct {
				ID        string `json:"id"`
				Reactions []struct {
					Emoji string `json:"emoji"`
					Count int    `json:"count"`
					Mine  bool   `json:"mine"`
				} `json:"reactions"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode stream page: %v", err)
		}
		for _, m := range page.Messages {
			if m.ID == messageID {
				return m.Reactions
			}
		}
		t.Fatalf("the posted message %s did not come back in the stream page", messageID)
		return nil
	}
	if chips := streamReactions(samMux, samBearer, post); len(chips) != 1 || chips[0].Count != 2 || !chips[0].Mine {
		t.Fatalf("the stream's reaction stitch: %+v", chips)
	}
	rec := adminDo(t, alexMux, alexBearer, http.MethodDelete, target+"/"+"👍", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete reaction: %d %s", rec.Code, rec.Body.String())
	}
	// The re-delete of what is already gone is still a 200 (idempotent).
	rec = adminDo(t, alexMux, alexBearer, http.MethodDelete, target+"/"+"👍", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the idempotent re-delete: %d %s", rec.Code, rec.Body.String())
	}
	if chips := streamReactions(alexMux, alexBearer, post); len(chips) != 1 || chips[0].Count != 1 || chips[0].Mine {
		t.Fatalf("after the remove the chip is count 1, not mine: %+v", chips)
	}

	// The system messages: an EVENT and an APPROVAL card both refuse, both
	// ways, and store nothing.
	eventKey := fmt.Sprintf("s3-evt-%d", time.Now().UnixNano())
	eventMsg, inserted, err := repo.InsertEventMessage(ctx, chOffice, eventKey, "a system line nobody reacts to", EventRef{Topic: "msg.event.test"}, false)
	if err != nil || !inserted {
		t.Fatalf("seed event message: inserted=%v err=%v", inserted, err)
	}
	s3TrackPosted(eventMsg.ID)
	approvalRef := ApprovalRef{ApprovalID: "s3-" + uuid.NewString(), PolicyType: "JE_OVER_THRESHOLD", Status: "PENDING"}
	cardMsg, inserted, err := repo.PostApprovalMessage(ctx, chOffice, approvalCardKey(approvalRef.ApprovalID), "an approval card nobody reacts to", approvalRef)
	if err != nil || !inserted {
		t.Fatalf("seed approval card: inserted=%v err=%v", inserted, err)
	}
	s3TrackPosted(cardMsg.ID)
	for _, sysID := range []string{eventMsg.ID, cardMsg.ID} {
		sysTarget := "/api/v1/messaging/channels/" + chOffice + "/messages/" + sysID + "/reactions"
		rec := adminDo(t, samMux, samBearer, http.MethodPut, sysTarget, []byte(`{"emoji":"👍"}`))
		if rec.Code != http.StatusConflict {
			t.Fatalf("reaction on system message %s: %d %s, want 409", sysID, rec.Code, rec.Body.String())
		}
		rec = adminDo(t, samMux, samBearer, http.MethodDelete, sysTarget+"/"+"👍", nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("reaction delete on system message %s: %d %s, want 409", sysID, rec.Code, rec.Body.String())
		}
	}
	var rows int
	if err := intPool(t).QueryRow(ctx,
		`SELECT COUNT(*) FROM msg_reactions WHERE message_id = ANY($1::uuid[])`,
		[]string{eventMsg.ID, cardMsg.ID}).Scan(&rows); err != nil {
		t.Fatalf("count system reactions: %v", err)
	}
	if rows != 0 {
		t.Fatalf("%d reactions sit on system messages", rows)
	}
}

// The reaction walls: a message of another room is not-found even for a member
// of the room the caller named (no cross-channel existence leaks), a
// non-member of the message's room is forbidden, and a bad key is a client
// fault, 400.
func TestIntegration_Reaction_Walls_Binding_Membership_Key(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)
	sam := Caller{UserID: uSamRivera, RoleCode: "SALES_MANAGER", Scope: "BRANCH"}
	casey := Caller{UserID: uCaseyDoyle, RoleCode: "YARD_MANAGER", Scope: "BRANCH"}
	s3CleanupPosted(t)

	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	// A TEXT post in the Trenton yard room; the reaction names #office instead.
	yardPost := s3Post(ctx, t, repo, svc, sam, chYardTrenton, "yard leg: the forklift charger is fixed")
	samMux, samBearer := s3RoutesAs(t, uSamRivera, s3Frontline)
	wrongRoom := "/api/v1/messaging/channels/" + chOffice + "/messages/" + yardPost + "/reactions"
	if rec := adminDo(t, samMux, samBearer, http.MethodPut, wrongRoom, []byte(`{"emoji":"👍"}`)); rec.Code != http.StatusNotFound {
		t.Fatalf("a foreign room's message must be not-found: %d %s", rec.Code, rec.Body.String())
	}
	if rec := adminDo(t, samMux, samBearer, http.MethodGet, "/api/v1/messaging/channels/"+chOffice+"/messages/"+yardPost+"/seen-by", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("seen-by on a foreign room's message: %d %s", rec.Code, rec.Body.String())
	}

	// A room Sam holds no seat in: Casey founds a TOPIC room of her own.
	topic, created, err := svc.CreateChannel(ctx, casey, CreateChannelInput{Kind: KindTopic, Name: fmt.Sprintf("s3 walls %d", time.Now().UnixNano())})
	if err != nil || !created {
		t.Fatalf("found topic room: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, topic.ID)
	})
	topicPost := s3Post(ctx, t, repo, svc, casey, topic.ID, "the walls leg's own post")
	outsiderTarget := "/api/v1/messaging/channels/" + topic.ID + "/messages/" + topicPost + "/reactions"
	if rec := adminDo(t, samMux, samBearer, http.MethodPut, outsiderTarget, []byte(`{"emoji":"👍"}`)); rec.Code != http.StatusForbidden {
		t.Fatalf("a non-member must be forbidden: %d %s", rec.Code, rec.Body.String())
	}

	// Bad keys: empty past the trim, or longer than the cap.
	ownTarget := "/api/v1/messaging/channels/" + topic.ID + "/messages/" + topicPost + "/reactions"
	caseyMux, caseyBearer := s3RoutesAs(t, uCaseyDoyle, s3YardMgr)
	for _, body := range []string{`{"emoji":"   "}`, `{"emoji":"` + strings.Repeat("x", 80) + `"}`} {
		rec := adminDo(t, caseyMux, caseyBearer, http.MethodPut, ownTarget, []byte(body))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("a bad reaction key must be 400: %d %s", rec.Code, rec.Body.String())
		}
	}
}

// The seen-by read: the author is never counted on their own post, a member's
// advanced cursor counts them, and a system message, whose author is nobody,
// counts every member who has read past it.
func TestIntegration_SeenBy_Author_Excluded_And_System_Counts_All(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)
	sam := Caller{UserID: uSamRivera, RoleCode: "SALES_MANAGER", Scope: "BRANCH"}
	s3CleanupPosted(t)

	s3ResetReads(ctx, t, repo, chOffice, uSamRivera, uAlexMorgan)
	post := s3Post(ctx, t, repo, svc, sam, chOffice, "seen-by leg: the pallet lands today")

	samMux, samBearer := s3RoutesAs(t, uSamRivera, s3Frontline)
	seenBy := func(messageID string) (int, string) {
		rec := adminDo(t, samMux, samBearer, http.MethodGet, "/api/v1/messaging/channels/"+chOffice+"/messages/"+messageID+"/seen-by", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("seen-by: %d %s (the route is not served)", rec.Code, rec.Body.String())
		}
		var out struct {
			Count   int `json:"count"`
			Readers []struct {
				UserID string `json:"user_id"`
			} `json:"readers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode seen-by: %v", err)
		}
		who := ""
		if len(out.Readers) > 0 {
			who = out.Readers[0].UserID
		}
		return out.Count, who
	}

	// Nobody has read past the post yet, and the author never counts.
	if n, who := seenBy(post); n != 0 || who != "" {
		t.Fatalf("before any read the count is 0: count=%d first=%s", n, who)
	}
	// Alex reads the room: he is counted, Sam (the author) is not.
	if err := repo.MarkRead(ctx, chOffice, uAlexMorgan, time.Now()); err != nil {
		t.Fatalf("alex mark read: %v", err)
	}
	if n, who := seenBy(post); n != 1 || who != uAlexMorgan {
		t.Fatalf("after Alex's read: count=%d first=%s, want 1 and Alex", n, who)
	}
	// A system message counts every member who reads past it (its author is
	// nobody): both members read again after it lands, both count.
	eventKey := fmt.Sprintf("s3-seen-%d", time.Now().UnixNano())
	eventMsg, inserted, err := repo.InsertEventMessage(ctx, chOffice, eventKey, "a system line the room reads", EventRef{Topic: "msg.event.test"}, false)
	if err != nil || !inserted {
		t.Fatalf("seed event message: inserted=%v err=%v", inserted, err)
	}
	s3TrackPosted(eventMsg.ID)
	time.Sleep(5 * time.Millisecond)
	for _, u := range []string{uSamRivera, uAlexMorgan} {
		if err := repo.MarkRead(ctx, chOffice, u, time.Now()); err != nil {
			t.Fatalf("mark read %s: %v", u, err)
		}
	}
	if n, _ := seenBy(eventMsg.ID); n != 2 {
		t.Fatalf("the system line's seen-by is both readers past it, got %d", n)
	}
}

// The offline path parses mentions too: a post through the sync create names
// the same people its REST twin would.
func TestIntegration_SyncCreate_Stores_Mentions(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	s3CleanupPosted(t)

	s3ResetReads(ctx, t, repo, chOffice, uAlexMorgan)
	caseyName := s3DisplayName(ctx, t, uCaseyDoyle)
	id, seq, err := repo.syncMessageCreate(ctx, uAlexMorgan, chOffice, fmt.Sprintf("@%s offline handoff, yard two", caseyName), "", uuid.NewString())
	if err != nil {
		t.Fatalf("sync create: %v", err)
	}
	s3TrackPosted(id)
	if seq == 0 {
		t.Fatal("the sync create's row carries no seq")
	}
	if !s3MentionRow(ctx, t, id, uCaseyDoyle, "") {
		t.Fatal("the offline post stored no mention row for the name it carried")
	}
}
