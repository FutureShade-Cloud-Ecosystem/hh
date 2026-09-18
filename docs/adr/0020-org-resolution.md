# ADR-0020: Org resolution (one meaning of which org is this)

Status: Proposed

Drafted in HH-3 lane HH-3.3 (Phase B, identity) for gap G3 of the live brief (`docs/sessions/HH-3.md:58`, the lane entry at `:78-88`) and the G4 middleware rule (`:60`, `:80`). It argues against the contract fragments inventoried by Integrations in B1 (`tools/org/outbox/integrations/b1-inventory/issuer-surface.md`, section "Contract side", every fragment re-verified against the worktree file). D3, D25, D26, D28, D31 and D33 stand unopened; ADR-0016 stands unopened. This draft records the decision and the statuses the B5 sweeps pin.

## Decision

The request resolves the org, not the deployment. There is one org-resolution rule with two bearer-conditioned legs:

- Staff bearer: the org is the caller's selection, carried in the `X-Org` header and validated against Team membership per request before any database is touched. The contract states this three times: the parameter (`core/api/openapi.yaml:21272-21278`, "Org slug. Resolved and validated against Team membership before any database is touched (context 4.4)"), the StaffBearer description (`openapi.yaml:21261-21265`, "The org is resolved from the X-Org header and validated against Team membership before any database is touched"), and the info description (`openapi.yaml:10`, "org resolved from the X-Org header"). The context file is the architecture authority and says the same (`context/FUTURESHADE-CONTEXT.md:139`, the 4.4 convention, and `:65`, the engine bullet: "org resolution via `X-Org` header validated against Team membership before any database is touched").
- Tenant bearer: the org is pinned by the token's Team; there is no selection to make, and `X-Org` is never sent (TenantBearer description, `openapi.yaml:21266-21270`: "The org comes from the Team, the tenant (the contractor account) comes from the token, and branch sits beneath both (D3). Tenant routes never take X-Org"). This is ADR-0016's C33 order carried unchanged: org from the Team, tenant from the token, branch beneath both.
- Public operations (the 24 carrying `security: []`): no org resolution happens and no org context exists downstream.

The per-org edge serving entry (`openapi.yaml:13-15`, "Served by the platform edge per environment per org; the contract pins no host") is deployment topology, not an org-resolution input. It answers "which deployment serves this request"; it never answers "which org is this request acting in". The two stories compose under this decision as: the edge decides where, the request decides who. The core never reads host, path, source address, deployment name, environment name, or any edge-asserted org fact as org identity; any such value is untrusted input on the same footing as any client-controlled header. The contract pinning no host is what makes this enforceable: the host carries no org meaning for the core to lean on, and it must stay that way.

Reconciling ADR-0016's "org from the Team" with the StaffBearer's "org is resolved from the X-Org header", which is the residue of the tension G3 names: these are not rival claims about authority. The Team is always the authority, because membership is what validation checks; the header is the staff caller's selection of which membership to act under; a tenant has no selection to make, so the Team alone pins the org. The header selects, the Team validates, the edge does neither. The rejected option below fails precisely because it makes the edge both.

What this costs the staff client, unsmoothed: every staff operation carries the header (the B1 crosstab puts XOrg on exactly the 595 StaffBearer-only operations and XOrgSync on the 2 dual-scheme sync operations, none anywhere else, out of 668 total), and a client that omits it is refused on every call, loudly and immediately, with a status that names the defect. That is the price of the core never trusting routing.

## Enforcement semantics (G4, the middleware rule)

The rule is bearer-conditioned and operation-agnostic, enforced in the server because OpenAPI cannot express it: `XOrgSync` is `required: false` at the parameter level with the two-branch rule only in prose (`openapi.yaml:21279-21285`; the live brief at `docs/sessions/HH-3.md:60`). Middleware order, fail closed, no pool before org:

1. JWKS verification; the bearer class is resolved first (staff, tenant, or none).
2. None (public): no org resolution; `X-Org`, if sent, is an undeclared header and is ignored. It can grant nothing, because org context is stamped only under a verified staff bearer. This leg is a named non-rule, not a silent one, and it is testable as an absence of effect: a public request carrying `X-Org` behaves identically to one without it.
3. Staff (the 595 XOrg-bearing operations, and the staff leg of the 2 sync operations): `X-Org` absent: refuse, status below. Present: validate Team membership for the slug per D28 (`docs/analysis/DECISIONS.md:147`: per-request resolution against the platform source, a short-lived in-process cache, the credential never cached). Platform teams are named `org:<slug>` (`context/FUTURESHADE-CONTEXT.md:61`), so the membership check keys on the slug itself and touches no core table. No membership: refuse, status below, one identical body whether the slug is unknown or the membership is absent. Only after membership passes does the middleware resolve the slug against the `org` anchor table (migration `core/db/migrations/0213_org_tenant_seq_deleted_idem.sql:26`) and stamp `org_id`; only then may any module pool be acquired. Pool acquisition is therefore observably later than org resolution, which is the strong form of the acceptance line that a valid token with a wrong `X-Org` never reaches a repository (QA B1 inventory, finding F5: the sweep counts statements per refused request with a tracer).
4. Tenant (the 47 TenantBearer operations, and the tenant leg of the 2 sync operations): `X-Org` present: refuse, status below. Absent: the org comes from the Team behind the token and `tenant_id` is stamped from the token.
5. Scoping is stamped request context in middleware, never left to individual queries (`context/FUTURESHADE-CONTEXT.md:136`, the first 4.4 convention).

The sync surface inherits the rule unchanged: its two operations accept either bearer, which is why their parameter is honestly optional in the document, and the server supplies the branch the document cannot (`openapi.yaml:21279-21285`). The route half of that surface is HH-3.5's acceptance; this lane proves the middleware half through its own shim surface (QA B1 inventory, finding F7).

## Refusal statuses, pinned

The quality bar fixes the family: 401 for who you are, 403 for what you may do, never an oracle about other tenants' data (`context/hh-3.3-phase-b-handoff.md`, "The quality bar" section). The contract fixes the 401, 403 and 404 members of that family by its own response descriptions: Unauthorized, "Missing or invalid credentials" (`openapi.yaml:21364-21365`); Forbidden, "Authenticated, but the gate in x-role refused" (`openapi.yaml:21370-21371`); NotFound, "Not found. Cross-tenant reads answer not-found, never forbidden, so existence does not leak across tenants" (`openapi.yaml:21376-21377`). The exact codes for the org legs, with reasons:

- Staff bearer without `X-Org`: 400. The document declares the parameter `required: true` on exactly the staff operations (`openapi.yaml:21275`, and the B1 crosstab: XOrg on the 595 StaffBearer-only operations, none on the 47 tenant and 24 public operations), so its absence is a request that fails validation against the operation's declared shape, which is the contract's own BadRequest class, "The request did not parse or failed validation" (`openapi.yaml:21358-21359`). It is not 401, because the bearer verified and the credential is not the problem: 401 invites a re-authentication that cannot fix a missing header. It is not 403, because no gate was reached: the x-role gate never ran, and spending the RBAC-403 signal on a shape error would make the under-tier sweep unable to distinguish a malformed request from a tier refusal.
- Tenant bearer carrying `X-Org`: 400, the mirror case. The 47 tenant operations declare no `XOrg` parameter and the TenantBearer description states "Tenant routes never take X-Org" (`openapi.yaml:21270`); sending a parameter the operation prohibits is the same shape class as omitting one it requires, and it takes the same status.
- Valid token with a wrong or unknown org slug: 403. The caller is authenticated and the org-membership gate refused, which is the contract's Forbidden description verbatim in meaning (`openapi.yaml:21370-21371`). The response is identical for an unknown slug and a known slug without membership: distinguishing them would let a caller with a valid token enumerate the platform's org directory by status difference, the same leak class the NotFound rule closes for tenant data (`openapi.yaml:21376-21377`), reopened one level up. Not 404: not-found is the cross-tenant data answer, and using it for org context would itself become the existence oracle. Not 401: the credential is valid, and the caller's remedy is a different org or a membership request, not re-authentication.
- Valid token with a revoked Team membership: 403, body identical to the wrong-org leg. No membership-history distinction may appear in any response: a caller must not learn whether an org was never joined or was joined and left. D28's short-lived in-process cache is the one bounded window in which a revoked membership may still pass; it is named here, it is justified by D28 (recorded, not re-decided), and it must be tested in both directions: refusal once the window has passed, and bounded staleness within it (QA B1 inventory, finding F4).

Family summary, as the sweeps assert it: shape failures 400, identity failures 401, permission refusals 403, cross-tenant data 404; and zero repository reach on every refusal leg, with the refused staff request executing no core-database statement at all because the membership check keys on the slug and the `org` anchor read happens only after membership passes.

## Options rejected, and what each loses

### Org resolved by the serving edge

The G3 rejected option: the per-org servers entry (`openapi.yaml:13-15`) read as "the org is whichever org this edge serves", with `X-Org` dropped or treated as advisory. What it loses:

- The operator and multi-org staff surfaces. D4 (`docs/analysis/DECISIONS.md:41-43`) puts operator reads and writes on ordinary org-scoped endpoints, and platform teams carry membership across orgs (`context/FUTURESHADE-CONTEXT.md:61`, `org:<slug>` teams). An operator, or any staff member holding membership in two orgs, cannot be served by one org-pinned edge without per-org credentials; the header model serves them from any edge with one login.
- Defense in depth. Org identity becomes a property of infrastructure configuration rather than a validated per-request membership. A misrouted edge, a wrong route table, or a reused preview host under the wildcard sandbox domain (`context/FUTURESHADE-CONTEXT.md:57`) would serve one org's data with no in-application check to catch it. The context rule at `:65` and `:139` exists precisely so the core never trusts routing; the rejected option deletes the rule.
- The contract's parameter becomes dead text: `XOrg` declared `required: true` on 595 operations while the server ignores it (`openapi.yaml:21272-21278` with the B1 crosstab). A required parameter the server does not read is a lying contract.
- D28 loses its meaning. Per-request membership validation validates a requested org; the edge supplies no requested org to validate, so the revocation window, and with it the whole D28 resolution, would have nothing to bite on.
- The test surface collapses. The contract pins no host precisely because the host is not an input; with the host as the input, every B5 sweep and every identity-owned route shim would need per-org host fixtures to function at all.

### X-Org advisory, with a fallback org from the token

Rejected, fail-open hybrid. A dropped header would silently select an org the caller did not choose, which is a wrong-org write wearing a success. Fail closed is the default; the only named, bounded, justified and tested window in this design is D28's in-process cache, which is a recorded decision rather than a new one.

### 401 for a missing X-Org

Rejected. What it loses: the meaning of the 401 signal. Unauthorized is "Missing or invalid credentials" (`openapi.yaml:21364-21365`) and the credential verified; a correct client following 401 semantics re-authenticates in a loop that cannot fix a missing header, and the anonymous sweeps' 401 for no token at all becomes indistinguishable from a shape error.

### 403 for a missing X-Org

Rejected. What it loses: the meaning of the RBAC-403 signal. The sweep asserts an under-tier principal gets 403 from the x-role gate and a correct-tier principal does not (QA B1 inventory, item 5 on the ERP antecedent's both-halves shape); conflating shape with gate refusal makes that assertion unable to tell a malformed request from a tier refusal, and the PR can no longer state what a 403 means.

### Silently ignoring X-Org on tenant routes

Rejected, though it is the HTTP default for undeclared headers. What it loses: the testable rule. G4's second half demands rejection with a test proving it (`docs/sessions/HH-3.md:60`, `:84`); a silently ignored header is the permission-gate-silently-off bug class ADR-0016 already rejects through C31, and a client would carry an org header that does nothing, with no signal.

### 404 for an unknown org slug, 403 for a known one

Rejected. What it loses: the directory oracle. A caller with a valid token could enumerate which org slugs exist by status difference; the NotFound rule exists to close exactly this existence leak for tenants (`openapi.yaml:21376-21377`), and this option reinstates the class at the org level.

## Recorded in context, not decided here

- The ERP staff secret also keys the messaging stream tickets and the Google SSO issuer in the ERP runtime (Integrations B1 inventory, finding 5: at the pinned commit, the stream-ticket HMAC at `backend/cmd/server/main.go:545-547` and the SSO issuer handoff at `main.go:637`). Their disposition belongs to the port lanes in Phase C. Phase B's core must not assume that secret exists: the JWKS verifier and both middleware legs depend on no shared HMAC secret, and B2 must not strand the two consumers behind a configuration the core no longer reads.
- EM rulings this draft works under, cited, not re-decided: Phase B proves route behavior on identity-owned route shims shaped exactly like the contract's routes, registered via the settled RegisterRoutes guard signature, with no module porting this lane (this resolves QA finding F1); and the portal capability 403 leg counts under the RBAC-403 acceptance line (this resolves QA finding F6).
- QA finding F2 resolves with this draft: the sweeps may pin 400, 400, 403 and 403 for the four legs above, and SW-4 and SW-6 may claim green against those codes once B2 and B3 land.

## Sessions gated

- HH-3.3: the B2 middleware implements the ordering and statuses above; the B3 guard re-expression keeps x-role refusals distinct at 403; the B5 sweeps assert both halves of every conditional rule with zero repository reach on refusals.
- HH-3.5: the sync routes inherit the bearer-conditioned rule unchanged and own only their route half.
- HH-5: per-org databases and per-org edges compose with this decision as stated; convergence changes where requests are served, never how the org is resolved.

## Addendum: the Phase B enforcement vocabulary, pinned in the B2 review

This section pins the mechanism choices the enforcement semantics above
required but did not name. Nothing reopens the decision; each item states
what it pins.

The bearer class is carried by the standard `aud` claim. D3 already
records the coarse gate as "org membership and audience (staff,
contractor, customer)" (docs/analysis/DECISIONS.md, D3 first sentence),
and `aud` is that audience axis in its standard carrier. Two
deployment-configured values exist in Phase B, the staff audience and the
tenant audience, both required at construction and required to differ.
Classification reads verified claims only. A token claiming both values
is refused. A token claiming neither, including a token for the customer
audience once that audience exists, is nobody for this API until
customer-carrying routes exist and a third value is configured.

The tenant contractor-account linkage is carried by a `tenant` claim (the
contractor account UUID), required on tenant-class tokens and read on no
other class. The org of a tenant request is never read from any claim:
it is resolved from the Team behind the token per request through the
same membership source as the staff leg (D28), which is what leg 4's
"the org comes from the Team" means mechanically. The S2 check (real
Appwrite tokens, an HH-5 precondition) may rename either vocabulary
item; the change is confined to the claims parse, the configuration, and
the minter.

The token vocabulary the verifier reads: `iss` equal to the configured
issuer, `sub` required, `aud` as above, `exp` required with no grace,
`iat` required (the carried driver session ceiling is judged against it,
so a token without it cannot support that check), `nbf` honored when
present, `tenant` as above, NumericDate claims as integral seconds.
Staff-class tokens carry no authority claims and none are read: role,
tier and branch authority resolve from the app-role tables per request
(D3).

Operational inability answers 503 with one static body. When the
middleware cannot run its checks (the membership source errors, the org
anchor read errors, the JWK Set cannot be fetched), the request is
refused 503: 401 would report an outage as a credential defect, 403 as a
policy refusal. No operational error admits, and no operational error is
cached (D28's cache stores answers, never errors).

The server enforces one slug shape ahead of any platform or database
call: lowercase letters, digits, dot, underscore and hyphen, starting
lowercase alphanumeric, at most 64 characters, sent exactly once. A
header failing that shape answers 400 in the shape family, as does a
repeated header and an empty-valued header on tenant routes. The
contract's XOrg parameter is a bare string (core/api/skeleton.yaml,
parameters.XOrg); this shape is a server-side pin of the vocabulary the
platform's `org:<slug>` team names carry, and the concrete resolver
normalizes to the lowercase form the platform guarantees (the anchor
slug column is CITEXT).

The zero-statement property is bounded: it binds every pre-membership
refusal and the four pinned legs, which execute no core-database
statement because the org anchor read is reachable only after membership
passes. The one refusal that reaches the anchor is the provisioning gap
(membership passed, anchor row missing); it performs exactly one anchor
read and refuses 403 with the shared body.

The branch-id rule of migration 0215 (a branch-scoped role's grant
requires a non-NULL branch_id; a global role's grant carries NULL) is
enforced as a data defect rather than a policy outcome: the identity
layer checks every active grant row it reads, answers the 503 family
with the shared body on violation, and never lets a malformed row
hide behind a higher-tier grant, so a provisioning defect is loud in
monitoring rather than silent inside a 403.

Refusal bodies are pinned: every 401 carries the contract's Unauthorized
description as the error string; every 403 one constant; every 503 one
constant; the 400 bodies name the header defect without echoing any
value; all conform to the Error envelope. Expiry gets no grace; the
clock tolerance applies to issued-at and not-before only. An expired JWK
Set cache is never served stale, and an unknown key id triggers at most
one refetch per configured floor.

The sweeps carve out the authentication-lifetime refusals from the
no-authenticated-call-answers-401 expectation, each answering 401 with
the shared unauthorized body because it is a decision about the
credential, not about the gate: a staff token whose self-declared window
exceeds the ceiling resolved for its role; a staff bearer whose linked
users row is not ACTIVE; and a tenant bearer whose portal_users row is
not active. Every other authenticated call remains expected non-401.

The session ceiling is judged against the role resolved per request
from staff_role_grant and reads the dealer knob at enforcement time,
so lowering the knob refuses an existing over-knob session at its
next request and a driver promoted to another role has the long
session cut at the next request: lifetime follows the resolved role,
never a claim (D3), which is the one honest limit this vocabulary can
express.

Population writes a staff_role_grant only through a linked live users
row (email-exact linkage, never guessed, C31), so on populated data
every grant has one by construction and the resolver's unlinked-grant
arm exists for hand-minted rows only. VerifyPopulation counts live
grants whose subject has no live users row (StaffGrantsUnlinked) with
expectation zero; a nonzero count names hand-minted or
post-hoc-unlinked rows, never a population output.

portal_users.tenant_id equals the row's own customer_id: the customer
is the contractor account, the tenant claim carries the contractor
account UUID, and 0213 added tenant_id additive beside customer_id
precisely so this equality could be stamped by population rather than
allocated by a new authority.

An HH Pro estimator-origin person enters under buyer_no_pay, the
least-privilege canonical role containing submit_rfq (the capability
the estimator job reconciles to per D3's extras-become-capabilities
rule and 0215's NULL mapping), with the CapabilityFallback marker on
the mapping result. The marker is in-memory; the S2/HH-5 importer must
persist the estimator distinction as capability data when a capability
surface exists, and must never import an estimator-origin person as a
plain buyer_no_pay row without that data. This ruling never fires on
ERP-origin rows, which are already canonical.

A branch-scoped role holder with zero live branch grants receives no
grant at population time, counted in SkippedBranchless, never inserted
with a NULL branch_id and never granted invented breadth (C31); this
is deliberately stricter than the ERP, which authenticates such a user
with empty branch breadth, and the read path reproduces the ERP's
fence for post-population drift because branch breadth resolves live
from user_branch_grants, never from the grant row.

Population is the carry act itself: the ERP-side equivalent of this
program is migration SQL and carry scripts, not a module repository on
a request path. The fence exists to keep request-time modules from
reaching into each other's tables; a one-shot, operator-run
provisioning program whose whole purpose is to write the scope columns
the fence protects is the recorded exception, and request-path code
gets none.
