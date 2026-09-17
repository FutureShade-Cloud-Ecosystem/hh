-- 0028: contractor-portal procurement Planning board (P4.2, centerpiece M3). Forward-only.
-- A new portal-owned aggregate for the PRIVATE Planning phase (staged material lists); the
-- committed artifacts (quotes/orders/invoices) are reused downstream and are NOT modeled here.
-- Spec: contractor-portal-plan §4.4 (hybrid domain model). Conventions: UUID PKs · TEXT + CHECK
-- (no ENUMs) · money BIGINT *_cents · qty NUMERIC · TIMESTAMPTZ · explicit ON DELETE.
-- Module boundary: internal/procurement OWNS procurement_projects/_lines only. The trgm indexes
-- below enable the A-18 catalog fuzzy/by-SKU search seam (P4.1) the line picker consumes; they
-- add indexes to catalog-owned products WITHOUT altering its columns (search-only, non-destructive).

-- Trigram search support for the line-add catalog picker (pg_trgm fuzzy on name + by-SKU prefix).
-- 0001 ships pgcrypto + citext only, so enable pg_trgm here (idempotent).
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- The Kanban card: one staged procurement project per row. status == the board lane; the board
-- groups the tenant's non-terminal projects client-side. created_by_portal_user_id records the
-- contractor actor (a portal user, never a staff users(id)).
CREATE TABLE procurement_projects (
  id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id               UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  name                      TEXT NOT NULL,
  status                    TEXT NOT NULL DEFAULT 'PLANNING'
                              CHECK (status IN ('PLANNING','QUOTING','ORDERED','FULFILLED','CANCELLED')),
  created_by_portal_user_id UUID REFERENCES portal_users(id) ON DELETE SET NULL,
  created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- The board reads "this tenant's projects in lane X" — index (customer_id, status) (plan §4.4).
CREATE INDEX idx_procurement_projects_customer ON procurement_projects (customer_id, status);

-- The staged list. line_no orders the lines within a project; product_id is a real catalog
-- product (this increment is mapped-only — AI quick-quote's nullable/unmapped lines are P5.1).
-- indicative_*_cents are the RFQ-time INDICATIVE prices (provisional cost basis — never
-- authoritative; the FE shows an "indicative" badge). NULL until submit-rfq prices the line.
CREATE TABLE procurement_project_lines (
  id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id                  UUID NOT NULL REFERENCES procurement_projects(id) ON DELETE CASCADE,
  line_no                     INTEGER NOT NULL,
  product_id                  UUID NOT NULL REFERENCES products(id),
  description                 TEXT,
  qty_entered                 NUMERIC,
  uom                         TEXT,
  notes                       TEXT,
  indicative_unit_price_cents BIGINT,
  indicative_line_total_cents BIGINT,
  created_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_procurement_project_lines_project ON procurement_project_lines (project_id);

-- Catalog fuzzy/prefix search indexes (consumed by GET /catalog/search). name is TEXT; sku is
-- CITEXT so it is indexed on its text projection so gin_trgm_ops applies cleanly.
CREATE INDEX idx_products_name_trgm ON products USING gin (name gin_trgm_ops);
CREATE INDEX idx_products_sku_trgm  ON products USING gin ((sku::text) gin_trgm_ops);
