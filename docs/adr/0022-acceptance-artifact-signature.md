# ADR-0022: The customer-quote acceptance artifact signature and token rotation

Status: Proposed

Number pre-allocated to this lane by the Phase C dispatch ruling's G3
(context/phase-c-dispatch-ruling-r1.md, gate G3); the repository's last is 0020 and
0021 belongs to HH-3.4. Drafted in HH-3 lane HH-3.6 (Phase C) for the behaviour half
of contract gap G1 (docs/sessions/HH-3.md, the HH-3.6 entry), arguing from the
reviewed design record (tools/org/outbox/backend/c-c1-acceptance-design/DESIGN.md
with the strongest-tier review's fixes F-1 to F-10 folded in,
tools/org/outbox/tech-lead/c-c1-review/DELIVERABLE.md). D2, D8, D10, D12, D23 and
D30 stand as recorded (docs/analysis/DECISIONS.md) and are not reopened.
ADR-0020's public leg stands as the governing middleware rule: no verification, no
org resolution, X-Org with zero effect, and the token is the whole authority on the
public routes. This is the security-shaped decision the dispatch ruling's Section 4
put a human checkpoint on: this ADR and the token lifecycle tests are what Colton
reads before this lane's work enters the integrated diff.

## Context

Two facts the frozen schema forces into the open:

1. The customer_quote table carries exactly one TEXT column of token state,
   share_token_hash, and no share-link table exists for it
   (core/db/migrations/0214_new_entities_and_pricing.sql, Block 8; the dealer side,
   by contrast, has per-link rows with revoked_at and a UNIQUE token_hash probe,
   core/db/migrations/0086_quote_share_links.sql). The lane's acceptance requires
   that a revised quote's old token answer 410, dead, never 404, unknown. A plain
   hash overwrite cannot do that: once the old hash is replaced, the old token is
   indistinguishable from one that never existed.
2. The quote_acceptance table records the attestation facts (typed name,
   affirmative, consent text, accepted total, the optional drawn-mark attachment
   reference) but has no column for any server attestation over them, and no
   anchor column tying an acceptance to an audit row (the dealer record carries
   both, 0086 Block by block). Nothing pins how a later verification proves the
   record was not rewritten.

The schema is frozen; a gap is an HH-3.7 stop, never an edit. This ADR therefore
decides the mechanism within the frozen shape and names what the frozen shape
costs, including the two material gaps routed to HH-3.7 by the review: no storage
for tenant_brand (finding F1) and no index serving token resolution (finding F2,
every public resolution is an unindexed scan under this design's predicates until
an index lands).

## Decision

### 1. Token rotation: the column holds the live hash plus an append-only dead list

The column overload, pinned in one sentence: this ADR redefines the content of
customer_quote.share_token_hash against the column's own name and against the
migration's comment ("token is stored only as a hash", 0214 Block 8); the value is
an encoding whose atoms are hashes, with the v1 marker inside the value, and
nothing but sha256 hex atoms is ever at rest, so the migration's recorded intent
(no raw token at rest, raw value only in the send response) holds; the encoding is
losslessly convertible to share-link rows if a migration ever opens. The at-rest
pin binds the coarse HTTP audit middleware's path capture too: the middleware
elides the token segment of every public token route's path to the fixed
"{token}" placeholder before the row persists, so no operator-readable trail
carries the credential (the serve-shaped regression core/cmd/core/
cq_auditwrap_test.go fails on any regression).

The encoding and its grammar, pinned exactly:

    v1:<live_sha256_hex>:<dead_sha256_hex>[,<dead_sha256_hex>...]

- The live part is the sha256 hex (64 lowercase hex characters) of the current raw
  token, or the sentinel "-" when no live token exists (after a revise, before the
  next send).
- The dead part is the sentinel "-", or the sha256 hexes of every retired raw
  token for this quote, appended in retirement order, deduplicated on write.
- A stored value that fails this grammar (a bare legacy hash, a truncated write, a
  duplicated dead entry, uppercase or short atoms, a missing v1 marker) matches no
  token: it never changes any response by itself, it is counted as a loud data
  defect the operator can see, and the presented token then resolves as
  never-existed if no other row matches. Parse before match, always.

Resolution of a presented raw token, in order:

1. Malformed shape (empty after trim, or not the mint's alphabet and length,
   base64url of 32 bytes): 404 with the never-existed body. Existence does not
   leak, and a malformed value never existed as a minted token.
2. Hash the token; fetch candidate rows by containment of the 64-hex hash, then
   parse each stored value and decide live or dead by exact element membership.
   Containment is exact membership here, not substring luck: every atom is exactly
   64 hex characters and every separator is non-hex, so a 64-character all-hex
   substring of a well-formed value coincides with exactly one atom; an
   ill-formed value is rejected by the parse of the previous rule before any
   membership claim.
3. Live match on a tombstoned quote (deleted_at set): 410 for every token of that
   quote, live or dead, and nothing of it renders.
4. Live match with a NULL link bound (share_expires_at): 410, fail closed, never
   an indefinite serve; it is a data defect and is counted loudly.
5. Live match past the link bound (share_expires_at): 410. Live match inside the
   bound: serve, subject to the offer-state rules for accept.
6. Dead match: 410. The holder learns the link died rather than being told the
   quote never existed.
7. More than one row matching one presented hash: a data defect, refused closed
   in the 503 family with a loud count, never a silent pick. The dealer side made
   collisions a constraint violation; this table cannot, so the refusal is the
   rule.
8. Neither: 404. The token never existed.

Rotation is immediate and append-only at two moments, both carried from the HH Pro
reference:

- Revise retires the live token the moment the intent to change forms, not at the
  eventual re-send, because from that moment the old link shows a proposal the
  contractor no longer stands behind (hh_pro_dibbits
  src/core/actions/customer-quote.ts, the revise comment and its pinned
  regression test). The live slot becomes empty. The HH Pro reference mints a
  fresh token at revise; since the raw token is only ever returned by send, a
  revise-minted token could never be delivered to anyone, so the empty live slot
  is the honest server-side equivalent, recorded here so nobody later calls it
  drift.
- Every send (first send and every re-send) mints a fresh 32-byte crypto/rand
  base64url token, retires whatever was live into the dead list, re-freezes the
  presentation lines so the homeowner sees exactly what was sent, and resets the
  view counters. The raw token appears exactly once, in the send response.

An accepted quote is never revisable, so no rotation can occur after an acceptance
and the accepted generation's artifact always names a generation that was live
when signed.

Constant-time comparison, scoped honestly: the artifact signature comparison uses
hmac.Equal and is constant time; the token resolution scan makes no timing claim
and needs none, because the digest is not secret and every early-exit difference
within the dead list collapses to one 410 body, so no generation is
distinguishable. A future implementer should neither skip hmac.Equal on the MAC
nor perform timing theater on the scan.

The dead list is not trimmed. No retention policy for retired token hashes has
been pinned by anyone, and inventing one silently would decide a second thing
nobody asked. The list is bounded in practice by one quote's revision count, and
the row lock serializes writes per quote, so a tenant can inflate only its own
row; the cross-quote cost of a wide row is scan cost, which is finding F2's to fix
with an index. A trim policy, if ever wanted, is a future recorded decision.

### 2. The acceptance artifact: HMAC-SHA256 over pinned canonical bytes, the signature on the audit row

The artifact is the canonical JSON serialization of a pinned field set, in a
pinned order, with the version string inside the signed bytes:

    v                        the string "1"
    acceptance_id            quote_acceptance.id
    quote_id                 the acceptance row's customer_quote_id
    org_id                   the acceptance row's org_id
    tenant_id                the acceptance row's tenant_id
    signed_name              the typed name as stored
    affirmative              the row's affirmative, true on every signed v1 path
    consent_text             the served constant, as stored
    accepted_total_cents     the server-computed total, as stored
    accepted_at              RFC 3339, UTC, always six fractional digits
    signature_attachment_id  the id, or the empty string when null

Canonicalization pins, so rebuild-from-row is byte-reproducible:

- The serializer is a hand-built writer whose string escaping matches Go's
  encoding/json with HTML escaping disabled: quote and backslash escaped, the
  short escapes for backspace, formfeed, newline, carriage return and tab, other
  control characters as lowercase \u00xx, U+2028 and U+2029 escaped, invalid
  UTF-8 mapped per byte to the six-character \ufffd escape. No HTML escaping anywhere, so a
  typed name containing angle brackets or an ampersand cannot fork the bytes
  between sign time and verify time. The invalid-UTF-8 rule was pinned at
  integration, when CI on one Go minor exposed that the toolchain's own
  encoding of invalid bytes differs between minors (the raw replacement rune
  versus the escape): the escape form is the writer's definition, so canonical
  bytes and signatures are toolchain independent, and a typed U+FFFD (valid
  UTF-8) passes through raw and remains distinguishable from the escape.
- accepted_at is rendered from the stored value in UTC with exactly six
  fractional digits (microsecond precision, the column's own); the signing path
  truncates to microsecond before serializing, so the signed bytes equal the
  rebuilt bytes by construction.
- The four ids are canonical UUID lowercase text; affirmative and the total are
  JSON literals; no insignificant whitespace; no additional keys; keys in the
  pinned order.
- The version field is inside the signed bytes, so a later format change can
  never be confused with a tampered v1 artifact: verification always rebuilds a
  v1 artifact from the stored columns, and any future v2 changes the field set
  and the version together.

artifact_hash is sha256 over the canonical bytes, compared by equality.
artifact_sig is HMAC-SHA256 over the same canonical bytes with the key the
recorded key id names, compared in constant time (hmac.Equal). Any mismatch,
including a row edited without the key, fails verification.

The signature lives on the audit row written on the acceptance's own
transaction. The audit seam is consumed through a narrow interface this lane
defines (write the acceptance row on the caller's transaction; load the
acceptance row by identity): the audit module itself is not in this lane's base
and arrives with Lane A's carry, so the concrete wiring is an integration-time
re-point against the seam, recorded here so nobody builds the seam twice.

The acceptance transaction also carries the seq allocation the design's write
path pins: the quote_acceptance row draws its seq under the quote_acceptance
scope and the customer_quote status flip draws a fresh seq under the
customer_quote scope, both through the scope_sequences upsert returning
next_value on the caller's transaction (gaps allowed, values never reused, the
per-scope convention of core/db/migrations/0213_org_tenant_seq_deleted_idem.sql,
the C34 and D30 lineage), and the quote's creation write draws its first seq
the same way under the customer_quote scope, so every row this module owns
enters its scope's pull cursor at the write that creates or re-tails it.

The module's other customer_quote writes (the send and revise rotations,
draft edits, view tracking, the change-request append) do not draw seq: no
reviewed text pins allocation there, and this decision claims no update
re-tailing beyond the acceptance flip. Whether every material write to a
synced row must re-tail its scope's cursor is HH-3.5's sync-lane question;
the C3 re-review rules the conservative reading stands until that lane
pins otherwise.

The linkage key, pinned: the audit row for an acceptance is the row with action
constant CUSTOMER_QUOTE_ACCEPTED, entity type "quote_acceptance" and entity_id
equal to the quote_acceptance row id. Zero rows (including a deleted audit row)
or multiple rows fails verification closed. The dealer acceptance anchors by an
audit_seq column this table does not have and keys its audit row on the quote,
not the acceptance; both differences are deliberate and pinned here.

The audit row's Extra keys, pinned exactly: artifact_hash, artifact_sig, alg,
key_id, ref_type (the constant "customer_quote_share_token") and token_hash (the
accepting token's hash), plus amount_cents carrying the accepted total as an
integer. The first six join the audit allowlist openly at the integration
re-point; the integer passes unchanged.

The redaction chokepoint, and the encoding that survives it: the carried audit
boundary redacts any string value containing a run of seven or more consecutive
digits, whole value (hardscapeos_dibbits backend/internal/audit/service.go, the
cleartext detector), and roughly half of random 64-character hex signatures trip
it. The artifact_hash, artifact_sig and token_hash values therefore ride the seam
separator-encoded: six hex characters, one colon, six hex characters, and so on,
so no run longer than six digits can occur in any stored value by construction.
The encoding is part of the seam contract; decoding lives in the acceptance
module; the alg is the pinned constant compared at verification and the key id
is shape-validated at load, so neither can carry a seven-digit run. This chooses the separator encoding over a redaction
exemption for these keys: the encoding needs no change inside the audit module
beyond the allowlist join, it protects the values under any future chokepoint
tightening, and it is provable by a unit test that feeds a signature containing a
long digit run through the chokepoint semantics and asserts verbatim survival.

The audit write is mandatory, never degradable. The dealer precedent degrades
(a nil seam returns 0 and the acceptance proceeds); here an acceptance without
its audit row would be an unsigned acceptance, so the seam is a spawn-time
requirement (construction fails without it), a failed audit write rolls the
acceptance back with the status flip and the acceptance row, and the divergence
from the dealer precedent is this paragraph, stated openly. The audit trail's
own honesty: audit_log is append-only as posture and by the audit module's
construction (no update or delete method), not by DDL; nothing in the frozen set
forbids UPDATE or DELETE mechanically. That is acceptable because the guarantee
claimed here is tamper evidence, not tamper prevention: a rewriter cannot re-sign,
so verification names the break, and a deleted audit row fails verification
closed by the linkage rule above.

The key schedule, pinned: one signing key at a time, identified by a key id; a
verify-only previous key is allowed during rotation; every signature records the
key id it was made under; verification resolves the key by id from the current
plus previous set and fails naming the key id when it has been withdrawn. The
minimum key entropy is 256 bits (32 bytes; the loader refuses shorter). The key
id shape is lowercase alphanumeric plus hyphen, at most 32 characters, starting
lowercase alphanumeric, and carries no run of seven or more digits. Keys arrive
as environment at spawn under the placeholder names
HH_ACCEPTANCE_SIGN_KEY_ID, HH_ACCEPTANCE_SIGN_KEY and
HH_ACCEPTANCE_SIGN_KEY_PREVIOUS with HH_ACCEPTANCE_SIGN_KEY_PREVIOUS_ID naming
the previous key's id when the previous key is configured; spawn fails without
the current key and its id; no value exists in any file here, and the Infisical
path is Colton's to name at deploy.

Server-held honesty, ahead of the signature: the consent sentence recorded is
the server's served constant, never a client-supplied string; the accepted total
is computed by the core from the frozen lines, never taken from the request; the
acceptance's actorship is the link, never a person, so the audit row names the
token reference and no staff, tenant or platform user id.

Threat model, honestly: the signature gives tamper evidence for the acceptance
record. It does not stop an attacker holding both the database and the key
environment, and it does not make the attestation a contractor-verifiable public
proof; that stronger property is exactly what the rejected Ed25519 option buys
and costs.

## Consequences

- The acceptance surface meets its two acceptance lines with no migration: a
  homeowner accepts with a token and no account, and a revised quote's old token
  answers 410 rather than 404.
- Public token resolution is an unindexed scan until an index lands (finding F2);
  routing to HH-3.7, recorded, not worked around.
- The dead list grows with revisions; the cost is a TEXT column's width; the
  refusal to trim is deliberate.
- The audit row becomes part of the acceptance's proof, so the audit seam's
  availability on the acceptance transaction is load-bearing for verification,
  not just for history, and the integration re-point is a blocking dependency
  for serving this surface, not a nicety.
- The frozen tables carry no quote number column for the homeowner-facing
  reference; responses carry the quote id, and a number column (or a derivation
  pin) is a new finding routed to HH-3.7 rather than a fabricated number here.

## Options rejected, and what each loses

### Random tokens with plain hash overwrite (the naive carry of the ERP convention)

What it loses: the 410 semantics themselves. The old token becomes
indistinguishable from a never-existed token at the first rotation, which is
exactly the 404 the lane's acceptance forbids. The dealer convention stores its
history in per-link rows this table does not have, so carrying the convention
without carrying the table changes its meaning.

### A customer-quote share-link table mirroring the dealer's quote_share_links

The honest long home: per-link rows with revoked_at give full multi-generation
history, an indexed probe, and the dealer's exact semantics with no structured
column value. What it loses: it needs a migration, the schema is frozen, and the
only door is HH-3.7. Rejected now, recorded as the preferred destination if a
migration opens; the decision section's mechanism is designed so a later move to
link rows changes storage, not semantics, and the lossless conversion is part of
the column-overload pin.

### Last-retired-only tombstone (keep one dead hash, not the list)

What it loses: the second revision's history. The first revision's token answers
404 once the tombstone slot is reused by the next retirement, and the red-proof
mutation for exactly that sequence (M2c in the test plan, demonstrated on the
implementation) exists because the weakness is real: the kill lands at the
third generation. Rejected for failing the rule it exists to satisfy.

### HMAC-token schemes (keyed token derivation instead of random tokens)

What it loses: nothing cryptographically, but nothing needed either. Keyed
derivation cannot deliver the classification on its own, because one stored value
still has to serve both row resolution and history; it adds a key dependency to
the public read path and a key rotation that kills every live link, for no
property the dead list does not already give. Rejected as complexity without
purchase.

### Ed25519 for the artifact signature

What it loses by being rejected: public verifiability and true non-repudiation.
With Ed25519 the contractor or a court could verify the artifact against a
published public key without trusting the operator's server. That is a real
property, deliberately not bought: every verifier today is the server itself, an
asymmetric scheme adds key publication and rotation surface this Session has no
owner for, and the artifact's version field plus the stored algorithm name make
the upgrade additive rather than a rewrite. Revisit when a surface exists that
verifies outside the server.

### The signature stored on the acceptance row

What it loses: it needs a column the frozen table does not have (the review's
finding F3), and it puts the attestation inside the very storage a row-level
tamper is suspected of; the audit trail is the append-only home the carried
design already trusts for exactly this posture. Rejected now; HH-3.7 may reopen
it.

### Client-supplied consent text or total

What it loses: the attestation's honesty at the source. A recorded consent
sentence the acceptor could have authored is evidence of nothing, and a total
taken from the request signs the client's number rather than the quote's. Both
are server-held in the decision section; recorded here because the alternative
is the obvious implementation shortcut.

## Recorded in context, not decided here

- The OpenAPI spellings and response shapes for the pair: HH-3.4's document half;
  the OpenAPI document is the arbiter of spelling, and this ADR's authority
  semantics bind either spelling.
- ADR-0020's public leg governs these routes; this ADR neither repeats nor
  reshapes it.
- The offline acceptance path and the manual conflict policy for customer_quote:
  HH-3.5's surface, with the artifact obligations inherited unchanged.
- The tenant_brand storage gap (finding F1) is adjacent to this surface and not
  this ADR's to solve; the public view carries the brand fields and renders them
  absent until the gap is routed.
- The three HH Pro refusal sentences whose source text carries em dashes are
  ported with normalized punctuation only (the em dash becomes a colon or
  semicolon per sentence, every word unchanged), because the repository's no-dash
  rule binds code too. The per-sentence deviations, with source paths, are
  recorded in the lane's completion report and carried to the mid-Session
  checkpoint; byte-for-byte copies would need a recorded exception only Colton
  grants, and the question rides the checkpoint with the deviation list attached.
- The consent sentence is dash-free in the source and ports verbatim; no
  deviation exists for it.

## Sessions gated

- HH-3.6's own units: the token primitive, the rotation writes, the
  classification and the artifact signing implement this ADR; the two named
  acceptance tests and the red proofs in the test plan are its enforcement.
- The mid-Session human checkpoint: Colton reads the token lifecycle tests and
  this ADR before Lane C's work enters the integrated diff.
- HH-3.5: the sync replay of an offline acceptance carries the same artifact
  obligations.
- HH-3.7: findings F1 and F2 arrive for routing (brand storage, token index),
  with the share-link table recorded as this ADR's preferred destination if a
  migration opens.
