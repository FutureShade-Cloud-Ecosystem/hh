-- 0158: DIB-472 — a stock transfer carries a PROMISED ARRIVAL date.
-- Owner: internal/inventory. Additive, non-breaking; follows 0157. Ruling D-083 (2026-08-18, option A).
--
-- ── THE COMPLAINT THIS ANSWERS ───────────────────────────────────────────────────────────────────
--
-- DIB-460 put "what is coming" on the product card. A purchase-order line there reads
-- "120 PC · expected Aug 19 · #PO-TRT-121" and, directly beneath it, a branch transfer reads
-- "15 PC · shipped Aug 12 · #TR-KGN-3". Side by side, one line answers "when does it land" and the
-- other does not, and the person reading the card has to go ask a yard office which is which.
--
-- 0116 gave stock_transfers created_at, shipped_at and received_at — three facts about events that
-- HAPPENED — and no promise about one that has not. DIB-460 declined to invent one, and said so in
-- inventory/incoming.go: deriving an ETA from shipped_at plus a guessed transit time would put a date
-- on a screen that nothing in the database supports, and people plan around dates.
--
-- ── WHY A TABLE AND NOT A CONSTANT ───────────────────────────────────────────────────────────────
--
-- The ticket's own worry is the load-bearing one: "if nobody maintains it, the ETA is a guess with a
-- database row behind it, which is worse than no ETA at all." A constant in Go (or a DEFAULT baked
-- into this file) is precisely the unmaintainable version — it is invisible, it cannot be corrected
-- by the people who actually drive the route, and it goes stale silently the first time the run
-- schedule changes.
--
-- So the transit time is CONFIGURATION, in the shape the house already uses for a tech-admin knob
-- (0151 quick_action_visibility, 0152 msg_event_classes): a small table, one row per ordered branch
-- pair, edited from an admin surface, with updated_at/updated_by on the row so the surface can show
-- WHEN somebody last looked at it. A default whose staleness is visible is a default that can be
-- maintained; that is the whole difference the ticket is asking about.
--
-- ── AND WHY A MISSING ROW MUST STAY MISSING ──────────────────────────────────────────────────────
--
-- There is deliberately NO fallback constant behind this table. A pair with no row pre-fills NOTHING
-- and the eta stays NULL, and the surface goes on saying "shipped Aug 12" exactly as it does today.
-- That keeps DIB-460's honesty rule intact for every pair nobody has configured: the system never
-- states an arrival it cannot support. Trenton and Kingston are seeded because that is the route the
-- business runs; a third yard opening gets an empty field until somebody who knows the drive fills it
-- in, which is the correct behaviour and not a gap.

-- ── (a) the promised arrival ─────────────────────────────────────────────────────────────────────
--
-- DATE, not TIMESTAMPTZ. The promise a yard makes is a DAY ("it's on Thursday's run"), not an
-- instant, and a timestamp would invite a surface to render an hour nobody committed to. It also
-- matches purchasing's expected_at, which is what the two lines on DIB-460's card are compared
-- against each other — one column type, one format on the wire, one formatter in the browser.
ALTER TABLE stock_transfers ADD COLUMN eta DATE;

COMMENT ON COLUMN stock_transfers.eta IS
  'DIB-472: promised arrival day, captured at SHIP time (default = ship date + branch_transit_days, editable). NULL = no promise was made; the surface says "shipped <date>" instead.';

-- An eta is DERIVED FROM A SHIP EVENT (ship date + transit days) and edited in the same breath, so it
-- cannot exist on a document that has not shipped. This is the schema's copy of "capture at ship
-- time": a later change that started stamping an eta on DRAFT creation would have to change this line
-- too, which is the point — the capture point stops being an implementation detail one refactor can
-- move. Every row that exists today has eta IS NULL, so the constraint is satisfied on arrival.
ALTER TABLE stock_transfers
  ADD CONSTRAINT stock_transfers_eta_needs_ship
  CHECK (eta IS NULL OR shipped_at IS NOT NULL);

-- ── (b) the per-pair transit time behind the default ─────────────────────────────────────────────
--
-- DIRECTED. (from, to) is the primary key and both directions are stored separately, because the two
-- legs of a route are not the same trip: the truck that leaves Trenton on the morning run comes back
-- in the afternoon, and a yard that batches its outbound loads differently at each end will want two
-- different numbers. Storing one undirected row and reading it both ways would silently claim they
-- are equal, and the first time they are not, nobody could express the difference.
CREATE TABLE branch_transit_days (
  from_branch_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
  to_branch_id   UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
  -- Days added to the ship date. ZERO IS A REAL AND MEANINGFUL SETTING — "loaded and unloaded the
  -- same day", which is what a two-hour drive actually permits when the truck leaves promptly — so
  -- the CHECK admits it rather than treating 0 as "unset". "Unset" is the ABSENCE of the row, and the
  -- two must stay distinguishable: 0 promises today, no row promises nothing.
  transit_days   INTEGER NOT NULL CHECK (transit_days >= 0 AND transit_days <= 60),
  -- Scrubname0033 last reviewed this number and when. The admin surface RENDERS the date, because the ticket's
  -- worry is staleness rather than wrongness: a number nobody has touched since the route changed is
  -- the failure mode, and the only cheap defence is making the last-reviewed date visible next to it.
  updated_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (from_branch_id, to_branch_id),
  -- 0116's transfer CHECK, mirrored: a transfer cannot cross from a branch to itself, so a transit
  -- time for that pair could never be read and would only be a row an admin has to wonder about.
  CHECK (from_branch_id <> to_branch_id)
);

COMMENT ON TABLE branch_transit_days IS
  'DIB-472: admin-maintained transit time per ORDERED branch pair. Backs the ship-time eta pre-fill. A missing pair pre-fills nothing — there is no fallback constant.';

-- ── the seeded pair ──────────────────────────────────────────────────────────────────────────────
--
-- Trenton <-> Kingston, BOTH DIRECTIONS, one day each.
--
-- WHY 1 AND NOT 0. The drive is two hours (incoming.go says so), but a transfer does not leave the
-- yard at the moment somebody presses Ship: it goes on the next scheduled inter-yard run, and the
-- yards' own answer to "when will it be there" is "tomorrow". 0 would make the eta equal the ship
-- date, which is both optimistic and — on DIB-460's card — indistinguishable from the sentence it
-- replaces. 1 is the number the route actually runs at, and the admin surface exists so a yard that
-- turns loads around same-day can set 0 without a release.
--
-- Joined on locations.code rather than written as literal UUIDs: the seed's branch ids are stable
-- today, and a migration that depends on that is a migration that breaks on any other deployment.
INSERT INTO branch_transit_days (from_branch_id, to_branch_id, transit_days)
SELECT f.id, t.id, 1
  FROM locations f
  CROSS JOIN locations t
 WHERE f.loc_type = 'BRANCH' AND t.loc_type = 'BRANCH'
   AND f.code IN ('TRT','KGN') AND t.code IN ('TRT','KGN')
   AND f.id <> t.id
ON CONFLICT (from_branch_id, to_branch_id) DO NOTHING;
