package authz

import (
	"context"
	"sync"
	"time"
)

// MembershipSource resolves platform Team membership (D28,
// docs/analysis/DECISIONS.md): per request against the platform source,
// with only a short-lived in-process cache in front. It resolves coarse
// membership only, never authority and never the credential; the
// credential is the platform JWT, verified per request by the identity
// verifier and never cached anywhere. Platform teams are named
// org:<slug> and tenant:<org>:<slug> (context section 3), so both methods
// key on the slug itself and touch no core table.
type MembershipSource interface {
	// OrgMember reports whether the platform user belongs to the team
	// org:<orgSlug>.
	OrgMember(ctx context.Context, platformUserID, orgSlug string) (bool, error)
	// TenantOrgs lists the distinct org slugs of the tenant:<org>:<slug>
	// teams the user belongs to. One org is the normal answer; the
	// middleware fails closed on zero or on more than one.
	TenantOrgs(ctx context.Context, platformUserID string) ([]string, error)
}

// OrgResolver resolves an org slug against the org anchor table. It is the
// ONLY core-database touch in the middleware and runs only on the success
// path, after Team membership passed (ADR-0020): a refused request
// executes no core-database statement. The implementation acquires its
// connection per call, which keeps pool acquisition observably later than
// org resolution (QA finding F5).
type OrgResolver interface {
	ResolveSlug(ctx context.Context, orgSlug string) (orgID string, ok bool, err error)
}

// Cache bounds for the D28 in-process membership cache. Stated as code,
// not prose, per the repository rule on committed text; the TTL is
// configurable so tests can exercise both directions of the revocation
// window (QA finding F4).
const (
	defaultMembershipTTL   = 30 * time.Second
	defaultMaxCacheEntries = 4096
)

// cachedEntry is one membership answer. It holds booleans and slug lists
// only: there is no field here that could hold a credential, by design.
type cachedEntry struct {
	expiresAt time.Time
	member    bool
	orgs      []string
}

// CachedMembership wraps a MembershipSource in the D28 short-lived
// in-process cache. Membership answers live at most the TTL, so a revoked
// membership takes effect within that window: refusal once the window has
// passed, bounded staleness within it. Errors are never cached, so a
// failed platform lookup is retried on the next request.
type CachedMembership struct {
	src     MembershipSource
	ttl     time.Duration
	max     int
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]cachedEntry
}

// NewCachedMembership wraps src with the default cache window.
func NewCachedMembership(src MembershipSource, ttl time.Duration) *CachedMembership {
	return newCachedMembership(src, ttl, 0, nil)
}

func newCachedMembership(src MembershipSource, ttl time.Duration, max int, now func() time.Time) *CachedMembership {
	if ttl <= 0 {
		ttl = defaultMembershipTTL
	}
	if max <= 0 {
		max = defaultMaxCacheEntries
	}
	if now == nil {
		now = time.Now
	}
	return &CachedMembership{
		src:     src,
		ttl:     ttl,
		max:     max,
		now:     now,
		entries: make(map[string]cachedEntry),
	}
}

// OrgMember implements MembershipSource.
func (c *CachedMembership) OrgMember(ctx context.Context, platformUserID, orgSlug string) (bool, error) {
	key := "member\x00" + platformUserID + "\x00" + orgSlug
	c.mu.Lock()
	entry, ok := c.entries[key]
	unexpired := ok && c.now().Before(entry.expiresAt)
	c.mu.Unlock()
	if unexpired {
		return entry.member, nil
	}
	member, err := c.src.OrgMember(ctx, platformUserID, orgSlug)
	if err != nil {
		return false, err
	}
	c.store(key, cachedEntry{member: member, expiresAt: c.now().Add(c.ttl)})
	return member, nil
}

// TenantOrgs implements MembershipSource.
func (c *CachedMembership) TenantOrgs(ctx context.Context, platformUserID string) ([]string, error) {
	key := "tenants\x00" + platformUserID
	c.mu.Lock()
	entry, ok := c.entries[key]
	unexpired := ok && c.now().Before(entry.expiresAt)
	c.mu.Unlock()
	if unexpired {
		return append([]string(nil), entry.orgs...), nil
	}
	orgs, err := c.src.TenantOrgs(ctx, platformUserID)
	if err != nil {
		return nil, err
	}
	c.store(key, cachedEntry{orgs: append([]string(nil), orgs...), expiresAt: c.now().Add(c.ttl)})
	return append([]string(nil), orgs...), nil
}

// store writes an entry under the bound: expired entries are swept first,
// and if the bound still stands, arbitrary live entries give way. Arbitrary
// eviction is safe here because every entry is a membership answer with a
// bounded TTL (D28): the worst case is an early refetch, never a wrong
// answer and never a credential.
func (c *CachedMembership) store(key string, entry cachedEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		now := c.now()
		for k, v := range c.entries {
			if !now.Before(v.expiresAt) {
				delete(c.entries, k)
			}
		}
		for k := range c.entries {
			if len(c.entries) < c.max {
				break
			}
			delete(c.entries, k)
		}
	}
	c.entries[key] = entry
}
