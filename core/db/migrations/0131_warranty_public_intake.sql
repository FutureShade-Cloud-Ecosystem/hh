-- 0131: DIB-222 — WTY-INTAKE v1, the PUBLIC warranty intake form. Owner: internal/warranty (W5-B).
--
-- THE DOORWAY, AND WHAT IT IS NOT. A homeowner whose driveway is spalling has no login and never will. The
-- will-call precedent (0090/DIB-79) established how this codebase opens a door to someone with no session:
-- a SEPARATE RegisterPublicRoutes method, so "this route has no guard" is a sentence someone wrote on purpose.
-- The difference here is direction — will-call READS one row and confirms it; intake WRITES. That makes three
-- things load-bearing, and all three are enforced in the schema as well as in the service:
--
--   1. THE PUBLIC ROUTE WRITES EXACTLY ONE THING: a claim in INTAKE phase with source='WEB', its lines, and its
--      photos. It cannot assign an owner, cannot pick a vendor, cannot set money, and cannot touch any table
--      outside warranty. There is no public read of a claim at all — the reference number below is a receipt to
--      quote on the phone, NOT a lookup key, because a public GET keyed on it would be exactly the auth bypass
--      this ticket forbids.
--   2. SIZE IS BOUNDED AT THE DOOR AND AGAIN HERE. Same posture as 0091's POD photos: the service caps the
--      upload and the CHECK is the backstop, so an oversized body is a 413 with a sentence, never a constraint
--      violation surfacing as a 500.
--   3. RATE LIMITING IS A TABLE, NOT A PROCESS VARIABLE. An in-memory counter resets on every deploy and is
--      per-instance, which means the limit is whatever the load balancer decides. A row survives both.

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. The client-facing reference number.
--
-- WHY IT IS RANDOM AND NOT #WTY-n. The claim already has claim_no (#WTY-n, gap-free, the staff identity). That
-- number is an enumeration: handing it to the public tells every submitter exactly how many warranty claims the
-- business has and lets anyone guess a neighbour's. public_ref is a high-entropy token rendered in a
-- human-readable shape, generated in the service, unique here.
-- ─────────────────────────────────────────────────────────────────────────────
ALTER TABLE warranty_claims ADD COLUMN public_ref CITEXT UNIQUE;
COMMENT ON COLUMN warranty_claims.public_ref IS
  'DIB-222 client-facing intake reference (WR-YYYY-XXXXXXXX). NULL for staff-captured claims. Not a lookup key: there is no public read route.';

-- Contact details a web submitter supplies. They are NOT a customer record: the person filing may not be in the
-- customer master at all (a homeowner whose contractor bought the stone), and silently creating a customer from
-- an unauthenticated form would let the public write to the customer master. Triage links a real customer_id
-- later, from the staff side.
ALTER TABLE warranty_claims ADD COLUMN intake_contact JSONB NOT NULL DEFAULT '{}';
COMMENT ON COLUMN warranty_claims.intake_contact IS
  'DIB-222 self-reported {name, email, phone, company} from the public form. Untrusted display data — never a customer master write.';

-- ─────────────────────────────────────────────────────────────────────────────
-- 2. Intake photos. The 0091 BYTEA pattern verbatim: bytes in Postgres because this system has no object store,
--    a three-type raster allowlist that is a SECURITY boundary (svg and html carry script and these bytes are
--    served back on a domain that also serves the authenticated app), and a size CHECK that matches the
--    service's door cap. The seam is the table: when an object store lands, add storage_key, backfill, drop
--    image_bytes — one repository method changes.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_intake_photos (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  claim_id      UUID NOT NULL REFERENCES warranty_claims(id) ON DELETE CASCADE,
  content_type  TEXT NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp')),
  byte_size     INTEGER NOT NULL CHECK (byte_size > 0 AND byte_size <= 4194304), -- 4 MiB, mirrors 0091
  image_bytes   BYTEA NOT NULL,
  caption       TEXT NOT NULL DEFAULT '',
  checksum      TEXT NOT NULL DEFAULT '',        -- md5 hex — the ETag for the staff-side byte route
  uploaded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT warranty_intake_photos_size_matches CHECK (byte_size = octet_length(image_bytes))
);
CREATE INDEX idx_warranty_intake_photos_claim ON warranty_intake_photos(claim_id, uploaded_at);

-- ─────────────────────────────────────────────────────────────────────────────
-- 3. Rate limiting. One row per bucket key per window. The key is a SHA-256 of the client address, never the
--    address itself: this table exists to throttle abuse, not to build a log of who visited the form, and an
--    unhashed IP column would be a PII store nobody asked for.
--
--    The window is a fixed bucket (window_start + window_seconds), which is coarser than a sliding window and
--    deliberately so: a sliding window needs per-request rows, i.e. exactly the log we just refused to keep.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_intake_rate_limit (
  bucket_key    TEXT NOT NULL,                   -- sha256 hex of the client address
  window_start  TIMESTAMPTZ NOT NULL,
  hits          INTEGER NOT NULL DEFAULT 0 CHECK (hits >= 0),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (bucket_key, window_start)
);
-- Sweep index: old windows are deleted, never read.
CREATE INDEX idx_warranty_intake_rate_limit_window ON warranty_intake_rate_limit(window_start);

-- ─────────────────────────────────────────────────────────────────────────────
-- 4. Intake configuration (singleton). Every tunable is a DB row: turning the public form OFF during an abuse
--    incident, or tightening the limit, is an UPDATE an operator can make at 2am without a deploy.
--    is_enabled=FALSE makes the public route answer 503 — the form is closed, not broken.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_intake_config (
  id                  BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),  -- singleton row
  is_enabled          BOOLEAN NOT NULL DEFAULT TRUE,
  max_body_bytes      INTEGER NOT NULL DEFAULT 8388608 CHECK (max_body_bytes > 0),   -- 8 MiB whole request
  max_photo_bytes     INTEGER NOT NULL DEFAULT 4194304 CHECK (max_photo_bytes > 0),  -- 4 MiB per photo (= CHECK above)
  max_photos          INTEGER NOT NULL DEFAULT 6 CHECK (max_photos >= 0),
  window_seconds      INTEGER NOT NULL DEFAULT 3600 CHECK (window_seconds > 0),
  max_per_window      INTEGER NOT NULL DEFAULT 5 CHECK (max_per_window > 0),
  -- Which branch a web claim lands in. The public form has no branch picker (a homeowner does not know the
  -- branch structure), so intake goes to the configured default and triage moves it.
  default_branch_id   UUID REFERENCES locations(id) ON DELETE RESTRICT,
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The singleton, with the Trenton branch as the default landing branch (the demo/primary branch, matching the
-- rest of the seed). If that location is absent the row still inserts with NULL and the service refuses public
-- intake with a configuration error rather than guessing a branch.
INSERT INTO warranty_intake_config (id, default_branch_id)
VALUES (TRUE, (SELECT id FROM locations WHERE id = '5d1b3f6e-0000-4000-9000-000000000001'))
ON CONFLICT (id) DO NOTHING;
