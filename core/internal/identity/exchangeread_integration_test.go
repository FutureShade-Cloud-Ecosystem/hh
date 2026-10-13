//go:build integration

package identity

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestLinkedActiveUserAndRoleLabel(t *testing.T) {
	pool := claimTestPool(t)
	ctx := context.Background()
	r, err := NewStaffAuthorityResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(status string, deleted bool) (string, string) {
		pid := "linkfix-" + uuid.NewString()[:8]
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (email, display_name, status, platform_user_id, deleted_at)
			 VALUES ($1, 'Fixture', $2, $3, CASE WHEN $4 THEN now() END) RETURNING id::text`,
			pid+"@fixture.invalid", status, pid, deleted).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, id) })
		return pid, id
	}
	pidActive, idActive := mk("ACTIVE", false)
	got, ok, err := r.LinkedActiveUser(ctx, pidActive)
	if err != nil || !ok || got != idActive {
		t.Fatalf("active: %q %v %v", got, ok, err)
	}
	for _, status := range []string{"INVITED", "SUSPENDED", "DEACTIVATED"} {
		pid, _ := mk(status, false)
		if id, ok, err := r.LinkedActiveUser(ctx, pid); err != nil || ok || id != "" {
			t.Errorf("%s: %q %v %v", status, id, ok, err)
		}
	}
	pidDel, _ := mk("ACTIVE", true)
	if _, ok, err := r.LinkedActiveUser(ctx, pidDel); err != nil || ok {
		t.Errorf("tombstoned: %v %v", ok, err)
	}
	if _, ok, err := r.LinkedActiveUser(ctx, "no-such-platform-user"); err != nil || ok {
		t.Errorf("unlinked: %v %v", ok, err)
	}
	if _, _, err := r.LinkedActiveUser(ctx, ""); err == nil {
		t.Error("empty id accepted")
	}

	label, ok, err := r.StaffRoleLabel(ctx, "driver")
	if err != nil || !ok || label != "Driver" {
		t.Fatalf("label: %q %v %v", label, ok, err)
	}
	if _, ok, err := r.StaffRoleLabel(ctx, "no_such_role"); err != nil || ok {
		t.Fatalf("unknown role: %v %v", ok, err)
	}
}
