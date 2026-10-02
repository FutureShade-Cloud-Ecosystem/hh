//go:build demo

package demo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The visitor key legs (the comprehensive review's P3-8): behind a host
// edge every tester shares the address the mint limit keys on, so the
// limit refuses a team that taps in together. The design answers with a
// server issued visitor id (the flows GET sets a cookie when none
// arrives) as the mint's own key, and an address keyed ceiling beside it
// so a script cannot buy its way past the box's bound by farming ids.
// These legs run through the real handlers over the session rig's fakes,
// and they pin the design's own names and numbers as literals: the
// cookie's name and attributes, the ceiling's allowance. A literal here
// holds production to the design; a constant could move with it.

// teamCeilingMints is the design's address ceiling as a literal: the
// 41st mint inside one window refuses whatever cookie it carries.
const teamCeilingMints = 40

// visitorJar is one browser's cookie jar for the rig: the flows GET
// seeds it the way an entry page load does, and every mint afterwards
// carries the id back. A jar the flows answer left empty carries
// nothing, which is the shape of a client from before the cookie
// existed.
type visitorJar struct {
	visitor string
}

// seed drives the flows route and keeps the visitor cookie the answer
// sets, if it sets one.
func (j *visitorJar) seed(t *testing.T, m *Mint) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/demo/flows", nil)
	rsp := httptest.NewRecorder()
	m.Flows(rsp, req)
	if rsp.Code != http.StatusOK {
		t.Fatalf("the flows GET answered %d, want 200", rsp.Code)
	}
	for _, line := range rsp.Header().Values("Set-Cookie") {
		c, err := http.ParseSetCookie(line)
		if err != nil {
			t.Fatalf("the flows answer carries an unparsable cookie line %q: %v", line, err)
		}
		if c.Name == "demo_visitor" {
			j.visitor = c.Value
		}
	}
}

// mint drives the session route from the rig's shared address, carrying
// the jar's visitor cookie when the jar holds one.
func (j *visitorJar) mint(t *testing.T, m *Mint, body string) *httptest.ResponseRecorder {
	t.Helper()
	headers := map[string]string{}
	if j.visitor != "" {
		headers["Cookie"] = "demo_visitor=" + j.visitor
	}
	return postSessionFrom(t, m, body, "192.0.2.1:1234", headers)
}

// The team leg: six browsers behind one edge, one shared address. Each
// jar seeds itself from its own flows GET, so each mint carries its own
// visitor id and holds its own bucket: all six mints pass. This is the
// leg the defect breaks: where the limit keys on the address alone, the
// sixth mint is the refusal, and on such a tree this leg fails there
// with the limit's own words.
func TestMintKeysEachVisitorCookieApart(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, _ := newTestMint(t, minter, nil)

	const testers = 6
	seen := map[string]bool{}
	for i := 0; i < testers; i++ {
		jar := &visitorJar{}
		jar.seed(t, m)
		if jar.visitor == "" {
			t.Errorf("browser %d holds no visitor cookie after the flows GET", i+1)
		}
		if jar.visitor != "" && seen[jar.visitor] {
			t.Errorf("browser %d holds an id another browser was already issued (%q): the id must be fresh per issue", i+1, jar.visitor)
		}
		seen[jar.visitor] = true
		if rsp := jar.mint(t, m, `{"flow":"sales"}`); rsp.Code != http.StatusOK {
			t.Fatalf("mint %d: status %d, want 200 (body: %s)", i+1, rsp.Code, rsp.Body.String())
		}
	}
	if len(minter.subjects) != testers {
		t.Errorf("the minter signed %d sessions, want one per tester", len(minter.subjects))
	}
}

// The script leg: one address, no cookies, the bare burst shape. The
// visitor id changes nothing here: without one the mint keys on the
// address exactly as before, the allowance is spent by the fifth mint
// and the sixth is refused with the same words.
func TestMintWithoutCookiesStillRefusesTheBurst(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, auditFake := newTestMint(t, minter, nil)

	for i := 0; i < DefaultBurstAllowance; i++ {
		if rsp := postSession(t, m, `{"flow":"sales"}`); rsp.Code != http.StatusOK {
			t.Fatalf("admission %d: status %d, want 200", i+1, rsp.Code)
		}
	}
	refused := postSession(t, m, `{"flow":"sales"}`)
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("the sixth mint answered %d, want 429 (body: %s)", refused.Code, refused.Body.String())
	}
	if got := refused.Body.String(); !strings.Contains(got, "too many sign-in attempts") {
		t.Errorf("the refusal does not name the limit: %s", got)
	}
	if len(minter.subjects) != DefaultBurstAllowance || len(auditFake.rows) != DefaultBurstAllowance {
		t.Errorf("%d mints and %d trail rows, want the allowance's %d: the refusals signed and trailed nothing",
			len(minter.subjects), len(auditFake.rows), DefaultBurstAllowance)
	}
}

// The farming leg: one address juggling many distinct visitor ids. Every
// mint carries a well formed id of its own, so no single key's allowance
// is ever spent twice and the per key limit refuses nothing; the address
// ceiling is what stops the farm, at the design's number, whatever id
// the mint past the ceiling carries.
func TestMintStopsAtTheAddressCeiling(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, _ := newTestMint(t, minter, nil)

	mintAs := func(id string) *httptest.ResponseRecorder {
		return postSessionFrom(t, m, `{"flow":"sales"}`, "192.0.2.1:1234",
			map[string]string{"Cookie": "demo_visitor=" + id})
	}
	for i := 0; i < teamCeilingMints; i++ {
		if rsp := mintAs(fmt.Sprintf("%032x", i)); rsp.Code != http.StatusOK {
			t.Fatalf("farm mint %d: status %d, want 200 inside the ceiling (body: %s)", i+1, rsp.Code, rsp.Body.String())
		}
	}
	if rsp := mintAs(fmt.Sprintf("%032x", teamCeilingMints)); rsp.Code != http.StatusTooManyRequests {
		t.Fatalf("the farm's mint %d answered %d, want 429 at the ceiling (body: %s)",
			teamCeilingMints+1, rsp.Code, rsp.Body.String())
	}
	// The first id's own key holds one admission of its allowance, so a
	// refusal under it names the ceiling and not the key.
	if rsp := mintAs(fmt.Sprintf("%032x", 0)); rsp.Code != http.StatusTooManyRequests {
		t.Fatalf("a mint under a barely spent key answered %d, want the ceiling's 429", rsp.Code)
	}
}

// The cookie hygiene leg, the issued shape: the flows GET sets the
// visitor cookie when none arrives, with exactly the attributes the
// design names, a fresh value per issue, and no re-issue when the
// request already carries one.
func TestFlowsSetsTheVisitorCookie(t *testing.T) {
	m, _, _ := newTestMint(t, &fakeMinter{token: "t"}, nil)

	get := func(cookie string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/demo/flows", nil)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		rsp := httptest.NewRecorder()
		m.Flows(rsp, req)
		if rsp.Code != http.StatusOK {
			t.Fatalf("the flows GET answered %d, want 200", rsp.Code)
		}
		return rsp
	}

	rsp := get("")
	lines := rsp.Header().Values("Set-Cookie")
	if len(lines) != 1 {
		t.Fatalf("the flows answer sets %d cookies, want exactly the visitor id", len(lines))
	}
	c, err := http.ParseSetCookie(lines[0])
	if err != nil {
		t.Fatalf("the cookie line does not parse: %v", err)
	}
	if c.Name != "demo_visitor" {
		t.Errorf("cookie name %q, want demo_visitor", c.Name)
	}
	if len(c.Value) != 32 {
		t.Errorf("cookie value %q is %d characters, want the issued shape of 32", c.Value, len(c.Value))
	}
	if c.Path != "/" {
		t.Errorf("cookie path %q, want /", c.Path)
	}
	if c.MaxAge != 86400 {
		t.Errorf("cookie Max-Age %d, want the design's 86400", c.MaxAge)
	}
	if !c.HttpOnly {
		t.Error("the cookie is not HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite is %v, want Lax", c.SameSite)
	}
	if c.Secure {
		t.Error("the cookie carries Secure: the demo box serves plain http behind its edge, a Secure cookie would be dropped there")
	}
	if strings.Contains(rsp.Body.String(), c.Value) {
		t.Error("the flows body renders the cookie value")
	}

	// A second bare load is a fresh id.
	second := get("")
	c2, err := http.ParseSetCookie(second.Header().Get("Set-Cookie"))
	if err != nil {
		t.Fatalf("the second cookie line does not parse: %v", err)
	}
	if c2.Value == c.Value {
		t.Error("two issued ids carry one value: the id must be fresh per issue")
	}

	// A load that already carries the id is not re-issued: the id is
	// stable for the browser, not rotated per call.
	again := get("demo_visitor=" + c.Value)
	if got := again.Header().Get("Set-Cookie"); got != "" {
		t.Errorf("a request holding the id was answered with Set-Cookie %q, want no re-issue", got)
	}
}

// The cookie hygiene leg, the mint's mouth: the id is never rendered.
// Not in an admission's body, not in a refusal's body, not in any cookie
// the mint itself sets. (The rig sees no log lines to scan: the module
// takes no logger, so there is nothing on that seam.)
func TestMintNeverRendersTheVisitorID(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, _ := newTestMint(t, minter, nil)
	jar := &visitorJar{}
	jar.seed(t, m)
	if jar.visitor == "" {
		t.Fatal("the flows GET set no visitor cookie, nothing to keep out of the answers")
	}

	admitted := jar.mint(t, m, `{"flow":"sales"}`)
	if admitted.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (body: %s)", admitted.Code, admitted.Body.String())
	}
	if strings.Contains(admitted.Body.String(), jar.visitor) {
		t.Error("an admission's body renders the cookie value")
	}
	for _, line := range admitted.Header().Values("Set-Cookie") {
		if strings.Contains(line, jar.visitor) {
			t.Errorf("an admission sets a cookie echoing the id: %q", line)
		}
	}

	// Spend the jar's allowance and hold the refusal's body to the same
	// silence.
	var refused *httptest.ResponseRecorder
	for i := 0; i < DefaultBurstAllowance; i++ {
		refused = jar.mint(t, m, `{"flow":"sales"}`)
	}
	if refused == nil || refused.Code != http.StatusTooManyRequests {
		status := 0
		if refused != nil {
			status = refused.Code
		}
		t.Fatalf("the jar's burst ended at %d, want the limit's 429", status)
	}
	if strings.Contains(refused.Body.String(), jar.visitor) {
		t.Error("a refusal's body renders the cookie value")
	}
}

// The cookie hygiene leg, the fallback: a value outside the issued shape
// is the client's word and the key takes none of it. Every malformed
// value lands on the address key, so a stream of distinct malformed
// values spends that one allowance: the sixth refuses, a bare mint after
// them refuses too (the address is what they spent), and a well formed
// id issued after still holds its own bucket.
func TestMintFallsBackToTheAddressKeyOnAMalformedCookie(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, _, _ := newTestMint(t, minter, nil)

	mintWith := func(value string) *httptest.ResponseRecorder {
		return postSessionFrom(t, m, `{"flow":"sales"}`, "192.0.2.1:1234",
			map[string]string{"Cookie": "demo_visitor=" + value})
	}
	values := []string{
		"short",
		"deadbeef",
		strings.Repeat("z", 32),
		strings.Repeat("0", 31),
		strings.Repeat("f", 33),
	}
	for i, v := range values {
		if rsp := mintWith(v); rsp.Code != http.StatusOK {
			t.Fatalf("malformed mint %d: status %d, want 200 (a malformed id refuses nothing, it falls back) (body: %s)",
				i+1, rsp.Code, rsp.Body.String())
		}
	}
	if rsp := mintWith(""); rsp.Code != http.StatusTooManyRequests {
		t.Fatalf("a sixth distinct malformed value answered %d, want 429: distinct malformed values buy no distinct buckets", rsp.Code)
	}
	if rsp := postSession(t, m, `{"flow":"sales"}`); rsp.Code != http.StatusTooManyRequests {
		t.Fatalf("a bare mint after the malformed ones answered %d, want 429: the malformed mints spent the address key", rsp.Code)
	}
	fresh := &visitorJar{}
	fresh.seed(t, m)
	if rsp := fresh.mint(t, m, `{"flow":"sales"}`); rsp.Code != http.StatusOK {
		t.Fatalf("a well formed id answered %d, want 200 from its own bucket (body: %s)", rsp.Code, rsp.Body.String())
	}
}
