//go:build integration

// vendor_order_integration_test.go, item 5 against a real database.
//
// The unit files prove the DECISIONS (who a send is aimed at, what a refused reader may see, that
// the two encoders are one source). What only a database can prove lives here: that migration 0197's
// CHECKs mean what the Go validators say they mean, that the policy override chain resolves in the
// order the SQL claims, that the folded vendor reference actually finds an order somebody typed back
// in four different ways, and that a clerk opening a real purchase order receives no cost bytes.
package purchasing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// - fixtures -

// item5Harness is newHarness plus the seams item 5 adds, and a cleanup for every row it writes.
func item5Harness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.svc = h.svc.WithBranches(itBranches{h: h}).WithVendorAddresses(nil)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_vendor_contacts WHERE vendor_id = $1::uuid`, h.vendor)
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_vendor_skus WHERE vendor_id = $1::uuid`, h.vendor)
		// The policy is a SEEDED row, so it is RESTORED rather than deleted: removing it would leave
		// the deployment without the default every other test reads.
		_, _ = h.pool.Exec(ctx, `UPDATE purchasing_policy SET cost_visibility = 'MANAGER_UP',
		        untagged_fallback = 'CONFIRM' WHERE branch_id IS NULL`)
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_policy WHERE branch_id IS NOT NULL`)
	})
	return h
}

// itBranches satisfies BranchDirectory over the real locations table, the same read main.go's
// adapter performs, kept in step by being the same two columns.
type itBranches struct{ h *harness }

func (a itBranches) Branch(ctx context.Context, branchID string) (BranchBrief, error) {
	var b BranchBrief
	err := a.h.pool.QueryRow(ctx, `
		SELECT l.name, l.code::text, COALESCE(l.address->>'line1','')
		  FROM locations l WHERE l.id = $1`, branchID).Scan(&b.Name, &b.Code, &b.Address)
	b.OrgName = "Dibbits Landscape Supply"
	return b, err
}

// itAddresses is the ambiguity fixture: it hands back however many addresses the test asks for.
type itAddresses struct{ addrs []string }

func (a itAddresses) VendorOrderCandidates(context.Context, string) ([]string, error) {
	return a.addrs, nil
}

func clerkCaller(userID string) identity.StaffContext {
	return identity.StaffContext{UserID: userID, RoleCode: "YARD_TEAM",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}

// asRole runs fn with the harness service answering as `c`, then restores the harness's own caller.
//
// IT IS A SWAP AND NOT A SECOND SERVICE, and the comment is here because the obvious spelling is
// wrong: withCaller is a BUILDER OPTION that MUTATES its receiver and returns it, so
// `clerkSvc := h.svc.withCaller(clerk)` turns h.svc into the clerk too, and a positive control read
// afterwards would silently be the clerk's, which is the one thing a gate test must not be.
func asRole(t *testing.T, h *harness, c identity.StaffContext, fn func()) {
	t.Helper()
	h.svc.withCaller(func(context.Context) (identity.StaffContext, bool) { return c, true })
	defer h.svc.withCaller(func(context.Context) (identity.StaffContext, bool) { return globalCaller(h.user), true })
	fn()
}

// - 1 · the policy, and its override chain -

func TestIntegration_Item5_PolicyResolvesTheDealerDefaultThenTheBranchOverride(t *testing.T) {
	h := item5Harness(t)

	if got := h.svc.PolicyFor(h.ctx, branchTRT); got.CostVisibility != settings.VisibilityManagerUp {
		t.Fatalf("the seeded default is %q, want MANAGER_UP, 0197 must ship restrictive", got.CostVisibility)
	}
	if got := h.svc.PolicyFor(h.ctx, branchTRT); got.IsBranchOverride {
		t.Fatal("a branch with no row reported an override")
	}

	if _, err := h.svc.SetPolicy(h.ctx, SetPolicyRequest{
		BranchID: branchTRT, CostVisibility: settings.VisibilityAllStaff}); err != nil {
		t.Fatalf("set branch policy: %v", err)
	}
	got := h.svc.PolicyFor(h.ctx, branchTRT)
	if got.CostVisibility != settings.VisibilityAllStaff || !got.IsBranchOverride {
		t.Fatalf("branch policy = %+v, want ALL_STAFF as an override", got)
	}
	// The OTHER branch is untouched: an override is one branch's, and a chain that leaked would be
	// a dealer-wide change nobody asked for.
	if other := h.svc.PolicyFor(h.ctx, branchKGN); other.CostVisibility != settings.VisibilityManagerUp {
		t.Fatalf("the override leaked to %s: %+v", branchKGN, other)
	}
	if _, err := h.svc.SetPolicy(h.ctx, SetPolicyRequest{CostVisibility: "EVERYONE"}); err == nil {
		t.Fatal("an unknown visibility word was accepted, it must be refused in words before the CHECK could 500")
	}
}

// - 2 · the cost gate, on a real purchase order -

func TestIntegration_Item5_AClerkOpensTheSameOrderAndReceivesNoCostBytes(t *testing.T) {
	h := item5Harness(t)
	product := h.newProduct(t, "gate")
	po := h.orderedPO(t, product, 120, 5511)

	// THE POSITIVE CONTROL, and it is first for the usual reason: a scan with no control passes over
	// a payload whose money never populated at all.
	asManager, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatalf("manager read: %v", err)
	}
	mgrJSON, _ := json.Marshal(asManager)
	for _, want := range []string{`"unit_cost_cents":5511`, `"subtotal_cents":661320`} {
		if !strings.Contains(string(mgrJSON), want) {
			t.Fatalf("the MANAGER is missing %s, every absence below would be vacuous:\n%s", want, mgrJSON)
		}
	}
	mgrCosts, err := h.svc.POCostsFor(h.ctx, po.ID)
	if err != nil {
		t.Fatalf("manager costs: %v", err)
	}
	if raw, _ := json.Marshal(mgrCosts); !strings.Contains(string(raw), `"subtotal_cents":661320`) {
		t.Fatalf("manager POCosts = %s", raw)
	}

	asRole(t, h, clerkCaller(h.user), func() {
		asClerk, err := h.svc.GetPO(h.ctx, po.ID)
		if err != nil {
			t.Fatalf("clerk read: %v", err)
		}
		body := string(mustJSON(t, asClerk))
		for _, forbidden := range []string{"unit_cost_cents", "line_total_cents", "billed_cents",
			"subtotal_cents", "subtotal_cad_cents", "5511", "661320"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("the clerk received %q:\n%s", forbidden, body)
			}
		}
		// `null` IS NOT ABSENCE, the arm three trains here have been caught by. Nothing on a
		// purchase order may arrive as a field that announces itself and declines to answer.
		if strings.Contains(body, "null") {
			t.Errorf("the clerk received a literal null:\n%s", body)
		}
		// The ORDER still arrived in full, which is the whole argument for gating at the marshal layer.
		if !strings.Contains(body, po.PONo) || !strings.Contains(body, `"qty_ordered_base":120`) {
			t.Errorf("the clerk lost the purchase order itself:\n%s", body)
		}
		clerkCosts, err := h.svc.POCostsFor(h.ctx, po.ID)
		if err != nil {
			t.Fatalf("clerk costs: %v", err)
		}
		if raw := mustJSON(t, clerkCosts); string(raw) != "{}" {
			t.Fatalf("clerk POCosts = %s, want {}", raw)
		}
	})

	// ALL_STAFF opens the gate for the same clerk, on the same order, with no other change.
	if _, err := h.svc.SetPolicy(h.ctx, SetPolicyRequest{
		BranchID: branchTRT, CostVisibility: settings.VisibilityAllStaff}); err != nil {
		t.Fatalf("open the gate: %v", err)
	}
	asRole(t, h, clerkCaller(h.user), func() {
		opened, err := h.svc.GetPO(h.ctx, po.ID)
		if err != nil {
			t.Fatalf("clerk read after ALL_STAFF: %v", err)
		}
		if raw := mustJSON(t, opened); !strings.Contains(string(raw), `"unit_cost_cents":5511`) {
			t.Fatalf("ALL_STAFF did not reach the clerk:\n%s", raw)
		}
	})
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestIntegration_Item5_TheDocumentLosesItsMoneyColumnsForAClerk(t *testing.T) {
	h := item5Harness(t)
	product := h.newProduct(t, "doc-gate")
	po := h.orderedPO(t, product, 120, 5511)

	mgrDoc, err := h.svc.PODocumentFor(h.ctx, po.ID)
	if err != nil {
		t.Fatalf("manager document: %v", err)
	}
	if !mgrDoc.ShowMoney {
		t.Fatal("the manager's document has no money, every absence below would be vacuous")
	}
	mgrCSV, _ := EncodePODocumentCSV(mgrDoc)
	if !strings.Contains(string(mgrCSV), "55.11") {
		t.Fatalf("the manager's document carries no price:\n%s", mgrCSV)
	}

	asRole(t, h, clerkCaller(h.user), func() {
		clerkDoc, err := h.svc.PODocumentFor(h.ctx, po.ID)
		if err != nil {
			t.Fatalf("clerk document: %v", err)
		}
		page, _ := RenderPODocumentHTML(clerkDoc)
		clerkCSV, _ := EncodePODocumentCSV(clerkDoc)
		for _, forbidden := range []string{"55.11", "6613.20", "Unit cost", "Line total", "Order total"} {
			if strings.Contains(page, forbidden) {
				t.Errorf("the clerk's print page carries %q", forbidden)
			}
			if strings.Contains(string(clerkCSV), forbidden) {
				t.Errorf("the clerk's CSV carries %q", forbidden)
			}
		}
		if !strings.Contains(page, po.PONo) || !strings.Contains(string(clerkCSV), po.PONo) {
			t.Error("the clerk's document lost the order number, the gate removes money and nothing else")
		}
	})
}

// - 3 · the ordering contact and the send -

func TestIntegration_Item5_TheOrderGoesToTheOneTaggedDeskOrIsRefusedInWords(t *testing.T) {
	h := item5Harness(t)
	product := h.newProduct(t, "send")
	po := h.orderedPO(t, product, 40, 2937)

	// NOBODY TAGGED, NO ADDRESSES ON THE RECORD → refused in words, and nothing written.
	_, err := h.svc.SendPO(h.ctx, po.ID, SendPORequest{})
	if err == nil {
		t.Fatal("an order with no recipient was sent")
	}
	if !strings.Contains(err.Error(), "tag an ordering contact") {
		t.Fatalf("the refusal does not name the way forward: %q", err)
	}
	if sends, _ := h.svc.ListPOSends(h.ctx, po.ID); len(sends) != 0 {
		t.Fatalf("a refused send left %d rows behind", len(sends))
	}

	// TWO ADDRESSES ON THE RECORD, NOBODY TAGGED → the ambiguity refusal, with the count.
	ambiguous := h.svc.WithVendorAddresses(itAddresses{addrs: []string{"a@v.example", "b@v.example"}})
	_, err = ambiguous.SendPO(h.ctx, po.ID, SendPORequest{ConfirmFallback: true})
	if err == nil {
		t.Fatal("two candidate addresses and no tagged desk was not refused")
	}
	if !strings.Contains(err.Error(), "2 email addresses") {
		t.Fatalf("the ambiguity refusal does not name the count: %q", err)
	}

	// TAG ONE → exactly that desk, recorded as VENDOR_CONTACT.
	if _, err := h.svc.SetVendorContact(h.ctx, VendorContact{
		VendorID: h.vendor, Name: "Orders desk", Email: "orders@vendor.example"}); err != nil {
		t.Fatalf("tag: %v", err)
	}
	got, err := h.svc.SendPO(h.ctx, po.ID, SendPORequest{IdempotencyKey: "it-send-1"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.Recipient != "orders@vendor.example" || got.RecipientKind != RecipientVendorContact {
		t.Fatalf("send went to %+v", got)
	}
	// NOTHING IN THIS BUILD DELIVERS EMAIL, and the row says so rather than claiming a delivery.
	if got.Status != SendFailed || !strings.Contains(got.Detail, "no transport is wired") {
		t.Fatalf("the unwired send claimed %q / %q", got.Status, got.Detail)
	}
	if got.DocumentName == "" || got.DocumentBytes == 0 {
		t.Fatalf("the send recorded no document: %+v", got)
	}

	// The retry reuses its row.
	again, err := h.svc.SendPO(h.ctx, po.ID, SendPORequest{IdempotencyKey: "it-send-1"})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if again.ID != got.ID {
		t.Fatalf("the retry minted a second order: %s vs %s", again.ID, got.ID)
	}
	sends, err := h.svc.ListPOSends(h.ctx, po.ID)
	if err != nil {
		t.Fatalf("list sends: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("%d send rows for one order", len(sends))
	}

	// UNTAGGING DOES NOT REWRITE HISTORY, the whole reason recipient_kind is a stored column.
	if err := h.svc.DeleteVendorContact(h.ctx, h.vendor); err != nil {
		t.Fatalf("untag: %v", err)
	}
	after, _ := h.svc.ListPOSends(h.ctx, po.ID)
	if len(after) != 1 || after[0].RecipientKind != RecipientVendorContact {
		t.Fatalf("untagging changed the story of a send that already happened: %+v", after)
	}
}

func TestIntegration_Item5_TheContactTableHoldsOneRowPerVendor(t *testing.T) {
	h := item5Harness(t)
	if _, err := h.svc.SetVendorContact(h.ctx, VendorContact{
		VendorID: h.vendor, Name: "First", Email: "first@vendor.example"}); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if _, err := h.svc.SetVendorContact(h.ctx, VendorContact{
		VendorID: h.vendor, Name: "Second", Email: "second@vendor.example"}); err != nil {
		t.Fatalf("retag: %v", err)
	}
	var n int
	if err := h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM purchasing_vendor_contacts WHERE vendor_id = $1::uuid`, h.vendor).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d contact rows for one vendor, a list is the blast, one INSERT at a time", n)
	}
	got, found, err := h.svc.GetVendorContact(h.ctx, h.vendor)
	if err != nil || !found || got.Email != "second@vendor.example" {
		t.Fatalf("retag did not replace: %+v found=%v err=%v", got, found, err)
	}
	// The CHECK holds against a direct write too: a contact with neither channel is not a contact.
	if _, err := h.pool.Exec(h.ctx, `INSERT INTO purchasing_vendor_contacts (vendor_id, contact_name)
	    VALUES ($1::uuid, 'nobody') ON CONFLICT (vendor_id) DO UPDATE SET email = '', phone = ''`, h.vendor); err == nil {
		t.Error("the database accepted a contact with no email and no phone")
	}
}

// - 4 · the vendor's catalogue vocabulary, on the real document -

func TestIntegration_Item5_TheDocumentSpeaksTheVendorsVocabularyWhereItIsMapped(t *testing.T) {
	h := item5Harness(t)
	mapped := h.newProduct(t, "mapped")
	unmapped := h.newProduct(t, "unmapped")

	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT, ExpectedAt: "2026-09-14",
		Lines: []AddLineParams{
			{ProductID: mapped, QtyOrderedBase: 120, UOM: "EA", UnitCostCents: 5511},
			{ProductID: unmapped, QtyOrderedBase: 40, UOM: "EA", UnitCostCents: 2937},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	h.cleanupPO(t, po.ID)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order: %v", err)
	}
	if _, err := h.svc.SetVendorSKU(h.ctx, VendorSKU{VendorID: h.vendor, ProductID: mapped,
		VendorSKU: "UNI-9911", VendorUOM: "PLT", QtyPerVendorUOM: 60}); err != nil {
		t.Fatalf("map: %v", err)
	}

	doc, err := h.svc.PODocumentFor(h.ctx, po.ID)
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	if len(doc.Lines) != 2 {
		t.Fatalf("%d document lines", len(doc.Lines))
	}
	if doc.Lines[0].ItemCode != "UNI-9911" || !doc.Lines[0].CodeFromVendor {
		t.Errorf("the mapped line does not speak the vendor's code: %+v", doc.Lines[0])
	}
	if doc.Lines[0].OrderQty != 2 || doc.Lines[0].OrderUOM != "PLT" {
		t.Errorf("120 EA at 60 per pallet is 2 PLT: %+v", doc.Lines[0])
	}
	if doc.Lines[0].BaseQty != 120 || doc.Lines[0].BaseUOM != "EA" {
		t.Errorf("the base pair was translated away: %+v", doc.Lines[0])
	}
	if doc.Lines[1].CodeFromVendor || doc.Lines[1].UnitFromVendor {
		t.Errorf("the unmapped line claims the vendor's vocabulary: %+v", doc.Lines[1])
	}

	// The rendered document names which vocabulary each line used, in BOTH encodings.
	page, _ := RenderPODocumentHTML(doc)
	blob, _ := EncodePODocumentCSV(doc)
	for _, want := range []string{"Vendor code and unit", "Our catalogue", "UNI-9911"} {
		if !strings.Contains(page, want) {
			t.Errorf("the print page does not say %q", want)
		}
		if !strings.Contains(string(blob), want) {
			t.Errorf("the CSV does not say %q", want)
		}
	}
	// The ship-to line came through the branch seam, not through a JOIN in this module.
	if !strings.Contains(page, "Trenton") {
		t.Error("the document has no ship-to branch")
	}

	// The all-or-nothing CHECK holds against a direct write.
	if _, err := h.pool.Exec(h.ctx, `INSERT INTO purchasing_vendor_skus (vendor_id, product_id, vendor_uom)
	    VALUES ($1::uuid, $2::uuid, 'PLT')`, h.vendor, unmapped); err == nil {
		t.Error("the database accepted a vendor unit with no conversion factor")
	}
}

// - 5 · the number the vendor echoes back -

func TestIntegration_Item5_AnEchoedOrderNumberFindsItsOrderHoweverItWasTypedBack(t *testing.T) {
	h := item5Harness(t)
	product := h.newProduct(t, "echo")
	po := h.orderedPO(t, product, 10, 100)

	// Every one of these is a real thing a vendor writes on an acknowledgement.
	for _, typed := range []string{po.PONo, strings.TrimPrefix(po.PONo, "#"),
		strings.ToLower(po.PONo), strings.ReplaceAll(strings.TrimPrefix(po.PONo, "#"), "-", " "),
		strings.ReplaceAll(strings.TrimPrefix(po.PONo, "#"), "-", "")} {
		got, err := h.svc.ResolveEchoedRef(h.ctx, typed)
		if err != nil {
			t.Errorf("%q did not find the order: %v", typed, err)
			continue
		}
		if got.ID != po.ID {
			t.Errorf("%q found %s, want %s", typed, got.PONo, po.PONo)
		}
		// THE TOTAL IS THE ORDER'S, NOT A ZERO. A header read with no line aggregate would stamp
		// `subtotal_cents: 0` for a caller who may see cost, which is a purchase order that reads as
		// free, strictly worse than the absence the gate produces for a caller who may not.
		if got.SubtotalCents != 1000 || got.LineCount != 1 {
			t.Errorf("%q resolved to a total of %d over %d lines, want 1000 over 1",
				typed, got.SubtotalCents, got.LineCount)
		}
	}
	if _, err := h.svc.ResolveEchoedRef(h.ctx, "PO-NOT-A-THING-999999"); err == nil {
		t.Error("a number nobody issued resolved to an order")
	}
	if _, err := h.svc.ResolveEchoedRef(h.ctx, "  "); err == nil {
		t.Error("an empty reference resolved to an order")
	}
	// The lookup is BRANCH-FENCED like every other read here.
	kgn, _ := scopedCaller(h.user, branchKGN)(context.Background())
	asRole(t, h, kgn, func() {
		if _, err := h.svc.ResolveEchoedRef(h.ctx, po.PONo); err == nil {
			t.Error("a Kingston-scoped caller resolved a Trenton order by its number")
		}
	})
}
