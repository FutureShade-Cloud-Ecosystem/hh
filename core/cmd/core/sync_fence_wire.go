package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// errSyncFenceNoPool is what the sync write fence's seams answer when the
// composition has no database (the wiring-shape tests): a fault, which the
// handler answers with the 503 family, never an admission.
var errSyncFenceNoPool = errors.New("sync: write fence has no role source (no database)")

// refusingSyncRoles is the fence's fail-closed seam pair for a nil pool.
type refusingSyncRoles struct{}

func (refusingSyncRoles) ResolveRoles(context.Context, string, string) ([]string, error) {
	return nil, errSyncFenceNoPool
}

func (refusingSyncRoles) PortalRole(context.Context, string, string) (string, bool, error) {
	return "", false, errSyncFenceNoPool
}

// syncFenceSeams builds the two seams the sync handler's write fence reads:
// the staff caller's every role (identity's StaffAuthorityResolver.
// ResolveRoles, the same reader the role gate walks) and the tenant
// caller's portal role (identity's PortalRoleResolver, the guards' own
// source). Both read per request with no cache. A nil pool wires the
// refusing pair; a constructor fault stops the boot.
func syncFenceSeams(pool *pgxpool.Pool) (sync.StaffRoles, sync.PortalRoles) {
	if pool == nil {
		return refusingSyncRoles{}, refusingSyncRoles{}
	}
	staff, err := erpidentity.NewStaffAuthorityResolver(pool)
	if err != nil {
		panic("sync: write fence staff roles refused to boot: " + err.Error())
	}
	portalRoles, err := erpidentity.NewPortalRoleResolver(pool)
	if err != nil {
		panic("sync: write fence portal roles refused to boot: " + err.Error())
	}
	return staff, portalRoles
}
