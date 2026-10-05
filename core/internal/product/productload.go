// productload.go is the product module's write path for the load doors'
// product master items (HH-LOAD-1 LD-5). The products table is this
// module's (0002, the 0018 promoted attributes beside it), so the
// writer lives here and the load frame hands it each item inside its
// own batch transaction, taken by constructor at the wiring. The upsert
// is by the SKU, the table's own natural key, so a load item whose SKU
// a staff member already made attaches to that row and stamps it the
// load's own.
//
// The record is the whole truth of the master row: the SKU and the
// name (required), the descriptive names, the 0018 promoted attributes,
// the dimensions and weight, the metadata object, the active flag, the
// base and price units by catalogue code (the module's own requirement,
// 0002's foreign keys: a code the catalogue does not carry refuses
// naming the field), and the category named by legacy key, which the
// frame resolves before the write (a name not yet loaded refuses as a
// missing reference; LD-4's category door loads first).
//
// THE UNIT SET READING. 0002 relates the set to the product one way:
// product_uoms names products, products does not name product_uoms, and
// no constraint asks a product row to carry a base set row, so a
// product is valid without one (the test kit inserts products with no
// set row and every suite runs). The door therefore creates the product
// alone; LD-3's unit set door replaces the set afterwards, and its own
// rules keep the product's base and price units inside the set it
// lands. The reading is recorded in the item report.
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
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
)

// productSeqCTE is the data modifying CTE the product write prepends:
// the seq is drawn atomically in the same statement from the products
// scope, the scope the module's own staff sync pull reads rows through,
// the same shape every module's single statement writers use.
const productSeqCTE = `WITH pseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('products')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// ProductLoadWriter is the load doors' product writer, the one write
// path a load batch's product master items run through.
type ProductLoadWriter struct{}

// Apply answers one product item. The create arm inserts (or attaches
// the row the SKU already names); the update arm renames the recorded
// row in place, a SKU another row carries refusing as a key conflict
// the frame words with both legacy keys. Both arms stamp the org, draw
// one seq and carry the derived key.
func (ProductLoadWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	sku, _ := in.Record["sku"].(string)
	if sku == "" {
		return "", &load.Refusal{Code: "field_required", Field: "sku",
			Detail: "the record names no sku"}
	}
	name, _ := in.Record["name"].(string)
	if name == "" {
		return "", &load.Refusal{Code: "field_required", Field: "name",
			Detail: "the record names no name"}
	}
	baseUOM, refused := productUnit(in.Record, "base_uom")
	if refused != nil {
		return "", refused
	}
	priceUOM, refused := productUnit(in.Record, "price_uom")
	if refused != nil {
		return "", refused
	}
	// The catalogue is the units' owner: a code it does not carry
	// refuses naming the field, before any write stands.
	for _, code := range []struct{ field, uom string }{{"base_uom", baseUOM}, {"price_uom", priceUOM}} {
		var known bool
		if err := tx.QueryRow(ctx, `SELECT true FROM uoms WHERE code = $1`, code.uom).Scan(&known); errors.Is(err, pgx.ErrNoRows) {
			return "", &load.Refusal{Code: "no_unit_row", Field: code.field,
				Detail: fmt.Sprintf("product legacy key %s names a %s the catalogue does not carry", in.LegacyID, code.field)}
		} else if err != nil {
			return "", fmt.Errorf("product: load unit code read: %w", err)
		}
	}
	category, refused := productCategory(in)
	if refused != nil {
		return "", refused
	}
	isActive := true
	if v, ok := in.Record["is_active"].(bool); ok {
		isActive = v
	}
	description, _ := in.Record["description"].(string)
	manufacturer, _ := in.Record["manufacturer"].(string)
	collection, _ := in.Record["collection"].(string)
	color, _ := in.Record["color"].(string)
	finish, _ := in.Record["finish"].(string)
	application, _ := in.Record["application"].(string)
	colorFamily, _ := in.Record["color_family"].(string)
	texture, _ := in.Record["texture"].(string)
	productSize, _ := in.Record["product_size"].(string)
	soldBy, _ := in.Record["sold_by"].(string)
	isFeatured := false
	if v, ok := in.Record["is_featured"].(bool); ok {
		isFeatured = v
	}
	isSpecialOrder := false
	if v, ok := in.Record["is_special_order"].(bool); ok {
		isSpecialOrder = v
	}
	eventDiscount := false
	if v, ok := in.Record["contractor_pricing_event_discount"].(bool); ok {
		eventDiscount = v
	}
	fullSkid, refused := integerField(in.Record, "full_skid_quantity")
	if refused != nil {
		return "", refused
	}
	layerQty, refused := integerField(in.Record, "layer_quantity")
	if refused != nil {
		return "", refused
	}
	palletCount, refused := integerField(in.Record, "pallet_count")
	if refused != nil {
		return "", refused
	}
	minOrderQty, refused := decimalField(in.Record, "minimum_order_qty")
	if refused != nil {
		return "", refused
	}
	weight, refused := decimalField(in.Record, "weight_per_unit_kg")
	if refused != nil {
		return "", refused
	}
	dimensions, _ := in.Record["dimensions_lwh"].(string)
	meta, refused := recordObject(in.Record, "metadata")
	if refused != nil {
		return "", refused
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("product: load metadata encode: %w", err)
	}

	var id string
	if in.RowID != "" {
		// The update arm: the legacy key's recorded row is the identity
		// and the SKU is content, so a changed SKU updates that row in
		// place. A SKU another row already carries returns that row's id
		// without a write, and the frame refuses the item as a conflict
		// naming both legacy keys.
		var other string
		err := tx.QueryRow(ctx,
			`SELECT id::text FROM products WHERE sku = $1 AND id <> $2::uuid`, sku, in.RowID).Scan(&other)
		if err == nil {
			return other, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("product: load sku collision read: %w", err)
		}
		err = tx.QueryRow(ctx, productSeqCTE+`
			UPDATE products SET
				sku = $1, name = $2, description = $3, category_id = NULLIF($4, '')::uuid,
				base_uom = $5, price_uom = $6,
				manufacturer = $7, collection = $8, color = $9, finish = $10, application = $11,
				color_family = $12, texture = $13, product_size = $14, sold_by = $15,
				is_featured = $16, is_special_order = $17, contractor_pricing_event_discount = $18,
				full_skid_quantity = $19, layer_quantity = $20, minimum_order_qty = $21,
				dimensions_lwh = $22, pallet_count = $23, weight_per_unit_kg = $24,
				is_active = $25, metadata = $26::jsonb,
				org_id = $27::uuid, seq = (SELECT next_value FROM pseq),
				idempotency_key = NULLIF($28, '')::uuid, updated_at = now()
			WHERE id = $29::uuid AND (org_id IS NULL OR org_id = $27::uuid)
			RETURNING id::text`,
			sku, name, description, category,
			baseUOM, priceUOM,
			manufacturer, collection, color, finish, application,
			colorFamily, texture, productSize, soldBy,
			isFeatured, isSpecialOrder, eventDiscount,
			fullSkid, layerQty, minOrderQty,
			dimensions, palletCount, weight,
			isActive, string(metaJSON),
			in.OrgID, in.DerivedKey, in.RowID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", productOrgRefusal(in)
		}
		if err != nil {
			return "", fmt.Errorf("product: load update: %w", err)
		}
	} else {
		err := tx.QueryRow(ctx, productSeqCTE+`
			INSERT INTO products (sku, name, description, category_id, base_uom, price_uom,
				manufacturer, collection, color, finish, application,
				color_family, texture, product_size, sold_by,
				is_featured, is_special_order, contractor_pricing_event_discount,
				full_skid_quantity, layer_quantity, minimum_order_qty,
				dimensions_lwh, pallet_count, weight_per_unit_kg,
				is_active, metadata, org_id, seq, idempotency_key)
			VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6,
				$7, $8, $9, $10, $11,
				$12, $13, $14, $15,
				$16, $17, $18,
				$19, $20, $21,
				$22, $23, $24,
				$25, $26::jsonb, $27::uuid, (SELECT next_value FROM pseq), NULLIF($28, '')::uuid)
			ON CONFLICT (sku) DO UPDATE SET
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				category_id = EXCLUDED.category_id,
				base_uom = EXCLUDED.base_uom,
				price_uom = EXCLUDED.price_uom,
				manufacturer = EXCLUDED.manufacturer,
				collection = EXCLUDED.collection,
				color = EXCLUDED.color,
				finish = EXCLUDED.finish,
				application = EXCLUDED.application,
				color_family = EXCLUDED.color_family,
				texture = EXCLUDED.texture,
				product_size = EXCLUDED.product_size,
				sold_by = EXCLUDED.sold_by,
				is_featured = EXCLUDED.is_featured,
				is_special_order = EXCLUDED.is_special_order,
				contractor_pricing_event_discount = EXCLUDED.contractor_pricing_event_discount,
				full_skid_quantity = EXCLUDED.full_skid_quantity,
				layer_quantity = EXCLUDED.layer_quantity,
				minimum_order_qty = EXCLUDED.minimum_order_qty,
				dimensions_lwh = EXCLUDED.dimensions_lwh,
				pallet_count = EXCLUDED.pallet_count,
				weight_per_unit_kg = EXCLUDED.weight_per_unit_kg,
				is_active = EXCLUDED.is_active,
				metadata = EXCLUDED.metadata,
				org_id = EXCLUDED.org_id,
				seq = EXCLUDED.seq,
				idempotency_key = EXCLUDED.idempotency_key,
				updated_at = now()
			WHERE products.org_id IS NULL OR products.org_id = EXCLUDED.org_id
			RETURNING id::text`,
			sku, name, description, category, baseUOM, priceUOM,
			manufacturer, collection, color, finish, application,
			colorFamily, texture, productSize, soldBy,
			isFeatured, isSpecialOrder, eventDiscount,
			fullSkid, layerQty, minOrderQty,
			dimensions, palletCount, weight,
			isActive, string(metaJSON), in.OrgID, in.DerivedKey).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", productOrgRefusal(in)
		}
		if err != nil {
			return "", fmt.Errorf("product: load write: %w", err)
		}
	}
	return id, nil
}

// productOrgRefusal is the refusal for a row another org owns: the
// load never re-stamps a row's org. It names the kind and the key.
func productOrgRefusal(in load.Write) *load.Refusal {
	return &load.Refusal{Code: "org_mismatch", Field: "legacy_id",
		Detail: fmt.Sprintf("product legacy key %s names a row another org owns", in.LegacyID)}
}

// productUnit reads one unit code field as text, required, non empty.
func productUnit(rec map[string]any, field string) (string, *load.Refusal) {
	code, _ := rec[field].(string)
	if code == "" {
		return "", &load.Refusal{Code: "field_required", Field: field,
			Detail: "the record names no unit code there"}
	}
	return code, nil
}

// productCategory picks the category reference out of the item's
// resolved references: at most one reference of the category kind
// names the category (a product may carry none), any reference of
// another kind is the wrong reference for this record shape, and two
// category references are a record shape refusal.
func productCategory(in load.Write) (string, *load.Refusal) {
	category := ""
	for _, r := range in.Refs {
		if r.Kind != "category" {
			return "", &load.Refusal{Code: load.ReasonWrongReference, Field: r.Kind,
				Detail: "a product record names no reference of that kind"}
		}
		if category != "" {
			return "", &load.Refusal{Code: "record_shape", Field: "category",
				Detail: "the item names more than one category"}
		}
		category = r.HHID
	}
	return category, nil
}

// integerField reads one record number as an optional whole integer
// count: the wire decodes numbers as float64 and a Go caller may carry
// an int, and both land the same. A fraction or a type that names no
// number refuses naming the field.
func integerField(rec map[string]any, field string) (*int, *load.Refusal) {
	raw, ok := rec[field]
	if !ok || raw == nil {
		return nil, nil
	}
	var v int64
	switch n := raw.(type) {
	case int:
		v = int64(n)
	case int64:
		v = n
	case float64:
		if n != float64(int64(n)) {
			return nil, &load.Refusal{Code: "record_shape", Field: field,
				Detail: "the field is not a whole number"}
		}
		v = int64(n)
	default:
		return nil, &load.Refusal{Code: "record_shape", Field: field,
			Detail: "the field is not a whole number"}
	}
	out := int(v)
	return &out, nil
}

// decimalField reads one record field as optional exact decimal text
// for a DECIMAL(19,4) column: strict decimal text in, or a number
// spelled back out with no exponent and no lost digits, and a scale
// past the column's four decimals refused naming the field, never
// rounded silently.
func decimalField(rec map[string]any, field string) (*string, *load.Refusal) {
	raw, ok := rec[field]
	if !ok || raw == nil {
		return nil, nil
	}
	text, isText := raw.(string)
	if !isText {
		num, isNum := raw.(float64)
		if !isNum {
			return nil, &load.Refusal{Code: "record_shape", Field: field,
				Detail: "the field is not a decimal"}
		}
		text = strconv.FormatFloat(num, 'f', -1, 64)
	}
	body := strings.TrimPrefix(strings.TrimPrefix(text, "-"), "+")
	intPart, fracPart, _ := strings.Cut(body, ".")
	if intPart == "" && fracPart == "" {
		return nil, &load.Refusal{Code: "record_shape", Field: field,
			Detail: "the field is not a decimal"}
	}
	for _, c := range intPart + fracPart {
		if c < '0' || c > '9' {
			return nil, &load.Refusal{Code: "record_shape", Field: field,
				Detail: "the field is not a decimal"}
		}
	}
	trimmed := strings.TrimRight(fracPart, "0")
	if len(trimmed) > 4 {
		return nil, &load.Refusal{Code: "field_scale", Field: field,
			Detail: "the field carries a fifth decimal place or beyond; the door refuses it rather than rounding it"}
	}
	if len(strings.TrimLeft(intPart, "0")) > 15 {
		return nil, &load.Refusal{Code: "field_scale", Field: field,
			Detail: "the field is wider than the column's integer digits; the door refuses it rather than truncating it"}
	}
	return &text, nil
}

// recordObject reads one record field as an object, the empty map when
// the field is absent or null, a refusal when it is anything else.
func recordObject(rec map[string]any, field string) (map[string]any, *load.Refusal) {
	raw, ok := rec[field]
	if !ok || raw == nil {
		return map[string]any{}, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, &load.Refusal{Code: "record_shape", Field: field,
			Detail: "the field is not an object"}
	}
	return obj, nil
}
