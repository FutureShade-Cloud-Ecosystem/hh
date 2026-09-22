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
// The served values mirror the ERP antecedent exactly: a static dealer
// stub (portal/handler.go config over model.go defaultConfig at the
// pinned carry commit), which is also the recorded fixture capture
// (core/api/fixtures-live/config.json). A real dealer config service is a
// later mission; until it lands this handler is the route's home, and the
// tenant module carry re-homes it.
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

// Default is the dealer stub carry: the same six field values the ERP
// antecedent served from its own defaultConfig, matching the recorded
// fixture capture for this route byte for byte in meaning.
func Default() apigen.TenantPublicConfig {
	return apigen.TenantPublicConfig{
		Id:           "dibbits",
		DealerName:   "Dibbits Landscape Supply",
		LogoUrl:      "",
		PrimaryColor: "#E8A74E",
		SupportEmail: "support@dibbits.example",
		SupportPhone: "",
	}
}

// ServeHTTP answers 200 with the config as JSON.
func (h *Handler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(h.config)
}
