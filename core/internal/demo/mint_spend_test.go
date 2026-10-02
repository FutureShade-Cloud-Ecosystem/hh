//go:build demo

package demo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The spend legs (round two of the comprehensive review's P3-8): the
// mint must fit both of its buckets, its own key and the address
// ceiling, before anything spends, and only a granted mint spends
// either. A refusal must leave both buckets exactly where they were:
// the tester locked out by the team's ceiling keeps their own
// allowance, and a spent key hammering the mint drains no team
// allowance.

// spendMint builds the module over the fakes with the driven clock, and
// returns it with a mint helper that carries one visitor cookie from the
// rig's shared address. The ceiling rides the default window throughout;
// the key's allowance and window name the leg's own shape.
func spendMint(t *testing.T, clock *testClock, keyAllowance int, keyWindow time.Duration, ceilingAllowance int) (*Mint, func(id string) *httptest.ResponseRecorder) {
	t.Helper()
	minter := &fakeMinter{token: "t"}
	m, err := NewMint(MintDeps{
		Minter: minter,
		Orgs:   &fakeOrgs{id: "org-demo-1", ok: true},
		Staff:  &fakeStaff{auth: salesAuthority(), ok: true},
		Audit:  &fakeAudit{},
		Now:    clock.Now,
	})
	if err != nil {
		t.Fatalf("NewMint: %v", err)
	}
	m.limit = NewAddressLimit(keyAllowance, keyWindow, clock.Now)
	m.ceiling = NewAddressLimit(ceilingAllowance, DefaultBurstWindow, clock.Now)
	mintAs := func(id string) *httptest.ResponseRecorder {
		return postSessionFrom(t, m, `{"flow":"sales"}`, "192.0.2.1:1234",
			map[string]string{"Cookie": "demo_visitor=" + id})
	}
	return m, mintAs
}

// The ceiling refusal leg: the team's ceiling is full and one more
// tester taps in. The mint is refused, and the refusal must spend
// nothing: the tester's own key takes no stamp, so when the ceiling's
// window slides open the same cookie mints. Where the key spends before
// the ceiling's answer stands, the refusal consumes the tester's own
// allowance, and this leg fails twice: the stamp sits on the key, and
// the cookie stays locked out after the slide.
func TestCeilingRefusalSpendsNothing(t *testing.T) {
	clock := &testClock{at: time.Unix(0, 0).UTC()}
	// The key allowance is one on a long window, the ceiling two on the
	// default one: a key stamp the refusal wrongly made would outlive
	// the ceiling's window and show in both checks below.
	m, mintAs := spendMint(t, clock, 1, 10*time.Minute, 2)

	for i, id := range []string{fmt.Sprintf("%032x", 1), fmt.Sprintf("%032x", 2)} {
		if rsp := mintAs(id); rsp.Code != http.StatusOK {
			t.Fatalf("filling mint %d: status %d, want 200 inside the ceiling (body: %s)", i+1, rsp.Code, rsp.Body.String())
		}
	}
	tester := fmt.Sprintf("%032x", 3)
	if rsp := mintAs(tester); rsp.Code != http.StatusTooManyRequests {
		t.Fatalf("the mint against a full ceiling answered %d, want 429 (body: %s)", rsp.Code, rsp.Body.String())
	}
	if got := len(m.limit.entries["v:"+tester]); got != 0 {
		t.Errorf("the refused mint left %d stamp(s) on the tester's own key, want none: a refusal spends nothing", got)
	}
	clock.advance(DefaultBurstWindow + time.Second)
	if rsp := mintAs(tester); rsp.Code != http.StatusOK {
		t.Fatalf("the same cookie after the slide answered %d, want 200: the tester's own allowance must have survived the refusal (body: %s)", rsp.Code, rsp.Body.String())
	}
}

// The spent key leg: one tester's allowance is gone and the tester
// hammers the mint while the team's ceiling has room. Every hammer is
// refused, and no hammer may spend the team's ceiling: the address's
// count stays at the one granted mint, and a fresh tester still mints
// afterwards. Where the ceiling spends before the key's answer (the
// reviewer's mirrored order), every hammer drains the team's allowance
// and the count here shows it.
func TestKeyRefusalSpendsNothing(t *testing.T) {
	clock := &testClock{at: time.Unix(0, 0).UTC()}
	// The key allowance is one, the ceiling three: after the granted
	// mint the ceiling still has room, which is the shape the hazard
	// needs.
	m, mintAs := spendMint(t, clock, 1, DefaultBurstWindow, 3)

	tester := fmt.Sprintf("%032x", 7)
	if rsp := mintAs(tester); rsp.Code != http.StatusOK {
		t.Fatalf("the granted mint answered %d, want 200 (body: %s)", rsp.Code, rsp.Body.String())
	}
	if got, want := len(m.ceiling.entries["192.0.2.1"]), 1; got != want {
		t.Fatalf("the ceiling holds %d admissions after the granted mint, want %d", got, want)
	}
	for i := 0; i < 5; i++ {
		if rsp := mintAs(tester); rsp.Code != http.StatusTooManyRequests {
			t.Fatalf("hammer %d answered %d, want the spent key's 429 (body: %s)", i+1, rsp.Code, rsp.Body.String())
		}
	}
	if got, want := len(m.ceiling.entries["192.0.2.1"]), 1; got != want {
		t.Errorf("the ceiling holds %d admissions after the hammering, want %d: a refused hammer spent the team's allowance", got, want)
	}
	fresh := fmt.Sprintf("%032x", 8)
	if rsp := mintAs(fresh); rsp.Code != http.StatusOK {
		t.Fatalf("a fresh tester after the hammering answered %d, want 200: the team's ceiling must have survived (body: %s)", rsp.Code, rsp.Body.String())
	}
}
