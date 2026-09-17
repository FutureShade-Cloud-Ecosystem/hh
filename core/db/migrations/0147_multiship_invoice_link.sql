-- 0147: DIB-422 — the shipment group → invoice link, and the CLOSED end of the group ladder.
--
-- 0126 shipped the group ladder ending at COMPLETE ("the truck came back") and left invoicing as a
-- published DECISION with no writer (InvoiceReadiness + the MultiShipInvoicer port). With the writer
-- landing in billing, a group needs to record WHICH document billed it — otherwise "invoice the
-- completed groups" is not idempotent in any meaningful sense: re-running it would re-bill work that
-- has already been billed, and under ON_DEMAND_SUBSET that is not a hypothetical, it is the normal
-- monthly rhythm of a phased job.
--
-- ── WHY A LINK COLUMN AND NOT A NEW STATUS ────────────────────────────────────────────────────
-- The temptation is a seventh ladder rung, 'INVOICED'. It is wrong: the ladder describes where the
-- STOCK is (planned → picked → on the truck → delivered), and billing is not a place stock can be.
-- A group that has been billed is still COMPLETE; it now additionally has a document. Two facts, two
-- columns. Adding a rung would also mean every existing ladder read — legal actions, readiness, the
-- surface — has to learn a state that says nothing about fulfilment.
--
--   invoice_id   the document that billed this group. NULL = not yet billed.
--   invoiced_at  when. Redundant with the invoice's own issue_date only until somebody re-issues a
--                document; then it is the group's own record of when IT was billed.
--
-- The FK is ON DELETE SET NULL, matching invoices.order_id in 0009: a purged document must not take
-- the fulfilment record with it.
--
-- ── THE IDEMPOTENCE RULING ────────────────────────────────────────────────────────────────────
-- A group with a non-NULL invoice_id is NOT invoiceable again. That is enforced in the writer's
-- read (only unbilled COMPLETE groups are collected) AND, one layer below, by the partial UNIQUE
-- index on invoices.idempotency_key in 0146 — the writer keys on the exact group set, so a
-- concurrent double-click races to one row rather than two documents.

BEGIN;

ALTER TABLE shipment_groups
    ADD COLUMN IF NOT EXISTS invoice_id  UUID REFERENCES invoices(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS invoiced_at TIMESTAMPTZ;

-- A group cannot be billed before it exists as a delivered unit. CANCELLED is excluded too: a
-- cancelled group carries no money and billing one would be a charge for a truck that never went.
ALTER TABLE shipment_groups
    DROP CONSTRAINT IF EXISTS ck_shipment_groups_invoiced_is_complete;
ALTER TABLE shipment_groups
    ADD CONSTRAINT ck_shipment_groups_invoiced_is_complete
        CHECK (invoice_id IS NULL OR status = 'COMPLETE');

-- And a billed group must say when.
ALTER TABLE shipment_groups
    DROP CONSTRAINT IF EXISTS ck_shipment_groups_invoiced_stamped;
ALTER TABLE shipment_groups
    ADD CONSTRAINT ck_shipment_groups_invoiced_stamped
        CHECK ((invoice_id IS NULL) = (invoiced_at IS NULL));

CREATE INDEX IF NOT EXISTS idx_shipment_groups_invoice
    ON shipment_groups(invoice_id) WHERE invoice_id IS NOT NULL;

COMMIT;
