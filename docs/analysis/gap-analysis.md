> Origin: `analysis/gap-analysis.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.

# HH-1 L5: shape-fit and gap analysis

Distance from the App Shape (context file sections 4.1 to 4.6), the multi-frontend and offline
framework (section 4.5), and the client implementation model (section 5), measured against what the
two repositories actually contain. Every claim below is drawn from the four lane inventories in this
directory and cites the repository path they cite. Where the two inventories disagree with a
repository's own documentation, the code wins and the divergence is named.

House rules: no em or en dashes; no durations, dates or calendar words; no secrets; paths on every
claim; the in-flight HardscapeOS delivery is a dependency to sequence around.

Vocabulary used in the verdict columns:

- **moves**: the artifact carries into the `hh` monorepo substantially as it is, with a mechanical
  retrofit at most (a column added, a package path changed, a test rehomed).
- **rewritten**: the capability survives; the code that provides it does not.
- **dropped**: neither the code nor the capability carries; the shape provides it or forbids it.
- **decision**: the verdict depends on an answer the path must get before the session that needs it.

---

## 1. Runtimes (context 4.1)

| Runtime | The shape requires | HardscapeOS has | HH Pro has | Verdict |
|---|---|---|---|---|
| Server | Go, one modular monolith binary with roles `serve`, `worker`, `migrate`; pgx; sqlc; forward-only SQL migrations run by the platform; OpenAPI as the contract; OpenTelemetry built in | Go modular monolith, pgx, hand-written repositories, 196 forward-only numbered migrations applied by a **separate** binary (`backend/cmd/migrate`), a scheduler and an outbox dispatcher that run **inside** the server process (`backend/internal/jobs/jobs.go`, `backend/internal/notify/dispatcher.go`), **no** sqlc, **no** OpenAPI document anywhere in the tree, **no** OpenTelemetry (`hardscapeos-inventory.md` sections 1, 2.3, 22) | no server of its own: a Vite dev middleware and an admin API in TypeScript (`server/claude-proxy.ts`, `server/admin-api.ts`), and a static production shape with no process at all (`deploy/RUNBOOK.md` section 0) | **moves** for the ERP's Go modules and migrations; **rewritten** for the entry point (three binaries become one with three roles), the query layer where sqlc is adopted, and the contract (OpenAPI authored from the 551 registered routes, `hardscapeos-inventory.md` section 2.6); **dropped** for HH Pro's `server/` |
| Data | PostgreSQL 18, one database per org per app; extensions limited to pgvector, pg_trgm, PostGIS | PostgreSQL 16 (`docker-compose.yml`), one database, no `org_id` anywhere (`domain-map.md` section 3.8), **`pgcrypto` and `citext` enabled** (`backend/migrations/0001_extensions.sql:5-6`) and `CITEXT` used across 51 migration files | `localStorage`, twelve stores, a schema version with a reseed-on-miss policy (`hhpro-inventory.md` section 12) | **moves** for the schema as the baseline of the `hh` org database; **decision** on `citext`, taken in the PR review: it is added to the shape's extension allowlist as a standard contrib extension that 51 baseline migrations depend on, recorded in the HH-2.4 extensions ADR together with the major-version move, rather than rewriting the baseline; **dropped** for HH Pro's browser store as a system of record (it becomes the offline projection in the offline-store package) |
| Web | Lit plus Ionic, Bun plus Vite, Zag, PWA-capable, static bundles at the platform edge | Lit 3 and Vite, **npm** not Bun, no Ionic, no Zag; six Vite entries; a manifest and icons for the driver entry but no service worker (`hardscapeos-inventory.md` sections 16.2, 18) | React 18, react-router, Tailwind v4, Radix, dnd-kit, Vite, npm; a framework-free `src/core` enforced by a Biome override and an architecture test (`hhpro-inventory.md` section 2) | **moves** for the ERP's Lit elements (rehomed per micro-app, Ionic chrome around them); **rewritten** for HH Pro's `src/ui` (React to Lit plus Ionic), which HH Pro's own architecture rule anticipated ("only `src/ui/` is rewritten", `hh_pro_dibbits/CLAUDE.md`); package manager change on both |
| Shell | Tauri 2, one shell per audience | none | none | **new**: `tauri/internal` and `tauri/hh-pro` are net-new; `role-catalog.yaml` `shells:` names four, of which two are Tauri shells, one is a token-credentialed public web shell and one is the launcher |
| Agents | statecharts package; agent seats provisioned by the platform; runners via ACP behind the Shade | `assist` and `quoteassist` modules (a read-command registry and an extractor that may never name a product, `hardscapeos-inventory.md` section 13) | an in-browser agentic loop over the Anthropic SDK with three tool tiers and a commit gate (`src/core/ai/session.ts`, `hhpro-inventory.md` section 10) | **rewritten**: the tool registry and the commit-gate rule move to `agents/` as seat configuration and to `statecharts/` as an approval machine; the browser-held loop is dropped (secrets must never reach a browser, context section 11) |
| Realtime | engine hub (WebSocket plus LISTEN/NOTIFY) for app streams; Appwrite Realtime for planning objects only | a server-sent-event stream on the messaging module (`hardscapeos-inventory.md` section 11, the messaging route list); the outbox dispatcher is a polling loop | none | **rewritten** onto the hub pattern; the SSE consumer in `house-messages` is the first client of it |
| Platform services | Identity (Appwrite Auth and Teams, JWKS), planning objects (TablesDB), storage (S3 API), messaging, secrets by injection, observability | HS256 local JWT for staff and a second HS256 secret for the portal, a reserved `JWKS_URL` that nothing reads (`backend/internal/config/config.go:42`), photos and documents in Postgres tables, secrets from environment variables, an AES-GCM vault for the card processor credential (`hardscapeos-inventory.md` sections 4, 12.2, 20) | an on-disk `.hhpro/secrets.json` for the dealer's LLM key, written by an admin route and never returned (`hhpro-inventory.md` section 10) | see section 2 for identity; **dropped** for the on-disk secret store (context 4.4: secrets arrive as environment at spawn only); **rewritten** for attachment storage onto the S3 API |

Not supported by the shape and present now: a user-supplied static hosting recipe (HH Pro's Caddy
vhost and systemd unit, `deploy/`), a second auth scheme (the portal JWT), and generic in-process
daemons outside a `worker` role (the ERP's scheduler and dispatcher). All three are **dropped** as
mechanisms; their behaviour is carried by the platform contract (section 4.6).

---

## 2. Identity (context 4.4, 3, section 11)

| Requirement | Now | Gap |
|---|---|---|
| JWT verified against the platform JWK Set | ERP: HS256 shared secret, issued by `POST /api/v1/auth/login`, session ceilings per role including the driver knob (`backend/internal/identity/auth.go`, `rbac.go`); portal: a second HS256 secret with its own audience (`backend/internal/portal/auth.go`); HH Pro: no authentication (`hhpro-inventory.md` section 11) | **rewritten**: one verifier against JWKS replaces both issuers; the login screens go; the driver session-length knob becomes a claim-lifetime policy the platform honours or a device-unlock policy (context 4.5), which is a **decision** |
| Org resolved from `X-Org` validated against Team membership before any database is touched | nothing; the ERP is single-org by construction (`docs/contractor-portal-plan.md` section 3.1, "single-org, no tenant column") | **new** middleware; the ERP's branch-scoping discipline (`branch_id` on every operational table, repositories filter by the acting branch) is a level **below** org and carries as is |
| Tenant scoping | ERP portal: `customer_id` predicate on every portal query with cross-tenant not-found (`hardscapeos-inventory.md` section 12.2); HH Pro: none | **rewritten** as `tenant_id` middleware scoping (context 4.4, "enforced in server middleware, never left to queries"); the portal BFF's predicate discipline is the reference behaviour |
| Roles from the JWT gate every endpoint | ERP: 13 business roles in 3 tiers plus `SUPER_ADMIN`, resolved from the database per request, guards passed into `RegisterRoutes` (`hardscapeos-inventory.md` section 2.6); portal: 5 roles collapsed to 9 capabilities (`backend/internal/portal/rbac.go`); HH Pro: 4 team roles with a capability matrix, gated client-side in the actions layer (`src/core/actions/team.ts`) | **decision** with two halves. (a) Team roles in Appwrite are coarse (operator, org_admin, reviewer, member; `tenant:<org>:<slug>` for dealers and customers); the 13 staff roles and the contractor roles are app roles that must live in the core (`staff_role_grant` and `tenant_user.role`, `domain-map.md` section 5.1) keyed by the platform user id, with the Team claim as the coarse gate. Whether Team membership surfaces as a claim at all is an open verification in context section 10. (b) Which contractor role set wins (`domain-map.md` C29; `role-catalog.yaml` open question 5): the ERP already added `buyer_no_pay` and `ap_finance` to express HH Pro's `pm` and `ap` (`backend/internal/portal/rbac.go`), so the sets are one migration apart |
| Identity offline: cached session plus device unlock for shared devices; audit records the unlocked user | none on either side; the ERP's kiosk takeover and shared counter devices exist without it (`app/src/kiosk-takeover.ts`) | **new** in the auth package (HH-4) |
| An `audit` table recording every privileged action | ERP: middleware over the whole mux, one row per mutating route, proven by `TestSurface_MutatingRoutesAudited` (`backend/internal/gate/httpsurface_test.go:145`); HH Pro: an unread-count feed, not an audit | **moves** (the ERP's); HH Pro's contractor actions gain audit rows the moment they become core endpoints |

---

## 3. Data conventions (context 4.4) and the unified model

| Convention | HardscapeOS | HH Pro | Verdict |
|---|---|---|---|
| `org_id` on org tables | absent from all 196 migrations | absent | **new**, additive migration on every org table (`domain-map.md` C33) |
| `tenant_id` on tenant-scoped tables | absent; `customer_id` plays the role on portal tables | absent | **new**, additive; `customers` becomes `tenant` (`domain-map.md` section 5.1) |
| Per-scope monotonic `seq` on ordered streams | only `audit_log.seq` (`0078`), with a warning about its backfilled values | absent | **new** for every declared offline scope (`domain-map.md` section 6, observation 1) |
| Cursor pagination, never offset | `httpx.Page[T] { Items, Total, Limit, Offset }` (`backend/internal/platform/httpx/httpx.go`) | mirrors it (`src/core/supplier/port.ts`, `SupplierPage`) | **rewritten** in `httpx` and the generated client; the ERP's clamp and nil-slice rules carry (`hardscapeos-inventory.md` section 2.4) |
| Tombstone deletes | `is_active` flags on masters, append-only ledgers, no tombstone column | hard delete with an undo toast | **new** `deleted_at` alongside `is_active`, because the flag is a business state and the tombstone is a sync fact (`domain-map.md` C35) |
| Migrations forward-only and numbered, run by the platform | yes, `NNNN_name.sql`, never edited after commit, applied by `cmd/migrate` (`hardscapeos-inventory.md` section 5) | not applicable | **moves**; the 196 files become the baseline of `core/db/migrations` and the applier becomes the binary's `migrate` role |
| Money as integer cents | `BIGINT` `_cents` columns, one rounding authority for every split (`backend/internal/platform/moneyutil/allocate.go`) | integer cents through a decimal-string scaler (`src/core/lib/money.ts`) | **moves**; agreement confirmed by path (`domain-map.md` C1) |
| Quantities and UOM | `DECIMAL(19,4)` plus conversion rows, price unit distinct from base unit (`docs/design/DATA_MODEL.md` section 0; `domain-map.md` C48) | a `Uom` union and integer quantities (`src/core/domain/units.ts`) | **moves** (the ERP's); HH Pro's unit vocabulary becomes a display table in the design system |
| Status fields as `TEXT` plus `CHECK` | yes | string unions | **moves**; the unions regenerate from the OpenAPI enums |
| Ids | UUID v4 | prefixed short ids (`src/core/lib/ids.ts`) | **moves** (UUID); HH Pro ids exist only in browser state and are not migrated (`domain-map.md` C2) |
| Time | `TIMESTAMPTZ`, period boundaries in the house time zone | ISO strings, no zone modelled | **moves**; the house-zone helper is a core concern and the audit tripwire that hunts UTC boundaries comes with it (`backend/internal/testaudit`) |

**The unified model** is `domain-map.md` section 5, traced field by field to 56 numbered conflicts.
The shape-relevant summary: every entity the context file's section 9 lists exists on at least one
side; four are two documents on the two sides (quote, C15; order and the board card, C19; invoice,
C25; contractor identity, C29); two exist on neither and are required by the shape (`attachment`,
C37; `org`, C33). Nothing in the model is blocked on the shape; the blocked items are pricing
(section 6 below) and tenancy (section 2).

---

## 4. API contract (context 4.1, 4.6)

| Requirement | Now | Gap |
|---|---|---|
| `core/api/openapi.yaml` as the contract, authored before code | no OpenAPI document in either repository. The ERP contract is 551 distinct route strings registered in Go (`hardscapeos-inventory.md` section 2.6); the contractor contract is the 50-odd `/api/portal/v1` routes (`backend/internal/portal/*.go`, listed in `hardscapeos-inventory.md` section 12); HH Pro holds hand-written wire types for eleven of those reads (`src/core/supplier/adapters/erp-wire.ts`) plus recorded live fixtures (`src/core/supplier/__tests__/fixtures-live/`) | **new** document, **derived**: the tenant API is written first from the portal BFF because HH Pro's contract tests already pin its shapes; the staff API is written module by module from `RegisterRoutes`, and a conformance test asserts the registered set equals the documented set so neither can drift |
| Generated api-client package | none; HH Pro's `erp-client.ts` is hand-rolled | **rewritten** as generated code; `zero-delta.test.ts` and `erp-contract.test.ts` (`hhpro-inventory.md` section 15) become the first contract tests against the generated client |
| Every endpoint gated by role from the JWT | ERP yes (section 2); portal yes by capability; HH Pro no | covered by section 2 |
| Sync endpoints per declared scope (`GET /sync/{scope}?since={seq}`, ordered outbox push, server-assigned `seq`, per-entity conflict policy from the manifest) | none anywhere | **new**; `domain-map.md` section 6 names the scope for every entity and the policy the manifest may declare |

---

## 5. Offline readiness (context 4.5)

Neither product has an offline client, and both say so in their own code.

- HardscapeOS: no service worker, no local database, no read cache, an **in-memory** driver outbox
  that survives a lost signal and not a reload, proof of delivery deliberately not queued, no
  `navigator.onLine` handling (`hardscapeos-inventory.md` section 18, quoting `app/src/drive.ts:93-121`).
- HH Pro: everything works offline because nothing leaves the browser, and there is no outbox, no
  sync protocol, no `seq`, no conflict policy and no tombstone; the simulator's task queue is the
  supplier's own future work, not a mutation queue (`hhpro-inventory.md` section 13).

What each side contributes to the offline-store package rather than to the gap:

| Prior art | Path | Carries into |
|---|---|---|
| Client-minted ids so a retry never enqueues twice; success removes the entry; retry only on no response or a 5xx; the label never says "done" | `app/src/dispatch-logic.ts:214-276` | the outbox contract and its states |
| The refusal to serve a stale read as if it were current, stated as a product rule | `app/src/drive.ts:101-108` | the `offline` and `conflict` states designed once per pattern |
| Idempotent replay of a queued sale, tested | `backend/internal/gate/gate_test.go:564` (GATE-7) | the push endpoint's idempotency key rule |
| All-or-nothing restore validation with a stashed corrupt backup; a debounced writer with an explicit flush on `pagehide`; a first save that must be complete before another tab can read it | `src/core/stores/persistence.ts`, `src/main.tsx`, `src/core/__tests__/first-save.test.ts` | the local projection's integrity rules |
| Cross-tab adoption that is echo-proof, plus a heartbeat leader lease so exactly one tab runs background work | `src/core/stores/cross-tab.ts` (`hhpro-inventory.md` section 12) | the PWA backend's single-writer rule for the sync machine |
| A per-entity scope table with a candidate conflict policy and a one-line reason | `domain-map.md` section 6 | the manifest's `offline:` blocks per frontend |

The sync statechart (`offline -> online -> pulling -> pushing -> idle`, with `conflict` and
`auth_expired`) exists nowhere and is the acceptance gate for the package; its model-based tests are
the first thing HH-4 writes.

---

## 6. Design system (context 3, 4.4, 4.5)

| Requirement | HardscapeOS | HH Pro | Verdict |
|---|---|---|---|
| Tokens exported from Penpot to `tokens.css`; base plus per-brand overrides | `app/src/styles/tokens.css`, hex values, `--hos-*` role names kept through a rebrand, three themes (dark default, light opt-in, loud), every signal pair measured (`hardscapeos-inventory.md` section 16) | `src/ui/styles/theme.css`, OKLCH, three token **layers** (platform, dealer, contractor) bridged into Tailwind, light-first, dark first-class, contrast measured through a canvas (`hhpro-inventory.md` section 14) | **rewritten** into one vocabulary: the shape's base tokens for internal apps (context 4.5, "internal apps utility-first on the FutureBuild tokens"), a per-brand override for `hh-pro`, and HH Pro's contractor layer as a second override that must beat the dealer's only on the public proposal (`role-catalog.yaml` open question 4). Both measured-contrast gates carry as tests |
| Primitives, patterns, app kits; Penpot components one-to-one with Lit components | Lit elements exist per screen with no shared primitive layer named as such; the design doctrine lives in skills (`.claude/skills/hardscape-ui-design/SKILL.md`) | React primitives (`Button`, sheets) with the disabled-must-look-disabled rule (`hh_pro_dibbits/CLAUDE.md`) | **new** package `web/packages/design-system` with the five kits the catalog uses (`scan-and-confirm`, `list-detail`, `guided-flow`, `dashboard`, `inbox`); the two doctrines merge into its README and its critique checklist |
| States designed once per pattern (empty, loading, offline, conflict, error) | empty and error states have gates (`app/src/_empty-states.test.mjs`, `_error-states.test.mjs`) | empty draft renders a dash, not a zero (`hh_pro_dibbits/CLAUDE.md`) | **moves** the two existing gates; **new** for offline and conflict |
| Product imagery | swatches drawn from measured colours, never photographs (`hh_pro_dibbits/CLAUDE.md`, `scripts/generate-swatches.mjs`) | the ERP has `product_images` (`0102`) | **decision** at the catalogue level, recorded in `domain-map.md` C8 |

---

## 7. Repository layout, manifest, CI, and the `fb` contract (context 4.2, 4.3, 4.6)

| Requirement | Now | Verdict |
|---|---|---|
| One repository per app with the fixed layout | two repositories, neither in the layout | **new** skeleton (HH-2) |
| `manifest.yaml` with `frontends:` per role | none; `role-catalog.yaml` is the draft, with 15 micro-apps and 4 shells | **new**, generated from the catalog |
| `AGENTS.md` canonical, `CLAUDE.md` imports it | both have a `CLAUDE.md`; the ERP has four skills under `.claude/skills/` | **rewritten**: the invariants in both `CLAUDE.md` files become `AGENTS.md` sections; the skills move as they are |
| `.github/workflows/ci.yml` with a job literally named `ci` and `id-token: write` | ERP: jobs named `backend` and `frontend`, no `id-token` (`.github/workflows/ci.yml`); HH Pro: no workflow at all (`hhpro-inventory.md` section 16) | **rewritten** |
| `fb check` conformance in CI | none | **new**; the ERP's own gates that live in the build (`app/scripts/bundle-fence.mjs`, `shipped-copy.mjs`) and its architecture tests are the first `fb check` rules |
| Reproducible build, deploy per environment per org, snapshot-migrate-start, previews with seed and TTL, backups, `fb export` | ERP: an artifact-host deploy over SSH with backup, migrate, demoseed, swap, restart, healthz (`scripts/staging_apply.sh`); HH Pro: a static bundle behind Caddy (`deploy/`) | **dropped** as mechanisms; the platform provides them. The ERP's additive `demoseed` (`backend/cmd/demoseed`) becomes `db/seed/demo.sql` plus the preview seed |

---

## 8. What moves, what is rewritten, what is dropped

### 8.1 Moves

- Every Go module under `backend/internal` except `demo`, `bistrack` phase 0's status route (it moves
  with its module), and the test-only packages, with three mechanical retrofits: the `org_id` and
  `tenant_id` columns and middleware, the `seq` and `deleted_at` columns on synced scopes, and the
  guard construction rebased on the JWKS verifier (`hardscapeos-inventory.md` section 3 is the list).
- The 196 migrations as the baseline; the outbox pattern (`events_outbox` plus the dispatcher) as the
  `worker` role's first job; the house-zone helper; the rounding authority; the tax engine; the
  pricing module (pending section 6's decision on which rules it enforces).
- The ERP's Lit elements, one micro-app at a time, per `role-catalog.yaml` `replaces:`.
- The ERP's frontend gates (element registry, layout contract, theme tokens, shipped copy, bundle
  fence) and Go gates (gate suite, RBAC-403 sweep, audit coverage, dated-test tripwire, house-zone
  tripwire, stored-copy scan).
- HH Pro's supplier wire types and recorded fixtures as contract tests; its stage-machine rules, price
  expiry, customer-quote acceptance artifact and `quotePayment` rule as **core** behaviour, since a
  browser-side gate is the bug class its own port comment names (`src/core/supplier/port.ts:71-77`).
- HH Pro's persistence and cross-tab discipline into the offline-store package (section 5).
- The swatch generator and `product-colours.json` (`domain-map.md` C8, pending the imagery decision).

### 8.2 Rewritten

- HH Pro `src/ui` as the `pro-procurement`, `pro-pay` and `pro-field` micro-apps in Lit plus Ionic
  inside the `hh-pro` standalone shell, and its `/q/:token` page as `public-desk`.
- The ERP's `app-shell.ts` (frozen registry, tab instances, popouts, per-role landing) as the
  `hh-internal` shell chrome; `dashboard-nav.ts`'s enumerable routing contract carries as the shell's
  deep-link table (`fb://hh/<entity>/<id>`).
- The ERP's `portal.html` and `app/src/portal/**` are superseded by the `pro-*` micro-apps; the BFF's
  route set becomes the tenant half of the OpenAPI document and its capability guard becomes the
  tenant role gate.
- Both authentication paths onto JWKS; both admin surfaces (HH Pro's console, the ERP's admin unit)
  into `dealer-admin`, with the credential half moving to the operator's launcher.
- The assistant (both products) onto the agent tier.
- Realtime from SSE onto the hub.
- `httpx` pagination from offset to cursor.
- The token vocabularies into one design-system package.
- The two `CLAUDE.md` files into `AGENTS.md`.

### 8.3 Dropped

- HH Pro `server/` (proxy, admin store, dev middleware, on-disk secrets and config files), its
  static-deploy recipe, and the simulator as a **runtime mode** (it survives only as the generator of
  the demo seed and the scenario fixtures, `src/core/data/scenario.ts`).
- The ERP's vestigial `events.Bus` (`main.go:137`, constructed and discarded, `hardscapeos-inventory.md`
  section 2.3), its separate `cmd/migrate` and `cmd/seed` binaries, the `demo` credential bypass and
  `demo.html` (previews with seed data replace them), `quick-action-harness.html`, and the in-memory
  driver outbox once the offline-store package holds a durable one.
- One of the two contractor role sets (section 2).
- Offset pagination, HS256 secrets, and the reserved `JWKS_URL` slot.

---

## 9. Sequencing constraints the gaps impose

1. **The in-flight delivery keeps landing migrations and routes.** The ERP's own notes list nine
   deferrals, four parked wiring patches and a scaffold-to-live progression still in motion
   (`hardscapeos-inventory.md` section 21). A core built from a snapshot of the schema is stale the
   moment it is cut. The path therefore carries a standing **tracking lane** from HH-3 until the
   cutover session that ports each new HardscapeOS migration and route into `core` behind a ledger,
   so the delivery is never asked to stop and the core is never asked to guess.
2. **Both applications must write one database before any role moves.** A strangler by role means a
   yard hand on the new micro-app and a counter clerk on the old ERP are writing the same stock
   ledger in the same transaction domain. The ERP's connection target therefore moves to the
   platform's org database (a deployment configuration change, not a code change) before the first
   internal micro-app ships, and the schema additions are additive so the ERP keeps running unchanged
   against columns it does not read.
3. **The BisTrack parallel run is upstream, not parallel.** The ERP's sync module is phase 0 and
   structurally unable to sync (`backend/internal/bistrack/module.go:6-15`); the deployment guarantee
   it exists to hold is that rollback stays free through the parallel run. The `hh` ERP cutover
   replaces the application over the same data, never the system of record, so it sits after the
   delivery's own last phase and inherits the sync module like any other module.
4. **Pricing is the first ADR.** Four conflicts (`domain-map.md` C38, C43, C45, C55) decide the shape
   of the price matrix scope every contractor frontend caches offline; they must be answered before
   HH-3's schema lane freezes `price_matrix`.
5. **Tenancy is the second.** `org_id`, `tenant_id`, the branch level beneath them, and the mapping
   of 13 staff roles plus one contractor role set onto Appwrite Teams (section 2) gate the core's
   middleware, and an unverified Team-claim behaviour (context section 10) may push the coarse gate
   into a userinfo-enriching Function.
