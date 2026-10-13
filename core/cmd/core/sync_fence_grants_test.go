package main

// sync_fence_grants_test.go: the sync write fence over the real role reader.
// A person holding two grants (a writer role and a role that is not one) is
// admitted on pick_tasks through the composed mux, and a principal holding
// only the non writer role is refused with the shared 403. The unit sweep
// proves the same against a double; this leg drives
// identity.StaffAuthorityResolver.ResolveRoles over staff_role_grant.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const (
	fenceSubTwoGrants  = "fxstaff-twogrant-fence"
	fenceUserTwoGrants = "3ef10000-0000-4000-8000-000000001431"
	fenceGrantYard     = "3ef10000-0000-4000-8000-000000001441"
	fenceGrantHR       = "3ef10000-0000-4000-8000-000000001442"
)

func TestSyncWriteFenceAdmitsAPersonHoldingTwoGrants(t *testing.T) {
	q := newQD1BFixture(t)
	pool := q.pool
	qdsrPlantRole(t, q.fx.env, pool, fenceSubTwoGrants, fenceUserTwoGrants, fenceGrantYard, "yard_team", true)
	qdsrPlantRole(t, q.fx.env, pool, fenceSubTwoGrants, fenceUserTwoGrants, fenceGrantHR, "hr", false)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM staff_role_grant WHERE platform_user_id = '` + fenceSubTwoGrants + `'`,
			`DELETE FROM user_branch_grants WHERE user_id = '` + fenceUserTwoGrants + `'`,
			`DELETE FROM users WHERE id = '` + fenceUserTwoGrants + `'`,
		} {
			_, _ = pool.Exec(ctx, stmt)
		}
	})

	push := func(sub string) (int, string) {
		return qd1bCall(t, q, http.MethodPost, "/api/v1/sync/pick_tasks", sub, map[string]any{"ops": []any{
			map[string]any{"client_id": uuid.NewString(), "op": "update",
				"payload": map[string]any{"id": uuid.NewString()}}}})
	}

	// Both grants: yard_team is a writer, so the push reaches the engine
	// (a 200 with a per operation result, the store rejecting the unknown row).
	code, body := push(fenceSubTwoGrants)
	if code != http.StatusOK || !strings.Contains(body, `"results"`) {
		t.Fatalf("two grants, one a writer: %d %s, want 200 with results", code, body)
	}
	// The non writer role alone: the shared 403, byte for byte.
	code, body = push(xrSubHR)
	if code != http.StatusForbidden || body != `{"error":"Forbidden"}` {
		t.Fatalf("hr alone: %d %s, want the shared 403", code, body)
	}
	// The writer role alone is admitted too (the control for the pair).
	code, body = push(xrSubYardTeam)
	if code != http.StatusOK {
		t.Fatalf("yard_team alone: %d %s, want 200", code, body)
	}
}
