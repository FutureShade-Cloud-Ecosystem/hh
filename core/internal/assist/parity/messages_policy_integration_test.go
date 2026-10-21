//go:build integration

package parity

// The message metadata row policies tested directly: the view owner reads the four base tables under signed
// claims that carry a channel list. A row of any of them is visible only for a staff claim at tier 1 or 2 whose
// channel_ids hold its channel; the channel table also leaves archived rooms out. Each predicate is pinned on its
// own table.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func chanClaim(ids ...string) readtx.Claims {
	c := staffClaim(nil, true)
	c.ChannelIDs = ids
	return c
}

func TestPolicy_MessageMetadataRowsNeedAStaffTierOneOrTwoClaimThatHoldsTheChannel(t *testing.T) {
	env := testsupport.Open(t)
	w := plantMsgWorld(t, env)
	all := w.channelIDs()
	quoted := make([]string, len(all))
	for i, id := range all {
		quoted[i] = "'" + id + "'"
	}
	in := "(" + strings.Join(quoted, ",") + ")"
	// A table, the filter that restricts it to the planted world, and the planted rows it holds per channel.
	type tbl struct {
		name, fq, scope, byChannel string
	}
	tables := []tbl{
		{"msg_channels", "public.msg_channels", "id IN " + in, "id"},
		{"msg_members", "public.msg_members", "channel_id IN " + in, "channel_id"},
		{"msg_messages", "public.msg_messages", "channel_id IN " + in, "channel_id"},
		{"msg_notification_prefs", "public.msg_notification_prefs", "channel_id IN " + in, "channel_id"},
	}
	c1, c2, archived := w.channels["c1"], w.channels["c2"], w.channels["c4-archived"]
	for _, tb := range tables {
		count := func(label string, c readtx.Claims) int {
			t.Helper()
			return countAsViewOwner(t, env, c, tb.fq+" WHERE "+tb.scope)
		}
		rowsOf := func(ids ...string) int {
			q := make([]string, len(ids))
			for i, id := range ids {
				q[i] = "'" + id + "'"
			}
			extra := ""
			if tb.name == "msg_channels" {
				extra = " AND NOT is_archived"
			}
			return countWhere(t, env, fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s IN (%s)%s`, tb.name, tb.byChannel, strings.Join(q, ","), extra))
		}
		if tb.name != "msg_notification_prefs" {
			if got, want := count("c1", chanClaim(c1)), rowsOf(c1); got != want || want == 0 {
				t.Errorf("%s: a claim holding c1 reads %d rows, want %d (c1 has rows)", tb.name, got, want)
			}
			if got, want := count("c1c2", chanClaim(c1, c2)), rowsOf(c1, c2); got != want {
				t.Errorf("%s: a claim holding c1 and c2 reads %d rows, want %d", tb.name, got, want)
			}
			if got := count("all", chanClaim(all...)); got != rowsOf(all...) {
				t.Errorf("%s: a claim holding every channel reads %d rows, want %d (positive control)", tb.name, got, rowsOf(all...))
			}
		} else {
			if got, want := count("c7", chanClaim(w.channels["c7-mentions"])), rowsOf(w.channels["c7-mentions"]); got != want || want == 0 {
				t.Errorf("%s: a claim holding c7 reads %d rows, want %d", tb.name, got, want)
			}
			if got := count("c1", chanClaim(c1)); got != 0 {
				t.Errorf("%s: a claim holding c1 reads %d rows, want 0 (the preference is on c7)", tb.name, got)
			}
		}
		if got := count("none", chanClaim()); got != 0 {
			t.Errorf("%s: a claim holding no channel reads %d rows, want 0", tb.name, got)
		}
		if got := count("another", chanClaim("99999999-9999-9999-9999-999999999999")); got != 0 {
			t.Errorf("%s: a claim holding another channel reads %d rows, want 0", tb.name, got)
		}
		pro := chanClaim(all...)
		pro.Audience = readtx.AudiencePro
		pro.CustomerID = "33333333-3333-3333-3333-333333333333"
		if got := count("pro", pro); got != 0 {
			t.Errorf("%s: a pro audience claim reads %d rows, want 0", tb.name, got)
		}
		if got := countAsViewOwnerUnclaimed(t, env, tb.fq+" WHERE "+tb.scope); got != 0 {
			t.Errorf("%s: no claim reads %d rows, want 0", tb.name, got)
		}
		for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
			c := chanClaim(all...)
			c.Tier = tier
			if got := count("tier", c); (got > 0) != want {
				t.Errorf("%s: a tier %d claim reads %d rows, readable want %v", tb.name, tier, got, want)
			}
		}
	}
	// An archived room is left out by the channel table even when the claim holds it, and not by the others
	// (the view reaches them through the channel).
	if got := countAsViewOwner(t, env, chanClaim(archived), "public.msg_channels WHERE id = '"+archived+"'"); got != 0 {
		t.Errorf("a claim holding an archived channel reads %d channel rows, want 0", got)
	}
	if got := countAsViewOwner(t, env, chanClaim(archived), "public.msg_messages WHERE channel_id = '"+archived+"'"); got == 0 {
		t.Errorf("the message table's policy is the channel list alone, and reads %d rows of the archived room", got)
	}
}
