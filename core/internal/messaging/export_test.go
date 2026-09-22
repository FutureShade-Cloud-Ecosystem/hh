package messaging

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - projectForExport masking (Feature B) -
// Mirrors the audit masked-only doctrine: a soft-deleted message is tombstoned, a cleartext-looking
// identifier (7+ digit run) is redacted, and a clean/short-digit body passes through.
func TestProjectForExport_Masking(t *testing.T) {
	del := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   Message
		want string
	}{
		{"deleted is tombstoned", Message{Body: "was here", DeletedAt: &del}, "[deleted]"},
		{"cleartext id redacted", Message{Body: "SIN is 123456789 fyi"}, "[redacted]"},
		{"pan-like redacted", Message{Body: "card 4111111111111111"}, "[redacted]"},
		{"clean body passes", Message{Body: "yard restock lands at 3pm"}, "yard restock lands at 3pm"},
		{"masked ref passes", Message{Body: "receipt •••• 4321"}, "receipt •••• 4321"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := projectForExport(c.in).Body; got != c.want {
				t.Fatalf("projectForExport body = %q, want %q", got, c.want)
			}
		})
	}
}

// projectForExport preserves author attribution + metadata (admin/HR export needs WHO said WHAT).
func TestProjectForExport_PreservesAttribution(t *testing.T) {
	m := Message{ID: "m1", ChannelID: "c1", AuthorID: "u1", AuthorName: "Sam", Kind: MsgText, Body: "hi"}
	out := projectForExport(m)
	if out.AuthorID != "u1" || out.AuthorName != "Sam" || out.ID != "m1" || out.ChannelID != "c1" {
		t.Fatalf("export must preserve id/channel/author attribution, got %+v", out)
	}
}

// - service export projects every row (Feature B) -
func TestExportMessages_ProjectsAllRows(t *testing.T) {
	del := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	f := newFakeStore()
	f.exportRows = []Message{
		{ID: "1", Body: "clean"},
		{ID: "2", Body: "was here", DeletedAt: &del},
		{ID: "3", Body: "acct 987654321"},
	}
	svc := svcWith(f)
	out, err := svc.ExportMessages(context.Background(), ExportFilter{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 exported rows, got %d", len(out))
	}
	if out[0].Body != "clean" || out[1].Body != "[deleted]" || out[2].Body != "[redacted]" {
		t.Fatalf("projection not applied per row: %+v", out)
	}
}

// - handler export (Feature B) -
// export() runs after the RequireAdmin route gate; the handler itself validates the cast-bound filter
// params (uuid / RFC3339) at the boundary and returns the masked rows.
func newExportHandler() (*Handler, *fakeStore) {
	f := newFakeStore()
	f.exportRows = []Message{{ID: "1", ChannelID: "c1", Kind: MsgText, Body: "hi"}}
	return NewHandler(svcWith(f), nil), f
}

func exportReq(rawQuery string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/messaging/export?"+rawQuery, nil)
	return r.WithContext(identity.ContextWithStaff(r.Context(),
		identity.StaffContext{UserID: uColtonTest, Status: "ACTIVE"}))
}

const uColtonTest = "d7000000-0000-4000-9000-000000000010"

func TestHandler_Export_ValidReturns200(t *testing.T) {
	h, _ := newExportHandler()
	rr := httptest.NewRecorder()
	h.export(rr, exportReq("channel_id=a5000000-0000-4000-9000-000000000005&since=2026-01-01T00:00:00Z"), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("valid export want 200, got %d (body %s)", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !contains(body, `"messages"`) {
		t.Fatalf("export body should carry a messages array, got %s", body)
	}
}

func TestHandler_Export_BadFiltersReturn400(t *testing.T) {
	h, _ := newExportHandler()
	for _, q := range []string{
		"channel_id=not-a-uuid",
		"user_id=nope",
		"since=not-a-time",
		"until=2026-13-99T00:00:00Z",
	} {
		rr := httptest.NewRecorder()
		h.export(rr, exportReq(q), guards.StaffCaller{})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("bad filter %q: want 400, got %d (body %s)", q, rr.Code, rr.Body.String())
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
