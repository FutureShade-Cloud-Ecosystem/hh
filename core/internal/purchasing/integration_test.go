//go:build integration

// integration_test.go, the purchasing module against a real database, with the REAL internal/inventory,
// internal/gl and internal/landedcost services behind the seams. Nothing here fakes the modules whose
// correctness the acceptance depends on; the only fake in the file is a stock seam that deliberately fails,
// used to prove receiving rolls back.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5432/swarm4_c?sslmode=disable' \
//	  go test -tags=integration -count=1 ./internal/purchasing/...
//
// FIXTURE DISCIPLINE. Every test creates its own products (SKU prefixed with the run's nanosecond) and its
// own PO, and deletes what it created in reverse dependency order on cleanup. Nothing here transiently breaks
// a whole-DB invariant: receiving goes through inventory in ONE transaction, so a stock move and its balance
// projection become visible together and the DIB-396 stock-drift gates never see a gap. No lease is taken,
// and none is needed, stated explicitly because "inventory swarms take note" applies to anything that
// writes stock, and this module's answer is that it writes none.
package purchasing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/landedcost"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	branchTRT = "5d1b3f6e-0000-4000-9000-000000000001"
	branchKGN = "5d1b3f6e-0000-4000-9000-000000000002"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@127.0.0.1:5432/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// - the real seam adapters (mirroring cmd/server/main.go's SWARM-W4-C block) -

type itStock struct{ svc *inventory.Service }

func (a itStock) ReceiveDocumentTx(ctx context.Context, tx pgx.Tx, docID string) error {
	return a.svc.ReceivePOTx(ctx, tx, docID)
}

type itVendors struct{ pool *pgxpool.Pool }

func (a itVendors) ListVendors(ctx context.Context) ([]Vendor, error) {
	rows, err := a.pool.Query(ctx, `SELECT id::text, vendor_no::text, name, payment_terms_days, is_active,
		is_t5018_subcontractor, COALESCE(business_number, '') FROM vendors ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Vendor
	for rows.Next() {
		var v Vendor
		if err := rows.Scan(&v.ID, &v.VendorNo, &v.Name, &v.PaymentTermsDays, &v.IsActive,
			&v.IsT5018Subcontractor, &v.BusinessNumber); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (a itVendors) Vendor(ctx context.Context, id string) (Vendor, error) {
	var v Vendor
	err := a.pool.QueryRow(ctx, `SELECT id::text, vendor_no::text, name, payment_terms_days, is_active,
		is_t5018_subcontractor, COALESCE(business_number, '') FROM vendors WHERE id = $1`, id).
		Scan(&v.ID, &v.VendorNo, &v.Name, &v.PaymentTermsDays, &v.IsActive,
			&v.IsT5018Subcontractor, &v.BusinessNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return Vendor{}, ErrUnknownVendor
	}
	return v, err
}

type itLanded struct{ svc *landedcost.Service }

func (a itLanded) CreateAndApply(ctx context.Context, in LandedCostDraft) (string, error) {
	id, err := a.svc.Create(ctx, landedcost.CreateInput{
		POID: in.POID, PORef: in.PORef, BranchID: in.BranchID, Currency: in.Currency, FXRate: in.FXRate,
		FreightCents: in.FreightCents, DutyCents: in.DutyCents, HandlingCents: in.HandlingCents,
		AllocationBasis: in.Basis,
	})
	if err != nil {
		return "", err
	}
	if err := a.svc.Apply(ctx, id); err != nil {
		return "", err
	}
	return id, nil
}

func (a itLanded) OpenPayablesAsOf(ctx context.Context, asOf time.Time, branchIDs []string, global bool) ([]LandedCostPayable, error) {
	rows, err := a.svc.OpenPayablesAsOf(ctx, asOf, branchIDs, global)
	if err != nil {
		return nil, err
	}
	out := make([]LandedCostPayable, len(rows))
	for i, r := range rows {
		out[i] = LandedCostPayable{
			ID:               r.ID,
			POID:             r.POID,
			PORef:            r.PORef,
			BranchID:         r.BranchID,
			Currency:         r.Currency,
			FreightCadCents:  r.FreightCadCents,
			DutyCadCents:     r.DutyCadCents,
			HandlingCadCents: r.HandlingCadCents,
			TotalCadCents:    r.TotalCadCents,
			ChargeKind:       r.ChargeKind,
			PayeeLabel:       r.PayeeLabel,
			CreatedAt:        r.CreatedAt,
			UpdatedAt:        r.UpdatedAt,
		}
	}
	return out, nil
}

type itGL struct{ svc *gl.Service }

// itGL mirrors main.go's w4cGLPoster field for field, the document date included, so the legs read
// the same filing the production wiring gives a posted bill.
func (a itGL) PostJournal(ctx context.Context, in GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines, DocumentDate: in.DocumentDate,
	})
}

// itGLClock mirrors itGL for reads: the GL service's own as-of filing rule behind
// purchasing's GLJournalClock seam, so the aging's inclusion instants are the
// journals' effective instants exactly as the production wiring answers them.
type itGLClock struct{ svc *gl.Service }

func (a itGLClock) JournalEffectiveInstants(ctx context.Context, journalIDs []string) (map[string]time.Time, error) {
	return a.svc.JournalEffectiveInstants(ctx, journalIDs)
}

func (a itGLClock) RefJournalInstants(ctx context.Context, source string, refIDs []string) ([]GLRefJournalInstant, error) {
	entries, err := a.svc.RefJournalInstants(ctx, source, refIDs)
	if err != nil {
		return nil, err
	}
	out := make([]GLRefJournalInstant, 0)
	for _, refs := range entries {
		for _, e := range refs {
			out = append(out, GLRefJournalInstant{RefID: e.SourceRefID, PostedAt: e.PostedAt, EffectiveAt: e.EffectiveAt})
		}
	}
	return out, nil
}

func (a itGLClock) JournalSourceRefByKey(ctx context.Context, key string) (string, bool, error) {
	return a.svc.JournalSourceRefByKey(ctx, key)
}

func (a itGLClock) JournalAmountByKey(ctx context.Context, key string) (int64, bool, error) {
	return a.svc.JournalAmountByKey(ctx, key)
}

// itGLWindow mirrors itGLClock for the posting horizon: the GL service's own answer to whether
// a posting dated on an instant would file, the rule the pay date is judged against.
type itGLWindow struct{ svc *gl.Service }

func (a itGLWindow) PostingDateRefusal(ctx context.Context, at time.Time) (string, error) {
	return a.svc.PostingDateRefusal(ctx, at)
}

// itGLJournalReader mirrors the production wiring's posted journal read by idempotency key,
// so the AP revaluation's replay judgement and reversal read the journal the run filed
// exactly as w4cGLJournalReader answers it.
type itGLJournalReader struct{ svc *gl.Service }

func (a itGLJournalReader) JournalByKey(ctx context.Context, key string) (GLPostedJournal, bool, error) {
	j, found, err := a.svc.JournalByKey(ctx, key)
	if err != nil || !found {
		return GLPostedJournal{}, found, err
	}
	out := GLPostedJournal{ID: j.ID, SourceRefID: j.SourceRefID, BranchID: j.BranchID, Memo: j.Memo,
		Lines: make([]GLJournalLeg, len(j.Lines))}
	for i, l := range j.Lines {
		out.Lines[i] = GLJournalLeg{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return out, true, nil
}

func (a itGL) PostJournalTx(ctx context.Context, tx pgx.Tx, in GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternalTx(ctx, tx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines, DocumentDate: in.DocumentDate,
	})
}

type itRules struct{ svc *gl.Service }

func (a itRules) ResolveRuleTx(ctx context.Context, tx pgx.Tx, e, r, b string) (string, error) {
	return a.svc.ResolveRuleTx(ctx, tx, e, r, b)
}

func (a itRules) ResolveRule(ctx context.Context, e, r, b string) (string, error) {
	return a.svc.ResolveRule(ctx, e, r, b)
}

// failingStock is the ONLY fake in this file: a stock seam that refuses, used to prove the receiving
// transaction rolls back around a seam failure.
type failingStock struct{ err error }

func (f failingStock) ReceiveDocumentTx(context.Context, pgx.Tx, string) error { return f.err }

// - harness -

type harness struct {
	pool   *pgxpool.Pool
	svc    *Service
	inv    *inventory.Service
	gl     *gl.Service
	landed *landedcost.Service
	vendor string
	user   string
	ctx    context.Context
}

// globalCaller is a GLOBAL leadership context, so the branch fence is satisfied for either branch. The fence
// itself is exercised separately by the scoped-caller test.
func globalCaller(userID string) identity.StaffContext {
	return identity.StaffContext{UserID: userID, RoleCode: "GENERAL_MANAGER",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}

// ctxCaller is the withCaller seam for a GLOBAL caller.
func ctxCaller(userID string) func(context.Context) (identity.StaffContext, bool) {
	return func(context.Context) (identity.StaffContext, bool) { return globalCaller(userID), true }
}

// scopedCaller is the withCaller seam for a caller granted exactly one branch, the horizontal-access fence.
func scopedCaller(userID, branchID string) func(context.Context) (identity.StaffContext, bool) {
	c := identity.StaffContext{UserID: userID, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{branchID: true}}
	return func(context.Context) (identity.StaffContext, bool) { return c, true }
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()

	var user string //nolint:gocritic // resolved below, then stamped onto ctx
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&user); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	var vendor string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE is_active LIMIT 1`).Scan(&vendor); err != nil {
		t.Skipf("no seeded vendor: %v", err)
	}

	// The SIBLING services resolve their own caller from the context exactly as they do behind the identity
	// guards (landedcost.Apply is branch-scoped). purchasing's own caller comes from the withCaller seam, but
	// the context has to carry a real StaffContext too or the seam call downstream is unauthenticated -
	// which is precisely what happens in production if a purchasing route is ever called off-request.
	ctx = identity.ContextWithStaff(ctx, globalCaller(user))

	glSvc := gl.NewService(gl.NewRepository(pool))
	invSvc := inventory.NewService(inventory.NewRepository(pool)).
		WithGLPoster(invGLBridge{svc: glSvc}).
		WithRuleResolver(invRuleBridge{svc: glSvc})
	// The landed cost journal clock is wired as production wires it (wire_operations), so a
	// landed cost payable counts from its own journal's effective instant, never from the row's
	// timestamps: those are the database's clock, which the evening guard's leg shifts apart
	// from the test's own clock (the W29A Sub2 red under the guard).
	landedSvc := landedcost.NewService(landedcost.NewRepository(pool)).
		WithCapitalizer(landedBridge{svc: invSvc}).
		WithGLPoster(landedGLBridge{svc: glSvc}).
		WithJournalClock(itLandedClock{svc: glSvc})

	svc := NewService(NewRepository(pool)).
		WithStockReceiver(itStock{svc: invSvc}).
		WithVendors(itVendors{pool: pool}).
		WithLandedCoster(itLanded{svc: landedSvc}).
		WithLandedCostPayables(itLanded{svc: landedSvc}).
		WithGLPoster(itGL{svc: glSvc}).
		WithRuleResolver(itRules{svc: glSvc}).
		WithGLJournalClock(itGLClock{svc: glSvc}).
		WithGLJournalReader(itGLJournalReader{svc: glSvc}).
		WithGLPostingWindow(itGLWindow{svc: glSvc}).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return globalCaller(user), true })

	return &harness{pool: pool, svc: svc, inv: invSvc, gl: glSvc, landed: landedSvc, vendor: vendor, user: user, ctx: ctx}
}

// newProduct creates a throwaway product and registers its cleanup. Its own SKU means a test never competes
// with another package's fixture for the same weighted-average cost row.
func (h *harness) newProduct(t *testing.T, suffix string) string {
	t.Helper()
	sku := fmt.Sprintf("W4C-%d-%s", time.Now().UnixNano(), suffix)
	var id string
	err := h.pool.QueryRow(h.ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom, is_active)
		VALUES ($1, $2, 'EA', 'EA', TRUE) RETURNING id::text`, sku, sku).Scan(&id)
	if err != nil {
		t.Fatalf("create product %s: %v", sku, err)
	}
	t.Cleanup(func() { h.cleanupProduct(id) })
	return id
}

// cleanupProduct removes the product and everything the receiving path hung off it, in dependency order.
// Restoring what a fixture touched is the rule; the stock rows go with the product so no drift is left behind.
func (h *harness) cleanupProduct(productID string) {
	ctx := context.Background()
	stmts := []string{
		`DELETE FROM stock_moves WHERE product_id = $1`,
		`DELETE FROM stock_balances WHERE product_id = $1`,
		`DELETE FROM product_branch_costs WHERE product_id = $1`,
		`DELETE FROM landed_cost_allocations WHERE product_id = $1`,
		`DELETE FROM purchasing_receipt_lines rl USING purchasing_po_lines l
		  WHERE rl.po_line_id = l.id AND l.product_id = $1`,
		`DELETE FROM purchasing_bill_lines bl USING purchasing_po_lines l
		  WHERE bl.po_line_id = l.id AND l.product_id = $1`,
		`DELETE FROM po_lines WHERE product_id = $1`,
		`DELETE FROM purchasing_po_lines WHERE product_id = $1`,
		`DELETE FROM products WHERE id = $1`,
	}
	for _, s := range stmts {
		_, _ = h.pool.Exec(ctx, s, productID)
	}
}

// cleanupPO removes a PO and its documents. Called after the product cleanup registration so it runs FIRST
// (t.Cleanup is LIFO), the PO's lines must go before the product they reference.
func (h *harness) cleanupPO(t *testing.T, poID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		var docIDs []string
		rows, err := h.pool.Query(ctx, `SELECT stock_doc_id::text FROM purchasing_receipts WHERE po_id = $1 AND stock_doc_id IS NOT NULL`, poID)
		if err == nil {
			for rows.Next() {
				var d string
				if rows.Scan(&d) == nil {
					docIDs = append(docIDs, d)
				}
			}
			rows.Close()
		}
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_bill_instalments i USING purchasing_bills b WHERE i.bill_id = b.id AND b.po_id = $1`, poID)
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_bill_lines bl USING purchasing_bills b WHERE bl.bill_id = b.id AND b.po_id = $1`, poID)
		_, _ = h.pool.Exec(ctx, `DELETE FROM gl_journal_lines jl USING gl_journals j, purchasing_bills b
			WHERE jl.journal_id = j.id AND j.id = b.gl_journal_id AND b.po_id = $1`, poID)
		_, _ = h.pool.Exec(ctx, `UPDATE purchasing_bills SET gl_journal_id = NULL WHERE po_id = $1`, poID)
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_bills WHERE po_id = $1`, poID)
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_receipt_lines rl USING purchasing_receipts rc WHERE rl.receipt_id = rc.id AND rc.po_id = $1`, poID)
		for _, d := range docIDs {
			_, _ = h.pool.Exec(ctx, `DELETE FROM landed_cost_allocations a USING landed_costs lc WHERE a.landed_cost_id = lc.id AND lc.po_id = $1`, d)
			_, _ = h.pool.Exec(ctx, `UPDATE purchasing_receipts SET landed_cost_id = NULL WHERE stock_doc_id = $1`, d)
			_, _ = h.pool.Exec(ctx, `DELETE FROM landed_costs WHERE po_id = $1`, d)
		}
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_receipts WHERE po_id = $1`, poID)
		for _, d := range docIDs {
			_, _ = h.pool.Exec(ctx, `DELETE FROM po_receipts WHERE po_id = $1`, d)
			_, _ = h.pool.Exec(ctx, `DELETE FROM po_lines WHERE po_id = $1`, d)
			_, _ = h.pool.Exec(ctx, `DELETE FROM purchase_orders WHERE id = $1`, d)
		}
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_po_lines WHERE po_id = $1`, poID)
		_, _ = h.pool.Exec(ctx, `DELETE FROM purchasing_pos WHERE id = $1`, poID)
	})
}

// orderedPO drafts a PO with one line and places it.
func (h *harness) orderedPO(t *testing.T, productID string, qty float64, unitCost int64) PODetail {
	t.Helper()
	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT, ExpectedAt: "2026-09-01",
		Lines: []AddLineParams{{ProductID: productID, QtyOrderedBase: qty, UOM: "EA", UnitCostCents: unitCost}},
	})
	if err != nil {
		t.Fatalf("create PO: %v", err)
	}
	h.cleanupPO(t, po.ID)
	placed, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered)
	if err != nil {
		t.Fatalf("order PO: %v", err)
	}
	return placed
}

func (h *harness) onHand(t *testing.T, productID, branchID string) float64 {
	t.Helper()
	var q float64
	if err := h.pool.QueryRow(h.ctx,
		`SELECT COALESCE(sum(on_hand_base),0) FROM stock_balances WHERE product_id = $1 AND branch_id = $2`,
		productID, branchID).Scan(&q); err != nil {
		t.Fatalf("on-hand: %v", err)
	}
	return q
}

// - inventory/landedcost bridge adapters (mirror main.go's existing ones) -

type invGLBridge struct{ svc *gl.Service }

func (a invGLBridge) PostJournal(ctx context.Context, in inventory.GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines,
	})
}

type invRuleBridge struct{ svc *gl.Service }

func (a invRuleBridge) ResolveRule(ctx context.Context, e, r, b string) (string, error) {
	return a.svc.ResolveRule(ctx, e, r, b)
}

type landedGLBridge struct{ svc *gl.Service }

func (a landedGLBridge) PostJournal(ctx context.Context, in landedcost.GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines,
	})
}

// PostJournalTx is PostJournal through the caller's transaction (gl.Service.PostExternalTx), the tx-aware
// seam variant landedcost Void posts its reversing journal with.
func (a landedGLBridge) PostJournalTx(ctx context.Context, tx pgx.Tx, in landedcost.GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternalTx(ctx, tx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines,
	})
}

type landedBridge struct{ svc *inventory.Service }

func (a landedBridge) ReceivedLinesForPO(ctx context.Context, poID string) ([]landedcost.ReceivedLine, error) {
	lines, err := a.svc.ReceivedLinesForPO(ctx, poID)
	if err != nil {
		return nil, err
	}
	out := make([]landedcost.ReceivedLine, len(lines))
	for i, l := range lines {
		out[i] = landedcost.ReceivedLine{POLineID: l.POLineID, ProductID: l.ProductID, QtyBase: l.QtyBase,
			ValueCents: l.ValueCents, WeightKg: l.WeightKg, IsSpecialBuy: l.IsSpecialBuy}
	}
	return out, nil
}

func (a landedBridge) CapitalizeLandedCostTx(ctx context.Context, tx pgx.Tx, productID, branchID string, cents int64, at time.Time, sourceRefID string) (int64, error) {
	return a.svc.CapitalizeLandedCostTx(ctx, tx, productID, branchID, cents, at, sourceRefID)
}

func (a landedBridge) CapitalizeLandedCostSnapTx(ctx context.Context, tx pgx.Tx, productID, branchID string, cents int64, at time.Time, sourceRefID string) (int64, int64, bool, error) {
	return a.svc.CapitalizeLandedCostSnapTx(ctx, tx, productID, branchID, cents, at, sourceRefID)
}

func (a landedBridge) RestoreLandedCostTx(ctx context.Context, tx pgx.Tx, productID, branchID string, snapAvg, absorbed int64, at time.Time, sourceRefID string) (int64, error) {
	return a.svc.RestoreLandedCostTx(ctx, tx, productID, branchID, snapAvg, absorbed, at, sourceRefID)
}
