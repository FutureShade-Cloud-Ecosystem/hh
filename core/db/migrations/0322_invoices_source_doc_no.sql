-- 0322_invoices_source_doc_no.sql
-- Owner module: internal/billing (HH-LOAD-1 item LD-11, the open
-- receivables door; the billing grant CP0-6 answer B names).
--
-- WHAT THIS ADDS. One additive nullable column: invoices.source_doc_no,
-- the source system's own document number for a row that arrived
-- through a load door, beside the invoice_no HH itself mints. The plan
-- doubted the invoices tables could carry it (LOAD-0 section 11.5);
-- they cannot: invoice_no is HH's minted number under its own sequence,
-- metadata is not a query surface, and no other column holds a source
-- identifier. So the door adds the one column, the shape every imported
-- document carries and no HH-raised document does (NULL there, the
-- 0146 posture: a new column's default reproduces exactly what an
-- existing row means).
--
-- No unique constraint: the same source number may name different rows
-- across source systems and orgs, and the load registry already holds
-- the one-name-one-row rule the doors enforce. The plain index serves
-- the lookup a reconciliation runs: the source's document numbers
-- against HH's rows, inside one org.
--
-- ADDITIVE. One nullable column, one index, one comment. No carried
-- migration is touched, no existing row changes shape.

ALTER TABLE invoices ADD COLUMN IF NOT EXISTS source_doc_no TEXT;

CREATE INDEX IF NOT EXISTS idx_invoices_org_source_doc_no
    ON invoices (org_id, source_doc_no)
    WHERE source_doc_no IS NOT NULL;

COMMENT ON COLUMN invoices.source_doc_no IS
  'The source system''s own document number on a row that arrived through a load door (source_kind IMPORT); NULL on every document HH raised itself. Uniqueness lives in the load key registry, not here: one name maps to one row there.';
