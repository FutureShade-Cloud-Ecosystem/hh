package authz

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	consoleTestProject = "proj-6b"
	consoleTestKey     = "server-key-6b-secret"
	consoleTestJWT     = "aaa.bbb.ccc"
)

// consoleStub serves canned answers and records what it was sent.
type consoleStub struct {
	hits    int32
	status  int
	body    string
	delay   time.Duration
	last    atomic.Value // *http.Request clone of the headers
	handler http.HandlerFunc
}

func (s *consoleStub) serve(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&s.hits, 1)
	s.last.Store(r.Clone(context.Background()))
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.handler != nil {
		s.handler(w, r)
		return
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	fmt.Fprint(w, s.body)
}

func (s *consoleStub) lastRequest() *http.Request {
	r, _ := s.last.Load().(*http.Request)
	return r
}

func newConsoleFixture(t *testing.T, stub *consoleStub, hc *http.Client) *ConsoleClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(srv.Close)
	if hc == nil {
		hc = srv.Client()
	}
	c, err := NewConsoleClient(srv.URL, consoleTestProject, consoleTestKey, hc)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const goodAccount = `{"$id":"user_1.a-b","email":"person@futurebuild.ai","name":"A Person","emailVerification":true,"status":true}`

func TestConsoleAccountUnderJWTSendsTheJWTAndNoServerKey(t *testing.T) {
	stub := &consoleStub{body: goodAccount}
	c := newConsoleFixture(t, stub, nil)
	acct, err := c.AccountUnderJWT(context.Background(), consoleTestJWT)
	if err != nil {
		t.Fatal(err)
	}
	if acct.ID != "user_1.a-b" || acct.Email != "person@futurebuild.ai" || acct.Name != "A Person" || !acct.EmailVerified || !acct.Active {
		t.Fatalf("account = %+v", acct)
	}
	r := stub.lastRequest()
	if r.URL.Path != "/account" || r.Method != http.MethodGet {
		t.Fatalf("request = %s %s", r.Method, r.URL.Path)
	}
	if r.Header.Get("X-Appwrite-Project") != consoleTestProject || r.Header.Get("X-Appwrite-JWT") != consoleTestJWT {
		t.Fatalf("headers = %v", r.Header)
	}
	if r.Header.Get("X-Appwrite-Key") != "" {
		t.Fatal("the server key was sent with the account read")
	}
}

func TestConsoleAccountRefusalsAndFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"console says 401", 401, `{"message":"secret detail"}`, ErrConsoleRefused},
		{"500", 500, "", ErrConsoleUnavailable},
		{"502", 502, "", ErrConsoleUnavailable},
		{"429", 429, "", ErrConsoleUnavailable},
		{"403 is not a verdict", 403, "", ErrConsoleUnavailable},
		{"404", 404, "", ErrConsoleUnavailable},
		{"redirect", 302, "", ErrConsoleUnavailable},
		{"204 is not 200", 204, "", ErrConsoleUnavailable},
		{"not json", 200, "<html>", ErrConsoleUnavailable},
		{"empty body", 200, "", ErrConsoleUnavailable},
		{"json array", 200, "[]", ErrConsoleUnavailable},
		{"no verification field", 200, `{"$id":"u1","email":"a@b.c","name":"n","status":true}`, ErrConsoleUnavailable},
		{"no status field", 200, `{"$id":"u1","email":"a@b.c","name":"n","emailVerification":true}`, ErrConsoleUnavailable},
		{"verification is a string", 200, `{"$id":"u1","email":"a@b.c","name":"n","emailVerification":"true","status":true}`, ErrConsoleUnavailable},
		{"no email", 200, `{"$id":"u1","name":"n","emailVerification":true,"status":true}`, ErrConsoleUnavailable},
		{"no id", 200, `{"email":"a@b.c","name":"n","emailVerification":true,"status":true}`, ErrConsoleRefused},
		{"empty id", 200, `{"$id":"","email":"a@b.c","name":"n","emailVerification":true,"status":true}`, ErrConsoleRefused},
		{"colon in id", 200, `{"$id":"persona:x","email":"a@b.c","name":"n","emailVerification":true,"status":true}`, ErrConsoleRefused},
		{"id too long", 200, `{"$id":"` + strings.Repeat("a", 37) + `","email":"a@b.c","name":"n","emailVerification":true,"status":true}`, ErrConsoleRefused},
		{"id starts with a special", 200, `{"$id":"-abc","email":"a@b.c","name":"n","emailVerification":true,"status":true}`, ErrConsoleRefused},
		{"id with a space", 200, `{"$id":"a b","email":"a@b.c","name":"n","emailVerification":true,"status":true}`, ErrConsoleRefused},
		{"oversized body", 200, `{"$id":"u1","email":"a@b.c","name":"` + strings.Repeat("x", 80000) + `","emailVerification":true,"status":true}`, ErrConsoleUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &consoleStub{status: tc.status, body: tc.body}
			c := newConsoleFixture(t, stub, nil)
			_, err := c.AccountUnderJWT(context.Background(), consoleTestJWT)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if n := atomic.LoadInt32(&stub.hits); n != 1 {
				t.Fatalf("%d attempts, want exactly one", n)
			}
			assertNoSecrets(t, err, tc.body)
		})
	}
}

func assertNoSecrets(t *testing.T, err error, body string) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	for _, secret := range []string{consoleTestJWT, consoleTestKey, consoleTestProject, "secret detail", "person@futurebuild.ai"} {
		if strings.Contains(msg, secret) {
			t.Fatalf("error text carries %q: %s", secret, msg)
		}
	}
	if body != "" && strings.Contains(msg, body) {
		t.Fatal("error text carries the response body")
	}
}

func TestConsoleRedirectIsRefusedAndTheJWTIsNotForwarded(t *testing.T) {
	target := &consoleStub{body: goodAccount}
	targetSrv := httptest.NewServer(http.HandlerFunc(target.serve))
	defer targetSrv.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		before := atomic.LoadInt32(&target.hits)
		stub := &consoleStub{handler: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, targetSrv.URL+"/account", status)
		}}
		// The injected client follows redirects by default; the client must
		// not rely on that.
		c := newConsoleFixture(t, stub, &http.Client{Timeout: time.Second})
		if _, err := c.AccountUnderJWT(context.Background(), consoleTestJWT); !errors.Is(err, ErrConsoleUnavailable) {
			t.Fatalf("status %d: err = %v", status, err)
		}
		if _, err := c.UserSessions(context.Background(), "user_1"); !errors.Is(err, ErrConsoleUnavailable) {
			t.Fatalf("status %d sessions: err = %v", status, err)
		}
		if atomic.LoadInt32(&target.hits) != before {
			t.Fatalf("status %d: the redirect target was reached", status)
		}
	}
}

func TestConsoleTimeoutIsUnavailableWithOneAttempt(t *testing.T) {
	stub := &consoleStub{body: goodAccount, delay: 300 * time.Millisecond}
	c := newConsoleFixture(t, stub, &http.Client{Timeout: 50 * time.Millisecond})
	_, err := c.AccountUnderJWT(context.Background(), consoleTestJWT)
	if !errors.Is(err, ErrConsoleUnavailable) {
		t.Fatalf("err = %v", err)
	}
	assertNoSecrets(t, err, "")
	time.Sleep(400 * time.Millisecond)
	if n := atomic.LoadInt32(&stub.hits); n != 1 {
		t.Fatalf("%d attempts", n)
	}
}

func TestConsoleUnreachableIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, err := NewConsoleClient(url, consoleTestProject, consoleTestKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.AccountUnderJWT(context.Background(), consoleTestJWT)
	if !errors.Is(err, ErrConsoleUnavailable) {
		t.Fatalf("err = %v", err)
	}
	assertNoSecrets(t, err, "")
}

func TestConsoleClientConstructionFailsClosed(t *testing.T) {
	for _, tc := range [][3]string{{"", "p", "k"}, {"/rel", "p", "k"}, {"http://x", "", "k"}, {"http://x", "p", ""}} {
		if _, err := NewConsoleClient(tc[0], tc[1], tc[2], nil); err == nil {
			t.Errorf("%v accepted", tc)
		}
	}
}

const goodSessions = `{"total":2,"sessions":[` +
	`{"$id":"s1","$createdAt":"2025-01-02T03:04:05.000+00:00","provider":"email"},` +
	`{"$id":"s2","$createdAt":"2025-01-02T04:04:05.123+00:00","provider":"google"}]}`

func TestConsoleUserSessionsUsesTheServerKeyAndParsesOnlyWhatItNeeds(t *testing.T) {
	stub := &consoleStub{body: goodSessions}
	c := newConsoleFixture(t, stub, nil)
	got, err := c.UserSessions(context.Background(), "user_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "s1" || got[0].Provider != "email" || got[1].Provider != "google" {
		t.Fatalf("sessions = %+v", got)
	}
	if want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC); !got[0].CreatedAt.Equal(want) {
		t.Fatalf("createdAt = %v", got[0].CreatedAt)
	}
	r := stub.lastRequest()
	if r.URL.Path != "/users/user_1/sessions" {
		t.Fatalf("path = %s", r.URL.Path)
	}
	if r.Header.Get("X-Appwrite-Key") != consoleTestKey || r.Header.Get("X-Appwrite-Project") != consoleTestProject {
		t.Fatalf("headers = %v", r.Header)
	}
	if r.Header.Get("X-Appwrite-JWT") != "" {
		t.Fatal("a JWT was sent with the session read")
	}
}

func TestConsoleUserSessionsFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"401", 401, ""}, {"404", 404, ""}, {"500", 500, ""}, {"redirect", 302, ""},
		{"not json", 200, "oops"},
		{"truncated listing", 200, `{"total":3,"sessions":[{"$id":"s1","$createdAt":"2025-01-02T03:04:05.000+00:00","provider":"email"}]}`},
		{"oversized", 200, `{"total":1,"sessions":[{"$id":"s1","provider":"` + strings.Repeat("x", 2<<20) + `"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &consoleStub{status: tc.status, body: tc.body}
			c := newConsoleFixture(t, stub, nil)
			_, err := c.UserSessions(context.Background(), "user_1")
			if !errors.Is(err, ErrConsoleUnavailable) {
				t.Fatalf("err = %v", err)
			}
			if atomic.LoadInt32(&stub.hits) != 1 {
				t.Fatal("more than one attempt")
			}
			assertNoSecrets(t, err, tc.body)
		})
	}
}

func TestConsoleUserSessionsRefusesABadUserID(t *testing.T) {
	stub := &consoleStub{body: goodSessions}
	c := newConsoleFixture(t, stub, nil)
	for _, id := range []string{"", "a/b", "../x", "persona:x", strings.Repeat("a", 37)} {
		if _, err := c.UserSessions(context.Background(), id); err == nil {
			t.Errorf("%q accepted", id)
		}
	}
	if atomic.LoadInt32(&stub.hits) != 0 {
		t.Fatal("a bad id reached the console")
	}
}

func TestConsoleSessionWithAnUnparseableTimeKeepsAZeroTime(t *testing.T) {
	stub := &consoleStub{body: `{"total":1,"sessions":[{"$id":"s1","$createdAt":"yesterday","provider":"email"}]}`}
	c := newConsoleFixture(t, stub, nil)
	got, err := c.UserSessions(context.Background(), "user_1")
	if err != nil || len(got) != 1 || !got[0].CreatedAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func jwtB64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func TestParseConsoleJWT(t *testing.T) {
	payload := `{"exp":1900000000,"projectId":"p","sessionId":"s1","userId":"user_1"}`
	sig := []byte{1, 2, 3, 4}
	tok := jwtB64(`{"alg":"HS256"}`) + "." + jwtB64(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)
	got, err := ParseConsoleJWT(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "s1" || got.UserID != "user_1" || got.ExpiresAt != 1900000000 {
		t.Fatalf("parsed = %+v", got)
	}
	if string(got.Signature) != string(sig) {
		t.Fatalf("signature = %v", got.Signature)
	}
	// A padded spelling of the same signature decodes to the same bytes.
	padded := jwtB64(`{"alg":"HS256"}`) + "." + jwtB64(payload) + "." + base64.URLEncoding.EncodeToString(sig)
	got2, err := ParseConsoleJWT(padded)
	if err != nil || string(got2.Signature) != string(sig) {
		t.Fatalf("padded spelling: %+v %v", got2, err)
	}
}

func TestParseConsoleJWTRefusals(t *testing.T) {
	h := jwtB64(`{"alg":"HS256"}`)
	sig := "AQID"
	bad := map[string]string{
		"two parts":        h + "." + jwtB64(`{}`),
		"four parts":       h + "." + jwtB64(`{}`) + "." + sig + ".x",
		"empty signature":  h + "." + jwtB64(`{"exp":1900000000}`) + ".",
		"payload not b64":  h + ".!!!." + sig,
		"payload not json": h + "." + jwtB64(`nope`) + "." + sig,
		"no exp":           h + "." + jwtB64(`{"sessionId":"s","userId":"u"}`) + "." + sig,
		"string exp":       h + "." + jwtB64(`{"exp":"1900000000"}`) + "." + sig,
		"fractional exp":   h + "." + jwtB64(`{"exp":1900000000.5}`) + "." + sig,
		"null exp":         h + "." + jwtB64(`{"exp":null}`) + "." + sig,
		"signature junk":   h + "." + jwtB64(`{"exp":1900000000}`) + ".!!!!",
	}
	for name, tok := range bad {
		if _, err := ParseConsoleJWT(tok); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestValidConsoleID(t *testing.T) {
	for _, id := range []string{"a", "A", "0", "user_1", "u.1-2", strings.Repeat("a", 36)} {
		if !ValidConsoleID(id) {
			t.Errorf("%q refused", id)
		}
	}
	for _, id := range []string{"", "_a", ".a", "-a", "a:b", "a b", "a/b", "é", strings.Repeat("a", 37), "persona:x"} {
		if ValidConsoleID(id) {
			t.Errorf("%q accepted", id)
		}
	}
}
