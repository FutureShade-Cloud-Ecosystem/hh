-- 0105: AR statements — month-end mass-send + on-demand interim statements (DIB-166).
-- Owner module: internal/billing. Wave 3, Swarm B.
--
-- WHAT THE OFFICE DOES TODAY. At month end somebody opens each account, reads the balance off the
-- screen, and types an email. There is no record that a statement was produced, no record of what it
-- SAID, and therefore no answer to "you never sent me one" other than a person's memory. Every one of
-- those three gaps is a collections problem before it is a software problem.
--
-- ── A STATEMENT IS AN ARTEFACT, NOT A QUERY ──────────────────────────────────────────────────────
-- The central decision here: a statement row stores the RENDERED HTML and the aging figures as they
-- stood at as_of_date. It is not a saved query re-run on demand. A statement is a claim we made to a
-- customer on a date, and re-deriving it later from live tables would silently rewrite history the
-- moment an invoice is voided, a payment is reversed, or the aging policy changes. When a contractor
-- calls in March about the January statement, the answer must be the January statement.
--
-- That is the same reasoning payment_nsf uses to snapshot the fee at bounce time, and the same
-- reasoning tax_decision_snapshot uses. It costs disk and it buys the ability to be held to your word.
--
-- ── PRINT_QUEUE GETS A THIRD DOC TYPE, NOT A FOURTH MECHANISM ────────────────────────────────────
-- 0088 built print_queue as "the one substrate every printed document goes through" and shipped it
-- with RECEIPT/LABEL_ZPL/DELIVERY_DOC. A statement is a BROWSER-target document with a ref_id and a
-- branch, i.e. precisely the shape print_queue already carries, so it gets a doc_type rather than a
-- private print path. The CHECK widening is additive: every value that validated before still does.
-- ref_id points at the ar_statements row (0088's doc_type-decides-what-ref_id-means posture).
--
-- ── SENDING IS FLAG-OFF, AND THE QUEUE IS THE PROOF ──────────────────────────────────────────────
-- A mass-send ENQUEUES; it does not deliver. delivery_status starts QUEUED and only a wired outbox
-- consumer moves it. With no consumer wired (the shipped default) a run produces statements, records
-- that they were queued, and sends nothing — which is the correct behaviour for a system that does
-- not yet hold a verified email or mobile for most accounts. The alternative, sending on generate,
-- makes the first accidental month-end run an incident with customers in it.
--
-- Conventions: UUID PKs · money BIGINT *_cents · TEXT + CHECK · TIMESTAMPTZ · explicit ON DELETE.
-- Rollback: additive (two tables + one CHECK widening). Dropping the tables leaves print_queue's
-- widened CHECK harmless.

-- ── 1. print_queue admits STATEMENT ──────────────────────────────────────────────────────────────
ALTER TABLE print_queue DROP CONSTRAINT print_queue_doc_type_check;
ALTER TABLE print_queue ADD CONSTRAINT print_queue_doc_type_check
  CHECK (doc_type IN ('RECEIPT','LABEL_ZPL','DELIVERY_DOC','STATEMENT'));

-- ── 2. A statement run — one row per "send the statements" action ────────────────────────────────
-- mode distinguishes the month-end sweep from a single on-demand interim statement, because the two
-- are asked about differently: "did the June run go out?" versus "who pulled an interim for this
-- account and when?".
CREATE TABLE ar_statement_runs (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  mode            TEXT NOT NULL CHECK (mode IN ('MASS','INTERIM')),
  branch_id       UUID REFERENCES locations(id) ON DELETE RESTRICT,  -- NULL = all branches
  as_of_date      DATE NOT NULL,
  status          TEXT NOT NULL DEFAULT 'RUNNING'
                    CHECK (status IN ('RUNNING','COMPLETED','FAILED')),
  -- Run accounting. generated ≠ queued: an account with a zero balance is generated and NOT queued
  -- for send (nobody wants a statement saying they owe nothing), and that difference must be visible.
  generated_count INT NOT NULL DEFAULT 0 CHECK (generated_count >= 0),
  queued_count    INT NOT NULL DEFAULT 0 CHECK (queued_count >= 0),
  error           TEXT NOT NULL DEFAULT '',
  requested_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at     TIMESTAMPTZ
);
CREATE INDEX idx_ar_statement_runs_recent ON ar_statement_runs (created_at DESC);

-- ── 3. The statements themselves ─────────────────────────────────────────────────────────────────
CREATE TABLE ar_statements (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id              UUID NOT NULL REFERENCES ar_statement_runs(id) ON DELETE CASCADE,
  customer_id         UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  as_of_date          DATE NOT NULL,
  -- Aging as it stood. These four sum to closing_balance_cents, and the CHECK below says so, so a
  -- bucketing bug is caught by the database at write time rather than by a customer at read time.
  current_cents       BIGINT NOT NULL DEFAULT 0,
  days_30_cents       BIGINT NOT NULL DEFAULT 0,
  days_60_cents       BIGINT NOT NULL DEFAULT 0,
  days_90_plus_cents  BIGINT NOT NULL DEFAULT 0,
  closing_balance_cents BIGINT NOT NULL DEFAULT 0,
  -- The artefact. Self-contained HTML (inline styles, no external assets) so it renders identically
  -- in a print dialog, an email body and an archive five years from now.
  html                TEXT NOT NULL DEFAULT '',
  print_job_id        UUID REFERENCES print_queue(id) ON DELETE SET NULL,
  delivery_status     TEXT NOT NULL DEFAULT 'NOT_QUEUED'
                        CHECK (delivery_status IN ('NOT_QUEUED','QUEUED','SENT','FAILED')),
  delivery_error      TEXT NOT NULL DEFAULT '',
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ar_statements_buckets_sum CHECK
    (current_cents + days_30_cents + days_60_cents + days_90_plus_cents = closing_balance_cents)
);
-- One statement per customer per run: a re-run inside the same run is a bug, not a second statement.
CREATE UNIQUE INDEX ux_ar_statements_run_customer ON ar_statements (run_id, customer_id);
-- The account read: "every statement we ever sent this customer, newest first".
CREATE INDEX idx_ar_statements_customer ON ar_statements (customer_id, as_of_date DESC);
