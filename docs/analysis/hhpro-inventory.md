> Origin: `analysis/hhpro-inventory.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# HH Pro: repository inventory

Lane L1 of session HH-1. Read-only analysis of `/home/user/hh_pro_dibbits` at branch
`analysis/hh-1`, tip commit `be46837` (`git log --oneline -30`). Every claim below cites a
repository path. Where `CLAUDE.md` disagrees with the code, the code wins and the divergence is
called out explicitly.

Scope note: this file is an inventory for a downstream planner who must be able to rebuild the
product on a different stack (Lit plus Ionic on a Go core, per the App Shape) without reopening
the repository. It describes what exists. It proposes nothing.

---

## 1. Stack and build

**Runtime and package manager.** Node, floor declared as `>=20.19.0` in `package.json:6-8`. There
is no `.nvmrc` and **no CI configuration in this repository at all**: `.github/` does not exist
(verified by listing the repository root, `ls -a`), and `.gitignore:20` ignores `.claude/`. The
deploy workflow that would run on a runner is parked, inert, at
`deploy/github-workflow-deploy-hhpro-staging.yml` and pins `node-version: 22`
(`deploy/github-workflow-deploy-hhpro-staging.yml:69-71`).

**Bundler.** Vite 6 (`package.json:59`), configured in `vite.config.ts`. Two entry points are
declared in `build.rollupOptions.input` (`vite.config.ts:69-77`):

| Entry | HTML | Mounts | Purpose |
|---|---|---|---|
| `main` | `index.html` | `src/main.tsx` | the contractor portal |
| `admin` | `admin.html` | `src/admin/main.tsx` | the dealer admin console |

The split is load-bearing: admin code must not ship inside the contractor bundle, which is the
bundle headed for embeddable web components (`vite.config.ts:70-73`). `admin.html:6` sets
`robots: noindex, nofollow` and deliberately ships **no** web manifest
(`admin.html:20-27`) so the console cannot be installed to a phone home screen.

Build target `es2022`, `sourcemap: true` (`vite.config.ts:66-68`). The sourcemap choice is
acknowledged in `deploy/RUNBOOK.md` as the reason the deployed bundle is large and ships full
sources publicly.

**The four gates** (`package.json:10-20`, restated in `CLAUDE.md`):

| Command | Script | What it is |
|---|---|---|
| `npm run typecheck` | `tsc --noEmit` | strict TypeScript, see below |
| `npm test` | `vitest run` | two vitest projects, see section 15 |
| `npm run build` | `tsc -b && vite build` | the pre-flight |
| `npm run check` | `biome check --write .` | format plus lint |

**The on-demand checks**, each a standalone Node script driving a real browser via Playwright:

| Command | Script | Fixed port | What it does |
|---|---|---|---|
| `npm run a11y` | `scripts/a11y-audit.mjs` | 4319 (`:18`) | axe over every screen of both apps at phone width, against a production build; fails on serious or critical violations |
| `npm run security` | `scripts/security-smoke.mjs` | 5417 (`:23`) | boots a real dev server and attacks it; every assertion maps to a vulnerability that was present and verified |
| `npm run e2e` | `scripts/e2e-smoke.mjs` | 4329 (`:21`) | journeys, not components, against a production build; any uncaught page error or 404 fails the run |
| `npm run contrast` | `scripts/contrast-probe.mjs` | n/a | paints a token to a canvas in a real browser and reads the pixel back |
| `npm run guide` | `scripts/capture-guide.mjs` | 4310 (`:25`) | rebuilds, walks the app, rewrites `docs/screenshots/` and checks that `docs/user-guide.md` references only PNGs the run produced |
| `npm run swatches` | `scripts/generate-swatches.mjs` | n/a | renders one SVG swatch per SKU into `public/images/products/` |

Every port-claiming script refuses to start if something is already listening, aborts if its own
server exits before serving, spawns `detached` and kills the process group, and waits for the port
to come back (`scripts/e2e-smoke.mjs:30-62` is the representative implementation). The guide
additionally asserts the served `<title>` matches this repository's own `index.html`.

**TypeScript** (`tsconfig.json`): `strict`, plus `noUncheckedIndexedAccess`, `noImplicitOverride`,
`noUnusedLocals`, `noUnusedParameters`, `noFallthroughCasesInSwitch`, `exactOptionalPropertyTypes`,
`verbatimModuleSyntax`, `isolatedModules`, `noEmit`. `include` covers `src`, `server`,
`vite.config.ts`, `vitest.config.ts`, `vitest.live.config.ts`.

**Path aliases**, declared in three places that must stay in sync: `tsconfig.json:26-30`,
`vite.config.ts:59-65`, `vitest.config.ts:42-48`. They are `@/*` to `src/*`, `@core/*` to
`src/core/*`, `@ui/*` to `src/ui/*`.

**Lint** is Biome 1.9.4 (`biome.json:2`), single quotes, trailing commas, semicolons, width 100.
`noExplicitAny`, `noNonNullAssertion`, `noUnusedImports`, `noUnusedVariables` are all errors.
`src/core/data/*.json` is excluded from formatting (`biome.json:4`).

**The dynamic SDK import.** `src/core/ai/session.ts` imports `@anthropic-ai/sdk` lazily rather
than statically, so the SDK is not in the contractor bundle until someone actually sends an
assistant message. `CLAUDE.md` records the measured effect (493 KB to 338 KB raw). The base URL
helper `proxyBaseUrl()` (`src/core/ai/session.ts:65`) makes the relative proxy path absolute
against `location.origin`, because the SDK builds request URLs with `new URL()` and a relative
`baseURL` throws before a byte leaves.

**Dependency surface** (`package.json:22-37`), the whole runtime list: `@anthropic-ai/sdk`,
`@dnd-kit/core` plus `sortable` plus `utilities`, `@radix-ui/react-dialog`,
`class-variance-authority`, `clsx`, `lucide-react`, `react` 18, `react-dom` 18, `react-router` 7,
`tailwind-merge`, `use-sync-external-store`, `zod`. Dev dependencies add Tailwind 4 via
`@tailwindcss/vite`, Playwright, axe, jsdom, testing-library, three self-hosted Fontsource
variable faces, and Biome.

---

## 2. Repository layout

```
index.html                 contractor entry
admin.html                 admin console entry
server/                    Node-side: proxy, admin API, admin store, dev middleware, env overlay
src/core/                  FRAMEWORK-FREE domain, sim, actions, selectors, stores, ai, supplier
src/ui/                    thin React over core
src/admin/                 the admin console React app
src/main.tsx               contractor bootstrap
scripts/                   the on-demand gates and the swatch generator
deploy/                    runbook, bundle build, Caddy vhost, systemd unit, Node entry, apply script
docs/                      user guide plus screenshots, brand token contract, strategy note
public/                    fonts, icons, brand marks, product swatches
verification/              captured evidence from a live run
analysis/                  this lane's output (contains empty `microapps/` and `sessions/` dirs)
```

### The `src/core` versus `src/ui` split, and how it is enforced

`src/core/` is framework-free: zero React imports, no exceptions. Enforced twice:

1. **Biome override** (`biome.json:35-52`): a `noRestrictedImports` rule scoped to `src/core/**`
   banning `react`, `react-dom`, `react-router`, `use-sync-external-store`, `lit`, each with an
   explanatory message.
2. **A test** (`src/core/__tests__/architecture.test.ts`): walks every `.ts`/`.tsx` under
   `src/core`, extracts static import and re-export specifiers (`:35`) and dynamic import
   specifiers (`:36`), and fails on any banned specifier (`:13-20`). It exists because a lint rule
   can be disabled inline and a test cannot (`:5-9`).

**The single React bridge** is `src/ui/hooks/useStore.ts`, a `useSyncExternalStoreWithSelector`
wrapper plus a `shallowArrayEqual` helper. Its own docstring states the migration promise: replace
this file with a roughly fifteen-line Lit ReactiveController and every file under `src/core` moves
untouched (`src/ui/hooks/useStore.ts:4-11`).

### `server/`

| File | Purpose |
|---|---|
| `server/claude-proxy.ts` | the Anthropic streaming passthrough, the health route, the rate limiter, the request validator, and the Vite plugin that mounts them |
| `server/admin-api.ts` | the dealer admin HTTP handler: authorize, read state, write config, validate and store credential, delete credential |
| `server/admin-store.ts` | the two on-disk stores plus usage accounting; resolves its data directory as `join(process.cwd(), '.hhpro')` with no environment override (`:24-36`) |
| `server/admin-plugin.ts` | injects dealer config and brand CSS into the HTML at serve AND build time, serves `/api/config`, mounts `/api/admin` |
| `server/dev-middleware.ts` | relocates the API middleware layers into the one correct slot in Vite's connect stack |
| `server/supplier-env.ts` | overlays only the `supplier` block of the raw config from three environment variables |
| `server/__tests__/` | four suites: `admin-api`, `claude-proxy`, `dev-middleware`, `supplier-env` |

### `deploy/`

`RUNBOOK.md`, `build_bundle.sh`, `github-workflow-deploy-hhpro-staging.yml` (parked, not under
`.github/`), `hhpro-staging.caddy`, `hhpro-staging.service`, `prod-entry.ts`, `staging_apply.sh`.
Detailed in section 16.

### `scripts/`

`a11y-audit.mjs`, `capture-guide.mjs`, `contrast-probe.mjs`, `e2e-smoke.mjs`,
`generate-swatches.mjs`, `security-smoke.mjs`, `swatch.mjs` (the SVG drawing library the generator
calls, `scripts/swatch.mjs:1-8`).

### `docs/`

`docs/user-guide.md` plus 34 PNGs in `docs/screenshots/` (`01-board-plan.png` through
`34-admin-signin.png`); `docs/brand-tokens.md`, described in its own first line as "the frozen
contract" for dealer token names, the `DealerBranding` shape, and the exact `brandingCss` output;
`docs/five-year-strategy.md`, a market and strategy synthesis, not an engineering document.

### `public/`

Self-hosted fonts (`public/fonts/`, three variable woff2 files plus `OFL.txt`), favicons and app
icons, `public/brand/mark.svg` and `mark-outline.svg`, `public/site.webmanifest`, brand logos under
`public/images/brands/`, and product imagery under `public/images/products/`. That last directory
is mixed: 28 generated `.svg` swatches named by hardscape SKU, alongside roughly 17 inherited
`.png` files with lumber names (`2x4-fir.png`, `shingles-gray.png`, `trex-saddle.png`) left over
from the fork ancestry.

### `verification/`

Two artifacts: a 390px-wide screenshot and a captured live-run transcript of the ERP contract
suite. The transcript shows the real staging server answering with dealer name
"Dibbits Landscape Supply" and `brandColor` `#E8A74E`, and the resolved capability map for the demo
account.

---

## 3. Screens and routes

All routing is `react-router` 7, declared in one place, `src/ui/App.tsx:47-68`. Real routes rather
than view state, because the phone hardware back button must return to the board rather than exit
the app, and because the public share link needs somewhere to live (`src/ui/App.tsx:41-46`).

| Route | Element | Notes |
|---|---|---|
| `/` | `Shell` to `BoardPage` | the procurement board |
| `/today` | `Shell` to `TodayPage` | the greeting landing screen |
| `/catalog` | `Shell` to `CatalogPage` | browse state lives in the query string (`?q=&cat=&stock=&sort=`) |
| `/catalog/:sku` | `Shell` to `ProductPage` | a product is its own destination so back returns to the search |
| `/pay` | `Shell` to `PayPage` | AR and payment |
| `/orders/:orderId` | `Shell` to `OrderPage` | scope editing; chrome hidden |
| `/orders/:orderId/quote` | `Shell` to `QuoteStudioPage` | the contractor's proposal builder |
| `/orders/:orderId/tracking` | `Shell` to `OrderTrackingPage` | fulfillment timeline |
| `/projects/:projectId` | `Shell` to `ProjectPage` | the drill-down |
| `/q/:token` | `PublicQuote` to `CustomerQuotePage` | **public, pre-auth, no portal chrome** |
| `*` | `Navigate to="/"` | catch-all |

The admin console is a **separate document**, not a route: `admin.html` mounts
`src/admin/main.tsx` to `src/admin/AdminApp.tsx`. It is reachable at `/admin` only because the host
serves the second built HTML file there.

### Shell chrome

`src/ui/layouts/PortalLayout.tsx` owns the shell. `PortalTab` is exactly four values:
`'today' | 'board' | 'catalog' | 'pay'` (`:32`), with `ALL_NAV` supplying label and icon (`:48-53`).
`navItems()` (`:64-66`) filters `pay` out when the `payments` feature flag is off; hiding a tab is
presentation only, and the actions layer still guards every mutation (`:56-63`). Mobile is a bottom
tab bar with the assistant raised in the centre under the thumb; desktop is a sidebar (`:26-29`).
`hideChrome` is passed for order, project and product pages, which render their own header with a
back control (`src/ui/App.tsx:148`).

### Mobile-first versus desktop

- **Board** is the one screen with two layouts and **exactly one mounted at a time**, selected by
  `useMediaQuery(DESKTOP_QUERY)` where `DESKTOP_QUERY` is `(min-width: 1024px)`
  (`src/ui/hooks/useMediaQuery.ts:24`). The reason is recorded at `:3-10`: keeping both mounted
  doubles the DOM on a phone and registers the mobile and desktop drop targets under the same
  dnd-kit ids simultaneously, making drop resolution ambiguous. `StageBar` is both the mobile
  navigation and, mid-drag, the drop-target row.
- Every other screen is one responsive layout, mobile-first, widening on `lg:`.
- The public quote page forces its own light palette and never renders supplier identity or
  contractor cost.

### Pages and the sheets under them

| Page | Major children |
|---|---|
| `BoardPage` | `board/Board`, `board/OrderCard`, `board/StageBar`, `board/StageTransitionSheet` |
| `OrderPage` | `order/LineItemRow`, `order/AddItemsSheet`, `order/OrderTimeline` |
| `CatalogPage` | `catalog/ProductRow` |
| `ProductPage` | `catalog/ProductRow`, `catalog/AddToPlanSheet` |
| `PayPage` | `pay/PaymentSheet` |
| `QuoteStudioPage` | the contractor-side proposal builder |
| `CustomerQuotePage` | `quote/SignaturePad` |
| `OrderTrackingPage` | `order/OrderTimeline` |
| `ProjectPage` | project drill-down, orders grouped by stage |
| `TodayPage` | greeting header only, at present |

Shell-level sheets, mounted beside `PortalLayout` in `src/ui/App.tsx:194-201`: `ActivitySheet`
(the only notification channel), `DemoDirector` (clock speed, skip controls, demo reset), and
`assistant/AssistantSheet`. `profile/ProfileSheet` hangs off the avatar inside `PortalLayout`.

### The recent `Today` and profile work

From `NOTES-handoff.md`, first section: pass 1 of the `Today` dashboard is merged at `23a8396`
(branch `feature/today-dashboard`). What landed: navigation became `Today`, `Board`, `Catalog`,
`Pay`; the team roster and the appearance toggle moved out of a `More` tab into a profile sheet off
the avatar; `/today` renders a real greeting page. That file names pass 2 as next: a dealer rep
card sourced from a static record in `demo-seed.json`, a delivery and pickup strip over real
scheduled delivery and pickup records only, and a "Needs you" aggregation selector over three existing signals
(quote-desk returns, lead-time conflicts, overdue invoices). Pass 3 is a project by stage matrix
over `buildProjectSummaries` (`src/core/selectors/board.ts:160`).

The code confirms the limited state: `src/ui/pages/TodayPage.tsx` is 33 lines and its own docstring
says so (`:6-12`). `ProfileSheet` (`src/ui/components/profile/ProfileSheet.tsx`) holds the acting
person switcher, the roster with role editing, and `settings/AppearanceCard`.

`NOTES-handoff.md` also records two integration bugs found during that pass and fixed before merge:
the guide script has no Escape handling for the assistant panel and must close it by its own
control, and the e2e role-gating step must leave the chrome-less order page before opening the
profile sheet.

---

## 4. Roles and permissions

**The role model** is `src/core/domain/team.ts`. Four roles (`:14`):
`'owner' | 'pm' | 'ap' | 'field'`. Six capabilities (`:30-36`): `edit-scope`, `move-stage`,
`customer-quote`, `pay`, `confirm-pickup`, `manage-team`.

The grant matrix, verbatim from `src/core/domain/team.ts:38-43`:

| Role | edit-scope | move-stage | customer-quote | pay | confirm-pickup | manage-team |
|---|---|---|---|---|---|---|
| `owner` | yes | yes | yes | yes | yes | yes |
| `pm` | yes | yes | yes | no | yes | no |
| `ap` | no | no | no | yes | no | no |
| `field` | no | no | no | no | yes | no |

`can(role, capability)` at `:45-47`. Labels, one-line role descriptions, and per-capability verbs
used to build refusal sentences are at `:49-72`. `initialsFor` at `:74`.

**Only actions are gated.** Everyone reads everything; the docstring at `:25-29` states the reason,
that hiding information from your own crew creates more phone calls than it prevents.

**The gate** is `requireCapability` in `src/core/actions/team.ts:50-59`. It returns
`Result<TeamMember | undefined>`. On refusal the sentence **names who can do it**: the template
interpolates `nameList(able)`, `CAPABILITY_VERBS[capability]`, the acting person's name and
`ROLE_LABELS[acting.role]` into one sentence of the form "Only Dana or Robin can make payments, you
are in as Marcus (Project manager)". That string reaches a contractor on a rejected action and
reaches the model verbatim as a `tool_result`.

**Gating lives in the actions layer, and nowhere else.** Every mutating action opens with a
`requireCapability` call. Verified by grep across `src/core/actions/`: `moveOrderToStage`
(`orders.ts:48`), `createOrder` (`:112`), `createProject` (`:153`), `updateOrder` (`:202`);
`addCatalogItem` (`scope.ts:59`), `addSpecialItem` (`:123`), `updateItemQtyDetailed` (`:175`),
`removeItem` (`:204`), `updateItemNotes` (`:218`), `applyTemplate` (`:240`); the whole
`customer-quote.ts` surface (`:133, :193, :235, :248, :264, :280, :294, :341`);
`confirmWillCallPickup` (`fulfillment.ts:72`), `requestDeliveryReschedule` (`:129`),
`updateSiteInstructions` (`:205`); `payInvoices` (`payments.ts:126`), `savePaymentMethod` (`:212`),
`setDefaultPaymentMethod` (`:242`), `removePaymentMethod` (`:261`); and the team actions themselves
(`team.ts:68, :98, :122`).

**No team seeded means everything allowed** (`src/core/actions/team.ts:50-53`): if
`activeMember()` is undefined, the gate returns `ok(undefined)`. The stated reason is that the gate
exists once people exist, so an embed of the core without the team feature costs nothing.

**Invariants** enforced in `src/core/actions/team.ts`: the company can never lose its last owner
(`:106-111` on role change, `:129-132` on removal), and the person currently acting cannot be
removed (`:133-135`).

**The switcher is the login story.** `switchActiveMember` (`:150-157`) is deliberately ungated,
with the reason in its docstring (`:145-149`): in this demo there is no login, so the switcher
exists to show role gating, not to enforce it against a hostile user. `activeId` is persisted state
(`src/core/stores/root.ts:31-39`).

**Cross-tab rule for identity.** `team` is in `TAB_LOCAL_KEYS` (`src/core/boot.ts:112`) and is
therefore excluded from `attachCrossTabSync`. The comment at `:103-111` gives the reason: `activeId`
is who you are acting as in **this** window, and adopting it wholesale meant a contractor's tab
silently became accounts payable because someone switched people in another one.

**A restore hazard already fixed**: `restorePersisted` backfills the team store when a save carried
projects, orders and scope but no team (`src/core/boot.ts:317-330`), because an empty team made
`activeMember()` undefined and every capability check pass. The comment states it plainly: an old
save silently turned the permission gate off.

**A second, server-side capability model exists** in the supplier port (see section 9):
`ErpCapabilities` carries the supplier's own nine capabilities uncollapsed, and `mapCapabilities`
(`src/core/supplier/adapters/erp-map.ts:173-183`) collapses them onto HH Pro's six as a
conjunction. That mapping is currently constructed but not consumed by any guard.

---

## 5. Entities

All money is integer cents (`type Cents = number`, `src/core/lib/money.ts:7`). All timestamps are
ISO strings (`type IsoDateTime = string`, `src/core/lib/time.ts:9`) so state stays
JSON-serializable. All ids are prefixed strings (`type EntityId = string`,
`src/core/lib/ids.ts:9`); `newId(prefix)` produces `prefix_` plus six random characters, and
`newShareToken()` produces 24 characters from `crypto.getRandomValues` for the one public surface.

### `src/core/domain/project.ts`

- `OrderStage = 'plan' | 'quote' | 'order' | 'invoice'`; `ORDER_STAGES`, `STAGE_LABELS`.
- `Fulfillment = 'delivery' | 'willcall'`.
- `Address`: `id`, optional `label`, `line1`, optional `line2`, `city`, `state`, `zip`.
- **`Project`** (store: `projectsStore`): `id`, `accountId`, `name`, optional `clientName` (the
  contractor's own customer), optional `address`, `createdAt`, `updatedAt`, optional `archivedAt`.
  **Has no stage.**
- **`Order`** (store: `ordersStore`) is the board card: `id`, `projectId`, `name`, `stage`,
  `fulfillment`, optional `requestedDate`, optional `deliveryAddressId`, optional
  `siteInstructions`, optional `poNumber`, optional `quoteId` / `salesOrderId` / `invoiceId` (set
  only when the supplier-side artifact exists), `createdAt`, `updatedAt`, `sortOrder` (position
  within its column).
- `PriceSource = 'erp' | 'quoted' | 'estimate' | 'unpriced'` (`:93`).
- **`ScopeItem`** (store: `scopeStore`): `id`, `orderId`, `kind: 'catalog' | 'special'`, optional
  `productId`, **`snapshot: ItemSnapshot`** (frozen copy of the product at the moment it was added,
  `:111-116`), `qty`, `uom`, optional `notes`, optional `unitPrice`, `priceSource`, optional
  `listPrice`, optional `priceExpiresAt`, `addedBy: 'user' | 'assistant'`, `addedAt`, `sortOrder`.
- `ItemSnapshot`: `sku`, `name`, optional `imageUrl`, optional `brandName`, optional
  `leadTimeDays`.
- `SPECIAL_ORDER_PREFIX = 'SO-'` and `isSpecialOrderSku` (`:149-153`).
- Price-expiry predicates: `isPriceExpired`, `isPriced(item, now?)`, `needsQuoteDesk(items, now?)`,
  `itemExtended` (`:155-182`). A lapsed quoted line counts as needing the desk again (`:171-177`).

### `src/core/domain/catalog.ts`

- **`Uom`** union of ten codes (`:19-29`): `EA`, `SF`, `LF`, `TON`, `CY`, `PLT`, `BG`, `BX`, `RL`,
  `BD`. The comment at `:14-18` states the vertical's core difference: hardscape prices by area and
  by weight.
- `Brand`: `id`, `name`, optional `description`, `logoUrl`, `website`.
- `Category`: `id`, `name`, `slug`, optional `parentId`.
- `Location`: `id`, `name`, `kind: 'yard' | 'warehouse'`.
- `StockLevel`: `locationId`, `onHand`, `onOrder`.
- `SpecPair`: `label`, `value`.
- **`Product`** (store: `catalogStore.products`): `id`, `sku`, `name`, `description`, `categoryId`,
  `brandId`, optional `imageUrl`, `baseUom`, `isActive`, `listPrice` (cents), optional `msrp`,
  `tags[]`, `relatedSkus[]`, `specs[]`, `leadTimeDays`, `stock: StockLevel[]`, optional
  `coverageSqFt` / `coverageLf`, optional `specClass` (substitutability group), and
  `presentation: 'selection' | 'commodity'`.
- **`PriceQuote`** (the ERP's answer, `:129-136`): `sku`, `unitPrice`, `listPrice`, `qty`, optional
  `nextBreak`. `VolumeBreak` is `{ minQty, unitPrice }` (`:138-142`), always absolute by the time it
  reaches a caller.
- Helpers: `totalOnHand`, `isStocked`, `savingsPerUnit`, `discountPercent`.

### `src/core/domain/supplier.ts`

- `QuoteStatus = 'submitted' | 'in-review' | 'priced' | 'expired' | 'withdrawn'`.
- **`Quote`** (store: `quotesStore`): `id`, `orderId`, `number` (`Q-1001` form), `status`,
  `submittedAt`, optional `pricedAt`, optional `expiresAt`, optional `deskNote`, and
  `linePrices: { scopeItemId, unitPrice, leadTimeDays }[]`.
- `SalesOrderStatus` (`:31-39`), eight values: `submitted`, `confirmed`, `picking`,
  `ready-willcall`, `out-for-delivery`, `delivered`, `invoiced`, `cancelled`. `SALES_ORDER_FLOW`
  (`:41-48`) is the six-step happy path; `ready-willcall` is substituted for `out-for-delivery`
  rather than defining a second flow constant.
- `TrackingEvent`: `at`, `status`, `note`.
- **`SalesOrder`** (store: `salesOrdersStore`): `id`, `orderId`, `number` (`SO-5001` form),
  `status`, `fulfillment`, `submittedAt`, optional `promisedDate`, optional `deliveredAt`,
  `subtotal`, `tracking[]`.
- **`Invoice`** (store: `invoicesStore`): `id`, `number` (`INV-9001` form), `accountId`, optional
  `orderId`, optional `salesOrderId`, `origin: 'portal' | 'counter'`, `issuedAt`, `dueAt`,
  `subtotal`, `balance`, `description`. `origin: 'counter'` is the offline sale that never went
  through the portal and has no `orderId`.
- `isOpen(invoice)` is `balance > 0`.

### `src/core/domain/customer-quote.ts`

- `CustomerQuoteStatus`: `draft`, `sent`, `viewed`, `changes-requested`, `accepted`, `declined`,
  `expired`.
- `CustomerQuoteLine`: `id`, `scopeItemId`, `name`, `qty`, `uom`, **`unitCost`** (what the
  contractor pays, never shown to the customer), `presentation`, and for selections only:
  `productId`, `sku`, `imageUrl`, `brandName`, `description`, `specs[]`, optional `note`.
- `LaborLine`: `id`, `description`, `rateType: 'hourly' | 'flat'`, `rate`, optional `hours`.
- `OverheadLine`: `id`, `label`, `amountType: 'flat' | 'percent'`, `value`.
- **`QuoteAcceptance`**: `signedName` (typed legal name), `signatureDataUrl` (drawn mark),
  `acceptedAt`, `consentText` (the exact sentence shown, captured verbatim), `acceptedTotal`
  (frozen). Kept as a record rather than a boolean because acceptance is a claim the contractor may
  have to stand behind.
- `ChangeRequest`: `id`, `message`, `createdAt`, optional `resolvedAt`.
- **`CustomerQuote`** (store: `customerQuotesStore`): `id`, `orderId`, `number` (`CQ-1001` form),
  `status`, `markupPercent`, `laborLines[]`, `overheadLines[]`, `hideLinePrices`, **`lines[]`**
  (snapshot, so a sent proposal cannot change under the customer), `contractor: ContractorBranding`,
  `customer: { name, email?, phone? }`, optional `shareToken`, `validUntil`, `createdAt`, optional
  `sentAt`, optional `firstViewedAt`, `viewCount`, optional `respondedAt`, `changeRequests[]`,
  optional `acceptance`.
- `QuoteTotals` and `computeQuoteTotals`: materials cost, markup, marked-up materials, labor,
  overhead (percentage overheads are taken on marked-up materials plus labor), grand, margin,
  margin percent.
- `maxValidUntil`, `isQuoteExpired`, `canRespond`, and `CONSENT_TEXT`, the verbatim e-signature
  sentence.

### `src/core/domain/payment.ts`

- `PaymentMethodKind = 'ach' | 'card'`. **`PaymentMethod`** (store: `paymentMethodsStore`): `id`,
  `accountId`, `kind`, `label` (display only, never a real PAN), `isDefault`, optional `expiry`.
- `PaymentStatus = 'processing' | 'succeeded' | 'failed'`. **`Payment`** (store: `paymentsStore`):
  `id`, `accountId`, `methodId`, `allocations: { invoiceId, amount }[]`, `amount`, `status`,
  `initiatedAt`, optional `settledAt`, optional `confirmationNumber`, optional `failureReason`.
- `CARD_FEE_PERCENT = 2.9`; `cardFeePercent()` reads the dealer config; `feeFor(method, amount)`
  charges it on cards only.
- `AgingBucket = 'current' | '1-30' | '31-60' | '60+'`, with labels.
- **`daysPastDue(dueAt, now)` is the single definition of late** (`:80-85`); `agingBucket` and
  `isOverdue` both derive from it because they used to disagree on the same screen.

### `src/core/domain/account.ts`

- **`Account`** (store: `sessionStore.account`): `id`, `name`, `accountNumber`,
  `type: 'cash' | 'charge'`, `paymentTermsCode: 'COD' | 'NET15' | 'NET30'`, `pricingTierId`
  (opaque to the contractor, feeds the pricing engine), optional `creditLimit`, `addresses[]`,
  `branding: ContractorBranding`.
- `ContractorBranding`: `companyName`, optional `contactName`, `phone`, `email`, `licenseNumber`,
  `logoDataUrl`, `accentColor`.
- `User` (store: `sessionStore.user`): `id`, `accountId`, `name`, `email`,
  `role: 'owner' | 'estimator' | 'field'`, `initials`. Note this role union is **separate from and
  unrelated to** `TeamRole`; nothing gates on it.
- `termsDays(code)` maps the terms code to a number.

### `src/core/domain/team.ts`

`TeamMember` (store: `teamStore.members`): `id`, `name`, `role`, optional `email`, `initials`,
`createdAt`. Plus the capability matrix described in section 4.

### `src/core/domain/activity.ts`

`Actor = 'user' | 'assistant' | 'system'`. `ActivityEntry`: `id`, `at`, `actor`, `kind`, `message`
(written for a contractor, not for a log), optional `orderId`, optional `projectId`.
`ActivityState` (store: `activityStore`): `entries[]` plus `readWatermark`. Bounded at
`MAX_ACTIVITY_ENTRIES = 400`. `unreadCount` counts only `system` entries after the watermark.

### `src/core/domain/units.ts`

One table, `UNITS: Record<Uom, UnitCopy>` (`:27-58`), where `UnitCopy` is `{ short, singular,
plural, sold }`. Exports `unitShort`, `perUnit` (the `/sq ft` suffix), `formatQty`, `soldBy`, and
`qtyStep` (`:87-89`), which steps by ten for `SF` and `LF` and by one otherwise. The test enumerates
the whole union so a missing entry is caught rather than rendering `/undefined`.

### `src/core/domain/totals.ts`

`OrderTotals`: `subtotal` (priced lines only), `listSubtotal`, `savings`, `itemCount`,
`unpricedCount`, `awaitingQuoteCount`. Plus `orderTotals`, `isFullyPriced`, `hasKnownSubtotal`
(`:68-70`, so an order with every line awaiting the desk renders an em-free placeholder rather than
a confident zero), and `sumOrderTotals`.

### `src/core/domain/config.ts`

`FeatureKey = 'assistant' | 'customerQuotes' | 'payments' | 'willCall'`. `DealerBranding` (nine
fields, `brandColor` required and the rest optional). `AssistantConfig`: `model`, `maxTokens`,
`dailyRequestCap`, `houseRules`, and a server-reported `hasCredential`. `SupplierStages`:
`{ erpReads: boolean }`. `SupplierConfig`: `termsDays`, `cardFeePercent`, `mode: 'sim' | 'erp'`,
optional `baseUrl`, `stages`. `DealerConfig` is the four together. Fully detailed in section 10.

### Store map, at a glance

`src/core/stores/root.ts` declares thirteen stores. Twelve are persisted (`PERSISTED_STORES`,
`:111-125`): `projects`, `orders`, `scope`, `quotes`, `salesOrders`, `invoices`, `customerQuotes`,
`paymentMethods`, `payments`, `activity`, `team`, `sim`. `catalogStore` and `sessionStore` are
**not** persisted: the catalog is derived from the seed every boot, and persisting it would only
bloat storage (`:99-104`).

---

## 6. Stage machine and actions

### `src/core/domain/stage.ts`

Pure, no store access, no side effects. `StageContext` (`:32-45`) carries `items`, `now` (sim time,
required because a quoted price can lapse), and optional `supplierStatus`.

`StageEffect` (`:51-55`) is one of four: `submit-to-quote-desk`, `withdraw-from-quote-desk`,
`create-sales-order`, `cancel-sales-order`. `StageMove` is `{ from, to, effects }`.

`canMoveToStage(order, to, ctx): Result<StageMove>` (`:83-187`), in evaluation order:

1. Same stage: `Already in ${to}.`
2. Any forward move on an empty order is refused.
3. **To `quote`**: only from `plan`; effect `submit-to-quote-desk`.
4. **To `order`**:
   - refused from `invoice`;
   - `needsQuoteDesk(items, now)` non-empty refuses, and the sentence distinguishes "never priced"
     from "the dealer's price ran out" by testing whether all offending lines are expired
     (`:118-127`);
   - any remaining line failing `isPriced(item, now)` refuses;
   - no `requestedDate` refuses;
   - a `requestedDate` earlier than the current sim date (`startOfDay` comparison) refuses, and the sentence says
     pickup or delivery according to `fulfillment`;
   - `delivery` with no `deliveryAddressId` refuses;
   - on success the effects are `create-sales-order`, preceded by `withdraw-from-quote-desk` when
     coming from `quote`.
5. **To `invoice`**: always refused. `Orders move to Invoice once the supplier delivers and bills
   them.` Invoicing is the supplier's move.
6. **Back to `plan`**: from `quote`, effect `withdraw-from-quote-desk`; from `order`, refused when
   `ctx.supplierStatus` is in `PAST_CANCEL = ['out-for-delivery', 'delivered', 'invoiced']`
   (`:48`), otherwise effect `cancel-sales-order`; from `invoice`, refused.

`systemAdvanceToInvoice(order)` (`:193-198`) is kept separate so a user-driven drag can never reach
it. `allowedTargets(order, ctx)` (`:201-208`) enumerates legal targets and drives drop-target
highlighting.

Guard sentences conjugate for number (`pluralItems`, `verb`, `:70-77`), because they are
contractor-facing copy.

### The `Result` convention

`src/core/lib/result.ts`: `Result<T> = { ok: true; value: T } | { ok: false; error: string }`, with
`ok`, `err`, `isOk`, `mapResult`, `allResults`. Actions return `Result` rather than throwing so a
guard's reason can be shown on a rejected board drop **and** handed to the model verbatim as a
`tool_result` (`:56-61`). Exceptions would lose that symmetry.

### `src/core/actions/` , one line each

| File | Mutation it owns |
|---|---|
| `orders.ts` | stage moves (`moveOrderToStage`, which runs the stage machine then dispatches its effects into the sim), `systemInvoiceOrder`, `createOrder`, `createProject`, `updateOrder`, `reorderInStage` |
| `scope.ts` | line items: `addCatalogItem`, `addSpecialItem`, `updateItemQty` plus `updateItemQtyDetailed`, `removeItem`, `updateItemNotes`, `applyTemplate`, `repriceOrder` |
| `catalog.ts` | `addProductToPlan`: chooses WHERE a catalog line lands (an existing plan, or a new one) and delegates the actual insert to `addCatalogItem` |
| `customer-quote.ts` | the contractor's proposal: `buildCustomerQuote`, `updateCustomerQuote`, labor and overhead line add/remove, `sendCustomerQuote`, `reviseCustomerQuote`, `recordQuoteView`, `acceptCustomerQuote`, `requestQuoteChanges`, plus the reads `quoteForOrder`, `quoteByToken`, `supplierPriceHorizon` |
| `fulfillment.ts` | post-placement logistics: `confirmWillCallPickup`, `requestDeliveryReschedule`, `updateSiteInstructions` |
| `payments.ts` | money: `quotePayment` (pure, shared with the UI), `payInvoices`, `savePaymentMethod`, `setDefaultPaymentMethod`, `removePaymentMethod`, plus the reads `paymentMethods`, `defaultPaymentMethod` |
| `team.ts` | the roster and **the gate**: `requireCapability`, `teamMembers`, `activeMember`, `addTeamMember`, `setMemberRole`, `removeTeamMember`, `switchActiveMember` |

Behaviours worth carrying to any rebuild:

- `moveOrderToStage` resolves `supplierStatus` from `salesOrdersStore` before calling the machine
  (`orders.ts:53-59`), then applies the stage change **before** running effects so the sim sees the
  new state (`:64-84`).
- `assertEditable` (`scope.ts:36-46`) locks scope once the order is at `quote`, `order` or
  `invoice`, with a different sentence for each.
- Adding a SKU already on the order bumps quantity rather than creating a second line; changing
  quantity re-quotes through `quoteForAccount` and reports a crossed volume break as
  `priceChanged: { from, to }` (`scope.ts:169-201`).
- `addProductToPlan` creates the order first when starting a new plan, and **rolls it back out** if
  the line then refuses (`catalog.ts:78-91`), so the board never shows a card for work that does not
  exist.
- `payInvoices` returns `ok()` on a decline: the action succeeded, the payment failed, and the
  `Payment` record is data the UI must render.
- `quotePayment` deduplicates invoice ids (`payments.ts:85`) and is the single source for both the
  displayed and the charged amount.
- `confirmWillCallPickup` cancels the sim's pending auto-collect task, or it fires later and
  appends a duplicate delivery and invoice (`fulfillment.ts:63-70`).
- `supplierPriceHorizon(orderId)` (`customer-quote.ts:68-75`) returns the earliest
  `priceExpiresAt` across the order's lines and caps how long the contractor's own offer may stand.
- A customer quote refuses unpriced lines with a sentence that explains the exposure
  (`customer-quote.ts:80-93`).

---

## 7. Pricing logic, in detail

This section reproduces shapes exactly, for field-by-field comparison against the ERP.

### `src/core/sim/pricing.ts`

The file lives in `sim/` and not in `domain/` on purpose (`:5-16`): tiers, category rules and
contract prices are the supplier's internal commercial machinery, the contractor sees only "my
price", and when a real ERP connects everything in this file is replaced by an API call returning
the same `PriceQuote` with nothing in `domain/` changing.

```ts
export interface PricingTier {
  id: EntityId;
  name: string;
  /** Baseline discount off list applied to everything. */
  percentOffList: number;
}

export type PricingRule =
  | { kind: 'category'; tierId?: EntityId; accountId?: EntityId;
      categoryId: EntityId; percentOffList: number }
  | { kind: 'contract'; accountId: EntityId; sku: string; unitPrice: Cents }
  | { kind: 'volume'; sku?: EntityId; categoryId?: EntityId; breaks: VolumeBreakRule[] };

export type VolumeBreakRule =
  | { minQty: number; unitPrice: Cents }
  | { minQty: number; extraPercentOff: number };

export interface PricingInput { accountId: EntityId; tierId: EntityId; }

export type CategoryParents = ReadonlyMap<EntityId, EntityId | undefined>;

export interface PricingEngine {
  quote(product: Product, qty: number, account: PricingInput): PriceQuote;
  quoteAll(products: readonly Product[], qty: number, account: PricingInput): PriceQuote[];
}
```

`createPricingEngine(tiers, rules, categoryParents = new Map())` (`:94-98`).
`categoryParentsFrom(categories)` (`:76-78`) builds the parent map; the docstring at `:67-75` says
it must always be passed, because matching category ids exactly would silently drop the discount on
every product sitting in a subcategory.

**The precedence order, reproduced verbatim from the comment at `:89-93`:**

> Precedence, most specific wins: contract price > account category rule > tier category rule >
> tier baseline > list. Volume breaks are evaluated last and only apply if they beat whatever the
> rules produced.

The implementation detail behind each step (`baseUnitPrice`, `:136-181`):

1. **Contract price**: first rule with `kind === 'contract'`, matching `accountId` and exact `sku`.
   Returns the locked `unitPrice`, immune to list changes.
2. **Category rules**, cascading up the tree. `categoryChain(categoryId)` (`:106-116`) walks the
   product's category and every ancestor, nearest first, guarding against a cycle with a `seen` set.
   `matchesCategory` returns the depth (0 is the product's own category). For each `category` rule:
   a rule bound to this `accountId` competes in the account bucket, a rule bound to
   `tierId === account.tierId` with `accountId === undefined` competes in the tier bucket. Within a
   bucket, **the nearest category wins, and at equal depth the better discount wins** (`:153-170`).
   Then **an account-specific rule outranks a tier rule regardless of depth** (`:173-176`), because
   it was negotiated for this contractor.
3. **Tier baseline**: `applyPercentOff(listPrice, tier.percentOffList)`; if the tier id is unknown,
   the product's `listPrice` stands.

`applyPercentOff(listPrice, percent)` is `Math.round(listPrice * (1 - percent / 100))` (`:85-87`).

**Volume breaks** (`volumeBreaksFor`, `:187-221`), resolved to absolute prices for this product
before they leave the function:

- A rule matches by `sku` exactly (`bySku`) or by category with ancestor cascade (`byCategory`).
- **The no-stacking rule** (`:201-205`): when the product has a contract price, a rule that is not
  `bySku` is skipped entirely, so a category-wide volume discount cannot stack on a negotiated
  price. A break written for that same sku **does** apply, because that is a deliberate volume tier
  on the contracted item.
- **Absolute versus relative** (`:207-217`): a `{ minQty, unitPrice }` break is only pushed when
  `bySku` is true. An absolute break attached to a category is **ignored, not trusted** (`:209-210`),
  because a category spans a cheap and an expensive product and a category-wide absolute price
  produces visibly fake numbers. A `{ minQty, extraPercentOff }` break is resolved against **that
  product's own base price** via `applyPercentOff(base, extraPercentOff)`.
- Breaks are sorted ascending by `minQty`.

`quote(product, qty, account)` (`:223-244`): compute `base`, resolve breaks, take the best break
the quantity already qualifies for (`qty >= minQty && unitPrice < unitPrice`), then find
`nextBreak` as the first break with **both** a higher `minQty` and a better price than what is
currently being paid. Anything else is noise (`:233-235`). The returned object is exactly
`{ sku, unitPrice, listPrice: product.listPrice, qty, ...(nextBreak ? { nextBreak } : {}) }`.

### `src/core/selectors/pricing.ts`: the single binding

Forty-six lines, and the docstring (`:7-27`) is the record of why it exists: `sim/pricing.ts`
computed prices, but "engine plus which account" was rebuilt at every call site.
`actions/scope.ts` froze the demo account in a module constant and `OrderPage` hand-wrote
`tierId: 'tier_pro'`, which was already wrong for any contractor not on the Pro tier.

```ts
export function accountPricing(): PricingInput {
  const account = sessionStore.get().account ?? DEMO_ACCOUNT;
  return { accountId: account.id, tierId: account.pricingTierId };
}

export function quoteForAccount(product: Product, qty: number): PriceQuote {
  return getContext().pricing.quote(product, qty, accountPricing());
}

export type QuoteFor = (product: Product, qty: number) => PriceQuote;
export const accountQuoteFor: QuoteFor = quoteForAccount;
```

`QuoteFor` is the injectable form: selectors take pricing as a parameter so they stay pure and
testable against a hand-built engine, and the app passes `accountQuoteFor`. Catalog browsing, the
product page, the add-to-plan sheet, the order page and the assistant's `search_catalog` all quote
through here. The comment at `:25-27` names this as the seam a real ERP replaces: account
resolution stays, the engine behind it changes.

### The demo's actual rules

`src/core/data/account-seed.ts`:

- Tiers (`:32-37`): `tier_standard` 0 percent off list, `tier_preferred` 6, `tier_pro` 12. The demo
  account is on `tier_pro`.
- Rules (`:44-93`), in order: an **account** category rule of 18 percent off hardscape written
  against the parent category so it cascades to pavers, walls, steps and porcelain; a **tier**
  category rule of 15 percent off aggregates for everyone on Pro; a **contract** price on
  `PVR-ACK-COMBO60` at `toCents(6.35)`; a **relative** volume rule on the pavers category with
  breaks at `minQty` 600 and 1500 (6 and 11 extra percent off); and an **absolute** volume rule
  named to the single sku `AGG-HPB-BULK` with breaks at `minQty` 20 and 60.
- The comment at `:68-74` restates why the paver breaks must be relative: that category spans a
  roughly `$4.62` per square foot economy paver and a `$17.67` large-format slab.
- `DEMO_ACCOUNT` (`:95-111`): `type: 'charge'`, `paymentTermsCode: 'NET30'`,
  `pricingTierId: TIER_PRO_ID`, `creditLimit: toCents(75_000)`.
- `SEED_PAYMENT_METHODS` (`:127-151`): `pm_ach` (default), `pm_visa`, and `pm_bad`, the card that
  always declines.

### Price expiry

- `ScopeItem.priceExpiresAt` is set by the quote desk when it prices special lines
  (`src/core/sim/quote-desk.ts:101-112`), to `addDays(now, SIM.quoteValidDays)`.
- `isPriced(item, now)` returns false for a lapsed line; `needsQuoteDesk(items, now)` counts a
  lapsed quoted line as needing the desk again (`src/core/domain/project.ts:156-177`).
- **An expired price re-blocks an order that was previously placeable**, because
  `canMoveToStage` consults both through `StageContext.now`.
- The desk re-prices expired lines: `priceSource === 'quoted'` alone is not enough, the handler
  also tests `isPriceExpired` (`src/core/sim/quote-desk.ts:88-90`), or the remedy loops forever.
- The sim schedules a `quote.expire` task at the expiry instant
  (`src/core/sim/quote-desk.ts:124`); the handler skips an order already at `order` or `invoice`,
  since a placed price is locked into the sales order (`:145-147`).
- `supplierPriceHorizon(orderId)` (`src/core/actions/customer-quote.ts:68-75`) is the earliest such
  expiry, and it caps `CustomerQuote.validUntil` on every update via `maxValidUntil`
  (`src/core/domain/customer-quote.ts:180-190`).

### The two tests that defend the single binding

Named in `CLAUDE.md` and located at `src/core/selectors/__tests__/catalog.test.ts`:

1. **A behavioural test** comparing every catalog row against the engine, for all 45 products at
   four quantities.
2. **A grep over `src/`** for discount arithmetic outside `sim/pricing.ts`. Both are needed: a
   "list minus the tier discount" shortcut agrees on 40 of the 45 products and is wrong exactly
   where the commercial relationship lives.

The engine itself has its own suite at `src/core/sim/__tests__/pricing.test.ts`.

---

## 8. The simulator

`src/core/sim/` plays the dealer's ERP. It is the **only** thing that writes supplier-side facts:
quotes, sales orders, invoices and tracking events.

### The façade

`src/core/sim/types.ts` declares `SimStores` (`:18-31`): five stores (`orders`, `scope`, `quotes`,
`salesOrders`, `invoices`) plus six mutators (`itemsForOrder`, `patchScopeItem`, `patchQuote`,
`putSalesOrder`, `putInvoice`, `advanceOrderToInvoice`). Handlers get this narrow object rather than
the stores directly, because the sim must not reach for contractor actions whose guards are meant
for a human (`:9-17`). `advanceOrderToInvoice` is deliberately the only stage change exposed.
`SimContext` adds `clock`, `scheduler`, `seed`, `accountId`, `termsDays` and `log`.

### The clock

`src/core/sim/clock.ts`. `ClockAnchor` is `{ anchorSim, anchorReal, speed }` and
`now() = anchorSim + (Date.now() - anchorReal) * speed` (`:41-43`). `reanchor` is called on every
`setSpeed` and `advance` so a speed change never retroactively moves history (`:45-52`).
`restore(anchor)` exists so another tab's persisted anchor can be adopted. Nothing in `src/core`
reads the wall clock directly.

### The scheduler

`src/core/sim/scheduler.ts`. `SimTask` is `{ id, dueAt (sim epoch ms), type, payload }`. The queue
lives in `simStore.tasks` and is therefore persisted, which is the whole point: close the tab
mid-quote and the work continues, because on boot everything already due runs immediately in `dueAt`
order (`:4-11`).

`pump()` (`:58-98`) is bounded to 50 passes so a handler that schedules something already due cannot
spin. Within a pass, due tasks are sorted strictly by `dueAt` for deterministic catch-up replay; a
task cancelled by an earlier handler in the same pass is skipped by re-reading the live list
(`:73-76`); each task is removed **before** its handler runs so a throwing handler costs only its
own task (`:78-81`); an unknown task type is logged and dropped. `start()` catches up then ticks
every 250 real milliseconds.

### The quote desk

`src/core/sim/quote-desk.ts`. Two handlers.

- `quote.review`: moves `submitted` to `in-review`, logs a system activity entry, and schedules
  `quote.price` at **`task.dueAt` plus the pricing delay**, never at `clock.now()`
  (`:66-69`), so a contractor away for a stretch returns to a priced quote rather than a desk that
  restarted its clock when they walked in.
- `quote.price`: prices every `special` line that is unpriced or lapsed, writes `unitPrice`,
  `priceSource: 'quoted'`, `priceExpiresAt` and a `leadTimeDays` into the snapshot, stamps the quote
  `priced` with a `deskNote` picked from four canned lines, and schedules `quote.expire`.
- `quote.expire`: flips a still-`priced` quote to `expired` and logs, unless the order has already
  moved to `order` or `invoice`.

Special-order prices are anchored on what the description sounds like: a regex ladder over the item
name picks an anchor (door or window or slider, rail, beam, cabinet, bracket, with a default), then
`rngFor(seed, 'special:' + itemId).jitter(anchor, SIM.specialOrderPriceJitter)` varies it
(`:21-34`). Lead time is `rng.int(SIM.specialOrderLeadDays.min, SIM.specialOrderLeadDays.max)`.

### The order lifecycle

`src/core/sim/order-lifecycle.ts`. The chain is `order.confirm`, `order.pick`, `order.staged`, then
either `order.dispatch` then `order.deliver`, or (will-call) straight to `order.deliver`, and
finally `order.invoice`.

- **Every chained step anchors to `task.dueAt`, never `clock.now()`** (`:65-69`). The comment states
  the failure it prevents: after time offline the two differ, and now-anchoring advanced a long
  fulfillment exactly one step per reload.
- **Nothing dispatches before the requested date**: `dispatchAt = Math.max(readyAt, promised)` where
  `promised` is `startOfDay(so.promisedDate)` (`:116-120`).
- **Will-call parks at `ready-willcall`** and schedules the assumed collection at
  `Math.max(task.dueAt + SIM.willCallAutoCollect, promisedPickup)` (`:105-113`), so the sim never
  "collects" goods before the pickup date the contractor asked for.
- `order.invoice` mints `INV-` plus a counter, sets `dueAt = addDays(issuedAt, ctx.termsDays)`,
  appends an `invoiced` tracking event, and **calls `advanceOrderToInvoice`**, which is the only
  stage change the simulator may make (`:181-184`), mirroring the rule that a contractor cannot drag
  a card into Invoice.
- `createSalesOrder` (`:193-217`) numbers `SO-` plus 5000 plus the count, honours `requestedDate` as
  `promisedDate` or promises a short default, and seeds the first tracking event.

### The public surface

`src/core/sim/index.ts` publishes exactly four supplier writes plus a control object:

```ts
export interface Sim {
  scheduler: Scheduler;
  submitToQuoteDesk(orderId: string): void;
  withdrawFromQuoteDesk(orderId: string): void;
  createOrderWithSupplier(order: Order): void;
  cancelWithSupplier(orderId: string): void;
  control: SimControl;
}

export interface SimControl {
  setSpeed(multiplier: number): void;
  speed(): number;
  skipToNextEvent(): boolean;
  advanceDays(days: number): void;
  pendingCount(): number;
  nextDueAt(): number | null;
}
```

`setSpeed`, `skipToNextEvent` and `advanceDays` each persist the **full** clock anchor
synchronously (`:190, :199, :206`); the comment at `:185-189` records the bug that forced it, a torn
pair of old anchor and new speed replayed at the highest multiplier.

`ctx.termsDays` prefers the dealer's configured terms with `??`, not `||`, because a configured zero
means due on receipt and `||` silently substituted the account default (`:84-87`).

Every delay constant is in one file, `src/core/sim/config.ts`: `quoteReviewDelay`,
`quotePricingDelay`, `quoteValidDays`, `orderConfirmDelay`, `orderPickDelay`, `orderStagedDelay`,
`deliveryRunDuration`, `invoiceDelay`, `willCallAutoCollect`, `specialOrderLeadDays`,
`specialOrderPriceJitter`, `tickMs`. `CLOCK_SPEEDS` are `[1, 60, 600, 3600]` with labels.

### What a real ERP integration replaces

Stated in the code in three separate places, and consistent:

- `src/core/sim/pricing.ts:14-16`: the pricing file is replaced by an API call returning the same
  `PriceQuote`; nothing in `domain/` changes.
- `src/core/data/catalog-seed.ts:31-33`: when a real ERP connects, the whole seed file is replaced
  by a sync.
- `src/core/supplier/index.ts:8-19`: the switch is one line in `boot`, two implementations, no fork,
  and there is deliberately no `if (mode === 'sim')` anywhere above that function.

**The shape is what is replaced, not the domain.** The sim is also a permanent, supported mode, not
scaffolding: it is how the demo runs, how the guide capture works against a production build, how
the e2e suite drives the product, and how a dealer evaluates before their ERP is connected
(`src/core/supplier/index.ts:10-14`).

---

## 9. The supplier layer

`src/core/supplier/` is newer than `CLAUDE.md`, which does not mention it at all. It is 1740 lines
across seven source files plus five test suites and two fixture directories. It was added by
DIB-480 and pointed at real staging by DIB-501 (`git log --oneline`: `2caa770`, `09a4e7a`).

### `src/core/supplier/port.ts` (310 lines)

The seam. Its opening comment says the interface was **discovered, not designed** (`:11-25`): the
four writes are `Sim`'s four methods verbatim, and the reads come from the ERP's own endpoint map.

Types, reproduced:

- `SupplierMode = 'sim' | 'erp'`.
- `SupplierPage<T>` is `{ items, total, limit, offset }`, preserving the ERP's list envelope rather
  than flattening it.
- `PageInput` is `{ limit?, offset?, status? }`, with `status` passed through verbatim.
- **`ErpCapabilities`** (`:54-64`), the supplier's own nine: `manageUsers`, `viewBilling`,
  `payInvoices`, `managePaymentMethods`, `submitRfq`, `createOrders`, `viewOrdersDeliveries`,
  `editDeliveryInstructions`, `manageFiles`. The comment (`:44-53`) explains why both grains are
  kept: HH Pro's six are coarser in two places that matter, `move-stage` spans `submitRfq` and
  `createOrders`, and `pay` spans `payInvoices` and `managePaymentMethods`.
- **`SupplierIdentity`**: `userId`, `accountId`, `name`, `email`, `role` (the supplier's own role
  code, never re-expressed as a `TeamRole`), `status`, `capabilities: Record<Capability, boolean>`,
  `erpCapabilities`.
- **`SupplierSession`**: `identity`, `branding`, `expiresAt`, `sessionExpiresAt`. There is
  **deliberately no `token` field** (`:84-94`): token custody belongs to the adapter alone, because
  HH Pro persists twelve stores to browser storage and stashes a corrupt-save backup, so a bearer
  token readable above the port eventually lands in a forensic stash or rides a cross-tab storage
  event. `expiresAt` is `null` after login because the ERP's login response does not carry it; only
  refresh does. That gap is pinned by a contract test so it cannot be quietly "fixed" client-side by
  decoding the JWT.
- **`SupplierReads`** (`:163-175`): `me`, `branding`, `searchCatalog`, `dashboard`,
  `billingSummary`, `listOrders`, `getOrder`, `listInvoices`, `getInvoice`, `listQuotes`,
  `getQuote`. All return `Promise<Result<T>>`.
- **`SupplierWrites`** (`:184-189`): the four sim writes, wrapped in `Promise<Result<T>>`.
- `SupplierAuth`: `login`, `refresh`, `signOut`, `session`.
- **`SupplierPort`**: `{ mode, reads, auth: SupplierAuth | null, writes: SupplierWrites | null }`.
  `auth` is null for the sim, which has no login at all. `writes` is null for the ERP adapter, and
  the null **is** the enforcement.
- `SUPPLIER_WRITE_METHODS` (`:226-252`) is a deny-list of twenty names, covering the four that exist now plus
  every mutating capability the endpoint map names for a later stage, used by a structural test.
- `TokenStore` (`:264-268`) plus `memoryTokenStore()`: injected because core is DOM-free by default
  and the correct browser home is per-tab session storage, not persisted and not cross-tab synced.
- `SUPPLIER_ERRORS` (`:292-310`): nine contractor-readable refusal sentences (`sessionGone`,
  `badCredentials`, `forbidden`, `notFound`, `unavailable`, `aborted`, `malformed`,
  `notConfigured`, `readOnly`). `sessionGone` is never softened into a retry, because a portal that
  quietly serves fabricated prices when its ERP session lapses is the worst failure this integration
  can have.

### `src/core/supplier/index.ts` (104 lines)

`usesErpReads(config)` is `config.mode === 'erp' && config.stages.erpReads === true` (`:46-48`):
both required, and the flag is the narrower of the two, so "the flag is absent" and "the flag is
off" are the same state. `createSupplier(config, deps)` builds the ERP adapter when that is true
(throwing loudly if a base URL or a `fetch` is somehow missing, because quietly running the
simulator while a contractor believes they are looking at their real account is the failure to
avoid), and otherwise wraps the `Sim` that `boot` already built. The `Sim` is **handed in, never
constructed here**: a second simulator would put a second scheduler on one persisted queue.

### `src/core/supplier/adapters/erp-client.ts` (210 lines)

The HTTP seam only. `FetchLike` is a **structural subset** of the platform fetch declared locally
rather than imported from `lib.dom`, so a test can hand over a recorded transport and core keeps its
no-ambient-globals property (`:8-13`). `platformFetch()` reads `globalThis.fetch` lazily and only on
the ERP arm. `PORTAL_ROOT = '/api/portal/v1'`. `AUTH_PATHS = ['/login', '/token/refresh']` is the
**only** allowlist of POST targets, making read-only a property of the transport rather than a
convention (`:72-85`). Two retries with a fixed backoff. `erpUrl` strips trailing slashes off the
configured base, because a double slash is a different route on some gateways.

### `src/core/supplier/adapters/erp-wire.ts` (167 lines)

The ERP's wire shapes, transcribed field for field from the Go structs that serialize them, with the
source files named in the header (`portal/model.go`, `portal/refresh.go`, `portal/settings.go`,
`portal/rbac.go`, `portal/catalog.go`, `portal/dashboard.go`, `product/model.go`,
`customer/model.go`, `order/model.go`, `billing/model.go`, `quote/model.go`,
`platform/httpx`). **Every field is optional at the type level on purpose**, because this is
untrusted input off a network and `erp-map.ts` decides what a missing or hostile field costs.

Types: `WireUser`, `WireCapabilities`, `WireMe`, `WireConfig`, `WireLogin`, `WireRefresh`,
`WirePage<T>`, `WireCatalogResult`, `WireCatalogSearch`, `WireAccount`, `WireDashboard`,
`WireBillingSummary`, `WireOrder`, `WireOrderDetail`, `WireInvoice`, `WireQuote`. Money arrives as
`*_cents` integers throughout (`subtotal_cents`, `tax_cents`, `total_cents`, `balance_cents`,
`holdback_cents`, `credit_limit_cents`, `open_balance_cents`, `available_credit_cents`).

### `src/core/supplier/adapters/erp-map.ts` (408 lines)

Wire shapes to domain types, the only place cents, unit codes and statuses translate. Two rules run
through it (`:44-53`): **whitelist, never pass-through**, every object is built field by named field
so an unknown field cannot survive the boundary; and **an unrecognised enum degrades to the most
neutral member**, never to the strongest claim.

- `FORBIDDEN_WIRE_FIELDS` (`:57-70`): twelve names that must never cross the counter, including
  `margin_bps`, `floor_bps`, `cost_cents`, `block_policy`, `bypass_reason`, `override_authority`,
  `actor_id`, `actor_name`.
- `UOM_FROM_WIRE` (`:118-136`): identities for HH Pro's own ten codes plus the ERP's spellings
  `PC` to `EA`, `BAG` to `BG`, `BOX` to `BX`, `T` to `TON`, `CYD` to `CY`. **Anything needing a
  conversion is refused, not converted**: `M2` is not `SF`, `KG` is not `TON`, `LYR` has no
  equivalent. `mapUom` returns `Uom | null`, and `CatalogHit.baseUom` is nullable for exactly that
  reason.
- `mapCapabilities` (`:173-183`) collapses the ERP's nine onto HH Pro's six as a **conjunction**:
  `edit-scope` is `submitRfq`; `move-stage` is `submitRfq && createOrders`; `customer-quote` is
  `true` by construction (the server has no opinion about the contractor's homeowner proposals);
  `pay` is `payInvoices && managePaymentMethods`; `confirm-pickup` is `viewOrdersDeliveries`;
  `manage-team` is `manageUsers`.
- Remaining mappers: `mapErpCapabilities`, `mapIdentity`, `mapBranding`, `mapCatalogHit`,
  `mapAccount`, `mapBillingSummary`, `mapFulfillment`, `mapOrderStatus`, `mapSalesOrder`,
  `mapInvoice`, `mapQuote`, `mapPage`.

### `src/core/supplier/adapters/erp.ts` (302 lines)

The read adapter. Three properties stated in its header (`:56-71`): **mode is config, never a
fallback** (a 401 ends the session and never resumes the simulator); **the token never leaves the
adapter**; **capabilities come from the server**, so `login()` is not finished until `GET /me` has
answered, because a session whose permissions HH Pro would have to guess is the local grant table
wearing a JWT.

`refresh()` (`:158-188`) re-resolves the capability map only when the role moved, because the ERP
mints the new token from the freshly loaded user; and it validates the new token **before**
advancing the stored instants, or the session would advertise an expiry belonging to a token it
never adopted. Every read is a GET against the portal root. `writes: null`.

### `src/core/supplier/adapters/sim.ts` (239 lines)

The current behaviour behind the port, adding nothing (`:34-49`). Its four writes are the four methods
`Sim` already publishes, on the same instance `boot` built, wrapped in resolved promises. Its reads
are store snapshots the selectors already read. In sim mode the acting person **is** the person
switcher's selection and capabilities come from the local grants table, because there is no server
to ask; that is the one place the two implementations legitimately differ (`:82-89`). `findIn`
returns `notFound` for a missing id, matching the ERP's "not yours and not there are one answer"
rule.

### Current wiring status, which matters for planning

`boot()` constructs the port and stores it on `AppContext.supplier` (`src/core/boot.ts:73`,
`:204-208`). **Nothing else reads it.** A grep across `src/ui` and `src/core` for consumers of
`AppContext.supplier` or `SupplierPort` returns only `boot.ts` itself and the supplier directory's
own files. The port is built, contract-tested against recorded and live fixtures, and not yet
consumed by any action, selector or screen.

---

## 10. Integrations

### The Anthropic proxy

`server/claude-proxy.ts`.

- **Model allowlist** (`:104`): `ALLOWED_MODELS = new Set(['claude-opus-4-8', 'claude-haiku-4-5'])`.
  Mirrored in `src/core/domain/config.ts:156` as `SELECTABLE_MODELS`, deliberately kept in lockstep
  so a dealer cannot configure their assistant into a 400.
- **Token ceiling** (`:106`): `MAX_TOKENS_CEILING = 32_000`. `validateRequest` (`:114-129`) rejects
  a model outside the allowlist and a `max_tokens` that is not a whole number in range; the integer
  check exists because the API rejects a fractional value.
- **Body cap** (`:165`): `MAX_BODY_BYTES = 12 * 1024 * 1024`, generous for base64 photo and PDF
  attachments but bounded, since the rate limiter charges per request rather than per byte. A
  too-large body answers 413 **then** destroys the request, rather than resetting the socket first.
- **Same-origin only** (`crossOrigin`, `:38-46`): a request whose `Origin` host differs from its
  `Host` is refused 403.
- **Rate limit** (`:52-70`): a token bucket per client address, capacity 20, refilling at 0.5 per
  second, answering 429.
- **Usage cap**: `consumeDailyQuota(cap, usageDay(), accountId)` is consulted per request and
  **counts before it enforces** (`server/admin-store.ts:254`), so a deployment with no cap still
  reports real numbers. A cap that cannot be evaluated fails open, wrapped in try/catch because an
  unhandled rejection inside async middleware hangs the request (`server/claude-proxy.ts:271-296`).
  The account id rides on the `x-hhpro-account` header (`ACCOUNT_HEADER`, `:209`) and is
  **attribution, not authorization** (`:275-283`).
- **Key resolution** (`resolveKey`, `:91-101`): the dealer's stored key wins over the server's
  environment variable, and a key must match the shape check `wellFormedKey` (`:74-76`). There is no
  longer any client-supplied key path.
- **Abort propagation** (`:303-317`): the abort controller listens on the **response**, not the
  request, because since Node 16 an `IncomingMessage` emits `close` on message completion, which
  aborted every upstream call and took the whole endpoint down. `writableEnded` distinguishes a
  normal finish from a hang-up.
- **Mid-stream failure** destroys the socket rather than calling `sendJson` after headers are on the
  wire (`:353-357`).
- **Health**: `GET /api/anthropic/health` answers `{ ok, hasKey }` where `hasKey` is
  `Boolean(apiKey || dealerKey())` (`:401-414`). Unauthenticated, so it stays a bare boolean.
- **Injectable dependencies** (`MessagesHandlerDeps`, `:218-236`): `fetchImpl`, `dealerKey`,
  `consumeQuota`, `readCap`. The docstrings record why: reading the admin store ambiently coupled
  the proxy suite to whatever another suite had left on disk, and the two went flaky against each
  other.

**A stale comment worth noting**: the file header (`:4-16`) still describes a two-source precedence
including a browser-supplied key and calls it BYOK, while `resolveKey` (`:78-101`) no longer accepts
one and its own docstring says so. The code is right; the header is stale.

### The dealer admin console

Client: `admin.html` plus `src/admin/{main.tsx, AdminApp.tsx, api.ts}`. Server:
`server/{admin-api.ts, admin-store.ts, admin-plugin.ts}`.

**Two stores, two files** (`server/admin-store.ts:24-36`), both under
`join(process.cwd(), '.hhpro')` with no environment override:

| File | Mode | Contents |
|---|---|---|
| `.hhpro/config.json` | `0o600` | the **public** `DealerConfig`: branding, assistant settings, supplier settings, feature flags |
| `.hhpro/secrets.json` | `0o600` | the LLM credential, with no route out of the process |
| `.hhpro/usage.json` | `0o600` | request counting, kept separate so accounting never touches the credential file |

The directory is created `0o700`. They are separate **files**, not separate keys, so a mistake that
serialises "the config" cannot serialise the secret: `DealerConfig` has no field that could carry
one. `readSecrets` throws rather than degrading to an empty object, because degrading meant one
transient read error wrote `{}` back and destroyed the dealer's key.

**Routes** (`server/admin-api.ts:184-261`), all under `/api/admin`:

| Method and path | Behaviour |
|---|---|
| `GET /state` | `{ config, revision, credential: { present, masked }, usage }`, built field by field so a secret cannot ride along (`:155-166`) |
| `PUT /config` | requires the `revision` as an `if-match` header; a **missing** header is refused exactly like a stale one, because a caller that sends none cannot have read current state |
| `PUT /credential` | validates against Anthropic **before** storing |
| `DELETE /credential` | clears the stored key |

**The gate** (`authorize`, `:87-101`) fails closed in three steps: no `HHPRO_ADMIN_TOKEN` answers
503 with instructions; a non-loopback caller answers 403 unless `allowRemote`; a token mismatch
answers 401. The comparison is `timingSafeEqual` over equal-length buffers (`:75-81`). Loopback is
`127.0.0.1`, `::1`, `::ffff:127.0.0.1` or any `127.` prefix (`:65-73`). The file's own header
(`:32-36`) states what it does not defend against: anyone who can read the environment or the
`.hhpro/` directory has the key, and a shared token is one factor with no identity, no rotation and
no audit. It calls itself a development gate and names the upgrade path as replacing `authorize()`
alone.

**Credential validation** (`keyWorks`, `:127-152`): a one-token request to the Messages API, the
cheapest call that still proves authentication. **Only a 2xx proves a key**; a 401 or 403 is
`rejected` and anything else is `unreachable`, which refuses with a 503 rather than storing an
unverified key under a UI that promised it had been checked.

**The admin client** (`src/admin/api.ts`) holds its token in **session** storage only (`:5-9`), so
it dies with the tab; there is deliberately no remember-me. `AdminState` carries `config`,
`revision`, `credential: { present, masked }`, and `usage: { total, byAccount[] }` sorted busiest
contractor first.

### Runtime config and branding

`src/core/config/runtime.ts` (57 lines):

- `CONFIG_GLOBAL = '__HHPRO_CONFIG__'`; `dealerConfig()` reads the injected global, runs it through
  `parseConfig`, caches, and falls back to `DEFAULT_CONFIG` (`:21-28`). Validated regardless of
  origin, because the global is trivially editable from a console.
- `resetConfigCache(next?)` for tests and the admin preview.
- **`supplierName()`** (`:44-46`) is the single source for the dealer's name in contractor-facing
  copy. The rule at `:35-43`: static module-level strings are the trap because they freeze at
  import, so the sentence must be built where it is rendered.
- `isEnabled(feature)` (`:55-57`): feature flags gate the UI, never the domain.

`brandingCss(config)` (`src/core/domain/config.ts:548-582`) emits `:root:root{...}` and
`:root:root[data-theme="dark"]{...}`. **Specificity, not source order** (`:564-569`): the bundler
injects the stylesheet after the injected style tag, so a plain `:root` loses the cascade and the
dealer's colour silently never applies, which is what happened the first time. Only `--brand`
prefixed tokens may be emitted, and the test parses every custom property back out to enforce that.
`--brand-on` is **computed** by `onColorFor`, never a dealer field, because a settable on-colour is
a settable way to write white on gold. Platform token values are inlined as literals, never named,
because the block is injected before the stylesheet.

Colour validation (`isValidColor`, `:185-191`) accepts only hex or a conservative `oklch()` form;
anything else is **refused rather than escaped** (`:175-184`), because these strings are
interpolated into a stylesheet and refusing is easy to verify. `isValidLogo` accepts a same-origin
path or an inline data image, never a remote URL, so no third party is beaconed and no mark can be
swapped later. `isValidSupplierBaseUrl` accepts absolute `http:` or `https:` only.

`parseConfig` doctrine: **a bad value costs that value** (`:312-315`); only a non-object payload is
refused outright, because rejecting the whole config over one field once reverted every setting to
defaults, lifting the spend cap and re-enabling every flag. `brandingConfigured` (`:277-302`)
distinguishes "no branding block" (ship the demo tenant whole) from "a branding block that omits a
role" (the dealer does not want that role), so a dealer who sets only their own colour does not
inherit someone else's dark-mode identity. A `mode: 'erp'` with an unusable base URL costs the
**mode**, and the stage flags go down with it (`:385-406`); that is the only place a supplier mode
is ever downgraded, and it is in the config layer, never at runtime.

Injection happens in `server/admin-plugin.ts:50-67` via `transformIndexHtml`, which Vite runs for
`vite build` as well as for dev, so the no-flash guarantee survives static hosting
(`deploy/prod-entry.ts:10-15`, asserted at build time by `deploy/build_bundle.sh:37-40`).

### The AI assistant

`src/core/ai/tools.ts` (499 lines), `prompt.ts` (113), `session.ts` (548), rendered by
`src/ui/components/assistant/AssistantSheet.tsx` (538).

**`ToolTier = 'read' | 'write' | 'commit'`** (`tools.ts:52`), documented at `:42-51`: `read` has no
side effects and runs silently; `write` changes the contractor's own draft and is reported;
`commit` reaches the supplier or moves money and **always asks first**.

`ToolDef` (`:54-67`) is `{ name, description, tier, input_schema, confirm?, run }`. `confirm` is
required for `commit` tools, and there is a test for it because the fallback prompt names no amount.
`toolSchemas()` (`:493`) strips `tier`, `confirm` and `run`, so those stay client-side.

The registry, `TOOLS` (`:93-486`):

| Tool | Tier |
|---|---|
| `get_board` | read |
| `get_order` | read |
| `search_catalog` | read |
| `get_open_invoices` | read |
| `create_project` | write |
| `create_order` | write |
| `add_materials` | write |
| `add_special_order_item` | write |
| `update_quantity` | write |
| `remove_line` | write |
| `set_order_details` | write |
| `build_customer_quote` | write |
| `move_order_stage` | **commit** |
| `send_customer_quote` | **commit** |
| `pay_invoices` | **commit** |

**Every mutating tool calls the same action a button calls** (`:32-40`). The imports at the top of
`tools.ts` are the proof: `buildCustomerQuote`, `sendCustomerQuote`, `updateCustomerQuote`,
`createOrder`, `createProject`, `moveOrderToStage`, `updateOrder`, `payInvoices`, `quotePayment`,
`addCatalogItem`, `addSpecialItem`, `removeItem`, `updateItemQty`. The assistant therefore cannot
reach a code path the contractor cannot, cannot bypass a stage guard, and cannot invent a price, and
a refusal reaches the model verbatim.

`pay_invoices`' confirmation prompt is built from `quotePayment` (`:460`), the same function the
action charges through, so shown and charged cannot drift.

**The system prompt** (`prompt.ts:29-91`) is rebuilt per request and names the dealer through
`supplierName()`. The catalog is inlined as `SKU | Product | List price | Category` lines, because
seeing every option at once beats iterative search for SKU matching; `search_catalog` remains the
durable interface for a catalog too large to inline. The acting person's role and role description
are injected when a team exists, with an instruction to relay a role refusal verbatim rather than
retry. Non-negotiable rules: confirm before anything reaches the supplier or moves money, relay a
refusal verbatim, never state a price not obtained from a tool, read before you write, and
**never invent or adjust a quantity**. Takeoff generation from dimensions is explicitly out of
scope. `houseRulesBlock()` (`:101-113`) appends the dealer's own rules **last and explicitly
subordinate**, framed so a dealer cannot position text to look like it overrides the safety rules.

`model()` and `maxTokens()` read the dealer config. The one deliberate exception to building
sentences at render time is AI **tool descriptions**, which evaluate once at module load and
therefore say "the supplier's catalog".

**The session loop** (`session.ts`) is hand-written rather than using the SDK's tool runner, because
a `commit` suspension spans a UI round trip of arbitrary length. `MAX_TURNS = 12` (`:120`).
`AssistantSession` takes an optional `fetch` through `SessionOptions` (`:70`) so the loop is
testable against canned SSE with no key. `cancel()` aborts the stream and resolves a waiting commit
confirmation as declined. After any error the history is repaired so role alternation and
tool_use/tool_result pairing stay legal. Old attachments are pruned to text placeholders so photos
are not reshipped every turn.

**With no key the assistant is disabled, not faked.** `src/ui/App.tsx:88-108` asks
`/api/anthropic/health` once at boot with a five-second abort, and renders the disabled state when
`hasKey` is false. There is intentionally no mock-echo fallback.

---

## 11. Auth and identity

Stated plainly, because a rebuild needs to know what is and is not there.

**What exists:**

- **An admin token gate.** One shared secret from `HHPRO_ADMIN_TOKEN`, compared with
  `timingSafeEqual`, loopback-only unless `HHPRO_ADMIN_ALLOW_REMOTE=true`
  (`server/admin-api.ts:87-101`). It fails closed: undefined never means "no check". Its own
  docstring calls it a development gate with no identity, no audit and no rotation
  (`server/admin-api.ts:32-36`).
- **The contractor "login" is the persisted acting-person switcher.** `teamStore.activeId` is
  persisted state that every capability check reads (`src/core/stores/root.ts:31-39`,
  `src/core/actions/team.ts:29-32`), so a reload keeps the hat on. `switchActiveMember` is ungated
  and says so (`src/core/actions/team.ts:145-149`).
- **The public share link** is a bearer-style capability URL: `/q/:token`, where the token is 24
  random characters from `crypto.getRandomValues` (`src/core/lib/ids.ts:27-30`). It rotates on
  re-send and immediately on `reviseCustomerQuote`, so a superseded link goes dead.
- **An unused ERP session capability.** `SupplierAuth` on the ERP adapter implements `login`,
  `refresh`, `signOut` and `session` against the portal API, with the token held in an injected
  `TokenStore` (`src/core/supplier/adapters/erp.ts:138-198`). Nothing in the app calls it.

**What does not exist:**

- **No contractor authentication of any kind** in the shipped app. No login screen, no password, no
  JWT, no session cookie. The routes are all open; opening the app puts you in as whoever
  `activeId` names.
- **No tenant scoping.** There is exactly one seeded account (`acct_summit`,
  `src/core/data/demo-seed.json`), and state lives in one browser. `deploy/RUNBOOK.md` says so in
  its risk list: state is per-browser, two testers do not see each other's data, and clearing site
  data resets the demo.
- **No server-side authorization for contractor actions.** The capability gate is client-side by
  construction, in `src/core/actions/team.ts`, and can be bypassed by anyone who can run code in the
  page. The supplier port's comment at `src/core/supplier/port.ts:71-77` names this as the class of
  bug to avoid when the ERP does arrive: never ship a role picker the server cannot enforce.
- **No audit trail** beyond `activityStore`, which is a contractor-facing notification feed bounded
  at 400 entries, not an audit log.
- **The usage counter is not a security control**, and the code says so
  (`server/claude-proxy.ts:275-283`): the account id comes from the client, so a caller could claim
  someone else's.
- **The loopback defence does not survive a reverse proxy.** `isLoopback` reads
  `req.socket.remoteAddress`, which behind Caddy is always `127.0.0.1`
  (`deploy/prod-entry.ts:38-47`, `deploy/RUNBOOK.md` §0 and risk 3). The mitigations are that the
  vhost 404s `/admin*` at the edge and `prod-entry.ts` refuses to mount `/api/admin` without an
  explicit opt-in.

---

## 12. Storage and persistence

Everything is `localStorage`, chosen over IndexedDB deliberately: the whole state is a few hundred
kilobytes, a synchronous read at boot means no hydration flash, and it is inspectable in devtools
(`src/core/stores/persistence.ts:3-9`).

**Key namespace**: `KEY_PREFIX = 'hh:'` (`:28`).

| Key | Written by |
|---|---|
| `hh:meta` | `writeMeta`, carrying `{ schemaVersion, seed, savedAt }` |
| `hh:projects`, `hh:orders`, `hh:scope` | the core trio; a save without all three is not a save |
| `hh:quotes`, `hh:salesOrders`, `hh:invoices` | supplier artifacts |
| `hh:customerQuotes` | contractor proposals |
| `hh:paymentMethods`, `hh:payments` | money |
| `hh:activity` | the notification feed |
| `hh:team` | the roster and `activeId` |
| `hh:sim` | the task queue and the clock anchor |
| `hh:leader` | the scheduler leadership lease |
| `hh:corrupt-backup` | the forensic stash |
| `hh:theme` | the viewer's light/dark choice (`src/ui/lib/theme.ts:24`) |

The admin console's token is separate and in **session** storage under `hhpro.admin-token`
(`src/admin/api.ts:12`).

**`SCHEMA_VERSION = 6`** (`src/core/stores/persistence.ts:64`). The comment above it (`:32-63`) is
the changelog and the reasoning: v3 added the seeded team; v4 was the hardscape fork, where the
whole catalog changed vertical; v5 was the dealer rebrand, needed because `supplierName()` is
interpolated at seed time into persisted tracking and activity strings; v6 was the catalog
relocalization, where SKU strings persisted in order lines would otherwise point at products the
catalog no longer has. **None of them has a `MIGRATIONS` entry, deliberately.**

**`MIGRATIONS`** (`:83-85`) is keyed by target version and currently empty. A missing step is a
deliberate signal, not a gap: `migrate()` returns null and the caller wipes and reseeds (`:126-139`).
A payload from a newer build also returns null.

**Debounced persistence**: `attachPersistence(key, store, debounceMs = 250)` (`:183-209`) writes on
change through a timer, registers its flush in a module-level `pendingFlushes` set, and returns a
detach function. **`flushPersistence()`** (`:220-222`) writes every attached store immediately and
is wired to `pagehide` in `src/main.tsx:15`, chosen because it fires reliably on mobile Safari where
`beforeunload` does not.

**All-or-nothing restore**: `restorePersisted()` (`src/core/boot.ts:295-336`) loads every persisted
key, and on **any** validation failure stashes, wipes and reseeds rather than restoring the valid
half, because a half-restore is how a card ends up in a stage with no supplier artifact behind it.
An absent key is fine (a save from before that feature existed). Shape validators live in
`persistence.ts`: `isValidCollection`, `isValidActivity`, `isValidSim`, `isValidTeam`, dispatched by
`validateStoreState` (`:274-279`). After a successful restore it backfills the team and the payment
methods if either is empty, for the permission-gate reason in section 4.

**The corrupt backup stash**: `stashCorruptState(reason)` (`:286-301`) copies every `hh:` key into
`hh:corrupt-backup` with a reason and a timestamp before the wipe, because silently destroying the
evidence would make corruption undiagnosable.

**First-save completeness**: `boot()` writes every persisted store synchronously once seeding is
done (`src/core/boot.ts:244`), with a long comment (`:226-243`) recording the bug: change-driven
persistence never wrote `projects`, `orders` or `scope` on a fresh demo, so a second tab loaded a
save with a customer quote but no core trio, failed the trio check, reseeded, and lost the quote.
The share link then opened on "This link is no longer valid". It self-healed on any reload, which is
why it only ever reproduced on a first run.

**Cross-tab sync**: `attachCrossTabSync(entries, onAdopted?)` (`persistence.ts:310-334`) listens for
`storage` events and adopts the other tab's write into the local store. Echoes cannot loop, because
browsers never fire `storage` in the writing tab and re-writing an identical string fires no event
elsewhere. Invalid payloads are ignored rather than adopted. `boot` passes an `onAdopted` callback
that re-restores the clock anchor when the `sim` key is adopted (`src/core/boot.ts:251-262`).

**The leader lease**: `hh:leader`, TTL `LEASE_TTL_MS = 7_000`, with `tryAcquireLeadership`,
`renewLeadership` and `releaseLeadership` (`persistence.ts:342-389`). Exactly one tab pumps the
simulator; the rest adopt its writes. `boot` renews on a heartbeat and, when it is not the leader,
polls for a lapsed lease (`src/core/boot.ts:268-284`). A follower never writes the clock anchor
back, because its anchor is a stale copy adopted at boot (`:213-221`). Without the lease, the
two-window flow ran two schedulers over one queue and fired every piece of supplier work twice.

**Not adopted cross-tab**: `TAB_LOCAL_KEYS = new Set(['team'])` (`src/core/boot.ts:112`), for the
identity reason in section 4. Everything else in `PERSISTED_STORES` follows.

**Storage failures never throw**: `storageAvailable()` wraps the accessor in try/catch (`:92-98`),
`saveStoreState` warns once and keeps running from memory (`:152-167`), and `writeMeta` logs rather
than aborting a boot or a pagehide flush mid-write.

---

## 13. Offline behaviour

**Everything works offline as the app stands**, because there is no server in the data path. State is entirely
in `localStorage` and the supplier is simulated in-process. Specifically:

- All twelve persisted stores are local (`src/core/stores/root.ts:111-125`).
- The simulator runs in the browser off a persisted task queue and a persisted clock anchor
  (`src/core/sim/scheduler.ts`, `src/core/sim/clock.ts`), so supplier work continues across a reload
  and across a closed tab.
- Pricing is computed locally by `createPricingEngine` over seeded rules.
- The catalog is regenerated from bundled JSON at every boot.
- Branding and feature flags are **injected into the HTML at build time**
  (`server/admin-plugin.ts:50-67`), so no fetch is needed to paint the dealer's identity.
- `deploy/RUNBOOK.md` §0 states the consequence directly: the only runtime call the app makes is
  `GET /api/anthropic/health`, once at boot.

**What is not offline:** the AI assistant, which needs the proxy and therefore the network. Its
failure mode is an honest disabled state rather than a queue.

**The `pagehide` flush** (`src/main.tsx:15`) is the one durability mechanism: it calls
`flushPersistence()` so the last action before a tab closes is not lost to the persistence
debounce window.

**What would break against a real ERP, stated precisely.** This application has **no outbox, no sync
protocol, and no sequence number.** There is:

- no queue of pending mutations to replay,
- no per-scope cursor or `last_seq`,
- no conflict policy, no tombstones, no attachment queue,
- no retry beyond the ERP client's two transport retries
  (`src/core/supplier/adapters/erp-client.ts:88-90`), which are per-request and in-memory only.

The `sim` task queue is **not** an outbox: it is the simulated supplier's own future work, keyed by
sim time, and every task it runs writes locally. The nearest thing to a conflict rule is
`attachCrossTabSync`, which is last-write-wins per key across tabs in one browser.

Consequently, against a real ERP every supplier-side write would be a live call with no offline
path, and every supplier-side read would be a live fetch with no cache: `SupplierReads` returns
`Promise<Result<T>>` with no local projection behind it
(`src/core/supplier/port.ts:163-175`), and the adapter deliberately never falls back to the
simulator when a session lapses (`src/core/supplier/adapters/erp.ts:58-62`). Building the
read-cache-plus-outbox shape the App Shape describes is net-new work in this codebase.

---

## 14. Design system

`src/ui/styles/theme.css` (359 lines) is the only source of truth, plus `src/ui/styles/identity.css`
(44 lines) for the logo mark's own colours, which are identity constants and explicitly not part of
the dealer contract.

**Three token layers**, stated at `theme.css:24-31`:

1. **Platform**: `--surface`, `--surface-2`, `--surface-3`, `--surface-inset`, `--text`,
   `--text-muted`, `--text-subtle`, `--border`, `--border-strong`, the four `--stage-*` colours,
   `--on-signal`, `--success`, `--warning`, `--danger`, `--info`, and three shadow tokens. Never
   dealer-overridable, so status semantics read the same across every dealer.
2. **Dealer**: everything `--brand`-prefixed. `--brand`, `--brand-hover`, `--brand-on`,
   `--brand-tint`; `--brand-fill`, `--brand-fill-hover`, `--brand-fill-on`; and six chrome tokens
   `--brand-chrome`, `-2`, `-line`, `-on`, `-muted`, `-accent`. The defaults in `theme.css:160-178`
   alias the platform values so an unconfigured deployment renders coherently.
3. **Contractor**: `--cbrand`, `--cbrand-on`, used **only** on customer-facing quote pages where the
   contractor's brand must beat the dealer's.

`docs/brand-tokens.md` is the frozen contract for the names, the `DealerBranding` shape, and the
exact `brandingCss` output. It records the three roles as three questions: identity as text, the
filled pressable control, and the frame around the content.

**Colour space is OKLCH** throughout (`theme.css:119-217` light, `:219-270` dark). Tokens are
bridged into Tailwind utilities through `@theme inline` (`:273` onward), which is how `bg-surface`
and `text-text-muted` work. **Tailwind v4**: there is no `tailwind.config.js` anywhere in the
repository; the theme lives in CSS and the plugin is `@tailwindcss/vite`.

**Light-first, dark first-class.** Light is the bare `:root`; dark is `:root[data-theme="dark"]`.
`src/ui/lib/theme.ts` is the toggle: `ThemeChoice` is `'light' | 'dark' | 'system'`, persisted under
`hh:theme`, defaulting to light. `applyTheme` stamps the attribute only for dark and **removes** it
for light (`:52-65`), because leaving `data-theme="light"` behind would be a third state neither
`brandingCss` block describes. The system preference is re-applied on change **only** while the
viewer has chosen `system`. `index.html` stamps the saved choice in an inline script before first
paint; `src/main.tsx:11` calls `startTheme()` before render.

**Contrast is measured, not estimated.** `npm run contrast` (`scripts/contrast-probe.mjs:1-8`)
paints a colour to a canvas in a real browser and reads the pixel back, because
`getComputedStyle().color` now echoes `oklch(...)` unchanged and parsing it as `rgb()` reads
lightness as red. `theme.css:13-26` records the standard every signal token clears: 4.5:1 three
ways, as text on a card, as text on the 14 percent tint chip of its own hue, and as the fill under
white pill text. The three text steps sit at measured ratios 17.6, 7.4 and 5.5 against `--surface`.

`src/core/lib/color.ts` holds the maths the config layer needs: `relativeLuminance`,
`contrastRatio`, `onColorFor` (picks the better of white and the platform ink for a given fill), and
`isLegibleFill` (refuses a mid-tone band where neither clears the bar). `resolveFill` in
`domain/config.ts:257-261` applies `isLegibleFill` to the **action fill only**, deliberately not to
`brandColor`, because `mapBranding()` never runs `parseConfig` and gating there would refuse the
recorded staging colour rather than simply giving it the correct ink.

**Typography** is self-hosted, three variable faces: Bitter (display, the dealer wordmark), Work
Sans (body), JetBrains Mono (the `.text-data` treatment for SKUs and document numbers). The reasons
are at `theme.css:33-53`: a CDN font beacons every contractor's visit to a third party, and a
webfont that failed mid-capture would silently re-render every guide screenshot in a system face
with nothing failing. `font-display: swap` is deliberate because the dealer wordmark is live text.
Weight ranges are measured rather than copied: JetBrains Mono clamps at 800. `index.html` preloads
Work Sans and Bitter with `crossorigin` (required even same-origin, or the file is fetched twice)
and deliberately does **not** preload the mono face, because an unused preload logs a console
warning that the e2e run treats as a failure.

Money and quantities use `tabular-nums` globally so figures do not jitter.

**Product swatches, not photographs.** `scripts/generate-swatches.mjs` plus the drawing library
`scripts/swatch.mjs` plus `src/core/data/product-colours.json` render one SVG per SKU into
`public/images/products/`. Run with `npm run swatches`. The data file has 45 entries, one per
product, each with `sku`, `colourName`, `primaryHex`, `secondaryHex`, `texture`, `confidence` and
`contested`. The `confidence` field is load-bearing: entries that are honest inferences from product
type stay labelled rather than being presented as fact. Texture families distinguish materials at
thumbnail size, where texture separates a tonne of base from a yard of mulch long before colour
does. Generation is deterministic from the SKU, so regenerating never churns the repository, and a
missing swatch throws rather than falling back to a neutral glyph.

**Shared UI primitives** are deliberately few: `components/ui/Button.tsx` (44px minimum height on
every variant, the touch-target floor for gloved hands; filled variants **drop their fill and go
grey when disabled**, because fade is not a state and colour is) and `components/ui/Sheet.tsx`
(bottom sheet on mobile, centred dialog on desktop, over `@radix-ui/react-dialog`).
`src/ui/lib/cn.ts` is a three-line `twMerge(clsx(...))`.

---

## 15. Tests

### The two vitest projects

`vitest.config.ts:11-40` declares them, and the split is a rule, not a convenience:

| Project | Environment | Include | Exclude |
|---|---|---|---|
| `core` | `node` | `src/**/*.test.ts`, `server/**/*.test.ts` | `src/**/*.dom.test.ts` |
| `ui` | `jsdom` | `src/**/*.test.tsx`, `src/**/*.dom.test.ts` | setup at `src/ui/__tests__/setup.ts` |

The node project runs core so a stray `document` cannot pass a test and then fail in a Lit build.
`.dom.test.ts` is the narrow escape hatch for a core module that genuinely needs a browser API, and
it must be **explicitly excluded** from the node project because `*.test.ts` matches it too
(`:22-26`). `server/` is in scope because the proxy middleware's absence from coverage once hid a
total outage.

### Counts, by area (via `find`)

| Area | Test files |
|---|---|
| `src/core/__tests__` | 4 (`architecture`, `board-flow`, `first-save`, `review-regressions`) |
| `src/core/actions/__tests__` | 6 (`catalog`, `customer-quote`, `fulfillment`, `payments`, `scope`, `team`) |
| `src/core/ai/__tests__` | 2 (`session`, `tools`) |
| `src/core/data/__tests__` | 1 (`catalog-seed`) |
| `src/core/domain/__tests__` | 3 (`config`, `stage`, `units`) |
| `src/core/lib/__tests__` | 2 (`color`, `money`) |
| `src/core/selectors/__tests__` | 1 (`catalog`) |
| `src/core/sim/__tests__` | 2 (`lifecycle`, `pricing`) |
| `src/core/stores/__tests__` | 3 (`cross-tab.dom`, `persistence`, `store`) |
| `src/core/supplier/__tests__` | 5 (`erp-contract`, `erp-staging-recorded`, `erp-staging.live`, `structure`, `zero-delta`) |
| `src/ui` | 2 (`components/board/__tests__/OrderCard.test.tsx`, `layouts/__tests__/PortalLayout.test.tsx`) |
| `server/__tests__` | 4 (`admin-api`, `claude-proxy`, `dev-middleware`, `supplier-env`) |

Totals: 31 node-project `.test.ts` files across `src` and `server`, 1 `.dom.test.ts`, 2 `.test.tsx`,
1 `.live.ts`.

### The named suites and what each defends

- **`src/core/__tests__/architecture.test.ts`** walks every core file and fails on a banned import
  specifier, static or dynamic. It is the un-disableable half of the framework-free rule.
- **`src/core/__tests__/review-regressions.test.ts`**, 24 `it` blocks. Each reproduces a confirmed
  finding from the post-milestone adversarial review; if one fails, that bug is back. It also
  contains a **tree-wide grep with comments stripped** for the demo dealer's name, failing with the
  offending file named, because one rename is a bug and forty is a class of bug.
- **`src/core/__tests__/first-save.test.ts`** is a separate file specifically so it can never call
  `flushPersistence()`. The first attempt at this regression test passed with the bug reintroduced,
  twice, because `flushPersistence` writes every attached store unconditionally and the test's own
  setup manufactured the complete save it then asserted on.
- **`src/core/__tests__/board-flow.test.ts`** covers the board's end-to-end action path.
- **`src/core/ai/__tests__/session.test.ts`** replays **canned SSE** through the injectable `fetch`
  on `SessionOptions`: tool dispatch, the commit gate, the verbatim refusal relay, the turn cap, and
  attachment blocks. It immediately caught the relative-`baseURL` `new URL()` failure.
  `tools.test.ts` pins, among other things, that every `commit` tool supplies a `confirm()`.
- **`src/core/supplier/__tests__/structure.test.ts`** (12 `it`s) asserts read-only on the **built
  object**, not on the type: no method under any of the twenty `SUPPLIER_WRITE_METHODS` names, and
  only GETs issued.
- **`src/core/supplier/__tests__/zero-delta.test.ts`** (11 `it`s) is the structural half of the
  flag-off proof: with `erpReads` absent, no network client is even built, which a passing suite
  alone cannot demonstrate.
- **`src/core/supplier/__tests__/erp-contract.test.ts`** (43 `it`s) covers every Stage 1 read against
  recorded fixtures in `fixtures/`, including the forbidden-wire-field check.
- **`src/core/supplier/__tests__/erp-staging-recorded.test.ts`** (14 `it`s) runs the same adapter
  against **verbatim recordings of the live staging server** in `fixtures-live/`. `NOTES-handoff.md`
  warns that `catalog-search-paver.json` is a real ERP recording and must never be edited.
- **`src/core/supplier/__tests__/erp-staging.live.ts`** is the live suite, deliberately outside
  `npm test` and run through **`vitest.live.config.ts`** (`name: 'live'`, `maxWorkers: 1`, generous
  timeouts). It requires a reachable server and a credential supplied through the environment;
  `vitest.live.config.ts:12-17` names the three variables and states the password is never stored in
  the repository. `npm test` stays hermetic: no network, no credentials, an exactly countable suite.
- **`server/__tests__/dev-middleware.test.ts`** drives a **real Vite dev server** and asserts on
  response **bodies** across three `Accept` values. It exists because every API route once answered
  `200 <!doctype html>` to a browser's default `Accept`, and three status-based checks stayed green
  through the outage.
- **`server/__tests__/claude-proxy.test.ts`** pins that a request carrying the old client-key header
  spends the dealer's key, and gets a 503 rather than spending the caller's when no dealer key is
  configured.
- **`server/__tests__/admin-api.test.ts`** walks several routes asserting the key never appears in
  any response body, and that a payload trying to smuggle a key lands nowhere.
- **`server/__tests__/supplier-env.test.ts`** covers the three-variable overlay.
- **`src/core/stores/__tests__/cross-tab.dom.test.ts`** is the single `.dom.test.ts`, for storage
  events.
- The two `.test.tsx` render tests cover `OrderCard` and `PortalLayout`.

### The out-of-band gates

Described in section 1. Each exists because it caught something unit tests structurally cannot: the
proxy outage, the dev-server API outage, the branding cascade failure, the served secrets file, the
DNS-rebinding bypass, and nine screens of palette failures. Two hard-won rules are embedded in them:
**assert on content, not status**, because the SPA fallback answers 200 for unknown paths; and
**a gate must know it is grading its own program**, after a leftover preview server from a sibling
checkout produced an entire user guide captured from a different application.

---

## 16. Build and deploy

`deploy/RUNBOOK.md` is the operational document. Target `hhpro-staging.gablelbm.com` on an existing
DigitalOcean droplet at `178.128.237.81`, **shared with a co-tenant** service (`dibbits-staging`).
Mode is SIM/DEMO: no ERP connection, no database, no new cloud resources. The whole deployment is
one directory under `/opt/hhpro`, one file in `/etc/caddy/conf.d/`, and one `import` line, and
removing those three returns the droplet to its prior state.

### The deployed shape

**Pure static. There is no server process** (`deploy/RUNBOOK.md` §0). Four facts make that correct
rather than a shortcut, each verified rather than assumed:

1. Dealer config and brand CSS are compiled into the HTML at build time, because
   `adminPlugin.transformIndexHtml` runs for `vite build` as well as for dev, so
   `dist/index.html` already carries `window.__HHPRO_CONFIG__` and `<style id="ln-dealer-brand">`.
   `deploy/build_bundle.sh:37-40` asserts this on every build so it cannot silently regress.
2. `/api/config` is never fetched by the client; it reads the injected global. The endpoint exists
   for the embeddable future.
3. The only runtime call the app makes is `GET /api/anthropic/health`, answered by the vhost with a
   literal `{"ok":true,"hasKey":false}` (`deploy/hhpro-staging.caddy:56-59`).
4. State is `localStorage`; SIM mode needs no database and no backend.

**What serves what:** Caddy serves `/opt/hhpro/current` as a static root with SPA fallback
(`try_files {path} {path}/ /index.html`, `deploy/hhpro-staging.caddy:85-97`), immutable caching on
hashed assets and `no-cache` on HTML. It answers `/healthz` with a literal JSON body carrying the
commit SHA (`:41-44`), 404s `/admin*` at the edge (`:70-71`), and 404s all other `/api/*` with a
JSON body rather than the SPA (`:79-82`). `X-Robots-Tag: noindex, nofollow, noarchive` is set.

### Where the API key lives at runtime

**On staging, nowhere: there is deliberately no key.** The reasoning is in `deploy/RUNBOOK.md` §0
and `deploy/prod-entry.ts:29-36`: the key is the dealer's credential and is billed to them, so
putting one on a droplet shared with a live UAT service behind a public demo URL means a leaked link
becomes a metered relay on somebody's real account. The health probe answers `hasKey:false` and the
app renders its own honest no-key state, which is a real UI state the product ships.

**In the Node mode upgrade path** (§7), the key lives in `/etc/hhpro/staging.env`, mode `0640`,
owner `root:hhpro`, read by systemd through `EnvironmentFile=-` (the `-` prefix meaning start anyway
if absent). It is deliberately not in the unit file, because a unit file is world-readable and shows
up in `systemctl cat` (`deploy/hhpro-staging.service:44-48`). The alternative home is the dealer's
own `.hhpro/secrets.json`, which is why `WorkingDirectory=/var/lib/hhpro` is load-bearing: pointing
the working directory at the release folder would put the stored credential somewhere the next
deploy deletes (`deploy/hhpro-staging.service:27-35`).

### The pieces

| File | Role |
|---|---|
| `deploy/build_bundle.sh` | builds on a machine with the toolchain, never on the droplet; **refuses a dirty tree** because the SHA names the release and is what `/healthz` reports; runs `npm ci`, `npm run build`, asserts the injected config, bundles `prod-entry.ts` with esbuild to a dependency-free `server.mjs`, and tars `dist`, `deploy/`, `VERSION` and `server.mjs` |
| `deploy/staging_apply.sh` | the twelve-step apply, run on the droplet, described below |
| `deploy/hhpro-staging.caddy` | the vhost; `caddy validate` clean, carries a commented-out `basic_auth` block and a commented-out `reverse_proxy` block for the Node upgrade |
| `deploy/hhpro-staging.service` | the systemd unit, **shipped disabled**; unprivileged user, loopback bind, hardening directives, `KillSignal=SIGTERM` with a stop timeout so in-flight streams drain |
| `deploy/prod-entry.ts` | the optional Node entry: static file serving with path-traversal protection, `/healthz`, `/api/anthropic/health`, `/api/anthropic/v1/messages`, `/api/config`, conditionally `/api/admin`, and a JSON 404 for any other `/api/*` so an unimplemented route never falls through to the SPA |
| `deploy/github-workflow-deploy-hhpro-staging.yml` | `workflow_dispatch` only, with `dry_run` and `skip_dns_check` inputs; parked outside `.github/` because the available token had no `workflow` scope |

**`staging_apply.sh`'s order**, from `deploy/RUNBOOK.md` §4: refuse unless root with Caddy active
and the co-tenant marker present; pre-flight the co-tenant's health on loopback and publicly;
pre-flight DNS; back up the Caddyfile; unpack to `/opt/hhpro/releases/<sha>` and flip
`/opt/hhpro/current` atomically; write the vhost with the SHA substituted; append the `import` line
only if absent; `caddy validate`; **`systemctl reload caddy`, never `restart`**, which would drop
the co-tenant's listeners; re-check the co-tenant and **roll back automatically** on degradation;
check the new site; prune to the last five releases and print both rollback commands. `HHPRO_DRY_RUN=1`
runs every check and changes nothing.

**Parked items** at the time of writing, per `deploy/RUNBOOK.md` §2 and the status table: the DNS
record (no credential available for the DNS provider) and the apply itself (no SSH client in the
preparation container). The runbook is explicit that the record must be DNS-only and not proxied, or
ACME cannot reach Caddy.

**A note for planning**: `CLAUDE.md`'s Deployment section still says the deploy target is undecided
and warns against reintroducing a host-shaped `api/` directory. The `deploy/` directory is a
complete, validated, host-specific answer to that question. The two disagree; `deploy/` is the newer
artifact.

**A second deployment shape is referenced in code but has no files here**: `server/supplier-env.ts`
opens by describing a Railway staging service that builds with `npm run build` and serves `dist/`
statically, where environment variables are the only operator lever
(`server/supplier-env.ts:4-16`). Those three variables are `HHPRO_SUPPLIER_MODE`,
`HHPRO_SUPPLIER_BASE_URL` and `HHPRO_SUPPLIER_ERP_READS`, read at **build** time by
`readConfig()` (`server/admin-store.ts:118-120`) because that is when the config is baked into the
HTML. Four properties are asserted in its header: unset means untouched (the input is returned by
reference); it is an overlay and not a validator, so the merged object still goes through
`parseConfig`; only the supplier block is reachable, so a compromised build environment cannot
repaint the shell or lift the spend cap; and nothing in those variables is a secret.

---

## 17. In-flight work

From `NOTES-handoff.md` and `git log --oneline -30` (read-only; no commit metadata reproduced).

**Everything named as outstanding in the previous handoff is now merged to `master`.** The file's
own first paragraph says so, and the log confirms the three merges:

- **Catalog relocalization to San Diego County vendors**, merged at `ca4a510` (branch
  `feature/catalog-socal-vendors`), work commit `5c7e2e1`. `SCHEMA_VERSION` went 5 to 6. Two
  deliberate honesty calls went in: no wood-grain paver exists in the chosen vendor's current
  catalog, so that slot was remapped to a large-format slab with the wood-grain language dropped
  rather than invented; and a slate-texture claim was dropped from a product that is a plain smooth
  module.
- **Three UI and UX wins**, merged at `e56b782`, work commit `d546872`: per-yard stock on the
  product page (`ProductDetail.stockByLocation`, `src/core/selectors/catalog.ts:295-321`), a
  next-volume-break sentence that finishes itself using numbers `quote` already returns, and
  pre-selection of overdue invoices on the pay screen via a lazy `useState` initializer rather than
  an effect, so a later hand deselection is never re-fired.
- **`Today` dashboard pass 1**, merged at `23a8396` (branch `feature/today-dashboard`), work commit
  `fc5d54b`, with a follow-up fixing the guide and e2e scripts against the new profile sheet
  (`627c92e`).

**What is explicitly next**, per `NOTES-handoff.md`: `Today` pass 2 (a dealer rep card as a static
record in `demo-seed.json`, the delivery and pickup strip over real scheduled records only with no
feasibility prediction, and a "Needs you" aggregation selector over three existing signals), then
pass 3, the project by stage matrix over the existing `buildProjectSummaries`. The plan lives
outside this repository in a Claude Code plans directory, and the handoff duplicates its essential
content for that reason.

**Open catalog questions deliberately left unresolved**, recorded in `NOTES-CATALOG.md` and
summarised in `NOTES-handoff.md` §2: three product families (a black granite chip, two cedar mulches,
and a triple mix soil) could not be verified against a real local vendor and were left unchanged
rather than renamed on a guess. The note names what source would settle each, and says not to rename
them without it. Eight wall, stepper, porcelain and fire-pit slots carry documented confidence gaps
(no published hex values, an unverified wood-grain attribute, an unconfirmed thickness, an
unverified price).

**A documented, accepted mismatch**: `src/core/data/demo-seed.ts:17-24` states that the catalog
deliberately did not move into the swappable demo layer, because the product and colour data
describe real manufacturers with researched provenance. The relocalization of the catalog has since
happened (`5c7e2e1`), but that comment still describes the split and names the mismatch as a known,
documented gap rather than a fabricated fix.

**Longer-running threads visible in the log**: DIB-480 built the supplier port and the read adapter
across five commits plus three cherry-picks plus a merge (`cc0e013` through `2caa770`, then
`db110c0`, `b16dad8`, `37fa1a1`); DIB-501 pointed that seam at the real staging ERP with env-driven
config, live recordings and unit translation (`f6ab137`, merged `09a4e7a`). Both landed. The
resulting port is still unconsumed by the app (section 9).

**Working notes present in the repository root**, which are evidence rather than source:
`NOTES-handoff.md`, `NOTES-CATALOG.md`, `NOTES-DIB480.md`, `NOTES-dib-501.md`.

---

## 18. Data volumes and seed

### The catalog source files (`src/core/data/`)

| File | Count | Shape |
|---|---|---|
| `products.json` | **45** | `id` (numeric), `sku`, `name`, `description`, `categoryId`, `imageUrl`, `baseUom`, `isActive`, `brand`, `price` (float dollars), `msrp`, `tags[]`, `relatedSkus[]`, `inStock`, and free-text `leadTime` |
| `categories.json` | **18** | `id`, `name`, `parentId`, `slug`, `isActive`, `treeLevel` |
| `brands.json` | **8** | `id`, `name`, `description`, `website`, `isActive`, `productCount` |
| `bom_templates.json` | **4** | `id`, `name`, `category`, `description`, `items[]` |
| `product-colours.json` | **45** | `sku`, `colourName`, `primaryHex`, `secondaryHex`, `texture`, `confidence`, `contested` |
| `demo-seed.json` | 1 document | region, 2 yards, 1 distribution center, 1 account, 4 team members, per-project site records |

**`CLAUDE.md` is stale here.** Its Layout section says "25 products, 26 categories, 12 brands, 4 BoM
templates". The actual counts are 45, 18, 8 and 4 (verified by parsing each file). The seed's own
comment at `src/core/data/catalog-seed.ts:26-27` also still says "8 of 25 products".

### `src/core/data/catalog-seed.ts` (357 lines)

Converts the raw JSON into the domain model. Two things it does at the boundary:

1. **Float dollars become integer cents exactly once**, here (`:20-23`). Nothing downstream sees a
   float price.
2. **Missing inventory is manufactured deterministically** from `rngFor(seed, 'avail:' + sku)`
   (`:210`), because the source has almost no inventory data and lead-time risk is a headline
   feature. Same seed, same catalog, every run.

Derivations worth carrying forward:

- **`deriveSpecClass`** (`:99-148`): groups interchangeable products. Porcelain is tested **before**
  the paver branch, because outdoor porcelain is tagged `slab` and was being filed as a 60mm concrete
  paver and then offered as an interchangeable alternate. Pavers substitute **within a thickness
  class and never across one** (`paver-80mm-vehicular`, `paver-70mm`, `paver-60mm`,
  `paver-50mm-pedestrian`), because a 60mm paver under a car fails. Further classes: `wall-cap`,
  `wall-segmental`, `step-unit`, `stone-armour`, `stone-flag`, `aggregate-base`,
  `aggregate-decorative`, `mulch-bulk`, `soil-bulk`, `jointing-sand`, `edge-restraint`,
  `geotextile`, `permeable-grid`, `turf-synthetic`, `lighting-lowvoltage`, `fire-feature`,
  `adhesive`.
- **`derivePresentation`** (`:155-181`): buried wins ties, because in hardscape almost everything by
  weight is buried; anything sold by `TON` or `CY` is a commodity; the remaining regex picks out the
  visible finishes a homeowner chose.
- **`splitAcrossYards`** (`:184-217`): an exact split, 60 to 95 percent at the primary yard with
  rounding dust landing back on the primary, so the rows always re-sum to what the source declared,
  because every availability answer goes through `totalOnHand`. A small count stays whole at the
  primary yard rather than inventing a logistics story the demo cannot back up.
- **`deriveAvailability`** (`:211-278`): honours explicit source signals first. A `leadTime` string
  containing "special order" forces zero on hand, because rolling dice over that was letting a
  special-order product come out fully stocked and silently removing the whole lead-time story. A
  stated duration in the source text is parsed and used rather than rolled, so the catalog and the
  card cannot contradict each other.

`seedCatalog(seed)` returns products, categories, brands and locations. `LOCATIONS` comes from the
demo layer: two yards plus one distribution center.

### `src/core/data/demo-seed.ts` and `demo-seed.json`

The swappable locale layer: region, yards (each with address and a `pickupLine` for will-call copy),
the distribution center, the contractor account facts, the crew, and per-project site towns.
`PRIMARY_YARD` falls back to the first entry rather than throwing, because a fixture missing its
`primary` flag should look wrong rather than take the app down on boot
(`src/core/data/demo-seed.ts:71-78`). The file exists because these literals were previously spread
across four modules, which is how every seeded project address ended up carrying a state code and
postcode from the fork's ancestry under a city name from a different country, invisible because the
UI renders only city and state.

### `src/core/data/scenario.ts` (336 lines)

Built to exercise every board rule rather than to look tidy (`:11-20`). Five projects:

| Project | Orders | What each exercises |
|---|---|---|
| Miller | `Patio base & bedding` (plan, fully priced) | can go straight Plan to Order |
| Miller | `Paver field & coping` (plan) | carries a `SO-` special line, so the quote-desk gate fires; also carries a long-lead line against a nearer requested date, which is what makes the lead-time warning mean something |
| Miller | `Pool surround (rough idea)` (plan) | **empty**, and with `inDays: null`, so the "add materials first" and "set a date" guards have a target |
| Anderson | `Permeable driveway` (quote) | sitting at the desk with a special line |
| Wilson | three orders (order, order, plan) | **one project with orders in three stages at once**, the whole reason board cards are orders and not projects |
| Kirkland | `Fire pit terrace` (invoice, will-call) | billed and awaiting payment |

`buildScenario` returns `{ projects, orders, items, invoices, salesOrders }`. **Every card started
past Plan gets its supplier artifacts seeded** (`:227-289`), because two bugs in a row came from a
card in a stage with no artifact behind it: an Invoice-stage card with no invoice, then an
Order-stage card with no sales order and therefore nothing to track. The rule is stated: if you add
a seeded card in a later stage, seed its artifacts too. Three sales orders are seeded
(`out-for-delivery`, `confirmed`, `invoiced`) with full tracking histories, and each is linked back
onto its order the way the simulator would.

Two invoices are seeded (`:305-333`): the Kirkland portal invoice, already past due, and a
**counter sale** with no `orderId`, which is the other half of the story, material bought at the
trade desk landing in the same AR bucket as everything ordered through the portal.

**Seeded prices are computed through the real pricing engine**, never hand-written, so scenario
totals cannot drift from what the app would compute (`src/core/boot.ts:53-59`).

### `src/core/data/team-seed.ts` and `template-seed.ts`

Four team seats, one per role, from the demo layer. Four bill-of-materials starter templates, the
fastest path from an empty order to a priced scope.

### Determinism, via `rngFor`

`src/core/lib/rng.ts`: `mulberry32` seeded by an FNV-style string hash, exposing `next`, `int`,
`pick` and `jitter`. **Sub-streams are keyed by entity id** so a value does not shift because another
entity was created first (`:5-6`). Everything stochastic draws from it, never `Math.random`:

| Call site | Key |
|---|---|
| `catalog-seed.ts:210` | `avail:<sku>` (stock levels, yard split, lead time) |
| `quote-desk.ts:22` | `special:<itemId>` (special-order price jitter) |
| `quote-desk.ts:38` | `lead:<itemId>` (special-order lead time) |
| `quote-desk.ts:112` | `note:<quoteId>` (which desk note) |
| `order-lifecycle.ts:84` | `pick:<salesOrderId>` (pick note) |
| `order-lifecycle.ts:125` | `truck:<salesOrderId>` (truck note) |
| `generate-swatches.mjs` | keyed by SKU, so regenerating never churns the repository |

`boot()` takes a `seed` option and otherwise uses the persisted seed or a fixed default constant
(`src/core/boot.ts:101`, `:127`). The seed is stored in `hh:meta` so a reload replays the same demo.

---

## 19. Appendix

### A. Every file under `src/core`

Tests, fixtures and `__tests__` directories omitted; they are enumerated in section 15.

| Path | Purpose |
|---|---|
| `src/core/boot.ts` | the single wiring point: migrate, restore or seed, build clock, pricing, sim and supplier, attach persistence, first-save, cross-tab, leader lease |
| `src/core/config/runtime.ts` | reads the injected dealer config, caches it, exposes `supplierName()` and `isEnabled()` |
| `src/core/lib/money.ts` | integer cents, `toCents` through the decimal string, formatting |
| `src/core/lib/result.ts` | the `Result` union and its helpers |
| `src/core/lib/ids.ts` | prefixed entity ids and the 24-character share token |
| `src/core/lib/rng.ts` | seeded PRNG with entity-keyed sub-streams |
| `src/core/lib/time.ts` | ISO string helpers, `startOfDay`, formatting; reads no wall clock |
| `src/core/lib/color.ts` | luminance, contrast ratio, `onColorFor`, `isLegibleFill` |
| `src/core/stores/store.ts` | the store primitive plus normalized `Collection` helpers |
| `src/core/stores/root.ts` | the thirteen store instances and the twelve-entry persisted list |
| `src/core/stores/persistence.ts` | schema version, migrations, load and save, validation, corrupt stash, cross-tab adoption, leader lease |
| `src/core/domain/project.ts` | `Project`, `Order`, `ScopeItem`, stages, price-source and expiry predicates |
| `src/core/domain/catalog.ts` | `Uom`, `Product`, `Brand`, `Category`, `Location`, `StockLevel`, `PriceQuote`, `VolumeBreak` |
| `src/core/domain/stage.ts` | the stage machine: `canMoveToStage`, `StageEffect`, `allowedTargets`, `systemAdvanceToInvoice` |
| `src/core/domain/supplier.ts` | `Quote`, `SalesOrder`, `Invoice`, statuses, tracking events, the flow constant |
| `src/core/domain/customer-quote.ts` | `CustomerQuote`, lines, labor, overhead, `QuoteAcceptance`, totals, `CONSENT_TEXT` |
| `src/core/domain/payment.ts` | `PaymentMethod`, `Payment`, card fee, `daysPastDue` and the aging derivation |
| `src/core/domain/account.ts` | `Account`, `ContractorBranding`, `User`, `termsDays` |
| `src/core/domain/team.ts` | roles, capabilities, the grant matrix, labels and refusal verbs |
| `src/core/domain/activity.ts` | `ActivityEntry`, `ActivityState`, unread counting, the entry cap |
| `src/core/domain/totals.ts` | `OrderTotals`, `hasKnownSubtotal`, summation |
| `src/core/domain/units.ts` | the one table of unit words, `perUnit`, `formatQty`, `qtyStep` |
| `src/core/domain/config.ts` | `DealerConfig` and its parts, `parseConfig`, colour and logo validation, `brandingCss` |
| `src/core/sim/clock.ts` | sim time, anchor, speed, advance, restore |
| `src/core/sim/scheduler.ts` | the persisted task queue and the bounded pump |
| `src/core/sim/config.ts` | every simulated delay and the clock speed presets |
| `src/core/sim/types.ts` | `SimStores`, the narrow façade, and `SimContext` |
| `src/core/sim/quote-desk.ts` | the desk: review, price, expire; special-item pricing and lead times |
| `src/core/sim/order-lifecycle.ts` | confirm, pick, stage, dispatch, deliver, invoice; `createSalesOrder` |
| `src/core/sim/pricing.ts` | the ERP pricing engine: tiers, category cascade, contracts, volume breaks |
| `src/core/sim/index.ts` | `createSim`: the four supplier writes, the demo controls, the activity log |
| `src/core/supplier/port.ts` | the supplier seam: reads, writes, auth, identity, capabilities, refusal sentences |
| `src/core/supplier/index.ts` | `createSupplier`, `usesErpReads`, the one-line switch |
| `src/core/supplier/adapters/sim.ts` | the current behaviour behind the port, adding nothing |
| `src/core/supplier/adapters/erp.ts` | the Stage 1 read adapter; `writes: null` |
| `src/core/supplier/adapters/erp-client.ts` | HTTP seam: base URL, bearer, 401, retry, abort, POST allowlist |
| `src/core/supplier/adapters/erp-wire.ts` | the ERP's wire shapes, all fields optional by design |
| `src/core/supplier/adapters/erp-map.ts` | wire to domain: whitelist mapping, unit table, capability collapse, forbidden fields |
| `src/core/actions/orders.ts` | stage moves and order/project creation and update |
| `src/core/actions/scope.ts` | line items: add, quantity, remove, notes, template, reprice |
| `src/core/actions/catalog.ts` | `addProductToPlan`, the catalog's one action |
| `src/core/actions/customer-quote.ts` | build, edit, send, revise, view, accept, request changes |
| `src/core/actions/fulfillment.ts` | pickup confirmation, reschedule, site instructions |
| `src/core/actions/payments.ts` | `quotePayment`, `payInvoices`, payment-method management |
| `src/core/actions/team.ts` | roster management and `requireCapability`, the gate |
| `src/core/selectors/board.ts` | `BoardCard`, `StageColumn`, `buildBoardCards`, `groupByStage`, `buildProjectSummaries` |
| `src/core/selectors/order.ts` | `OrderDetail`, `ScopeLine`, `BreakOpportunity`, `searchProducts` |
| `src/core/selectors/catalog.ts` | browse filtering and sorting, category tree, `ProductDetail`, `PlanTarget` |
| `src/core/selectors/pricing.ts` | `accountPricing`, `quoteForAccount`: the single pricing binding |
| `src/core/selectors/ar.ts` | `ArSummary`, aging slices, invoice rows, paid rows, selected total |
| `src/core/selectors/tracking.ts` | `OrderTracking`, step flow, will-call substitution, `isDispatched` |
| `src/core/ai/tools.ts` | the tool registry, the three tiers, the commit confirmations |
| `src/core/ai/prompt.ts` | the system prompt, inlined catalog, house-rules block, model and token settings |
| `src/core/ai/session.ts` | the hand-written agentic loop, streaming, commit suspension, error repair |
| `src/core/data/catalog-seed.ts` | raw JSON to domain, with derived spec class, presentation and availability |
| `src/core/data/account-seed.ts` | the demo account, tiers, pricing rules, payment methods |
| `src/core/data/scenario.ts` | the showcase board state and its supplier artifacts |
| `src/core/data/demo-seed.ts` | typed door onto the swappable locale layer |
| `src/core/data/team-seed.ts` | four seats, one per role |
| `src/core/data/template-seed.ts` | bill-of-materials starters |
| `src/core/data/products.json` | 45 products |
| `src/core/data/categories.json` | 18 categories |
| `src/core/data/brands.json` | 8 brands |
| `src/core/data/bom_templates.json` | 4 templates |
| `src/core/data/product-colours.json` | 45 colour and texture records with confidence |
| `src/core/data/demo-seed.json` | the demo tenant's places and people |

### B. Every file under `src/ui/pages`

| Path | Lines | Purpose |
|---|---|---|
| `src/ui/pages/TodayPage.tsx` | 33 | the greeting landing screen; rep card, strip and matrix are later passes |
| `src/ui/pages/BoardPage.tsx` | 129 | the procurement board host |
| `src/ui/pages/OrderPage.tsx` | 413 | one order: scope, totals, logistics, the route into quote studio and tracking |
| `src/ui/pages/ProjectPage.tsx` | 123 | the drill-down: one project, its orders grouped by stage |
| `src/ui/pages/CatalogPage.tsx` | 252 | browse and filter, with state in the query string |
| `src/ui/pages/ProductPage.tsx` | 355 | one product: price at quantity, per-yard stock, alternates, add to a plan |
| `src/ui/pages/QuoteStudioPage.tsx` | 452 | the contractor's proposal builder: markup, labor, overhead, send |
| `src/ui/pages/CustomerQuotePage.tsx` | 624 | the public share page; own light palette, no supplier identity, no contractor cost |
| `src/ui/pages/OrderTrackingPage.tsx` | 322 | fulfillment steps, reschedule, will-call confirmation |
| `src/ui/pages/PayPage.tsx` | 381 | AR: aging, invoice selection, the always-visible pay bar |

### C. Every file under `src/ui/components`, by directory

| Path | Lines | Purpose |
|---|---|---|
| `components/ActivitySheet.tsx` | 79 | what the supplier did while you were not looking; the only notification channel |
| `components/DemoDirector.tsx` | 150 | demo controls: clock speed, skip to next event, skip forward, demo reset |
| `components/ErrorBoundary.tsx` | 67 | crash containment with a recovery path on screen |
| `components/assistant/AssistantSheet.tsx` | 538 | the assistant UI: streaming, attachments, the commit approval round trip, the disabled state |
| `components/board/Board.tsx` | 303 | the two board layouts, exactly one mounted, dnd-kit wiring |
| `components/board/OrderCard.tsx` | 209 | one card: photos, totals, lead-time chip, supplier status |
| `components/board/StageBar.tsx` | 88 | mobile stage navigation and, mid-drag, the drop-target row |
| `components/board/StageTransitionSheet.tsx` | 193 | confirmation and rejection, showing the stage machine's own sentence and offering the named remedy as a button |
| `components/board/stageStyles.ts` | small | stage colour, platform-owned and never dealer-overridable |
| `components/brand/DealerLogo.tsx` | 107 | resolves what identity this deployment shows, and how |
| `components/brand/DealerMark.tsx` | 166 | the product's own mark, inline, so it can be painted from tokens |
| `components/catalog/AddToPlanSheet.tsx` | 201 | "where does this go?", listing only plans that can legally take a line |
| `components/catalog/ProductRow.tsx` | 138 | one product in a list; the whole row opens the product |
| `components/order/AddItemsSheet.tsx` | 262 | three ways onto an order: catalog search, a template, or a described special order |
| `components/order/LineItemRow.tsx` | 201 | one line of scope; the contractor price is the number, list is the comparison |
| `components/order/OrderTimeline.tsx` | 96 | the vertical fulfillment timeline, rendering every step and not only the ones reached |
| `components/pay/PaymentSheet.tsx` | 311 | paying; the method picker prices every option, not only the selected one |
| `components/profile/ProfileSheet.tsx` | 302 | the acting-person switcher, the roster with role editing, and appearance |
| `components/quote/SignaturePad.tsx` | 137 | draw-to-sign; clears only on a genuine width change |
| `components/settings/AppearanceCard.tsx` | 91 | the light, dark and system choice |
| `components/team/Avatar.tsx` | 83 | initials chip whose tint is keyed to role, with near-black initials for contrast |
| `components/team/PersonSwitcher.tsx` | 61 | who is acting; in the demo this stands in for login |
| `components/ui/Button.tsx` | 48 | the shared button; 44px minimum height, disabled drops the fill and goes grey |
| `components/ui/Sheet.tsx` | 66 | bottom sheet on mobile, centred dialog on desktop |

### D. Other `src/ui` files

| Path | Purpose |
|---|---|
| `src/ui/App.tsx` | boots the core, declares every route, resolves the tab and heading, mounts the shell-level sheets |
| `src/ui/layouts/PortalLayout.tsx` | the shell: four destinations, mobile tab bar with the raised assistant, desktop sidebar, avatar to profile |
| `src/ui/hooks/useStore.ts` | the only module that knows about both core stores and React |
| `src/ui/hooks/useMediaQuery.ts` | reactive media query and the desktop breakpoint constant |
| `src/ui/lib/theme.ts` | the light/dark choice, persistence, and the attribute stamp |
| `src/ui/lib/cn.ts` | Tailwind class merge |
| `src/ui/styles/theme.css` | the design system: fonts, three token layers, light and dark, the Tailwind bridge |
| `src/ui/styles/identity.css` | the logo mark's four identity constants, outside the dealer contract |

---

## Divergences between `CLAUDE.md` and the code

Collected here so a planner is not misled by the longest document in the repository.

| `CLAUDE.md` says | The code shows |
|---|---|
| Layout: "25 products, 26 categories, 12 brands, 4 BoM templates" | `src/core/data/products.json` has 45, `categories.json` 18, `brands.json` 8, `bom_templates.json` 4 |
| Deployment: "the deploy target is undecided (an M9 concern)" | `deploy/` contains a complete, validated staging deployment: runbook, bundle build, apply script, Caddy vhost, systemd unit, Node entry, and a parked dispatch-only workflow |
| Nothing about a supplier abstraction | `src/core/supplier/` is 1740 lines across seven source files, with a port, two adapters, and five test suites including live recordings |
| Nothing about environment-driven supplier config | `server/supplier-env.ts` plus `SupplierConfig.mode`, `baseUrl` and `stages.erpReads` in `src/core/domain/config.ts` |
| Commands list omits `npm run swatches` | `package.json:20` declares it |
| "Status: M8 complete, plus the catalog destination" with nav implied as Board, Catalog, Pay, More | navigation is `Today`, `Board`, `Catalog`, `Pay` (`src/ui/layouts/PortalLayout.tsx:48-53`), with `More` retired into the profile sheet |
| The proxy header describes a browser-supplied key path | `resolveKey` (`server/claude-proxy.ts:91-101`) accepts only the dealer key and the server environment key; the header comment at `:4-16` is stale, and `CLAUDE.md`'s own later section says the client path was removed |
| Implies CI runs the frontend gates | there is no `.github/` directory in this repository; the only workflow file is parked under `deploy/` and is dispatch-only |
| `src/core/data/catalog-seed.ts:26` still says "inStock on 8 of 25 products" | the file it reads has 45 products |
| `deploy/RUNBOOK.md` §5 expects the browser to paint "Dibbits Landscape Supply" branding | `DEFAULT_CONFIG.branding.companyName` is "Gable Landscape Supply" (`src/core/domain/config.ts:118`); that dealer name arrives only from a dealer config file or from the ERP `/config` read, as the captured live-run transcript in `verification/` shows |
