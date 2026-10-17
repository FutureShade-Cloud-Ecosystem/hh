> For any decision made after this file's last revision, `docs/DECISIONS-CURRENT.md` wins.

# FUTURESHADE CONTEXT (for Claude Code and other harness sessions)

Single-file synthesis of every decision and plan for FutureBuild Cloud, the Shade, and the App Shape. Attach this to a session's starter prompt or place it at `context/FUTURESHADE-CONTEXT.md` in any repo the session works in. It is authoritative; the individual ADRs and plans it summarises are the long form. When this file and a repo's own `AGENTS.md` disagree on a repo-local convention, the repo wins; on architecture, this file wins.

---

## 0. Rules for any session reading this

- Never use em dashes or en dashes in anything you write: code comments, docs, commit messages, PR text. Use commas, colons, or parentheses.
- Read `docs/adr/` before changing architecture. Any new architectural decision gets an ADR, numbered after the last one in Section 12.
- Work only inside your assigned worktree and lane scope. Never touch secrets paths, `.env` files, credentials, deploy scripts, or `ee/` directories in mirrored repos.
- Secrets arrive as environment variables at spawn. Never read a vault, never print a secret, never commit one. If you need a credential you do not have, stop and report.
- Commits are signed off (DCO, `git commit -s`). All changes go through pull requests; the `ci` check must pass; the verifier's report gates merge.
- Statecharts and OpenAPI files are the spec; tests generated from them are the acceptance criteria.
- Analysis sessions are read only: no edits, branches, or pushes to the repos being analysed; write findings under `analysis/` and cite a repository path for every claim.
- Never propose pausing, freezing, or rewriting the in-flight HardscapeOS delivery; sequence around it.
- Plane is the status tracker until the Shade's planning module exists; git holds decisions; never put a secret in Plane.

---

## 1. The four nouns

| Noun | What it is |
|---|---|
| **FutureBuild Cloud** | The platform: the golden path (App Shape), the engines (identity, data, storage, messaging, hosting, secrets, observability, backups), the control plane (orgs, apps, deployments, previews, runners, budgets), and the `fb` CLI. v1 runs on Coolify and Appwrite 2.0 as engines on DigitalOcean. |
| **The Shade** | Each organisation's collaborative and agentic workspace on FutureBuild Cloud: rooms where humans and agents work, and the surface where an org sees and steers its products and deployments. v1 starts as a single-page launcher and grows rooms, cards, and agents later. |
| **Apps** | Products conforming to the App Shape and running on FutureBuild Cloud for one or more orgs: Gable, the Hardscape House products, partner apps later. |
| **Orgs** | Ventures, communities, and clients FutureBuild provisions: `fb` (internal), `gable` (community), `hh`, client orgs such as `dibbits`, more later. Each gated on its own domain with its own database, manifest, agents, and Shade workspace. |

One sentence: FutureBuild Cloud runs apps for orgs, and the Shade is where each org and FutureBuild meet to plan, build, deploy, and support them. FutureShade is the project that builds all of this; `FutureShade-Cloud-Ecosystem` is the GitHub org and `futureshade` is the Infisical project name. The deployed Platform surface (FB Console from Appwrite, FB Deploy from Coolify, the launcher) is recorded as built in `docs/fb-platform-as-built.md`.

Versions: v1 internal (FutureBuild builds its own products), v2 partner orgs (partners build on the same shape via admin provisioning, with a developer role), v3 public cloud (self-serve; mostly trust, billing, and law rather than new engineering).

---

## 2. Standing decisions (one line each)

1. Greenfield build. Campfire and Buzz are feature-pattern references only, never dependencies.
2. Compute: self-hosted Coolify on DigitalOcean manages every daemon; Appwrite 2.0 provides identity, data, storage, messaging, Functions, and Sites. Appwrite has no long-running process primitive, so daemons never run "on" Appwrite; Coolify cannot run on Appwrite either.
3. Appwrite 2.0.0 self-hosted on PostgreSQL, one organisation per instance, one project for the Shade and engine-backed products, stock and pinned, zero fork patches in Phase 1. Appwrite's own internal database is never touched.
4. Identity root: Appwrite Auth and Teams. Appwrite 2.0's built-in OAuth 2.1 / OpenID Connect provider is the SSO for third-party tools (no Go OIDC bridge); the Shade hosts the consent screen; engines verify JWTs against the published JWK Set. Tools without OIDC sit behind forward-auth at the edge.
5. Data: Go engine Postgres per org owns runtime objects (rooms, messages, threads, read cursors, presence, agent seats and state, jobs, branches, sandboxes, audit). Appwrite TablesDB owns planning objects (specs, tasks, cycles, ADRs, knowledge cards, comments, products). Cross-reference by ID only; the Bun tier is the only writer of spec state transitions; anything needing hot-path joins across the two stores moves to the engine.
6. Agents: a Bun tier runs runtime-agnostic XState-style statecharts (definitions in a shared package with no Bun or DOM imports, guards and actions by name) so a later Go runtime is a port. The client keeps interaction state only (Zag machines inside Lit elements) except the device-local offline sync machine. Agents capture knowledge only in designated intake rooms; human approval gates every dispatch.
7. Runtime layer: ACP adapters. Claude Agent (official adapter) primary on API keys through an LLM gateway; Kimi CLI secondary on Kimi Code membership keys (the one provider that sanctions subscription keys in third-party tools); Kilo and goose optional; Hermes or Pi experimental. One container per job, secrets injected at spawn, budgets enforced pre-dispatch and at the gateway, permission policy denies writes outside the checkout. Anthropic prohibits subscription OAuth outside Claude Code and Claude.ai; the platform never touches a subscription token.
8. v1 SaaS accepted with written exit paths: GitHub org `FutureShade-Cloud-Ecosystem` (pushes through the `futurebuildai` account), Infisical Cloud, Tailscale control plane. Forgejo and self-hosted Infisical are S11 options.
9. Ventures are provisioned by FutureBuild per venture: no signup, no billing, no plans in v1. Operators get one login across orgs; members see plain member mode.
10. Workstation: 128 GB Linux desktop as fabrication host, local mirror, optional Coolify-managed runner host over Tailscale, and home backup vault. Omarchy (stable channel) recommended before S0 if the GPU test passes; otherwise Ubuntu LTS plus Omakub and Timeshift. Toolchain reproducible via per-repo Nix dev shells or dev containers.
11. Every upstream you deploy or may patch is a private mirror with a patch series: pristine `upstream` branch force-synced nightly, `main` = `PINNED_TAG` plus `[fs-patch]` commits, `PATCHES.md` ledger, public forks only as PR vehicles. Fork policy: fork or patch only for a missing integration point, a tier-gated feature the licence allows reimplementing, or an abandoned upstream; never for branding.
12. v1 front door: a single-page launcher (one login, every tool, status, activity, a simple spec board, org switcher) instead of the full Shade; Claude Code and Kilo Code as development harnesses; the Shade's rooms and agents attach to the launcher after the HH and Gable migrations.
13. App Shape (Section 4) is the admission rule for FutureBuild Cloud; the client implementation model (Section 5) is how products reach clients; `fb export` is the exit promise.
14. House rules for all output: no em dashes anywhere; artifact-based deliverables; upfront clarifying questions before large deliverables; brief conversational replies.

---

## 3. The stack, layer by layer

**Machines and network.** DigitalOcean: services droplet (Coolify plus daemons), Appwrite droplet (Appwrite's own Compose stack and Traefik, Coolify proxy off), sandbox droplet (runner containers and per-branch product stacks, wildcard `*.sandbox.<domain>`), Managed Postgres (engine databases, one per org, plus Appwrite's underlying database if the installer accepts an external host), Spaces (object storage), Container Registry optional (GHCR in v1). Tailscale on every droplet, the workstation, and laptops with ACL tags (`infra`, `workstation`, `laptop`, `runner`); no public ports except the two Traefik edges; Tailscale SSH only. Off-provider backups to R2 or B2 via Autorestic; quarterly restore drills.

**Compute and delivery.** Coolify (self-hosted) as control plane for all daemons across the three droplets and optionally the workstation. GitHub org `FutureShade-Cloud-Ecosystem`: 2FA required, base permission read, rulesets on `main` (PR required, `ci` check required, linear history, no force push, zero required approvals until Grant is active), a GitHub App for the dispatch actor (never PATs), GHCR for images, a self-hosted Actions runner on the workstation for heavy jobs. Sites and Coolify use their native GitHub integrations. Uptime Kuma; Grafana and Loki deferred to pre-launch.

**Platform (Appwrite 2.0 on PostgreSQL).** Auth and Teams (`org-<slug>` teams with roles operator, org_admin, reviewer, member; `tenant:<org>:<slug>` teams for dealers and customers); OAuth2 server as OIDC provider; TablesDB for planning objects; VectorsDB and Embeddings API for knowledge cards if self-hosted models check out (else pgvector in the engine); Storage (S3 API adopted when it lands self-hosted); Messaging for push and email; Functions (Go runtime) for webhooks, TablesDB triggers, cron sweeps, and the launcher's server-side reads; Sites for every PWA with per-branch previews; Realtime for planning boards only; the Appwrite MCP server as the agent tier's path to planning objects. Combined worker topology; 8 to 16 GB droplet.

**Secrets and access.** Infisical Cloud is the source of truth: one project `futureshade`, environments dev, staging, prod, folders per consumer (`/infra`, `/appwrite`, `/core`, `/agents`, `/gateway`, `/runners`, `/workstation`, `/ci`), machine identities per consumer (GitHub Actions via OIDC auth, workstation via universal auth, Coolify, engine, agents, runner hosts). The workstation runs the Infisical agent and `infisical run` for every local process. Laptops get short-lived dev-scoped tokens over Tailscale only.

**Engine and agent tier.** `futureshade-core` (Go modular monolith, pgx and sqlc, Postgres per org, WebSocket hub, LISTEN/NOTIFY bus, org resolution via `X-Org` header validated against Team membership before any database is touched, operator API, typed Cloud events into org rooms). `futureshade-agents` (Bun, XState v5 actors: intake, spec, dispatch, sandbox_request, session; `native` connector; Appwrite server SDK for TablesDB; snapshots persisted through the engine). `@futureshade/statecharts` (definitions plus model-based tests). `@futureshade/api` (types generated from OpenAPI). LLM gateway (LiteLLM or equivalent) with virtual keys per (org, runner), spend caps, model allowlists. FutureShade MCP server for runners: `read_spec`, `post_status`, `request_review`, `open_pr`, `deploy_sandbox`, `report_blocker`, plus the App Shape tools `add_migration`, `add_query`, `add_endpoint`, `add_component`, `add_machine`, `run_checks`.

**Client.** Lit custom elements with Ionic components, Bun plus Vite, Zag for interaction state, in-memory store fed by WebSocket and REST with an optimistic outbox reconciled by `seq`. One bundle per micro-app; PWA on Sites; Tauri 2 shells per audience; browser-side SQLite for offline in Tauri (wa-sqlite on OPFS in the PWA).

**Design and knowledge.** Penpot (stock, under Coolify) with its MCP server; design tokens exported to `tokens.css` drive the Shade, the products, and the workstation theme; four-layer design system (tokens, primitives, patterns, app kits). Project brain: knowledge cards in TablesDB, tenant-scoped, filed only from intake rooms.

**Interaction layer.** The Shade (member mode; developer mode in v2; operator mode) starting as the launcher; Cowork with Fable for planning, briefs, ADRs, and weekly supervision; Claude Code and Kilo Code (or Kimi CLI) swarms on the workstation for Sessions; Penpot for Grant; Console IV for planning-object editing and platform administration.

---

## 4. The App Shape (ADR-011, 011a, 011b)

An app is admitted to FutureBuild Cloud only if it conforms. Conformance is checked by `fb check` in CI.

### 4.1 Runtimes (exhaustive)

| Runtime | Standard |
|---|---|
| Server | Go (current stable line, pinned in the template), one modular monolith binary with roles `serve`, `worker`, `migrate`; pgx; sqlc; forward-only SQL migrations run by the platform; OpenAPI as the contract; OpenTelemetry built in |
| Data | PostgreSQL 18, one database per org per app; allowed extensions pgvector, pg_trgm, PostGIS |
| Web | Lit plus Ionic, Bun plus Vite, Zag, PWA-capable, static bundles served by the platform edge |
| Shell | Tauri 2, one shell per audience (Section 4.5) |
| Agents | Statecharts package; agent seats provisioned by the platform; runners via ACP behind the Shade |
| Realtime | The engine hub pattern (WebSocket plus LISTEN/NOTIFY) for app streams; Appwrite Realtime for planning objects only |
| Platform services | Identity (Appwrite Auth and Teams, JWKS), planning objects (TablesDB), storage (S3 API), messaging, secrets by injection, observability |

Not supported: other languages or frameworks, user-supplied Dockerfiles, other databases, bespoke auth, generic serverless functions, daemons outside the `worker` role.

### 4.2 Repository layout (one repository per app)

```
<app>/
  manifest.yaml
  core/                    Go module: cmd/core (serve|worker|migrate), internal/, db/migrations (NNNN_name.sql), db/queries (sqlc), api/openapi.yaml
  web/
    packages/              design-system, api-client (generated), offline-store, auth
    apps/<microapp>/       one Vite entry per micro-app
  tauri/internal/          internal shell (loads role-allowed micro-apps)
  tauri/<public-app>/      standalone public shells (for example hh-pro)
  statecharts/             including the offline sync machine where used
  agents/                  optional seat configs and personas
  docs/adr/
  AGENTS.md                canonical; CLAUDE.md contains @AGENTS.md plus Claude-only notes
  .github/workflows/ci.yml job literally named ci; permissions include id-token: write
```

### 4.3 Manifest

```yaml
shape: 1
app: hh
name: Hardscape House
runtimes: [core, web, tauri, agents]
tenancy: multi-org
database: { extensions: [pg_trgm], seed: db/seed/demo.sql }
capabilities: { features: [threads, member_sandboxes, intake_rooms] }
agents: { seats: [product_lead], intake_rooms: [ideas, product_feedback] }
observability: { service: hh }
frontends:
  - { id: yard, audience: internal, roles: [yard_staff, yard_lead], devices: [tablet, phone], kit: scan-and-confirm,
      offline: { mode: full, scopes: [pick_lists, receiving, inventory_lookup, photos] } }
  - { id: counter, audience: internal, roles: [counter, sales], devices: [desktop, tablet], kit: list-detail,
      offline: { mode: read_cache_plus_outbox, scopes: [customers, quotes, orders] } }
  - { id: pro, audience: external, roles: [contractor, customer], devices: [phone, desktop], kit: guided-flow,
      shell: standalone, brand: hh-pro, offline: { mode: read_cache_plus_outbox, scopes: [quotes, price_matrix, orders] } }
```

The app manifest says what an app can do; the org manifest (per org, held by the platform) says what that org may use; the platform intersects them at request time.

### 4.4 Conventions the template enforces

- Org data tables carry `org_id`; tenant-scoped tables carry `tenant_id`; scoping enforced in server middleware, never left to queries.
- Ordered streams use a per-scope monotonic `seq`; pagination by cursor, never offset.
- An `audit` table records every privileged action (actor, action, ref, timestamp).
- JWT verified against the platform JWK Set; org resolved from `X-Org` validated against Team membership before touching a database.
- Migrations are forward-only and numbered; the platform snapshots the database, migrates, then starts the new binary, rolling back on failure.
- Secrets arrive as environment at spawn only.
- OTel conventions from the template; no per-app observability setup.
- Components come from the shared design system; tokens from `tokens.css`; Penpot components map one-to-one to Lit components.

### 4.5 Micro-apps, shells, offline (ADR-011a and 011b)

- Micro-app per role, shell per audience. Adding a role adds a micro-app, never a store listing; only a new public audience adds a shell. Internal staff get one installable shell per product family that loads role-allowed micro-apps with an in-shell switcher; third parties get a standalone brand-forward shell per public app; operators get the launcher, later the Shade.
- Separate bundles built from shared packages; no runtime-composed micro-frontends. The core gates every endpoint by role from the JWT; the shell is never the security boundary.
- Offline: one `offline-store` package with two backends (SQLite in Tauri; wa-sqlite on OPFS in the PWA, IndexedDB fallback for read-cache-only). Local schema: projection tables per declared scope, `outbox(id, op, payload, scope, created_at, attempts, status, error)`, `sync_state(scope, last_seq, last_pull_at)`, `attachments_queue`. Protocol: pull `GET /sync/{scope}?since={seq}`; push replays the outbox in order with client-generated ids as idempotency keys; the core assigns `seq`; conflicts return server state with per-entity policy from the manifest (`server_wins`, `last_write_wins_by_field`, `manual` which surfaces a review card); deletes are tombstones. Identity offline: cached session plus device unlock (PIN or biometric) for shared devices; audit records the unlocked user. A `sync` statechart (`offline -> online -> pulling -> pushing -> idle`, with `conflict` and `auth_expired`) lives in `statecharts/` with model-based tests as the acceptance gate. Full two-way sync only for declared yard-style scopes; everything else is read cache plus outbox.
- UX principles: one job per app (a role's top three tasks one tap from home); identical shell chrome (org, user, app switcher, sync state, notifications); explicit handoffs through shared entities opened by deep link (`fb://<app>/<entity>/<id>`, read-only sheet if the target app is not allowed); role-shaped defaults, not forks; visible sync and queue state; states designed once per pattern (empty, loading, offline, conflict, error); yard apps assume gloves, sunlight, one hand, noise; internal apps utility-first on the FutureBuild tokens, external apps brand-forward on the product's public brand.
- Design system: tokens (base plus per-brand overrides), primitives, patterns (list-detail, scan-and-confirm, guided flow, dashboard, approval card, conflict review, offline banner), app kits (`scan-and-confirm`, `list-detail`, `guided-flow`, `dashboard`, `inbox`). A new micro-app is a one-page brief: kit, three jobs, shared entities, offline scope, brand.

### 4.6 The platform contract and the `fb` CLI

Conforming apps get: reproducible builds under five minutes; deploy per environment per org with replicas and one-action rollback; snapshot-migrate-start; per-branch previews with seed data and TTL; one login and org/tenant isolation; secrets, TLS, domains; logs, metrics, traces, uptime, and cost per app per org; nightly off-provider backups with restore drills; agent seats and the spec to branch to PR to sandbox loop; and exit via `fb export` (Go binary plus Postgres plus static bundle as a Compose bundle that runs anywhere).

`fb` (Go, Apache-2.0): `fb new`, `fb check`, `fb dev` (against the local mirror), `fb migrate`, `fb preview`, `fb deploy <env> --org <slug>`, `fb rollback`, `fb export`, `fb upgrade` (scripted codemods), operator subcommands `fb org provision`, `fb app register`. The shape is versioned (`shape: 1`); the platform supports the current and one previous version.

---

## 5. Client implementation model (ADR-013)

An implementation is not a copy of the product. Three repositories, three jobs:

| Repository | Example | Contains | Never contains |
|---|---|---|---|
| Product | `gable` | The App Shape app and a capability manifest listing every feature it can expose | Any client's data, branding, or credentials |
| Implementation | `impl-gable-dibbits` | `PRODUCT_TAG`, the org manifest (capabilities on), configuration (branding tokens, workflow settings, integration endpoints, secrets by reference only), seed and data-import scripts, sandbox template, docs, `OVERLAYS.md` ledger | Product code copies or forks |
| Overlay (exception) | inside the implementation | A minimal ledgered patch when the product cannot express a behaviour by configuration yet | Anything that could have been a product feature behind a capability flag |

Default answer to "the dealer needs X": a product feature behind a capability flag, shipped to everyone and switched on in that client's manifest. Overlays need an ADR entry and are rebased on every product tag. `fb upgrade` bumps `PRODUCT_TAG`, replays overlays, migrates a preview restored from the client's seed, runs acceptance checks, opens a PR. A client is never more than one product minor version behind without a ledger entry.

Orgs: the product's community org (`gable`) stays shared for all dealers; each client's deployed instance runs in its own org (`dibbits`) with its own database, domain, manifest, agents, and Shade workspace; one login covers both. Work happens in an implementation room per client in the `fb` org, with a client implementation manager seat orchestrating engineer seats; intake feedback is split into product spec (generalisable) or implementation task (client-specific) by the intake agent and decided by a reviewer.

---

## 6. Repositories, mirrors, branches

**First-party (GitHub org `FutureShade-Cloud-Ecosystem`, private until launch, intent Apache-2.0):** `template`, `futureshade-core`, `futureshade-agents`, `statecharts`, `api`, `app`, `tauri`, `infra` (IaC, Coolify and Appwrite configs, `docs/adr`, `docs/sessions`, runbooks), `workstation` (OS layer, session runner, hooks, personas), `.github`, and `fb_cloud_launcher` (the launcher as built, a standalone service rather than a monorepo micro-app). Product repos `hh`, `gable`; implementation repos `impl-<product>-<client>`.

**Mirrors (`mirror-<name>`, private):** `agentclientprotocol/claude-agent-acp`, `MoonshotAI/kimi-cli`, `appwrite/mcp`, `appwrite/appwrite`, `coollabsio/coolify`, `penpot/penpot`, `louislam/uptime-kuma`, `cupcakearmy/autorestic`; later `Infisical/infisical`. Layout: `upstream` branch pristine and force-synced nightly by the `upstream-sync` workflow (which also opens an `upstream-release` issue with release notes when a newer tag appears), `main` = `PINNED_TAG` plus `[fs-patch]` commits, `PATCHES.md`, ruleset on `main` only, your tags prefixed `fs-`. The upstream-watch agent lane turns each release issue into a digest, a rebase PR on the mirror, and a config PR on `infra`; PRs only; more than a handful of patches or a rebase that fights back opens an ADR. Not mirrored: Buzz, Campfire, Plane, Omarchy. As built, the Appwrite and Coolify mirrors are `mirror-fb-console` (FB Console, pinned 2.1.0) and `mirror-fb-deploy` (FB Deploy, pinned v4.3.21), both carrying ledgered FutureBuild rebrand patches; the org also carries `mirror-listmonk`, `mirror-postal`, and `mirror-ntfy`. Deployed pins, patch state, and droplet facts are in `docs/fb-platform-as-built.md`.

**Branch and PR conventions:** `main` protected; lanes work in worktrees on `lane/<session>-<n>-<slug>`; integrator merges lanes to `qa/<session>`; verifier gates; PR descriptions carry the spec or brief and a transcript summary; DCO sign-off.

---

## 7. Sessions and harness routing

A Session is a long-run swarmed harness session against a locked brief. Brief format: purpose, locked inputs, lanes (parallel unless marked, each with scope, paths touched, acceptance checks, and a paste-ready prompt), integrator, verifier, human checkpoints (start, mid for UI with Grant, end), exit test (observable conditions, no durations or dates), outputs, unlocks, `harness` field with optional per-lane override.

Harness routing: Fable in Cowork is the fixed supervisor (briefs, reviews, sign-off). The swarm runs on Claude Code or the Kimi Code CLI (or Kilo) chosen per Session or per lane by quota; integrator and verifier prefer Claude; security-critical lanes (identity, OIDC, permission policy, secrets handling, statecharts, engine auth) prefer Claude; breadth lanes (client components, docs, tests, infra scripts, seed data, runbooks) run fine on Kimi K3 or Kilo. Partition work by lane in separate worktrees; use Kimi `/swarm` only for review fan-out, research, and test generation. The acceptance gate (CI, model-based tests, exit checklist) is harness-independent. `AGENTS.md` is canonical in every repo with `CLAUDE.md` importing it; one deny-by-default hook policy is expressed for every harness; per-worker isolated homes for concurrent Kimi processes; a quota ledger (harness, model, thinking level, rough usage) goes in each lane's as-built note. Interactive Sessions run on your subscriptions; the platform's automated runners run on API keys through the gateway; the two never share credentials. Claude Code on the web can run swarms against GitHub repos from the Claude app (multiple repos per session, isolated VM, branch pushed for review) and shares the account's rate limits.

Tracker: Plane holds Sessions as cycles (one issue per lane, a pinned exit-test checklist per cycle, a Verifications module, an Ops module) until the Shade's planning module exists; Session briefs and as-built notes are committed to `infra/docs/sessions/` and mirrored as Plane pages; ADRs live in `infra/docs/adr/`.

---

## 8. Current order of work

Pre-S0 (GitHub org, Infisical Cloud, template skeleton, first-party repos, mirrors, ADR-0001 forge and secrets v1) -> S0 Foundations (DigitalOcean, Tailscale, Coolify, secrets wiring, monitoring and backups, workstation layer and session runner) -> S1 Delivery layer (Actions templates, self-hosted runner, GHCR, dispatch GitHub App, Penpot, runbooks; LLM gateway optional here or in the runtime Session) -> S2 Appwrite 2.0 platform and identity (install on Postgres, project and Teams, OAuth2 server and Team claims, Sites via GitHub, Appwrite MCP, local mirror) -> S3-L Launcher (one Session: SSO tiles, status and activity via Appwrite Functions, simple spec board, org switcher; exit: Colton and Grant start every day from it) -> HH-1 to HH-5 (Section 9) -> G-1 to G-4 (Gable on the shape with the shared packages present; Dibbits as first implementation) -> the Shade Sessions (orgs and operator basics, statecharts and agent tier, runtime layer and branch rooms, sandboxes) -> Gable community launch -> Hardscape House org and manifests -> Convergence (Shade absorbs remaining tabs, offline, replicas, optional Go port of statecharts, Forgejo and self-hosted Infisical if wanted).

Monthly Ops Session: upgrades in a window after snapshots, mirror rebases, restore drills, cost and quota review.

---

## 9. HH and Gable migration (ADR-014)

Merge HH Yard and HH Pro into one App Shape monorepo `hh` with a unified core and role-based micro-apps in audience shells, strangler by screen and by role, then apply the same plan to Gable. Steps: inventory (screens, roles, entities, integrations, pricing logic, offline needs) -> unified data model (customers, contractors, products, price matrix, quotes, orders, inventory, yard tasks, receiving, attachments, audit; `org_id`, `tenant_id`, `seq`) -> API first (`core/api/openapi.yaml` before code) -> core build -> shared packages (design system, auth with device unlock, offline-store on both backends, sync statechart with model-based tests) -> internal shell and first internal micro-apps by role priority (Yard first, offline full mode) -> HH Pro as the standalone public shell -> data migration scripts and anonymised seed datasets -> strangler cutover with each role moving completely on its date and old apps going read-only -> `fb export` proof -> Gable repeats the sequence with the shared packages already built and exposes the capability flags the implementation model needs. HH-1 is a read-only analysis Session whose deliverable is `analysis/transformation-path.md` plus one executable swarm brief per session under `analysis/sessions/`; the in-flight HardscapeOS delivery is a dependency to sequence around, never something to pause or estimate.

---

## 10. Appwrite 2.0 facts and open verifications

Facts (Appwrite 2.0, announced and self-hosted release both in the current cycle): Appwrite runs on PostgreSQL by default for new self-hosted installs (fixed for the instance's life); TablesDB, DocumentsDB, VectorsDB, and an Embeddings API; an OAuth 2.1 / OIDC provider with discovery, PKCE, JWKS, device grant, client registration API, and a product-hosted consent screen; Console IV with Terminal, Explorer, realtime event tail; combined worker topology (about 16 containers plus ClickHouse for usage metrics); Git deployments for Functions and Sites from GitHub, GitLab, Bitbucket, Gitea; one organisation per self-hosted instance. Cloud-only or not yet self-hosted: dedicated provisioned Postgres engines with snapshot branches, the S3 Storage API (coming to self-hosted), Domains, Firewall, activity log.

Verify on first install: installer accepts an external Postgres host; OAuth2 server present in Community Edition and whether Team memberships surface as claims (fallbacks: a Function enriching userinfo, or a standard IdP with Appwrite as OIDC client); Appwrite MCP server against 2.0 APIs; Sites GitHub integration and per-branch previews self-hosted; Embeddings and VectorsDB self-hosted model availability; S3 API arrival; per-tool SSO tiers (Infisical, NocoDB if ever used); Claude Agent adapter headless on an API key (open issue about OAuth-only login); `kimi acp` honouring a configured API key (open PR); workstation GPU and Omarchy on a spare disk.

---

## 11. Security rules that survive everything

- No secret in chat, tracker, PR, log, or transcript; keys live in Infisical and arrive as environment at spawn; runners never hold a secret-reading tool.
- Tool tokens never reach a browser; the launcher's third-party reads go through Appwrite Functions holding scoped keys.
- Tenant scoping is enforced in engine and agent tier query layers; knowledge from one tenant never surfaces in another's rooms; shared community rooms are community-wide by design and branch rooms spawned from them stay private to FutureBuild plus originators.
- Every agent post, approval, dispatch, deploy, sandbox action, and operator action is audited.
- Runner containers reach only the gateway and the forge; writes only inside the job's checkout; no publishing, force pushes, or secrets paths; destructive git operations and dependency additions escalate to a reviewer card.
- Production data leaves the cloud only as encrypted restic snapshots; local and preview databases restore from anonymised seeds.
- `ee/` directories in mirrors are never modified; patches are recorded in `PATCHES.md`, which is also the licence ledger.

---

## 12. ADR index

| ADR | Title | Status |
|---|---|---|
| 0001 | Forge and secrets v1 (GitHub org `futureshade`, Infisical Cloud, mirror model) | To be committed in Pre-S0 |
| 002 | Chat and runtime objects in the Go engine's Postgres with its own WebSocket; amended: planning objects in TablesDB with the boundary rule | Accepted |
| 003 | Bun agent tier with runtime-agnostic statecharts; Zag on the client; amended: the device-local offline sync machine may run on the client | Accepted |
| 004 | Appwrite as identity root; v2 supersedes the Go OIDC bridge with Appwrite 2.0's OAuth2 server; per-tool SSO table, licence caution, and fork policy stand | Accepted (v2) |
| 005 | Forge and CI (GitHub now, Forgejo exit path) | Accepted |
| 006 | PM and spec tool: Plane interim, TablesDB planning module in the Shade | Accepted |
| 007 | Edge and PaaS: Coolify for daemons, Sites for PWAs, one proxy per host | Accepted |
| 008 | Secrets handling for agents: spawn-time injection only | Accepted |
| 009 | Frontend standard: Lit plus Ionic, Bun plus Vite, Zag, Tauri | Accepted |
| 010 | Fork policy: stock and pinned until a concrete need; mirrors with patch series | Accepted |
| 011 | The App Shape | Proposed |
| 011a | Multi-frontend apps with offline shells | Proposed |
| 011b | Micro-app UX framework (shell per audience, catalog, kits) | Proposed |
| 012 | Gable migration onto the App Shape | To be written from HH-1's path |
| 013 | Client implementation model | Proposed |
| 014 | HH monorepo migration | Proposed |
| next | Pricing-model resolution (first candidate from HH-1); Appwrite project topology confirmation; runner choice per job kind | Pending |

---

## 13. Glossary

App Shape: the runtimes, layout, manifest, conventions, and contract an app must follow to run on FutureBuild Cloud. Capability manifest: the per-org list of what that org may use; intersected with the app manifest. Implementation: a thin per-client repo pinning a product version plus configuration, data, and ledgered overlays. Intake room: the only kind of room where agents read and file knowledge. Branch room: a room per feature branch created by the harness. Sandbox: a warm current-app stack per product or a per-branch preview with seed data and TTL. Session: a long-run swarmed harness session against a locked brief. Lane: one parallel unit of a Session in its own worktree. Integrator and verifier: the two standing roles that merge lanes and gate the merge. Mirror: a private, independent copy of an upstream with a pristine `upstream` branch and a ledgered patch series on `main`. Operator: FutureBuild staff with cross-org access. Launcher: the v1 single-page front door that grows into the Shade. `fb`: the CLI shared by developers and the platform.
