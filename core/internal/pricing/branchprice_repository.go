// branchprice_repository.go: the pgx half of the branch price editor surface
// (HH-LOAD-1 LD-7). Zero-trust: pricing's own tables only, with the products
// and locations reads living inside the statements as FK targets and the org
// stamp read off the product, exactly as the module's anchor write path
// (applyMatrixPrice) does. The set and the delete each open ONE transaction
// in the repository (the ApplyBatch precedent), every statement inside runs
// on that transaction, and every error wraps with %w so a deadlock or a
// serialisation failure is recognised by the caller.
package pricing

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// branchPriceSeqCTE is the data-modifying CTE the branch price write prepends:
// the seq is drawn atomically in the same statement, the priceSeqCTE shape.
// Gaps from a no-op are allowed by the 0213 convention.
const branchPriceSeqCTE = `WITH bseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('` + branchPriceSyncScope + `')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// BranchListPrice reads one product's live branch price at one branch; nil
// when none stands (the resolver falls back to the dealer-wide anchor). A
// tombstoned row is not a live price.
func (r *Repository) BranchListPrice(ctx context.Context, productID, branchID string) (*ListPrice, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	var lp ListPrice
	err := r.pool.QueryRow(ctx,
		`SELECT product_id::text, list_price_cents, price_uom::text FROM pricing_branch_prices
		  WHERE product_id = $1 AND branch_id = $2 AND deleted_at IS NULL`,
		productID, branchID).Scan(&lp.ProductID, &lp.ListPriceCents, &lp.PriceUOM)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("branch list price: %w", err)
	}
	return &lp, nil
}

const branchPriceCols = `product_id::text, branch_id::text, list_price_cents, price_uom::text, updated_at`

func scanBranchPrice(row pgx.Row) (BranchPrice, error) {
	var bp BranchPrice
	if err := row.Scan(&bp.ProductID, &bp.BranchID, &bp.ListPriceCents, &bp.PriceUOM, &bp.UpdatedAt); err != nil {
		return BranchPrice{}, err
	}
	return bp, nil
}

// ListBranchPrices answers the live rows in (product_id, branch_id) order,
// the list's stable key, past the caller's resume key when one is carried.
func (r *Repository) ListBranchPrices(ctx context.Context, f BranchPriceFilter) ([]BranchPrice, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT ` + branchPriceCols + ` FROM pricing_branch_prices
	       WHERE deleted_at IS NULL
	         AND ($1 = '' OR product_id = $1::uuid)
	         AND ($2 = '' OR branch_id = $2::uuid)
	         AND ($3 = '' OR (product_id::text || '/' || branch_id::text) > $3)
	       ORDER BY product_id, branch_id
	       LIMIT $4`
	rows, err := r.pool.Query(ctx, q, f.ProductID, f.BranchID, f.AfterKey, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("list branch prices: %w", err)
	}
	defer rows.Close()
	out := []BranchPrice{}
	for rows.Next() {
		bp, err := scanBranchPrice(rows)
		if err != nil {
			return nil, fmt.Errorf("list branch prices: %w", err)
		}
		out = append(out, bp)
	}
	return out, rows.Err()
}

// PutBranchPrice sets one product's price at one branch in ONE transaction:
// the prior row read, the upsert (org stamped off the product, seq drawn from
// the branch_prices allocator, a tombstone revived), and the change log row
// naming the branch, before and after. An identical re-set of a live row is
// the value-equal arm: nothing is written and nothing is drawn, so a replayed
// set cannot inflate the trail. The row's own updated_at is the read back
// truth, not a Go clock.
func (r *Repository) PutBranchPrice(ctx context.Context, productID, branchID string, in BranchPriceWrite, userID string) (BranchPrice, error) {
	if r.pool == nil {
		return BranchPrice{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return BranchPrice{}, fmt.Errorf("branch price put: %w", err)
	}
	defer tx.Rollback(ctx)

	var priorCents *int64
	var priorUOM string
	var priorDeleted bool
	err = tx.QueryRow(ctx,
		`SELECT list_price_cents, price_uom::text, (deleted_at IS NOT NULL) FROM pricing_branch_prices
		  WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
		productID, branchID).Scan(&priorCents, &priorUOM, &priorDeleted)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return BranchPrice{}, fmt.Errorf("branch price prior read: %w", err)
	}
	if found && !priorDeleted && priorCents != nil && *priorCents == in.ListPriceCents && priorUOM == in.PriceUOM {
		bp, err := scanBranchPrice(tx.QueryRow(ctx,
			`SELECT `+branchPriceCols+` FROM pricing_branch_prices
			  WHERE product_id = $1::uuid AND branch_id = $2::uuid`, productID, branchID))
		if err != nil {
			return BranchPrice{}, fmt.Errorf("branch price read back: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return BranchPrice{}, fmt.Errorf("branch price put: %w", err)
		}
		return bp, nil
	}

	var bp BranchPrice
	err = tx.QueryRow(ctx, branchPriceSeqCTE+`
		INSERT INTO pricing_branch_prices (product_id, branch_id, list_price_cents, price_uom, org_id, seq, deleted_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::citext,
		        (SELECT org_id FROM products WHERE id = $1::uuid),
		        (SELECT next_value FROM bseq), NULL)
		ON CONFLICT (product_id, branch_id) DO UPDATE SET
			list_price_cents = EXCLUDED.list_price_cents,
			price_uom        = EXCLUDED.price_uom,
			updated_at       = now(),
			seq              = EXCLUDED.seq,
			deleted_at       = NULL
		RETURNING `+branchPriceCols,
		productID, branchID, in.ListPriceCents, in.PriceUOM).Scan(
		&bp.ProductID, &bp.BranchID, &bp.ListPriceCents, &bp.PriceUOM, &bp.UpdatedAt)
	if err != nil {
		if isFKViolation(err) {
			return BranchPrice{}, errFKViolation // nonexistent product, branch or unit -> 422
		}
		return BranchPrice{}, fmt.Errorf("branch price upsert: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO price_change_log (product_id, branch_id, field, before_cents, after_cents, changed_by)
		 VALUES ($1::uuid, $2::uuid, 'retail_cents', $3, $4, NULLIF($5,'')::uuid)`,
		productID, branchID, priorCents, in.ListPriceCents, userID); err != nil {
		return BranchPrice{}, fmt.Errorf("branch price trail: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return BranchPrice{}, fmt.Errorf("branch price put: %w", err)
	}
	return bp, nil
}

// DeleteBranchPrice tombstones one product's branch price in ONE transaction:
// deleted_at set, the row retained, a fresh seq (the module's delete shape,
// the matrix adapter's), and the change log row carrying the price it took
// away (after NULL). An absent or already deleted row is errNotFound.
func (r *Repository) DeleteBranchPrice(ctx context.Context, productID, branchID, userID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("branch price delete: %w", err)
	}
	defer tx.Rollback(ctx)

	var seq int64
	var before *int64
	err = tx.QueryRow(ctx, `
		WITH bseq AS (
			INSERT INTO scope_sequences (scope) VALUES ('`+branchPriceSyncScope+`')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value
		)
		UPDATE pricing_branch_prices SET
			deleted_at = now(),
			updated_at = now(),
			seq        = (SELECT next_value FROM bseq)
		WHERE product_id = $1::uuid AND branch_id = $2::uuid AND deleted_at IS NULL
		RETURNING seq, list_price_cents`,
		productID, branchID).Scan(&seq, &before)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("branch price tombstone: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO price_change_log (product_id, branch_id, field, before_cents, after_cents, changed_by)
		 VALUES ($1::uuid, $2::uuid, 'retail_cents', $3, NULL, NULLIF($4,'')::uuid)`,
		productID, branchID, before, userID); err != nil {
		return fmt.Errorf("branch price trail: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("branch price delete: %w", err)
	}
	return nil
}
