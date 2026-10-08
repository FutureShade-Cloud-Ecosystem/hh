-- 0373_supplier_deposit_invoices.sql
--
-- Sub item 9.5 (the row 1047 deposits work): a supplier's demanded deposit on
-- a special order, billed to a credit customer's account at once. Two facts:
--
--   1. invoices.source_kind gains 'SUPPLIER_DEPOSIT', the marker of the
--      deposit document (the invoice for the deposit amount, billed now, its
--      GL posting to Customer Deposits rather than revenue). The doc_type
--      stays INVOICE: it is a real invoice the account owes; only its POSTING
--      and its provenance differ, and the hold, the register and Pay balance
--      read it exactly as they read any invoice.
--   2. the posting rule for it: invoice.finalized / customer_deposit -> 2300
--      (Customer Deposits, planted by 0007 and unused until now). The GL
--      builder posts DR 1100 AR / CR 2300 for a SUPPLIER_DEPOSIT document
--      instead of the revenue and tax legs, so the deposit sits as the
--      liability it is until the special order's fulfilment applies it.
--
-- Owner module: internal/pos (the writer), internal/gl (the posting).

-- 1. The source kind.
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS ck_invoices_source_kind;
ALTER TABLE invoices
    ADD CONSTRAINT ck_invoices_source_kind
        CHECK (source_kind IN ('','POS','ORDER','MULTISHIP','WARRANTY_CREDIT','IMPORT','SUPPLIER_DEPOSIT'));

-- 2. The posting rule (the default, branch-NULL row; a branch override rides
--    the same partial index pair every other rule does).
INSERT INTO posting_rules (event_type, line_role, account_code)
VALUES ('invoice.finalized', 'customer_deposit', '2300')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
