-- 0102: GABLE:product-images-ui — product thumbnails as first-class catalogue data.
--
-- WHY THE BYTES LIVE IN POSTGRES (the 0091 delivery_pod_photos precedent, applied deliberately rather than
-- copied). This deployment has no object store. 0091 argued the case for a handful of proof-of-delivery
-- photos; a product catalogue is a different size class — 30 seeded products today, a few thousand SKUs at
-- full BisTrack cutover — so the cap here is tighter (1 MiB, not 4) and the intended payload is a THUMBNAIL,
-- not a photograph. A 1 MiB ceiling on a few thousand rows is a couple of gigabytes worst case, which a
-- nightly pg_dump handles; a 4 MiB ceiling would not be.
--
-- THE OBJECT-STORE MIGRATION PATH, written down now so nobody has to reverse-engineer it later:
--   1. ALTER TABLE product_images ADD COLUMN storage_key TEXT;              -- nullable, no behavior change
--   2. Backfill: stream each row's image_bytes to the bucket, stamp storage_key, leave the bytes in place.
--   3. Flip the repository read to prefer storage_key when set (ONE method: Repository.ReadImageBytes).
--   4. Once every row carries a key: ALTER TABLE product_images DROP COLUMN image_bytes, and relax the
--      byte_size/octet_length CHECK to a plain positive check.
-- No consumer changes in any step, because no consumer touches the column: every reader goes through
-- GET /api/v1/products/{id}/image, which returns bytes with a content type and an ETag.
--
-- CONTENT-TYPE IS AN ALLOWLIST, NOT A HINT. The bytes are served back with the stored content type, so an
-- attacker-chosen type would be a stored-XSS primitive (image/svg+xml carries script; text/html obviously
-- does). The CHECK below is the boundary: three raster types, nothing that a browser will execute.

CREATE TABLE product_images (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id   UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  content_type TEXT NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp')),
  byte_size    INTEGER NOT NULL CHECK (byte_size > 0 AND byte_size <= 1048576), -- 1 MiB thumbnail cap
  image_bytes  BYTEA NOT NULL,
  alt_text     TEXT NOT NULL DEFAULT '',
  -- Display order within a product. The catalogue thumbnail is always sort_order = 0.
  sort_order   INTEGER NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
  -- Cheap cache validator for the byte-serving route: md5 of the payload, computed once at write.
  checksum     TEXT NOT NULL DEFAULT '',
  uploaded_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT product_images_size_matches CHECK (byte_size = octet_length(image_bytes))
);
CREATE INDEX idx_product_images_product ON product_images (product_id, sort_order);
-- Exactly one primary thumbnail per product: the UI asks for "the" image and must get a deterministic one.
CREATE UNIQUE INDEX uq_product_images_primary ON product_images (product_id) WHERE sort_order = 0;
