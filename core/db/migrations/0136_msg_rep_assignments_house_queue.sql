-- 0136: DIB-273 + DIB-333 (rep assignment) + DIB-271 (house fallback queue).
-- Owner: internal/messaging. Wave 5 / Swarm D — Messaging Completion.
-- Follows the 0050/0052/0069/0071 conventions: identity is the SoT (FKs to users/customers/orders/
-- locations, never a private copy); UUID v4 PKs; TIMESTAMPTZ; additive and non-breaking.
--
-- ── THE RULING (DIB-273 + DIB-333), stated here so it is reviewable in one place ──
-- Two ledger candidates were open: "a rep is assigned to a CUSTOMER" and "a rep is assigned to an
-- ORDER". The ruling is:
--
--     REP ASSIGNMENT IS **CUSTOMER-LEVEL**, WITH AN OPTIONAL **PER-ORDER OVERRIDE**.
--
-- Rationale: the conversational unit a customer message belongs to is the CUSTOMER (an inbound SMS /
-- portal note arrives with a phone number or a portal login, not an order number), so the routable
-- default must be customer-level or most inbound would be unroutable. The per-order override exists
-- for SPLIT BOOKS — a shared account where one job is written by a different rep (commission splits,
-- a covering rep on a single large order). Resolution order is therefore:
--
--     order override (when the message names an order) → customer-level → NONE (house queue, DIB-271)
--
-- REVERSIBILITY (the ruling is a decision, not a lock-in): ONE table carries BOTH candidates. The
-- customer-level row is `order_id IS NULL`; the override row is `order_id IS NOT NULL`. Flipping the
-- ruling to order-primary (or to order-ONLY) needs NO migration — it is a change to the resolution
-- order in internal/messaging/repassign.go (resolveOrder) plus, for order-ONLY, dropping the
-- customer-level rows. Both shapes are already legal here. The two partial unique indexes below are
-- deliberately independent so neither shape constrains the other.

-- ── Rep assignments ─────────────────────────────────────────────────────────────
CREATE TABLE rep_assignments (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  -- NULL  => the customer-level assignment (the routable default).
  -- SET   => the per-order override (split books). The customer_id is still carried so a lookup by
  --          customer alone sees the whole book, and so the override can never point at an order
  --          belonging to a different customer than the row claims (enforced in the service).
  order_id    UUID REFERENCES orders(id) ON DELETE CASCADE,
  rep_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- How the row came to exist. MANUAL = an admin set it in the rep-assignment UI. CLAIM = a rep
  -- claimed the conversation out of the house queue (DIB-271), which is what makes a claim STICKY:
  -- the claim IS the assignment, so the next inbound from that customer routes to the claimer's DM.
  source      TEXT NOT NULL DEFAULT 'MANUAL' CHECK (source IN ('MANUAL','CLAIM','IMPORT')),
  assigned_by UUID REFERENCES users(id) ON DELETE SET NULL,
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  note        TEXT NOT NULL DEFAULT ''
);

-- At most ONE customer-level assignment per customer (the default book).
CREATE UNIQUE INDEX uq_rep_assignments_customer ON rep_assignments(customer_id) WHERE order_id IS NULL;
-- At most ONE override per order (an order has exactly one writing rep).
CREATE UNIQUE INDEX uq_rep_assignments_order ON rep_assignments(order_id) WHERE order_id IS NOT NULL;
-- "My book" lookups (the rep-side list).
CREATE INDEX idx_rep_assignments_rep ON rep_assignments(rep_user_id);

-- ── DIB-271: the house fallback queue, one channel per branch ───────────────────
-- 0069 left this GATED with an explicit reason: "No assigned-rep column exists … a `house-<branch>`
-- channel cannot be auto-routed yet." rep_assignments above closes that gate, so the channels are
-- seeded now (they are no longer dead, unrouted channels).
--
-- A house channel is a BRANCH-scoped TEAM channel with auto_role '*': every user granted that branch
-- sees the queue, exactly like #yard-<branch> (0052). Narrowing it to one role is not possible in the
-- 0050 rule model (auto_role holds a single code) and would be wrong anyway — an unclaimed customer
-- is everyone's problem until someone claims it. CLAIMING is what narrows it (a claim writes a
-- rep_assignments row, after which that customer's inbound leaves the queue for the claimer's DM).
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role, tier) VALUES
  ('a5000000-0000-4000-9000-000000000271','TEAM','5d1b3f6e-0000-4000-9000-000000000001',
     'house-trenton','#house-trenton',
     'Unassigned customer messages — Trenton. Claim one to make it yours.','BRANCH','*','STANDARD'),
  ('a5000000-0000-4000-9000-000000000272','TEAM','5d1b3f6e-0000-4000-9000-000000000002',
     'house-kingston','#house-kingston',
     'Unassigned customer messages — Kingston. Claim one to make it yours.','BRANCH','*','STANDARD')
ON CONFLICT (id) DO NOTHING;

-- The house-queue slug for a branch is derived (house-<lower(name-after-em-dash)>) in Go
-- (repository.BranchHouseSlug) exactly as the yard slug is, so a NEW branch only needs its channel
-- row seeded with the matching slug — no code change. A branch with no house channel falls back to
-- the GLOBAL fallback below so an inbound is NEVER dropped on the floor.

-- ── Global last-resort house queue ──────────────────────────────────────────────
-- An inbound whose customer has no home branch (walk-in / portal contact with no branch) still has to
-- land somewhere a human reads. #house-unassigned is that floor: GLOBAL scope, every active staffer.
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role, tier) VALUES
  ('a5000000-0000-4000-9000-000000000270','TEAM',NULL,
     'house-unassigned','#house-unassigned',
     'Unassigned customer messages with no branch — the last-resort house queue.','GLOBAL','*','STANDARD')
ON CONFLICT (id) DO NOTHING;

-- ── Conversation ledger (what a claim claims) ──────────────────────────────────
-- One row per (customer, branch) conversation that has landed in a house queue. It is the queue's
-- READ model — "who is waiting, since when, who claimed it" — so the queue UI does not have to
-- reconstruct that by scanning messages. The ASSIGNMENT truth stays in rep_assignments (a claim
-- writes both, in one transaction): this table never decides routing, it only records the queue state.
CREATE TABLE msg_house_conversations (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id  UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  branch_id    UUID REFERENCES locations(id) ON DELETE SET NULL,
  channel_id   UUID NOT NULL REFERENCES msg_channels(id) ON DELETE CASCADE,
  first_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  message_count INTEGER NOT NULL DEFAULT 0,
  claimed_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  claimed_at   TIMESTAMPTZ
);
-- One conversation row per customer per house channel (the upsert target on each inbound).
CREATE UNIQUE INDEX uq_msg_house_conversations ON msg_house_conversations(customer_id, channel_id);
-- The queue read: unclaimed first, oldest first.
CREATE INDEX idx_msg_house_conversations_open ON msg_house_conversations(channel_id, last_at DESC)
  WHERE claimed_by IS NULL;

-- GATED (do NOT build here): rep TERRITORY rules (auto-assign by postal code / product line) and
-- round-robin house-queue distribution. Both need a territory model that does not exist; the
-- explicit-assignment + claim path above is the settled v1 and neither is blocked by this schema.
