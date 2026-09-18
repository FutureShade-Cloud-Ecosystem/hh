package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// appwriteStub is a test double for the Appwrite server API: it counts
// attempts (the no-retry proof) and serves a canned membership page.
type appwriteStub struct {
	mu        sync.Mutex
	attempts  int32
	projects  map[string]bool
	keys      map[string]bool
	page      appwriteMembershipPage
	status    int
	rawBody   string
	requested atomic.Value // string
}

func (s *appwriteStub) handler(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&s.attempts, 1)
	s.requested.Store(r.URL.String())
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.projects[r.Header.Get("X-Appwrite-Project")] || !s.keys[r.Header.Get("X-Appwrite-Key")] {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	if s.rawBody != "" {
		fmt.Fprint(w, s.rawBody)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.page)
}

// requestedPath reports the last requested URI, so the endpoint shape is
// asserted rather than trusted.
func (s *appwriteStub) requestedPath() string {
	v, _ := s.requested.Load().(string)
	return v
}

func newAppwrite(t *testing.T, mutate func(*appwriteStub)) (*AppwriteMembershipSource, *appwriteStub) {
	t.Helper()
	stub := &appwriteStub{
		projects: map[string]bool{"proj-b3": true},
		keys:     map[string]bool{"key-b3": true},
	}
	if mutate != nil {
		mutate(stub)
	}
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	src, err := NewAppwriteMembershipSource(srv.URL, "proj-b3", "key-b3", srv.Client())
	if err != nil {
		t.Fatalf("NewAppwriteMembershipSource: %v", err)
	}
	return src, stub
}

func TestAppwriteConstructionFailsClosed(t *testing.T) {
	cases := []struct {
		name                 string
		endpoint, project, k string
	}{
		{"empty endpoint", "", "proj", "key"},
		{"relative endpoint", "/local", "proj", "key"},
		{"no project", "http://platform.local", "", "key"},
		{"no key", "http://platform.local", "proj", ""},
	}
	for _, c := range cases {
		if _, err := NewAppwriteMembershipSource(c.endpoint, c.project, c.k, nil); err == nil {
			t.Fatalf("%s: construction succeeded", c.name)
		}
	}
}

// OrgMember both halves: the membership present passes, the membership
// absent is the boolean false, and both legs answer without error. The
// listing is requested once per call, on the user-scoped memberships
// path with the bounded page size.
func TestAppwriteOrgMemberBothDirections(t *testing.T) {
	src, stub := newAppwrite(t, func(s *appwriteStub) {
		s.page = appwriteMembershipPage{Total: 2, Memberships: []appwriteMembership{
			{TeamID: "org:otheryard", UserID: "platform-user-9"},
			{TeamID: "org:yardone", UserID: "platform-user-1"},
		}}
	})
	member, err := src.OrgMember(context.Background(), "platform-user-1", "yardone")
	if err != nil || !member {
		t.Fatalf("member leg: member=%v err=%v", member, err)
	}
	member, err = src.OrgMember(context.Background(), "platform-user-1", "otheryard")
	if err != nil || member {
		t.Fatalf("absent leg: member=%v err=%v", member, err)
	}
	if got := stub.requestedPath(); got != "/users/platform-user-1/memberships?limit=100" {
		t.Fatalf("requested path = %q", got)
	}
	if got := atomic.LoadInt32(&stub.attempts); got != 2 {
		t.Fatalf("attempts = %d, want one per call", got)
	}
}

// TenantOrgs parses tenant team ids into distinct org slugs and skips
// everything that is not a well-formed tenant team.
func TestAppwriteTenantOrgs(t *testing.T) {
	src, _ := newAppwrite(t, func(s *appwriteStub) {
		s.page = appwriteMembershipPage{Total: 5, Memberships: []appwriteMembership{
			{TeamID: "org:yardone", UserID: "platform-user-1"},
			{TeamID: "tenant:yardone:acme", UserID: "platform-user-1"},
			{TeamID: "tenant:yardone:beta", UserID: "platform-user-1"},
			{TeamID: "tenant:otheryard:gamma", UserID: "platform-user-1"},
			{TeamID: "tenant:bad", UserID: "platform-user-1"},
		}}
	})
	orgs, err := src.TenantOrgs(context.Background(), "platform-user-1")
	if err != nil {
		t.Fatalf("TenantOrgs: %v", err)
	}
	want := map[string]bool{"yardone": false, "otheryard": false}
	for _, org := range orgs {
		if _, ok := want[org]; ok {
			want[org] = true
		} else {
			t.Fatalf("unexpected org %q", org)
		}
	}
	for org, seen := range want {
		if !seen {
			t.Fatalf("org %q missing from %v", org, orgs)
		}
	}
}

// Fail-closed legs: a non-200 answer, a malformed body and a truncated
// listing are errors, never answers, and an erroring endpoint sees exactly
// one attempt per call (no retry storm).
func TestAppwriteFailClosedSingleAttempt(t *testing.T) {
	src, stub := newAppwrite(t, func(s *appwriteStub) { s.status = http.StatusInternalServerError })
	if _, err := src.OrgMember(context.Background(), "platform-user-1", "yardone"); err == nil {
		t.Fatalf("500 answered without error")
	}
	if got := atomic.LoadInt32(&stub.attempts); got != 1 {
		t.Fatalf("attempts = %d, want exactly one", got)
	}
	if _, err := src.TenantOrgs(context.Background(), "platform-user-1"); err == nil {
		t.Fatalf("500 answered without error on the tenant leg")
	}
	if got := atomic.LoadInt32(&stub.attempts); got != 2 {
		t.Fatalf("attempts = %d, want one per call", got)
	}

	malformed, _ := newAppwrite(t, func(s *appwriteStub) {
		s.status = 0
		s.rawBody = "<html>not json</html>"
	})
	if _, err := malformed.OrgMember(context.Background(), "platform-user-1", "yardone"); err == nil {
		t.Fatalf("malformed body answered without error")
	}

	truncated, _ := newAppwrite(t, func(s *appwriteStub) {
		s.page = appwriteMembershipPage{Total: 101, Memberships: make([]appwriteMembership, 100)}
	})
	if _, err := truncated.OrgMember(context.Background(), "platform-user-1", "yardone"); err == nil {
		t.Fatalf("truncated listing answered without error")
	}
}

// The credential never reaches a page answer: the cache wraps this source
// elsewhere, and what it stores is the boolean and the slug list, so the
// proof here is that the source itself returns no structure that could
// carry the key: booleans, slug lists and errors only.
func TestAppwriteAnswersCarryNoCredential(t *testing.T) {
	src, stub := newAppwrite(t, func(s *appwriteStub) {
		s.page = appwriteMembershipPage{Total: 1, Memberships: []appwriteMembership{{TeamID: "org:yardone", UserID: "platform-user-1"}}}
	})
	member, err := src.OrgMember(context.Background(), "platform-user-1", "yardone")
	if err != nil || !member {
		t.Fatalf("member=%v err=%v", member, err)
	}
	orgs, err := src.TenantOrgs(context.Background(), "platform-user-1")
	if err != nil {
		t.Fatalf("TenantOrgs: %v", err)
	}
	for _, v := range orgs {
		if v == "key-b3" {
			t.Fatalf("credential-shaped value in the answer")
		}
	}
	if atomic.LoadInt32(&stub.attempts) != 2 {
		t.Fatalf("attempts = %d", stub.attempts)
	}
}

// A shape-invalid slug is refused before any request leaves the process.
func TestAppwriteRejectsMalformedSlug(t *testing.T) {
	src, stub := newAppwrite(t, nil)
	if _, err := src.OrgMember(context.Background(), "platform-user-1", "Not-A-Slug"); err == nil {
		t.Fatalf("malformed slug accepted")
	}
	if atomic.LoadInt32(&stub.attempts) != 0 {
		t.Fatalf("request left the process for a malformed slug")
	}
}
