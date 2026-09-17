-- 0038: W6 M6 — Lien Cockpit + Bill 60 holdback + compliance calendar. Owners: internal/lien + internal/compliance.
-- Conventions: money BIGINT *_cents · TIMESTAMPTZ · TEXT+CHECK · explicit ON DELETE · every tunable is a DB row.
-- M6 has ZERO client-validated requirements (Scrubname0013's discovery stale, Scrubname0001 silent) → the templates are
-- statute-driven and DRAFT-bannered; the DIB-42 Scrubname0030 demo is the only validation checkpoint. All banner /
-- deadline / percentage values here are DOCUMENTED PLACEHOLDERS pending ratification (D-021).

-- 1. Lien job: the Construction-Act "improvement" a holdback + release cycle attaches to. Loosely linked to a
--    customer/branch; a job can span many invoices (the holdback aggregates across them).
CREATE TABLE lien_jobs (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_no        CITEXT NOT NULL UNIQUE,
  customer_id   UUID REFERENCES customers(id) ON DELETE SET NULL,
  branch_id     UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  description   TEXT NOT NULL DEFAULT '',
  contract_cents          BIGINT NOT NULL DEFAULT 0,
  holdback_pct_bps        INTEGER NOT NULL DEFAULT 1000 CHECK (holdback_pct_bps BETWEEN 0 AND 10000), -- 10% statutory (PLACEHOLDER)
  substantial_perf_date   DATE,          -- clock start for the 45-day release (Bill 60)
  last_supply_date        DATE,
  status        TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','SUBSTANTIALLY_PERFORMED','RELEASED','CLOSED')),
  metadata      JSONB NOT NULL DEFAULT '{}',
  created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_lien_jobs_customer ON lien_jobs(customer_id);
CREATE INDEX idx_lien_jobs_branch_status ON lien_jobs(branch_id, status);

-- 2. Participants on a job (owner / GC / sub / payer / trustee) for the Monday triage worklist + notices.
CREATE TABLE lien_participants (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id      UUID NOT NULL REFERENCES lien_jobs(id) ON DELETE CASCADE,
  party_role  TEXT NOT NULL CHECK (party_role IN ('OWNER','GENERAL_CONTRACTOR','SUBCONTRACTOR','PAYER','TRUSTEE','OTHER')),
  name        TEXT NOT NULL,
  contact     JSONB NOT NULL DEFAULT '{}',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_lien_participants_job ON lien_participants(job_id);

-- 3. Lien documents — statute-driven, DRAFT-bannered, DATA-DRIVEN templates rendered to versioned content.
--    template_kind is the Ontario Construction Act form; status carries the DRAFT/PENDING banner state.
CREATE TABLE lien_documents (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id        UUID NOT NULL REFERENCES lien_jobs(id) ON DELETE CASCADE,
  template_kind TEXT NOT NULL CHECK (template_kind IN
                  ('CONDITIONAL_RELEASE','UNCONDITIONAL_RELEASE','STATUTORY_DECLARATION','DISCHARGE','NOTICE','OTHER')),
  status        TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','PENDING','ISSUED','SUPERSEDED')),
  version       INTEGER NOT NULL DEFAULT 1,
  rendered_content TEXT NOT NULL DEFAULT '',
  data          JSONB NOT NULL DEFAULT '{}',      -- the merge fields the template rendered from
  is_draft_banner  BOOLEAN NOT NULL DEFAULT TRUE, -- data-driven DRAFT banner (M6 has no client sign-off yet)
  issued_at     TIMESTAMPTZ,
  created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_lien_documents_job ON lien_documents(job_id, template_kind);

-- 4. Holdback ledger — retention aging + release, keyed to a job (and optionally an invoice). RETENTION books
--    the 1150 holdback receivable / 2350 holdback payable; a RELEASE MUST carry the GL journal it posted (the
--    _HoldbackReleaseHasJournal gate-suite invariant is enforced structurally by the CHECK below — a release
--    with no journal cannot be inserted). branch_id for the per-branch aging buckets.
CREATE TABLE holdback_ledger (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id        UUID NOT NULL REFERENCES lien_jobs(id) ON DELETE RESTRICT,
  invoice_id    UUID REFERENCES invoices(id) ON DELETE SET NULL,
  branch_id     UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  entry_type    TEXT NOT NULL CHECK (entry_type IN ('RETENTION','RELEASE')),
  amount_cents  BIGINT NOT NULL,
  gl_journal_id UUID REFERENCES gl_journals(id) ON DELETE RESTRICT,   -- the posted 1150/2350 journal
  released_at   DATE,
  reason        TEXT NOT NULL DEFAULT '',
  created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- A RELEASE without its GL journal is a book that lies (retention released off-ledger). Forbidden.
  CONSTRAINT holdback_release_has_journal CHECK (entry_type <> 'RELEASE' OR gl_journal_id IS NOT NULL)
);
CREATE INDEX idx_holdback_ledger_job ON holdback_ledger(job_id);
CREATE INDEX idx_holdback_ledger_branch ON holdback_ledger(branch_id, entry_type);

-- 5. Compliance calendar — the deadline registry driving the 45-day release rule + Scrubname0030's Monday triage
--    reminders (badges, not a scheduler — no scheduler infra at W6; the worklist is computed on read).
CREATE TABLE compliance_calendar (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id        UUID REFERENCES lien_jobs(id) ON DELETE CASCADE,
  deadline_kind TEXT NOT NULL CHECK (deadline_kind IN
                  ('HOLDBACK_RELEASE','LIEN_EXPIRY','NOTICE_DEADLINE','PROOF_OF_CLAIM','OTHER')),
  due_date      DATE NOT NULL,
  rule_days     INTEGER NOT NULL DEFAULT 45,   -- 45-day statutory release window (PLACEHOLDER)
  status        TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','SATISFIED','WAIVED','OVERDUE')),
  notes         TEXT NOT NULL DEFAULT '',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_compliance_calendar_due ON compliance_calendar(due_date, status);

-- 6. Review status registry — the shared DRAFT/PENDING banner source the M6 UI + templates read (data-driven
--    so a banner is a row edit, never a redeploy). subject_type/id addresses a job or a document.
CREATE TABLE review_status (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  subject_type  TEXT NOT NULL CHECK (subject_type IN ('LIEN_JOB','LIEN_DOCUMENT','HOLDBACK','OTHER')),
  subject_id    UUID NOT NULL,
  state         TEXT NOT NULL DEFAULT 'DRAFT' CHECK (state IN ('DRAFT','PENDING','APPROVED','REJECTED')),
  banner_text   TEXT NOT NULL DEFAULT 'DRAFT — statute-derived, pending client validation (DIB-42)',
  reviewed_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  reviewed_at   TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_review_status_subject ON review_status(subject_type, subject_id);

-- 7. order_lines gains the per-line holdback status the order lien seam reads (HoldbackStatusForOrder). NONE =
--    no retention applies; HELD = retention withheld; RELEASED = the 45-day window satisfied + journal posted.
ALTER TABLE order_lines
  ADD COLUMN IF NOT EXISTS lien_holdback_status TEXT NOT NULL DEFAULT 'NONE'
    CHECK (lien_holdback_status IN ('NONE','HELD','RELEASED'));

-- 8. Holdback GL accounts (the retention pair). 1150 already exists (0007 invoice.finalized holdback leg);
--    2350 is the payable side. Seed the holdback.released posting rule roles for gl.PostExternal.
INSERT INTO gl_accounts (code, name, account_type, normal_balance) VALUES
  ('2350','Holdback Payable (Construction Act)', 'LIABILITY','CREDIT')
ON CONFLICT (code) DO NOTHING;

INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
  ('holdback.released', 'holdback_receivable', '1150'),
  ('holdback.released', 'holdback_payable',    '2350')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
