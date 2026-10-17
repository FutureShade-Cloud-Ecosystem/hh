package inventory

// stock_lookup.go, the caller-scoped "have we got any?" read.
//
// WHY THIS EXISTS AS A SEPARATE READ, next to AvailabilityAllBranches rather than instead of it.
//
// AvailabilityAllBranches (visibility.go, DIB-73) answers a DIFFERENT question and answers it
// correctly: "show me this product at EVERY branch, including the ones with none", because a branch
// missing from a cross-branch table reads as a bug rather than as a zero. That read deliberately
// synthesises a row of zeros for a branch with no stock_balances line, and it is deliberately not
// branch-scoped, it is the cross-branch view.
//
// A LOOKUP is the other question: "does this product have any stock on record, in the yards I can
// reach?" and its two empties are NOT the same sentence:
//
//	no such product          → found=false. The reference did not resolve to anything.
//	product, no stock rows   → found=true, zero rows. It exists and has never moved.
//
// That distinction is DIB-443's whole finding, one ticket earlier: a surface that reported an
// ordinary empty state in the voice of a fault. Synthesising zero rows here would erase it, the
// caller could no longer tell "never received" from "received and sold out", and both would read as
// a table of zeros with no explanation. So this read returns only branches that actually carry a
// balance row, and an empty slice means what it says.
//
// BRANCH SCOPE IS A REQUIRED ARGUMENT, not an option and not something re-derived from a context.
// The three cases are the ones w6aBranchScope in the composition root already encodes for the
// dashboard, and the MIDDLE one is the point:
//
//	Global                → no branch filter.
//	granted branches      → only those.
//	NO grants at all      → NOTHING. Not "everything", a caller with no branch access sees no
//	                        yard, which is the exact inversion branch scoping exists to prevent.
//
// NOTHING HERE WRITES. Every function in this file is a read; there is no tx parameter anywhere in
// it by construction.

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/likeutil"
)

// isNoRows is the local spelling of "the query matched nothing", so the three attempts in
// ResolveProductRef read as a cascade rather than as three copies of an errors.Is.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// BranchReach is a caller's branch authority projected onto a read. It is deliberately NOT an
// identity.StaffContext: this module must not import identity, and a read must not be able to
// re-derive an authority that was already decided by the request guard. The composition root builds
// it from the guard-resolved caller and passes it in.
//
// Global true means "every branch" and BranchIDs is then ignored. Global false with an EMPTY
// BranchIDs means "no branches at all", which is a real state (a scoped account with no grants) and
// must resolve to no rows rather than to every row.
type BranchReach struct {
	Global    bool
	BranchIDs []string
}

// branchFilter renders the reach for the `($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))`
// predicate the reads below carry. nil → SQL NULL → predicate short-circuits true.
//
// The empty-array arm is the one that matters: ANY('{}') is false for every row, so a caller with no
// grants measures nothing. Returning nil there would hand them the whole company.
func (r BranchReach) branchFilter() any {
	if r.Global {
		return nil
	}
	if r.BranchIDs == nil {
		return []string{}
	}
	return r.BranchIDs
}

// covers reports whether one branch is inside the reach.
func (r BranchReach) covers(branchID string) bool {
	if r.Global {
		return true
	}
	for _, b := range r.BranchIDs {
		if b == branchID {
			return true
		}
	}
	return false
}

// reachesNothing reports the short-circuit case: a scoped caller with no grants. Answered without a
// round trip, the same way ResolveOrderID short-circuits its own version of this.
func (r BranchReach) reachesNothing() bool { return !r.Global && len(r.BranchIDs) == 0 }

// ProductLookup is what a product reference resolved to, so an answer can name the product back the
// way a person would recognise it instead of echoing whatever was typed.
type ProductLookup struct {
	ProductID string `json:"product_id"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	// UOM is the product's BASE unit, the unit every quantity in stock_balances is expressed in.
	// It travels with the lookup so a consumer cannot pair a quantity with the wrong unit.
	UOM string `json:"uom"`
}

// ErrStockLookupNotConfigured is returned when the persistence seam behind this read is absent (a
// unit-test fake implementing only the base store). It is a wiring state, not a user error, and it
// is distinguishable so a consumer can say "not available here" rather than "something failed".
var ErrStockLookupNotConfigured = errors.New("inventory: stock lookup store not configured")

// stockLookupStore is the optional persistence seam for this read.
//
// A SEPARATE interface rather than a widened `store`, deliberately: `store` is implemented by fakes
// in several test files and (as the codebase's convention has it for editStore in internal/order)
// widening a seam that others implement is a breaking change dressed up as an addition. *Repository
// implements this, so every real deployment takes the real path.
type stockLookupStore interface {
	ResolveProductRef(ctx context.Context, ref string) (ProductLookup, bool, error)
	ProductStockByBranch(ctx context.Context, productID string, reach BranchReach) ([]BranchStock, error)
}

func (s *Service) stockLookupStore() (stockLookupStore, error) {
	ls, ok := s.db().(stockLookupStore)
	if !ok {
		return nil, ErrStockLookupNotConfigured
	}
	return ls, nil
}

// LookupStock resolves a product reference and returns its per-branch position within the caller's
// reach.
//
// The three-valued answer is the contract, and every arm of it is an ANSWER rather than a failure:
//
//	found=false, no rows  → no product matches that reference.
//	found=true,  no rows  → the product exists and has no stock on record in the caller's branches.
//	found=true,  rows     → the position, one row per branch that carries a balance.
//
// The second arm folds two situations together on purpose, "no stock anywhere" and "no stock in
// YOUR yards" are the same sentence to the person asking, and separating them would tell a scoped
// caller that a branch they cannot see is holding some. That is precisely the leak branch scoping
// exists to close, so the ambiguity is the correct behaviour and not an oversight.
func (s *Service) LookupStock(ctx context.Context, reach BranchReach, ref string) (ProductLookup, []BranchStock, bool, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ProductLookup{}, nil, false, badReq("inventory: a product reference is required")
	}
	ls, err := s.stockLookupStore()
	if err != nil {
		return ProductLookup{}, nil, false, err
	}
	p, found, err := ls.ResolveProductRef(ctx, ref)
	if err != nil || !found {
		return ProductLookup{}, nil, false, err
	}
	// A caller with no branch grants gets the product's identity (it resolved, that is not a secret,
	// the catalogue is not branch-scoped) and no positions. Short-circuited rather than queried.
	if reach.reachesNothing() {
		return p, nil, true, nil
	}
	rows, err := ls.ProductStockByBranch(ctx, p.ProductID, reach)
	if err != nil {
		return ProductLookup{}, nil, false, err
	}
	return p, rows, true, nil
}

// - pgx -

// ResolveProductRef resolves what a person typed into one product.
//
// THE ORDER OF THE ATTEMPTS IS THE DESIGN, most specific first, so a lookup is never ambiguous when
// an exact answer exists:
//
//  1. a product UUID         , a deep link or a copied id.
//  2. an exact SKU           , CITEXT, so case is already irrelevant; this is what is printed on a
//     shelf label and what a yard hand actually types.
//  3. a prefix of the name   , the fallback for "blu 60", ordered by SKU so the answer is stable
//     across calls rather than whatever the planner returned first.
//
// A name prefix that matches several products resolves to ONE of them, deterministically. That is a
// deliberate narrowing: this read answers "how many have we got", and a disambiguation list is a
// different surface (the browse screen). The SKU is returned with the answer, so the caller can see
// which product it landed on and retype if it guessed wrong.
//
// INACTIVE PRODUCTS RESOLVE. A discontinued line still has stock in the yard, and "I can't find that
// product" for something sitting on a pallet is the DIB-443 failure in a new costume.
func (r *Repository) ResolveProductRef(ctx context.Context, ref string) (ProductLookup, bool, error) {
	if r.pool == nil {
		return ProductLookup{}, false, errNoDB
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ProductLookup{}, false, nil
	}
	const cols = `p.id::text, p.sku::text, p.name, p.base_uom::text`

	// 1. a product id. Graded in Go before it can reach a uuid column as an uncastable literal -
	// the DIB-443 lesson: a typo must not arrive at the driver and come back as an internal error.
	if id, err := uuid.Parse(ref); err == nil {
		var p ProductLookup
		err := r.pool.QueryRow(ctx, `SELECT `+cols+` FROM products p WHERE p.id = $1::uuid`, id.String()).
			Scan(&p.ProductID, &p.SKU, &p.Name, &p.UOM)
		if err != nil {
			if isNoRows(err) {
				return ProductLookup{}, false, nil
			}
			return ProductLookup{}, false, err
		}
		return p, true, nil
	}

	// 2. an exact SKU.
	var p ProductLookup
	err := r.pool.QueryRow(ctx, `SELECT `+cols+` FROM products p WHERE p.sku = $1::citext`, ref).
		Scan(&p.ProductID, &p.SKU, &p.Name, &p.UOM)
	if err == nil {
		return p, true, nil
	}
	if !isNoRows(err) {
		return ProductLookup{}, false, err
	}

	// 3. a name prefix. LIMIT 1 with an ORDER BY, never a bare LIMIT 1: an unordered limit is a
	// different product on different days for the same question. The reference is escaped under
	// `ESCAPE '\'`, so a typed '%' or '_' matches text and never widens into a wildcard.
	err = r.pool.QueryRow(ctx, `
		SELECT `+cols+`
		  FROM products p
		 WHERE p.name ILIKE $1 || '%' ESCAPE '\'
		 ORDER BY p.is_active DESC, p.sku
		 LIMIT 1`, likeutil.EscapeLikePattern(ref)).Scan(&p.ProductID, &p.SKU, &p.Name, &p.UOM)
	if err != nil {
		if isNoRows(err) {
			return ProductLookup{}, false, nil
		}
		return ProductLookup{}, false, err
	}
	return p, true, nil
}

// ProductStockByBranch reads one product's balance rows, one per branch that carries one, filtered
// to the caller's reach.
//
// It drives off stock_balances (NOT off locations, which is what AvailabilityAllBranches does) so a
// branch with no balance row produces NO row. That is the whole difference between the two reads and
// it is what keeps "this product has never moved" sayable, see the file header.
//
// available is computed in Go by AvailableBase, the ONE definition, exactly as BranchStock does. A
// second `on_hand - reserved` in this SELECT list would be a second thing to forget when the rule
// changes.
func (r *Repository) ProductStockByBranch(ctx context.Context, productID string, reach BranchReach) ([]BranchStock, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if productID == "" {
		return nil, badReq("inventory: product_id is required")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT $1::text AS product_id,
		       l.id::text, l.code::text, l.name,
		       COALESCE(bal.on_hand, 0), COALESCE(bal.reserved, 0),
		       COALESCE(tr.in_transit, 0), COALESCE(soft.qty, 0),
		       COALESCE(pbc.avg_unit_cost_cents, 0)
		  FROM locations l
		  JOIN LATERAL (
		      SELECT SUM(sb.on_hand_base) AS on_hand, SUM(sb.qty_reserved) AS reserved
		        FROM stock_balances sb
		       WHERE sb.product_id = $1::uuid AND sb.branch_id = l.id
		      HAVING count(*) > 0
		  ) bal ON TRUE
		  LEFT JOIN LATERAL (
		      SELECT SUM(tl.qty_shipped_base - tl.qty_received_base) AS in_transit
		        FROM stock_transfer_lines tl
		        JOIN stock_transfers t ON t.id = tl.transfer_id
		       WHERE tl.product_id = $1::uuid
		         AND t.to_branch_id = l.id
		         AND t.status = 'IN_TRANSIT'
		         AND tl.qty_shipped_base > tl.qty_received_base
		  ) tr ON TRUE
		  LEFT JOIN LATERAL (
		      SELECT SUM(sr.qty_base) AS qty
		        FROM stock_reservations sr
		       WHERE sr.product_id = $1::uuid AND sr.branch_id = l.id
		         AND sr.status = 'ACTIVE' AND sr.hold_mode = 'SOFT'
		  ) soft ON TRUE
		  LEFT JOIN product_branch_costs pbc
		         ON pbc.product_id = $1::uuid AND pbc.branch_id = l.id
		 WHERE l.loc_type = 'BRANCH'
		   AND ($2::uuid[] IS NULL OR l.id = ANY($2::uuid[]))
		 ORDER BY l.code`, productID, reach.branchFilter())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BranchStock
	for rows.Next() {
		var b BranchStock
		if err := rows.Scan(&b.ProductID, &b.BranchID, &b.BranchCode, &b.BranchName,
			&b.OnHandBase, &b.ReservedBase, &b.InTransitBase, &b.SoftHeldBase, &b.AvgCostCents); err != nil {
			return nil, err
		}
		b.AvailableBase = AvailableBase(b.OnHandBase, b.ReservedBase)
		out = append(out, b)
	}
	return out, rows.Err()
}
