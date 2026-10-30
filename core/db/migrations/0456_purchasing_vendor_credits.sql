-- 0456: vendor credits, the supplier credit note (wave 3 item 0.8).
--
-- THE DOCUMENT. A vendor ships pavers back, doubles an invoice by mistake, or agrees a price
-- adjustment after the bill posted: the supplier sends a credit note, and until now that note had
-- no door of its own in purchasing. The note is entered against a vendor and a branch (the
-- supplier's own credit number, a document date, an optional original bill or PO reference), posted
-- through the same GL seam a bill posts through, and applied to that vendor's open bills in the
-- same branch or held on account.
--
-- THE JOURNAL. A credit note is a bill read backwards:
--
--   DR 2100 Accounts Payable        the liability shrinks, through the bill's own posting rule
--                                   (vendor.bill.posted/ap), never a literal code.
--   CR <the lines' accounts>        inventory clearing or expense, each line naming the account it
--                                   puts the credit back on. The lines' accounts resolve against
--                                   the bill event's own debit side roles (ap_accrual, the GRNI
--                                   clearing account, and price_variance, the expense), so a credit
--                                   can only land where a bill could have charged.
--
-- No new posting rules and no new GL source: the credit rides the vendor.bill.posted event's rules
-- and the PO_RECEIPT source its bill sibling rides, because the CHECK set on gl_journals.source is
-- closed and a credit note is a purchasing AP document like the bill it reverses.
--
-- THE APPLICATION. Applying a credit to a bill is a netting INSIDE accounts payable: the credit's
-- journal already debited 2100 and the bill's journal already credited it, so an application moves
-- no money and writes no journal. It moves the open amounts in the subledger only, which is why an
-- unapplied credit shows in the AP aging as a negative item on the vendor's row and the aging still
-- ties to GL 2100 to the cent.

CREATE TABLE purchasing_vendor_credits (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- The SUPPLIER's credit note number as printed on their paper, the duplicate-entry control the
  -- bill's vendor invoice number carries: the same note cannot be entered twice under two clerks.
  credit_no   CITEXT NOT NULL,
  vendor_id   UUID NOT NULL REFERENCES vendors(id) ON DELETE RESTRICT,
  branch_id   UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  -- The optional original bill or PO the credit refers to. A reference is a pointing hand for the
  -- clerk, never a constraint on where the credit may be applied: the application's own guards
  -- decide that, at apply time, against the opens as they stand then.
  bill_id     UUID REFERENCES purchasing_bills(id) ON DELETE RESTRICT,
  po_id       UUID REFERENCES purchasing_pos(id) ON DELETE RESTRICT,
  status      TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','POSTED')),
  credit_date DATE NOT NULL DEFAULT CURRENT_DATE,
  -- The credit's total, always the positive magnitude the supplier wrote. The sign lives in the
  -- journal and in the aging's negative item, never in this column.
  total_cents BIGINT NOT NULL CHECK (total_cents > 0),
  gl_journal_id UUID REFERENCES gl_journals(id) ON DELETE RESTRICT,
  posted_at   TIMESTAMPTZ,
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (vendor_id, credit_no)
);

CREATE INDEX idx_purchasing_vendor_credits_branch_status ON purchasing_vendor_credits(branch_id, status);
CREATE INDEX idx_purchasing_vendor_credits_vendor ON purchasing_vendor_credits(vendor_id);

CREATE TABLE purchasing_vendor_credit_lines (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  credit_id    UUID NOT NULL REFERENCES purchasing_vendor_credits(id) ON DELETE CASCADE,
  line_no      INTEGER NOT NULL CHECK (line_no > 0),
  -- The account the credit puts its cents back on, resolved against the posting rules at entry
  -- (the bill event's ap_accrual and price_variance accounts) and stored resolved, so the journal
  -- posts what the clerk saw and no second copy of the rule exists.
  account_code TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
  UNIQUE (credit_id, line_no)
);

CREATE INDEX idx_purchasing_vendor_credit_lines_credit ON purchasing_vendor_credit_lines(credit_id);

-- One application moves amount from a credit's open onto one bill. A credit may be applied to the
-- same bill in several presses (each press bounded by the opens as they stand then); the
-- idempotency key is the replay control (one key per press, the Idempotency-Key header's rule): a
-- re-press under the first press's key answers the first result and writes nothing, and the unique
-- index makes a second row under one key impossible even under a race the advisory lock somehow
-- let through.
CREATE TABLE purchasing_vendor_credit_applications (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  credit_id       UUID NOT NULL REFERENCES purchasing_vendor_credits(id) ON DELETE CASCADE,
  bill_id         UUID NOT NULL REFERENCES purchasing_bills(id) ON DELETE RESTRICT,
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
  idempotency_key TEXT NOT NULL UNIQUE,
  applied_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  applied_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_purchasing_vcredit_apps_credit ON purchasing_vendor_credit_applications(credit_id);
CREATE INDEX idx_purchasing_vcredit_apps_bill ON purchasing_vendor_credit_applications(bill_id);
