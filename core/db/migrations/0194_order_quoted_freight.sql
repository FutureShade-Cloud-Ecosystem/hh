-- 0194 — the QUOTED freight baseline on an order, so a raised freight has something to be raised ABOVE.
--
-- THE CLIENT'S CASE, verbatim: "If there is a situation where they're adding a second truck that's
-- unforeseen and there's some cost that you're trying to pass on to the customer they weren't
-- originally quoted on, we'll have some sort of approval gate for that."
--
-- A gate on "the freight went up" needs two numbers, and this deployment had neither of them
-- populated. Both facts were measured on this tree before the column was written, and both matter:
--
--   1. THE LINK IS NOT MISSING. The lane that filed this gate recorded the blocker as
--      "quotes.delivery_fee_cents sits on another row with no link maintained after conversion"
--      (NOTES-DISPATCH-COMMIT.md §G). That is not true of this tree. `orders.quote_id` has existed
--      since 0008 (:50), is indexed (:67), and quote/convert.go:179-180 stamps it inside the
--      conversion's own seven-column INSERT; 0162 adds a second, stronger link
--      (quote_accept_provenance, UNIQUE(order_id)) written in the same transaction. The backfill
--      below is therefore a one-line join, not archaeology.
--
--   2. THE ACTUAL IS NOT MISSING — IT IS UNWRITTEN. `orders.freight_cents` (0184:251) has ZERO
--      production write sites. Not the POS ring, not the conversion, not the order edit path, not
--      cmd/seed. The only writers in the whole tree are three fixture .sql scripts, and its only
--      reader — dispatchOrderValueAdapter.OrderFreightCents — maps `cents <= 0` to "unknown", so
--      dispatch.SplitFreight refuses every real order in words before it does any arithmetic. The
--      freight split shipped arithmetically complete and economically inert.
--
-- So this migration adds the BASELINE, and the conversion (quote/convert.go, same change) starts
-- writing BOTH: `freight_cents` as the opening actual and `quoted_freight_cents` as the frozen
-- baseline, from the one figure the customer was actually shown.
--
-- ── WHY NULLABLE, WHEN ITS NEIGHBOUR IS `NOT NULL DEFAULT 0` ─────────────────────────────────────
--
-- Because "no baseline" and "quoted zero" are different facts and the gate treats them oppositely.
--
--   NULL  the order never came from a quote (a counter sale, an order typed in directly, or any of
--         the 38 demo orders). There is nothing to compare against, so the gate MUST NOT fire — an
--         order with no quote baseline is never gated, which is the brief's own acceptance clause.
--   0     the customer WAS quoted, and was quoted no freight. Raising freight above zero is exactly
--         the unforeseen cost the client described, so this is the case the gate exists for.
--
-- 0184 chose `NOT NULL DEFAULT 0` for `freight_cents` and that choice is why its reader has to map
-- `<= 0` to "unknown" — the column cannot say "nobody has priced this" (see the rendering it causes:
-- every unsplit load reads "Freight $0.00" rather than a dash). This column deliberately does NOT
-- copy that shape, and nothing may read it through a `<= 0` rule.
--
-- ── WHY NOT WIDEN quotes.delivery_fee_cents' MEANING INSTEAD ─────────────────────────────────────
--
-- Because a quote is EDITABLE and an order's baseline must not be. `quotes.delivery_fee_cents` is
-- frozen after SENT by the edit matrix, but a quote can be revised into a new version, and one quote
-- may legally mint SEVERAL orders (0008 puts no unique constraint on quote_id, and DIB-491 relies on
-- that). A per-ORDER stamp is immune to both; a read-time join back to the quote is not — see
-- portal/projects.go:30-33, which documents the same fan-out hazard for its own rollup.
--
-- ── SAFETY ──────────────────────────────────────────────────────────────────────────────────────
--
-- ADD COLUMN NULL + one UPDATE. Nothing is dropped, nothing is renamed, no existing value changes
-- except from NULL to a figure the customer was already shown. Staging is never wiped; this applies
-- clean from zero and on top of a live 0191 database, and re-running it is a no-op because
-- cmd/migrate records the version and never replays a file.
-- Reverting costs `ALTER TABLE orders DROP COLUMN quoted_freight_cents` and the loss of the
-- baselines the backfill derived, which are re-derivable from the same join for as long as the
-- quotes survive.

ALTER TABLE orders
  ADD COLUMN quoted_freight_cents BIGINT CHECK (quoted_freight_cents IS NULL OR quoted_freight_cents >= 0);

COMMENT ON COLUMN orders.quoted_freight_cents IS
  'The freight the customer was QUOTED, frozen at conversion — the baseline dispatch measures a raised freight against. NULL means this order never came from a quote and must never be gated; 0 means the customer was quoted no freight, which is the case the gate exists for. Never read this through a "<= 0 is unknown" rule: that conflates the two.';

-- ── the backfill ────────────────────────────────────────────────────────────────────────────────
--
-- Every order that carries a quote_id takes its quote's fee as its baseline. Orders with no quote
-- stay NULL and are never gated.
--
-- IT IS EXPECTED TO YIELD ZERO NONZERO BASELINES ON THIS TREE'S SEEDED DATA, and that is not a
-- failure of the backfill. `quotes.delivery_fee_cents` arrived in 0173; the only fixture order with a
-- quote_id (0055:83-88) points at a pre-0173 quote whose fee is 0 by column default, and the seven
-- orders that carry freight_cents (the loadboard and dispatch-commit fixtures) all have quote_id
-- NULL. The backfill is written for the deployment's real history, not for the fixtures.
--
-- A DELIVERY quote is the only kind that can carry a nonzero fee — builder.go:386-389 ZEROES
-- delivery_fee_cents whenever fulfillment is PICKUP — so "quoted 0" is the overwhelmingly common
-- honest case and the gate must not read it as "unquoted".
UPDATE orders o
   SET quoted_freight_cents = q.delivery_fee_cents
  FROM quotes q
 WHERE q.id = o.quote_id
   AND o.quoted_freight_cents IS NULL;
