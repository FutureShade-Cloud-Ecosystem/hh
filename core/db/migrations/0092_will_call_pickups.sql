-- 0092: will-call QR pickup self-confirmation (DIB-79, bundle W2-A).
--
-- THE PROBLEM. A WILL_CALL order sits READY on the yard rack until someone drives in for it. The
-- pickup itself was never recorded anywhere: the counter marked the order fulfilled from memory,
-- often the next morning, and "who actually took this material" had no answer at all. That is the
-- same evidence gap the delivery POD closes — the only difference is that the customer comes to the
-- yard instead of the truck going to them.
--
-- THE SHAPE. Staff mint a pickup token when the order goes on the rack; the token is rendered as a
-- QR on the rack tag. Whoever arrives scans it, sees the order they are collecting, types their full
-- name and affirms — the SAME typed-name attestation (DIB-386, TYPED_NAME_V1) the delivery POD
-- captures, so the two pieces of evidence in this system read alike and are verified alike.
--
-- THE SECURITY MODEL IS quote_share_links, VERBATIM (0086, R4). A QR token is a CREDENTIAL, not an
-- authentication: whoever holds it is treated as the collector, because that is the only thing a
-- printed square can prove. Therefore it grants read + confirm of EXACTLY ONE order and nothing
-- else, it EXPIRES, it is REVOCABLE, only its sha256 hash is stored, the public payload carries no
-- staff-only field (no cost, no margin, no other order), and a dead token answers 410 GONE rather
-- than 404 — the holder should learn the token died, not be told the order never existed.
--
-- WHY THE EXPIRY IS PER-ROW AND NOT A CONSTANT: a rack tag's life is a property of that tag. A
-- constant would retroactively extend or kill every live token the day someone edited it.

CREATE TABLE will_call_pickups (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id     UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  branch_id    UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  -- sha256 hex of the raw token. The raw value is returned EXACTLY ONCE, at mint, to the staff
  -- member printing the tag (portal.newInviteToken / identity.newResetToken / quote share links —
  -- the three existing token primitives in this codebase all do this).
  token_hash   TEXT NOT NULL UNIQUE,
  status       TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','CONFIRMED','REVOKED')),
  expires_at   TIMESTAMPTZ NOT NULL,
  -- ── the confirmation ──
  -- Same posture as delivery_stops: the ref points at the sealed pos_signature row; the display name
  -- is kept unsealed so the counter can answer "who collected it" without decrypting evidence.
  signature_ref  TEXT NOT NULL DEFAULT '',
  collected_by   TEXT NOT NULL DEFAULT '',
  confirmed_at   TIMESTAMPTZ,
  revoked_at     TIMESTAMPTZ,
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- A CONFIRMED pickup has evidence behind it, always — the will-call half of the POD check in 0090.
  CONSTRAINT will_call_pickups_confirmed_needs_signature
    CHECK (status <> 'CONFIRMED' OR (signature_ref <> '' AND confirmed_at IS NOT NULL))
);
CREATE INDEX idx_will_call_pickups_order ON will_call_pickups (order_id, created_at DESC);
-- An order has at most ONE live (mintable, scannable) token at a time. A reprint revokes and re-mints
-- rather than leaving two valid squares on two tags in the yard.
CREATE UNIQUE INDEX uq_will_call_pickups_active ON will_call_pickups (order_id) WHERE status = 'ACTIVE';

-- Every scan and every confirm is logged against the token, exactly as quote_share_link_events does,
-- and for the same reason it is a separate table from audit_log: these are events attributable to an
-- ANONYMOUS token holder, and audit_log's actor_id shape assumes an identified staff principal.
CREATE TABLE will_call_pickup_events (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  pickup_id   UUID NOT NULL REFERENCES will_call_pickups(id) ON DELETE CASCADE,
  event       TEXT NOT NULL CHECK (event IN ('VIEW','CONFIRM')),
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_will_call_pickup_events_pickup ON will_call_pickup_events (pickup_id, occurred_at);
