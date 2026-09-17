-- 0173 — DIB-506/DIB-507: quote_policy (the pilot's TYPED policy half of the D-105 hybrid) + the
-- quote TERMS / summary fields the concept's work area edits + the per-line discount.
--
-- ── quote_policy — behavior/security-bearing knobs, TYPED (spec §4.3/§4.4) ──────────────────────
-- The 0043 pos_policy shape verbatim: branch_id UUID UNIQUE NULL where the NULL row IS the dealer
-- default and a branch row overrides; flipped live through the admin-gated policy PUT (service
-- validates 422-in-words BEFORE any CHECK could 500 — internal/settings + internal/quote/builder.go).
--
--   cost_visibility       WHO may receive cost/margin fields on quote payloads — the 0170
--                         margin_visibility vocabulary (MANAGER_UP default | ALL_STAFF | HIDDEN),
--                         enforced at the MARSHAL layer (byte-absence, fail closed) by the quote
--                         margin glance. Display never guards secrets; the server does.
--   default_validity_days the "Valid for" default a new quote opens on.
--   default_deposit_pct   the deposit % a quote's terms strip opens on (0 = deposit off).
--   line_discounts_enabled  the per-line Disc %% input: off = the input is refused server-side too.
--   send_email_enabled / send_sms_enabled  which SEND channels the dealer offers.
--   include_product_images  the dealer default for "include product images on the sent quote".
--   quick_chip_set        curated category chips for the products panel ('' = derive from
--                         top-selling categories at read time — the 0170 posture, re-consumed).
--   workspace_split_pct / workspace_arrangement  the dealer-default panel geometry (0166's
--                         columns, repeated per-surface as typed policy — spec §5.1).
CREATE TABLE quote_policy (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id              UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE, -- NULL = dealer default row
  cost_visibility        TEXT NOT NULL DEFAULT 'MANAGER_UP'
                           CHECK (cost_visibility IN ('MANAGER_UP', 'ALL_STAFF', 'HIDDEN')),
  default_validity_days  INTEGER NOT NULL DEFAULT 30 CHECK (default_validity_days BETWEEN 1 AND 365),
  default_deposit_pct    INTEGER NOT NULL DEFAULT 0 CHECK (default_deposit_pct BETWEEN 0 AND 100),
  line_discounts_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  send_email_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
  send_sms_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
  include_product_images BOOLEAN NOT NULL DEFAULT TRUE,
  quick_chip_set         TEXT NOT NULL DEFAULT '',
  workspace_split_pct    INTEGER NOT NULL DEFAULT 58 CHECK (workspace_split_pct BETWEEN 20 AND 80),
  workspace_arrangement  TEXT NOT NULL DEFAULT 'PRIMARY_LEFT'
                           CHECK (workspace_arrangement IN ('PRIMARY_LEFT', 'PRIMARY_RIGHT')),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The dealer default row, seeded so the restrictive default is VISIBLE in the data (the 0043/0162
-- posture). send_sms_enabled is seeded TRUE for the Dibbits deployment — an explicit dealer choice
-- (the DIB-77 SMS quote delivery already ships consent-gated); the COLUMN default stays FALSE so a
-- future dealer starts email-only until they opt in.
INSERT INTO quote_policy (branch_id, send_sms_enabled) VALUES (NULL, TRUE);

COMMENT ON TABLE quote_policy IS
  'DIB-506/507 pilot: dealer policy for the quotes.builder surface. NULL branch_id row = dealer default; branch row overrides (0043 idiom). Security-bearing knobs stay typed where the marshal layer reads them (D-105).';
COMMENT ON COLUMN quote_policy.cost_visibility IS
  'Which staff tiers receive cost/margin fields on quote payloads (marshal-gated, byte-absent otherwise, fail closed). MANAGER_UP (default) | ALL_STAFF | HIDDEN.';

-- ── quote TERMS + summary fields (spec §5.4: the column always exists on the domain object) ─────
-- The work area's right panel edits these; the dealer's field-set knobs only govern rendering.
-- All defaults are the no-op so every existing quote is byte-identical after this migration.
ALTER TABLE quotes
  ADD COLUMN deposit_required   BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN deposit_pct        INTEGER NOT NULL DEFAULT 0 CHECK (deposit_pct BETWEEN 0 AND 100),
  ADD COLUMN include_images     BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN customer_notes     TEXT NOT NULL DEFAULT '',
  ADD COLUMN contact_name       TEXT NOT NULL DEFAULT '',
  ADD COLUMN project_job        TEXT NOT NULL DEFAULT '',
  ADD COLUMN customer_po        TEXT NOT NULL DEFAULT '',
  ADD COLUMN salesperson_id     UUID REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN fulfillment        TEXT NOT NULL DEFAULT 'PICKUP' CHECK (fulfillment IN ('PICKUP', 'DELIVERY')),
  ADD COLUMN delivery_address   TEXT NOT NULL DEFAULT '',
  ADD COLUMN requested_date     DATE,
  ADD COLUMN delivery_fee_cents BIGINT NOT NULL DEFAULT 0 CHECK (delivery_fee_cents >= 0);

ALTER TABLE quotes ADD CONSTRAINT ck_quotes_customer_notes_len   CHECK (char_length(customer_notes) <= 500);
ALTER TABLE quotes ADD CONSTRAINT ck_quotes_contact_name_len     CHECK (char_length(contact_name) <= 120);
ALTER TABLE quotes ADD CONSTRAINT ck_quotes_project_job_len      CHECK (char_length(project_job) <= 64);
ALTER TABLE quotes ADD CONSTRAINT ck_quotes_customer_po_len      CHECK (char_length(customer_po) <= 64);
ALTER TABLE quotes ADD CONSTRAINT ck_quotes_delivery_address_len CHECK (char_length(delivery_address) <= 200);

COMMENT ON COLUMN quotes.customer_notes IS
  'DIB-506: free-text notes printed on the sent/shared quote (≤500 chars, 422-validated in words before this CHECK).';
COMMENT ON COLUMN quotes.delivery_fee_cents IS
  'DIB-506: the quoted delivery fee. Folded into total_cents by the totals recompute (a 0 fee is byte-identical to pre-0173 behavior).';

-- ── per-line discount (concept: the Disc %% column) ──────────────────────────────────────────────
--   disc_pct                 hundredths of a percent NEVER — plain integer percent 0–100, matching
--                            the concept's whole-percent input; 0 = no discount (the backfill).
--   pre_discount_total_cents the ENGINE's undiscounted line total, stored so the totals card can
--                            speak Products vs Discount without lossy back-arithmetic. 0 on legacy
--                            lines means "same as line_total_cents" (no discount ever applied).
ALTER TABLE quote_lines
  ADD COLUMN disc_pct                 INTEGER NOT NULL DEFAULT 0 CHECK (disc_pct BETWEEN 0 AND 100),
  ADD COLUMN pre_discount_total_cents BIGINT  NOT NULL DEFAULT 0 CHECK (pre_discount_total_cents >= 0);

COMMENT ON COLUMN quote_lines.disc_pct IS
  'DIB-506: per-line discount percent (0-100). line_total_cents is stored NET of this discount; pre_discount_total_cents keeps the engine''s gross figure.';
