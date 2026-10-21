-- 0430: an index on invoices.reverses_invoice_id (SF-2e).
--
-- A credit memo linked to an invoice is capped at what the invoice has left, and the remainder is the invoice's total
-- less the sum of every memo already linked to it. That sum is read inside the memo's own transaction, under the lock
-- on the reversed invoice, by reverses_invoice_id. The column had no index (0146 added it with a foreign key only),
-- so the read was a scan of invoices. Partial, because almost every invoice reverses nothing.

CREATE INDEX IF NOT EXISTS invoices_reverses_invoice_id_idx
    ON invoices (reverses_invoice_id)
    WHERE reverses_invoice_id IS NOT NULL;
