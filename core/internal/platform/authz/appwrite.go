package authz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AppwriteMembershipSource is the concrete MembershipSource: a thin client
// over the Appwrite server API (D28: membership resolves against the
// platform per request, coarse membership only, never authority and never
// the credential). Platform org teams are named org-<slug> (OrgTeamID; the
// Console refuses a colon in a Team id and caps it at 36 characters) and
// tenant teams are ten-<slug> (TenantTeamID), so both methods read the user's
// team membership list and key on the team id: no core table is touched,
// which is what keeps the zero-statement property whole. Exactly one
// attempt per call: no retries anywhere in this file, so an unreachable
// platform is one failed request, never a retry storm behind a locked
// request. Errors are fail-closed inputs to the 503 family; no error
// message carries a response body, a team id or the API key.
//
// Endpoint paths are the B3 proposal against the Appwrite server REST API
// and are subject to the S2 check (real Appwrite tokens, an HH-5
// precondition): if S2 moves them, the change is confined to this file.
type AppwriteMembershipSource struct {
	endpoint string
	project  string
	apiKey   string
	hc       *http.Client
}

const (
	appwriteDefaultTimeout = 10 * time.Second
	// appwriteListLimit is the page size for the membership listing. A
	// listing larger than one page is refused rather than answered
	// partially: a missing page could hide the membership the caller
	// claims, and fail closed is the rule. The bound is a named constant
	// so raising it is a one-line, reviewed change.
	appwriteListLimit = 100
)

// NewAppwriteMembershipSource validates the configuration and returns the
// source. The endpoint must be absolute http or https; the project id and
// the API key arrive from environment at spawn and are never defaulted,
// never logged and never written into any cache entry (D28: the cache
// stores answers, never the credential). A nil client selects one with the
// package timeout; tests inject their own.
func NewAppwriteMembershipSource(endpoint, project, apiKey string, hc *http.Client) (*AppwriteMembershipSource, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("authz: appwrite endpoint must be an absolute http or https URL")
	}
	if project == "" {
		return nil, errors.New("authz: appwrite project is required")
	}
	if apiKey == "" {
		return nil, errors.New("authz: appwrite api key is required")
	}
	// A redirect is an operational failure: the key header is never
	// re-sent to wherever a 3xx points.
	return &AppwriteMembershipSource{endpoint: u.String(), project: project, apiKey: apiKey, hc: refusingRedirects(hc)}, nil
}

// appwriteMembership is the one response field both methods read.
type appwriteMembership struct {
	TeamID string `json:"teamId"`
	UserID string `json:"userId"`
}

type appwriteMembershipPage struct {
	Total       int                  `json:"total"`
	Memberships []appwriteMembership `json:"memberships"`
}

// orgTeamPrefix and maxTeamIDLen are the Console's Team id rules as they
// bear on org teams: at most 36 characters from a-z A-Z 0-9 period hyphen
// underscore, not starting with a special character, and no colon.
const (
	orgTeamPrefix = "org-"
	maxTeamIDLen  = 36
)

// tenantTeamPrefix is the contractor Team family: one Team per org named
// ten-<slug>, built the way the org Team is. A colon is refused by the
// Console and a tenant account id cannot fit beside an org slug in 36
// characters, so the Team says "a contractor of this org" and the portal
// row says which contractor account. The prefix is four characters, like
// the org one, so a 32 character slug still fits.
const tenantTeamPrefix = "ten-"

// TenantTeamID is the one place the tenant Team id form is spelled. It
// returns ten-<slug>, and false when the slug fails the pinned shape or the
// result would exceed the Console's cap. Nothing is truncated.
func TenantTeamID(slug string) (string, bool) {
	if !orgSlugPattern.MatchString(slug) {
		return "", false
	}
	id := tenantTeamPrefix + slug
	if len(id) > maxTeamIDLen {
		return "", false
	}
	return id, true
}

// OrgTeamID is the one place the org Team id form is spelled. It returns
// org-<slug>, and false when the slug fails the pinned shape or the result
// would exceed the Console's 36 character cap. Nothing is truncated: two
// slugs never share a Team.
func OrgTeamID(slug string) (string, bool) {
	if !orgSlugPattern.MatchString(slug) {
		return "", false
	}
	id := orgTeamPrefix + slug
	// Guards a future widening of orgSlugPattern; today the pattern's 32 character cap already keeps the id within the limit.
	if len(id) > maxTeamIDLen {
		return "", false
	}
	return id, true
}

// OrgMember reports whether the platform user belongs to the team
// org-<orgSlug>. Absence is the boolean false, not an error: the ADR-0020
// rule needs an unknown slug and an absent membership to answer
// identically downstream.
func (s *AppwriteMembershipSource) OrgMember(ctx context.Context, platformUserID, orgSlug string) (bool, error) {
	teamID, ok := OrgTeamID(orgSlug)
	if !ok {
		return false, fmt.Errorf("authz: org slug does not match the pinned shape")
	}
	memberships, err := s.userMemberships(ctx, platformUserID)
	if err != nil {
		return false, err
	}
	for _, m := range memberships {
		// Both fields must match: the listing is scoped to the user, so a
		// row naming another user is not this caller's membership and is
		// never trusted.
		if m.TeamID == teamID && m.UserID == platformUserID {
			return true, nil
		}
	}
	return false, nil
}

// TenantOrgs lists the distinct org slugs of the ten-<slug> teams
// the user belongs to. One org is the normal answer; the middleware fails
// closed on zero or on more than one.
func (s *AppwriteMembershipSource) TenantOrgs(ctx context.Context, platformUserID string) ([]string, error) {
	memberships, err := s.userMemberships(ctx, platformUserID)
	if err != nil {
		return nil, err
	}
	var orgs []string
	seen := make(map[string]bool)
	for _, m := range memberships {
		org, ok := tenantTeamOrg(m.TeamID)
		if !ok {
			continue
		}
		if !seen[org] {
			seen[org] = true
			orgs = append(orgs, org)
		}
	}
	return orgs, nil
}

// tenantTeamOrg extracts the org slug of a ten-<slug> team id. The slug
// must pass the pinned shape, so anything else is not a tenant team and is
// skipped rather than trusted.
func tenantTeamOrg(teamID string) (string, bool) {
	slug, ok := strings.CutPrefix(teamID, tenantTeamPrefix)
	if !ok || !orgSlugPattern.MatchString(slug) {
		return "", false
	}
	return slug, true
}

// userMemberships performs the one request both methods share: the user's
// team membership listing, one attempt, one page, refused when truncated.
func (s *AppwriteMembershipSource) userMemberships(ctx context.Context, platformUserID string) ([]appwriteMembership, error) {
	if platformUserID == "" {
		return nil, errors.New("authz: platform user id is required")
	}
	u := fmt.Sprintf("%s/users/%s/memberships?limit=%d", s.endpoint, url.PathEscape(platformUserID), appwriteListLimit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.New("authz: appwrite membership request could not be built")
	}
	req.Header.Set("X-Appwrite-Project", s.project)
	req.Header.Set("X-Appwrite-Key", s.apiKey)
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, errors.New("authz: appwrite membership lookup failed")
	}
	defer func() {
		// The body is drained only to release the connection; its content
		// is never read into anything that could reach an error or a log.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authz: appwrite membership lookup answered %d", resp.StatusCode)
	}
	var page appwriteMembershipPage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&page); err != nil {
		return nil, errors.New("authz: appwrite membership listing is malformed")
	}
	if page.Total > len(page.Memberships) {
		return nil, fmt.Errorf("authz: appwrite membership listing truncated (%d of %d)", len(page.Memberships), page.Total)
	}
	return page.Memberships, nil
}
