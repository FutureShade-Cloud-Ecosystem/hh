package main

// HH-197: the portal change-password route checked the legacy password hash
// and wrote a new one that only the unregistered login and reset paths read,
// while every real session is issued by Appwrite (D38). A contractor who
// changed their password got a 200 and nothing that signs anyone in changed.
// Until HH-4.2 routes password changes through Appwrite the served route
// answers 501 and writes nothing. This is the composition leg: the real
// registerRoutes, real bearers, the scratch database behind it.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestPortalChangePasswordIsNotImplementedAndWritesNothing(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	ctx := context.Background()

	const current = "hh197-current-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(current), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// The fixture rows carry a placeholder, not a hash; plant a real one so the
	// pre-fix route would verify it and write a new one, and restore on exit.
	var original string
	if err := graph.pool.QueryRow(ctx, `SELECT password_hash FROM portal_users WHERE id = $1`, testkit.PortalCrewAlphaID).Scan(&original); err != nil {
		t.Fatalf("read fixture hash: %v", err)
	}
	if _, err := graph.pool.Exec(ctx, `UPDATE portal_users SET password_hash = $2 WHERE id = $1`, testkit.PortalCrewAlphaID, string(hash)); err != nil {
		t.Fatalf("plant hash: %v", err)
	}
	t.Cleanup(func() {
		_, _ = graph.pool.Exec(context.Background(), `DELETE FROM portal_password_resets WHERE portal_user_id = $1 AND token_hash = 'hh197-probe-token-hash'`, testkit.PortalCrewAlphaID)
		_, _ = graph.pool.Exec(context.Background(), `UPDATE portal_users SET password_hash = $2 WHERE id = $1`, testkit.PortalCrewAlphaID, original)
	})
	// A live reset row: the old best-effort "revoke" consumed these.
	if _, err := graph.pool.Exec(ctx,
		`INSERT INTO portal_password_resets (portal_user_id, token_hash, expires_at) VALUES ($1, 'hh197-probe-token-hash', now() + interval '1 hour')`,
		testkit.PortalCrewAlphaID); err != nil {
		t.Fatalf("plant reset row: %v", err)
	}

	type snapshot struct {
		hash       string
		resets     int
		liveResets int
	}
	snap := func() snapshot {
		var s snapshot
		if err := graph.pool.QueryRow(ctx, `SELECT password_hash FROM portal_users WHERE id = $1`, testkit.PortalCrewAlphaID).Scan(&s.hash); err != nil {
			t.Fatalf("read hash: %v", err)
		}
		if err := graph.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE consumed_at IS NULL) FROM portal_password_resets WHERE portal_user_id = $1`, testkit.PortalCrewAlphaID).Scan(&s.resets, &s.liveResets); err != nil {
			t.Fatalf("count resets: %v", err)
		}
		return s
	}
	before := snap()

	// bodyReads counts the bytes the handler pulled from the request body: a 501 must not even decode
	// the passwords it was sent.
	var bodyReads int
	post := func(bearer string) *httptest.ResponseRecorder {
		body := &countingReader{r: strings.NewReader(`{"current_password":"` + current + `","new_password":"hh197-brand-new-password"}`), n: &bodyReads}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tenant/me/password", body)
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		return rsp
	}
	// The guards run before the 501: no bearer is 401, and a staff bearer on
	// the tenant route is refused 403 (this route has no capability, only the
	// tenant audience).
	if rsp := post(""); rsp.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: status %d, want 401", rsp.Code)
	}
	if rsp := post(env.minter.StaffToken(t, testkit.SubPortalCrewAlpha)); rsp.Code != http.StatusForbidden {
		t.Fatalf("staff bearer on the tenant route: status %d, want 403", rsp.Code)
	}
	if after := snap(); after != before {
		t.Fatalf("a refused call wrote: before %+v after %+v", before, after)
	}

	rsp := post(env.minter.TenantToken(t, testkit.SubPortalCrewAlpha, testkit.TenantAlphaID))

	if rsp.Code != http.StatusNotImplemented {
		t.Fatalf("POST /api/v1/tenant/me/password as an authorised caller: status %d body %s, want 501", rsp.Code, rsp.Body.String())
	}
	if !strings.Contains(rsp.Body.String(), `"error"`) || !strings.Contains(rsp.Body.String(), "changing your password here is not available yet") {
		t.Fatalf("501 body %q lacks the module's error shape or the plain message", rsp.Body.String())
	}
	if strings.Contains(rsp.Body.String(), "hh197-") {
		t.Fatalf("501 body %q echoes request content", rsp.Body.String())
	}
	if bodyReads != 0 {
		t.Fatalf("the handler read %d bytes of the request body; a 501 must not decode the passwords", bodyReads)
	}
	after := snap()
	if after.hash != before.hash {
		t.Fatalf("stored password hash changed behind a refused request")
	}
	if after.resets != before.resets || after.liveResets != before.liveResets {
		t.Fatalf("reset rows touched: before %+v after %+v", before, after)
	}
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n *int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += n
	return n, err
}
