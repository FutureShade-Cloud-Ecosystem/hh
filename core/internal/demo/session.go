//go:build demo

// The demo module (W2, the plan's 3.2 piece 1): the tap-a-role sign-in,
// carried from the older app's demo. GET /api/v1/demo/flows lists the
// closed flow table and issues the visitor cookie the mint keys on when
// the request carries none (visitor.go); POST /api/v1/demo/session mints
// one demo session for one named flow. Both routes are pre-auth by
// design (the contract's x-role public, security none), a recorded
// demo-only exception to D38 (D49), and they register only in the demo
// build, at the marked block in core/cmd/core/demo_on.go, on the same
// public leg the tenant config uses.
//
// The request carries a flow name and nothing else, so there is nowhere
// to put an email or a role: the closed flow table names the account, the
// token subject and the label, and the account the table names is the
// only thing a mint can answer. Each flow names the role its account must
// hold, and the core's StaffAuthorityResolver must answer that role in
// the demo org or the mint refuses: the table's word about an account is
// never enough on its own, the database's live grant decides.
//
// The order inside a mint is the plan's own: the limits refuse the burst
// first (the mint is granted only when its own key, the visitor id when
// the request carries a well formed one and the address otherwise, and
// the address ceiling would both admit it, and only a granted mint
// spends either, so a refusal spends nothing), then the audit row is
// written, and only then is the token minted, so the bypass's trail
// always exists by the time its credential does, and a burst leaves
// neither.
//
// Everything after the token is the real path: the token is an ordinary
// staff bearer the unchanged verifier classifies, and the org, the role
// grants and the dealer's role configuration hold it per request exactly
// as they hold a platform-issued one.
//
// The response's role label comes from the flow table, never from the
// roles table (the older app's lesson): the table is the one copy of the
// labels the testers see.
package demo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Minter is the narrow seam the module needs of the demo issuer
// (issuer.Issuer satisfies it): sign one session token for one platform
// user id. The subject is never taken from the request.
type Minter interface {
	Mint(subject string) (string, error)
}

// StaffResolver is the narrow seam the module needs of the core's staff
// authority resolver (identity.StaffAuthorityResolver satisfies it): the
// live answer to "what does this platform user hold in this org", read
// from the app-role tables per call. The mint trusts it, never the flow
// table alone, and never any role fact carried in a request.
type StaffResolver interface {
	ResolveStaff(ctx context.Context, platformUserID, orgID string) (identity.StaffAuthority, bool, error)
}

// AuditRecorder is the narrow seam the module needs of the audit module
// (audit.Service satisfies it): the one way a sibling writes to
// audit_log, where the ledger already writes. The mint's row is the
// bypass's trail, and it is written before the token exists.
type AuditRecorder interface {
	Record(ctx context.Context, a audit.SensitiveAccess) error
}

// The mint's audit row vocabulary. audit_log's action is free text; the
// entity type follows the middleware's derived spelling of the route
// (demo.session), and the entity and actor are the account the session
// is minted for.
const (
	auditAction     = "DEMO_SESSION_MINTED"
	auditEntityType = "demo.session"
	auditPurpose    = "DEMO_SIGN_IN"
)

// MintDeps is the module's constructor input: every collaborator arrives
// here, none is reached around the constructor.
type MintDeps struct {
	// Minter signs the session token; required.
	Minter Minter
	// Orgs resolves the demo org's slug to its id (the platform org
	// resolver, the same seam the gate resolves X-Org with); required.
	Orgs authz.OrgResolver
	// Staff resolves the flow account's live authority; required.
	Staff StaffResolver
	// Audit writes the mint's trail row; required.
	Audit AuditRecorder
	// Limit refuses a burst from the mint's own key: the visitor id when
	// the request carries a well formed one, the address otherwise; a nil
	// limit selects the default allowance and window.
	Limit *AddressLimit
	// Ceiling caps the mints one address may spend however its visitor
	// ids rotate; a nil ceiling selects the team scale default allowance
	// (DefaultAddressCeiling) and the default window.
	Ceiling *AddressLimit
	// Now pins the clock for the limits when a test drives one; nil means
	// the wall clock.
	Now func() time.Time
}

// Mint is the demo module: the two routes the entry page calls.
type Mint struct {
	minter  Minter
	orgs    authz.OrgResolver
	staff   StaffResolver
	audit   AuditRecorder
	limit   *AddressLimit
	ceiling *AddressLimit
}

// NewMint validates the collaborators and returns the module.
func NewMint(d MintDeps) (*Mint, error) {
	if d.Minter == nil {
		return nil, errors.New("demo: the mint requires a session issuer")
	}
	if d.Orgs == nil {
		return nil, errors.New("demo: the mint requires an org resolver")
	}
	if d.Staff == nil {
		return nil, errors.New("demo: the mint requires a staff authority resolver")
	}
	if d.Audit == nil {
		return nil, errors.New("demo: the mint requires an audit recorder")
	}
	limit := d.Limit
	if limit == nil {
		limit = NewAddressLimit(0, 0, d.Now)
	}
	ceiling := d.Ceiling
	if ceiling == nil {
		ceiling = NewAddressLimit(DefaultAddressCeiling, 0, d.Now)
	}
	return &Mint{minter: d.Minter, orgs: d.Orgs, staff: d.Staff, audit: d.Audit, limit: limit, ceiling: ceiling}, nil
}

// flowRow is one DemoFlow of the contract: the flow name, the staff role
// code the flow's account holds, and the label the entry page renders.
// The row carries no account name and no address: the listing is pre-auth
// and minimal on purpose.
type flowRow struct {
	Flow     string `json:"flow"`
	RoleCode string `json:"role_code"`
	Label    string `json:"label"`
}

// flowList is the DemoFlowList envelope.
type flowList struct {
	Flows []flowRow `json:"flows"`
}

// Flows answers GET /api/v1/demo/flows: the closed table, in the table's
// own stable order, with the visitor cookie issued when the request
// carries none. The cookie is the mint's per browser key behind a host
// edge; its value is answered once in the Set-Cookie line and nowhere
// else.
func (m *Mint) Flows(w http.ResponseWriter, r *http.Request) {
	setVisitorCookie(w, r)
	all := flows.All()
	rows := make([]flowRow, 0, len(all))
	for _, a := range all {
		rows = append(rows, flowRow{Flow: a.Flow, RoleCode: a.Role, Label: a.Label})
	}
	httpx.WriteJSON(w, http.StatusOK, flowList{Flows: rows})
}

// sessionRequest is the DemoSessionRequest: one enum field and nothing
// else, by design.
type sessionRequest struct {
	Flow string `json:"flow"`
}

// sessionUser is the staff login user block (the contract's
// IdentityUser), field for field, so the client's session plumbing needs
// no second shape. The role name is the flow table's label, never the
// roles table's; the tier and scope are the resolver's live answer, the
// scope spelled the contract's enum (GLOBAL, BRANCH).
type sessionUser struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	RoleCode    string `json:"role_code"`
	RoleName    string `json:"role_name"`
	Tier        int    `json:"tier"`
	Scope       string `json:"scope"`
}

// sessionResponse is the DemoSessionResponse. chapter_key stays absent:
// the closed table names no walkthrough, and an absent optional field is
// the honest answer.
type sessionResponse struct {
	Token string      `json:"token"`
	User  sessionUser `json:"user"`
	Flow  string      `json:"flow"`
}

// maxSessionBodyBytes bounds the request body: the contract's request is
// one short string field, so anything larger is not a client of this
// route.
const maxSessionBodyBytes = 4096

// Session answers POST /api/v1/demo/session: mint one session for the
// account one named flow stands for. The mint refuses a burst from its
// own key and caps the address itself, refuses unless the staff resolver
// answers the flow's role for the account in the demo org, and writes
// the audit row before the token: a flow whose account reaches nothing,
// or reaches the wrong role, mints nothing and trails nothing, and the
// coarse audit middleware takes no row for this route at all, so a
// refused mint trails nothing anywhere.
func (m *Mint) Session(w http.ResponseWriter, r *http.Request) {
	var in sessionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionBodyBytes))
	// The request carries a flow name and nothing else, by design: an
	// unknown field or a trailing value is refused, not silently dropped.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "demo: the body must be one JSON object carrying only the flow name")
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		httpx.Error(w, http.StatusBadRequest, "demo: the body must be one JSON object carrying only the flow name")
		return
	}
	if in.Flow == "" {
		httpx.Error(w, http.StatusBadRequest, "demo: flow is required")
		return
	}
	account, ok := flows.Get(in.Flow)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "demo: unknown flow: the entry page offers only the flows GET /api/v1/demo/flows lists")
		return
	}

	// The two bucket check, in the order the ceiling then the key is
	// asked: the mint is granted only when BOTH buckets would admit it,
	// and only a granted mint spends either, so no refusal spends
	// anything, here or in either bucket. A tester locked out by the
	// team's ceiling keeps their own allowance, and a spent key
	// hammering the mint drains no team allowance. The two limits keep
	// their own mutexes and the peeks and the records are separate
	// instants, so two concurrent mints at the boundary can both peek
	// fit and both spend, and N concurrent at the boundary can overshoot
	// the allowance by N minus 1: that is the sweep class's own
	// looseness, a few extra mints, never a wrong refusal.
	address := clientAddress(r)
	key := "a:" + address
	if id := visitorIDFrom(r); id != "" {
		key = "v:" + id
	}
	if !m.ceiling.Peek(address) {
		httpx.Error(w, http.StatusTooManyRequests, "demo: too many sign-in attempts from this address, let the window pass and tap the card again")
		return
	}
	if !m.limit.Peek(key) {
		httpx.Error(w, http.StatusTooManyRequests, "demo: too many sign-in attempts from this address, let the window pass and tap the card again")
		return
	}
	m.limit.Record(key)
	m.ceiling.Record(address)

	orgID, ok, err := m.orgs.ResolveSlug(r.Context(), flows.OrgSlug)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "demo: the demo org could not be resolved")
		return
	}
	if !ok {
		// Unreachable on a guarded box (the boot guard refuses a database
		// whose one org is not the demo's), refused here so the module
		// alone still fails closed.
		httpx.Error(w, http.StatusInternalServerError, "demo: the demo org is missing")
		return
	}

	authority, ok, err := m.staff.ResolveStaff(r.Context(), account.PlatformUserID, orgID)
	if err != nil {
		if errors.Is(err, identity.ErrStaffUserInactive) {
			httpx.Error(w, http.StatusForbidden, "demo: the flow's account is not active, no session is minted")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "demo: the flow's authority could not be resolved")
		return
	}
	if !ok {
		httpx.Error(w, http.StatusForbidden, "demo: the flow's account holds no staff grant in the demo org, no session is minted")
		return
	}
	if authority.RoleID != account.Role {
		httpx.Error(w, http.StatusForbidden, "demo: the flow's account holds "+authority.RoleID+", the flow requires "+account.Role+", no session is minted")
		return
	}

	// The audit row first: the bypass's trail exists by the time its
	// credential does, and a failure to trail is a failure to mint. The
	// row is the record of an attempt that passed every check, not proof
	// a credential left the door: an issuer failure after it leaves one
	// MINTED row with no session, the safe direction, a credential with
	// no row being impossible.
	err = m.audit.Record(r.Context(), audit.SensitiveAccess{
		Action:     auditAction,
		EntityType: auditEntityType,
		EntityID:   account.PlatformUserID,
		ActorID:    account.PlatformUserID,
		Purpose:    auditPurpose,
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "demo: the session's audit row could not be written, no session is minted")
		return
	}

	token, err := m.minter.Mint(account.PlatformUserID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "demo: the session could not be minted")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sessionResponse{
		Token: token,
		User: sessionUser{
			UserID:      account.PlatformUserID,
			Email:       account.Email,
			DisplayName: account.DisplayName,
			RoleCode:    account.Role,
			RoleName:    account.Label,
			Tier:        authority.Tier,
			Scope:       strings.ToUpper(authority.Scope),
		},
		Flow: account.Flow,
	})
}

// clientAddress is the one address a mint arrived from: the limit's
// fallback key when the request carries no well formed visitor id, and
// the address ceiling's key always. Behind the box's own proxy (the demo
// image's Caddy on loopback) every connection's peer is the proxy, so
// the peer address alone would be one bucket for the whole box; there,
// the first address of the forwarded chain names the tester instead.
// From any other peer the header is untrusted and the connection's own
// address stands: a direct client can write any header it likes. An
// address the net package cannot split is taken as it stands rather than
// discarded, so a peer shape the split does not expect still counts
// against something.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !isLoopbackHost(host) {
		return host
	}
	if chain := r.Header.Get("X-Forwarded-For"); chain != "" {
		first := strings.TrimSpace(strings.Split(chain, ",")[0])
		if first != "" {
			return first
		}
	}
	return host
}

// isLoopbackHost reports whether the address is one of the box's own
// loopback forms, the proxy's signature. An unparseable address is not
// loopback: only a real loopback peer may speak for another address.
func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
