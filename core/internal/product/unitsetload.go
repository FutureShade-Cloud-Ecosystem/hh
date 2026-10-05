// unitsetload.go is the product module's write path for the load doors'
// unit set items (HH-LOAD-1 LD-3). The product_uoms table is this
// module's (0002: one base row at factor one, every factor positive,
// the checks at the table), so the writer lives here and the load frame
// hands it each item inside its own batch transaction, taken by
// constructor at the wiring.
//
// The item is one product's unit set, and the record is the whole
// truth: the door REPLACES the product's set atomically, the old rows
// deleted and the new rows inserted inside the item's own transaction,
// so a refused shape leaves the old set standing untouched. The set's
// rules, each refusing with nothing of the replace standing: exactly
// one base row at factor one; every factor positive and at scale eight
// or under, a factor beyond refused naming the legacy key and the
// field, never a value, never rounded (the plan's section 8 point 3);
// every code naming a catalogue unit; the product's base and price
// units inside the set; no unit removed while a price, break, rule or
// stock row still names it (checked through the owning modules' read
// seams beside their own tables: the price and break and rule rows
// through the pricing module's reader, the stock rows through the
// inventory module's reader, the product module's own rounding rules
// read here); and the decimal round trip on every conversion, one sell
// unit to base and back, in exact decimal.
//
// The product is named by legacy key through the registry (the frame
// resolves the reference before any write; a name not yet loaded
// refuses as a missing reference naming the kind and the key). The
// product door of LD-5 does not exist yet, so the test kit and the
// frame's own registry are the only way a product name stands, and this
// door resolves it through no module read of its own.
//
// The writer's rules, binding: it opens no connection of its own, it
// never commits or rolls back the transaction it is given, and its
// errors wrap the statement's own (%w) so a Postgres deadlock stays
// recognisable at the batch boundary. Its refusals come back as
// *load.Refusal and carry codes and fields, never a record value.
package product

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
)

// maxSetRows bounds one set's row list: a unit set is a handful of
// conversions, and the bound keeps a stray record from landing an
// unbounded replace. Over the bound refuses as a record shape.
const maxSetRows = 64

// PricingUnitUseReader is the pricing module's read the unit set door
// checks a removal through: whether any pricing row of this product
// (the list price, the rules, the quantity break headers and items, the
// conversion edges) still names the unit. The consumer declares the
// narrow interface; the concrete reader is pricing.LoadedUnitUseReader.
type PricingUnitUseReader interface {
	ProductUnitInUseTx(ctx context.Context, tx pgx.Tx, productID, uom string) (bool, error)
}

// InventoryUnitUseReader is the inventory module's read the unit set
// door checks a removal through: whether any stock row of this product
// still names the unit. The concrete reader is
// inventory.LoadedUnitUseReader.
type InventoryUnitUseReader interface {
	ProductUnitInUseTx(ctx context.Context, tx pgx.Tx, productID, uom string) (bool, error)
}

// UnitSetLoadWriter is the load doors' unit set writer, the one write
// path a load batch's unit set items run through. The pricing and stock
// readers arrive by constructor: a nil reader fails the removal check
// loudly at the first ask, never as a silent clear.
type UnitSetLoadWriter struct {
	Pricing PricingUnitUseReader
	Stock   InventoryUnitUseReader
}

// Apply answers one unit set item: the reference of kind product names
// the product, the record's rows are the set. The replace runs delete
// then insert on the item's transaction; the frame's savepoint discards
// both when the item refuses anywhere.
func (w UnitSetLoadWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	productID, refused := setProduct(in)
	if refused != nil {
		return "", refused
	}
	rows, refused := setRows(in)
	if refused != nil {
		return "", refused
	}
	if len(rows) == 0 {
		return "", &load.Refusal{Code: "record_shape", Field: "rows",
			Detail: fmt.Sprintf("unit set legacy key %s names no rows; the record is the whole truth", in.LegacyID)}
	}
	if len(rows) > maxSetRows {
		return "", &load.Refusal{Code: "record_shape", Field: "rows",
			Detail: "the set names more rows than a unit set carries"}
	}
	// The factor gate: exact decimals, positive, scale eight or under,
	// the base row single and at one.
	baseCount := 0
	codes := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		if r.code == "" {
			return "", &load.Refusal{Code: "field_required", Field: "code",
				Detail: "a row of the set names no unit code"}
		}
		key := lowercaseCode(r.code)
		if seen[key] {
			return "", &load.Refusal{Code: "record_shape", Field: "code",
				Detail: "the set names one unit code twice"}
		}
		seen[key] = true
		codes = append(codes, r.code)
		if _, refused := parseUnitFactor(r.qty); refused != nil {
			refused.Detail = fmt.Sprintf("unit set legacy key %s: %s", in.LegacyID, refused.Detail)
			return "", refused
		}
		if refused := decimalRoundTrip(r.qty); refused != nil {
			refused.Detail = fmt.Sprintf("unit set legacy key %s: %s", in.LegacyID, refused.Detail)
			return "", refused
		}
		if r.isBase {
			baseCount++
			parsed, refused := parseUnitFactor(r.qty)
			if refused != nil {
				return "", refused
			}
			if parsed.value.Cmp(newBigRatOne()) != 0 {
				return "", &load.Refusal{Code: "unit_set_base", Field: "rows",
					Detail: fmt.Sprintf("unit set legacy key %s: the base row's factor is not one", in.LegacyID)}
			}
		}
	}
	if baseCount != 1 {
		return "", &load.Refusal{Code: "unit_set_base", Field: "rows",
			Detail: fmt.Sprintf("unit set legacy key %s: the set carries %d base rows, want exactly one", in.LegacyID, baseCount)}
	}
	// Every code names a catalogue unit.
	for _, code := range codes {
		var known bool
		if err := tx.QueryRow(ctx, `SELECT true FROM uoms WHERE code = $1`, code).Scan(&known); errors.Is(err, pgx.ErrNoRows) {
			return "", &load.Refusal{Code: "no_unit_row", Field: "code",
				Detail: "a row of the set names a code the catalogue does not carry"}
		} else if err != nil {
			return "", fmt.Errorf("product: unit set code read: %w", err)
		}
	}
	// The product's base and price units ride inside the set.
	var baseUOM, priceUOM string
	err := tx.QueryRow(ctx, `SELECT base_uom::text, price_uom::text FROM products WHERE id = $1::uuid`, productID).
		Scan(&baseUOM, &priceUOM)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", &load.Refusal{Code: load.ReasonUpdateTargetMissing, Field: "product",
			Detail: fmt.Sprintf("unit set legacy key %s names a product row that is gone", in.LegacyID)}
	}
	if err != nil {
		return "", fmt.Errorf("product: unit set product read: %w", err)
	}
	if !seen[lowercaseCode(baseUOM)] {
		return "", &load.Refusal{Code: "unit_set_base", Field: "base_uom",
			Detail: "the product's base unit is not inside the set"}
	}
	if !seen[lowercaseCode(priceUOM)] {
		return "", &load.Refusal{Code: "unit_set_base", Field: "price_uom",
			Detail: "the product's price unit is not inside the set"}
	}
	// No unit removed while a price, break, rule or stock row still
	// names it: the rows the set drops, checked through the owning
	// modules' readers and this module's own rounding rules. The refusal
	// names the field and the legacy key, never the code it would drop.
	current, err := tx.Query(ctx, `SELECT uom_code::text FROM product_uoms WHERE product_id = $1::uuid`, productID)
	if err != nil {
		return "", fmt.Errorf("product: unit set current read: %w", err)
	}
	removed := []string{}
	for current.Next() {
		var code string
		if err := current.Scan(&code); err != nil {
			current.Close()
			return "", fmt.Errorf("product: unit set current scan: %w", err)
		}
		if !seen[lowercaseCode(code)] {
			removed = append(removed, code)
		}
	}
	current.Close()
	if err := current.Err(); err != nil {
		return "", fmt.Errorf("product: unit set current walk: %w", err)
	}
	for _, code := range removed {
		var named bool
		if err := tx.QueryRow(ctx,
			`SELECT true FROM product_rounding_rules WHERE product_id = $1::uuid AND sell_increment_uom = $2`,
			productID, code).Scan(&named); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("product: unit set rounding rule read: %w", err)
		} else if named {
			return "", &load.Refusal{Code: "unit_in_use", Field: "code",
				Detail: fmt.Sprintf("unit set legacy key %s drops a unit the product's rounding rule still names; nothing of the replace stands", in.LegacyID)}
		}
		if w.Pricing == nil {
			return "", fmt.Errorf("product: the unit set door carries no pricing reader; a removal cannot be checked")
		}
		inUse, err := w.Pricing.ProductUnitInUseTx(ctx, tx, productID, code)
		if err != nil {
			return "", fmt.Errorf("product: unit set pricing use read: %w", err)
		}
		if inUse {
			return "", &load.Refusal{Code: "unit_in_use", Field: "code",
				Detail: fmt.Sprintf("unit set legacy key %s drops a unit a price, break, rule or conversion row still names; nothing of the replace stands", in.LegacyID)}
		}
		if w.Stock == nil {
			return "", fmt.Errorf("product: the unit set door carries no stock reader; a removal cannot be checked")
		}
		inUse, err = w.Stock.ProductUnitInUseTx(ctx, tx, productID, code)
		if err != nil {
			return "", fmt.Errorf("product: unit set stock use read: %w", err)
		}
		if inUse {
			return "", &load.Refusal{Code: "unit_in_use", Field: "code",
				Detail: fmt.Sprintf("unit set legacy key %s drops a unit a stock row still names; nothing of the replace stands", in.LegacyID)}
		}
	}
	// The replace: the old rows gone, the new rows in, both on the
	// item's transaction.
	if _, err := tx.Exec(ctx, `DELETE FROM product_uoms WHERE product_id = $1::uuid`, productID); err != nil {
		return "", fmt.Errorf("product: unit set replace delete: %w", err)
	}
	for _, r := range rows {
		hints := r.roleHints
		if hints == nil {
			hints = []string{}
		}
		// ON CONFLICT DO UPDATE: two batches replacing one product's set
		// race on the primary key's index entries (each transaction's
		// delete cannot see the other's uncommitted insert), and the
		// loser's insert converges on the winner's row instead of
		// erroring the batch.
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base, role_hints, org_id, idempotency_key)
			VALUES ($1::uuid, $2, $3::numeric, $4, $5::text[], $6::uuid, NULLIF($7, '')::uuid)
			ON CONFLICT (product_id, uom_code) DO UPDATE SET
				qty_in_base = EXCLUDED.qty_in_base,
				is_base = EXCLUDED.is_base,
				role_hints = EXCLUDED.role_hints,
				org_id = EXCLUDED.org_id,
				idempotency_key = EXCLUDED.idempotency_key,
				updated_at = now()`,
			productID, r.code, r.qty, r.isBase, hints, in.OrgID, in.DerivedKey); err != nil {
			return "", fmt.Errorf("product: unit set replace insert: %w", err)
		}
	}
	return productID, nil
}

// setRow is one parsed row of the set record.
type setRowRecord struct {
	code      string
	qty       string
	isBase    bool
	roleHints []string
}

// setProduct picks the product reference out of the item's resolved
// references: exactly one reference of the product kind, any other kind
// is the wrong reference for this record shape.
func setProduct(in load.Write) (string, *load.Refusal) {
	productID := ""
	for _, r := range in.Refs {
		if r.Kind != "product" {
			return "", &load.Refusal{Code: load.ReasonWrongReference, Field: r.Kind,
				Detail: "a unit set record names no reference of that kind"}
		}
		if productID != "" {
			return "", &load.Refusal{Code: "record_shape", Field: "product",
				Detail: "the item names more than one product"}
		}
		productID = r.HHID
	}
	if productID == "" {
		return "", &load.Refusal{Code: "record_shape", Field: "product",
			Detail: "the item names no product reference; the set is named by the product's legacy key"}
	}
	return productID, nil
}

// setRows reads the record's rows: an array of objects, each naming the
// code, the factor as decimal text or a number, the base flag and, if
// named, the role hints as text strings. The wire decodes the array as
// []any of map[string]any; a Go caller through the frame may carry
// []map[string]any, and both land the same.
func setRows(in load.Write) ([]setRowRecord, *load.Refusal) {
	raw, ok := in.Record["rows"]
	if !ok || raw == nil {
		return nil, &load.Refusal{Code: "record_shape", Field: "rows",
			Detail: "the record names no rows"}
	}
	list := []any{}
	switch rows := raw.(type) {
	case []any:
		list = rows
	case []map[string]any:
		list = make([]any, len(rows))
		for i, r := range rows {
			list[i] = r
		}
	default:
		return nil, &load.Refusal{Code: "record_shape", Field: "rows",
			Detail: "the rows are not an array"}
	}
	out := make([]setRowRecord, 0, len(list))
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, &load.Refusal{Code: "record_shape", Field: "rows",
				Detail: "a row of the set is not an object"}
		}
		row := setRowRecord{}
		row.code, _ = obj["code"].(string)
		qtyText, isText := obj["qty_in_base"].(string)
		if !isText {
			if qtyNum, ok := obj["qty_in_base"].(float64); ok {
				qtyText = strconv.FormatFloat(qtyNum, 'f', -1, 64)
			} else {
				return nil, &load.Refusal{Code: "record_shape", Field: "qty_in_base",
					Detail: "a row of the set names no factor"}
			}
		}
		row.qty = qtyText
		if v, ok := obj["is_base"].(bool); ok {
			row.isBase = v
		}
		if hints, ok := obj["role_hints"].([]any); ok {
			for _, h := range hints {
				s, ok := h.(string)
				if !ok {
					return nil, &load.Refusal{Code: "record_shape", Field: "role_hints",
						Detail: "a role hint is not text"}
				}
				row.roleHints = append(row.roleHints, s)
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// lowercaseCode folds a unit code the way CITEXT compares: the case
// fold for the set's own duplicate and membership checks.
func lowercaseCode(code string) string {
	b := []byte(code)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// newBigRatOne is the exact one the base factor compares against.
func newBigRatOne() *big.Rat {
	return big.NewRat(1, 1)
}
