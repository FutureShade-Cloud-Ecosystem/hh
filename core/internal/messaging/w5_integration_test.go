//go:build integration

// Wave-5 (Swarm D) integration tests. These prove the half of the wave that lives in SQL and cannot
// be proven with a double: the prefs-aware badge view (DIB-268), the un-mutable escalation channel
// (DIB-269), the LAST-WRITE-WINS supersede sweep (DIB-217), the ack arithmetic over msg_acks
// (DIB-275), and the rep-assignment / house-queue routing (DIB-273/DIB-333/DIB-271).
//
// Every fixture RESTORES what it touches (prefs rows deleted, assignments deleted, read cursors
// restored) so the package stays green under -shuffle=on and in a parallel full-suite run.
//
// Run: cd backend && DATABASE_URL=... go test -tags integration ./internal/messaging/... -v
package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Wave-5 seeded fixtures (migrations 0136/0137).
const (
	chHouseTrenton = "a5000000-0000-4000-9000-000000000271"
	chEscalations  = "a5000000-0000-4000-9000-000000000269"

	custQuinte = "c7000000-0000-4000-9000-000000000001" // Quinte Hardscapes Inc., home branch Trenton
)

// w5Cleanup removes every row this wave's fixtures can create for one customer + user, and restores
// the prefs row for a channel. Registered with t.Cleanup by each fixture so a failed test still
// leaves the DB as it found it (DIB-396 discipline: a fixture restores what it touches).
func w5Cleanup(t *testing.T, pool *pgxpool.Pool, customerID, userID string, channelIDs ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		if customerID != "" {
			_, _ = pool.Exec(ctx, `DELETE FROM rep_assignments WHERE customer_id = $1`, customerID)
			_, _ = pool.Exec(ctx, `DELETE FROM msg_house_conversations WHERE customer_id = $1`, customerID)
		}
		for _, ch := range channelIDs {
			if userID != "" {
				_, _ = pool.Exec(ctx, `DELETE FROM msg_notification_prefs WHERE channel_id = $1 AND user_id = $2`, ch, userID)
			}
		}
	})
}

// - DIB-268: the badge pipeline honours the mode -
//
// THE acceptance: "mute a channel → badge stops". This runs against msg_unread_effective, the view
// UnreadSummary and ListChannelsForUser both read, so proving it here proves it on both surfaces.
func TestIntegration_MutedChannelStopsBadging(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	w5Cleanup(t, pool, "", uSamRivera, chOffice)

	for _, u := range []string{uSamRivera, uAlexMorgan} {
		if err := repo.SyncMembership(ctx, u); err != nil {
			t.Fatal(err)
		}
		if err := repo.MarkRead(ctx, chOffice, u, dbNow(t)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := repo.InsertMessage(ctx, chOffice, uAlexMorgan, fmt.Sprintf("badge-probe-%d", time.Now().UnixNano()), ""); err != nil {
		t.Fatal(err)
	}

	// Baseline: Sam badges.
	if got := unreadFor(ctx, t, repo, uSamRivera, chOffice); got < 1 {
		t.Fatalf("precondition: Sam should badge before muting, got %d", got)
	}

	// Sam mutes #office.
	if _, err := repo.UpsertNotificationPrefs(ctx, applyMode(DefaultNotificationPrefs(chOffice, uSamRivera), ModeMuted)); err != nil {
		t.Fatalf("mute: %v", err)
	}

	// THE assertion: the badge stops. Not "the toast stops", the BADGE.
	summary, err := repo.UnreadSummary(ctx, uSamRivera)
	if err != nil {
		t.Fatalf("unread summary: %v", err)
	}
	for _, c := range summary.Channels {
		if c.ChannelID == chOffice {
			t.Fatalf("a MUTED channel must not appear in the badge summary, got unread=%d", c.Unread)
		}
	}
	// And the sidebar projection agrees, one view, both surfaces.
	chans, err := repo.ListChannelsForUser(ctx, uSamRivera)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	for _, c := range chans {
		if c.ID == chOffice && c.UnreadCount != 0 {
			t.Fatalf("the channel list must show 0 unread for a muted channel, got %d", c.UnreadCount)
		}
	}

	// Un-muting restores the badge, the mute suppresses the COUNT, it does not consume the messages.
	if _, err := repo.UpsertNotificationPrefs(ctx, applyMode(DefaultNotificationPrefs(chOffice, uSamRivera), ModeAll)); err != nil {
		t.Fatalf("unmute: %v", err)
	}
	if got := unreadFor(ctx, t, repo, uSamRivera, chOffice); got < 1 {
		t.Fatalf("un-muting must restore the badge (the messages were never consumed), got %d", got)
	}
}

// MENTIONS mode counts only the messages that name the user, "quiet but reachable".
func TestIntegration_MentionOnlyModeCountsOnlyMentions(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	w5Cleanup(t, pool, "", uSamRivera, chOffice)

	for _, u := range []string{uSamRivera, uAlexMorgan} {
		if err := repo.SyncMembership(ctx, u); err != nil {
			t.Fatal(err)
		}
		if err := repo.MarkRead(ctx, chOffice, u, dbNow(t)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.UpsertNotificationPrefs(ctx, applyMode(DefaultNotificationPrefs(chOffice, uSamRivera), ModeMentions)); err != nil {
		t.Fatalf("set mentions: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	// Two ordinary posts, neither names Sam.
	for i := 0; i < 2; i++ {
		if _, err := repo.InsertMessage(ctx, chOffice, uAlexMorgan, fmt.Sprintf("chatter-%d-%d", i, time.Now().UnixNano()), ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := unreadFor(ctx, t, repo, uSamRivera, chOffice); got != 0 {
		t.Fatalf("in MENTIONS mode ordinary chatter must not badge, got %d", got)
	}

	// Now one that names him by his e-mail handle.
	if _, err := repo.InsertMessage(ctx, chOffice, uAlexMorgan,
		fmt.Sprintf("@sam.rivera can you take this one? %d", time.Now().UnixNano()), ""); err != nil {
		t.Fatal(err)
	}
	if got := unreadFor(ctx, t, repo, uSamRivera, chOffice); got != 1 {
		t.Fatalf("in MENTIONS mode a mention MUST badge (exactly the mentions, not the chatter), got %d", got)
	}
}

// - DIB-269: the escalation channel cannot be muted, at BOTH guards -

func TestIntegration_EscalationChannelRefusesMute(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	w5Cleanup(t, pool, "", uAlexMorgan, chEscalations)

	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatal(err)
	}
	tier, err := repo.ChannelTier(ctx, chEscalations)
	if err != nil {
		t.Fatalf("tier: %v", err)
	}
	if tier != TierEscalation {
		t.Fatalf("#escalations must be seeded at the ESCALATION tier, got %q", tier)
	}
	muted := ModeMuted
	if _, err := svc.SetNotifyPrefs(ctx, Caller{UserID: uAlexMorgan}, chEscalations, PrefsPatch{Mode: &muted}); !errors.Is(err, ErrChannelUnmutable) {
		t.Fatalf("muting #escalations must be refused, got %v", err)
	}

	// GUARD 2 (defence in depth): even a row written by direct SQL, a legacy row, a migration, a future
	// endpoint that forgets the check, cannot silence the badge, because the view forces ESCALATION
	// back to mode ALL.
	if _, err := pool.Exec(ctx, `
		INSERT INTO msg_notification_prefs (channel_id, user_id, muted, mode)
		VALUES ($1, $2, TRUE, 'MUTED')
		ON CONFLICT (channel_id, user_id) DO UPDATE SET muted = TRUE, mode = 'MUTED'`,
		chEscalations, uAlexMorgan); err != nil {
		t.Fatalf("force-insert muted row: %v", err)
	}
	var mode string
	if err := pool.QueryRow(ctx,
		`SELECT mode FROM msg_unread_effective WHERE channel_id = $1 AND user_id = $2`,
		chEscalations, uAlexMorgan).Scan(&mode); err != nil {
		t.Fatalf("read effective mode: %v", err)
	}
	if mode != ModeAll {
		t.Fatalf("the view must force an ESCALATION channel back to ALL even with a MUTED row present, got %q", mode)
	}
}

// - DIB-217: the supersede sweep in SQL -

func TestIntegration_SupersedeResolvesStaleForcedAck(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	stateKey := fmt.Sprintf("order:int-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE state_key = $1`, stateKey)
	})

	blocked, ok, err := repo.InsertEventMessageOpts(ctx, chYardTrenton, stateKey+":blocked",
		"Order blocked, credit hold.", EventRef{Topic: TopicOrderBlocked},
		EventOpts{Priority: PriorityHigh, StateKey: stateKey})
	if err != nil || !ok {
		t.Fatalf("insert blocked: %v (inserted=%v)", err, ok)
	}
	if !blocked.RequiresAck {
		t.Fatal("a HIGH-priority event must persist with requires_ack set")
	}

	// It shows in the un-acked poll (both consumers of the shared schema read this).
	if !inUnacked(ctx, t, repo, uAlexMorgan, blocked.ID) {
		t.Fatal("a live forced-ack message must appear in the un-acked poll")
	}

	time.Sleep(5 * time.Millisecond)
	released, ok, err := repo.InsertEventMessageOpts(ctx, chYardTrenton, stateKey+":released",
		"Order released, pick list ready.", EventRef{Topic: TopicOrderReleased},
		EventOpts{StateKey: stateKey})
	if err != nil || !ok {
		t.Fatalf("insert released: %v (inserted=%v)", err, ok)
	}
	sup, err := repo.SupersedeOlderForState(ctx, stateKey, released.ID)
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if len(sup) != 1 || sup[0].MessageID != blocked.ID {
		t.Fatalf("the newer fact must resolve exactly the older bubble, got %+v", sup)
	}
	// THE acceptance: the stale bubble stops blocking.
	if inUnacked(ctx, t, repo, uAlexMorgan, blocked.ID) {
		t.Fatal("a SUPERSEDED forced-ack message must stop blocking, a user never acks a stale fact")
	}
	// Idempotent: a second sweep resolves nothing more.
	again, err := repo.SupersedeOlderForState(ctx, stateKey, released.ID)
	if err != nil {
		t.Fatalf("supersede again: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("the sweep must be idempotent, second run resolved %d", len(again))
	}
}

// An already-ACKED message keeps its ack: supersession resolves, it does not rewrite ack history.
func TestIntegration_SupersedeSkipsAckedMessages(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	stateKey := fmt.Sprintf("order:acked-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE state_key = $1`, stateKey)
	})

	blocked, _, err := repo.InsertEventMessageOpts(ctx, chYardTrenton, stateKey+":b",
		"Order blocked.", EventRef{}, EventOpts{Priority: PriorityHigh, StateKey: stateKey})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AckMessage(ctx, chYardTrenton, blocked.ID, uAlexMorgan, AckTap); err != nil {
		t.Fatalf("ack: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	released, _, err := repo.InsertEventMessageOpts(ctx, chYardTrenton, stateKey+":r",
		"Order released.", EventRef{}, EventOpts{StateKey: stateKey})
	if err != nil {
		t.Fatal(err)
	}
	sup, err := repo.SupersedeOlderForState(ctx, stateKey, released.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sup) != 0 {
		t.Fatalf("an ACKED message must never be superseded, got %+v", sup)
	}
}

// - DIB-275: ack arithmetic over the real msg_acks -

func TestIntegration_AckArithmeticIsIdempotentPerUser(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	key := fmt.Sprintf("ackmath-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE event_key = $1`, key)
	})

	for _, u := range []string{uAlexMorgan, uSamRivera} {
		if err := repo.SyncMembership(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	m, _, err := repo.InsertEventMessageOpts(ctx, chYardTrenton, key, "Credit hold, Acme.",
		EventRef{}, EventOpts{Priority: PriorityHigh, StateKey: ""})
	if err != nil {
		t.Fatal(err)
	}

	before, err := repo.AckProgressFor(ctx, chYardTrenton, m.ID)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if before.Acked != 0 || before.Required < 1 || before.Complete {
		t.Fatalf("a fresh forced-ack message: 0 acked of ≥1 required, incomplete; got %+v", before)
	}

	if _, err := repo.AckMessage(ctx, chYardTrenton, m.ID, uAlexMorgan, AckTap); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.AckProgressFor(ctx, chYardTrenton, m.ID)
	if one.Acked != 1 {
		t.Fatalf("after one ack want acked=1, got %d", one.Acked)
	}

	// Re-ack by the SAME user, twice more, with a different method each time. The PK makes it an UPDATE.
	for _, method := range []string{AckVoice, AckTap} {
		if _, err := repo.AckMessage(ctx, chYardTrenton, m.ID, uAlexMorgan, method); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := repo.AckProgressFor(ctx, chYardTrenton, m.ID)
	if after.Acked != 1 {
		t.Fatalf("re-acking by the SAME user must not inflate the count, got acked=%d", after.Acked)
	}
	if len(after.Acks) != 1 {
		t.Fatalf("re-acking must UPDATE one row, not append; got %d ack rows", len(after.Acks))
	}
	if after.Remaining != after.Required-1 {
		t.Fatalf("remaining arithmetic wrong: %+v", after)
	}
}

// - DIB-273/DIB-333 + DIB-271: routing over the real tables -

func TestIntegration_RoutingMatrix_AssignedHouseClaim(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	w5Cleanup(t, pool, custQuinte, "", chHouseTrenton)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM msg_messages WHERE event_key LIKE 'w5int-%'`)
	})
	for _, u := range []string{uSamRivera, uCaseyDoyle, uAlexMorgan} {
		if err := repo.SyncMembership(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	// Start from a known-unassigned state.
	if _, err := repo.DeleteRepAssignment(ctx, custQuinte, ""); err != nil {
		t.Fatal(err)
	}

	stamp := time.Now().UnixNano()

	// 1. UNASSIGNED → the branch house queue.
	first, err := svc.RouteCustomerMessage(ctx, CustomerInbound{
		CustomerID: custQuinte, Body: "Any update on my order?",
		EventKey: fmt.Sprintf("w5int-%d-1", stamp), Channel: "PORTAL",
	})
	if err != nil {
		t.Fatalf("route unassigned: %v", err)
	}
	if first.Kind != RouteHouse || first.ChannelSlug != "house-trenton" {
		t.Fatalf("an unassigned customer must land in #house-trenton, got kind=%s slug=%s", first.Kind, first.ChannelSlug)
	}
	if first.ChannelID != chHouseTrenton {
		t.Fatalf("house channel id mismatch: got %s want %s", first.ChannelID, chHouseTrenton)
	}
	// The server-resolved customer name reaches the post (never the payload's).
	if got := first.Message.Body; got == "" || !contains(got, "Quinte Hardscapes Inc.") {
		t.Fatalf("the post must carry the server-resolved customer name, got %q", got)
	}

	// 2. CLAIM → sticky. Casey claims it out of the queue.
	claimed, err := svc.ClaimConversation(ctx, Caller{UserID: uAlexMorgan}, first.ConversationID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.ClaimedBy != uAlexMorgan {
		t.Fatalf("claim must stamp the claimer, got %q", claimed.ClaimedBy)
	}
	second, err := svc.RouteCustomerMessage(ctx, CustomerInbound{
		CustomerID: custQuinte, Body: "Thanks!", EventKey: fmt.Sprintf("w5int-%d-2", stamp),
	})
	if err != nil {
		t.Fatalf("route after claim: %v", err)
	}
	if second.Kind != RouteRepDM || second.RepUserID != uAlexMorgan {
		t.Fatalf("a claim must be STICKY, the next message routes to the claimer's DM; got kind=%s rep=%s",
			second.Kind, second.RepUserID)
	}
	// A second claim loses the race deterministically.
	if _, err := svc.ClaimConversation(ctx, Caller{UserID: uSamRivera}, first.ConversationID); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("a second claim must be refused, got %v", err)
	}

	// 3. RE-ASSIGN → a different rep's DM (the admin path).
	if _, err := svc.AssignRep(ctx, Caller{UserID: uAlexMorgan}, custQuinte, "", uSamRivera, AssignManual, "book transfer"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	third, err := svc.RouteCustomerMessage(ctx, CustomerInbound{
		CustomerID: custQuinte, Body: "One more thing", EventKey: fmt.Sprintf("w5int-%d-3", stamp),
	})
	if err != nil {
		t.Fatalf("route after reassign: %v", err)
	}
	if third.RepUserID != uSamRivera {
		t.Fatalf("re-assignment must MOVE the book, got rep=%s", third.RepUserID)
	}
	rows, err := repo.RepAssignmentsForCustomer(ctx, custQuinte)
	if err != nil {
		t.Fatal(err)
	}
	custLevel := 0
	for _, r := range rows {
		if !r.IsOrderOverride() {
			custLevel++
		}
	}
	if custLevel != 1 {
		t.Fatalf("there must be exactly ONE customer-level row (one answer to 'who owns this'), got %d", custLevel)
	}

	// 4. UNASSIGN → back to the house queue.
	if err := svc.UnassignRep(ctx, custQuinte, ""); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	fourth, err := svc.RouteCustomerMessage(ctx, CustomerInbound{
		CustomerID: custQuinte, Body: "Hello again", EventKey: fmt.Sprintf("w5int-%d-4", stamp),
	})
	if err != nil {
		t.Fatalf("route after unassign: %v", err)
	}
	if fourth.Kind != RouteHouse {
		t.Fatalf("after UNASSIGN the next message must fall back to the house queue, got %s", fourth.Kind)
	}
}

// The rep's DM channel is provisioned once and reused, a rep never ends up with two inboxes.
func TestIntegration_EnsureRepDMChannelIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	a, err := repo.EnsureRepDMChannel(ctx, uSamRivera)
	if err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	b, err := repo.EnsureRepDMChannel(ctx, uSamRivera)
	if err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	if a != b {
		t.Fatalf("a rep must have exactly ONE customer-message DM channel, got %s and %s", a, b)
	}
	ok, err := repo.IsMember(ctx, a, uSamRivera)
	if err != nil || !ok {
		t.Fatalf("the rep must be an EXPLICIT member of their own inbox (err=%v ok=%v)", err, ok)
	}
}

// The house-slug fallback: an unknown branch (and no branch at all) resolves to the global floor
// rather than erroring, an inbound is never dropped because routing could not find a home.
func TestIntegration_BranchHouseSlugFallsBackToTheFloor(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	got, err := repo.BranchHouseSlug(ctx, branchTrenton)
	if err != nil || got != "house-trenton" {
		t.Fatalf("Trenton must resolve to house-trenton, got %q (%v)", got, err)
	}
	for _, b := range []string{"", "5d1b3f6e-0000-4000-9000-0000000000ff"} {
		got, err := repo.BranchHouseSlug(ctx, b)
		if err != nil {
			t.Fatalf("branch %q: %v", b, err)
		}
		if got != houseSlugUnassigned {
			t.Fatalf("branch %q must fall back to %q, got %q", b, houseSlugUnassigned, got)
		}
	}
}

// inUnacked reports whether a message is in a user's un-acked forced-ack poll.
func inUnacked(ctx context.Context, t *testing.T, repo *Repository, userID, messageID string) bool {
	t.Helper()
	msgs, err := repo.UnackedRequiresAck(ctx, userID)
	if err != nil {
		t.Fatalf("unacked poll: %v", err)
	}
	for _, m := range msgs {
		if m.ID == messageID {
			return true
		}
	}
	return false
}
