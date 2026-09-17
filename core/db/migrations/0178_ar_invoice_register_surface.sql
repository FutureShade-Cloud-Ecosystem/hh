-- 0178 — DIB-510: the receivables register as a CONFIGURABLE SURFACE — the D-105 hybrid, second
--        consumer. `ar_invoice_policy` is the TYPED half; the `ar.invoices` rows in 0172's generic
--        substrate are the PRESENTATION half.
--
-- WHY BOTH HALVES AND NOT ONE. The quote pilot (0173) established the split and this migration is the
-- first proof it generalizes rather than being a quote-shaped idea: knobs that decide WHO MAY SEE
-- WHAT, or that the server must enforce whatever the screen shows, are typed columns read at the
-- marshal layer; knobs that decide WHICH COLUMN SITS WHERE are content-free strings and positions in
-- surface_role_items / surface_user_items. Nothing in this file teaches the generic substrate a single
-- receivables word — that was the exit criterion, and it holds.
--
-- ── ar_invoice_policy — the typed half (0043/0173 branch-NULL idiom) ───────────────────────────
--   account_position_visibility  WHO receives the ACCOUNT-LEVEL financial position on a register row
--                         or an invoice detail — the customer's total open balance, their credit
--                         limit and their available credit. MANAGER_UP (default) | ALL_STAFF |
--                         HIDDEN. Enforced by BYTE-ABSENCE at the marshal layer, fail closed: a
--                         refused caller receives a payload with those fields simply not present, and
--                         the screen renders nothing because there is nothing to render.
--
--                         WHY THIS FIELD AND NOT THE INVOICE. An invoice's own figures — subtotal,
--                         tax, total, balance, days overdue — are the document the customer already
--                         holds, and a counter clerk taking a cheque needs every one of them. What is
--                         genuinely sensitive is the ACCOUNT's position: "this contractor is CA$81k
--                         into a CA$60k limit" is a fact about the customer's business, it is what the
--                         existing AR console is manager-gated for (till/handler.go says so in as many
--                         words), and it belongs to the same tier here. Cost and margin are not on
--                         this screen at all and no knob can put them there.
--   due_soon_days         how many days ahead the "Due soon" chip reaches. Typed rather than curated
--                         because it changes a COUNT the screen asserts, and a count that means
--                         something different per user is not a count.
--   reminder_tones        which tones the Send-reminder control offers, in order. A dealer who never
--                         wants a FINAL notice sent from the counter takes it out here and the server
--                         REFUSES that tone — curation hides controls, policy is the authority (the
--                         0173 line_discounts_enabled ruling, re-applied).
--   reminder_cooldown_hours  how recently we may have contacted an ACCOUNT before the register warns
--                         the sender. Per customer, not per invoice: eleven overdue invoices is one
--                         conversation (0110's rule, re-consumed rather than re-invented).
--   show_credits_default  whether the register opens with credit memos (negative documents) shown.
--   default_branch_scope  '' = All branches; a branch id opens the register on that branch's tab.
--   workspace_split_pct / workspace_arrangement  the dealer-default panel geometry (0166 columns,
--                         per-surface as typed policy — the spec §5.1 shape 0173 already uses).
CREATE TABLE IF NOT EXISTS ar_invoice_policy (
  id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id                   UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE, -- NULL = dealer default
  account_position_visibility TEXT NOT NULL DEFAULT 'MANAGER_UP'
                                CHECK (account_position_visibility IN ('MANAGER_UP','ALL_STAFF','HIDDEN')),
  due_soon_days               INTEGER NOT NULL DEFAULT 7 CHECK (due_soon_days BETWEEN 1 AND 90),
  reminder_tones              TEXT NOT NULL DEFAULT 'FRIENDLY,FIRM,FINAL',
  reminder_cooldown_hours     INTEGER NOT NULL DEFAULT 48 CHECK (reminder_cooldown_hours BETWEEN 0 AND 720),
  show_credits_default        BOOLEAN NOT NULL DEFAULT FALSE,
  default_branch_scope        TEXT NOT NULL DEFAULT '',
  workspace_split_pct         INTEGER NOT NULL DEFAULT 58 CHECK (workspace_split_pct BETWEEN 20 AND 80),
  workspace_arrangement       TEXT NOT NULL DEFAULT 'PRIMARY_LEFT'
                                CHECK (workspace_arrangement IN ('PRIMARY_LEFT','PRIMARY_RIGHT')),
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The dealer default row, seeded so the restrictive default is VISIBLE in the data (the 0043/0173
-- posture) rather than only implied by a column default nobody can query.
INSERT INTO ar_invoice_policy (branch_id) VALUES (NULL)
ON CONFLICT DO NOTHING;

COMMENT ON TABLE ar_invoice_policy IS
  'DIB-510: dealer policy for the ar.invoices surface. NULL branch_id row = dealer default; branch row overrides (0043 idiom). Security-bearing knobs stay typed where the marshal layer reads them (D-105).';
COMMENT ON COLUMN ar_invoice_policy.account_position_visibility IS
  'Which staff tiers receive the ACCOUNT position (open balance / credit limit / available credit) on register + detail payloads. Marshal-gated byte-absence, fail closed. The invoice''s own figures are never gated — the customer holds that document.';

-- ── the presentation half: `ar.invoices` in the 0172 generic substrate ─────────────────────────
-- Item ids are FRONTEND REGISTRY ids (app/src/ar-invoice-surface.ts); the server validates SHAPE
-- only (the 0149/0164 ruling). An id this seed names that the registry does not know renders nothing,
-- silently, and the registry test is the drift gate. Ids are ADD-ONLY: renaming orphans curation.
--
-- Every profile opens on the same set, with two deliberate differences from "all on":
--   · `col.salesperson` ships hidden for every profile — it is a real column a sales-led dealer will
--     want and a distraction for a controller working month-end. Curate it on; no migration needed.
--   · the account-position column ships VISIBLE for every profile precisely BECAUSE it is server
--     gated: a curated-visible column whose data the marshal layer refuses renders empty, and that is
--     the honest shape (0172's own note about the cost columns, pointed the other way — there the
--     column was hidden AND gated; here the dealer's default is to offer it and let the server decide,
--     because a manager seeing it is the common case).
INSERT INTO surface_role_items (surface_id, profile, item_kind, item_id, position, visible)
SELECT 'ar.invoices', p.profile, i.item_kind, i.item_id, i.position, i.visible
FROM (VALUES ('counter'), ('yard'), ('driver'), ('manager'), ('admin')) AS p (profile)
CROSS JOIN (VALUES
    -- panels. register + detail are the minimum set; proof/payment/activity are curatable off for a
    -- dealer whose invoices never carry a delivery (a pure counter shop).
    ('panel',  'register',            0, true),
    ('panel',  'detail',              1, true),
    ('panel',  'proof',               2, true),
    ('panel',  'payment',             3, true),
    ('panel',  'activity',            4, true),
    -- the register table's columns, in the concept's order
    ('column', 'col.select',          0, true),
    ('column', 'col.invoice_no',      1, true),
    ('column', 'col.customer',        2, true),
    ('column', 'col.order_po',        3, true),
    ('column', 'col.issued',          4, true),
    ('column', 'col.due',             5, true),
    ('column', 'col.amount',          6, true),
    ('column', 'col.balance',         7, true),
    ('column', 'col.status',          8, true),
    ('column', 'col.row_actions',     9, true),
    ('column', 'col.salesperson',    10, false), -- optional class: curate on
    ('column', 'col.branch',         11, false), -- optional class: curate on
    -- the count-bearing filter chips, in the concept's order
    ('chip',   'chip.open',           0, true),
    ('chip',   'chip.due_soon',       1, true),
    ('chip',   'chip.overdue',        2, true),
    ('chip',   'chip.disputed',       3, true),
    ('chip',   'chip.paid',           4, true),
    -- the actions: bulk bar first, then the detail panel's own
    ('action', 'act.send_reminder',   0, true),
    ('action', 'act.create_statement',1, true),
    ('action', 'act.record_payment',  2, true),
    ('action', 'act.export',          3, true),
    ('action', 'act.select_overdue',  4, true),
    ('action', 'act.preview_pdf',     5, true),
    ('action', 'act.print',           6, true),
    ('action', 'act.send_invoice',    7, true),
    ('action', 'act.email_receipt',   8, true),
    ('action', 'act.apply_credit',    9, true),
    ('action', 'act.credit_note',    10, true),
    ('action', 'act.promise_to_pay', 11, true),
    ('action', 'act.mark_disputed',  12, true)
) AS i (item_kind, item_id, position, visible)
ON CONFLICT (surface_id, profile, item_kind, item_id) DO NOTHING;
