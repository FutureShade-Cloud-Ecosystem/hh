> Origin: `analysis/transformation-path.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.

# HH-1 L6: the transformation path

From two applications (HardscapeOS, the dealer ERP in Go and Lit; HH Pro, the contractor portal in React over a framework-free core) to one App Shape monorepo `hh` with a unified Go core, role-based micro-apps in audience shells, and declared offline scopes. Every session below is its own executable swarm brief under `sessions/`, written to be handed to a harness unchanged.

Built from `hhpro-inventory.md` (L1), `hardscapeos-inventory.md` (L2), `domain-map.md` (L3), `role-catalog.yaml` and `microapps/` (L4), and `gap-analysis.md` (L5). House rules: no em or en dashes; no durations, dates or calendar references; no secrets; the in-flight HardscapeOS delivery is a dependency to sequence around, never something to pause, freeze, rewrite or estimate.

---

## 1. The shape of the path in one paragraph

The core is the ERP's Go monolith carried into the shape with additive retrofits, plus the HH Pro domain rules that belong server-side. The strangler runs by role, not by screen: both applications write one org database from HH-5 onward, each role moves completely to its micro-app in its own session, and the ERP goes read-only for that role with a sentence naming the new app. The ERP cutover (HH-11) is the removal of the second writer, and it sits after the delivery's own BisTrack phase because it replaces the application over the same data, never the system of record. The in-flight delivery is tracked, not paused: a standing lane ports every migration and route it lands into `core` behind a ledger from HH-3 until the cutover closes it. Contractors move in HH-10 onto a standalone brand-forward shell; the export proof and decommission follow the cutover; Gable repeats the sequence with the shared packages already built.

---

## 2. Dependency graph

The ERP cutover session is **HH-11**. Every other session is placed relative to it, with the reason.

```
HH-1 analysis (this)
  |
  v
HH-2 skeleton and contract ---------------------------+
  |                                                    |
  +----> HH-3 unified core (+ tracking lane 3.7) ----> |    HH-4 shared packages
  |                                                    |      (parallel with HH-3)
  |                                                    v          |
  +------------------------------------------------> HH-5 database convergence and seeds
                                                          |
                                                          v
                                                     HH-6 internal shell + yard-pick  (first role moves)
                                                          |
                                                          v
                                                     HH-7 counter-till + quote-desk
                                                          |
                                             +------------+------------+
                                             v                         v
                                     HH-8 dispatch, drive,      HH-10 hh-pro shell + public-desk
                                          buying-and-stock            (needs HH-7 quotes, HH-8 stops)
                                             |                         |
                                             v                         |
                                     HH-9 money, manager,              |
                                          messages, dealer-admin       |
                                             |                         |
                                             +------------+------------+
                                                          |
                          [delivery's own BisTrack phase] |   (external dependency, sequenced around)
                                                          v
                                                     HH-11 ERP CUTOVER
                                                          |
                                                          v
                                                     HH-12 fb export proof + decommission
                                                          |
                                                          v
                                                     G-1 .. G-4 (Gable, section 5)
```

| Session | Position relative to HH-11 | Reason |
|---|---|---|
| HH-2 skeleton and contract | upstream | Creates the target; touches neither running product. The OpenAPI document is authored from the ERP's registered routes and the portal BFF before any core code. |
| HH-3 unified core | upstream | Carries the ERP modules and migrations into `core` with additive retrofits, runs beside the ERP against a restored copy on a preview database with test-generated keys (no S0 or S2 dependency), and starts the tracking lane that keeps `core` current with the delivery until HH-11 closes it. |
| HH-4 shared packages | upstream | Pure library work; every micro-app depends on it; nothing deployed. Parallel with HH-3. |
| HH-5 database convergence and seeds | upstream, the pivot | Both applications must write one org database before any role can move. The only step that touches the delivery's environment, and it touches a connection target only, with the previous target kept warm. Explicit platform preconditions: **S0** (Managed Postgres, the `dibbits` org database) and **S2** (Appwrite identity, real JWKS tokens). |
| HH-6 HH Field shell and yard-pick | upstream | First strangler step: the yard roles move (Yard first per D2, full offline mode); every other role keeps working in the ERP. |
| HH-7 HH Work shell, counter-till and quote-desk | upstream | Sales roles move; builds the office shell (D2) and needs the pricing decision (D1) implemented in `core`. |
| HH-8 dispatch, drive, buying and stock | upstream | Logistics, driver and procurement roles move; needs orders from HH-7 and the yard scopes from HH-6 as its offline template. |
| HH-9 money, manager, messages, dealer-admin | upstream, the last internal step | Finance, leadership, HR and admin roles move; needs the invoices, bills and approvals HH-7 and HH-8 write. After it, every internal role is on the shell. |
| HH-10 hh-pro shell and public-desk | upstream | Contractors move; needs the tenant API (HH-3), the quotes the desk produces (HH-7) and the stops the field app tracks (HH-8). Parallel with HH-8 and HH-9 once HH-7 lands. |
| HH-11 ERP cutover | **the cutover itself** | Removes the second writer. Requires every role moved (HH-6 to HH-10) and sits after the delivery's own BisTrack phase, whose sync responsibility it carries into `core`'s `worker`. |
| HH-12 fb export proof and decommission | downstream | Nothing here can begin while a second writer exists, and nothing here is needed for any role to work. |
| G-1 to G-4 | downstream | Gable repeats the sequence with the shared packages present (section 5). |

**Where the platform sessions sit.** S0 (Managed Postgres, the `dibbits` org database) and S2 (Appwrite identity, real JWKS tokens) are external preconditions of HH-5 and of every session after it. HH-2, HH-3 and HH-4 need neither: HH-3 runs on a preview database with test-generated keys and HH-4 tests its auth package against a fake credential provider, so the path starts before the platform is up and converges on it at HH-5.

**Where the in-flight delivery sits.** It is not a node in this graph because it is not a session of this path; it is an external dependency with two touch points. The tracking lane (HH-3.7) reads its `master` continuously and ports what lands. Its own last phase (the BisTrack parallel run and cutover, `hardscapeos-inventory.md` section 14.5) is upstream of HH-11: if it completes first, HH-11.2 carries the sync module as archive tooling; if it is still open, HH-11.2 runs the push and pull from `core`'s `worker` under the module's frozen seam and direction data. Either way the delivery's code, release cadence and repository are never edited by this path; the only change it experiences is the connection target in HH-5, made by its operator from a runbook with a rehearsed rollback.

---

## 3. The sessions in order

Each file follows the required format exactly (goal, preconditions, position, harness, lanes with scope, touches, acceptance and a paste-ready prompt, integrator, verifier, human checkpoints, exit test, produces, unlocks).

| ID | File | What it delivers |
|---|---|---|
| HH-2 | `sessions/HH-2-skeleton-and-contract.md` | The `hh` repository in the App Shape layout; `ci` and `fb check` green; `manifest.yaml` generated from the catalog; `core/api/openapi.yaml` authored from the ERP routes and validated against HH Pro's recorded fixtures; four ADR drafts (pricing, tenancy and identity, shell split and first micro-app, extensions and Postgres version); the analysis moved in. |
| HH-3 | `sessions/HH-3-unified-core.md` | `core/` as one binary with `serve`, `worker`, `migrate`; the 196 ERP migrations as baseline plus additive `org_id`, `tenant_id`, `seq`, tombstones and the HH Pro entities; JWKS auth with `X-Org` and tenant middleware; contract conformance and role gating from the document; sync endpoints per declared scope; HH Pro's stage, expiry, acceptance and payment rules server-side; the standing tracking lane and its ledger. |
| HH-4 | `sessions/HH-4-shared-packages.md` | `design-system` (one token vocabulary, five kits, five states, both measured-contrast gates), `auth` (device unlock, in-memory token custody), `offline-store` (SQLite and wa-sqlite backends, the ERP's outbox rules and HH Pro's persistence rules carried), the `sync` statechart with model-based tests as the gate, the generated `api-client` with the recorded-fixture contract tests; the `guided-tour` pattern and the tour statechart convention with its model-based harness and headless runner. |
| HH-5 | `sessions/HH-5-database-convergence-and-seeds.md` | The rehearsed move of the ERP database onto the platform org database with checksums and a rehearsed rollback; HH Pro's scenario as tenant seed priced through `core`; `db/seed/demo.sql` and the anonymiser for previews; the readiness report and the per-role read-only switch; the per-tour seed slices; the `impl-hh-demo` implementation repository. |
| HH-6 | `sessions/HH-6-internal-shell-and-yard-pick.md` | `hh-field` (web and Tauri, phone-first, camera and GPS, per D2) with identical chrome, switcher, per-role landing, deep links and device unlock; `yard-pick` in full offline mode; the yard scopes proven on two devices; the yard roles switched with a rehearsed rollback; the strangler ledger opened. |
| HH-7 | `sessions/HH-7-counter-till-and-quote-desk.md` | `hh-work` (the office shell, desktop and tablet, per D2); `counter-till` (money never queues) and `quote-desk` (resolved price bands cached, never rule inputs) with the shared quote-to-order entity view; HH Pro's two pricing-parity defences generalised to `core` under D1; the sales roles switched with app-rung and ERP-rung sales proven to post identically; the `hh-demo` org provisioned with its six guardrails; the counter-to-yard walkthrough recorded; the public entry page with role cards and QR codes in a new `hh-public` shell. |
| HH-8 | `sessions/HH-8-dispatch-drive-buying.md` | `dispatch-board`, a durable `drive-run` (proof of delivery queued at last, the bundle fence carried), `buying-and-stock`; the logistics, driver and procurement roles switched; the dispatch-to-delivery walkthrough recorded. |
| HH-9 | `sessions/HH-9-money-manager-messages-admin.md` | `money-desk` (online-only, house-zone tripwire extended), `manager-home`, `house-messages` on the engine hub, `dealer-admin` merging both admin surfaces with credentials moved to the launcher; the office roles switched; the internal ledger complete. |
| HH-10 | `sessions/HH-10-hh-pro-shell-and-public-desk.md` | The `hh-pro` standalone shell (Tauri and PWA) with `pro-procurement`, `pro-pay`, `pro-field`, and `public-desk` as a magic-link page under HH Pro's brand (D2); the cached band labelled an estimate until sync (D1); tenants mapped per D3 and switched; the sim deployment retired reversibly; the contractor-to-homeowner walkthrough recorded and the four audience landing pages built from the recordings. |
| HH-11 | `sessions/HH-11-erp-cutover.md` | The final tracking reconciliation; BisTrack responsibility into `worker`; the global read-only switch and the write-authority handover with the readiness report before and after; the observed job per micro-app; the three walkthroughs run as release checks against the `dibbits` staging restore before and after the switch; the ledger closed. |
| HH-12 | `sessions/HH-12-fb-export-proof-and-decommission.md` | The export proof outside the platform, brought up from `impl-hh-demo` with the three walkthroughs as its one-job-per-micro-app evidence; decommission of both old deployments and archive of both repositories, with `hh-demo` retained; the Gable handover. |

The context file's section 8 names the HH stretch as HH-1 to HH-5. This path expands those five labels to eleven sessions because the brief requires each of the strangler's role moves and the export proof to be a separately executable brief; the mapping is HH-2 and HH-3 for the context's HH-2 (skeleton and core), HH-4 for its HH-3 (packages), HH-5 to HH-10 for its HH-4 (shells and micro-apps), and HH-11 and HH-12 for its HH-5 (cutover and export).

---

## 4. Standing rules across every session

1. **Tracking, not pausing.** HH-3.7 runs in every session from HH-3 to HH-11.1. It ports; it never asks. Its ledger (`core/db/PORTED.md`) is part of every verifier's checklist.
2. **Additive only until HH-11.** No migration between HH-3 and HH-11 may drop, rename or narrow anything the ERP reads; the ERP's own integration suite is run against the shared schema in every session's verifier to prove it.
3. **A role moves whole.** A role's micro-app takes every job that role has, the ERP goes read-only for that role with the naming sentence, and the switch has a rehearsed rollback. The strangler ledger holds one row per role in `role-catalog.yaml` and is closed only in HH-11.4.
4. **Money never queues.** A tender, a payment, a delivery-date commit and a freight raise resolve online because their gates resolve server-side; the till has no outbox at all (D5). Reads cache; the outbox carries everything else.
5. **Never render a confident wrong number.** Carried from HH Pro's rules into every kit: an unknown total is a dash, a stale measure shows its staleness, a queued action never shows as done.
6. **The refusal sentence is the contract.** Every guard in `core` returns a contractor-facing or staff-facing sentence, and the board drop, the micro-app, and the agent tool result all show the same words.
7. **Security-critical lanes on Claude.** Identity, gating, sync, statecharts, money paths and the cutover itself; breadth lanes on Kimi or Kilo; integrator and verifier on the strongest available model, per the routing note in context section 7.
8. **No secret anywhere.** Test keys are generated in tests; configuration arrives as environment at spawn; no route returns a credential; the export bundle is scanned.

---

## 5. The same sequence mapped onto Gable

Gable is the LBM (lumber and building materials) vertical: the Gable ERP is the `gable` product repository, and its contractor portal is LumberNow, the repository HH Pro was forked from (`hh_pro_dibbits/CLAUDE.md`, "This repo is a fork of LumberNow"; the two share no git ancestry, so every port is a patch by hand, and the parts worth keeping in sync are the framework-free `src/core` rules). The Gable sequence assumes the shared packages and the `hh` core patterns exist, and it exposes the capability flags the implementation model needs so that each dealer (Dibbits included, as `impl-gable-dibbits` if the LBM product ever serves that org) is a thin implementation repository rather than a fork.

| Gable session | Mirrors | What changes because the packages exist |
|---|---|---|
| G-1 analysis, skeleton and contract | HH-1 and HH-2 | The inventory lanes run over the Gable ERP and LumberNow with this session's lane prompts reused verbatim (paths substituted). The skeleton starts from the `hh` repository's layout rather than the template, `web/packages` are consumed as published packages rather than built, and the OpenAPI document is authored from the Gable ERP's routes with LumberNow's supplier port as the tenant contract test. The pricing ADR is re-derived for LBM (LumberNow's tier, category-cascade, contract and volume-break model is the same shape HH Pro carries), and the capability manifest is drafted from the start because Gable is the product the implementation model was written for (context section 5). |
| G-2 unified core and database convergence | HH-3 and HH-5 | The Gable ERP's modules and migrations carry with the same additive retrofits; the tracking lane runs against the Gable ERP's `master`; convergence puts the Gable ERP and the `gable` core on one org database per dealer. The `citext` and Postgres-version ADR is reused, not re-decided. Capability flags gate every module a dealer may switch off. `impl-gable-demo` is created here from the same implementation model that produced `impl-hh-demo` (HH-5.6), with every Gable capability switched on, brand `gable`, the seed pointed at the LBM demo scenario, integrations stubbed and payments in test mode; the per-tour seed slices are re-cut for LBM stock. |
| G-3 shells and micro-apps by role | HH-6 to HH-10 | The internal shell, the kits, the offline store, the sync statechart and the auth package are imported; Gable's role catalog is derived with the L4 lane prompt and the same fifteen micro-app shapes are expected to recur with LBM vocabulary (a framing package instead of a paver pallet; the same yard-first order). LumberNow becomes the standalone public shell the way HH Pro did, with its `src/core` rules ported server-side once (the two portals' domain rules are already meant to be kept in sync). The three walkthroughs are re-scripted here in the same convention and run by the same headless runner: counter to yard, dispatch to delivery, and contractor to homeowner, with LBM vocabulary throughout (a framing package rather than a paver pallet, a lift of studs rather than a pallet of pavers, a lumber yard's pick rather than a hardscape yard's). The `gable-demo` org is provisioned from `impl-gable-demo` once the micro-apps the first walkthrough crosses exist, under the same six guardrails, and the audience landing pages are re-cut for the LBM audiences. |
| G-4 cutover, export proof and the community org | HH-11 and HH-12 | The Gable ERP cutover per dealer org follows the same runbook; the export proof is run once for the product; and because Gable is the community product, its org (`gable`) stays shared while each dealer runs in its own org with its own implementation repository and `fb upgrade` bumping `PRODUCT_TAG` (context section 5). G-4 also writes ADR-012 (Gable migration onto the App Shape) from this path, which the context file lists as "to be written from HH-1's path". The walkthroughs run as release checks before each dealer's cutover step exactly as HH-11.3a runs them, and the export proof is brought up from `impl-gable-demo` with the three walkthroughs as its one-job-per-micro-app evidence, as HH-12.4 does for `hh`. |

What Gable does **not** repeat: the shared-package sessions (HH-4), the design-system merge (its tokens are a brand override on the same base), the offline-store and statechart work (its yard and driver scopes declare the same policies), and the walkthrough machinery, since the `guided-tour` pattern, the tour statechart convention and the headless runner were all built once in HH-4.6 and HH-4.7 and are imported. What Gable does repeat, and must: the tour definitions themselves, because a walkthrough is a statechart over real screens with real guards, and an LBM counter converting a framing package is a different set of steps from a hardscape counter converting a paver pallet even when the shape of the story is identical. Re-scripting is therefore new definitions and new seed slices in the same convention, never a find and replace over the `hh` files. What Gable adds: the capability manifest as a first-class artifact from G-1, the per-dealer org provisioning as a runbook in G-2, and the implementation-repository template in G-4, which `impl-gable-demo` is the second instance of.

---

## 6. The three decisions the path needed, now taken

All three were taken by Colton at the close of HH-1 and are recorded in full in `DECISIONS.md` (D1, D2, D3). HH-2.4 turns them into ADR drafts with the decision filled in. In summary, and with what each gated:

1. **The pricing model (D1)**: a contract price is final and beats everything; otherwise base price, then a quantity break that replaces the tier; promotions a separate time-bounded level that never stacks; the core computes and is authoritative; HH Pro's cached band is labelled an estimate until sync confirms. Resolves `domain-map.md` C38, C43, C45, C55 and, by consequence, C46, C52, C53. Unblocks HH-3's `price_matrix` freeze and HH-7's band.
2. **The shell split and the first micro-app (D2)**: two internal shells split by work context, HH Work (office roles, desktop and tablet) and HH Field (Yard Pick and Drive Run, phone-first, camera and GPS, offline full); Counter Till and Quote Desk stay separate micro-apps with a shared quote-to-order entity view; homeowner acceptance is a magic-link page under HH Pro's brand with no install; Yard goes first. Resolves `role-catalog.yaml` open questions 1, 2, 3, 4, 10. HH-6 builds HH Field, HH-7 builds HH Work.
3. **Tenancy and identity (D3)**: Appwrite Teams as the coarse gate only (org membership and audience); the thirteen staff roles in app-role tables keyed by platform user id; the ERP portal's contractor role set canonical with HH Pro's roles mapped onto it once by a mapping table, extras becoming capabilities; driver a role plus a profile row, not a separate identity. Resolves `domain-map.md` C29, C30, C31, C33 and `role-catalog.yaml` open questions 5 and 6. Unblocks HH-3.3 and every role switch.

---

## 7. Environments and orgs

Every session above builds for an org. There are four, and the order they first exist in is part of the path: a role cannot move onto a micro-app that has nowhere to run, and a prospect cannot be shown a walkthrough of a product that has not yet grown the screens the walkthrough walks.

| Org | Purpose | Data | Access | First exists in |
|---|---|---|---|---|
| `hh-staging` | Internal QA of every HH session's `qa/<session>` branch | Anonymised seed (HH-5.3), reset on each deploy | Operators and reviewers | S2, as the first org provisioned after `fb` |
| `hh-demo` | Public walkthroughs and sales demos | The demo scenario seed (HH-5.2 and HH-5.5), reset on a schedule and on demand | Prospects through expiring demo sessions issued by role; operators for guided demos | HH-7.5, once the yard, the counter and quotes exist |
| `dibbits` | The client's production instance | Real, converged onto the platform org database in HH-5.1 | Dibbits staff and contractors | HH-5, and every role switch from HH-6 onward lands here |
| Branch previews | "Try this change" per pull request | Seed only (`db/seed/demo.sql`, HH-5.3) with a bounded life | Reviewers, and members through the Shade once it exists | S2 (Sites previews) and the Coolify pull request previews from S1 |

All four are ordinary orgs on the platform, and `hh-demo` is deliberately the least special of them: it is an implementation repository, `impl-hh-demo`, built from the client implementation model in context section 5 (a `PRODUCT_TAG`, an org manifest with every capability switched on, brand `hh`, the seed pointed at the demo scenario, outbound integrations stubbed and payments in test mode, an `OVERLAYS.md` ledger that stays empty). The demo is therefore provisioned by the same runbook as a client, upgraded by the same `fb upgrade`, and cannot drift from the product: a walkthrough that passes against `impl-hh-demo` is a walkthrough of the shipped thing.

Six guardrails hold that org, and each one is an acceptance line in a lane rather than a note here: a visible demo banner in every shell, rate limits at the edge, outbound integrations stubbed with email and push routed to a sink, expiring demo sessions with no signup, a reset that runs both on a schedule and on demand from the launcher, and no export and no intake, so nothing leaves the org as a bundle and the project brain never reads it.

Preconditions are unchanged by any of this. S0 to S2 stand before `hh-staging`; `hh-demo` waits on `impl-hh-demo` (HH-5.6) and on the micro-apps its first walkthrough crosses (HH-6 and HH-7); nothing in this section touches the in-flight HardscapeOS delivery, changes a precondition of HH-5, or adds a writer to the `dibbits` database.
