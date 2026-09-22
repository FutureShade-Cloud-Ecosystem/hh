package messaging

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func boolp(b bool) *bool { return &b }

// - shouldNotify matrix (Feature A) -
// Pins the exact rule: a FIREHOSE channel bypasses `muted` ONLY when escalation_override is on;
// otherwise `muted` suppresses; otherwise the desktop toggle decides. ESCALATION/STANDARD never bypass
// a mute (the override is firehose-only, by spec).
func TestShouldNotify_Matrix(t *testing.T) {
	cases := []struct {
		name     string
		tier     string
		muted    bool
		override bool
		desktop  bool
		want     bool
	}{
		{"firehose muted+override notifies (bypass)", TierFirehose, true, true, false, true},
		{"firehose muted no-override suppressed", TierFirehose, true, false, true, false},
		{"firehose unmuted override desktop-on notifies", TierFirehose, false, true, true, true},
		{"firehose unmuted desktop-off suppressed", TierFirehose, false, false, false, false},
		{"escalation muted+override still suppressed (firehose-only bypass)", TierEscalation, true, true, true, false},
		{"escalation unmuted desktop-on notifies", TierEscalation, false, false, true, true},
		{"standard muted+override still suppressed", TierStandard, true, true, true, false},
		{"standard unmuted desktop-on notifies", TierStandard, false, false, true, true},
		{"standard unmuted desktop-off suppressed", TierStandard, false, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pref := NotificationPrefs{Muted: c.muted, EscalationOverride: c.override, DesktopEnabled: c.desktop}
			if got := shouldNotify(pref, c.tier); got != c.want {
				t.Fatalf("shouldNotify(tier=%s muted=%v override=%v desktop=%v) = %v, want %v",
					c.tier, c.muted, c.override, c.desktop, got, c.want)
			}
		})
	}
}

// - notify-prefs upsert (Feature A) -

// GET returns the defaults when the caller has no explicit pref row.
func TestGetNotifyPrefs_DefaultsWhenUnset(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	svc := svcWith(f)
	p, err := svc.GetNotifyPrefs(context.Background(), Caller{UserID: uAlice}, chGen)
	if err != nil {
		t.Fatalf("get prefs: %v", err)
	}
	if !p.DesktopEnabled || !p.EscalationOverride || p.Muted {
		t.Fatalf("unset prefs must be the defaults (desktop on, override on, not muted), got %+v", p)
	}
}

// A partial PUT (only Muted set) must MERGE over the current prefs, the omitted fields keep their
// current values rather than being coerced to false.
func TestSetNotifyPrefs_UpsertMergesPartial(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	svc := svcWith(f)

	// Patch only Muted=true; desktop_enabled/escalation_override omitted → keep defaults (true/true).
	got, err := svc.SetNotifyPrefs(context.Background(), Caller{UserID: uAlice}, chGen, PrefsPatch{Muted: boolp(true)})
	if err != nil {
		t.Fatalf("set prefs: %v", err)
	}
	if !got.DesktopEnabled || !got.EscalationOverride || !got.Muted {
		t.Fatalf("partial patch should keep desktop/override defaults and set muted, got %+v", got)
	}

	// Second partial patch: only DesktopEnabled=false → muted (true) and override (true) must persist.
	got2, err := svc.SetNotifyPrefs(context.Background(), Caller{UserID: uAlice}, chGen, PrefsPatch{DesktopEnabled: boolp(false)})
	if err != nil {
		t.Fatalf("set prefs 2: %v", err)
	}
	if got2.DesktopEnabled || !got2.EscalationOverride || !got2.Muted {
		t.Fatalf("second partial patch must preserve muted/override, got %+v", got2)
	}
	// The store must hold the merged row (upsert, not a fresh default).
	if stored := f.prefs[chGen+"|"+uAlice]; stored.DesktopEnabled || !stored.Muted {
		t.Fatalf("stored upsert row wrong: %+v", stored)
	}
}

func TestNotifyPrefs_NonMemberForbidden(t *testing.T) {
	f := newFakeStore()
	f.exists[chGen] = true // channel exists, Bob not a member
	svc := svcWith(f)
	if _, err := svc.GetNotifyPrefs(context.Background(), Caller{UserID: uBob}, chGen); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member GET prefs want ErrNotMember, got %v", err)
	}
	if _, err := svc.SetNotifyPrefs(context.Background(), Caller{UserID: uBob}, chGen, PrefsPatch{Muted: boolp(true)}); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member PUT prefs want ErrNotMember, got %v", err)
	}
	if len(f.prefs) != 0 {
		t.Fatalf("a forbidden PUT must not upsert prefs, got %d rows", len(f.prefs))
	}
}

// - handler wiring for notify-prefs (mirrors markRead handler tests) -

func newPrefsHandler(t *testing.T) *Handler {
	t.Helper()
	fs := newFakeStore()
	fs.setMember(chUUID, uAlice)
	return NewHandler(svcWith(fs), nil)
}

func prefsReq(method, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/api/v1/messaging/channels/"+chUUID+"/notify-prefs", nil)
	} else {
		r = httptest.NewRequest(method, "/api/v1/messaging/channels/"+chUUID+"/notify-prefs", strings.NewReader(body))
	}
	r.SetPathValue("id", chUUID)
	return r.WithContext(identity.ContextWithStaff(r.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
}

func TestHandler_GetNotifyPrefs_MemberReturns200(t *testing.T) {
	h := newPrefsHandler(t)
	rr := httptest.NewRecorder()
	h.getNotifyPrefs(rr, prefsReq(http.MethodGet, ""), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("member GET prefs want 200, got %d (body %s)", rr.Code, rr.Body.String())
	}
}

func TestHandler_PutNotifyPrefs_MemberUpserts200(t *testing.T) {
	h := newPrefsHandler(t)
	rr := httptest.NewRecorder()
	h.putNotifyPrefs(rr, prefsReq(http.MethodPut, `{"muted":true}`), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("member PUT prefs want 200, got %d (body %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"muted":true`) {
		t.Fatalf("PUT prefs should echo the upserted row, got %s", rr.Body.String())
	}
}

// A malformed (non-UUID) {id} on the prefs routes must be a 4xx client fault (mirrors markRead).
func TestHandler_NotifyPrefs_NonUUIDPath_Returns4xx(t *testing.T) {
	h := newPrefsHandler(t)
	for _, m := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request, guards.StaffCaller)
	}{
		{"GET", h.getNotifyPrefs},
		{"PUT", h.putNotifyPrefs},
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/messaging/channels/not-a-uuid/notify-prefs", strings.NewReader(`{}`))
		req.SetPathValue("id", "not-a-uuid")
		req = req.WithContext(identity.ContextWithStaff(req.Context(),
			identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
		m.fn(rr, req, guards.StaffCaller{})
		if rr.Code < 400 || rr.Code >= 500 {
			t.Fatalf("%s notify-prefs non-uuid: want 4xx, got %d (body %s)", m.name, rr.Code, rr.Body.String())
		}
	}
}
