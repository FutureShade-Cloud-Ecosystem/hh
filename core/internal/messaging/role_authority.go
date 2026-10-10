// role_authority.go is the membership wall's authority half: which roles
// may manage a channel's members beside the room's owner. The role matrix
// is the source (core/internal/roleconfig/default.json, the thirteen cells
// on house-messages: twelve roles at work, the general manager at admin),
// and the service is built by the operations wiring, which the role
// configuration does not pass, so the composition root PUBLISHES the
// configuration it built the role gate with (PublishRoleConfig, beside the
// identity package's PublishFloorApprovers and the quote package's
// PublishRoleConfig pattern) and this half reads it. The held
// configuration is atomic, and nothing published means no admin for
// anyone: the wall fails closed to owner-only, it never falls back to a
// permissive default.
package messaging

import (
	"sync/atomic"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// SurfaceHouseMessages is the role matrix surface the membership wall
// reads. The route gate holds the same cell for every house messages
// route; the membership wall asks it for the ADMIN level alone (who may
// manage a room's members), which default.json gives the general manager
// alone.
const SurfaceHouseMessages = "house-messages"

// publishedRoles is the role configuration the membership wall decides
// with.
var publishedRoles atomic.Pointer[roleconfig.Config]

// PublishRoleConfig publishes the dealer's role configuration for the
// membership wall: the configuration the composition root gave the role
// gate. A nil configuration unpublishes, and an unpublished wall knows no
// admin (owner-only).
func PublishRoleConfig(c *roleconfig.Config) { publishedRoles.Store(c) }

// houseMessagesAdmin reports whether the caller's role is one the matrix
// gives Admin on house-messages. The caller's RoleCode is the ERP code;
// it maps through the identity bridge onto the configuration's canonical
// vocabulary, exactly as the quote desk's level resolver does. An
// unpublished configuration, an empty role, an unmapped code and an
// unknown cell all answer false: the wall fails closed.
func houseMessagesAdmin(c Caller) bool {
	cfg := publishedRoles.Load()
	if cfg == nil || c.RoleCode == "" {
		return false
	}
	canonical, ok := identity.CanonicalForERPCode(c.RoleCode)
	if !ok {
		return false
	}
	holds, err := cfg.Holds(canonical, SurfaceHouseMessages, roleconfig.LevelAdmin)
	return err == nil && holds
}
