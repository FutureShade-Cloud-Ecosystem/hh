-- 0086: quote share links + customer acceptance (DIB-57, bundle W10-A).
--
-- A share link is a CREDENTIAL, not an authentication (R4): possession of the token is the whole
-- proof, so the token grants read + accept of exactly ONE quote, expires, is revocable, and never
-- exposes a staff-only field. Only the sha256 HASH is stored — the raw token is shown once, to the
-- staff member who minted it, and is unrecoverable afterwards. That mirrors the two existing token
-- primitives in this codebase (portal/auth.go newInviteToken, identity/password_reset.go
-- newResetToken), both of which store hash-only for the same reason.

CREATE TABLE quote_share_links (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  quote_id    UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
  -- sha256 hex of the raw token. UNIQUE so a lookup is a single indexed probe and a (vanishingly
  -- improbable) collision is a constraint violation rather than an ambiguous match.
  token_hash  TEXT NOT NULL UNIQUE,
  expires_at  TIMESTAMPTZ NOT NULL,
  revoked_at  TIMESTAMPTZ,
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_quote_share_links_quote ON quote_share_links (quote_id, created_at DESC);

-- Every view and every accept against a token is logged (R4). This is deliberately a separate table
-- from audit_log: these are events attributable to an ANONYMOUS link holder, not to a staff actor, and
-- audit_log's actor_id/branch_id shape assumes an identified principal.
CREATE TABLE quote_share_link_events (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  share_link_id UUID NOT NULL REFERENCES quote_share_links(id) ON DELETE CASCADE,
  event         TEXT NOT NULL CHECK (event IN ('VIEW','ACCEPT')),
  occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_quote_share_link_events_link ON quote_share_link_events (share_link_id, occurred_at);

-- The acceptance record. This is the artifact that has to answer "what exactly did the customer agree
-- to, and how do we know" months later, possibly in a dispute.
--
-- WHY IT CARRIES NO FOREIGN KEYS (C1). It must stay meaningful even if the quote row is gone. An FK
-- with ON DELETE CASCADE would delete the evidence along with the document; ON DELETE SET NULL would
-- keep the row but null out precisely the identifiers that make it interpretable. So quote_id and
-- share_link_id are RECORDED IDENTIFIERS, not live references — the same posture audit_log.entity_id
-- takes, and for the same reason: an immutable business record must not be reshaped by the lifecycle
-- of what it describes.
--
-- For the same reason the money is SNAPSHOT here rather than read back through quote_id at display
-- time. "The customer accepted $4,812.50" must remain true even if the quote is later edited (it
-- cannot be — acceptance freezes it), converted, or deleted. A join would make the record narrate
-- whatever the quote says today.
--
-- WHAT IT IS NOT. This is an electronic ATTESTATION — a typed name plus an explicit affirmative — not
-- a captured signature. The codebase's pos_signature pad is a stub: it sets a boolean on pointerdown
-- and seals the literal string 'SIMULATED:<purpose>:<ref>'. Reusing it here would have produced an
-- artifact that implies ink nobody captured, so this surface deliberately presents no drawing
-- affordance at all (ruling, 2026-07-30; the POS-side stub is DIB-386).
CREATE TABLE quote_acceptances (
  id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  quote_id                UUID NOT NULL,   -- recorded identifier; deliberately NOT an FK (see above)
  share_link_id           UUID NOT NULL,   -- which token was used — the 4th part of the quartet
  quote_no                CITEXT NOT NULL, -- snapshot, so the record names the document without a join
  typed_name              TEXT NOT NULL,   -- what the customer typed as their full name
  accepted_subtotal_cents BIGINT NOT NULL, -- snapshot of what was agreed to
  accepted_tax_cents      BIGINT NOT NULL,
  accepted_total_cents    BIGINT NOT NULL,
  -- audit_log.seq (0078) of the QUOTE_ACCEPTED row written in the SAME transaction as this record.
  -- It is the causal anchor: it places the acceptance exactly in the quote's change trail, so "what
  -- had been edited before they agreed" is answerable rather than inferred from timestamps.
  audit_seq               BIGINT,
  accepted_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_quote_acceptances_quote ON quote_acceptances (quote_id, accepted_at DESC);
