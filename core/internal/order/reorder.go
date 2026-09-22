package order

// reorder.go, CP-04: contractor-portal "Reorder". Turns a past order into a NEW DRAFT
// order for the same customer, with every line REPRICED AT TODAY'S RATES through the
// pricing resolver (never the historical price_each, the old fork's defect D1) and
// RE-CHECKED for availability (discontinued / out-of-stock lines are flagged in the
// preview and rejected on submit, defect D2). The portal BFF composes this through
// the same tenant-scoped seam as the other order reads: a missing or cross-tenant
// source order is ErrNotFound (404, never 403, IDOR-safe, US-RO-04).
//
// Module boundary: order stays the sole owner of orders + order_lines, the DRAFT
// write lives here (like quote.Convert writes its converted order), NOT in the portal.
// Pricing and availability arrive via injected seams (zero-trust: no pricing_* or
// stock table is read from this module).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/jackc/pgx/v5"
)

// - injected seams -

// PriceResolver is the today's-rates pricing seam (satisfied by *pricing.Service).
// US-RO-01 keystone: every reorder line price flows through ResolvePrice, the source
// order's unit_price_cents is NEVER copied onto the draft.
type PriceResolver interface {
	ResolvePrice(ctx context.Context, in pricing.ResolveInput) (pricing.Breakdown, error)
}

// InventoryReader is the availability + cost-basis seam (satisfied by *inventory.Service).
// CheckAvailability is a plain read (no locking); CurrentCostCents supplies the cost basis
// the resolver derives a retail anchor from when a product has no explicit list price.
type InventoryReader interface {
	CheckAvailability(ctx context.Context, productID, branchID string, qtyBase float64) (ok bool, reason string, err error)
	CurrentCostCents(ctx context.Context, productID, branchID string) (int64, bool, error)
}

// WithReorderPricing injects the pricing seam (main.go wires *pricing.Service).
func (s *Service) WithReorderPricing(p PriceResolver) *Service { s.pricer = p; return s }

// WithReorderInventory injects the availability/cost seam (main.go wires *inventory.Service).
func (s *Service) WithReorderInventory(inv InventoryReader) *Service { s.inv = inv; return s }

// ErrReorderNotConfigured is returned until both reorder seams are injected, the portal
// maps it to 501, matching the scaffold behaviour of the other BFF features.
var ErrReorderNotConfigured = errors.New("order: reorder seams not configured")

// Reorder line reason codes (the availability codes DISCONTINUED / OUT_OF_STOCK come
// from the inventory seam; these cover the pricing/data causes).
const (
	ReorderReasonNoCatalogItem    = "NOT_A_CATALOG_ITEM" // free-text line: nothing to reprice or stock-check
	ReorderReasonBadQuantity      = "INVALID_QUANTITY"
	ReorderReasonPriceUnavailable = "PRICE_UNAVAILABLE"    // the resolver could not price it today
	ReorderReasonPriceBlocked     = "PRICE_NEEDS_APPROVAL" // below the margin floor, needs the A-20 queue
)

// ReorderUnavailableError reports the selected lines a submit must reject (US-RO-03).
// The portal maps it to 409 with the per-line reasons so the picker can self-correct.
type ReorderUnavailableError struct {
	Lines []ReorderRejection `json:"lines"`
}

// ReorderRejection is one rejected line: the SOURCE order line number + the reason code.
type ReorderRejection struct {
	LineNo int    `json:"line_no"`
	Reason string `json:"reason"`
}

func (e *ReorderUnavailableError) Error() string {
	return fmt.Sprintf("order: %d selected line(s) cannot be reordered", len(e.Lines))
}

// - request/response shapes -

// ReorderContext carries the pricing facts the BFF resolves before delegating
// (customer type comes off the resolved account, exactly like the POS ring).
type ReorderContext struct {
	CustomerType string
}

// ReorderPreview is the GET .../reorder-preview payload: every source line priced at
// today's rates with its availability verdict. Flagged lines render unselectable.
type ReorderPreview struct {
	SourceOrderID string        `json:"source_order_id"`
	SourceOrderNo string        `json:"source_order_no"`
	BranchID      string        `json:"branch_id"`
	Lines         []ReorderLine `json:"lines"`
}

// ReorderLine is one source line graded for reorder. LineNo is the SOURCE order line
// number (what a submit selects). Price fields are zero when Available=false.
type ReorderLine struct {
	LineNo         int    `json:"line_no"`
	ProductID      string `json:"product_id"`
	Description    string `json:"description"`
	QtyEntered     string `json:"qty_entered"` // DECIMAL as string (matches order.Line)
	UOMEntered     string `json:"uom_entered"`
	UnitPriceCents int64  `json:"unit_price_cents"` // TODAY'S resolved price, pre-tax
	PriceUOM       string `json:"price_uom"`
	LineTotalCents int64  `json:"line_total_cents"`
	Available      bool   `json:"available"`
	Reason         string `json:"reason,omitempty"` // machine code when Available=false
}

// ReorderResult identifies the created DRAFT order.
type ReorderResult struct {
	OrderID string `json:"order_id"`
	OrderNo string `json:"order_no"`
	Status  string `json:"status"`
}

// - store seam (satisfied by *Repository; asserted so existing fakes stay valid) -

// ReorderSource is the tenant-scoped projection of the source order the reorder reads.
type ReorderSource struct {
	OrderID     string
	OrderNo     string
	BranchID    string
	Fulfillment string
	Lines       []ReorderSourceLine
}

// ReorderSourceLine is one source order_lines row (quantities as DECIMAL strings).
type ReorderSourceLine struct {
	LineNo      int
	ProductID   string // "" for a free-text line
	Description string
	QtyEntered  string
	UOMEntered  string
	QtyBase     string
}

// ReorderDraft is the write shape for the new DRAFT order. TaxCents is deliberately
// absent: a portal DRAFT is pre-tax (tax is assessed when the order is confirmed/rung
// , M5 owns tax), so the repo persists tax_cents = 0 and total = subtotal.
type ReorderDraft struct {
	CustomerID     string
	BranchID       string
	Fulfillment    string
	SourceOrderID  string
	IdempotencyKey string
	SubtotalCents  int64
	Lines          []ReorderDraftLine
}

// ReorderDraftLine is one order_lines row of the draft (already repriced).
type ReorderDraftLine struct {
	LineNo         int
	ProductID      string
	Description    string
	QtyEntered     string
	UOMEntered     string
	QtyBase        string
	UnitPriceCents int64
	PriceUOM       string
	LineTotalCents int64
}

// reorderStore is the extra persistence the reorder needs. It is asserted off the
// injected orderStore (the pricing batchStore pattern) so the existing service fakes
// keep compiling; *Repository satisfies it in prod.
type reorderStore interface {
	ReorderSource(ctx context.Context, customerID, orderID string) (ReorderSource, error)
	CreateReorderDraft(ctx context.Context, d ReorderDraft) (ReorderResult, error)
}

func (s *Service) reorderStore() (reorderStore, error) {
	rs, ok := s.store.(reorderStore)
	if !ok {
		return nil, ErrReorderNotConfigured
	}
	return rs, nil
}

// - service -

// PreviewReorder grades every line of the tenant's past order for reorder: today's
// price (US-RO-01) + availability flag (US-RO-03). A missing/cross-tenant order is
// ErrNotFound (US-RO-04).
func (s *Service) PreviewReorder(ctx context.Context, customerID, orderID string, rc ReorderContext) (ReorderPreview, error) {
	if customerID == "" || orderID == "" {
		return ReorderPreview{}, httpx.BadRequestf("customer id and order id are required")
	}
	if s.pricer == nil || s.inv == nil {
		return ReorderPreview{}, ErrReorderNotConfigured
	}
	rs, err := s.reorderStore()
	if err != nil {
		return ReorderPreview{}, err
	}
	src, err := rs.ReorderSource(ctx, customerID, orderID)
	if err != nil {
		return ReorderPreview{}, err
	}
	lines, err := s.gradeReorderLines(ctx, customerID, rc, src)
	if err != nil {
		return ReorderPreview{}, err
	}
	return ReorderPreview{
		SourceOrderID: src.OrderID,
		SourceOrderNo: src.OrderNo,
		BranchID:      src.BranchID,
		Lines:         lines,
	}, nil
}

// CreateReorder creates a DRAFT order from the selected source line numbers, repriced
// at today's rates. Any selected line that grades unavailable rejects the WHOLE submit
// with a ReorderUnavailableError (US-RO-03), a draft never silently drops a line.
// idempotencyKey is required: a retried POST returns the already-created draft.
func (s *Service) CreateReorder(ctx context.Context, customerID, orderID string, rc ReorderContext, selected []int, idempotencyKey string) (ReorderResult, error) {
	if customerID == "" || orderID == "" {
		return ReorderResult{}, httpx.BadRequestf("customer id and order id are required")
	}
	if idempotencyKey == "" {
		return ReorderResult{}, httpx.BadRequestf("idempotency_key is required")
	}
	if len(idempotencyKey) > 128 {
		return ReorderResult{}, httpx.BadRequestf("idempotency_key exceeds 128 characters")
	}
	if len(selected) == 0 {
		return ReorderResult{}, httpx.BadRequestf("select at least one line to reorder")
	}
	if s.pricer == nil || s.inv == nil {
		return ReorderResult{}, ErrReorderNotConfigured
	}
	rs, err := s.reorderStore()
	if err != nil {
		return ReorderResult{}, err
	}
	src, err := rs.ReorderSource(ctx, customerID, orderID)
	if err != nil {
		return ReorderResult{}, err
	}
	graded, err := s.gradeReorderLines(ctx, customerID, rc, src)
	if err != nil {
		return ReorderResult{}, err
	}
	byNo := make(map[int]int, len(graded)) // source line_no -> index into graded/src.Lines
	for i, g := range graded {
		byNo[g.LineNo] = i
	}

	// Validate the selection: every number must exist on the source order (400) and be
	// available (else collected into the 409). Duplicates are collapsed.
	picked := make([]int, 0, len(selected))
	seen := make(map[int]bool, len(selected))
	var rejected []ReorderRejection
	for _, n := range selected {
		if seen[n] {
			continue
		}
		seen[n] = true
		i, ok := byNo[n]
		if !ok {
			return ReorderResult{}, httpx.BadRequestf("line %d is not on this order", n)
		}
		if !graded[i].Available {
			rejected = append(rejected, ReorderRejection{LineNo: n, Reason: graded[i].Reason})
			continue
		}
		picked = append(picked, i)
	}
	if len(rejected) > 0 {
		sort.Slice(rejected, func(a, b int) bool { return rejected[a].LineNo < rejected[b].LineNo })
		return ReorderResult{}, &ReorderUnavailableError{Lines: rejected}
	}
	sort.Ints(picked)

	draft := ReorderDraft{
		CustomerID:     customerID,
		BranchID:       src.BranchID,
		Fulfillment:    src.Fulfillment,
		SourceOrderID:  src.OrderID,
		IdempotencyKey: idempotencyKey,
	}
	for newNo, i := range picked {
		g, sl := graded[i], src.Lines[i]
		draft.Lines = append(draft.Lines, ReorderDraftLine{
			LineNo:         newNo + 1,
			ProductID:      sl.ProductID,
			Description:    sl.Description,
			QtyEntered:     sl.QtyEntered,
			UOMEntered:     sl.UOMEntered,
			QtyBase:        sl.QtyBase,
			UnitPriceCents: g.UnitPriceCents, // today's resolved price, never sl's historical price
			PriceUOM:       g.PriceUOM,
			LineTotalCents: g.LineTotalCents,
		})
		draft.SubtotalCents += g.LineTotalCents
	}

	res, err := rs.CreateReorderDraft(ctx, draft)
	if err != nil {
		return ReorderResult{}, err
	}
	// Q-044: order mutation surfaces audit who/what/when (mirrors SetDeliveryInstructions).
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: audit.ActOrderChanged, EntityType: "order", EntityID: res.OrderID,
			ActorID: customerID, Purpose: "PORTAL_REORDER", Reason: "reorder_draft_created",
			Extra: map[string]any{"source_order_id": src.OrderID, "lines": len(draft.Lines)},
		})
		// DIB-159 order-change history: a reorder is a creation writer, the order is INSERTed at
		// DRAFT (reorder.go CreateReorderDraft), so this is its creation transition ""→DRAFT on the
		// per-order ORDER_STATUS_CHANGED timeline. Without it a reorder draft's history is empty even
		// though the order exists at DRAFT (the ActOrderChanged row above is a field-change action,
		// which OrderHistory filters out). Same Recorder seam + entity ("order") the POS ring uses.
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: audit.ActOrderStatusChanged, EntityType: "order", EntityID: res.OrderID,
			ActorID: customerID, Purpose: "ORDER_STATUS_TRANSITION", Reason: "reorder_draft_created",
			Extra: map[string]any{"old_status": "", "new_status": "DRAFT"},
		})
	}
	return res, nil
}

// gradeReorderLines prices + availability-grades every source line, in source order.
// Grading is per-line and total: an unpriceable or unavailable line is FLAGGED (never
// dropped, never fatal to the preview), mirroring pricing.LowMarginReport's
// surface-don't-drop rule. Infrastructure errors from the seams DO fail the call.
func (s *Service) gradeReorderLines(ctx context.Context, customerID string, rc ReorderContext, src ReorderSource) ([]ReorderLine, error) {
	out := make([]ReorderLine, 0, len(src.Lines))
	for _, sl := range src.Lines {
		g := ReorderLine{
			LineNo:      sl.LineNo,
			ProductID:   sl.ProductID,
			Description: sl.Description,
			QtyEntered:  sl.QtyEntered,
			UOMEntered:  sl.UOMEntered,
		}
		if sl.ProductID == "" {
			g.Reason = ReorderReasonNoCatalogItem
			out = append(out, g)
			continue
		}
		qtyEntered, err1 := strconv.ParseFloat(sl.QtyEntered, 64)
		qtyBase, err2 := strconv.ParseFloat(sl.QtyBase, 64)
		if err1 != nil || err2 != nil || qtyEntered <= 0 || qtyBase <= 0 {
			g.Reason = ReorderReasonBadQuantity
			out = append(out, g)
			continue
		}

		ok, reason, err := s.inv.CheckAvailability(ctx, sl.ProductID, src.BranchID, qtyBase)
		if err != nil {
			return nil, fmt.Errorf("check availability line %d: %w", sl.LineNo, err)
		}
		if !ok {
			g.Reason = reason
			out = append(out, g)
			continue
		}

		// Cost basis for the resolver's derived retail anchor (cost × markup), most
		// seeded products have no explicit list price. found=false leaves it 0.
		cost, _, err := s.inv.CurrentCostCents(ctx, sl.ProductID, src.BranchID)
		if err != nil {
			return nil, fmt.Errorf("cost basis line %d: %w", sl.LineNo, err)
		}
		bd, err := s.pricer.ResolvePrice(ctx, pricing.ResolveInput{
			CustomerID:   customerID,
			CustomerType: rc.CustomerType,
			ProductID:    sl.ProductID,
			BranchID:     src.BranchID,
			QtyEntered:   qtyEntered,
			UOMEntered:   sl.UOMEntered,
			CostCents:    cost,
		})
		if err != nil {
			// A line the resolver cannot price today (no anchor, no UOM factor, unknown
			// product) is flagged, the contractor phones the counter for that one line
			// instead of the whole preview 500ing.
			g.Reason = ReorderReasonPriceUnavailable
			out = append(out, g)
			continue
		}
		if bd.Blocked {
			g.Reason = ReorderReasonPriceBlocked
			out = append(out, g)
			continue
		}
		g.Available = true
		g.UnitPriceCents = bd.FinalCents
		g.PriceUOM = bd.PriceUOM
		g.LineTotalCents = bd.LineTotalCents
		out = append(out, g)
	}
	return out, nil
}

// - repository -

// ReorderSource loads the tenant-scoped source projection (header facts + lines).
// A missing or cross-tenant id maps to ErrNotFound, never forbidden (US-RO-04).
func (r *Repository) ReorderSource(ctx context.Context, customerID, orderID string) (ReorderSource, error) {
	if r.pool == nil {
		return ReorderSource{}, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return ReorderSource{}, ErrNotFound
	}
	var src ReorderSource
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, order_no::text, branch_id::text, fulfillment
		 FROM orders WHERE id = $1 AND customer_id = $2`, orderID, customerID).
		Scan(&src.OrderID, &src.OrderNo, &src.BranchID, &src.Fulfillment)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReorderSource{}, ErrNotFound
		}
		return ReorderSource{}, fmt.Errorf("reorder source %q: %w", orderID, err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT line_no, product_id::text, description, qty_entered::text, uom_entered::text, qty_base::text
		 FROM order_lines WHERE order_id = $1 ORDER BY line_no`, src.OrderID)
	if err != nil {
		return ReorderSource{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var l ReorderSourceLine
		var productID *string
		if err := rows.Scan(&l.LineNo, &productID, &l.Description, &l.QtyEntered, &l.UOMEntered, &l.QtyBase); err != nil {
			return ReorderSource{}, err
		}
		if productID != nil {
			l.ProductID = *productID
		}
		src.Lines = append(src.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return ReorderSource{}, err
	}
	return src, nil
}

// CreateReorderDraft writes the DRAFT order + its repriced lines in one transaction.
// Idempotency without a schema change: the key is stamped into orders.metadata and the
// (customer, key) pair is serialized under a pg advisory xact lock, so a concurrent or
// retried POST with the same key returns the already-created draft instead of a twin.
func (r *Repository) CreateReorderDraft(ctx context.Context, d ReorderDraft) (ReorderResult, error) {
	if r.pool == nil {
		return ReorderResult{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ReorderResult{}, fmt.Errorf("create reorder draft: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 42))`,
		d.CustomerID+"|"+d.IdempotencyKey); err != nil {
		return ReorderResult{}, fmt.Errorf("create reorder draft: lock: %w", err)
	}

	var res ReorderResult
	err = tx.QueryRow(ctx,
		`SELECT id::text, order_no::text, status FROM orders
		 WHERE customer_id = $1 AND metadata->>'reorder_idempotency_key' = $2`,
		d.CustomerID, d.IdempotencyKey).Scan(&res.OrderID, &res.OrderNo, &res.Status)
	if err == nil {
		return res, tx.Commit(ctx) // idempotent replay, the draft already exists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ReorderResult{}, fmt.Errorf("create reorder draft: idempotency read: %w", err)
	}

	meta, err := json.Marshal(map[string]any{
		"source":                  "portal_reorder",
		"source_order_id":         d.SourceOrderID,
		"reorder_idempotency_key": d.IdempotencyKey,
	})
	if err != nil {
		return ReorderResult{}, err
	}
	// tax_cents = 0 by design: the DRAFT is pre-tax (M5 assesses tax at confirmation).
	err = tx.QueryRow(ctx,
		// DIB-388: the branch-coded mint (migration 0087's next_order_no) replaces the global
		// '#SO-' || nextval sequence, so a reorder draft carries the branch it belongs to in its number.
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment,
		                     subtotal_cents, tax_cents, total_cents, metadata)
		 VALUES (next_order_no($2), $1, $2, 'DRAFT', $3, $4, 0, $4, $5)
		 RETURNING id::text, order_no::text, status`,
		d.CustomerID, d.BranchID, d.Fulfillment, d.SubtotalCents, meta).
		Scan(&res.OrderID, &res.OrderNo, &res.Status)
	if err != nil {
		return ReorderResult{}, fmt.Errorf("create reorder draft: insert order: %w", err)
	}

	for _, l := range d.Lines {
		var productID any
		if l.ProductID != "" {
			productID = l.ProductID
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered,
			                          uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			res.OrderID, l.LineNo, productID, l.Description, l.QtyEntered,
			l.UOMEntered, l.QtyBase, l.UnitPriceCents, l.PriceUOM, l.LineTotalCents); err != nil {
			return ReorderResult{}, fmt.Errorf("create reorder draft: insert line %d: %w", l.LineNo, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ReorderResult{}, fmt.Errorf("create reorder draft: commit: %w", err)
	}
	return res, nil
}
