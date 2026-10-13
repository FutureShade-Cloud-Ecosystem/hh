//go:build demo

package demo

import "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"

// DealerConfig is the demo dealer's public config: Gable Landscape Supply,
// the invented two yard dealer of GLS-TENANT-r3, in the brand kit's navy.
// The demo wiring hands it to the tenant config handler's New, so the
// public route names the demo's dealer and the shared default names none
// (decision log row 1592). The values are invented: the support address
// sits at the reserved demo domain, which no mail can reach, and the number
// in the 555 0100 block; the logo is left to the apps, which carry the
// kit's lockups. Gold is the action fill and never a chrome colour, so the
// primary colour is the navy.
func DealerConfig() apigen.TenantPublicConfig {
	return apigen.TenantPublicConfig{
		Id:           "gable",
		DealerName:   "Gable Landscape Supply",
		LogoUrl:      "",
		PrimaryColor: "#0B2338",
		SupportEmail: "support@" + demoDomain,
		SupportPhone: "(619) 555-0142",
	}
}
