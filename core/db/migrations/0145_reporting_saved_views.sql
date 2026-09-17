-- 0145: DIB-95 follow-on — saved parameter sets, plus the two read-path indexes the report queries need.
-- Owner module: internal/reporting. Wave 6, Swarm B. Last migration in this swarm's 0143–0145 allocation.
--
-- ── 1. report_saved_views — "the way I always run this" ──────────────────────────────────────────────
-- The report that gets used is the one that opens with last month's parameters already in it. Without
-- this, "Margin by Category, Kingston, last month" is six form fields retyped every time, and the person
-- who mistypes a date reports a number that is wrong in a way nobody can see.
--
-- A saved view is PER USER by default and shareable to the branch (is_shared). It stores parameters, not
-- results: opening a saved view re-runs the report, which is right — a saved VIEW is a lens, not an
-- artefact. The artefact is report_runs (0143), written only when someone prints.
--
-- Parameters are stored as a JSONB object and are re-validated against the template's param_schema on
-- every run, exactly as if they had been typed. A saved view can therefore never become a way to smuggle
-- an unvalidated parameter past the allowlist: the validation is on the RUN path, not the SAVE path.
CREATE TABLE report_saved_views (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  template_code TEXT NOT NULL REFERENCES report_templates(code) ON DELETE CASCADE,
  owner_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  params        JSONB NOT NULL DEFAULT '{}'::jsonb,
  -- Shared views are visible to every staff member who can reach the template. Sharing is additive
  -- only: a shared view can be read by others but edited and deleted solely by its owner.
  is_shared     BOOLEAN NOT NULL DEFAULT FALSE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One name per template per owner: saving twice under the same name is an UPDATE, never a second row
-- the user cannot tell apart from the first.
CREATE UNIQUE INDEX uq_report_saved_views_owner_name
  ON report_saved_views(owner_user_id, template_code, name);
CREATE INDEX idx_report_saved_views_template ON report_saved_views(template_code);

-- ── 2. Two read-path indexes on peer tables ──────────────────────────────────────────────────────────
-- Stated explicitly because these are not this module's tables. Both are ADDITIVE and BEHAVIOUR-FREE:
-- an index changes no result, no constraint and no write path — it changes only how long a read takes.
-- The precedent for a reporting migration touching a peer object is 0105's print_queue CHECK widening;
-- this is strictly weaker than that.
--
-- Why these two specifically. Every date-ranged sales, margin and tax template filters
-- invoices.issue_date, and the existing invoices indexes are on (branch_id,status), (customer_id) and
-- (due_date) — none of which help an issue_date range. The margin and category templates join
-- invoice_lines by product_id, and invoice_lines has only its PK and the (invoice_id,line_no) unique.
-- On the demo dataset neither matters; on a real ledger the daily sales report is the first thing that
-- gets slow, and a report nobody waits for is a report nobody runs.
CREATE INDEX IF NOT EXISTS idx_invoices_issue_date ON invoices(issue_date);
CREATE INDEX IF NOT EXISTS idx_invoice_lines_product ON invoice_lines(product_id);
