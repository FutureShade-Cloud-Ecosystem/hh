package main

// The x-role equality assertions (B-assert, security-critical): for every
// route the core registers, the gate the handler actually runs must equal
// the x-role the document declares for that operation, and the bearer class
// the gate actually requires must equal the document's security scheme.
// The single source of both document facts is the generated census table
// (core/internal/platform/apigen/routes.gen.go, Route.XRole and
// Route.Bearer, regenerated from core/api/openapi.yaml), and the binding
// from operationId to registration path is the parity test's own
// machinery: registerRoutes over a recordingRegistrar, patterns matched
// against the census. No route is named by a literal here. A new
// registration is asserted the moment it lands; a removed one stops being
// asserted; a census edit or a guard swap without the other fails below.
//
// The gate class is observed, not asked: each constructor family
// (core/internal/identity/guards/guards.go) refuses and admits on a fixed
// skeleton of probe legs, with statuses and bodies pinned by ADR-0020
// (docs/adr/0020-org-resolution.md, enforcement semantics and refusal
// statuses) and bodies that are the authz constants
// (core/internal/platform/authz/middleware.go). Expected values were
// cross-checked against the Integrations census column
// (tools/org/outbox/integrations/b1-inventory/guard-surface.txt section 3)
// per the Tech Lead's B1 and B3 review notes.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/shims"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/tenantconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

const (
	xrIssuer    = "xrole-issuer"
	xrStaffAud  = "xrole-staff"
	xrTenantAud = "xrole-tenant"
	xrKeyID     = "b4-xrole-rsa"
	xrSubject   = "b4-xrole-platform-user"
	xrTenantID  = "99999999-9999-4999-8999-999999999999"
	xrOrgSlug   = "xroleprobe"
	xrOrgID     = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

// xrMembers admits the one anchored slug on both legs: staff membership for
// the probe slug, and exactly one tenant org.
type xrMembers struct{ slug string }

func (m xrMembers) OrgMember(_ context.Context, _, slug string) (bool, error) {
	return slug == m.slug, nil
}

func (m xrMembers) TenantOrgs(context.Context, string) ([]string, error) {
	return []string{m.slug}, nil
}

// xrOrgs anchors the probe slug.
type xrOrgs struct{ slug, id string }

func (o xrOrgs) ResolveSlug(_ context.Context, slug string) (string, bool, error) {
	if slug == o.slug {
		return o.id, true, nil
	}
	return "", false, nil
}

// xrStaffSrc is the mutable authority double: the tier legs flip it per leg.
type xrStaffSrc struct{ auth identity.StaffAuthority }

func (s *xrStaffSrc) ResolveStaff(context.Context, string, string) (identity.StaffAuthority, bool, error) {
	return s.auth, true, nil
}

// xrPortalSrc is the mutable portal role double: the capability and no-row
// legs flip it per leg.
type xrPortalSrc struct {
	role string
	ok   bool
}

func (p *xrPortalSrc) PortalRole(context.Context, string, string) (string, bool, error) {
	return p.role, p.ok, nil
}

// xrFixture is the registered surface under probe: the same registerRoutes
// the binary serves, driven through the parity test's recordingRegistrar so
// the registered patterns are enumerable, over real guards wired on
// runtime-generated keys and the mutable doubles. All roles used below are
// non-driver, so the nil knob source never refuses the ceiling leg.
type xrFixture struct {
	keys       *rsa.PrivateKey
	reg        *recordingRegistrar
	staff      *xrStaffSrc
	portal     *xrPortalSrc
	registered []string
}

func newXRoleFixture(t *testing.T) *xrFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	verifier, err := identity.NewVerifier(identity.Config{
		KeySource:      identity.NewStaticKeySource(map[string]crypto.PublicKey{xrKeyID: &key.PublicKey}),
		Issuer:         xrIssuer,
		StaffAudience:  xrStaffAud,
		TenantAudience: xrTenantAud,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	gate, err := authz.NewGate(verifier, xrMembers{slug: xrOrgSlug}, xrOrgs{slug: xrOrgSlug, id: xrOrgID})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	fx := &xrFixture{
		keys:   key,
		staff:  &xrStaffSrc{auth: identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch}},
		portal: &xrPortalSrc{ok: true, role: identity.PortalRoleAccountAdmin},
	}
	g, err := guards.NewGuards(gate, fx.staff, fx.portal, nil)
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}
	fx.reg = &recordingRegistrar{inner: http.NewServeMux()}
	registerRoutes(fx.reg, g, shims.NewRecorder(256), tenantconfig.Default())
	fx.registered = fx.reg.patterns
	return fx
}

// staffToken mints a staff-class token with a short, in-ceiling window.
func (fx *xrFixture) staffToken(t *testing.T) string {
	t.Helper()
	return fx.mint(t, map[string]any{
		"iss": xrIssuer, "sub": xrSubject, "aud": xrStaffAud,
		"iat": time.Now().Add(-time.Minute).Unix(),
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	})
}

// tenantToken mints a tenant-class token for the probe tenant.
func (fx *xrFixture) tenantToken(t *testing.T) string {
	t.Helper()
	return fx.mint(t, map[string]any{
		"iss": xrIssuer, "sub": xrSubject, "aud": xrTenantAud,
		"iat":    time.Now().Add(-time.Minute).Unix(),
		"exp":    time.Now().Add(10 * time.Minute).Unix(),
		"tenant": xrTenantID,
	})
}

func (fx *xrFixture) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": xrKeyID, "typ": "JWT"}
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	signed := b64(hb) + "." + b64(cb)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, fx.keys, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed + "." + b64(sig)
}

// xrPathParam substitutes a fixed non-empty segment for every path
// parameter in a documented template, so the probe request matches the
// registered pattern without naming any resource.
var xrPathParam = regexp.MustCompile(`\{[^/}]+\}`)

// xrLeg is one probe: it sets the doubled authority and portal state and
// returns the complete request headers, Authorization included.
type xrLeg struct {
	name  string
	apply func(t *testing.T, fx *xrFixture) map[string]string
}

// xrLegs is the fixed probe skeleton every family is asserted against. The
// anonymous leg is first because the public family compares every
// admitting body against the anonymous body.
var xrLegs = []xrLeg{
	{"anonymous", func(*testing.T, *xrFixture) map[string]string { return nil }},
	{"malformed bearer", func(*testing.T, *xrFixture) map[string]string {
		return map[string]string{"Authorization": "Bearer not-a-token"}
	}},
	{"staff bearer without X-Org", func(t *testing.T, fx *xrFixture) map[string]string {
		fx.staff.auth = identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch}
		return map[string]string{"Authorization": "Bearer " + fx.staffToken(t)}
	}},
	{"staff tier 3 branch", func(t *testing.T, fx *xrFixture) map[string]string {
		fx.staff.auth = identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch}
		return map[string]string{"Authorization": "Bearer " + fx.staffToken(t), authz.HeaderXOrg: xrOrgSlug}
	}},
	{"staff tier 2 branch", func(t *testing.T, fx *xrFixture) map[string]string {
		fx.staff.auth = identity.StaffAuthority{RoleID: "branch_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch}
		return map[string]string{"Authorization": "Bearer " + fx.staffToken(t), authz.HeaderXOrg: xrOrgSlug}
	}},
	{"staff tier 1 global", func(t *testing.T, fx *xrFixture) map[string]string {
		fx.staff.auth = identity.StaffAuthority{RoleID: "general_manager", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
		return map[string]string{"Authorization": "Bearer " + fx.staffToken(t), authz.HeaderXOrg: xrOrgSlug}
	}},
	{"tenant account admin", func(t *testing.T, fx *xrFixture) map[string]string {
		fx.portal.ok, fx.portal.role = true, identity.PortalRoleAccountAdmin
		return map[string]string{"Authorization": "Bearer " + fx.tenantToken(t)}
	}},
	{"tenant field crew", func(t *testing.T, fx *xrFixture) map[string]string {
		fx.portal.ok, fx.portal.role = true, identity.PortalRoleFieldCrew
		return map[string]string{"Authorization": "Bearer " + fx.tenantToken(t)}
	}},
	{"tenant no portal row", func(t *testing.T, fx *xrFixture) map[string]string {
		fx.portal.ok, fx.portal.role = false, ""
		return map[string]string{"Authorization": "Bearer " + fx.tenantToken(t)}
	}},
	{"tenant bearer carrying X-Org", func(t *testing.T, fx *xrFixture) map[string]string {
		fx.portal.ok, fx.portal.role = true, identity.PortalRoleAccountAdmin
		return map[string]string{"Authorization": "Bearer " + fx.tenantToken(t), authz.HeaderXOrg: xrOrgSlug}
	}},
}

// xrLegByName finds one probe leg by name; the bearer test's legs are a
// subset of the skeleton and name them by constant string only.
func xrLegByName(t *testing.T, name string) xrLeg {
	t.Helper()
	for _, l := range xrLegs {
		if l.name == name {
			return l
		}
	}
	t.Fatalf("no probe leg named %q", name)
	return xrLeg{}
}

// xrWant is the pinned expectation for one leg under one family. Exactly
// one of the four shapes of pin holds: status alone (an admitted leg); body
// non-nil (a byte-identical family refusal); shape (a 400 X-Org shape
// refusal: the 400 bodies are package-private in authz by the landed B2
// review, so the pin is the status plus two properties from the ADR-0020
// addendum, never a family body and never an echo of a sent value);
// anonBody (admitted with the byte-identical anonymous answer, the public
// leg's zero-effect rule).
type xrWant struct {
	status   int
	body     []byte
	shape    bool
	anonBody bool
}

func xrAdmitted() xrWant { return xrWant{status: http.StatusOK} }

func xrAnonIdentical() xrWant { return xrWant{status: http.StatusOK, anonBody: true} }

func xrRefuse(status int, body []byte) xrWant { return xrWant{status: status, body: body} }

func xrShapeRefused() xrWant { return xrWant{status: http.StatusBadRequest, shape: true} }

// xrFamilyFor maps the document's x-role spelling onto the expectation
// table of one guard constructor family. The second return names the
// family for failure messages; ok false means the spelling names no family
// this suite can assert.
func xrFamilyFor(xrole string) (map[string]xrWant, string, bool) {
	shape := xrShapeRefused()
	u := xrRefuse(http.StatusUnauthorized, authz.BodyUnauthorized)
	f := xrRefuse(http.StatusForbidden, authz.BodyForbidden)
	switch {
	case xrole == "public":
		// The unguarded public leg (ADR-0020 leg 2): no credential, no
		// header, no bearer class has any effect on the answer.
		wants := make(map[string]xrWant, len(xrLegs))
		for _, leg := range xrLegs {
			wants[leg.name] = xrAnonIdentical()
		}
		return wants, "Public (unguarded)", true
	case xrole == "requireStaff":
		return map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": shape,
			"staff tier 3 branch":        xrAdmitted(), "staff tier 2 branch": xrAdmitted(), "staff tier 1 global": xrAdmitted(),
			"tenant account admin": f, "tenant field crew": f, "tenant no portal row": f, "tenant bearer carrying X-Org": f,
		}, "RequireStaff (any active grant)", true
	case xrole == "requireManager":
		return map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": shape,
			"staff tier 3 branch":        f, "staff tier 2 branch": xrAdmitted(), "staff tier 1 global": xrAdmitted(),
			"tenant account admin": f, "tenant field crew": f, "tenant no portal row": f, "tenant bearer carrying X-Org": f,
		}, "RequireManager (tier 1 or 2)", true
	case xrole == "requireAdmin":
		return map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": shape,
			"staff tier 3 branch":        f, "staff tier 2 branch": f, "staff tier 1 global": xrAdmitted(),
			"tenant account admin": f, "tenant field crew": f, "tenant no portal row": f, "tenant bearer carrying X-Org": f,
		}, "RequireAdmin (tier 1 global)", true
	case xrole == "tenant":
		return map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": f, "staff tier 3 branch": f, "staff tier 2 branch": f, "staff tier 1 global": f,
			// No capability is consulted: a role that holds nothing is still
			// admitted, and the no-row boundary is the unpinned leg guards.go
			// records as admitted.
			"tenant account admin": xrAdmitted(), "tenant field crew": xrAdmitted(), "tenant no portal row": xrAdmitted(),
			"tenant bearer carrying X-Org": shape,
		}, "RequireTenant (tenant bearer, portal row status)", true
	case xrole == "scope-member":
		return map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": shape,
			"staff tier 3 branch":        xrAdmitted(), "staff tier 2 branch": xrAdmitted(), "staff tier 1 global": xrAdmitted(),
			"tenant account admin": xrAdmitted(), "tenant field crew": xrAdmitted(), "tenant no portal row": xrAdmitted(),
			"tenant bearer carrying X-Org": shape,
		}, "RequireSync (either bearer, G4 bearer-conditioned)", true
	case strings.HasPrefix(xrole, "capability:"):
		// The capability family derives its tenant-leg expectations from the
		// same carried role-to-capability model the guard reads
		// (identity.CapabilitiesFor, pinned both-halves per capability in
		// guards_test.go), so the table stays correct for every capability
		// name without literals: a role that holds the named capability is
		// admitted, one that lacks it answers the RBAC-403, and no linked
		// portal user is no authority and answers the RBAC-403.
		capName := identity.Capability(strings.TrimPrefix(xrole, "capability:"))
		wants := map[string]xrWant{
			"anonymous": u, "malformed bearer": u,
			"staff bearer without X-Org": f, "staff tier 3 branch": f, "staff tier 2 branch": f, "staff tier 1 global": f,
			"tenant no portal row":         f,
			"tenant bearer carrying X-Org": shape,
		}
		for role, legName := range map[string]string{
			identity.PortalRoleAccountAdmin: "tenant account admin",
			identity.PortalRoleFieldCrew:    "tenant field crew",
		} {
			if identity.CapabilitiesFor(role).Has(capName) {
				wants[legName] = xrAdmitted()
			} else {
				wants[legName] = f
			}
		}
		return wants, "RequireCapability (" + xrole + ")", true
	}
	return nil, "", false
}

// xrCensusByPattern indexes the generated census by method-prefixed
// pattern, the same index the parity test asserts both directions against,
// so operationId, XRole and Bearer arrive through that machinery and never
// from a literal in this file.
func xrCensusByPattern(t *testing.T) map[string]apigen.Route {
	t.Helper()
	byPattern := make(map[string]apigen.Route, len(apigen.Routes))
	for _, route := range apigen.Routes {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("document has two operations for %s", pattern)
		}
		byPattern[pattern] = route
	}
	return byPattern
}

// probe issues one request through the registered surface.
func (fx *xrFixture) probe(t *testing.T, method, path string, header map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rsp := httptest.NewRecorder()
	fx.reg.inner.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.String()
}

// probeLeg runs one probe leg against one census route.
func (fx *xrFixture) probeLeg(t *testing.T, route apigen.Route, leg xrLeg) (int, string) {
	t.Helper()
	return fx.probe(t, route.Method, xrPathParam.ReplaceAllString(route.Path, "probe"), leg.apply(t, fx))
}

// TestRegisteredRouteGateMatchesDocumentXRole is the x-role equality
// assertion: for every registered route, the gate the handler runs is
// observed on the fixed probe skeleton and must match the expectation
// table of the family the document's x-role names. A document x-role edit
// without the gate, or a gate swap without the document, fails here.
func TestRegisteredRouteGateMatchesDocumentXRole(t *testing.T) {
	fx := newXRoleFixture(t)
	census := xrCensusByPattern(t)
	if len(fx.registered) == 0 {
		t.Fatal("registerRoutes registered nothing; the assertion surface is empty")
	}
	for _, pattern := range fx.registered {
		route, ok := census[pattern]
		if !ok {
			t.Fatalf("registered route %s is not in the document; the parity test owns this direction", pattern)
		}
		t.Run(route.OperationID, func(t *testing.T) {
			if route.XRole == "operator" {
				t.Fatalf("x-role operator (documented for %s) names a class no guard constructor implements: RequireStaffPredicate composes it once its lane records what the class means, and asserting a family the guard layer has not decided would invent policy", route.OperationID)
			}
			wants, family, ok := xrFamilyFor(route.XRole)
			if !ok {
				t.Fatalf("document x-role %q names no gate family this suite knows", route.XRole)
			}
			anonCode, anonBody := fx.probeLeg(t, route, xrLegs[0])
			for _, leg := range xrLegs {
				want, ok := wants[leg.name]
				if !ok {
					t.Fatalf("leg %q has no expectation under x-role %q; the table must be total over the probe skeleton", leg.name, route.XRole)
				}
				code, body := fx.probeLeg(t, route, leg)
				if code != want.status {
					t.Fatalf("leg %q: status %d, want %d under document x-role %q (%s); the registered gate does not equal the document",
						leg.name, code, want.status, route.XRole, family)
				}
				if want.body != nil && body != string(want.body) {
					t.Fatalf("leg %q: refusal body %q, want the pinned family body %q", leg.name, body, want.body)
				}
				if want.shape {
					for _, familyBody := range [][]byte{authz.BodyUnauthorized, authz.BodyForbidden, authz.BodyUnavailable} {
						if body == string(familyBody) {
							t.Fatalf("leg %q: the 400 shape refusal wears a family body %q", leg.name, body)
						}
					}
					if strings.Contains(body, xrOrgSlug) || strings.Contains(body, xrSubject) || strings.Contains(body, xrTenantID) {
						t.Fatalf("leg %q: the 400 shape refusal echoes a sent value: %q", leg.name, body)
					}
				}
				if want.anonBody && (code != anonCode || body != anonBody) {
					t.Fatalf("leg %q: changed the public answer (ADR-0020 leg 2 zero effect): status %d body %q, want the anonymous answer %d %q",
						leg.name, code, body, anonCode, anonBody)
				}
			}
		})
	}
	t.Logf("x-role equality asserted for %d registered operations against the census families", len(fx.registered))
}

// TestRegisteredRouteBearerMatchesCensus asserts the census Bearer column
// equals the bearer class the registered gate actually requires, observed
// as which credential class the gate admits and which it refuses with the
// RBAC-403. A shim registered under the wrong bearer family, or a census
// Bearer edit without the gate, fails here even when the x-role family
// assertion alone would pass; the sync pair is the class where the two are
// independent (Tech Lead B3 review note). The staff probe is the tier 1
// global leg, the one staff caller every staff-family constructor admits,
// so the observation isolates the credential class from the x-role tier
// gates (a tier 3 caller on a manager route is refused by the gate, not by
// the bearer class, and would conflate the two assertions).
func TestRegisteredRouteBearerMatchesCensus(t *testing.T) {
	fx := newXRoleFixture(t)
	census := xrCensusByPattern(t)
	for _, pattern := range fx.registered {
		route, ok := census[pattern]
		if !ok {
			t.Fatalf("registered route %s is not in the document; the parity test owns this direction", pattern)
		}
		t.Run(route.OperationID, func(t *testing.T) {
			assert := func(legName string, want xrWant) {
				t.Helper()
				code, body := fx.probeLeg(t, route, xrLegByName(t, legName))
				if code != want.status {
					t.Fatalf("bearer class %q: leg %q status %d, want %d; the gate's bearer requirement does not equal the census",
						route.Bearer, legName, code, want.status)
				}
				if want.body != nil && body != string(want.body) {
					t.Fatalf("bearer class %q: leg %q refusal body %q, want %q", route.Bearer, legName, body, want.body)
				}
			}
			switch route.Bearer {
			case "staff":
				assert("staff tier 1 global", xrAdmitted())
				assert("tenant account admin", xrRefuse(http.StatusForbidden, authz.BodyForbidden))
			case "tenant":
				assert("tenant account admin", xrAdmitted())
				assert("staff tier 1 global", xrRefuse(http.StatusForbidden, authz.BodyForbidden))
			case "staff-or-tenant":
				assert("staff tier 1 global", xrAdmitted())
				assert("tenant account admin", xrAdmitted())
			case "public":
				assert("anonymous", xrAdmitted())
			default:
				t.Fatalf("census Bearer %q names no bearer class the guard constructors define", route.Bearer)
			}
		})
	}
	t.Logf("bearer class asserted for %d registered operations against the census column", len(fx.registered))
}
