-- 0214 · a purchase order remembers whether the supplier delivers or the yard collects.
--
-- DIB-572 asked the receiving worklist for a "pickup / delivered" column, and purchasing had nowhere
-- to keep the answer. The two words already exist on the SALES side — order.Fulfillment is
-- PICKUP | DELIVERY | WILL_CALL, and dispatch splits its load board on them — but that is how goods
-- LEAVE. Nothing in the program recorded how they ARRIVE, so a yard that dispatches its own flatbed
-- to a supplier read the same worklist as one waiting on a vendor truck.
--
-- WHY THE DEFAULT IS 'DELIVERY' AND NOT AN 'UNKNOWN' THIRD STATE. Every purchase order raised before
-- this migration was raised by a buyer who was not asked the question, and a buyer who is not
-- arranging a truck is waiting for one. 'UNKNOWN' would be the honest label for a row nobody
-- classified, but it would also be the label on every historical row for ever, and a column that
-- reads "Unknown" down its whole length is a column a receiver stops looking at. The two-value
-- CHECK is the same shape as order.Fulfillment's, minus WILL_CALL, which is a customer's word.

ALTER TABLE purchasing_pos
  ADD COLUMN delivery_method TEXT NOT NULL DEFAULT 'DELIVERY'
  CHECK (delivery_method IN ('PICKUP','DELIVERY'));

COMMENT ON COLUMN purchasing_pos.delivery_method IS
  'How the goods reach the yard: DELIVERY (the supplier brings them) or PICKUP (we collect).';

-- The receiving worklist filters on branch and status and then reads this column; it does not filter
-- on it, so this index is deliberately NOT created. See idx_purchasing_pos_branch_status.
