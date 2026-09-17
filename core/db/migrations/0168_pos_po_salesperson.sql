-- 0168 — DIB-488 rev 6 (item D): optional PO / Job reference + salesperson on the counter sale.
--
-- Both facts live on the ORDER (not pos_sale) because they describe the order itself — the
-- customer's purchase-order / job reference and the person who made the sale — and every
-- downstream reader (order views, receipts, reporting) reads orders, not the POS facet.
--
-- po_job_ref     free text the customer dictates ("PO 4471", "SMITH DECK"), optional, capped at
--                64 chars (validated 422-in-words in the service BEFORE this CHECK could 500).
-- salesperson_id the staff member credited with the sale. Optional; the POS terminal defaults it
--                to the signed-in operator. ON DELETE SET NULL: a departed staff row never blocks
--                or cascades into committed orders.
ALTER TABLE orders ADD COLUMN po_job_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE orders ADD CONSTRAINT ck_orders_po_job_ref_len CHECK (char_length(po_job_ref) <= 64);
ALTER TABLE orders ADD COLUMN salesperson_id UUID REFERENCES users(id) ON DELETE SET NULL;
