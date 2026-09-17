-- 0091: proof-of-delivery photos (DIB-64, bundle W2-A).
--
-- WHY THE BYTES LIVE IN POSTGRES. This system has no object store. The only existing binary-upload
-- path in the tree (internal/feedback) pushes straight to Plane's presigned S3 gateway — a foreign
-- host, for a foreign system's attachments — and there is nothing here to reuse. Inventing an S3
-- dependency to hold a handful of 200 KB delivery photos would be a deployment change dressed as a
-- feature; storing them as BYTEA is what pos_signature.signature_enc already does with its evidence,
-- and it means a POD photo is backed up, restored and access-controlled by exactly the mechanism
-- that already protects the delivery it proves.
--
-- THE SEAM IS THE TABLE, NOT THE COLUMN. When an object store does land, the migration is: add a
-- nullable storage_key, backfill, drop image_bytes. Callers read the photo through
-- GET /api/v1/dispatch/stops/{id}/photos/{photoId}, never through the column — so that migration
-- changes one repository method and no consumer.
--
-- SIZE IS BOUNDED AT THE DOOR. The service caps an upload at 4 MiB and the CHECK below is the
-- backstop: a phone camera photo of a pallet on a driveway is 1–3 MB, and a table that will hold one
-- row per stop forever must not be able to hold a video.

CREATE TABLE delivery_pod_photos (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  stop_id      UUID NOT NULL REFERENCES delivery_stops(id) ON DELETE CASCADE,
  -- Denormalized branch fence: the photo read is gated without joining back to the stop, for the
  -- same reason every other table in 0090 carries branch_id.
  branch_id    UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  content_type TEXT NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp')),
  byte_size    INTEGER NOT NULL CHECK (byte_size > 0 AND byte_size <= 4194304), -- 4 MiB
  image_bytes  BYTEA NOT NULL,
  caption      TEXT NOT NULL DEFAULT '',
  captured_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  captured_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT delivery_pod_photos_size_matches CHECK (byte_size = octet_length(image_bytes))
);
CREATE INDEX idx_delivery_pod_photos_stop ON delivery_pod_photos (stop_id, captured_at);
