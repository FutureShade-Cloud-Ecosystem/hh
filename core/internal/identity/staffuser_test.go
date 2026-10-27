//go:build integration

package identity

// The named testers' staff rows against a real migrated and seeded database
// (W2-2): the email ownership refusal, the tombstone restore, and the
// revoke that takes every live grant the platform user holds in the org.
// One platform subject per leg, so a leg's failure names its own rule and
// no leg leans on another.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// liveStaffGrants counts the live grants one platform user holds in one org.
func liveStaffGrants(t *testing.T, ctx context.Context, db DBTX, orgID, platformUserID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(ctx, `
		SELECT count(*) FROM staff_role_grant
		WHERE org_id = $1::uuid AND platform_user_id = $2 AND deleted_at IS NULL`,
		orgID, platformUserID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// liveStaffGrantRoles reads the roles of the live grants one platform user
// holds in one org.
func liveStaffGrantRoles(t *testing.T, ctx context.Context, db DBTX, orgID, platformUserID string) []string {
	t.Helper()
	rows, err := db.Query(ctx, `
		SELECT role_id FROM staff_role_grant
		WHERE org_id = $1::uuid AND platform_user_id = $2 AND deleted_at IS NULL
		ORDER BY role_id`,
		orgID, platformUserID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			t.Fatal(err)
		}
		out = append(out, role)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIntegration_StaffUser(t *testing.T) {
	ctx := context.Background()
	pool := claimTestPool(t)

	var orgID string
	slug := "staffuser-" + uuid.NewString()[:8]
	if err := pool.QueryRow(ctx,
		`INSERT INTO org (slug, name) VALUES ($1, 'Staff User Test') RETURNING id::text`, slug).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM staff_role_grant WHERE org_id = $1::uuid`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE org_id = $1::uuid`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM org WHERE id = $1::uuid`, orgID)
	})

	t.Run("the email ownership refusal", func(t *testing.T) {
		// A row with no platform link holds its email.
		unlinked := "staffuser-unlinked-" + uuid.NewString()[:8] + "@example.invalid"
		var unlinkedID string
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (email, display_name, status) VALUES ($1, 'No Platform', 'ACTIVE') RETURNING id::text`,
			unlinked).Scan(&unlinkedID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, unlinkedID) })
		if _, _, err := EnsureStaffUser(ctx, pool, orgID, "staffuser-nolink-"+uuid.NewString()[:8], unlinked, "Someone"); !errors.Is(err, ErrStaffEmailTaken) {
			t.Fatalf("an email held by a row with no platform link must refuse, got %v", err)
		}
		// The owner holds its own email, and no other platform subject may take it.
		email := "staffuser-owner-" + uuid.NewString()[:8] + "@example.invalid"
		owner := "staffuser-owner-" + uuid.NewString()[:8]
		if _, _, err := EnsureStaffUser(ctx, pool, orgID, owner, email, "Owner"); err != nil {
			t.Fatalf("the owner: %v", err)
		}
		if _, _, err := EnsureStaffUser(ctx, pool, orgID, "staffuser-other-"+uuid.NewString()[:8], email, "Other"); !errors.Is(err, ErrStaffEmailTaken) {
			t.Fatalf("an email held by another platform id must refuse, got %v", err)
		}
	})

	t.Run("the tombstone restore", func(t *testing.T) {
		platformID := "staffuser-tombstone-" + uuid.NewString()[:8]
		email := platformID + "@example.invalid"
		id, _, err := EnsureStaffUser(ctx, pool, orgID, platformID, email, "Tombstone")
		if err != nil {
			t.Fatal(err)
		}
		// The row is tombstoned and deactivated, as a removal would leave it.
		if _, err := pool.Exec(ctx,
			`UPDATE users SET deleted_at = now(), status = 'DEACTIVATED' WHERE id = $1::uuid`, id); err != nil {
			t.Fatal(err)
		}
		// The re-provision finds the row by platform id and restores it.
		again, _, err := EnsureStaffUser(ctx, pool, orgID, platformID, email, "Tombstone")
		if err != nil {
			t.Fatalf("the restore: %v", err)
		}
		if again != id {
			t.Fatalf("the restore wrote a new row %s, want the same row %s", again, id)
		}
		var status string
		var deleted bool
		if err := pool.QueryRow(ctx,
			`SELECT status, deleted_at IS NOT NULL FROM users WHERE id = $1::uuid`, id).Scan(&status, &deleted); err != nil {
			t.Fatal(err)
		}
		if status != "ACTIVE" || deleted {
			t.Fatalf("the restored row: status %q, deleted %v, want ACTIVE and not deleted", status, deleted)
		}
	})

	t.Run("the revoke of every grant", func(t *testing.T) {
		platformID := "staffuser-multi-" + uuid.NewString()[:8]
		email := platformID + "@example.invalid"
		if _, _, err := EnsureStaffUser(ctx, pool, orgID, platformID, email, "Multi"); err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"financial_controller", "payables_receivables"} {
			if _, err := EnsureStaffGrant(ctx, pool, orgID, platformID, role, nil); err != nil {
				t.Fatalf("the %s grant: %v", role, err)
			}
		}
		if n := liveStaffGrants(t, ctx, pool, orgID, platformID); n != 2 {
			t.Fatalf("the person holds %d live grants, want 2", n)
		}
		// The revoke takes every live grant the person holds in the org, not
		// only the one role the caller names.
		grantIDs, revoked, err := RevokeStaffGrants(ctx, pool, orgID, platformID)
		if err != nil {
			t.Fatalf("the revoke: %v", err)
		}
		if !revoked {
			t.Fatal("the revoke must report the grants it took")
		}
		if len(grantIDs) != 2 {
			t.Fatalf("the revoke took %d grants, want 2", len(grantIDs))
		}
		if n := liveStaffGrants(t, ctx, pool, orgID, platformID); n != 0 {
			t.Fatalf("the person still holds %d live grants after the revoke, want 0", n)
		}
		// The rows are tombstoned, not removed: the history keeps them.
		var tombstones int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM staff_role_grant
			WHERE org_id = $1::uuid AND platform_user_id = $2 AND deleted_at IS NOT NULL`,
			orgID, platformID).Scan(&tombstones); err != nil {
			t.Fatal(err)
		}
		if tombstones != 2 {
			t.Fatalf("the revoke left %d tombstoned grants, want 2", tombstones)
		}
		// A later provision brings the person back with the role named then,
		// and with that role only.
		if _, err := EnsureStaffGrant(ctx, pool, orgID, platformID, "financial_controller", nil); err != nil {
			t.Fatalf("the re-provision: %v", err)
		}
		roles := liveStaffGrantRoles(t, ctx, pool, orgID, platformID)
		if len(roles) != 1 || roles[0] != "financial_controller" {
			t.Fatalf("after the re-provision the person holds %v, want financial_controller only", roles)
		}
	})
}
