//go:build demo

// Package membership is the demo's MembershipSource: the answer to "is
// this subject a member of this org" that the demo build constructs the
// authz gate with in place of the platform client. It answers membership
// only in the demo org and only for the accounts the closed flow table
// holds; every other question answers no. It makes no network call of any
// kind, so FB Console is never reached from a demo box, and it reads none
// of the platform client's environment inputs (the three CORE_APPWRITE_*
// values): the demo serve wiring never constructs the platform client at
// all.
//
// What the source answers is coarse membership only, the same narrow fact
// the platform source answers (D28): authority still resolves from the
// database per request through the staff resolver, so a flow account can
// reach exactly what its role grant and the dealer's role configuration
// allow, and nothing more.
package membership

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
)

// Source answers membership from the flow table.
type Source struct{}

// New returns the demo membership source.
func New() *Source { return &Source{} }

// OrgMember reports whether the subject is one of the flow accounts and
// the org is the demo org. Absence is the boolean false, not an error:
// the gate treats an unknown subject and a wrong org identically
// downstream, which keeps the refusal body the one 403 the middleware
// already answers.
func (s *Source) OrgMember(_ context.Context, platformUserID, orgSlug string) (bool, error) {
	if orgSlug != flows.OrgSlug {
		return false, nil
	}
	a, ok := flows.ByPlatformUserID(platformUserID)
	// A contractor persona is a tenant principal, never a staff member:
	// the staff leg answers no for it, whatever token it carries.
	return ok && a.Class() == flows.ClassStaff, nil
}

// TenantOrgs answers the demo org for a contractor persona, the one
// tenant team its portal user holds, and empty for every other subject: a
// staff account has no tenant team, so a staff subject can never ride the
// tenant path, and the gate's tenant leg fails closed on an empty list.
func (s *Source) TenantOrgs(_ context.Context, platformUserID string) ([]string, error) {
	a, ok := flows.ByPlatformUserID(platformUserID)
	if !ok || a.Class() != flows.ClassTenant {
		return nil, nil
	}
	return []string{flows.OrgSlug}, nil
}
