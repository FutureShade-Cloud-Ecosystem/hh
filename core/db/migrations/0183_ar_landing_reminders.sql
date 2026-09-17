-- 0183 — the AR LANDING half: the aging cards over the shipped register (DIB-510), the reminder
-- machinery behind them, the paper-path black-hole flag, and the AP contact a send is aimed at.
--
-- ── WHY THIS SPLITS THE WAY IT DOES (D-105 hybrid) ─────────────────────────────────────────────
--
-- Two substrates, and the split is by WHAT THE KNOB BEARS, not by what is convenient here:
--
--   TYPED POLICY COLUMNS (this file's ar_landing_policy). The bucket edges, the reminder cadence
--   and the black-hole window all gate a MONEY CONVERSATION. A dealer who moves the escalation edge
--   from 90 to 75 days changes who gets chased, who gets held, and what a customer is told on the
--   phone. That belongs in a column with a CHECK on it, resolved through the shared settings kit
--   (backend/internal/settings), validated in words BEFORE a CHECK could 500 — the 0043/0173 shape:
--   branch_id UUID UNIQUE NULL where the NULL row IS the dealer default and a branch row overrides.
--
--   PRESENTATION CURATION (the surface_role_items seed at the foot of this file). WHICH cards the
--   landing shows and IN WHAT ORDER is a display preference. It carries no authority and moves no
--   money, so it is content-free strings + positions in the 0172 generic substrate — the 0140/0149
--   dashboard idiom, reused rather than re-invented. No new curation table exists in this file, and
--   that is the point: a second curation model for the same user on the same screen is how a product
--   teaches people that nothing quite works the way they expect (0149's ruling, verbatim).
--
-- ── WHAT THE TRACKING TABLES ARE FOR ───────────────────────────────────────────────────────────
--
-- The client's defect, in their words: customers who never open anything digital are invisible until
-- they are 90 days late. The cure is not a better reminder — it is a DURABLE RECORD of whether the
-- reminders were opened, so "we have texted this account four times and nobody has ever opened one"
-- is a fact the software can raise at day 20 instead of a discovery someone makes at day 90.
--
-- So the send row is the durable part, not the provider call. ar_reminder_sends survives a provider
-- swap, a credential rotation and a vendor change, because it records what WE decided and what came
-- back — never a vendor's object. There is no vendor column shape anywhere in this file.

-- ── ar_landing_policy — the typed, CHECK-constrained half ───────────────────────────────────────
CREATE TABLE ar_landing_policy (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id              UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE, -- NULL = dealer default

  -- THE BUCKET EDGES. Days PAST DUE, not days since invoice: "0 to 60 to 90, 90 plus" is measured
  -- from the due date because that is the number the customer argues about on the phone.
  --   current      not yet past due
  --   1 .. warn    aging_warn_days
  --   warn+1 .. escalate   aging_escalate_days
  --   escalate+    the escalation queue
  aging_warn_days        INTEGER NOT NULL DEFAULT 60  CHECK (aging_warn_days BETWEEN 1 AND 364),
  aging_escalate_days    INTEGER NOT NULL DEFAULT 90  CHECK (aging_escalate_days BETWEEN 2 AND 365),

  -- THE REMINDER CADENCE. How long an account is left alone after a reminder, and how many a single
  -- past-due run may send before a person has to decide. Both bound a machine that texts customers
  -- about money, so both are typed and both are checked.
  reminder_cadence_days  INTEGER NOT NULL DEFAULT 7   CHECK (reminder_cadence_days BETWEEN 1 AND 90),
  reminder_max_per_cycle INTEGER NOT NULL DEFAULT 3   CHECK (reminder_max_per_cycle BETWEEN 1 AND 20),

  -- THE BLACK-HOLE WINDOW. Delivered-but-never-opened over this many days, across at least this many
  -- sends, raises the paper-path flag. Two numbers rather than one because either alone is wrong:
  -- a window with no send floor flags an account we messaged once, and a send floor with no window
  -- flags an account that stopped opening two years ago and pays by cheque today.
  black_hole_window_days INTEGER NOT NULL DEFAULT 45  CHECK (black_hole_window_days BETWEEN 7 AND 365),
  black_hole_min_sends   INTEGER NOT NULL DEFAULT 3   CHECK (black_hole_min_sends BETWEEN 1 AND 20),

  -- WHICH CHANNELS THE DEALER OFFERS. Email ships on; SMS ships OFF and stays off until a dealer
  -- opts in — the 0173 posture for send_sms_enabled, for the same reason (consent).
  reminder_email_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  reminder_sms_enabled   BOOLEAN NOT NULL DEFAULT FALSE,

  -- WHAT HAPPENS WHEN NOBODY TAGGED AN AP CONTACT. 'CONFIRM' = the send is allowed but only as a
  -- named, recorded fallback that the operator has to choose; 'REFUSE' = the send is refused in
  -- words until somebody tags a contact. There is deliberately no 'BLAST' value: fanning a
  -- collections notice out to every address on an account is the behaviour this column exists to
  -- prevent, so it is not spellable.
  untagged_fallback      TEXT NOT NULL DEFAULT 'CONFIRM'
                           CHECK (untagged_fallback IN ('CONFIRM', 'REFUSE')),

  -- THE TIER. Basic AR ships native; accelerated/advanced AR is paid. This column is the ONLY
  -- statement of that boundary — there is no second surface, no second route table and no second
  -- component. The server reads it, resolves a capability, and omits the advanced payload for a
  -- caller who does not hold it (byte-absence, internal/billing/ar_landing.go).
  ar_tier                TEXT NOT NULL DEFAULT 'BASE' CHECK (ar_tier IN ('BASE', 'PRO')),

  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),

  -- The edges must stay in order or every bucket below is nonsense. The service refuses this in
  -- words first (422); the CHECK is the guarantee that nothing else can write it.
  CONSTRAINT ck_ar_landing_policy_edges CHECK (aging_warn_days < aging_escalate_days)
);

-- The dealer default row, seeded so the restrictive default is VISIBLE in the data (0043/0173).
-- BASE, SMS off, CONFIRM fallback: nothing about merging this migration changes what anybody is
-- charged for or who gets texted.
INSERT INTO ar_landing_policy (branch_id) VALUES (NULL);

COMMENT ON TABLE ar_landing_policy IS
  'AR landing policy: aging bucket edges, reminder cadence and the black-hole window (money-gating knobs, typed per D-105), plus the BASE/PRO tier the advanced-payload capability resolves from. NULL branch_id row = dealer default; branch row overrides (0043 idiom).';
COMMENT ON COLUMN ar_landing_policy.ar_tier IS
  'BASE | PRO. PRO grants the ar.advanced capability: the escalation WORK QUEUE and the reminder engagement payload. Withheld payloads are byte-absent, never CSS-hidden.';
COMMENT ON COLUMN ar_landing_policy.untagged_fallback IS
  'What a send does when no AP contact is tagged. CONFIRM = allowed as a named, recorded fallback the operator chooses; REFUSE = refused in words. A silent blast is not spellable.';

-- ── ar_ap_contacts — the tagged AP contact a send is AIMED at ───────────────────────────────────
--
-- ONE ROW PER ACCOUNT, and the primary key says so. A collections notice has exactly one right
-- recipient: the person who pays the bills. Modelling this as a list would recreate the blast the
-- client asked us to stop, one INSERT at a time.
--
-- Either channel alone is enough (the CHECK), because a real AP contact is often a phone number on
-- a card and nothing else — refusing to record that would push the operator straight back to the
-- account's general address book, which is where the blast comes from.
CREATE TABLE ar_ap_contacts (
  customer_id  UUID PRIMARY KEY REFERENCES customers(id) ON DELETE CASCADE,
  contact_name TEXT NOT NULL DEFAULT ''  CHECK (char_length(contact_name) <= 120),
  email        TEXT NOT NULL DEFAULT ''  CHECK (char_length(email) <= 254),
  phone        TEXT NOT NULL DEFAULT ''  CHECK (char_length(phone) <= 32),
  tagged_by    UUID REFERENCES users(id),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ck_ar_ap_contact_reachable CHECK (email <> '' OR phone <> '')
);

COMMENT ON TABLE ar_ap_contacts IS
  'The designated accounts-payable contact for one account. A reminder goes HERE, not to every address and number on the account. One row per customer by primary key — a list would be the blast again.';

-- ── ar_reminder_sends — one row per send, and the durable half of the tracking ──────────────────
--
-- PROVIDER-AGNOSTIC BY CONSTRUCTION. `provider` is a free lowercase name and `provider_ref` is an
-- opaque string; no column here is shaped by any vendor's API. Swapping the transport changes two
-- strings in these rows and nothing else in the schema, which is the whole point of the seam.
--
-- track_token is what makes open and click OBSERVABLE. It is unique, unguessable, and the only
-- thing the public tracking endpoints accept — no customer id, no invoice id and no send id ever
-- appears in a URL that leaves the building.
CREATE TABLE ar_reminder_sends (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id     UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  invoice_id      UUID REFERENCES invoices(id) ON DELETE SET NULL,

  channel         TEXT NOT NULL CHECK (channel IN ('EMAIL', 'SMS')),

  -- WHO IT ACTUALLY WENT TO, and whether that was the tagged contact. recipient_kind is the audit
  -- answer to "did we blast this account?" — and it is a column rather than a derivation because
  -- the AP contact can be tagged tomorrow and the send that already went out must not change its
  -- story when it is.
  recipient       TEXT NOT NULL CHECK (char_length(recipient) BETWEEN 1 AND 254),
  recipient_kind  TEXT NOT NULL CHECK (recipient_kind IN ('AP_CONTACT', 'FALLBACK')),
  fallback_reason TEXT NOT NULL DEFAULT '' CHECK (char_length(fallback_reason) <= 200),

  bucket          TEXT NOT NULL CHECK (bucket IN ('CURRENT', 'WARN', 'LATE', 'ESCALATION')),

  -- THE PAST-DUE WATERMARK. A resend is visibly a resend: the flag drives the stamp on the rendered
  -- invoice, and `watermark` records the exact words that were stamped on THAT send. Recording the
  -- text rather than re-deriving it means a dealer who changes the wording next quarter does not
  -- silently rewrite what a customer was already shown.
  resend          BOOLEAN NOT NULL DEFAULT FALSE,
  watermark       TEXT NOT NULL DEFAULT '' CHECK (char_length(watermark) <= 40),

  status          TEXT NOT NULL DEFAULT 'QUEUED'
                    CHECK (status IN ('QUEUED', 'DELIVERED', 'FAILED')),
  provider        TEXT NOT NULL DEFAULT 'stub' CHECK (provider ~ '^[a-z][a-z0-9_-]{0,31}$'),
  provider_ref    TEXT NOT NULL DEFAULT '' CHECK (char_length(provider_ref) <= 128),

  track_token     TEXT NOT NULL UNIQUE CHECK (track_token ~ '^[A-Za-z0-9_-]{16,64}$'),

  -- One-time idempotency key on the mutation (the house rule): an identical retry reuses its key
  -- and reuses its row, so a double-clicked "Send reminder" is one message, not two.
  idempotency_key TEXT NOT NULL DEFAULT '' CHECK (char_length(idempotency_key) <= 80),

  sent_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  delivered_at    TIMESTAMPTZ,
  -- FIRST open/click are separate columns from the counts on purpose: "did this ever land" and "how
  -- often was it read" are different questions, and the black-hole rule only ever asks the first.
  first_open_at   TIMESTAMPTZ,
  first_click_at  TIMESTAMPTZ,
  open_count      INTEGER NOT NULL DEFAULT 0 CHECK (open_count >= 0),
  click_count     INTEGER NOT NULL DEFAULT 0 CHECK (click_count >= 0)
);

CREATE UNIQUE INDEX ux_ar_reminder_sends_idem
    ON ar_reminder_sends (idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX ar_reminder_sends_customer_idx ON ar_reminder_sends (customer_id, sent_at DESC);
CREATE INDEX ar_reminder_sends_open_idx     ON ar_reminder_sends (customer_id, status, first_open_at);

COMMENT ON TABLE ar_reminder_sends IS
  'One AR reminder send. The DURABLE half of open/click tracking: it records what we decided and what came back, never a vendor object, so a provider swap changes two strings and no schema.';

-- ── ar_reminder_events — every open and every click, appended ───────────────────────────────────
--
-- The counters on the send row are the fast answer; this table is the honest one. They are kept
-- together rather than derived because the counters are read on every landing render and the events
-- are read when somebody asks "when exactly?" — and because an append-only log is the only form of
-- this record that a later bug cannot quietly rewrite.
CREATE TABLE ar_reminder_events (
  id          BIGSERIAL PRIMARY KEY,
  send_id     UUID NOT NULL REFERENCES ar_reminder_sends(id) ON DELETE CASCADE,
  event       TEXT NOT NULL CHECK (event IN ('DELIVERED', 'OPEN', 'CLICK', 'BOUNCE', 'FAILED')),
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  user_agent  TEXT NOT NULL DEFAULT '' CHECK (char_length(user_agent) <= 256),
  detail      TEXT NOT NULL DEFAULT '' CHECK (char_length(detail) <= 256)
);

CREATE INDEX ar_reminder_events_send_idx ON ar_reminder_events (send_id, occurred_at);

-- ── ar_paper_path_flags — the black hole, made visible ──────────────────────────────────────────
--
-- THE FLAG IS DURABLE, NOT DERIVED, and that is the requirement rather than an implementation taste.
-- A derived flag disappears the moment somebody sends one more reminder or the window rolls, taking
-- the decision with it. What the client asked for is that SOMEBODY DELIBERATELY MOVES THE ACCOUNT TO
-- PAPER — a decision with a person's name on it — so the flag has to be a thing that can be raised,
-- looked at, acted on, and closed with a reason.
--
-- The partial unique index is the idempotency: the detector can run every night and an account that
-- is already flagged does not accumulate a second one.
CREATE TABLE ar_paper_path_flags (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id     UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  status          TEXT NOT NULL DEFAULT 'OPEN'
                    CHECK (status IN ('OPEN', 'MOVED_TO_PAPER', 'DISMISSED')),
  -- The evidence, frozen at the moment of raising. Re-deriving it later would answer a different
  -- question, because the window will have moved.
  delivered_sends INTEGER NOT NULL CHECK (delivered_sends >= 0),
  window_days     INTEGER NOT NULL CHECK (window_days > 0),
  first_send_at   TIMESTAMPTZ,
  last_send_at    TIMESTAMPTZ,
  raised_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at     TIMESTAMPTZ,
  resolved_by     UUID REFERENCES users(id),
  resolution_note TEXT NOT NULL DEFAULT '' CHECK (char_length(resolution_note) <= 300)
);

CREATE UNIQUE INDEX ux_ar_paper_path_one_open
    ON ar_paper_path_flags (customer_id) WHERE status = 'OPEN';

COMMENT ON TABLE ar_paper_path_flags IS
  'Accounts whose reminders are delivered and never opened across the policy window. Durable, not derived: the point is a deliberate move to paper with a person attached, and a derived flag would vanish the moment one more reminder went out.';

-- ── presentation curation: the landing cards, in the 0172 substrate ─────────────────────────────
--
-- Card ids belong to app/src/ar-landing-surface.ts (the 0149/0164 ruling: the registry is the front
-- end's and only the front end's). The server stores these strings and validates SHAPE only; an id
-- this build's registry does not know renders nothing, silently, and ar-landing-surface.test.mjs
-- reads THIS FILE and asserts every id below resolves — that test is the drift gate.
--
-- ORDER IS THE ARGUMENT. Every profile opens on the same order and it is not the aging report's
-- order: the escalation queue is FIRST. The aging report reads left to right from healthy to sick
-- because it is a report; a landing is a place someone starts work, and the work is at the sick end.
-- Current is last and is still there, because "how much is not yet a problem" is the number that
-- tells you whether the sick end is the whole story.
--
-- The paper-path card ships VISIBLE for manager and admin and HIDDEN for the three profiles that
-- cannot act on it — a counter clerk cannot decide to post an account's invoices, so a card asking
-- them to would be a decision they have to route rather than make. It is one curation row away for
-- any dealer who disagrees, which is exactly what this substrate is for.
INSERT INTO surface_role_items (surface_id, profile, item_kind, item_id, position, visible)
SELECT 'ar.landing', p.profile, 'panel', i.item_id, i.position,
       CASE WHEN i.item_id = 'card.paper_path' AND p.profile NOT IN ('manager', 'admin')
            THEN false ELSE i.visible END
FROM (VALUES ('counter'), ('yard'), ('driver'), ('manager'), ('admin')) AS p (profile)
CROSS JOIN (VALUES
    ('card.escalation', 0, true),
    ('card.late',       1, true),
    ('card.warn',       2, true),
    ('card.paper_path', 3, true),
    ('card.current',    4, true)
) AS i (item_id, position, visible)
ON CONFLICT (surface_id, profile, item_kind, item_id) DO NOTHING;
