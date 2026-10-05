// imageload.go is the product module's own contribution to the load
// doors' product image door (HH-LOAD-1 LD-5): the rules the door's
// boundary carries (this module's image rules, images.go: the raster
// allowlist, the magic byte sniff, and the 0102 size cap, single
// sourced here), and the write path the door's transaction runs on (the
// module's own image store, 0102's one primary thumbnail per product,
// upserted on the caller's transaction).
//
// The writer's rules, binding: it opens no connection of its own, it
// never commits or rolls back the transaction it is given, and its
// errors wrap the statement's own (%w) so a Postgres deadlock stays
// recognisable at the door. Its refusals come back as *load.Refusal and
// carry codes and fields, never a byte of the image.
package product

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
)

// ProductImageLoadRules carries the module's own image rules to the
// image door's boundary. One source of rules: images.go's allowlist,
// sniff and cap, the same ones the staff byte route serves under.
type ProductImageLoadRules struct{}

// MaxImageBytes is the 0102 cap the boundary bounds its read with.
func (ProductImageLoadRules) MaxImageBytes() int { return maxImageBytes }

// ValidateImage runs the module's rules over one put: the declared type
// on the allowlist, the bytes present, the size under the cap, and the
// magic bytes agreeing with the declared type. A violation names the
// field and carries no value.
func (ProductImageLoadRules) ValidateImage(contentType string, body []byte) (string, string, bool) {
	ct := normalizeContentType(contentType)
	if !allowedImageTypes[ct] {
		return "content_type", "the declared type is outside the raster allowlist", false
	}
	if len(body) == 0 {
		return "image", "the image body is empty", false
	}
	if len(body) > maxImageBytes {
		return "image", "the image is over the size cap the module carries", false
	}
	if sniffed := sniffContentType(body); sniffed == "" || sniffed != ct {
		return "content_type", "the bytes disagree with the declared type", false
	}
	return "", "", true
}

// ProductImageLoadWriter is the image door's write path onto the
// module's own image store: the primary thumbnail upsert, on the door's
// transaction. The module's rules run again here (defence in depth: the
// boundary carried them first), refusing through *load.Refusal.
type ProductImageLoadWriter struct{}

// ApplyImage upserts the product's primary image on the caller's
// transaction, the same statement the staff byte route's store write
// carries (the unique partial index makes the primary image a single
// row, so an upload is an upsert), with uploaded_by left null: the
// column foreign keys the users table, and the load's caller of record
// is the machine principal, a user of no row.
func (ProductImageLoadWriter) ApplyImage(ctx context.Context, tx pgx.Tx, in load.ImageWrite) error {
	rules := ProductImageLoadRules{}
	if field, detail, ok := rules.ValidateImage(in.ContentType, in.Bytes); !ok {
		return &load.Refusal{Code: "image_rule", Field: field, Detail: detail}
	}
	contentType := strings.TrimSpace(in.ContentType)
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = strings.ToLower(strings.TrimSpace(contentType[:i]))
	}
	sum := md5.Sum(in.Bytes)
	// uploaded_by stays NULL on a loaded image: the column foreign keys
	// the users table and the caller of record is the machine principal,
	// a user of no row. The put's actor lives on the audit row and the
	// image's registry name, where it belongs.
	if _, err := tx.Exec(ctx, `
		INSERT INTO product_images (product_id, content_type, byte_size, image_bytes, alt_text, sort_order, checksum)
		VALUES ($1::uuid, $2, $3, $4, $5, 0, $6)
		ON CONFLICT (product_id) WHERE sort_order = 0
		DO UPDATE SET content_type = EXCLUDED.content_type, byte_size = EXCLUDED.byte_size,
		              image_bytes = EXCLUDED.image_bytes, alt_text = EXCLUDED.alt_text,
		              checksum = EXCLUDED.checksum, created_at = now()`,
		in.ProductID, contentType, len(in.Bytes), in.Bytes, strings.TrimSpace(in.AltText),
		hex.EncodeToString(sum[:])); err != nil {
		return fmt.Errorf("product: load image write: %w", err)
	}
	return nil
}
