//go:build integration

// qd5_overlap_integration_test.go, QD-5 review round 4 (fold 2): the group writer's overlap branch
// marks before it refuses. A fault between the invoice commit and the group marking (the marking is
// a separate transaction by design, and its retry was the recovery) used to leave a standing
// document whose groups the plan still read as unbilled, so a later request that shared one of those
// groups hit the overlap refusal, read the plan again, was offered the same group again, and was
// refused again: the route answered 400 for good. The overlap branch now links the overlapping
// document's groups to it (the marking is idempotent, the same write-back the raise itself runs)
// before it refuses, so the refusal's own advice, read the plan again and invoice what is left,
// describes a plan that can actually be followed.
//
// The fault is a cancelled context landing exactly between the invoice commit and the group marking,
// through the real group invoice path (order.InvoiceShipments over billing.InvoiceOrderGroups, the
// manager route's own stack), never a hand shaped row.
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run QD5Overlap -count=1 -p 1
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// qd5CancellingSource wraps the real order source and cancels the call's context at the FIRST group
// marking: the invoice has just committed inside the writer, and the marking that follows it fails
// the way a caller hanging up or a process dying after the commit would.
type qd5CancellingSource struct {
	billing.OrderInvoiceSource
	cancel context.CancelFunc
	marks  int
}

func (s *qd5CancellingSource) MarkGroupsInvoiced(ctx context.Context, groupIDs []string, invoiceID string, at time.Time) error {
	s.marks++
	if s.marks == 1 {
		s.cancel()
		return ctx.Err()
	}
	return s.OrderInvoiceSource.MarkGroupsInvoiced(ctx, groupIDs, invoiceID, at)
}

// TestQD5Overlap_TheRefusalMarksTheOverlappingDocumentsGroupsBeforeItRefuses: group 1 is invoiced
// and the marking faults after the commit; group 2 then completes. The next call's set shares group
// 1 with the standing document and must be refused, and by the time the refusal is answered group 1
// must be linked to that document, so the call the refusal tells the caller to make (read the plan
// again, invoice what is left) bills group 2 and finishes the order.
func TestQD5Overlap_TheRefusalMarksTheOverlappingDocumentsGroupsBeforeItRefuses(t *testing.T) {
	e := qd5Setup(t)
	q := e.seedQuote(1_000_000, 0, qd5Trenton)
	res := e.convert(q)
	ctx := context.Background()
	mgr := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: e.userID, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{qd5Trenton: true},
	})

	if _, err := e.orderSvc.SetInvoicePolicy(mgr, res.OrderID, "ON_DEMAND_SUBSET"); err != nil {
		t.Fatalf("set the invoice policy: %v", err)
	}
	// Group 1 complete, group 2 still planned: the first call's set is group 1 alone.
	var g1, g2 string
	if err := e.pool.QueryRow(ctx, `
		WITH g AS (
		  INSERT INTO shipment_groups (order_id, group_no, status, completed_at)
		  VALUES ($1, 1, 'COMPLETE', now()), ($1, 2, 'PLANNED', NULL) RETURNING id, group_no),
		l AS (
		  INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
		  SELECT g.id, ol.id, CASE g.group_no WHEN 1 THEN 1 ELSE ol.qty_base - 1 END
		    FROM g JOIN order_lines ol ON ol.order_id = $1 RETURNING 1)
		SELECT (SELECT id::text FROM g WHERE group_no = 1), (SELECT id::text FROM g WHERE group_no = 2), (SELECT count(*) FROM l)`,
		res.OrderID).Scan(&g1, &g2, new(int)); err != nil {
		t.Fatalf("seed the two shipment groups: %v", err)
	}

	// The route's own stack over a writer whose first group marking is cancelled: the invoice for
	// group 1 commits, the marking faults, and the faulted state is real.
	fault := &qd5CancellingSource{OrderInvoiceSource: w6cOrderInvoiceSource{svc: e.orderSvc}}
	ctx1, cancel := context.WithCancel(mgr)
	fault.cancel = cancel
	ship := order.NewService(order.NewRepository(e.pool)).
		WithMultiShipInvoicer(w6cMultiShipInvoicer{w: billing.NewInvoiceWriter(e.pool).WithOrderSource(fault)})
	if _, _, _, err := ship.InvoiceShipments(ctx1, res.OrderID); err == nil || !strings.Contains(err.Error(), "could not be marked") {
		t.Fatalf("the first call must fail at the cancelled marking, after its invoice committed, got %v", err)
	}
	cancel()
	var firstInv string
	if err := e.pool.QueryRow(ctx,
		`SELECT id::text FROM invoices WHERE customer_id = $1 AND doc_type = 'INVOICE'`, q.CustomerID).Scan(&firstInv); err != nil {
		t.Fatalf("the faulted call left no committed invoice: %v", err)
	}
	var marked *string
	if err := e.pool.QueryRow(ctx,
		`SELECT invoice_id::text FROM shipment_groups WHERE id = $1`, g1).Scan(&marked); err != nil || marked != nil {
		t.Fatalf("the fixture is wrong unless group 1 is unmarked after the fault: %v %v", marked, err)
	}

	// Another group completes, and the manager invoices again: the set now shares group 1 with the
	// standing document.
	if _, err := e.pool.Exec(ctx,
		`UPDATE shipment_groups SET status = 'COMPLETE', completed_at = now() WHERE id = $1`, g2); err != nil {
		t.Fatalf("complete group 2: %v", err)
	}
	_, _, _, err := ship.InvoiceShipments(mgr, res.OrderID)
	var br httpx.BadRequestError
	if !errors.As(err, &br) || !strings.Contains(br.Msg, "read the shipment plan again") {
		t.Fatalf("the overlapping request must still be refused with the plan advice, got %v", err)
	}
	// The refusal has healed the faulted marking: the standing document's groups are linked to it,
	// idempotently, before the refusal was answered. Without this the plan keeps offering group 1 and
	// the route answers 400 for good.
	var link string
	if err := e.pool.QueryRow(ctx,
		`SELECT invoice_id::text FROM shipment_groups WHERE id = $1`, g1).Scan(&link); err != nil || link != firstInv {
		t.Fatalf("the refusal must link the overlapping document's groups to it before it refuses: group 1 links %q, want invoice %q (%v)",
			link, firstInv, err)
	}

	// The recovery the refusal names: read the plan again and invoice what is left. Group 1 is now
	// billed on the plan, so this call bills group 2 and the order is billed exactly once across the
	// two documents.
	_, invID, _, err := ship.InvoiceShipments(mgr, res.OrderID)
	if err != nil {
		t.Fatalf("the recovery call after the refusal: %v", err)
	}
	if invID == "" || invID == firstInv {
		t.Fatalf("the recovery call answers a new document for what is left, got %q (first %q)", invID, firstInv)
	}
	docs, total := e.billedTotal(q)
	if docs != 2 || total != q.Total {
		t.Fatalf("after the recovery the order is billed %d across %d documents, want exactly the order total %d across 2",
			total, docs, q.Total)
	}
	var link2 string
	if err := e.pool.QueryRow(ctx,
		`SELECT invoice_id::text FROM shipment_groups WHERE id = $1`, g2).Scan(&link2); err != nil || link2 != invID {
		t.Fatalf("group 2 must link to the recovery's document: %q (%v)", link2, err)
	}
}
