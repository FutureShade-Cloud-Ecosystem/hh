> Origin: `analysis/DECISIONS.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; the content below this header was unchanged at the time of the copy.
> This copy is the live decisions file from D20 onward; the copy in hh_pro_dibbits is frozen at the close of HH-1.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# HH-1 decisions, as taken by Colton

Recorded at the end of the HH-1 session, in answer to the three decisions `transformation-path.md` section 6 asked for. Each becomes an ADR draft in HH-2.4 with the status `Proposed` and the decision already filled in. Where a decision changes a session file, that file was updated and cites the decision by number.

## D1. Pricing model

One deterministic resolution order, computed by the core and authoritative:

1. A contract price, if one exists, is final and beats everything.
2. Otherwise the base price, then a quantity break that **replaces** the tier rather than stacking on it.
3. Promotions are a separate, time-bounded level that never stacks.

The customer gets the best single price, never a pile of discounts. HH Pro shows a cached price band offline, labelled as an estimate until the core confirms on sync.

What this resolves in `domain-map.md`: C45 (contract finality, the ERP's rule), C43 (replace rather than stack, HH Pro's rule, reversing the ERP's `0026` change), C38 (promotion as a separate never-stacking level), C55 (the core computes; the client's band is an estimate, never authoritative), and by consequence C46 (the margin gate applies inside the core's order), C52 (validity is a property of the confirmed price, so the band's estimate label expires with it) and C53 (the next-break hint is computed by the core under the replace rule).

Implementation note for HH-3 and HH-7: the core's resolver implements this order from HH-3; the running ERP's resolver is untouched until its roles switch in HH-7, and no pricing rule row changes. Where the two resolvers would answer differently for the same customer, product and quantity, HH-7.3's parity test lists the differences as a report for the sales manager before the sales switch, so nobody discovers a changed price at the counter.

## D2. Shell split and first micro-app

Two internal shells, split by work context rather than by app, which keeps the shell-per-audience rule while avoiding a desktop shell that asks every counter user for location permission:

- **HH Work**: office roles. Counter Till, Quote Desk, Dispatch Board, Buying and Stock, Money Desk, Manager Home, Dealer Admin, House Messages. Desktop and tablet.
- **HH Field**: Yard Pick and Drive Run, plus House Messages. Phone-first, camera and GPS permissions, offline full mode.

Counter Till and Quote Desk stay separate micro-apps with a shared quote-to-order entity view, not one tab. Homeowner acceptance renders under HH Pro's brand as a magic-link page with no install. Yard goes first, because it exercises the offline store, the hardest shared package, and does not wait on the pricing decision the way Counter does.

What this resolves in `role-catalog.yaml`: open questions 1 (Drive lives in HH Field, not its own shell), 2 (two micro-apps, shared entity view), 3 (a public magic-link page, no shell install), 4 (HH Pro's brand on acceptance), 10 (Yard Pick and Dispatch stay in different shells; the yard manager installs both). The catalog's `hh-internal` shell has become two shells, `hh-work` and `hh-field` (applied in the PR review revision of `role-catalog.yaml`); `hh-public` stays a web shell with brand `hh-pro`.

## D3. Tenancy and identity

Appwrite Teams are the coarse gate only: org membership and audience (staff, contractor, customer). The thirteen staff roles live in app-role tables in the core keyed by platform user id, because per-membership Team roles would sprawl. The ERP portal's contractor role set (`account_admin`, `buyer`, `field_crew`, `buyer_no_pay`, `ap_finance`) is canonical, since the ERP is the system of record for accounts; HH Pro's roles map onto it (`owner` to `account_admin`, `pm` to `buyer_no_pay`, `ap` to `ap_finance`, `field` to `field_crew`), and a tenant holding the other set is reconciled once by a mapping table, with any extras becoming capabilities. Driver is a role plus a profile row (vehicle, licence), not a separate identity, so one person can be yard staff and a driver.

What this resolves in `domain-map.md`: C29 (the ERP set wins with a one-time mapping), C30 (staff roles as app tables), C31 (no fail-open default: an unmapped user has no app role), C33 (org from the Team, tenant from the token, branch beneath both). In `role-catalog.yaml`: open questions 5 and 6.

## D4. Operators use the launcher (catalog open question 7)

Operators use the launcher and, later, the Shade's operator mode. `operator-console` is a launcher section owned by the launcher session (S3-L), not an `hh` micro-app: it is not a `frontends:` entry in `manifest.yaml`, and the `hh` core exposes the operator reads and writes it needs (the platform key roster, the dealer credential's write-only route, the per-account usage counter) as ordinary org-scoped endpoints in the OpenAPI document. In `role-catalog.yaml` it moves from `micro_apps` to a `launcher_sections` block so its `replaces:` coverage still holds.

Touches: HH-2.2 (no frontend entry), HH-2.3 (operator endpoints in the document), HH-9.4 (the credential half goes to the launcher, as already written).

## D5. The till gets no outbox (catalog open question 9)

Tenders, payments, delivery-date commits and freight raises resolve online, per standing rule 4 in `transformation-path.md`. `counter-till` is therefore read-cache-only: customers, products, the resolved price band, parked tickets, invoices and warranty claims cache for reading; nothing queues. A parked ticket edited with no signal is held in interaction state and written when the signal returns, never through an outbox.

Touches: `role-catalog.yaml` and `microapps/counter-till.md` (offline mode), HH-7.1.

## D6. One canonical order id (catalog open question 12)

After unification there is one canonical order id in `core`. The contractor-side id (HH Pro's `Order.id`, the board card) is kept as an alias column on the unified order for the mapping period, readable and never a key; `fb://hh/order/<id>` carries the canonical id only. The tenant API's order resources expose the canonical id as `id` and the alias as `legacy_contractor_id` until the mapping period closes.

Touches: HH-2.3 (resolved in the OpenAPI document), HH-3.1 (the alias column), HH-10.1 (the board reads canonical ids).

# Decisions D7 to D18, as taken by Colton

Recorded exactly as handed over, each with the conflict it resolves. D7 to D12 are threaded into HH-2.3 and HH-2.4; D13 to D18 into HH-3.1 and HH-3.6.

## HH-2: contract and ADRs

**D7 (resolves C19). Board card versus dealer order.** One canonical `order` aggregate in `core`. The contractor's board card is a read projection, `OrderCard`, defined in the OpenAPI document with an explicit field map and a documented state map from dealer order states to the contractor vocabulary. No second entity, no second writer. The alias column from D6 carries the contractor-side id through the mapping period.

**D8 (resolves C15). Two documents named quote.** Two resources: `dealer_quote` (dealer to contractor, priced by `core` under D1, carries `valid_until`) and `customer_quote` (contractor to homeowner, carries the acceptance artifact and the magic-link token). They share a `quote_line` shape and nothing else. Different senders, lifecycles and acceptance semantics rule out a `kind` field.

**D9 (resolves C25). Invoice shape.** The ERP invoice is canonical: lines, tax, holdback, the AR subledger tie. The portal exposes `InvoiceView`: lines, tax and holdback totals, balance due, payment history. Contractors never see subledger internals; payments post through `core` and the fee rule from HH-3.6 applies.

**D10 (resolves C37). Attachment entity.** `attachment` in `core`: id, `org_id`, nullable `tenant_id`, `kind` (photo, proof_of_delivery, document, signature, product_image), `appwrite_file_id`, bucket, mime, size, checksum, `created_by`, `created_at`, `deleted_at`. Bytes live in Appwrite Storage. A polymorphic `attachment_link` (`attachment_id`, `owner_kind`, `owner_id`, `role`) attaches one file to orders, plans, stops, receiving and quotes without per-owner columns. Offline scopes carry attachments through `attachments_queue` as the offline framework defines.

**D11 (resolves C8). Imagery.** One `product_image` model. An uploaded image is canonical whenever it exists; the measured swatch is stored as an attribute (`swatch_hex`) and rendered only as a fallback, never as an image when a real one exists. The swatch generator stays a script, not a runtime feature.

**D12 (resolves C14). Contractor branding.** A `tenant_brand` record in `core` under tenant scope: display name, logo attachment, primary colour, footer text. Rendered inside the HH Pro frame on customer quotes and the public desk, per D2's ruling that acceptance renders under HH Pro's brand; the dealer's tokens never appear on a contractor's document.

## HH-3: schema and identity

**D13 (resolves C6). Lead time.** `lead_time_days` is a product attribute and the default. `availability` is a computed read model, never stored, with `source` in {attribute, incoming_supply, transfer}; when incoming supply or a transfer estimate exists it overrides the attribute for that quantity.

**D14 (resolves C17). Price validity grain.** Validity is document-level (`dealer_quote.valid_until`), which is the ERP's current rule and what a contractor can reason about. Each line carries a `price_snapshot` with `resolved_at` for audit. Expiry re-blocks the plan at document level. Stock holds are separate objects with their own expiry. D1's estimate label expires with the document.

**D15 (resolves C20). Frozen line versus live reference.** Both, by phase: every line stores `product_id` as the live reference and, at document freeze, a snapshot of the priced fields (sku, description, unit, unit price, tax code). Before freeze the reference renders; after freeze the snapshot renders and the reference is for navigation only.

**D16 (resolves C21). Special-order lines.** A boolean `is_special_order` plus `special_order_description` and an optional `supplier_ref`. The synthetic sku prefix is a legacy encoding; a one-time migration parses it into the boolean and keeps the original sku in the snapshot for traceability.

**D17 (resolves C24). What a contractor sees of a stop.** The minimum that answers "when, and did it arrive": an ETA window, the stop status (planned, en route, delivered, exception), the driver's first name, and the proof of delivery. Never the route, the sequence, or other stops.

**D18 (resolves C26). Card fee.** Surcharge off by default. When enabled for an org it is credit-only, capped by the jurisdiction's rule, and configured with the jurisdiction recorded; the "fee shown equals fee charged" rule from HH Pro holds byte for byte. The flat-percentage model is rejected because it cannot express the cap or the credit-only restriction. Enabling a surcharge requires a recorded jurisdiction and its cap.

## HH-10: the walkthrough surfaces

**D19 (resolves the question HH-10.6 raised: where the audience landing pages live).** The four audience landing pages live in the hh monorepo under the hh-public shell family and its bundle fence, published as a static pre-auth bundle; the Hardscape House marketing site links to them and holds no copy. The fence, the link check that fails a page referencing a recording the runner did not produce, and the recordings themselves therefore stay in one repository, and there is no second surface that can drift from the clips. The alternative, pages in the marketing site repository, is rejected because it splits the link check from the runner that produces what it checks.

Touches: HH-10.6 (scope states the marketing site links in and owns no copy; acceptance gains that no landing page is published outside this repository).

## HH-2: integration

**D20 (resolves the conflict between the lane acceptance and what exists).** `fb check` does not exist and nothing between HH-2 and HH-12 builds it, so `scripts/check-shape.sh` in this repository is the App Shape gate until the platform CLI exists. Every exit test that says "fb check passes" reads "the shape check passes" instead. When `fb` lands, the script becomes a thin wrapper around it and the `docs/BLOCKERS.md` entries recording the substitution are deleted. Two lanes reached this substitution independently, which is what makes it the brief that needs amending rather than the lanes.

**D21 (resolves where the repository lives).** The `hh` monorepo was transferred into the FutureShade org, `FutureShade-Cloud-Ecosystem`, under the `futurebuildai` GitHub account, which also holds the copies of the OSS cloud infrastructure the platform deploys across. The Go module path follows the new owner. HH-2's brief and context section 6 name the old account; both are amended by this decision rather than by an edit to the frozen analysis.

## HH-3: preflight

**D22 (resolves where the contract gates run).** The contract gates run in `ci`, not on a workstation. The App Shape makes the `ci` check the gate and requires route parity to fail the build when code and contract diverge; a gate that runs only where one person happens to be sitting is not a gate. Mechanism: the two sibling-dependent gates take vendored inputs rather than sibling checkouts. `parity.mjs` does not need the ERP repository, it needs the ERP's route list, so that list is vendored as `core/api/erp-routes.txt` behind a provenance header naming the ERP commit it was taken from, and the script reads the file instead of shelling out to `grep` across a sibling. `fixtures.mjs` does not need HH Pro, it needs the sixteen recorded captures, so they are vendored under `core/api/fixtures-live/` behind the same kind of header. Both inputs are snapshots of frozen sources by nature, which is why vendoring them is recording what they are rather than working around what CI lacks. The alternatives are rejected: submodules need deploy keys for two private repositories in Actions and drag an entire ERP tree along for sixteen JSON files; a self-hosted runner makes `ci` depend on one desk being powered on.

Named as a blocker rather than worked around, per the decision: a vendored route list is a snapshot, so `ci` proves contract against snapshot, not contract against the live ERP. The second half of that proof belongs to HH-3.7, the tracking lane, whose acceptance already extends parity to diff against the ERP head. So the gate splits in two, each half running where it can honestly run: `ci` checks contract against snapshot on every pull request, and the tracking lane checks snapshot against ERP head with a human deciding ported or deferred on each change. Refreshing `core/api/erp-routes.txt` becomes a named output of the tracking lane. Until that lane runs for the first time, the snapshot is the ERP at the HH-2 baseline and nothing has moved.

Touches: `core/api/tools/parity.mjs`, `core/api/tools/fixtures.mjs`, `core/api/tools/check-all.mjs`, `.github/workflows/ci.yml`, `docs/BLOCKERS.md` (the workstation-only entry is deleted), HH-3.7's Produces.

**D23 (resolves `public-desk`'s gate).** `public-desk` is token-gated, like `demo-entry` and `hh-site`. The homeowner role is carried by the magic-link token, not by an account the person creates, which follows from D2 putting homeowner acceptance on a magic-link page under HH Pro's brand. `hh-public` stays purely pre-auth and no homeowner ever gets an account.

This corrects the catalog rather than changing direction, and the contract already says so. The `CustomerQuote` schema that HH-2.3 shipped describes the flow in its own words: "Contractor to homeowner (D8): carries the acceptance artifact and the magic-link token ... acceptance is a public magic-link page with no install." The catalog row carrying `roles: [homeowner]` and therefore `gate: role` was the outlier against the contract the same Session produced. The word `homeowner` appears exactly once in the whole contract, in that description, and never as an `x-role`, so the gating vocabulary loses nothing.

Touches: `docs/analysis/role-catalog.yaml` (`public-desk` roles becomes empty, gate becomes token by the generator's own rule), `manifest.yaml` (regenerated), the pre-auth set in `scripts/manifest-catalog.test.ts` grows to three. The catalog copy in `hh` becomes the live catalog from D23 onward, its provenance header amended by one sentence exactly as `DECISIONS.md` was at D20, and the copy in `hh_pro_dibbits` stays frozen at the close of HH-1.

**D24 (resolves what the frozen analysis is for, and where live planning artifacts live).** The copied tree under `docs/analysis/` is history. Its bodies stay frozen, and its provenance header gains one sentence stating that in-body `analysis/...` paths resolve to `docs/analysis/...` in this copy, so a harness handed one of these briefs can resolve what it reads without the freeze being broken. Live planning artifacts do not live there: from HH-3 onward, the session brief a harness actually works against lives at `docs/sessions/HH-<n>.md`, and the frozen copy under `docs/analysis/sessions/` is the version of record for what HH-1 wrote. Where a live brief and its frozen origin disagree, the live one wins and says so in its first line.

Reason: 55 in-body references stopped resolving when the tree moved, while the same files use the new spelling 18 times, so the tree contradicts itself; and HH-3's brief has to change (D22, D23, D25, and four contract gaps) while the freeze has to hold. Rewriting the frozen bodies would break the freeze for real and cost the provenance that makes the copy worth having. This is the cheaper and more honest split: history stays honest, live planning is allowed to move.

Taken by the supervisor rather than by Colton, because HH-3's lanes need resolvable paths and a live brief before they can start, and because it is reversible.

Touches: the provenance header of every file under `docs/analysis/`, a new `docs/sessions/` directory, `AGENTS.md` gains one line naming which tree is which.

**D25 (resolves who performs the human actions).** Until FutureBuild Cloud is deployed and refined, Colton performs every human action in the path: every checkpoint, every sign-off, every design sync. Grant becomes the designer after that. The session files are amended so design-sync checkpoints name Colton, and Q8 (per-role landing default) and Q11 (warranty as its own micro-app) become decisions Colton makes at their checkpoints rather than items pending another person.

Reason: a checkpoint that names someone who is not yet in the path is a checkpoint that will be skipped or silently reassigned, and the plan already carries three of them. Naming the person who is actually there makes the gate real.

Touches: `DECISIONS.md` "Still open, by design" (Q8 and Q11 lose "with Grant"), the HH-6, HH-9 and HH-10 human-checkpoint lines, and any Produces line that assigns a design review.

**D26 (resolves whether HH-3.5 is attended).** HH-3.5 is attended. It implements the per-entity conflict policy, and a wrong `server_wins` where `manual` was meant silently overwrites work a crew did in a yard with no signal, discovered by the crew rather than by a test. That is the same class of failure as a wrong schema, one step further out, so it takes the same control: the lane does not run while nobody is at the terminal, and where a policy cannot be read off the domain map it stops and asks rather than choosing and recording a blocker. Colton reads the policy entity by entity before the lane's routes are integrated.

Phase C therefore carries HH-3.2, HH-3.4 and HH-3.6 only, HH-3.5 runs alone as Phase D, and the tracking lane becomes Phase E. Placing HH-3.5 after the other three rather than before costs one phase and buys two things: the sync lane writes against the final module set HH-3.6 lands, and Colton is free while Phase C runs. HH-3.4 is now the only HH-3 lane that is neither attended nor Claude-preferred under an all-Kimi routing, which is where the harness choice bites.

Touches: `docs/sessions/HH-3.md` (the phases table, the HH-3.5 lane heading and acceptance, the integrator order, the human checkpoints, the harness note).

**D27 (resolves what the freeze on `docs/analysis/` permits, and how a file records that its body changed).** The rule is: no body under `docs/analysis/` changes except where a recorded decision names the file live or names the edit an exception. Two files are live: `DECISIONS.md` from D20 and `role-catalog.yaml` from D23. Two body edits are recorded exceptions: D23's single `roles` line in the catalog, and D25's reassignment of the performer in the checkpoint, design-sync and design-review lines. Three corrections follow. The provenance line asserting that the content below it is unchanged is amended, on each file whose body a decision has changed, to name the decision that changed it. The frozen-tree sentence in `AGENTS.md` gains the same exception clause. And any later body edit under `docs/analysis/` carries a decision number or does not happen.

Reason: the preflight left eight files asserting in their own headers that their bodies were unchanged in the same commit range that changed them, and an `AGENTS.md` sentence saying those bodies are never edited beside two files the same session edited. A freeze documented inaccurately is not a freeze, it is a claim, and the next lane to notice will either stop and ask or decide quietly. Both cost more than the clause does.

Taken by the supervisor rather than by Colton, like D24, because it corrects the documentation of a rule rather than the rule itself, and because it is reversible.

Touches: the second provenance line of `docs/analysis/role-catalog.yaml` and of the seven session files D25 reassigned, and the house rules in `AGENTS.md`.

## HH-3.1: schema and the freeze

**D28 (resolves where the engine reads Team membership when OIDC claims are absent).** No durable Team-membership cache table. If the S2 check finds that Appwrite Team memberships do not surface as OIDC claims, HH-3.3 resolves membership against Appwrite per request and holds the answer in a short-lived in-process cache, so a revocation takes effect within that window with no invalidation path to design and no stale-authorization table to reason about. The schema therefore assumes nothing about the claim in either direction, which is what D3 already requires of it. A durable cache, if load ever justifies one, is an HH-5 migration rather than a table carried unused through four phases.

Stated because the question arose and the word is overloaded: membership here is which organizations a person belongs to, which is coarse authorization. It is not the credential. The credential is the platform JWT verified against the JWK Set, it is required on every non-public route regardless, and nothing caches it.

Touches: `core/db/migrations` (no membership cache table), HH-3.3's org resolution and `docs/adr/0020-org-resolution.md`.

**D29 (resolves how D1's estimate label is proved after the fact).** Every `price_snapshot` records how that line's price was resolved: by the core, or served from a cached band. D1 labels a cached band an estimate and D14 expires that label with the document, and neither is checkable if the only record of it is whichever client drew the screen. With the field, the pre-switch pricing difference report reads it rather than inferring it, which matters because that report is the one a sales manager signs before sales roles move.

Touches: `core/db/migrations` (`price_snapshot` gains the resolution source), HH-3.6's pricing rules, HH-11's pre-switch report.

**D30 (resolves how a synced row carries the id its offline outbox replays with).** The entities the plan flow adds (`plan`, `plan_line`, `customer_quote`, `quote_acceptance`, `attachment`) take client-generatable UUID keys, so a create made in a yard with no signal needs no server round trip and no local-to-server id map in the client. Every synced table, carried and new, gains an idempotency key that the client generates and the server accepts, which is what lets HH-3.5's property test (two replays of one outbox produce one server state) assert something real rather than trust the client to deduplicate.

On the carried ERP tables the column is nullable at the database level and its presence is enforced at the API boundary, never by a not-null constraint, so neither the ERP's own suite nor any writer in the in-flight delivery can be broken by it. The tracking lane records the change in `core/db/PORTED.md`.

Touches: `core/db/migrations`, `core/db/PORTED.md`, HH-3.5's sync routes, the `offline-store` local schema in HH-4.

**D31 (resolves what Phase A runs against while no restored dump exists).** Phase A's fixture is the migrations applied from zero plus the ERP's own seed. The two checks that need a restored HardscapeOS dump are neither skipped nor weakened: that the migrations apply cleanly on top of a restored dump, and D16's requirement that the special-order migration parse every synthetic sku in that dump. Both are recorded as open in `docs/BLOCKERS.md` and are preconditions of Phase B, which does not start until both have been run against a restored dump and reported. The ERP's own integration suite still runs in Phase A against the seeded database, because that is the check proving the additions are invisible to the running application and it does not need production data.

Reason: a schema frozen on seed data alone is frozen on less evidence than its own acceptance line asks for. Saying which half has been proved is cheaper than letting a green suite imply both, and the freeze is the one thing in this Session that cannot be revisited cheaply.

Touches: `docs/BLOCKERS.md`, HH-3.1's acceptance, and Phase B's preconditions.

**D32 (resolves what of the carried baseline's fixture text reaches `hh`'s history).** The baseline's DDL, filenames and version numbers are carried verbatim. Its fixture text is scrubbed of third-party identifying values before the first commit, because migrations are forward-only and this is the only point at which it can be done without rewriting history.

Scrubbed, in comments as well as in SQL values: names of the client's customers, names of individuals, email addresses on live domains, and street addresses. Not scrubbed: the client's own business name, which the repository names already carry and which identifies no third party; branch town names standing alone, which are public business locations; reserved-domain test logins; and fixture identifiers such as `dibbits_truck`, which the ERP's own gate suite depends on. Replacement values are invented and checked not to resolve to a real business or a real street, because a scrub that lands on somebody else's name has not fixed anything.

Why now: the exposure's cost grows rather than holding steady. Rewriting history on a repository with one Session in it is cheap; rewriting it after HH-4 through HH-12, with merged pull requests, a tracker citing commits and a ledger citing blob hashes, is not, and it breaks every citation. Underneath the privacy question there is also a product boundary: `hh` is the product that every later dealer implementation inherits, and under ADR-013 one dealer's customer data belongs in that dealer's implementation repository, not in the baseline all of them carry.

What it costs, accepted: data applied from zero differs from the ERP's, which is correct, because the from-zero path is a development fixture and not a replica, and the restored-dump path is unaffected since these migrations are skipped there by version. And HH-3.7's diff against ERP head shows the scrub on every affected file forever, which `core/db/PORTED.md` absorbs by recording each file's ERP blob hash so the tracking lane compares against the recorded original rather than against the scrubbed text.

Mechanism: the replacement map is built on the workstation, never committed, never reported, and discarded once the proofs pass. Three proofs, all required: the from-zero apply succeeds; the ERP's gate suite passes; and a search for every original literal across `core/db/migrations` returns zero, reported as a count rather than as the literals. The third is what proves no occurrence was missed.

This overrides the live brief's instruction to carry the baseline unedited. That instruction stands for DDL, filenames and version numbers, and no longer stands for fixture text.

Touches: `core/db/migrations`, `core/db/PORTED.md`, `core/db/README.md`, `docs/BLOCKERS.md` (the pending entry is closed by this decision).

**D33 (resolves which line of the ERP is HH's baseline and its tracking target).** `master`. The baseline HH-3.1 carries is `hardscapeos_dibbits` at `6eaea3ec`, the head of `master` and of `origin/master`, holding 196 migrations spanning 0001 to 0212. The `Demo` branch runs ahead of it carrying demo-tenant rebrand, relocation, branch-code, reference-number and portal-customer migrations plus widget and Home shape work, none of which the unified core carries. HH-3.7 tracks `master`, and a change that exists only on `Demo` is not a port decision until it reaches `master`.

A consequence to correct, recorded here rather than fixed here: the commit the preflight named as the ERP head, and from which `core/api/erp-routes.txt` was vendored under D22, is a `Demo` commit sixteen ahead of `master` rather than `master`'s head. The parity gate in `ci` has therefore been proving the contract against a `Demo` snapshot. Refreshing that file from `master` with `refresh-erp-routes.mjs` is HH-3.7's first entry, and the route difference that refresh reports is the measure of how far the snapshot was off. It is not fixed in this lane, because `core/api/` is outside HH-3.1's scope and the tracking lane owns that tool by D22.

Reason: the preflight's prompt named a checkout and not a branch, and that checkout was sitting on `Demo`. Naming the line rather than the directory is what makes the tracking lane's question answerable at all, because "has the ERP moved" has no meaning until "which ERP" is fixed.

Touches: `core/db/README.md`, `core/db/PORTED.md`, `docs/BLOCKERS.md`, and HH-3.7's first port entry.

## HH: the ecosystem reconciliation rulings

Recorded from `HH-RECONCILIATION-RULING-r1`, Colton's ruling on the reconciliation report `hh-reconciliation-report-r1.md` (both held in the session workspace's `context/` directory), which approved the report with amendments, adopted the FB Move draft (r3) as the migration standard's record, and commissioned Phase C. Each decision below is his wording as settled there.

**D34 (messaging).** All HH product messaging, email, text, push and campaigns, sends through Messaging in FB Console behind the bought relays (Resend; Twilio or Vonage). The core holds no mail server and no provider accounts of its own; Postal and Listmonk are cut. Resolves the target shape of the carried direct-Twilio provider (`hardscapeos_dibbits/backend/internal/messagingprovider/`), which the strangler keeps writing until its roles switch.

Touches: HH-3.2's provider carry, HH-9 (House Messages), demo email.

**D35 (the dealer support widget).** Every deployed HH app, plus the public support page, carries a feedback and support widget that flows into that dealer's project in FB Plan, or a general Support project, and it exists before the first go-live. Placement, folded in by the ruling: a shell-chrome component in the design system that every shell inherits, posting to a Function that creates the FB Plan work item with context attached and emails the dealer a reference, the FB Plan key held server-side.

Touches: HH-4 (one implementation in the design system), HH-6 through HH-10 (every shell).

**D36 (no HH Session depends on work outside the path).** Sessions HH-2 through HH-12 depend on nothing in the Factory, the Office, any partner plane, or the parked deliverables (planning objects in TablesDB, the Bun agent tier, the LLM gateway, the Migration Studio). FB Plan carries planning for the whole path. Basis: Colton's decision that the cutover work stays off the Factory until after the cutover. This replaces the report's proposed wording, which named rooms and chat as parked; the partner planes are adopted on their own track, and the Hardscape House team's operating plane is not a Session deliverable. It resolves D4's forward pointer (the launcher section later giving way to a Shade operator mode) and the "until the Shade's planning module exists" phrasing in the platform context: FB Plan carries planning until a recorded decision says otherwise.

**D37 (jobs in the worker role, for the strangler).** Phase C ports the ERP's scheduler and both outbox dispatchers into the `worker` role unchanged in behaviour, per the HH-3.2 work order. Reasons, all from the report's side-by-side: the running ERP keeps writing `job_schedules`, `events_outbox` and `sms_outbox` until HH-11, so a behaviour-compatible port is the mechanism least likely to double-run or strand work while it shares those tables; River could not absorb the outboxes during the strangler in any case; and the port adds no migration, so the freeze holds without an exception. River is not rejected. It is deferred to a post-cutover App Shape question, together with greenfield products that carry no scheduler, and is adopted there only after an end-to-end dive through its repository and documentation. If it is adopted into a carried database later, its tables go in their own schema, which its migrator supports, rather than through `core/db/migrations/`.

**D38 (sign-in for HH product users, the settled parts).** Appwrite Auth issues every HH product user's session, as HH-3.3's JWKS verifier and the HH-4 auth package already assume. The verified email is the join key to the product's own user and role rows. Staff sign-in keeps both gates the ERP enforces today: a verified email inside the company Workspace domain, and an existing active staff row, with no auto-provisioning; the core refuses anything else whatever the upstream provider passes through. Which method each audience uses stays open, tracked to HH-4.2 and decided before it locks the auth package.

**D39 (ERP migrations numbered past the D33 pin).** The ERP's own migrations 0213 through 0219, and any later ones, are ported by HH-3.7 through its existing door, each as a ported or deferred decision, and take the next free numbers in core's sequence. `core/db/PORTED.md` records each ERP number beside its core number. Core's frozen additive migrations are never renumbered. Before HH-5 puts the ERP and the core on one database, HH-3.7 reports how each migration runner records what it has applied, so two histories with overlapping numbers cannot share one tracking table.

**D40 (the Registrar re-pin).** Ratified and split from the fold's combined entry by the Phase C integration review ruling (r1), held at `context/phase-c-integration-ruling-r1.md` in the session workspace, whose section 4(a) is the ratification: the settled registration signature's first parameter becomes the platform Registrar interface, a Phase B input a lane amended and the ruling now makes official.

The Registrar re-pin, applied per the B3 review's items 1 and 2: the one-method registration seam (`Handle(pattern string, h http.Handler)`) is a platform type (`core/internal/platform/httpx`, `registrar.go`), with `type Registrar = httpx.Registrar` left beside the identity shims so real modules never depend on shims for a type; every ported module's exported registrar declares `RegisterRoutes(r Registrar, g *guards.Guards, owned <collaborator>)` as the official interface form, callers passing a concrete `*http.ServeMux` compile unchanged, and every production registration lands inside `registerRoutes` (`core/cmd/core/main.go`), never on a privately constructed mux, with the ServeMux duplicate-registration panic as the shim-removal backstop.

Touches: `core/internal/platform/httpx/registrar.go`, every carried module registrar, `core/cmd/core` (registerRoutes), `docs/analysis/DECISIONS.md`.

**D41 (the driver session ceiling's no-fallback read).** Ratified and split by the same ruling, whose section 4(b) is the ratification, given knowing the consequence: an unreadable dispatch_policy row locks drivers out rather than giving them a working session, a user-visible behaviour change during the strangler that is Colton's yes, not a lane's.

The driver session ceiling's knob read has no fallback arm, upholding B2-07 against the ERP's behaviour: the ceiling reads the `dispatch_policy` dealer-default row at enforcement time and refuses on every uncomputable arm (the 503 family), where the ERP's login-time read fell back to the twelve-hour constant on every error. Authority is B2-07, whose operative sentence is "the default arm refusing when the check cannot be computed"; no later change may reintroduce a fallback on this read.

Touches: `core/cmd/core` (the knob wiring), `docs/analysis/DECISIONS.md`.

**D42 (the position-cursor convention).** Ratified and split by the same ruling, whose section 4(c) finds it minor and correctly recorded.

The yard pending-tickets feed's cursor is a position token, not a row key, recorded as that endpoint's convention difference in `core/api/CONVENTIONS.md` (the Pagination section's position-cursor sentence); HH-3.4's conformance work meets it recorded, and a row key there would be a seam redesign with its own review, ordered above both lanes.

Touches: `core/api/CONVENTIONS.md` (the Pagination section's position-cursor sentence), `docs/analysis/DECISIONS.md`.

**D43 (no write-back to BisTrack; the parallel run is a nightly reload).** Recorded from `HH-BISTRACK-CUTOVER-RULING-r1` (held at `context/hh-bistrack-cutover-ruling-r1.md` in the session workspace), Colton's answers in chat to the dependency map's questions. Nothing on the HH path writes to BisTrack, at any phase. The parallel run loads a recent BisTrack backup into a Dibbits staging org of the HH core every night, through the FB Move stages (land, profile, map, validate, load, reconcile), so Dibbits staff see their real operations in HH while they keep working in BisTrack. The parallel run is read-only against BisTrack and replaces its target nightly; nothing entered in the staging org is kept. The nightly run is an operator job conducted by FB Connect, per the overlap rule. The carried design in `core/internal/bistrack` that pushes to BisTrack during a shadow phase, and the "rollback is free until November" promise written into it, are retired by this decision. The module stays the fail-closed stub it is today, and the first lane that touches it removes the push half and that sentence.

**D44 (the cutover is a single switch by default).** Recorded from the same ruling. HH-11 cuts over in one switch: a final BisTrack backup, a final FB Move load into the production `dibbits` org, reconcile and sign-off, then BisTrack goes read-only for reference. BisTrack is Dibbits' only production system of record until HH-11; the legacy ERP is the product lab and never goes to production, and HH-11 cuts over from BisTrack to HH, not from the ERP to HH (the ERP is retired at HH-12 as a lab, with no production cutover of its own). A staggered cutover is allowed only along a seam where, with no write-back (D43), no data diverges. Safe to stagger: capabilities BisTrack never held, such as HH Pro for contractors, customer quotes to homeowners, and the walkthroughs; these can go live ahead of the switch because BisTrack has nothing to stay in step with. Not safe to stagger: any role or branch whose records BisTrack still holds for someone else; once one side writes in HH and the other in BisTrack with no write-back, the two copies drift apart the same day. Any proposed stagger is named in HH-11's live brief with its seam and is Colton's to approve. This replaces the role-by-role style pinned for HH-11 under the FB Move draft.

**D45 (the D32 baseline gap in two carried seed values; the port retires the value).** Recorded from Colton's answers in chat holding the HH-3.7 first-run pull request on one condition. The D32 scrub of the carried baseline replaced the personal identifier's first token only: in the seed values carried at `0032_gl_module.sql:55` and `0035_nsf_credit_hold.sql:63`, a real surname remains beside the patterned placeholder token, beside a dash character and workstream text the scrub classes never covered. The finding is recorded against both files in `core/db/PORTED.md` by reason class only, never the value. The baseline files are never edited and history is never rewritten: frozen migrations stay byte-for-byte as carried, and the text remains in git history as a known exposure in a private repository. The port of the ERP's 0217 (core 0220) retires the value from live data by rewriting both seeded rows to the ERP's clean replacement wording, matching the rows with LIKE predicates that anchor on the placeholder token and the workstream code and carry wildcards where the surname and the dash sit, so no new core file carries the value.

Touches: `core/db/PORTED.md` (scrub record and port ledger), `core/db/migrations/0220_a_finance_note_a_client_reads_names_nobody.sql`, HH-3.7's first run.

**D46 (receiving rides to HH-5; no cursor from the receipt number).** Recorded from Colton's answers in chat ruling on the HH-3.5 yard-pick stop condition (DECISIONS.md is live for this entry under D27). The condition found: `purchasing_receipts` carries no column a per-scope sync feed can use. The table's own `seq` is the per-order receipt event number (unique with `po_id`, embedded in the human GRN a person reads), and 0213's additive feed-sequence column was a silent no-op there because the name already existed; reusing the receipt number as a cursor would break since-cursor pulls (per-order numbers repeat across orders), and renumbering it would break the GRN. Two options were put: A, open a narrow door inside HH-3 for one core-authored additive migration, a receiving feed-sequence column; B, let receiving ride to HH-5. The ruling is B. The two receiving routes keep their tested 501, the registry row keeps its declared manual policy, and the receipt number is never reused as a cursor and never renumbered. The additive feed-sequence migration is the first item of the HH-5 brief.

Touches: the sync scope registry (receiving, manual, unwired), the 501 route test, `docs/sessions/HH-3.md` (Phase D scope), HH-5's brief.

## Carried as verification, not decided

Whether Appwrite Team memberships surface as OIDC claims stays a S2 check. D3 stands either way: if claims are absent, a Function enriches userinfo and the engine resolves the coarse gate from Team membership by lookup.

## Still open, by design

- Q8 (per-role landing) at HH-6's mid checkpoint with Colton; default is the switcher seeded from the served row.
- Q11 (warranty micro-app) at HH-9's design sync with Colton.
- The state of the delivery's BisTrack phase at HH-11's start checkpoint.
- The sign-in method per audience (staff, contractors, homeowners) at HH-4.2, per D38; the ruling's proposal to confirm there: staff by Google as built, contractors by an email link with Google allowed where the account matches, homeowners by the magic link D2 specifies.
