// Package attachments owns the D10 canonical attachment entity and its
// attachment_link polymorphism (decision D10, migration 0214): one file
// record whose bytes live in Appwrite Storage and never in this row, and
// one polymorphic link that attaches a file to orders, plans, stops,
// receiving and quotes without per-owner columns.
//
// The module exists because the tables need an owner for the sync surface
// (HH-3.5 #166): the attachments scope is last_write_wins_by_field, the
// yard-pick kit's way of carrying a photo or a proof taken in a yard with
// no signal. The registration row (kind, file id, bucket, mime, size,
// checksum, links) syncs; the BYTES do not and cannot: no payload field
// admits them, the writable sets refuse anything shaped like content, and
// the projection carries exactly the metadata legs the contract's
// Attachment schema pins.
//
// The frozen schema gives this module no append-only audit table of its
// own, so its idempotency story is the row's single idempotency_key
// column (the D30 shape) plus two rules: the delete leg never restamps
// the column (the engine's tombstone arm answers a replayed delete from
// the retained row), and a write whose every declared leg already equals
// the row's state is a no-op that draws no seq and restamps nothing, so
// a replayed operation that already landed converges on the unchanged
// row instead of churning the feed. Two replay shapes are not smooth
// without a history table the schema does not give: an outbox holding
// create-then-update on ONE attachment row replays its create leg as
// rejected (the row exists under the update's key), and an outbox
// holding two DIFFERENT sequential updates on one row replays them in
// order, converging on the same values through fresh seqs. Both leave
// the server state's values identical and are documented here and in the
// lane's residuals; the tasks module solves them with task_events
// because it owns one, and when the schema door opens again this module
// takes the same shape.
package attachments

import (
	"fmt"
	"strings"
	"time"
)

// Kinds (D10, the 0214 CHECK and the contract's Attachment.kind enum).
const (
	KindPhoto           = "photo"
	KindProofOfDelivery = "proof_of_delivery"
	KindDocument        = "document"
	KindSignature       = "signature"
	KindProductImage    = "product_image"
)

// validKind reports whether k is one of D10's kinds.
func validKind(k string) bool {
	switch k {
	case KindPhoto, KindProofOfDelivery, KindDocument, KindSignature, KindProductImage:
		return true
	}
	return false
}

// Owner kinds (D10's polymorphism, the contract's AttachmentLink enum).
const (
	OwnerOrder     = "order"
	OwnerPlan      = "plan"
	OwnerStop      = "stop"
	OwnerReceiving = "receiving"
	OwnerQuote     = "quote"
)

// validOwnerKind reports whether k is one of D10's owner kinds.
func validOwnerKind(k string) bool {
	switch k {
	case OwnerOrder, OwnerPlan, OwnerStop, OwnerReceiving, OwnerQuote:
		return true
	}
	return false
}

// Link is one attachment_link row: the polymorphic edge from one file to
// one owner, the contract's AttachmentLink schema.
type Link struct {
	AttachmentID string `json:"attachment_id"`
	OwnerKind    string `json:"owner_kind"`
	OwnerID      string `json:"owner_id"`
	Role         string `json:"role"`
}

// Attachment is the D10 record, the contract's Attachment schema with its
// links nested: the synced shape the attachments scope carries. Bytes
// stay in Appwrite Storage; appwrite_file_id names them there.
type Attachment struct {
	ID             string     `json:"id"`
	OrgID          string     `json:"org_id"`
	TenantID       *string    `json:"tenant_id"`
	Kind           string     `json:"kind"`
	AppwriteFileID string     `json:"appwrite_file_id"`
	Bucket         string     `json:"bucket"`
	Mime           string     `json:"mime,omitempty"`
	SizeBytes      *int64     `json:"size_bytes,omitempty"`
	Checksum       string     `json:"checksum,omitempty"`
	CreatedBy      string     `json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	Links          []Link     `json:"links"`
}

// looksLikeUUID is the canonical 36-char hex shape, the same boundary
// guard every uuid-keyed payload leg checks before it reaches a Postgres
// uuid column.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// validateLinks checks a declared link set: D10's owner kinds, uuid owner
// ids, bounded roles. The unique index (attachment_id, owner_kind,
// owner_id, role) makes a repeated edge in one payload a defect, so that
// is refused here rather than at the insert.
func validateLinks(links []Link) error {
	seen := map[string]bool{}
	for _, l := range links {
		if !validOwnerKind(l.OwnerKind) {
			return fmt.Errorf("attachments: unknown owner_kind %q", l.OwnerKind)
		}
		if !looksLikeUUID(l.OwnerID) {
			return fmt.Errorf("attachments: owner_id must be a uuid")
		}
		if len(l.Role) > 200 {
			return fmt.Errorf("attachments: role is too long (max 200)")
		}
		key := l.OwnerKind + "|" + l.OwnerID + "|" + l.Role
		if seen[key] {
			return fmt.Errorf("attachments: link %s %s %s is declared twice", l.OwnerKind, l.OwnerID, l.Role)
		}
		seen[key] = true
	}
	return nil
}

// linksOf takes the raw links payload leg (the loose JSON shape included)
// and validates it.
func linksOf(raw any) ([]Link, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("attachments: links must be an array")
	}
	out := make([]Link, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("attachments: each link must be an object")
		}
		var l Link
		l.AttachmentID = "" // stamped at insert, never client-declared
		l.OwnerKind, _ = m["owner_kind"].(string)
		l.OwnerID, _ = m["owner_id"].(string)
		l.Role, _ = m["role"].(string)
		// The contract's AttachmentLink carries owner_kind, owner_id and
		// role as required legs; an empty role would also dodge the
		// (attachment_id, owner_kind, owner_id, role) unique index, which
		// treats NULL roles as distinct.
		if strings.TrimSpace(l.OwnerKind) == "" || strings.TrimSpace(l.OwnerID) == "" || strings.TrimSpace(l.Role) == "" {
			return nil, fmt.Errorf("attachments: owner_kind, owner_id and role are required on every link")
		}
		out = append(out, l)
	}
	if err := validateLinks(out); err != nil {
		return nil, err
	}
	return out, nil
}
