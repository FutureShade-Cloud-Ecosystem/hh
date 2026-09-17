-- 0198 — THE PRICE-FILE IMPORT: a per-vendor column mapping that is created when a file arrives,
-- and an audit row that survives the batch it staged.
--
-- THE ASK, in the client's terms: a manufacturer emails a price list, somebody maps its columns
-- once, sees what would change BEFORE anything changes, and then commits it. Three of those four
-- verbs already had a table (0039 gave us price_import_profiles, price_batches, price_change_lines,
-- price_change_log). The two that did not are the two this migration adds: a mapping for the OTHER
-- twenty-three vendors, and a record of the import itself as distinct from the price moves it caused.
--
-- ── 1 · WHY THE OTHER TWENTY-THREE PROFILES ARE NOT SEEDED HERE ─────────────────────────────────
--
-- 0039 seeded seven, keyed to the seven V-* vendors somebody had a file from. There are thirty
-- vendors. The obvious finishing move is to seed the remaining twenty-three with the same
-- {sku,cost,retail,effective_date} mapping the seven got, and it is the wrong move, because that
-- mapping is not a default — IT IS A GUESS ABOUT A FILE NOBODY HAS SENT YET.
--
-- A seeded guess is worse than an absent profile in three specific ways, all of which cost money:
--
--   a. IT LOOKS ANSWERED. A profile row on the screen reads as "this vendor is configured". An
--      absent profile reads as "map this one". Only the second is true before a file has arrived,
--      and the first is the state in which somebody imports without looking at the columns.
--   b. THE HEADERS WILL NOT MATCH, AND THE FAILURE IS SILENT. `cost_col` defaults to the header
--      literal `cost`. A file whose column is `Dealer Net` yields no cost on any row, every row
--      derives from nothing, and the import produces a clean-looking preview of zero changes. A
--      profile that does not exist cannot produce that preview; a seeded one can, and did.
--   c. IT PRE-COMMITS THE DERIVATION ANCHOR. The seeded mapping omits `trade_col`, which is the
--      HIGHEST-PRECEDENCE anchor in the derivation (batch_service.go deriveRetail: trade+FOB beats
--      cost×markup beats manufacturer retail). Seeding twenty-three more rows without it is
--      twenty-three more silent votes for cost×markup cast by a migration rather than by the person
--      holding the file.
--
-- So: LAZY. A profile is created by the first import for that vendor, out of the mapping the person
-- actually chose against the actual headers of the actual file, and `origin` below records which of
-- the two ways it came into being so nobody has to guess later whether a mapping was authored or
-- inherited. The seven that exist keep existing and keep their names — this migration adds no
-- profile rows at all, and the ON CONFLICT-free INSERT it does not contain is the point.
--
-- ── 2 · WHY THE IMPORT NEEDS ITS OWN AUDIT ROW WHEN price_change_log EXISTS ─────────────────────
--
-- price_change_log (0039) is per PRODUCT per FIELD: "list price for SKU X went 24.00 → 26.50 on
-- batch B". It is the right record of a price MOVE and the wrong record of an IMPORT, because the
-- three questions an import raises afterwards are not about any one product:
--
--   "which file did this come from"        — a filename, a byte count, a content digest
--   "what did the mapping say that day"    — the mapping is mutable; the log rows are not, and a
--                                            mapping edited next month rewrites the explanation of
--                                            every import that ever ran under it unless the mapping
--                                            USED is frozen at the moment it was used
--   "what did it NOT do"                   — the unchanged and the unknown rows. price_change_log
--                                            records moves, so a file of 400 rows that moved 12
--                                            prices leaves 12 rows and no trace of the 388, and
--                                            "the import only touched twelve" is exactly the claim
--                                            somebody will need to make.
--
-- price_import_runs answers all three, and is append-only for the same reason price_change_log is:
-- an edited audit row is not an audit row. The trigger below is 0039's, re-declared rather than
-- shared, because a shared trigger function means one DROP FUNCTION ... CASCADE takes the guard off
-- both tables at once.
--
-- ── 3 · WHY COST VISIBILITY IS A ROW AND NOT A ROLE CHECK IN CODE ───────────────────────────────
--
-- Cost on this screen is the manufacturer's dealer-net column. Whether a counter clerk may see it is
-- a DEALER's decision, not ours, and the settings kit's own vocabulary already spells the three
-- answers (internal/settings: MANAGER_UP | ALL_STAFF | HIDDEN, the 0173 quote_policy column's
-- values). It lands on pricing_anchor_policy because that is the row the import already reads for
-- its markup and its variance threshold — one read, already made, already branch-over-NULL — rather
-- than a fourth policy table for one column. Default MANAGER_UP, matching every other cost knob in
-- the product; a dealer who wants the counter to see dealer net says so, and a dealer who wants it
-- gone entirely has HIDDEN.
--
-- ── RE-RUN AND STAGING POSTURE ──────────────────────────────────────────────────────────────────
--
-- Every statement is ADD/CREATE with IF NOT EXISTS, and there is no UPDATE and no DELETE in this
-- file: staging is never wiped, so nothing here is allowed to be a no-op only on a fresh database.
-- The added columns all carry defaults, so an existing row is complete the moment the ALTER lands
-- and no backfill is needed. Applies clean from zero and onto a database already at 0191.

-- ── price_import_profiles: where a mapping came from, and when it was last believed ─────────────

-- origin distinguishes the seven 0039 seeded a mapping for from the ones a person authored against a
-- real file. It is not decoration: the screen offers a seeded mapping as a starting point and an
-- authored one as the answer, and without this column those two are the same row.
ALTER TABLE price_import_profiles
  ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'SEEDED'
    CHECK (origin IN ('SEEDED', 'FIRST_IMPORT'));

-- created_by is NULL for the seeded seven and that NULL is honest — no person made them. ON DELETE
-- SET NULL rather than RESTRICT: a departed employee must not pin a mapping the yard still imports.
ALTER TABLE price_import_profiles
  ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- last_import_at is the one fact that tells a manager whether a mapping is live or archaeology.
ALTER TABLE price_import_profiles
  ADD COLUMN IF NOT EXISTS last_import_at TIMESTAMPTZ;

-- The lazy resolve is "the active profile for this vendor, oldest first" — deliberately NOT a new
-- UNIQUE(vendor_id), which would be a constraint added to a live table that seven rows and any
-- hand-made staging row would have to be proven against first. Oldest-first means the seven seeded
-- profiles keep answering for their vendors and a second profile can never displace one silently.
CREATE INDEX IF NOT EXISTS idx_price_import_profiles_vendor_active
  ON price_import_profiles(vendor_id, created_at)
  WHERE is_active;

-- ── pricing_anchor_policy: the dealer's cost-visibility knob ────────────────────────────────────

ALTER TABLE pricing_anchor_policy
  ADD COLUMN IF NOT EXISTS import_cost_visibility TEXT NOT NULL DEFAULT 'MANAGER_UP'
    CHECK (import_cost_visibility IN ('MANAGER_UP', 'ALL_STAFF', 'HIDDEN'));

-- ── price_import_runs: the append-only record of an import having happened ──────────────────────

CREATE TABLE IF NOT EXISTS price_import_runs (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vendor_id       UUID REFERENCES vendors(id) ON DELETE SET NULL,
  profile_id      UUID REFERENCES price_import_profiles(id) ON DELETE SET NULL,
  -- The batch this import staged. SET NULL rather than CASCADE: a cancelled batch deletes the price
  -- moves, and the fact that somebody imported a file on a Tuesday must outlive that.
  batch_id        UUID REFERENCES price_batches(id) ON DELETE SET NULL,
  source_name     TEXT NOT NULL DEFAULT '',            -- the filename as the browser reported it
  source_format   TEXT NOT NULL DEFAULT 'CSV' CHECK (source_format IN ('CSV', 'XLSX')),
  source_bytes    BIGINT NOT NULL DEFAULT 0 CHECK (source_bytes >= 0),
  -- content_digest is a hex SHA-256 over the PARSED rows, not the raw upload: two exports of the
  -- same price list from the same spreadsheet differ byte-for-byte (timestamps, zip ordering) while
  -- carrying identical prices, and the question this column answers is "is this the same price
  -- list", never "is this the same file on disk".
  content_digest  TEXT NOT NULL DEFAULT '',
  -- mapping_used is the mapping FROZEN at the moment of the import. price_import_profiles.mapping is
  -- mutable by design; this is the copy that explains this run after that one has moved on.
  mapping_used    JSONB NOT NULL DEFAULT '{}',
  row_count       INTEGER NOT NULL DEFAULT 0 CHECK (row_count >= 0),
  -- The four outcomes of the diff the operator approved, counted over EVERY row rather than over the
  -- page they were shown. A preview that displayed 200 of 4,000 rows still recorded 4,000 here.
  new_count       INTEGER NOT NULL DEFAULT 0 CHECK (new_count >= 0),
  changed_count   INTEGER NOT NULL DEFAULT 0 CHECK (changed_count >= 0),
  unchanged_count INTEGER NOT NULL DEFAULT 0 CHECK (unchanged_count >= 0),
  unknown_count   INTEGER NOT NULL DEFAULT 0 CHECK (unknown_count >= 0),
  -- unpriced_count is not a fifth outcome; it is the diagnostic that keeps the other four honest. A
  -- row whose SKU we stock but whose mapped columns yield no number is truthfully UNCHANGED, and a
  -- mis-mapped cost column makes EVERY row truthfully unchanged. Recording the count is how an
  -- import that read nothing is later distinguishable from one that found nothing to do.
  unpriced_count  INTEGER NOT NULL DEFAULT 0 CHECK (unpriced_count >= 0),
  imported_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_price_import_runs_vendor ON price_import_runs(vendor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_price_import_runs_batch  ON price_import_runs(batch_id);

-- Append-only, enforced the way 0039 enforces it on price_change_log. A separate function per table
-- rather than one shared helper: a shared function is one DROP ... CASCADE away from disarming both
-- audits at once, and the duplication here is four lines.
CREATE OR REPLACE FUNCTION price_import_runs_no_update() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'price_import_runs is append-only — a recorded import cannot be edited';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_price_import_runs_no_update ON price_import_runs;
CREATE TRIGGER trg_price_import_runs_no_update
  BEFORE UPDATE ON price_import_runs
  FOR EACH ROW EXECUTE FUNCTION price_import_runs_no_update();
