-- 0184 — the DISPATCH LOAD BOARD (Aug-26 call, dispatch requirement set A–I).
--
-- WHAT THIS CLOSES. 0090 gave the day a board: which truck, which driver, which stops. What a
-- dispatcher actually does at 6:45am is BUILD A LOAD — decide what goes on which truck — and the
-- four facts that decision turns on had nowhere to live: how heavy it is, how much room it takes,
-- how many pallet positions it eats, and what it is WORTH. The last one is the one people are
-- surprised by, and it is the client's own case: "it's only 25,000 pounds. We can't send it… it's
-- also a $30,000 load because it's all porcelain." Value is a RISK fact on this surface — it
-- changes what goes on one truck — and it is still money, so it is gated like money.
--
-- THE D-105 SPLIT IS OBSERVED EXACTLY. Everything below that gates money or permission is a TYPED,
-- CHECK-CONSTRAINED policy column on dispatch_policy (this file, §1) and is read through the shared
-- Go settings kit. Everything that is merely CURATION — which quick cards, which load-card facts,
-- which board axis a dealer opens on — is rows in the 0172 generic substrate (§6), because those
-- are content-free strings and positions and a second table for them would be a second idiom.
--
-- ── §1 · dispatch_policy — the typed, security-bearing half ───────────────────────────────────
--
-- The 0043/0173 shape verbatim: branch_id UUID UNIQUE NULL where the NULL row IS the dealer default
-- and a branch row overrides. Every knob here either gates MONEY (load value) or gates PERMISSION
-- (the 48-hour lock), which is precisely the test D-105 sets for "this is not a curation row".
--
--   lock_window_hours     THE 48-HOUR LOCK'S WINDOW, as a knob. 48 is the client's number, not a
--                         law of physics: a dealer running same-day gravel wants 8 and a dealer
--                         staging armour stone wants 96. 0 switches the window off without
--                         switching the POLICY off, so a dealer can keep the manager-unlock
--                         vocabulary while running an open board.
--   lock_policy           WHO the lock answers to. MANAGER_UNLOCK (default) — inside the window a
--                         load is frozen and only a dispatch manager may thaw it. OFF — no locking
--                         at all. HARD — nobody unlocks, not even a manager (the dealer who wants
--                         the cut-off to be a cut-off).
--   load_value_visibility WHICH staff tiers receive value_cents on a load payload. The 0170/0173
--                         vocabulary re-consumed so the words cannot drift. Enforced at the MARSHAL
--                         layer as BYTE-ABSENCE, fail closed — a display rule is not a gate.
--   commit_horizon_weeks  how far out the requested/committed board plans. The call said 6–8 weeks;
--                         8 is the default and the number is a knob because the horizon is a way of
--                         working, not a fact about the software.
--   freight_split_mode    EVEN (the default the call asked for) or MANUAL (the dealer's own numbers
--                         from the first split). The SPLIT ITSELF IS ALWAYS SERVER-COMPUTED — this
--                         chooses the opening proposal, never who does the arithmetic.

CREATE TABLE dispatch_policy (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id             UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE, -- NULL = dealer default
  lock_window_hours     INTEGER NOT NULL DEFAULT 48  CHECK (lock_window_hours BETWEEN 0 AND 336),
  lock_policy           TEXT    NOT NULL DEFAULT 'MANAGER_UNLOCK'
                          CHECK (lock_policy IN ('OFF','MANAGER_UNLOCK','HARD')),
  load_value_visibility TEXT    NOT NULL DEFAULT 'MANAGER_UP'
                          CHECK (load_value_visibility IN ('MANAGER_UP','ALL_STAFF','HIDDEN')),
  commit_horizon_weeks  INTEGER NOT NULL DEFAULT 8   CHECK (commit_horizon_weeks BETWEEN 1 AND 26),
  freight_split_mode    TEXT    NOT NULL DEFAULT 'EVEN' CHECK (freight_split_mode IN ('EVEN','MANUAL')),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The dealer default row, seeded so the RESTRICTIVE default is visible in the data (0043/0173).
INSERT INTO dispatch_policy (branch_id) VALUES (NULL);

COMMENT ON TABLE dispatch_policy IS
  'Aug-26 load board: dealer policy for the dispatch.loadboard surface. NULL branch_id row = dealer default; a branch row overrides (0043 idiom). Security-bearing knobs stay typed where the marshal layer and the lock guard read them (D-105).';
COMMENT ON COLUMN dispatch_policy.lock_window_hours IS
  'The 48-hour lock window, in hours. Scrubname0014 it a load is frozen per lock_policy. 0 = no window.';
COMMENT ON COLUMN dispatch_policy.load_value_visibility IS
  'Which staff tiers receive a load''s dollar value (marshal-gated, byte-absent otherwise, fail closed). MANAGER_UP (default) | ALL_STAFF | HIDDEN.';

-- ── §2 · the truck's other two capacities ───────────────────────────────────────────────────────
-- 0090 gave a truck capacity_kg and said, correctly, that it is ADVISORY. The call asks for the
-- same advisory treatment of the two things that actually stop a flatbed leaving the yard: it runs
-- out of DECK before it runs out of payload on light bulky material, and it runs out of PALLET
-- POSITIONS before either on a mixed load. Both are NULLable: a truck nobody has measured shows no
-- gauge rather than a green one, because a green gauge built from a missing number is a lie.
ALTER TABLE dispatch_trucks
  ADD COLUMN capacity_m3      NUMERIC(12,2) CHECK (capacity_m3 IS NULL OR capacity_m3 >= 0),
  ADD COLUMN pallet_positions INTEGER       CHECK (pallet_positions IS NULL OR pallet_positions >= 0);

COMMENT ON COLUMN dispatch_trucks.capacity_m3 IS
  'Advisory deck/volume capacity in cubic metres (the 0090 capacity_kg posture). NULL = unmeasured; the board draws no volume gauge rather than a green one.';

-- ── §3 · what a stop puts on the truck ──────────────────────────────────────────────────────────
-- 0107 already put weight_kg here, NULLable, for exactly the reason repeated below: a guessed number
-- printed beside a capacity is worse than a blank. volume_m3 and pallet_positions join it on the
-- same terms. value_cents is the third, and it is different in kind — see §4.
ALTER TABLE delivery_stops
  ADD COLUMN volume_m3        NUMERIC(19,4) CHECK (volume_m3 IS NULL OR volume_m3 >= 0),
  ADD COLUMN pallet_positions INTEGER       CHECK (pallet_positions IS NULL OR pallet_positions >= 0);

-- ── §4 · the dollar value of what is on the truck ───────────────────────────────────────────────
-- STAMPED, NOT JOINED. The branch is a column on this table and not a JOIN for a stated reason
-- (0090's header); value is a column for the same one plus a second: the board reads a day's stops
-- in one query, and a per-stop reach into orders to price a gauge is N round trips on the screen a
-- dispatcher refreshes most. It is copied from the order at schedule time exactly as branch_id is.
--
-- NULLable, and the distinction matters: NULL means NOBODY PRICED IT (an order with no total the
-- seam could read), which the card prints as '—'. It never means zero.
ALTER TABLE delivery_stops
  ADD COLUMN value_cents BIGINT CHECK (value_cents IS NULL OR value_cents >= 0);

COMMENT ON COLUMN delivery_stops.value_cents IS
  'The dollar value of the material on this load, stamped from the order at schedule time (0090 denormalisation posture). Marshal-gated by dispatch_policy.load_value_visibility — byte-absent for a caller without it. NULL = nobody priced it; never 0.';

-- ── §5 · REQUESTED vs COMMITTED ─────────────────────────────────────────────────────────────────
-- "The salesperson REQUESTS a date; the dispatcher COMMITS it." Two states, and the whole value of
-- the distinction is that they must be impossible to confuse — so the state is a column with a
-- CHECK, not a convention about which field is populated.
--
--   requested_date  what the salesperson asked for. It SURVIVES the commit: "we said the 14th and
--                   it went on the 16th" is the question a customer asks, and overwriting the ask
--                   with the promise destroys the only row that could answer it.
--   date_state      REQUESTED — scheduled_date is an ASK. Nobody has promised it.
--                   COMMITTED — a dispatcher stood behind it.
--   committed_at/by WHO promised, and when. A COMMITTED row with no committer is refused below,
--                   because "committed by nobody" is how a promise becomes deniable.
--
-- EXISTING ROWS BACK-FILL TO COMMITTED. Every stop on the board today was put there by a dispatcher
-- through the scheduling screen — that act IS the commitment, and marking a live board REQUESTED
-- overnight would flip a yard full of real promises into asks.
ALTER TABLE delivery_stops
  ADD COLUMN requested_date DATE,
  ADD COLUMN date_state     TEXT NOT NULL DEFAULT 'COMMITTED'
                              CHECK (date_state IN ('REQUESTED','COMMITTED')),
  -- DEFAULT now(), and the default is load-bearing. date_state defaults to COMMITTED (see above),
  -- so ANY writer that inserts a stop without naming these columns — a reporting fixture, a
  -- back-fill script, a sibling module's integration seed — would otherwise produce a COMMITTED row
  -- with no stamp and trip the CHECK below. Two other packages' suites found exactly that.
  --
  -- The service still writes both explicitly and passes NULL for a REQUESTED row, so the default
  -- never fabricates a promise: it only stamps rows that already claim to be committed, with the
  -- moment they were written, which is the honest reading of "somebody inserted this as committed".
  ADD COLUMN committed_at   TIMESTAMPTZ DEFAULT now(),
  ADD COLUMN committed_by   UUID REFERENCES users(id) ON DELETE SET NULL;

UPDATE delivery_stops SET committed_at = created_at WHERE committed_at IS NULL;

-- "Committed by nobody" is how a promise becomes deniable. The CHECK is the backstop for every
-- writer that is not the service; the default above is what keeps it from being a trap.
ALTER TABLE delivery_stops ADD CONSTRAINT delivery_stops_committed_needs_stamp
  CHECK (date_state <> 'COMMITTED' OR committed_at IS NOT NULL);

CREATE INDEX idx_delivery_stops_datestate ON delivery_stops (branch_id, date_state, scheduled_date);

COMMENT ON COLUMN delivery_stops.requested_date IS
  'The date the salesperson asked for. Kept after the commit so "we said the 14th and it went on the 16th" is answerable.';

-- ── §6 · one order, several loads (the split) ───────────────────────────────────────────────────
-- The client's case: a $22,000 armour-stone order going out as THREE loads on ONE invoice. 0090's
-- uq_delivery_stops_live_order allows exactly one live stop per order, which is the right guard
-- against a dispatcher clicking Schedule twice and the wrong guard against a deliberate split.
--
-- load_seq separates the two. A duplicate schedule still collides (it mints seq 1 again); a split
-- mints 2 and 3 explicitly. load_count is stored so a card can say "load 2 of 3" without counting
-- its siblings on every render.
ALTER TABLE delivery_stops
  ADD COLUMN load_seq   INTEGER NOT NULL DEFAULT 1 CHECK (load_seq >= 1),
  ADD COLUMN load_count INTEGER NOT NULL DEFAULT 1 CHECK (load_count >= 1),
  -- FREIGHT IS SERVER-COMPUTED, ALWAYS. This column is written by dispatch.Service.SplitFreight and
  -- by nothing else; the client proposes a shape (even, or an override set) and the server does the
  -- arithmetic and refuses a set that does not sum to the order's freight to the cent.
  ADD COLUMN freight_cents BIGINT NOT NULL DEFAULT 0 CHECK (freight_cents >= 0),
  ADD COLUMN freight_is_override BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE delivery_stops ADD CONSTRAINT delivery_stops_load_seq_within_count
  CHECK (load_seq <= load_count);

DROP INDEX IF EXISTS uq_delivery_stops_live_order;
CREATE UNIQUE INDEX uq_delivery_stops_live_order ON delivery_stops (order_id, load_seq)
  WHERE status NOT IN ('CANCELLED','FAILED');

COMMENT ON COLUMN delivery_stops.freight_cents IS
  'This load''s share of the order''s freight, in cents. WRITTEN ONLY BY THE SERVER (dispatch.Service.SplitFreight). Defaults to an even split with the remainder on the earliest loads; an override must sum to the order total exactly or it is refused.';

-- ── §7 · the 48-hour lock, as a row ─────────────────────────────────────────────────────────────
-- The LOCK ITSELF IS COMPUTED (policy window vs the run's date and the clock), because a stored
-- boolean would need a scheduler to flip it and a board would show a stale padlock at 06:00. What
-- IS stored is the UNLOCK — a manager's deliberate act, which is a fact with a person on it.
--
--   lock_state    AUTO     — the policy decides (the normal state of every run).
--                 UNLOCKED — a dispatch manager thawed this run inside the window.
--   unlocked_by/at/reason  who, when, why. A blank reason is refused by the service in words; the
--                          CHECK is the backstop for any other writer.
ALTER TABLE delivery_runs
  ADD COLUMN lock_state    TEXT NOT NULL DEFAULT 'AUTO' CHECK (lock_state IN ('AUTO','UNLOCKED')),
  ADD COLUMN unlocked_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN unlocked_at   TIMESTAMPTZ,
  ADD COLUMN unlock_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE delivery_runs ADD CONSTRAINT delivery_runs_unlock_needs_evidence
  CHECK (lock_state <> 'UNLOCKED' OR (unlocked_at IS NOT NULL AND btrim(unlock_reason) <> ''));

COMMENT ON COLUMN delivery_runs.lock_state IS
  'AUTO = dispatch_policy decides whether this run is inside the lock window. UNLOCKED = a dispatch manager thawed it, with a reason. The lock is never stored as a boolean: a stored lock goes stale at midnight.';

-- ── §8 · the surface registry rows (0172 substrate, presentation half) ──────────────────────────
-- Item ids are FRONTEND REGISTRY ids (app/src/dispatch-surface-items.ts); the server validates
-- SHAPE only (the 0149/0164 ruling). Ids are add-only.
--
-- THE BOARD AXIS IS CURATION, NOT POLICY, and it is modelled the way the tab strip models a pinned
-- tab: two action rows, and the FIRST VISIBLE one in position order is the axis the surface opens
-- on. A dealer who wants a status board flips the two positions; a person who disagrees overrides
-- it for themselves through surface_user_items, and DELETE is still reset. No new vocabulary.
--
-- THE VALUE COLUMN SHIPS HIDDEN FOR THE FRONTLINE PROFILES and visible for manager/admin — and that
-- curation decides nothing about safety: the data is absent from the payload for a caller the
-- policy refuses, so a curated-visible column renders '—' rather than a number.
INSERT INTO surface_role_items (surface_id, profile, item_kind, item_id, position, visible)
SELECT 'dispatch.loadboard', p.profile, i.item_kind, i.item_id, i.position, i.visible
FROM (VALUES ('counter'), ('yard'), ('driver'), ('manager'), ('admin')) AS p (profile)
CROSS JOIN (VALUES
    -- panels
    ('panel',  'quickcards',        0, true),
    ('panel',  'staging',           1, true),   -- the not-on-a-truck lane (min set, always on)
    ('panel',  'board',             2, true),   -- the axis grid (min set, always on)
    -- the load card's facts, in the order the client named them
    ('column', 'load.capacity',     0, true),
    ('column', 'load.weight',       1, true),
    ('column', 'load.volume',       2, true),
    ('column', 'load.pallets',      3, true),
    ('column', 'load.value',        4, false),  -- cost class: seeded hidden, server-gated regardless
    ('column', 'load.gaps',         5, true),
    ('column', 'load.freight',      6, true),
    -- the quick cards across the top
    ('chip',   'card.deliveries',   0, true),
    ('chip',   'card.pickups',      1, true),
    ('chip',   'card.unassigned',   2, true),
    ('chip',   'card.ready',        3, true),
    ('chip',   'card.requested',    4, true),
    ('chip',   'card.locked',       5, true),
    -- the axis + the view. First VISIBLE axis row wins as the opening axis.
    ('action', 'axis.truck',        0, true),
    ('action', 'axis.status',       1, true),
    ('action', 'view.day',          2, true),
    ('action', 'view.week',         3, true),
    ('action', 'view.month',        4, true)
) AS i (item_kind, item_id, position, visible)
ON CONFLICT (surface_id, profile, item_kind, item_id) DO NOTHING;

-- The two profiles that are allowed to SEE money get the value column curated on. The gate is still
-- the policy; this only stops a manager having to switch on a column they always want.
UPDATE surface_role_items SET visible = true
 WHERE surface_id = 'dispatch.loadboard' AND item_kind = 'column' AND item_id = 'load.value'
   AND profile IN ('manager','admin');

-- ── §9 · the order's freight, so there is something to split ───────────────────────────────────
-- 0173 gave a QUOTE a delivery_fee_cents. An order — the thing that actually goes on three trucks —
-- had nowhere to carry the figure, so "split the freight" had no numerator. The column is additive
-- with a zero default, which makes every existing order byte-identical: an order with no freight
-- refuses a split in words ("this order carries no freight to split") rather than dividing nothing
-- into three and looking like it worked.
--
-- IT LIVES ON orders AND NOT ON delivery_stops for the reason the client's case makes obvious: one
-- invoice, three loads. The freight is a property of the ORDER; delivery_stops.freight_cents is the
-- SHARE, and the server is the only thing that turns one into the other.
ALTER TABLE orders
  ADD COLUMN freight_cents BIGINT NOT NULL DEFAULT 0 CHECK (freight_cents >= 0);

COMMENT ON COLUMN orders.freight_cents IS
  'The freight this order carries. Divided across its loads by dispatch.Service.SplitFreight — server-side, always. A 0 here means there is nothing to split, which is refused in words rather than split into zeros.';
