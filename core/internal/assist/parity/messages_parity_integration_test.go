//go:build integration

package parity

// Per record and per column parity for the message channel metadata (plan r3 section 3.7, slice 7b), against the
// messaging module's own channel list (Service.ListMyChannels for the asker), for planted users who are members of
// some channels and not of others. The channel ids in each claim come from the handler's own path over the
// messaging module's membership read, resolved at request time. No message body and no member reaches the view:
// bodies are planted with secrets and none appears in any cell.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/adapters"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
)

const bodySecret = "BODY-SECRET-DO-NOT-LEAK"

type msgWorld struct {
	tag      string
	users    map[string]string
	channels map[string]string
	a        string
}

func plantMsgWorld(t *testing.T, env *testsupport.Env) *msgWorld {
	t.Helper()
	ctx := testsupport.Ctx(t)
	tag := randomHex(t, 3)
	w := &msgWorld{tag: tag, users: map[string]string{}, channels: map[string]string{}}
	w.a = plantBranch(t, env, tag+"MA", "S7b messages A")
	for _, u := range []string{"u1", "u2", "u3", "u4"} {
		var id string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO users (email, display_name, status) VALUES ($1, $2, 'ACTIVE') RETURNING id::text`,
			fmt.Sprintf("%s.%s@example.test", u, tag), "Member "+u+" "+tag).Scan(&id); err != nil {
			t.Fatalf("plant user: %v", err)
		}
		w.users[u] = id
	}
	ch := func(name, kind string, archived bool, mode, branch string, members ...string) {
		var id string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO msg_channels (kind, name, topic, is_archived, default_mode, branch_id) VALUES ($1, $2, 'topic '||$2, $3, $4, NULLIF($5,'')::uuid) RETURNING id::text`,
			kind, name+" "+tag, archived, mode, branch).Scan(&id); err != nil {
			t.Fatalf("plant channel %s: %v", name, err)
		}
		w.channels[name] = id
		for _, m := range members {
			if _, err := env.Admin.Exec(ctx, `INSERT INTO msg_members (channel_id, user_id, source) VALUES ($1::uuid, $2::uuid, 'EXPLICIT')`, id, w.users[m]); err != nil {
				t.Fatalf("plant member: %v", err)
			}
		}
	}
	ch("c1", "TOPIC", false, "ALL", w.a, "u1", "u2")
	ch("c2", "TOPIC", false, "ALL", "", "u2", "u3")
	ch("c3-dm", "DM", false, "ALL", "", "u1", "u3")
	ch("c4-archived", "TOPIC", true, "ALL", "", "u1")
	ch("c5-muted", "TOPIC", false, "MUTED", "", "u1", "u2")
	ch("c6-others", "TOPIC", false, "ALL", "", "u2")
	ch("c7-mentions", "TOPIC", false, "ALL", "", "u1", "u2")
	msg := func(channel, author, body string, ageMinutes int) {
		if _, err := env.Admin.Exec(ctx, `INSERT INTO msg_messages (channel_id, author_id, kind, body, created_at) VALUES ($1::uuid, $2::uuid, 'TEXT', $3, now() - make_interval(mins => $4))`,
			w.channels[channel], w.users[author], bodySecret+" "+body, ageMinutes); err != nil {
			t.Fatalf("plant message: %v", err)
		}
	}
	msg("c1", "u2", "one", 30)
	msg("c1", "u2", "two", 20)
	msg("c1", "u1", "mine", 10) // authored by u1: never unread for u1
	msg("c2", "u3", "three", 15)
	msg("c3-dm", "u3", "dm one", 5)
	msg("c3-dm", "u3", "dm two", 4)
	msg("c5-muted", "u2", "muted one", 3)
	msg("c4-archived", "u1", "gone", 2)
	msg("c1", "u2", "deleted one", 8) // a tombstoned message is never unread
	if _, err := env.Admin.Exec(ctx, `UPDATE msg_messages SET deleted_at = now() WHERE body LIKE '%deleted one'`); err != nil {
		t.Fatal(err)
	}
	msg("c7-mentions", "u2", "mention one", 6)
	msg("c7-mentions", "u2", "mention two", 5)
	// u1 asks to hear only mentions in c7: the module counts the mentions, which this view cannot read.
	if _, err := env.Admin.Exec(ctx, `INSERT INTO msg_notification_prefs (channel_id, user_id, mode) VALUES ($1::uuid, $2::uuid, 'MENTIONS')`, w.channels["c7-mentions"], w.users["u1"]); err != nil {
		t.Fatal(err)
	}
	// u1 read c1 up to 25 minutes ago, so only the 20 minute message counts as unread for u1.
	if _, err := env.Admin.Exec(ctx, `UPDATE msg_members SET last_read_at = now() - interval '25 minutes' WHERE channel_id = $1::uuid AND user_id = $2::uuid`, w.channels["c1"], w.users["u1"]); err != nil {
		t.Fatal(err)
	}
	dsn := env.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DELETE FROM msg_channels WHERE name LIKE $1`, "% "+tag)
		_, _ = c.Exec(context.Background(), `DELETE FROM users WHERE email LIKE $1`, "%."+tag+"@example.test")
	})
	return w
}

func (w *msgWorld) channelIDs() []string {
	out := []string{}
	for _, id := range w.channels {
		out = append(out, id)
	}
	return out
}

// msgCallers: each planted user as a manager of the planted branch, one global leadership user, and a frontline.
func (w *msgWorld) callers() []staffCallerCase {
	mk := func(name, user string, tier identity.Tier, global bool) staffCallerCase {
		sc := scoped("BRANCH_MANAGER", w.a)
		sc.UserID = w.users[user]
		sc.Tier = tier
		if global {
			sc.Scope = identity.ScopeGlobal
		}
		return staffCallerCase{name: name, sc: sc}
	}
	return []staffCallerCase{
		mk("u1 manager", "u1", identity.TierManager, false),
		mk("u2 manager", "u2", identity.TierManager, false),
		mk("u3 global leadership", "u3", identity.TierLeadership, true),
		mk("u4 manager with no channel", "u4", identity.TierManager, false),
		mk("u1 frontline", "u1", identity.TierFrontline, false),
	}
}

func TestParity_EveryMessageChannelFollowsTheMessagingModulesOwnListForTheAsker(t *testing.T) {
	env := testsupport.Open(t)
	w := plantMsgWorld(t, env)
	ctx := testsupport.Ctx(t)
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	msgSvc := messaging.NewService(messaging.NewRepository(pool))
	h := assist.NewHandler(nil, nil).WithChannelMembership(adapters.NewChannelMembership(msgSvc))
	compared, unreadSeen, mentionsSeen := 0, 0, 0
	for _, c := range w.callers() {
		floor := c.sc.Tier == identity.TierLeadership || c.sc.Tier == identity.TierManager
		// The module's list resyncs derived membership first (the role and branch rule materialises the seats), so
		// it is read before the claim is built: the assistant's membership read is the materialised one and writes
		// nothing, the state every message read's membership gate uses.
		list, err := msgSvc.ListMyChannels(ctx, messaging.Caller{UserID: c.sc.UserID})
		if err != nil {
			t.Fatalf("%s: the channel list: %v", c.name, err)
		}
		_, claims := claimsOf(t, h, c.sc)
		type cells struct {
			kind, name, branch, mode string
			members                  int
			unread                   *int
			latest                   *int64
		}
		got := map[string]cells{}
		readRows(t, env, claims, `SELECT id::text, kind, name, branch_label, notify_mode, member_count, unread_count, floor(extract(epoch from latest_unread_at))::bigint FROM ai.message_channels`, func(rs pgx.Rows) error {
			var id string
			var x cells
			if err := rs.Scan(&id, &x.kind, &x.name, &x.branch, &x.mode, &x.members, &x.unread, &x.latest); err != nil {
				return err
			}
			got[id] = x
			return nil
		})
		want := map[string]messaging.Channel{}
		for _, ch := range list {
			want[ch.ID] = ch
		}
		if !floor {
			if len(got) != 0 {
				t.Errorf("%s: a tier 3 asker reads %d channels through the assistant", c.name, len(got))
			}
			continue
		}
		for id, ch := range want {
			x, ok := got[id]
			if !ok {
				t.Errorf("%s: the channel list returns %q and the view does not", c.name, ch.Name)
				continue
			}
			compared++
			var members int
			if err := env.Admin.QueryRow(ctx, `SELECT count(*) FROM msg_members WHERE channel_id = $1::uuid`, id).Scan(&members); err != nil {
				t.Fatal(err)
			}
			// A MENTIONS channel counts the asker's mentions, which the view cannot read: its count is NULL, not a
			// number that could be wrong; every other mode equals the module's badge.
			if x.mode == "MENTIONS" {
				if x.unread != nil {
					t.Errorf("%s: channel %q is MENTIONS only and the view shows a count %d", c.name, ch.Name, *x.unread)
				}
				mentionsSeen++
			} else if x.unread == nil || *x.unread != ch.UnreadCount {
				t.Errorf("%s: channel %q unread differs: view %v, module %d", c.name, ch.Name, x.unread, ch.UnreadCount)
			}
			if x.kind != ch.Kind || x.name != ch.Name || x.members != members {
				t.Errorf("%s: channel %q differs: view %+v, module %+v (members %d)", c.name, ch.Name, x, ch, members)
			}
			if (x.latest == nil) != (ch.LatestAt == nil) || (x.latest != nil && *x.latest != ch.LatestAt.Unix()) {
				t.Errorf("%s: channel %q latest unread differs: view %v, module %v", c.name, ch.Name, x.latest, ch.LatestAt)
			}
			if ch.UnreadCount > 0 {
				unreadSeen++
			}
		}
		for id, x := range got {
			if _, ok := want[id]; !ok {
				t.Errorf("%s: the view returns %q and the channel list does not", c.name, x.name)
			}
		}
		// The planted askers: each reads exactly their own channels.
		if strings.HasPrefix(c.name, "u1 manager") {
			for _, n := range []string{"c1", "c3-dm", "c5-muted", "c7-mentions"} {
				if _, ok := got[w.channels[n]]; !ok {
					t.Errorf("%s: should read %s", c.name, n)
				}
			}
			for _, n := range []string{"c2", "c4-archived", "c6-others"} {
				if _, ok := got[w.channels[n]]; ok {
					t.Errorf("%s: should not read %s", c.name, n)
				}
			}
			if x := got[w.channels["c7-mentions"]]; x.mode != "MENTIONS" || x.unread != nil {
				t.Errorf("%s: c7 should be MENTIONS with no count: %+v", c.name, x)
			}
			if x := got[w.channels["c1"]]; x.unread == nil || *x.unread != 1 || x.members != 2 {
				t.Errorf("%s: c1 should show 1 unread (the read cursor) and 2 members: %+v", c.name, x)
			}
			if x := got[w.channels["c5-muted"]]; x.unread == nil || *x.unread != 0 || x.mode != "MUTED" {
				t.Errorf("%s: a muted channel shows %+v, want 0 unread", c.name, x)
			}
		}
		if strings.HasPrefix(c.name, "u4") {
			// A user in none of the planted channels still holds the seats the role and branch rule gives every
			// active staff member (derived, materialised by the module's list); none of the planted ones.
			for n, id := range w.channels {
				if _, ok := got[id]; ok {
					t.Errorf("%s: should not read the planted channel %s", c.name, n)
				}
			}
		}
	}
	if compared < 8 || unreadSeen < 3 || mentionsSeen < 1 {
		t.Fatalf("too little was compared: %d channels, %d with unread messages, %d mentions only", compared, unreadSeen, mentionsSeen)
	}
}

// The view carries metadata only: no cell of any channel row contains a message body or a member's name or email.
func TestMessages_NoBodyAndNoMemberReachesTheView(t *testing.T) {
	env := testsupport.Open(t)
	w := plantMsgWorld(t, env)
	ctx := testsupport.Ctx(t)
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	msgSvc := messaging.NewService(messaging.NewRepository(pool))
	h := assist.NewHandler(nil, nil).WithChannelMembership(adapters.NewChannelMembership(msgSvc))
	_, claims := claimsOf(t, h, w.callers()[0].sc)
	rows := 0
	readRows(t, env, claims, `SELECT to_jsonb(v)::text FROM ai.message_channels v`, func(rs pgx.Rows) error {
		var row string
		if err := rs.Scan(&row); err != nil {
			return err
		}
		rows++
		for _, s := range []string{bodySecret, "Member u", "example.test", "mine", "dm one"} {
			if strings.Contains(row, s) {
				t.Errorf("a channel row carries %q: %s", s, row)
			}
		}
		return nil
	})
	if rows < 3 {
		t.Fatalf("the asker read %d channels", rows)
	}
	// No view column and no catalogue column is a body, a member or an author.
	var cols []string
	readRows(t, env, claims, `SELECT column_name FROM information_schema.columns WHERE table_schema = 'ai' AND table_name = 'message_channels'`, func(rs pgx.Rows) error {
		var c string
		if err := rs.Scan(&c); err != nil {
			return err
		}
		cols = append(cols, c)
		return nil
	})
	for _, c := range cols {
		for _, bad := range []string{"body", "message", "author", "user", "email", "member_id", "members"} {
			if strings.Contains(c, bad) && c != "member_count" {
				t.Errorf("ai.message_channels has a column named %s", c)
			}
		}
	}
	// The view owner can read no text of a message: not the body, not the event reference, not the event key.
	for _, col := range []string{"body", "ref", "event_key", "kind"} {
		var can bool
		if err := env.Admin.QueryRow(ctx, `SELECT has_column_privilege($1, 'public.msg_messages', $2, 'SELECT')`, env.Roles.ViewOwner, col).Scan(&can); err != nil {
			t.Fatal(err)
		}
		if can {
			t.Errorf("the view owner can read msg_messages.%s", col)
		}
	}
	_ = readtx.AudienceStaff
}

// Membership is resolved at each request, not remembered: a user taken out of a channel reads it in no later
// answer, a user put into one reads it at once, and a claim signed before the change lists no channel the user has since left.
func TestMessages_MembershipIsResolvedLiveAtEachRequest(t *testing.T) {
	env := testsupport.Open(t)
	w := plantMsgWorld(t, env)
	ctx := testsupport.Ctx(t)
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	h := assist.NewHandler(nil, nil).WithChannelMembership(adapters.NewChannelMembership(messaging.NewService(messaging.NewRepository(pool))))
	u1 := w.callers()[0].sc
	has := func(claims readtx.Claims, name string) bool {
		found := false
		readRows(t, env, claims, `SELECT id::text FROM ai.message_channels`, func(rs pgx.Rows) error {
			var id string
			if err := rs.Scan(&id); err != nil {
				return err
			}
			found = found || id == w.channels[name]
			return nil
		})
		return found
	}
	_, before := claimsOf(t, h, u1)
	if !has(before, "c1") || has(before, "c2") {
		t.Fatalf("u1 should read c1 and not c2 to begin with")
	}
	if _, err := env.Admin.Exec(ctx, `DELETE FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid`, w.channels["c1"], w.users["u1"]); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Admin.Exec(ctx, `INSERT INTO msg_members (channel_id, user_id, source) VALUES ($1::uuid, $2::uuid, 'EXPLICIT')`, w.channels["c2"], w.users["u1"]); err != nil {
		t.Fatal(err)
	}
	_, after := claimsOf(t, h, u1)
	if has(after, "c1") || !has(after, "c2") {
		t.Errorf("after the change u1 should read c2 and not c1")
	}
	// The earlier claim still names c1, but the view requires the asker's own member row as well (PR 581 read P3 2),
	// so a claim signed before the change lists no channel the user has since left.
	if has(before, "c1") {
		t.Errorf("a claim signed before the user left c1 still lists it")
	}
}

// A stale or mistaken claim cannot list a channel (PR 581 read P3 2, FROM-PRINCIPAL item 52). The signed list is
// server made, so this is not reachable today, but the view requires the asker's own member row as well: a claim
// that names channels the asker is not in lists none of them, and still lists the ones they are in.
func TestMessages_AClaimNamingChannelsTheAskerIsNotInListsNoneOfThem(t *testing.T) {
	env := testsupport.Open(t)
	w := plantMsgWorld(t, env)
	ctx := testsupport.Ctx(t)
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	msgSvc := messaging.NewService(messaging.NewRepository(pool))
	h := assist.NewHandler(nil, nil).WithChannelMembership(adapters.NewChannelMembership(msgSvc))
	u1 := w.callers()[0].sc
	if _, err := msgSvc.ListMyChannels(ctx, messaging.Caller{UserID: u1.UserID}); err != nil {
		t.Fatal(err)
	}
	_, claims := claimsOf(t, h, u1)
	// u1 is not in c2 (u2 and u3), c6-others (u2) or the archived room's neighbours; name them anyway.
	stale := claims
	stale.ChannelIDs = append(append([]string(nil), claims.ChannelIDs...), w.channels["c2"], w.channels["c6-others"])
	listed := map[string]bool{}
	readRows(t, env, stale, `SELECT id::text FROM ai.message_channels`, func(rs pgx.Rows) error {
		var id string
		if err := rs.Scan(&id); err != nil {
			return err
		}
		listed[id] = true
		return nil
	})
	for _, n := range []string{"c2", "c6-others"} {
		if listed[w.channels[n]] {
			t.Errorf("a claim naming %s lists it for a user who is not in it", n)
		}
	}
	for _, n := range []string{"c1", "c3-dm", "c5-muted", "c7-mentions"} {
		if !listed[w.channels[n]] {
			t.Errorf("a claim naming %s no longer lists it for a user who is in it", n)
		}
	}
	// The base tables are not narrowed by this (they hold the signed list alone); the view is.
	_ = readtx.AudienceStaff
}
