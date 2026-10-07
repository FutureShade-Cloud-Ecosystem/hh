-- 0365: the counter's manual discount and the manager who approved it (Phase 0, row 1047 item 3).
-- Owner module: internal/pos. Purely additive: columns with safe defaults, so every sale written
-- before this carries no discount and every reader keeps answering what it answered.
--
-- A Dibbits counter salesperson gives a discount only after a manager says yes in person. The
-- team keeps that verbal, no approval queue, but every manual discount names the manager who
-- approved it so it is auditable.
--
--   order_lines.discount_cents          the line's own discount, already out of line_total_cents
--   order_lines.discount_percent_bps    the percent entered (NULL when the discount was an amount)
--   order_lines.discount_approved_by    the approving manager's staff id
--   order_lines.discount_approved_role  that manager's role when approving (staff_role.id), kept
--                                       as a word so a later role change never rewrites a receipt
--   order_lines.sale_discount_cents     the line's share of the ticket discount, also out of
--                                       line_total_cents (largest remainder over the lines)
--   order_lines.discount_approved_qty / _base_cents
--                                       what the manager's yes was given for: the quantity and the
--                                       line total the discount was struck from. The ring refuses a
--                                       discount whose approval no longer matches, so a yes never
--                                       grows with the quantity or the ticket
--   pos_sale.discount_*                 the ticket discount: its cents, percent, approver and the
--                                       subtotal (after the line discounts) the yes was given for
--
-- line_total_cents stays what was CHARGED, so a return (refund = line total over quantity) refunds
-- what was paid with no special case, and the invoice lines, which copy it, add up.
--
-- The CHECKs: a discount of any size has an approver, and an approver implies a discount.

ALTER TABLE order_lines
    ADD COLUMN discount_cents         BIGINT NOT NULL DEFAULT 0 CHECK (discount_cents >= 0),
    ADD COLUMN discount_percent_bps   INTEGER NULL CHECK (discount_percent_bps IS NULL OR discount_percent_bps BETWEEN 1 AND 10000),
    ADD COLUMN discount_approved_by   UUID NULL REFERENCES users(id),
    ADD COLUMN discount_approved_role TEXT NULL,
    ADD COLUMN sale_discount_cents    BIGINT NOT NULL DEFAULT 0 CHECK (sale_discount_cents >= 0),
    ADD COLUMN discount_approved_qty  NUMERIC(19,4) NULL,
    ADD COLUMN discount_approved_base_cents BIGINT NULL;

ALTER TABLE order_lines
    ADD CONSTRAINT order_lines_discount_has_approver
    CHECK (discount_cents = 0 OR (discount_approved_by IS NOT NULL AND discount_approved_role IS NOT NULL));

ALTER TABLE pos_sale
    ADD COLUMN discount_cents         BIGINT NOT NULL DEFAULT 0 CHECK (discount_cents >= 0),
    ADD COLUMN discount_percent_bps   INTEGER NULL CHECK (discount_percent_bps IS NULL OR discount_percent_bps BETWEEN 1 AND 10000),
    ADD COLUMN discount_approved_by   UUID NULL REFERENCES users(id),
    ADD COLUMN discount_approved_role TEXT NULL,
    ADD COLUMN discount_approved_base_cents BIGINT NULL;

ALTER TABLE pos_sale
    ADD CONSTRAINT pos_sale_discount_has_approver
    CHECK (discount_cents = 0 OR (discount_approved_by IS NOT NULL AND discount_approved_role IS NOT NULL));
