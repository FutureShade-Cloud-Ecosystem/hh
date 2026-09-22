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
(`tools/org/outbox/tech-lead/c-c1-review/review-notes.md`, section 5). Closed
at the lane's B-record batch with the mapping table annex, the after-census
totals, and the handler value sourcing, per the obligations the Tech Lead's B1
through B4 reviews bind (F-4, R-2, the F-C recommendation, the census reuse).
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

The handler half has landed against this shape, and its value sourcing is
recorded here so the ADR says it rather than a deliverable: the route registers
on the public leg at the binary's one composition point (`core/cmd/core/main.go`,
`registerRoutes`) and the handler reads nothing, serving the one config it was
constructed over. The served values are the ERP antecedent's own static dealer
identity (`core/internal/identity/tenantconfig/config.go`, `Default`), equal
field for field to the recorded capture `core/api/fixtures-live/config.json`
(the D22 vendored capture), until a dealer config service is ordered; because
the constructor takes the whole config as its argument, a later source swap
touches only the composition point.

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

Census method, before and after alike: transitive closure over internal `$ref`
from `paths` (every operation's parameters, request bodies and responses,
through `components.parameters` and `components.responses`) into
`components.schemas`; a schema is an orphan when no operation reaches it at any
depth. Census before this batch: 1009 schemas, 999 reachable, 10 orphans (B1
FINDINGS section 8, Tech Lead re-derived). Census after it: 1012 schemas, 1009
reachable, 3 orphans, exactly the three keeps below, re-derived twice
independently (the batch's closure and the Tech Lead review's) and reproduced
by the command in the mapping table annex's census section. The constraint set:
D8's isolation pin
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
pattern cannot transfer). Their referencing surfaces land as ordered work
arrives (the board projection read for OrderCard, the canonical dealer quote
read, a product image metadata read) or the residual goes to Colton as a
finding. This table is the per-entity record the
acceptance line orders; the after-census totals above keep the acceptance line
auditable from this ADR alone, with no dependence on session tooling.

## Annex: the module-to-path-group mapping table (ruling G1)

This lane is the sole author of the recorded module-to-path-group mapping table
(ruling G1, `context/phase-c-dispatch-ruling-r1.md`), the reconciliation of the
alias gap the B1 inventory found: ERP module names and contract path groups are
not one-to-one, and the correspondence needed one recorded authority. It is
this ADR's second annex so the reconciliation travels with the conformance
decisions and no second docs/ authority exists. Inputs: the B1 mapping draft
(`tools/org/outbox/backend/c-b1-conformance-inventory/MAPPING-TABLE-DRAFT.md`),
whose rows and wiring sites the Tech Lead re-verified at the carry commit
(`tools/org/outbox/tech-lead/c-b1-review/DELIVERABLE.md`), with the placement
footnotes that review's F-4 binds and the after-census obligation its B2 review
records. Lane A's parallel findings are compared against this table at
integration per the ruling; a disagreement there is a finding for Colton, not a
lane-level fix.

### Universes and the census

- Rows: the 39 distinct ERP modules named by the guard inventory
  (`tools/org/outbox/integrations/b1-inventory/guard-surface.txt` section 1).
  Of these, 36 carry at least one guard-bearing registrar signature, 62
  guard-bearing signatures of 84 non-test registrar signatures in total;
  `demo`, `identity` and `portal` are noguard and carried as marked rows. This
  corrects the inherited "38": a miscount of the same inventory, re-derived
  twice by independent method (the unit's recount and the Tech Lead's git
  census at the carry commit).
- Columns: the contract staff path groups, the distinct first path segment
  under `/api/v1/` of operations declaring the XOrg parameter. 46 at this
  lane's baseline document (the inherited "45" was not reconstructible from
  its prose), 47 after Decision 2's send route added `customer-quotes`. The
  four groups with no staff operation at all (`demo`, `identity`, `tenant`,
  `will-call`) and the dual-bearer `sync` group are carried in the sub-annexes
  below, so the full matrix form stays reconstructible from this table.
- Method, ERP side: literal-route grep per module directory over the read-only
  ERP reference checkout at the carry commit `6eaea3ec` (the same analysis
  `core/api/tools/refresh-erp-routes.mjs` performs at vendor time; the
  checkout is never modified). Method, contract side: mechanical
  group-to-fragment extraction over `core/api/skeleton.yaml` and
  `core/api/fragments/*.yaml`, plus each fragment's header naming its ERP
  source modules. Method, wiring: the ERP's `backend/cmd/server/main.go` call
  sites; every `main.go` line reference in the table below is into that file
  at the carry commit.
- The wiring census caveat, kept so nobody cites the baseline as complete: the
  B1 wiring inventory (guard-surface.txt section 2) lists 47 direct call sites
  and is not a complete call-site census. Nine modules register through chained
  calls it missed (assist, branches, customer, export, messaging, purchasing,
  quoteassist, tour, settings), and settings mounts from inside the quote
  builder (`backend/internal/quote/builder_handler.go`, inside
  registerBuilderRoutes, not main.go). Their wired guards below come from the
  ERP source directly, verified at the carry commit.
- The wired-guards column records call-site wiring only. The authoritative
  per-operation gate is the document's `x-role`, and the lane's equality suite
  asserts the two equal for every registered route (B-assert,
  `core/cmd/core/xrole_assert_test.go` over
  `core/internal/platform/apigen/routes.gen.go`); this table must not become a
  second gate census.
- Live census at close (`core/internal/platform/apigen/routes.gen.go`, the
  equality suite's authority): 671 operations; x-role dispatch requireStaff
  354, requireManager 176, requireAdmin 63, the eight capability gates 43,
  public 26, tenant 4, operator 3, scope-member 2; bearer classes 596 staff,
  47 tenant, 2 dual, 26 public; 13 registered, 658 ledgered. Exactly 85
  operations carry `x-guards`, unchanged by this lane (requireBranch 52,
  requirePathUUID 25, requirePlatformAdmin 2, service:isAdmin 2, yardBranchFence
  2, requirePicked 1, service:isSuperAdmin 1, holdback-release-dual-gate 1).
  ADR-0020's merged prose carries the earlier split of the earlier 668 (595
  staff-only, 24 public); it stands as a record of its moment, and the counts
  here are the live ones.

State at close, code side, so a reader of this ADR alone knows what enforces
it: the parity test asserts both directions over the production wiring (671
documented, 13 registered, 658 ledgered; `core/cmd/core/parity_test.go` with
its ledger), the x-role equality suite auto-asserts every new registration the
moment it lands, the server types generate deterministically from the document
(`core/api/tools/gen-types.mjs`, `core/internal/platform/apigen`), and the
shim's tenant/me registration is PUT, aligned to the document with the
three-way evidence recorded at the site (the ERP registration at the carry
commit, the vendored list, the fragment's method).

### The table

Status key: MAPPED means both sides verified as the method above describes.
NOGUARD marks the three modules with no guard-bearing signature.

| ERP module | Guard params at signature | Wired as, at the call site | ERP path groups | Contract fragment(s) | Status |
| :--- | :--- | :--- | :--- | :--- | :--- |
| assist | requireStaff, requireAdmin | RequireStaff, RequireAdmin (main.go:1808) | assist | catalog-support.yaml | MAPPED |
| audit | requireAdmin, requireStaff | RequireAdmin, RequireStaff (main.go:352) | audit | reporting.yaml | MAPPED |
| bankrec | requireStaff, requireManager, requireAdmin | same triple (main.go:473) | bankrec | gl.yaml | MAPPED |
| billing | requireStaff, requireManager, requireAdmin | same triple (finstmt mount, billing.yaml header cites dib510_register_mount.go) | ar (register, plus the three public ar tracks) | billing.yaml | MAPPED |
| bistrack | requireStaff | RequireStaff (main.go:179) | (none literal: descriptor table) | skeleton.yaml | MAPPED, non-literal registration per drop-list.yaml:45-46 |
| branches | requireStaff | RequireStaff (main.go:223) | branches | catalog-support.yaml | MAPPED |
| compliance | staff, manager, admin | same triple (main.go:506) | compliance | warranty.yaml | MAPPED |
| customer | requireStaff, variadic writeGuard | RequireStaff, RequireManager (main.go:214) | customers | catalog-support.yaml | MAPPED |
| dashboard | requireStaff, requireAdmin (four registrars) | RequireStaff, RequireAdmin (main.go:1600, 1650); viz RequireStaff (1601) | dashboard, quick-actions | dashboard.yaml | MAPPED |
| demo | none | no guards (main.go:2013) | (none literal: constants) | skeleton.yaml | NOGUARD, non-literal per drop-list.yaml:41-44 |
| dispatch | requireStaff, requireManager (plus W3 staff, public bare) | RequireStaff, RequireManager (main.go:775); W3 RequireStaff (1051); public bare (779) | dispatch, will-call | dispatch.yaml | MAPPED |
| export | requireStaff | RequireStaff (main.go:384) | exports | reporting.yaml | MAPPED |
| feedback | requireStaff | RequireStaff (main.go:598) | feedback | messaging.yaml | MAPPED |
| finstmt | staff, manager, admin | same triple (main.go:478) | finstmt | gl.yaml | MAPPED |
| gl | staff, manager, admin | same triple (main.go:456) | gl | gl.yaml | MAPPED |
| identity | none | no guards (main.go:175; Google routes 638) | auth, identity, staff | identity.yaml | NOGUARD |
| invcount | staff, manager, admin | same triple (main.go:524) | counts | inventory.yaml | MAPPED |
| inventory | staff, manager, admin (plus stock truth staff, manager) | same triple (main.go:461); stock truth (1234) | inventory | inventory.yaml | MAPPED |
| landedcost | staff, manager, admin | same triple (main.go:532) | landed-costs | purchasing.yaml | MAPPED |
| lien | staff, manager, admin | same triple (main.go:514) | lien | warranty.yaml | MAPPED |
| messaging | requireStaff, requireAdmin (two registrars) | RequireStaff, RequireAdmin (main.go:546) | messaging | messaging.yaml | MAPPED |
| order | requireStaff (plus edit, transition, change: staff, manager) | RequireStaff (main.go:282); transitions (287), edit (403), change (1403): staff, manager | order-changes, orders, shipment-groups | order.yaml | MAPPED |
| payment | readGuard, adminGuard | RequireStaff, RequireAdmin (main.go:299) | payments | billing.yaml | MAPPED |
| payments | requireManager | RequireManager (main.go:949) | ar (the reverse route) | billing.yaml | MAPPED |
| portal | none | no guards (main.go:266) | tenant | tenant.yaml | NOGUARD |
| pos | staff, manager, admin (plus typed signature staff, return requests staff) | same triple (main.go:332); typed (624) | pos | pos.yaml, catalog-support.yaml (receipt) | MAPPED |
| pricing | readGuard, writeGuard (four registrars) | RequireStaff, RequireManager (main.go:199) | pricing | pricing.yaml | MAPPED |
| printing | requireStaff | RequireStaff (main.go:613) | printing, pos (the receipt route) | catalog-support.yaml | MAPPED |
| product | writeGuard (plus counter staff, writeGuard) | RequireManager (main.go:185); counter RequireStaff, RequireManager (919) | products | catalog-support.yaml | MAPPED |
| purchasing | staff, manager, admin | same triple (main.go:1300, a chained call the baseline's section 2 missed) | purchasing | purchasing.yaml | MAPPED |
| quote | requireStaff (six registrars, one public) | RequireStaff (main.go:429, 431, 1404); public share bare (437) | orders (related-documents), quote-availability, quotes | quote.yaml | MAPPED |
| quoteassist | requireStaff, requireAdmin | RequireStaff, RequireAdmin (main.go:1715) | quote-assist | quote.yaml | MAPPED |
| reporting | staff, manager, admin | same triple (main.go:1771) | reporting | reporting.yaml | MAPPED |
| settings | requireStaff | RequireStaff, mounted from inside the quote builder (backend/internal/quote/builder_handler.go:33, not main.go) | platform (hotkeys), surfaces | dashboard.yaml | MAPPED, note the mounting quirk |
| tasks | requireStaff (three registrars) | RequireStaff (main.go:580, 859, 1057) | tasks, yard | order.yaml | MAPPED |
| tax | staff, manager, admin | same triple (main.go:487) | tax | identity.yaml | MAPPED |
| till | staff, manager, admin (plus history staff) | same triple (main.go:311) | ar (nine receivables routes), till | pos.yaml | MAPPED |
| tour | requireStaff | RequireStaff (main.go:1882) | platform (tour steps, policy), tour | catalog-support.yaml | MAPPED |
| warranty | staff, manager, admin (plus W5 staff, manager, and a public registrar) | same triple (main.go:592); W5 (1455); public bare (1456) | warranty | warranty.yaml | MAPPED |

Footnotes binding the placements a reader will take for transcription errors
(F-4, the Tech Lead's B1 review; a future correction that "fixes" any of these
breaks the record):

1. The `tax` routes live in the identity fragment (`core/api/fragments/
identity.yaml`, whose header names identity plus tax as its two modules): a
deliberate fragment assignment, not a transcription error.
2. The `ar` group is served by three modules across two fragments: billing
   (the register subtree and the three public ar tracks) and payments (the
   reverse route) in `billing.yaml`, till (nine receivables routes) in
   `pos.yaml`.
3. The `settings` mount: registered from inside the quote builder
   (`backend/internal/quote/builder_handler.go`), not from main.go. This is
   ERP wiring history Lane A meets during carry; the footnote travels with the
   table so the mount site is not "fixed" either.

### Contract groups with no ERP source module

From the parity ledger (`core/api/drop-list.yaml`, additions) and the skeleton
(`core/api/skeleton.yaml`, whose README section names its resident surfaces):

| Contract group | Source decision | Fragment |
| :--- | :--- | :--- |
| attachments (3 ops) | D10 canonical attachment entity | skeleton.yaml |
| operator (4 ops) | D4 launcher surface | skeleton.yaml |
| sync (2 ops, dual bearer) | context 4.5 sync protocol | skeleton.yaml |
| tenants (2 ops, staff brand writes) | D12 tenant_brand | skeleton.yaml |
| customer-quotes (1 staff op, the send; the public pair sits under /api/public/v1/customer-quotes) | this ADR, Decision 2 (G1) | skeleton.yaml |

### Shared groups, served by more than one ERP module

| Contract group | ERP modules and their share |
| :--- | :--- |
| ar | billing (the register subtree and the three public tracks), payments (POST /api/v1/ar/payments/{id}/reverse), till (bounced, nsf, payment history, invoice payments, credit holds, nsf policy, payment attempts) |
| orders | order (the lifecycle), quote (GET /api/v1/orders/{id}/related-documents, transcribed in quote.yaml:355) |
| platform | settings (hotkeys pair), tour (tour steps and policy) |
| pos | pos (the counter), printing (GET /api/v1/pos/sales/{id}/receipt) |

### Groups outside the staff column set

- `sync`: the two dual-bearer operations declare XOrgSync, not XOrg; they form
  their own column for the matrix form.
- `demo`, `identity`, `tenant`, `will-call`: groups whose operations are all
  non-staff (public or tenant bearer). `tenant` is portal's column; the other
  three map to demo, identity, and dispatch respectively (dispatch's public
  will-call pair sits in dispatch.yaml).

### Reproducing the census

Every number above reproduces read-only. From the repo root, the suite first
(it prints the assembled totals, 559 path keys, 671 operations, 1012 schemas,
and leaves the worktree clean, so the assembled document is byte-stable):

```
node core/api/tools/check-all.mjs
```

The orphan schema census over the assembled document (prints `schemas 1012
reachable 1009 orphans 3 [DealerQuote, OrderCard, ProductImage]`):

```
node - core/api/openapi.yaml <<'EOF'
const fs = require("fs");
const YAML = require("./core/api/tools/node_modules/yaml");
const doc = YAML.parse(fs.readFileSync(process.argv[2], "utf8"));
const C = doc.components, S = C.schemas;
const reach = new Set(), done = new Set();
const refs = (o, out) => {
  if (Array.isArray(o)) { o.forEach(x => refs(x, out)); return; }
  if (o && typeof o === "object") for (const [k, v] of Object.entries(o))
    k === "$ref" && typeof v === "string" ? out.push(v) : refs(v, out);
};
const follow = (r) => {
  if (!r.startsWith("#/components/")) return;
  const p = r.slice(2).split("/"), name = p[p.length - 1];
  if (p[1] === "schemas") {
    if (reach.has(name)) return;
    reach.add(name);
    const out = []; refs(S[name], out); out.forEach(follow);
  } else if (!done.has(r) && C[p[1]] && C[p[1]][name]) {
    done.add(r);
    const out = []; refs(C[p[1]][name], out); out.forEach(follow);
  }
};
for (const item of Object.values(doc.paths)) {
  const out = [];
  refs(item.parameters || [], out);
  for (const [m, op] of Object.entries(item))
    if (m !== "parameters" && op && op.responses) refs(op, out);
  out.forEach(follow);
}
const orphans = Object.keys(S).filter(n => !reach.has(n)).sort();
console.log("schemas", Object.keys(S).length, "reachable", reach.size,
            "orphans", orphans.length, "[" + orphans.join(", ") + "]");
EOF
```

The staff path group census, same document (prints `staff path groups 47`
today, `customer-quotes` among them; run against the lane's starting document,
`git show cf1f1f6:core/api/openapi.yaml` through a temp file, it prints 46):

```
node - core/api/openapi.yaml <<'EOF'
const fs = require("fs");
const YAML = require("./core/api/tools/node_modules/yaml");
const doc = YAML.parse(fs.readFileSync(process.argv[2], "utf8"));
const groups = new Set();
for (const [path, item] of Object.entries(doc.paths)) {
  if (!path.startsWith("/api/v1/")) continue;
  const ops = item.parameters ? [{ parameters: item.parameters }] : [];
  for (const [m, op] of Object.entries(item))
    if (m !== "parameters" && op && op.responses) ops.push(op);
  for (const op of ops)
    if ((op.parameters || []).some(p => p && (p.$ref === "#/components/parameters/XOrg" ||
        (p.in === "header" && p.name === "X-Org"))))
      groups.add(path.split("/")[3]);
}
console.log("staff path groups", groups.size,
            "[" + [...groups].sort().join(", ") + "]");
EOF
```

The ERP module census, over the read-only reference checkout, pinned to the
carry commit (the working tree of that checkout may be ahead; the census reads
git at the commit and modifies nothing; prints `signatures 84, guard 62;
modules 39, guard 36`):

```
cd /home/colton/Desktop/FBHQ/hh-sessions/hardscapeos_dibbits
git ls-tree -r --name-only 6eaea3ec -- backend/internal \
  | grep '\.go$' | grep -v '_test\.go$' \
  | while IFS= read -r f; do git show "6eaea3ec:$f" | awk -v path="$f" '
      function flushsig() {
        if (sig != "" && sig ~ /[Rr]egister[A-Za-z0-9_]*Routes\(/) {
          split(path, pp, "/")
          print pp[3], (sig ~ /func\(http\.HandlerFunc\) http\.HandlerFunc/ ? "guard" : "noguard")
        }
        sig = ""
      }
      function balanced(  t, o, c) {
        t = sig; o = gsub(/\(/, "(", t); t = sig; c = gsub(/\)/, ")", t); return o == c
      }
      /^func / {
        if (sig != "") flushsig()
        sig = $0
        if (index(sig, "{") && balanced()) flushsig()
        next
      }
      sig != "" {
        sig = sig " " $0
        if (index($0, "{") && balanced()) flushsig()
      }
      END { if (sig != "") flushsig() }
    '; done \
  | awk '{ sigs++; if ($2 == "guard") guards++; mods[$1] = 1; if ($2 == "guard") gmods[$1] = 1 }
          END {
            n = 0; for (m in mods) n++
            k = 0; for (m in gmods) k++
            printf "signatures %d, guard %d; modules %d, guard %d\n", sigs, guards, n, k
          }'
```

The live x-role and bearer dispatch, from the generated route table the
equality suite reads (354 requireStaff, 176 requireManager, 63 requireAdmin,
43 across the eight capability gates, 26 public, 4 tenant, 3 operator,
2 scope-member; bearers 596 staff, 47 tenant, 2 dual, 26 public; 671 rows):

```
grep -o 'XRole: "[^"]*"' core/internal/platform/apigen/routes.gen.go | sort | uniq -c | sort -rn
grep -o 'Bearer: "[^"]*"' core/internal/platform/apigen/routes.gen.go | sort | uniq -c
```

## What this ADR does not decide

The module-to-path-group mapping table is recorded above as this ADR's second
annex (ruling G1: this lane is sole author); what stays outside it is the Lane
A comparison at integration, where a disagreement is a finding for Colton per
the ruling, not a lane-level fix. The tenant brand backing store is gap-held
under queue card
20260921-210000-tenantbrand-gap (the operations carry the hold sentence in
their descriptions); the store is greenfield schema the ERP never had and
lands in HH-5 per the routing of record in the Lane C checkpoint ruling
(HH-LANE-C-CHECKPOINT-RULING-r1), not in HH-3.7 and not in this ADR.
Decision 2's own subject carries the same routing: the customer-quote
public pair inherits QuotePublicView, whose required quote_no has no
column in the frozen schema (no number exists there), so the number is
an HH-5 landing per that ruling, and until it lands the served surface
references the quote by id only. Of the handler halves, the config route's served
shape and the PUT alignment of the tenant/me shim have landed with this lane's
code batches; the customer-quote routes' implementations belong to the lane
that registers them, and their three parity-ledger entries come out in that
registering diff; the `operator` x-role class stays undecided until its lane
records what it means, and the equality suite refuses it loudly until then.
The pagination limit clamp (the document's minimum and maximum versus the
helpers' fail-quiet clamp) is recorded as a convention in
`core/api/CONVENTIONS.md`, Pagination, per the Tech Lead's ruling on the
divergence; strictness, if ever wanted, is a deliberate helper change with its
own decision record, not a silent edit and not this ADR's.
