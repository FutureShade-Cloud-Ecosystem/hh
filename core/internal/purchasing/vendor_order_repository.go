package purchasing

// vendor_order_repository.go, pgx over the four tables migration 0197 adds: the purchasing policy,
// the vendor's ordering contact, the vendor's catalogue mapping, and the send record.
//
// Same posture as repository.go: a nil pool is tolerated and every method returns errNoDB, no vendor
// table is JOINed (the vendor's NAME still comes through the VendorDirectory seam), and the only
// non-purchasing table touched is `products`, for the SKU a mapping editor has to show beside the
// vendor's code, the identical LEFT JOIN poLineCols already performs, and for the identical reason.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// - the policy -

// Policy resolves a branch's row, else the dealer default (the NULL branch_id row), else the seeded
// default in code. NO ROW IS NOT AN ERROR: a deployment whose 0197 seed was removed still reads.
func (r *Repository) Policy(ctx context.Context, branchID string) (Policy, error) {
	if r.pool == nil {
		return Policy{}, errNoDB
	}
	// ORDER BY the branch match first, LIMIT 1: one round trip resolves the override chain, and the
	// chain's precedence is stated in the SQL rather than assembled in Go from two reads.
	var p Policy
	var branch *string
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(branch_id::text, ''), cost_visibility, untagged_fallback,
		       (branch_id IS NOT NULL)
		  FROM purchasing_policy
		 WHERE branch_id IS NULL OR branch_id = NULLIF($1,'')::uuid
		 ORDER BY (branch_id IS NOT NULL) DESC
		 LIMIT 1`, branchID).Scan(&branch, &p.CostVisibility, &p.UntaggedFallback, &p.IsBranchOverride)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultPolicy(), nil
	}
	if err != nil {
		return Policy{}, fmt.Errorf("purchasing: read policy: %w", err)
	}
	if branch != nil {
		p.BranchID = *branch
	}
	return p, nil
}

// UpsertPolicy writes the dealer default (empty branch) or one branch's override.
//
// The dealer-default row has a NULL branch_id, and a UNIQUE index does not treat two NULLs as equal,
// so ON CONFLICT cannot address it. The two arms below are that fact, written out rather than
// worked around with a sentinel branch id, a sentinel would be a fake location row that every
// other reader of `locations` would then have to know about.
func (r *Repository) UpsertPolicy(ctx context.Context, branchID, visibility, fallback string) error {
	if r.pool == nil {
		return errNoDB
	}
	if branchID == "" {
		tag, err := r.pool.Exec(ctx, `
			UPDATE purchasing_policy
			   SET cost_visibility = $1, untagged_fallback = $2, updated_at = now()
			 WHERE branch_id IS NULL`, visibility, fallback)
		if err != nil {
			return fmt.Errorf("purchasing: set dealer policy: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		_, err = r.pool.Exec(ctx, `
			INSERT INTO purchasing_policy (branch_id, cost_visibility, untagged_fallback)
			VALUES (NULL, $1, $2)`, visibility, fallback)
		if err != nil {
			return fmt.Errorf("purchasing: seed dealer policy: %w", err)
		}
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO purchasing_policy (branch_id, cost_visibility, untagged_fallback)
		VALUES ($1::uuid, $2, $3)
		ON CONFLICT (branch_id) DO UPDATE
		   SET cost_visibility = EXCLUDED.cost_visibility,
		       untagged_fallback = EXCLUDED.untagged_fallback,
		       updated_at = now()`, branchID, visibility, fallback)
	if err != nil {
		return fmt.Errorf("purchasing: set branch policy: %w", err)
	}
	return nil
}

// - the ordering contact -

// VendorContact reads the tagged ordering desk. `found=false` with a nil error is the ordinary
// answer for every vendor nobody has tagged yet, and the send path turns it into words.
func (r *Repository) VendorContact(ctx context.Context, vendorID string) (VendorContact, bool, error) {
	if r.pool == nil {
		return VendorContact{}, false, errNoDB
	}
	if vendorID == "" {
		return VendorContact{}, false, nil
	}
	var c VendorContact
	err := r.pool.QueryRow(ctx, `
		SELECT vendor_id::text, contact_name, email, phone, to_char(updated_at,'YYYY-MM-DD"T"HH24:MI:SSOF')
		  FROM purchasing_vendor_contacts WHERE vendor_id = $1::uuid`, vendorID).
		Scan(&c.VendorID, &c.Name, &c.Email, &c.Phone, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return VendorContact{}, false, nil
	}
	if err != nil {
		return VendorContact{}, false, fmt.Errorf("purchasing: read vendor contact: %w", err)
	}
	return c, true, nil
}

// UpsertVendorContact tags or retags the ordering desk. One row per vendor by primary key.
func (r *Repository) UpsertVendorContact(ctx context.Context, c VendorContact, actorID string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO purchasing_vendor_contacts (vendor_id, contact_name, email, phone, tagged_by)
		VALUES ($1::uuid, $2, $3, $4, NULLIF($5,'')::uuid)
		ON CONFLICT (vendor_id) DO UPDATE
		   SET contact_name = EXCLUDED.contact_name, email = EXCLUDED.email,
		       phone = EXCLUDED.phone, tagged_by = EXCLUDED.tagged_by, updated_at = now()`,
		c.VendorID, c.Name, c.Email, c.Phone, actorID)
	if err != nil {
		return fmt.Errorf("purchasing: tag vendor contact: %w", err)
	}
	return nil
}

// DeleteVendorContact untags the desk. Sends that already went out keep their stored recipient_kind.
func (r *Repository) DeleteVendorContact(ctx context.Context, vendorID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM purchasing_vendor_contacts WHERE vendor_id = $1::uuid`, vendorID); err != nil {
		return fmt.Errorf("purchasing: untag vendor contact: %w", err)
	}
	return nil
}

// - the vendor's catalogue vocabulary -

// VendorSKUs lists one vendor's mappings, newest catalogue name first so the editing surface can
// show what it is about to change without a second read.
func (r *Repository) VendorSKUs(ctx context.Context, vendorID string) ([]VendorSKU, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if vendorID == "" {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.vendor_id::text, m.product_id::text, COALESCE(p.sku::text,''), COALESCE(p.name,''),
		       m.vendor_sku, COALESCE(m.vendor_uom::text,''),
		       COALESCE(m.qty_per_vendor_uom,0)::float8,
		       to_char(m.updated_at,'YYYY-MM-DD"T"HH24:MI:SSOF')
		  FROM purchasing_vendor_skus m
		  LEFT JOIN products p ON p.id = m.product_id
		 WHERE m.vendor_id = $1::uuid
		 ORDER BY COALESCE(p.sku::text,''), m.product_id
		 LIMIT 2000`, vendorID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: list vendor skus: %w", err)
	}
	defer rows.Close()
	var out []VendorSKU
	for rows.Next() {
		var m VendorSKU
		if err := rows.Scan(&m.VendorID, &m.ProductID, &m.SKU, &m.ProductName,
			&m.VendorSKU, &m.VendorUOM, &m.QtyPerVendorUOM, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("purchasing: scan vendor sku: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpsertVendorSKU records the vendor's code and selling unit for one product. The unit pair is
// written as a pair (both NULL or both set) because the 0197 CHECK will not accept anything else.
func (r *Repository) UpsertVendorSKU(ctx context.Context, m VendorSKU) error {
	if r.pool == nil {
		return errNoDB
	}
	var uom any
	var factor any
	if m.VendorUOM != "" && m.QtyPerVendorUOM > 0 {
		uom, factor = m.VendorUOM, m.QtyPerVendorUOM
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO purchasing_vendor_skus (vendor_id, product_id, vendor_sku, vendor_uom, qty_per_vendor_uom)
		VALUES ($1::uuid, $2::uuid, $3, $4::citext, $5::numeric)
		ON CONFLICT (vendor_id, product_id) DO UPDATE
		   SET vendor_sku = EXCLUDED.vendor_sku, vendor_uom = EXCLUDED.vendor_uom,
		       qty_per_vendor_uom = EXCLUDED.qty_per_vendor_uom, updated_at = now()`,
		m.VendorID, m.ProductID, m.VendorSKU, uom, factor)
	if err != nil {
		return fmt.Errorf("purchasing: map vendor sku: %w", err)
	}
	return nil
}

// DeleteVendorSKU removes one mapping.
func (r *Repository) DeleteVendorSKU(ctx context.Context, vendorID, productID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM purchasing_vendor_skus WHERE vendor_id = $1::uuid AND product_id = $2::uuid`,
		vendorID, productID); err != nil {
		return fmt.Errorf("purchasing: unmap vendor sku: %w", err)
	}
	return nil
}

// - the order, through the item-5 seam -

// POHeader and POLinesFor are one-line delegates onto the reads repository.go already has. They
// exist so orderStore has a production implementation without a second query, the point of the
// seam is a test that needs no database, not a second definition of what a purchase order is.
func (r *Repository) POHeader(ctx context.Context, poID string) (PO, error) {
	return r.GetPO(ctx, poID)
}

func (r *Repository) POLinesFor(ctx context.Context, poID string) ([]POLine, error) {
	return r.POLines(ctx, nil, poID, false)
}

// - the send record -

const poSendCols = `id::text, po_id::text, channel, recipient, recipient_kind, fallback_reason,
	status, provider, provider_ref, detail, document_name, document_bytes, idempotency_key, sent_at`

func scanPOSend(row pgx.Row) (POSend, error) {
	var s POSend
	err := row.Scan(&s.ID, &s.POID, &s.Channel, &s.Recipient, &s.RecipientKind, &s.FallbackReason,
		&s.Status, &s.Provider, &s.ProviderRef, &s.Detail, &s.DocumentName, &s.DocumentBytes,
		&s.IdempotencyKey, &s.SentAt)
	return s, err
}

// InsertPOSend writes the send row BEFORE the transport is called. See SendPO for why the order is
// not negotiable.
func (r *Repository) InsertPOSend(ctx context.Context, in POSend, actorID string) (POSend, error) {
	if r.pool == nil {
		return POSend{}, errNoDB
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO purchasing_po_sends
		  (po_id, channel, recipient, recipient_kind, fallback_reason, status, provider,
		   document_name, document_bytes, idempotency_key, sent_by)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11,'')::uuid)
		RETURNING `+poSendCols,
		in.POID, in.Channel, in.Recipient, in.RecipientKind, in.FallbackReason,
		in.Status, in.Provider, in.DocumentName, in.DocumentBytes, in.IdempotencyKey, actorID)
	out, err := scanPOSend(row)
	if err != nil {
		return POSend{}, fmt.Errorf("purchasing: record po send: %w", err)
	}
	return out, nil
}

// MarkPOSendResult finalises the row with what the transport said. On this deployment that is
// always FAILED with "no transport is wired on this server".
func (r *Repository) MarkPOSendResult(ctx context.Context, id, status, provider, ref, detail string) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.pool.Exec(ctx, `
		UPDATE purchasing_po_sends
		   SET status = $2, provider = $3, provider_ref = left($4, 128), detail = left($5, 300)
		 WHERE id = $1::uuid`, id, status, provider, ref, detail); err != nil {
		return fmt.Errorf("purchasing: finalise po send: %w", err)
	}
	return nil
}

// FindPOSendByKey is the idempotency read: an identical retry reuses its row. The read is keyed to THIS
// order because the key index is global (one idempotency key, one send, across every order), so a key
// held by another order is a collision the insert refuses, not a replay this read may hand back.
func (r *Repository) FindPOSendByKey(ctx context.Context, key, poID string) (POSend, bool, error) {
	if r.pool == nil {
		return POSend{}, false, errNoDB
	}
	if key == "" {
		return POSend{}, false, nil
	}
	out, err := scanPOSend(r.pool.QueryRow(ctx,
		`SELECT `+poSendCols+` FROM purchasing_po_sends WHERE idempotency_key = $1 AND po_id = $2::uuid`, key, poID))
	if errors.Is(err, pgx.ErrNoRows) {
		return POSend{}, false, nil
	}
	if err != nil {
		return POSend{}, false, fmt.Errorf("purchasing: find po send: %w", err)
	}
	return out, true, nil
}

// ListPOSends is one order's send history, newest first.
func (r *Repository) ListPOSends(ctx context.Context, poID string) ([]POSend, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+poSendCols+` FROM purchasing_po_sends WHERE po_id = $1::uuid
		 ORDER BY sent_at DESC LIMIT 100`, poID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: list po sends: %w", err)
	}
	defer rows.Close()
	var out []POSend
	for rows.Next() {
		var s POSend
		if err := rows.Scan(&s.ID, &s.POID, &s.Channel, &s.Recipient, &s.RecipientKind,
			&s.FallbackReason, &s.Status, &s.Provider, &s.ProviderRef, &s.Detail,
			&s.DocumentName, &s.DocumentBytes, &s.IdempotencyKey, &s.SentAt); err != nil {
			return nil, fmt.Errorf("purchasing: scan po send: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// - the echoed reference -

// vendorRefMatchSQL folds a purchase-order number to the form a vendor can echo back.
//
// ONE EXPRESSION, APPLIED TO BOTH SIDES, and that is the whole design. `#PO-TRT-4` comes back from a
// vendor as "PO TRT 4", "po-trt-4", "POTRT4" or with the hash eaten by a mail client, and an AP clerk
// holding the invoice should not have to find the order by eye. Folding both the stored number and
// the typed string through the SAME expression means there is exactly one definition of "the same
// number"; doing the folding in Go and the storage in SQL would be two, and two definitions of
// sameness drift the first time somebody adds a separator.
//
// NO FUNCTIONAL INDEX BACKS IT, deliberately. The register is bounded (the list read itself caps at
// 500) and this is a rare, human-initiated lookup; an index whose expression must match this one
// character for character in order to be used is a drift hazard bought for no measured gain.
const vendorRefMatchSQL = `upper(translate(%s, '#-_. /\', ''))`

// POByVendorRef finds the order a vendor quoted back. AMBIGUITY IS REFUSED HERE TOO: if two orders
// fold to the same string the caller is told so rather than handed one of them, because "we found
// your order" pointing at the wrong branch's PO is worse than "say which one".
func (r *Repository) POByVendorRef(ctx context.Context, ref string) (PO, error) {
	if r.pool == nil {
		return PO{}, errNoDB
	}
	sql := fmt.Sprintf(`
		SELECT `+poCols+`
		  FROM purchasing_pos p
		 WHERE %s = %s
		 LIMIT 2`,
		fmt.Sprintf(vendorRefMatchSQL, "p.po_no::text"), fmt.Sprintf(vendorRefMatchSQL, "$1::text"))
	rows, err := r.pool.Query(ctx, sql, ref)
	if err != nil {
		return PO{}, fmt.Errorf("purchasing: lookup po by vendor ref: %w", err)
	}
	defer rows.Close()
	var found []PO
	for rows.Next() {
		var p PO
		if err := rows.Scan(&p.ID, &p.PONo, &p.VendorID, &p.BranchID, &p.Status,
			&p.ExpectedAt, &p.OrderedAt, &p.Notes, &p.CreatedAt, &p.UpdatedAt,
			&p.Currency, &p.FXRate, &p.DeliveryMethod); err != nil {
			return PO{}, fmt.Errorf("purchasing: scan po by vendor ref: %w", err)
		}
		found = append(found, p)
	}
	if err := rows.Err(); err != nil {
		return PO{}, fmt.Errorf("purchasing: lookup po by vendor ref: %w", err)
	}
	switch len(found) {
	case 0:
		return PO{}, errNotFound
	case 1:
		return found[0], nil
	default:
		return PO{}, conflict(
			"purchasing: more than one order reads as %q, open it from the order list instead", ref)
	}
}

// compile-time proof that *Repository satisfies every seam this file backs. Without these, an
// interface a test fakes could drift away from the production implementation and nothing would say
// so until a deployment tried to read a policy at 7am.
var (
	_ orderStore     = (*Repository)(nil)
	_ policyStore    = (*Repository)(nil)
	_ sendStore      = (*Repository)(nil)
	_ vendorSKUStore = (*Repository)(nil)
)
