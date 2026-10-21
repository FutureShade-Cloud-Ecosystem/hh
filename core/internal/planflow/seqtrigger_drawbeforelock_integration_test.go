//go:build integration

// seqtrigger_drawbeforelock_integration_test.go: the draw before lock leg for plan and plan_line (PR 556, round 3c). The
// sync tombstone used to allocate the scope_items and orders counter and then lock the row; a writer that relies on the sync seq
// trigger takes the row first. The tombstone now relies on the trigger too. The trigger relying writer (a plain
// touch) is parked between its row lock and its draw with a BEFORE INSERT trigger on scope_sequences
// (testkit.DrawPark); the real Delete runs on the same row. Red at 099f2e27: the Delete draws the counter, waits
// for the row, the park releases, the touch wants the counter, 40P01. The Delete still answers the seq the
// trigger drew (RowState.Seq above zero).
package planflow

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	dblOrgID    = "a0e41300-0000-4000-8000-000000000001"
	dblTenantID = "a0e41300-0000-4000-8000-000000000002"
	dblPlanID   = "a0e41300-0000-4000-8000-000000000003"
	dblLineID   = "a0e41300-0000-4000-8000-000000000004"
)

func TestIntegration_DrawBeforeLock_PlanTombstonesBesideAWrite(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("planflow draw before lock leg: DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	for _, q := range []string{
		`INSERT INTO org (id, slug, name) VALUES ('` + dblOrgID + `', 'planflow-draw-before-lock', 'draw before lock')`,
		`INSERT INTO customers (id, account_no, name, org_id, seq) VALUES ('` + dblTenantID + `', 'PFDBL-1', 'draw before lock tenant', '` + dblOrgID + `', 1)`,
		`INSERT INTO plan (id, org_id, tenant_id, name, stage, seq) VALUES ('` + dblPlanID + `', '` + dblOrgID + `', '` + dblTenantID + `', 'card', 'plan', 1)`,
		`INSERT INTO plan_line (id, plan_id, org_id, tenant_id, description, qty_entered, seq) VALUES ('` + dblLineID + `', '` + dblPlanID + `', '` + dblOrgID + `', '` + dblTenantID + `', 'line', 1, 1)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM plan_line WHERE org_id = $1`, dblOrgID)
		_, _ = pool.Exec(c, `DELETE FROM plan WHERE org_id = $1`, dblOrgID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, dblTenantID)
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id = $1`, dblOrgID)
	})
	filter := hhsync.ScopeFilter{OrgID: dblOrgID, Tenant: true, TenantID: dblTenantID, Actor: "draw-before-lock-leg"}

	for _, c := range []struct {
		name, table, scope, id string
		key                    int64
		store                  hhsync.Store
	}{
		{"plan", "plan", "orders", dblPlanID, 55010009, NewOrdersSyncStore(pool)},
		{"plan_line", "plan_line", "scope_items", dblLineID, 55010010, NewItemsSyncStore(pool)},
	} {
		t.Run(c.name, func(t *testing.T) {
			engine := hhsync.NewEngine()
			if err := engine.RegisterScope(c.scope, c.store); err != nil {
				t.Fatalf("register: %v", err)
			}
			info, _ := hhsync.Lookup(c.scope)
			before, err := engine.Pull(ctx, info, filter, 0, 100)
			if err != nil {
				t.Fatalf("pull: %v", err)
			}
			park := testkit.NewDrawPark(t, pool, c.scope, c.key)
			touch := park.TouchRow(c.table, "updated_at = now()", c.id)
			park.WaitParked("the touch stands between its row lock and its draw")

			del := make(chan error, 1)
			go func() {
				_, err := c.store.Delete(ctx, filter, c.id, "")
				del <- err
			}()
			park.WaitBlocked("the tombstone waits")
			park.Answers(map[string]chan error{"touch": touch, "tombstone": del})

			pulled, err := engine.Pull(ctx, info, filter, before.LastSeq, 100)
			if err != nil {
				t.Fatalf("pull after: %v", err)
			}
			for _, tb := range pulled.Tombstones {
				if tb.Id == c.id && tb.Seq > before.LastSeq {
					return
				}
			}
			t.Fatalf("the device did not receive the tombstone from cursor %d: %+v", before.LastSeq, pulled.Tombstones)
		})
	}
}
