package exchange

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

type methodsFx struct {
	orgs    *fakeOrgs
	methods *fakeMethods
	h       *MethodsHandler
	now     time.Time
	mu      sync.Mutex
}

func newMethodsFx(t *testing.T) *methodsFx {
	t.Helper()
	m := &methodsFx{orgs: &fakeOrgs{ok: true}, methods: &fakeMethods{set: []string{"google", "magic_link"}}, now: time.Now()}
	h, err := NewMethodsHandler(MethodsConfig{Orgs: m.orgs, Methods: m.methods, Now: func() time.Time { return m.now }})
	if err != nil {
		t.Fatal(err)
	}
	m.h = h
	return m
}

func (m *methodsFx) get(rawQuery, peer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/methods?"+rawQuery, nil)
	if peer != "" {
		req.RemoteAddr = peer
	}
	rec := httptest.NewRecorder()
	m.h.ServeHTTP(rec, req)
	return rec
}

const defaultMethodsBody = `{"methods":["email_password","google"]}` + "\n"

func TestMethodsAnswersTheOrgsSet(t *testing.T) {
	m := newMethodsFx(t)
	rec := m.get("org=acme", "")
	if rec.Code != 200 || rec.Body.String() != `{"methods":["google","magic_link"]}`+"\n" {
		t.Fatalf("= %d %q", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type %q", ct)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("cache control %q", rec.Header().Get("Cache-Control"))
	}
	if len(m.methods.calls) != 1 || m.methods.calls[0] != fxOrgID {
		t.Fatalf("reader calls = %v", m.methods.calls)
	}
}

// No directory oracle: an org with no row, an unknown slug and a malformed
// slug all answer the same bytes, status and headers.
func TestMethodsThreeKindsOfOrgAnswerByteIdentical(t *testing.T) {
	known := newMethodsFx(t)
	known.methods.set = []string{"email_password", "google"} // the default set held as a row
	unknown := newMethodsFx(t)
	unknown.orgs.ok = false
	malformed := newMethodsFx(t)
	var ref *httptest.ResponseRecorder
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"org with the default set": known.get("org=acme", ""),
		"unknown slug":             unknown.get("org=nobody", ""),
		"malformed slug":           malformed.get("org=NOT%20A%20SLUG", ""),
	} {
		if rec.Code != 200 || rec.Body.String() != defaultMethodsBody {
			t.Fatalf("%s = %d %q", name, rec.Code, rec.Body)
		}
		if ref == nil {
			ref = rec
			continue
		}
		if !bytes.Equal(ref.Body.Bytes(), rec.Body.Bytes()) || ref.Code != rec.Code {
			t.Fatalf("%s differs from the others", name)
		}
		for _, h := range []string{"Content-Type", "Cache-Control"} {
			if ref.Header().Get(h) != rec.Header().Get(h) {
				t.Errorf("%s: header %s differs: %q vs %q", name, h, ref.Header().Get(h), rec.Header().Get(h))
			}
		}
		if len(ref.Header()) != len(rec.Header()) {
			t.Errorf("%s: header count differs", name)
		}
	}
}

func TestMethodsBadShapesAnswerTheDefaultAndTouchNothing(t *testing.T) {
	for _, q := range []string{
		"", "org=", "org=UPPER", "org=-lead", "org=a%20b", "org=a/b", "org=..%2f..", "org=" + strings.Repeat("a", 33),
		"org=%00", "org=caf%C3%A9", "other=acme", "org=acme%0a",
	} {
		m := newMethodsFx(t)
		rec := m.get(q, "")
		if rec.Code != 200 || rec.Body.String() != defaultMethodsBody {
			t.Errorf("%q = %d %q", q, rec.Code, rec.Body)
		}
		if len(m.methods.calls) != 0 {
			t.Errorf("%q reached the reader", q)
		}
	}
}

func TestMethodsAnUnknownOrgNeverReachesTheReader(t *testing.T) {
	m := newMethodsFx(t)
	m.orgs.ok = false
	if rec := m.get("org=ghost", ""); rec.Body.String() != defaultMethodsBody {
		t.Fatalf("%q", rec.Body)
	}
	if len(m.methods.calls) != 0 {
		t.Fatal("the reader ran for an unknown org")
	}
}

func TestMethodsErrorsAre503(t *testing.T) {
	m := newMethodsFx(t)
	m.orgs.err = errBoom
	m.orgs.ok = false
	rec := m.get("org=acme", "")
	if rec.Code != 503 || !bytes.Equal(rec.Body.Bytes(), bodyV) {
		t.Fatalf("resolver error = %d %q", rec.Code, rec.Body)
	}
	g := newMethodsFx(t)
	g.methods.err = errBoom
	rec = g.get("org=acme", "")
	if rec.Code != 503 || !bytes.Equal(rec.Body.Bytes(), bodyV) {
		t.Fatalf("reader error = %d %q", rec.Code, rec.Body)
	}
}

func TestMethodsIsRateLimitedPerAddress(t *testing.T) {
	m := newMethodsFx(t)
	lim := ratelimit.NewAddressLimit(3, time.Minute, func() time.Time { return m.now })
	h, err := NewMethodsHandler(MethodsConfig{Orgs: m.orgs, Methods: m.methods, AddressLimit: lim, Now: func() time.Time { return m.now }})
	if err != nil {
		t.Fatal(err)
	}
	m.h = h
	for i := 0; i < 3; i++ {
		if rec := m.get("org=acme", "192.0.2.1:4000"); rec.Code != 200 {
			t.Fatalf("request %d = %d", i, rec.Code)
		}
	}
	rec := m.get("org=acme", "192.0.2.1:4001")
	if rec.Code != 429 {
		t.Fatalf("fourth = %d", rec.Code)
	}
	// A malformed slug costs an allowance too, so the shape check is no free probe.
	if rec := m.get("org=BAD", "192.0.2.1:4002"); rec.Code != 429 {
		t.Fatalf("malformed over the limit = %d", rec.Code)
	}
	// Another address is its own bucket.
	if rec := m.get("org=acme", "192.0.2.9:4000"); rec.Code != 200 {
		t.Fatalf("other address = %d", rec.Code)
	}
}

func TestMethodsAllowanceIsTheNamedConstant(t *testing.T) {
	if MethodsAddressAllowance != 60 {
		t.Fatalf("allowance %d", MethodsAddressAllowance)
	}
	m := newMethodsFx(t)
	for i := 0; i < MethodsAddressAllowance; i++ {
		if rec := m.get("org=acme", "192.0.2.1:1"); rec.Code != 200 {
			t.Fatalf("request %d = %d", i, rec.Code)
		}
	}
	if rec := m.get("org=acme", "192.0.2.1:1"); rec.Code != 429 {
		t.Fatalf("over = %d", rec.Code)
	}
}

func TestMethodsHonoursTrustedProxyHeader(t *testing.T) {
	m := newMethodsFx(t)
	trusted, err := ratelimit.ParseTrustedProxies("10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	lim := ratelimit.NewAddressLimit(1, time.Minute, func() time.Time { return m.now })
	h, err := NewMethodsHandler(MethodsConfig{Orgs: m.orgs, Methods: m.methods, Trusted: trusted, AddressLimit: lim, Now: func() time.Time { return m.now }})
	if err != nil {
		t.Fatal(err)
	}
	send := func(client string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/methods?org=acme", nil)
		req.RemoteAddr = "10.0.0.1:5000"
		req.Header.Set("X-Forwarded-For", client)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if send("198.51.100.1") != 200 || send("198.51.100.2") != 200 || send("198.51.100.1") != 429 {
		t.Fatal("clients behind the trusted proxy are not counted apart")
	}
}

func TestMethodsConfigRequiresItsCollaborators(t *testing.T) {
	good := MethodsConfig{Orgs: &fakeOrgs{}, Methods: &fakeMethods{}}
	for name, mut := range map[string]func(*MethodsConfig){
		"orgs":    func(c *MethodsConfig) { c.Orgs = nil },
		"methods": func(c *MethodsConfig) { c.Methods = nil },
	} {
		c := good
		mut(&c)
		if _, err := NewMethodsHandler(c); err == nil {
			t.Errorf("%s: accepted nil", name)
		}
	}
}

func TestMethodsBodyCarriesOnlyTheCodes(t *testing.T) {
	m := newMethodsFx(t)
	body := m.get("org=acme", "").Body.String()
	if strings.Contains(body, fxOrgID) || strings.Contains(body, "acme") {
		t.Fatalf("body names the org: %q", body)
	}
}
