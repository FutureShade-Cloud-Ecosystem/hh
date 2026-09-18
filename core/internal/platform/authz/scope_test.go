package authz

import (
	"context"
	"testing"
)

// The accessor is the fail-closed boundary: scope is readable only when
// the matching middleware leg stamped it, the wrong-kind accessor reports
// nothing, and a stamp of one kind replaces any stamp of the other.
func TestScopeAccessors(t *testing.T) {
	ctx := context.Background()

	if _, ok := StaffScopeFrom(ctx); ok {
		t.Fatalf("staff scope readable from a bare context")
	}
	if _, ok := TenantScopeFrom(ctx); ok {
		t.Fatalf("tenant scope readable from a bare context")
	}

	staff := StaffScope{PlatformUserID: "u1", OrgSlug: "alpha", OrgID: "id-1"}
	ctx = WithStaffScope(ctx, staff)
	if got, ok := StaffScopeFrom(ctx); !ok || got != staff {
		t.Fatalf("staff scope not readable: %+v ok=%v", got, ok)
	}
	if _, ok := TenantScopeFrom(ctx); ok {
		t.Fatalf("tenant scope readable on a staff request")
	}

	tenant := TenantScope{PlatformUserID: "u1", OrgSlug: "alpha", OrgID: "id-1", TenantID: "t1"}
	ctx = WithTenantScope(ctx, tenant)
	if got, ok := TenantScopeFrom(ctx); !ok || got != tenant {
		t.Fatalf("tenant scope not readable: %+v ok=%v", got, ok)
	}
	if _, ok := StaffScopeFrom(ctx); ok {
		t.Fatalf("staff scope survived a tenant stamp")
	}
}
