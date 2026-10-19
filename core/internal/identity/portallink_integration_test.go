//go:build integration

package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestLinkedPortalUserReadsOnlyALiveStampedRowByPlatformUserID(t *testing.T) {
	pool := claimTestPool(t)
	ctx := context.Background()
	r, err := NewPortalRoleResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	var customer string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY account_no LIMIT 1`).Scan(&customer); err != nil {
		t.Fatalf("no customer to hang a portal row on: %v", err)
	}
	mk := func(status string, stamp, deleted bool) (pid, rowID string) {
		pid = "linkportal-" + uuid.NewString()[:8]
		if err := pool.QueryRow(ctx, `
			INSERT INTO portal_users (customer_id, email, name, password_hash, role, status, platform_user_id, tenant_id, deleted_at)
			VALUES ($1::uuid, $2, 'Fixture', 'fx-not-a-hash', 'buyer', $3, $4,
			        CASE WHEN $5 THEN $1::uuid END, CASE WHEN $6 THEN now() END)
			RETURNING id::text`, customer, pid+"@fixture.invalid", status, pid, stamp, deleted).Scan(&rowID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM portal_users WHERE id = $1::uuid`, rowID) })
		return pid, rowID
	}

	pid, rowID := mk("active", true, false)
	got, ok, err := r.LinkedPortalUser(ctx, pid)
	if err != nil || !ok {
		t.Fatalf("active: %+v %v %v", got, ok, err)
	}
	if got.PortalUserID != rowID || got.TenantID != customer || got.Role != PortalRoleBuyer || got.Email != pid+"@fixture.invalid" {
		t.Fatalf("link = %+v", got)
	}

	for _, status := range []string{"inactive", "pending"} {
		pid, _ := mk(status, true, false)
		if _, ok, err := r.LinkedPortalUser(ctx, pid); ok || !errors.Is(err, ErrPortalUserInactive) {
			t.Errorf("%s: ok=%v err=%v, want ErrPortalUserInactive", status, ok, err)
		}
	}
	// An unstamped tenant_id does not resolve (fail closed, as PortalRole),
	// a tombstoned row is no row, and an unknown or empty id is no row or an error.
	pidUnstamped, _ := mk("active", false, false)
	if _, ok, err := r.LinkedPortalUser(ctx, pidUnstamped); ok || err != nil {
		t.Errorf("unstamped: ok=%v err=%v", ok, err)
	}
	pidDel, _ := mk("active", true, true)
	if _, ok, err := r.LinkedPortalUser(ctx, pidDel); ok || err != nil {
		t.Errorf("tombstoned: ok=%v err=%v", ok, err)
	}
	if _, ok, err := r.LinkedPortalUser(ctx, "no-such-platform-user"); ok || err != nil {
		t.Errorf("unlinked: ok=%v err=%v", ok, err)
	}
	if _, _, err := r.LinkedPortalUser(ctx, ""); err == nil {
		t.Error("empty id accepted")
	}
}
