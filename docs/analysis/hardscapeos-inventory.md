> Origin: `analysis/hardscapeos-inventory.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# HardscapeOS ERP: repository inventory (lane L2, session HH-1)

Read-only inventory of `/home/user/hardscapeos_dibbits`, the greenfield HardscapeOS ERP for Dibbits
Landscape Supply (Trenton and Kingston, Ontario). Every claim below cites a repository path. Paths
are relative to the repository root unless written absolute.

Scope note: this file describes what is in the tree. It proposes nothing, plans nothing, and
recommends no change of direction. Where `CLAUDE.md` or `README.md` disagrees with the code, the
code is reported and the stale document is named.

Conventions in this file: "the house time zone" means `America/Toronto`, the zone the ledger period,
the dashboard window, the messaging rollup, transfer arrival estimates and the SMS send window all
resolve against (`backend/internal/gl/businessday.go`, `backend/internal/messagingprovider/policy.go:38-40`).

Naming note: this document uses no calendar words and no dates. A handful of repository identifiers
contain one (two handler file names in `backend/internal/billing` and `app/src`, a column on
`dispatch_policy`, a quiet-window type in `messagingprovider`, two period columns on
`fiscal_periods`, one `inventory` column and its route). Those are described by role, or written with
a trailing `*` where the exact spelling is not needed to find them; the owning migration or file is
always cited so the exact spelling can be read there. Likewise, several `NOTES-*.md` files at the
repository root carry a date in their names and are referred to by purpose and directory.

---

## 1. Stack and build

### 1.1 Languages and dependencies

| Fact | Value | Path |
|---|---|---|
| Go module | `github.com/futurebuildai/hardscapeos` | `backend/go.mod:1` |
| Go version | `go 1.25.0` | `backend/go.mod:3` |
| Direct deps | `github.com/google/uuid v1.6.0`, `github.com/jackc/pgx/v5 v5.10.0`, `golang.org/x/crypto v0.53.0`, `golang.org/x/oauth2 v0.36.0`, `google.golang.org/api v0.288.0` | `backend/go.mod:5-11` |
| Frontend runtime dep | `lit ^3.2.0`, and nothing else | `app/package.json` |
| Frontend dev deps | `@playwright/test ^1.62.1`, `typescript ^5.9.0`, `vite ^7.0.0` | `app/package.json` |
| Node engine floor | `>=22.12.0` | `app/package.json` |

There is no ORM, no router library, no web framework, no third party spreadsheet writer, and no
third party JWT library. The Excel writer is hand written OOXML over `archive/zip` plus hand written
XML parts (`backend/internal/export/workbook.go:1-20`), by client ruling. The staff JWT is a
stdlib HS256 implementation (`backend/internal/identity/auth.go`).

`google.golang.org/api` and `golang.org/x/oauth2` exist only for Google Sign-In
(`backend/internal/identity/google_oauth.go`).

### 1.2 Local infrastructure and ports

`docker-compose.yml` brings up exactly two services:

- `postgres:16-alpine`, published on host port **5435** (container 5432), database `hardscapeos`,
  with a healthcheck (`docker-compose.yml:5-20`).
- `nats:2-alpine` with `-js -m 8223`, published on **4223** (client) and **8223** (monitoring)
  (`docker-compose.yml:22-27`).

The compose header states the ports are offset from the sibling portal stack so both can run side by
side (`docker-compose.yml:1-2`).

**NATS is provisioned and unused.** `NATS_URL` is loaded into config (`backend/internal/config/config.go:41,150`)
and referenced nowhere else in Go. There is no NATS client in the tree. See section 2.3.

Ports are environment overridable: `HOS_UI_PORT` and `HOS_API_TARGET` for Vite and Playwright
(`app/vite.config.ts:13-14`, `app/playwright.config.ts:15`), and `PGPORT` for psql in the scripts.

### 1.3 Makefile targets

`Makefile` declares: `up`, `down`, `dev`, `migrate`, `seed`, `reset`, `test`, `test-integration`,
`audit`, `build`, `pg-shell`, `web-install`, `web-build`, `web-clean`.

- `make dev` = `up` + `migrate` + `go run ./cmd/server` (`Makefile:30`).
- `make reset` = `docker compose down -v`, `up -d`, wait, `migrate`, `seed` (`Makefile:20-26`). Destructive.
- `make test` = `go test ./... -count=1 -timeout 120s`, no database (`Makefile:36`).
- `make test-integration` = `go test -tags=integration ./... -count=1 -p 1 -timeout 180s` (`Makefile:39`).
- `make audit` = `go build ./...` then `go vet ./...` then `go test ./...` then the frontend build,
  printing `ZERO-TRUST AUDIT PASSED` (`Makefile:42-45`).

### 1.4 CI

`.github/workflows/ci.yml` has two jobs, on push to `master` and on every pull request.

- `backend`: `actions/setup-go@v5` pinned to `1.25`, then `go build ./...`, `go vet ./...`,
  `go test ./... -count=1 -timeout 120s` with `CGO_ENABLED=0`.
- `frontend`: `actions/setup-node@v4` pinned **exactly** to `22.23.1`, then `npm ci`,
  `npm run build`, then `npm run test:ci`.

**What CI does not run**: the integration-tagged Go suite. `backend/internal/gate/doc.go:11-14`
states this explicitly, and it is correct: the DB backed proof is a local prove-run.

**What the gate doc is now stale about**: `backend/internal/gate/doc.go:12` says
"`.github/workflows/ci.yml` runs only the untagged `go test ./...` + the frontend build". CI now also
runs `npm run test:ci` (`.github/workflows/ci.yml:44`), added in the commit
"Wave 8: the chrome snap over eight waves of drift, and CI finally runs the frontend suite"
(`git log --oneline`, commit `12c220c`). Two build-gate source comments repeat the same stale
sentence: `app/scripts/shipped-copy.mjs:26-28` and `app/scripts/bundle-fence.mjs:22-25`.

### 1.5 The Node pin and the zero-test trap

`app/package.json` wires `pretest` to `scripts/check-node.mjs` and `test:ci` to
`scripts/assert-test-floor.mjs npm test`. The reason is recorded in `README.md:26-31` and
`CLAUDE.md`: the suite is `node --test 'src/**/*.test.mjs'`, and glob expansion plus TypeScript type
stripping are Node 22 features. On Node 20 the runner treats the glob as a literal path, matches
nothing, and exits 0. The check script fails loudly on an old Node and also fails if the glob
matches zero files; `assert-test-floor.mjs` additionally reads the run's own test count and fails
below a floor. CI pins the exact patch version rather than the major, with the rationale in the
workflow comment (`.github/workflows/ci.yml:24-30`).

### 1.6 The build-time frontend gates

`app/vite.config.ts` registers three plugins, and the file's own comment explains that a rule about
shipped bytes has to live where shipping happens:

1. `stripCssComments()` and 2. `stripHtmlComments()` from `app/scripts/shipped-copy.mjs`. A comment
   inside a `` css`` `` tagged template is CSS syntax, survives minification, and is installed as a
   constructed stylesheet; 192 internal codes reached `app/dist` that way
   (`app/scripts/shipped-copy.mjs:4-9`). Applied in `build` only, so `npm run dev` keeps the
   rationale visible in devtools.
3. `bundleFence({ entry: 'drive', page: 'drive.html', ... })` from `app/scripts/bundle-fence.mjs`.
   It fails the build if the driver entry has picked up a money word or another surface's component,
   and it prints the entry's transitive byte weight against main's on every build
   (`app/scripts/bundle-fence.mjs:1-35`). The forbidden-word list is shared with the component-level
   scan so the two gates cannot disagree (`app/scripts/bundle-fence.mjs:40-51`).

The house split is stated in the same file: the rule is tested as a pure function by a `.test.mjs`,
and the wiring is pinned by a source scan of `vite.config.ts`.

---

## 2. Backend architecture

### 2.1 The module pattern

One Go binary, `backend/internal/<module>` per department, files `handler.go -> service.go ->
repository.go -> model.go` with tests beside the code. The pattern is stated in `repo.md:19-22` and
`docs/design/MODULE_FRAMEWORK.md` section 2, and is followed with variations: larger modules split
the same four roles across many files (`backend/internal/order` has 57 Go files,
`backend/internal/quote` 55, `backend/internal/billing` 53).

Some modules use `module.go` instead of `model.go` as the doc-comment and scaffold-freeze file:
`gl`, `bankrec`, `finstmt`, `tax`, `lien`, `compliance`, `invcount`, `landedcost`, `inventory`,
`purchasing`, `warranty`, `bistrack`. Those files exist to freeze exported types and seam signatures
while bodies were `errNotImplemented` stubs (for example `backend/internal/gl/module.go:6-10`).
Several such "SCAFFOLD STATUS" notes are now historical: `backend/internal/compliance/module.go:94-96`
records the status flipping to LIVE and says the scaffold note below it is history.

### 2.2 The composition root

`backend/cmd/server/main.go` is **4311 lines**. Supporting files in the same package:
`needs_adapters.go` (181 lines), `viz_adapters.go` (476 lines), `dib483_dispatch_orderref.go`
(28 lines).

`main()` begins at `backend/cmd/server/main.go:114`. Shape:

1. `config.Load()` then `cfg.Validate()`, exiting non-zero on insecure production config
   (`main.go:115-121`).
2. `db.Connect`; a failure logs `starting without database (scaffold mode)` and continues with a nil
   pool that modules tolerate (`main.go:126-133`).
3. `bus := events.NewInMemoryBus()` followed by `_ = bus` with the comment "first subscribers arrive
   with W1 modules" (`main.go:136-138`). See 2.3.
4. `mux := http.NewServeMux()`, `GET /healthz` (`main.go:140-152`).
5. A dev-only `GET /` serving an embedded single-file demo page (`main.go:154-167`).
6. Module wiring, one block per module, each ending in a `RegisterRoutes(mux, guards...)` call.

`MODULE_FRAMEWORK.md` section 2 asked for "per-module wire funcs, not a 930-line wall". The file is
a flat sequence of inline blocks rather than `wire<Module>` functions, and it is more than four times
that length. `CLAUDE.md` describes it accurately as "a long but deliberately flat list".

Roughly 40 `RegisterRoutes` call sites live in `main.go` (lines 175 through 1883). Everything after
roughly line 1,930 onward is adapter types: over 120 small structs implementing one consumer-declared interface
each (`cardChargerAdapter`, `dispatchOrderAdapter`, `posTaxAdapter`, `w4cStockReceiver`,
`w5aQuoteStockReserver`, `invCountAdapter`, and so on). That adapter block is the physical form of
boundary rule 2: the consumer declares the narrow interface, the composition root adapts the
provider.

`_ "time/tzdata"` is imported at the composition root with a long comment (`main.go:22-30`): the
server ships `CGO_ENABLED=0` static binaries, so without the embedded zone database every house-zone
computation would degrade **silently** to UTC. Ruling D-112.

### 2.3 Platform packages

| Package | Lines | What it actually contains | Path |
|---|---|---|---|
| `platform/db` | 23 | `Connect(ctx, url) (*pgxpool.Pool, error)` and nothing else | `backend/internal/platform/db/db.go` |
| `platform/events` | 58 | `Event{Topic, Payload}`, `Bus` interface, `InMemoryBus` with synchronous fan-out | `backend/internal/platform/events/bus.go` |
| `platform/httpx` | 148 | `BadRequestError`/`BadRequestf`, `WriteJSON`, `Error`, `CounterMessage`, `ErrorWith`, `NotImplemented`, `Pagination`/`ParsePagination`, `Page[T]`/`WritePage` | `backend/internal/platform/httpx/httpx.go` |
| `platform/logging` | 15 | global slog JSON logger | `backend/internal/platform/logging/logging.go` |
| `platform/moneyutil` | 97 | `AllocateLargestRemainder` | `backend/internal/platform/moneyutil/allocate.go` |

`platform/db` also carries two integration test files that assert whole-database invariants
(`gl_seed_integrity_integration_test.go`, `invariants_integration_test.go`) plus the advisory-lease
helper.

**`RunInTx` and `GetExecutor` do not exist in `platform/db`.** `CLAUDE.md` and
`docs/design/MODULE_FRAMEWORK.md` section 2 both describe `platform/db.RunInTx(ctx, fn)` storing the
transaction in the context and every repository resolving its executor via `GetExecutor(ctx)`. A
grep across `backend/` finds no such functions. The shipped pattern is a per-module
`func (r *Repository) RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) error` that opens one
transaction from the pool and passes the `pgx.Tx` **explicitly** down the call chain
(`backend/internal/pos/repository.go:29-30`, consumed at `backend/internal/pos/service.go:74,586,1083`).
Cross-module atomicity is achieved by seam methods that take a `tx pgx.Tx` as their first real
argument, which is why so many adapters in `main.go` have `...Tx(ctx, tx, ...)` signatures
(for example `main.go:2360` `posStockAdapter.DecrementForSaleTx`, `main.go:3413`
`w5aStockReserver.ReserveTx`). Modules carrying a `RunInTx`/`InTx` of their own:
`bankrec, billing, dashboard, dispatch, gl, identity, invcount, inventory, jobs, landedcost,
messaging, messagingprovider, notify, order, payment, payments, portal, pos, pricing, procurement,
purchasing, quote, reporting, settings, tasks, till, warranty`.

**The events bus is declared, constructed, and never subscribed.** `events.NewInMemoryBus()` is
called once in `main.go:137` and immediately discarded to `_`. The `events.Bus` interface has exactly
one implementation (`InMemoryBus`) and no production consumer. `CLAUDE.md` and
`backend/internal/platform/events/bus.go:1-5` both state that production swaps in NATS JetStream
behind the same interface; no such implementation exists in the tree.

The **real** cross-module write path is the transactional outbox: `events_outbox` (migration
`0013_audit_outbox.sql`) written inside the business transaction, drained by
`backend/internal/notify/dispatcher.go`. The dispatcher is a polling loop over an `OutboxStore` whose
`ProcessBatch` claims PENDING rows (`backend/internal/notify/dispatcher.go:46-58`), with
`Register(topic, handler)`, `RegisterSecret`, `RegisterObserver`, `Run`, and `DrainOnce`.
`backend/internal/gl/module.go:282-289` registers the GL posting consumer for eight topics onto that
dispatcher. So the architectural rule holds in substance; the `events.Bus` abstraction is vestigial.

### 2.4 Error envelopes

`backend/internal/platform/httpx/httpx.go` is the single place JSON encoding and the error envelope
live. `httpx.BadRequestError` is the one exported `errors.As`-able 400 type shared across modules
(`httpx.go:13-25`). `httpx.Error` writes `{"error": msg}` and **strips the `<module>: ` prefix at that
one door**, because "dispatch:" tells a counter clerk which of our directories was unhappy
(`httpx.go:38-75`). The prefix regex is deliberately narrow, `^[a-z][a-z0-9]*: `, so it cannot reach
into a sentence and take a clause (`httpx.go:38-46`). `CounterMessage` is the strip exported on its
own for envelopes that carry extra fields, and `ErrorWith` writes `error` last so a caller's extra
key cannot shadow it (`httpx.go:79-96`).

`Pagination`/`Page[T]` define the list contract: default limit 50, max 200, offset clamped to >= 0,
and a nil slice serialised as `[]` never `null` (`httpx.go:104-148`).

### 2.5 The three boundary rules and where each is enforced

| Rule | Stated in | Enforced by |
|---|---|---|
| A module owns its tables exclusively | `docs/design/MODULE_FRAMEWORK.md` section 1 table; `repo.md:19` | Migration headers naming an owner module (for example `backend/migrations/0033_bank_recon.sql:1` "Owner module: internal/bankrec"); package doc comments stating sole ownership (`backend/internal/billing/model.go:3-6`, `backend/internal/order/model.go:2-5`). `MODULE_FRAMEWORK.md` section 5 promises a CI lint that greps each module's SQL for foreign tables; **no such lint script exists** in `Makefile`, `.github/workflows/ci.yml`, or `scripts/`. |
| Cross-module reads go through the owning module's Go interface | `docs/design/MODULE_FRAMEWORK.md` section 2 | Consumer-declared interfaces plus `main.go` adapters. Enforced structurally by import graphs in several packages with a test: `backend/internal/dashboard/model.go:4-9` ("it imports NO peer module"), `backend/internal/reporting/model.go:50-55`, `backend/internal/tasks/model.go:5-8`, `backend/internal/quoteassist/model.go:7-16` (`TestExtract_PackageHasNoQuoteWriter` scans the package's own source). |
| Cross-module write side-effects cross only as events | `docs/design/MODULE_FRAMEWORK.md` section 4 | `events_outbox` + `notify.Dispatcher`; GL's eight consumed topics at `backend/internal/gl/posting.go:37-53`. The sanctioned exception is the explicit-`pgx.Tx` seam described in 2.3. |

Required collaborators arrive by constructor; `WithX()` setters are reserved for optional
integrations. Examples of the setter form in the composition root:
`bistrack.NewService(...).WithConfigured(cfg.BisTrackConfigured())` (`main.go:172`),
`identity.NewService(...).WithStaffBaseURL(cfg.StaffBaseURL)` (`main.go:172-173`),
`payment.NewHandler(...).WithSimulated(...)` (`main.go:299`).

### 2.6 Routing and guards

stdlib `http.ServeMux` with Go 1.22 method patterns. **551 distinct `<METHOD> /api/...` route
strings** appear in non-test Go (`grep -rhoE '"(GET|POST|PUT|PATCH|DELETE) /api[^"]*"'` over
`backend/internal`, deduplicated). Each module registers under its own prefix.

Guards are `func(http.HandlerFunc) http.HandlerFunc` passed **into** `RegisterRoutes` from
`main.go`, so gating is declared at the route, at registration, and is enumerable. The three
exported by identity (`backend/internal/identity/handler.go:107-117`):

- `RequireStaff` (`h.authn`): any ACTIVE authenticated staff member.
- `RequireManager` (`h.manager`): tier 1 or 2.
- `RequireAdmin` (`h.admin`): tier 1 with GLOBAL scope.

Modules that take them under different parameter names: `pricing` uses `readGuard, writeGuard`
(`backend/internal/pricing/handler.go:62`), `payment` uses `readGuard, adminGuard`
(`backend/internal/payment/handler.go:47`), `customer` takes `requireStaff` plus a variadic
`writeGuard` (`backend/internal/customer/handler.go:35`), `product` takes only `writeGuard` and
panics if none is supplied (`backend/internal/product/handler.go:33`, rationale in
`docs/PRR-W2.5-audit-remediation.md` C-1).

Two modules register routes with **no** guard parameter at all, which is the "separately-named
registration call" convention: `identity.RegisterRoutes(mux)` (public login and password-reset
confirm plus its own internally-guarded routes) and `portal.RegisterRoutes(mux)` (portal login,
accept-invite and config are public; the rest are wrapped by portal's own `h.admin` / capability
guards). `demo.RegisterRoutes(mux) bool` returns whether it mounted at all
(`backend/internal/demo/handler.go:77`).

Public, pre-auth routes under `/api/public/v1/` exist in four modules: `quote` (token quote view and
accept), `dispatch` (will-call confirm), `warranty` (public intake), `billing` (invoice open and
click tracking pixels).

---

## 3. Module catalog

Forty-nine directories under `backend/internal` (counting `platform` as one). Guard column lists the
guard names each package's `RegisterRoutes` accepts or applies. "Owns" is from migration headers,
package doc comments, and the repository SQL.

| Module | Purpose (one line) | Tables owned | Route prefix | Guards | Events | Notable seams consumed |
|---|---|---|---|---|---|---|
| `assist` | TanyaAI: a deterministic read-command registry (half B) plus an optional chat pilot whose only power is invoking those commands (half C) (`assist/model.go:1-31`) | `assist_settings`, `assist_command_policy`, `assist_token_ledger`, `assist_role_limits`, `assist_user_limits` (0153-0155) | `/api/v1/assist` | requireStaff, requireAdmin | none | every read service behind narrow consumer interfaces wired in `assist/adapters/adapters.go` |
| `audit` | Append-only trail of mutating API requests, as middleware over the whole mux, plus a `Recorder` seam for sensitive-access rows (`audit/model.go:1-4`) | `audit_log` (0013), `audit_log.seq` (0078) | `/api/v1/audit` | requireAdmin, requireStaff | none | `identity.Actor` for attribution |
| `bankrec` | Bank statement import, auto-match engine, manual match and unmatch, clearing journals, the period reconciliation lifecycle (`bankrec/module.go:1-4`) | `bank_accounts`, `bank_import_profiles`, `bank_match_rules`, `bank_matches`, `bank_reconciliations`, `bank_statement_imports`, `bank_statement_lines` (0033) | `/api/v1/bankrec` | requireStaff, requireManager, requireAdmin | publishes `bank.recon.completed` | `GLBalanceReader`, `JournalPoster` (adapters over `gl`), till read seams |
| `billing` | Invoices plus AR read service; the receivables landing, register, statements, reminders, disputes, promises, paper path, credit gates | `ar_credit_policy`, `ar_statement_runs`, `ar_statements`, `ar_notice_log`, `ar_invoice_deliveries`, `ar_promises_to_pay`, `ar_invoice_disputes`, `ar_invoice_policy`, `ar_ap_contacts`, `ar_landing_policy`, `ar_paper_path_flags`, `ar_reminder_events`, `ar_reminder_sends` (0104,0105,0110,0175-0179,0183); sole reader of `invoices`/`invoice_lines`/`customer_transactions` (0009) | `/api/v1/ar`, `/api/public/v1/ar` | requireStaff, requireManager, requireAdmin | consumes/emits via outbox (`StatementNoticeQueue` seam) | `dispatch` AR gate, `printing` queue, `messagingprovider` notice queue |
| `bistrack` | M9 BisTrack live-sync module, phase 0: schema-first and structurally unable to sync (`bistrack/module.go:1-20`) | `bistrack_id_map`, `sync_jobs`, `sync_discrepancies` (0079) | staff-gated GET status only, registered from `main.go:179` | requireStaff | none | none; one `unconfiguredConnector` failing closed with `ErrUnconfigured` |
| `branches` | Read-only reader over the seeded branch `locations` rows plus the system default branch (`branches/model.go:1-9`) | none (reads `locations` BRANCH rows, `system_settings.default_branch_id`) | `/api/v1/branches` | requireStaff | none | none |
| `compliance` | Compliance calendar: deadline registry driving the holdback release rule and lien triage reminders (`compliance/module.go:1-6`) | `compliance_calendar`, `review_status` (0038) | `/api/v1/compliance` | requireManager | none | `Registrar` provider interface; pairs with `lien` |
| `config` | Environment configuration loader plus fail-closed validation (`config/config.go:1-2`) | none | none | none | none | none |
| `customer` | Customer/account read service plus contact reads and the SMS-consent write (`customer/model.go:1-8`) | sole reader of `customers`, `customer_contacts` (0006); writes `customer_sms_prefs` (0109) | `/api/v1/customers` | requireStaff, writeGuard | none | `localization` postal/province validation on the write path |
| `dashboard` | The cockpit substrate: tile catalog, per-role and per-user layout, and the `MetricProvider` seam every module's number arrives through (`dashboard/model.go:1-12`) | `dashboard_role_profiles`, `dashboard_profile_tiles`, `dashboard_user_tiles`, `dashboard_profile_home`, `dashboard_profile_tabs`, `dashboard_user_tabs`, `dashboard_profile_widgets`, `dashboard_user_widgets`, `dashboard_needs_sources`, `dashboard_gallery_entries`, `quick_action_settings`, `quick_action_profile_visibility` (0140,0141,0149,0151,0201,0208) | `/api/v1/dashboard`, `/api/v1/quick-actions` | requireStaff, requireAdmin | none | `MetricProvider`, satisfied entirely by `main.go` adapters; imports no peer module by design |
| `demo` | A documented credential bypass: one pre-auth route that mints a real staff session for one of four named demo people (`demo/demo.go:1-20`) | none | `/api/v1/demo/session`, `/api/v1/demo/flows` (`demo/handler.go:22,31`) | none, by design; mounts only when `DEMO_MODE` and `APP_ENV=demo` or dev | none | `identity` staff-session issuer, `audit.Recorder` |
| `dispatch` | The physical leg of the order lifecycle: the delivery, the run, the driver, the proof, the handback to billing, plus will-call (`dispatch/model.go:1-21`) | `delivery_runs`, `delivery_stops`, `dispatch_trucks`, `dispatch_drivers`, `dispatch_branch_depots`, `run_no_sequences`, `delivery_pod_photos`, `will_call_pickups`, `will_call_pickup_events`, `dispatch_reschedules`, `load_scan_checkpoints`, `dispatch_policy`, `delivery_freight_raises` (0090-0093,0107,0108,0184,0185,0195) | `/api/v1/dispatch`, `/api/public/v1/will-call` | requireStaff, requireManager, plus internal `requireBranch`, `requirePicked` | emits `sms.dispatch.notice` | order brief/fulfil/freight adapters, `printing` queue, `pos` signature capture, AR dispatch gate, `tasks` pick feed |
| `e2e` | Test-only cross-module end-to-end package (7 files, all tests) | none | none | none | none | none |
| `export` | Native `.xlsx` workbook builder using only the standard library (`export/workbook.go:1-20`) | none | `/api/v1/exports` | requireStaff | none | `product` and `inventory` read services |
| `feedback` | Bridges in-app staff feedback to the Dibbits Plane board; the Plane key is server-side only (`feedback/feedback.go:1-9`) | none | `/api/v1/feedback` (plus outbound Plane API paths) | requireStaff | none | Plane HTTP API |
| `finstmt` | Financial statements built only from the posted-filtered views (`finstmt/module.go:1-5`) | `finstmt_schedules` (0034); reads views `v_income_statement`, `v_balance_sheet`, `v_trial_balance`, `v_gl_detail`, `v_ar_aging` | `/api/v1/finstmt` | requireManager, requireAdmin (registered with all four) | none | `billing.ARBalanceCents` for the subledger tie |
| `gate` | The cross-module CSA gate-evidence suite; test-only, build-tagged `integration` (`gate/doc.go`) | none | none | none | none | the whole wired mux |
| `gl` | General ledger: chart of accounts, journal lifecycle with maker-checker, the outbox posting consumer, trial balance and subledger tie-out, period and year close, and the Great Plains parity export (`gl/module.go:1-5`) | evolves 0007 `gl_accounts`/`gl_journals`/`gl_journal_lines`/`posting_rules`/`fiscal_periods`; owns `gl_policy`, `gl_period_overrides`, `gl_recurring_templates`, `gl_export_batches`, `gl_tieout_state` (0032), `gl_approver_assignments` (0106), `gl_entry_no_sequences` (0119) | `/api/v1/gl` | requireManager, requireAdmin (registered with all four) | **consumes** eight outbox topics (`gl/posting.go:37-53`) | `CostBasis` (satisfied by `inventory`), `PaymentReader`, `JEDecider` back into `identity` |
| `identity` | Staff auth, RBAC, branch grants, the approvals rail, Google Sign-In, admin password reset | `users`, `roles`, `user_roles`, `user_branch_grants` (0004), `permission_approval_requests` (0016,0020,0075), `users_password_resets` (0047) | `/api/v1/auth`, `/api/v1/identity`, `/api/v1/staff` | own `authn`/`manager`/`admin`; exports `RequireStaff`/`RequireManager`/`RequireAdmin` | emits `approval.decided` | `ApprovalNotifier` (messaging adapter), `JEDecider` (gl adapter), `SessionTTLResolver` (dispatch policy) |
| `invcount` | Cycle counts: snapshot-frozen sheets, variance at weighted average, reason codes with per-reason GL mapping and maker-checker, the periodic inventory-to-GL reconciliation (`invcount/module.go:1-5`) | `count_sheets`, `count_lines`, `count_reason_codes`, `inventory_recon_runs` (0040) | `/api/v1/counts` | requireStaff, requireManager | none | `inventory` snapshot/weighted-average/adjustment seam, `gl` poster |
| `inventory` | The stock-move ledger and weighted-average cost read model, plus reservations, transfers, reorder points and replenishment (`inventory/module.go:1-5`) | 0037 additions to the 0005 ledger; `stock_reservations`, `branch_allocation_policy` (0115), `stock_transfers`, `stock_transfer_lines`, `transfer_no_sequences` (0116), `product_reorder_points` (0117), `branch_transit_*` (0158) | `/api/v1/inventory` | requireStaff, requireManager, requireAdmin | writes `inventory.moved`, `inventory.adjusted`, `inventory.reorder.nudge` to the outbox | `POCreator` (purchasing), `Nudger` (messaging), `OnOrder` (purchasing) |
| `jobs` | The cron-style scheduler this ERP did not have: fires registered work on a clock and records every attempt (`jobs/jobs.go:1-9`) | `job_schedules`, `job_runs` (0096) | none | none | none | any registered job function |
| `landedcost` | Freight, duty and handling capitalised into product cost, allocated across received PO lines, with USD PO support and late-freight retro adjustment (`landedcost/module.go:1-5`) | `landed_costs`, `landed_cost_allocations` (0041) | `/api/v1/landed-costs` | requireStaff, requireManager | none | `inventory` capitalise/restore seam, `gl` poster |
| `lien` | Lien cockpit: the Ontario Construction Act document workflow, holdback aging, the holdback GL (`lien/module.go:1-5`) | `lien_jobs`, `lien_documents`, `lien_participants`, `holdback_ledger` (0038) | `/api/v1/lien` | requireManager | none | `gl` poster, `compliance` calendar, `billing` invoice reads |
| `localization` | Canadian postal-code validation and normalisation plus an ISO-3166-2:CA province constraint; pure functions, no DB, no HTTP (`localization/localization.go:1-9`) | none | none | none | none | none |
| `messaging` | Internal messaging hub: channels, DMs, threads, system EVENT messages, unread badges, forced acknowledgement, rep assignment, house queues, notification modes (`messaging/model.go:1-8`) | `msg_channels`, `msg_members`, `msg_messages`, `msg_attachments` (0050), `msg_notification_prefs` (0069), `msg_retention_policy`, `msg_message_archive` (0070), `msg_acks` (0071), `msg_house_conversations`, `rep_assignments` (0136), `msg_event_classes` (0152), `msg_payment_notify_rules` (0157) | `/api/v1/messaging` | requireStaff, requireAdmin | consumes outbox topics as chat EVENTs; `msg.event.conversation.claimed` | `notify.Dispatcher`, identity approvals fan-out |
| `messagingprovider` | The out-of-band SMS arm: notifiers, templates, and a durable queue disjoint from the GL outbox (`messagingprovider/provider.go:1-21`) | `sms_outbox` (0097), reads `customer_sms_prefs` (0109), `ar_notice_log` (0110) | none | none | `sms.ar.notice`, `sms.quote.share`, `sms.quote.followup`, `sms.dispatch.notice` | `notify.Notifier` port; a stub is the default and no credentials exist in the repo (`provider.go:28-36`) |
| `notify` | The transactional-outbox dispatcher plus the provider-port `Notifier` (`notify/notifier.go:1-9`) | `events_outbox` (0013) drained here | none | none | drains every topic | imports no other internal module by design |
| `order` | Orders read service, lifecycle transitions, change orders, shipment groups, notes, history, duplicate detection (`order/model.go:1-7`) | sole reader/writer of `orders`, `order_lines` (0008); owns `order_no_sequences` (0087), `change_orders`, `change_order_lines` (0125), `shipment_groups`, `shipment_group_lines` (0126), `order_notes` (0161) | `/api/v1/orders`, `/api/v1/order-changes`, `/api/v1/shipment-groups` | requireStaff, requireManager | writes to outbox (`order.fulfilled`, `ar.invoice.finalized` path) | `StockReserver` (inventory), pricing resolver, credit gate |
| `payment` | The card rail: processor credentials stored module-local and AES-256-GCM encrypted, PAN tokenised client-side (`payment/vault.go:1-6`) | `payment_config` (0027) | `/api/v1/payments/config` | readGuard, adminGuard | none | `Processor` seam; the till is the sole writer of the `payments` row |
| `payments` | The portal-auth-gated payment module: stored methods and the multi-invoice pay flow, plus staff payment reversal (`payments/model.go:1-8`) | `portal_payment_methods` (0030), `payment_intents` (0103); writes `payments`, `payment_allocations`, `customer_transactions` in one transaction | `/api/v1/ar/payments/{id}/reverse` (staff), portal payment routes via the BFF | requireManager | `payment.received` to outbox | `PaymentGateway` port; deterministic stub is the default |
| `platform` | db, events, httpx, logging, moneyutil (see 2.3) | none | none | none | none | none |
| `portal` | Contractor portal auth and BFF: per-person logins, five roles, capability gating, and every contractor-facing feature composed in process (`portal/rbac.go:1-5`) | `portal_users`, `portal_invites` (0019), `portal_password_resets` (0030), `projects` (0048) | `/api/portal/v1` | own `admin`/capability guards; login, accept-invite, config are public | writes `crm.NewPortalContact` to the outbox | `customer`, `billing`, `order`, `quote`, `payments`, `procurement`, `product`, returns, projects |
| `pos` | The counter: sale ring, split tender, parked tickets, returns, signatures, margin glance, workspace preferences, add-on suggestions, account standing | `pos_sale`, `pos_signature`, `purchaser_eligibility_profile`, `tax_decision_snapshot`, `tax_evidence_doc` (0029), `pos_policy`, `pos_parked_tickets`, `pos_returns`, `pos_return_lines`, `pricing_orders`, `pricing_order_lines` (0043), `pos_sale_tenders` (0100), `pos_user_workspace` (0166), `pos_addon_pairings` (0169) | `/api/v1/pos` | requireStaff, requireManager, requireAdmin | emits `pos.sale.committed`, `pos.return.committed`, `sale.cash.new`, `ar.invoice.finalized` | `TaxAssessor` (tax), `Stock` (inventory), `CardCharger` (payment), `TenderTaker` (till), pricing resolver |
| `pricing` | The rule-based price resolver plus promotions, clearance, rebates, quantity breaks, price files, the price change log and the pricing audit (`pricing/model.go:1-6`) | `pricing_rules`, `pricing_list_prices`, `pricing_category_policy`, `pricing_product_policy`, `quantity_break_headers`, `quantity_break_items`, `customer_category_overrides`, `pricing_audit_log` (0023); `price_batches`, `price_change_lines`, `price_change_log`, `price_import_profiles`, `pricing_anchor_policy` (0039); `pricing_promotions`, `pricing_clearance_items` (0111); `rebate_programs`, `rebate_accruals` (0112); `pricing_uom_conversions` (0113); `price_import_runs` (0198) | `/api/v1/pricing` | readGuard, writeGuard, plus `requirePathUUID` | none | `CategoryResolver`, `UOMConverter` (catalog seam), `OverrideAuthority` (identity), `MarginGate` |
| `printing` | Every document the system puts on paper: the queue substrate and the documents themselves (`printing/model.go:1-6`) | `print_queue` (0088) | `/api/v1/printing`, `/api/v1/pos/sales/{id}/receipt` | requireStaff | none | order, invoice, dispatch and reporting document producers |
| `procurement` | The contractor portal's Planning board (`procurement/model.go:1-6`) | `procurement_projects`, `procurement_project_lines` (0031) | none directly; reached through the portal BFF at `/api/portal/v1/procurement` | portal capability guards | none | pricing seam, priced indicatively only |
| `product` | The products catalogue: faceted read, attribute edit, counter search, barcodes, synonyms, images, coverage (`product/model.go:1-4`) | `products`, `product_categories`, `product_uoms`, `product_rounding_rules`, `uoms` (0002); `product_barcodes`, `product_search_synonyms` (0101); `product_images` (0102) | `/api/v1/products` | requireStaff, writeGuard (panics if absent) | none | none |
| `purchasing` | The buy side: purchase orders with a real lifecycle, line-level receiving, AP vendor bills with three-way match, landed-cost tie-in, vendor ordering desk (`purchasing/module.go:1-27`) | `purchasing_pos`, `purchasing_po_lines`, `purchasing_receipts`, `purchasing_receipt_lines`, `po_no_sequences` (0120); `purchasing_bills`, `purchasing_bill_lines`, `purchasing_bill_instalments`, `purchasing_match_tolerance` (0121); `purchasing_policy`, `purchasing_vendor_contacts`, `purchasing_vendor_skus`, `purchasing_po_sends` (0197) | `/api/v1/purchasing` | requireStaff, requireManager, requireAdmin | `purchasing.po.created`, `purchasing.po.sent`, `purchasing.receipt.recorded`, `purchasing.bill.created`, `purchasing.bill.posted`, `purchasing.policy.set`, `purchasing.tolerance.set`, `purchasing.receipt.landed_cost`, `vendor.bill.posted` | `StockReceiver` (inventory; refuses with `ErrStockSeamUnwired` if unwired), `VendorDirectory`, `LandedCoster`, `GLPoster`, `RuleResolver` |
| `quote` | Quotes read service, the staff quote builder, the contractor state machine, share links, public acceptance, follow-ups, holds, scope terms, policy (`quote/model.go:1-15`) | sole reader/writer of `quotes`, `quote_lines` (0008); `quote_share_links`, `quote_share_link_events`, `quote_acceptances` (0086); `quote_followup_plans`, `quote_followup_events` (0110); `quote_accept_provenance` (0162); `quote_policy` (0173); `quote_no_seq` | `/api/v1/quotes`, `/api/public/v1/quotes`, `/api/v1/quote-availability` | requireStaff | `sms.quote.share`, `sms.quote.followup` | `CreditGate` (the same instance the POS ring uses), `StockReserver`, pricing resolver, `Availability` (inventory) |
| `quoteassist` | The quote builder's assist layer: item extraction from free text and scope-gap detection (`quoteassist/model.go:1-30`) | `quote_scope_rules` (0159) | `/api/v1/quote-assist` | requireStaff, requireAdmin | none | the same ranked counter search the POS counter uses; an optional `AIExtractor` that may never name a product |
| `reporting` | The native report substrate (the core BisTrack replacements) plus commission tracking (`reporting/model.go:1-4`) | `report_templates`, `report_runs` (0143), `commission_rules`, `commission_runs`, `commission_run_lines` (0144), `report_saved_views` (0145) | `/api/v1/reporting` | requireStaff, requireManager, requireAdmin | none | `RepAssignmentReader`; reads sibling tables read-only through its own repository |
| `settings` | The configurable-surfaces kit: typed module policy plus a generic presentation-curation substrate (`settings/settings.go:1-25`) | `surface_role_items`, `surface_user_items`, `surface_user_workspace` (0172), `dealer_hotkeys` (0164), `platform_hotkeys` (0191) | `/api/v1/surfaces`, `/api/v1/platform/hotkeys` | requireStaff | none | per-module policy readers; validates shape only, never item lists |
| `tasks` | WMS-Lite operational task queue: a chat message or staff POST becomes a claimable PICK task bound to an order, with an append-only transition audit (`tasks/model.go:1-13`) | `tasks`, `task_events` (0073), `task_shortages` (0095), `task_status_advance_rules` (0108), `task_pick_lines` (0187) | `/api/v1/tasks`, `/api/v1/yard` | requireStaff | posts chat EVENTs via a narrow notifier | `OrderReader`, `ChatNotifier`, `StagedNotice` queue, dispatch pick source |
| `tax` | The configurable, effective-dated Ontario HST table and status-card exemptions that replaced the flat 13 percent POS stub (`tax/module.go:1-10`) | `tax_rates`, `tax_config`, `tax_status_card_profiles`, `tax_evidence` (0042); inherits the 0029 evidence tables | `/api/v1/tax` | requireStaff, requireManager, requireAdmin | emits `tax.relief.assessed` | `pos.TaxAssessor` (frozen seam), `GLBalanceReader` |
| `testaudit` | Test-only package that audits the suite itself by AST walk (`testaudit/dated_tests_test.go:1-31`) | none | none | none | none | none |
| `till` | Drawer sessions, tender taking, movements, denomination counts, Z report, NSF handling and credit holds | `till_sessions` (0010), `till_movements`, `till_denomination_counts`, `till_tender_totals`, `till_policy` (0028), `credit_holds`, `nsf_policy`, `payment_attempts`, `payment_nsf` (0035) | `/api/v1/till`, `/api/v1/ar` (NSF and credit holds) | requireStaff, requireManager, requireAdmin | emits `till.session.completed` | `payment.Processor` via a card-charger adapter; the sole writer of the `payments` row |
| `tour` | The guided demo walkthrough: chapters, steps, and a per-person bookmark (`tour/model.go:1-20`) | `platform_tour_chapters`, `platform_tour_steps`, `tour_policy` (0192), `tour_user_progress` (0193) | `/api/v1/tour`, `/api/v1/platform/tour` | requireStaff | none | none |
| `warranty` | Warranty claim management: vendor registry, tracked claim object, checklist-gated Kanban, evidence seam, settlement money paths, recovery board, public intake, SLA timers (`warranty/module.go:1-12`) | `warranty_vendor_profiles`, `warranty_claims`, `warranty_claim_lines`, `warranty_claim_events`, `warranty_checklist_items`, `warranty_product_overrides` (0053-0054); `warranty_outcome_paths`, `warranty_settlement_postings` (0129); `warranty_recovery_actions` (0130); `warranty_intake_config`, `warranty_intake_photos`, `warranty_intake_rate_limit` (0131); `warranty_sla_config`, `warranty_sla_alerts` (0132) | `/api/v1/warranty`, `/api/public/v1/warranty/intake` | requireStaff, requireManager, requireAdmin | emits `warranty.sla.breached`, `warranty.sla.sweep` | `gl` poster, SLA notifier |

Two rows that are directories but not domain modules: `e2e` and `gate` hold only integration tests;
`testaudit` holds only tests about tests.

---

## 4. Identity, auth, RBAC

### 4.1 The role model (`backend/internal/identity/rbac.go`, 432 lines)

Three tiers (`rbac.go:15-21`):

| Tier constant | Value | Meaning |
|---|---|---|
| `TierLeadership` | 1 | org-wide: General Manager, Financial Controller, HR |
| `TierManager` | 2 | branch managers plus central Payables/Receivables |
| `TierFrontline` | 3 | inside and outside sales, yard team, drivers |

Two scopes (`rbac.go:23-28`): `ScopeGlobal` reaches both branches; `ScopeBranch` is limited to
granted branches.

The thirteen business roles (`rbac.go:70-85`), exactly as coded:

| Code | Name | Tier | Scope |
|---|---|---|---|
| `GENERAL_MANAGER` | General Manager | 1 | GLOBAL |
| `FINANCIAL_CONTROLLER` | Financial Controller | 1 | GLOBAL |
| `HR` | HR | 1 | GLOBAL |
| `BRANCH_MANAGER` | Branch Manager | 2 | BRANCH |
| `PROCUREMENT_MANAGER` | Procurement Manager | 2 | BRANCH |
| `SALES_MANAGER` | Sales Manager | 2 | BRANCH |
| `YARD_MANAGER` | Yard Manager | 2 | BRANCH |
| `LOGISTICS_MANAGER` | Logistics Manager | 2 | BRANCH |
| `PAYABLES_RECEIVABLES` | Payables/Receivables | 2 | GLOBAL |
| `INSIDE_SALES` | Inside Sales | 3 | BRANCH |
| `OUTSIDE_SALES` | Outside Sales | 3 | BRANCH |
| `YARD_TEAM` | Yard Team | 3 | BRANCH |
| `DRIVERS` | Drivers | 3 | BRANCH |

`SUPER_ADMIN` is a fourteenth role above the thirteen (`rbac.go:39-46`, migration
`0043_super_admin_platform_role.sql`). It shares tier 1 and GLOBAL scope so it satisfies every
existing authority gate, and it is the only role permitted to mint or assign another platform admin.
It is kept out of `Roles()` so the business catalog stays thirteen, and `AllRoles()` adds it back for
validation and the create-user surface (`rbac.go:60-67`).

`StaffContext` is the resolved authority for one request: `UserID`, `RoleCode`, `Tier`, `Scope`,
`Status`, `Branches map[string]bool` (`rbac.go:89-98`). It is rebuilt from the database on every
authenticated request, never trusted from the token. Predicates: `IsManager()` (tier 1 or 2),
`IsAdmin()` (tier 1 and GLOBAL), `IsSuperAdmin()` (role code match), `CanAccessBranch(branchID)`
(GLOBAL passes, scoped checks the grant map) (`rbac.go:100-116`).

### 4.2 Policy gates in `rbac.go`

Five policy types (`rbac.go:120-146`): `MARGIN_BELOW_MINIMUM`, `CREDIT_LIMIT_EXCEEDED`,
`COD_CONSTRAINT`, `JE_OVER_THRESHOLD`, `FREIGHT_ABOVE_QUOTE`. `ActionBlocked` carries
`policy_type`, `branch_id`, `detail`, `data` and serialises as a 403 body (`rbac.go:151-162`).

`MarginFloorBps = 1200` is the legacy floor constant (`rbac.go:149`). `CheckMargin` delegates to
`CheckMarginAt(price, cost, branch, hasApprovedOverride, floorBps)` so pricing can pass a per
category floor (`rbac.go:176-200`). `CheckCredit` blocks an order over the credit limit, and blocks
a credit-terms order on an account with no limit as `COD_CONSTRAINT` (`rbac.go:205-224`).

Three role-code allowlists, each with the exclusions argued in the comment:

- `railApproverRoles` = `GENERAL_MANAGER`, `FINANCIAL_CONTROLLER`, `SUPER_ADMIN`. Deliberately
  narrower than `IsAdmin()`, which would admit HR (`rbac.go:226-241`).
- `sellingRoles` = `INSIDE_SALES`, `OUTSIDE_SALES`, `SALES_MANAGER`, `BRANCH_MANAGER`,
  `GENERAL_MANAGER`, `FINANCIAL_CONTROLLER`, `SUPER_ADMIN`. A tier test would lock out the two
  frontline roles who stand at the counter (`rbac.go:243-275`). `CanSellOnCredit` also requires
  `Status == ACTIVE`.
- `dispatchCommittingRoles` includes `LOGISTICS_MANAGER`, `YARD_MANAGER` and `YARD_TEAM`, and
  excludes all three sales roles and `DRIVERS`, quoting the client call that produced the rule
  (`rbac.go:289-303`).

### 4.3 Tokens and sessions (`backend/internal/identity/auth.go`, 215 lines)

- HS256, stdlib, `alg` and `typ` pinned, `iat` and `exp` validated, lifetime re-bounded, empty
  secret fails closed (`docs/PRR-W2.5-audit-remediation.md` C-1).
- `staffJWTTTL` is the session bound for twelve of the thirteen roles and the fallback for the
  thirteenth, matching the portal's own bound (`auth.go:17-24`).
- `MaxDriverSessionTTL` is the ceiling `verifyStaffJWT` honours on a token asserting `DRIVERS`,
  fourteen times the standard bound, and it is pinned by a test to exactly the largest value the
  driver-session column's CHECK admits in migration 0209, because a ceiling below it would refuse a
  session a dealer legitimately set and a ceiling above it would guard nothing (`auth.go:26-56`).
  `maxSessionTTLFor(role)` selects between the two (`auth.go:58-63`). The role claim is consulted for
  lifetime only and grants no authority (`auth.go:42-49`).
- `clockSkew` tolerates minor drift when rejecting future-dated tokens (`auth.go:65-66`).
- `staffClaims` carries `sub`, optional `role` (documented as informational), `iat`, `exp`
  (`auth.go:100-106`).
- Login-miss path: `dummyHash` plus `burnPassword` so a non-existent or inactive account costs the
  same wall clock as a real one (`auth.go:75-82`); `errInvalidCredentials` is a single generic
  failure with no account enumeration (`auth.go:70-73`).
- Passwords are bcrypt at `bcrypt.DefaultCost` (`auth.go:84-96`).

The driver session length is a dealer knob read through a `SessionTTLResolver` adapter in the
composition root (the `driverSessionTTLAdapter` type in `backend/cmd/server/main.go`).

### 4.4 Google Sign-In (`backend/internal/identity/google_oauth.go`, 301 lines)

PKCE handshake with a signed state cookie `hos_g_oauth`, valid for a short window
(a bounded `googleStateTTL`, `google_oauth.go:22-30`). `GoogleConfig` carries `ClientID`,
`ClientSecret`, `RedirectURL`, `AllowedDomain` (the Workspace `hd` claim staff must belong to) and
`SPABaseURL` (`google_oauth.go:34-41`). The handshake cookie is signed with the staff JWT secret as
an HMAC key, in a distinct audience that is never verified as a session token
(`google_oauth.go:43-56`). `idValidate` is injected so tests can stub Google's id_token validation.
Whether the feature is on at all is reported on `/healthz` as `google_sso`
(`backend/cmd/server/main.go:86-111`).

### 4.5 Password reset (`backend/internal/identity/password_reset.go`, 71 lines)

Admin-generated only, no email and no outbox: the raw token and a ready-to-share URL are returned to
the **admin** in the response body, and the admin hands the link over out of band
(`password_reset.go:36-40`). Token primitive: 32 bytes from `crypto/rand`, base64url-raw encoded,
stored only as a sha256 hex hash (`password_reset.go:17-34`). `staffPasswordResetTTL` is
deliberately wider than the portal's email-based reset window, because the link is handed over out of
band (`password_reset.go:12-15`). The confirm route is public because it is reachable only with the token
(`backend/internal/identity/handler.go:43-45`).

### 4.6 Branch grants

`user_branch_grants(user_id, branch_id)` (migration `0004_identity_rbac_scaffold.sql`). Branch-scoped
repositories filter by the acting branch; the doctrine is stated in
`docs/design/MODULE_FRAMEWORK.md` section 3 ("no query without a branch predicate on branch-scoped
tables"). Dispatch states its own version: every read and write is gated on
`identity.StaffContext.CanAccessBranch` against a `branch_id` that lives **on the row** rather than
at the end of a join, and a document in a branch the caller cannot reach is NOT FOUND rather than
forbidden when they only ever held a number (`backend/internal/dispatch/model.go:19-21`).

### 4.7 The identity route surface

`backend/internal/identity/handler.go:39-70`:

| Route | Guard |
|---|---|
| `GET /api/v1/identity/ping` | public |
| `POST /api/v1/auth/login` | public |
| `POST /api/v1/auth/password-reset/confirm` | public (token bearing) |
| `POST /api/v1/auth/change-password` | authn |
| `GET /api/v1/staff/roles` | authn |
| `POST /api/v1/staff/policy/check` | authn |
| `GET /api/v1/staff` | manager |
| `POST /api/v1/staff` | admin |
| `PUT /api/v1/staff/{id}/role` | admin |
| `POST /api/v1/staff/{id}/password-reset` | admin |
| `POST /api/v1/staff/approvals` | authn |
| `GET /api/v1/staff/approvals` | authn |
| `POST /api/v1/staff/approvals/{id}/decide` | authn |

`Actor(r)` verifies the bearer token for **attribution only**, without a database re-resolve, for the
audit middleware (`handler.go:119-123`).

### 4.8 The approvals rail (`backend/internal/identity/approvals_rail.go`, 265 lines)

Identity stays the source of truth for approval state (`permission_approval_requests`, migration
0016, extended by 0020 with `subject_ref` and by 0075 with additive CHECK widenings) and for
authority (`CanApprove`). Two nil-able seams let a rail render and notify without identity importing
messaging or gl (`approvals_rail.go:11-19`):

- `ApprovalNotifier` with `ApprovalRequested(ctx, ApprovalNotice)` and
  `ApprovalDecided(ctx, ApprovalDecidedNotice)`; best-effort, log-and-continue, so a messaging
  outage never fails a decision (`approvals_rail.go:21-28`).
- `JEDecider.ApproveJournalTx(ctx, tx, journalID, deciderID)`, which posts the parked DRAFT journal
  **within the approval decision's transaction**, so the ledger post and the request stamp commit or
  roll back together (`approvals_rail.go:51-59`).

Four-eyes is enforced: a requester can never decide their own override
(`docs/PRR-W2.5-audit-remediation.md` H-2).

Five policies ride the one queue (section 4.2). `FREIGHT_ABOVE_QUOTE` deliberately carries **no
money** in its row or its `data` JSONB, because the approvals list is open to every authenticated
staff member and the figure is a customer's invoice amount (`rbac.go:135-145`).

### 4.9 Audit (`backend/internal/audit`, 6 files)

`audit` records an append-only trail of mutating API requests as **middleware over the whole ERP
mux**, so every module is covered without per-handler instrumentation, and exposes a `Recorder` seam
siblings call for sensitive-access rows (privacy and override accountability)
(`backend/internal/audit/model.go:1-4`). It is the first and only Go writer of `audit_log`
(migration 0013; `0078_audit_log_seq.sql` added a monotonic insertion-order key).

What it records, from the module's own integration-test header: the end-to-end coarse trail, the
reopen and masking projection, the masked evidence view, viewer filters plus a limit clamp, and
cross-row `request_id` correlation that survives the database round trip. It writes a
per-request `request_id` so rows from one request correlate.

Routes: `GET /api/v1/audit`, `GET /api/v1/audit/{id}`,
`GET /api/v1/audit/transaction/{entityType}/{entityId}`, registered with `requireAdmin` and
`requireStaff` (`backend/internal/audit/handler.go:29`).

### 4.10 The RBAC-403 sweep

`backend/internal/gate/httpsurface_test.go` is table-driven over every registered mutating route
(`theMutatingRoutes()`), with three legs:

- HS-2: anonymous gets 401 on every mutating route.
- HS-3 / GATE-2: a tier-3 caller gets 403 on every manager or admin route, and a correctly-tiered
  caller is not 401 or 403.
- HS-1 / GATE-4: every mutating route writes exactly one `audit_log` row; GET and login write none.

The table is a hand-maintained enumeration built from a grep of the handler files, and the header
states that a new mutating route must be added there or HS-2 and GATE-4 fail
(`httpsurface_test.go:28-31`). `backend/internal/gate/order_lifecycle_test.go` adds the leg the sweep
structurally cannot cover: staff-tier routes have no under-tier leg, so it proves a real frontline
caller reaches `picking` with 200 and is refused `cancel` with 403 **at the route**.

---

## 5. Data model

### 5.1 Conventions (`docs/design/DATA_MODEL.md` section 0)

| Concern | Rule |
|---|---|
| Primary keys | `UUID DEFAULT gen_random_uuid()` |
| Money | `BIGINT` integer cents, `_cents` column suffix, CAD only |
| Quantities | `DECIMAL(19,4)` (ruling D-008) |
| UoM conversion factors | `DECIMAL(19,8)` |
| Time | `TIMESTAMPTZ` everywhere; `created_at`/`updated_at NOT NULL DEFAULT now()` |
| Status fields | `TEXT` plus `CHECK (status IN (...))`; no PostgreSQL ENUMs |
| Branch | `branch_id UUID NOT NULL` on every operational table from birth |
| Foreign keys | every FK declares an explicit `ON DELETE` |
| Soft state | `is_active BOOLEAN`, flag rather than delete |
| Escape hatch | `metadata JSONB NOT NULL DEFAULT '{}'` on products, order and quote lines, stock moves |

The acceptance criteria for the design (`DATA_MODEL.md` section 15) include a convention lint
asserting zero non-`_cents` money columns, zero FLOAT or REAL, zero PostgreSQL ENUMs, zero
TIMESTAMP-without-zone, every FK with an explicit `ON DELETE`, and `branch_id` on every operational
table. Migration `0021_audit_schema_invariants.sql` carries post-audit schema invariants including a
DEFERRABLE constraint trigger asserting that POSTED `gl_journals` balance at commit, and
`0022_fix_stock_drift_view.sql` corrects the `v_stock_balance_drift` reconciliation view for
TRANSFER moves (`docs/PRR-W2.5-audit-remediation.md` M-2).

### 5.2 The actual migration set

- **196 files** in `backend/migrations`, numbered `0001` to `0212`.
- **195 distinct numbers.** `0043` appears **twice**: `0043_pos_counter_v2.sql` and
  `0043_super_admin_platform_role.sql`. This is safe because `cmd/migrate` records the **full
  filename stem** as the `schema_migrations.version`, not the number
  (`backend/cmd/migrate/main.go:44`), and applies files in `sort.Strings` order of the glob.
- **Seventeen numbers were allocated and never used**: 0080, 0081, 0082, 0083, 0084, 0094, 0099,
  0127, 0128, 0135, 0142, 0150, 0163, 0188, 0189, 0190, 0196. Several notes record the lapse
  explicitly (`NOTES-UNION-TRAIN16.md`: "Migrations 0187 and 0191; 0188-0190 lapsed unused";
  `NOTES-W6-A.md`: "migrations 0140, 0141 (0142 unused)"). Each swarm reserves a range, and unused
  reservations are never backfilled because migrations are never edited after commit.
- The runner creates `schema_migrations(version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ)` if absent,
  then applies each unrecorded file exactly once (`backend/cmd/migrate/main.go:29-50`).
- **262 `CREATE TABLE` statements** and **14 views** across the set.

The baseline sequence 0001 to 0013 is the trade-flow order set out in `DATA_MODEL.md` section 0:
things, then places, then people, then ledger, then documents, then money, then supply, then
dispatch, then plumbing.

| # | File | Concern |
|---|---|---|
| 0001 | `extensions` | platform extensions only |
| 0002 | `uom_and_products` | UoM as data plus product master plus the hardscape facets |
| 0003 | `locations_and_branches` | one locations hierarchy, branches as roots, Trenton and Kingston seeded |
| 0004 | `identity_rbac_scaffold` | identity plus the three-tier RBAC scaffold |
| 0005 | `stock_ledger` | immutable double-entry stock ledger, balance projection, layer-capable valuation |
| 0006 | `customers` | customer master plus contacts |
| 0007 | `gl_block` | the GL block, **before** any financial document table exists |
| 0008 | `quotes_orders` | quotes and orders as separate tables; conversion is a link, not a mutation |
| 0009 | `invoices_ar` | invoices plus the AR subledger, with a holdback column from birth |
| 0010 | `payments_till` | payments, allocations, till sessions (till first, payments reference it) |
| 0011 | `vendors_purchasing` | vendors, purchase orders, receiving headers |
| 0012 | `manifests` | durable versioned dispatch manifests plus classification |
| 0013 | `audit_outbox` | audit log plus the transactional event outbox |

The full list of all 196 files is Appendix A.

### 5.3 Tables by area

**UOM model.** `uoms(code CITEXT PK, name, dimension CHECK IN ('count','area','weight','volume','length'), is_active)`;
`product_uoms(product_id, uom_code, qty_in_base DECIMAL(19,8) CHECK > 0, is_base, role_hints TEXT[])`
with exactly one `is_base` row per product and a stated any-to-any rule
`qty x from.qty_in_base / to.qty_in_base` (one hop, no graph walk);
`product_rounding_rules(product_id PK, sell_increment_uom, break_pallet_allowed,
break_pallet_fee_cents, waste_factor_pct)` (`DATA_MODEL.md` section 1, migration 0002). Documents
store the entered trio `qty_entered`, `uom_entered`, `qty_base`, so the contractor sees the selling
unit and the yard sees the picking unit.

A **second** conversion table arrived later for the sales flow: `pricing_uom_conversions` (migration
0113) with `product_id XOR category_id XOR global`, `from_uom`, `to_uom`, `factor NUMERIC(19,8) CHECK > 0`,
`round_up_at_pick BOOLEAN` (the only flag that turns 77 square feet into 2 pallets rather than 1.27),
and a mandatory `source` of `SEEDED_DEFAULT | CLIENT_CONFIRMED | VENDOR_SHEET | MEASURED` plus a
`source_note`. Most specific wins **per edge**, not per lookup.

**Stock ledger.** `stock_moves` is append-only: `product_id`, `from_location_id`/`to_location_id`
(NULL on either end means world), `qty_base`, the entered trio, `move_type CHECK IN
('RECEIPT','TRANSFER','PICK','SHIP','ADJUST','COUNT','BREAK_PALLET','RETURN')`, `unit_cost_cents`,
`value_cents` (what GL posts from), `weight_kg_total`, `source_type`/`source_ref_id`, `reason`,
`actor_id`, `branch_id`, `metadata`, `occurred_at`. Corrections are compensating moves, never
updates. `stock_balances(product_id, location_id, branch_id, on_hand_base, allocated_base)` is the
projection, maintained in the same transaction and rebuildable, with
`CHECK (on_hand_base >= allocated_base)` and a guarded-UPDATE concurrency rule
(`DATA_MODEL.md` section 5). Migration 0037 relaxed the negative-stock CHECK and added the on-hand
plus weighted-average read model (`backend/internal/inventory/module.go:1-5`).

**Cost.** `cost_layers(product_id, branch_id, received_at, qty_received, qty_remaining,
unit_cost_cents, source_move_id)` and `product_branch_costs(product_id, branch_id,
avg_unit_cost_cents)` are **both** maintained on every receipt; which one issues consume was left as
a service-level policy switch pending a client answer (`DATA_MODEL.md` section 5, open slot Q-001).

**GL block.** `gl_accounts(code CITEXT unique, name, account_type CHECK IN
('ASSET','LIABILITY','EQUITY','REVENUE','EXPENSE'), normal_balance CHECK IN ('DEBIT','CREDIT'),
parent_id)`; `fiscal_periods`, keyed on the fiscal-year and period-ordinal pair (unique across the
two, with an ordinal CHECK of 1 to 12) plus `status CHECK IN ('OPEN','CLOSED')`, `closed_at` and
`closed_by`; `gl_journals(fiscal_year, entry_no, period_id, source CHECK IN
('MANUAL','INVOICE','PAYMENT','FULFILLMENT','PO_RECEIPT','ADJUSTMENT','CLOSING'), source_ref_id,
status CHECK IN ('DRAFT','POSTED','VOID'), posted_at, branch_id)` unique on
`(fiscal_year, entry_no)`; `gl_journal_lines` with a debit-XOR-credit CHECK and `branch_id` plus
`project_ref` as dimensions; `posting_rules(event_type, line_role, account_code, branch_id)` unique
on the triple, with NULL branch meaning default and a branch row overriding
(`DATA_MODEL.md` section 7). Posting doctrine: a journal entry is written **in the same transaction**
as its source document.

Account codes reach GL only through `posting_rules`, never as literals; `purchasing` states this as
its own rule (`backend/internal/purchasing/module.go:23-26`). `gl_entry_no_sequences` (0119, with a
self-heal in 0123) replaced `MAX()+1` entry numbering.

**Quotes and orders.** `quotes(quote_no CITEXT unique, customer_id, branch_id, status CHECK IN
('DRAFT','SENT','ACCEPTED','CONVERTED','EXPIRED','LOST'), valid_until DATE, subtotal_cents,
tax_cents, total_cents, created_by, metadata)` and `quote_lines(quote_id, line_no, product_id
nullable for free-text, description, qty_entered, uom_entered, qty_base, unit_price_cents,
price_uom, line_total_cents, metadata)` unique on `(quote_id, line_no)`.

`orders(order_no CITEXT unique, customer_id, branch_id, quote_id nullable, status CHECK IN
('DRAFT','CONFIRMED','PICKING','READY','PARTIALLY_FULFILLED','FULFILLED','INVOICED','CLOSED','CANCELLED'),
fulfillment CHECK IN ('PICKUP','DELIVERY','WILL_CALL'), requested_date DATE, subtotal_cents,
tax_cents, total_cents, deposit_required_cents, created_by, metadata)` and `order_lines` adding
`allocated_base`, `fulfilled_base`, `is_special_order`, with
`CHECK (fulfilled_base <= qty_base)` (`DATA_MODEL.md` section 8). Order numbering became
branch-aware in 0087 (`order_no_sequences`).

**Invoices and AR.** `invoices(invoice_no CITEXT unique, customer_id, order_id, branch_id, status
CHECK IN ('DRAFT','FINALIZED','PARTIALLY_PAID','PAID','VOID'), issue_date, due_date, subtotal_cents,
tax_cents, holdback_cents, total_cents, balance_cents, finalized_at, metadata)`; `invoice_lines`;
and the AR subledger `customer_transactions(customer_id, txn_type CHECK IN
('INVOICE','PAYMENT','CREDIT_MEMO','ADJUSTMENT','NSF','WRITE_OFF'), amount_cents signed,
balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)`
(`DATA_MODEL.md` section 9). The holdback column exists from birth for the Ontario Construction Act
work that migration 0038 builds on.

**Payments and till.** `payments(payment_no CITEXT unique, customer_id nullable for walk-ins, method
CHECK IN ('CASH','CARD','CHEQUE','EFT','OTHER'), amount_cents CHECK > 0, status CHECK IN
('RECEIVED','DEPOSITED','BOUNCED','REFUNDED','VOID'), received_at, branch_id, till_session_id,
gateway_ref, received_by, metadata)`; `payment_allocations(payment_id, invoice_id, amount_cents)`;
`till_sessions(branch_id, station, status CHECK IN ('OPEN','CLOSED'), opened_by, opened_at,
float_cents, closed_by, closed_at, counted_cents, over_short_cents)` (`DATA_MODEL.md` section 10).
Migration 0028 added drawer operations (`till_movements`, `till_denomination_counts`,
`till_tender_totals`, `till_policy`); 0035 added `credit_holds`, `nsf_policy`, `payment_attempts`,
`payment_nsf`; 0068 set the cash over and short tolerance to a client-answered value; 0100 added
`pos_sale_tenders` for split tender; 0103 added `payment_intents` as the durable pre-charge intent;
0106 added payment reversal provenance.

**Purchasing.** The 0011 baseline (`vendors`, `purchase_orders`, `po_lines`, `po_receipts`) is still
present and is used as the **receiving document** shape. The Wave 4 module owns a second,
independent document family in 0120 and 0121 (`purchasing_pos`, `purchasing_po_lines`,
`purchasing_receipts`, `purchasing_receipt_lines`, `purchasing_bills`, `purchasing_bill_lines`,
`purchasing_bill_instalments`, `purchasing_match_tolerance`, `po_no_sequences`), plus 0122 posting
rules, 0134 currency, and 0197 the vendor ordering desk (`purchasing_policy`,
`purchasing_vendor_contacts`, `purchasing_vendor_skus`, `purchasing_po_sends`). The two families
coexist by design: `purchasing` owns no stock arithmetic and hands a receiving-document id to the
`StockReceiver` seam that `inventory` satisfies (`backend/internal/purchasing/module.go:14-22`).

**Dispatch manifests.** The 0012 baseline is `manifests(manifest_no, branch_id, delivery_date,
vehicle, status CHECK IN ('DRAFT','FINAL','DISPATCHED','COMPLETED','CANCELLED'), mto_class CHECK IN
('STANDARD','MTO','MIXED'))`, `manifest_lines`, and `manifest_versions` holding an immutable JSONB
snapshot per version. The live dispatch module added `delivery_runs(status CHECK IN
('PLANNED','DISPATCHED','COMPLETED','CANCELLED'))` and `delivery_stops(status CHECK IN
('READY','LOADED','EN_ROUTE','DELIVERED','FAILED','CANCELLED'))` in 0090, POD photos in 0091,
will-call in 0092, reschedules and MTO in 0107, the scan trail in 0108, the load board in 0184,
commit state in 0185, and the freight-raise approval in 0195.

**Audit and outbox.** `audit_log(action dot-namespaced past tense, entity_type, entity_id, actor_id
with no FK so it survives user deletion and system actors, branch_id, changes JSONB, request_id)`;
`events_outbox(event_id, topic, payload JSONB, occurred_at, status CHECK IN
('PENDING','PUBLISHED','DEAD'), attempts, published_at, last_error)` (`DATA_MODEL.md` section 13).

**Pricing tables.** See section 6.

**Tax tables.** `tax_rates`, `tax_config`, `tax_status_card_profiles`, `tax_evidence` (0042), over
the 0029 `tax_decision_snapshot`, `tax_evidence_doc` and `purchaser_eligibility_profile`.

**Portal tables.** `portal_users`, `portal_invites` (0019, role CHECK widened by 0160),
`portal_password_resets`, `portal_payment_methods` (0030), `procurement_projects`,
`procurement_project_lines` (0031), `return_requests`, `return_request_lines` (0045),
`projects` (0048).

**BisTrack sync tables.** `bistrack_id_map`, `sync_jobs`, `sync_discrepancies` (0079).

**Late arrivals.** Widgets: `dashboard_profile_widgets`, `dashboard_user_widgets`,
`dashboard_needs_sources` (0201), `dashboard_gallery_entries` (0208), with 0202 carrying existing
charts onto the widget substrate, 0205 making a widget window state what it measures, and 0207
widening the bento to twelve columns with per-widget spans. Driver session: 0209 turns the driver
session length into a dealer knob on `dispatch_policy`. Receivables flow: 0175 to 0183 build the
invoice register, deliveries, promises to pay, disputes, the register surface and the AR landing with
reminders. Demo flows: 0210 carries four demo flows through their write, 0211 gives the receivables
flow its own chapter, 0212 makes Reports a real surface so the role strips can point at it.

### 5.4 The metadata escape hatch and the UDF promote list

`metadata JSONB NOT NULL DEFAULT '{}'` exists on products, order and quote lines, and stock moves
(`DATA_MODEL.md` section 0). Its stated purpose is BT-A-006: unpromoted BisTrack user-defined-field
facts land there until promoted to real columns.

`backend/seed/udf_promote_list.csv` (58 lines including the header) is the canonical promote list
that migration `0018_promote_udf_attributes.sql` builds against. Columns: `udf_name`,
`source_table`, `bistrack_type`, `fill_rate`, `distinct_values`, `proposed_column`, `proposed_type`,
`classification`, `rationale`. Classifications are `promote | metadata | drop | exists`
(`backend/cmd/decode_udf/main.go:34-40`). Examples from the file: `udfFullSkidQuantity` to
`full_skid_quantity INTEGER`, `udfLayerQuantity` to `layer_quantity INTEGER`, `udfDibbitsFavourite`
to `is_featured BOOLEAN`. `backend/seed/udf_promote_mapping.md` is the prose companion.

`backend/cmd/decode_udf` (180 lines) is the ETL decode pass that produces that CSV from the BisTrack
column census. Its header records finding D-027: `dbo.OrderLineUDF` has 168,395 rows but a single
column, so no order-line UDF **values** exist in the snapshot, and the real in-use user-defined
fields are product-level (24 `udf*` columns on `dbo.Product`). Fill rate is reported as
`no_row_data` because the snapshot carries schema but not row data
(`backend/cmd/decode_udf/main.go:7-19`).

---

## 6. Pricing logic, in detail

This section is written for field-by-field comparison against the HH Pro repository's tier, category
rule, contract price and volume-break model. Go structs and SQL columns are reproduced as written.

### 6.1 Files in `backend/internal/pricing` (47 Go files)

Non-test files: `model.go`, `service.go`, `repository.go`, `handler.go`, `gate.go`, `counter.go`,
`promo.go`, `rebate.go`, `uomplan.go`, `auditref.go`, `override_authority.go`, `listprice_bulk.go`,
`batch_model.go`, `batch_repository.go`, `batch_service.go`, `batch_handler.go`,
`pricefile_model.go`, `pricefile_repository.go`, `pricefile_service.go`, `pricefile_handler.go`,
`xlsx_reader.go`, `w4_handler.go`, `w4_repository.go`.

### 6.2 Vocabularies (`backend/internal/pricing/model.go`)

`CalcType` (`model.go:11-15`):

```
CalcAbsolute     = "ABSOLUTE"        // a set price per price_uom (CUSTOMER_PRODUCT only)
CalcPctOffList   = "PCT_OFF_LIST"    // discount_bps off the retail anchor (the dominant calc)
CalcMarkupOnCost = "MARKUP_ON_COST"  // builds the retail anchor (cost x 1.85 family)
```

`BreakValueType` (`model.go:17-27`). A quantity break is an **adjustment** on the tier-resolved base,
never a competing base price:

```
BreakPctOff       = "PCT_OFF"        // discount: percent OFF the resolved base
BreakAbsOff       = "ABS_OFF"        // discount: flat cents OFF the resolved base
BreakSurchargePct = "SURCHARGE_PCT"  // charge MORE: positive percent uplift (partial skid)
BreakSurchargeAbs = "SURCHARGE_ABS"  // charge MORE: positive flat uplift
```

`Scope` values for `pricing_rules` (`model.go:29-35`): `CUSTOMER_PRODUCT`, `CUSTOMER_CATEGORY`,
`TIER_CATEGORY`, `TIER_DEFAULT`.

`Source`, the resolved precedence level surfaced in the breakdown (`model.go:37-46`):
`CUSTOMER_PRODUCT_RULE`, `QUANTITY_BREAK`, `CUSTOMER_CATEGORY_RULE`, `CUSTOMER_OVERRIDE`,
`TIER_CATEGORY`, `TIER_DEFAULT`, `LIST`. A `PROMOTION` source is added by the promotions work
(`service.go:585`).

Bypass reasons, the closed set the `pricing_audit_bypass_reason_known` CHECK accepts
(`model.go:69-74`): `PRODUCT_POLICY_DISABLED`, `CATEGORY_POLICY_DISABLED`, `APPROVED_OVERRIDE`,
`MANAGER_SCOPE_OVERRIDE`. The last two are written when the gate **ran, blocked, and a server-side
authority cleared the block**, with two values rather than one so "how many below-floor prices went
out without anybody filing an approval" is answerable (`model.go:52-67`).

### 6.3 The resolution order, exactly

`ResolvePrice` (`backend/internal/pricing/service.go:287-300`) documents the base waterfall:

```
CUSTOMER_PRODUCT > CUSTOMER_OVERRIDE / CUSTOMER_CATEGORY > TIER_CATEGORY > TIER_DEFAULT (~99%) > LIST
```

The shared implementation is `resolve(ctx, in, dryRun)` (`service.go:323`), in seven numbered steps:

1. **Category.** Resolved via the injected `CategoryResolver` seam, never a `product_categories`
   SELECT. A not-found maps to pricing's `errNotFound` so an unknown product is 404 not 500
   (`service.go:336-340`).
2. **Retail anchor.** An explicit `pricing_list_prices` row **is** the anchor; when absent the
   derivation is cost times 1.85 (`service.go:365`, migration `0023_pricing.sql:36-38`).
3. **Candidate rules**, one query (`store.MatchingRules`). Quantity breaks and the customer override
   are fetched **lazily**, only when no `CUSTOMER_PRODUCT` contract price matched, because a
   contract price is final (`service.go:401-405`).
4. **Base level, first match wins** (`service.go:410-484`):
   - `CUSTOMER_PRODUCT`: `r.CustomerID == in.CustomerID && r.ProductID == in.ProductID`, both
     non-empty. Contract price is FINAL: breaks and override are neither fetched nor applied.
   - otherwise fetch breaks (`QuantityBreaksFor(productID, categoryID)`) and the override
     (`OverrideFor(customerID, categoryID)`), select the single applicable break
     (product header preferred over category header) via `selectBreak`, and then:
     - `CUSTOMER_OVERRIDE` if an override row exists;
     - else `CUSTOMER_CATEGORY` rule where `r.CustomerID == in.CustomerID && r.CategoryID == in.CategoryID`;
     - else `TIER_CATEGORY` where `r.CustomerType == in.CustomerType && r.CategoryID == in.CategoryID`;
     - else `TIER_DEFAULT` where `r.CustomerType == in.CustomerType`;
     - else `LIST`.
   - A missing threshold-UOM conversion surfaces as `errNoConversion` rather than silently dropping
     the break: a misconfigured break must fail loud, never silently undercharge
     (`service.go:437-441`).
5. **Result UOM and the base calc** (`service.go:485-536`). A percentage discount is dimensionless
   so it resolves in the **retail anchor's** UOM; only `ABSOLUTE` and `MARKUP_ON_COST` carry their own
   declared `price_uom`:

   ```go
   resultUOM := declaredUOM
   if source != SourceList && calcType == CalcPctOffList { resultUOM = listUOM }
   originalCents := roundCents(float64(listCents) * fRes / fList)
   costInResUOM  := roundCents(float64(in.CostCents) * fRes)
   ```

   Then: `LIST` uses the anchor and errors with `errNoListPrice` if there is none; `ABSOLUTE` takes
   `price_cents` outright (a negotiated contract price needs no list anchor); `PCT_OFF_LIST` refuses
   to discount off a zero anchor and otherwise `applyPctOff(originalCents, discount_bps)`;
   `MARKUP_ON_COST` refuses a zero cost basis and otherwise `applyMarkup(costInResUOM, markup_bps)`.
6. **5b, promotion and clearance** (`service.go:538-599`). A promotion competes for BASE selection
   directly below the contract price and above the whole override, tier and list waterfall, but only
   when it BEATS the base that waterfall produced (the never-worse guard). The clearance badge read
   runs on both paths including the contract path, because the badge is a fact about the product.
   **The promo is computed off the retail anchor, not off the tier-resolved base** (`service.go:568-577`),
   with the comment explaining that passing the resolved base would quietly give a contractor
   18 percent off AND 25 percent off (retail x 0.82 x 0.75). The three outcomes are: the promo could
   not be expressed on this line (a fixed-price promo authored in a different UOM), the promo wins
   and `Source` becomes `PROMOTION`, or the never-worse guard fires and the reason is recorded in
   `Detail`.
7. **6, quantity-break adjustment** (`service.go:602-634`). Applied ON TOP of the resolved base:
   `PCT_OFF` and `ABS_OFF` discount it, `SURCHARGE_PCT` and `SURCHARGE_ABS` uplift it. `ABS_OFF`
   clamps at zero. **The `Source` stays the base level**; the composition is recorded in `Detail`.
   Marked PROVISIONAL pending client confirmation of the stacking model and the break values.
8. **6 (second), quantities and line total** (`service.go:643-652`):
   `qtyBase = QtyEntered * factor(UOMEntered)`, `qtyInResUOM = QtyEntered * factor(UOMEntered) / factor(resultUOM)`,
   `lineTotal = roundCents(qtyInResUOM * finalCents)`.
9. **7, margin gate** (`service.go:669 onward). The effective floor comes from
   `pricing_category_policy` (base plus or minus the category adjuster); when that policy is unseeded
   the floor falls back to `defaultFloorBps` (the legacy 1200 bps). If enforcement is disabled on the
   category or the product, the gate does not run and a BYPASS row is appended with a named reason;
   **product wins the attribution** when both are disabled.

`ResolvePricePreview` is `ResolvePrice` with `dryRun=true`, which suppresses the BYPASS audit append
so a surface scoring hypothetical alternatives does not stamp a row per alternative
(`service.go:304-320`).

### 6.4 The Go request and result shapes

`ResolveInput` (`backend/internal/pricing/model.go:198-236`), verbatim field list:

```go
type ResolveInput struct {
    CustomerID        string  `json:"customer_id"`
    CustomerType      string  `json:"customer_type"`
    ProductID         string  `json:"product_id"`
    CategoryID        string  `json:"category_id"`
    BranchID          string  `json:"branch_id"`
    QtyEntered        float64 `json:"qty_entered"`
    UOMEntered        string  `json:"uom_entered"`
    CostCents         int64   `json:"cost_cents"`
    OverrideRequested bool    `json:"has_approved_override"` // wire name unchanged; server decides
    UserID            string  `json:"-"`                     // audit attribution only
    DocRefType        string  `json:"doc_ref_type,omitempty"`
    DocRefID          string  `json:"doc_ref_id,omitempty"`
}
```

`CostCents` is **caller supplied and basis-agnostic**: the cost-basis choice is owned by costing and
stock, not by pricing (`model.go:198-201`). It is expressed per the product's base UOM.

`Breakdown` (`model.go:238-303`), the pre-tax result. It never carries `tax_cents`:

```go
type Breakdown struct {
    OriginalCents     int64   `json:"original_cents"`   // retail anchor in the result price_uom
    FinalCents        int64   `json:"final_cents"`      // resolved unit price per price_uom, pre-tax
    DiscountBps       int64   `json:"discount_bps"`     // off-anchor bps (negative = surcharge)
    Source            string  `json:"source"`
    Detail            string  `json:"detail"`
    PriceUOM          string  `json:"price_uom"`
    QtyBase           float64 `json:"qty_base"`
    LineTotalCents    int64   `json:"line_total_cents"`
    MarginBps         int64   `json:"margin_bps"`
    FloorBps          int64   `json:"floor_bps"`
    Blocked           bool    `json:"blocked"`
    BlockPolicy       string  `json:"block_policy,omitempty"`
    PromoCode         string  `json:"promo_code,omitempty"`
    PromoName         string  `json:"promo_name,omitempty"`
    Clearance         bool    `json:"clearance"`
    ClearanceReason   string  `json:"clearance_reason,omitempty"`
    BypassReason      string  `json:"bypass_reason,omitempty"`
    OverrideRequested bool    `json:"override_requested,omitempty"`
    OverrideAuthority string  `json:"override_authority,omitempty"`
}
```

`Rule` (`model.go:96-116`), one `pricing_rules` row, empty strings for unset nullable scope
dimensions: `ID, Scope, CustomerID, CustomerType, CategoryID, ProductID, BranchID, CalcType,
PriceCents *int64, DiscountBps *int64, MarkupBps *int64, PriceUOM, Priority int64, ValidFrom
*time.Time, ValidTo *time.Time, DetailText, IsActive, Version int64`.

`QuantityBreak`, the flattened header-times-item row the resolver uses (`model.go:118-133`):
`HeaderID, ItemID, Name, ProductID, CategoryID, ThresholdUOM, FromQty float64, ToQty *float64,
ValueType, DiscountBps *int64, DiscountCents *int64, SurchargeBps *int64, SurchargeCents *int64,
PriceUOM`.

`QuantityBreakHeader` and `QuantityBreakItem` are the admin editor's unit, under an optimistic
`Version`; scope is EXACTLY ONE of `ProductID` or `CategoryID`, and product-specific schedules win
over category schedules at resolve time (`model.go:135-163`).

`Override`, one `customer_category_overrides` row, with ABSOLUTE forbidden by a database CHECK
(`model.go:165-176`). `ListPrice`, the retail anchor (`model.go:178-183`). `CategoryPolicy`
(`MarginTargetBps`, `MarginFloorBps`, `EnforcementDisabled`) and `ProductPolicy`
(`EnforcementDisabled`) (`model.go:185-196`).

`AuditEntry` (`model.go:314-333`): `EntityType, EntityID, Action (CREATE|UPDATE|DELETE|BYPASS),
FieldName, OldValue, NewValue, UserID, RefEntityType, RefEntityID, BypassReason`.

`LowMarginEntry` (`model.go:377-401`) and `LowMarginScanRow` (`model.go:420-455`) are the batch
preview and the graded live-sales scan. Levels are `warn` (margin below the category target but at
or above the floor), `critical` (below the floor) and `invalid` (unknown cost or a non-positive
charged price). Invalid rows are surfaced, never dropped. `LowMarginSaleLine.AvgUnitCostCents` is a
`*int64` and nil means the product has no `product_branch_costs` row at the sale's branch
(`model.go:405-419`).

### 6.5 The SQL, column by column (`backend/migrations/0023_pricing.sql`)

`pricing_category_policy`:

```sql
category_id                 UUID PRIMARY KEY REFERENCES product_categories(id) ON DELETE CASCADE,
margin_target_bps           INTEGER NOT NULL CHECK (margin_target_bps BETWEEN 0 AND 10000),
margin_floor_bps            INTEGER NOT NULL CHECK (margin_floor_bps  BETWEEN 0 AND 10000),
margin_enforcement_disabled BOOLEAN NOT NULL DEFAULT FALSE,
CHECK (margin_floor_bps <= margin_target_bps)
```

The real policy recorded in the header comment: target 30 percent, soft basement 25 percent, hard
floor about 20 percent enforced, with a category adjuster on both target and floor; the exact bps
values are open and the 0024 seed is marked PROVISIONAL (`0023_pricing.sql:10-13`). Per-branch floor
values are deferred but not precluded.

`pricing_product_policy`: `product_id UUID PRIMARY KEY`, `margin_enforcement_disabled BOOLEAN`.

`pricing_list_prices`: `product_id UUID PRIMARY KEY`, `list_price_cents BIGINT CHECK >= 0`,
`price_uom CITEXT REFERENCES uoms(code)`. The header states this row IS the retail anchor per ruling
D-A07-2: manufacturer trade price plus freight when given, else cost times 1.85.

`pricing_rules` (`0023_pricing.sql:48-105`):

```sql
id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
scope          TEXT NOT NULL CHECK (scope IN ('CUSTOMER_PRODUCT','CUSTOMER_CATEGORY','TIER_CATEGORY','TIER_DEFAULT')),
customer_id    UUID REFERENCES customers(id) ON DELETE CASCADE,
customer_type  TEXT CHECK (customer_type IN ('CONTRACTOR','RETAIL','BUILDER','GOVERNMENT','INTERNAL')),
category_id    UUID REFERENCES product_categories(id) ON DELETE CASCADE,
product_id     UUID REFERENCES products(id) ON DELETE CASCADE,
branch_id      UUID REFERENCES locations(id) ON DELETE CASCADE,   -- NULL = all branches
calc_type      TEXT NOT NULL CHECK (calc_type IN ('ABSOLUTE','PCT_OFF_LIST','MARKUP_ON_COST')),
price_cents    BIGINT,
discount_bps   INTEGER,
markup_bps     INTEGER,
price_uom      CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
priority       INTEGER NOT NULL DEFAULT 0,
valid_from     TIMESTAMPTZ,
valid_to       TIMESTAMPTZ,
detail_text    TEXT NOT NULL DEFAULT '',
is_active      BOOLEAN NOT NULL DEFAULT TRUE,
version        INTEGER NOT NULL DEFAULT 1,
created_by     UUID REFERENCES users(id) ON DELETE SET NULL
```

Two CHECKs matter for a comparison:

- exactly one calc value matches the calc type (ABSOLUTE implies `price_cents` and the other two
  NULL, and so on);
- **the discount-percent-only constraint**: `CHECK (scope = 'CUSTOMER_PRODUCT' OR calc_type <> 'ABSOLUTE')`.
  An absolute price per UOM is allowed ONLY on `CUSTOMER_PRODUCT`, a negotiated fixed contract
  price. Tier, customer-category and category scopes must be `PCT_OFF_LIST`. The header records the
  client's words: "never dollars per square foot" (ruling D-A07-2).

There is an anti-collision unique index on the normalised selection key plus `priority` plus
`valid_from`, with NULL scope dimensions coalesced to sentinels, so the total-order tie-break
(`priority DESC, valid_from DESC, id ASC`) never has to reach for `id` between two genuinely
equivalent rows (`0023_pricing.sql:89-105`).

**Reserved and unbuilt**: `CUSTOMER_SUPPLIER` and `SUPPLIER_CATEGORY` are deliberately not in the
CHECK and there is no `supplier_id` column, because `products` has no supplier or vendor column, so
the resolver cannot resolve a product's supplier (`0023_pricing.sql:52-57`).

`quantity_break_headers`: `code CITEXT UNIQUE`, `name`, `product_id` nullable, `category_id`
nullable, `threshold_uom CITEXT NOT NULL DEFAULT 'EA'`, `is_active`,
`CHECK (product_id IS NOT NULL OR category_id IS NOT NULL)`. The threshold UOM may be a weight UOM,
which is what supports the partial-skid surcharge; bucketing goes through the catalog conversion
seam, never a direct `product_uoms` SELECT.

`quantity_break_items`: `header_id`, `from_qty DECIMAL(19,4) CHECK >= 0`, `to_qty DECIMAL(19,4)`
(NULL means open-ended top tier), `value_type TEXT CHECK IN ('ABSOLUTE','PCT_OFF_LIST','SURCHARGE_PCT','SURCHARGE_ABS')`,
`price_cents`, `discount_bps`, `surcharge_bps`, `surcharge_cents`, `price_uom`, with a four-arm CHECK
tying the value to the type. Note the **SQL enum and the Go enum differ in spelling**: the table
says `ABSOLUTE` and `PCT_OFF_LIST`, the Go `BreakValueType` set says `ABS_OFF` and `PCT_OFF`
(`model.go:17-27`), and migration `0026_quantity_break_adjustments.sql` is the file that turned
breaks into adjustments on the tier-resolved base and widened the CHECK.

`customer_category_overrides`: `customer_id`, `category_id`, `calc_type`, `price_cents`,
`discount_bps`, `markup_bps`, `price_uom`, `notes`, `version`, `UNIQUE (customer_id, category_id)`,
and `CHECK (calc_type <> 'ABSOLUTE')` for the same "never dollars per square foot" reason.

`pricing_audit_log`: `entity_type`, `entity_id`, `action CHECK IN ('CREATE','UPDATE','DELETE','BYPASS')`,
`field_name`, `old_value`, `new_value`, `user_id`, `created_at`, plus `ref_entity_type`/`ref_entity_id`
added by 0098 and the document reference added by 0114, plus `bypass_reason` with its own CHECK.

`0025_pricing_bps_ranges.sql` adds defence-in-depth bps range CHECKs on the rule and override calc
value columns.

### 6.6 Promotions and clearance (`backend/migrations/0111_pricing_promotions_clearance.sql`)

`pricing_promotions`:

```sql
code        CITEXT NOT NULL UNIQUE,
name        TEXT NOT NULL,
product_id  UUID REFERENCES products(id) ON DELETE CASCADE,
category_id UUID REFERENCES product_categories(id) ON DELETE CASCADE,
branch_id   UUID REFERENCES locations(id) ON DELETE CASCADE,   -- NULL = every branch
CHECK (product_id IS NULL OR category_id IS NULL),             -- product XOR category; BOTH NULL = store-wide
starts_at   TIMESTAMPTZ NOT NULL,
ends_at     TIMESTAMPTZ NOT NULL,
CHECK (ends_at > starts_at),
calc_type   TEXT NOT NULL CHECK (calc_type IN ('PCT_OFF','AMT_OFF','FIXED_PRICE')),
discount_bps   INTEGER CHECK (discount_bps IS NULL OR (discount_bps >= 0 AND discount_bps <= 10000)),
discount_cents BIGINT  CHECK (discount_cents IS NULL OR discount_cents >= 0),
price_cents    BIGINT  CHECK (price_cents IS NULL OR price_cents >= 0),
price_uom   CITEXT REFERENCES uoms(code) ON DELETE RESTRICT
```

Date bounds are half-open `[starts_at, ends_at)`, so the boundary test is
`now >= starts_at AND now < ends_at` with no midnight ambiguity. Both product and category NULL is
legal and means a store-wide promotion; the comment explains that refusing to express it would push
a user into authoring one promotion per category and forgetting one. `price_uom` is meaningful only
for `FIXED_PRICE`.

`pricing_clearance_items`: `product_id UUID PRIMARY KEY`, `reason`, `clearance_price_cents BIGINT`
nullable, `price_uom` nullable, `CHECK ((clearance_price_cents IS NULL) = (price_uom IS NULL))`,
`started_at`, `ends_at` nullable (clearance runs until the stock is gone), `is_active`, `version`.
When the price is set, clearance behaves as the most specific promotion there is; when NULL the row
is a **badge only** and the item prices through the ordinary waterfall.

### 6.7 Rebates (`backend/migrations/0112_pricing_rebates.sql`)

`rebate_programs`: `code CITEXT UNIQUE`, `name`, `vendor_id NOT NULL`, `product_id` XOR
`category_id` XOR everything-this-vendor-supplies, `basis TEXT CHECK IN
('PCT_OF_SALE','PCT_OF_COST','AMT_PER_UNIT')`, `rate_bps INTEGER` (0 to 10000) or
`rate_cents_per_unit BIGINT`, with a CHECK tying the rate column to the basis, `starts_at NOT NULL`,
`ends_at` nullable. The header states why the basis is not cosmetic: manufacturers write rebate
sheets in all three forms and picking the wrong one is a silent order-of-magnitude error.

`rebate_accruals`: `program_id`, `vendor_id` (denormalised deliberately, because a program's vendor
is fixed at creation and there is no update path for it), `product_id`, `branch_id`, `source_type
TEXT CHECK IN ('POS_SALE','ORDER','ADJUSTMENT')`, `source_id` with
`CHECK ((source_type = 'ADJUSTMENT') = (source_id IS NULL))`, and a `status CHECK IN ('ACCRUED')`.
The module is CORE ACCRUAL ONLY per the migration header; the table is append-only and corrections
are `ADJUSTMENT` rows.

### 6.8 UOM-aware pricing (`backend/migrations/0113_pricing_uom_conversions.sql`)

See 5.3. The scoping rule is product XOR category XOR global, most specific wins **per edge**, so a
product can override just its weight edge and inherit its category's pallet edge. `round_up_at_pick`
lives on the edge and not on the UOM because the same code can be divisible for one product and not
another: a partial pallet of bagged goods is normal, a partial pallet of banded slabs is not.

### 6.9 Price files, imports and the change log

- `0039_price_change_log.sql`: `price_batches`, `price_change_lines`, `price_change_log`,
  `price_import_profiles`, `pricing_anchor_policy`. Batch manufacturer price updates with scheduled
  effective dates; batch status set is `DRAFT | STAGED | APPROVED | APPLIED | CANCELLED`.
- `0198_price_file_import.sql`: `price_import_runs`, a per-vendor column mapping created when a file
  arrives. Import status set is `IMPORTED | FAILED`.
- Go: `pricefile_model.go`, `pricefile_repository.go`, `pricefile_service.go`, `pricefile_handler.go`
  and `xlsx_reader.go` (reading the spreadsheet without a third-party library, the mirror of
  `internal/export`'s writer).

### 6.10 Margin visibility, the override authority, and cost compare

`override_authority.go` implements the DIB-420 change: `OverrideRequested` is a **request** for a
below-floor price, and whether one is honoured is decided server-side by the `OverrideAuthority`
seam against the guard-resolved caller (`model.go:207-212`). Two authority sources exist:
`AuthorityApproval` (an APPROVED margin override on the approvals substrate) and
`AuthorityManagerScope` (the caller's own manager scope over the branch being priced), each mapping
to its own audit bypass reason (`model.go:76-88`). The composition-root adapter is
`w5cOverrideAuthority` (`backend/cmd/server/main.go:3592`).
`0133_pricing_override_authority_reason.sql` carries the schema half.

`gate.go` holds the margin gate that delegates to `identity.CheckMarginAt`. Cost comparison surfaces
live on the frontend (`app/src/price-cost-compare.ts`) over the resolver's cost basis.

### 6.11 The pricing route surface

`backend/internal/pricing` registers 37 route strings under `/api/v1/pricing`, with `readGuard` and
`writeGuard` supplied from `main.go:199` as `RequireStaff` and `RequireManager`
(`backend/cmd/server/main.go:199`), plus a `requirePathUUID` wrapper of its own.

### 6.12 Frontend surfaces that consume pricing

| File | What it is |
|---|---|
| `app/src/pricing-admin.ts` | the `hos-pricing-admin` screen mounted at `#sales/pricing` |
| `app/src/pricing-shared.ts` | shared pricing view helpers |
| `app/src/rule-editor.ts` | the `pricing_rules` editor |
| `app/src/customer-pricing.ts` | per-customer overrides |
| `app/src/quantity-breaks.ts` | the break header and tier editor |
| `app/src/promo-admin.ts`, `app/src/promo-logic.ts` | promotions and clearance admin, plus the pure selection logic |
| `app/src/pricing-rebates.ts` | the rebate program and accrual ledger |
| `app/src/pricing-uom.ts`, `app/src/pricing-uom-logic.ts` | the conversion edge editor |
| `app/src/pricing-audit.ts`, `app/src/_pricing-audit.ts` | the audit log viewer and its pure half |
| `app/src/price-breakdown.ts` | the "why is this the price" breakdown component |
| `app/src/price-batches.ts`, `app/src/price-change-log.ts` | scheduled manufacturer price updates |
| `app/src/price-file-import.ts`, `app/src/price-file-api.ts`, `app/src/price-file-logic.ts`, `app/src/price-imports.ts` | the vendor price-file import |
| `app/src/price-cost-compare.ts` | cost compare |
| `app/src/lowmargin-report.ts` | the low-margin report |
| `app/src/margin-grid.ts` | the margin grid |
| `app/src/pricing-doc.ts` | the printable pricing document (the POS "pricing order") |
| `app/src/pricing-words-logic.ts` | the pure copy layer for pricing vocabulary |
| `app/src/pos-api.ts` plus `POST /api/v1/pos/counter-prices` and `POST /api/v1/pos/margin-glance` | the counter's per-line price and the manager-gated margin glance |

Margin visibility at the counter is gated in the **marshal layer**, not the handler:
`pos/rev8.go`'s `marginAllowed` and `quote/builder.go`'s `costAllowed`
(`backend/internal/settings/settings.go:25-28`). Migration `0170_pos_margin_job_fields.sql` carries
the dealer config plus a per-user quick-hide.

---

## 7. Tax

`backend/internal/tax` (8 Go files) is the configurable, effective-dated Ontario HST engine that
replaced the flat 13 percent POS stub through the frozen `pos.TaxAssessor` seam
(`backend/internal/tax/module.go:1-10`).

What it owns and does:

- The **effective-dated rate table** `tax_rates` with `effective_from` (migration
  `0042_tax_engine.sql`), plus `tax_config`.
- **Status-card exemptions**: three FORCED-evidence profiles in `tax_status_card_profiles`, with
  masked-only serialisation and the zero-percent evidence hard gate preserved from the earlier work.
- The **round-once largest-remainder per-line tax split**, which goes through
  `moneyutil.AllocateLargestRemainder` (`backend/internal/platform/moneyutil/allocate.go:5-18`,
  which names the per-line tax split and the landed-cost allocation as its two callers).
- **Evidence**: `tax_evidence` (0042) over the earlier `tax_evidence_doc`, `tax_decision_snapshot`
  and `purchaser_eligibility_profile` (0029). The snapshot recompute is insert-only and
  audit-truth only.
- The **HST to GL reconciliation** through a `GLBalanceReader` seam.

Ontario only at this stage; Avalara and postal-code routing are dormant stubs
(`module.go:9`). `backend/internal/localization` is the shipped half of the Canadian localisation
scope: postal-code validation and normalisation plus an ISO-3166-2:CA province constraint applied
server-side on the customer and address write path, with a note that the tracker title claimed an
Avalara fallback and postal routing, neither of which was built
(`backend/internal/localization/localization.go:1-12`).

Tax admin surfaces: routes `GET/POST /api/v1/tax/{config,rates,profiles,evidence}`,
`GET /api/v1/tax/recon`, `GET /api/v1/tax/review-queue`, `POST /api/v1/tax/recompute`,
`POST /api/v1/tax/evidence/{id}/validate`, `POST /api/v1/tax/evidence/{id}/download`. Frontend:
`app/src/tax-admin.ts` (the `hos-tax-admin` element at `#compliance/tax`),
`app/src/tax-admin-logic.ts`, `app/src/tax-api.ts`, `app/src/tax-evidence.ts`,
`app/src/pos-assess-relief.ts` (the counter's relief assessment).

The relief assessment emits `tax.relief.assessed`, which GL consumes as a posting topic
(`backend/internal/gl/posting.go:41`).

---

## 8. Quotes, orders, POS, invoices, payments, till

### 8.1 Lifecycle states, from the database CHECKs

| Entity | States | Source |
|---|---|---|
| Quote | `DRAFT, SENT, ACCEPTED, CONVERTED, EXPIRED, LOST` | `0008_quotes_orders.sql` |
| Order | `DRAFT, CONFIRMED, PICKING, READY, PARTIALLY_FULFILLED, FULFILLED, INVOICED, CLOSED, CANCELLED` | `0008_quotes_orders.sql` |
| Order fulfillment | `PICKUP, DELIVERY, WILL_CALL` | `0008_quotes_orders.sql` |
| Invoice | `DRAFT, FINALIZED, PARTIALLY_PAID, PAID, VOID` | `0009_invoices_ar.sql` |
| Payment | `RECEIVED, DEPOSITED, BOUNCED, REFUNDED, VOID` (`PENDING` added for a queued offline card) | `0010_payments_till.sql`, `0027_payment_config.sql` |
| Till session | `OPEN, CLOSED` | `0010_payments_till.sql` |
| POS sale | `COMMITTED, VOIDED` | `0029_pos_counter_sales.sql` |
| POS return | `COMMITTED` only | `0043_pos_counter_v2.sql` |
| Parked ticket | `PARKED, RESUMED, DISCARDED` | `0043_pos_counter_v2.sql` |
| Return pickup | `NONE, REQUESTED, SCHEDULED, DONE` | `0043_pos_counter_v2.sql` |
| GL journal | `DRAFT, POSTED, VOID` | `0007_gl_block.sql` |
| Fiscal period | `OPEN, CLOSED` | `0007_gl_block.sql` |
| Payment intent | `PENDING, CHARGING, SETTLED, FAILED, ABANDONED` | `0103_payment_intents.sql` |
| Delivery run | `PLANNED, DISPATCHED, COMPLETED, CANCELLED` | `0090_dispatch_core.sql` |
| Delivery stop | `READY, LOADED, EN_ROUTE, DELIVERED, FAILED, CANCELLED` | `0090_dispatch_core.sql` |
| Task | `QUEUED, PUSHED, ACCEPTED, IN_PROGRESS, STAGED, DONE, CANCELLED` | `0073_tasks_and_task_events.sql`, widened by `0095_yard_pick_stage.sql` |
| Purchasing PO | `DRAFT, ORDERED, PARTIAL, RECEIVED, CLOSED, CANCELLED` | `0120_purchasing_core.sql` |
| Baseline PO (0011) | `DRAFT, SUBMITTED, PARTIALLY_RECEIVED, RECEIVED, CLOSED, CANCELLED` | `0011_vendors_purchasing.sql` |
| Manifest | `DRAFT, FINAL, DISPATCHED, COMPLETED, CANCELLED` | `0012_manifests.sql` |
| Stock transfer | `DRAFT, IN_TRANSIT, RECEIVED, CANCELLED` | `0116_stock_transfers.sql` |
| Stock reservation | `ACTIVE, RELEASED, CONSUMED, EXPIRED` | `0115_stock_reservations.sql` |
| Count sheet | `OPEN, FROZEN, COUNTED, POSTED, CANCELLED` | `0040_inventory_counts.sql` |
| Landed cost | `DRAFT, ALLOCATED, POSTED, VOIDED, RETRO_ADJUSTED` | `0041_landed_cost.sql` |
| Lien job | `OPEN, SUBSTANTIALLY_PERFORMED, RELEASED, CLOSED` | `0038_lien_holdback_compliance.sql` |
| Lien document | `DRAFT, PENDING, ISSUED, SUPERSEDED` | `0038_lien_holdback_compliance.sql` |
| Compliance deadline | `OPEN, SATISFIED, WAIVED, OVERDUE` | `0038_lien_holdback_compliance.sql` |
| Approval request | `PENDING, APPROVED, REJECTED` | `0016_permission_approvals.sql` |
| Outbox event | `PENDING, PUBLISHED, DEAD` | `0013_audit_outbox.sql` |
| Print job | `QUEUED, PRINTED, FAILED` | `0088_print_queue.sql` |
| SMS outbox | `QUEUED, DELIVERED, FAILED` | `0097_sms_outbox.sql` |
| Job run | `PENDING, RUNNING, SUCCEEDED, FAILED, SKIPPED` | `0096_job_scheduler.sql` |
| Quote share link | `ACTIVE, CONFIRMED, REVOKED` | `0086_quote_share_links.sql` |
| Promise to pay | `OPEN, KEPT, BROKEN, CANCELLED` | `0176_ar_promises_to_pay.sql` |
| Invoice dispute | `OPEN, RESOLVED` | `0177_ar_invoice_disputes.sql` |
| Paper-path flag | `OPEN, MOVED_TO_PAPER, DISMISSED` | `0183_ar_landing_reminders.sql` |
| Bank statement line | `UNMATCHED, MATCHED, EXCLUDED` | `0033_bank_recon.sql` |
| GL tie-out | `OPEN, TIED, OUT_OF_TIE` | `0032_gl_module.sql` |
| Warranty settlement posting | `POSTED, NO_EFFECT, SEAM_UNWIRED, FALLBACK_GL, FAILED, MANUAL` | `0129_warranty_settlement_paths.sql` |
| Procurement project | `PLANNING, QUOTING, ORDERED, FULFILLED, CANCELLED` | `0031_procurement.sql` |
| Rebate accrual | `ACCRUED` | `0112_pricing_rebates.sql` |
| Price batch | `DRAFT, STAGED, APPROVED, APPLIED, CANCELLED` | `0039_price_change_log.sql` |
| Commission rule | `DRAFT, APPROVED, RETIRED` | `0144_commission_rules.sql` |
| User | `INVITED, ACTIVE, SUSPENDED, DEACTIVATED` | `0004_identity_rbac_scaffold.sql` |

Two commit-state vocabularies are worth naming because they are the client's own words rather than
ours: `date_state IN ('REQUESTED','COMMITTED')` and `commit_state IN ('REQUESTED','COMMITTED')` plus
`lock_state IN ('AUTO','UNLOCKED')` and `reopen_policy IN ('ANYONE','MANAGER','NOBODY')`
(`0185_dispatch_commit.sql`, whose header reads "COMMIT, NOT LOCK. The client's vocabulary, and the
model correction it exposes").

### 8.2 Quotes

The `quote` module is both the read service and the contractor-facing state machine
(`backend/internal/quote/model.go:1-8`). Accept and Reject transition the quote in place; Convert
hands off to the order write path and returns the new order's id rather than importing the order
module.

**The credit and hold gate runs inside Convert**, not upstream. The module doc records that the
earlier claim was false: Convert inserted an order behind a status check alone, so a customer on an
ACTIVE credit hold converted straight past the counter. The gate now runs inside the convert
transaction through the `quote.CreditGate` seam, which `till.HoldAwareCreditGate` satisfies
structurally, and it is the SAME gate instance the POS ring uses
(`backend/internal/quote/model.go:10-15`).

Quote holds and expiry:

- `GET /api/v1/quotes/{id}/holds`, `POST .../holds/release`, `POST .../holds/resync`, and
  `GET .../hold-expiry` are the hold surface. The stock hold is a `stock_reservations` row of type
  quote, and `REPORT-W5-A.md` records the invariant "a quote hold is SOFT" with the test that pins
  it (`syncQuoteHoldsTx`: `ResTypeQuote` becomes `ResTypeOrder`).
- `valid_until DATE` on the quote plus `quote_policy` (0173) carry expiry; the house time zone rule
  applies, because a quote valid "until the fifth" must not stop being acceptable in the evening of
  the fifth with the customer at the counter (`backend/internal/testaudit/house_day_test.go:16-27`).
- Follow-up cadence lives in `quote_followup_plans` and `quote_followup_events` (0110) and emits
  `sms.quote.followup` and an email variant.

**The quote guard** is a separate screen (`#sales/quote`, element `hos-quote-guard`,
`app/src/quote-guard.ts`) gated `always` in the nav table, which is the below-margin decision
surface rather than the builder.

**Public quote acceptance**: `app/quote.html` is its own Vite entry because it runs pre-auth and
must not ship session machinery (`app/vite.config.ts:47-50`). It is credentialed by a share-link
token, read at `GET /api/public/v1/quotes/{token}` and accepted at
`POST /api/public/v1/quotes/{token}/accept`. Tables: `quote_share_links`, `quote_share_link_events`,
`quote_acceptances` (0086), plus `quote_accept_provenance` (0162) and acceptance controls in the
same migration. Frontend: `app/src/quote-public.ts` and `app/src/_quote-public.ts`.

`quoteassist` is the assist layer over the builder: extraction produces PROPOSALS only, and the
package has **no writer seam, no quote import and no INSERT against `quote_lines`**, pinned by a
test that scans the package's own source and by an integration test that extracts over a real quote
and counts the lines afterwards (`backend/internal/quoteassist/model.go:7-17`). The optional AI path
may split a paragraph into candidate phrases and read quantities; every product reference is then
resolved by the deterministic resolver against the real catalogue through the same ranked counter
search the POS counter uses, so a model cannot name a SKU
(`backend/internal/quoteassist/model.go:18-27`). Scope-gap rules are data in `quote_scope_rules`
(0159).

### 8.3 Orders

Order edits: `PATCH /api/v1/orders/{id}`, `PATCH .../lines/{lineNo}`, `POST .../lines`,
`DELETE .../lines/{lineNo}`, with an edit trail at `GET .../edits`. Post-pick changes are a separate
aggregate, `change_orders` and `change_order_lines` (0125), with a five-step route set:
`POST /api/v1/orders/{id}/changes`, then `apply`, `cancel`, `reject`, `finance-approve` and
`yard-ack` on `/api/v1/order-changes/{changeId}`.

Duplicate detection: `GET /api/v1/orders/{id}/duplicate-check`, `GET .../duplicate-preview`,
`POST .../duplicate` (`backend/internal/order/…`, frontend `app/src/order-duplicate.ts` and
`app/src/_order-duplicate.ts`).

Multi-shipment fulfilment with a single invoice: `shipment_groups`, `shipment_group_lines` (0126),
routes `GET/PUT /api/v1/orders/{id}/shipments`, `POST .../shipments/invoice`,
`POST .../shipments/invoice-policy`, `POST /api/v1/shipment-groups/{groupId}/status`, and the
group-to-invoice link in 0147.

Lifecycle transitions: `POST /api/v1/orders/{id}/confirm`, `/picking`, `/ready`, `/cancel`. Cancel is
`RequireManager`; the others are staff (`backend/internal/gate/order_lifecycle_test.go:12-19`).

Quoted freight baseline: `0194_order_quoted_freight.sql` adds the quoted freight on the order so a
raised freight has something to be raised above, and `0195_dispatch_freight_raise.sql` adds the
second-truck approval gate as a fifth policy on the one approvals queue.

### 8.4 POS

`backend/internal/pos` (52 Go files) registers 34 routes under `/api/v1/pos`. The flow, from
`docs/design/POS_COUNTER_V2.md` and the shipped files:

1. **Lookup and search.** `GET /api/v1/pos/quick-picks`, `GET /api/v1/pos/stock-glance`,
   `POST /api/v1/pos/counter-prices`, `GET /api/v1/products/counter-search`,
   `GET /api/v1/products/scan` (barcodes from 0101). The hot list opens on the yard's bulk board
   (`0186_pos_hotlist_bulk_board.sql`).
2. **Customer.** `GET /api/v1/pos/account-standing` decides who may see a past-due balance at the
   till (`0181_pos_balance_visibility.sql`); walk-in is a seeded customer (0029).
3. **Margin.** `POST /api/v1/pos/margin-glance`, gated in the marshal layer.
4. **Park and resume.** `GET/POST /api/v1/pos/parked`, `POST .../parked/{id}/resume`,
   `DELETE .../parked/{id}`.
5. **Ring.** `POST /api/v1/pos/sales`, idempotent on a key (`0029_pos_counter_sales.sql`).
6. **Split tender.** `pos_sale_tenders` (0100): one sale settled across multiple tenders (cash plus
   card plus account). Frontend `app/src/pos-split-tender.ts`, `app/src/_pos-split.ts`,
   `app/src/tender-logic.ts`.
7. **Signature.** `POST /api/v1/pos/signatures` and `POST /api/v1/pos/signatures/typed`
   (`pos_signature` 0029, the typed-name attestation provider 0089). Frontend
   `app/src/pos-signature.ts`, `app/src/pos-signature-logic.ts`.
8. **Receipt.** `GET /api/v1/pos/sales/{id}/receipt` served by `printing` through `print_queue`
   (0088).
9. **Returns.** `GET /api/v1/pos/returns/lookup`, `POST /api/v1/pos/returns`,
   `POST .../returns/{id}/pickup`, `GET .../returns/pickups`; and the portal-initiated
   request-and-approve flow `GET /api/v1/pos/return-requests`, `POST .../{id}/approve`,
   `POST .../{id}/reject` over `return_requests` and `return_request_lines` (0045).
10. **Pricing order** (a printable price or compare document that is neither a quote nor an order):
    `pricing_orders` and `pricing_order_lines` (0043), routes `GET/POST /api/v1/pos/pricing-orders`.

Presentation is dealer-configurable data rather than code: `pos_policy` (0043), workspace
preferences (0165), the two-panel workspace layout (0166), lookup presentation as cards or rows
(0167), optional PO or job reference plus salesperson (0168), add-on pairings (0169), margin and job
fields (0170), and the dealer hotkey map (0164, 0171, 0191).

### 8.5 Invoice register, AR landing and period end

The invoice register is `#sales/invoices` (element `hos-invoice-register`,
`app/src/ar-invoice-register.ts`), deliberately filed under Sales and not Finance because it is what
a counter person opens throughout a shift; the sensitive half (the account's balance, limit and headroom) is
gated **inside the payload** rather than at the door (`app/src/nav-access.ts:47-53`).
Backend: `backend/internal/billing/register_handler.go`, tables `ar_invoice_policy` (0178),
`ar_invoice_deliveries` (0175), `ar_promises_to_pay` (0176), `ar_invoice_disputes` (0177).

The AR landing is `#finance/ar` (element `hos-ar-console`) plus the landing surface in
`app/src/ar-landing.ts` and `app/src/ar-landing-surface.ts`, over `ar_landing_policy`,
`ar_reminder_events`, `ar_reminder_sends`, `ar_ap_contacts`, `ar_paper_path_flags` (0183).

Period-end lives in the period-end handler file in `backend/internal/billing` and its screen in
`app/src` (both file names use the calendar word this document avoids), plus `ar_statement_runs` and
`ar_statements` (0105) with notice accounting in 0148. Statement routes:
`GET /api/v1/ar/customers/{customerID}/statement`, `POST /api/v1/ar/statement-runs`,
`GET /api/v1/ar/statement-runs/{id}`, `POST /api/v1/ar/register/statements`.

Collections cadence: `ar_notice_log` and the reminder ledgers (0110, 0183), routes
`GET/POST /api/v1/ar/reminders`, `POST /api/v1/ar/register/reminders`. Credit gates and automatic
holds: `ar_credit_policy` (0104), `credit_holds` (0035), routes `GET /api/v1/ar/holds`,
`POST /api/v1/ar/holds/{id}/override`, `POST /api/v1/ar/holds/{id}/ar-release`,
`GET/PUT /api/v1/ar/auto-hold`, `POST /api/v1/ar/credit-sweep`,
`GET /api/v1/ar/dispatch-gate/{orderID}`.

### 8.6 Bank reconciliation

`backend/internal/bankrec` (10 files). Statement import through a pluggable `StatementSource`; an
auto-match engine with three strategies named in the module doc (gateway-reference exact, lodgement
reference exact, run-settlement fuzzy); manual match and unmatch with history; clearing journals
posted through the GL provider seam; and the reconciliation lifecycle
(`backend/internal/bankrec/module.go:1-4`). It reads GL balances only through `GLBalanceReader` and
never the statement views directly. Tables in 0033. Routes: nine under `/api/v1/bankrec`.
Frontend: `app/src/bankrec-console.ts`, `bankrec-import.ts`, `bankrec-match.ts`, `bankrec-rules.ts`,
`bankrec-shared.ts`, `bankrec-api.ts`.

### 8.7 Financial statements

`backend/internal/finstmt` builds Income Statement, Balance Sheet, Trial Balance, GL Detail and AR
Aging **only** from the posted-filtered views created in `0034_financial_statement_views.sql`
(`v_income_statement`, `v_balance_sheet`, `v_trial_balance`, `v_gl_detail`, `v_ar_aging`) plus
`fiscal_periods`. It performs no posting and reads no other module's tables; a subtly wrong view is
fixed by the integrator in the reserved 0036, never by finstmt
(`backend/internal/finstmt/module.go:1-5`). Its integration-test header lists the acceptance
criteria it proves: posted-only views, the balance-sheet invariant including loans and leases,
branch sum equals consolidated, comparatives including empty history, drill-down ties, aging buckets
tying to `billing.ARBalanceCents`, a performance smoke, and schedules CRUD. `finstmt_schedules`
(0034) is its only table. Seven routes plus schedules CRUD. Frontend: `app/src/finstmt-home.ts` and
six siblings.

### 8.8 Lien and holdbacks, and warranty

`backend/internal/lien` owns `lien_jobs`, `lien_documents`, `lien_participants` and
`holdback_ledger` (0038). It implements the Ontario Construction Act document workflow (Conditional
Release, Unconditional Release, Statutory Declaration, Discharge) with data-driven templates carrying
a DRAFT banner, the triage worklist (dual gated), holdback aging, and the holdback GL through the GL
provider (`backend/internal/lien/module.go:1-5`). The module doc states plainly that this scope has
zero client-validated requirements and is statute-derived. `invoices.holdback_cents` has existed
since 0009. `backend/internal/compliance` holds the deadline calendar that pairs with it; the
worklist is computed on read because there was no scheduler infrastructure when it was built
(`backend/internal/compliance/module.go:1-4`) - `internal/jobs` (0096) arrived later.

`backend/internal/warranty` (27 files) owns fifteen `warranty_*` tables across five migrations and
keys off the vendor, customer, product and invoice masters. Shipped scope per the module doc: the
vendor registry, the tracked claim object with capture from office, counter and invoice doorways, the
client-confirmed Kanban lifecycle with checklist-gated phase advancement, the evidence-reference
seam, manual settlement recording with a GL hand-off, and the headline reports (by-manufacturer
frequency, win and loss, time to settlement, funnel counts, exposure by vendor)
(`backend/internal/warranty/module.go:1-12`). Later migrations added per-outcome settlement money
paths (0129), the recovery board (0130), a public intake form with rate limiting (0131) and per-phase
SLA timers with overdue alerts (0132).

---

## 9. Inventory and yard

| Concern | Module | Tables | Surface |
|---|---|---|---|
| Stock ledger and weighted-average cost | `inventory` | 0005 plus 0037 | `GET /api/v1/inventory/moves`, `/on-hand`, `/browse`, `/branch-stock` |
| Cycle counts | `invcount` | `count_sheets`, `count_lines`, `count_reason_codes`, `inventory_recon_runs` (0040) | `/api/v1/counts` |
| Landed cost | `landedcost` | `landed_costs`, `landed_cost_allocations` (0041) | `/api/v1/landed-costs` |
| Purchase orders, receiving, AP bills | `purchasing` | 0120, 0121, 0122, 0134, 0197 | `/api/v1/purchasing` |
| Portal planning board | `procurement` | `procurement_projects`, `procurement_project_lines` (0031) | portal BFF |
| Receiving (the stock half) | `inventory` | writes `stock_moves` | `POST /api/v1/inventory/receive` |
| Reservations and allocation policy | `inventory` | `stock_reservations`, `branch_allocation_policy` (0115) | `GET/POST /api/v1/inventory/reservations`, `/reservations/release`, `/reservations/expire`, `GET/PUT /api/v1/inventory/allocation-policy` |
| Inter-branch transfers with arrival estimate | `inventory` | `stock_transfers`, `stock_transfer_lines`, `transfer_no_sequences` (0116), `branch_transit_*` (0158) | `GET/POST /api/v1/inventory/transfers`, `/{id}/ship`, `/{id}/receive`, `/{id}/cancel`, `GET /api/v1/inventory/incoming-transfers`, `GET/PUT /api/v1/inventory/transit-*` |
| Replenishment | `inventory` | `product_reorder_points` (0117) | `GET/PUT /api/v1/inventory/reorder-points`, `POST /api/v1/inventory/reorder-points/bootstrap`, `POST /api/v1/inventory/reorder-po`, `GET /api/v1/inventory/shortfall`, `POST /api/v1/inventory/shortfall/create-po`, `POST /api/v1/inventory/nudge-procurement` |
| Yard console and pick queue | `tasks` | `tasks`, `task_events` (0073), `task_shortages` (0095), `task_status_advance_rules` (0108), `task_pick_lines` (0187) | `/api/v1/yard/pick-queue`, `/picks/{id}`, `/picks/{id}/lines`, `/picks/{id}/stage`, `/picks/{id}/weight`, `/picks/{id}/shortages`, `/weight-requests`, `/pending-tickets`, `/advance-rules/{taskType}` |
| Branch stock lookup | `inventory` | `stock_balances` | `GET /api/v1/inventory/branch-stock` |

The transfer arrival estimate resolves in the house time zone
(`backend/internal/inventory/transfer_eta.go`, cited in
`backend/internal/gl/businessday.go:27` and `backend/internal/testaudit/house_day_test.go:29-31`).

**The yard console** is a registered unit of its own (`yard`) with one module (`yard`), element
`hos-yard-console` (`app/src/yard-console.ts`), classified `work` in the page-type contract, gated
`authed`. Its pure logic half is `app/src/yard-logic.ts`. `NOTES-UNION-TRAIN16.md` records the
shipped scope: the union queue, per-line picks, bulk loading and paging.

`tasks` never joins orders or messaging: it validates an order and reads its branch through the
narrow `OrderReader` interface, and posts chat EVENTs through a narrow `ChatNotifier`, both injected
in `main.go` (`backend/internal/tasks/model.go:5-8`). The module doc also records what is still
gated off: the dispatch and weight round-trips and push-timer auto-assignment leave their schema
seams but no code drives them (`backend/internal/tasks/model.go:10-13`). Migration 0108 and the
DIB-483 work later filled parts of that.

**Negative stock** is a first-class read: `GET /api/v1/counts/negative-stock`. The relaxed
negative-stock CHECK is 0037's doing; `v_stock_balance_drift` is the reconciliation view
(0021, corrected in 0022).

---

## 10. Dispatch and driver

### 10.1 The dispatch module

`backend/internal/dispatch` (43 Go files) owns the physical leg of the order lifecycle. Its doc names
what it does **not** own: not stock (inventory decrements at the ring or fulfilment, not here), not
money (the handback advances the order through the existing order seam and billing does the rest),
not routing (no optimiser, no map service, no PostGIS, just latitude and longitude and a haversine
estimate, ruled by the commander), and not a fleet system (trucks and drivers are a two-column
registry, deliberately) (`backend/internal/dispatch/model.go:11-14`).

Surfaces, from the 47 route strings:

- **Load board**: `GET /api/v1/dispatch/loadboard`, `/loadboard/calendar`, `/loadboard/gaps`,
  `GET /api/v1/dispatch/orders/{id}/gaps`, `POST /api/v1/dispatch/loads/mark-loaded`,
  `POST /api/v1/dispatch/auto-assign` (`0184_dispatch_loadboard.sql`). Frontend
  `app/src/dispatch-loadboard.ts`, `dispatch-loadboard-surface.ts`, `dispatch-loadboard-api.ts`,
  `dispatch-load-logic.ts`.
- **The board and the schedule**: `GET /api/v1/dispatch/board`, `app/src/dispatch-board.ts`,
  `app/src/dispatch-schedule.ts`.
- **Runs and stops**: `POST /api/v1/dispatch/runs`, `/runs/{id}/crew`, `/runs/{id}/dispatch`,
  `/runs/{id}/complete`, `/runs/{id}/cancel`, `/runs/{id}/reopen`, `/runs/{id}/reopen/clear`,
  `POST /api/v1/dispatch/stops`, `/stops/{id}/assign`, `/stops/{id}/status`, `/stops/{id}/commit`,
  `/stops/{id}/reopen`, `/stops/{id}/load-facts`, `/stops/{id}/mto`.
- **Manifest**: `GET /api/v1/dispatch/runs/{id}/manifest`, `POST .../manifest/print` (into
  `print_queue` as a delivery document; the 0088 CHECK is what the integration test proves).
- **Will-call**: `GET/POST /api/v1/dispatch/will-call`, `POST .../will-call/{id}/revoke`, and the
  public pair `GET /api/public/v1/will-call/{token}` and `POST .../{token}/confirm`
  (`0092_will_call_pickups.sql`). Frontend `app/src/dispatch-willcall.ts` and
  `app/src/dispatch-willcall-public.ts`.
- **Proof of delivery**: `POST /api/v1/dispatch/stops/{id}/pod`, `/photos`, `/attach-photos`,
  `GET /api/v1/dispatch/photos/{id}` (`0091_dispatch_pod_photos.sql`, attachment in 0107).
  Frontend `app/src/dispatch-pod.ts`.
- **Scan trail**: `POST /api/v1/dispatch/scans`, `GET /api/v1/dispatch/scan-trail`
  (`load_scan_checkpoints`, 0108). Frontend `app/src/dispatch-scan-trail.ts`.
- **Reschedule**: `POST /api/v1/dispatch/runs/{id}/reschedule`, `/stops/{id}/reschedule`,
  `GET .../reschedules` (`dispatch_reschedules`, 0107). Frontend `app/src/dispatch-reschedule.ts`.
- **Fleet**: `GET/POST /api/v1/dispatch/trucks`, `/drivers` (`dispatch_trucks`,
  `dispatch_drivers`, `dispatch_branch_depots`, seeded fleet in 0093). Truck types are
  `FLATBED, BOOM, DUMP, PICKUP, TRAILER`. Frontend `app/src/dispatch-fleet.ts`.
- **Freight**: `POST /api/v1/dispatch/freight-split`, `GET /api/v1/dispatch/freight-raises/{id}`,
  over `delivery_freight_raises` (0195) and the quoted freight baseline (0194).
- **Policy**: `GET/PUT /api/v1/dispatch/policy`, `GET /api/v1/dispatch/policy/history`
  (`dispatch_policy`, 0184, extended by 0185 and 0209). Frontend `app/src/dispatch-settings.ts`,
  `app/src/dispatch-settings-logic.ts`.
- **Driver-facing**: `GET /api/v1/dispatch/my-runs`, `GET /api/v1/dispatch/stops/{id}`,
  `GET /api/v1/dispatch/runs/{id}/payment-holds`.

The AR dispatch gate is a seam: `arGateFor(log)` in `main.go:2965` returns either
`stubARGate` (always allows) or the real gate depending on the `DISPATCH_AR_GATE` environment
variable, and the union gate lives at `main.go:3095`.

### 10.2 HH Drive, and why it is a separate bundle

`app/drive.html` is the sixth Vite entry. The stated reason is not "it runs pre-auth" (that is
`quote.html`'s reason) but "it runs on a phone on cellular at the side of a road"
(`app/vite.config.ts:53-61`). The driver view remains a tab inside `index.html` as well, because a
dispatcher reads it over a driver's shoulder, and `drive.ts` imports the **same**
`dispatch-driver-run.ts`, `dispatch-api.ts` and `dispatch-logic.ts`, so there is exactly one
lifecycle matrix and one API client. The separation is about bytes.

The measurement that produced it (`app/scripts/bundle-fence.mjs:11-16`): `main-*.js` in the checked
in `dist` was 1,944,469 bytes, and the driver view was a tab inside it, so opening the run sheet
meant fetching the load board, purchasing, replenishment, counts and inventory over cellular. The
component named no money; the bundle it arrived in was full of it, and a driver was downloading
`value_cents` nine times.

`bundleFence` therefore fails the build if the driver entry's transitive source contains any of the
forbidden money words, or another surface's component, and prints the entry's weight against main's
on every build. The forbidden list deliberately excludes `formatMoney` (an identifier, which
minifies) and uses words that survive minification: four JSON keys and three English words
(`app/scripts/bundle-fence.mjs:40-51`). The shipped measurement recorded in
the most recent session notes file at the repository root (`NOTES-SESSION-*.md`) is "drive entry 13,731 B with 0 money words".

There is a `public/manifest.webmanifest` and drive icons, so HH Drive is installable. **There is no
service worker** (see section 18).

**Driver session length** is a dealer knob: a bounded driver-session column on
`dispatch_policy`, migration `0209_how_long_a_driver_stays_signed_in_is_a_dealers_knob.sql`, read
through the `driverSessionTTLAdapter` type in `backend/cmd/server/main.go`, with the verifier ceiling
`identity.MaxDriverSessionTTL` pinned to exactly that column's maximum by a test in the dispatch
package (`backend/internal/identity/auth.go:26-33`).

---

## 11. Messaging and notifications

### 11.1 `messaging` (51 Go files)

The internal messaging hub: channels, DMs and threads, system EVENT messages, unread badges, and a
headless full-screen web route (`backend/internal/messaging/model.go:1-8`). It reuses the existing
transactional outbox and `notify.Dispatcher` for system events; no new broker.

Tables by migration: core channels, members, messages and attachments (0050); the unread summary
view (0051); demo channels (0052, 0060, 0139); notification preferences and channel tiers plus an
alerts firehose (0069); retention policy and an archive that never deletes (0070); screen-takeover
forced acknowledgement with `msg_acks` (0071); the contractor-portal inbound bridge to a house queue
(0072); per-branch role team channels (0076); rep assignments and the house fallback queue (0136);
per-channel notification modes (0137); high-priority automations and supersede (0138); event-class
tiers plus a routine rollup and standalone payment channels (0152); targeted payment notification
opt-in rules (0157).

Routes: 21 under `/api/v1/messaging`, including a server-sent-event stream
(`GET /api/v1/messaging/stream` plus `POST /api/v1/messaging/stream-ticket`), an export, and
`POST /api/v1/messaging/inbound/customer` for the portal bridge.

The rollup resolves in the house time zone (`backend/internal/messaging/rollup.go:46-50`, quoted in
`backend/internal/gl/businessday.go:33-36`): a UTC boundary cuts the Ontario working period in the
evening and files the evening's receipts under the next one.

`app/messages.html` is a dedicated Vite entry for the headless full-screen second-screen surface
(`app/vite.config.ts:38-40`, `app/src/messages.ts`).

### 11.2 `messagingprovider` (23 Go files)

The out-of-band SMS arm. Every notifier satisfies `notify.Notifier`, the same port the portal's
invite and reset mail already travels through, and delivery is driven by `notify.Dispatcher` with the
shipped retry and dead-letter policy. What it adds is a real SMS provider implementation, two
message templates, and a `notify.OutboxStore` over the **disjoint** `sms_outbox` table (0097), so an
SMS outage can never retry, block or dead-letter a GL journal
(`backend/internal/messagingprovider/provider.go:1-13`).

**The stub is the default and the flag flip is a config act.** `Select()` returns `StubNotifier`
unless the environment both asks for the provider and supplies complete credentials. There are no
credentials in this package, this repository, or any test, only the **names** of environment
variables documented on the Settings screen; turning it on is setting four environment variables on
the box (`provider.go:16-27`). Every walkthrough and test in the program passes with the flag off.

The **send window** (`backend/internal/messagingprovider/policy.go`): `DefaultZone =
"America/Toronto"` (`policy.go:38-40`); the quiet-window type is expressed in local minutes past
midnight with a clamping constructor, configured through `SMS_WINDOW_START_MIN` and its sibling; and
a named zone that cannot be loaded raises a loud error rather than a silent UTC fallback
(`policy.go:110-118`).
**Consent** is a gate, not a preference: `customer_sms_prefs` (0109) holds consent, the quiet
window and a send-window column; absence means no. The customer module's integration test header records that the
table had zero rows and no writer reachable by a member of staff, so every SMS was skipped for want
of consent, correctly and forever, until the staff control shipped
(`backend/internal/customer/dib482_sms_consent_integration_test.go:1-6`). The control is
`GET/PUT /api/v1/customers/{id}/sms-consent`, frontend `app/src/customer-sms-consent.ts` and
`app/src/_sms-consent.ts`.

SMS topics: `sms.ar.notice`, `sms.quote.share`, `sms.quote.followup`, `sms.dispatch.notice`, plus the
AR notice ledger `ar_notice_log` (0110).

### 11.3 `notify` (8 Go files)

The contractor-portal notification seam and the universal out-of-band delivery dependency: a
transactional outbox dispatcher plus a provider-port `Notifier`. **It imports no other internal
module**; handlers map a topic's payload to a `notify.Message` and live in the owning domain,
registered against the dispatcher at wire time (`backend/internal/notify/notifier.go:1-9`).

The dispatcher's `OutboxStore.ProcessBatch(ctx, n, settle)` claims up to n PENDING rows with the
SKIP LOCKED claim living in the store (`backend/internal/notify/dispatcher.go:46-62`). Registration
kinds: `Register`, `RegisterSecret`, `RegisterObserver`, with `validateObservers` at wire time
(`dispatcher.go:102-153`). `Run` polls; `DrainOnce` is the test and job entry point.

### 11.4 Feedback widget and tour

`backend/internal/feedback` bridges in-app staff feedback to the Dibbits Plane board: a widget in the
ERP shell posts bug, feedback and flag submissions, and each becomes a Plane issue in the project's
Feedback module. The Plane API key lives server-side only and the frontend never sees Plane;
submissions require an authenticated staff session and are rate limited per user so a stuck retry
loop cannot flood the board (`backend/internal/feedback/feedback.go:1-9`). Frontend
`app/src/feedback-widget.ts`.

`backend/internal/tour` is the guided demo walkthrough: nine chapters from migration 0192, the steps
inside them, and the per-person bookmark of 0193. The acceptance clause that shapes the whole
package is "byte-absent from payloads when off, not CSS-hidden": when the deployment is not a demo,
or the dealer has turned the tour off, `GET /api/v1/tour` answers the two bytes `{}`, achieved
through `omitempty` on every field of a zero-value type rather than a handler decision
(`backend/internal/tour/model.go:5-17`). The doc also records that three separate work trains in
this repository shipped a `"field":null` believing it was the same as an absent key, which is why the
unit gate scans the off bytes for the string `null` (`model.go:23-30`). Frontend
`app/src/tour-layer.ts` and `app/src/_tour.ts`, which re-sorts by position and key because it is
written not to trust a body's order.

---

## 12. The contractor portal inside the ERP

This section matters for lanes comparing the ERP's own contractor surface against the HH Pro
repository, so it is precise about which capability lives where.

### 12.1 What it is

`backend/internal/portal` (43 Go files) plus `app/portal.html` and `app/src/portal/**` (31 files).
It is a **BFF inside the monolith**: `/api/portal/v1/*` is a published surface that composes other
domain services **in process** through Go seams, never via ERP HTTP and never via cross-module
database reads (`docs/contractor-portal-plan.md` section 4.1).

`portal.Handler` holds the injected domain seams and a nil seam means that feature's handlers return
501 (`backend/internal/portal/handler.go:29-49`): `customerSvc`, `billingSvc`, `orderSvc`,
`quoteSvc`, `paymentSvc`, `procurementSvc`, `productSvc`, `returnsSvc`, `projectsSvc`.

Request bodies are capped at 1 MiB (`handler.go:24-26`).

### 12.2 How it authenticates

Per-person email and password, HS256 JWT with a **separate `PortalJWTSecret` audience** enforced as a
config invariant, enumeration-safe login with a constant-time miss path, and a per-request database
re-load of role and status so a demotion or deactivation takes effect immediately
(`docs/contractor-portal-plan.md` section 3.1; `backend/internal/portal/auth.go`,
`backend/internal/portal/service.go`). Invites are single-use, time-boxed and sha256-hashed, with a
three-phase anti-denial-of-service accept. Tenant isolation is by `customer_id` on the verified
token, with cross-tenant reads answering not-found.

Public portal routes: `POST /api/portal/v1/login`, `POST /api/portal/v1/accept-invite`,
`GET /api/portal/v1/config` (`backend/internal/portal/handler.go:89-91`). Everything else is behind
a capability guard, and the team routes behind `h.admin` (`handler.go:94-98`).

`POST /api/portal/v1/token/refresh` and `POST /api/portal/v1/password-reset/{request,confirm}` exist.

### 12.3 Roles and capabilities

`backend/internal/portal/rbac.go` defines five roles (the CHECK set on `portal_users.role` and
`portal_invites.role`, migration 0019 widened by 0160):

| Role constant | DB value | Meaning |
|---|---|---|
| `RoleAccountAdmin` | `account_admin` | manages the company's users and sees billing |
| `RoleBuyer` | `buyer` | transacts, sees billing, no team management |
| `RoleFieldCrew` | `field_crew` | orders and deliveries only, no financials, no team |
| `RoleBuyerNoPay` | `buyer_no_pay` | orders, RFQ and deliveries; **cannot pay** |
| `RoleAPFinance` | `ap_finance` | pays and manages payment methods; **cannot order** |

The last two are explicitly the ERP's expression of two HH Pro personas: the comment names them
"HH Pro `pm`" and "HH Pro `ap`", and states the reason they exist: `buyer` held both `PayInvoices`
and `CreateOrders`, and a six-person contractor outfit does not work that way, so without them the
HH Pro portal would have had to render a role picker the server cannot enforce
(`backend/internal/portal/rbac.go:17-29`).

Every endpoint gates on a **capability**, never a raw role (`rbac.go:31-48`):

```go
type Capabilities struct {
    ManageUsers              bool `json:"manage_users"`
    ViewBilling              bool `json:"view_billing"`
    PayInvoices              bool `json:"pay_invoices"`
    ManagePaymentMethods     bool `json:"manage_payment_methods"`
    SubmitRFQ                bool `json:"submit_rfq"`
    CreateOrders             bool `json:"create_orders"`
    ViewOrdersDeliveries     bool `json:"view_orders_deliveries"`
    EditDeliveryInstructions bool `json:"edit_delivery_instructions"`
    ManageFiles              bool `json:"manage_files"`
}
```

`CapabilitiesFor(role)` maps a role to its capabilities; an unknown role grants nothing
(`rbac.go:50-54`).

### 12.4 The full portal route surface (52 routes)

Auth and account: `POST /login`, `POST /accept-invite`, `GET /config`, `GET /me`, `PUT /me`,
`POST /me/password`, `POST /token/refresh`, `POST /password-reset/request`,
`POST /password-reset/confirm`.

Team: `GET /users`, `PUT /users/{id}/role`, `PUT /users/{id}/status`, `GET /invites`,
`POST /invites`.

Dashboard and billing: `GET /dashboard`, `GET /billing/summary`, `GET /invoices`,
`GET /invoices/{id}`.

Orders: `GET /orders`, `GET /orders/{id}`, `PUT /orders/{id}/delivery-instructions`,
`GET /orders/{id}/reorder-preview`, `POST /orders/{id}/reorder`,
`GET /orders/{id}/return-eligibility`, `POST /orders/{id}/return-requests`,
`GET /return-requests`.

Quotes: `GET /quotes`, `GET /quotes/{id}`, `POST /quotes/{id}/accept`, `POST /quotes/{id}/reject`,
`POST /quotes/{id}/convert`.

Payments: `GET /payment-methods`, `POST /payment-methods`, `PUT /payment-methods/{id}/default`,
`DELETE /payment-methods/{id}`, `GET /payments`, `POST /payments`.

Procurement (the planning board): `GET /procurement/projects`, `POST /procurement/projects`,
`GET /procurement/projects/{id}`, `PUT /procurement/projects/{id}`,
`DELETE /procurement/projects/{id}`, `POST /procurement/projects/{id}/lines`,
`PUT /procurement/projects/{id}/lines/{lineId}`, `DELETE /procurement/projects/{id}/lines/{lineId}`,
`POST /procurement/projects/{id}/submit-rfq`.

Projects and jobs: `GET /projects`, `GET /projects/{id}`.

Catalog: `GET /catalog/search`, `GET /catalog/products/{id}`.

Addresses: `GET /addresses`, `GET /addresses/{id}`.

### 12.5 The frontend

`app/portal.html` is its own Vite entry (`app/vite.config.ts:36`). 31 files in `app/src/portal/`:
`portal-shell.ts`, `portal-router.ts`, `portal-login.ts`, `portal-accept.ts`, `portal-reset.ts`,
`portal-dashboard.ts`, `portal-invoices.ts`, `portal-orders.ts`, `portal-order-detail.ts`,
`portal-quotes.ts`, `portal-payment-methods.ts`, `portal-method-card.ts`, `portal-procurement.ts`,
`portal-procurement-project.ts`, `portal-projects.ts`, `portal-project-detail.ts`,
`portal-return-request.ts`, `portal-reorder-modal.ts`, `portal-team.ts`, `portal-settings.ts`,
`portal-timeline.ts`, plus primitives (`portal-api.ts`, `portal-auth-url.ts`, `portal-data-table.ts`,
`portal-empty.ts`, `portal-form.ts`, `portal-modal.ts`, `portal-styles.ts`, `portal-toast.ts`,
`portal-main.ts`) and one test.

Ruling D-004 and D-028: React belongs to the sibling repository, this one is Lit, and the plan's
canonical-target note says so explicitly
(`docs/contractor-portal-plan.md:4`).

### 12.6 Where the ERP portal and HH Pro overlap

Capabilities present in the ERP portal today, by route: account dashboard with a billing summary,
invoice list and detail, order list and detail with delivery instructions, reorder with a preview,
return requests with staff approval, quote list and detail with accept, reject and convert, stored
payment methods with a default, multi-invoice payment, a procurement planning board with an RFQ
submit, projects and jobs rollup, a catalog search and product page, address book, team management
with invites and role and status changes, and settings with a password change.

Capabilities named in the ERP's own plan as target-state and **not** present as routes: files and
document management and sharing (`ManageFiles` is a declared capability with no route), live
driver-on-the-way delivery tracking for the contractor, notification preferences beyond the
settings write, and the voice-to-text or file-upload quick quote (the ERP's assist layer for that
lives staff-side in `quoteassist`, not in the portal)
(`docs/contractor-portal-plan.md` section 2).

**The plan document is stale in one load-bearing way**: `docs/contractor-portal-plan.md` section 3.2
states that `cmd/server/main.go` wires "only four modules: identity, product, pricing, portal" and
tabulates quotes, orders, invoices, payments, manifests, GL and inventory as `SCAFFOLD-ONLY` with
zero Go behaviour. That was true at revision 2 of the plan; it is not true of the tree. `main.go`
now wires roughly forty modules and every one of those domains has a service, a repository and a
route surface. Read that section as a historical snapshot.

---

## 13. Assist and AI

### 13.1 `backend/internal/assist` (37 Go files plus `assist/adapters`)

TanyaAI, built as two halves that can be reasoned about and shipped separately
(`backend/internal/assist/model.go:1-31`):

- **Half B, the structured command layer** (`commands.go`, `commands_domain.go`,
  `commands_activity.go`, `registry.go`). A registry of deterministic READ commands. No model, no
  network, no keys. Given the same question and the same data it returns the same answer forever, and
  it keeps working with the AI switched off or the internet down at the yard. **Every command is
  read-only in this pilot; nothing in this package writes to another domain's tables.**
- **Half C, the chat pilot** (`provider.go`, `stub_provider.go`, `anthropic_provider.go`,
  `service.go`). A conversational surface whose only power is to invoke the commands from half B. The
  assistant does not query the database, does not hold a connection, and cannot reach a table that is
  not behind a registered command.

The order of the two halves is the design: written the other way round, every question about what the
assistant can see becomes a question about what a model chose to do, and there is no answer that
survives an auditor (`model.go:17-20`).

**The invariant**: a command can never show a user something the app itself would not show them.
Held by never having a second path to the data. Commands call the SAME read services the screens
call, through consumer interfaces, carrying the asking user's `Caller`. There is no service account,
no elevated context, and no cache keyed on anything but the request in hand (`model.go:22-31`).

The seventeen registered commands, with their tier floor (`MaxTier` is the numerically highest, that
is least privileged, tier permitted):

| Code | Tier floor | Source |
|---|---|---|
| `payment.lookup` | 2 | `commands.go:134` |
| `order.lookup` | 3 | `commands.go:146` |
| `stock.lookup` | 3 | `commands.go:156` |
| `customer.balance` | 2 | `commands.go:172` |
| `customer.activity` | 3 | `commands.go:182` |
| `order.activity` | 3 | `commands_activity.go:35` |
| `payments.today` | 2 | `commands_domain.go:41` |
| `payments.range` | 2 | `commands_domain.go:48` |
| `ar.aging` | 2 | `commands_domain.go:60` |
| `ar.holds` | 2 | `commands_domain.go:67` |
| `customer.invoices` | 2 | `commands_domain.go:74` |
| `customer.orders` | 3 | `commands_domain.go:85` |
| `customer.quotes` | 3 | `commands_domain.go:96` |
| `quote.holds` | 3 | `commands_domain.go:107` |
| `stock.incoming` | 3 | `commands_domain.go:117` |
| `product.find` | 3 | `commands_domain.go:131` |
| `product.price` | 2 | `commands_domain.go:141` |

### 13.2 The model and its gating

`defaultAnthropicModel = "claude-sonnet-4-5"` is used when `assist_settings` has no model row, named
as a constant rather than inlined so the ledger's `model` column and the constant are obviously the
same fact (`backend/internal/assist/anthropic_provider.go:43-45`). The API version header is a pinned
constant in the same file (`anthropic_provider.go:47`). The system prompt is short "because the fence is not made
of words": the model cannot reach any data except through registered commands
(`anthropic_provider.go:122-124`).

Provider selection is config-driven; `stub_provider.go` is the shipped default.

Governance is config-as-data across three migrations: `assist_settings` and
`assist_command_policy` (0153), the token ledger `assist_token_ledger` recording every exchange once
(0154), and the budget in `assist_role_limits` and `assist_user_limits` (0155).

Routes: `POST /api/v1/assist/chat`, `POST /api/v1/assist/command`, `GET /api/v1/assist/commands`,
`GET /api/v1/assist/me`, plus the admin set `GET/PUT /api/v1/assist/admin/settings`,
`GET /api/v1/assist/admin/spend`, `GET /api/v1/assist/admin/limits`,
`PUT /api/v1/assist/admin/limits/role/{role}`, `PUT|DELETE /api/v1/assist/admin/limits/user/{user}`.
Guards: `requireStaff` for the panel, `requireAdmin` for the spend and limits screen
(`app/src/nav-access.ts:80-82`).

**Adapters**: `backend/internal/assist/adapters/adapters.go` wires TanyaAI's read seams to the domain
services that answer them. That file is the only place `assist` meets the rest of the program.

A refusal is deliberately short: the shipped measurement recorded in the test header is that a
refusal used to be 279 words with 0 chips at the manager tier and 155 words at the counter tier, and
the tests now **pin the number**, because a budget nobody measures walks back
(`backend/internal/assist/dib513_refusal_test.go`, quoted in the package's test doc block).

### 13.3 `quoteassist`

Covered in 8.2. Two pinned invariants: nothing here writes a quote line, and the model never names a
product.

### 13.4 The frontend assist surfaces

`app/src/assist-panel.ts` (`hos-assist-panel`, tab `assist`), `app/src/assist-admin.ts`
(`hos-assist-admin`, tab `assist-admin`), `app/src/assist-model.ts`, `app/src/_assist-links.ts`,
`app/src/_assist-conversation.test.mjs`. `app/src/quote-assist.ts` and `app/src/_quote-assist.ts` are
the quote-builder half. `app/src/_voice.ts` is the voice input helper.

---

## 14. BisTrack replacement

### 14.1 The sync module

`backend/internal/bistrack` (12 Go files) is the live-sync module. Its own doc states the deployment
guarantee it exists to serve: during the parallel run staff work in HardscapeOS, a PUSH keeps
BisTrack current, and a PULL catches net-new BisTrack rows
(`backend/internal/bistrack/module.go:1-4`).

**Phase 0 is schema-first and structurally unable to sync.** It freezes the module's data shapes
(`model.go`, `shapes.go`), the connector seam (`connector.go`, pull and push, both frozen), the pure
mapping layer (`mapping.go`), the per-phase and per-entity direction-of-truth as data
(`direction.go`), and the 0079 persistence. It ships ONE connector, `unconfiguredConnector`, whose
every method fails closed with `ErrUnconfigured`, and it wires that connector to **nothing**: no
route, no worker, no scheduler. The only HTTP surface is a staff-gated status read. Config-gated
means the reported status flips to "unconfigured" without credentials; it does **not** mean a
configured deploy would sync, because phase 0 cannot, by construction. The structural guarantee is
enforced by `structural_test.go`, not by a checklist (`module.go:6-15`).

**Credentials by reference only**: `BISTRACK_ENDPOINT` and `BISTRACK_API_KEY` are environment
variable names read by `config.Load` (`backend/internal/config/config.go`), and
`cfg.BisTrackConfigured()` is the only thing the module is told (`backend/cmd/server/main.go:172`).
No credential value appears anywhere in the repository.

**The health token**: `/healthz` carries `"bistrack":"unconfigured"|"idle"` as an additive field
(`backend/cmd/server/main.go:86-111`), so an operator can see the sync module's state without an
authenticated call.

Tables (0079): `bistrack_id_map`, `sync_jobs`, `sync_discrepancies`.

### 14.2 The legacy map and the anomaly register

`docs/legacy/bistrack_map.json` (56,898 bytes) is the machine-readable companion to
`docs/legacy/ANOMALIES.md`. Structure: `meta`, `subsystems` (23), `domain_mappings` (12),
`anomalies` (29, IDs BT-A-001 through BT-A-029).

The 23 subsystems and their target module: Customer (crm), Product/Item (product),
Stock/Inventory (inventory), Pricing (product), Invoice (finance), Branch (admin), Order (sales),
GL/Accounting (finance), Delivery/Dispatch (logistics), EDI (edi), Payment (finance),
Credit/Hold (crm), Journey/Manifest (logistics), Audit (governance), Tax (tax), Pack (inventory),
Pick (inventory), Vehicle/Driver (logistics), Per (UoM) (product), Quote (sales), Hire (none),
Lock/Hold (governance), Other (triage).

The 12 domain mappings, by BisTrack entity: Customers/AR; Products/UoM (the "Per" system);
Orders/Quotes (the `OrderHeader` plus `OrderType` pattern); Inventory/Stock valuation; Payments/COD;
GL seams to Great Plains; Audit/Locks; Invoicing (AR documents); Delivery/Dispatch/Manifest; Credit
holds; EDI; Pricing.

`meta` records the verification ceiling honestly: the raw backup is not in the workspace, nothing was
re-verified against a live database, and the schema inventory lists only the top 34 of 1,240 tables.

The anomaly scoreboard (`docs/legacy/ANOMALIES.md:7`): **1 BLOCKER, 8 HIGH, 14 MEDIUM, 6 LOW**, with
10 items needing a decision. The ones that trace directly into the schema:

| ID | Finding | Where it lands in the schema |
|---|---|---|
| BT-A-001 (BLOCKER) | The conversion table has 42 entries for 4,779 products; for 99 percent of the catalogue BisTrack does not know the conversions | `uoms` plus `product_uoms` plus `product_rounding_rules` (0002); the decode procedure is `docs/design/UOM_DECODE_PLAN.md` |
| BT-A-002 (HIGH) | Nobody has confirmed whether inventory is valued first-in-first-out, average or standard | `cost_layers` **and** `product_branch_costs` are both maintained; which one issues consume is a service-level switch (`DATA_MODEL.md` section 5) |
| BT-A-003 (HIGH) | 3.97 million cost-history rows, almost all dead | carry only costs for inventory still on the shelf; retention depth is a decision-queue item |
| BT-A-004 (HIGH) | The legacy system probably shows negative stock | the relaxed CHECK in 0037 plus `GET /api/v1/counts/negative-stock` |
| BT-A-005 (HIGH) | There is no active-product switch and there are probably duplicates | `is_active BOOLEAN` on every master, from birth (`DATA_MODEL.md` section 0) |
| BT-A-006 (HIGH) | Sixty product facts trapped in a user-defined blob with 168,000 entries | the ten hardscape facet columns on `products` plus `metadata JSONB`, plus migration 0018 and `backend/seed/udf_promote_list.csv` |
| BT-A-007 (HIGH) | 1,885 stored procedures, 100 examined | recorded as an open risk; the GL export shape is an open slot in `DATA_MODEL.md` section 7 |
| BT-A-008 (HIGH) | The sync plan rests on an untested assumption about incremental change queries | `internal/bistrack` phase 0 is structurally unable to sync, so the assumption is not yet load bearing |
| BT-A-010 | A forgotten copy of the customer list (8,408 entries) sits inside the live system | do-not-migrate list |
| BT-A-011, BT-A-012 | Picking and pallet tracking have never been used, zero records | `tasks` and the scan trail start from a blank slate |
| BT-A-014 | Manifests exist only as report views, nothing durable to audit | `manifests`, `manifest_lines`, `manifest_versions` with immutable JSONB snapshots (0012) |
| BT-A-020 | The Great Plains export rule table has 387,000 rows and an unknown shape | `posting_rules` indirection makes a compatible export additive (`DATA_MODEL.md` section 7) |
| BT-A-021 | 11.7 million rows of AR balance history | stays in the legacy archive; opening balances migrate as `ADJUSTMENT` subledger rows (`DATA_MODEL.md` section 9) |
| BT-A-023 | Six open operational questions, including lot and serial tracking | deliberately not modelled; a `lots` table plus a nullable `lot_id` on `stock_moves` is an additive migration (`DATA_MODEL.md` section 3) |
| BT-A-025 | BisTrack stores quotes and orders in one header table split by type | HardscapeOS splits the tables; the ETL maps `OrderHeader.OrderType` accordingly (`DATA_MODEL.md` section 8) |

### 14.3 The UOM decode plan

`docs/design/UOM_DECODE_PLAN.md` (40 lines). A correction from the real data is recorded at the top:
the rules live in `Per` (1,454 rows), `PerRule` (692) and `PerRuleConversion` (6,565); a table called
`PerConversionRule` does not exist, and `PerConversion` (42 rows) is only the per-code to per-code
multiplier matrix. `ProductUnitSize` and `ProductImportUnitSize` are empty.

The five-step decode procedure (section 2): restore to a disposable container and dump the `Per*`
tables plus the procedure bodies; profile the rules table for distinct kinds, driving key fields,
factor distributions and a live-versus-stale split; classify each rule kind into a direct
`product_uoms` factor, a rounding or selling policy into `product_rounding_rules`, a pricing-only
per-code into the pricing module, or dead; produce a per-product validation worksheet with SKU,
decoded factors, source rule and confidence for client sign-off; emit `seed/uom_factors.csv` for the
seed pipeline.

The ingestion mapping table (section 3) is the one a replacement core needs: per-code becomes a
`product_uoms` row with `qty_in_base` adjusted by the multiplier; per-weight pricing becomes a row
with a density-derived factor; the stocking-to-pricing implicit factor becomes two rows sharing the
base anchor with `products.price_uom` recording which one prices; the pallet, layer and piece
hierarchy from the user-defined fields becomes three rows plus convenience columns; coverage becomes
a square-foot row; and rounding habits become `product_rounding_rules` captured in a walkthrough
rather than from the data.

Risk posture (section 4): there is **no schema risk**, because whatever the rules table holds lands
as rows in tables that already exist. The blocker is a data-creation problem, not a design problem.

### 14.4 Which modules exist because of the replacement, and which are net-new

**Because of the replacement** (they exist to carry a BisTrack or Great Plains capability across):

`gl` (replaces the Great Plains general ledger), `finstmt` (the statements Great Plains produced),
`bankrec`, `reporting` (its own doc calls itself "the ~20 core BisTrack replacements",
`backend/internal/reporting/model.go:1-2`), `export` (the spreadsheet the counter used to produce by
retyping), `bistrack` (the parallel-run sync itself), `localization` (the Canadian locale the legacy
system handled), `tax` (the HST and status-card relief), `purchasing` plus the 0011 baseline,
`invcount`, `landedcost`, `inventory`, `product` including the user-defined-field promotion, `pricing`
(retargeting the legacy sell-price rule engine, named in `0023_pricing.sql:47`), `customer`,
`order`, `quote`, `invoice`/`billing`, `payment`, `payments`, `till`, `dispatch` (the manifests that
were only report views, BT-A-014), `identity`, `audit`, `branches`, `settings`.

**Net-new capability** (no legacy counterpart; each is a new product decision):

`portal` and `procurement` (the contractor portal and its planning board), `messaging` and
`messagingprovider` (the internal hub and the SMS arm), `notify`, `tasks` (WMS-Lite; BT-A-011 and
BT-A-012 record that picking and pallet tracking were never used), `warranty`, `lien` and
`compliance` (statute-derived, zero client-validated requirements at build time), `assist` and
`quoteassist`, `dashboard` (the cockpit and widgets), `tour`, `demo`, `feedback`, `printing`,
`jobs` (the scheduler this ERP did not have, `backend/internal/jobs/jobs.go:1-9`).

### 14.5 The parallel run and cutover, as sequence

`README.md:45` describes the roadmap as six phases in order: **Foundation, Financials and Portal,
Sales and Edge, Infrastructure and Migration, Parallel Run, Cutover**, with a parallel run alongside
BisTrack **before** cutover and explicitly no big-bang go-live. `backend/internal/bistrack/module.go:2-4`
states the deployment guarantee the sync exists to hold: rollback stays free for the length of the
parallel run, during which staff work in HardscapeOS, a push keeps BisTrack current, and a pull
catches net-new legacy rows. `docs/legacy/ANOMALIES.md` BT-A-022 records a read-only archive
sign-off as a cutover-playbook item.

`README.md:45` also carries a dated schedule. Per this file's own conventions the phases are named
and ordered above and the schedule is not reproduced.

---

## 15. The gate criteria mapping

### 15.1 What the repository actually names

The repository does **not** contain a single enumerated list of the CSA gate criteria. It references
them by number, and the numbers that appear are 1 through 6. The authoritative source
(`command/state/roadmap.yaml`, CSA Exhibit B) lives in a **different repository**, the workspace
root, and is not in this checkout (`docs/context/CONTEXT_INDEX.md:3`).

The numbered references found in this tree:

| Criterion | Named as | Cited at |
|---|---|---|
| 1 | RBAC, 13 roles and 3 tiers | `docs/PRR-W2.5-audit-remediation.md:11,56` |
| 2 | Pricing engine plus the price datum | `docs/PRR-W2.5-audit-remediation.md:50`; `backend/internal/pricing/model.go:1` |
| 3 | POS, the counter-sale tender paths | `docs/PRR-W2.5-audit-remediation.md:50`; `backend/internal/gate/doc.go:3`; `backend/migrations/0028_till_drawer_ops.sql:1` |
| 4 | User add and manage flows | `docs/PRR-W2.5-audit-remediation.md:11,56`; `backend/internal/gate/gate_test.go:353` |
| 5 | not named by number anywhere in this checkout | (absent) |
| 6 | migration, GL and stock invariants (the substrate) | `backend/internal/gate/gate_test.go:440` |

Two further criteria are named by phase rather than by number: the GL reconciled against static
BisTrack test data (`backend/internal/gl/bistrack_tieout.go:11`, "roadmap M2 gate criterion C1"), and
the native report substrate (`backend/internal/reporting/model.go:1-2`, "M4's gate criterion";
`backend/migrations/0143_reporting_templates.sql:5`).

**Report exactly this**: the repository names five criteria by number (1, 2, 3, 4, 6) plus two by
phase (C1 and the reporting criterion). It does not name criterion 5 anywhere in this checkout.

### 15.2 The four hard governance gates

`README.md:41` and `CLAUDE.md` name four hard gates administered by the workspace-root command
centre, which is not in this checkout: **G-PUSH, G-CLIENT, G-LOCK, G-MONEY**. Their in-repository
traces:

- **G-PUSH** is the commander's gate on pushing: "Local commits on feature branches; not pushed
  (G-PUSH is the commander's gate)" (`docs/PRR-W2.md:54`).
- **G-CLIENT** gates contractor-visible copy: "contractor-visible auto-reply text (G-CLIENT)"
  (`backend/internal/messaging/portal_bridge.go:29`).
- **G-LOCK** gates a spec before it can be built from, and appears as a per-spec identifier:
  `G-LOCK-016` (`backend/cmd/seed/main.go:5`), `G-LOCK-077` (`backend/internal/warranty/model.go:5`,
  `backend/internal/warranty/module.go:6`), and `docs/design/MODULE_FRAMEWORK.md:3` carries
  "Status: DRAFT, pending G-LOCK".
- **G-MONEY** has no in-repository trace beyond the two summary lines in `README.md` and `CLAUDE.md`.

### 15.3 The gate-evidence suite, test by test

`backend/internal/gate` (7 files, all build-tagged `integration`). Tests named GATE-1 through
GATE-9, plus the HTTP-surface sweep HS-1/2/3, the order-lifecycle route test, and the stored-copy
vocabulary scan.

| Gate test | What it proves | Test path | Module exercised |
|---|---|---|---|
| GATE-1a `TestGate_CounterSale_Cash` | cash counter sale: order CONFIRMED, one CASH payment row, invoice PAID, allocation written, AR PAYMENT subledger row; pre-tax line plus tax header | `gate/gate_test.go:27` | pos, till, billing, order |
| GATE-1b `TestGate_CounterSale_CardStub` | card sale via the stub: gateway reference set, status RECEIVED, card type persisted, invoice PAID | `gate/gate_test.go:53` | pos, payment, till |
| GATE-1c `TestGate_CounterSale_OnAccount` | on-account sale | `gate/gate_test.go:76` | pos, billing |
| GATE-1d `TestGate_CounterSale_BelowFloor_Routes` | a below-floor line routes to the approvals queue | `gate/gate_test.go:112` | pricing, identity |
| GATE-1e `TestGate_CaptureBeforeIssue_Invariant` | capture before issue: a forced card decline leaves no issued document | `gate/gate_test.go:156` | pos, payment |
| GATE-1f `TestGate_PricingModel_ClientRules` | the shipped pricing model against the client's own rules | `gate/gate_test.go:199` | pricing |
| GATE-2 | tier-3 frontline gets 403 on manager and admin routes; the correct tier is not refused | `gate/httpsurface_test.go:116` `TestSurface_GuardedWritesRejectUnderTier` | identity plus every module with a guarded write |
| GATE-3 `TestGate_UserManageLoop_ClosesOnDB` | the user add and manage loop closes on the live database (criterion 4) | `gate/gate_test.go:364` | identity |
| GATE-4 `TestSurface_MutatingRoutesAudited` | every mutating route writes exactly one audit row; GET and login write none | `gate/httpsurface_test.go:145` | audit plus the whole mux |
| GATE-5a `TestGate_Reset_MigrationsClean` | migrations apply cleanly from scratch and the reset is clean | `gate/gate_test.go:444` | migrations |
| GATE-5b `TestGate_GL_JournalsBalance` | every POSTED journal balances | `gate/gate_test.go:472` | gl |
| GATE-5c `TestGate_StockReconciles_NoNegative` | stock reconciles with no negative balance | `gate/gate_test.go:505` | inventory |
| GATE-6 `TestGate_Tax_DisplayAndAssessEntry` | tax display: a sale carries the HST header, and the assess entry point | `gate/gate_test.go:522` | tax, pos |
| GATE-7 `TestGate_Offline_IdempotentReplay` | an idempotent replay of a queued sale | `gate/gate_test.go:564` | pos |
| GATE-8 `TestGate_Surcharge_Clamp` | the surcharge clamp | `gate/gate_test.go:596` | pricing |
| GATE-9 `TestGate_FNRelief_Privacy` | relief assessment surfaces only masked evidence | `gate/gate_test.go:619` | tax, audit |
| HS-2 `TestSurface_GuardedWritesRejectAnon` | anonymous gets 401 on every mutating route | `gate/httpsurface_test.go:103` | every module |
| HS-2 read leg `TestSurface_GuardedReadsRejectAnon` | anonymous gets 401 on guarded reads | `gate/httpsurface_test.go:90` | every module |
| order lifecycle `TestOrderLifecycle_RouteGuards` | anon 401, frontline 200 on picking, frontline 403 on cancel at the route, manager admitted | `gate/order_lifecycle_test.go:66` | order, identity |
| stored copy `TestIntegration_NoColumnDefaultCarriesAWorkstreamCode` | no column default in the live database carries an internal workstream code | `gate/stored_copy_vocabulary_integration_test.go:40` | the whole schema |
| stored copy `TestIntegration_NoStoredOperatorCopyCarriesAWorkstreamCode` | no operator-facing note column carries one either | `gate/stored_copy_vocabulary_integration_test.go:87` | the whole schema |

GATE-6 through GATE-9 are "verify-if-shipped": they record a not-built honestly rather than asserting
against absent behaviour (`gate/gate_test.go`, the GATE-6/7/8/9 header comment).

The card path always uses `payment.RunPaymentsStub`: no live processor, no real credentials, no
card number, ever (`backend/internal/gate/doc.go:8-10`).

`backend/internal/gate/stored_copy_vocabulary_integration_test.go:5-23` records the three doors from
the backlog to a dealer's screen, and which gate watches each: the frontend source (two `.test.mjs`
gates plus the bundler-modelling lens), composed Go sentences (two banner vocabulary tests), and
**the database**, which was guarded by nothing and was quietly shipping four column defaults with a
code in them. That third gate scans `information_schema` rather than a list, so a table added later
is covered without anybody remembering to add it.

---

## 16. Frontend architecture

### 16.1 Lit 3, and only Lit

`lit ^3.2.0` is the single runtime dependency (`app/package.json`). 361 TypeScript files under
`app/src`, of which 330 are at the top level and 31 in `app/src/portal`. 170 `.test.mjs` files (169
at the top level plus one in `portal`).

### 16.2 The six Vite entries

`app/vite.config.ts:41-70`, each with the reason it is separate:

| Entry | Document | Why separate |
|---|---|---|
| `main` | `index.html` | the staff ERP |
| `portal` | `portal.html` | the customer-facing contractor portal |
| `messages` | `messages.html` | the headless full-screen messaging second screen |
| `quote` | `quote.html` | the public token-credentialed quote; runs pre-auth and must not ship session machinery |
| `drive` | `drive.html` | HH Drive, the driver's phone app; separated for bytes on cellular, not for auth |
| `demo` | `demo.html` | the pre-auth demo chooser; deployment-conditional and inert on every box but one |

There is also `app/quick-action-harness.html`, which is not a build input: a harness document served
in development for the quick-action registry.

### 16.3 `dashboard-nav.ts`: the frozen registry

`app/src/dashboard-nav.ts` (243 lines): plain data, no Lit, no DOM, no imports, so the route table can
be enumerated without a browser. The header states the argument: "a routing contract you cannot
enumerate is a routing contract you cannot prove" (`dashboard-nav.ts:6`).

`NAV_REGISTRY`, complete and in rail order (which is also the unit overview tile order):

```
sales:      catalogue, calculator, quote, quotes, pos, invoices, returns, pricing
finance:    gl, bankrec, statements, ar
inventory:  inventory
compliance: lien, tax
messaging:  messaging
warranty:   warranty
dispatch:   dispatch
yard:       yard
purchasing: purchasing
reporting:  reporting
admin:      staff, approvals, audit, assist, assist-admin, hotkeys, account
```

Eleven units, twenty-eight modules. Two ordering decisions are argued in the file itself: `calculator`
sits directly after `catalogue` so the rail reads browse, size, quote in the order the counter
conversation happens; and `invoices` sits directly after `pos` and lives in Sales rather than Finance,
because the register is what a counter person opens throughout a shift while Finance is the period-end area every
screen of which is manager-gated (`dashboard-nav.ts:26-40`).

Exports: `NavUnitKey`, `NavModuleKey`, `NAV_UNIT_KEYS`, `NAV_UNIT_OF`, `isNavUnitKey`,
`isNavModuleKey`, `enumerateDeepLinks()`, `parseDeepLink(hash)`, plus the pinned-tab helpers
`MAX_PINNED_TABS = 6`, `PinnedTarget`, `pinKeyFor`, `resolvePinKey`, `resolvePinKeys`, `pinHref`,
`splitPinnedTabs`, `unpinnedOpenUnits`.

Own-property checks in `isNavUnitKey`/`isNavModuleKey` are load bearing: a plain `in` walks the
prototype chain, so `#__proto__/pos` or `#sales/constructor` would otherwise read as valid keys
(`dashboard-nav.ts:129-133`). A module is honoured only when it belongs to the named unit, so
`#finance/pos` names two real keys that do not go together and resolves to the unit overview
(`dashboard-nav.ts:141-146`).

`resolvePinKey` returns null for a key that leaves the registry, and it deliberately does **not**
half-resolve: a retired `sales/catalogue` does not become a bare Sales tab, because a tab labelled
Sales where a person expects Catalogue is a different tab wearing the same position
(`dashboard-nav.ts:167-179`).

### 16.4 `hash-route.ts`: the contract

`app/src/hash-route.ts` (92 lines), dependency-free by the same rule.
`hashTargetFor(active, module)` yields `#home`, `#apps`, `#<unit>` or `#<unit>/<module>`, and the
round-trip property is stated: `hashTargetFor(adopted state)` must equal the incoming hash, or the
shell rewrites `location.hash` and pushes a stale entry so Back walks onto the wrong screen
(`hash-route.ts:6-11`).

`OwnHashWrite` is a one-shot, counted ledger that makes the shell's own hash writes
self-identifying, because `hashchange` carries no origin. Three declared properties: one use per
write then gone; a miss clears the ledger rather than lying in wait; and writes are **counted**, not
merely remembered, because one gesture can write the hash twice (`openUnit` then `setActiveModule`)
(`hash-route.ts:24-56`).

### 16.5 `app-shell.ts`

**4219 lines.** Responsibilities:

- Derives `Tab` and `UnitKey` from `NAV_REGISTRY` rather than declaring them twice; the label, gate
  and meta maps are `Record<Tab, ...>` so a registry entry with no label, gate or meta does not
  compile (`app-shell.ts:285-292`).
- `PAGE_TYPE: Record<Tab, PageType>` with `'work' | 'form' | 'reading'` (`app-shell.ts:342-388`).
  The comment records the measurement that produced it: at a 1920px viewport with the rail collapsed, seven
  surfaces painted 1056px and left 864px, 45 percent of the window, dead. The previous mechanism was
  an opt-in allowlist of four names, and "an allowlist cannot be wrong about a screen it never heard
  of; it can only be silent" (`app-shell.ts:296-320`).
- `SCREEN_LABEL: Record<Tab, string>` (`app-shell.ts:520-549`).
- `UNIT_META: Record<UnitKey, {label, glyph, lead, defaultModule?}>` (`app-shell.ts:442`).
- `SCREEN_META: Record<Tab, {glyph, accent}>`, with a shared accent per unit family
  (`app-shell.ts:569-608`).
- `MODULES` assembled from `NAV_UNIT_KEYS` so the launcher, the tab strip, the in-tab rails and the
  hash router all read one source (`app-shell.ts:555-567`).
- `view(tab)` is the single switch from tab to mounted element (`app-shell.ts:3430-3496`).
- **Tab instances**: open units survive a reload within the browser session; the stored value is a
  list of instance ids, and the first instance of a unit is spelled as the bare unit key so a session
  written by the previous build restores unchanged (`app-shell.ts:610-618`, `app/src/tab-instances.ts`).
- **Popout**: `index.html?popout=<tab>` runs the shell headless, rendering one screen full window
  with no chrome. `popoutTab` is the parsed target, `popoutRestore` carries the opener's in-progress
  POS order across the handover, and `PopoutController` handles the claim, the title and
  multi-monitor placement (`app-shell.ts:1364-1430`, `app/src/popout.ts`,
  `app/src/popout-controller.ts`). The parse is gated by `isTabKey` for the same prototype-chain
  reason as the router (`app-shell.ts:629-631`).
- Persistence keys: `hos-open-units`, `hos-active-module`, `hos-rail-collapsed`.
- The Apps **tab** was retired in favour of a header launcher control, while `active === 'apps'` and
  the `#apps` hash remain a real surface so an old bookmark still works (`app-shell.ts:620-640`).

### 16.6 `nav-access.ts`: who may see which door

`app/src/nav-access.ts` (120 lines), no Lit and no DOM. `Gate` is `'always' | 'authed' | 'manager' |
'admin'`; `SCREEN_GATE: Record<NavModuleKey, Gate>` is the table; `AccessRole` is
`{authed, manager, admin}`; `canSeeGate`, `visibleModuleKeys(unit, role)` and
`openableUnitKeys(role)` are the three functions, and the last is the single source for the Apps
page grid, the header launcher popover, the pin menu and pin pruning, so an app not offered in one is
not offered in any (`nav-access.ts:104-119`).

The file states plainly that it is **not** a security boundary: the backend enforces the real RBAC,
and these gates only decide whether an entry point is offered (`nav-access.ts:8-11`).

### 16.7 `api.ts`

`app/src/api.ts` (417 lines). Staff auth is a real JWT obtained from `POST /api/v1/auth/login`, held
in a module variable and mirrored to `sessionStorage` so a reload keeps the session, attached as
`Authorization: Bearer <token>` on every staff request (`api.ts:1-4`).

Keys: `hos-staff-token`, `hos-staff-user`, `hos-staff-landing`, `hos-staff-full-app`.

**The storage shim** (`api.ts:27-52`): these reads ran at module scope, and browsers set to deny site
data make the `sessionStorage` **accessor itself** throw, so before any component mounted the bundle
died and the page rendered nothing. The shim probes both access and use inside a `try`, and degrades
to an in-memory `Map`: login still works, the session simply does not survive a reload, which is the
honest behaviour for that posture.

`hos-staff-landing` carries the **served** landing decision across a full-page navigation to
`drive.html`. It is written only from a server answer (the `home` block of `GET /api/v1/dashboard`)
and decides nothing locally; a role code is never consulted, and an absent value costs exactly the
pre-existing behaviour (`api.ts:67-80`).

### 16.8 `tokens.css` and the three themes

`app/src/styles/tokens.css` (789 lines) is the only place colour lives. Never hardcode a colour in a
component.

- **HH Yard Dark** is the default, on bare `:root` (`tokens.css:27`).
- **Light** is opt-in via `:root[data-theme="light"]` (`tokens.css:525`), persisted to a storage key
  the shell toggle writes.
- **Loud** is a **third** value of the same `data-theme` attribute, `:root[data-theme="loud"]`
  (`tokens.css:657`), described as a blacker ground with brighter data, "a LOOK, not a modifier", a
  flat sibling of the light block, with no light-loud variant (`tokens.css:615-616`).

`CLAUDE.md` describes two themes ("HH Yard Dark is the default theme; Light is opt-in") and does not
mention Loud. The third theme landed in the commits "Loud: a third theme, and eight hues that had to
earn their place" and "Loud reaches the whole app, and the fence it had was built to open"
(`git log --oneline`, commits `7dea51c` and `8202030`).

**The gold rule** (`tokens.css:14-18`): `#FFC313` is a SURFACE, never an ink. Every gold fill carries
near-black `--hos-stone-amber-ink` `#0B1725` in **both** themes, never white, because white on gold
measures 1.61:1. On light surfaces the gold role as ink is carried by dark gold
`--hos-stone-amber-text` `#8A6500`, which is what the source design does: it never sets the bright
gold as text on a light section.

**Names are role names and were kept through the rebrand** (`tokens.css:19-24`):
`--hos-stone-amber` is gold, not amber; `--hos-deep-earth` is navy, not earth. Renaming about 1,400
references across about 90 components would have been a larger diff than the rebrand with a real
chance of a phantom token, for no rendered difference.

**One documented per-theme-tuned pair** (`tokens.css:45-70`, ruling D-077): the danger red's FILL is
per theme while `#fff` stays the single ink, because clearing 4.5:1 on the dark text red needed an
ink luminance at or below 0.0138 while clearing it on the light red needed 0.925 or above, and no ink
is both. The exception is for that pair only.

Contrast values in the sheet are **measured**, not estimated; `app/scripts/contrast-check.mjs` and
`app/scripts/phantom-tokens.mjs` are the tools.

### 16.9 The frontend test conventions

Tests are `.test.mjs` run by the Node built-in runner with a TypeScript-stripping loader
(`app/src/_ts-js-loader.mjs`). Most are **source scans**, not render tests, and the reason is stated
in several of them: `app-shell.ts` calls `customElements.define` at module scope and cannot be
imported outside a browser, so what a component **declares** is pinned by a scan and what it
**paints** is measured in real Chromium by Playwright; where the two disagree, Chromium wins
(`app/src/_layout-contract.test.mjs:12-18`, `app/src/_theme-tokens.test.mjs:6-12`).

Scans strip comments first, through `shipped()` and `bundledSource()`, because a comment that quotes
a forbidden thing must not be read as the thing (`app/src/_shipped-copy.test.mjs:10-14`).

Standing gates:

| Gate | What it holds |
|---|---|
| `_element-registry.test.mjs` | every mounted `<hos-*>` tag is reached by a `@customElement` define through the mounting file's import closure, across every `.ts` and every HTML document that mounts a root. An unregistered tag type-checks, lays out as an empty box, and renders nothing forever; this repository shipped that once (`_element-registry.test.mjs:1-20`) |
| `_layout-contract.test.mjs` | `PAGE_TYPE` stays a total `Record<Tab, PageType>` over the real union, the `.pane` rule actually keys off it, and no numeric width cap is reintroduced beside it. It imports the real `dashboard-nav.ts` rather than a copy (`_layout-contract.test.mjs:1-20`) |
| `_theme-tokens.test.mjs` | no phantom tokens (`var(--never-defined)`), the theme bootstrap, and the ink rules. Explicitly resolves no cascade (`_theme-tokens.test.mjs:1-12`) |
| `_shipped-copy.test.mjs` | no internal workstream codes in shipped bytes, through a lens that models the bundler rather than the two older regexes that erased the class by construction (`_shipped-copy.test.mjs:1-14`) |
| `_counter-vocabulary.test.mjs` | counter-facing copy vocabulary |
| `_bundle-fence.test.mjs`, `_bundle-comments.test.mjs` | the pure functions behind the two build plugins |
| `_driver-surface.test.mjs`, `_drive-shell.test.mjs`, `_driver-redirect.test.mjs`, `_driver-session-knob.test.mjs` | the driver surface money scan and the shell contract |
| `_ci-test-floor.test.mjs` | the floor wrapper itself |
| `_chrome-snap.test.mjs`, `_empty-states.test.mjs`, `_error-states.test.mjs`, `_home-icons.test.mjs` | shell chrome and state coverage |

### 16.10 Playwright

One spec: `app/e2e/entry-contrast.spec.ts`. It is described in `app/playwright.config.ts:1-8` as
existing for **one** class of defect: contrast that is only real once a browser has resolved the
cascade. The defect it exists for shipped: a customer-facing page rendered near-white text on white
cards because `var(--hos-surface, #fff)` referenced a token `tokens.css` never defined, while the
real `--hos-text` resolved to near-white under the dark default that `quote.html` never overrode.
Structure was fine, types were fine, the page was unreadable
(`app/e2e/entry-contrast.spec.ts:3-12`).

It walks the DOM and asserts every text node's computed colour against its **actual painted**
background, resolving transparency up the ancestor chain, at `AA_NORMAL = 4.5` and `AA_LARGE = 3.0`.

**It is not wired into CI** and the config says so, naming that as its own ticket
(`app/playwright.config.ts:9-13`). It needs a share token for the public quote leg, without which
that leg is skipped rather than silently passing against an error page.
`HOS_CHROMIUM_PATH` allows a pre-provisioned Chromium on hosts that cannot reach the download host.

### 16.11 The walkthrough harness pattern and the evidence directories

`scripts/walkthrough-*.cjs` (72 files) are Playwright harnesses that log in via a shared
`scripts/walkthrough-common.cjs` and pierce shadow DOM. There are also audit, budget, cost and probe
scripts (`scripts/audit-*.cjs`, `scripts/budget-*.cjs`, `scripts/probe-*.cjs`,
`scripts/taskcost-*.cjs`, `scripts/verify-513-a11y.cjs`), plus a second family under `app/scripts`
(`counterconf-walkthrough.mjs`, `train11-walkthrough.mjs`, `union14-walkthrough.mjs`, the shot
scripts, `pair-audit.mjs`).

Output lands in **70 committed evidence directories** at the repository root:
`verification/`, `audit-evidence/`, and 68 `walkthrough-*/` directories. They are screenshots and
JSON, committed on purpose so a reviewer can check the claim rather than the prose
(`CLAUDE.md`, "Repository artifacts").

Also at the root: 78 `NOTES-*.md` per-ticket working notes, 15 `REPORT-W*.md` per-wave audit reports,
four `PARK-*.patch` parked wiring diffs, and several sweep logs.

---

## 17. Screens

Every screen reachable from the registry, with its element, its file, its page type, its entry-point
gate, and the backend guard behind it.

| Unit | Module | Element | File | Page type | Entry gate | Backend guard |
|---|---|---|---|---|---|---|
| sales | catalogue | `hos-product-catalogue` | `app/src/product-catalogue.ts` | work | always | `requireStaff` reads, `RequireManager` writes on `/api/v1/products` |
| sales | calculator | `hos-material-calc` | `app/src/material-calc.ts` | form | authed | `requireStaff` (`/api/v1/products/coverage`) |
| sales | quote | `hos-quote-guard` | `app/src/quote-guard.ts` | form | always | `requireStaff` (`/api/v1/staff/policy/check`, pricing resolve) |
| sales | quotes | `hos-quote-builder` | `app/src/quote-builder.ts` (plus `quote-workarea.ts`) | work | authed | `requireStaff` on `/api/v1/quotes` |
| sales | pos | `hos-pos-terminal` | `app/src/pos-terminal.ts` | work | authed | `requireStaff`, with `requireManager`/`requireAdmin` on policy |
| sales | invoices | `hos-invoice-register` | `app/src/ar-invoice-register.ts` | work | authed | `requireStaff` on the register; manager on campaigns; admin on policy |
| sales | returns | `hos-return-approvals` | `app/src/return-approvals.ts` | work | authed | `requireStaff` / `requireManager` on approve and reject |
| sales | pricing | `hos-pricing-admin` | `app/src/pricing-admin.ts` | work | manager | `readGuard`=RequireStaff, `writeGuard`=RequireManager |
| finance | gl | `hos-gl-workbench` | `app/src/gl-workbench.ts` | work | manager | `requireManager`, `requireAdmin` |
| finance | bankrec | `hos-bank-recon` | `app/src/bankrec-console.ts` | work | manager | `requireStaff`, `requireManager`, `requireAdmin` |
| finance | statements | `hos-financial-statements` | `app/src/finstmt-home.ts` | reading | manager | `requireManager`, `requireAdmin` |
| finance | ar | `hos-ar-console` | `app/src/ar-console.ts` | work | manager | `requireManager`, `requireAdmin` |
| inventory | inventory | `hos-inventory-home` | `app/src/inventory-home.ts` | work | manager | `requireStaff`, `requireManager`, `requireAdmin` |
| compliance | lien | `hos-lien-cockpit` | `app/src/lien-cockpit.ts` | work | manager | `requireManager` |
| compliance | tax | `hos-tax-admin` | `app/src/tax-admin.ts` | form | manager | `requireStaff`, `requireManager`, `requireAdmin` |
| messaging | messaging | `hos-messaging-hub` | `app/src/messaging-hub.ts` | work | authed | `requireStaff`, `requireAdmin` |
| warranty | warranty | `hos-warranty-surface` | `app/src/warranty-surface.ts` | work | authed | `requireStaff` reads, `requireAdmin` registry, `requireManager` settlement |
| dispatch | dispatch | `hos-dispatch-surface` | `app/src/dispatch-surface.ts` | work | authed | `requireStaff`, `requireManager` |
| yard | yard | `hos-yard-console` | `app/src/yard-console.ts` | work | authed | `requireStaff` |
| purchasing | purchasing | `hos-purchasing-console` | `app/src/purchasing-console.ts` | work | authed | `requireStaff`, `requireManager`, `requireAdmin` |
| reporting | reporting | `hos-reporting-console` | `app/src/reporting-console.ts` | work | manager | `requireStaff` on template discovery and saved views, `requireManager` on every run, export, print and commission read, `requireAdmin` on writing a commission rule |
| admin | staff | `hos-staff-roles` | `app/src/staff-roles.ts` | form | always | `manager` list, `admin` create and role assign |
| admin | approvals | `hos-pending-approvals` | `app/src/pending-approvals.ts` | work | always | `authn` |
| admin | audit | `hos-audit-log` | `app/src/audit-log.ts` | work | admin | `requireAdmin` |
| admin | assist | `hos-assist-panel` | `app/src/assist-panel.ts` | reading | authed | `requireStaff`, with server-side tier filtering of commands |
| admin | assist-admin | `hos-assist-admin` | `app/src/assist-admin.ts` | form | admin | `requireAdmin` |
| admin | hotkeys | `hos-platform-hotkeys` | `app/src/platform-hotkeys.ts` | form | admin | `requireStaff` read; the server says `can_edit` on the payload rather than the table predicting it |
| admin | account | `hos-account-settings` | `app/src/account-settings.ts` | form | authed | `authn` |

Two fixed surfaces outside the registry but inside the shell: `#home` (the bento home,
`app/src/home-bento.ts`, `widget-card.ts`, `widget-body.ts`, `widget-builder.ts`, `viz-tile.ts`,
`viz-composer.ts`) and `#apps` (the launcher page, `app/src/launcher-page.ts`,
`app/src/app-launcher.ts`).

Standalone entry documents and their roots:

| Document | Root element | File | Audience and device |
|---|---|---|---|
| `index.html` | `hos-app-shell` | `app/src/app-shell.ts` via `app/src/main.ts` | staff, desktop first; the pane contract adapts by page type |
| `portal.html` | `hos-portal-shell` | `app/src/portal/portal-shell.ts` via `portal-main.ts` | contractor, mobile first (`docs/contractor-portal-plan.md` section 2) |
| `messages.html` | the messaging root | `app/src/messages.ts` | staff second screen, full screen, headless |
| `quote.html` | the public quote root | `app/src/quote-public.ts` | homeowner or contractor, pre-auth, token in `?t=` |
| `drive.html` | `hos-drive` | `app/src/drive.ts` | driver, phone, installable, no service worker |
| `demo.html` | the demo chooser | `app/src/demo.ts` | pre-auth prospect, deployment-conditional |

Device assumptions stated in source: the driver surface is built at 390px
(`NOTES-UNION-TRAIN16.md`, "driver surface (390px, POD, handback, money instrument)"); the yard
console assumes the person building the next truck (`backend/internal/identity/rbac.go:296-299`);
`app/src/kiosk-takeover.ts` exists for a role-based screen takeover configured by migration 0141.

---

## 18. Offline behaviour

Stated plainly, from the code.

**What exists:**

1. **An in-memory outbox for driver status taps.** `app/src/dispatch-logic.ts:214-276`. Types:
   `OutboxState = 'PENDING' | 'SENDING' | 'FAILED'`, `OutboxEntry {id, stopId, kind:
   'status'|'pod'|'photo', payload, state, attempts, lastError}`. `newOutboxEntry` mints a stable
   client id so a retry never enqueues a duplicate. `afterAttempt` removes an entry on success
   (a queue that keeps successful entries re-sends them). `isRetryable(status)` returns true only
   for no response at all or a 5xx, because a 4xx will fail identically forever and retrying it
   produces a spinner that never stops. `outboxLabel` **never** says "done": the three labels are
   "Sending...", "Not sent, tap to retry" and "Waiting to send".
2. **The honest UI state that goes with it.** The module's own header states the one unforgivable
   behaviour would be a green tick for a status the server never received
   (`dispatch-logic.ts:214-223`).
3. **A web app manifest and icons** for HH Drive: `app/public/manifest.webmanifest`,
   `drive-icon-192.png`, `drive-icon-512.png`, `drive-icon-maskable-512.png`, `drive-icon.svg`,
   generated by `app/scripts/drive-icons.mjs`. Referenced by `drive.html` and `demo.html`.
4. **`sessionStorage` for the staff session**, with the throw-safe shim described in 16.7.
5. **`localStorage` for the theme** and a small number of per-viewer conveniences (the rail collapsed
   state, open units, the active module).
6. **Graceful degradation on a failed fetch** in a handful of screens, marked with an `/* offline */`
   comment: `app/src/quote-builder.ts:293` (validation still guards the submit),
   `app/src/staff-roles.ts:370,393` (the shipped branch names render as a fallback).
7. `app/src/pos-api.ts:175` carries an `offline?: boolean` field, and the tender vocabulary reserves
   `PENDING` on a payment for an offline card that has been queued but not captured
   (`app/src/tender-logic.ts:59`, migration 0027).

**What does not exist:**

1. **No service worker.** Nothing in this repository registers one. `app/src/drive.ts:93-99`
   quotes the earlier refusal verbatim and declines to overturn it: "It does NOT mean a service
   worker and a local database, a real offline client is a project, and shipping a half-built one
   would put a cache between a driver and the truth."
2. **No local database.** No IndexedDB, no wa-sqlite, no OPFS. The only matches for `indexedDB` in
   `app/src` are inside test files and `popout-controller.ts`.
3. **No cache of reads.** `app/src/drive.ts:101-108` names the four reads a cache would have to
   serve (`GET /dispatch/my-runs`, `/dispatch/stops/{id}`, `/dispatch/runs/{id}/payment-holds`,
   `/dispatch/photos/{id}`) and states the choice explicitly: with no signal none of them answers and
   the screen says so, because a stale stop list is indistinguishable from a current one on a phone
   and a driver acting on one is worse off than a driver told the run did not load.
4. **The outbox is not durable.** It survives a lost signal and does **not** survive a reload, an
   operating-system kill, or a dead battery (`app/src/drive.ts:109-113`).
5. **Proof of delivery is not queued at all.** Three of the four driver writes are queued; the POD
   write is not, because an attestation is captured against a live read and sealed in one request
   (`app/src/drive.ts:111-113`).
6. **No `navigator.onLine` handling.** The string does not appear in `app/src` outside tests.
7. **No conflict resolution and no sync state.** `app/src/drive.ts:114-119` names the deferral
   precisely: the missing piece is not a service worker, it is durable storage for that outbox plus a
   conflict rule for a POD captured against a stop that moved while the phone was dark.

The honest summary the code itself gives: "an installed HH Drive with no signal is an app that opens
instantly and tells you it cannot see the run" (`app/src/drive.ts:120-121`).

---

## 19. Seeding and demo

### 19.1 `cmd/seed`

`backend/cmd/seed/main.go` (79 lines) plus an embedded `dibbits_seed.sql` (101,519 bytes). It loads
the representative hardscape world: real suppliers and catalogue, both branches, demo customers,
opening inventory (`cmd/seed/main.go:1-5`). It is:

- **prod-guarded**: it refuses to run anywhere but `dev` unless `ALLOW_SEED=true`, because it plants
  tier-1 GLOBAL admins with a repository-committed demo password, and the config's stance is that
  everything that is not dev is production grade (`cmd/seed/main.go:26-33`);
- **idempotent**: it no-ops if the demo catalogue is already present.

It is destructive only in the sense that `make reset` calls it after wiping the volume
(`Makefile:20-26`).

### 19.2 `cmd/demoseed`

`backend/cmd/demoseed` (7 files, 2,655 lines total). Additive only, for staging, which carries real
tester homework and must never be wiped (`cmd/demoseed/main.go:1-15`).

- Nothing deletes, nothing updates, nothing truncates, and nothing mints from a shared sequence.
- Every write is an INSERT with an explicit deterministic id and `ON CONFLICT DO NOTHING`
  (`ids.go` derives the ids; `apply.go` is the only writer and **can express no other statement
  kind**).
- Running it twice produces the same world: the second run's inserts all conflict and report
  `created=0`.
- **`-plan` is the default.** `-apply` is required and is the only flag that writes. Passing both is
  refused as two contradictory instructions (`main.go:54-57`). `-now` pins the clock; `-ids` prints
  every row id the plan would write.

The property it does **not** have, recorded so nobody assumes it: `demoseed` cannot UPDATE. It can
add a row that is missing; it can never repair one that is wrong. Repairing such a row remains a
migration's job (`scripts/staging_apply.sh:32-35`).

`bin/demoseed` is built **and run** on every staging deploy (`scripts/staging_apply.sh:15-35`), which
makes every deploy a writer to the staging database; the safety argument is the three structural
properties above.

### 19.3 `scripts/demo_reset.sh`

Full demo reset in two steps: drop and recreate the database, run every migration, load the seed;
then, with the API up, configure the card rail in **test mode** (a deterministic simulated gateway,
no card reader and no live processor call) so card tender works out of the box
(`scripts/demo_reset.sh:1-10`). Modes: `--db` (default), `--card`, `--all`.

It requires the server to run with a **fixed** `PAYMENT_VAULT_KEY` (64 hex characters): an empty key
generates an ephemeral one per process, and the stored card config would stop decrypting on the next
restart (`scripts/demo_reset.sh:18-21`). Demo credentials are embedded in `scripts/demo_reset.sh`.

`scripts/demo_apply.sh` is the droplet-side apply for the demo site; it refuses to run if the
environment file it finds is not a demo box's (`.github/workflows/deploy-demo.yml:4-9`).

### 19.4 Fixtures in `scripts/`

SQL fixtures: `dib433-counter-login.sql`, `fixture-513-finance.sql`, `fixture-513-guard.sql`,
`fixture-ar-landing.sql`, `fixture-dispatch-commit.sql`, `fixture-driver-run.sql`,
`fixture-loadboard.sql`, `fixture-posflow-pastdue.sql`, `seed-483.sql`, `seed-train9.sql`.
Shell fixtures: `fixture-470.sh`, `fixture-472.sh`, `fixture-478.sh`, `fixture-513-orders.sh`,
`fixture-513-payments.sh`, `posflow-stack.sh`, `isodb.sh` (mints an isolated database per run).
Node fixtures: `fixtures-union-triple.cjs`. Gate runners: `gate-sessionA.sh`, `gate-sessionB.sh`,
`sweep-w6d.sh`. A swatch generator: `gen-demo-product-swatches.py`.

Demo credentials appear in several of these fixtures and in `scripts/demo_reset.sh`; nothing further
about them is reproduced here.

### 19.5 The demo landing page and the demo module

`app/demo.html` plus `app/src/demo.ts` is a pre-auth chooser over four demo flows. It is inert, and
says so in words, on every box where the gate's route is absent, which is every box but one
(`app/vite.config.ts:62-69`).

`backend/internal/demo` is the credential bypass behind it, and the package doc opens by saying so:
one route that accepts no password, no token and no cookie, answering with a real fully-authorized
staff session for one of four named people (`backend/internal/demo/demo.go:1-10`). Routes:
`POST /api/v1/demo/session` and `GET /api/v1/demo/flows` (`demo/handler.go:22,31`).

It mounts only when **both** `DEMO_MODE=true` and `APP_ENV=demo` (or dev), and the two are
deliberately different kinds of fact: the flag is an explicit opt-in that says "yes, I mean it", and
the environment is the box saying what it is. `DEMO_MODE` set on a staging or production box is a
**boot failure**, not a warning (`backend/internal/config/config.go:31-34,184-194`).

The demo flows themselves are data: migration `0210_four_flows_that_finish.sql` carries the four
flows through their write, and `0211` gives the receivables flow its own chapter.

### 19.6 The tour

Section 11.4. Nine chapters as data (0192), a per-person bookmark (0193), three demo personas
(0200), and a byte-absence contract when off.

---

## 20. Deploy

Shape only. No host names, keys or credentials are reproduced.

### 20.1 Two droplets, both artifact hosts

Neither droplet has a repository clone the workflow relies on, and neither needs a toolchain. Each
deploy builds the Go binaries and the frontend **on the GitHub runner**, ships one tarball over SSH,
and pipes an apply script over the same connection
(`.github/workflows/deploy-staging.yml:1-10`).

Layout, staging (`scripts/staging_apply.sh:36-42`):

- `/opt/hardscapeos/{bin,dist,migrations}`
- systemd unit `hardscapeos`
- environment file at `/etc/hardscapeos/staging.env`
- compose Postgres bound to loopback, API on port 8080
- Caddy fronting `dist` plus the API

Layout, demo (`docs/demo-droplet-provisioning.md` section 0): its own app directory
(`/opt/hardscapeos-demo`), its own systemd unit (`hardscapeos-demo`), its own environment file
(`/etc/hardscapeos/demo.env`), its own API port (8090), its own database and database role, and its
own public name. The ruling it implements is that resetting the demo world can never reach a tester's
homework on staging, and that ruling lives in three places: the workflow, the apply script (which
refuses to run against a non-demo environment file), and the provisioning checklist.

### 20.2 `deploy-staging.yml`

Triggers: `workflow_dispatch` and every push to `master`. Concurrency group `deploy-staging` with
`cancel-in-progress: false`. Secrets: an SSH key, a host, a user, each read from repository secrets;
the key step normalises paste-mangled secrets (lost newlines, CRLF, a literal escape) and validates
with `ssh-keygen` before any connection, so a bad secret fails loudly. A guard job runs first: a
manual dispatch with missing secrets is a real failure, while an automatic run on push soft-skips to
keep `master` green.

Four binaries are built and shipped: `server`, `migrate`, `seed`, `demoseed`. A bundle without
`bin/demoseed` is rejected on the droplet rather than deployed quietly.

### 20.3 `staging_apply.sh`

Sequence (`scripts/staging_apply.sh:6-13`): unpack, back up the current artifacts and take a database
dump (a **hard failure** unless `SKIP_BACKUP=1`), migrate, `demoseed -apply`, then swap in the new
artifacts, restart the unit, and check `healthz`. Migrate and demoseed both run **before** the
artifact swap, so a failure in either rolls back with nothing swapped. Failures print the journal
tail and the rollback command.

### 20.4 `deploy-demo.yml`

A near-copy of the staging workflow, with four deliberate differences
(`.github/workflows/deploy-demo.yml:17-35`):

1. It triggers on pushes to a `demo` branch a human moves, so a demo can be pinned to a known-good
   commit while `master` keeps moving.
2. Reset is a `workflow_dispatch` input defaulting to false. A push event carries no inputs, so a
   push **cannot** reset: the guarantee that nobody wipes a demo mid-show by pushing a branch is
   structural, not a default someone can edit.
3. `bin/seed` is built **and run** on the demo droplet, because one persona comes from the seed and
   from no migration. Staging builds the same four binaries and runs only `demoseed`.
4. The public verify asks three questions rather than one: `healthz` proves the site is up;
   `GET /api/v1/demo/flows` over the public URL proves the proxy routes the API to the demo unit and
   that the two demo flags actually took; and the bare domain is compared byte for byte against
   `/demo.html`, because a missing site block would open the demo's front door on the ERP login form
   with nothing else noticing.

Demo secrets fall back to the staging ones if unset, so moving the demo to its own droplet is
a secrets change and not a workflow change.

### 20.5 `healthz` semantics

`GET /healthz` (`backend/cmd/server/main.go:82-111`):

- Returns **503** when the database is unreachable, via a **live** `pool.Ping` with a short
  timeout, not a startup nil check. Both deploy gates depend on it failing honestly.
- The JSON body is **additive only**: `{"status":"ok","db":"up"|"down","google_sso":bool,
  "bistrack":"unconfigured"|"idle"}`. Only the status code changes on a down database, so the client
  can still read every flag.
- `pingDB` is nil in scaffold or offline mode.

### 20.6 The two droplet documents

`docs/staging-droplet-caddy.md` is a **record** of the live Caddy configuration, not a source of
truth the deploy reads; it opens by saying the Caddyfile lives only on the droplet and nothing in
this repository applies it, and it exists because "a droplet change nobody wrote down is the next
person's mystery" (`docs/staging-droplet-caddy.md:1-10`). The change it records is a `/drive`
rewrite: the catch-all block ended in `try_files {path} /index.html`, so a path with no file on disk
fell through to the ERP, and `dist/drive.html` is a real separate document
(`docs/staging-droplet-caddy.md:13-25`).

`docs/demo-droplet-provisioning.md` is a checklist a human runs once with root on the droplet. Until
it is done, `deploy-demo.yml` fails on every run and names the missing piece, which is the design: a
workflow cannot create a systemd unit, a site block or a DNS record, so the honest thing for it to do
is stop and say so rather than half-deploy into a directory nobody serves
(`docs/demo-droplet-provisioning.md:1-6`).

---

## 21. In-flight work

Kept separate, as the brief requires. This is a description of what the tree shows to be mid-stream,
not a plan and not a recommendation. The delivery continues; this section only records its shape.

### 21.1 Branch and working tree

The checkout is on branch `claude/init-uq0fzi` with a **clean** working tree
(`git status --porcelain` returns nothing). The branch's tip commit is
`e8f2dd4 Add CLAUDE.md: the repo's operating guide for Claude Code sessions`, which is the only
commit on it. Everything below it is the shared history.

### 21.2 What the most recent commits landed

From `git log --oneline -40`, newest first, with no dates reproduced:

- `e8f2dd4` adds `CLAUDE.md`.
- Four commits closing a review: `6eaea3e` (both open findings closed, a category keeps its colour
  and the empty state fits), `75c6772` (three gates that were weaker than their names), `f89037b`
  (the review's findings fixed, including one that shipped), `dd882b4` (saying what the three-theme
  end-to-end leg actually measures), `cfdd774` (the one clipped card on Home, measured and shown not
  to be the theme's).
- `14cbd9b` migration 0212 and `04b3359`: Reporting became a first-class registered unit, two waves
  after the module shipped, plus the reachability gate that found it (`a3eff2f`).
- `bdda639`, `c033d12`, `8202030`, `7dea51c`: the Loud theme, its eight hues, the fence that was
  built to open, and the discovery that the visualisation tile's second shape does not ship.
- `dce6094`: the `/drive` front door and the droplet change written down.
- `9f02d24`, `bcc3357`, `2fda7a3`, `0426a5d`: the demo site, its pre-auth landing page, the
  receivables demo flow, and running `demoseed` on every staging deploy.
- `9998384`, `24d613c`, `12c220c`, `acd0320`, `ca294be`: the ten-wave polish and Drive session, its
  hand-back notes, CI finally running the frontend suite, HH Drive itself, and Home at twelve
  columns with a gallery and the pop kit.
- `c10a02b`, `60d5dbb`, `802b0e8`, `325121e`, `88cd763`, `2559845`, `9075d2d`, `a9b34ea`: waves 6
  down to 0 of the same session, ending at a measured branch-point baseline.
- `dd72e3b` through `3c1cdc8`: the previous gate train, its follow-up, and the tzdata ruling.

### 21.3 Parked wiring

Four `PARK-*.patch` files at the repository root are diffs against
`backend/cmd/server/main.go` that were **not** committed because the composition root was outside the
lane's fence while another worktree held it:

| File | What it wires |
|---|---|
| `PARK-470-main-wiring.patch` | a block after `inventorySvc.WithPOCreator(...)` |
| `PARK-478-main-wiring.patch` | the TanyaAI conversational-depth block; the rest is committed on its own branch |
| `PARK-6667-main-wiring.patch` | the one block for the quote-assist extraction and scope-gap work |
| `PARK-INV461-main-wiring.patch` | a second inventory block in the same region |

Each patch header states the reason it is a park rather than a commit, and names the branch that
carries everything else.

`backend/internal/export/evidence_test.go:1-17` records the same situation from the other side: the
route the package serves is mounted in `main.go`, the lane did not touch `main.go`, and the wiring is
parked in a notes file, so the evidence harness runs the parked block for real out of tree behind a
small front door that reverse-proxies everything else to the real backend.

### 21.4 Named deferrals from the most recent session

the most recent session notes file at the repository root (`NOTES-SESSION-*.md`) section 10 lists nine, recorded here as-is:

1. One cockpit metric is **half fixed**, which its own note calls worse than not fixed: the period
   boundary resolves correctly through the house-zone helper and is then fed to a SQL predicate that
   promotes it back to UTC, so the tile is labelled one period and counts a different one. Named the
   highest-value follow-up.
2. Four more SQL boundary sites against timestamptz columns, in tax, gl, billing and reporting; the
   reporting one has nine sites that both filter and group, so fixing one without the other yields a
   report whose total disagrees with its own rows.
3. 84 of 86 branch-identifier request sites have no existence check: reads answer a silent empty 200
   for a nonexistent id and a 500 for a malformed one.
4. The dated-test tripwire cannot see a pinned date that is a **string**; widening it implicates 27
   test files, each needing a signed reason.
5. Fifteen remaining client-side branch defaults.
6. The driver close-out summary counts a cancelled stop as done, so an all-cancelled run offers a
   button the server answers with a 409.
7. One widget form serves two body heights, so a metric that satisfies the short one leaves the tall
   card sparse.
8. A brand lockup is pinned verbatim by two tests, which blocks two copy findings; the note says this
   needs a ruling because the pin is a deliberate brand decision.
9. `/drive` resolves as `/drive.html` until a deploy-level rewrite ships; there is no proxy config in
   this repository, so it is a named deploy task. The reachable half was taken: the bundle
   completeness check now requires the driver document.

The same file's closing section records two rulings wanted before shipping (the brand lockup pin, and
a settings knob whose branch save also rewrites a company-wide setting) and one ruling implemented
against its author's first instinct (a driver may close a run only when every stop is delivered or
failed, so a run carrying a cancelled stop is now closed by dispatch rather than the driver).

### 21.5 Lane notes that record an unapplied step

- `NOTES-DEMO-DATA.md` states in bold that nothing in that lane had been run against staging and that
  applying it was the commander's call. The subsequent commit `0426a5d` ("Run demoseed on every
  staging deploy") is the step that was later taken.
- `NOTES-PULL-INSTRUCTION.md` records that the lane shipped backend and pure client logic only, with
  **no UI**, scoped to make the yard queue right before the surface lane.
- `NOTES-dib-506.md` records the settings kit's exit criterion for the POS retrofit (two surfaces,
  one generator) as still ahead, with the specific parked step named: folding the counter policy
  presentation knobs into a POS surface registry over the shipped generic substrate.

### 21.6 Scaffolds still declared as scaffolds in source

Several module docs still carry a SCAFFOLD STATUS block saying bodies are `errNotImplemented` stubs:
`bankrec`, `finstmt`, `gl`, `invcount`, `inventory`, `landedcost`, `lien`, `tax`. In at least one
case the block is provably stale and says so in place (`compliance/module.go:94-96` records the flip
to LIVE and says "the scaffold note below is history"). The others have shipped route surfaces and
integration suites, so each block should be read as the frozen-signature contract it was written to
be rather than as a current status claim.

`bistrack` is the one module whose scaffold note is a live description: phase 0 is structurally
unable to sync, by construction, and a structural test enforces it (section 14.1).

---

## 22. Test inventory

### 22.1 Go

| Kind | Count | How counted |
|---|---|---|
| `*_test.go` files, total | **497** | `find backend -name '*_test.go'` |
| build-tagged `integration` | **185** | files containing `//go:build integration` |
| untagged (unit, no database) | **312** | the remainder |

`make test` runs only the untagged set, with in-memory fakes and no database
(`Makefile:35-36`). `make test-integration` runs the tagged set with `-p 1` after `make reset`
(`Makefile:38-39`).

Counts from the most recent session's own gate record (the most recent session notes file at the repository root (`NOTES-SESSION-*.md`) section 0),
recorded here as the numbers that session measured: backend unit went from 3711 to 3786 individual
tests, backend integration from 4902 to 4991, frontend from 2641 to 3020, with zero lost on all
three. The integration figure was verified by a second run against the same database returning a
byte-identical name list. Three export tests skip without a spreadsheet converter installed, which
accounts for a three-test difference against the brief's figure.

### 22.2 The testaudit tripwires

`backend/internal/testaudit` is a test-only package with no non-test source that nothing imports
(`dated_tests_test.go:1-2`). Two tripwires:

1. **The dated-test tripwire** (`dated_tests_test.go`). A test that pins a calendar instant and then
   compares it against a real clock (`time.Now()` in Go, or the SQL now and current-date tokens
   inside a string literal the test executes) is not a test; it is a claim about a date that has not
   arrived, and the gap widens by one unit per unit. Two such tests went red for nobody's commit
   before this existed, both named in the header. It is an **AST walk, not a grep**, because a grep
   over-reported: it flagged a file whose only `now()` is a fake-clock method and another whose only
   `time.Now` is a word in a comment. It is file-scoped and therefore deliberately conservative: it
   proves the two ingredients are in one file and cannot prove they meet in one assertion, so each
   surviving instance is signed off in an allowlist **with a reason**, per kind rather than per file,
   because a file-level entry would wave a newly added bomb straight through
   (`dated_tests_test.go:6-52`).
2. **The house-zone tripwire** (the second file in `backend/internal/testaudit`). It asks
   mechanically how many places still decide the period boundary at Greenwich. The header records that the house had ruled on this five times and
   written the argument down each time (in messaging, inventory, messagingprovider, gl and dashboard),
   that every one of those five was found by hand by somebody who happened to be looking at the right
   file, and that nothing had ever asked how many remained. It was written alongside the sixth
   (that file, lines 16 to 34).

### 22.3 The gate suite

Section 15.3. Seven files in `backend/internal/gate`, all `integration`-tagged, holding GATE-1
through GATE-9, HS-1/2/3, the order-lifecycle route test, and the stored-copy vocabulary scan.
Not in CI by design.

`backend/internal/e2e` is a second test-only package (7 files) holding cross-module end-to-end
fixtures.

### 22.4 The advisory lease

`dib396*Lease` appears in four packages (`gate`, `e2e`, `inventory`, `invcount`, plus
`platform/db`). It is a Postgres advisory lock, held per session and spanning processes, taken
`Shared` by a fixture that must transiently break a whole-database invariant and `Exclusive` by a
test that asserts one. Postgres queues a pending exclusive request ahead of new shared requests, so
the asserting test is not starved. **It delays a read; it never filters a row**
(`backend/internal/gate/dib396_lease_test.go:1-24`).

### 22.5 Frontend

| Kind | Count |
|---|---|
| `.test.mjs` files | **170** (169 in `app/src`, 1 in `app/src/portal`) |
| Playwright specs | **1** (`app/e2e/entry-contrast.spec.ts`) |

Run by the Node built-in runner through `app/src/_ts-js-loader.mjs`. `npm test` runs the glob;
`npm run test:ci` wraps it in the ran-count floor. The most recent session recorded that on the
pinned Node version a glob matching nothing prints zero tests and still exits 0, so the version pin
alone does not retire the risk and the ran-count guard is load bearing
(the most recent session notes file at the repository root (`NOTES-SESSION-*.md`) section 11).

### 22.6 The other checks

- `app/scripts/contrast-check.mjs` measures colour pairs.
- `app/scripts/phantom-tokens.mjs` finds `var(--never-defined)`.
- `app/scripts/pair-audit.mjs` audits token pairs.
- `scripts/gate-sessionA.sh` and `scripts/gate-sessionB.sh` run a whole session gate in one command:
  from-zero plus upgrade-onto-the-previous-migration, all three suites, a second-run trap, a multiset
  diff of test names, a bundle scan, and clause checks. The most recent recorded full run was
  24 clauses passed and 0 failed (the most recent session notes file at the repository root (`NOTES-SESSION-*.md`) section 0).

---

## Appendix A: the migration list

196 files. Number, filename stem, and the one-line purpose from the file's own header comment.
Numbers allocated but never used: 0080 to 0084, 0094, 0099, 0127, 0128, 0135, 0142, 0150, 0163,
0188 to 0190, 0196. `0043` is used twice with two different stems.

| # | Stem | Purpose (from the file header, with calendar words and dates removed) |
|---|---|---|
| 0001 | `extensions` | platform extensions only |
| 0002 | `uom_and_products` | UoM system (as data, not enum) + product master + hardscape facets |
| 0003 | `locations_and_branches` | single locations hierarchy, branches as roots; Trenton + Kingston seeded |
| 0004 | `identity_rbac_scaffold` | identity + 3-tier RBAC scaffold. A-20 (W1) seeds the 13 roles + authority matrix |
| 0005 | `stock_ledger` | immutable double-entry stock ledger + balance projection + layer-capable valuation |
| 0006 | `customers` | customer master + contacts. Global parties; home branch is a preference, not a fence |
| 0007 | `gl_block` | the GL block , BEFORE any financial document table exists (R3) |
| 0008 | `quotes_orders` | quotes + orders (separate tables; conversion is a link, not a mutation) |
| 0009 | `invoices_ar` | invoices + AR subledger. Bill 60 holdback column from birth (M6/W6 builds the cockpit) |
| 0010 | `payments_till` | payments + allocations + till sessions. Till first (payments FK it) |
| 0011 | `vendors_purchasing` | vendors + purchase orders + receiving headers |
| 0012 | `manifests` | durable, versioned dispatch manifests + MTO classification |
| 0013 | `audit_outbox` | audit log + transactional event outbox |
| 0014 | `dibbits_branch_config` | Dibbits branch configuration (W1 , M7 / Branch Setup folded in) |
| 0015 | `rbac_roles` | A-20 RBAC , seed Dibbits's 13 staff roles across 3 tiers (W1) |
| 0016 | `permission_approvals` | A-20 permission override / approval queue (W1) |
| 0017 | `dibbits_branch_addresses` | real Dibbits branch addresses + contact (Q-028 answered on thecall) |
| 0018 | `promote_udf_attributes` | A-18 , promote in-use BisTrack Product UDFs to first-class product columns |
| 0019 | `portal_users_and_invites` | CP-07 , contractor portal multi-user auth |
| 0020 | `approval_subject_ref` | H-2 , bind an override request to an optional subject (a future quote/order) |
| 0021 | `audit_schema_invariants` | post-W2-audit schema invariants (M-2, L-2, L-6, L-8) |
| 0022 | `fix_stock_drift_view` | correct v_stock_balance_drift for TRANSFER moves (L8 review nit) |
| 0023 | `pricing` | pricing engine tables (A-07 / W3). Owner module: internal/pricing |
| 0024 | `pricing_seed` | SEED only the CATEGORY-INDEPENDENT pricing reference data , the TIER_DEFAULT CONTRACTOR rule |
| 0025 | `pricing_bps_ranges` | defense-in-depth bps range CHECKs on the rule + override calc-value columns |
| 0026 | `quantity_break_adjustments` | quantity breaks become ADJUSTMENTS on the tier-resolved base (A-07 stacking fix + editor) |
| 0027 | `payment_config` | payment processor config (encrypted, write-only creds) + payments processor/token/card_type |
| 0028 | `till_drawer_ops` | A-03 till / cash-drawer drawer-session ops (W4, gate criterion 3 cash half) |
| 0029 | `pos_counter_sales` | A-19 POS / counter-sales. Idempotency + minimal FN-tax-evidence data model + WALK-IN seed |
| 0030 | `portal_commerce` | contractor-portal commerce , stored payment methods, password resets, and the |
| 0031 | `procurement` | contractor-portal procurement Planning board (P4.2, centerpiece M3). Forward-only |
| 0032 | `gl_module` | W5 A-01 , GL module additions on the EXISTING seeded 0007 block (gl_accounts / gl_journals / |
| 0033 | `bank_recon` | W5 A-02 , bank reconciliation. Owner module: internal/bankrec |
| 0034 | `financial_statement_views` | W5 A-04 , financial statement read views. Owner module: internal/finstmt (reads these views ONLY) |
| 0035 | `nsf_credit_hold` | W5 A-05 , NSF handling + credit holds + declined-attempt log. Owner module: internal/till (NSF |
| 0036 | `w5_remediation` | W5 S3 remediation , the reserved integrator migration. Owner modules: internal/bankrec + internal/gl |
| 0037 | `inventory_core` | W6 INV-CORE , the minimal inventory spine sales/counts/landed-cost build on. Owner: internal/inventory |
| 0038 | `lien_holdback_compliance` | W6 M6 , Lien Cockpit + Bill 60 holdback + compliance calendar. Owners: internal/lien + internal/compliance |
| 0039 | `price_change_log` | W6 A-08 , Price Change Log: batch manufacturer price updates with scheduled effective dates. Owner |
| 0040 | `inventory_counts` | W6 A-15 , Inventory Counts: cycle counts with reason-coded GL adjustments + per-period recon. Owner |
| 0041 | `landed_cost` | W6 A-16 , Landed Cost: freight/duty/handling capitalized into product cost, incl. USD POs. Owner |
| 0042 | `tax_engine` | W6 M5 , Tax Engine: the configurable Ontario HST table + status-card exemptions that replaces the W4 |
| 0043 | `pos_counter_v2` | POS Counter V2 , the client-confirmed transaction types the W4 counter didn't carry |
| 0043 | `super_admin_platform_role` | SUPER_ADMIN platform role + bootstrap the platform owner |
| 0044 | `platform_admin_password_reset` | reset the platform-owner (colton@futurebuild.ai) password off the |
| 0045 | `portal_return_requests` | A-06 , contractor-portal customer returns with a REQUEST + STAFF-APPROVAL flow (commander-ruled |
| 0046 | `uat_portal_test_fixtures` | UAT portal test fixtures , a fresh contractor login + a returnable/reorderable order |
| 0047 | `staff_password_reset` | staff (ERP) admin-generated password reset (DIB-186) |
| 0048 | `portal_projects` | CP-03 Customer-Portal Projects/Jobs rollup (scope reconciliation, A-3) |
| 0049 | `seed_demo_project` | seed one demo Project and link the UAT fixture order SO-UAT-0001 to it, so CP-03's |
| 0050 | `messaging_core` | MSG-01 Internal Messaging Hub , P1 core schema (channels / members / messages / attachments) |
| 0051 | `messaging_unread_view` | MSG-01 , unread-summary view + the messaging event-topic contract note |
| 0052 | `messaging_demo_seed` | MSG-01 , demo channels + members so the hub is demoable out of the box (walkthrough #17) |
| 0053 | `warranty_claims` | WTY-01 Warranty-Claim Management (P1). Owner: internal/warranty. Spec: PRODUCT_SPEC.md (G-LOCK-077) |
| 0054 | `warranty_registry_seed` | WTY-01 default checklist template (P1). Owner: internal/warranty. DIB-153 |
| 0055 | `portal_demo_fixtures` | SELF-CONTAINED portal demo fixtures for CP-03 (Projects/Jobs rollup) demonstrability |
| 0056 | `bistrack_tieout_demo` | M2-C1 BisTrack tie-out DEMO SEED , post the deterministic FY2026-06 counterpart GL journals the |
| 0057 | `warranty_demo_seed` | WTY-01 warranty DEMO seed , makes GET /warranty/vendors (FR-1 registry) and the FR-7 dashboard |
| 0058 | `landedcost_demo_fixture` | L8 landed-cost , (1) SCHEMA: snapshot column for the cent-exact Apply/Void round-trip, and (2) a |
| 0059 | `order_history_demo` | DIB-159 ORDER-HISTORY FEATURE , one DEMO order with a realistic, multi-step |
| 0060 | `messaging_demo_topic_dm` | MSG-01 , make the TOPIC and DM channel kinds demonstrable with the ONLY seeded staff login |
| 0061 | `homework_demo_users` | Homework demo seed , make the Dibbits team homework flows (command/state/demo/ |
| 0062 | `homework_roles_pricing_fix` | Homework polish fix , applied AFTER 0061 already ran on staging (migrations run once) |
| 0063 | `homework_flow_fixes` | homework-flow blocker fixes surfaced by walking the flows live (sweep #46) |
| 0064 | `ar_aging_customer_name` | T2.2 , AR Aging showed a raw customer UUID under a "Customer" header. Add the customer NAME |
| 0065 | `dpa3_low_margin_demo` | D-PA-3 recurring low-margin report , demo cost + committed-sale fixtures so the |
| 0066 | `ryan_rood_email` | Ryan Rood's homework/login email → ryan@roodinc.ca (was ryanr@coppsbuildall.com) |
| 0067 | `bulk_category_and_aggregate_enforcement` | DIB-260 (Bulk as a first-class pricing category) + DIB-252 (enable AGGREGATE margin |
| 0068 | `till_variance_threshold` | DIB-264 , cash over/short tolerance set to ±$3 (client answer, W8 UAT feedback) |
| 0069 | `messaging_notif_prefs_and_tiers` | MSG-01 , per-user notification preferences + channel tiers + the #alerts-all firehose |
| 0070 | `messaging_retention_archive` | MSG-01 , message retention policy + archive (Feature B). Archive-never-delete |
| 0071 | `messaging_forced_ack` | MSG-01 Feature 1 , Screen-Takeover forced acknowledgement (requires_ack + msg_acks) |
| 0072 | `portal_inbox_channel` | MSG-01 scopeBrief #6 , Contractor-Portal → Chat inbound bridge (portal-inbox house queue) |
| 0073 | `tasks_and_task_events` | WMS-Lite , Chat→Order Tasks (PICK walking skeleton) + append-only transition audit |
| 0074 | `staging_uat_admin_access` | Grant the four Dibbits UAT testers full platform-admin (SUPER_ADMIN) so they can |
| 0075 | `approvals_rail` | MSG-APPROVALS generic approvals rail v1 (DIB-216 / B1) , additive CHECK widenings ONLY |
| 0076 | `messaging_branch_team_channels` | MSG-01 / DIB-272 , per-branch role TEAM channels (#sales-<branch>, #drivers-<branch>) |
| 0077 | `uat_tier3_inside_sales` | DIB-191 B2/R6: a Tier-3, branch-scoped UAT staff account so the |
| 0078 | `audit_log_seq` | a monotonic insertion-order key on audit_log (DIB-175 B2-1) |
| 0079 | `bistrack_sync` | BisTrack live-sync persistence (DIB-53 phase 0 scaffold). Owner: internal/bistrack |
| 0085 | `quote_staff_surface` | the staff quote surface (DIB-56, bundle W10-A) |
| 0086 | `quote_share_links` | quote share links + customer acceptance (DIB-57, bundle W10-A) |
| 0087 | `branch_order_numbering` | branch-aware order numbering (DIB-388) |
| 0088 | `print_queue` | print_queue , the one substrate every printed document in this system will go through |
| 0089 | `typed_name_signature_provider` | the typed-name attestation provider (DIB-386, bundle W1A-B) |
| 0090 | `dispatch_core` | dispatch core , the physical leg of the order lifecycle (DIB-64, bundle W2-A) |
| 0091 | `dispatch_pod_photos` | proof-of-delivery photos (DIB-64, bundle W2-A) |
| 0092 | `will_call_pickups` | will-call QR pickup self-confirmation (DIB-79, bundle W2-A) |
| 0093 | `dispatch_demo_fleet` | demo fleet + depot coordinates for the two seeded branches (DIB-64, bundle W2-A) |
| 0095 | `yard_pick_stage` | Yard pick → STAGE → shortage, on the shipped WMS-Lite tasks substrate (0073) |
| 0096 | `job_scheduler` | the job scheduler this ERP does not have. Owner: internal/jobs (Wave 2, Swarm B) |
| 0097 | `sms_outbox` | the out-of-band (SMS) notification outbox. Owner: internal/messagingprovider (Wave 2, Swarm B) |
| 0098 | `pricing_audit_entity_ref` | DIB-402 , entity-reference columns on pricing_audit_log, so an audit row can be |
| 0100 | `pos_split_tender` | DIB-165 POS split tender , ONE sale settled across MULTIPLE tenders (cash + card + account) |
| 0101 | `product_barcodes_and_synonyms` | GABLE:pos-barcode + GABLE:search-synonyms , the two lookup tables the counter search needs |
| 0102 | `product_images` | GABLE:product-images-ui , product thumbnails as first-class catalogue data |
| 0103 | `payment_intents` | payment_intents , the durable PRE-CHARGE intent that makes the portal replay guard real |
| 0104 | `ar_credit_gates` | AR credit gates , automatic credit holds from aging / limit breach / COD (DIB-65, DIB-52) |
| 0105 | `ar_statements` | AR statements , period-end mass-send + on-demand interim statements (DIB-166) |
| 0106 | `approvals_and_reversals` | JE approver assignment + payment reversal provenance + the surcharge/EFT flip (DIB-360/361 |
| 0107 | `dispatch_mto_pod_reschedule` | dispatch follow-through , MTO compliance on the manifest, POD photo attachment, and the |
| 0108 | `scan_trail_weight_advance` | the pallet/load scan trail, the NEED_WEIGHT round trip, and the task→order advance gate |
| 0109 | `customer_sms_prefs` | SMS consent, quiet window, and a send-window column on the notice queue |
| 0110 | `ar_notices_quote_followups` | the two cadence ledgers , AR collections notices (DIB-76) and quote follow-ups (DIB-58) |
| 0111 | `pricing_promotions_clearance` | DIB-70 , seasonal promotions + clearance |
| 0112 | `pricing_rebates` | DIB-69 , manufacturer rebate engine, CORE ACCRUAL ONLY |
| 0113 | `pricing_uom_conversions` | SCOPE M2-uom , the sales-flow UoM conversion table (SF → PLT → TON) and its pick policy |
| 0114 | `pricing_audit_doc_ref` | E3 unpark , pricing audit rows can point at the DOCUMENT they were written for |
| 0115 | `stock_reservations` | DIB-60 , three-tier stock allocation policy + the reservation substrate |
| 0116 | `stock_transfers` | DIB-71 , inter-branch stock transfers (Trenton <-> Kingston) |
| 0117 | `reorder_points` | DIB-72 , reorder points per (product, branch) + the replenishment worklist substrate |
| 0118 | `inventory_browse_facets` | DIB-61 , index support for the inventory browse surface (A-18 UDF facets + trigram text) |
| 0119 | `gl_entry_no_sequence` | gl_journals.entry_no is minted from a sequence row, not from MAX()+1 (DIB-418) |
| 0120 | `purchasing_core` | purchasing core , branch-scoped PO numbering, the PO aggregate, and the receiving document |
| 0121 | `purchasing_ap_bills` | AP vendor bills, the three-way match, and instalments |
| 0122 | `purchasing_posting_rules` | the GL account + posting rules a vendor bill posts through |
| 0123 | `gl_entry_no_selfheal` | next_entry_no floors itself at the register it is numbering (DIB-418, follow-up) |
| 0124 | `union_w4_texture_facet` | UNION-W4 (B's E1, supervisor ruling): texture is now a REAL queryable facet (DIB-61 filter + |
| 0125 | `order_change_orders` | SCOPE:F10 , post-pick change orders |
| 0126 | `order_shipment_groups` | DIB-78 , multi-shipment / single-invoice fulfilment groups |
| 0129 | `warranty_settlement_paths` | ERP:warranty-p2 , PER-OUTCOME SETTLEMENT MONEY PATHS. Owner: internal/warranty (Wave 5, Swarm B) |
| 0130 | `warranty_recovery_board` | DIB-221 , WTY-RECOVERY, the outstanding-money recovery worklist. Owner: internal/warranty (W5-B) |
| 0131 | `warranty_public_intake` | DIB-222 , WTY-INTAKE v1, the PUBLIC warranty intake form. Owner: internal/warranty (W5-B) |
| 0132 | `warranty_sla` | DIB-223 , WTY-SLA, per-phase SLA timers and overdue alerts. Owner: internal/warranty (W5-B) |
| 0133 | `pricing_override_authority_reason` | DIB-420, Wave 5 Swarm C |
| 0134 | `purchasing_currency` | DIB-421, Wave 5 Swarm C. The deferred coherent chunk from |
| 0136 | `msg_rep_assignments_house_queue` | DIB-273 + DIB-333 (rep assignment) + DIB-271 (house fallback queue) |
| 0137 | `msg_notification_modes` | DIB-268 (per-channel notification preferences, respected by the badge pipeline) + |
| 0138 | `msg_events_supersede` | DIB-217 , MSG-EVENTS high-priority automations, forced-ack bubbles, and the |
| 0139 | `msg_conversations_demo` | Wave 5 / Swarm D , demo content so the conversational tier is demoable out of the box |
| 0140 | `dashboard_layouts` | DIB-353: the cockpit's role-based tile catalog + per-user overrides |
| 0141 | `dashboard_profile_home` | DIB-354: the role-based screen takeover, as CONFIG |
| 0143 | `reporting_templates` | DIB-95 , the native report substrate (report_templates + report_runs + a REPORT print doc type) |
| 0144 | `commission_rules` | DIB-96 / A-22 , commission tracking (rules, computed periods, drill-through lines) |
| 0145 | `reporting_saved_views` | DIB-95 follow-on , saved parameter sets, plus the two read-path indexes the report queries need |
| 0146 | `billing_invoice_documents` | DIB-422 , the invoice DOCUMENT shape that lets a second producer share the invoices table |
| 0147 | `multiship_invoice_link` | DIB-422 , the shipment group → invoice link, and the CLOSED end of the group ladder |
| 0148 | `statement_run_notice_counts` | statement-run NOTICE accounting , making the HOS_STATEMENT_NOTICES flip an informed act |
| 0149 | `dashboard_pinned_tabs` | DIB-433: the tab strip's role-pinned working set, as DATA |
| 0151 | `quick_action_visibility` | DIB-434: whether a global quick-action RENDERS is DATA |
| 0152 | `msg_event_classes_rollup` | DIB-449 , EVENT-CLASS TIERS, the ROUTINE recurring rollup, and the standalone #payments channels |
| 0153 | `assist_commands_config` | DIB-431 (B): the structured command layer's config-as-data |
| 0154 | `assist_token_ledger` | DIB-431 (governance 1): every AI exchange, written down once |
| 0155 | `assist_limits` | DIB-431 (governance 2): the per-period AI budget, as config-as-data |
| 0156 | `ar_credit_memo_repost_schedule` | DIB-474: the schedule row DIB-429 was missing |
| 0157 | `msg_payment_notify_rules` | DIB-462 , targeted payment notifications (per-user opt-in rules) + |
| 0158 | `transfer_eta_transit_*` | DIB-472 , a stock transfer carries a PROMISED ARRIVAL |
| 0159 | `quote_scope_rules` | DIB-67 (SP-04) , quote scope-gap rules, AS DATA |
| 0160 | `portal_split_buyer_roles` | DIB-479 / OR-2 , the two SPLIT-BUYER portal roles |
| 0161 | `order_notes` | DIB-493 , CRM-style human notes on an order |
| 0162 | `quote_accept_controls` | DIB-491 , quote acceptance controls (D-097, Aug-18 sync) |
| 0164 | `dealer_hotkeys` | DIB-496 (ruling D-095): keyboard shortcuts are DEALER-level DATA |
| 0165 | `pos_workspace_prefs` | DIB-488 / D-096 , POS workspace presentation preferences, as dealer-configurable DATA |
| 0166 | `pos_workspace_layout` | DIB-488 rev 2 , the POS two-panel WORKSPACE layout: dealer defaults + per-user ergonomics |
| 0167 | `pos_lookup_presentation` | DIB-488 rev 3 , the INVENTORY LOOKUP panel's results presentation: CARDS or ROWS |
| 0168 | `pos_po_salesperson` | 0168 , DIB-488 rev 6 (item D): optional PO / Job reference + salesperson on the counter sale |
| 0169 | `pos_addon_pairings` | 0169 , DIB-502 (built inside the DIB-488 rev-8 slate): the suggested add-ons CO-PURCHASE |
| 0170 | `pos_margin_job_fields` | 0170 , DIB-488 rev 8: margin visibility (dealer config + per-user quick-hide), the curated |
| 0171 | `dealer_hotkeys_pay` | UNION-TRAIN11: the concept's "F8 Pay" joins the dealer hotkey map |
| 0172 | `surface_settings_substrate` | 0172 , DIB-507 (D-105): the configurable-surfaces settings kit's GENERIC substrate |
| 0173 | `quote_policy_terms` | 0173 , DIB-506/DIB-507: quote_policy (the pilot's TYPED policy half of the D-105 hybrid) + the |
| 0174 | `surface_workspace_open_ref` | 0174 , DIB-506 simplification pass: resume-the-open-object on a configurable surface |
| 0175 | `ar_invoice_deliveries` | 0175 , DIB-510: WHAT WE SENT A CUSTOMER ABOUT AN INVOICE, AND WHETHER THEY OPENED IT |
| 0176 | `ar_promises_to_pay` | 0176 , DIB-510: A PROMISE TO PAY, in the customer's own words |
| 0177 | `ar_invoice_disputes` | 0177 , DIB-510: A DISPUTED INVOICE. The customer is not late; they are arguing |
| 0178 | `ar_invoice_register_surface` | 0178 , DIB-510: the receivables register as a CONFIGURABLE SURFACE , the D-105 hybrid, second |
| 0179 | `ar_invoice_register_demo` | 0179 , DIB-510: the receivables register's DEMO FIXTURE. A book of invoices that actually has an |
| 0180 | `ar_demo_photo_pixel` | 0180 , DIB-510 fix pass: the demo delivery photographs were a RED pixel |
| 0181 | `pos_balance_visibility` | 0181 , the counter's ACCOUNT STANDING: who period see a customer's past-due balance at the till |
| 0182 | `home_metric_catalog` | the home screen's COMPOSABLE METRIC CATALOG, as curation |
| 0183 | `ar_landing_reminders` | 0183 , the AR LANDING half: the aging cards over the shipped register (DIB-510), the reminder |
| 0184 | `dispatch_loadboard` | 0184 , the DISPATCH LOAD BOARD (Aug-26 call, dispatch requirement set A, I) |
| 0185 | `dispatch_commit` | 0185 , COMMIT, NOT LOCK. The client's vocabulary, and the model correction it exposes |
| 0186 | `pos_hotlist_bulk_board` | 0186 , the counter hot list opens on the YARD'S BULK BOARD (commander's ruling, 27 Aug, R2) |
| 0187 | `task_pick_lines` | 0187 , WHAT WAS ACTUALLY PULLED, line by line. Owner: internal/tasks. Session A, item 1 |
| 0191 | `platform_hotkeys` | D-111: the PLATFORM tier of the hotkey model |
| 0192 | `tour_substrate` | 0192 , THE GUIDED DEMO WALKTHROUGH, AS DATA |
| 0193 | `tour_progress` | 0193 , WHERE ONE PERSON HAD GOT TO, so a tour can be left and picked up |
| 0194 | `order_quoted_freight` | 0194 , the QUOTED freight baseline on an order, so a raised freight has something to be raised ABOVE |
| 0195 | `dispatch_freight_raise` | 0195 , the second-truck approval gate: a FIFTH policy on the ONE approvals queue, and the parked |
| 0197 | `purchasing_vendor_order` | 0197 , the standardized outbound purchase order: the vendor's ordering desk, the vendor's own |
| 0198 | `price_file_import` | 0198 , THE PRICE-FILE IMPORT: a per-vendor column mapping that is created when a file arrives |
| 0199 | `operator_copy_carries_no_workstream_code` | 0199 , every place a workstream code is stored as DATA and rendered to a person |
| 0200 | `tour_demo_personas` | 0200 , the three demo personas the guided walkthrough has no way to run as |
| 0201 | `dashboard_widgets` | THE WIDGET SUBSTRATE. Home stops being a grid of equal boxes and |
| 0202 | `widget_carryover` | MOVE EVERY CHART SOMEBODY BUILT ONTO THE WIDGET SUBSTRATE, LOSING |
| 0203 | `dispatcher_dock_home` | 0203 , the dispatcher gets the dock Home |
| 0204 | `yard_landing_retires_its_takeover` | 0204 , the yard shape lands on its own Home. The driver keeps the run sheet |
| 0205 | `widget_windows_tell_the_truth` | 0205 , a widget's window says what its number actually measures |
| 0206 | `a_demo_row_names_a_demo_person` | 0206 , the demo world had rows and it had people, and nothing joined them |
| 0207 | `widget_free_spans` | 0207 , the bento becomes twelve columns wide, and a widget period say its own span |
| 0208 | `widget_gallery` | 0208 , "Add widget" opens a GALLERY first, and the gallery is rows |
| 0209 | `how_long_a_driver_stays_signed_in_is_a_dealers_knob` | 0209 , how long a driver stays signed in stops being a constant in a Go file |
| 0210 | `four_flows_that_finish` | 0210 , the four demo flows stop at the interesting part, and this carries them through the write |
| 0211 | `the_receivables_flow_gets_a_chapter_of_its_own` | 0211 , the receivables flow gets a chapter of its own, and the platform keys get theirs back |
| 0212 | `reports_is_a_surface_now_so_the_strips_can_point_at_it` | 0212 , Reports is a surface now, so the manager and admin strips can stop pointing at a stand-in |

---

## Appendix B: every backend package, with its file count

Counts are `.go` files at the package's own directory level; files inside a listed subdirectory are
not included in that package's count (`assist/adapters` holds one further file, and the `testdata`
subdirectories hold fixtures rather than Go). 1,016 Go files across the listed directories.


| Package | Go files | non-test | test |
|---|---|---|---|
| `assist (subdirs: adapters)` | 37 | 23 | 14 |
| `audit` | 6 | 4 | 2 |
| `bankrec (subdirs: testdata)` | 10 | 7 | 3 |
| `billing` | 53 | 32 | 21 |
| `bistrack (subdirs: testdata)` | 12 | 9 | 3 |
| `branches` | 9 | 4 | 5 |
| `compliance` | 10 | 6 | 4 |
| `config` | 3 | 1 | 2 |
| `customer` | 18 | 9 | 9 |
| `dashboard` | 47 | 25 | 22 |
| `demo` | 5 | 2 | 3 |
| `dispatch` | 43 | 22 | 21 |
| `e2e` | 7 | 0 | 7 |
| `export` | 13 | 6 | 7 |
| `feedback` | 3 | 2 | 1 |
| `finstmt` | 14 | 8 | 6 |
| `gate` | 7 | 1 | 6 |
| `gl (subdirs: testdata)` | 33 | 12 | 21 |
| `identity` | 20 | 9 | 11 |
| `invcount` | 9 | 5 | 4 |
| `inventory` | 50 | 27 | 23 |
| `jobs` | 5 | 3 | 2 |
| `landedcost` | 10 | 6 | 4 |
| `lien` | 12 | 9 | 3 |
| `localization` | 2 | 1 | 1 |
| `messaging` | 51 | 24 | 27 |
| `messagingprovider` | 23 | 13 | 10 |
| `notify` | 8 | 4 | 4 |
| `order` | 57 | 28 | 29 |
| `payment` | 20 | 9 | 11 |
| `payments` | 22 | 12 | 10 |
| `platform/db` | 4 | 1 | 3 |
| `platform/events` | 2 | 1 | 1 |
| `platform/httpx` | 2 | 1 | 1 |
| `platform/logging` | 1 | 1 | 0 |
| `platform/moneyutil` | 2 | 1 | 1 |
| `portal` | 43 | 24 | 19 |
| `pos` | 52 | 27 | 25 |
| `pricing (subdirs: testdata)` | 47 | 23 | 24 |
| `printing` | 12 | 7 | 5 |
| `procurement` | 5 | 3 | 2 |
| `product` | 16 | 8 | 8 |
| `purchasing` | 29 | 15 | 14 |
| `quote` | 55 | 33 | 22 |
| `quoteassist` | 14 | 8 | 6 |
| `reporting` | 21 | 13 | 8 |
| `settings` | 12 | 7 | 5 |
| `tasks` | 23 | 12 | 11 |
| `tax` | 8 | 5 | 3 |
| `testaudit` | 2 | 0 | 2 |
| `till` | 14 | 7 | 7 |
| `tour` | 6 | 4 | 2 |
| `warranty` | 27 | 15 | 12 |
| **total** | **1016** | | |
