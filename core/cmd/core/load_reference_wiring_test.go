package main

// load_reference_wiring_test.go proves the four submit doors' composed
// wiring (HH-LOAD-1 LD-4) on the real startup path, beside the frame
// doors' own wiring proof: with the doors off (the default) each submit
// route does not exist; with the doors on, a wrong caller gets the
// machine gate's one 403 body, and the right caller, holding the door
// on its own principal record, reaches the handler's own answers. All
// principals, slugs and keys are fixtures; the machine key material is
// generated inside the test and discarded with the process.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// fixtureMoverRefID is the fixture principal that holds one submit
// door, invented.
const fixtureMoverRefID = "7c2e6b41-0000-4000-8000-00000000d101"

// submitRoutes are the submit doors' registration patterns: LD-4's four,
// LD-3's two (the unit catalogue upsert and the product unit set
// replace), and LD-5's three (the product master upsert, the product
// image put, and the supplier SKU upsert).
var submitRoutes = []string{
	"PUT /api/v1/load/branches",
	"PUT /api/v1/load/users",
	"PUT /api/v1/load/categories",
	"PUT /api/v1/load/suppliers",
	"PUT /api/v1/load/units",
	"PUT /api/v1/load/products/unit-sets",
	"PUT /api/v1/load/products",
	"PUT /api/v1/load/products/{legacyId}/image",
	"PUT /api/v1/load/supplier-skus",
}

// plantSubmitPrincipal plants one enabled machine principal beside the
// kit's north org holding the branch map door alone.
func plantSubmitPrincipal(t *testing.T, env *compEnv) {
	t.Helper()
	ctx := context.Background()
	_, err := env.fixturePool().Exec(ctx, `
		INSERT INTO machine_principal (id, org_id, name, enabled, doors)
		VALUES ($1::uuid, $2::uuid, 'fixture submit mover', TRUE, $3::text[])
		ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, doors = EXCLUDED.doors`,
		fixtureMoverRefID, testkit.OrgNorthID, []string{"loadMapBranches"})
	if err != nil {
		t.Fatalf("plant submit principal: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.fixturePool().Exec(ctx,
			`DELETE FROM machine_principal WHERE id = $1::uuid`, fixtureMoverRefID)
	})
}

// machineJWKSServer serves the fixture's machine key set over a local
// test server the composition's verifier reads.
func machineJWKSServer(t *testing.T, key *rsa.PrivateKey) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(machineJWKS(t, key))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSubmitDoorsFollowTheSwitchAndTheGate(t *testing.T) {
	// Doors off, the default: none of the four routes exists.
	offEnv := newCompEnv(t)
	offRec, _ := registeredSurface(t, offEnv)
	for _, route := range submitRoutes {
		rec := serveLoad(t, offRec.inner, http.MethodPut, route[len("PUT "):], "", "", `{}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s with the doors off answered %d, want 404: the route does not exist", route, rec.Code)
		}
	}

	// Doors on, with the machine credential's configuration and a
	// principal holding the branch map door.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate machine key: %v", err)
	}
	srv := machineJWKSServer(t, key)
	onEnv := newCompEnv(t)
	plantSubmitPrincipal(t, onEnv)
	onEnv.set("CORE_LOAD_DOORS", "on")
	onEnv.set("CORE_MACHINE_AUDIENCE", fixtureMachineAudience)
	onEnv.set("CORE_MACHINE_JWKS_URL", srv.URL)
	onRec, _ := registeredSurface(t, onEnv)
	mux := onRec.inner

	right := machineBearer(t, onEnv, key, fixtureMoverRefID)

	// A wrong caller on any submit route is the one 403 body.
	staff := onEnv.minter.StaffToken(t, testkit.SubGeneralMgr)
	rec := serveLoad(t, mux, http.MethodPut, "/api/v1/load/branches", staff, testkit.OrgNorthSlug,
		`{"source_system":"fixture","batch_id":"b-wrong","items":[{"legacy_id":"B-1","record":{"code":"FXB1"}}]}`)
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("a staff bearer on a submit door answered %d %q, want the one 403 body", rec.Code, rec.Body.String())
	}

	// The right caller reaches the handler's own answers: a body with no
	// items is the handler's 400, wearing no gate body, which no wrong
	// caller can reach.
	rec = serveLoad(t, mux, http.MethodPut, "/api/v1/load/branches", right, testkit.OrgNorthSlug,
		`{"source_system":"fixture","batch_id":"b-empty","items":[]}`)
	if rec.Code != http.StatusBadRequest || rec.Body.String() == string(authz.BodyForbidden) {
		t.Fatalf("the right caller with an empty list answered %d %q, want the handler's 400", rec.Code, rec.Body.String())
	}
	// The same right caller on a door its principal does not hold is the
	// gate's 403 again: the principal's own door list is the wall.
	rec = serveLoad(t, mux, http.MethodPut, "/api/v1/load/users", right, testkit.OrgNorthSlug,
		`{"source_system":"fixture","batch_id":"b-other","items":[{"legacy_id":"U-1","record":{"email":"fixture.rep.one@example.invalid"}}]}`)
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("a right caller on an unheld door answered %d %q, want the one 403 body", rec.Code, rec.Body.String())
	}
}

// TestSubmitDoorsServeEachDoorEndToEndThroughTheGate drives all four
// doors with a principal that holds them, through the real wiring: the
// branch and user maps answer attached against rows that already exist
// (the seeded TRT branch and one planted staff row), the category door
// builds a two level tree by legacy key, the supplier door lands a
// vendor with its contact, and a replay of the branch map answers
// unchanged. Rows the test leaves are removed with it.
func TestSubmitDoorsServeEachDoorEndToEndThroughTheGate(t *testing.T) {
	ctx := context.Background()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate machine key: %v", err)
	}
	srv := machineJWKSServer(t, key)
	env := newCompEnv(t)
	pool := env.fixturePool()
	if _, err := pool.Exec(ctx, `
		INSERT INTO machine_principal (id, org_id, name, enabled, doors)
		VALUES ($1::uuid, $2::uuid, 'fixture submit mover', TRUE, $3::text[])
		ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, doors = EXCLUDED.doors`,
		fixtureMoverRefID, testkit.OrgNorthID,
		[]string{"loadMapBranches", "loadMapUsers", "loadUpsertCategories", "loadUpsertSuppliers",
			"loadUpsertUnits", "loadPutUnitSets", "loadUpsertProducts", "loadPutProductImage", "loadUpsertSupplierSkus"}); err != nil {
		t.Fatalf("plant principal: %v", err)
	}
	const staffID = "7c2e6b41-0000-4000-8000-00000000d102"
	const staffEmail = "fixture.wired.rep@example.invalid"
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, status) VALUES ($1::uuid, $2, 'Fixture Wired Rep', 'ACTIVE')
		ON CONFLICT (id) DO NOTHING`, staffID, staffEmail); err != nil {
		t.Fatalf("plant staff row: %v", err)
	}
	clean := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid AND source_system = 'wiredfx'`, testkit.OrgNorthID)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE entity_id IN (SELECT id FROM load_batch WHERE org_id = $1::uuid AND source_system = 'wiredfx')`, testkit.OrgNorthID)
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = $1::uuid AND source_system = 'wiredfx'`, testkit.OrgNorthID)
		_, _ = pool.Exec(ctx, `DELETE FROM purchasing_vendor_skus WHERE vendor_id IN (SELECT id FROM vendors WHERE vendor_no = 'FXV-W1')`)
		_, _ = pool.Exec(ctx, `DELETE FROM purchasing_vendor_contacts WHERE vendor_id IN (SELECT id FROM vendors WHERE vendor_no = 'FXV-W1')`)
		_, _ = pool.Exec(ctx, `DELETE FROM vendors WHERE vendor_no = 'FXV-W1'`)
		_, _ = pool.Exec(ctx, `DELETE FROM product_categories WHERE code LIKE 'fxcat-w%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM uoms WHERE code LIKE 'FXU-W%'`)
		_, _ = pool.Exec(ctx, `UPDATE uoms SET org_id = NULL, idempotency_key = NULL WHERE code = 'PLT'`)
		_, _ = pool.Exec(ctx, `DELETE FROM product_uoms WHERE product_id = $1::uuid`, wiredSetProductID)
		_, _ = pool.Exec(ctx, `DELETE FROM product_images WHERE product_id = $1::uuid`, wiredSetProductID)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE sku = 'FX-W-1'`)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = $1::uuid`, wiredSetProductID)
	}
	clean()
	t.Cleanup(func() {
		clean()
		_, _ = pool.Exec(ctx, `DELETE FROM machine_principal WHERE id = $1::uuid`, fixtureMoverRefID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, staffID)
	})
	env.set("CORE_LOAD_DOORS", "on")
	env.set("CORE_MACHINE_AUDIENCE", fixtureMachineAudience)
	env.set("CORE_MACHINE_JWKS_URL", srv.URL)
	rec, _ := registeredSurface(t, env)
	mux := rec.inner
	right := machineBearer(t, env, key, fixtureMoverRefID)

	batchID := ""
	type answer struct {
		BatchID string `json:"batch_id"`
		Counts  map[string]int
		Items   []struct {
			LegacyID string  `json:"legacy_id"`
			Status   string  `json:"status"`
			HHID     string  `json:"hh_id"`
			Reason   *string `json:"reason"`
			Field    *string `json:"field"`
		}
	}
	put := func(route, body string) answer {
		t.Helper()
		resp := serveLoad(t, mux, http.MethodPut, route, right, testkit.OrgNorthSlug, body)
		if resp.Code != http.StatusOK {
			t.Fatalf("PUT %s answered %d %q, want 200", route, resp.Code, resp.Body.String())
		}
		var a answer
		if err := json.Unmarshal(resp.Body.Bytes(), &a); err != nil {
			t.Fatalf("PUT %s body: %v", route, err)
		}
		return a
	}
	statuses := func(a answer) string {
		s := ""
		for _, it := range a.Items {
			s += it.LegacyID + "=" + it.Status + ";"
		}
		return s
	}

	a := put("/api/v1/load/branches",
		`{"source_system":"wiredfx","batch_id":"b-w-1","items":[{"legacy_id":"WB-1","record":{"code":"TRT"}}]}`)
	if statuses(a) != "WB-1=attached;" || a.Counts["attached"] != 1 {
		t.Fatalf("the branch map answered %s counts %v, want attached", statuses(a), a.Counts)
	}
	// The batch's audit row carries the attached count: the allowlist
	// names it, so the platform's audit path keeps it.
	var attachedCount string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(a.changes->>'load_attached', '') FROM audit_log a
		JOIN load_batch b ON b.id = a.entity_id
		WHERE b.org_id = $1::uuid AND b.source_system = 'wiredfx' AND b.batch_id = 'b-w-1'`,
		testkit.OrgNorthID).Scan(&attachedCount); err != nil {
		t.Fatalf("attach batch audit row: %v", err)
	}
	if attachedCount != "1" {
		t.Fatalf("the attach batch's audit row carries load_attached %q, want 1", attachedCount)
	}
	a = put("/api/v1/load/branches",
		`{"source_system":"wiredfx","batch_id":"b-w-1r","items":[{"legacy_id":"WB-1","record":{"code":"TRT"}}]}`)
	if statuses(a) != "WB-1=unchanged;" {
		t.Fatalf("the branch map replay answered %s, want unchanged", statuses(a))
	}
	a = put("/api/v1/load/users",
		`{"source_system":"wiredfx","batch_id":"b-w-2","items":[{"legacy_id":"WU-1","record":{"email":"`+staffEmail+`"}},{"legacy_id":"WU-2","record":{"email":"nobody.here@example.invalid"}}]}`)
	if statuses(a) != "WU-1=attached;WU-2=refused;" || a.Items[0].HHID != staffID {
		t.Fatalf("the user map answered %s, want one attached to the staff row and one refused", statuses(a))
	}
	a = put("/api/v1/load/categories",
		`{"source_system":"wiredfx","batch_id":"b-w-3","items":[{"legacy_id":"WC-1","record":{"code":"fxcat-w1","name":"Fixture Root"}},{"legacy_id":"WC-2","record":{"code":"fxcat-w2","name":"Fixture Leaf"},"requires":[{"kind":"category","legacy_id":"WC-1"}]}]}`)
	if statuses(a) != "WC-1=created;WC-2=created;" {
		t.Fatalf("the category door answered %s, want the tree created", statuses(a))
	}
	a = put("/api/v1/load/suppliers",
		`{"source_system":"wiredfx","batch_id":"b-w-4","items":[{"legacy_id":"FXV-W1","record":{"name":"Fixture Wired Supplier","payment_terms_days":30,"contact":{"contact_name":"Fixture Desk","email":"desk@example.invalid"}}}]}`)
	if statuses(a) != "FXV-W1=created;" {
		t.Fatalf("the supplier door answered %s, want created", statuses(a))
	}
	// The unit door: a fresh code created, the seeded PLT attached, and
	// the replay unchanged.
	a = put("/api/v1/load/units",
		`{"source_system":"wiredfx","batch_id":"b-w-5","items":[{"legacy_id":"WUN-1","record":{"code":"FXU-W1","name":"Fixture Wired Unit","dimension":"count"}},{"legacy_id":"WUN-2","record":{"code":"PLT","name":"Pallet","dimension":"count"}}]}`)
	if statuses(a) != "WUN-1=created;WUN-2=attached;" {
		t.Fatalf("the unit door answered %s, want created and attached", statuses(a))
	}
	a = put("/api/v1/load/units",
		`{"source_system":"wiredfx","batch_id":"b-w-5r","items":[{"legacy_id":"WUN-1","record":{"code":"FXU-W1","name":"Fixture Wired Unit","dimension":"count"}}]}`)
	if statuses(a) != "WUN-1=unchanged;" {
		t.Fatalf("the unit door replay answered %s, want unchanged", statuses(a))
	}
	// The unit set door: the product named by legacy key through the
	// registry, the set replacing whole, and the replay unchanged.
	if _, err := pool.Exec(ctx, `
		INSERT INTO products (id, sku, name, base_uom, price_uom, org_id)
		VALUES ($1::uuid, 'FX-SET-W1', 'Fixture Wired Set Product', 'EA', 'EA', $2::uuid)`,
		wiredSetProductID, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant set product: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
		VALUES ($1::uuid, 'wiredfx', 'product', 'WUP-1', $2::uuid, repeat('f', 64))`,
		testkit.OrgNorthID, wiredSetProductID); err != nil {
		t.Fatalf("plant set product name: %v", err)
	}
	a = put("/api/v1/load/products/unit-sets",
		`{"source_system":"wiredfx","batch_id":"b-w-6","items":[{"legacy_id":"WUS-1","record":{"rows":[{"code":"EA","qty_in_base":"1","is_base":true},{"code":"PC","qty_in_base":"1","is_base":false}]},"requires":[{"kind":"product","legacy_id":"WUP-1"}]}]}`)
	if statuses(a) != "WUS-1=created;" {
		t.Fatalf("the unit set door answered %s, want created", statuses(a))
	}
	a = put("/api/v1/load/products/unit-sets",
		`{"source_system":"wiredfx","batch_id":"b-w-6r","items":[{"legacy_id":"WUS-1","record":{"rows":[{"code":"EA","qty_in_base":"1","is_base":true},{"code":"PC","qty_in_base":"1","is_base":false}]},"requires":[{"kind":"product","legacy_id":"WUP-1"}]}]}`)
	if statuses(a) != "WUS-1=unchanged;" {
		t.Fatalf("the unit set door replay answered %s, want unchanged", statuses(a))
	}
	// A factor that arrives as a JSON number refuses at the door naming
	// factor_type: the factor is decimal text only, and the wire's float
	// re-spelling never touches it.
	a = put("/api/v1/load/products/unit-sets",
		`{"source_system":"wiredfx","batch_id":"b-w-6n","items":[{"legacy_id":"WUS-NUM","record":{"rows":[{"code":"EA","qty_in_base":"1","is_base":true},{"code":"PC","qty_in_base":2,"is_base":false}]},"requires":[{"kind":"product","legacy_id":"WUP-1"}]}]}`)
	if statuses(a) != "WUS-NUM=refused;" {
		t.Fatalf("the numeric factor answered %s, want refused", statuses(a))
	}
	if a.Items[0].Reason == nil || *a.Items[0].Reason != "factor_type" || a.Items[0].Field == nil || *a.Items[0].Field != "factor_type" {
		t.Fatalf("the numeric factor refused as %v on %v, want factor_type on factor_type", a.Items[0].Reason, a.Items[0].Field)
	}

	// LD-5's product master door: the category by legacy key (WC-2
	// loaded above), the base and price units by seeded catalogue code,
	// the 0018 attributes and the metadata landing; the replay
	// unchanged; a changed field updated.
	productBody := func(name string) string {
		return `{"source_system":"wiredfx","batch_id":"` + batchID + `","items":[{"legacy_id":"WUP-2","record":{"sku":"FX-W-1","name":"` + name + `","description":"Fixture Wired Product","base_uom":"EA","price_uom":"EA","color_family":"Grey","is_special_order":true,"weight_per_unit_kg":"18.5","metadata":{"fixture_note":"invented"}},"requires":[{"kind":"category","legacy_id":"WC-2"}]}]}`
	}
	batchID = "b-w-7"
	a = put("/api/v1/load/products", productBody("Fixture Wired Product"))
	if statuses(a) != "WUP-2=created;" {
		t.Fatalf("the product door answered %s, want created", statuses(a))
	}
	var wiredProductID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products WHERE sku = 'FX-W-1'`).Scan(&wiredProductID); err != nil {
		t.Fatalf("wired product read: %v", err)
	}
	var wiredCategory string
	if err := pool.QueryRow(ctx, `
		SELECT c.code FROM products p JOIN product_categories c ON c.id = p.category_id WHERE p.sku = 'FX-W-1'`).Scan(&wiredCategory); err != nil {
		t.Fatalf("wired product category read: %v", err)
	}
	if wiredCategory != "fxcat-w2" {
		t.Fatalf("the wired product's category is %q, want the row the legacy key WC-2 names", wiredCategory)
	}
	batchID = "b-w-7r"
	a = put("/api/v1/load/products", productBody("Fixture Wired Product"))
	if statuses(a) != "WUP-2=unchanged;" {
		t.Fatalf("the product door replay answered %s, want unchanged", statuses(a))
	}
	batchID = "b-w-7u"
	a = put("/api/v1/load/products", productBody("Fixture Wired Product Renamed"))
	if statuses(a) != "WUP-2=updated;" {
		t.Fatalf("the product door change answered %s, want updated", statuses(a))
	}

	// The image door: the bytes through the module's own store, the
	// product by legacy key; the identical replay unchanged.
	img := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)
	putImage := func(route, body, contentType string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, route, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Authorization", "Bearer "+right)
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	imageBody := func(hexed bool) string {
		if hexed {
			return img
		}
		return "\x89PNG\r\n\x1a\n" + strings.Repeat("\x01", 32)
	}
	imgRec := putImage("/api/v1/load/products/WUP-2/image?source_system=wiredfx", imageBody(true), "image/png")
	if imgRec.Code != http.StatusOK {
		t.Fatalf("the image door answered %d %q, want 200", imgRec.Code, imgRec.Body.String())
	}
	var imgOutcome struct {
		LegacyID string `json:"legacy_id"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(imgRec.Body.Bytes(), &imgOutcome); err != nil {
		t.Fatalf("image outcome body: %v", err)
	}
	if imgOutcome.LegacyID != "WUP-2" || imgOutcome.Status != "created" {
		t.Fatalf("the image door answered %+v, want created on WUP-2", imgOutcome)
	}
	var imgBytes int
	if err := pool.QueryRow(ctx,
		`SELECT byte_size FROM product_images WHERE product_id = $1::uuid AND sort_order = 0`, wiredProductID).Scan(&imgBytes); err != nil {
		t.Fatalf("wired image read: %v", err)
	}
	if imgBytes != len("\x89PNG\r\n\x1a\n")+32 {
		t.Fatalf("the stored image carries %d bytes, want the put's", imgBytes)
	}
	imgRec = putImage("/api/v1/load/products/WUP-2/image?source_system=wiredfx", imageBody(true), "image/png")
	if imgRec.Code != http.StatusOK {
		t.Fatalf("the image replay answered %d %q, want 200", imgRec.Code, imgRec.Body.String())
	}
	if err := json.Unmarshal(imgRec.Body.Bytes(), &imgOutcome); err != nil {
		t.Fatalf("image replay body: %v", err)
	}
	if imgOutcome.Status != "unchanged" {
		t.Fatalf("the image replay answered %+v, want unchanged", imgOutcome)
	}

	// The supplier SKU door: the product and the supplier each by
	// legacy key (FXV-W1 and the loaded product), the code and the pack
	// pair landing in the purchasing module's own table; the replay
	// unchanged.
	a = put("/api/v1/load/supplier-skus",
		`{"source_system":"wiredfx","batch_id":"b-w-8","items":[{"legacy_id":"WSK-1","record":{"vendor_sku":"FX-W-SKU","vendor_uom":"PLT","qty_per_vendor_uom":"60"},"requires":[{"kind":"supplier","legacy_id":"FXV-W1"},{"kind":"product","legacy_id":"WUP-2"}]}]}`)
	if statuses(a) != "WSK-1=created;" {
		t.Fatalf("the supplier sku door answered %s, want created", statuses(a))
	}
	var vendorSKU string
	if err := pool.QueryRow(ctx, `
		SELECT m.vendor_sku FROM purchasing_vendor_skus m
		JOIN vendors v ON v.id = m.vendor_id
		JOIN products p ON p.id = m.product_id
		WHERE v.vendor_no = 'FXV-W1' AND p.sku = 'FX-W-1'`).Scan(&vendorSKU); err != nil {
		t.Fatalf("wired vendor sku read: %v", err)
	}
	if vendorSKU != "FX-W-SKU" {
		t.Fatalf("the wired vendor sku is %q, want the record's", vendorSKU)
	}
	a = put("/api/v1/load/supplier-skus",
		`{"source_system":"wiredfx","batch_id":"b-w-8r","items":[{"legacy_id":"WSK-1","record":{"vendor_sku":"FX-W-SKU","vendor_uom":"PLT","qty_per_vendor_uom":"60"},"requires":[{"kind":"supplier","legacy_id":"FXV-W1"},{"kind":"product","legacy_id":"WUP-2"}]}]}`)
	if statuses(a) != "WSK-1=unchanged;" {
		t.Fatalf("the supplier sku door replay answered %s, want unchanged", statuses(a))
	}
}

// TestLoadProductReachesTheStaffSyncPullByRequest is exit line 6 by
// request (the review's P1-2): a product loaded through the real gate
// and wiring is found by pulling the staff sync route with a staff
// bearer of its org, and a staff bearer of another org (the kit's
// two-org subject acting on the south org) does not find it.
func TestLoadProductReachesTheStaffSyncPullByRequest(t *testing.T) {
	ctx := context.Background()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate machine key: %v", err)
	}
	srv := machineJWKSServer(t, key)
	env := newCompEnv(t)
	// The two-org subject's memberships: the fake's default wires one
	// team per subject, and this leg needs both orgs the fixture's grant
	// rows carry (the kit's SubTwoOrg holds a grant in each).
	env.appwrite.setTeam(testkit.SubTwoOrg, "org:"+testkit.OrgNorthSlug, "org:"+testkit.OrgSouthSlug)
	pool := env.fixturePool()
	if _, err := pool.Exec(ctx, `
		INSERT INTO machine_principal (id, org_id, name, enabled, doors)
		VALUES ($1::uuid, $2::uuid, 'fixture pull mover', TRUE, $3::text[])
		ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, doors = EXCLUDED.doors`,
		fixtureMoverRefID, testkit.OrgNorthID, []string{"loadUpsertProducts"}); err != nil {
		t.Fatalf("plant principal: %v", err)
	}
	clean := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid AND source_system = 'pullfx'`, testkit.OrgNorthID)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE entity_id IN (SELECT id FROM load_batch WHERE org_id = $1::uuid AND source_system = 'pullfx')`, testkit.OrgNorthID)
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = $1::uuid AND source_system = 'pullfx'`, testkit.OrgNorthID)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE sku = 'FX-PULL-1'`)
	}
	clean()
	t.Cleanup(func() {
		clean()
		_, _ = pool.Exec(ctx, `DELETE FROM machine_principal WHERE id = $1::uuid`, fixtureMoverRefID)
	})
	env.set("CORE_LOAD_DOORS", "on")
	env.set("CORE_MACHINE_AUDIENCE", fixtureMachineAudience)
	env.set("CORE_MACHINE_JWKS_URL", srv.URL)
	rec, _ := registeredSurface(t, env)
	mux := rec.inner
	right := machineBearer(t, env, key, fixtureMoverRefID)

	// The product loads through the real gate and wiring.
	resp := serveLoad(t, mux, http.MethodPut, "/api/v1/load/products", right, testkit.OrgNorthSlug,
		`{"source_system":"pullfx","batch_id":"b-pull-1","items":[{"legacy_id":"WPP-1","record":{"sku":"FX-PULL-1","name":"Fixture Pull Product","base_uom":"EA","price_uom":"EA"}}]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("the product put answered %d %q, want 200", resp.Code, resp.Body.String())
	}
	var landed struct {
		Items []struct {
			LegacyID string `json:"legacy_id"`
			Status   string `json:"status"`
			HHID     string `json:"hh_id"`
		}
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &landed); err != nil {
		t.Fatalf("product put body: %v", err)
	}
	if len(landed.Items) != 1 || landed.Items[0].Status != "created" || landed.Items[0].HHID == "" {
		t.Fatalf("the product put answered %+v, want created with its hh id", landed.Items)
	}
	productID := landed.Items[0].HHID

	// The staff sync route, pulled by request: a staff bearer of the
	// product's org finds it, a staff bearer of another org does not.
	pull := func(org string) (string, []syncEntity) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sync/products?since=0&limit=1000", nil)
		req.Header.Set("Authorization", "Bearer "+env.minter.StaffToken(t, testkit.SubTwoOrg))
		req.Header.Set(authz.HeaderXOrg, org)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("the %s pull answered %d %q, want 200", org, rr.Code, rr.Body.String())
		}
		var page struct {
			Items []syncEntity `json:"items"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
			t.Fatalf("the %s pull body: %v", org, err)
		}
		return org, page.Items
	}
	_, north := pull(testkit.OrgNorthSlug)
	found := false
	for _, it := range north {
		if it.Id == productID && it.Seq > 0 && it.Data["sku"] == "FX-PULL-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the loaded product %s is absent from its own org's staff sync pull: %d items", productID, len(north))
	}
	_, south := pull(testkit.OrgSouthSlug)
	for _, it := range south {
		if it.Id == productID {
			t.Fatalf("the loaded product appears in another org's staff sync pull")
		}
	}
}

// syncEntity is the sync pull's item shape over the wire, read locally
// so the leg asserts the served answer rather than a store call.
type syncEntity struct {
	Id   string         `json:"id"`
	Seq  int64          `json:"seq"`
	Data map[string]any `json:"data"`
}

// wiredSetProductID is the fixture product the unit set leg loads, the
// test's own row so the kit's catalogue product is never disturbed.
const wiredSetProductID = "7c2e6b41-0000-4000-8000-00000000d1f1"
