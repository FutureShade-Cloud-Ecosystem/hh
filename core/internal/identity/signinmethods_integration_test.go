//go:build integration

package identity

import (
	"context"
	"reflect"
	"testing"
)

func TestSignInMethodStoreAgainstTheTable(t *testing.T) {
	pool := claimTestPool(t)
	ctx := context.Background()
	store, err := NewSignInMethodStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	var orgID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO org (slug, name) VALUES ('signin-methods-' || substr(gen_random_uuid()::text, 1, 8), 'sign in methods test') RETURNING id::text`).Scan(&orgID); err != nil {
		t.Fatalf("make the test org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM org_signin_method WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM org WHERE id = $1`, orgID)
	})

	got, err := store.AllowedSignInMethods(ctx, orgID)
	if err != nil || !reflect.DeepEqual(got, DefaultSignInMethods()) {
		t.Fatalf("no row: %v, %v; want the default set", got, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO org_signin_method (org_id, code) VALUES ($1, 'magic_link'), ($1, 'google')`, orgID); err != nil {
		t.Fatal(err)
	}
	got, err = store.AllowedSignInMethods(ctx, orgID)
	if err != nil || !reflect.DeepEqual(got, []string{"google", "magic_link"}) {
		t.Fatalf("two rows: %v, %v", got, err)
	}
	// An unknown code in the table is a data defect: refused, never skipped.
	if _, err := pool.Exec(ctx, `INSERT INTO org_signin_method (org_id, code) VALUES ($1, 'sms')`, orgID); err != nil {
		t.Fatal(err)
	}
	got, err = store.AllowedSignInMethods(ctx, orgID)
	if err == nil || got != nil {
		t.Fatalf("unknown code: %v, %v; want a refusal", got, err)
	}
	// A malformed org id is an error, not the default.
	if got, err := store.AllowedSignInMethods(ctx, "not-a-uuid"); err == nil || got != nil {
		t.Fatalf("bad org id: %v, %v", got, err)
	}
}
