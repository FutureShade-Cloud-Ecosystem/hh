// Package shims registers the identity-owned route shims: the lane's proof
// surface for route behavior on JWKS-signed test tokens (EM ruling on QA
// finding F1, recorded in ADR-0020's recorded-in-context section). The
// routes are shaped exactly like the contract's routes
// (core/api/openapi.yaml, path, method and x-role class per operation) and
// are superseded by real modules in Phase C: every handler is a
// reached-recorder that records the stamped scope facts and answers 200
// with a minimal body, and no handler touches any table.
package shims

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// StaffFacts are the stamped staff scope facts one reached shim records.
type StaffFacts struct {
	PlatformUserID string
	OrgSlug        string
	OrgID          string
	RoleID         string
	Tier           int
	Scope          string
}

// TenantFacts are the stamped tenant scope facts one reached shim records.
type TenantFacts struct {
	PlatformUserID string
	OrgSlug        string
	OrgID          string
	TenantID       string
}

// Reached is one reached-recorder entry: the operation, and the scope
// facts the guards stamped when it was reached. Exactly one of Staff,
// Tenant is set on guarded routes; neither is set on the public route,
// where no scope exists by design.
type Reached struct {
	Operation string
	Staff     *StaffFacts
	Tenant    *TenantFacts
}

// Recorder collects Reached entries under a bound, so the proof surface
// cannot grow memory without limit. It holds scope facts only: no header
// values, no tokens, no request bodies.
type Recorder struct {
	mu      sync.Mutex
	entries []Reached
	limit   int
}

// NewRecorder returns a Recorder holding at most limit entries; the oldest
// give way. limit must be positive.
func NewRecorder(limit int) *Recorder {
	if limit < 1 {
		limit = 1
	}
	return &Recorder{limit: limit}
}

func (rec *Recorder) record(entry Reached) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.entries) >= rec.limit {
		rec.entries = rec.entries[1:]
	}
	rec.entries = append(rec.entries, entry)
}

// Entries returns a copy of the recorded entries.
func (rec *Recorder) Entries() []Reached {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]Reached(nil), rec.entries...)
}

// RegisterRoutes registers the shim surface the real modules have not yet
// replaced: a representative tenant set from /api/v1/tenant, the two
// dual-bearer sync operations, and one capability-qualified portal
// operation, each named by its contract operationId. The order lifecycle
// five and the public quote view were this surface's first half and came
// out when the money-spine batch (HH-3.2 B4) registered the real order and
// quote routes on the serve mux: Security S-3's per-operationId removal,
// with the ServeMux duplicate-registration panic as the backstop. They
// survive only as the DB-free proof registrar below, which no served mux
// ever registers.
// staffRecorder and publicRecorder are the reached-recorder handler
// factories the registrars share (they were closures inside one registrar
// before the superseded split).
func staffRecorder(rec *Recorder, operation string) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		rec.record(Reached{Operation: operation, Staff: &StaffFacts{
			PlatformUserID: caller.PlatformUserID,
			OrgSlug:        caller.OrgSlug,
			OrgID:          caller.OrgID,
			RoleID:         caller.RoleID,
			Tier:           caller.Tier,
			Scope:          caller.Scope,
		}})
		writeOK(w)
	}
}

func publicRecorder(rec *Recorder, operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec.record(Reached{Operation: operation})
		writeOK(w)
	}
}

// Registrar is the compatibility alias the Phase C B3 ruling keeps
// at the old home after the interface promoted to httpx.
type Registrar = httpx.Registrar

func RegisterRoutes(r httpx.Registrar, g *guards.Guards, rec *Recorder) {
	tenant := func(operation string) guards.TenantHandler {
		return func(w http.ResponseWriter, r *http.Request, scope authz.TenantScope) {
			rec.record(Reached{Operation: operation, Tenant: &TenantFacts{
				PlatformUserID: scope.PlatformUserID,
				OrgSlug:        scope.OrgSlug,
				OrgID:          scope.OrgID,
				TenantID:       scope.TenantID,
			}})
			writeOK(w)
		}
	}
	sync := func(operation string) guards.SyncHandler {
		return func(w http.ResponseWriter, r *http.Request, caller guards.SyncCaller) {
			entry := Reached{Operation: operation}
			if caller.Staff != nil {
				entry.Staff = &StaffFacts{
					PlatformUserID: caller.Staff.PlatformUserID,
					OrgSlug:        caller.Staff.OrgSlug,
					OrgID:          caller.Staff.OrgID,
					RoleID:         caller.Staff.RoleID,
					Tier:           caller.Staff.Tier,
					Scope:          caller.Staff.Scope,
				}
			}
			if caller.Tenant != nil {
				entry.Tenant = &TenantFacts{
					PlatformUserID: caller.Tenant.PlatformUserID,
					OrgSlug:        caller.Tenant.OrgSlug,
					OrgID:          caller.Tenant.OrgID,
					TenantID:       caller.Tenant.TenantID,
				}
			}
			rec.record(entry)
			writeOK(w)
		}
	}
	// A representative tenant set from /api/v1/tenant, x-role tenant.
	r.Handle("GET /api/v1/tenant/me", g.RequireTenant(tenant("tenantGetMe")))
	r.Handle("PATCH /api/v1/tenant/me", g.RequireTenant(tenant("tenantUpdateMe")))
	r.Handle("POST /api/v1/tenant/token/refresh", g.RequireTenant(tenant("tenantRefreshToken")))

	// The two dual-bearer sync operations, x-role scope-member; the G4
	// rule is bearer-conditioned inside the guard.
	r.Handle("GET /api/v1/sync/{scope}", g.RequireSync(sync("syncPull")))
	r.Handle("POST /api/v1/sync/{scope}", g.RequireSync(sync("syncPush")))

	// One capability-qualified portal operation, x-role
	// capability:view_billing.
	r.Handle("GET /api/v1/tenant/invoices", g.RequireCapability(identity.CapViewBilling, tenant("tenantListInvoices")))
}

// RegisterSupersededProofRoutes registers the operationIds whose shims the
// money-spine modules replaced on the served mux: the five order lifecycle
// routes (orderCancel carries requireManager) and the public quote view.
// It exists so the DB-free acceptance sweeps keep a proof vehicle for the
// guard classes those operations carried (kid tokens, rotation, the
// anonymous public leg) while the served mux answers the real modules; it
// is registered only by test harnesses, never by serve, and it comes out
// entirely when the sweeps re-point at the real routes.
func RegisterSupersededProofRoutes(r httpx.Registrar, g *guards.Guards, rec *Recorder) {
	// The order lifecycle, x-role requireStaff except orderCancel
	// (requireManager), from the staff-order tag.
	r.Handle("GET /api/v1/orders/{id}", g.RequireStaff(staffRecorder(rec, "orderRead")))
	r.Handle("POST /api/v1/orders/{id}/confirm", g.RequireStaff(staffRecorder(rec, "orderConfirm")))
	r.Handle("POST /api/v1/orders/{id}/picking", g.RequireStaff(staffRecorder(rec, "orderMarkPicking")))
	r.Handle("POST /api/v1/orders/{id}/ready", g.RequireStaff(staffRecorder(rec, "orderMarkReady")))
	r.Handle("POST /api/v1/orders/{id}/cancel", g.RequireManager(staffRecorder(rec, "orderCancel")))

	// One public operation, security: [] and no x-role gate.
	r.Handle("GET /api/public/v1/quotes/{token}", g.Public(publicRecorder(rec, "quotePublicViewQuote")))
}

// writeOK answers 200 with the minimal shim body. It reads nothing and
// writes nothing: no table, no request body, no path parameters.
func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "reached"})
}
