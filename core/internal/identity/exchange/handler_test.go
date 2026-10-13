package exchange

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

// ---- gate 1: the body ----

func TestGate1BodyShapeAnswers400WithItsOwnWordsAndCallsNothing(t *testing.T) {
	f := newFx(t)
	good := f.goodJWT()
	long := strings.Repeat("a", authz.MaxConsoleJWTLen+1)
	cases := map[string]string{
		"not json":         `nope`,
		"empty":            ``,
		"array":            `[]`,
		"unknown field":    `{"jwt":"` + good + `","org":"acme","role":"x"}`,
		"trailing value":   `{"jwt":"` + good + `","org":"acme"} {}`,
		"missing jwt":      `{"org":"acme"}`,
		"missing org":      `{"jwt":"` + good + `"}`,
		"jwt not a number": `{"jwt":5,"org":"acme"}`,
		"jwt two parts":    `{"jwt":"aaa.bbb","org":"acme"}`,
		"jwt bad chars":    `{"jwt":"aaa.b b.ccc","org":"acme"}`,
		"jwt empty part":   `{"jwt":"aaa..ccc","org":"acme"}`,
		"jwt too long":     `{"jwt":"` + long + `.b.c","org":"acme"}`,
		"org upper case":   `{"jwt":"` + good + `","org":"Acme"}`,
		"org colon":        `{"jwt":"` + good + `","org":"a:b"}`,
		"org too long":     `{"jwt":"` + good + `","org":"` + strings.Repeat("a", 33) + `"}`,
		"body oversized":   `{"jwt":"` + good + `","org":"acme","x":"` + strings.Repeat("y", 20000) + `"}`,
	}
	for name, body := range cases {
		rec := f.postRaw(body, "198.51.100.7:1")
		f.assertRefusal(rec, http.StatusBadRequest, nil)
		for _, shared := range [][]byte{bodyU, bodyF, bodyV} {
			if string(shared) == rec.Body.String() {
				t.Errorf("%s: gate 1 used a shared body", name)
			}
		}
		if strings.Contains(rec.Body.String(), "X-Org") {
			t.Errorf("%s: gate 1 uses the header words", name)
		}
		if !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("%s: no error word", name)
		}
	}
	if f.console.accountHit != 0 || f.claims.calls != 0 {
		t.Fatal("a shape refusal reached the console or the claim store")
	}
}

// ---- gate 2: the rate limits ----

func TestGate2AddressLimitAnswers429BeforeAnyConsoleCall(t *testing.T) {
	f := newFx(t)
	f.cfg.AddressLimit = ratelimit.NewAddressLimit(2, time.Minute, f.cfg.Now)
	for i := 0; i < 2; i++ {
		if rec := f.post(f.goodJWT(), fxOrgSlug); rec.Code != http.StatusOK {
			t.Fatalf("call %d = %d %s", i, rec.Code, rec.Body)
		}
	}
	f.audit.rows = nil
	before := f.console.accountHit
	rec := f.post(f.goodJWT(), fxOrgSlug)
	if rec.Code != http.StatusTooManyRequests || strings.Contains(rec.Body.String(), "token") {
		t.Fatalf("third call = %d %s", rec.Code, rec.Body)
	}
	if f.console.accountHit != before {
		t.Fatal("the limited call reached the console")
	}
	if len(f.audit.rows) != 0 {
		t.Fatal("the limited call wrote audit")
	}
}

func TestGate2TheForwardedHeaderCountsOnlyFromATrustedPeer(t *testing.T) {
	f := newFx(t)
	trusted, _ := ratelimit.ParseTrustedProxies("10.0.0.5")
	f.cfg.Trusted = trusted
	f.cfg.AddressLimit = ratelimit.NewAddressLimit(1, time.Minute, f.cfg.Now)
	h := f.build()
	send := func(peer, xff string) int {
		body := `{"jwt":"` + f.goodJWT() + `","org":"acme"}`
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
		r.RemoteAddr = peer
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if c := send("10.0.0.5:1", "203.0.113.1"); c != 200 {
		t.Fatalf("first = %d", c)
	}
	if c := send("10.0.0.5:2", "203.0.113.2"); c != 200 {
		t.Fatalf("a different client behind the proxy = %d", c)
	}
	if c := send("10.0.0.5:3", "203.0.113.1"); c != http.StatusTooManyRequests {
		t.Fatalf("the same client behind the proxy = %d", c)
	}
	// An untrusted peer cannot choose its bucket with the header.
	if c := send("198.51.100.9:1", "203.0.113.50"); c != 200 {
		t.Fatalf("untrusted first = %d", c)
	}
	if c := send("198.51.100.9:2", "203.0.113.51"); c != http.StatusTooManyRequests {
		t.Fatalf("untrusted second = %d, the header must not pick the bucket", c)
	}
}

func TestGate2UserLimitAfterGate3AnswersBefore3b(t *testing.T) {
	f := newFx(t)
	f.cfg.UserLimit = ratelimit.NewAddressLimit(1, time.Minute, f.cfg.Now)
	if rec := f.post(f.goodJWT(), fxOrgSlug); rec.Code != 200 {
		t.Fatalf("first = %d", rec.Code)
	}
	rows := len(f.audit.rows)
	claims := f.claims.calls
	rec := f.post(f.goodJWT(), fxOrgSlug)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second = %d %s", rec.Code, rec.Body)
	}
	if f.claims.calls != claims || len(f.audit.rows) != rows {
		t.Fatal("the user limit did not stop the request before the claim")
	}
	// Another person is unaffected.
	other := f.jwt("other_user", "sess_1", f.now.Add(10*time.Minute), accountJSON("other_user", "o@futurebuild.ai", true, true))
	if rec := f.post(other, fxOrgSlug); rec.Code == http.StatusTooManyRequests {
		t.Fatal("another user hit the first user's limit")
	}
}

func TestDefaultAllowancesAreNamedConstants(t *testing.T) {
	if AddressAllowance <= UserAllowance || UserAllowance < 3 || LimitWindow <= 0 {
		t.Fatalf("allowances %d / %d / %v", AddressAllowance, UserAllowance, LimitWindow)
	}
}

// ---- gate 3: the console account ----

func TestGate3ConsoleRefusalIs401AndFailuresAre503(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(f *fx)
		status int
		body   []byte
	}{
		{"console refuses the JWT", func(f *fx) { f.console.accountSt = 401 }, 401, bodyU},
		{"console 500", func(f *fx) { f.console.accountSt = 500 }, 503, bodyV},
		{"console 503", func(f *fx) { f.console.accountSt = 503 }, 503, bodyV},
		{"console 429", func(f *fx) { f.console.accountSt = 429 }, 503, bodyV},
		{"console 404", func(f *fx) { f.console.accountSt = 404 }, 503, bodyV},
		{"console 3xx", func(f *fx) { f.console.accountSt = 302 }, 503, bodyV},
		{"unknown JWT", func(f *fx) { f.console.accounts = map[string]string{} }, 401, bodyU},
		{"account body is junk", func(f *fx) { f.console.accounts[""] = "" }, 0, nil},
	}
	for _, c := range cases {
		if c.status == 0 {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			f := newFx(t)
			j := f.goodJWT()
			c.mutate(f)
			f.assertRefusal(f.post(j, fxOrgSlug), c.status, c.body)
			if f.claims.calls != 0 {
				t.Fatal("a gate 3 refusal reached the claim store")
			}
			if f.members.calls != 0 {
				t.Fatal("a gate 3 refusal reached the membership read")
			}
		})
	}
}

func TestGate3AccountShapesThatAreNotAnAccount(t *testing.T) {
	cases := map[string]struct {
		account string
		status  int
		body    []byte
	}{
		"junk body":           {`<html>`, 503, bodyV},
		"no verification":     {`{"$id":"` + fxUserID + `","email":"a@futurebuild.ai","status":true}`, 503, bodyV},
		"id with a colon":     {accountJSON("persona:x", "a@futurebuild.ai", true, true), 401, bodyU},
		"id too long":         {accountJSON(strings.Repeat("a", 37), "a@futurebuild.ai", true, true), 401, bodyU},
		"empty id":            {accountJSON("", "a@futurebuild.ai", true, true), 401, bodyU},
		"id differs from JWT": {accountJSON("someone_else", "a@futurebuild.ai", true, true), 401, bodyU},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFx(t)
			j := f.jwt(fxUserID, "sess_1", f.now.Add(10*time.Minute), c.account)
			f.assertRefusal(f.post(j, fxOrgSlug), c.status, c.body)
			if f.claims.calls != 0 {
				t.Fatal("reached the claim store")
			}
		})
	}
}

func TestGate3TimeoutIs503(t *testing.T) {
	f := newFx(t)
	j := f.goodJWT()
	f.console.delay = 400 * time.Millisecond
	client, _ := authz.NewConsoleClient(f.console.srv.URL, fxProject, fxKey, &http.Client{Timeout: 50 * time.Millisecond})
	f.cfg.Console = client
	f.assertRefusal(f.post(j, fxOrgSlug), 503, bodyV)
}

func TestGate3RedirectIs503AndTheJWTIsNotForwarded(t *testing.T) {
	var hits int32
	var forwarded atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		forwarded.Store(r.Header.Get("X-Appwrite-JWT"))
	}))
	defer target.Close()
	f := newFx(t)
	j := f.goodJWT()
	f.console.redirectTo = target.URL + "/account"
	f.assertRefusal(f.post(j, fxOrgSlug), 503, bodyV)
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("the redirect target was reached with the JWT %q", forwarded.Load())
	}
}

// ---- gate 3b: the claim ----

func TestGate3bClaimRefusals(t *testing.T) {
	t.Run("no exp", func(t *testing.T) {
		f := newFx(t)
		tok := seg(`{"alg":"HS256"}`) + "." + seg(`{"sessionId":"sess_1","userId":"`+fxUserID+`"}`) + ".c2ln"
		f.console.accounts[tok] = accountJSON(fxUserID, fxEmail, true, true)
		f.assertRefusal(f.post(tok, fxOrgSlug), 401, bodyU)
	})
	t.Run("string exp", func(t *testing.T) {
		f := newFx(t)
		tok := seg(`{"alg":"HS256"}`) + "." + seg(`{"exp":"1999999999","sessionId":"sess_1","userId":"`+fxUserID+`"}`) + ".c2ln"
		f.console.accounts[tok] = accountJSON(fxUserID, fxEmail, true, true)
		f.assertRefusal(f.post(tok, fxOrgSlug), 401, bodyU)
	})
	t.Run("exp beyond the bound", func(t *testing.T) {
		f := newFx(t)
		j := f.jwt(fxUserID, "sess_1", f.now.Add(identity.MinJWTClaimBound+time.Hour), accountJSON(fxUserID, fxEmail, true, true))
		f.assertRefusal(f.post(j, fxOrgSlug), 401, bodyU)
	})
	t.Run("claim store error", func(t *testing.T) {
		f := newFx(t)
		f.claims.err = errBoom
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 503, bodyV)
	})
	t.Run("replay", func(t *testing.T) {
		f := newFx(t)
		j := f.goodJWT()
		if rec := f.post(j, fxOrgSlug); rec.Code != 200 {
			t.Fatalf("first = %d", rec.Code)
		}
		rows := len(f.audit.rows)
		minted := f.minter.calls
		rec := f.post(j, fxOrgSlug)
		if rec.Code != 401 || rec.Body.String() != string(bodyU) {
			t.Fatalf("replay = %d %s", rec.Code, rec.Body)
		}
		if len(f.audit.rows) != rows || f.minter.calls != minted {
			t.Fatal("a replay wrote an audit row or minted")
		}
		if f.members.calls != 1 {
			t.Fatal("a replay reached the membership read")
		}
	})
}

func TestGate3bConcurrentPresentationsOfOneJWTMintExactlyOnce(t *testing.T) {
	f := newFx(t)
	j := f.goodJWT()
	f.cfg.AddressLimit = ratelimit.NewAddressLimit(1000, time.Minute, f.cfg.Now)
	f.cfg.UserLimit = ratelimit.NewAddressLimit(1000, time.Minute, f.cfg.Now)
	f.build()
	var ok int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.post(j, fxOrgSlug).Code == 200 {
				atomic.AddInt32(&ok, 1)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || f.minter.calls != 1 || len(f.audit.rows) != 1 {
		t.Fatalf("%d answers, %d mints, %d audit rows, want one each", ok, f.minter.calls, len(f.audit.rows))
	}
}

// ---- gate 4: the account and the allow lists ----

func TestGate4Refusals(t *testing.T) {
	cases := map[string]struct {
		account string
		domains []string
		addrs   []string
	}{
		"email not verified":          {accountJSON(fxUserID, fxEmail, false, true), []string{"futurebuild.ai"}, nil},
		"account not active":          {accountJSON(fxUserID, fxEmail, true, false), []string{"futurebuild.ai"}, nil},
		"domain outside the lists":    {accountJSON(fxUserID, "x@elsewhere.example", true, true), []string{"futurebuild.ai"}, nil},
		"subdomain is not the domain": {accountJSON(fxUserID, "x@mail.futurebuild.ai", true, true), []string{"futurebuild.ai"}, nil},
		"suffix trick":                {accountJSON(fxUserID, "x@notfuturebuild.ai", true, true), []string{"futurebuild.ai"}, nil},
		"two at signs":                {accountJSON(fxUserID, "x@evil.example@futurebuild.ai", true, true), []string{"futurebuild.ai"}, nil},
		"no local part":               {accountJSON(fxUserID, "@futurebuild.ai", true, true), []string{"futurebuild.ai"}, nil},
		"empty email":                 {accountJSON(fxUserID, "", true, true), []string{"futurebuild.ai"}, nil},
		"empty lists refuse all":      {accountJSON(fxUserID, fxEmail, true, true), nil, nil},
		"address list near miss":      {accountJSON(fxUserID, "other@elsewhere.example", true, true), nil, []string{"someone@elsewhere.example"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFx(t)
			f.cfg.AllowedDomains, f.cfg.AllowedAddresses = c.domains, c.addrs
			j := f.jwt(fxUserID, "sess_1", f.now.Add(10*time.Minute), c.account)
			f.assertRefusal(f.post(j, fxOrgSlug), 403, bodyF)
			if f.members.calls != 0 || len(f.methods.calls) != 0 {
				t.Fatal("a gate 4 refusal went on to a later gate")
			}
		})
	}
}

func TestGate4AllowListsMatchCaseInsensitivelyAndExactly(t *testing.T) {
	f := newFx(t)
	f.cfg.AllowedDomains = []string{"FutureBuild.AI"}
	if rec := f.post(f.goodJWT(), fxOrgSlug); rec.Code != 200 {
		t.Fatalf("domain, mixed case = %d %s", rec.Code, rec.Body)
	}
	g := newFx(t)
	g.cfg.AllowedDomains = nil
	g.cfg.AllowedAddresses = []string{"PERSON@futurebuild.AI"}
	if rec := g.post(g.goodJWT(), fxOrgSlug); rec.Code != 200 {
		t.Fatalf("address, mixed case = %d %s", rec.Code, rec.Body)
	}
}

func TestParseAllowList(t *testing.T) {
	got := ParseAllowList(" FutureBuild.ai , ,hardscapehouse.com,")
	if len(got) != 2 || got[0] != "futurebuild.ai" || got[1] != "hardscapehouse.com" {
		t.Fatalf("got %v", got)
	}
	if len(ParseAllowList("")) != 0 || len(ParseAllowList("  ")) != 0 {
		t.Fatal("blank list is not empty")
	}
}

// ---- gate 4b: the sign in method seam ----

func TestGate4bSeamRunsAfterGate4AndNotBefore(t *testing.T) {
	f := newFx(t)
	j := f.jwt(fxUserID, "sess_1", f.now.Add(10*time.Minute), accountJSON(fxUserID, fxEmail, false, true))
	f.assertRefusal(f.post(j, fxOrgSlug), 403, bodyF)
	if len(f.methods.calls) != 0 {
		t.Fatal("the seam ran for a request gate 4 refused")
	}
	g := newFx(t)
	if rec := g.post(g.goodJWT(), fxOrgSlug); rec.Code != 200 {
		t.Fatalf("= %d %s", rec.Code, rec.Body)
	}
	if len(g.methods.calls) != 1 || g.methods.calls[0] != "email" {
		t.Fatalf("seam calls = %v, want one with the session's provider", g.methods.calls)
	}
}

func TestGate4bSeamRefusalsAndErrors(t *testing.T) {
	f := newFx(t)
	f.methods.allowed = false
	f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 403, bodyF)
	g := newFx(t)
	g.methods.err = errBoom
	g.assertRefusal(g.post(g.goodJWT(), fxOrgSlug), 503, bodyV)
}

// ---- gate 5 and 5b: the Team and the anchor ----

func TestGate5MembershipRefusals(t *testing.T) {
	f := newFx(t)
	f.members.member = false
	f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 403, bodyF)
	g := newFx(t)
	g.members.err = errBoom
	g.assertRefusal(g.post(g.goodJWT(), fxOrgSlug), 503, bodyV)
	if g.staff.linked && g.orgs.err != nil {
		t.Fatal("unreachable")
	}
}

// The exchange reads the UNCACHED source: a removal between two exchanges
// inside the cache window takes effect at once, while the cache the
// per request gate sits behind still answers the old truth.
func TestGate5ReadsMembershipUncached(t *testing.T) {
	f := newFx(t)
	cached := authz.NewCachedMembership(f.members, time.Minute)
	if ok, _ := cached.OrgMember(context.Background(), fxUserID, fxOrgSlug); !ok {
		t.Fatal("cache priming failed")
	}
	if rec := f.post(f.goodJWT(), fxOrgSlug); rec.Code != 200 {
		t.Fatalf("first exchange = %d", rec.Code)
	}
	f.members.set(false)
	if ok, _ := cached.OrgMember(context.Background(), fxUserID, fxOrgSlug); !ok {
		t.Fatal("the cache should still be inside its window")
	}
	callsBefore := f.members.calls
	rec := f.post(f.goodJWT(), fxOrgSlug)
	f.audit.rows = nil
	if rec.Code != 403 || rec.Body.String() != string(bodyF) {
		t.Fatalf("second exchange = %d %s, want the removal to take effect at once", rec.Code, rec.Body)
	}
	if f.members.calls != callsBefore+1 {
		t.Fatal("the exchange did not read the source")
	}
}

func TestGate5bAnchorRefusals(t *testing.T) {
	f := newFx(t)
	f.orgs.ok = false
	f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 403, bodyF)
	g := newFx(t)
	g.orgs.err = errBoom
	g.assertRefusal(g.post(g.goodJWT(), fxOrgSlug), 503, bodyV)
	// No anchor and no membership are the same bytes.
	h := newFx(t)
	h.members.member = false
	rec := h.post(h.goodJWT(), fxOrgSlug)
	if rec.Body.String() != string(bodyF) {
		t.Fatal("no membership differs from no anchor")
	}
}

// ---- gate 6: the staff row ----

func TestGate6StaffRowRefusals(t *testing.T) {
	cases := map[string]struct {
		mutate func(s *fakeStaff)
		status int
		body   []byte
	}{
		"no linked users row":    {func(s *fakeStaff) { s.linked = false; s.linkedID = "" }, 403, bodyF},
		"linked row inactive":    {func(s *fakeStaff) { s.linked = false }, 403, bodyF},
		"resolver says inactive": {func(s *fakeStaff) { s.authorityErr = identity.ErrStaffUserInactive }, 403, bodyF},
		"no grant":               {func(s *fakeStaff) { s.authorityOK = false }, 403, bodyF},
		"linkage read error":     {func(s *fakeStaff) { s.linkedErr = errBoom }, 503, bodyV},
		"authority read error":   {func(s *fakeStaff) { s.authorityErr = errBoom }, 503, bodyV},
		"grant shape defect":     {func(s *fakeStaff) { s.authorityErr = identity.ErrGrantShape }, 503, bodyV},
		"role label read error":  {func(s *fakeStaff) { s.labelErr = errBoom }, 503, bodyV},
		"role label missing":     {func(s *fakeStaff) { s.labelOK = false }, 503, bodyV},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFx(t)
			c.mutate(f.staff)
			f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), c.status, c.body)
		})
	}
}

// ---- gate 7: the ceiling ----

func TestGate7DriverWithAnUnreadableKnobIs503(t *testing.T) {
	for name, knob := range map[string]fakeKnob{
		"error":          {err: errBoom},
		"zero hours":     {hours: 0},
		"above contract": {hours: 169},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFx(t)
			f.staff.authority = identity.StaffAuthority{RoleID: identity.RoleDriver, Tier: 3, Scope: "branch"}
			f.knob = knob
			f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 503, bodyV)
			if f.console.sessionHit != 0 {
				t.Fatal("the session read ran before the ceiling")
			}
		})
	}
}

func TestGate7AStaffRoleNeverReadsTheKnob(t *testing.T) {
	f := newFx(t)
	f.knob = fakeKnob{err: errBoom}
	if rec := f.post(f.goodJWT(), fxOrgSlug); rec.Code != 200 {
		t.Fatalf("= %d %s", rec.Code, rec.Body)
	}
}

// ---- gate 7b: the sign in age ----

func TestGate7bSessionAgeAndLookup(t *testing.T) {
	t.Run("older than the ceiling", func(t *testing.T) {
		f := newFx(t)
		f.console.sessions = sessionsJSON(sessionJSON("sess_1", f.now.Add(-13*time.Hour), "email"))
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 401, bodyU)
	})
	t.Run("driver older than the knob", func(t *testing.T) {
		f := newFx(t)
		f.staff.authority = identity.StaffAuthority{RoleID: identity.RoleDriver, Tier: 3, Scope: "branch"}
		f.knob = fakeKnob{hours: 2}
		f.console.sessions = sessionsJSON(sessionJSON("sess_1", f.now.Add(-3*time.Hour), "email"))
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 401, bodyU)
	})
	t.Run("less than the floor left", func(t *testing.T) {
		f := newFx(t)
		f.console.sessions = sessionsJSON(sessionJSON("sess_1", f.now.Add(-12*time.Hour+30*time.Second), "email"))
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 401, bodyU)
	})
	t.Run("no matching session", func(t *testing.T) {
		f := newFx(t)
		f.console.sessions = sessionsJSON(sessionJSON("other", f.now.Add(-time.Minute), "email"))
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 503, bodyV)
	})
	t.Run("empty listing", func(t *testing.T) {
		f := newFx(t)
		f.console.sessions = sessionsJSON()
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 503, bodyV)
	})
	t.Run("JWT names no session", func(t *testing.T) {
		f := newFx(t)
		j := f.jwt(fxUserID, "", f.now.Add(10*time.Minute), accountJSON(fxUserID, fxEmail, true, true))
		f.assertRefusal(f.post(j, fxOrgSlug), 503, bodyV)
	})
	t.Run("created time malformed", func(t *testing.T) {
		f := newFx(t)
		f.console.sessions = `{"total":1,"sessions":[{"$id":"sess_1","$createdAt":"soon","provider":"email"}]}`
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 503, bodyV)
	})
	t.Run("created time in the future", func(t *testing.T) {
		f := newFx(t)
		f.console.sessions = sessionsJSON(sessionJSON("sess_1", f.now.Add(2*time.Hour), "email"))
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 503, bodyV)
	})
	t.Run("sessions 5xx", func(t *testing.T) {
		f := newFx(t)
		f.console.sessionsSt = 500
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 503, bodyV)
	})
	t.Run("sessions truncated", func(t *testing.T) {
		f := newFx(t)
		f.console.sessions = `{"total":5,"sessions":[` + sessionJSON("sess_1", f.now.Add(-time.Minute), "email") + `]}`
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 503, bodyV)
	})
	t.Run("sessions redirect", func(t *testing.T) {
		f := newFx(t)
		f.console.sessionsSt = 307
		f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 503, bodyV)
	})
}

// ---- gate 8 and the token ----

func TestGate8AuditRowIsWrittenBeforeTheTokenAndFailureMintsNothing(t *testing.T) {
	f := newFx(t)
	f.audit.err = errBoom
	f.assertRefusal(f.post(f.goodJWT(), fxOrgSlug), 503, bodyV)
	g := newFx(t)
	if rec := g.post(g.goodJWT(), fxOrgSlug); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	var seq []string
	for _, e := range g.order {
		if e == "audit" || e == "mint" {
			seq = append(seq, e)
		}
	}
	if strings.Join(seq, ",") != "audit,mint" {
		t.Fatalf("order = %v, want the audit row first", seq)
	}
}

func TestGate8AuditRowContent(t *testing.T) {
	f := newFx(t)
	if rec := f.post(f.goodJWT(), fxOrgSlug); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if len(f.audit.rows) != 1 {
		t.Fatalf("%d audit rows", len(f.audit.rows))
	}
	a := f.audit.rows[0]
	if a.Action != "AUTH_EXCHANGE" || a.ActorID != fxUsersRow || a.EntityID != fxUsersRow || a.EntityType == "" {
		t.Fatalf("row = %+v", a)
	}
	if a.Extra["actor_subject"] != fxUserID {
		t.Fatalf("extra = %v", a.Extra)
	}
	var _ audit.SensitiveAccess = a
}

func TestTokenWindowIsTheMinimumOfCeilingTTLAndRemainingSignInAge(t *testing.T) {
	cases := []struct {
		name    string
		role    string
		hours   int
		ttl     time.Duration
		created time.Duration // age of the console session
		want    time.Duration
	}{
		{"ttl is smallest", "yard_team", 0, time.Hour, 10 * time.Minute, time.Hour},
		{"staff ceiling smallest", "yard_team", 0, 20 * time.Hour, 10 * time.Minute, 12*time.Hour - 10*time.Minute},
		{"remaining age smallest", "yard_team", 0, time.Hour, 11*time.Hour + 30*time.Minute, 30 * time.Minute},
		{"driver knob", identity.RoleDriver, 2, 90 * time.Minute, 30 * time.Minute, 90 * time.Minute},
		{"driver remaining", identity.RoleDriver, 2, 90 * time.Minute, 100 * time.Minute, 20 * time.Minute},
		{"exactly the floor", "yard_team", 0, time.Hour, 12*time.Hour - time.Minute, time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFx(t)
			f.staff.authority = identity.StaffAuthority{RoleID: c.role, Tier: 3, Scope: "branch"}
			f.knob = fakeKnob{hours: c.hours}
			f.cfg.TokenTTL = c.ttl
			f.console.sessions = sessionsJSON(sessionJSON("sess_1", f.now.Add(-c.created), "email"))
			rec := f.post(f.goodJWT(), fxOrgSlug)
			if rec.Code != 200 {
				t.Fatalf("= %d %s", rec.Code, rec.Body)
			}
			a := decodeAnswer(t, rec)
			iat, exp := tokenWindow(t, a.Token)
			if got := time.Duration(exp-iat) * time.Second; got != c.want {
				t.Fatalf("exp - iat = %v, want %v", got, c.want)
			}
			if a.ExpiresAtMs != exp*1000 {
				t.Fatalf("expiresAtMs = %d, exp = %d", a.ExpiresAtMs, exp)
			}
			if c.hours == 0 {
				return
			}
		})
	}
}

func TestAnswerCarriesTheUserBlock(t *testing.T) {
	f := newFx(t)
	f.staff.authority = identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch"}
	f.staff.label = "Branch Manager"
	rec := f.post(f.goodJWT(), fxOrgSlug)
	if rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	a := decodeAnswer(t, rec)
	u := a.User
	if u.UserID != fxUserID || u.Email != fxEmail || u.DisplayName != "Display Name" ||
		u.RoleCode != "branch_manager" || u.RoleName != "Branch Manager" || u.Tier != 2 || u.Scope != "BRANCH" {
		t.Fatalf("user = %+v", u)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	for k := range raw {
		if k != "token" && k != "expiresAtMs" && k != "user" {
			t.Errorf("unexpected key %q", k)
		}
	}
	// A two role person is judged by the winning role the resolver names.
	g := newFx(t)
	g.staff.authority = identity.StaffAuthority{RoleID: "general_manager", Tier: 1, Scope: "global"}
	g.staff.label = "General Manager"
	b := decodeAnswer(t, g.post(g.goodJWT(), fxOrgSlug))
	if b.User.RoleCode != "general_manager" || b.User.Scope != "GLOBAL" || b.User.Tier != 1 {
		t.Fatalf("winner = %+v", b.User)
	}
}

// The happy path through the real verifier and authz.Gate.
func TestMintedTokenIsAcceptedByTheRealVerifierAndStaffGate(t *testing.T) {
	f := newFx(t)
	pub, err := identity.NewSessionKeySet(&f.key.PublicKey, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := identity.NewVerifier(identity.Config{KeySource: pub, Issuer: fxIssuer, StaffAudience: fxAudience, TenantAudience: "hh-tenant"})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := authz.NewGate(verifier, f.members, f.orgs)
	if err != nil {
		t.Fatal(err)
	}
	probe := gate.StaffScoped(func(w http.ResponseWriter, r *http.Request, s authz.StaffScope) {
		_, _ = w.Write([]byte(s.PlatformUserID + "|" + s.OrgSlug))
	})
	a := decodeAnswer(t, f.post(f.goodJWT(), fxOrgSlug))
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("X-Org", fxOrgSlug)
	rec := httptest.NewRecorder()
	probe.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != fxUserID+"|"+fxOrgSlug {
		t.Fatalf("probe = %d %s", rec.Code, rec.Body)
	}
	// Same token without X-Org is the gate's 400, proving the org is not in the token.
	req = httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+a.Token)
	rec = httptest.NewRecorder()
	probe.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("without X-Org = %d", rec.Code)
	}
}

// ---- failure codes, counters, logs ----

func TestRefusalsGoToAStaticCodeAndACounterNeverTheWire(t *testing.T) {
	f := newFx(t)
	f.members.member = false
	j := f.goodJWT()
	rec := f.post(j, fxOrgSlug)
	if rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	if got := f.h.GateFailures()[CodeNotMember]; got != 1 {
		t.Fatalf("counter %q = %d (%v)", CodeNotMember, got, f.h.GateFailures())
	}
	logs := f.logBuf.String()
	if !strings.Contains(logs, CodeNotMember) {
		t.Fatalf("log lacks the code: %s", logs)
	}
	for _, secret := range []string{j, fxEmail, fxUserID, fxKey, "Person"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log carries %q", secret)
		}
	}
	if strings.Contains(rec.Body.String(), CodeNotMember) {
		t.Fatal("the code reached the wire")
	}
}

func TestEveryGateHasADistinctStaticCode(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range AllCodes() {
		if c == "" || seen[c] {
			t.Fatalf("code %q empty or repeated", c)
		}
		seen[c] = true
	}
	if len(seen) < 20 {
		t.Fatalf("only %d codes", len(seen))
	}
}

// ---- construction ----

func TestNewRefusesMissingCollaborators(t *testing.T) {
	base := newFx(t).cfg
	mut := map[string]func(c *Config){
		"console": func(c *Config) { c.Console = nil },
		"members": func(c *Config) { c.Members = nil },
		"orgs":    func(c *Config) { c.Orgs = nil },
		"claims":  func(c *Config) { c.Claims = nil },
		"staff":   func(c *Config) { c.Staff = nil },
		"knob":    func(c *Config) { c.Knob = nil },
		"audit":   func(c *Config) { c.Audit = nil },
		"minter":  func(c *Config) { c.Minter = nil },
		"methods": func(c *Config) { c.Methods = nil },
		"ttl":     func(c *Config) { c.TokenTTL = 0 },
	}
	for name, m := range mut {
		c := base
		m(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: constructed without it", name)
		}
	}
}
