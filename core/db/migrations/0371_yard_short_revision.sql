-- 0371: a revision on the shortfall record of an order line. Owner: internal/order.
--
-- THE REASON. The notice that tells an order's sales rep about a short pick is posted under an
-- idempotency key so a repeat of the same record is never announced twice and a notice that was lost
-- is sent again when the yard repeats the record. A content key alone cannot tell the first short
-- from a later identical one: short 3, restored to 5, short 3 again would collapse into one message
-- and the rep would never hear about the second. The revision counts the changes of one line's
-- shortfall, so each real change has its own key and a repeat keeps the key it already had.
--
-- Additive: one column with a default on a table 0369 created. A row that exists starts at 1.
ALTER TABLE order_line_shortfalls
  ADD COLUMN IF NOT EXISTS revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1);
