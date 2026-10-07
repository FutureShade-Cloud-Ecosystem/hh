-- 0369: each line's own tax, frozen while the order is whole. Owner: internal/order.
--
-- THE GAP. 0366 pro-rates the order header's tax when a short pick lowers a line, but each order
-- line carries its own tax (order_lines.tax_cents, 0042, the tax engine's split of the header tax)
-- and a return reads THAT figure: a full return of a shorted line refunded the line's reduced money
-- plus its full original tax. The lines no longer summed to the header either.
--
-- THE FIX. order_line_tax_basis holds every line's ordered tax, taken whenever the order is whole (the
-- same moment the header's ordered basis is taken, 0366), so a line's tax is always re-derived from
-- the originals and never from the last pro-ration, and a correction restores each line to the cent.
-- Every line needs one, not only the short ones: the largest remainder split moves cents between
-- lines, and a whole line's tax is only the original if the original was kept.
--
-- Additive: one new table, no change to an existing table, no backfill (an order never short has no
-- rows here, which reads as "the lines' tax is what it is").
CREATE TABLE IF NOT EXISTS order_line_tax_basis (
  order_line_id     UUID PRIMARY KEY REFERENCES order_lines(id) ON DELETE CASCADE,
  order_id          UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  ordered_tax_cents BIGINT NOT NULL CHECK (ordered_tax_cents >= 0)
);

CREATE INDEX IF NOT EXISTS idx_order_line_tax_basis_order ON order_line_tax_basis (order_id);

COMMENT ON TABLE order_line_tax_basis IS
  '0369: each order line''s tax as ordered, taken while the order is whole, so a short pick re-derives line tax from the originals.';
