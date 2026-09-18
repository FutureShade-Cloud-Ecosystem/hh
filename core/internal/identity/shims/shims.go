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

// RegisterRoutes registers the shim surface on mux through the settled
// Guards signature. This is the whole proof surface: the five order
// lifecycle routes (orderCancel carries requireManager), a representative
// tenant set from /api/v1/tenant, the two dual-bearer sync operations, one
// public operation and one capability-qualified portal operation, each
// named by its contract operationId.
func RegisterRoutes(mux *http.ServeMux, g *guards.Guards, rec *Recorder) {
	staff := func(operation string) guards.StaffHandler {
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
	public := func(operation string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			rec.record(Reached{Operation: operation})
			writeOK(w)
		}
	}

	// The order lifecycle, x-role requireStaff except orderCancel
	// (requireManager), from the staff-order tag.
	mux.Handle("GET /api/v1/orders/{id}", g.RequireStaff(staff("orderRead")))
	mux.Handle("POST /api/v1/orders/{id}/confirm", g.RequireStaff(staff("orderConfirm")))
	mux.Handle("POST /api/v1/orders/{id}/picking", g.RequireStaff(staff("orderMarkPicking")))
	mux.Handle("POST /api/v1/orders/{id}/ready", g.RequireStaff(staff("orderMarkReady")))
	mux.Handle("POST /api/v1/orders/{id}/cancel", g.RequireManager(staff("orderCancel")))

	// A representative tenant set from /api/v1/tenant, x-role tenant.
	mux.Handle("GET /api/v1/tenant/me", g.RequireTenant(tenant("tenantGetMe")))
	mux.Handle("PATCH /api/v1/tenant/me", g.RequireTenant(tenant("tenantUpdateMe")))
	mux.Handle("POST /api/v1/tenant/token/refresh", g.RequireTenant(tenant("tenantRefreshToken")))

	// The two dual-bearer sync operations, x-role scope-member; the G4
	// rule is bearer-conditioned inside the guard.
	mux.Handle("GET /api/v1/sync/{scope}", g.RequireSync(sync("syncPull")))
	mux.Handle("POST /api/v1/sync/{scope}", g.RequireSync(sync("syncPush")))

	// One public operation, security: [] and no x-role gate.
	mux.Handle("GET /api/public/v1/quotes/{token}", g.Public(http.HandlerFunc(public("quotePublicViewQuote"))))

	// One capability-qualified portal operation, x-role
	// capability:view_billing.
	mux.Handle("GET /api/v1/tenant/invoices", g.RequireCapability(identity.CapViewBilling, tenant("tenantListInvoices")))
}

// writeOK answers 200 with the minimal shim body. It reads nothing and
// writes nothing: no table, no request body, no path parameters.
func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "reached"})
}
