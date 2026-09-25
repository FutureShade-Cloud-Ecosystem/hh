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
// replaced: the sliding-refresh tenant operation (the D38 mint leave-behind:
// no real module registers it, and the shim is the reached-recorder proof
// vehicle for its guard class, never a minting surface). The two
// dual-bearer sync operations were this registrar's until the sync core
// module (HH-3.5 #165) registered the real surface; their proof shims
// moved to RegisterSupersededProofRoutes in the same change. The representative tenant set
// (tenantGetMe, tenantUpdateMe) and the capability-qualified invoices read
// (tenantListInvoices) were this registrar's until the Phase C integration
// re-homed the portal module onto their documented paths: the real module
// registers them now, so their shims moved to the DB-free proof registrar
// below in the same change (Security S-3's per-operationId removal, with
// the ServeMux duplicate-registration panic as the backstop). The order
// lifecycle five and the public quote view were this surface's first half
// and came out when the money-spine batch (HH-3.2 B4) registered the real
// order and quote routes on the serve mux.
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

// tenantRecorder is the reached-recorder factory for the tenant guard
// classes; a package-level factory since the superseded split moved the
// tenant-class shims onto the proof registrar beside this one.
func tenantRecorder(rec *Recorder, operation string) guards.TenantHandler {
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

// Registrar is the compatibility alias the Phase C B3 ruling keeps
// at the old home after the interface promoted to httpx.
type Registrar = httpx.Registrar

// syncRecorder is the reached-recorder factory for the two dual-bearer
// sync operations; a package-level factory since the sync pair moved to
// the proof registrar below (the sync core module owns the served pair
// now, HH-3.5 #165).
func syncRecorder(rec *Recorder, operation string) guards.SyncHandler {
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

func RegisterRoutes(r httpx.Registrar, g *guards.Guards, rec *Recorder) {
	// The sliding refresh, x-role tenant. A MINT leg the portal module
	// deliberately does not register (the D38 sign-in-methods leave-behind:
	// no portal mint surface exists in core until HH-4.2 lands one); this
	// shim is the reached-recorder proof vehicle for the plain tenant guard
	// class, answers with no credential of its own and mints nothing, and
	// comes out whole when HH-4.2 registers the real surface.
	r.Handle("POST /api/v1/tenant/token/refresh", g.RequireTenant(tenantRecorder(rec, "tenantRefreshToken")))

}

// RegisterSupersededProofRoutes registers the operationIds whose shims the
// real modules replaced on the served mux: the five order lifecycle routes
// (orderCancel carries requireManager) and the public quote view from the
// money-spine batch, plus the portal re-homing's three (the tenant me pair
// under RequireTenant and the capability-qualified invoices read), whose
// documented paths the portal module now registers. tenantUpdateMe is PUT,
// not PATCH: the document declares put (core/api/fragments/tenant.yaml, the
// tenantUpdateMe operation), both ERP baselines register the route as PUT
// (backend/internal/portal/settings.go at the pinned commit, and the
// vendored list at core/api/erp-routes.txt line 655), so the earlier PATCH
// spelling was a transcription slip in this shim, fixed to match the
// document (B-parity, Tech Lead F-1: the document is authoritative). It
// exists so the DB-free acceptance sweeps keep a proof vehicle for the
// guard classes those operations carried (kid tokens, rotation, the
// anonymous public leg, the plain tenant class, the capability class)
// while the served mux answers the real modules; it is registered only by
// test harnesses, never by serve, and it comes out entirely when the
// sweeps re-point at the real routes.
func RegisterSupersededProofRoutes(r httpx.Registrar, g *guards.Guards, rec *Recorder) {
	// The order lifecycle, x-role requireStaff except orderCancel
	// (requireManager), from the staff-order tag.
	r.Handle("GET /api/v1/orders/{id}", g.RequireStaff(staffRecorder(rec, "orderRead")))
	r.Handle("POST /api/v1/orders/{id}/confirm", g.RequireStaff(staffRecorder(rec, "orderConfirm")))
	r.Handle("POST /api/v1/orders/{id}/picking", g.RequireStaff(staffRecorder(rec, "orderMarkPicking")))
	r.Handle("POST /api/v1/orders/{id}/ready", g.RequireStaff(staffRecorder(rec, "orderMarkReady")))
	r.Handle("POST /api/v1/orders/{id}/cancel", g.RequireManager(staffRecorder(rec, "orderCancel")))

	// One public operation, security: [] and no x-role gate.
	r.Handle("GET /api/public/v1/quotes/{token}", publicRecorder(rec, "quotePublicViewQuote"))

	// The portal re-homing's three (Phase C integration): the tenant me
	// pair, x-role tenant, and one capability-qualified operation, x-role
	// capability:view_billing.
	r.Handle("GET /api/v1/tenant/me", g.RequireTenant(tenantRecorder(rec, "tenantGetMe")))
	r.Handle("PUT /api/v1/tenant/me", g.RequireTenant(tenantRecorder(rec, "tenantUpdateMe")))
	r.Handle("GET /api/v1/tenant/invoices", g.RequireCapability(identity.CapViewBilling, tenantRecorder(rec, "tenantListInvoices")))

	// The two dual-bearer sync operations, x-role scope-member; the G4
	// rule is bearer-conditioned inside the guard. They were the served
	// registrar's until the sync core module (HH-3.5 #165) registered the
	// real surface: the served mux answers the engine now, and this proof
	// pair keeps the DB-free G4 sweeps their reached-recorder vehicle.
	r.Handle("GET /api/v1/sync/{scope}", g.RequireSync(syncRecorder(rec, "syncPull")))
	r.Handle("POST /api/v1/sync/{scope}", g.RequireSync(syncRecorder(rec, "syncPush")))
}

// writeOK answers 200 with the minimal shim body. It reads nothing and
// writes nothing: no table, no request body, no path parameters.
func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "reached"})
}
