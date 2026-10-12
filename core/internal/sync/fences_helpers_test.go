package sync

import (
	"context"
	"sync/atomic"
)

// roleDouble is the staff roles seam double: it answers a settable role
// list (or a fault) and counts its reads.
type roleDouble struct {
	roles []string
	err   error
	calls atomic.Int64
}

func (d *roleDouble) ResolveRoles(context.Context, string, string) ([]string, error) {
	d.calls.Add(1)
	if d.err != nil {
		return nil, d.err
	}
	return append([]string(nil), d.roles...), nil
}

// portalDouble is the portal roles seam double.
type portalDouble struct {
	role string
	has  bool
	err  error
}

func (d *portalDouble) PortalRole(context.Context, string, string) (string, bool, error) {
	return d.role, d.has, d.err
}

// allWriters is the double pair the older route tests use: a staff caller
// holding every role the registry names as a writer and a tenant caller
// holding the contractor administrator role, so those tests keep exercising
// the surfaces they were written for and the fence has its own tests.
func allWriters() (*roleDouble, *portalDouble) {
	seen := map[string]bool{}
	var roles []string
	for _, info := range Scopes() {
		for _, r := range info.Writers {
			if !seen[r] {
				seen[r] = true
				roles = append(roles, r)
			}
		}
	}
	return &roleDouble{roles: roles}, &portalDouble{role: roleAccountAdmin, has: true}
}
