-- 0214_new_entities_and_pricing.sql
--
-- Additive only. Every new column is nullable or carries a default, so the
-- running ERP is unaffected. Block-to-decision map:
--
--   Block 1 (orders alias):            D6 (legacy_contractor_id, readable, never a key)
--   Block 2 (products lead time):      D13 (lead_time_days is the attribute and the default;
--                                       availability is a computed read model, never stored)
--   Block 3 (price_snapshot on lines): D14 (each line carries a price_snapshot with resolved_at)
--                                       and D29 (the snapshot records resolution source:
--                                       core-resolved or served from a cached band)
--   Block 4 (frozen line snapshot):    D15 (live product reference plus, at document freeze,
--                                       a snapshot of sku, description, unit, unit price, tax code)
--   Block 5 (special-order columns):   D16 (boolean plus description plus optional supplier_ref)
--   Block 6 (org surcharge config):    D18 (off by default, credit-only, capped, jurisdiction
--                                       recorded; enabling requires jurisdiction and cap)
--   Block 7 (plan, plan_line):         D30 (client-generatable UUID keys, idempotency key),
--                                       domain-map 5.4 shapes; C19 grain per D7's contractor
--                                       vocabulary; C11 entered trio; C47 price_source;
--                                       C52 per-line price validity
--   Block 8 (customer_quote):          D8 (contractor-to-homeowner document), D23 (magic-link,
--                                       token-gated, token stored only as a hash), domain-map 5.4
--   Block 9 (quote_acceptance):        D8 and C18 (typed name, affirmative, consent text,
--                                       accepted total; the contested drawn mark is optional and
--                                       stored as an attachment of kind 'signature' per D10)
--   Block 10 (attachment, attachment_link): D10 (one attachment entity, bytes in Appwrite
--                                       Storage, polymorphic link table)
--
-- D17 needs no DDL: the contractor's stop view (ETA window, stop status,
-- driver first name, proof of delivery) is a read projection over the
-- carried dispatch tables, never the route, the sequence, or other stops.
--
-- D14's document-level validity already exists as quotes.valid_until in the
-- carried baseline (0008); nothing is added there.
--
-- Assumes the carried baseline (0001 to 0212) and the earlier additive
-- migration 0213, which creates org and adds scoping columns. tenant_id
-- references the carried customers table, which is the tenant row.

-- Block 1: D6, one canonical order id, contractor-side id kept as an alias.
ALTER TABLE orders ADD COLUMN legacy_contractor_id CITEXT NULL;
CREATE INDEX orders_legacy_contractor_id_idx ON orders (legacy_contractor_id);

-- Block 2: D13, lead time as a product attribute and the default.
-- No availability table is created anywhere: availability is computed.
ALTER TABLE products ADD COLUMN lead_time_days INTEGER NULL;

-- Block 3: D14 plus D29, per-line price_snapshot for audit.
-- JSON keys: resolved_at (TIMESTAMPTZ text), source ('core' or 'cached_band').
-- Shape is enforced at the API boundary; the column stays nullable so the
-- running ERP's writers are unaffected.
ALTER TABLE quote_lines ADD COLUMN price_snapshot JSONB NULL;
ALTER TABLE order_lines ADD COLUMN price_snapshot JSONB NULL;

-- Block 4: D15, frozen snapshot of the priced fields at document freeze.
-- JSON keys: sku, description, unit, unit_price_cents, tax_code.
-- Before freeze the live product_id reference renders; after freeze this
-- snapshot renders and the reference is for navigation only.
ALTER TABLE quote_lines ADD COLUMN snapshot JSONB NULL;
ALTER TABLE order_lines ADD COLUMN snapshot JSONB NULL;

-- Block 5: D16, special-order lines. order_lines already carries
-- is_special_order from the baseline; quote_lines gains all three columns.
ALTER TABLE quote_lines ADD COLUMN is_special_order BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE quote_lines ADD COLUMN special_order_description TEXT NULL;
ALTER TABLE quote_lines ADD COLUMN supplier_ref TEXT NULL;
ALTER TABLE order_lines ADD COLUMN special_order_description TEXT NULL;
ALTER TABLE order_lines ADD COLUMN supplier_ref TEXT NULL;

-- Block 6: D18, card surcharge configuration, off by default, credit-only,
-- capped by the recorded jurisdiction's rule. Fee shown equals fee charged.
ALTER TABLE org ADD COLUMN card_surcharge_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE org ADD COLUMN card_surcharge_jurisdiction TEXT NULL;
ALTER TABLE org ADD COLUMN card_surcharge_cap_bps INTEGER NULL;
ALTER TABLE org ADD CONSTRAINT org_surcharge_requires_jurisdiction
  CHECK (NOT card_surcharge_enabled
         OR (card_surcharge_jurisdiction IS NOT NULL
             AND card_surcharge_cap_bps IS NOT NULL));

-- Block 7: plan and plan_line, domain-map 5.4, D30 keys.
CREATE TABLE plan (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id            UUID NOT NULL REFERENCES org (id),
  tenant_id         UUID NOT NULL REFERENCES customers (id),
  project_id        UUID NULL REFERENCES projects (id),
  name              TEXT NOT NULL DEFAULT '',
  stage             TEXT NOT NULL DEFAULT 'plan'
                    CHECK (stage IN ('plan', 'quote', 'order', 'invoice')),
  fulfillment       TEXT NULL
                    CHECK (fulfillment IN ('PICKUP', 'DELIVERY', 'WILL_CALL')),
  requested_date    DATE NULL,
  delivery_address  JSONB NULL,
  site_instructions TEXT NULL,
  po_reference      TEXT NULL,
  sort_order        INTEGER NOT NULL DEFAULT 0,
  seq               BIGINT NULL,
  idempotency_key   UUID NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at        TIMESTAMPTZ NULL
);
CREATE UNIQUE INDEX plan_idempotency_key_uq ON plan (idempotency_key);
CREATE INDEX plan_tenant_idx ON plan (tenant_id);
CREATE INDEX plan_org_idx ON plan (org_id);

CREATE TABLE plan_line (
  id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  plan_id                   UUID NOT NULL REFERENCES plan (id),
  org_id                    UUID NOT NULL REFERENCES org (id),
  tenant_id                 UUID NOT NULL REFERENCES customers (id),
  product_id                UUID NULL REFERENCES products (id),
  description               TEXT NOT NULL DEFAULT '',
  snapshot                  JSONB NULL,
  qty_entered               DECIMAL(19,4) NULL,
  uom_entered               CITEXT NULL REFERENCES uoms (code),
  qty_base                  DECIMAL(19,4) NULL,
  unit_price_cents          BIGINT NULL,
  price_uom                 CITEXT NULL REFERENCES uoms (code),
  line_total_cents          BIGINT NULL,
  price_source              TEXT NOT NULL DEFAULT 'unpriced'
                            CHECK (price_source IN ('erp', 'quoted', 'estimate', 'unpriced')),
  price_valid_until         TIMESTAMPTZ NULL,
  price_snapshot            JSONB NULL,
  is_special_order          BOOLEAN NOT NULL DEFAULT FALSE,
  special_order_description TEXT NULL,
  supplier_ref              TEXT NULL,
  notes                     TEXT NOT NULL DEFAULT '',
  added_by                  TEXT NULL,
  metadata                  JSONB NOT NULL DEFAULT '{}',
  seq                       BIGINT NULL,
  idempotency_key           UUID NULL,
  created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at                TIMESTAMPTZ NULL
);
CREATE UNIQUE INDEX plan_line_idempotency_key_uq ON plan_line (idempotency_key);
CREATE INDEX plan_line_plan_idx ON plan_line (plan_id);
CREATE INDEX plan_line_tenant_idx ON plan_line (tenant_id);

-- Block 8: customer_quote, contractor to homeowner (D8). The magic-link
-- token is stored only as a hash (carried share convention); the raw token
-- appears only in the send response. Totals are derived, never stored, so
-- no confident wrong number can persist.
CREATE TABLE customer_quote (
  id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                  UUID NOT NULL REFERENCES org (id),
  tenant_id               UUID NOT NULL REFERENCES customers (id),
  plan_id                 UUID NULL REFERENCES plan (id),
  status                  TEXT NOT NULL DEFAULT 'draft'
                          CHECK (status IN ('draft', 'sent', 'viewed',
                                            'changes-requested', 'accepted',
                                            'declined', 'expired')),
  markup_bps              INTEGER NULL,
  labour_lines            JSONB NOT NULL DEFAULT '[]',
  overhead_lines          JSONB NOT NULL DEFAULT '[]',
  hide_line_prices        BOOLEAN NOT NULL DEFAULT FALSE,
  lines                   JSONB NOT NULL DEFAULT '[]',
  customer_contact        JSONB NULL,
  share_token_hash        TEXT NULL,
  share_expires_at        TIMESTAMPTZ NULL,
  valid_until             TIMESTAMPTZ NULL,
  supplier_price_horizon  TIMESTAMPTZ NULL,
  view_count              INTEGER NOT NULL DEFAULT 0,
  first_viewed_at         TIMESTAMPTZ NULL,
  change_requests         JSONB NOT NULL DEFAULT '[]',
  seq                     BIGINT NULL,
  idempotency_key         UUID NULL,
  created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at              TIMESTAMPTZ NULL
);
CREATE UNIQUE INDEX customer_quote_idempotency_key_uq ON customer_quote (idempotency_key);
CREATE INDEX customer_quote_tenant_idx ON customer_quote (tenant_id);

-- Block 10: attachment and attachment_link (D10). Bytes live in Appwrite
-- Storage; the row lands when the bytes do.
CREATE TABLE attachment (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id           UUID NOT NULL REFERENCES org (id),
  tenant_id        UUID NULL REFERENCES customers (id),
  kind             TEXT NOT NULL
                   CHECK (kind IN ('photo', 'proof_of_delivery', 'document',
                                   'signature', 'product_image')),
  appwrite_file_id TEXT NOT NULL,
  bucket           TEXT NOT NULL,
  mime             TEXT NOT NULL,
  size_bytes       BIGINT NULL,
  checksum         TEXT NULL,
  created_by       TEXT NULL,
  seq              BIGINT NULL,
  idempotency_key  UUID NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ NULL
);
CREATE UNIQUE INDEX attachment_idempotency_key_uq ON attachment (idempotency_key);
CREATE INDEX attachment_tenant_idx ON attachment (tenant_id);

CREATE TABLE attachment_link (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  attachment_id UUID NOT NULL REFERENCES attachment (id),
  owner_kind    TEXT NOT NULL,
  owner_id      UUID NOT NULL,
  role          TEXT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX attachment_link_uq
  ON attachment_link (attachment_id, owner_kind, owner_id, role);

-- Block 9: quote_acceptance (D8, C18). The drawn mark is optional and lives
-- in attachment storage, never as an inline data value.
CREATE TABLE quote_acceptance (
  id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                  UUID NOT NULL REFERENCES org (id),
  tenant_id               UUID NOT NULL REFERENCES customers (id),
  customer_quote_id       UUID NOT NULL REFERENCES customer_quote (id),
  signed_name             TEXT NOT NULL,
  affirmative             BOOLEAN NOT NULL DEFAULT FALSE,
  consent_text            TEXT NOT NULL,
  accepted_total_cents    BIGINT NOT NULL,
  signature_attachment_id UUID NULL REFERENCES attachment (id),
  accepted_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  seq                     BIGINT NULL,
  idempotency_key         UUID NULL,
  created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at              TIMESTAMPTZ NULL
);
CREATE UNIQUE INDEX quote_acceptance_idempotency_key_uq ON quote_acceptance (idempotency_key);
CREATE INDEX quote_acceptance_quote_idx ON quote_acceptance (customer_quote_id);
