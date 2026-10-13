// Package tenantconfig serves the public dealer config route, GET
// /api/v1/tenant/config: the G2 handler half of ADR 0021
// (docs/adr/0021-contract-conformance.md, Decision 1). The route serves
// the narrowed TenantPublicConfig, dealer fields only, with no brand
// property and no tenant-scoped data, on the public leg (ADR-0020 leg 2:
// no verification, no org resolution, and X-Org on the request is an
// undeclared header with zero effect). The contractor's own brand rides
// the authenticated carriers (TenantLoginResponse and
// TenantRefreshResponse, whose config is TenantConfig).
//
// The served shape mirrors the ERP antecedent (portal/handler.go config
// over model.go defaultConfig at the pinned carry commit, recorded in
// core/api/fixtures-live/config.json), but not its values: the antecedent
// served its first dealer's identity, and the default here carries no
// dealer's name. A real dealer config service is a later mission; until it
// lands this handler is the route's home, serving the config it is
// constructed over, and the tenant module carry re-homes it.
package tenantconfig

import (
	"encoding/json"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// Handler serves the public config route. It reads nothing: no table, no
// request body, no path or query parameter, and it stamps no scope,
// because the route is public. The response is the one fixed config the
// handler was constructed over.
type Handler struct {
	config apigen.TenantPublicConfig
}

// New returns the handler over one fixed dealer config. The config is the
// whole payload: six dealer fields, no tenant-scoped data, so an
// unauthenticated caller can learn nothing any dealer has not published
// about itself.
func New(config apigen.TenantPublicConfig) *Handler {
	return &Handler{config: config}
}

// Default is the config of no dealer: the same six fields the ERP
// antecedent served, with no dealer's name, logo, colour or support line in
// them. The antecedent's stub served its first dealer's identity; this
// route must never show one dealer's name on another's box (decision log
// row 1592), so a dealer's name and brand come from the dealer's own
// configuration through New, the demo's from the demo seed. The id is the
// neutral key "default".
func Default() apigen.TenantPublicConfig {
	return apigen.TenantPublicConfig{
		Id:           "default",
		DealerName:   "",
		LogoUrl:      "",
		PrimaryColor: "",
		SupportEmail: "",
		SupportPhone: "",
	}
}

// ServeHTTP answers 200 with the config as JSON.
func (h *Handler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(h.config)
}
