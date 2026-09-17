-- 0197 — the standardized outbound purchase order: the vendor's ordering desk, the vendor's own
-- catalogue vocabulary, the record of what we sent them, and the first cost gate this module has.
--
-- ── WHAT WAS ACTUALLY MISSING, MEASURED ON THIS TREE ────────────────────────────────────────────
--
-- A purchase order here has been a screen, not a document. Four facts, each verified before a line
-- of this file was written:
--
--   1. NOBODY IS TAGGED. There is no vendor_contacts table anywhere in this schema. `vendors` (0011)
--      carries vendor_no, name, terms, an is_edi flag and two free-form JSONB blobs; not one column
--      is a person. So "send the order" has had no destination to resolve, and the office sends it
--      from their own mail client, off a screenshot.
--   2. THE VENDOR'S VOCABULARY IS ABSENT. There is no vendor SKU table and no vendor UOM column in
--      the program. `purchasing_po_lines` carries qty_ordered_base + uom, and uom defaults to the
--      PRODUCT's base_uom. Our order therefore speaks entirely in our catalogue: our code, our unit.
--      The vendor's order desk retypes it, and a retyped order is a wrong order eventually.
--   3. NOTHING RECORDS A SEND. There is no outbound row for a purchase order in any table.
--   4. THERE IS NO COST GATE. purchasing_po_lines.unit_cost_cents reaches a client through
--      GET /api/v1/purchasing/pos/{id}, which is RequireStaff — every frontline role in the
--      building can read what we pay for stone. Margin and quote cost have been gated at the
--      marshal layer for three trains (0170's cost_visibility vocabulary); PURCHASE cost never was.
--
-- ── THE CONTACT IS ONE ROW PER VENDOR, AND THAT IS A COPY OF 0183 ───────────────────────────────
--
-- ar_ap_contacts (0183) is one row per customer BY PRIMARY KEY, and its header states the reason in
-- one sentence: "Modelling this as a list would recreate the blast the client asked us to stop, one
-- INSERT at a time." The buy side has the same failure with a different victim. A purchase order
-- mailed to every address on a vendor record reaches their warehouse, their accounts desk and a rep
-- who left in 2023; three people believe they own it, two of them act, and the yard takes two
-- trucks. So purchasing_vendor_contacts is one row per vendor by primary key, the resolver returns
-- ONE recipient or an error, and there is no code path in the module that can express a list.
--
-- THE ALTERNATIVE WE REJECTED, since it is the obvious one: a contacts table keyed
-- (vendor_id, role) so a dealer could tag an orders desk AND a returns desk AND a rep. That is a
-- list wearing a taxonomy. The moment two rows can match one send, something has to choose between
-- them, and every implementation of that choice is a guess. When a second purpose genuinely arrives
-- it gets its own single-row table with its own name, and the choice stays impossible.
--
-- ── recipient_kind IS STORED, NEVER DERIVED ─────────────────────────────────────────────────────
--
-- 0183:136-139, verbatim in its reasoning: the answer to "did we blast this?" is a column, because
-- the contact can be tagged tomorrow and the order that already went out must not change its story
-- when it is. A send that reads AP_CONTACT today and FALLBACK next week — or the reverse — is an
-- audit trail that rewrites itself, which is worse than no audit trail because people trust it.
--
-- ── WHY THE VENDOR SKU TABLE IS NOT A COLUMN ON purchasing_po_lines ─────────────────────────────
--
-- Because the mapping belongs to the VENDOR-PRODUCT pair and outlives any one order. Stamping the
-- vendor's code onto a line at authoring time would freeze it: the vendor renumbers their catalogue
-- in January, and every open PO keeps quoting a code their system no longer knows. It would also be
-- unmaintainable — correcting a typo'd vendor code would mean an UPDATE across every line that ever
-- carried it. One row per (vendor, product) is corrected once and every document re-renders right.
--
-- The cost of that choice is honest and is paid at render time: an order placed before a mapping
-- existed prints the mapping it has TODAY, not the one it had when it was placed. For a code the
-- vendor uses to find a pallet that is the correct trade-off; for money it would not be, which is
-- why cost is not in this table.
--
-- ── WHY THE UNIT AND THE FACTOR ARE ALL-OR-NOTHING ──────────────────────────────────────────────
--
-- A vendor unit with no conversion factor names a unit nothing can be counted in; a factor with no
-- unit is a number with no dimension. Either alone renders a line the receiving clerk cannot check
-- against the skid in front of them, so the CHECK makes the pair inseparable and the service refuses
-- the half-filled form in words before the CHECK could 500.
--
-- ── SAFETY ──────────────────────────────────────────────────────────────────────────────────────
--
-- Four CREATE TABLE IF NOT EXISTS, three CREATE INDEX IF NOT EXISTS, one seeded default row behind a
-- NOT EXISTS guard, and nothing else. No DROP, no RENAME, no UPDATE of any existing row, no change
-- to any shipped column. Staging is never wiped, so every statement here is additive by
-- construction: this applies clean from zero, applies onto a live database at 0191, and running the
-- FILE itself twice by hand changes nothing (the runner records the version and never replays it,
-- but a file that is only idempotent because nothing replays it is not idempotent).
-- Reverting costs four DROP TABLEs and the loss of the tagged contacts and mappings.

-- ── purchasing_policy — the cost gate, and the untagged-send posture ────────────────────────────
--
-- TYPED COLUMNS, NOT THE GENERIC SUBSTRATE, and 0183's D-105 split says why: a knob that gates a
-- MONEY CONVERSATION belongs in a column with a CHECK on it. Both knobs here do. cost_visibility
-- decides who in the building learns the buy price of stone — the single most commercially
-- sensitive number a supply yard holds. untagged_fallback decides whether an order may leave the
-- building addressed to somebody nobody tagged.
--
-- The branch_id UNIQUE NULL shape is the 0043/0173/0183 idiom, unchanged: the NULL row IS the
-- dealer default and a branch row overrides it. It is seeded here so the restrictive default is
-- VISIBLE in the data rather than implied by a Go constant.
CREATE TABLE IF NOT EXISTS purchasing_policy (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id        UUID UNIQUE REFERENCES locations(id) ON DELETE CASCADE, -- NULL = dealer default

  -- WHO SEES WHAT WE PAY. The 0170 three-value vocabulary, shared with quotes and the counter
  -- through internal/settings.ValidateVisibility so the three cannot drift on the words:
  --   MANAGER_UP  Tier-1/Tier-2 only. The shipped default, and the restrictive one.
  --   ALL_STAFF   every authenticated staff member — a small dealer where the counter buys too.
  --   HIDDEN      nobody, through this surface. A dealer who reads cost in the GL and nowhere else.
  -- There is deliberately no 'CUSTOM' value: a per-role matrix is a permissions system, and this
  -- program already has one (identity tiers). A second one keyed by column would answer the same
  -- question differently on one screen, which is how a product teaches people to distrust it.
  cost_visibility  TEXT NOT NULL DEFAULT 'MANAGER_UP'
                     CHECK (cost_visibility IN ('MANAGER_UP', 'ALL_STAFF', 'HIDDEN')),

  -- WHAT A SEND DOES WHEN NOBODY TAGGED AN ORDERING CONTACT. 'CONFIRM' = allowed, but only as a
  -- named, recorded fallback the operator explicitly chooses; 'REFUSE' = refused in words until
  -- somebody tags a contact. 0183's column, 0183's values, and 0183's omission: there is no 'BLAST'
  -- value, because fanning a purchase order across every address on a vendor record is the exact
  -- behaviour this column exists to prevent, so it is not spellable.
  untagged_fallback TEXT NOT NULL DEFAULT 'CONFIRM'
                     CHECK (untagged_fallback IN ('CONFIRM', 'REFUSE')),

  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The dealer default row. Restrictive on both knobs, seeded so a reader of the database can see the
-- posture without reading Go: managers only, and an untagged send must be confirmed by a person.
--
-- THE GUARD IS `NOT EXISTS` AND NOT `ON CONFLICT`, and the difference is not cosmetic: the UNIQUE on
-- branch_id does not treat two NULLs as equal, so `ON CONFLICT (branch_id) DO NOTHING` inserts a
-- SECOND dealer default every time this file is executed. The runner records the version and never
-- replays it, so that would never have fired in a deployment — which is exactly why it is the kind
-- of thing that survives to bite whoever runs the file by hand.
INSERT INTO purchasing_policy (branch_id)
SELECT NULL
 WHERE NOT EXISTS (SELECT 1 FROM purchasing_policy WHERE branch_id IS NULL);

COMMENT ON TABLE purchasing_policy IS
  'Purchasing policy: who may see purchase cost (0170 vocabulary, resolved through internal/settings), and what an untagged send does. NULL branch_id row = dealer default; branch row overrides (0043 idiom).';
COMMENT ON COLUMN purchasing_policy.cost_visibility IS
  'MANAGER_UP | ALL_STAFF | HIDDEN. Refused callers get cost BYTE-ABSENT from every PO payload and from the PO document — not zeroed, not null, absent.';
COMMENT ON COLUMN purchasing_policy.untagged_fallback IS
  'What a send does when no ordering contact is tagged. CONFIRM = allowed as a named, recorded fallback the operator chooses; REFUSE = refused in words. A silent blast is not spellable.';

-- ── purchasing_vendor_contacts — the ordering desk an order is AIMED at ─────────────────────────
--
-- ONE ROW PER VENDOR, and the primary key says so. See the header: a list here is the blast.
--
-- Either channel alone is enough (the CHECK), for 0183's reason held up against the buy side and
-- kept: a real ordering desk is often a phone number on a card. Refusing to record that would push
-- the buyer back to the vendor record's free-form blobs, which is where the blast comes from. This
-- build sends on EMAIL only (purchasing_po_sends.channel), so a phone-only contact is a contact we
-- can hold and cannot mail — and the resolver says exactly that rather than silently skipping it.
CREATE TABLE IF NOT EXISTS purchasing_vendor_contacts (
  vendor_id    UUID PRIMARY KEY REFERENCES vendors(id) ON DELETE CASCADE,
  contact_name TEXT NOT NULL DEFAULT ''  CHECK (char_length(contact_name) <= 120),
  email        TEXT NOT NULL DEFAULT ''  CHECK (char_length(email) <= 254),
  phone        TEXT NOT NULL DEFAULT ''  CHECK (char_length(phone) <= 32),
  tagged_by    UUID REFERENCES users(id),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ck_purchasing_vendor_contact_reachable CHECK (email <> '' OR phone <> '')
);

COMMENT ON TABLE purchasing_vendor_contacts IS
  'The designated ordering contact for one vendor. A purchase order goes HERE, not to every address on the vendor record. One row per vendor by primary key — a list would be the blast (0183 idiom).';

-- ── purchasing_vendor_skus — the vendor's own catalogue vocabulary ──────────────────────────────
--
-- One row per (vendor, product). Everything in it is OPTIONAL and every absence degrades to our own
-- catalogue in the open: no vendor code prints our SKU, no vendor unit prints the base quantity in
-- the base unit, and the document names which of the two it used on every line. A blank we quietly
-- filled with our own value would be a document that looks translated and is not.
CREATE TABLE IF NOT EXISTS purchasing_vendor_skus (
  vendor_id          UUID NOT NULL REFERENCES vendors(id) ON DELETE CASCADE,
  product_id         UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,

  -- The code the VENDOR's order desk types. Scrubname0011 text on purpose: it is their identifier, not ours,
  -- and imposing a shape on somebody else's catalogue is how an import starts dropping rows.
  vendor_sku         TEXT NOT NULL DEFAULT '' CHECK (char_length(vendor_sku) <= 64),

  -- The unit the vendor SELLS in, and how many of OUR base units one of them holds. The pair is
  -- all-or-nothing (the CHECK below): a unit with no factor cannot be counted and a factor with no
  -- unit names nothing. 1 SKID = 60 PC is stored as ('SKID', 60).
  vendor_uom         CITEXT REFERENCES uoms(code) ON DELETE RESTRICT,
  qty_per_vendor_uom DECIMAL(19,4) CHECK (qty_per_vendor_uom > 0),

  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (vendor_id, product_id),
  CONSTRAINT ck_purchasing_vendor_uom_pair CHECK ((vendor_uom IS NULL) = (qty_per_vendor_uom IS NULL))
);

CREATE INDEX IF NOT EXISTS idx_purchasing_vendor_skus_product
  ON purchasing_vendor_skus(product_id);

COMMENT ON TABLE purchasing_vendor_skus IS
  'The vendor''s own code and selling unit for one of our products. Absent or partial rows degrade to our SKU and our base unit, and the rendered PO names which vocabulary each line used.';
COMMENT ON COLUMN purchasing_vendor_skus.qty_per_vendor_uom IS
  'How many BASE units one vendor unit holds (1 SKID = 60 PC → 60). A line whose base quantity is not a whole multiple of this is NOT expressed in the vendor unit: 1.6667 skids invites the vendor to guess.';

-- ── purchasing_po_sends — one row per send, and the durable half ────────────────────────────────
--
-- THE ROW IS WRITTEN BEFORE THE TRANSPORT IS CALLED (ar_reminders.go:418-423, same order, same
-- reason): an order that left the building with no row behind it is a commitment this business has
-- no record of, which is strictly worse than a row for a send that failed — the row can say FAILED,
-- and on this deployment it always does.
--
-- NOTHING IN THIS BUILD DELIVERS EMAIL. notify.NewLogNotifier is the wired notifier and its Live()
-- is false. So the honest posture is not a stub that reports DELIVERED for the convenience of tests
-- (0183's ar_reminder_sends chose that, with its own reason: its tracking, cadence and black-hole
-- rules all key off DELIVERED and would be untestable otherwise). Nothing here keys off delivery, so
-- an unwired send lands FAILED with 'no transport is wired on this server' in `detail`, and the
-- surface says so in words. A purchase order that a buyer BELIEVES was sent is how a yard waits
-- three weeks for a truck nobody ordered.
CREATE TABLE IF NOT EXISTS purchasing_po_sends (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  po_id           UUID NOT NULL REFERENCES purchasing_pos(id) ON DELETE CASCADE,

  -- EMAIL only today. The single-value CHECK is deliberate rather than a column omitted: 0011 ships
  -- vendors.is_edi, so the second channel is already named in this schema and is a widening of this
  -- CHECK when it lands — not a backfill of a fact nobody recorded.
  channel         TEXT NOT NULL DEFAULT 'EMAIL' CHECK (channel IN ('EMAIL')),

  -- WHO IT WENT TO, and whether that was the tagged desk. STORED, never derived — see the header.
  recipient       TEXT NOT NULL CHECK (char_length(recipient) BETWEEN 1 AND 254),
  recipient_kind  TEXT NOT NULL CHECK (recipient_kind IN ('VENDOR_CONTACT', 'FALLBACK')),
  fallback_reason TEXT NOT NULL DEFAULT '' CHECK (char_length(fallback_reason) <= 200),

  status          TEXT NOT NULL DEFAULT 'QUEUED'
                    CHECK (status IN ('QUEUED', 'DELIVERED', 'FAILED')),
  provider        TEXT NOT NULL DEFAULT 'unwired' CHECK (provider ~ '^[a-z][a-z0-9_-]{0,31}$'),
  provider_ref    TEXT NOT NULL DEFAULT '' CHECK (char_length(provider_ref) <= 128),
  detail          TEXT NOT NULL DEFAULT '' CHECK (char_length(detail) <= 300),

  -- WHAT WENT WITH IT. The document is regenerated from the PO on every render, so these two record
  -- the SHAPE of what was attached (a name a person can ask the vendor about, and a byte count that
  -- says an attachment existed at all) without duplicating the order into a second copy that could
  -- disagree with the first. A frozen PDF blob was the alternative and was rejected: this program
  -- has no PDF toolchain, and a frozen HTML copy would be a second source for the one document the
  -- acceptance clause requires to have exactly one.
  document_name   TEXT NOT NULL DEFAULT '' CHECK (char_length(document_name) <= 120),
  document_bytes  INTEGER NOT NULL DEFAULT 0 CHECK (document_bytes >= 0),

  -- One-time idempotency key (the house rule): an identical retry reuses its key and its row, so a
  -- double-clicked "Send order" is one order, not two.
  idempotency_key TEXT NOT NULL DEFAULT '' CHECK (char_length(idempotency_key) <= 80),

  sent_by         UUID REFERENCES users(id),
  sent_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_purchasing_po_sends_po
  ON purchasing_po_sends(po_id, sent_at DESC);

-- The idempotency index is PARTIAL so the empty default (a send made with no key) never collides
-- with another; only a real key is unique.
CREATE UNIQUE INDEX IF NOT EXISTS idx_purchasing_po_sends_key
  ON purchasing_po_sends(idempotency_key) WHERE idempotency_key <> '';

COMMENT ON TABLE purchasing_po_sends IS
  'One row per outbound purchase order. Written BEFORE the transport call and marked FAILED when it does not deliver — this build wires no transport, so every row here says so in `detail`.';
COMMENT ON COLUMN purchasing_po_sends.recipient_kind IS
  'VENDOR_CONTACT | FALLBACK. A stored column, never derived: tagging a contact tomorrow must not rewrite the story of an order that already went out (0183:136-139).';
