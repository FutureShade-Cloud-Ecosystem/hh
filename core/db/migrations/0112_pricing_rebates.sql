-- 0112: DIB-69 — manufacturer rebate engine, CORE ACCRUAL ONLY.
--
-- SCOPE FENCE, STATED IN THE SCHEMA SO THE NEXT READER DOES NOT GUESS
-- ------------------------------------------------------------------
-- DIB-69 as written is three things: (1) accrue a rebate when a covered product sells, (2) a vendor
-- scorecard, (3) a rebate-sheet upload that authors programs in bulk. This migration builds ONLY
-- (1) plus the ledger it produces, because (1) is the thing that is WRONG-BY-OMISSION every day it
-- is missing — every sale that goes through without an accrual row is money that cannot be claimed
-- back later, and no amount of scorecard UI recovers it retroactively. (2) and (3) read this
-- ledger; they are additive on top and are parked explicitly in REPORT-W4-A.md.
--
-- WHY THE LEDGER IS APPEND-ONLY
-- -----------------------------
-- A rebate accrual is a CLAIM against a vendor. The claim is eventually reconciled against what the
-- vendor actually paid, and the difference is the whole point of the scorecard. If accrual rows
-- were mutable, a program rate corrected in September would silently rewrite July's claims and the
-- variance would vanish — the ledger would agree with the vendor by construction. So rows here are
-- INSERT-only: a correction is a new row with a negative accrual (source_type='ADJUSTMENT'), the
-- same discipline the GL uses for a reversing entry.

-- ── 1 · programs (vendor + product/category + rate) ───────────────────────────────────────────
CREATE TABLE rebate_programs (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code        CITEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  vendor_id   UUID NOT NULL REFERENCES vendors(id) ON DELETE CASCADE,
  -- COVERAGE: product XOR category XOR "everything this vendor supplies". Same most-specific-first
  -- shape as pricing_promotions, deliberately — one selection idiom in this package, not two.
  product_id  UUID REFERENCES products(id) ON DELETE CASCADE,
  category_id UUID REFERENCES product_categories(id) ON DELETE CASCADE,
  CHECK (product_id IS NULL OR category_id IS NULL),

  -- THE RATE, and its BASIS. The basis is not cosmetic: manufacturers write rebate sheets in all
  -- three of these forms and picking the wrong one is a silent order-of-magnitude error.
  --   PCT_OF_SALE — rate_bps applied to the line's pre-tax EXTENDED SALE amount
  --   PCT_OF_COST — rate_bps applied to the line's extended COST (the common "x% of purchases" form)
  --   AMT_PER_UNIT — rate_cents_per_unit × qty in the product BASE uom (the "$0.15/block" form)
  basis       TEXT NOT NULL CHECK (basis IN ('PCT_OF_SALE','PCT_OF_COST','AMT_PER_UNIT')),
  rate_bps    INTEGER CHECK (rate_bps IS NULL OR (rate_bps >= 0 AND rate_bps <= 10000)),
  rate_cents_per_unit BIGINT CHECK (rate_cents_per_unit IS NULL OR rate_cents_per_unit >= 0),
  CHECK ( (basis IN ('PCT_OF_SALE','PCT_OF_COST') AND rate_bps IS NOT NULL AND rate_cents_per_unit IS NULL)
       OR (basis = 'AMT_PER_UNIT' AND rate_cents_per_unit IS NOT NULL AND rate_bps IS NULL) ),

  -- Programs run for a season/year. starts_at NOT NULL because a program with no start accrues on
  -- history the moment it is created; ends_at NULL = open-ended, which is the common annual case
  -- renewed in place.
  starts_at   TIMESTAMPTZ NOT NULL,
  ends_at     TIMESTAMPTZ,
  CHECK (ends_at IS NULL OR ends_at > starts_at),

  notes       TEXT NOT NULL DEFAULT '',
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  version     BIGINT NOT NULL DEFAULT 1,
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_rebate_programs_vendor   ON rebate_programs (vendor_id) WHERE is_active;
CREATE INDEX idx_rebate_programs_product  ON rebate_programs (product_id)  WHERE is_active AND product_id  IS NOT NULL;
CREATE INDEX idx_rebate_programs_category ON rebate_programs (category_id) WHERE is_active AND category_id IS NOT NULL;

-- ── 2 · the accrual ledger ────────────────────────────────────────────────────────────────────
CREATE TABLE rebate_accruals (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  program_id  UUID NOT NULL REFERENCES rebate_programs(id) ON DELETE CASCADE,
  -- vendor_id is DENORMALISED from the program on purpose. The per-vendor ledger read is the only
  -- read this table has, and joining through programs to get there would make the hot path a join
  -- for a value that cannot change: a program's vendor is fixed at creation (there is no UPDATE
  -- path for vendor_id in the service).
  vendor_id   UUID NOT NULL REFERENCES vendors(id) ON DELETE CASCADE,
  product_id  UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  branch_id   UUID REFERENCES locations(id) ON DELETE SET NULL,

  -- PROVENANCE. source_type + source_id say which sale produced the claim, and are what makes the
  -- ledger auditable against the sales history rather than being a number someone has to trust.
  -- 'ADJUSTMENT' is the correction shape (see the append-only note above) and carries a NULL
  -- source_id, because a correction is not caused by a document.
  source_type TEXT NOT NULL CHECK (source_type IN ('POS_SALE','ORDER','ADJUSTMENT')),
  source_id   UUID,
  CHECK ((source_type = 'ADJUSTMENT') = (source_id IS NULL)),

  qty_base    NUMERIC(19,8) NOT NULL,        -- qty in the product BASE uom (the AMT_PER_UNIT multiplier)
  basis_cents BIGINT NOT NULL,               -- the extended sale or cost the rate was applied to
  accrual_cents BIGINT NOT NULL,             -- the claim. NEGATIVE is legal, and only on ADJUSTMENT.
  CHECK (accrual_cents >= 0 OR source_type = 'ADJUSTMENT'),
  rate_note   TEXT NOT NULL DEFAULT '',      -- the rate AS APPLIED, frozen: '2.50% of sale'
  accrued_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- IDEMPOTENCY, and it is load-bearing. Accrual is driven by a sale event, and every event
-- transport in this system is at-least-once (the jobs runner retries, the POS client can resubmit
-- a ticket after a timeout). Without this index a retried sale double-claims, and a double-claim
-- against a manufacturer is a credibility problem, not a rounding problem. Partial on the three
-- document source types so that ADJUSTMENT rows — which are deliberately repeatable — are exempt.
CREATE UNIQUE INDEX ux_rebate_accrual_source
  ON rebate_accruals (program_id, source_type, source_id, product_id)
  WHERE source_id IS NOT NULL;

CREATE INDEX idx_rebate_accruals_vendor ON rebate_accruals (vendor_id, accrued_at DESC);
CREATE INDEX idx_rebate_accruals_program ON rebate_accruals (program_id, accrued_at DESC);

-- ── 3 · seeded demo programs ──────────────────────────────────────────────────────────────────
-- Two programs against real seeded vendors so the admin surface and the per-vendor ledger have
-- something to show on a fresh migrate. Rates are DEMO VALUES, not client-confirmed sheet terms —
-- the note column says so in the row itself so nobody reads them off a screen as fact.
INSERT INTO rebate_programs (code, name, vendor_id, category_id, basis, rate_bps, starts_at, notes)
SELECT 'RB-BESTWAY-PAVERS', 'Best Way Stone — paver volume rebate',
       v.id, c.id, 'PCT_OF_SALE', 250, date_trunc('year', now()),
       'DEMO RATE (2.50% of sale). Not a confirmed vendor sheet term — see REPORT-W4-A.md.'
  FROM vendors v, product_categories c
 WHERE v.vendor_no = 'V-BESTWAY' AND c.name = 'Pavers'
ON CONFLICT (code) DO NOTHING;

INSERT INTO rebate_programs (code, name, vendor_id, category_id, basis, rate_cents_per_unit, starts_at, notes)
SELECT 'RB-TECHO-WALLS', 'Techo-Bloc — retaining wall unit rebate',
       v.id, c.id, 'AMT_PER_UNIT', 15, date_trunc('year', now()),
       'DEMO RATE ($0.15 per base unit). Not a confirmed vendor sheet term — see REPORT-W4-A.md.'
  FROM vendors v, product_categories c
 WHERE v.name ILIKE 'Techo%' AND c.name = 'Retaining Walls'
ON CONFLICT (code) DO NOTHING;

COMMENT ON TABLE rebate_programs IS
  'DIB-69 CORE: manufacturer rebate rules (vendor + product/category + rate). Scorecard and rebate-sheet upload are follow-up scope.';
COMMENT ON TABLE rebate_accruals IS
  'DIB-69 CORE: append-only accrual ledger. A correction is a new ADJUSTMENT row with a negative accrual, never an UPDATE.';
