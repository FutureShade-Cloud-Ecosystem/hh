-- 0048: CP-03 Customer-Portal Projects/Jobs rollup (scope reconciliation 2026-07-14, A-3).
--
-- The shipped "Projects" is a procurement/RFQ board (internal/procurement — a legit, separate
-- feature that STAYS). CP-03's FEATURE_BRIEF specced a DIFFERENT thing: a jobs rollup that groups
-- a contractor's orders/invoices/deliveries/spend by project. This migration adds the specced
-- projects table + orders.project_id FK the FEATURE_BRIEF calls out (it references a phantom
-- "migration 035"; this is the real greenfield equivalent).
--
-- Migration number: master head is 0046; a pending auth PR takes 0047, so this new migration
-- starts at 0048 (0049 seeds the demo linkage) to avoid collision.

CREATE TABLE projects (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  job_address TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Every rollup query filters by the portal JWT's customer_id, so index it (IDOR-safe scoping).
CREATE INDEX idx_projects_customer ON projects(customer_id);

-- The order→project link the rollup reaches invoices/deliveries through
-- (invoice → order → project_id, delivery/manifest_line → order → project_id). Nullable: an
-- order need not belong to a project. ON DELETE SET NULL so deleting a project unlinks orders
-- rather than cascading a delete through the order-to-cash spine.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_orders_project ON orders(project_id) WHERE project_id IS NOT NULL;
