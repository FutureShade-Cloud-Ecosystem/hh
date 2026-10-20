//go:build integration

// seqtrigger_drawbeforelock_integration_test.go: the draw before lock legs for portal_users (PR 556, round 3c).
// The team member writers (UpdateRole, UpdateStatus, plain and under the last admin guard) used to draw the
// team counter in a CTE and then lock the user row; a writer that relies on the sync seq trigger takes the row
// first. They now rely on the trigger too. The trigger relying writer (a plain touch) is parked between its row
// lock and its draw with a BEFORE INSERT trigger on scope_sequences (testkit.DrawPark); the real writer runs on
// the same user. Red at 099f2e27: the writer draws the counter, waits for the row, the park releases, the touch
// wants the counter, 40P01.
package portal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_DrawBeforeLock_TeamMemberWritersBesideAUserWrite(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)

	var branchID, custID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('DB-'||$1, 'Draw Before '||$1, 'CONTRACTOR', $2::uuid, 1) RETURNING id::text`, suffix, branchID).Scan(&custID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM portal_users WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})
	users := make([]string, 3)
	roles := []string{"account_admin", "account_admin", "buyer"}
	for i := range users {
		if err := pool.QueryRow(ctx,
			`INSERT INTO portal_users (customer_id, email, name, password_hash, role, status)
			 VALUES ($1::uuid, $2, 'User', 'x', $3, 'active') RETURNING id::text`,
			custID, fmt.Sprintf("db%d-%s@fixture.test", i, suffix), roles[i]).Scan(&users[i]); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	repo := NewRepository(pool)

	for _, c := range []struct {
		name  string
		key   int64
		user  string
		write func() error
	}{
		{"plain role change", 55010005, users[2], func() error { return repo.UpdateRole(ctx, users[2], "field_crew", false) }},
		{"guarded status change", 55010006, users[0], func() error { return repo.UpdateStatus(ctx, users[0], "inactive", true) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			park := testkit.NewDrawPark(t, pool, "team", c.key)
			touch := park.TouchRow("portal_users", "updated_at = now()", c.user)
			park.WaitParked("the user touch stands between its row lock and its draw")

			write := make(chan error, 1)
			go func() { write <- c.write() }()
			park.WaitBlocked("the team member write waits")

			park.Answers(map[string]chan error{"touch": touch, "write": write})
		})
	}
}
