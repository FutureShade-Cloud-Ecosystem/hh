// supplierskuload.go is the purchasing module's write path for the load
// doors' supplier SKU items (HH-LOAD-1 LD-5). The purchasing_vendor_skus
// table is this module's (0197: the vendor's own catalogue vocabulary,
// one row per vendor and product), so the writer lives here and the
// load frame hands it each item inside its own batch transaction, taken
// by constructor at the wiring.
//
// WHAT THE TABLE CARRIES. 0197 carries three content columns and no
// more: the vendor's own code (vendor_sku, bounded text), and the pack
// pair all or nothing (vendor_uom with qty_per_vendor_uom, the table's
// own CHECK refusing a unit with no factor or a factor with no unit).
// The table carries no cost column, so the door lands none: an opening
// cost has no way in at this head (the plan's risk 9), and a cost field
// a caller sends is refused naming the field rather than dropped.
//
// THE KEYS. The product and the supplier each arrive by legacy key and
// the frame resolves them against the registry before any write (a name
// not yet loaded refuses as a missing reference naming the kind and the
// key). The row's own identity is the vendor and product pair: the
// table's primary key, no uuid column, so the registry names the row by
// a v5 style uuid derived from the pair (SupplierSKURowID), the units
// door's derived row id precedent (stable, one per pair, both
// directions of the registry's uniqueness holding).
//
// NO 0213 STAMPS. 0213's column passes name purchasing_vendor_skus on
// no list, so the row carries no org, seq or idempotency column: the
// org rides through the vendor row the supplier door stamped, and the
// writer draws no sequence and stamps no key. A vendor row another org
// owns refuses as org_mismatch naming the kind and the key; the load
// never re-stamps a row's org.
//
// The writer's rules, binding: it opens no connection of its own, it
// never commits or rolls back the transaction it is given (the frame's
// savepoint protocol owns that decision), and its errors wrap the
// statement's own (%w) so a Postgres deadlock stays recognisable at the
// batch boundary. Its refusals come back as *load.Refusal and carry
// codes and fields, never a record value.
package purchasing

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
)

// maxVendorSKULength is the 0197 column's own bound; a longer code
// refuses naming the field rather than reaching the constraint.
const maxVendorSKULength = 64

// supplierSkuRowNamespace is the v5 derivation's namespace: the uuid
// the string names hashes to, fixed so the same pair derives the same
// row id on every deployment.
const supplierSkuRowNamespace = "hh:load:supplier-sku:v1"

// SupplierSKURowID derives the row's registry id from the vendor and
// product pair: a v5 style uuid (sha1 over the namespace and the two
// ids, version and variant bits set), stable across deployments, one
// per pair. The table keys its rows by the pair and carries no uuid
// column, so this derived id is what the registry's hh_id names; the
// reading is recorded in the item report.
func SupplierSKURowID(vendorID, productID string) string {
	h := sha1.New()
	h.Write([]byte(supplierSkuRowNamespace))
	h.Write([]byte{0})
	h.Write([]byte(vendorID))
	h.Write([]byte{0})
	h.Write([]byte(productID))
	sum := h.Sum(nil)
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(sum[:16])
	return strings.Join([]string{
		hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32],
	}, "-")
}

// UnitCodeReader is the product module's read the writer checks a pack
// unit through: whether the catalogue carries the code, inside the
// frame's transaction. The consumer declares the narrow interface it
// needs; the concrete reader is the product package's
// UnitCatalogueReader. A nil reader fails the check loudly at the first
// ask, never as a silent clear.
type UnitCodeReader interface {
	UnitCodeKnownTx(ctx context.Context, tx pgx.Tx, code string) (bool, error)
}

// SupplierSKULoadWriter is the load doors' supplier SKU writer, the one
// write path a load batch's supplier SKU items run through. The unit
// catalogue's reader arrives by constructor: the catalogue is the
// product module's, and the fence rule sends the read through its
// interface.
type SupplierSKULoadWriter struct {
	Units UnitCodeReader
}

// Apply answers one supplier SKU item. The record names the vendor's
// code and, as a pair, the selling unit and the base units one of them
// holds; a reload whose record names no pair clears the pair (the
// record is the whole truth, the pair's columns nullable). Both arms
// resolve the row by the vendor and product pair the references name.
func (w SupplierSKULoadWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	// The table carries no cost column, and a cost field a caller sends
	// is refused naming the field rather than dropped: a silent drop
	// would let the caller believe a cost landed (the plan's risk 9).
	// The scan matches any key carrying cost on purpose: the neighbours
	// name their columns unit_cost_cents and cost_uom (po lines, stock
	// moves), but a record's cost spelling is the sender's, and an exact
	// list would silently drop every spelling it fails to name, the very
	// harm the refusal exists to prevent. Over refusing costs one loud
	// record_shape on a record the caller fixes; under refusing costs a
	// number nobody sees was never landed.
	for _, key := range sortedRecordKeys(in.Record) {
		if strings.Contains(strings.ToLower(key), "cost") {
			return "", &load.Refusal{Code: "record_shape", Field: key,
				Detail: fmt.Sprintf("supplier sku legacy key %s names a cost field; the table carries no cost column and the field is refused rather than dropped", in.LegacyID)}
		}
	}
	vendorID, productID, refused := supplierSkuRefs(in)
	if refused != nil {
		return "", refused
	}
	vendorSKU, _ := in.Record["vendor_sku"].(string)
	if vendorSKU == "" {
		return "", &load.Refusal{Code: "field_required", Field: "vendor_sku",
			Detail: "the record names no vendor sku"}
	}
	if len(vendorSKU) > maxVendorSKULength {
		return "", &load.Refusal{Code: "record_shape", Field: "vendor_sku",
			Detail: fmt.Sprintf("supplier sku legacy key %s names a code over the bound the table carries", in.LegacyID)}
	}
	uom, qty, refused := vendorSkuPack(in)
	if refused != nil {
		return "", refused
	}
	if uom != "" {
		// The pair's unit is a catalogue code, checked through the
		// product module's own read: a code the catalogue does not carry
		// refuses naming the field, before the pair's completeness or
		// any write stands.
		if w.Units == nil {
			return "", fmt.Errorf("purchasing: the supplier sku door carries no unit catalogue reader; a pack unit cannot be checked")
		}
		known, err := w.Units.UnitCodeKnownTx(ctx, tx, uom)
		if err != nil {
			return "", fmt.Errorf("purchasing: vendor sku unit read: %w", err)
		}
		if !known {
			return "", &load.Refusal{Code: "no_unit_row", Field: "vendor_uom",
				Detail: fmt.Sprintf("supplier sku legacy key %s names a unit the catalogue does not carry", in.LegacyID)}
		}
	}
	if uom == "" && qty != "" {
		return "", &load.Refusal{Code: "record_shape", Field: "vendor_uom",
			Detail: fmt.Sprintf("supplier sku legacy key %s names half a pack pair; the unit and the factor stand or fall together", in.LegacyID)}
	}
	if uom != "" && qty == "" {
		return "", &load.Refusal{Code: "record_shape", Field: "qty_per_vendor_uom",
			Detail: fmt.Sprintf("supplier sku legacy key %s names half a pack pair; the unit and the factor stand or fall together", in.LegacyID)}
	}
	// The vendor row is the org's own or unclaimed: a row another org
	// owns refuses, the load never re-stamping a row's org.
	var vendorOrg *string
	if err := tx.QueryRow(ctx,
		`SELECT org_id::text FROM vendors WHERE id = $1::uuid`, vendorID).Scan(&vendorOrg); err != nil {
		return "", fmt.Errorf("purchasing: vendor sku vendor read: %w", err)
	}
	if vendorOrg != nil && *vendorOrg != in.OrgID {
		return "", &load.Refusal{Code: "org_mismatch", Field: "supplier",
			Detail: fmt.Sprintf("supplier legacy key %s names a vendor row another org owns", supplierLegacyID(in))}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO purchasing_vendor_skus (vendor_id, product_id, vendor_sku, vendor_uom, qty_per_vendor_uom)
		VALUES ($1::uuid, $2::uuid, $3, NULLIF($4, '')::citext, NULLIF($5, '')::numeric)
		ON CONFLICT (vendor_id, product_id) DO UPDATE SET
			vendor_sku = EXCLUDED.vendor_sku,
			vendor_uom = EXCLUDED.vendor_uom,
			qty_per_vendor_uom = EXCLUDED.qty_per_vendor_uom,
			updated_at = now()`,
		vendorID, productID, vendorSKU, uom, qty); err != nil {
		return "", fmt.Errorf("purchasing: vendor sku load write: %w", err)
	}
	return SupplierSKURowID(vendorID, productID), nil
}

// sortedRecordKeys is the record's keys in sorted order, so the cost
// refusal names the same field regardless of the map's walk order.
func sortedRecordKeys(rec map[string]any) []string {
	keys := make([]string, 0, len(rec))
	for k := range rec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// supplierLegacyID names the item's supplier reference in a refusal, or
// the item's own key when the shape named none.
func supplierLegacyID(in load.Write) string {
	for _, r := range in.Refs {
		if r.Kind == "supplier" {
			return r.LegacyID
		}
	}
	return in.LegacyID
}

// supplierSkuRefs picks the pair out of the item's resolved references:
// exactly one reference of the supplier kind and one of the product
// kind; any other kind is the wrong reference for this record shape,
// and a missing or doubled member is a record shape refusal naming the
// field.
func supplierSkuRefs(in load.Write) (vendorID, productID string, refused *load.Refusal) {
	for _, r := range in.Refs {
		switch r.Kind {
		case "supplier":
			if vendorID != "" {
				return "", "", &load.Refusal{Code: "record_shape", Field: "supplier",
					Detail: "the item names more than one supplier"}
			}
			vendorID = r.HHID
		case "product":
			if productID != "" {
				return "", "", &load.Refusal{Code: "record_shape", Field: "product",
					Detail: "the item names more than one product"}
			}
			productID = r.HHID
		default:
			return "", "", &load.Refusal{Code: load.ReasonWrongReference, Field: r.Kind,
				Detail: "a supplier sku record names no reference of that kind"}
		}
	}
	if vendorID == "" {
		return "", "", &load.Refusal{Code: "record_shape", Field: "supplier",
			Detail: "the item names no supplier reference; the row is keyed by the supplier's legacy key"}
	}
	if productID == "" {
		return "", "", &load.Refusal{Code: "record_shape", Field: "product",
			Detail: "the item names no product reference; the row is keyed by the product's legacy key"}
	}
	return vendorID, productID, nil
}

// vendorSkuPack reads the record's pack pair members: the unit code and
// the base units one of them holds, as decimal text or a number. The
// factor checks run here (positive, at the column's four decimals and
// inside its width, a fifth place or a wider integer part refused
// naming the field, never rounded); the pair's completeness is the
// caller's, so a named unit meets the catalogue check before the pair
// refuses.
func vendorSkuPack(in load.Write) (uom, qty string, refused *load.Refusal) {
	rawUOM, hasUOM := in.Record["vendor_uom"]
	rawQty, hasQty := in.Record["qty_per_vendor_uom"]
	if hasUOM {
		uom, _ = rawUOM.(string)
	}
	if hasQty && rawQty != nil {
		switch n := rawQty.(type) {
		case string:
			qty = n
		case float64:
			qty = strconv.FormatFloat(n, 'f', -1, 64)
		default:
			return "", "", &load.Refusal{Code: "record_shape", Field: "qty_per_vendor_uom",
				Detail: fmt.Sprintf("supplier sku legacy key %s names a factor that is not a decimal", in.LegacyID)}
		}
	}
	if qty == "" {
		return uom, "", nil
	}
	// The column carries factors above zero only (0197's CHECK), and a
	// negative sign refuses here, before the insert reaches it: a
	// batch wide constraint error would roll the batch's other items
	// back with it.
	if strings.HasPrefix(qty, "-") {
		return "", "", &load.Refusal{Code: "record_shape", Field: "qty_per_vendor_uom",
			Detail: fmt.Sprintf("supplier sku legacy key %s names a negative factor; the column carries factors above zero", in.LegacyID)}
	}
	body := strings.TrimPrefix(strings.TrimPrefix(qty, "-"), "+")
	intPart, fracPart, _ := strings.Cut(body, ".")
	if intPart == "" && fracPart == "" {
		return "", "", &load.Refusal{Code: "record_shape", Field: "qty_per_vendor_uom",
			Detail: fmt.Sprintf("supplier sku legacy key %s names a factor that is not a decimal", in.LegacyID)}
	}
	for _, c := range intPart + fracPart {
		if c < '0' || c > '9' {
			return "", "", &load.Refusal{Code: "record_shape", Field: "qty_per_vendor_uom",
				Detail: fmt.Sprintf("supplier sku legacy key %s names a factor that is not a decimal", in.LegacyID)}
		}
	}
	if !strings.ContainsAny(body, "123456789") {
		return "", "", &load.Refusal{Code: "record_shape", Field: "qty_per_vendor_uom",
			Detail: fmt.Sprintf("supplier sku legacy key %s names a factor that is not above zero", in.LegacyID)}
	}
	if len(strings.TrimRight(fracPart, "0")) > 4 {
		return "", "", &load.Refusal{Code: "field_scale", Field: "qty_per_vendor_uom",
			Detail: fmt.Sprintf("supplier sku legacy key %s names a fifth decimal place or beyond; the door refuses it rather than rounding it", in.LegacyID)}
	}
	if len(strings.TrimLeft(intPart, "0")) > 15 {
		return "", "", &load.Refusal{Code: "field_scale", Field: "qty_per_vendor_uom",
			Detail: fmt.Sprintf("supplier sku legacy key %s names a factor wider than the column's integer digits; the door refuses it rather than truncating it", in.LegacyID)}
	}
	return uom, qty, nil
}
