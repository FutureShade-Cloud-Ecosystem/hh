//go:build demo

package membership

import (
	"context"
	"errors"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// Composite is the demo build's membership source under the console sign in
// mode (state note section 5): the persona subjects are answered from a
// static source compiled into this build only, and every other subject by
// FB Console behind its cache.
//
// The split is by shape, before any lookup. An FB Console id holds no colon
// (the id shape the exchange enforces at gate 3), and every persona subject
// holds one, so a subject with a colon is never sent to FB Console, listed
// or not, and a subject without one is never looked up in the static map.
// Neither side can answer for the other's subjects.
type Composite struct {
	console authz.MembershipSource
}

// NewComposite wraps the FB Console source. A nil source is a construction
// error: the composite never serves on the static half alone.
func NewComposite(console authz.MembershipSource) (*Composite, error) {
	if console == nil {
		return nil, errors.New("membership: the composite requires the FB Console source")
	}
	return &Composite{console: console}, nil
}

// OrgMember answers a persona subject from the static map: a member of the
// demo org only, and only the enterable personas (the contractor personas
// wait for the Pro cycle). Any other subject with a colon is no member.
func (c *Composite) OrgMember(ctx context.Context, platformUserID, orgSlug string) (bool, error) {
	if strings.Contains(platformUserID, ":") {
		if orgSlug != flows.OrgSlug {
			return false, nil
		}
		p, ok := flows.PersonaBySubject(platformUserID)
		return ok && !p.Contractor, nil
	}
	return c.console.OrgMember(ctx, platformUserID, orgSlug)
}

// TenantOrgs answers empty for a subject with a colon: a persona holds no
// tenant team, so the gate's tenant leg fails closed on it. Every other
// subject is FB Console's.
func (c *Composite) TenantOrgs(ctx context.Context, platformUserID string) ([]string, error) {
	if strings.Contains(platformUserID, ":") {
		return nil, nil
	}
	return c.console.TenantOrgs(ctx, platformUserID)
}
