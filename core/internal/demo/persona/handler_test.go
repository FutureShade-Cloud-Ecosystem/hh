//go:build demo

package persona

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

func enterable() []flows.Persona {
	var out []flows.Persona
	for _, p := range flows.Personas() {
		if !p.Contractor {
			out = append(out, p)
		}
	}
	return out
}

// ---- the happy path, both copies ----

func TestPartnerEntersAsEveryEnterablePersona(t *testing.T) {
	for _, p := range enterable() {
		f := newFx(t, CallerPartner)
		rec := f.enter(p.ID)
		a := decode(t, rec)
		sub, iat, exp := claims(t, a.Token)
		if sub != "persona:"+p.ID || a.User.UserID != sub {
			t.Fatalf("%s: sub %q user %q", p.ID, sub, a.User.UserID)
		}
		want := int64(12 * 3600)
		if p.Role == "driver" {
			want = 24 * 3600
		}
		if exp-iat != want {
			t.Fatalf("%s: window %d, want %d", p.ID, exp-iat, want)
		}
		if a.ExpiresAtMs != exp*1000 {
			t.Fatalf("%s: expiresAtMs %d exp %d", p.ID, a.ExpiresAtMs, exp)
		}
		if a.User.RoleCode != p.Role || a.User.RoleName != p.Label || a.User.Email != p.Email || a.User.Scope == "" || a.User.Tier == 0 {
			t.Fatalf("%s: user block %+v", p.ID, a.User)
		}
		if a.User.Scope != strings.ToUpper(a.User.Scope) {
			t.Fatalf("%s: scope %q", p.ID, a.User.Scope)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: cache control %q", p.ID, rec.Header().Get("Cache-Control"))
		}
	}
}

func TestOpenCopyEntersWithNoBearer(t *testing.T) {
	f := newFx(t, CallerOpen)
	a := decode(t, f.enterOpen("logistics-manager", fxAddr))
	if sub, _, _ := claims(t, a.Token); sub != "persona:logistics-manager" {
		t.Fatalf("sub %q", sub)
	}
	// A bearer on the open route is ignored, not judged.
	rec := f.do(`{"persona":"logistics-manager"}`, "Bearer junk", "198.51.100.8:1")
	decode(t, rec)
}

// ---- the token window (sub item 4) ----

func TestWindowIsTheRolesCeilingJudgedAtMint(t *testing.T) {
	f := newFx(t, CallerOpen)
	f.knob.hours = 5
	a := decode(t, f.enterOpen("driver", fxAddr))
	_, iat, exp := claims(t, a.Token)
	if exp-iat != 5*3600 {
		t.Fatalf("driver window %d, want the dealer knob's 5 hours", exp-iat)
	}
	calls := f.knob.calls
	decode(t, f.enterOpen("sales", "198.51.100.9:1"))
	if f.knob.calls != calls {
		t.Fatal("a staff persona read the driver knob")
	}
	// The yard manager is also a driver, but the winning role is the
	// yard manager's: its ceiling is the staff window, not the knob's.
	a = decode(t, f.enterOpen("yard-manager", "198.51.100.10:1"))
	if _, i, e := claims(t, a.Token); e-i != int64(identity.StaffSessionTTL/time.Second) {
		t.Fatalf("yard manager window %d", e-i)
	}
	if f.knob.calls != calls {
		t.Fatal("the yard manager read the driver knob")
	}
}

func TestUnreadableOrOutOfContractKnobIsTheShared503(t *testing.T) {
	for name, set := range map[string]func(k *fakeKnob){
		"error": func(k *fakeKnob) { k.err = errBoom }, "zero": func(k *fakeKnob) { k.hours = 0 },
		"169": func(k *fakeKnob) { k.hours = 169 },
	} {
		f := newFx(t, CallerOpen)
		set(f.knob)
		rec := f.enterOpen("driver", fxAddr)
		if rec.Code != 503 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		f.assertShared(rec, 503)
	}
}

func TestRoleDriftAndMissingAuthorityAre503(t *testing.T) {
	f := newFx(t, CallerOpen)
	f.staff.roleDrift = "general_manager"
	f.assertShared(f.enterOpen("sales", fxAddr), 503)
	f = newFx(t, CallerOpen)
	f.staff.authMissing = true
	f.assertShared(f.enterOpen("sales", fxAddr), 503)
	f = newFx(t, CallerOpen)
	f.staff.authErr = errBoom
	f.assertShared(f.enterOpen("sales", fxAddr), 503)
	f = newFx(t, CallerOpen)
	f.staff.personaOK = false
	f.assertShared(f.enterOpen("sales", fxAddr), 503)
	f = newFx(t, CallerOpen)
	f.staff.linkedErr = errBoom
	f.assertShared(f.enterOpen("sales", fxAddr), 503)
	f = newFx(t, CallerOpen)
	f.orgs.ok = false
	f.assertShared(f.enterOpen("sales", fxAddr), 503)
	f = newFx(t, CallerOpen)
	f.orgs.err = errBoom
	f.assertShared(f.enterOpen("sales", fxAddr), 503)
}

// ---- the caller rules, testers' copy (sub item 2) ----

func TestPartnerCopyRefusesWithoutAGoodBearer(t *testing.T) {
	f := newFx(t, CallerPartner)
	other, _ := identityMinterFor(t, "other")
	expired, err := f.minter.inner.MintAt(fxPartner, time.Minute, f.now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for name, hdr := range map[string]string{
		"none": "", "scheme": "Basic abc", "empty token": "Bearer ", "junk": "Bearer junk",
		"two tokens": "Bearer a b", "other key": "Bearer " + other, "expired": "Bearer " + expired,
	} {
		rec := f.do(`{"persona":"sales"}`, hdr, fxAddr)
		if name == "none" {
			rec = f.do(`{"persona":"sales"}`, "", fxAddr)
		}
		f.assertShared(rec, 401)
	}
}

func TestPartnerCopyRefusesAnUnlistedCaller(t *testing.T) {
	f := newFx(t, CallerPartner)
	rec := f.do(`{"persona":"sales"}`, "Bearer "+f.mintFor("66ffffffffffffffffffffff"), fxAddr)
	f.assertShared(rec, 403)
	// An empty list refuses every caller.
	f = newFx(t, CallerPartner)
	f.cfg.Partners = nil
	f.rebuild()
	f.assertShared(f.enter("sales"), 403)
	f.cfg.Partners = []string{"", " "}
	f.rebuild()
	f.assertShared(f.enter("sales"), 403)
}

func TestPartnerWithNoLinkedActiveRowIsRefused(t *testing.T) {
	f := newFx(t, CallerPartner)
	f.staff.partnerOK = false
	f.assertShared(f.enter("sales"), 403)
	f = newFx(t, CallerPartner)
	f.staff.linkedErr = errBoom
	f.assertShared(f.enter("sales"), 503)
}

// A persona token cannot chain, even when its subject is on the list.
func TestAPersonaTokenCannotChain(t *testing.T) {
	f := newFx(t, CallerPartner)
	f.cfg.Partners = []string{"persona:sales"}
	if _, err := New(f.cfg); err == nil {
		t.Fatal("a persona subject was accepted on the partner list")
	}
	f.cfg.Partners = []string{fxPartner}
	f.rebuild()
	a := decode(t, f.enter("sales"))
	rec := f.do(`{"persona":"hr"}`, "Bearer "+a.Token, fxAddr)
	if rec.Code != 403 || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("chain: %d %s", rec.Code, rec.Body)
	}
	if f.minter.calls != 1 {
		t.Fatalf("mint calls %d", f.minter.calls)
	}
}

func TestPartnerCopyRefusesOtherBearerClasses(t *testing.T) {
	for name, tok := range map[string]identity.Token{
		"tenant":  {Class: identity.ClassTenant, Subject: fxPartner, TenantID: "t"},
		"machine": {Class: identity.ClassMachine, Subject: fxPartner},
		"none":    {Class: identity.ClassNone, Subject: fxPartner},
		"nosub":   {Class: identity.ClassStaff},
	} {
		f := newFx(t, CallerPartner)
		fv := &fakeVerifier{tok: tok}
		f.cfg.Verifier = fv
		f.rebuild()
		f.assertShared(f.enter("sales"), 403)
		if fv.calls != 1 {
			t.Fatalf("%s: verifier calls %d", name, fv.calls)
		}
	}
}

func TestVerifierOperationalFailureIs503(t *testing.T) {
	f := newFx(t, CallerPartner)
	f.cfg.Verifier = &fakeVerifier{err: operationalError(t)}
	f.rebuild()
	f.assertShared(f.enter("sales"), 503)
}

// ---- the caller rules, public copy ----

func TestOpenCopyRateLimitsPerAddressAndPerAddressAndPersona(t *testing.T) {
	f := newFx(t, CallerOpen)
	for i := 0; i < AddressPersonaAllowance; i++ {
		decode(t, f.enterOpen("sales", fxAddr))
	}
	rec := f.enterOpen("sales", fxAddr)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("per address and persona: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "persona") {
		t.Fatalf("the 429 names the bucket: %s", rec.Body)
	}
	if f.minter.calls != AddressPersonaAllowance || len(f.audit.rows) != AddressPersonaAllowance {
		t.Fatalf("the refusal minted or wrote: %d %d", f.minter.calls, len(f.audit.rows))
	}
	// Another persona from the same address still fits the address allowance.
	decode(t, f.enterOpen("logistics-manager", fxAddr))
	// Another address is its own bucket for the same persona.
	decode(t, f.enterOpen("sales", "203.0.113.9:1"))
}

// The address allowance is the outer wall: spent through many personas, it
// refuses every persona from that address.
func TestOpenCopyAddressAllowanceBoundsEveryPersona(t *testing.T) {
	f := newFx(t, CallerOpen)
	f.cfg.AddressLimit = ratelimit.NewAddressLimit(3, time.Minute, func() time.Time { return f.now })
	f.rebuild()
	for _, id := range []string{"sales", "logistics-manager", "driver"} {
		decode(t, f.enterOpen(id, fxAddr))
	}
	if rec := f.enterOpen("yard-team", fxAddr); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("fourth persona from one address: %d", rec.Code)
	}
	if f.minter.calls != 3 {
		t.Fatalf("mints %d", f.minter.calls)
	}
	decode(t, f.enterOpen("yard-team", "203.0.113.9:1"))
}

// The refusals of an unknown persona spend the address allowance and not
// the persona's bucket, so guessing ids cannot lock a real persona out.
func TestUnknownPersonaDoesNotSpendAPersonaBucket(t *testing.T) {
	f := newFx(t, CallerOpen)
	for i := 0; i < AddressPersonaAllowance+2; i++ {
		f.enterOpen("nobody", fxAddr)
	}
	decode(t, f.enterOpen("sales", fxAddr))
}

func TestAddressLimitIsBeforeTheBodyAndTheVerifier(t *testing.T) {
	f := newFx(t, CallerPartner)
	fv := &fakeVerifier{err: errBoom}
	f.cfg.Verifier = fv
	f.cfg.AddressLimit = ratelimit.NewAddressLimit(2, time.Minute, func() time.Time { return f.now })
	f.rebuild()
	f.do(`x`, "Bearer a", fxAddr)
	f.do(`x`, "Bearer a", fxAddr)
	before := fv.calls
	rec := f.do(`{"persona":"sales"}`, "Bearer a", fxAddr)
	if rec.Code != 429 || fv.calls != before {
		t.Fatalf("code %d verifier calls %d->%d", rec.Code, before, fv.calls)
	}
}

func TestTrustedProxyHeaderCountsOnlyFromAListedPeer(t *testing.T) {
	f := newFx(t, CallerOpen)
	trusted, err := ratelimit.ParseTrustedProxies("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.Trusted = trusted
	f.rebuild()
	for i := 0; i < AddressPersonaAllowance; i++ {
		decode(t, f.do(`{"persona":"logistics-manager"}`, "", "127.0.0.1:9", "X-Forwarded-For", "203.0.113.50"))
	}
	if rec := f.do(`{"persona":"logistics-manager"}`, "", "127.0.0.1:9", "X-Forwarded-For", "203.0.113.50"); rec.Code != 429 {
		t.Fatalf("the named client was not limited: %d", rec.Code)
	}
	decode(t, f.do(`{"persona":"logistics-manager"}`, "", "127.0.0.1:9", "X-Forwarded-For", "203.0.113.51"))
	// From an unlisted peer the header is ignored: forging it buys nothing.
	f = newFx(t, CallerOpen)
	for i := 0; i < AddressPersonaAllowance; i++ {
		decode(t, f.do(`{"persona":"logistics-manager"}`, "", "192.0.2.1:9", "X-Forwarded-For", "203.0.113."+string(rune('1'+i%9))))
	}
	if rec := f.do(`{"persona":"logistics-manager"}`, "", "192.0.2.1:9", "X-Forwarded-For", "203.0.113.99"); rec.Code != 429 {
		t.Fatalf("a forged header got a fresh bucket: %d", rec.Code)
	}
}

// ---- unknown and contractor personas, the body ----

func TestUnknownAndContractorPersonasAreTheSharedForbiddenWithNoList(t *testing.T) {
	for _, id := range []string{"nobody", "Sales", "persona:sales", "", " sales", "sales ", "../x", "contractor-owner", "contractor-buyer", "contractor-ap", "contractor-field"} {
		for _, mode := range []CallerMode{CallerPartner, CallerOpen} {
			f := newFx(t, mode)
			var rec *httptest.ResponseRecorder
			if mode == CallerPartner {
				rec = f.enter(id)
			} else {
				rec = f.enterOpen(id, fxAddr)
			}
			f.assertShared(rec, 403)
			for _, p := range flows.Personas() {
				if strings.Contains(rec.Body.String(), p.ID) {
					t.Fatalf("%q: the answer names %q", id, p.ID)
				}
			}
		}
	}
}

// Every 403 of the route is byte identical: unlisted caller, unknown
// persona, contractor persona, chained token.
func TestEveryForbiddenIsByteIdentical(t *testing.T) {
	f := newFx(t, CallerPartner)
	a := f.rawBody(f.do(`{"persona":"sales"}`, "Bearer "+f.mintFor("66ffffffffffffffffffffff"), fxAddr))
	b := f.rawBody(f.enter("nobody"))
	c := f.rawBody(f.enter("contractor-ap"))
	if a != b || b != c || a != string(authz.BodyForbidden) {
		t.Fatalf("%q %q %q", a, b, c)
	}
}

func (f *fx) rawBody(rec *httptest.ResponseRecorder) string {
	f.t.Helper()
	if rec.Code != 403 {
		f.t.Fatalf("status %d", rec.Code)
	}
	return rec.Body.String()
}

func TestBodyShapeIs400AndCallsNothing(t *testing.T) {
	long := `{"persona":"` + strings.Repeat("a", 9000) + `"}`
	for name, body := range map[string]string{
		"empty": ``, "not json": `nope`, "array": `[]`, "unknown field": `{"persona":"sales","role":"hr"}`,
		"trailing": `{"persona":"sales"} {}`, "missing": `{}`, "number": `{"persona":5}`, "null": `{"persona":null}`,
		"oversized": long,
	} {
		f := newFx(t, CallerPartner)
		rec := f.do(body, "Bearer "+f.partner, fxAddr)
		if rec.Code != 400 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		for _, shared := range [][]byte{authz.BodyUnauthorized, authz.BodyForbidden, authz.BodyUnavailable} {
			if rec.Body.String() == string(shared) {
				t.Fatalf("%s: shared body on a 400", name)
			}
		}
		f.assertNothingMinted()
	}
}

// An unlisted caller with a malformed body learns nothing about the body
// shape: the caller refusal comes first.
func TestCallerIsJudgedBeforeTheBody(t *testing.T) {
	f := newFx(t, CallerPartner)
	f.assertShared(f.do(`nope`, "", fxAddr), 401)
	f = newFx(t, CallerPartner)
	f.assertShared(f.do(`nope`, "Bearer "+f.mintFor("66ffffffffffffffffffffff"), fxAddr), 403)
}

// ---- the audit row (sub item 5) ----

func TestPartnerAuditRowNamesThePartnerAndThePersona(t *testing.T) {
	f := newFx(t, CallerPartner)
	decode(t, f.enter("driver"))
	if len(f.audit.rows) != 1 {
		t.Fatalf("%d rows", len(f.audit.rows))
	}
	r := f.audit.rows[0]
	p, _ := flows.PersonaByID("driver")
	if r.Action != "AUTH_PERSONA_ENTRY" || r.EntityType != "auth.persona" || r.Purpose != "PERSONA_ENTRY" {
		t.Fatalf("row %+v", r)
	}
	if r.ActorID != fxPartnerRow || r.EntityID != p.UserID {
		t.Fatalf("actor %q entity %q", r.ActorID, r.EntityID)
	}
	if r.Extra["actor_subject"] != fxPartner || r.Extra["subject_ref"] != "persona:driver" {
		t.Fatalf("extra %v", r.Extra)
	}
	if _, ok := r.Extra["address_bucket"]; ok {
		t.Fatal("the testers' copy row carries an address bucket")
	}
	for k, v := range r.Extra {
		if s, ok := v.(string); ok && (strings.Contains(s, "198.51.100") || strings.Contains(s, "Bearer")) {
			t.Fatalf("extra %s holds %q", k, s)
		}
	}
	if len(f.order) != 2 || f.order[0] != "audit" || f.order[1] != "mint" {
		t.Fatalf("order %v", f.order)
	}
}

func TestPublicAuditRowNamesTheBucketAndThePersonaNeverTheAddress(t *testing.T) {
	f := newFx(t, CallerOpen)
	decode(t, f.enterOpen("logistics-manager", "198.51.100.7:1111"))
	decode(t, f.enterOpen("logistics-manager", "198.51.100.7:2222"))
	decode(t, f.enterOpen("logistics-manager", "198.51.100.8:1111"))
	if len(f.audit.rows) != 3 {
		t.Fatalf("%d rows", len(f.audit.rows))
	}
	b := func(i int) string {
		return f.audit.rows[i].MaskedRef
	}
	if b(0) == "" || b(0) != b(1) || b(0) == b(2) {
		t.Fatalf("buckets %q %q %q", b(0), b(1), b(2))
	}
	p, _ := flows.PersonaByID("logistics-manager")
	for _, r := range f.audit.rows {
		if r.Extra["subject_ref"] != "persona:logistics-manager" || r.EntityID != p.UserID || r.Action != "AUTH_PERSONA_ENTRY" {
			t.Fatalf("row %+v", r)
		}
		if r.ActorID != "" && r.ActorID != p.UserID {
			t.Fatalf("a public row names an actor %q", r.ActorID)
		}
		if _, ok := r.Extra["actor_subject"]; ok {
			t.Fatal("a public row names a partner")
		}
		for _, v := range r.Extra {
			if s, ok := v.(string); ok && strings.Contains(s, "198.51.100") {
				t.Fatalf("the raw address is in the row: %q", s)
			}
		}
		if strings.ContainsAny(b(0), "0123456789.:") {
			t.Fatalf("bucket %q holds characters the cleartext detector can redact", b(0))
		}
	}
	// The bucket is salted: another handler gives another bucket.
	g := newFx(t, CallerOpen)
	decode(t, g.enterOpen("logistics-manager", "198.51.100.7:1"))
	if g.audit.rows[0].MaskedRef == b(0) {
		t.Fatal("two handlers share a salt")
	}
}

func TestAuditFailureIs503AndNoToken(t *testing.T) {
	for _, mode := range []CallerMode{CallerPartner, CallerOpen} {
		f := newFx(t, mode)
		f.audit.err = errBoom
		var rec *httptest.ResponseRecorder
		if mode == CallerPartner {
			rec = f.enter("sales")
		} else {
			rec = f.enterOpen("sales", fxAddr)
		}
		if rec.Code != 503 || rec.Body.String() != string(authz.BodyUnavailable) {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		if f.minter.calls != 0 {
			t.Fatal("a token was minted after the audit failed")
		}
	}
}

func TestMintFailureIs503(t *testing.T) {
	f := newFx(t, CallerOpen)
	f.minter.err = errBoom
	rec := f.enterOpen("sales", fxAddr)
	if rec.Code != 503 || rec.Body.String() != string(authz.BodyUnavailable) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// ---- logs, counters, construction ----

func TestRefusalsGoToAStaticCodeNeverTheWire(t *testing.T) {
	f := newFx(t, CallerPartner)
	bearer := "Bearer " + f.mintFor("66ffffffffffffffffffffff")
	rec := f.do(`{"persona":"sales"}`, bearer, fxAddr)
	if rec.Code != 403 {
		t.Fatal(rec.Code)
	}
	if f.h.GateFailures()[CodeCallerNotListed] != 1 {
		t.Fatalf("counters %v", f.h.GateFailures())
	}
	logs := f.logBuf.String()
	if !strings.Contains(logs, CodeCallerNotListed) {
		t.Fatalf("log lacks the code: %s", logs)
	}
	for _, secret := range []string{bearer[7:], "66ffffffffffffffffffffff", fxPartner, "198.51.100.7", "persona:"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log carries %q", secret)
		}
	}
	if strings.Contains(rec.Body.String(), CodeCallerNotListed) {
		t.Fatal("the code reached the wire")
	}
}

func TestEveryCodeIsDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range AllCodes() {
		if c == "" || seen[c] {
			t.Fatalf("code %q empty or repeated", c)
		}
		seen[c] = true
	}
	if len(seen) < 15 {
		t.Fatalf("only %d codes", len(seen))
	}
}

func TestNewRefusesMissingCollaboratorsAndBadModes(t *testing.T) {
	base := newFx(t, CallerPartner).cfg
	mut := map[string]func(c *Config){
		"mode":     func(c *Config) { c.Mode = "" },
		"mode bad": func(c *Config) { c.Mode = "both" },
		"verifier": func(c *Config) { c.Verifier = nil },
		"staff":    func(c *Config) { c.Staff = nil },
		"orgs":     func(c *Config) { c.Orgs = nil },
		"knob":     func(c *Config) { c.Knob = nil },
		"audit":    func(c *Config) { c.Audit = nil },
		"minter":   func(c *Config) { c.Minter = nil },
		"colon":    func(c *Config) { c.Partners = []string{"a:b"} },
	}
	for name, m := range mut {
		c := base
		m(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: constructed without it", name)
		}
	}
	open := newFx(t, CallerOpen).cfg
	open.Partners = []string{fxPartner}
	if _, err := New(open); err == nil {
		t.Error("the open copy accepted a partner list")
	}
	open.Partners = nil
	open.Verifier = &fakeVerifier{}
	if _, err := New(open); err == nil {
		t.Error("the open copy accepted a verifier it never uses")
	}
}

// ---- the end to end through the real verifier and the real gate ----

func TestMintedTokenPassesTheRealVerifierAndTheStaffGate(t *testing.T) {
	f := newFx(t, CallerPartner)
	members := compositeFor(t)
	gate, err := authz.NewGate(f.verify, members, f.orgs)
	if err != nil {
		t.Fatal(err)
	}
	probe := gate.StaffScoped(func(w http.ResponseWriter, r *http.Request, s authz.StaffScope) {
		_, _ = w.Write([]byte(s.PlatformUserID + "|" + s.OrgSlug))
	})
	for _, p := range enterable() {
		a := decode(t, f.do(`{"persona":"`+p.ID+`"}`, "Bearer "+f.partner, "198.51.100."+p.UserID[len(p.UserID)-2:]+":1"))
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.Header.Set("Authorization", "Bearer "+a.Token)
		req.Header.Set("X-Org", flows.OrgSlug)
		rec := httptest.NewRecorder()
		probe.ServeHTTP(rec, req)
		if rec.Code != 200 || rec.Body.String() != p.Subject()+"|"+flows.OrgSlug {
			t.Fatalf("%s: probe %d %s", p.ID, rec.Code, rec.Body)
		}
		// Not for another org.
		req.Header.Set("X-Org", "other")
		rec = httptest.NewRecorder()
		probe.ServeHTTP(rec, req)
		if rec.Code == 200 {
			t.Fatalf("%s: a persona token reached another org", p.ID)
		}
	}
}

var _ = identity.ClassStaff

// The lead's rule, pending the principal's A6 ruling on admin cards on the
// public copy: on the open copy every persona whose resolved authority is an
// admin (tier 1, global) is the shared 403, judged after ResolveStaff. The
// testers' copy mints all four.
func TestOpenCopyRefusesEveryAdminPersonaAndTestersCopyMintsThem(t *testing.T) {
	for _, id := range []string{"general-manager", "financial-controller", "hr", "dealer-admin"} {
		f := newFx(t, CallerOpen)
		rec := f.enterOpen(id, fxAddr)
		f.assertShared(rec, 403)
		if f.h.GateFailures()[CodeOpenAdmin] != 1 {
			t.Fatalf("%s: counters %v", id, f.h.GateFailures())
		}
		if f.knob.calls != 0 {
			t.Fatalf("%s: the refusal read the knob", id)
		}
		g := newFx(t, CallerPartner)
		a := decode(t, g.enter(id))
		if a.User.Tier != 1 || a.User.Scope != "GLOBAL" {
			t.Fatalf("%s: tier %d scope %s", id, a.User.Tier, a.User.Scope)
		}
	}
	// A tier 2 global role (Payables and Receivables) is not an admin.
	f := newFx(t, CallerOpen)
	decode(t, f.enterOpen("payables-receivables", fxAddr))
}
