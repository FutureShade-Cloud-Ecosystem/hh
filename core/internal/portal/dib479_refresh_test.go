package portal

// dib479_refresh_test.go, sliding refresh with a 7-day absolute cap (OR-3).
//
// EVERY INSTANT IN THIS FILE IS INJECTED. The service reads its clock through s.now, so each test
// moves time by assigning that field and never by sleeping or by reading the wall clock. There is
// no time.Now() anywhere below, deliberately, and the DIB-427 tripwire enforces it: a file that
// pins a date AND reads a real clock is a test that starts failing on a morning nobody committed
// anything, and the whole point of a cap measured in days is that its test must be able to stand
// seven days in the future without waiting seven days.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mintLegacyToken signs a token exactly as the pre-DIB-479 issuer did: the same header, the same
// claims, and NO session anchor (Ost is omitempty, so a zero value simply does not appear in the
// payload). It exists so the backward-compatibility test can present a genuinely old token rather
// than a modern one with a field blanked afterwards.
func mintLegacyToken(t *testing.T, secret string, c Claims, now time.Time) (string, error) {
	t.Helper()
	c.Iat = now.Unix()
	c.Exp = now.Add(jwtTTL).Unix()
	c.Ost = 0
	hdr, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signingInput := b64(hdr) + "." + b64(payload)
	return signingInput + "." + sign(secret, signingInput), nil
}

// loginBase is the fixed instant every session in this file starts at. Far-past sentinel year on
// purpose: nothing here is ever compared against a real clock, and a reader can tell at a glance
// that these dates are fixtures rather than "roughly now".
func loginBase() time.Time { return time.Date(2031, 3, 4, 6, 0, 0, 0, time.UTC) }

// refreshFixture seeds one active user and returns the service, its store, and a setter that moves
// the injected clock.
func refreshFixture(t *testing.T) (*Service, *fakeStore, func(time.Time)) {
	t.Helper()
	store := newFakeStore()
	hash, err := hashPassword("hunter2pw")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "pm@acme.test", Name: "Dana",
		Role: string(RoleBuyer), Status: "active", passwordHash: hash})
	svc := NewService(store, testSecret)
	at := loginBase()
	svc.now = func() time.Time { return at }
	return svc, store, func(tt time.Time) { at = tt }
}

// login runs the real login path at the current injected instant.
func login(t *testing.T, svc *Service) string {
	t.Helper()
	resp, err := svc.Login(context.Background(), "pm@acme.test", "hunter2pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return resp.Token
}

// slideTo walks a live session forward to `target` the way a running client would, refreshing
// every 11 hours, inside the 12h token TTL, and returns the token held at that instant. A test
// cannot simply jump a week ahead holding its login token: that token expires after 12 hours, and
// asserting the CAP requires arriving at the deadline with a session that is still alive.
func slideTo(t *testing.T, svc *Service, setNow func(time.Time), tok string, target time.Time) string {
	t.Helper()
	for at := loginBase().Add(11 * time.Hour); at.Before(target); at = at.Add(11 * time.Hour) {
		setNow(at)
		resp, err := svc.Refresh(context.Background(), tok)
		if err != nil {
			t.Fatalf("slide: refresh at %s failed: %v", at.Sub(loginBase()), err)
		}
		tok = resp.Token
	}
	setNow(target)
	return tok
}

// claimsOf decodes a token's payload WITHOUT verifying it, so a test can inspect what was minted
// even at an instant where the token would be refused.
func claimsOf(t *testing.T, token string) Claims {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return c
}

// -- the sliding half ---------------------------------------------------------

// A refresh mints a NEW token with a later expiry, carries every identity claim across unchanged,
// and, the load-bearing part, does NOT re-stamp the session anchor.
func TestRefresh_RotatesTheTokenAndPreservesTheSessionAnchor(t *testing.T) {
	svc, _, setNow := refreshFixture(t)
	tok := login(t, svc)
	original := claimsOf(t, tok)

	setNow(loginBase().Add(11 * time.Hour))
	resp, err := svc.Refresh(context.Background(), tok)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	fresh := claimsOf(t, resp.Token)

	if resp.Token == tok {
		t.Fatal("refresh must mint a NEW token, not echo the old one")
	}
	if fresh.Exp <= original.Exp {
		t.Fatalf("expiry must roll forward: was %d, now %d", original.Exp, fresh.Exp)
	}
	if fresh.Iat != loginBase().Add(11*time.Hour).Unix() {
		t.Fatalf("iat must be the refresh instant, got %d", fresh.Iat)
	}
	if fresh.Exp != loginBase().Add(11*time.Hour).Add(jwtTTL).Unix() {
		t.Fatalf("a refreshed token must carry a full %s window, got exp %d", jwtTTL, fresh.Exp)
	}
	// Every claim the login set survives.
	if fresh.Sub != original.Sub || fresh.CustomerID != original.CustomerID || fresh.Role != original.Role {
		t.Fatalf("identity claims must survive refresh: %+v vs %+v", fresh, original)
	}
	// And the anchor is the LOGIN instant, not the refresh instant. This is the cap's memory.
	if fresh.Ost != original.Ost || fresh.Ost != loginBase().Unix() {
		t.Fatalf("session anchor must be the original login (%d), got %d (was %d)",
			loginBase().Unix(), fresh.Ost, original.Ost)
	}
	// The response tells the client both deadlines, and they must agree with the token.
	if !resp.ExpiresAt.Equal(time.Unix(fresh.Exp, 0).UTC()) {
		t.Fatalf("expires_at %s disagrees with the token's exp %d", resp.ExpiresAt, fresh.Exp)
	}
	if want := loginBase().Add(sessionMaxAge); !resp.SessionExpiresAt.Equal(want) {
		t.Fatalf("session_expires_at = %s, want %s", resp.SessionExpiresAt, want)
	}
	// The new token works.
	if _, err := svc.Authenticate(context.Background(), resp.Token); err != nil {
		t.Fatalf("refreshed token must authenticate: %v", err)
	}
}

// A chain of refreshes keeps the session alive across days, the whole reason the endpoint exists.
// The contractor who opened the board on Monday is still signed in on Wednesday.
func TestRefresh_SlidesAcrossDaysWhileTheSessionIsYoung(t *testing.T) {
	svc, _, setNow := refreshFixture(t)
	tok := login(t, svc)

	for hours := 11; hours <= 6*24; hours += 11 {
		setNow(loginBase().Add(time.Duration(hours) * time.Hour))
		resp, err := svc.Refresh(context.Background(), tok)
		if err != nil {
			t.Fatalf("refresh at +%dh must succeed (session is %s old, cap is %s): %v",
				hours, time.Duration(hours)*time.Hour, sessionMaxAge, err)
		}
		tok = resp.Token
	}
	// Six days in, still one session: the anchor never moved.
	if got := claimsOf(t, tok).Ost; got != loginBase().Unix() {
		t.Fatalf("after a week of sliding the anchor moved: %d != %d", got, loginBase().Unix())
	}
}

// -- the cap half -------------------------------------------------------------

// THE CAP. A session may not be refreshed once it is older than sessionMaxAge, and the refusal is
// the generic invalid-token error (which the route renders as a plain 401).
//
// This is the test the "remove the cap" mutation kills.
func TestRefresh_RefusesPastTheSevenDayAbsoluteCap(t *testing.T) {
	svc, _, setNow := refreshFixture(t)
	tok := login(t, svc)

	// Walk the session up to one minute before the deadline, then refresh once more: still allowed.
	tok = slideTo(t, svc, setNow, tok, loginBase().Add(sessionMaxAge-time.Minute))
	resp, err := svc.Refresh(context.Background(), tok)
	if err != nil {
		t.Fatalf("refresh one minute BEFORE the cap must succeed: %v", err)
	}
	last := resp.Token

	// Just past the cap: refused. Note the token itself is only minutes old and perfectly valid on
	// its own terms, it is the SESSION that has aged out, which is exactly what the anchor is for.
	setNow(loginBase().Add(sessionMaxAge + time.Minute))
	if _, err := svc.Refresh(context.Background(), last); err == nil {
		t.Fatal("refresh AFTER the 7-day cap must be refused")
	}
}

// The cap cannot be walked past by refreshing often enough. This drives the endpoint the way a
// running client would, every 11 hours, forever, and asserts the session dies within a minute of
// its 7-day deadline no matter how many hops it took to get there.
//
// This is the "refresh forever" test named in the mutation log.
func TestRefresh_ChainingCannotOutliveTheCap(t *testing.T) {
	svc, _, setNow := refreshFixture(t)
	tok := login(t, svc)

	var lastOK time.Duration
	died := time.Duration(-1)
	for hours := 11; hours <= 30*24; hours += 11 {
		age := time.Duration(hours) * time.Hour
		setNow(loginBase().Add(age))
		resp, err := svc.Refresh(context.Background(), tok)
		if err != nil {
			died = age
			break
		}
		tok = resp.Token
		lastOK = age
	}
	if died < 0 {
		t.Fatalf("the session never died: still refreshing after 30 days (last ok at %s), the absolute cap is not enforced", lastOK)
	}
	if died > sessionMaxAge+11*time.Hour {
		t.Fatalf("the session outlived the cap: died at %s, cap is %s", died, sessionMaxAge)
	}
	if lastOK > sessionMaxAge {
		t.Fatalf("a refresh succeeded at %s, past the %s cap", lastOK, sessionMaxAge)
	}
}

// The cap is enforced on EVERY request, not only at the refresh route: a token whose session has
// aged out stops authenticating even though its own 12h window is still open.
func TestVerifyJWT_RefusesATokenWhoseSessionAgedOut(t *testing.T) {
	svc, _, setNow := refreshFixture(t)
	tok := login(t, svc)

	// Slide to the last legal moment, then take the token it issued.
	tok = slideTo(t, svc, setNow, tok, loginBase().Add(sessionMaxAge-time.Hour))
	resp, err := svc.Refresh(context.Background(), tok)
	if err != nil {
		t.Fatalf("refresh inside the cap: %v", err)
	}
	fresh := claimsOf(t, resp.Token)
	if fresh.Exp <= loginBase().Add(sessionMaxAge).Unix() {
		t.Fatal("fixture is not exercising the case: the token must outlast the session cap")
	}

	// One hour later the token is still un-expired on its own terms...
	setNow(loginBase().Add(sessionMaxAge + time.Hour))
	if _, err := svc.Authenticate(context.Background(), resp.Token); err == nil {
		t.Fatal("a token whose SESSION aged out must not authenticate, even inside its own TTL")
	}
}

// -- the re-check half --------------------------------------------------------

// A user deactivated after their last request cannot refresh their way back in. This is why the
// refresh path runs the full authenticate (DB reload) rather than trusting the presented claims.
func TestRefresh_DeactivatedUserIsRefused(t *testing.T) {
	svc, store, setNow := refreshFixture(t)
	tok := login(t, svc)

	u, _ := store.GetUser(context.Background(), "u1")
	u.Status = "inactive"
	store.addUser(u)

	setNow(loginBase().Add(time.Hour))
	if _, err := svc.Refresh(context.Background(), tok); err == nil {
		t.Fatal("a deactivated user must not be able to refresh")
	}
}

// A demoted user refreshes into their CURRENT role, not the one frozen in the token they present.
// The token is an identity assertion, never an authorization cache (H-4).
func TestRefresh_MintsTheCurrentRoleNotTheClaimedOne(t *testing.T) {
	svc, store, setNow := refreshFixture(t)
	tok := login(t, svc)
	if claimsOf(t, tok).Role != string(RoleBuyer) {
		t.Fatal("fixture should start as buyer")
	}

	u, _ := store.GetUser(context.Background(), "u1")
	u.Role = string(RoleBuyerNoPay) // demoted: may still buy, may no longer pay
	store.addUser(u)

	setNow(loginBase().Add(time.Hour))
	resp, err := svc.Refresh(context.Background(), tok)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := claimsOf(t, resp.Token).Role; got != string(RoleBuyerNoPay) {
		t.Fatalf("refreshed token must carry the CURRENT role, got %q", got)
	}
	if resp.User.Role != string(RoleBuyerNoPay) {
		t.Fatalf("the response's user record must carry the current role, got %q", resp.User.Role)
	}
}

// An expired token cannot be refreshed. Refresh renews a LIVE session; it does not resurrect a
// lapsed one, or the 12h TTL would mean nothing.
func TestRefresh_ExpiredTokenIsRefused(t *testing.T) {
	svc, _, setNow := refreshFixture(t)
	tok := login(t, svc)

	setNow(loginBase().Add(jwtTTL + time.Minute))
	if _, err := svc.Refresh(context.Background(), tok); err == nil {
		t.Fatal("an expired token must not be refreshable")
	}
}

// A token minted before the session anchor existed (no `ost` claim) still works, and its session is
// measured from its own iat, which can only make the cap arrive sooner, never later.
func TestRefresh_LegacyTokenWithoutAnAnchorMeasuresFromItsOwnIat(t *testing.T) {
	svc, _, setNow := refreshFixture(t)

	// Hand-mint a pre-DIB-479 token: the claims as they were, with no anchor.
	legacy := Claims{Sub: "u1", CustomerID: "c1", Role: string(RoleBuyer)}
	tok, err := mintLegacyToken(t, testSecret, legacy, loginBase())
	if err != nil {
		t.Fatalf("mint legacy token: %v", err)
	}
	if claimsOf(t, tok).Ost != 0 {
		t.Fatal("fixture must produce a token with NO session anchor")
	}

	// Inside the window measured from its own iat: refreshable, and the refresh adopts that iat as
	// the anchor so the session cannot silently restart.
	setNow(loginBase().Add(2 * time.Hour))
	resp, err := svc.Refresh(context.Background(), tok)
	if err != nil {
		t.Fatalf("a legacy token inside the cap must refresh: %v", err)
	}
	if got := claimsOf(t, resp.Token).Ost; got != loginBase().Unix() {
		t.Fatalf("the legacy token's own iat must become the anchor: got %d want %d", got, loginBase().Unix())
	}

	// And past the cap measured from that same iat, it is refused.
	setNow(loginBase().Add(sessionMaxAge + time.Hour))
	if _, err := svc.Refresh(context.Background(), resp.Token); err == nil {
		t.Fatal("a legacy session must also die at the cap")
	}
}

// -- the route ----------------------------------------------------------------

// The route is registered, is guarded, and answers a PLAIN 401 for every refusal, no hint about
// whether the token was bad, the user gone, or the session simply too old.
func TestRefreshRoute_GuardedAndAnswersAPlain401(t *testing.T) {
	svc, store, setNow := refreshFixture(t)
	svc.WithConfig(Config{ID: "acme", DealerName: "Acme Yard"})
	h := NewHandler(svc, nil)
	tok := login(t, svc)

	call := func(bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/portal/v1/token/refresh", nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		h.refreshToken(w, r)
		return w
	}

	// No token at all → 401 from the guard.
	if w := call(""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated refresh = %d, want 401", w.Code)
	}
	// A live session → 200 with a token.
	setNow(loginBase().Add(time.Hour))
	w := call(tok)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var body RefreshResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Token == "" || body.User.ID != "u1" || body.Config.DealerName != "Acme Yard" {
		t.Fatalf("refresh response must carry {token, user, config}: %+v", body)
	}
	if body.SessionExpiresAt.IsZero() || body.ExpiresAt.IsZero() {
		t.Fatalf("refresh response must carry both deadlines: %+v", body)
	}

	// Past the cap → 401, and the body must not explain which refusal it was. The session is slid
	// up to the deadline first, so the refusal under test is the CAP and not an expired token
	// (which would give the same 401 for a different reason and prove nothing).
	live := slideTo(t, svc, setNow, body.Token, loginBase().Add(sessionMaxAge-time.Minute))
	setNow(loginBase().Add(sessionMaxAge + time.Minute))
	capped := call(live)
	if capped.Code != http.StatusUnauthorized {
		t.Fatalf("refresh past the cap = %d, want 401", capped.Code)
	}
	if s := strings.ToLower(capped.Body.String()); strings.Contains(s, "session") || strings.Contains(s, "expired") || strings.Contains(s, "cap") {
		t.Fatalf("the 401 body must not say WHY: %q", capped.Body.String())
	}

	// A deactivated user gets the same 401 from the same route.
	u, _ := store.GetUser(context.Background(), "u1")
	u.Status = "inactive"
	store.addUser(u)
	setNow(loginBase().Add(time.Hour))
	if w := call(tok); w.Code != http.StatusUnauthorized {
		t.Fatalf("deactivated refresh = %d, want 401", w.Code)
	}
}
