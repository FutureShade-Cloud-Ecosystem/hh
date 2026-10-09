//go:build integration

// po_branch_fence_integration_test.go, HH-181 in the procurement manager's own shape: a branch
// scoped buyer's PO reads answer that branch and no other. The guard itself (the send replay keyed
// to its order and answered only after the branch fence) is vendor_order.go's, landed by PR 56 and
// bitten there; this file holds the four READ doors the role's everyday work walks through to the
// same standard: the PO list, one PO, what is incoming, and the receiving worklist, plus the send
// history read and the send itself for the replay's own door. Every leg is written against the
// service exactly as the identity guards stamp the caller, so what fails here is what would fail at
// the route.
package purchasing

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// procurementAt is the buyer the roadmap fences: a manager tier role, branch scoped, holding
// exactly one grant. The branch fence must hold for this caller the way it holds for the branch
// manager scopedCaller already exercises, and for the same reason: the grant map, not the role
// code, is what the fence reads.
func procurementAt(userID, branchID string) identity.StaffContext {
	return identity.StaffContext{UserID: userID, RoleCode: "PROCUREMENT_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{branchID: true}}
}

// fenceWorld plants one placed PO per branch on its own throwaway product, so every read has
// something to leak on both sides of the fence.
func fenceWorld(t *testing.T, h *harness) (poTRT, poKGN, prodTRT, prodKGN string) {
	t.Helper()
	prodTRT = h.newProduct(t, "fence-trt")
	prodKGN = h.newProduct(t, "fence-kgn")
	mk := func(branchID, prodID string) string {
		po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
			VendorID: h.vendor, BranchID: branchID, ExpectedAt: "", Notes: "HH-181 fence fixture",
			Lines: []AddLineParams{{ProductID: prodID, QtyOrderedBase: 40, UOM: "PC", UnitCostCents: 125}},
		})
		if err != nil {
			t.Fatalf("create PO at %s: %v", branchID, err)
		}
		po, err = h.svc.Transition(h.ctx, po.ID, StatusOrdered)
		if err != nil {
			t.Fatalf("place PO at %s: %v", branchID, err)
		}
		return po.ID
	}
	poTRT = mk(branchTRT, prodTRT)
	poKGN = mk(branchKGN, prodKGN)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_pos WHERE id = ANY($1::uuid[])`,
			[]string{poTRT, poKGN})
	})
	return poTRT, poKGN, prodTRT, prodKGN
}

func TestIntegration_HH181_ABranchScopedBuyerReadsOnlyTheirBranch(t *testing.T) {
	h := newHarness(t)
	poTRT, poKGN, prodTRT, prodKGN := fenceWorld(t, h)

	asRole(t, h, procurementAt(h.user, branchTRT), func() {
		// The list, no branch named: the sole grant is the implicit answer, and Kingston's PO is
		// not on it.
		pos, err := h.svc.ListPOs(h.ctx, "", "")
		if err != nil {
			t.Fatalf("list POs: %v", err)
		}
		for _, p := range pos {
			if p.BranchID == branchKGN {
				t.Errorf("the list carries the Kingston PO %s (%s) for a Trenton only buyer", p.PONo, p.ID)
			}
		}
		var sawTRT bool
		for _, p := range pos {
			if p.ID == poTRT {
				sawTRT = true
			}
		}
		if !sawTRT {
			t.Errorf("the list omits the buyer's own Trenton PO %s, so the empty answer is not the fence's shape", poTRT)
		}

		// The list, Kingston named outright: asking about a branch the caller cannot see is a
		// forbidden question, not an empty answer.
		if _, err := h.svc.ListPOs(h.ctx, branchKGN, ""); !errors.Is(err, errForbidden) {
			t.Errorf("list POs at Kingston: got %v, want the branch fence's refusal", err)
		}

		// One PO, Kingston's id: refused, never read.
		if _, err := h.svc.GetPO(h.ctx, poKGN); !errors.Is(err, errForbidden) {
			t.Errorf("read the Kingston PO: got %v, want the branch fence's refusal", err)
		}
		// The send history door beside it, same refusal.
		if _, err := h.svc.ListPOSends(h.ctx, poKGN); !errors.Is(err, errForbidden) {
			t.Errorf("read the Kingston PO's sends: got %v, want the branch fence's refusal", err)
		}
		// The send itself, with a replay key: the fence answers before the replay lookup does
		// (HH-181's own ordering, vendor_order.go).
		if _, err := h.svc.SendPO(h.ctx, poKGN, SendPORequest{IdempotencyKey: "fence-replay-key"}); !errors.Is(err, errForbidden) {
			t.Errorf("send the Kingston PO under a replay key: got %v, want the fence before the replay", err)
		}

		// Incoming, no branch named: the buyer's reach is Trenton, so Kingston's outstanding line
		// is not an answer and Trenton's is.
		lines, err := h.svc.IncomingForProduct(h.ctx, prodKGN, "")
		if err != nil {
			t.Fatalf("incoming for the Kingston product: %v", err)
		}
		if len(lines) != 0 {
			t.Errorf("incoming for the Kingston product answered %d lines for a Trenton only buyer, want none", len(lines))
		}
		lines, err = h.svc.IncomingForProduct(h.ctx, prodTRT, "")
		if err != nil {
			t.Fatalf("incoming for the Trenton product: %v", err)
		}
		if len(lines) != 1 || lines[0].BranchID != branchTRT || lines[0].POID != poTRT {
			t.Errorf("incoming for the Trenton product answered %+v, want the one Trenton line", lines)
		}
		// Incoming, Kingston named outright: the forbidden question again.
		if _, err := h.svc.IncomingForProduct(h.ctx, prodTRT, branchKGN); !errors.Is(err, errForbidden) {
			t.Errorf("incoming at Kingston: got %v, want the branch fence's refusal", err)
		}

		// The receiving worklist: Kingston's truck is not this buyer's queue.
		items, err := h.svc.Worklist(h.ctx, "")
		if err != nil {
			t.Fatalf("worklist: %v", err)
		}
		for _, it := range items {
			if it.BranchID == branchKGN {
				t.Errorf("the receiving worklist carries the Kingston PO %s for a Trenton only buyer", it.PONo)
			}
		}
		var sawOwn bool
		for _, it := range items {
			if it.POID == poTRT {
				sawOwn = true
			}
		}
		if !sawOwn {
			t.Errorf("the receiving worklist omits the buyer's own Trenton PO %s", poTRT)
		}
		if _, err := h.svc.Worklist(h.ctx, branchKGN); !errors.Is(err, errForbidden) {
			t.Errorf("worklist at Kingston: got %v, want the branch fence's refusal", err)
		}
	})
}

func TestIntegration_HH181_TheFenceHoldsForTheWriteDoorsToo(t *testing.T) {
	h := newHarness(t)
	poKGN := ""
	_, poKGN, _, _ = fenceWorld(t, h)

	asRole(t, h, procurementAt(h.user, branchTRT), func() {
		// statusOf reads the PO back as the harness's global caller (the asRole discipline: swap,
		// read, restore), so a refusal this test is proving cannot be masked by the read fence
		// answering the same 403 the mutation fence should have.
		statusOf := func() string {
			h.svc.withCaller(func(context.Context) (identity.StaffContext, bool) { return globalCaller(h.user), true })
			defer h.svc.withCaller(func(context.Context) (identity.StaffContext, bool) { return procurementAt(h.user, branchTRT), true })
			po, err := h.svc.GetPO(h.ctx, poKGN)
			if err != nil {
				t.Fatalf("read the Kingston PO as a global caller: %v", err)
			}
			return po.Status
		}
		// mutatePO (add a line, remove a line, place, receive) fences on the loaded PO's branch
		// before any mutation runs: the load-bearing leg for a buyer who cannot act on another
		// branch's order, not only read it.
		if _, err := h.svc.AddLine(h.ctx, poKGN, AddLineParams{ProductID: h.prodAny(t), QtyOrderedBase: 5, UOM: "PC", UnitCostCents: 100}); !errors.Is(err, errForbidden) {
			t.Errorf("add a line to the Kingston PO: got %v, want the branch fence's refusal", err)
		}
		if _, err := h.svc.RemoveLine(h.ctx, poKGN, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, errForbidden) {
			t.Errorf("remove a line on the Kingston PO: got %v, want the branch fence's refusal", err)
		}
		// CANCELLED is a legal move for this PO, so the only thing standing between a Trenton buyer
		// and cancelling Kingston's order is the fence: the refusal AND the status still ORDERED
		// after it, because a fence refused only at the response read would leave the mutation
		// committed behind the 403.
		if _, err := h.svc.Transition(h.ctx, poKGN, StatusCancelled); !errors.Is(err, errForbidden) {
			t.Errorf("cancel the Kingston PO: got %v, want the branch fence's refusal", err)
		}
		if got := statusOf(); got != StatusOrdered {
			t.Errorf("the Kingston PO reads %s after the refused cancel, want ORDERED: the fence refused the read but not the write", got)
		}
		// The correctable fields ride the same fence (incoming.go: both resolve through GetPO and
		// branchFence before they write).
		if _, err := h.svc.SetExpectedDate(h.ctx, poKGN, "2026-08-19"); !errors.Is(err, errForbidden) {
			t.Errorf("set the Kingston PO's expected date: got %v, want the branch fence's refusal", err)
		}
		if _, err := h.svc.SetDeliveryMethod(h.ctx, poKGN, "PICKUP"); !errors.Is(err, errForbidden) {
			t.Errorf("set the Kingston PO's arrival: got %v, want the branch fence's refusal", err)
		}
		if got := statusOf(); got != StatusOrdered {
			t.Errorf("the Kingston PO reads %s after the refused corrections, want ORDERED", got)
		}
	})
}

// prodAny hands back any seeded product id, for the one write leg whose product is incidental.
func (h *harness) prodAny(t *testing.T) string {
	t.Helper()
	var id string
	if err := h.pool.QueryRow(context.Background(), `SELECT id::text FROM products LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("no seeded product: %v", err)
	}
	return id
}
