package authz

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// stubSource is a counting MembershipSource with settable answers.
type stubSource struct {
	mu     sync.Mutex
	member bool
	orgs   []string
	err    error
	calls  int
}

func (s *stubSource) OrgMember(context.Context, string, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.member, s.err
}

func (s *stubSource) TenantOrgs(context.Context, string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.orgs, s.err
}

func (s *stubSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// The D28 window, both directions, on the cache in isolation: answers
// hold for at most the TTL, so a revocation takes effect within the
// window, and refusal after it.
func TestCachedMembershipWindow(t *testing.T) {
	clk := &testClock{now: time.Now()}
	src := &stubSource{member: true}
	cached := newCachedMembership(src, time.Minute, 0, clk.Now)
	ctx := context.Background()

	if member, err := cached.OrgMember(ctx, "user-1", "alpha"); err != nil || !member {
		t.Fatalf("first: member=%v err=%v", member, err)
	}
	if src.count() != 1 {
		t.Fatalf("source calls = %d", src.count())
	}

	// Inside the window the answer is served from cache.
	if member, err := cached.OrgMember(ctx, "user-1", "alpha"); err != nil || !member {
		t.Fatalf("cached: member=%v err=%v", member, err)
	}
	if src.count() != 1 {
		t.Fatalf("cache did not hold: %d calls", src.count())
	}

	// A different key does not share the entry.
	if _, _ = cached.OrgMember(ctx, "user-1", "beta"); src.count() != 2 {
		t.Fatalf("keys collide: %d calls", src.count())
	}
	if _, _ = cached.OrgMember(ctx, "user-2", "alpha"); src.count() != 3 {
		t.Fatalf("subjects collide: %d calls", src.count())
	}

	// Past the window, the source is asked again and the revocation
	// lands.
	src.member = false
	clk.advance(2 * time.Minute)
	if member, err := cached.OrgMember(ctx, "user-1", "alpha"); err != nil || member {
		t.Fatalf("post-window: member=%v err=%v, want refusal", member, err)
	}
	if src.count() != 4 {
		t.Fatalf("expected a refetch, calls=%d", src.count())
	}
}

func TestCachedMembershipTenantOrgs(t *testing.T) {
	clk := &testClock{now: time.Now()}
	src := &stubSource{orgs: []string{"alpha"}}
	cached := newCachedMembership(src, time.Minute, 0, clk.Now)
	ctx := context.Background()

	orgs, err := cached.TenantOrgs(ctx, "user-1")
	if err != nil || len(orgs) != 1 || orgs[0] != "alpha" {
		t.Fatalf("first: orgs=%v err=%v", orgs, err)
	}

	// The returned slice is a copy: mutating it cannot corrupt the cache.
	orgs[0] = "tampered"
	again, err := cached.TenantOrgs(ctx, "user-1")
	if err != nil || again[0] != "alpha" {
		t.Fatalf("cache corrupted by caller mutation: %v", again)
	}
}

// Errors are never cached: a failed platform lookup is retried on the
// next request rather than frozen as an answer.
func TestCachedMembershipErrorsNotCached(t *testing.T) {
	clk := &testClock{now: time.Now()}
	src := &stubSource{member: true, err: errors.New("platform down")}
	cached := newCachedMembership(src, time.Minute, 0, clk.Now)
	ctx := context.Background()

	if _, err := cached.OrgMember(ctx, "user-1", "alpha"); err == nil {
		t.Fatalf("error swallowed")
	}
	src.mu.Lock()
	src.err = nil
	src.mu.Unlock()
	if member, err := cached.OrgMember(ctx, "user-1", "alpha"); err != nil || !member {
		t.Fatalf("recovery blocked by a cached error: member=%v err=%v", member, err)
	}
}

// The cache is bounded: no unbounded growth from distinct keys.
func TestCachedMembershipBounded(t *testing.T) {
	clk := &testClock{now: time.Now()}
	src := &stubSource{}
	cached := newCachedMembership(src, time.Minute, 8, clk.Now)
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		if _, err := cached.OrgMember(ctx, "user", slugOf(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	cached.mu.Lock()
	size := len(cached.entries)
	cached.mu.Unlock()
	if size > 8 {
		t.Fatalf("cache grew past its bound: %d entries", size)
	}
}

func slugOf(i int) string {
	if i == 0 {
		return "orgzero"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return "org" + digits
}

func TestCachedMembershipConcurrent(t *testing.T) {
	clk := &testClock{now: time.Now()}
	src := &stubSource{member: true}
	cached := newCachedMembership(src, time.Minute, 0, clk.Now)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cached.OrgMember(context.Background(), "user-1", "alpha"); err != nil {
				t.Errorf("concurrent lookup: %v", err)
			}
		}()
	}
	wg.Wait()
}
