# ADR-0021: Contract conformance (the public tenant config, the customer-quote surface, the orphan ledger)

Status: Proposed

Drafted in HH-3 lane HH-3.4 (Phase C, contract conformance) for the two decision
subjects the lane's batch plan names (`tools/org/outbox/backend/c-b1-conformance-inventory/BATCH-PLAN.md`,
B-doc entry criteria) plus the orphan ledger the live brief's acceptance line
orders (`docs/sessions/HH-3.md`, HH-3.4 acceptance: "every orphan schema is
either referenced or removed, with the choice recorded per entity"). The number
is pre-allocated to this lane by ruling G3 (`context/phase-c-dispatch-ruling-r1.md`);
0022 and 0023 belong to the other lanes. Inputs: the B1 inventory
(`tools/org/outbox/backend/c-b1-conformance-inventory/FINDINGS.md`), the Tech
Lead review with fixes F-2 and F-3 and the G2 arbitration input
(`tools/org/outbox/tech-lead/c-b1-review/DELIVERABLE.md`), and the Lane C
cross-lane enum finding
(`tools/org/outbox/tech-lead/c-c1-review/review-notes.md`, section 5).
ADR-0019 records the entities this ADR wires; nothing here reopens it.

## Decision 1: the public tenant config serves dealer fields only (G2)

`GET /api/v1/tenant/config` stays public and parameterless, because its one
consumer is the pre-login portal chrome, which cannot authenticate first. Its
200 is narrowed at schema level to the new `TenantPublicConfig`
(`core/api/fragments/tenant.yaml`): the six dealer fields the ERP antecedent
served (`hardscapeos_dibbits` portal `handler.go` `defaultConfig`, from
`model.go`: dealer name, color, support email and phone; no tenant resolution,
no brand). The tenant-scoped `TenantBrand` reaches the contractor only through
the authenticated carriers that already require it: `TenantLoginResponse` and
`TenantRefreshResponse` embed `config: TenantConfig`, and `TenantConfig` keeps
its optional `brand`. The document half is this decision; the handler half is
the B-parity batch's work against this recorded shape.

The schema-level narrowing is the decision, not an implementation promise: the
prior 200 schema was `TenantConfig` with `brand` described as "present when one
is on file", which is the G2 defect (a public, parameterless route promising a
tenant-scoped record it has no way to resolve) restated as a promise. The
recorded fixture `config.json` now validates against `TenantPublicConfig`, the
schema of the route it was captured from.

### Options rejected, and what each loses

- **Keep the route public with a tenant identifier (path or query).** It builds
  an unauthenticated enumeration and brand-disclosure oracle: which tenants
  exist, their display names, their colors, one probe per guess. This is the
  same leak class ADR-0020 closes for org slugs (identical refusal bodies so
  the slug directory cannot be probed) and the NotFound rule closes for
  cross-tenant reads. It also breaks the ERP carry shape, whose antecedent is
  parameterless. Rejected without a counterweight that could pay for it.
- **Make the route tenant-authenticated.** One schema, one route, but the login
  screen renders unbranded: the chrome consumer cannot authenticate before a
  token exists, so it would have nothing to render from, and the ERP carry
  changes from a public bootstrap route to a gated one. The brand already has
  an authenticated delivery path, so this buys a second one at the cost of the
  only pre-login consumer.
- **Serve `TenantConfig` and trust the handler to omit `brand`.** The schema
  would keep promising tenant-scoped data on a public route; a handler
  regression then leaks it with no contract-level guard. The narrowing is the
  fix; the promise is the defect.

## Decision 2: the customer-quote send and acceptance surface (G1, F-3)

`CustomerQuote` gains its surface as three operations in the skeleton (the
non-transcribed part of the document), modelled on the dealer share-link pair
at `core/api/fragments/quote.yaml` (`GET /api/public/v1/quotes/{token}`,
`POST /api/public/v1/quotes/{token}/accept`) and its staff mint:

- `POST /api/v1/customer-quotes/{id}/send`, `x-role: requireStaff`
  (StaffBearer with X-Org). The send is a dealer action: the issuing
  contractor is recorded on the entity (`CustomerQuote.tenant_id`, "The
  contractor account that issued the quote"), and the desk that operates the
  send is the dealer's. The pin follows the census vocabulary's staff classes
  (`core/api/CONVENTIONS.md`, x-role list; the guard constructors at
  `core/internal/identity/guards/guards.go`) and the exact family it copies:
  the dealer send routes that mint a token and return it once
  (`quoteMintShareLink`, `quoteShareBySMS`) are `requireStaff`; no manager or
  admin decision lives in minting and sending a link. The 200 is
  `CustomerQuote` itself, carrying `magic_link_token` here and nowhere else,
  per that field's own contract.
- `GET /api/public/v1/customer-quotes/{token}` and
  `POST /api/public/v1/customer-quotes/{token}/accept`, both `x-role: public`
  with `security: []`, credentialed by the path token alone: 404 for a token
  that never existed, 410 for a link that died (expired, revoked, or rotated
  by a later send), 409 for acceptance in a wrong state, with the typed-name
  attestation (a full name plus an explicit affirmative) recorded as the
  acceptance artifact, whose signature lands in the D10 attachment store. This
  is the surface the D10 and D12 token ruling lands on.
- The `CustomerQuote` status enum carries the frozen seven-value CHECK
  verbatim (`core/db/migrations/0214_new_entities_and_pricing.sql`,
  customer_quote status CHECK): `draft, sent, viewed, changes-requested,
  accepted, declined, expired`. The contract moves to match the frozen schema
  and the HH Pro lifecycle, not the reverse (Lane C's cross-lane finding,
  Tech Lead confirmed: `viewed` is written on first view of a sent link,
  `changes-requested` on the public change request).

### Options rejected, and what each loses

- **Tenant bearer with capability `submit_rfq` on the send** (the prepared
  option in F-3, matching the tenant quotes surface). What it loses: the
  capability name lies (`submit_rfq` names RFQ submission, not link minting),
  and the desk cannot send, because the yard staff who operate the quote desk
  hold no tenant bearer. A tenant-side send, if ever wanted, is a separate
  operation with its own x-role, not a loosening of this one.
- **An elevated staff class (`requireManager` or `requireAdmin`) on the send.**
  What it loses: the counter. The dealer send family is `requireStaff`, and
  nothing in minting a link is a book-of-business or administrative decision;
  elevating it would block the staff who run the desk, matching no ERP
  antecedent.

## Annex: the orphan schema ledger (per entity)

Census before this batch: 1009 schemas, 999 reachable, 10 orphans (B1 FINDINGS
section 8, Tech Lead re-derived). The constraint set: D8's isolation pin
(`core/api/tools/d8-isolation.mjs`) requires `DealerQuote` and `CustomerQuote`
to exist and share exactly `QuoteLine`; D7 defines `OrderCard` in this document
with the total state map `core/api/tools/statemap.mjs` asserts; D11 defines the
one `product_image` model; D10 defines the attachment entities; the tenant
wire schemas are pinned by the 16 recorded fixtures (`core/api/tools/fixtures.mjs`).

| Schema | Defining file | Choice | Authority | Constraints honored |
| :--- | :--- | :--- | :--- | :--- |
| CustomerQuote | core/api/skeleton.yaml | referenced | `POST /api/v1/customer-quotes/{id}/send` 200 (x-role requireStaff); public pair alongside | G1 surface; gains the seven-value CHECK enum |
| QuoteLine | core/api/skeleton.yaml | referenced | transitively, `CustomerQuote.lines` (and `DealerQuote.lines`) | pinned by D8; the two quote kinds share exactly this shape |
| DealerQuote | core/api/skeleton.yaml | kept, reference pending | D8 and the d8-isolation pin; both served surfaces name it in their descriptions (the tenant quotes routes, the staff quote routes) while serving ERP wire shapes the recorded fixtures pin | cannot be removed (the D8 check fails without it); no honest $ref home exists without breaking fixture truth or inventing wire; see the gap note below |
| OrderCard | core/api/skeleton.yaml | kept, reference pending | D7 ("defined in the OpenAPI document") and the statemap pin; `TenantOrder` names it as the board projection of the same aggregate | cannot be removed (the statemap check fails without it); its serving surface, the board projection read, does not exist yet; see the gap note below |
| ProductImage | core/api/skeleton.yaml | kept, reference pending | D11; the product image routes' descriptions name it as the metadata model those routes govern | the image routes move bytes only; no metadata read surface exists to serve it; see the gap note below |
| Order | core/api/fragments/order.yaml | removed | this annex; ADR 0021 | dead wire duplicate: every order route serves the view family; no operation or schema referenced it |
| OrderLine | core/api/fragments/order.yaml | removed | this annex; ADR 0021 | same as Order |
| IdentityActionBlocked | core/api/fragments/identity.yaml | referenced | `POST /api/v1/staff/policy/check` 403 (x-role requireStaff), via the `IdentityPolicyBlocked` response carrying it under `block` | the operation's own description already promised exactly this object under the block key; the response now types it |
| MessagingApprovalRef | core/api/fragments/messaging.yaml | referenced | `MessagingMessage.ref` oneOf leg (every messaging read and the SSE message event) | `MessagingMessage`'s own description names it as the APPROVAL card ref; the loose `additionalProperties` object is narrowed to the two named shapes |
| MessagingEventRef | core/api/fragments/messaging.yaml | referenced | `MessagingMessage.ref` oneOf leg | same; named as the EVENT message ref |

Gap note, stated rather than smoothed: three keeps (DealerQuote, OrderCard,
ProductImage) remain unreferenced by the census after this batch. Each is
pinned by a recorded decision and a tool in the acceptance suite, so removal
is not available to this lane; each is named in prose by the surfaces that
carry its entity; and each lacks an honest $ref home today because every
served shape over its entity is an ERP wire transcription that the house rules
(`core/api/CONVENTIONS.md`, source discipline) forbid rewriting and that the
recorded fixtures pin (the `TenantInvoiceDetail` allOf over `InvoiceView`
works only because `InvoiceView` requires two fields; `DealerQuote` requires
`lines` and `valid_until`, which the wire captures do not carry, so the same
pattern cannot transfer). The B-record batch either lands their referencing
surfaces as ordered work arrives (the board projection read for OrderCard, the
canonical dealer quote read, a product image metadata read) or the residual
goes to Colton as a finding. This table is the per-entity record the
acceptance line orders; the census totals after this batch are recorded in the
lane's B-doc deliverable.

## What this ADR does not decide

The module-to-path-group mapping table (ruling G1: this lane is sole author)
arrives as a second annex at the B-record batch, reconciled against Lane A's
findings at integration; nothing in this document anticipates it. The tenant
brand backing store is gap-held under queue card
20260921-210000-tenantbrand-gap (the operations carry the hold sentence in
their descriptions); the ruling and the store belong to the schema queue and
HH-3.7, not to this ADR. The handler halves (the config route's served shape,
the customer-quote routes' implementations, the PATCH to PUT shim alignment)
belong to B-parity and later batches.
