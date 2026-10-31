-- 0468_bill_line_supply_kind.sql
--
-- Marks each purchasing bill line as goods or services for the CRA T5018 Statement of Contract
-- Payments. A PO matched line is goods received against a purchase order, so existing rows with
-- po_line_id set are backfilled to 'GOODS'. Rows with no PO line stay NULL (unmarked) until the
-- clerk marks them; the T5018 report counts unmarked lines as services and says so.

ALTER TABLE purchasing_bill_lines
    ADD COLUMN IF NOT EXISTS supply_kind TEXT NULL
        CHECK (supply_kind IN ('GOODS', 'SERVICES'));

UPDATE purchasing_bill_lines
   SET supply_kind = 'GOODS'
 WHERE po_line_id IS NOT NULL
   AND supply_kind IS NULL;
