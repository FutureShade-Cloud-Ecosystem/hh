package order

// duplicate.go, DIB-489 staff order duplication + DIB-490 duplicate-order detection.
//
// DIB-489. Dealer staff duplicate orders constantly: the same order with one or two changes, or a
// multi-truck job (create once, duplicate four). This is the COUNTER door onto the CP-04 reorder
// machinery (reorder.go): from any order the caller can reach, a past order or a draft, create a
// fresh DRAFT for the SAME customer from a chosen subset of its lines, with per-line quantity
// adjustments, repriced at TODAY'S rates through the pricing resolver (the historical price_each is
// never copied, defect D1) and re-checked for availability (defect D2). The new draft records
// provenance in orders.metadata (source_order_id), which is what DIB-490 reads to keep an
// intentional duplicate from warning about its own source.
//
//	GET  /api/v1/orders/{id}/duplicate-preview → every source line priced today + availability
//	POST /api/v1/orders/{id}/duplicate         → create the DRAFT from the selected lines
//	GET  /api/v1/orders/{id}/duplicate-check   → DIB-490 read-only candidate-duplicates
//
// AUTHORITY. All three are open to any ACTIVE in-branch staff member (RequireStaff at the route, the
// caller ladder + branch check here, the OrderRead posture). Creating a DRAFT moves no money and
// commits no credit: that happens at confirm/convert, which keep their own gates. This mirrors the
// portal reorder, which lets the customer themselves land a DRAFT with no extra tier.
//
// DIB-490. Two reps sometimes key the same order. duplicate-check is a READ-ONLY heuristic the
// draft-submit UI consults: same customer + a materially overlapping line set + created within
// DuplicateCheckWindow of this order → candidates, worst first. It NEVER blocks, the client renders
// a warning with "open that order" / "continue anyway", so a legitimate repeat order goes through
// with one extra click. Provenance-linked orders are excluded (the source this draft was duplicated
// from, drafts duplicated FROM this order, and siblings duplicated from the same source): a
// duplication the operator asked for must not be reported back as a suspected accident.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// - DIB-490 tunables -

// DuplicateCheckWindow is the recency window for DIB-490: only orders created within this span of
// the checked order's own created_at are candidates. One var, one place to tune. It is measured
// SYMMETRICALLY (before and after) because the check runs at submit time, which can be hours after
// the draft was keyed, a twin keyed just after this one is exactly as suspicious as one keyed just
// before.
var DuplicateCheckWindow = 24 * time.Hour

// maxDuplicateCandidates bounds the DIB-490 answer: the warning names ONE order and the panel can
// list a handful; nobody triages twenty.
const maxDuplicateCandidates = 5

// materialOverlap is the DIB-490 "materially overlapping lines" rule, kept deliberately simple: the
// candidate shares at least HALF of the checked order's distinct catalogue products (rounded up),
// and always at least one. An order with no catalogue products (all free-text) can never overlap.
func materialOverlap(sharedProducts, draftProducts int) bool {
	if draftProducts <= 0 || sharedProducts <= 0 {
		return false
	}
	need := (draftProducts + 1) / 2 // ceil(draftProducts / 2)
	if need < 1 {
		need = 1
	}
	return sharedProducts >= need
}

// - shapes -

// DuplicateSelection is one chosen source line: the SOURCE line number plus an optional replacement
// quantity ("" keeps the source quantity). Quantities stay strings on the wire (house rule: qty is
// DECIMAL, never float).
type DuplicateSelection struct {
	LineNo     int    `json:"line_no"`
	QtyEntered string `json:"qty_entered,omitempty"`
}

// DuplicateCandidate is one DIB-490 suspect: enough for the client to name it ("order #X"), open it,
// and say why it matched.
type DuplicateCandidate struct {
	OrderID        string    `json:"order_id"`
	OrderNo        string    `json:"order_no"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	SharedProducts int       `json:"shared_products"`
}

// DuplicateCheck is the duplicate-check answer. Candidates is empty, never null, when nothing
// matched; an empty answer is the ordinary outcome, not an error.
type DuplicateCheck struct {
	OrderID string `json:"order_id"`
	// WindowHours lets the client word the warning ("keyed in the last 24 hours") from the server's
	// actual setting instead of hard-coding it.
	WindowHours   int                  `json:"window_hours"`
	DraftProducts int                  `json:"draft_products"`
	Candidates    []DuplicateCandidate `json:"candidates"`
}

// - store capabilities (asserted off the injected orderStore, the reorderStore pattern) -

// duplicateStore is the persistence the staff duplicate needs beyond reorderStore: the same
// transactional draft write, but attributed to the STAFF caller (orders.created_by) and stamped with
// the staff-duplicate provenance keys.
type duplicateStore interface {
	CreateDuplicateDraft(ctx context.Context, d ReorderDraft, createdBy string) (ReorderResult, error)
}

// duplicateCheckStore is the DIB-490 read: candidates sharing products with orderID within the
// window, provenance-linked orders excluded, worst (most shared, newest) first. draftProducts is the
// checked order's own distinct catalogue-product count, the denominator of the overlap rule.
type duplicateCheckStore interface {
	SimilarRecentOrders(ctx context.Context, orderID string, window time.Duration, limit int) (cands []DuplicateCandidate, draftProducts int, err error)
}

func (s *Service) duplicateStore() (duplicateStore, error) {
	ds, ok := s.store.(duplicateStore)
	if !ok {
		return nil, ErrDuplicateNotConfigured
	}
	return ds, nil
}

// ErrDuplicateNotConfigured is returned until the store/seams the duplicate needs are wired, the
// handler maps it to 503 naming what is missing, mirroring ErrEditNotConfigured.
var ErrDuplicateNotConfigured = errors.New("order: duplicate seams not configured")

// - DIB-489 service -

// duplicateAuthorize runs the shared staff ladder for all three duplicate surfaces, in the SAME
// order OrderRead uses so the HTTP mapping is deterministic and a cross-branch order is never
// distinguishable from a nonexistent one: caller → ACTIVE → resolve id (branch-scoped, accepts
// "#SO-4") → branch reach. Returns the caller's user id and the RESOLVED order id.
func (s *Service) duplicateAuthorize(ctx context.Context, idOrNo string) (actorID, orderID string, err error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return "", "", err
	}
	if caller.Status != identity.StatusActive {
		return "", "", ErrForbidden
	}
	if idOrNo == "" {
		return "", "", httpx.BadRequestf("an order id is required, open an order first, then duplicate it")
	}
	rGlobal, rBranches := callerBranchScope(caller)
	orderID, err = s.store.ResolveOrderID(ctx, idOrNo, rGlobal, rBranches)
	if err != nil {
		return "", "", err
	}
	_, branchID, err := s.store.OrderHeader(ctx, orderID)
	if err != nil {
		return "", "", err // ErrNotFound, no existence leak
	}
	if !caller.CanAccessBranch(branchID) {
		return "", "", ErrForbidden
	}
	return caller.UserID, orderID, nil
}

// PreviewDuplicate grades every line of the source order for duplication: today's resolved price +
// availability verdict, exactly the working set the portal reorder preview serves, but reached by a
// STAFF caller proving branch reach, with the customer and pricing tier resolved server-side.
func (s *Service) PreviewDuplicate(ctx context.Context, idOrNo string) (ReorderPreview, error) {
	_, orderID, err := s.duplicateAuthorize(ctx, idOrNo)
	if err != nil {
		return ReorderPreview{}, err
	}
	if s.pricer == nil || s.inv == nil || s.custType == nil {
		return ReorderPreview{}, ErrDuplicateNotConfigured
	}
	customerID, _, err := s.orderPricingContext(ctx, orderID)
	if err != nil {
		return ReorderPreview{}, err
	}
	custType, err := s.custType.CustomerType(ctx, customerID)
	if err != nil {
		return ReorderPreview{}, fmt.Errorf("resolve customer pricing tier: %w", err)
	}
	rs, err := s.reorderStore()
	if err != nil {
		return ReorderPreview{}, ErrDuplicateNotConfigured
	}
	src, err := rs.ReorderSource(ctx, customerID, orderID)
	if err != nil {
		return ReorderPreview{}, err
	}
	lines, err := s.gradeReorderLines(ctx, customerID, ReorderContext{CustomerType: custType}, src)
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

// CreateDuplicate creates a fresh DRAFT order from the source order's selected lines, each at its
// (possibly adjusted) quantity, repriced at today's rates. Any selected line that cannot be
// duplicated, free-text, unpriceable, margin-blocked, or unavailable at the requested quantity -
// rejects the WHOLE submit with a ReorderUnavailableError (409, per-line reasons) so a draft never
// silently drops or mis-prices a line. idempotencyKey is required: a retried POST returns the
// already-created draft instead of a twin.
func (s *Service) CreateDuplicate(ctx context.Context, idOrNo string, sel []DuplicateSelection, idempotencyKey string) (ReorderResult, error) {
	actorID, orderID, err := s.duplicateAuthorize(ctx, idOrNo)
	if err != nil {
		return ReorderResult{}, err
	}
	if idempotencyKey == "" {
		return ReorderResult{}, httpx.BadRequestf("an idempotency_key is required, generate one per submit so a retry cannot create a second draft")
	}
	if len(idempotencyKey) > 128 {
		return ReorderResult{}, httpx.BadRequestf("idempotency_key exceeds 128 characters")
	}
	if len(sel) == 0 {
		return ReorderResult{}, httpx.BadRequestf("select at least one line to duplicate")
	}
	if s.pricer == nil || s.inv == nil || s.custType == nil {
		return ReorderResult{}, ErrDuplicateNotConfigured
	}
	ds, err := s.duplicateStore()
	if err != nil {
		return ReorderResult{}, err
	}
	rs, err := s.reorderStore()
	if err != nil {
		return ReorderResult{}, ErrDuplicateNotConfigured
	}
	customerID, _, err := s.orderPricingContext(ctx, orderID)
	if err != nil {
		return ReorderResult{}, err
	}
	src, err := rs.ReorderSource(ctx, customerID, orderID)
	if err != nil {
		return ReorderResult{}, err
	}
	byNo := make(map[int]ReorderSourceLine, len(src.Lines))
	for _, sl := range src.Lines {
		byNo[sl.LineNo] = sl
	}

	// Validate the selection ONCE, up front: every number must exist on the source order and appear
	// only once, a repeated line_no is almost certainly a client bug, and guessing which quantity
	// was meant would be worse than saying so.
	seen := make(map[int]bool, len(sel))
	ordered := make([]DuplicateSelection, 0, len(sel))
	for _, pick := range sel {
		if _, ok := byNo[pick.LineNo]; !ok {
			return ReorderResult{}, httpx.BadRequestf("line %d is not on this order, reload the preview and pick again", pick.LineNo)
		}
		if seen[pick.LineNo] {
			return ReorderResult{}, httpx.BadRequestf("line %d is selected twice, send each line once, with the quantity you want", pick.LineNo)
		}
		seen[pick.LineNo] = true
		ordered = append(ordered, pick)
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a].LineNo < ordered[b].LineNo })

	draft := ReorderDraft{
		CustomerID:     customerID,
		BranchID:       src.BranchID,
		Fulfillment:    src.Fulfillment,
		SourceOrderID:  src.OrderID,
		IdempotencyKey: idempotencyKey,
	}
	var rejected []ReorderRejection
	for _, pick := range ordered {
		sl := byNo[pick.LineNo]
		line, reason, gerr := s.gradeDuplicateLine(ctx, customerID, src.BranchID, sl, pick.QtyEntered)
		if gerr != nil {
			return ReorderResult{}, gerr // infrastructure failure, never a silent flag
		}
		if reason != "" {
			rejected = append(rejected, ReorderRejection{LineNo: pick.LineNo, Reason: reason})
			continue
		}
		line.LineNo = len(draft.Lines) + 1 // renumbered from 1, in source order
		draft.Lines = append(draft.Lines, line)
		draft.SubtotalCents += line.LineTotalCents
	}
	if len(rejected) > 0 {
		sort.Slice(rejected, func(a, b int) bool { return rejected[a].LineNo < rejected[b].LineNo })
		return ReorderResult{}, &ReorderUnavailableError{Lines: rejected}
	}

	res, err := ds.CreateDuplicateDraft(ctx, draft, actorID)
	if err != nil {
		return ReorderResult{}, err
	}
	// Q-044 + DIB-159, mirroring the portal reorder writer (reorder.go): one ORDER_CHANGED row naming
	// the mutation surface, and the creation ""→DRAFT transition on the per-order status timeline -
	// without which a duplicated draft's history would be silently empty.
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: audit.ActOrderChanged, EntityType: "order", EntityID: res.OrderID,
			ActorID: actorID, Purpose: "STAFF_DUPLICATE", Reason: "duplicate_draft_created",
			Extra: map[string]any{"source_order_id": src.OrderID, "lines": len(draft.Lines)},
		})
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: audit.ActOrderStatusChanged, EntityType: "order", EntityID: res.OrderID,
			ActorID: actorID, Purpose: "ORDER_STATUS_TRANSITION", Reason: "duplicate_draft_created",
			Extra: map[string]any{"old_status": "", "new_status": "DRAFT"},
		})
	}
	return res, nil
}

// gradeDuplicateLine grades ONE selected source line at its effective quantity. It mirrors
// gradeReorderLines' per-line body (same reason vocabulary, same availability-then-price order) with
// one addition: qtyOverride. The availability check runs against the OVERRIDDEN base quantity -
// derived linearly from the source line's base-per-entered ratio, because qty_base is qty_entered ×
// the line's fixed UOM factor, and the persisted base comes from the resolver's own QtyBase, which
// owns UOM math for real (the priceLine discipline, edit.go).
//
// Returns (line, "", nil) when the line is duplicable, ("", reason, nil) when it must be rejected
// with a per-line reason, and a non-nil error only for an infrastructure failure. The customer's
// pricing tier is resolved inside priceLine (the staff pricing choke-point), never trusted from the
// caller.
func (s *Service) gradeDuplicateLine(ctx context.Context, customerID, branchID string, sl ReorderSourceLine, qtyOverride string) (ReorderDraftLine, string, error) {
	if sl.ProductID == "" {
		// A free-text line has nothing to reprice or stock-check, same verdict as the reorder path.
		return ReorderDraftLine{}, ReorderReasonNoCatalogItem, nil
	}
	srcQty, err1 := strconv.ParseFloat(sl.QtyEntered, 64)
	srcBase, err2 := strconv.ParseFloat(sl.QtyBase, 64)
	if err1 != nil || err2 != nil || srcQty <= 0 || srcBase <= 0 {
		return ReorderDraftLine{}, ReorderReasonBadQuantity, nil
	}
	qtyEntered := sl.QtyEntered
	qty := srcQty
	if strings.TrimSpace(qtyOverride) != "" {
		q, qerr := parseEditQty(qtyOverride)
		if qerr != nil {
			// The operator typed it just now, a malformed adjustment is a 400 with the reason, not
			// a flagged line (there is nothing to deselect, only something to retype).
			return ReorderDraftLine{}, "", qerr
		}
		qty = q
		qtyEntered = strings.TrimSpace(qtyOverride)
	}
	estBase := qty * (srcBase / srcQty)
	ok, reason, err := s.inv.CheckAvailability(ctx, sl.ProductID, branchID, estBase)
	if err != nil {
		return ReorderDraftLine{}, "", fmt.Errorf("check availability line %d: %w", sl.LineNo, err)
	}
	if !ok {
		return ReorderDraftLine{}, reason, nil
	}
	lp, err := s.priceLine(ctx, customerID, branchID, sl.ProductID, sl.UOMEntered, qty)
	if err != nil {
		var blocked *PriceBlockedError
		var br httpx.BadRequestError
		switch {
		case errors.As(err, &blocked):
			return ReorderDraftLine{}, ReorderReasonPriceBlocked, nil
		case errors.As(err, &br):
			// priceLine wraps a resolver refusal (no anchor, no UOM factor) as a BadRequest; on this
			// path the quantity was already validated above, so a BadRequest here means the line
			// cannot be priced today, flag it, exactly like the reorder preview does.
			return ReorderDraftLine{}, ReorderReasonPriceUnavailable, nil
		default:
			return ReorderDraftLine{}, "", err // seams unwired / infra, fail the call
		}
	}
	return ReorderDraftLine{
		ProductID:      sl.ProductID,
		Description:    sl.Description,
		QtyEntered:     qtyEntered,
		UOMEntered:     sl.UOMEntered,
		QtyBase:        formatQty(lp.QtyBase),
		UnitPriceCents: lp.UnitPriceCents,
		PriceUOM:       lp.PriceUOM,
		LineTotalCents: lp.LineTotalCents,
	}, "", nil
}

// - DIB-490 service -

// CheckDuplicates answers the DIB-490 question read-only: which other recent orders for the same
// customer look like this one? The store returns provenance-filtered raw candidates; the material-
// overlap rule is applied here so it stays a plain, unit-testable function. The answer is advisory -
// nothing here (or anywhere) blocks a submit because of it.
func (s *Service) CheckDuplicates(ctx context.Context, idOrNo string) (DuplicateCheck, error) {
	_, orderID, err := s.duplicateAuthorize(ctx, idOrNo)
	if err != nil {
		return DuplicateCheck{}, err
	}
	dcs, ok := s.store.(duplicateCheckStore)
	if !ok {
		return DuplicateCheck{}, ErrDuplicateNotConfigured
	}
	cands, draftProducts, err := dcs.SimilarRecentOrders(ctx, orderID, DuplicateCheckWindow, maxDuplicateCandidates)
	if err != nil {
		return DuplicateCheck{}, err
	}
	out := DuplicateCheck{
		OrderID:       orderID,
		WindowHours:   int(DuplicateCheckWindow / time.Hour),
		DraftProducts: draftProducts,
		Candidates:    []DuplicateCandidate{}, // empty, never null, an empty answer is the good outcome
	}
	for _, c := range cands {
		if materialOverlap(c.SharedProducts, draftProducts) {
			out.Candidates = append(out.Candidates, c)
		}
	}
	return out, nil
}

// - repository -

// CreateDuplicateDraft writes the DRAFT order + its repriced lines in one transaction, the
// CreateReorderDraft shape (advisory xact lock on (customer, key), metadata-stamped idempotency,
// replay returns the existing draft) with the staff-duplicate provenance and the creating STAFF user
// attributed on orders.created_by (the portal reorder has no staff actor; this door does).
func (r *Repository) CreateDuplicateDraft(ctx context.Context, d ReorderDraft, createdBy string) (ReorderResult, error) {
	if r.pool == nil {
		return ReorderResult{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ReorderResult{}, fmt.Errorf("create duplicate draft: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 42))`,
		d.CustomerID+"|dup|"+d.IdempotencyKey); err != nil {
		return ReorderResult{}, fmt.Errorf("create duplicate draft: lock: %w", err)
	}

	var res ReorderResult
	err = tx.QueryRow(ctx,
		`SELECT id::text, order_no::text, status FROM orders
		 WHERE customer_id = $1 AND metadata->>'duplicate_idempotency_key' = $2`,
		d.CustomerID, d.IdempotencyKey).Scan(&res.OrderID, &res.OrderNo, &res.Status)
	if err == nil {
		return res, tx.Commit(ctx) // idempotent replay, the draft already exists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ReorderResult{}, fmt.Errorf("create duplicate draft: idempotency read: %w", err)
	}

	// source_order_id is THE provenance key: DIB-490's exclusion list is built from it, and it is
	// deliberately the SAME key the portal reorder stamps so one link works for both creation doors.
	meta, err := json.Marshal(map[string]any{
		"source":                    "staff_duplicate",
		"source_order_id":           d.SourceOrderID,
		"duplicate_idempotency_key": d.IdempotencyKey,
	})
	if err != nil {
		return ReorderResult{}, err
	}
	var createdByArg any
	if createdBy != "" && looksLikeUUID(createdBy) {
		createdByArg = createdBy
	}
	// tax_cents = 0 by design: a DRAFT is pre-tax (M5 assesses tax downstream), the reorder rule.
	err = tx.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment,
		                     subtotal_cents, tax_cents, total_cents, metadata, created_by, org_id)
		 VALUES (next_order_no($2), $1, $2, 'DRAFT', $3, $4, 0, $4, $5, $6,
		         (SELECT l.org_id FROM locations l WHERE l.id = $2))
		 RETURNING id::text, order_no::text, status`,
		d.CustomerID, d.BranchID, d.Fulfillment, d.SubtotalCents, meta, createdByArg).
		Scan(&res.OrderID, &res.OrderNo, &res.Status)
	if err != nil {
		return ReorderResult{}, fmt.Errorf("create duplicate draft: insert order: %w", err)
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
			return ReorderResult{}, fmt.Errorf("create duplicate draft: insert line %d: %w", l.LineNo, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ReorderResult{}, fmt.Errorf("create duplicate draft: commit: %w", err)
	}
	return res, nil
}

// SimilarRecentOrders is the DIB-490 candidate read: same customer, created within ±window of the
// checked order's created_at, not CANCELLED, sharing at least one distinct catalogue product -
// minus every provenance-linked order (the checked order's own source, orders duplicated FROM it,
// and siblings duplicated from the same source), because those duplications were asked for.
// Ordered worst-first: most shared products, then newest. The material-overlap threshold is applied
// by the SERVICE (materialOverlap) so the rule stays a pure function.
func (r *Repository) SimilarRecentOrders(ctx context.Context, orderID string, window time.Duration, limit int) ([]DuplicateCandidate, int, error) {
	if r.pool == nil {
		return nil, 0, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return nil, 0, ErrNotFound
	}
	var draftProducts int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(DISTINCT product_id) FROM order_lines WHERE order_id = $1 AND product_id IS NOT NULL`,
		orderID).Scan(&draftProducts); err != nil {
		return nil, 0, fmt.Errorf("duplicate check: own products %q: %w", orderID, err)
	}
	if draftProducts == 0 {
		return nil, 0, nil // nothing to overlap on, free-text-only orders never warn
	}
	rows, err := r.pool.Query(ctx,
		`WITH me AS (
		    SELECT id, customer_id, created_at, NULLIF(metadata->>'source_order_id','') AS source_id
		    FROM orders WHERE id = $1
		 ), mine AS (
		    SELECT DISTINCT product_id FROM order_lines WHERE order_id = $1 AND product_id IS NOT NULL
		 )
		 SELECT o.id::text, o.order_no::text, o.status, o.created_at,
		        count(DISTINCT ol.product_id)::int AS shared
		 FROM orders o
		 JOIN me ON o.customer_id = me.customer_id
		 JOIN order_lines ol ON ol.order_id = o.id
		 JOIN mine ON mine.product_id = ol.product_id
		 WHERE o.id <> me.id
		   AND o.status <> 'CANCELLED'
		   AND o.created_at BETWEEN me.created_at - make_interval(secs => $2)
		                        AND me.created_at + make_interval(secs => $2)
		   AND (me.source_id IS NULL OR o.id::text <> me.source_id)
		   AND COALESCE(NULLIF(o.metadata->>'source_order_id',''), '') <> me.id::text
		   AND (me.source_id IS NULL
		        OR COALESCE(NULLIF(o.metadata->>'source_order_id',''), '') <> me.source_id)
		 GROUP BY o.id, o.order_no, o.status, o.created_at
		 ORDER BY shared DESC, o.created_at DESC
		 LIMIT $3`,
		orderID, window.Seconds(), limit)
	if err != nil {
		return nil, 0, fmt.Errorf("duplicate check: candidates %q: %w", orderID, err)
	}
	defer rows.Close()
	var out []DuplicateCandidate
	for rows.Next() {
		var c DuplicateCandidate
		if err := rows.Scan(&c.OrderID, &c.OrderNo, &c.Status, &c.CreatedAt, &c.SharedProducts); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, draftProducts, nil
}

// - handler -

// maxDuplicateBodyBytes bounds the duplicate POST. Wider than the shared maxBodyBytes because the
// body legitimately carries one entry per selected line; 64 KiB comfortably covers the 500-line
// bound below with its JSON envelope.
const maxDuplicateBodyBytes = 64 << 10

// maxDuplicateLines bounds the selection payload (the portal reorder's maxReorderLines posture: an
// order never has anywhere near this many lines; this only guards the decoder).
const maxDuplicateLines = 500

// duplicateCreateRequest is the POST body: the chosen source lines (each with an optional adjusted
// quantity) plus the client-generated idempotency key.
type duplicateCreateRequest struct {
	Lines          []DuplicateSelection `json:"lines"`
	IdempotencyKey string               `json:"idempotency_key"`
}

// registerDuplicateRoutes wires the DIB-489/490 endpoints. Called from RegisterRoutes (history.go)
// so the whole order HTTP surface keeps registering through the calls the composition root already
// makes, no main.go change. All three are open to any active in-branch staff (see the file header
// for why duplicate does not need the selling/manager tiers).
func (h *Handler) registerDuplicateRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/orders/{id}/duplicate-preview", g.RequireStaff(h.staff(h.duplicatePreview)))
	r.Handle("POST /api/v1/orders/{id}/duplicate", g.RequireStaff(h.staff(h.duplicateCreate)))
	r.Handle("GET /api/v1/orders/{id}/duplicate-check", g.RequireStaff(h.staff(h.duplicateCheck)))
}

func (h *Handler) duplicatePreview(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	prev, err := h.svc.PreviewDuplicate(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeDuplicateErr(w, err)
		return
	}
	if prev.Lines == nil {
		prev.Lines = []ReorderLine{}
	}
	httpx.WriteJSON(w, http.StatusOK, prev)
}

func (h *Handler) duplicateCreate(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req duplicateCreateRequest
	if r.Body == nil {
		httpx.Error(w, http.StatusBadRequest, "a JSON body is required, send the selected lines and an idempotency_key")
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDuplicateBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "malformed JSON body, send {lines: [{line_no, qty_entered?}], idempotency_key}")
		return
	}
	if len(req.Lines) > maxDuplicateLines {
		httpx.Error(w, http.StatusBadRequest, "too many lines selected, an order cannot carry that many; reload the preview and pick again")
		return
	}
	res, err := h.svc.CreateDuplicate(r.Context(), r.PathValue("id"), req.Lines, req.IdempotencyKey)
	if err != nil {
		h.writeDuplicateErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) duplicateCheck(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	chk, err := h.svc.CheckDuplicates(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeDuplicateErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, chk)
}

// writeDuplicateErr maps the duplicate-specific errors, then falls back to the shared writeErr
// (handler.go) so the authority/not-found answers are byte-identical to the other order endpoints'.
// The unavailable-lines rejection carries the per-line reasons (the portal writeReorderErr shape) so
// the picker can self-correct instead of guessing.
func (h *Handler) writeDuplicateErr(w http.ResponseWriter, err error) {
	var ua *ReorderUnavailableError
	switch {
	case errors.As(err, &ua):
		httpx.ErrorWith(w, http.StatusConflict,
			"some selected lines cannot be duplicated as they are, deselect or adjust the flagged lines and try again",
			map[string]any{"lines": ua.Lines})
	case errors.Is(err, ErrDuplicateNotConfigured), errors.Is(err, ErrEditNotConfigured), errors.Is(err, ErrReorderNotConfigured):
		httpx.Error(w, http.StatusServiceUnavailable,
			"order duplication is not configured on this server (pricing or inventory seams not wired), nothing was created")
	default:
		h.writeErr(w, err)
	}
}
