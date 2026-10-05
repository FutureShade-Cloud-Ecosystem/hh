// loadwriter.go is the product module's write path for the load doors'
// category items (HH-LOAD-1 LD-4). The product_categories table is this
// module's (0002), so the writer lives here and the load frame hands it
// each item inside its own batch transaction, taken by constructor at
// the wiring. It upserts by the category code (the table's own natural
// key), so a load item whose code a staff member already made attaches
// to that row and stamps it the load's own, and a tree lands with its
// parent named by legacy key and resolved by the frame.
//
// The writer's rules, binding: it opens no connection of its own, it
// never commits or rolls back the transaction it is given (the frame's
// savepoint protocol owns that decision), and its errors wrap the
// statement's own (%w) so a Postgres deadlock stays recognisable at the
// batch boundary. Its refusals come back as *load.Refusal and carry
// codes and fields, never a record value.
package product

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
)

// categorySeqCTE is the data modifying CTE the writer's single
// statement prepends: the seq is drawn atomically in the same
// statement, the same shape every module's single statement writers
// use. The scope name is the table's own (the 0213 column), so a sync
// scope declared for the table later reads rows this writer stamped.
const categorySeqCTE = `WITH catseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('product_categories')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// categoryCycleDepth bounds the parent chain walk: a deeper chain is
// not a tree the catalogue serves, and the bound keeps a pathological
// pre existing chain from walking unbounded.
const categoryCycleDepth = 64

// CategoryLoadWriter is the load doors' category writer, the one write
// path a load batch's category items run through.
type CategoryLoadWriter struct{}

// Apply answers one category item: one statement both arms, the create
// arm inserting and the attach arm claiming the row the code already
// names, both stamping the org, drawing one seq and carrying the
// derived key. The parent arrives resolved (the frame refuses a parent
// name not yet loaded before the write); the cycle check walks the
// parent chain after the write and refuses with nothing of the write
// standing, the frame's savepoint discarding the attempted row.
func (CategoryLoadWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	code, _ := in.Record["code"].(string)
	name, _ := in.Record["name"].(string)
	if code == "" {
		return "", &load.Refusal{Code: "field_required", Field: "code",
			Detail: "the record names no category code"}
	}
	if name == "" {
		return "", &load.Refusal{Code: "field_required", Field: "name",
			Detail: "the record names no category name"}
	}
	isActive := true
	if v, ok := in.Record["is_active"].(bool); ok {
		isActive = v
	}
	parent, refused := categoryParent(in)
	if refused != nil {
		return "", refused
	}
	var id string
	if in.RowID != "" {
		// The update arm: the legacy key's recorded row is the identity and
		// the code is content, so a changed code updates that row in place.
		// A code another row already carries returns that row's id without
		// a write, and the frame refuses the item as a conflict naming both
		// legacy keys.
		var other string
		err := tx.QueryRow(ctx,
			`SELECT id::text FROM product_categories WHERE code = $1 AND id <> $2::uuid`, code, in.RowID).Scan(&other)
		if err == nil {
			return other, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("product: category code collision read: %w", err)
		}
		err = tx.QueryRow(ctx, categorySeqCTE+`
			UPDATE product_categories SET
				parent_id = NULLIF($1, '')::uuid, code = $2, name = $3, is_active = $4,
				org_id = $5::uuid, seq = (SELECT next_value FROM catseq),
				idempotency_key = NULLIF($6, '')::uuid, updated_at = now()
			WHERE id = $7::uuid AND (org_id IS NULL OR org_id = $5::uuid)
			RETURNING id::text`,
			parent, code, name, isActive, in.OrgID, in.DerivedKey, in.RowID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", categoryOrgRefusal(in)
		}
		if err != nil {
			return "", fmt.Errorf("product: category load update: %w", err)
		}
	} else {
		err := tx.QueryRow(ctx, categorySeqCTE+`
			INSERT INTO product_categories (parent_id, code, name, is_active, org_id, seq, idempotency_key)
			VALUES (NULLIF($1, '')::uuid, $2, $3, $4, $5::uuid, (SELECT next_value FROM catseq), NULLIF($6, '')::uuid)
			ON CONFLICT (code) DO UPDATE SET
				parent_id = EXCLUDED.parent_id,
				name = EXCLUDED.name,
				is_active = EXCLUDED.is_active,
				org_id = EXCLUDED.org_id,
				seq = EXCLUDED.seq,
				idempotency_key = EXCLUDED.idempotency_key,
				updated_at = now()
			WHERE product_categories.org_id IS NULL OR product_categories.org_id = EXCLUDED.org_id
			RETURNING id::text`,
			parent, code, name, isActive, in.OrgID, in.DerivedKey).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", categoryOrgRefusal(in)
		}
		if err != nil {
			return "", fmt.Errorf("product: category load write: %w", err)
		}
	}
	if parent != "" {
		cycle, deep, err := categoryCycleTx(ctx, tx, id, parent)
		if err != nil {
			return "", fmt.Errorf("product: category cycle check: %w", err)
		}
		if cycle {
			return "", &load.Refusal{Code: "category_cycle", Field: "category",
				Detail: "the parent chain returns to the row the item names; the tree would cycle and nothing of the write stands"}
		}
		if deep {
			return "", &load.Refusal{Code: "category_depth", Field: "category",
				Detail: "the parent chain is deeper than the bound the catalogue serves; the item refuses and nothing of the write stands"}
		}
	}
	return id, nil
}

// categoryOrgRefusal is the refusal for a row another org owns: the
// load never re-stamps a row's org. It names the kind and the key.
func categoryOrgRefusal(in load.Write) *load.Refusal {
	return &load.Refusal{Code: "org_mismatch", Field: "legacy_id",
		Detail: fmt.Sprintf("category legacy key %s names a row another org owns", in.LegacyID)}
}

// categoryParent picks the parent out of the item's resolved
// references: exactly one reference of the category kind names the
// parent, none names a root, and any reference of another kind is the
// wrong reference for this record shape and refuses.
func categoryParent(in load.Write) (string, *load.Refusal) {
	parent := ""
	for _, r := range in.Refs {
		if r.Kind != "category" {
			return "", &load.Refusal{Code: load.ReasonWrongReference, Field: r.Kind,
				Detail: "a category record names no reference of that kind"}
		}
		if parent != "" {
			return "", &load.Refusal{Code: "record_shape", Field: "category",
				Detail: "the item names more than one parent"}
		}
		parent = r.HHID
	}
	return parent, nil
}

// categoryCycleTx walks the parent chain from parentID inside the
// caller's transaction, one read of the module's own table per step.
// cycle reports the chain returning to rowID (self parenthood included);
// deep reports the walk reaching its bound without ending, which the
// caller refuses: the walk fails closed.
func categoryCycleTx(ctx context.Context, tx pgx.Tx, rowID, parentID string) (cycle, deep bool, err error) {
	cur := parentID
	for depth := 0; depth < categoryCycleDepth; depth++ {
		if cur == rowID {
			return true, false, nil
		}
		var next *string
		err := tx.QueryRow(ctx,
			`SELECT parent_id::text FROM product_categories WHERE id = $1::uuid`, cur).Scan(&next)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, false, nil
		}
		if err != nil {
			return false, false, fmt.Errorf("product: category cycle walk: %w", err)
		}
		if next == nil {
			return false, false, nil
		}
		cur = *next
	}
	return false, true, nil
}
