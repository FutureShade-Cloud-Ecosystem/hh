//go:build integration

// dib494_activity_integration_test.go, order.activity against the REAL order service, the REAL
// audit service and a REAL migrated Postgres, driven through assist.Registry.Run as a seeded
// caller.
//
//	DATABASE_URL=... go test -tags integration -count=1 ./internal/assist/adapters/
//
// WHAT ONLY A DATABASE CAN PROVE HERE
// -----------------------------------
//   - the trail the assistant reads is the audit_log's OWN rows, causally ordered by the GENERATED
//     seq column, the unit fakes hand seq values in; only Postgres proves the identity column and
//     the ListByEntity read agree with the projection;
//   - the fence is the same SQL fence the order screens use: a Trenton-scoped caller asking for a
//     Kingston order's activity, by NUMBER or by pasted UUID, gets the branch-blind not-found
//     sentence and the trail is never surfaced;
//   - the row FILTER holds against real jsonb: the coarse HTTP middleware row and a field-less
//     ORDER_CHANGED row sit in the same audit_log and never surface;
//   - the actor's display name resolves through the real users LEFT JOIN.
//
// Fixtures are created under the DIB494 prefix and removed in t.Cleanup, mirroring the DIB431B
// harness in integration_test.go.

package adapters

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/jackc/pgx/v5/pgxpool"
)

type activityWorld struct {
	pool                  *pgxpool.Pool
	reg                   *assist.Registry
	trentonID, kingstonID string

	trentonOrderNo, kingstonOrderNo string
	kingstonOrderID                 string
	actorName                       string
}

func newActivityWorld(t *testing.T) *activityWorld {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	tag := strings.ToUpper(fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000))

	w := &activityWorld{pool: pool}
	mustQuery(t, pool, `SELECT id::text FROM locations WHERE code = 'TRT' AND loc_type = 'BRANCH'`, &w.trentonID)
	mustQuery(t, pool, `SELECT id::text FROM locations WHERE code = 'KGN' AND loc_type = 'BRANCH'`, &w.kingstonID)

	trentonCust := insertCustomer(t, pool, "DIB494-T-"+tag, "DIB-494 Trenton Contractor "+tag, w.trentonID)
	kingstonCust := insertCustomer(t, pool, "DIB494-K-"+tag, "DIB-494 Kingston Contractor "+tag, w.kingstonID)

	w.trentonOrderNo = "#SO-DIB494-T-" + tag
	w.kingstonOrderNo = "#SO-DIB494-K-" + tag
	trentonOrderID := insertOrder(t, pool, w.trentonOrderNo, trentonCust, w.trentonID, "PICKING", 512300)
	w.kingstonOrderID = insertOrder(t, pool, w.kingstonOrderNo, kingstonCust, w.kingstonID, "CONFIRMED", 99900)

	// A real staff row, so the actor name resolves through the real LEFT JOIN.
	w.actorName = "DIB-494 Fixture Staffer " + tag
	var actorID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, status)
		VALUES ($1::citext, $2, 'ACTIVE') RETURNING id::text`,
		"dib494-"+strings.ToLower(tag)+"@fixture.test", w.actorName).Scan(&actorID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// The trail, written the way the real writers write it (same actions, same changes keys), in
	// insertion order = causal order, so the GENERATED seq carries the ordering claim.
	insertAudit := func(orderID, action string, changes string, actor string) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO audit_log (action, entity_type, entity_id, actor_id, branch_id, changes)
			VALUES ($1, 'order', $2::uuid, NULLIF($3,'')::uuid, NULL, $4::jsonb)`,
			action, orderID, actor, changes); err != nil {
			t.Fatalf("insert audit %s: %v", action, err)
		}
	}
	insertAudit(trentonOrderID, audit.ActOrderStatusChanged, `{"old_status":"","new_status":"CONFIRMED"}`, "")
	insertAudit(trentonOrderID, audit.ActOrderChanged, `{"field":"unit_price_cents","line_no":2,"before":"125000","after":"118000"}`, actorID)
	// The rows that must NOT surface: a coarse middleware record and a pre-DIB-175 field-less one.
	insertAudit(trentonOrderID, audit.ActHTTPPost, `{"path":"/api/v1/orders"}`, actorID)
	insertAudit(trentonOrderID, audit.ActOrderChanged, `{"note":"portal delivery instructions"}`, actorID)
	insertAudit(trentonOrderID, audit.ActOrderStatusChanged, `{"old_status":"CONFIRMED","new_status":"PICKING","reason":"picker_started"}`, actorID)
	// The Kingston order has a trail too, the thing the fence test must never show.
	insertAudit(w.kingstonOrderID, audit.ActOrderStatusChanged, `{"old_status":"","new_status":"CONFIRMED"}`, actorID)

	// The SHIPPED services, wired as main.go wires them: the order service with the audit trail
	// seam, behind the same DeepReaders call the composition root makes.
	orderSvc := order.NewService(order.NewRepository(pool))
	orderSvc.WithAuditTrail(audit.NewService(audit.NewRepository(pool), nil))
	deps := DeepReaders(Services{Orders: orderSvc})
	w.reg = assist.NewRegistry(deps, assist.NewRepository(pool))

	t.Cleanup(func() {
		exec := func(sql string) { _, _ = pool.Exec(ctx, sql) }
		exec(`DELETE FROM audit_log WHERE entity_type = 'order' AND entity_id IN (SELECT id FROM orders WHERE order_no::text LIKE '#SO-DIB494-%')`)
		exec(`DELETE FROM orders WHERE order_no::text LIKE '#SO-DIB494-%'`)
		exec(`DELETE FROM customers WHERE account_no::text LIKE 'DIB494-%'`)
		exec(`DELETE FROM users WHERE email::text LIKE 'dib494-%@fixture.test'`)
	})
	return w
}

func (w *activityWorld) trenton() assist.Caller {
	return assist.Caller{UserID: "u-trt", RoleCode: "INSIDE_SALES", Tier: 3, BranchIDs: []string{w.trentonID}}
}

func TestIntegration_OrderActivityReadsTheRealTrailInCausalOrder(t *testing.T) {
	w := newActivityWorld(t)

	res, err := w.reg.Run(context.Background(), w.trenton(), "order.activity", assist.Args{"order_no": w.trentonOrderNo})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Empty {
		t.Fatalf("a real trail answered empty: %q", res.Headline)
	}
	// Three panel-visible rows; the HTTP.POST and the field-less ORDER_CHANGED never surface.
	if len(res.Items) != 3 {
		t.Fatalf("got %d items, want 3: %+v", len(res.Items), res.Items)
	}
	if !strings.Contains(res.Headline, "3 recorded changes") || !strings.Contains(res.Headline, "status moving to Picking") {
		t.Fatalf("headline = %q", res.Headline)
	}
	// Newest first, by the GENERATED seq: PICKING transition, then the price edit, then creation.
	if !strings.Contains(res.Items[0].Title, "Confirmed → Picking") {
		t.Fatalf("items[0] = %q", res.Items[0].Title)
	}
	if res.Items[1].Title != "Unit price (line 2)" {
		t.Fatalf("items[1] = %q", res.Items[1].Title)
	}
	if res.Items[2].Title != "Status set to Confirmed" {
		t.Fatalf("items[2] = %q", res.Items[2].Title)
	}
	// The money edit renders as money, and the actor as their real display name.
	if v := fieldOf(res.Items[1], "From"); v != "$1,250.00" {
		t.Fatalf("From = %q", v)
	}
	if v := fieldOf(res.Items[0], "By"); v != w.actorName {
		t.Fatalf("By = %q, want the users row's display name", v)
	}
	// The unattributed creation reads "system", as the order screens render it.
	if v := fieldOf(res.Items[2], "By"); v != "system" {
		t.Fatalf("creation By = %q", v)
	}
	if res.Ref == nil || res.Ref.Kind != "order" {
		t.Fatalf("ref = %+v", res.Ref)
	}
}

// The fence, against real SQL, both ways a person can point at another branch's order. The command
// must speak the branch-blind sentence and carry NO ref, an answer that admitted the order existed
// would be the oracle the branch-scoped resolve exists to prevent.
func TestIntegration_AnotherBranchsTrailIsNotFoundNotForbidden(t *testing.T) {
	w := newActivityWorld(t)

	for name, ref := range map[string]string{
		"by number":      w.kingstonOrderNo,
		"by pasted uuid": w.kingstonOrderID,
	} {
		t.Run(name, func(t *testing.T) {
			res, err := w.reg.Run(context.Background(), w.trenton(), "order.activity", assist.Args{"order_no": ref})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !res.Empty {
				t.Fatalf("a Trenton caller read a Kingston trail: %q", res.Headline)
			}
			if !strings.Contains(res.Headline, "I can't find an order numbered") {
				t.Fatalf("headline = %q, want the branch-blind sentence", res.Headline)
			}
			if res.Ref != nil || len(res.Items) != 0 {
				t.Fatalf("a refused order leaked structure: ref=%+v items=%d", res.Ref, len(res.Items))
			}
		})
	}
}

func fieldOf(it assist.Item, label string) string {
	for _, f := range it.Fields {
		if f.Label == label {
			return f.Value
		}
	}
	return ""
}
