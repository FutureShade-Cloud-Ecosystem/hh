-- 0453: the stock ledger records a VALUE ONLY move (W2-5B).
--
-- A landed cost capitalization, a retro capitalization and a restore move the stock ledger's VALUE without
-- moving any stock: they raise (or put back) the weighted average cost, and the value they move is the value
-- the GL posts to account 1200. The ledger had no row for such an event, so the close pre flight's stock tie
-- (W2-5, which unwinds every movement after the month's end) read a month's end as if the event had already
-- been inside it, while 1200 read the month as it stood before the event's own journal: a landed cost applied
-- in one month refused the previous month's close by the absorbed amount, and a landed cost voided in one month
-- refused it by the restored amount.
--
-- The fix gives the ledger a VALUE move type: a row that moves NO quantity, names NO location, and carries
-- the SIGNED value the event moved, at the SAME instant the event's GL journal carries (the landed cost's own
-- clock). The as of read unwinds it, so the stock side and 1200 cut the event at one instant.
--
-- The three CHECK relaxations are narrow and additive in effect: a zero quantity row is allowed only for a
-- VALUE move, only a VALUE move may name no location, and the move type CHECK gains VALUE. Every quantity
-- move keeps qty_base > 0 and its location rule; the ledger stays append-only (0037's trigger).

ALTER TABLE stock_moves DROP CONSTRAINT stock_moves_qty_base_check;
ALTER TABLE stock_moves ADD CONSTRAINT stock_moves_qty_base_check
  CHECK (qty_base > 0 OR move_type = 'VALUE');

ALTER TABLE stock_moves DROP CONSTRAINT stock_moves_move_type_check;
ALTER TABLE stock_moves ADD CONSTRAINT stock_moves_move_type_check
  CHECK (move_type IN ('RECEIPT','TRANSFER','PICK','SHIP','ADJUST','COUNT','BREAK_PALLET','RETURN','VALUE'));

ALTER TABLE stock_moves DROP CONSTRAINT stock_moves_check;
ALTER TABLE stock_moves ADD CONSTRAINT stock_moves_check
  CHECK (from_location_id IS NOT NULL OR to_location_id IS NOT NULL OR move_type = 'VALUE');
