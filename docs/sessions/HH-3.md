# HH-3: the unified core

> Live brief, for `hh/docs/sessions/HH-3.md`. It supersedes the frozen copy at `docs/analysis/sessions/HH-3-unified-core.md`, which stays as the version of record for what HH-1 wrote (D24). Where the two disagree, this one wins. Amended from the frozen brief by D22, D23, D25 and D26, and by four contract gaps the HH-2 verification found.

## Goal

Make `core/` a conforming Go monolith carrying every HardscapeOS module, the HH Pro domain rules that belong server-side, org and tenant scoping, JWKS authentication, role gating from the contract, sync endpoints per declared scope, and a standing lane that tracks the in-flight HardscapeOS delivery until cutover.

## What changed from the frozen brief

**The Session is phased, not flat.** HH-2 ran five lanes in parallel and integrated at the end. HH-3 cannot: everything builds on the schema, and a wrong-but-passing schema does not stay in HH-3. It propagates into HH-5's convergence and from there into production data, where it is no longer a test failure but a customer's wrong number. So the schema is written once, by an attended lane, read by a human, and then frozen with a machine check.

**The schema lane owns every migration in the Session.** In the frozen brief, HH-3.3 created `staff_role_grant` and the contractor role mapping table, which are migrations. Under the freeze rule those cannot land after sign-off, so they move into HH-3.1's scope and HH-3.3 becomes Go only. This is what makes the freeze enforceable rather than aspirational: one lane, one phase, one review.

**Three lanes are attended and the rest are not.** Attended means the lane does not run while nobody is at the terminal, and it inverts the usual blocker rule: at a named decision point it stops and asks rather than recording a blocker and continuing. Unattended lanes keep the HH-2 rule.

**The freeze has exactly one door.** HH-3.7, the tracking lane, ports new ERP migrations, and each port is a human decision of ported or deferred. Nothing else may touch `core/db/migrations/` after sign-off.

## Phases

| Phase | Lanes | Attended | Gate to leave |
| :---- | :---- | :---- | :---- |
| A | HH-3.1 schema | yes | Colton reads the migration set, signs off, freeze marker lands |
| B | HH-3.3 identity | yes | the isolation tests pass and Colton reads the org and tenant resolution |
| C | HH-3.2, HH-3.4, HH-3.6 | no | the integrator lands them |
| D | HH-3.5 sync | yes | Colton reads the per-entity conflict policy and signs it off |
| E | HH-3.7 tracking | yes, standing | runs from here through HH-11 |

Phase C lanes run in parallel against a frozen schema and a merged identity layer. Phase B could in principle run beside C, and it does not: every Phase C lane touches a `RegisterRoutes` signature that Phase B changes, which is the collision the frozen brief already predicted between 3.3 and 3.4. Sequencing B before C costs one phase and removes the conflict entirely. Phase D is HH-3.5 alone, attended, after Phase C rather than inside it (D26), which costs one more phase and buys two things: the sync lane writes against the final module set HH-3.6 lands, and Colton is free while Phase C runs.

## The attendance call, taken

HH-3.5 is attended (D26). It implements the per-entity conflict policy, and a wrong `server_wins` where `manual` was meant silently overwrites work a crew did in a yard with no signal, discovered by the crew rather than by a test. That is the same class as a wrong schema, one step further out, so it takes the same control. Phase C loses it, HH-3.5 runs alone as Phase D, and the tracking lane becomes Phase E.

## Preconditions

HH-2 merged, which it is, and the preflight pull request landing D22 to D26 (see the preflight prompt). No platform dependency: this Session runs on a preview database of the same shape and on JWKS tokens signed by test-generated keys. S0 (managed Postgres, the `dibbits` org database) and S2 (Appwrite identity, real tokens) are preconditions of HH-5, not of this Session.

Position relative to the ERP cutover: upstream. The core runs beside HardscapeOS against a restored copy of its database here, and becomes a co-writer of the live org database only in HH-5.

## Locked inputs

- `context/FUTURESHADE-CONTEXT.md` in the repository, authoritative on architecture.  
- `docs/analysis/DECISIONS.md`, D1 to D26, all taken. Live file from D20 onward.  
- `docs/analysis/domain-map.md` section 5 for the schema, `hardscapeos-inventory.md` section 3 for the module list. Frozen history; in-body `analysis/...` paths resolve to `docs/analysis/...` in this tree (D24).  
- `core/api/openapi.yaml`, the contract HH-2 shipped, and `core/api/CONVENTIONS.md`.  
- `manifest.yaml`, regenerated after D23, with `public-desk` pre-auth.  
- The vendored gate inputs from D22: `core/api/erp-routes.txt` and `core/api/fixtures-live/`.

## The four contract gaps, as named inputs

These are defects in what HH-2 shipped, found by the verifier and confirmed by reading the document. Each is assigned to a lane and none is optional.

**G1, the customer quote has no surface.** `CustomerQuote` appears exactly once in the whole contract: its own definition. No send route, no acceptance route. D10 and D12 ruled that the token appears only in the send response, and there is no send response for it to appear in; D23 puts homeowner acceptance on a pre-auth magic-link page, and there is no page's route to call. The template already exists and is the dealer-quote pair the ERP contributed: `GET /api/public/v1/quotes/{token}` and `POST /api/public/v1/quotes/{token}/accept`, both `x-role: public` with `security: []`, the opaque token in the path as the credential, 404 when the token never existed and 410 when the link died. Build the customer-quote equivalents on that shape. Owner: HH-3.4 for the document, HH-3.6 for the behaviour.

**G2, a public route carries tenant-scoped data.** `GET /api/v1/tenant/config` is public and embeds the tenant brand. Either it is not public or the brand does not belong in it. Owner: HH-3.4, and it is a finding to resolve, not to document away.

**G3, two org-resolution stories.** The contract carries both the `X-Org` header and per-org edge serving, never reconciled. The core cannot implement both meanings of "which org is this". Owner: HH-3.3, and it produces an ADR rather than a quiet choice.

**G4, a constraint OpenAPI cannot express.** `XOrgSync` is optional, with prose saying it is required under `StaffBearer` and never sent under `TenantBearer`. The document cannot say that, so the server must, and a test must prove both halves. Owner: HH-3.3 for the middleware, HH-3.5 for the sync routes.

Carried, lower priority: `OrderCard`, `DealerQuote` and `ProductImage` are also referenced by no operation. Each is either a route that was missed or a schema that should not exist; decide per entity and record which.

## Lanes

### HH-3.1 Baseline, the additive schema, and every migration in this Session (Phase A, attended)

Scope: as the frozen brief gives it, plus the identity tables that used to sit in HH-3.3. Carry `hardscapeos_dibbits/backend/migrations/0001` through the last file as the baseline of `core/db/migrations`, unedited. Then additive migrations only: `org_id` on every org-owned table and `tenant_id` on every tenant-scoped table per `docs/analysis/domain-map.md` section 5; `seq` per declared scope with a server-side allocator; `deleted_at` tombstones on synced tables; the HH Pro entities the ERP lacks (`plan`, `plan_line`, `customer_quote`, `quote_acceptance`, `attachment`) in the shapes section 5.4 gives; the `legacy_contractor_id` alias column on the unified order (D6); the D13 to D18 columns; and now also `staff_role_grant` keyed by platform user id, the contractor role set on `tenant_user` with the one-time HH Pro mapping table, and the driver profile row, all per D3 and the HH-3.3 scope they came from. Nothing destructive: HardscapeOS must keep running against this schema unchanged.

Touches: `core/db/migrations/`, `core/db/README.md` with the additive-only rule and the ledger of ported ERP migrations, `core/db/SCHEMA-FROZEN` (the freeze marker, written at sign-off).

Acceptance: the migrations apply from zero and on top of a restored HardscapeOS dump; the ERP's own integration suite (`hardscapeos_dibbits/backend/internal/gate`, run against this database) still passes, which proves the additions are invisible to the old application; `GATE-5a` and `GATE-5b` pass; the special-order migration parses every synthetic sku in the seed and in the restored dump with none left unparsed (D16); no table stores availability (D13); every table this Session needs exists, including the identity tables, because nothing may add one later. **And: after the freeze marker lands, no migration file may be added or edited by any lane.** A `ci` step enforces it: any diff under `core/db/migrations/` after the commit named in `core/db/SCHEMA-FROZEN` fails the build, and the only pull requests permitted to move the marker are HH-3.7's ports, each carrying its `PORTED.md` entry.

Human checkpoint, and the reason this lane is attended: Colton reads the migration set itself, not a report about it. What to look for: every new column nullable or defaulted; no destructive statement anywhere; `org_id` and `tenant_id` present on the tables the domain map says own them and absent from the ones it does not; the special-order parse covering the whole dump; and the identity tables shaped so HH-3.3 needs nothing added. Sign-off writes the freeze marker.

Prompt: "Bring the HardscapeOS migration set into `core/db/migrations` verbatim as the baseline, then write additive migrations for `org_id`, `tenant_id`, per-scope `seq`, `deleted_at`, the entities in `docs/analysis/domain-map.md` section 5 that the ERP lacks, the D13 to D18 columns, and the identity tables per D3: `staff_role_grant` keyed by platform user id, the contractor role set on `tenant_user` with a one-time mapping table from HH Pro's set, and the driver profile row. This lane owns every migration in HH-3; no later lane may add one, so anything any lane needs must exist when you are done. Every new column is nullable or defaulted so the running ERP is unaffected, and you prove it by running the ERP's integration suite against the migrated database. This lane is attended: when a shape in the domain map is ambiguous, stop and ask rather than choosing and recording a blocker. No em or en dashes; no dates."

### HH-3.3 Identity: JWKS, org resolution, tenant middleware (Phase B, attended)

Scope: as the frozen brief gives it, minus the migrations, which HH-3.1 now owns. Replace both HS256 issuers with one verifier against the platform JWK Set; add the `X-Org` middleware validating Team membership before any pool is touched; add tenant scoping middleware stamping `tenant_id` from the token; populate and read the identity tables HH-3.1 created, carrying the 13 staff roles and the ERP portal's contractor role set as canonical with HH Pro's set mapped once (`owner` to `account_admin`, `pm` to `buyer_no_pay`, `ap` to `ap_finance`, `field` to `field_crew`); model driver as a role plus a profile row on the same identity; re-express `RequireStaff`, `RequireManager`, `RequireAdmin` and the portal capability guard over the new claims; carry branch grants, the approval rail and the driver session ceiling unchanged. **Plus G3:** reconcile the two org-resolution stories and write the ADR. **Plus G4's middleware half:** `X-Org` is required under staff bearer and rejected under tenant bearer, enforced in the server because the document cannot say it.

Touches: `core/internal/identity`, `core/internal/platform/authz` (new), every `RegisterRoutes` signature naming a guard, `docs/adr/0020-org-resolution.md`.

Acceptance: the RBAC-403 sweep, the anonymous sweeps and the order lifecycle route test pass against JWKS-signed test tokens; a test proves a valid token with a wrong `X-Org` never reaches a repository; a test proves a tenant token cannot read another tenant's rows on any tenant route; a test proves a staff call without `X-Org` is refused and a tenant call carrying one is refused; the ADR for G3 states the decision, the rejected option and what it loses. No migration file is touched.

Human checkpoint: Colton reads the org and tenant resolution path end to end before Phase C starts, because every later lane inherits it.

Prompt: "Replace HardscapeOS's two HS256 JWT paths with JWKS verification against the platform, add `X-Org` and tenant scoping as middleware per `context/FUTURESHADE-CONTEXT.md` section 4.4, and populate the identity tables HH-3.1 created. Reconcile the two org-resolution stories the contract carries, the `X-Org` header and per-org edge serving, into one, and write ADR-0020 recording the decision and what the rejected option loses. Enforce in the server what the document cannot express: `X-Org` required under the staff bearer, rejected under the tenant bearer, with a test for each half. Every existing guard keeps its meaning. You may not add or edit a migration; the schema is frozen. This lane is attended: stop and ask before inventing an auth behaviour the context file does not describe. No em or en dashes; no dates."

### HH-3.2 Module carry and the single binary (Phase C)

Unchanged from the frozen brief. Move every package under `hardscapeos_dibbits/backend/internal` into `core/internal` keeping module boundaries; fold the four binaries into `cmd/core` with `serve`, `worker` and `migrate` roles; remove the vestigial `events.Bus`; add OpenTelemetry; keep `httpx` as the single envelope and convert `Page[T]` to cursor pagination; break the 4,311-line composition root into `wire<Module>` functions without changing a wiring decision. Leave `demo` behind.

Acceptance: `go build ./... && go vet ./...` clean; the untagged unit suite (312 files) passes unchanged; the outbox drains from `worker` and not from `serve`; a test asserts `serve` starts no scheduler. No migration file is touched.

### HH-3.4 Contract conformance and role gating from the document (Phase C)

Scope: as the frozen brief gives it, plus G1's document half and G2. A test that every route registered in `core` is in `core/api/openapi.yaml` and the reverse; generated server types from the document; every handler's gate asserted equal to the document's `x-role`; the tenant routes re-homed from `/api/portal/v1` to `/api/v1/tenant/`. **G1:** add the customer-quote send and acceptance routes, modelled on `GET /api/public/v1/quotes/{token}` and `POST /api/public/v1/quotes/{token}/accept`, so `CustomerQuote` has a surface and the D10 and D12 token ruling has somewhere to bite. **G2:** resolve `GET /api/v1/tenant/config` carrying a tenant-scoped brand on a public route, in the document and in the handler.

Acceptance: the parity test passes against the vendored route list; the recorded fixtures validate; `CustomerQuote` is referenced by at least one operation and its acceptance route carries `x-role: public` with `security: []` and the 404 and 410 semantics the dealer pair uses; `tenant/config` no longer serves tenant-scoped data to an unauthenticated caller; every orphan schema is either referenced or removed, with the choice recorded per entity.

### HH-3.5 Sync endpoints per scope (Phase D, attended)

Scope: as the frozen brief gives it, for all 43 declared scopes, full-mode first. Plus G4's route half: the sync routes accept staff or tenant bearers and handle `X-Org` per the rule the middleware enforces. Six scopes are excepted by D46 and D47: receiving has no column a per-scope feed can use (the receipt number is the per-order GRN event and 0213's feed-sequence addition was a silent no-op there); the messaging trio and channels lack an org or feed column under the derived-membership model; tracking_events has no table in the frozen schema. All six ride to HH-5, their routes keep their tested 501s and their registry rows the declared policies, and the HH-5 brief carries the schema work as one block designed together: the receiving feed column, the messaging-side columns, and the tracking_events table.

Acceptance: contract tests per scope; `GATE-7` generalised to every scope; a property test that two replays of one outbox produce one server state; a test that `manual` never overwrites and always returns the server row; the `SyncScope` enum gate stays green, which it will, since the catalog and the enum now agree at 43\.

Attended (D26): the lane does not run while nobody is at the terminal, and where the per-entity conflict policy cannot be read off `docs/analysis/domain-map.md` it stops and asks rather than choosing and recording a blocker.

Human checkpoint: Colton reads the conflict policy entity by entity before the lane's routes are integrated. What to look for: every entity set to `server_wins` is one where a silent overwrite is acceptable, and every entity where a crew's field edit must survive is `manual`.

### HH-3.6 HH Pro domain rules move server-side (Phase C)

Scope: as the frozen brief gives it, plus G1's behaviour half: the customer-quote send and acceptance lifecycle behind the new routes, with token rotation on revise and the signed acceptance artifact, and acceptance reachable with no account, per D23.

Acceptance: as the frozen brief gives it, plus a test that a homeowner accepts a customer quote with a token and no account, and that a revised quote's old token is dead with the 410 semantics rather than a 404\.

### HH-3.7 The ERP tracking lane (Phase E, standing, attended)

Scope: as the frozen brief gives it, plus D22's obligation. Each migration or route landing on HardscapeOS `master` after the baseline is ported into `core` behind a ledger entry in `core/db/PORTED.md`. **Each port is a human decision of ported or deferred, made by Colton at the time, and the lane records which and why.** This lane is the only one permitted to move the schema freeze marker, and only in a pull request carrying the matching `PORTED.md` entry. **Refreshing `core/api/erp-routes.txt` from the ERP head is an output of this lane**, which is the half of the parity proof that CI cannot do.

Acceptance: the ledger has an entry for every ERP migration above the baseline, each marked ported or deferred with a reason; the vendored route list is current with the ERP head at the end of each run; the parity test passes against it.

## Integrator

Phase A alone, then Phase B alone, then Phase C's three together in the order 3.2, 3.4, 3.6, then Phase D's 3.5 alone on top of them. The frozen brief's expected conflict between 3.3 and 3.4 over `RegisterRoutes` signatures is gone, since B lands before C starts. The remaining expected conflict is 3.6's new modules against 3.4's renamed tenant module, both adding tenant routes; the OpenAPI document is the arbiter. 3.5 lands last, adding sync routes over a settled module set, so it carries no expected conflict of its own.

## Verifier

`ci` green, including the new schema-freeze step and the two gates D22 moved into it. The shape check passes (D20). The unit suite unchanged and green. The integration suite green against a restored HardscapeOS dump plus the additive migrations. The 401 and 403 sweeps green on JWKS tokens. The tenant-isolation test green. The sync property tests green for every declared scope. The recorded fixtures replay green. The ERP's own gate suite still passes against the shared schema. The tracking ledger current.

And, new this Session, the verifier proves the freeze is real: it adds a migration on a scratch branch and shows `ci` fails.

## Human checkpoints

Start: Colton confirms D1 and D3 as recorded so `price_matrix` and the role tables can freeze, and names which restored dump is the fixture.

Phase A end: Colton reads the migration set and signs off. This is the checkpoint the Session exists around.

Phase B end: Colton reads the org and tenant resolution path.

Phase D end: Colton reads the per-entity conflict policy, entity by entity.

End: Colton signs off the ported gate results, the `PORTED.md` ledger, and ADR-0020.

All checkpoints are Colton's per D25.

## Exit test

`core` builds as one binary with three roles. Every ERP route and every tenant route is served under the contract with JWKS auth and org and tenant scoping. Every declared scope has working sync endpoints with idempotent replay. The customer quote has a send route, an acceptance route reachable with a token and no account, and a dead token answers 410\. The ERP's own integration suite still passes against the shared schema. The schema freeze is machine-enforced and proven to fail when violated. The tracking ledger is current with the ERP head, and the vendored route list with it.

## Produces

`core/` conforming; `core/db/SCHEMA-FROZEN`; `core/db/PORTED.md`; the sync package; the plan and customer-quote modules; ADR-0020 on org resolution; ADR amendments where implementation forced a choice; a refreshed `core/api/erp-routes.txt`.

## Unlocks

HH-5 (database convergence), HH-6 through HH-10 (every micro-app reads this core).

## Harness

The frozen brief prefers Claude for HH-3.3, HH-3.4 and HH-3.5 as security-critical, and for the integrator and verifier. HH-2 ran entirely on Kimi with the mitigation that I read the verifier's report before the merge rather than after, and that mitigation caught a blocker and two gates that could not fail. Either routing is defensible here. Attendance is the stronger control on the three lanes that have it, and HH-3.4 is now the only lane that is neither attended nor Claude under an all-Kimi routing, so it is where the harness choice actually bites. Your call, recorded either way in the quota ledger.

No thinking-level line appears in any prompt in this brief. Three HH-2 rounds specified a level and three ledgers reported standard, so the instruction does not take in this Kimi build and asserting it only buys false confidence.

&nbsp;