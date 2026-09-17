-- 0053: WTY-01 Warranty-Claim Management (P1). Owner: internal/warranty. Spec: PRODUCT_SPEC.md (G-LOCK-077),
-- DIB-153. The confirmed 2026-07-07 Kanban phases + the ~$40k / 6-8 open-claim backlog become tracked objects.
-- Conventions (house): money BIGINT *_cents · TIMESTAMPTZ · TEXT+CHECK · explicit ON DELETE · UUID PKs · every
-- tunable is a DB row. Registry keys off the vendor master (0011); claims link the customer/invoice/order/PO
-- masters when known. Evidence rides masked refs (the A-21 evidence-storage seam) — P1 stores the REFERENCE
-- shape only; file-upload infra is P2 (documented seam, not built here).

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. Vendor warranty registry (FR-1). One profile per vendor with an explicit UNDOCUMENTED state (the Oaks
--    case): a claim against an undocumented vendor prompts "capture the terms you negotiate" — the registry
--    LEARNS. channel is how the claim is filed (email/portal/rep). docs is the policy-document ref list
--    (also the future FR-10 AI-triage corpus — P2). term_months NULL = unknown/none.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_vendor_profiles (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vendor_id     UUID NOT NULL UNIQUE REFERENCES vendors(id) ON DELETE CASCADE,
  status        TEXT NOT NULL DEFAULT 'UNDOCUMENTED'
                  CHECK (status IN ('DOCUMENTED','UNDOCUMENTED')),
  coverage      TEXT NOT NULL DEFAULT '',        -- coverage summary (what's covered / excluded, free text)
  term_months   INTEGER CHECK (term_months IS NULL OR term_months >= 0), -- warranty term length; NULL = unknown
  channel       TEXT NOT NULL DEFAULT 'EMAIL'
                  CHECK (channel IN ('EMAIL','PORTAL','REP','PHONE','OTHER')),
  channel_ref   TEXT NOT NULL DEFAULT '',        -- the email / portal URL / rep name the channel resolves to
  docs          JSONB NOT NULL DEFAULT '[]',     -- [{title, ref}] policy-document refs (FR-10 corpus seam, P2)
  notes         TEXT NOT NULL DEFAULT '',
  created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_warranty_vendor_profiles_status ON warranty_vendor_profiles(status);

-- Per-product overrides on a vendor profile (FR-1): a product with a term/coverage that differs from the
-- vendor default. product_id keys the product master (0002).
CREATE TABLE warranty_product_overrides (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  profile_id    UUID NOT NULL REFERENCES warranty_vendor_profiles(id) ON DELETE CASCADE,
  product_id    UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  coverage      TEXT NOT NULL DEFAULT '',
  term_months   INTEGER CHECK (term_months IS NULL OR term_months >= 0),
  notes         TEXT NOT NULL DEFAULT '',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (profile_id, product_id)
);
CREATE INDEX idx_warranty_product_overrides_product ON warranty_product_overrides(product_id);

-- ─────────────────────────────────────────────────────────────────────────────
-- 2. The claim object (FR-2). Each gets #WTY-n (claim_no, human sequence), an owner, and a vendor (or UNKNOWN
--    pending triage — vendor_id NULL). phase is the CONFIRMED 2026-07-07 Kanban lifecycle (FR-3). source is
--    the doorway (office/counter INTAKE, COUNTER from an invoice, WEB in P2). sla_due is stamped on entering
--    Submitted (FR-3 automation seam). Money (FR-5): expected vs realized cents; outcome on close. opened_at
--    is BACK-DATABLE (FR-9 migration of the live claims with true history).
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_claims (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  claim_no      CITEXT NOT NULL UNIQUE,          -- #WTY-n human sequence (assigned in service, gap-free per year)
  customer_id   UUID REFERENCES customers(id) ON DELETE SET NULL,
  job_address   JSONB NOT NULL DEFAULT '{}',     -- {line1, city, province, postal} — the failure site
  vendor_id     UUID REFERENCES vendors(id) ON DELETE SET NULL,   -- NULL = UNKNOWN pending triage
  owner_id      UUID REFERENCES users(id) ON DELETE SET NULL,     -- whose court the ball is in (FR-2)
  branch_id     UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  phase         TEXT NOT NULL DEFAULT 'INTAKE'
                  CHECK (phase IN ('INTAKE','TRIAGE','SUBMITTED','WAITING_ON_DECISION',
                                   'DECISION','SETTLEMENT','CLOSED','REJECTED','ABANDONED')),
  source        TEXT NOT NULL DEFAULT 'INTAKE'
                  CHECK (source IN ('INTAKE','COUNTER','WEB')),
  description   TEXT NOT NULL DEFAULT '',         -- failure description (efflorescence, spalling, breakage, …)
  invoice_id    UUID REFERENCES invoices(id) ON DELETE SET NULL,  -- linked original-sale invoice (FR-2)
  order_id      UUID REFERENCES orders(id) ON DELETE SET NULL,    -- linked order line the claim started from
  po_id         UUID,                            -- linked purchase order/receipt (procurement master; SET-NULL-soft, no hard FK — P1 seam)
  supplier_purchase_date DATE,                   -- claim-level purchase-from-supplier date (sheet Inventory tab)
  submitted_at  TIMESTAMPTZ,                     -- stamped on entering Submitted (FR-3 automation)
  sla_due       TIMESTAMPTZ,                     -- SLA timer target set on Submitted (FR-3; notify seam is P2)
  outcome       TEXT
                  CHECK (outcome IS NULL OR outcome IN
                    ('VENDOR_CREDIT','REPLACEMENT_STOCK','CUSTOMER_ACCOMMODATION','WRITE_OFF')),
  expected_cents BIGINT NOT NULL DEFAULT 0,      -- expected recovery (the exposure the ~$40k backlog measures)
  realized_cents BIGINT NOT NULL DEFAULT 0,      -- what actually came back (settlement recording, FR-5)
  gl_journal_id UUID REFERENCES gl_journals(id) ON DELETE SET NULL, -- the settlement journal when GL-wired (P2)
  opened_at     TIMESTAMPTZ NOT NULL DEFAULT now(), -- BACK-DATABLE on capture (FR-9)
  closed_at     TIMESTAMPTZ,                     -- set on entering a terminal phase
  metadata      JSONB NOT NULL DEFAULT '{}',
  created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- A closed claim (terminal phase) must carry a closed_at; an open claim must not. Keeps funnel/age math honest.
  CONSTRAINT warranty_claims_closed_at CHECK (
    (phase IN ('CLOSED','REJECTED','ABANDONED')) = (closed_at IS NOT NULL)
  )
);
CREATE INDEX idx_warranty_claims_phase ON warranty_claims(phase);
CREATE INDEX idx_warranty_claims_vendor ON warranty_claims(vendor_id);
CREATE INDEX idx_warranty_claims_branch_phase ON warranty_claims(branch_id, phase);
CREATE INDEX idx_warranty_claims_owner ON warranty_claims(owner_id);

-- Claim lines (FR-2): the product(s) + qty on a claim, with both purchase dates from the sheet's Inventory tab
-- (supplier purchase date + contractor purchase date). product_id NULL tolerated for a free-text/web line.
CREATE TABLE warranty_claim_lines (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  claim_id      UUID NOT NULL REFERENCES warranty_claims(id) ON DELETE CASCADE,
  product_id    UUID REFERENCES products(id) ON DELETE SET NULL,
  product_text  TEXT NOT NULL DEFAULT '',        -- free-text product when not a catalogue match (web/retail intake)
  qty           DECIMAL(19,4) NOT NULL DEFAULT 0,
  uom_code      CITEXT REFERENCES uoms(code) ON DELETE SET NULL,
  purchase_date_supplier   DATE,                 -- purchase date from supplier (sheet)
  purchase_date_contractor DATE,                 -- purchase date from contractor (sheet)
  notes         TEXT NOT NULL DEFAULT '',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_warranty_claim_lines_claim ON warranty_claim_lines(claim_id);
CREATE INDEX idx_warranty_claim_lines_product ON warranty_claim_lines(product_id);

-- ─────────────────────────────────────────────────────────────────────────────
-- 3. The audit spine (FR-3). Every phase transition AND every checklist tick lands here, append-only — this is
--    how the funnel counts + time-to-settlement + who-did-what history are computed. event_type distinguishes
--    a PHASE_CHANGE (from_phase→to_phase) from a CHECKLIST_TICK (checklist_item_id + checked) from a note.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_claim_events (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  claim_id      UUID NOT NULL REFERENCES warranty_claims(id) ON DELETE CASCADE,
  event_type    TEXT NOT NULL
                  CHECK (event_type IN ('CREATED','PHASE_CHANGE','CHECKLIST_TICK','SETTLEMENT','NOTE','EVIDENCE')),
  from_phase    TEXT,                            -- PHASE_CHANGE: the phase left
  to_phase      TEXT,                            -- PHASE_CHANGE: the phase entered
  checklist_item_id UUID,                        -- CHECKLIST_TICK: the item toggled (soft ref, template may change)
  checklist_key TEXT NOT NULL DEFAULT '',        -- CHECKLIST_TICK: the item key, denormalized for history stability
  checked       BOOLEAN,                         -- CHECKLIST_TICK: the new checked state
  note          TEXT NOT NULL DEFAULT '',
  evidence_ref  TEXT NOT NULL DEFAULT '',        -- EVIDENCE: masked evidence-store ref (A-21 seam; no file infra in P1)
  actor_id      UUID REFERENCES users(id) ON DELETE SET NULL,
  detail        JSONB NOT NULL DEFAULT '{}',     -- extra structured payload (settlement amounts, etc.)
  -- occurred_at is distinct from created_at so a BACK-DATED migration event carries its TRUE time (FR-9).
  occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_warranty_claim_events_claim ON warranty_claim_events(claim_id, occurred_at);
CREATE INDEX idx_warranty_claim_events_type ON warranty_claim_events(event_type);

-- ─────────────────────────────────────────────────────────────────────────────
-- 4. Checklist templates (FR-3). Per-phase (and optionally per-vendor) required/optional items that GATE
--    advancement: a phase can't advance while any REQUIRED item for that phase is unticked. Data-driven so a
--    checklist is a row edit, not a redeploy. vendor_id NULL = the default template applied to every vendor;
--    a vendor-specific row overrides the default for that (phase, item_key).
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_checklist_items (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  phase         TEXT NOT NULL
                  CHECK (phase IN ('INTAKE','TRIAGE','SUBMITTED','WAITING_ON_DECISION','DECISION','SETTLEMENT')),
  vendor_id     UUID REFERENCES vendors(id) ON DELETE CASCADE,  -- NULL = default (all vendors)
  item_key      TEXT NOT NULL,                   -- stable key (e.g. 'photos_attached') — history-stable identifier
  label         TEXT NOT NULL,                   -- human label shown on the card
  required      BOOLEAN NOT NULL DEFAULT TRUE,   -- required items gate advancement OUT of the phase
  sort_order    INTEGER NOT NULL DEFAULT 0,
  is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- one row per (phase, item_key) for the default; one per (phase, vendor, item_key) for a vendor override.
  UNIQUE (phase, vendor_id, item_key)
);
CREATE INDEX idx_warranty_checklist_items_phase ON warranty_checklist_items(phase, is_active);

-- ─────────────────────────────────────────────────────────────────────────────
-- 5. GL posting-rule roles for the settlement journals (FR-5). P1 RECORDS settlements + amounts; the GL wiring
--    is a documented seam (posted through gl.PostExternal when the manager books it — see the module's GLPoster
--    seam). These rows register the account roles so the P2 wire-up is a config, not a schema change.
--    Accounts are DOCUMENTED PLACEHOLDERS pending the accountant ruling (PRD §9 Q5 — joins the Q-021 set).
-- ─────────────────────────────────────────────────────────────────────────────
INSERT INTO gl_accounts (code, name, account_type, normal_balance) VALUES
  ('4900','Warranty Recovery (vendor credits)', 'REVENUE',  'CREDIT'),
  ('5900','Warranty Expense (write-offs / accommodations)', 'EXPENSE', 'DEBIT')
ON CONFLICT (code) DO NOTHING;

INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
  ('warranty.settled', 'warranty_recovery', '4900'),
  ('warranty.settled', 'warranty_expense',  '5900')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
