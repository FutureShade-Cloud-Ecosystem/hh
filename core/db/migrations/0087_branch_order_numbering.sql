-- 0087: branch-aware order numbering (DIB-388).
--
-- THE BUG: three writers minted order numbers three different ways. pos/repository.go and
-- order/reorder.go used '#SO-' || nextval('order_no_seq'); quote/repository.go used a BARE
-- nextval('order_no_seq')::text. order.Repository.ResolveOrderID normalizes a number by prepending '#',
-- so a quote-converted order ('4') could never be resolved by its own number — the customer's order
-- was unreachable by the only identifier they were shown.
--
-- THE RULE (ruled): order numbers are branch-prefixed — #SO-TRT-1041, #SO-KGN-2087. One helper,
-- next_order_no(branch_id), is the single mint. All three writers call it, so the three-way drift
-- cannot recur: a fourth writer that forgets the format has to go out of its way to.
--
-- WHY A TABLE, NOT ONE SEQUENCE PER BRANCH: a sequences table keyed by branch needs no DDL when a
-- branch is opened (a CREATE SEQUENCE at runtime would need elevated rights and could not be rolled
-- back with the row that motivated it). Per-branch rows also mean two branches never contend on the
-- same counter. The INSERT … ON CONFLICT DO UPDATE below takes a row lock, so concurrent mints on one
-- branch serialize and gaps only appear where a transaction rolls back — the same guarantee a
-- sequence gives, minus the "sequences are not transactional" surprise.

CREATE TABLE order_no_sequences (
  branch_id  UUID PRIMARY KEY REFERENCES locations(id) ON DELETE RESTRICT,
  last_value BIGINT NOT NULL DEFAULT 0 CHECK (last_value >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the existing branches ABOVE the legacy global counter, so a new branch-coded number is never a
-- lower ordinal than the legacy '#SO-N' series it succeeds (nothing depends on this — it just keeps a
-- counter clerk from reading #SO-TRT-1 as older than #SO-9).
INSERT INTO order_no_sequences (branch_id, last_value)
SELECT l.id, GREATEST((SELECT last_value FROM order_no_seq), 0)
  FROM locations l
 WHERE l.loc_type = 'BRANCH'
ON CONFLICT (branch_id) DO NOTHING;

-- next_order_no mints the next number for a branch: '#SO-' || the branch's locations.code || '-' || n.
-- The argument may be a BRANCH location or any location beneath one (orders.branch_id references
-- locations(id); the column is branch-typed by convention, not by constraint) — either resolves to the
-- owning branch's code, so the number always names a real branch. An unknown branch RAISES rather than
-- minting an unattributable number: an order that cannot be branch-stamped must not be written at all.
CREATE FUNCTION next_order_no(p_branch_id UUID) RETURNS TEXT AS $$
DECLARE
  v_code TEXT;
  v_root UUID;
  v_n    BIGINT;
BEGIN
  SELECT CASE WHEN l.loc_type = 'BRANCH' THEN l.id ELSE l.branch_id END
    INTO v_root
    FROM locations l
   WHERE l.id = p_branch_id;
  IF v_root IS NULL THEN
    RAISE EXCEPTION 'next_order_no: unknown branch %', p_branch_id;
  END IF;

  SELECT l.code::text INTO v_code FROM locations l WHERE l.id = v_root;
  IF v_code IS NULL OR v_code = '' THEN
    RAISE EXCEPTION 'next_order_no: branch % has no code', v_root;
  END IF;

  INSERT INTO order_no_sequences (branch_id, last_value)
       VALUES (v_root, 1)
  ON CONFLICT (branch_id)
  DO UPDATE SET last_value = order_no_sequences.last_value + 1, updated_at = now()
    RETURNING last_value INTO v_n;

  RETURN '#SO-' || upper(v_code) || '-' || v_n::text;
END;
$$ LANGUAGE plpgsql;

-- Backfill: every pre-existing order_no is brought onto the resolvable form. ResolveOrderID normalizes a
-- typed number by prepending '#', so a stored number WITHOUT the '#' can never be found — that is the
-- legacy quote-conversion damage plus the demo/seed rows ('SO-2026-0001'). Prepending '#' is the whole
-- repair: the legacy '#SO-N' shape keeps resolving unchanged, and these now resolve too.
--
-- COUNTED: the migration reports how many rows it repaired and how many it deliberately skipped, then
-- proves the invariant it claims (no row left both unprefixed and repairable).
DO $$
DECLARE
  v_fixed   BIGINT;
  v_skipped BIGINT;
BEGIN
  WITH repaired AS (
    UPDATE orders o SET order_no = '#' || o.order_no
     WHERE o.order_no !~ '^#'
       -- Do not collide with an existing row that already holds the prefixed form (order_no is CITEXT
       -- UNIQUE). Such a row is left alone and counted as skipped rather than aborting the migration.
       AND NOT EXISTS (SELECT 1 FROM orders x WHERE x.order_no = '#' || o.order_no)
    RETURNING 1
  )
  SELECT count(*) INTO v_fixed FROM repaired;

  SELECT count(*) INTO v_skipped FROM orders WHERE order_no !~ '^#';

  RAISE NOTICE '0087: order_no backfill — % repaired, % skipped (prefixed twin already present)',
    v_fixed, v_skipped;

  IF EXISTS (
    SELECT 1 FROM orders o
     WHERE o.order_no !~ '^#'
       AND NOT EXISTS (SELECT 1 FROM orders x WHERE x.order_no = '#' || o.order_no)
  ) THEN
    RAISE EXCEPTION '0087: order_no backfill left % repairable row(s) unprefixed', v_skipped;
  END IF;
END;
$$;

-- order_no_seq is deliberately LEFT IN PLACE: it is the provenance of every legacy '#SO-N' number, and
-- the seed value above reads it. It simply has no writer any more.
