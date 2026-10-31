-- 0471: holdback rules by province, with a dealer override and a job override (wave 3 RT-1).
--
-- THE RULE FOR A JOB is answered in one read, in this order: the job's own override, else the dealer's,
-- else the province's. Only the holdback PERCENT can be overridden; the preserve and perfect days are the
-- province's statute and no override changes them.
--
-- THE PROVINCE of a job is the province of its site (the ship to address on its customer_jobs row) when
-- it names one, else the dealer's own province, which the dealer rule row carries (no other module's
-- table is read to find it).
--
-- THE PERCENT is basis points, an INTEGER from 0 to 10000 (0 to 100 percent), on every table below:
-- money-adjacent figures are integers, and a bound the table holds cannot be skipped by a caller.
--
-- Owner: internal/lien, the module that owns the holdback and the lien jobs (migration 0038). The job
-- override sits in a lien-owned table that REFERENCES customer_jobs (migration 0460) by foreign key
-- only: customer_jobs belongs to the customer module and lien never writes a column on it.
--
-- SCOPE: the province rules and the dealer rule are the dealer's own and carry no branch. A job override
-- carries the branch of the job it was set on (branch_id, NOT NULL) so the write and the read are fenced
-- by the caller's branch reach.

CREATE TABLE holdback_province_rules (
  province         TEXT PRIMARY KEY CHECK (province ~ '^[A-Z]{2}$'),
  holdback_pct_bps INTEGER NOT NULL CHECK (holdback_pct_bps BETWEEN 0 AND 10000),
  preserve_days    INTEGER NOT NULL CHECK (preserve_days >= 0),
  perfect_days     INTEGER NOT NULL CHECK (perfect_days >= 0)
);

-- Ontario: 10 percent, 60 days to preserve, 90 days to perfect.
INSERT INTO holdback_province_rules (province, holdback_pct_bps, preserve_days, perfect_days)
VALUES ('ON', 1000, 60, 90);

-- The dealer rule: ONE row. The dealer's own province, and an optional percent that overrides the
-- province's. A NULL percent means no override, never zero percent.
CREATE TABLE holdback_dealer_rule (
  singleton        BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  province         TEXT NOT NULL DEFAULT 'ON' REFERENCES holdback_province_rules(province) ON DELETE RESTRICT,
  holdback_pct_bps INTEGER CHECK (holdback_pct_bps IS NULL OR holdback_pct_bps BETWEEN 0 AND 10000),
  set_by           UUID REFERENCES users(id) ON DELETE SET NULL,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO holdback_dealer_rule (singleton, province) VALUES (TRUE, 'ON');

-- A job override: the percent for one customer job. Gone with the job.
CREATE TABLE holdback_job_overrides (
  customer_job_id  UUID PRIMARY KEY REFERENCES customer_jobs(id) ON DELETE CASCADE,
  branch_id        UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  holdback_pct_bps INTEGER NOT NULL CHECK (holdback_pct_bps BETWEEN 0 AND 10000),
  set_by           UUID REFERENCES users(id) ON DELETE SET NULL,
  set_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_holdback_job_overrides_branch ON holdback_job_overrides(branch_id);

-- A lien job links to ONE customer job (the customer's own job account). The service holds the rule that
-- it is the same customer's and not closed; the column carries the link. A deleted customer job unlinks.
ALTER TABLE lien_jobs
  ADD COLUMN customer_job_id UUID REFERENCES customer_jobs(id) ON DELETE SET NULL;
CREATE INDEX idx_lien_jobs_customer_job ON lien_jobs(customer_job_id) WHERE customer_job_id IS NOT NULL;
