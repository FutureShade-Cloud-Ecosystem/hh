-- 0381_credited_open_lines.sql
--
-- Item 10, Q5: "a return of unfulfilled goods is credit only, the open lines reduced". When goods that
-- never left the shelf are credited, the order must stop asking the yard for them, or the yard picks
-- and ships goods the customer was already paid for.
--
-- order_lines.released_base is the quantity of the line released from fulfilment by a credit only
-- return. The line's quantity (qty_base) is NOT touched: it is also the quantity the returns are
-- measured against, and the price and the invoice stay as sold. What is still open to fulfil is
-- qty_base less fulfilled_base less released_base, and the CHECK keeps the three honest.
-- Owner module: internal/order (the column and the readers of the open quantity).
--
-- pos_return_lines.credit_only_base is the part of a returned line that was credit only (the goods
-- never left the shelf, so none came back): the part that DID come back is the rest, which is what the
-- next return's restock and write off are measured from. Owner: internal/pos.

ALTER TABLE order_lines
  ADD COLUMN released_base DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (released_base >= 0),
  ADD CONSTRAINT order_lines_released_within_qty CHECK (fulfilled_base + released_base <= qty_base);

ALTER TABLE pos_return_lines
  ADD COLUMN credit_only_base DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (credit_only_base >= 0);
