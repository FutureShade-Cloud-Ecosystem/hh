-- 0378_return_line_dispositions.sql
--
-- Sub item 10.3, the three choices on a returned line and the write off of damaged goods.
--
-- Each returned line now says what became of the goods:
--   RESTOCK    the goods went back on the shelf (a RETURN stock move, COGS reversed to inventory)
--   DAMAGED    the goods came back unsaleable and are written off (the Q4 ruling: expensed to 5200
--              at the current average cost, the cost leaving COGS)
--   NO_RETURN  the customer keeps the goods; the money reversal only
-- Two quantities record what actually happened to the physical goods, because a RESTOCK or DAMAGED
-- line on goods that never left the shelf (a held or not yet picked order) is credit only, no stock
-- moves and no write off (the D1 defect and the Q5 ruling):
--   restocked_base    the quantity that went back on the shelf
--   written_off_base  the quantity written off
-- The cost of a write off is not stored: the posting reads the current average at posting time, the
-- way the COGS reversal does.
--
-- Rows from before this migration carry the return level restock flag they were made under:
-- restocked rows read RESTOCK with the whole quantity restocked, the rest read NO_RETURN.
--
-- The posting pair for pos.return.written_off is DR 5200 (expense) / CR 5000 (cogs).
--
-- Owner module: internal/pos (the columns and the emission), internal/gl (the posting rules).

ALTER TABLE pos_return_lines
  ADD COLUMN disposition TEXT NOT NULL DEFAULT 'RESTOCK'
    CHECK (disposition IN ('RESTOCK','DAMAGED','NO_RETURN')),
  ADD COLUMN restocked_base DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (restocked_base >= 0),
  ADD COLUMN written_off_base DECIMAL(19,4) NOT NULL DEFAULT 0 CHECK (written_off_base >= 0);

UPDATE pos_return_lines l
   SET disposition = CASE WHEN r.restock THEN 'RESTOCK' ELSE 'NO_RETURN' END,
       restocked_base = CASE WHEN r.restock THEN l.qty_base ELSE 0 END
  FROM pos_returns r
 WHERE r.id = l.return_id;

INSERT INTO posting_rules (event_type, line_role, account_code)
VALUES
    ('pos.return.written_off', 'expense', '5200'),
    ('pos.return.written_off', 'cogs',    '5000')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
