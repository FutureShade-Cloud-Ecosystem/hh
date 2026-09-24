-- 0216 · a purchase order may say that nobody recorded how the goods arrive.
--
-- WHAT 0214 GOT WRONG. 0214 gave purchasing_pos a delivery_method with two values and a DELIVERY
-- default, and argued for that default in its own comment: a buyer who was not asked is waiting for
-- a truck, and a column reading "Unknown" down its whole length is a column a receiver stops looking
-- at. The half that argument did not say is the half that matters: NOTHING COULD WRITE THE COLUMN
-- AFTER THE DRAFT WAS CREATED. No route, no service method, no UPDATE anywhere. So the default was
-- not a starting guess a person could correct — it was a permanent assertion. A yard that sends its
-- own flatbed to the supplier read "Delivered" on every order it had ever raised, on a worklist a
-- receiver plans the day from, with no row it could put right.
--
-- A default is defensible when it is cheap to correct. This release makes it correctable
-- (PUT /api/v1/purchasing/pos/{id}/delivery-method) and, having done that, stops the program
-- asserting an arrival that no person ever stated.
--
-- THE THIRD VALUE IS NOT A NULL. "Nobody recorded it" is a fact about the order and it is rendered
-- as one — "Not recorded", not an empty cell, which on a table reads as a rendering fault and, in
-- this column, reads as Delivered. One representation of not knowing, not two.

ALTER TABLE purchasing_pos DROP CONSTRAINT IF EXISTS purchasing_pos_delivery_method_check;

ALTER TABLE purchasing_pos
  ADD CONSTRAINT purchasing_pos_delivery_method_check
  CHECK (delivery_method IN ('PICKUP','DELIVERY','UNKNOWN'));

-- The column default follows the same rule as the service's normalizeDeliveryMethod: a writer who
-- says nothing has recorded nothing.
ALTER TABLE purchasing_pos ALTER COLUMN delivery_method SET DEFAULT 'UNKNOWN';

-- THE BACKFILL, AND WHY IT IS SCOPED RATHER THAN BLANKET.
--
-- Rows created before 0214 was applied are exactly the rows raised while the column did not exist —
-- so no buyer could have been asked, and the DELIVERY that 0214's ADD COLUMN DEFAULT wrote onto them
-- is a fact the program invented. Those become UNKNOWN.
--
-- Rows created after 0214 applied are left alone. On a real deployment there are none — 0214 and
-- 0216 land in the same release and the form that asks the question ships with them — but on a
-- developer's database, where 0214 has been live for a day, a PICKUP somebody deliberately chose is
-- an answer, and a migration that erased it would be making the same mistake in the other direction.
--
-- THE ONE ROW THIS LEAVES AMBIGUOUS, named rather than glossed: an order raised on a developer's
-- database in the window BETWEEN 0214 and 0216, by a caller that named nothing, carries 0214's
-- DELIVERY and is indistinguishable here from a buyer who chose it. That row keeps DELIVERY. The
-- window is one branch's lane databases and it closes when this release ships; widening the backfill
-- to catch it would erase every deliberate DELIVERY in the same sweep, which is the worse trade.
--
-- COALESCE(..., now()) is the honest fallback if the bookkeeping row is somehow absent: with no way
-- to tell when the column arrived, treat every existing row as unclassified. The failure direction
-- of this migration is "we do not know", never "we assert Delivered".
--
-- updated_at is deliberately NOT touched. This is a correction of something the schema said, not
-- something a person did to the order, and bumping the stamp would make it look like one.
-- IT IS A DO BLOCK BECAUSE schema_migrations IS THE RUNNER'S BOOKKEEPING, NOT THE SCHEMA'S.
-- cmd/migrate creates that table; cmd/demoseed's own test harness applies these same files to a
-- scratch database WITHOUT it, and Postgres resolves every relation in a statement at PARSE time —
-- so a plain subquery here fails six sibling tests with `relation "schema_migrations" does not
-- exist` before it can be guarded. Deferring the lookup into EXECUTE is what lets the guard run.
DO $mig$
DECLARE
  cut timestamptz;
BEGIN
  IF to_regclass('public.schema_migrations') IS NOT NULL THEN
    -- ORDER BY applied_at DESC, and the direction is the whole point. Commit 84232f0a records that
    -- two lanes landing a 0214_* file at once is a collision this repo has actually had; with a bare
    -- LIMIT 1 the row Postgres returns is then arbitrary. Of the two possible cuts, the LATER one
    -- back-fills MORE rows and the earlier one back-fills fewer — and fewer means some order raised
    -- before the column existed keeps the DELIVERY the program invented for it. That is the one
    -- direction this migration says out loud that it never fails in. So: take the later stamp, fail
    -- towards "we do not know", never towards "we assert Delivered".
    EXECUTE 'SELECT applied_at FROM schema_migrations WHERE version LIKE ''0217!_%'' ESCAPE ''!'' '
            'ORDER BY applied_at DESC LIMIT 1'
      INTO cut;
  END IF;

  UPDATE purchasing_pos
     SET delivery_method = 'UNKNOWN'
   WHERE delivery_method = 'DELIVERY'
     AND created_at < COALESCE(cut, now());
END
$mig$;

COMMENT ON COLUMN purchasing_pos.delivery_method IS
  'How the goods reach the yard: DELIVERY (the supplier brings them), PICKUP (we collect), or '
  'UNKNOWN (nobody has recorded it). Set at draft time and correctable while the order is DRAFT, '
  'ORDERED or PARTIAL.';
