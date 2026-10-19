package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - ShouldDeliverPush matrix (Slice S7, 0401) -
//
// The brief's rule in five clauses:
//
//	(a) forced-ack OR escalation tier OR firehose+override -> breaks through every quiet gate;
//	(b) !shouldNotify -> false;
//	(c) !classPref.Push -> false (for events with a category);
//	(d) qh.ActiveAt AND NOT (dm bypass) -> false;
//	(e) otherwise -> true.
//
// Every row exercises one rule. The predicate is pure; the matrix is provable DB-free.
func TestShouldDeliverPush_Matrix(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	at := time.Date(2026, 4, 1, 22, 0, 0, 0, loc) // 22:00 LA, inside an 18:00-06:00 overnight window
	prefs := NotificationPrefs{DesktopEnabled: true, EscalationOverride: true, Muted: false, Mode: ModeAll}
	classPref := DefaultPersonEventPref(uAlice, EventCategoryOrdersReleasedAndBlocked)
	qh := QuietHours{Enabled: true, StartLocal: "18:00", EndLocal: "06:00", Timezone: "America/Los_Angeles", BypassUserIDs: []string{}}
	cases := []struct {
		name        string
		tier        string
		muted       bool
		isForcedAck bool
		category    string
		classPush   bool
		inQuiet     bool
		dmSender    string
		bypass      []string
		want        bool
	}{
		{"forced-ack breaks through quiet hours", TierStandard, false, true, EventCategoryOrdersReleasedAndBlocked, false, true, "", nil, true},
		{"escalation tier breaks through quiet hours", TierEscalation, false, false, EventCategoryOrdersReleasedAndBlocked, false, true, "", nil, true},
		{"firehose+override breaks through quiet hours", TierFirehose, true, false, EventCategoryOrdersReleasedAndBlocked, false, true, "", nil, true},
		{"muted channel suppresses outside quiet hours", TierStandard, true, false, EventCategoryOrdersReleasedAndBlocked, true, false, "", nil, false},
		{"classPref.Push=false suppresses ordinary event in quiet hours", TierStandard, false, false, EventCategoryStockReceived, false, true, "", nil, false},
		{"classPref.Push=true allows ordinary event outside quiet hours", TierStandard, false, false, EventCategoryStockReceived, true, false, "", nil, true},
		{"no category is unaffected by classPref.Push", TierStandard, false, false, "", false, true, "", nil, false},
		{"DM from bypass user breaks through quiet hours", TierStandard, false, false, EventCategoryOrdersReleasedAndBlocked, true, true, uBob, []string{uBob}, true},
		{"DM from non-bypass user does NOT break through quiet hours", TierStandard, false, false, EventCategoryOrdersReleasedAndBlocked, true, true, uBob, []string{"other"}, false},
		{"non-DM in quiet hours is suppressed", TierStandard, false, false, EventCategoryOrdersReleasedAndBlocked, false, true, "", []string{uBob}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := prefs
			p.Muted = c.muted
			cp := classPref
			cp.Push = c.classPush
			if c.category == "" {
				cp = PersonEventPref{} // empty category disables the per-category check
			}
			qhNow := qh
			qhNow.Enabled = c.inQuiet
			qhNow.BypassUserIDs = c.bypass
			if got := ShouldDeliverPush(at, p, c.tier, c.isForcedAck, c.category, cp, qhNow, c.dmSender); got != c.want {
				t.Fatalf("ShouldDeliverPush tier=%s muted=%v forced=%v cat=%s push=%v quiet=%v dm=%s bypass=%v = %v, want %v",
					c.tier, c.muted, c.isForcedAck, c.category, c.classPush, c.inQuiet, c.dmSender, c.bypass, got, c.want)
			}
		})
	}
}

// - QuietHours.ActiveAt -

// Overnight window (18:00 to 06:00) is active at 22:00 and 03:00, inactive at 12:00 and 17:59,
// active at 18:00 (start inclusive), inactive at 06:00 (end exclusive).
func TestActiveAt_OvernightWindow(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	qh := QuietHours{Enabled: true, StartLocal: "18:00", EndLocal: "06:00", Timezone: "America/Los_Angeles"}
	cases := []struct {
		name string
		h, m int
		want bool
	}{
		{"12:00 is OUTSIDE 18:00-06:00", 12, 0, false},
		{"17:59 is OUTSIDE 18:00-06:00", 17, 59, false},
		{"18:00 is INSIDE 18:00-06:00 (start inclusive)", 18, 0, true},
		{"22:00 is INSIDE 18:00-06:00", 22, 0, true},
		{"03:00 is INSIDE 18:00-06:00", 3, 0, true},
		{"05:59 is INSIDE 18:00-06:00", 5, 59, true},
		{"06:00 is OUTSIDE 18:00-06:00 (end exclusive)", 6, 0, false},
		{"09:00 is OUTSIDE 18:00-06:00", 9, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			at := time.Date(2026, 4, 1, c.h, c.m, 0, 0, loc)
			if got := qh.ActiveAt(at); got != c.want {
				t.Fatalf("ActiveAt(%02d:%02d) = %v, want %v", c.h, c.m, got, c.want)
			}
		})
	}
}

// Same-day window (09:00 to 17:00) is active 09:00-16:59, inactive at 08:59, 17:00.
func TestActiveAt_SameDayWindow(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	qh := QuietHours{Enabled: true, StartLocal: "09:00", EndLocal: "17:00", Timezone: "America/Los_Angeles"}
	cases := []struct {
		h, m int
		want bool
	}{
		{8, 59, false}, {9, 0, true}, {12, 0, true}, {16, 59, true}, {17, 0, false}, {23, 0, false},
	}
	for _, c := range cases {
		at := time.Date(2026, 4, 1, c.h, c.m, 0, 0, loc)
		if got := qh.ActiveAt(at); got != c.want {
			t.Fatalf("ActiveAt(%02d:%02d) = %v, want %v", c.h, c.m, got, c.want)
		}
	}
}

// Disabled window is never active, regardless of time.
func TestActiveAt_DisabledNeverActive(t *testing.T) {
	qh := QuietHours{Enabled: false, StartLocal: "18:00", EndLocal: "06:00", Timezone: "America/Los_Angeles"}
	if qh.ActiveAt(time.Date(2026, 4, 1, 22, 0, 0, 0, time.UTC)) {
		t.Fatalf("disabled quiet hours must never be active")
	}
}

// - NotificationSettings service behaviour (0401) -

// Default 6 categories returned in board order when no rows exist.
func TestService_GetNotificationSettings_DefaultsAllSixCategories(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)
	got, err := svc.GetNotificationSettings(context.Background(), Caller{UserID: uAlice})
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if len(got.EventPrefs) != len(EventCategories) {
		t.Fatalf("event_prefs: want %d categories, got %d", len(EventCategories), len(got.EventPrefs))
	}
	for i, cat := range EventCategories {
		if got.EventPrefs[i].EventCategory != cat {
			t.Fatalf("event_prefs[%d].EventCategory = %s, want %s", i, got.EventPrefs[i].EventCategory, cat)
		}
	}
	if got.EventPrefs[0].Push != true || got.EventPrefs[0].InApp != true {
		t.Fatalf("orders_released_and_blocked default want in_app=true push=true, got %+v", got.EventPrefs[0])
	}
	if got.EventPrefs[1].Push != false || got.EventPrefs[1].InApp != true {
		t.Fatalf("stock_received default want in_app=true push=false, got %+v", got.EventPrefs[1])
	}
	if got.EventPrefs[4].Push != false || got.EventPrefs[4].InApp != false {
		t.Fatalf("payments default want in_app=false push=false, got %+v", got.EventPrefs[4])
	}
	if got.QuietHours.Enabled != false {
		t.Fatalf("default quiet hours should be disabled, got %+v", got.QuietHours)
	}
}

// A partial PUT that touches only `push` on one category does not mutate OTHER categories'
// defaults; a follow-up GET surfaces them from the per-category default still.
func TestService_SetNotificationSettings_PartialUpsertPreservesOtherCategories(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)

	// Alice has never visited the surface; the orders row's defaults (true, true) must survive a
	// PUT that touches only stock_received.
	push := false
	_, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uAlice}, NotificationSettingsPatch{
		EventPrefs: []PersonEventPrefsPatch{{Category: EventCategoryStockReceived, Push: &push}},
	})
	if err != nil {
		t.Fatalf("set settings: %v", err)
	}
	if _, ok := f.personPrefs[uAlice][EventCategoryOrdersReleasedAndBlocked]; ok {
		t.Fatalf("orders_released_and_blocked must NOT have a stored row (no PUT touched it); got %+v", f.personPrefs[uAlice][EventCategoryOrdersReleasedAndBlocked])
	}
	stored2, ok := f.personPrefs[uAlice][EventCategoryStockReceived]
	if !ok || stored2.Push != false {
		t.Fatalf("stock_received should reflect push=false, got %+v", stored2)
	}
	// And the GET must still surface the default for orders_released_and_blocked (the surface
	// never sees a half-applied change).
	got, err := svc.GetNotificationSettings(context.Background(), Caller{UserID: uAlice})
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	for _, p := range got.EventPrefs {
		if p.EventCategory == EventCategoryOrdersReleasedAndBlocked {
			if !p.InApp || !p.Push {
				t.Fatalf("orders_released_and_blocked should still be in_app=true push=true in the GET, got %+v", p)
			}
		}
		if p.EventCategory == EventCategoryStockReceived {
			if p.Push {
				t.Fatalf("stock_received should be push=false in the GET, got %+v", p)
			}
		}
	}
}

// Unknown category refuses the WHOLE request (no partial write).
func TestService_SetNotificationSettings_UnknownCategory400(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)
	_, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uAlice}, NotificationSettingsPatch{
		EventPrefs: []PersonEventPrefsPatch{{Category: "not_a_category"}},
	})
	if !errors.Is(err, ErrBadEventCategory) {
		t.Fatalf("unknown category want ErrBadEventCategory, got %v", err)
	}
	if len(f.personPrefs[uAlice]) != 0 {
		t.Fatalf("unknown category must not write any rows, got %d", len(f.personPrefs[uAlice]))
	}
}

// Invalid HH:MM or timezone refuses the WHOLE request.
func TestService_SetNotificationSettings_BadHHMMAndTimezoneRefused(t *testing.T) {
	cases := []struct {
		name string
		p    QuietHoursPatch
		want error
	}{
		{"bad HH:MM", QuietHoursPatch{StartLocal: stringp("25:99")}, ErrBadHHMM},
		{"bad timezone", QuietHoursPatch{Timezone: stringp("Mars/Olympus")}, ErrBadTimezone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeStore()
			svc := svcWith(f)
			_, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uAlice}, NotificationSettingsPatch{QuietHours: c.p})
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			if _, ok := f.quietHours[uAlice]; ok {
				t.Fatalf("bad quiet-hours patch must not write the row, got %+v", f.quietHours[uAlice])
			}
		})
	}
}

// Caller isolation: Alice's PUT must not alter Bob's stored settings.
func TestService_SetNotificationSettings_CallerIsolation(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)
	push := true
	// Seed Bob's orders row already.
	_, _ = svc.SetNotificationSettings(context.Background(), Caller{UserID: uBob}, NotificationSettingsPatch{
		EventPrefs: []PersonEventPrefsPatch{{Category: EventCategoryOrdersReleasedAndBlocked, Push: &push}},
	})
	bobStored, ok := f.personPrefs[uBob][EventCategoryOrdersReleasedAndBlocked]
	if !ok {
		t.Fatalf("setup: bob has no row")
	}
	// Now Alice turns the same category off.
	off := false
	_, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uAlice}, NotificationSettingsPatch{
		EventPrefs: []PersonEventPrefsPatch{{Category: EventCategoryOrdersReleasedAndBlocked, Push: &off}},
	})
	if err != nil {
		t.Fatalf("alice set: %v", err)
	}
	if !f.personPrefs[uBob][EventCategoryOrdersReleasedAndBlocked].Push {
		t.Fatalf("alice's PUT must not change bob's row, bob.Push now %v", f.personPrefs[uBob][EventCategoryOrdersReleasedAndBlocked].Push)
	}
	if f.personPrefs[uAlice][EventCategoryOrdersReleasedAndBlocked].Push {
		t.Fatalf("alice's PUT must persist her own row, alice.Push now %v", f.personPrefs[uAlice][EventCategoryOrdersReleasedAndBlocked].Push)
	}
	if !bobStored.Push {
		t.Fatalf("bob's stored value should be unchanged from setup")
	}
}

// Quiet-hours upsert writes a row when at least one field is set; a totally absent patch is a no-op.
func TestService_SetNotificationSettings_QuietHoursRoundTrip(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)
	enabled := true
	start := "20:00"
	tz := "America/Los_Angeles"
	got, err := svc.SetNotificationSettings(context.Background(), Caller{UserID: uAlice}, NotificationSettingsPatch{
		QuietHours: QuietHoursPatch{Enabled: &enabled, StartLocal: &start, Timezone: &tz},
	})
	if err != nil {
		t.Fatalf("set qh: %v", err)
	}
	if !got.QuietHours.Enabled || got.QuietHours.StartLocal != "20:00" {
		t.Fatalf("quiet hours not round-tripped, got %+v", got.QuietHours)
	}
	// Empty patch should leave the stored row alone.
	before := f.quietHours[uAlice]
	_, err = svc.SetNotificationSettings(context.Background(), Caller{UserID: uAlice}, NotificationSettingsPatch{})
	if err != nil {
		t.Fatalf("empty set: %v", err)
	}
	if _, ok := f.quietHours[uAlice]; !ok {
		t.Fatalf("empty patch must not erase the row")
	}
	if f.quietHours[uAlice].Enabled != before.Enabled ||
		f.quietHours[uAlice].StartLocal != before.StartLocal ||
		f.quietHours[uAlice].EndLocal != before.EndLocal ||
		f.quietHours[uAlice].Timezone != before.Timezone {
		t.Fatalf("empty patch must not mutate the row; before=%+v after=%+v", before, f.quietHours[uAlice])
	}
}

// - handler wiring for notification-settings (mirrors the work-facing route surface) -

func newNotificationSettingsHandler(t *testing.T) *Handler {
	t.Helper()
	fs := newFakeStore()
	return NewHandler(svcWith(fs), nil)
}

func nsReq(method, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/api/v1/messaging/notification-settings", nil)
	} else {
		r = httptest.NewRequest(method, "/api/v1/messaging/notification-settings", strings.NewReader(body))
	}
	return r.WithContext(identity.ContextWithStaff(r.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
}

func TestHandler_GetNotificationSettings_ReturnsDefaultsSix(t *testing.T) {
	h := newNotificationSettingsHandler(t)
	rr := httptest.NewRecorder()
	h.getNotificationSettings(rr, nsReq(http.MethodGet, ""), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("GET settings want 200, got %d (body %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"event_category":"orders_released_and_blocked"`) {
		t.Fatalf("GET should return the closed set in board order; got %s", rr.Body.String())
	}
}

func TestHandler_PutNotificationSettings_UnknownCategory400(t *testing.T) {
	h := newNotificationSettingsHandler(t)
	rr := httptest.NewRecorder()
	h.putNotificationSettings(rr, nsReq(http.MethodPut, `{"event_prefs":[{"category":"nope"}]}`), guards.StaffCaller{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown category want 400, got %d (body %s)", rr.Code, rr.Body.String())
	}
}

func TestHandler_PutNotificationSettings_BadHHMM400(t *testing.T) {
	h := newNotificationSettingsHandler(t)
	rr := httptest.NewRecorder()
	h.putNotificationSettings(rr, nsReq(http.MethodPut, `{"quiet_hours":{"start_local":"25:99"}}`), guards.StaffCaller{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad HH:MM want 400, got %d (body %s)", rr.Code, rr.Body.String())
	}
}

func TestHandler_PutNotificationSettings_BadTimezone400(t *testing.T) {
	h := newNotificationSettingsHandler(t)
	rr := httptest.NewRecorder()
	h.putNotificationSettings(rr, nsReq(http.MethodPut, `{"quiet_hours":{"timezone":"Mars/Olympus"}}`), guards.StaffCaller{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad timezone want 400, got %d (body %s)", rr.Code, rr.Body.String())
	}
}

func stringp(s string) *string { return &s }
