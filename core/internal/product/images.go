package product

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
)

// - GABLE:product-images-ui -
//
// A hardscape counter is a visual sale. The customer points at a paver they saw in a driveway; the
// cashier has a list of names like "Techo-Bloc Blu 60 Slab" and no way to confirm they are talking
// about the same stone. Thumbnails are not decoration here, they are the disambiguation.
//
// STORAGE. Bytes in Postgres, capped at 1 MiB, three raster content types, one primary per product.
// The full argument (and the four-step object-store exit) is written in migration 0102's header; the
// only thing this file adds is the door: every reader goes through ReadPrimaryImage, so the exit
// changes one method.
//
// THE ALLOWLIST IS A SECURITY BOUNDARY, NOT A FORMAT PREFERENCE. These bytes are served back to a
// browser with the content type they were stored under. image/svg+xml carries script and text/html
// obviously does, so an attacker-chosen type would be stored XSS on a domain that also serves the
// authenticated app. Three raster types, checked here at the door and again by the CHECK constraint.
//
// AND THE MAGIC BYTES ARE CHECKED TOO. A declared Content-Type is a claim by the uploader, and the
// uploader is the attacker in the case that matters. sniffContentType reads the first bytes and
// requires them to agree with the declared type, so "image/png" containing HTML is refused rather
// than stored and later served.

// maxImageBytes mirrors the 0102 CHECK. Enforced at the door so an oversized upload is a 413 with a
// clear message rather than a constraint violation surfacing as a 500.
const maxImageBytes = 1 << 20 // 1 MiB

// allowedImageTypes is the content-type allowlist, identical to 0102's CHECK.
var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

// ProductImage is the stored thumbnail as the byte route serves it.
type ProductImage struct {
	ProductID   string
	ContentType string
	ByteSize    int
	Checksum    string // md5 hex, the ETag for the byte route
	AltText     string
	Bytes       []byte
}

// imageStore is the persistence seam for thumbnails (satisfied by *Repository; tests fake it).
type imageStore interface {
	ReadPrimaryImage(ctx context.Context, productID string) (ProductImage, bool, error)
	UpsertPrimaryImage(ctx context.Context, img ProductImage, uploadedBy string) error
	DeletePrimaryImage(ctx context.Context, productID string) (bool, error)
}

// WithImages injects the thumbnail store. Additive: unwired, the image routes answer 503 and every
// other product surface behaves exactly as it did before images existed.
func (s *Service) WithImages(st imageStore) *Service { s.images = st; return s }

// errNoImageStore is the unwired-seam answer: a 503, not a 500 and not a silent empty image. The
// difference matters operationally, "images are not configured here" is a deployment fact an
// operator can fix, while a 500 sends someone reading stack traces.
var errNoImageStore = fmt.Errorf("product: image storage is not configured")

// ErrImageTooLarge is returned when an upload exceeds the 1 MiB thumbnail cap.
var ErrImageTooLarge = fmt.Errorf("product: image exceeds the %d byte thumbnail cap", maxImageBytes)

// ErrImageType is returned when the declared content type is outside the allowlist, or when the
// bytes do not look like the type they claim to be.
var ErrImageType = fmt.Errorf("product: image must be image/jpeg, image/png or image/webp")

// GetPrimaryImage returns the product's catalogue thumbnail. Not-found is ErrNotFound so the handler
// answers 404 and the UI falls back to its placeholder glyph, an absent image is an ordinary state,
// not an error condition.
func (s *Service) GetPrimaryImage(ctx context.Context, productID string) (ProductImage, error) {
	if strings.TrimSpace(productID) == "" {
		return ProductImage{}, badReq("product id is required")
	}
	if s.images == nil {
		return ProductImage{}, errNoImageStore
	}
	img, found, err := s.images.ReadPrimaryImage(ctx, productID)
	if err != nil {
		return ProductImage{}, err
	}
	if !found {
		return ProductImage{}, ErrNotFound
	}
	return img, nil
}

// PutPrimaryImage validates and stores the catalogue thumbnail, replacing any existing one (the
// unique partial index in 0102 makes "the" thumbnail a single row, so an upload is an upsert).
func (s *Service) PutPrimaryImage(ctx context.Context, productID, contentType, altText string, body []byte, uploadedBy string) error {
	if strings.TrimSpace(productID) == "" {
		return badReq("product id is required")
	}
	if s.images == nil {
		return errNoImageStore
	}
	// The product must exist: an image row for a missing product would be an orphan the FK would
	// refuse anyway, and a 404 is the honest answer.
	if _, err := s.store.Get(ctx, productID); err != nil {
		return err
	}
	contentType = normalizeContentType(contentType)
	if !allowedImageTypes[contentType] {
		return ErrImageType
	}
	if len(body) == 0 {
		return badReq("image body is empty")
	}
	if len(body) > maxImageBytes {
		return ErrImageTooLarge
	}
	if sniffed := sniffContentType(body); sniffed == "" || sniffed != contentType {
		// The declared type and the bytes disagree. Refuse: this is the arm that keeps a text/html
		// payload from being stored as "image/png" and served back with a type a browser will render.
		return ErrImageType
	}
	sum := md5.Sum(body)
	return s.images.UpsertPrimaryImage(ctx, ProductImage{
		ProductID: productID, ContentType: contentType, ByteSize: len(body),
		Checksum: hex.EncodeToString(sum[:]), AltText: strings.TrimSpace(altText), Bytes: body,
	}, uploadedBy)
}

// DeletePrimaryImage removes the thumbnail. Idempotent: deleting an absent image is not an error,
// because the caller's intent ("this product has no image") is satisfied either way.
func (s *Service) DeletePrimaryImage(ctx context.Context, productID string) error {
	if strings.TrimSpace(productID) == "" {
		return badReq("product id is required")
	}
	if s.images == nil {
		return errNoImageStore
	}
	_, err := s.images.DeletePrimaryImage(ctx, productID)
	return err
}

// normalizeContentType strips any parameters ("image/png; charset=binary") and lowercases, so the
// allowlist compares against a canonical value rather than whatever the client sent verbatim.
func normalizeContentType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// sniffContentType identifies the three allowed formats from their magic bytes. It returns "" for
// anything it does not recognise, which the caller treats as a refusal, an allowlist by
// construction rather than a denylist that has to anticipate every dangerous format.
func sniffContentType(b []byte) string {
	switch {
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}

// - store -

// ReadPrimaryImage loads the sort_order = 0 thumbnail.
func (r *Repository) ReadPrimaryImage(ctx context.Context, productID string) (ProductImage, bool, error) {
	if r.pool == nil {
		return ProductImage{}, false, errNoDB
	}
	if !looksLikeUUID(productID) {
		// A non-uuid id can never match the uuid FK; treat it as not-found rather than letting a
		// 22P02 surface as a 500 (the same boundary hardening the sale reopen applies).
		return ProductImage{}, false, nil
	}
	const q = `
SELECT product_id::text, content_type, byte_size, checksum, alt_text, image_bytes
  FROM product_images WHERE product_id = $1 AND sort_order = 0`
	var img ProductImage
	err := r.pool.QueryRow(ctx, q, productID).Scan(&img.ProductID, &img.ContentType, &img.ByteSize,
		&img.Checksum, &img.AltText, &img.Bytes)
	if err != nil {
		if isNoRows(err) {
			return ProductImage{}, false, nil
		}
		return ProductImage{}, false, fmt.Errorf("read product image %q: %w", productID, err)
	}
	return img, true, nil
}

// UpsertPrimaryImage writes (or replaces) the sort_order = 0 thumbnail.
func (r *Repository) UpsertPrimaryImage(ctx context.Context, img ProductImage, uploadedBy string) error {
	if r.pool == nil {
		return errNoDB
	}
	const q = `
INSERT INTO product_images (product_id, content_type, byte_size, image_bytes, alt_text, sort_order, checksum, uploaded_by)
VALUES ($1, $2, $3, $4, $5, 0, $6, NULLIF($7,'')::uuid)
ON CONFLICT (product_id) WHERE sort_order = 0
DO UPDATE SET content_type = EXCLUDED.content_type, byte_size = EXCLUDED.byte_size,
              image_bytes = EXCLUDED.image_bytes, alt_text = EXCLUDED.alt_text,
              checksum = EXCLUDED.checksum, uploaded_by = EXCLUDED.uploaded_by,
              created_at = now()`
	_, err := r.pool.Exec(ctx, q, img.ProductID, img.ContentType, img.ByteSize, img.Bytes,
		img.AltText, img.Checksum, uploadedBy)
	if err != nil {
		return fmt.Errorf("upsert product image %q: %w", img.ProductID, err)
	}
	return nil
}

// DeletePrimaryImage removes the thumbnail; the bool reports whether a row was actually removed.
func (r *Repository) DeletePrimaryImage(ctx context.Context, productID string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	if !looksLikeUUID(productID) {
		return false, nil
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM product_images WHERE product_id = $1 AND sort_order = 0`, productID)
	if err != nil {
		return false, fmt.Errorf("delete product image %q: %w", productID, err)
	}
	return tag.RowsAffected() > 0, nil
}
