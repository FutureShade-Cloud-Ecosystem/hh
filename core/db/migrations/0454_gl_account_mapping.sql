-- 0454: the accounting package export's account mapping (W3 AX-1, decision 1.6, audit GL-08).
-- Each HH GL account maps to a target package's account code, per target: QuickBooks Online first,
-- Sage 50 next. A third package is one more word in the CHECK, by a later forward migration. The
-- mapping is read at export time, so a re-mapping answers in the next export, and every recorded
-- export batch keeps the content it was rendered under (the batch row is the record).

CREATE TABLE gl_account_mapping (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  target          TEXT NOT NULL CHECK (target IN ('QUICKBOOKS_ONLINE','SAGE_50')),
  account_code    CITEXT NOT NULL REFERENCES gl_accounts(code) ON DELETE CASCADE,
  package_account TEXT NOT NULL CHECK (btrim(package_account) <> ''),
  set_by          UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (target, account_code)
);
