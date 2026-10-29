-- 0454: customer_jobs, the job account under a customer (wave 3 JA-1, decision 1.1 A).
--
-- A job is a site of work under one customer: it carries its own ship to address, PO reference and
-- contact, and may carry its own credit limit and payment terms. A job shares the customer's limit
-- and terms unless it carries its own.
--
-- THE TWO NULLABLE FIGURES ARE THE WHOLE POINT. A NULL credit_limit_cents and a NULL terms_days
-- mean the CUSTOMER's own figure, not zero: absence and zero are different accounts, and a job
-- that read as zero credit because nobody filled a field in would be refused at the counter for a
-- limit nobody set. A job that carries its own limit may never exceed the customer's: the
-- customer's exposure includes every job it holds, so a job that raised the ceiling would raise the
-- customer's exposure with it. That ceiling is the service's rule (customer/job_service.go), and
-- this table carries the figures the rule reads.
--
-- THE JOB NUMBER IS THE CUSTOMER'S OWN. Two customers may each hold a job numbered J-1, and one
-- customer may not hold two, so the unique constraint is on the pair (customer_id, job_no). The
-- number is CITEXT, as account_no is, so J-1 and j-1 are one job and not two.
--
-- THE BRANCH. A job sits at a branch: the customer's own home branch when the customer has one,
-- and the branch the create names when the customer does not (a company-wide customer). The branch
-- is NOT NULL because a job without one is a job nobody's fence can hold, and the branch is what
-- the staff reads are fenced by: a job of another branch is not found by a caller whose grants do
-- not reach it.
--
-- THE LIEN DATES ARE KEPT HERE FOR WAVE 3 JA-2: lien_jobs (migration 0038) is left alone, and
-- JA-2 links the two. A job's own lien dates are the job's own facts, not a lien module's.
--
-- Owner: internal/customer, the module that owns customers (migration 0006). Conventions: money
-- BIGINT *_cents, dates DATE, TEXT with a CHECK, every FK explicit about what happens on delete.

CREATE TABLE customer_jobs (
  id                           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id                  UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  branch_id                    UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  job_no                       CITEXT NOT NULL,
  name                         TEXT NOT NULL,
  ship_to_address              JSONB NOT NULL DEFAULT '{}',
  po_reference                 TEXT NOT NULL DEFAULT '',
  contact_name                 TEXT NOT NULL DEFAULT '',
  contact_email                CITEXT,
  contact_phone                TEXT NOT NULL DEFAULT '',
  credit_limit_cents           BIGINT,
  terms_days                   INTEGER,
  status                       TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','CLOSED')),
  substantial_performance_date DATE,
  last_supply_date             DATE,
  created_by                   UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- The job's own figures are money and days: neither may be negative. The ceiling (a job's own
  -- limit may not exceed the customer's) is the service's rule, not a constraint: the customer's
  -- own limit may be edited later, and a constraint written today would then refuse a job that
  -- was fair when it was made.
  CONSTRAINT customer_jobs_credit_limit_nonneg CHECK (credit_limit_cents IS NULL OR credit_limit_cents >= 0),
  CONSTRAINT customer_jobs_terms_nonneg CHECK (terms_days IS NULL OR terms_days >= 0),
  -- The job number is unique PER CUSTOMER, not globally.
  CONSTRAINT customer_jobs_job_no_per_customer UNIQUE (customer_id, job_no)
);

CREATE INDEX idx_customer_jobs_customer ON customer_jobs(customer_id);
CREATE INDEX idx_customer_jobs_branch ON customer_jobs(branch_id);
CREATE INDEX idx_customer_jobs_customer_status ON customer_jobs(customer_id, status);
