//go:build integration

// dib491_acceptcontrols_integration_test.go, DIB-491 (D-097) against a LIVE Postgres.
//
// This file proves the three claims the ruling actually makes, in the order it makes them:
//
//  1. RESTRICTIVE DEFAULT. With the switch off (or absent), a second acceptance, at the public
//     accept door AND at the conversion, is refused, atomically, with the first acceptance's
//     DOCUMENT named in the message.
//  2. PROVENANCE. With the switch on, repeat acceptance works, and EVERY order minted from the quote
//     carries a provenance row: which numbered acceptance, which door, who, written on the
//     conversion's own transaction.
//  3. LINEAGE. The related-documents read returns the same family from either end of the
//     quote→order link, branch-scoped like every other staff read.
//
// The DIB-393 guards this lane extends keep their own tests (share_integration_test.go), those run
// with the switch at its default and must stay green untouched; the compatibility is the point.
package quote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// setMultiAccept writes the dealer switch and restores the RESTRICTIVE default on cleanup, so no
// other test, in this package or any other sharing the database, ever observes a widened default
// it did not ask for.
func setMultiAccept(t *testing.T, pool *pgxpool.Pool, on bool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ($1, to_jsonb($2::boolean))
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		SettingAllowMultiAccept, on); err != nil {
		t.Fatalf("set %s=%v: %v", SettingAllowMultiAccept, on, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			INSERT INTO system_settings (key, value) VALUES ($1, 'false')
			ON CONFLICT (key) DO UPDATE SET value = 'false'`,
			SettingAllowMultiAccept)
	})
}

// familyOrders reads the quote's orders (id, order_no), oldest first.
func familyOrders(t *testing.T, pool *pgxpool.Pool, quoteID string) [][2]string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT id::text, order_no::text FROM orders WHERE quote_id=$1 ORDER BY created_at`, quoteID)
	if err != nil {
		t.Fatalf("read orders: %v", err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var id, no string
		if err := rows.Scan(&id, &no); err != nil {
			t.Fatalf("scan order: %v", err)
		}
		out = append(out, [2]string{id, no})
	}
	return out
}

// provenanceRows reads (accept_seq, origin, order_id, actor) for a quote, in sequence order.
type provRow struct {
	seq     int
	origin  string
	orderID string
	actorID string
}

func provenanceRows(t *testing.T, pool *pgxpool.Pool, quoteID string) []provRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT accept_seq, origin, order_id::text, COALESCE(actor_id::text,'')
		  FROM quote_accept_provenance WHERE quote_id=$1 ORDER BY accept_seq`, quoteID)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	defer rows.Close()
	var out []provRow
	for rows.Next() {
		var p provRow
		if err := rows.Scan(&p.seq, &p.origin, &p.orderID, &p.actorID); err != nil {
			t.Fatalf("scan provenance: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// - 1 · the restrictive default -

// TestIntegration_DIB491_FlagOff_DoubleAcceptRefusedWithTheDocumentNamed is the ruling's first half
// stated as behavior, at both doors, at both stages (accepted-not-converted, then converted).
//
// MUTATION ANCHOR (M1): invert the `!allow` check in AcceptQuoteGuarded or Convert and this dies -
// a second acceptance lands, a duplicate order is minted, or the refusal loses its document.
func TestIntegration_DIB491_FlagOff_DoubleAcceptRefusedWithTheDocumentNamed(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, false) // explicit, so the test states its own precondition

	svc, quoteID, token := mintedFor(t, pool, now, nil)
	var cust string
	if err := pool.QueryRow(context.Background(),
		`SELECT customer_id::text FROM quotes WHERE id=$1`, quoteID).Scan(&cust); err != nil {
		t.Fatalf("read customer: %v", err)
	}
	cleanupConvertedOrders(t, pool, quoteID)
	// Headroom, so the only refusals this test can see are DIB-491's own (the credit gate is
	// DIB-387's test's job, and a COD refusal here would shadow the guard under test).
	setCreditLimit(t, pool, cust, 1_000_00)

	// First acceptance, the ordinary path, untouched.
	if _, err := svc.AcceptByToken(context.Background(), token, "Dana Reyes", true); err != nil {
		t.Fatalf("first accept: %v", err)
	}

	// Second acceptance while ACCEPTED: refused, atomically, with the STATE and the way forward.
	var already *AlreadyAcceptedError
	_, err := svc.AcceptByToken(context.Background(), token, "Dana Reyes", true)
	if !errors.As(err, &already) {
		t.Fatalf("re-accepting an ACCEPTED quote with the switch off must return AlreadyAcceptedError; got %v", err)
	}
	if already.HasOrder {
		t.Error("no order exists yet, the refusal must not invent one")
	}
	if !strings.Contains(err.Error(), "ACCEPTED") {
		t.Errorf("the refusal must name the state; got %q", err.Error())
	}
	if n := acceptanceRows(t, pool, quoteID); n != 1 {
		t.Fatalf("the refusal must be atomic, %d acceptance rows, want 1", n)
	}

	// Convert once (legitimate), then try both doors again.
	res, err := NewRepository(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	// The accept door names the ORDER now.
	_, err = svc.AcceptByToken(context.Background(), token, "Dana Reyes", true)
	if !errors.As(err, &already) {
		t.Fatalf("re-accepting a CONVERTED quote must return AlreadyAcceptedError; got %v", err)
	}
	if !already.HasOrder || already.OrderNo != res.OrderNo {
		t.Errorf("the refusal must name the order the acceptance became (%s); got %+v", res.OrderNo, already)
	}
	for _, want := range []string{res.OrderNo, "CONVERTED"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q must contain %q", err.Error(), want)
		}
	}

	// The convert door refuses a duplicate the same way.
	_, err = NewRepository(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{})
	if !errors.As(err, &already) {
		t.Fatalf("re-converting with the switch off must return AlreadyAcceptedError; got %v", err)
	}
	if orders := familyOrders(t, pool, quoteID); len(orders) != 1 {
		t.Fatalf("a refused re-convert must mint nothing, %d orders, want 1", len(orders))
	}
	// And the portal's own Accept answers the document too, not a bare invalid-transition.
	_, err = NewRepository(pool).Accept(context.Background(), cust, quoteID)
	if !errors.As(err, &already) {
		t.Fatalf("portal Accept on a converted quote must return AlreadyAcceptedError; got %v", err)
	}
}

// - 2 · provenance on every mint -

// TestIntegration_DIB491_FlagOn_EveryAcceptCarriesProvenance: the switch on, a repeat acceptance and
// a duplicate order, and every mint numbered, doored and attributed, written on the conversion's
// own transaction.
//
// MUTATION ANCHOR (M2): remove the recordAcceptProvenanceTx call from Convert and this dies on the
// empty provenance table.
func TestIntegration_DIB491_FlagOn_EveryAcceptCarriesProvenance(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, true)

	svc, quoteID, token := mintedFor(t, pool, now, nil)
	var cust string
	if err := pool.QueryRow(context.Background(),
		`SELECT customer_id::text FROM quotes WHERE id=$1`, quoteID).Scan(&cust); err != nil {
		t.Fatalf("read customer: %v", err)
	}
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 1_000_00)
	repo := NewRepository(pool)

	// Accept + convert, the PORTAL door (no staff context on the ctx).
	if _, err := svc.AcceptByToken(context.Background(), token, "Dana Reyes", true); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if _, err := repo.Convert(context.Background(), cust, quoteID, ConvertOptions{}); err != nil {
		t.Fatalf("first convert: %v", err)
	}

	// The REPEAT acceptance: allowed, honest about being a repeat, and status is NOT dragged back.
	res, err := svc.AcceptByToken(context.Background(), token, "Dana Reyes", true)
	if err != nil {
		t.Fatalf("repeat accept with the switch on: %v", err)
	}
	if !res.Repeat {
		t.Error("a repeat acceptance must say so (repeat_acceptance), C2 honesty")
	}
	if n := acceptanceRows(t, pool, quoteID); n != 2 {
		t.Fatalf("every accept records, %d acceptance rows, want 2", n)
	}
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM quotes WHERE id=$1`, quoteID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "CONVERTED" {
		t.Fatalf("a repeat acceptance must not drag CONVERTED back; status is %q", status)
	}
	// The trail records the TRUE (non-)transition, not an invented SENT→ACCEPTED.
	var before, after string
	if err := pool.QueryRow(context.Background(), `
		SELECT changes->>'before', changes->>'after' FROM audit_log
		 WHERE entity_id=$1 AND action='QUOTE_ACCEPTED' ORDER BY seq DESC LIMIT 1`, quoteID).
		Scan(&before, &after); err != nil {
		t.Fatalf("read acceptance trail: %v", err)
	}
	if before != "CONVERTED" || after != "CONVERTED" {
		t.Errorf("the repeat acceptance's trail must read CONVERTED→CONVERTED; got %s→%s", before, after)
	}

	// The duplicate order, the COUNTER door this time (a staff context on the ctx).
	staffCtx := identity.ContextWithStaff(context.Background(), intStaff(intTrenton))
	res2, err := repo.Convert(staffCtx, cust, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("re-convert with the switch on: %v", err)
	}

	orders := familyOrders(t, pool, quoteID)
	if len(orders) != 2 {
		t.Fatalf("multi-accept must mint a SECOND order, got %d", len(orders))
	}
	if orders[0][1] == orders[1][1] {
		t.Fatalf("the duplicate must draw its own order number; both are %s", orders[0][1])
	}

	prov := provenanceRows(t, pool, quoteID)
	if len(prov) != 2 {
		t.Fatalf("every mint carries provenance, %d rows, want 2", len(prov))
	}
	if prov[0].seq != 1 || prov[1].seq != 2 {
		t.Errorf("acceptances must be numbered 1,2; got %d,%d", prov[0].seq, prov[1].seq)
	}
	if prov[0].origin != OriginPortal {
		t.Errorf("the first mint came through the portal door; origin = %q", prov[0].origin)
	}
	if prov[1].origin != OriginCounter {
		t.Errorf("the second mint came through the counter door; origin = %q", prov[1].origin)
	}
	if prov[1].actorID != intStaff(intTrenton).UserID {
		t.Errorf("the counter mint must be attributed to the staff actor; got %q", prov[1].actorID)
	}
	if prov[1].orderID != res2.OrderID {
		t.Errorf("provenance row 2 must point at the order it explains (%s); got %s", res2.OrderID, prov[1].orderID)
	}
}

// TestIntegration_DIB491_FlagOn_TheShareSurfaceFollows: with the switch on, a CONVERTED quote can be
// shared again and its public view offers acceptance again; with it off, both stay shut. The display
// halves of the feature, proven against the same live switch the write halves read.
func TestIntegration_DIB491_FlagOn_TheShareSurfaceFollows(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "CONVERTED", nil)
	cleanupShares(t, pool, quoteID)
	svc := shareSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))

	setMultiAccept(t, pool, false)
	var notEditable *NotEditableError
	if _, err := svc.MintShareLink(ctx, quoteID, 0); !errors.As(err, &notEditable) {
		t.Fatalf("switch off: minting a link for a CONVERTED quote must refuse (NotEditableError); got %v", err)
	}

	setMultiAccept(t, pool, true)
	minted, err := svc.MintShareLink(ctx, quoteID, 0)
	if err != nil {
		t.Fatalf("switch on: minting a link for a CONVERTED quote must work; got %v", err)
	}
	view, err := svc.PublicQuoteByToken(context.Background(), minted.Token)
	if err != nil {
		t.Fatalf("public view: %v", err)
	}
	if !view.Acceptable {
		t.Error("switch on: the public view must offer acceptance again")
	}

	setMultiAccept(t, pool, false)
	view, err = svc.PublicQuoteByToken(context.Background(), minted.Token)
	if err != nil {
		t.Fatalf("public view: %v", err)
	}
	if view.Acceptable {
		t.Error("switch off: the accept form must not be offered for a CONVERTED quote")
	}
}

// - 3 · lineage -

// seedInvoiceFor writes a minimal invoice against an order (0009), with cleanup.
func seedInvoiceFor(t *testing.T, pool *pgxpool.Pool, cust, orderID string) (invoiceID, invoiceNo string) {
	t.Helper()
	tag := fmt.Sprintf("D491-%d", time.Now().UnixNano())
	invoiceNo = "#INV-" + tag
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, due_date, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2::uuid, $3::uuid, $4::uuid, '2026-09-30', 2000, 0, 2000)
		RETURNING id::text`, invoiceNo, cust, orderID, intTrenton).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM invoices WHERE id = $1`, invoiceID)
	})
	return invoiceID, invoiceNo
}

// TestIntegration_DIB491_Lineage_SameFamilyFromEitherEnd builds the full family, quote, two orders
// through two doors, an invoice, and asserts the related-documents read returns it whole, correctly
// parent-linked and provenance-annotated, whether asked from the quote or from an order (by its
// VISIBLE number, because that is what is on the paperwork).
//
// MUTATION ANCHOR (M3): break any leg of the traversal (orders WHERE, invoice IN, provenance JOIN)
// and a row goes missing or loses its parent here.
func TestIntegration_DIB491_Lineage_SameFamilyFromEitherEnd(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	setMultiAccept(t, pool, true)

	svc, quoteID, token := mintedFor(t, pool, now, nil)
	var cust, quoteNo string
	if err := pool.QueryRow(context.Background(),
		`SELECT customer_id::text, quote_no::text FROM quotes WHERE id=$1`, quoteID).
		Scan(&cust, &quoteNo); err != nil {
		t.Fatalf("read quote: %v", err)
	}
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 1_000_00)
	repo := NewRepository(pool)

	if _, err := svc.AcceptByToken(context.Background(), token, "Dana Reyes", true); err != nil {
		t.Fatalf("accept: %v", err)
	}
	res1, err := repo.Convert(context.Background(), cust, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert 1: %v", err)
	}
	res2, err := repo.Convert(identity.ContextWithStaff(context.Background(), intStaff(intTrenton)),
		cust, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert 2: %v", err)
	}
	_, invoiceNo := seedInvoiceFor(t, pool, cust, res1.OrderID)

	ctx := qCtxAs(intStaff(intTrenton))

	byKind := func(rows []RelatedDocument) map[string][]RelatedDocument {
		m := map[string][]RelatedDocument{}
		for _, r := range rows {
			m[r.Kind] = append(m[r.Kind], r)
		}
		return m
	}

	// From the quote.
	fam, err := svc.RelatedDocumentsForQuote(ctx, quoteID)
	if err != nil {
		t.Fatalf("family from quote: %v", err)
	}
	m := byKind(fam)
	if len(m[DocKindQuote]) != 1 || len(m[DocKindOrder]) != 2 || len(m[DocKindInvoice]) != 1 {
		t.Fatalf("family from quote = %d quote / %d order / %d invoice, want 1/2/1 (%+v)",
			len(m[DocKindQuote]), len(m[DocKindOrder]), len(m[DocKindInvoice]), fam)
	}
	if q := m[DocKindQuote][0]; !q.Self || q.Number != quoteNo {
		t.Errorf("the quote row must be self and numbered %s; got %+v", quoteNo, q)
	}
	for i, o := range m[DocKindOrder] {
		if o.ParentID != quoteID {
			t.Errorf("order %d must be parent-linked to the quote; got %q", i, o.ParentID)
		}
	}
	if o := m[DocKindOrder][0]; o.Origin != OriginPortal || o.AcceptSeq != 1 || o.Number != res1.OrderNo {
		t.Errorf("order 1 must carry (PORTAL, seq 1, %s); got %+v", res1.OrderNo, o)
	}
	if o := m[DocKindOrder][1]; o.Origin != OriginCounter || o.AcceptSeq != 2 || o.Number != res2.OrderNo {
		t.Errorf("order 2 must carry (COUNTER, seq 2, %s); got %+v", res2.OrderNo, o)
	}
	if inv := m[DocKindInvoice][0]; inv.ParentID != res1.OrderID || inv.Number != invoiceNo {
		t.Errorf("the invoice must be parent-linked to order 1 (%s); got %+v", res1.OrderID, inv)
	}

	// From an order, by its VISIBLE number, the SAME family, self moved.
	fam2, err := svc.RelatedDocumentsForOrder(ctx, res2.OrderNo)
	if err != nil {
		t.Fatalf("family from order: %v", err)
	}
	if len(fam2) != len(fam) {
		t.Fatalf("the family must be the same from either end: %d vs %d rows", len(fam2), len(fam))
	}
	var self []string
	for _, r := range fam2 {
		if r.Self {
			self = append(self, r.ID)
		}
	}
	if len(self) != 1 || self[0] != res2.OrderID {
		t.Errorf("asked from order 2, exactly that row is self; got %v", self)
	}
}

// TestIntegration_DIB491_Lineage_ScopeAndTheQuoteless: the read is branch-scoped with no existence
// oracle, and an order with no quote behind it answers itself rather than erroring.
func TestIntegration_DIB491_Lineage_ScopeAndTheQuoteless(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	_, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	cleanupShares(t, pool, quoteID)
	svc, _, _ := intSvc(t, pool, now)

	// A Kingston-only caller cannot see a Trenton quote's family. By raw UUID the answer is 403 -
	// exactly StaffQuoteRead's posture (ResolveQuoteID passes a UUID through; the branch check
	// refuses). The no-oracle 404 leg is the by-NUMBER path, which the order half below proves.
	if _, err := svc.RelatedDocumentsForQuote(qCtxAs(intStaff(intKingston)), quoteID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-branch family read by id must be ErrForbidden (the StaffQuoteRead posture); got %v", err)
	}

	// A quote-less order (a counter sale) has a one-member family plus downstream documents.
	var cust string
	if err := pool.QueryRow(context.Background(),
		`SELECT customer_id::text FROM quotes WHERE id=$1`, quoteID).Scan(&cust); err != nil {
		t.Fatalf("read customer: %v", err)
	}
	tag := fmt.Sprintf("D491-%d", time.Now().UnixNano())
	var orderID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO orders (order_no, customer_id, branch_id, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2::uuid, $3::uuid, 2000, 0, 2000) RETURNING id::text`,
		"#SO-"+tag, cust, intTrenton).Scan(&orderID); err != nil {
		t.Fatalf("seed bare order: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, orderID)
	})

	fam, err := svc.RelatedDocumentsForOrder(qCtxAs(intStaff(intTrenton)), orderID)
	if err != nil {
		t.Fatalf("quote-less family: %v", err)
	}
	if len(fam) != 1 || fam[0].Kind != DocKindOrder || !fam[0].Self {
		t.Fatalf("a quote-less order answers itself alone; got %+v", fam)
	}
	// And the Kingston caller cannot reach it either.
	if _, err := svc.RelatedDocumentsForOrder(qCtxAs(intStaff(intKingston)), orderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-branch order family must be ErrNotFound; got %v", err)
	}
}

// - the settings row itself -

// TestIntegration_DIB491_Setting_DefaultAndRoundTrip: an ABSENT key reads as false (the restrictive
// default survives a database that never ran 0162's seed), the write round-trips, and the flip lands
// on the audit trail in the house shape.
//
// MUTATION ANCHOR (M5): change allowMultiAcceptSQL's COALESCE fallback to true and the absent-key
// assertion here dies.
func TestIntegration_DIB491_Setting_DefaultAndRoundTrip(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	// Absent key = false. (setMultiAccept's cleanup restores the seeded row afterwards.)
	setMultiAccept(t, pool, false)
	if _, err := pool.Exec(ctx,
		`DELETE FROM system_settings WHERE key = $1`, SettingAllowMultiAccept); err != nil {
		t.Fatalf("remove setting: %v", err)
	}
	on, err := repo.AllowMultiAccept(ctx)
	if err != nil {
		t.Fatalf("read absent setting: %v", err)
	}
	if on {
		t.Fatal("an ABSENT setting must read as OFF, the restrictive default (D-097)")
	}

	// The admin surface round-trips and audits.
	svc, _, _ := intSvc(t, pool, time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	admin := identity.StaffContext{
		// CP0-5 answer A: the General Manager saves the acceptance settings.
		UserID: seedUserID, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
		Status: identity.StatusActive,
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE action = $1`, ActMultiAcceptToggled)
	})
	got, err := svc.SetAllowMultiAccept(qCtxAs(admin), true, "integration round-trip")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !got.AllowMultiAccept || !got.SwitchWired {
		t.Fatalf("the write must land and report the seam wired; got %+v", got)
	}
	if on, err = repo.AllowMultiAccept(ctx); err != nil || !on {
		t.Fatalf("read-after-write = (%v, %v), want (true, nil)", on, err)
	}
	var before, after, field string
	if err := pool.QueryRow(ctx, `
		SELECT changes->>'field', changes->>'before', changes->>'after'
		  FROM audit_log WHERE action = $1 ORDER BY seq DESC LIMIT 1`, ActMultiAcceptToggled).
		Scan(&field, &before, &after); err != nil {
		t.Fatalf("the flip must land on the audit trail: %v", err)
	}
	if field != SettingAllowMultiAccept || before != "off" || after != "on" {
		t.Errorf("trail = {%s, %s→%s}, want {%s, off→on}", field, before, after, SettingAllowMultiAccept)
	}
}
