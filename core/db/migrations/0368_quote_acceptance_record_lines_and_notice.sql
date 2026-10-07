-- 0368: the acceptance record keeps what the customer was shown: the lines as accepted and the notice.
--
-- quote_acceptances snapshots the name, the time and the money (0086) and, since 0366, whether the
-- customer confirmed the quantities. In a dispute about quantities that is not enough: the lines can
-- be edited afterwards and the notice wording is the dealer's to change, so the record must hold the
-- lines as they stood at the accept and the notice words that were on the page.
--
-- THE BACKFILL. Acceptances taken before this migration have no snapshot to give: their lines read as
-- an empty list and their notice as the empty string, which says plainly that none was kept, and the
-- readers show nothing for them rather than today's wording or today's lines.
ALTER TABLE quote_acceptances
  ADD COLUMN IF NOT EXISTS quantity_notice TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS accepted_lines JSONB NOT NULL DEFAULT '[]'::jsonb;

COMMENT ON COLUMN quote_acceptances.quantity_notice IS
  'The quantity notice wording on the page at the accept (branch over dealer default); empty on acceptances taken before 0368';
COMMENT ON COLUMN quote_acceptances.accepted_lines IS
  'The quote lines as accepted: line_no, description, qty_entered, uom_entered, unit_price_cents, line_total_cents; empty on acceptances taken before 0368';
