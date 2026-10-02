package main

// HH-196: portal invites are created but can never be delivered or accepted
// (the accept route is unregistered until HH-4.2 and no live notifier is
// wired), so the served create route answers 501 and writes nothing. This is
// the composition leg: the real registerRoutes, a manage_users bearer, the
// scratch database behind it.

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/portal"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestPortalInviteCreateIsNotImplementedAndWritesNothing(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	ctx := context.Background()

	count := func() int {
		var n int
		if err := graph.pool.QueryRow(ctx, `SELECT count(*) FROM portal_invites WHERE email = $1`, "hh196-crew@example.test").Scan(&n); err != nil {
			t.Fatalf("count invites: %v", err)
		}
		return n
	}
	before := count()
	outbox := func() int {
		var n int
		if err := graph.pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE topic = $1`, portal.TopicInvite).Scan(&n); err != nil {
			t.Fatalf("count outbox: %v", err)
		}
		return n
	}
	outboxBefore := outbox()

	post := func(bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tenant/invites",
			strings.NewReader(`{"email":"hh196-crew@example.test","role":"field_crew"}`))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		return rsp
	}
	// The guards run before the 501: no bearer is 401, a crew member without
	// manage_users is 403.
	if rsp := post(""); rsp.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: status %d, want 401", rsp.Code)
	}
	if rsp := post(env.minter.TenantToken(t, testkit.SubPortalCrewAlpha, testkit.TenantAlphaID)); rsp.Code != http.StatusForbidden {
		t.Fatalf("crew without manage_users: status %d, want 403", rsp.Code)
	}

	rsp := post(env.minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID))

	if rsp.Code != http.StatusNotImplemented {
		t.Fatalf("POST /api/v1/tenant/invites as manage_users: status %d body %s, want 501", rsp.Code, rsp.Body.String())
	}
	if !strings.Contains(rsp.Body.String(), `"error"`) {
		t.Fatalf("501 body %q lacks the module's error shape", rsp.Body.String())
	}
	if !strings.Contains(rsp.Body.String(), "inviting users is not available yet") {
		t.Fatalf("501 body %q lacks the plain message", rsp.Body.String())
	}
	if strings.Contains(rsp.Body.String(), "token") {
		t.Fatalf("501 body %q must not carry a token", rsp.Body.String())
	}
	if after := count(); after != before {
		t.Fatalf("portal_invites rows for the probe email: %d before, %d after; a 501 must write nothing", before, after)
	}
	if after := outbox(); after != outboxBefore {
		t.Fatalf("invite outbox events: %d before, %d after; a 501 must emit nothing", outboxBefore, after)
	}
}

// The boot wiring of the log notifier is the only production control on
// message bodies: outside dev a token in a body must not reach the log, in dev
// it must (a developer needs the link). The real wiring, the real notifier.
func TestBootLogNotifierShowsBodiesOnlyInDev(t *testing.T) {
	const token = "RAWTOKEN-hh196-boot"
	for _, tc := range []struct {
		appEnv string
		want   bool
	}{{"staging", false}, {"dev", true}} {
		t.Run(tc.appEnv, func(t *testing.T) {
			env := newCompEnv(t)
			t.Setenv("APP_ENV", tc.appEnv)
			// A non-dev box must state its payment vault key (config.Validate);
			// an invented all-zero fixture value, not a credential.
			t.Setenv("PAYMENT_VAULT_KEY", strings.Repeat("00", 32))
			var buf bytes.Buffer
			prior := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
			t.Cleanup(func() { slog.SetDefault(prior) })

			_, graph := registeredSurface(t, env)
			buf.Reset()
			err := graph.tops.notifier.Send(context.Background(), notify.Message{
				Channel: notify.EmailChannel, To: "who@example.test", Subject: "Invite",
				Body: "Activate: https://portal.example/?token=" + token})
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			if got := strings.Contains(buf.String(), token); got != tc.want {
				t.Fatalf("APP_ENV=%s: token in log = %v, want %v; log: %s", tc.appEnv, got, tc.want, buf.String())
			}
		})
	}
}
