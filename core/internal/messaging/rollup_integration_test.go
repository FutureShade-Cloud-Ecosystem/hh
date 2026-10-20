//go:build integration

// DIB-449 integration tests. These prove the half of the fix that lives in SQL and CANNOT be proven
// with a double:
//
//	the unique index really does make a day ONE ROW (the double can only imitate it);
//	the in-place update really does leave one row rather than two;
//	the recompute really is a fold over Postgres rows, so redelivery cannot inflate a money total;
//	msg_unread_effective really does fall back to the CHANNEL default, so #payments is quiet for a
//	  user who has no prefs row at all, which is every user, on day one.
//
// Every fixture restores what it touches (DIB-396 discipline) so the package stays green under
// -shuffle=on and in a parallel full-suite run.
//
// Run: cd backend && DATABASE_URL=... go test -tags integration ./internal/messaging/... -v
package messaging

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The 0152 seeded channels.
const (
	chPaymentsTrenton = "a5000000-0000-4000-9000-000000000449"
	chPaymentsGlobal  = "a5000000-0000-4000-9000-000000000451"
)

// testDay is a FIXED day in the past that these fixtures own outright.
//
// It is not a stylistic choice. The rollup is keyed on (channel, day), so a test that posted "today"
// would share a rollup row with whatever else is on the shared demo DB, the seeded data, a live
// server draining the outbox beside the test run, or the previous test file. Owning a day means each
// fixture can assert on EXACTLY the rows it created and delete EXACTLY those rows afterwards, instead
// of truncating a channel and calling it cleanup (which restores nothing and destroys demo data).
var testDay = time.Date(2019, 3, 14, 15, 0, 0, 0, time.UTC)

// rollupCleanup removes only what this fixture creates: its own day's rollup root, the replies under
// it (replies first, thread_root_id pins the root), and any prefs row it wrote.
func rollupCleanup(t *testing.T, pool *pgxpool.Pool, userID string, channelIDs ...string) {
	t.Helper()
	wipe := func() {
		ctx := context.Background()
		key := rollupKey(ClassRoutine, rollupDay(testDay))
		for _, ch := range channelIDs {
			_, _ = pool.Exec(ctx, `
				DELETE FROM msg_messages
				WHERE thread_root_id IN (SELECT id FROM msg_messages WHERE channel_id = $1 AND event_key = $2)`, ch, key)
			_, _ = pool.Exec(ctx, `DELETE FROM msg_messages WHERE channel_id = $1 AND event_key = $2`, ch, key)
			if userID != "" {
				_, _ = pool.Exec(ctx, `DELETE FROM msg_notification_prefs WHERE channel_id = $1 AND user_id = $2`, ch, userID)
			}
		}
	}
	wipe() // start from a known state even if a previous run died mid-test
	t.Cleanup(wipe)
}

// rollupRow reads this fixture's own rollup root + its reply count, ignoring anything else in the
// channel.
func rollupRow(t *testing.T, pool *pgxpool.Pool, channelID string) (id, body string, created time.Time, edited *time.Time, replies int) {
	t.Helper()
	ctx := context.Background()
	key := rollupKey(ClassRoutine, rollupDay(testDay))
	err := pool.QueryRow(ctx, `
		SELECT m.id::text, m.body, m.created_at, m.edited_at,
		       (SELECT count(*) FROM msg_messages c WHERE c.thread_root_id = m.id AND c.deleted_at IS NULL)
		FROM msg_messages m WHERE m.channel_id = $1 AND m.event_key = $2`, channelID, key).
		Scan(&id, &body, &created, &edited, &replies)
	if err != nil {
		t.Fatalf("read the day's rollup row: %v", err)
	}
	return
}

// rollupRoots counts the top-level rollup rows for this fixture's day, the assertion that a day is
// ONE message, scoped so other days in the channel cannot mask a regression or fake a pass.
func rollupRoots(t *testing.T, pool *pgxpool.Pool, channelID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM msg_messages
		WHERE channel_id = $1 AND thread_root_id IS NULL AND event_key = $2`,
		channelID, rollupKey(ClassRoutine, rollupDay(testDay))).Scan(&n); err != nil {
		t.Fatalf("count rollup roots: %v", err)
	}
	return n
}

// ringPayments posts payments through the real service seam, all on the fixture's own day.
func ringPayments(t *testing.T, svc *Service, slug string, amounts map[string]int64) {
	t.Helper()
	ctx := context.Background()
	for key, cents := range amounts {
		body, ref := render(TopicPaymentReceived, eventPayload{AmountText: dollars(cents)})
		ref.Topic = TopicPaymentReceived
		if _, err := svc.PostRoutineEvent(ctx, slug, key, body, ref, cents, testDay); err != nil {
			t.Fatalf("post routine %s: %v", key, err)
		}
	}
}

// dayLine is what a rollup for the fixture's (past) day should read. A past day is NAMED, never called
// "today", the renderer's own rule, asserted here against real rows.
func dayLine(count int, money string) string {
	return "Payments " + rollupDay(testDay) + ": " + itoa(count) + " · " + money
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// THE acceptance, in SQL: three payments leave exactly ONE top-level row in the channel, and three
// replies under it. This is the assertion the (channel_id, event_key) unique index enforces, the
// invariant is held by the schema, not by application discipline a later caller could forget.
func TestIntegration_Rollup_ADayIsOneRow(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton)
	svc := NewService(NewRepository(pool))

	ringPayments(t, svc, "payments-trenton", map[string]int64{
		"itest-p1": 124000, "itest-p2": 388050, "itest-p3": 92075,
	})

	if n := rollupRoots(t, pool, chPaymentsTrenton); n != 1 {
		t.Fatalf("a day must be ONE top-level row, got %d", n)
	}
	_, body, _, _, replies := rollupRow(t, pool, chPaymentsTrenton)
	if replies != 3 {
		t.Fatalf("each payment must survive as a reply, got %d", replies)
	}
	if want := dayLine(3, "$6,041.25"); body != want {
		t.Fatalf("the day's line = %q, want %q", body, want)
	}
}

// The in-place update does not move the message. created_at is the day's position in the stream; if
// the recompute bumped it, the rollup would resurface at the top of the hub on every payment, which
// is the "keeps pestering me" behaviour this ticket removes, rebuilt one layer down.
func TestIntegration_Rollup_UpdateDoesNotResurfaceTheMessage(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton)
	svc := NewService(NewRepository(pool))

	ringPayments(t, svc, "payments-trenton", map[string]int64{"itest-a": 1000})
	_, _, first, _, _ := rollupRow(t, pool, chPaymentsTrenton)

	time.Sleep(20 * time.Millisecond)
	ringPayments(t, svc, "payments-trenton", map[string]int64{"itest-b": 2000})
	_, body, second, edited, _ := rollupRow(t, pool, chPaymentsTrenton)

	if !second.Equal(first) {
		t.Fatalf("the rollup must keep its place in the stream: created_at moved %v -> %v", first, second)
	}
	// A system recompute is not a human edit; stamping edited_at would put "(edited)" on a message
	// nobody edited.
	if edited != nil {
		t.Fatalf("a recompute must not stamp edited_at, got %v", edited)
	}
	if want := dayLine(2, "$30.00"); body != want {
		t.Fatalf("the ONE row must carry the CURRENT total, got %q want %q", body, want)
	}
}

// Redelivery is the outbox's normal behaviour, not an edge case. The total must not drift.
func TestIntegration_Rollup_RedeliveryDoesNotInflateTheTotal(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton)
	svc := NewService(NewRepository(pool))

	for i := 0; i < 3; i++ {
		ringPayments(t, svc, "payments-trenton", map[string]int64{"itest-dup": 500000})
	}
	_, body, _, _, replies := rollupRow(t, pool, chPaymentsTrenton)
	if replies != 1 {
		t.Fatalf("three deliveries of one payment must record ONE reply, got %d", replies)
	}
	if want := dayLine(1, "$5,000.00"); body != want {
		t.Fatalf("redelivery inflated the day: %q want %q", body, want)
	}
}

// - quiet by default, and the subscription that overrides it -

// The channel default reaches a user who has NO prefs row, which is every user until they touch the
// control. This is what makes "#payments defaults quiet for everyone" true rather than aspirational.
func TestIntegration_PaymentsChannelIsQuietByDefault(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton)
	repo := NewRepository(pool)
	svc := NewService(repo)

	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// Make sure there is no explicit row: the point is the ABSENCE of one.
	if _, err := pool.Exec(ctx,
		`DELETE FROM msg_notification_prefs WHERE channel_id = $1 AND user_id = $2`,
		chPaymentsTrenton, uAlexMorgan); err != nil {
		t.Fatalf("clear prefs: %v", err)
	}
	// Read cursor to the epoch so every message below is genuinely unread.
	if _, err := pool.Exec(ctx,
		`UPDATE msg_members SET last_read_at = 'epoch' WHERE channel_id = $1 AND user_id = $2`,
		chPaymentsTrenton, uAlexMorgan); err != nil {
		t.Fatalf("reset cursor: %v", err)
	}
	ringPayments(t, svc, "payments-trenton", map[string]int64{
		"itest-q1": 1000, "itest-q2": 2000, "itest-q3": 3000,
	})

	var mode string
	var unread int64
	if err := pool.QueryRow(ctx,
		`SELECT mode, unread_count FROM msg_unread_effective WHERE channel_id = $1 AND user_id = $2`,
		chPaymentsTrenton, uAlexMorgan).Scan(&mode, &unread); err != nil {
		t.Fatalf("unread: %v", err)
	}
	if mode != ModeMuted || unread != 0 {
		t.Fatalf("#payments must be quiet with no prefs row, got mode=%s unread=%d", mode, unread)
	}

	// And the prefs READ must say so too, rather than claiming "ALL", a control that shows the wrong
	// current state is the UI lying about the user's own settings.
	p, err := repo.GetNotificationPrefs(ctx, chPaymentsTrenton, uAlexMorgan)
	if err != nil {
		t.Fatalf("get prefs: %v", err)
	}
	if p.Mode != ModeMuted || !p.Muted {
		t.Fatalf("the prefs read must report the channel default, got mode=%s muted=%v", p.Mode, p.Muted)
	}
	if !p.Inherited {
		t.Fatalf("a channel-defaulted answer must be marked Inherited so the surface can say so")
	}
}

// The AR clerk's subscription: setting the channel to ALL beats the quiet default and yields a clean
// payment-only stream. An explicit choice ALWAYS wins over the channel default, otherwise "you can
// opt in" would be untrue.
func TestIntegration_PaymentsChannelCanBeSubscribedTo(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton)
	repo := NewRepository(pool)
	svc := NewService(repo)
	bindPaymentTrailFor(t, pool, svc, uAlexMorgan)

	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE msg_members SET last_read_at = 'epoch' WHERE channel_id = $1 AND user_id = $2`,
		chPaymentsTrenton, uAlexMorgan); err != nil {
		t.Fatalf("reset cursor: %v", err)
	}
	all := ModeAll
	if _, err := svc.SetNotifyPrefs(ctx, Caller{UserID: uAlexMorgan}, chPaymentsTrenton, PrefsPatch{Mode: &all}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ringPayments(t, svc, "payments-trenton", map[string]int64{
		"itest-s1": 1000, "itest-s2": 2000,
	})

	var mode string
	var unread int64
	if err := pool.QueryRow(ctx,
		`SELECT mode, unread_count FROM msg_unread_effective WHERE channel_id = $1 AND user_id = $2`,
		chPaymentsTrenton, uAlexMorgan).Scan(&mode, &unread); err != nil {
		t.Fatalf("unread: %v", err)
	}
	if mode != ModeAll {
		t.Fatalf("an explicit ALL must beat the channel default, got %s", mode)
	}
	if unread == 0 {
		t.Fatalf("a subscriber must actually receive the stream, got unread=0")
	}
}

// The escalation tier is still un-mutable through the NEW fallback as well as the old one. A channel
// default can no more silence #escalations than a user pref can, DIB-269, unchanged.
func TestIntegration_ChannelDefaultCannotSilenceEscalations(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	// Restore whatever the channel's default was, whatever this test does to it.
	var was string
	if err := pool.QueryRow(ctx, `SELECT default_mode FROM msg_channels WHERE id = $1`, chEscalations).Scan(&was); err != nil {
		t.Fatalf("read default: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE msg_channels SET default_mode = $2 WHERE id = $1`, chEscalations, was)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE msg_channels SET default_mode = 'MUTED' WHERE id = $1`, chEscalations); err != nil {
		t.Fatalf("force a muted default onto the escalation channel: %v", err)
	}
	if err := NewRepository(pool).SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatalf("sync: %v", err)
	}
	var mode string
	if err := pool.QueryRow(ctx,
		`SELECT mode FROM msg_unread_effective WHERE channel_id = $1 AND user_id = $2`,
		chEscalations, uAlexMorgan).Scan(&mode); err != nil {
		t.Fatalf("unread: %v", err)
	}
	if mode != ModeAll {
		t.Fatalf("an escalation channel must stay ALL even with a MUTED channel default, got %s", mode)
	}
}

// - the class config, in SQL -

// The locked CRITICAL rows cannot be reclassified, and the repository's own `AND locked = FALSE`
// predicate is the second guard, so even a caller that skips the service refusal changes nothing.
func TestIntegration_EventClass_LockedRowsAreImmovable(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	for _, topic := range []string{TopicCreditHold, TopicOrderBlocked} {
		if _, err := repo.SetEventClass(ctx, topic, ClassRoutine, uAlexMorgan); err == nil {
			t.Fatalf("%s: the repository must refuse a locked row even without the service check", topic)
		}
		var class string
		if err := pool.QueryRow(ctx,
			`SELECT class FROM msg_event_classes WHERE event_topic = $1`, topic).Scan(&class); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if class != ClassCritical {
			t.Fatalf("%s must still be CRITICAL, got %s", topic, class)
		}
	}
}

// Payments are seeded ROUTINE and the effective class agrees, the one classification this ticket moves.
func TestIntegration_EventClass_PaymentsAreRoutine(t *testing.T) {
	pool := intPool(t)
	svc := NewService(NewRepository(pool))
	if got := svc.ClassOf(context.Background(), TopicPaymentReceived); got != ClassRoutine {
		t.Fatalf("payments must be ROUTINE, got %s", got)
	}
	if got := svc.ClassOf(context.Background(), TopicCreditHold); got != ClassCritical {
		t.Fatalf("credit hold must stay CRITICAL, got %s", got)
	}
	if got := svc.ClassOf(context.Background(), TopicOrderReleased); got != ClassNotable {
		t.Fatalf("order released must stay NOTABLE, got %s", got)
	}
}
