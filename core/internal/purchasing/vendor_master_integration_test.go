//go:build integration

// vendor_master_integration_test.go, the vendor create and edit pair against a real database: the
// roadmap's Buying and Stock core slice names vendor create and edit as missing ("no vendor create
// or edit", section 1.5), and this file is the red first half of filling it. The load doors'
// supplier writer (vendorload.go) was the only writer the table had; these routes are the staff
// writer, and they follow the load's own conventions at source: the number is minted server side
// and never accepted from the caller, the seq is drawn from the vendors scope in the same
// statement, and the org stamp is the caller's.
package purchasing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// vendorMasterCleanup removes the vendors this file mints, by the number prefix the mint draws.
func vendorMasterCleanup(t *testing.T, h *harness) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(),
			`DELETE FROM vendors WHERE vendor_no ~ '^(V|VM)-[0-9]+$' AND vendor_no <> 'V-0100'`)
	})
}

func TestIntegration_VendorMaster_CreateMintsTheNumberAndStampsTheOrg(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)

	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{
		Name: "Test Aggregate Works (demo)", OrderEmail: "orders@testaggregate.example",
	})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	if v.ID == "" || !strings.HasPrefix(v.VendorNo, "VM-0") {
		t.Fatalf("the created vendor reads %+v, want a minted VM-0xxx number", v)
	}
	if !v.IsActive || v.PaymentTermsDays != 30 {
		t.Errorf("a created vendor reads terms %d active %v, want the 30 day active default", v.PaymentTermsDays, v.IsActive)
	}
	if v.OrderEmail != "orders@testaggregate.example" {
		t.Errorf("the ordering email reads %q, want what the create wrote", v.OrderEmail)
	}

	// The row the projections read: the directory lists it, and the remit fallback carries the
	// email the outbound send's address book reads.
	var email string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT remit_address->>'email' FROM vendors WHERE id = $1`, v.ID).Scan(&email); err != nil || email != "orders@testaggregate.example" {
		t.Errorf("remit_address->email reads %q (err %v), want the ordering email", email, err)
	}
	items, err := h.svc.ListVendors(h.ctx)
	if err != nil {
		t.Fatalf("list vendors: %v", err)
	}
	found := false
	for _, it := range items {
		if it.ID == v.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("the directory projection omits the created vendor")
	}

	// A PO may be raised against it at once: the whole point of the route is a buyer adding a
	// supplier mid job.
	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{VendorID: v.ID, BranchID: branchTRT,
		Lines: []AddLineParams{{ProductID: h.prodAny(t), QtyOrderedBase: 10, UOM: "PC", UnitCostCents: 90}}})
	if err != nil {
		t.Fatalf("draft a PO against the created vendor: %v", err)
	}
	_, _ = h.pool.Exec(context.Background(), `DELETE FROM purchasing_pos WHERE id = $1`, po.ID)
}

func TestIntegration_VendorMaster_CreateRefusesTheMalformed(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)

	if _, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "   "}); err == nil {
		t.Errorf("a blank name created a vendor, want a refusal")
	}
	if _, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "Terms negative",
		PaymentTermsDays: ptr(-1)}); err == nil {
		t.Errorf("negative terms created a vendor, want a refusal")
	}
	if _, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "Bad email",
		OrderEmail: "not-an-address"}); err == nil {
		t.Errorf("a malformed ordering email created a vendor, want a refusal")
	}
	if _, err := h.svc.CreateVendor(h.ctx, "not-a-uuid", CreateVendorRequest{Name: "No org"}); err == nil {
		t.Errorf("a caller with no org stamp created a vendor, want a refusal")
	}
	// Zero day terms are legal (pay on delivery), not a malformed absent.
	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "Cash on delivery (demo)", PaymentTermsDays: ptr(0)})
	if err != nil {
		t.Fatalf("zero day terms refused: %v", err)
	} else if v.PaymentTermsDays != 0 {
		t.Errorf("zero day terms read %d, want 0", v.PaymentTermsDays)
	}
}

func TestIntegration_VendorMaster_EditCorrectsTheRecordAndTheActiveSwitchHasTeeth(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)

	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "First name (demo)"})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	edited, err := h.svc.UpdateVendor(h.ctx, org, v.ID, UpdateVendorRequest{
		Name: ptr("Renamed supplier (demo)"), PaymentTermsDays: ptr(45), OrderEmail: ptr("desk@renamed.example"),
	})
	if err != nil {
		t.Fatalf("edit vendor: %v", err)
	}
	if edited.Name != "Renamed supplier (demo)" || edited.PaymentTermsDays != 45 || edited.OrderEmail != "desk@renamed.example" {
		t.Errorf("the edited vendor reads %+v, want the corrections", edited)
	}

	// Deactivating has teeth: the PO route refuses a new order against an inactive vendor.
	if _, err := h.svc.UpdateVendor(h.ctx, org, v.ID, UpdateVendorRequest{IsActive: ptr(false)}); err != nil {
		t.Fatalf("deactivate vendor: %v", err)
	}
	_, err = h.svc.CreatePO(h.ctx, CreatePORequest{VendorID: v.ID, BranchID: branchTRT,
		Lines: []AddLineParams{{ProductID: h.prodAny(t), QtyOrderedBase: 10, UOM: "PC", UnitCostCents: 90}}})
	if err == nil {
		t.Fatalf("a PO was drafted against the deactivated vendor, want the refusal")
	}

	// An unknown vendor is a 404, never a silent edit of nothing.
	if _, err := h.svc.UpdateVendor(h.ctx, org, uuid.NewString(), UpdateVendorRequest{Name: ptr("Nobody")}); !errors.Is(err, ErrUnknownVendor) {
		t.Errorf("edit an unknown vendor: got %v, want ErrUnknownVendor", err)
	}
	// A row another org owns is not this caller's to edit.
	_, _ = h.pool.Exec(context.Background(),
		`UPDATE vendors SET org_id = $2 WHERE id = $1`, v.ID, uuid.NewString())
	// ... and it answers exactly as an unknown id does (one 404, no existence answer).
	_, other := h.svc.UpdateVendor(h.ctx, org, v.ID, UpdateVendorRequest{Name: ptr("Other org row")})
	_, gone := h.svc.UpdateVendor(h.ctx, org, uuid.NewString(), UpdateVendorRequest{Name: ptr("Nobody")})
	if !errors.Is(other, ErrUnknownVendor) || other.Error() != gone.Error() {
		t.Errorf("edit another org's vendor: got %v, want the same answer as an unknown vendor (%v)", other, gone)
	}
}

// P3-2 of the security review: a carried vendor (org NULL, the rows a load or the seed brought in
// before tenancy) is stamped with the caller's org on the first edit, in the same statement as the
// write, and from then on it is that org's alone: another org's buyer meets the 404 an unknown id
// gets, and the stamp cannot be raced because the WHERE carries the org test.
func TestIntegration_VendorMaster_ACarriedVendorIsClaimedByTheFirstEditAndFencedAfter(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)
	otherOrg := uuid.NewString()

	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "Carried (demo)"})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(), `UPDATE vendors SET org_id = NULL WHERE id = $1`, v.ID); err != nil {
		t.Fatalf("carry the row: %v", err)
	}
	if _, err := h.svc.UpdateVendor(h.ctx, org, v.ID, UpdateVendorRequest{Name: ptr("Claimed (demo)")}); err != nil {
		t.Fatalf("first edit of a carried vendor: %v", err)
	}
	var stamped *string
	if err := h.pool.QueryRow(context.Background(), `SELECT org_id::text FROM vendors WHERE id = $1`, v.ID).Scan(&stamped); err != nil ||
		stamped == nil || *stamped != org {
		t.Fatalf("after the first edit the org stamp reads %v (err %v), want the caller's org %s", stamped, err, org)
	}
	_, other := h.svc.UpdateVendor(h.ctx, otherOrg, v.ID, UpdateVendorRequest{Name: ptr("Hijack")})
	if !errors.Is(other, ErrUnknownVendor) {
		t.Errorf("another org's edit of the claimed row: got %v, want the unknown vendor 404", other)
	}
	var name string
	if err := h.pool.QueryRow(context.Background(), `SELECT name FROM vendors WHERE id = $1`, v.ID).Scan(&name); err != nil || name != "Claimed (demo)" {
		t.Errorf("the row reads %q (err %v) after the refused edit, want Claimed (demo)", name, err)
	}
}

// An address with an apostrophe is a legal local part (mail.ParseAddress accepts it), so the
// create and the edit both have to carry it: the email rides the statement as a bound parameter,
// never as text built into it, and reads back byte for byte.
func TestIntegration_VendorMaster_AnApostropheAddressSavesOnCreateAndEdit(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)

	const first = "o'brien@example.com"
	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "Apostrophe create (demo)", OrderEmail: first})
	if err != nil {
		t.Fatalf("create with an apostrophe address: %v", err)
	}
	if v.OrderEmail != first {
		t.Errorf("the created vendor reads %q, want %q", v.OrderEmail, first)
	}

	const second = "d'arcy+orders@example.com"
	edited, err := h.svc.UpdateVendor(h.ctx, org, v.ID, UpdateVendorRequest{OrderEmail: ptr(second)})
	if err != nil {
		t.Fatalf("edit with an apostrophe address: %v", err)
	}
	if edited.OrderEmail != second {
		t.Errorf("the edited vendor reads %q, want %q", edited.OrderEmail, second)
	}
	var stored string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT remit_address->>'email' FROM vendors WHERE id = $1`, v.ID).Scan(&stored); err != nil || stored != second {
		t.Errorf("the stored remit email reads %q (err %v), want %q", stored, err, second)
	}
	// The edit merges only the email key: a key another hand planted survives it.
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE vendors SET remit_address = remit_address || '{"city":"Trenton"}'::jsonb WHERE id = $1`, v.ID); err != nil {
		t.Fatalf("plant a sibling key: %v", err)
	}
	if _, err := h.svc.UpdateVendor(h.ctx, org, v.ID, UpdateVendorRequest{OrderEmail: ptr(first)}); err != nil {
		t.Fatalf("second edit: %v", err)
	}
	var city, email string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT remit_address->>'city', remit_address->>'email' FROM vendors WHERE id = $1`, v.ID).Scan(&city, &email); err != nil ||
		city != "Trenton" || email != first {
		t.Errorf("after the merge the remit reads city %q email %q (err %v), want Trenton and %q", city, email, err, first)
	}
}

// P3-2 of the Opus review: the minted number and the load's keys must not share a key space. Two
// halves: a create whose minted number is already taken answers a 409 in words (never the raw
// 23505 as a 500), and the load refuses a legacy key in the mint's shape, so a later load item can
// never take the ON CONFLICT arm and overwrite a staff made vendor.
func TestIntegration_VendorMaster_ATakenMintedNumberIsA409InWords(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)

	first, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "Mint probe (demo)"})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	// Plant a row at the number the mint will draw next, as a load would have.
	var prefix string
	var n int
	for i := len(first.VendorNo); i > 0; i-- {
		if c := first.VendorNo[i-1]; c < '0' || c > '9' {
			prefix = first.VendorNo[:i]
			n, _ = strconv.Atoi(first.VendorNo[i:])
			break
		}
	}
	next := fmt.Sprintf("%s%0*d", prefix, len(first.VendorNo)-len(prefix), n+1)
	if _, err := h.pool.Exec(context.Background(),
		`INSERT INTO vendors (vendor_no, name, payment_terms_days, is_active) VALUES ($1, 'Planted by a load', 30, TRUE)`, next); err != nil {
		t.Fatalf("plant %s: %v", next, err)
	}
	_, err = h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "Collides (demo)"})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != http.StatusConflict || !strings.Contains(ce.msg, "vendor number") {
		t.Fatalf("a create whose minted number %s is taken = %v, want a 409 in words naming the vendor number", next, err)
	}
	// The sequence has moved, so a retry succeeds: the 409 says so.
	if _, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "Retry (demo)"}); err != nil {
		t.Errorf("the retry after the 409: %v", err)
	}
}

func TestIntegration_VendorMaster_TheLoadRefusesAKeyInTheMintsShape(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	e := vendorLoadSetup(t)
	t.Cleanup(func() { _, _ = e.pool.Exec(context.Background(), `DELETE FROM vendors WHERE vendor_no = 'VM-0101'`) })
	item := vendItem("VM-0101", "Loaded under a minted number", nil)
	item.DerivedKey = "4442a800-0000-4000-8000-0000000000f1"
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-mint", Caller: fxVendorActorID, Items: []load.Item{item}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if o := vendorOutcome(t, r, "VM-0101"); o.Status != load.OutcomeRefused || o.Field != "legacy_id" {
		t.Fatalf("a load key in the mint's shape answered %+v, want refused naming legacy_id", o)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM vendors WHERE vendor_no = 'VM-0101'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("the refused item wrote %d rows (err %v), want none", n, err)
	}
	_ = h
}

func ptr[T any](v T) *T { return &v }

// orgID plants one throwaway org anchor row and answers its id, because an org row on this gate
// database is otherwise the demo populate's to provision and the gate build stops at migrate and
// seed. The route stamps the org the guard resolved; the test has to own one to resolve.
func (h *harness) orgID(t *testing.T) string {
	t.Helper()
	const id = "7e110000-0000-4000-9000-0000000000b1"
	if _, err := h.pool.Exec(context.Background(),
		`INSERT INTO org (id, slug, name) VALUES ($1::uuid, 'vm-fence-org', 'Vendor master fence org')
		 ON CONFLICT (id) DO NOTHING`, id); err != nil {
		t.Fatalf("plant the org anchor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM org WHERE id = $1::uuid`, id)
	})
	return id
}
