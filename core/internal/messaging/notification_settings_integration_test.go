//go:build integration

// Slice S7 (0401) integration tests. Proves the half of the feature that lives in SQL and CANNOT be
// proven with a double:
//   - the closed 6-item category set is what the SQL CHECK admits, so a bad category hits the
//     CHECK rather than a Go-side refusal;
//   - absent rows return the per-category defaults the brief pins;
//   - a partial PUT preserves untouched categories (the merge is on the per-category row, not the
//     closed set as a whole);
//   - the quiet-hours upsert round-trips the start_local / end_local / timezone / bypass_user_ids
//     fields and respects the CHECKs;
//   - caller isolation: alice's PUT does not mutate bob's stored row.
//
// Run: DATABASE_URL=... go test -tags integration ./internal/messaging/... -v
package messaging

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// clearPersonPrefs removes the rows a fixture wrote; the table ships empty (0401) and is meant to
// stay empty on user "no row", so the cleanup deletes whatever the fixture inserted.
func clearPersonPrefs(t *testing.T, pool *pgxpool.Pool, userIDs ...string) {
	t.Helper()
	wipe := func() {
		for _, u := range userIDs {
			_, _ = pool.Exec(context.Background(), `DELETE FROM msg_person_event_prefs WHERE user_id = $1::uuid`, u)
			_, _ = pool.Exec(context.Background(), `DELETE FROM msg_quiet_hours WHERE user_id = $1::uuid`, u)
		}
	}
	wipe()
	t.Cleanup(wipe)
}

// The closed 6-item category set is what the GET renders. The CHECK is the schema's copy of the
// code's EventCategories, so the SQL refuses a foreign value (a separate unit test pins the
// 400-side refusal; this test pins the schema-level refusal — defence in depth).
func TestIntegration_NotificationSettings_CategorySetMatchesCode(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT event_category FROM msg_person_event_prefs
		WHERE user_id = $1::uuid
		GROUP BY event_category`, uSamRivera)
	if err != nil {
		t.Fatalf("query distinct categories: %v", err)
	}
	defer rows.Close()
	var seen []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen = append(seen, c)
	}
	if len(seen) > len(EventCategories) {
		t.Fatalf("the DB has more categories than the closed set: %v vs %v", seen, EventCategories)
	}
	// Try to insert a foreign category: the CHECK must reject.
	_, err = pool.Exec(ctx, `
		INSERT INTO msg_person_event_prefs (user_id, event_category, in_app, push)
		VALUES ($1::uuid, 'definitely_not_a_category', TRUE, TRUE)`, uSamRivera)
	if err == nil {
		t.Fatalf("CHECK should reject a foreign category, got nil error")
	}
	clearPersonPrefs(t, pool, uSamRivera)
}

// Absent rows return the per-category defaults via GetNotificationSettings. The default for
// payments is in_app=false push=false; every other category is in_app=true push=true except
// stock_received and deliveries_scheduled (in_app=true push=false). Pinned from the brief's list.
func TestIntegration_NotificationSettings_DefaultsForFreshUser(t *testing.T) {
	pool := intPool(t)
	clearPersonPrefs(t, pool, uSamRivera)
	repo := NewRepository(pool)
	svc := NewService(repo)

	got, err := svc.GetNotificationSettings(context.Background(), Caller{UserID: uSamRivera})
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if len(got.EventPrefs) != len(EventCategories) {
		t.Fatalf("want %d, got %d", len(EventCategories), len(got.EventPrefs))
	}
	wantPush := map[string]bool{
		EventCategoryOrdersReleasedAndBlocked: true,
		EventCategoryStockReceived:            false,
		EventCategoryCreditHoldsAndCreditOwed: true,
		EventCategoryDeliveriesScheduled:      false,
		EventCategoryPayments:                 false,
		EventCategoryApprovalsWaitingOnMe:     true,
	}
	wantInApp := map[string]bool{
		EventCategoryOrdersReleasedAndBlocked: true,
		EventCategoryStockReceived:            true,
		EventCategoryCreditHoldsAndCreditOwed: true,
		EventCategoryDeliveriesScheduled:      true,
		EventCategoryPayments:                 false,
		EventCategoryApprovalsWaitingOnMe:     true,
	}
	for _, p := range got.EventPrefs {
		if p.InApp != wantInApp[p.EventCategory] {
			t.Fatalf("%s in_app want %v, got %v", p.EventCategory, wantInApp[p.EventCategory], p.InApp)
		}
		if p.Push != wantPush[p.EventCategory] {
			t.Fatalf("%s push want %v, got %v", p.EventCategory, wantPush[p.EventCategory], p.Push)
		}
	}
	if got.QuietHours.Enabled || got.QuietHours.StartLocal != "18:00" || got.QuietHours.EndLocal != "06:00" || got.QuietHours.Timezone != "America/Los_Angeles" {
		t.Fatalf("default quiet hours unexpected: %+v", got.QuietHours)
	}
}

// A partial PUT that touches one category preserves the OTHER categories' defaults through the
// next GET. The merge is on the per-category row; an omitted field keeps its current value.
func TestIntegration_NotificationSettings_PartialPutPreservesOtherCategories(t *testing.T) {
	pool := intPool(t)
	clearPersonPrefs(t, pool, uSamRivera)
	repo := NewRepository(pool)
	svc := NewService(repo)

	push := false
	if _, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uSamRivera}, NotificationSettingsPatch{
		EventPrefs: []PersonEventPrefsPatch{{Category: EventCategoryStockReceived, Push: &push}},
	}); err != nil {
		t.Fatalf("partial put: %v", err)
	}
	got, err := svc.GetNotificationSettings(context.Background(), Caller{UserID: uSamRivera})
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	for _, p := range got.EventPrefs {
		switch p.EventCategory {
		case EventCategoryStockReceived:
			if p.Push {
				t.Fatalf("stock_received push want false, got true")
			}
		case EventCategoryOrdersReleasedAndBlocked:
			if !p.InApp || !p.Push {
				t.Fatalf("orders_released_and_blocked default should be in_app=true push=true, got %+v", p)
			}
		}
	}
}

// Quiet-hours round-trip writes the start_local / end_local / timezone / bypass_user_ids fields
// and respects the CHECKs. The HH:MM CHECK rejects a malformed string; the service refuses it at
// the boundary first, but this test pins the SQL defence-in-depth.
func TestIntegration_NotificationSettings_QuietHoursRoundTrip(t *testing.T) {
	pool := intPool(t)
	clearPersonPrefs(t, pool, uSamRivera)
	repo := NewRepository(pool)
	svc := NewService(repo)

	enabled := true
	start := "20:00"
	end := "07:30"
	tz := "America/Los_Angeles"
	bypass := []string{uAlexMorgan}
	got, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uSamRivera}, NotificationSettingsPatch{
		QuietHours: QuietHoursPatch{
			Enabled:       &enabled,
			StartLocal:    &start,
			EndLocal:      &end,
			Timezone:      &tz,
			BypassUserIDs: bypass,
		},
	})
	if err != nil {
		t.Fatalf("set qh: %v", err)
	}
	if !got.QuietHours.Enabled || got.QuietHours.StartLocal != "20:00" || got.QuietHours.EndLocal != "07:30" {
		t.Fatalf("qh round-trip mismatch: %+v", got.QuietHours)
	}
	if len(got.QuietHours.BypassUserIDs) != 1 || got.QuietHours.BypassUserIDs[0] != uAlexMorgan {
		t.Fatalf("bypass user ids lost on round-trip: %+v", got.QuietHours.BypassUserIDs)
	}

	// The SQL CHECK rejects a malformed HH:MM at the column boundary (defence in depth: the
	// service catches it first, but a direct write must also fail).
	if _, err := pool.Exec(context.Background(), `
		UPDATE msg_quiet_hours SET start_local = '25:99' WHERE user_id = $1::uuid`, uSamRivera); err == nil {
		t.Fatalf("CHECK should reject a malformed HH:MM, got nil error")
	}
}

// Caller isolation is intrinsic: c.UserID comes from the verified StaffContext (never from the
// body), so alice's PUT cannot alter bob's stored row. The integration counterpart pins this
// against a real DB.
func TestIntegration_NotificationSettings_CallerIsolation(t *testing.T) {
	pool := intPool(t)
	clearPersonPrefs(t, pool, uSamRivera, uAlexMorgan)
	repo := NewRepository(pool)
	svc := NewService(repo)

	// Seed alice's orders row as in_app=true push=true (the default) explicitly.
	inApp := true
	push := true
	if _, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uSamRivera}, NotificationSettingsPatch{
		EventPrefs: []PersonEventPrefsPatch{{Category: EventCategoryOrdersReleasedAndBlocked, InApp: &inApp, Push: &push}},
	}); err != nil {
		t.Fatalf("seed alice: %v", err)
	}
	// Now bob turns the SAME category off; alice's row must stay (true, true).
	inAppOff := false
	pushOff := false
	if _, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uAlexMorgan}, NotificationSettingsPatch{
		EventPrefs: []PersonEventPrefsPatch{{Category: EventCategoryOrdersReleasedAndBlocked, InApp: &inAppOff, Push: &pushOff}},
	}); err != nil {
		t.Fatalf("set bob: %v", err)
	}
	alice, err := svc.GetNotificationSettings(context.Background(), Caller{UserID: uSamRivera})
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	for _, p := range alice.EventPrefs {
		if p.EventCategory == EventCategoryOrdersReleasedAndBlocked && (!p.InApp || !p.Push) {
			t.Fatalf("alice's row should be unchanged: in_app=%v push=%v", p.InApp, p.Push)
		}
	}
}

// Service-level: the bad-HH:MM and bad-timezone refusals must run before any row is written. A
// half-applied write would leave the row in a state the surface cannot honestly present back.
func TestIntegration_NotificationSettings_BadFieldRefusesWholeRequest(t *testing.T) {
	pool := intPool(t)
	clearPersonPrefs(t, pool, uSamRivera)
	repo := NewRepository(pool)
	svc := NewService(repo)

	if _, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uSamRivera}, NotificationSettingsPatch{
		QuietHours: QuietHoursPatch{StartLocal: stringPtr("25:99")},
	}); !errors.Is(err, ErrBadHHMM) {
		t.Fatalf("bad HH:MM want ErrBadHHMM, got %v", err)
	}
	if _, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uSamRivera}, NotificationSettingsPatch{
		QuietHours: QuietHoursPatch{Timezone: stringPtr("Mars/Olympus")},
	}); !errors.Is(err, ErrBadTimezone) {
		t.Fatalf("bad timezone want ErrBadTimezone, got %v", err)
	}
	// After both refused writes, no quiet-hours row should exist for this user.
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM msg_quiet_hours WHERE user_id = $1::uuid`, uSamRivera).Scan(&n); err != nil {
		t.Fatalf("post-refusal read: %v", err)
	}
	if n != 0 {
		t.Fatalf("refused writes must leave the row absent; got %d rows", n)
	}
}

// stringPtr is a tiny helper used by the bad-field tests; kept beside them so the file is
// self-sufficient.
func stringPtr(s string) *string { return &s }
