-- 0045: A-06 — contractor-portal customer returns with a REQUEST + STAFF-APPROVAL flow (commander-ruled
-- 2026-07-13). A portal user submits a return REQUEST on one of their orders (no money, no stock); a
-- staff member approves (executes the EXISTING W7 POS return rail with ACCOUNT_CREDIT + restock) or
-- rejects with a reason. Owner module: internal/pos (the returns domain — pos_returns/0043 precedent);
-- the portal BFF reaches it only through the pos service seam, never these tables.
-- Conventions: UUID PKs · money BIGINT *_cents · TEXT + CHECK (no ENUMs) · TIMESTAMPTZ · explicit ON DELETE.

-- ── 1. Restocking-fee policy — DATA, not code (nsf_policy fee precedent; pos_policy is the POS
--     behavior policy, branch row overrides the org NULL row). Default 0 bps — commander-ruled: no fee
--     until the client turns one on. Admin-writable through the existing PUT /api/v1/pos/policy.
ALTER TABLE pos_policy
  ADD COLUMN restocking_fee_bps INT NOT NULL DEFAULT 0
  CHECK (restocking_fee_bps BETWEEN 0 AND 10000);

-- ── 2. The fee's money trail. The fee (when > 0) reduces the ACCOUNT_CREDIT refund and books to its
--     own revenue line: pos.return.committed grows an optional fee_cents — DR revenue + DR HST stay the
--     full reversal; CR AR drops by the fee and the difference credits 4210 (the 4200 NSF-fee shape).
ALTER TABLE pos_returns
  ADD COLUMN fee_cents BIGINT NOT NULL DEFAULT 0 CHECK (fee_cents >= 0);
INSERT INTO gl_accounts (code, name, account_type, normal_balance) VALUES
  ('4210','Restocking Fee Income','REVENUE','CREDIT');
INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
  ('pos.return.committed', 'fee_revenue', '4210');

-- ── 3. Return requests — the portal-submitted intent. PENDING moves NO money and NO stock; approval
--     links the pos_returns row the rail created; rejection records only the reason.
CREATE SEQUENCE return_request_no_seq START 1;
CREATE TABLE return_requests (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  request_no      CITEXT NOT NULL UNIQUE,        -- '#RREQ-n'
  order_id        UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  customer_id     UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  status          TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','APPROVED','REJECTED')),
  reason_code     TEXT NOT NULL CHECK (reason_code IN ('DAMAGED','DEFECT','WRONG_ITEM','NO_LONGER_NEEDED','OTHER')),
  note            TEXT NOT NULL DEFAULT '',      -- free text; REQUIRED when reason_code = 'OTHER' (service-enforced)
  requested_by    UUID REFERENCES portal_users(id) ON DELETE SET NULL,  -- the portal user who asked
  decided_by      UUID REFERENCES users(id) ON DELETE SET NULL,         -- the staff approver/rejecter
  decided_at      TIMESTAMPTZ,
  decision_reason TEXT NOT NULL DEFAULT '',      -- REQUIRED on rejection (service-enforced)
  fee_cents       BIGINT NOT NULL DEFAULT 0 CHECK (fee_cents >= 0),     -- restocking fee snapshot at approval
  return_id       UUID REFERENCES pos_returns(id) ON DELETE SET NULL,   -- the rail-committed return (approval)
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_return_requests_status   ON return_requests(status, created_at);
CREATE INDEX idx_return_requests_customer ON return_requests(customer_id, created_at);

CREATE TABLE return_request_lines (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id    UUID NOT NULL REFERENCES return_requests(id) ON DELETE CASCADE,
  order_line_id UUID NOT NULL REFERENCES order_lines(id) ON DELETE RESTRICT,
  qty_base      DECIMAL(19,4) NOT NULL CHECK (qty_base > 0)
);
CREATE INDEX idx_return_request_lines_request ON return_request_lines(request_id);
