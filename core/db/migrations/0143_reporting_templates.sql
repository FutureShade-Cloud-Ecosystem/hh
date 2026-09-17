-- 0143: DIB-95 — the native report substrate (report_templates + report_runs + a REPORT print doc type).
-- Owner module: internal/reporting. Wave 6, Swarm B — Reporting & Commissions.
--
-- WHAT THE OFFICE DOES TODAY. Twenty-odd numbers that run the business live in BisTrack reports. Every
-- one of them is a screen somebody opens, reads, and retypes into a spreadsheet. M4's gate criterion is
-- not "we can write SQL" — it is "the twenty reports people actually open have native replacements that
-- give the SAME numbers". This migration is the substrate those twenty sit on.
--
-- ── THE CENTRAL SAFETY DECISION: NO SQL LIVES IN THIS TABLE ──────────────────────────────────────────
-- The obvious shape for a "report definition" table is (name, category, sql_text) with a runner that
-- executes sql_text. That shape is a remote-code-execution surface with a UI on it: anyone who can write
-- a report_templates row can run arbitrary SQL as the application role, and "anyone" eventually includes
-- an import, a support script, or a compromised admin session. It is also unversioned — the query that
-- produced last quarter's margin number is whatever the row says TODAY.
--
--     THIS TABLE HOLDS THE DEFINITION. THE GO CODE HOLDS THE QUERY.
--
-- A row here declares WHAT a report is (code, name, category, the parameters it accepts, the columns it
-- returns) and nothing about HOW it is computed. internal/reporting/catalog.go holds the one fixed SQL
-- string per code, with $1..$n placeholders and an EXPLICIT column allowlist, compiled into the binary
-- and reviewed like any other code. A row whose code has no catalog entry cannot be run — it 404s. A
-- catalog entry with no row is invisible in the UI. The named test
-- TestCatalog_MatchesSeededTemplates pins both directions.
--
-- Consequences worth stating plainly, because they are the point:
--   · No user input is ever concatenated into SQL. Parameters bind; they never interpolate. An optional
--     filter is expressed SQL-side as ($n::uuid IS NULL OR col = $n::uuid), never by building a WHERE.
--   · An unknown parameter is REFUSED, not ignored. Ignoring it is how a caller learns that params are
--     advisory; refusing it is how the allowlist stays a contract. (TestParams_RejectsUnknownParameter,
--     TestParams_InjectionAttemptIsRefused.)
--   · The column allowlist is checked against what the database ACTUALLY returned. A template whose SQL
--     drifts to return an extra column fails the run rather than leaking it.
--
-- param_schema is JSONB and is DESCRIPTIVE — it is what the UI renders a form from and what the service
-- validates against. It is not a place to put behaviour. Kinds: date | uuid | enum | int | text_enum.
--
-- ── REPORT_RUNS: A PRINTED REPORT IS AN ARTEFACT, NOT A RE-RUN ───────────────────────────────────────
-- Same reasoning 0105 used for ar_statements, and for the same reason. When a report is PRINTED it stops
-- being a query and becomes a claim somebody carried into a meeting. report_runs stores the rendered
-- HTML, the parameters, the row count and the totals as they stood at run time. Re-deriving a printed
-- report later would silently rewrite it the moment an invoice is voided or a cost layer moves.
--
-- A run row is written ONLY on the print path. Browsing a report on screen writes nothing — otherwise
-- the table becomes a log of every click and the artefacts are lost in it.
--
-- ── PRINT_QUEUE GETS A FIFTH DOC TYPE, NOT A FIFTH MECHANISM ─────────────────────────────────────────
-- 0088 built print_queue as "the one substrate every printed document goes through"; 0105 added
-- STATEMENT to it rather than growing a private print path. A report is a BROWSER-target document with a
-- ref_id and a branch — precisely print_queue's shape — so it gets a doc_type. The CHECK widening is
-- additive: every value that validated before still does. ref_id points at the report_runs row (0088's
-- doc_type-decides-what-ref_id-means posture).
--
-- Conventions: UUID PKs · money BIGINT *_cents · TEXT + CHECK · TIMESTAMPTZ · explicit ON DELETE.
-- Rollback: additive (two tables + one CHECK widening). Dropping the tables leaves the widened CHECK
-- harmless.

-- ── 1. print_queue admits REPORT ─────────────────────────────────────────────────────────────────────
ALTER TABLE print_queue DROP CONSTRAINT print_queue_doc_type_check;
ALTER TABLE print_queue ADD CONSTRAINT print_queue_doc_type_check
  CHECK (doc_type IN ('RECEIPT','LABEL_ZPL','DELIVERY_DOC','STATEMENT','REPORT'));

-- ── 2. report_templates — the DEFINITION substrate ───────────────────────────────────────────────────
CREATE TABLE report_templates (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- The join to internal/reporting/catalog.go. Immutable by convention: renaming a code orphans the
  -- catalog entry and the report stops resolving, which is the loud failure we want.
  code        TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  -- The left-hand nav grouping. Constrained because the UI renders one pane per category and an
  -- unrecognised category would render nowhere.
  category    TEXT NOT NULL CHECK (category IN
                ('SALES','MARGIN','AR','INVENTORY','PURCHASING','TAX','PAYMENTS','CREDIT',
                 'DELIVERY','WARRANTY','COMMISSION')),
  description TEXT NOT NULL DEFAULT '',
  -- What the BisTrack report this replaces is called, so the person migrating can find their report.
  legacy_name TEXT NOT NULL DEFAULT '',
  -- Ordered parameter declaration. A JSON ARRAY of objects, ORDER SIGNIFICANT — the array index is the
  -- $n bind position in the catalog's SQL, which is why it is an array and not an object.
  --   [{"name":"from_date","kind":"date","required":true,"label":"From"}, ...]
  param_schema JSONB NOT NULL DEFAULT '[]'::jsonb,
  sort_order  INTEGER NOT NULL DEFAULT 0,
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_report_templates_category ON report_templates(category, sort_order);

-- ── 3. report_runs — the printed artefact ────────────────────────────────────────────────────────────
CREATE TABLE report_runs (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- TEMPLATE   → template_code names a report_templates row (and a catalog entry).
  -- COMMISSION → the artefact is a commission period register (0144). It is deliberately the SAME
  --              artefact table: a commission statement is a printed report, and giving it its own
  --              table would give print_queue a sixth doc type for no gain.
  kind          TEXT NOT NULL DEFAULT 'TEMPLATE' CHECK (kind IN ('TEMPLATE','COMMISSION')),
  template_code TEXT NOT NULL DEFAULT '',
  -- The parameters AS BOUND, after validation. This is the reproduction recipe.
  params        JSONB NOT NULL DEFAULT '{}'::jsonb,
  branch_id     UUID REFERENCES locations(id) ON DELETE SET NULL,
  row_count     INTEGER NOT NULL DEFAULT 0,
  -- Column-name → integer cents (or count) rollups, as computed at run time.
  totals        JSONB NOT NULL DEFAULT '{}'::jsonb,
  -- The rendered document. The artefact, not a pointer to one.
  html          TEXT NOT NULL DEFAULT '',
  print_job_id  UUID REFERENCES print_queue(id) ON DELETE SET NULL,
  run_by        UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_report_runs_code_time ON report_runs(template_code, created_at DESC);
CREATE INDEX idx_report_runs_kind_time ON report_runs(kind, created_at DESC);

-- ── 4. The twenty-two seeded definitions ─────────────────────────────────────────────────────────────
-- Twenty-two, not twenty: the brief's list expands to 22 once "warranty claims by vendor/status" and
-- "sales by day/branch/rep/category" are counted as the separate reports they are. Each is ONE fixed
-- query, so splitting a group-by into two templates costs nothing and keeps the alternative — a
-- user-supplied GROUP BY column — off the table entirely.
--
-- param_schema arrays below are ORDER-SIGNIFICANT (index = $n).
INSERT INTO report_templates (code, name, category, description, legacy_name, param_schema, sort_order) VALUES
 ('sales_by_day','Scrubname0027 by Scrubname0009','SALES',
  'Invoiced sales per day: invoice count, subtotal, tax, total. Excludes DRAFT and VOID.',
  'Scrubname0027 Analysis — Daily',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 10),

 ('sales_by_branch','Scrubname0027 by Branch','SALES',
  'Invoiced sales rolled to branch for the period.',
  'Scrubname0027 Analysis — Branch',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"}]'::jsonb, 20),

 ('sales_by_rep','Scrubname0027 by Rep','SALES',
  'Invoiced sales attributed through the rep-assignment substrate (order override, then customer-level, then house).',
  'Scrubname0027 Analysis — Salesperson',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 30),

 ('sales_by_category','Scrubname0027 by Category','SALES',
  'Invoiced line revenue and quantity rolled to product category.',
  'Scrubname0027 Analysis — Product Group',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 40),

 ('margin_by_product','Margin by Product','MARGIN',
  'Revenue, weighted-average cost and gross margin per SKU for the period.',
  'Gross Profit — Product',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 10),

 ('margin_by_category','Margin by Category','MARGIN',
  'Revenue, weighted-average cost and gross margin rolled to product category.',
  'Gross Profit — Product Group',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 20),

 ('ar_aging_detail','AR Aging — Detail','AR',
  'Every open AR item with its age and bucket, from the 0034 v_ar_aging view.',
  'Aged Debt — Detail',
  '[{"name":"branch_id","kind":"uuid","required":false,"label":"Branch"},
    {"name":"customer_id","kind":"uuid","required":false,"label":"Customer"}]'::jsonb, 10),

 ('ar_aging_summary','AR Aging — Summary','AR',
  'One row per customer with the current / 1-30 / 31-60 / 61-90 / 90+ buckets and the total.',
  'Aged Debt — Summary',
  '[{"name":"branch_id","kind":"uuid","required":false,"label":"Branch"},
    {"name":"customer_id","kind":"uuid","required":false,"label":"Customer"}]'::jsonb, 20),

 ('statement_run_history','Statement Run History','AR',
  'Every month-end and interim statement run: mode, as-of date, generated and queued counts.',
  'Statement Run Log',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"}]'::jsonb, 30),

 ('inventory_valuation','Inventory Valuation','INVENTORY',
  'On-hand quantity times weighted-average unit cost, per SKU per branch.',
  'Stock Valuation',
  '[{"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 10),

 ('inventory_movement','Inventory Movement','INVENTORY',
  'Stock movements per day per SKU, optionally narrowed to one movement type.',
  'Stock Movement Report',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"},
    {"name":"move_type","kind":"enum","required":false,"label":"Movement type",
     "values":["RECEIPT","TRANSFER","PICK","SHIP","ADJUST","COUNT","BREAK_PALLET","RETURN"]}]'::jsonb, 20),

 ('below_reorder','Below Reorder Point','INVENTORY',
  'Products whose on-hand has fallen below their branch reorder point, with the shortfall and preferred vendor.',
  'Re-order Report',
  '[{"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 30),

 ('po_status','Purchase Order Status','PURCHASING',
  'Open and closed purchase orders with ordered vs received quantity and committed value.',
  'PO Status Enquiry',
  '[{"name":"status","kind":"enum","required":false,"label":"Status",
     "values":["DRAFT","ORDERED","PARTIAL","RECEIVED","CLOSED","CANCELLED"]},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"},
    {"name":"vendor_id","kind":"uuid","required":false,"label":"Vendor"}]'::jsonb, 10),

 ('receiving_history','Receiving History','PURCHASING',
  'Goods received notes for the period with quantity and landed value.',
  'Goods Received Log',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 20),

 ('three_way_variance','Three-Way Match Variance','PURCHASING',
  'Bill lines that did not match cleanly to PO and receipt, with the quantity and price variance.',
  'Invoice Variance Report',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 30),

 ('tax_collected','Tax Collected','TAX',
  'Taxable base and tax charged per day per branch, from finalized invoices.',
  'HST Collected',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 10),

 ('payments_by_method','Payments by Method','PAYMENTS',
  'Received and deposited payments grouped by tender method, with surcharge.',
  'Cash Receipts by Type',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 10),

 ('payments_by_day','Payments by Scrubname0009','PAYMENTS',
  'Received and deposited payments per day, with the count and total.',
  'Cash Receipts — Daily',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 20),

 ('credit_hold_history','Credit Hold History','CREDIT',
  'Credit holds placed in the period: kind, reason, who placed and who released.',
  'Account Stop Log',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"status","kind":"enum","required":false,"label":"Status","values":["ACTIVE","RELEASED"]}]'::jsonb, 10),

 ('delivery_performance','Delivery Performance','DELIVERY',
  'Stops per scheduled day per branch with delivered, failed and on-time counts.',
  'Delivery Performance',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 10),

 ('warranty_by_vendor','Warranty Claims by Vendor','WARRANTY',
  'Claims opened in the period grouped by manufacturer, with expected and realized recovery.',
  'Warranty Claims — Supplier',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 10),

 ('warranty_by_status','Warranty Claims by Status','WARRANTY',
  'Claims opened in the period grouped by lifecycle phase and settlement outcome.',
  'Warranty Claims — Status',
  '[{"name":"from_date","kind":"date","required":true,"label":"From"},
    {"name":"to_date","kind":"date","required":true,"label":"To"},
    {"name":"branch_id","kind":"uuid","required":false,"label":"Branch"}]'::jsonb, 20);
