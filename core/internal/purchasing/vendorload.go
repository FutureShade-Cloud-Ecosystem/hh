// vendorload.go is the purchasing module's write path for the load
// doors' supplier items (HH-LOAD-1 LD-4). The vendors table is this
// module's (0011) and the ordering contact's store is the module's own
// (purchasing_vendor_contacts, 0197), so both writes live here and the
// load frame hands the writer each item inside its own batch
// transaction, taken by constructor at the wiring. The upsert is by the
// vendor number, the table's own natural key: the source's natural key
// as text, which is the item's legacy id, so a load item whose vendor
// number a staff member already made attaches to that row and stamps it
// the load's own.
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
	"encoding/json"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
)

// vendorSeqCTE is the data modifying CTE the vendor write prepends: the
// seq is drawn atomically in the same statement from the vendors scope,
// the scope the module's own vendors pull reads rows through, the same
// shape the purchase order writes use.
const vendorSeqCTE = `WITH vseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('vendors')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// VendorLoadWriter is the load doors' supplier writer, the one write
// path a load batch's supplier items run through: the vendor record
// with its 0011 columns, and the ordering contact through the module's
// own contact store.
type VendorLoadWriter struct{}

// Apply answers one supplier item. The record carries the name
// (required) and, named, the terms in days, the EDI flag, the remit
// address, the active flag, the free form metadata, and the ordering
// contact (contact name, email, phone). A contact that names neither an
// email nor a phone refuses: the contact store's own check keeps an
// unreachable desk out, and the writer says so before the write rather
// than letting the statement fail.
func (VendorLoadWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	name, _ := in.Record["name"].(string)
	if name == "" {
		return "", &load.Refusal{Code: "field_required", Field: "name",
			Detail: "the record names no supplier name"}
	}
	if len(in.Refs) > 0 {
		return "", &load.Refusal{Code: load.ReasonWrongReference, Field: in.Refs[0].Kind,
			Detail: "a supplier record names no references"}
	}
	terms := 30
	if raw, ok := in.Record["payment_terms_days"]; ok && raw != nil {
		days, whole := numberField(raw)
		if !whole {
			return "", &load.Refusal{Code: "record_shape", Field: "payment_terms_days",
				Detail: "the terms in days are not a whole number"}
		}
		terms = days
	}
	isEDI := false
	if v, ok := in.Record["is_edi"].(bool); ok {
		isEDI = v
	}
	isActive := true
	if v, ok := in.Record["is_active"].(bool); ok {
		isActive = v
	}
	remit, refused := recordObject(in.Record, "remit_address")
	if refused != nil {
		return "", refused
	}
	meta, refused := recordObject(in.Record, "metadata")
	if refused != nil {
		return "", refused
	}
	remitJSON, err := json.Marshal(remit)
	if err != nil {
		return "", fmt.Errorf("purchasing: vendor remit encode: %w", err)
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("purchasing: vendor metadata encode: %w", err)
	}
	contact, refused := vendorContact(in)
	if refused != nil {
		return "", refused
	}
	var id string
	err = tx.QueryRow(ctx, vendorSeqCTE+`
		INSERT INTO vendors (vendor_no, name, payment_terms_days, is_edi, remit_address, is_active, metadata, org_id, seq, idempotency_key)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7::jsonb, $8::uuid, (SELECT next_value FROM vseq), NULLIF($9, '')::uuid)
		ON CONFLICT (vendor_no) DO UPDATE SET
			name = EXCLUDED.name,
			payment_terms_days = EXCLUDED.payment_terms_days,
			is_edi = EXCLUDED.is_edi,
			remit_address = EXCLUDED.remit_address,
			is_active = EXCLUDED.is_active,
			metadata = EXCLUDED.metadata,
			org_id = EXCLUDED.org_id,
			seq = EXCLUDED.seq,
			idempotency_key = EXCLUDED.idempotency_key,
			updated_at = now()
		RETURNING id::text`,
		in.LegacyID, name, terms, isEDI, string(remitJSON), isActive, string(metaJSON),
		in.OrgID, in.DerivedKey).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("purchasing: vendor load write: %w", err)
	}
	if contact != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO purchasing_vendor_contacts (vendor_id, contact_name, email, phone)
			VALUES ($1::uuid, $2, $3, $4)
			ON CONFLICT (vendor_id) DO UPDATE SET
				contact_name = EXCLUDED.contact_name,
				email = EXCLUDED.email,
				phone = EXCLUDED.phone,
				updated_at = now()`,
			id, contact.name, contact.email, contact.phone); err != nil {
			return "", fmt.Errorf("purchasing: vendor contact write: %w", err)
		}
	}
	return id, nil
}

// vendorContact is the record's named contact, nil when the record
// names none. Both channels empty is the refusal: the contact store
// keeps one reachable desk per vendor.
type vendorContactRow struct {
	name, email, phone string
}

func vendorContact(in load.Write) (*vendorContactRow, *load.Refusal) {
	raw, ok := in.Record["contact"]
	if !ok || raw == nil {
		return nil, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, &load.Refusal{Code: "record_shape", Field: "contact",
			Detail: "the contact is not an object"}
	}
	c := &vendorContactRow{
		name:  stringField(obj, "contact_name"),
		email: stringField(obj, "email"),
		phone: stringField(obj, "phone"),
	}
	if c.email == "" && c.phone == "" {
		return nil, &load.Refusal{Code: "contact_unreachable", Field: "contact",
			Detail: "the contact names neither an email nor a phone; the order desk must be reachable"}
	}
	return c, nil
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

// stringField reads one object field as text, the empty string when it
// is absent or null.
func stringField(obj map[string]any, field string) string {
	s, _ := obj[field].(string)
	return s
}

// numberField reads a record number as a whole count of days: the wire
// decodes numbers as float64 and a Go caller may carry an int, and both
// must land the same. A fraction or a type that names no number is not
// a whole number of days.
func numberField(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}
