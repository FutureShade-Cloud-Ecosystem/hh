> Origin: `analysis/domain-map.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# Domain unification map: HH Pro (contractor portal) and HardscapeOS (dealer ERP)

Lane L3 of session HH-1. Read-only analysis. Every claim below cites a repository path.

Two repositories are compared:

- **HH Pro**, `/home/user/hh_pro_dibbits` : React portal, framework-free domain in `src/core/`.
- **HardscapeOS**, `/home/user/hardscapeos_dibbits` : Go modular monolith in `backend/internal/`, Lit frontend in `app/`, 196 numbered SQL migrations in `backend/migrations/`.

## House rules applied to this file

1. No em dash or en dash anywhere. Where a source comment is quoted and the original used one, it has been replaced by a comma or a colon; the wording is otherwise verbatim, and the path is given so the original can be read.
2. No durations, calendar words, or years. Several real identifiers on both sides carry a time unit in their name (a lead-time field on `Product`, a payment-terms column on `customers`, a validity-window constant in the simulator config, a past-due counter in the AR domain). Those are **described**, not spelled, and their file is cited so the exact identifier is one `grep` away. Constant values with a time unit are written as "a configured validity window" per the lane brief. Period-end accounting is written as "period-end". The house time zone rule is written as "period boundaries resolve in the house time zone (America/Toronto)".
2b. One exception to rule 2 is unavoidable: `backend/internal/gl/businessday.go` is a real repository path and house rule 4 requires the citation. It appears four times below as a path only, never as prose.
3. Conflicts are listed, never smoothed. Section 7 collects them. No conflict is resolved here.

---

# 1. Method

## 1.1 What defines the domain on each side

**HH Pro.** The domain is the set of plain TypeScript modules under `src/core/`, and the architectural rule that keeps them plain is enforced twice (`/home/user/hh_pro_dibbits/CLAUDE.md`, "The one architectural rule that matters"; `src/core/__tests__/architecture.test.ts`). The defining files:

| Concern | Path |
|---|---|
| Product, category, brand, UOM union, `PriceQuote` | `src/core/domain/catalog.ts` |
| Unit words, quantity formatting, stepper granularity | `src/core/domain/units.ts` |
| Project, Order, ScopeItem, price provenance, price lapse predicates | `src/core/domain/project.ts` |
| Contractor account, contractor branding, portal user | `src/core/domain/account.ts` |
| Board stage machine and its refusal sentences | `src/core/domain/stage.ts` |
| Supplier-side artifacts (Quote, SalesOrder, Invoice, TrackingEvent) | `src/core/domain/supplier.ts` |
| Contractor-to-homeowner proposal, signature record, totals | `src/core/domain/customer-quote.ts` |
| AR, payment methods, payment, aging | `src/core/domain/payment.ts` |
| Contractor crew roles and capability matrix | `src/core/domain/team.ts` |
| Order money math | `src/core/domain/totals.ts` |
| Dealer configuration (branding, supplier knobs, feature flags) | `src/core/domain/config.ts` |
| Money, ids, time, result, seeded randomness | `src/core/lib/{money,ids,time,result,rng}.ts` |
| The dealer's pricing machinery (deliberately outside `domain/`) | `src/core/sim/pricing.ts` |
| The single binding of engine plus acting account | `src/core/selectors/pricing.ts` |
| The seam to a real supplier, already built | `src/core/supplier/port.ts`, `src/core/supplier/adapters/erp-map.ts` |

**HardscapeOS.** The domain is the SQL schema plus the per-module Go model. The binding conventions are in `docs/design/DATA_MODEL.md` section 0; the ownership table (which module owns which tables, and the rule that cross-module reads go through a Go interface and cross-module writes go through events) is `docs/design/MODULE_FRAMEWORK.md` section 1. Per-module definitions used here:

| Concern | Path |
|---|---|
| UOM rows, conversion factors, rounding rules | `docs/design/DATA_MODEL.md` section 1 (migration `0002`) |
| Products and categories | `docs/design/DATA_MODEL.md` section 2 (migration `0002`, promoted attributes `0018`) |
| Branches and locations | `docs/design/DATA_MODEL.md` section 3 (`0003`) |
| Staff identity and RBAC | `backend/internal/identity/rbac.go`, `docs/design/DATA_MODEL.md` section 4 (`0004`, roles seeded `0015`, platform role `0043`) |
| Stock ledger, balances, cost layers | `docs/design/DATA_MODEL.md` section 5 (`0005`); service in `backend/internal/inventory/` |
| Customers and contacts | `docs/design/DATA_MODEL.md` section 6 (`0006`) |
| General ledger and posting rules | `docs/design/DATA_MODEL.md` section 7 (`0007`); `backend/internal/gl/businessday.go` |
| Quotes and orders | `docs/design/DATA_MODEL.md` section 8 (`0008`); `backend/internal/quote/model.go`, `backend/internal/order/` |
| Invoices and AR subledger | `docs/design/DATA_MODEL.md` section 9 (`0009`); `backend/internal/billing/` |
| Payments, allocations, till | `docs/design/DATA_MODEL.md` section 10 (`0010`); `backend/internal/payment/`, `backend/internal/till/` |
| Vendors, purchase orders, receiving | `docs/design/DATA_MODEL.md` section 11 (`0011`); `backend/internal/inventory/model.go` receive path |
| Dispatch manifests, runs, stops | `docs/design/DATA_MODEL.md` section 12 (`0012`); `backend/internal/dispatch/model.go` |
| Audit log and event outbox | `docs/design/DATA_MODEL.md` section 13 (`0013`), sequence column `0078`; `backend/internal/audit/model.go` |
| Pricing engine | `backend/migrations/0023_pricing.sql`, `0025`, `0026`, `0039`, `0111`, `0112`, `0113`, `0114`, `0133`, `0198`; `backend/internal/pricing/*.go` |
| Tax | `backend/migrations/0042_tax_engine.sql`; `backend/internal/tax/model.go` |
| Contractor portal identity and commerce | `backend/migrations/0019_portal_users_and_invites.sql`, `0030_portal_commerce.sql`, `0048_portal_projects.sql`, `0160_portal_split_buyer_roles.sql`; `backend/internal/portal/rbac.go` |
| Contractor planning board | `backend/migrations/0031_procurement.sql`; `backend/internal/procurement/model.go` |
| Yard tasks | `backend/migrations/0073_tasks_and_task_events.sql`, `0187_task_pick_lines.sql`; `backend/internal/tasks/` |

## 1.2 How the two sides were matched

Three matching passes, in this order:

1. **By existing adapter.** HH Pro already carries a built, tested adapter against the HardscapeOS portal API: `src/core/supplier/port.ts` declares the seam and `src/core/supplier/adapters/erp-map.ts` is, in its own words, "the ONLY place cents, UOM and status translate". Every mapping decision already made there is treated as evidence, not as a proposal, and the places where that file **refuses** to map (an unmappable unit, an unbuilt timeline route, a quote line price keyed by an id the ERP has never seen) are treated as pre-discovered conflicts. The wire shapes it consumes are declared in `src/core/supplier/adapters/erp-wire.ts` and its recorded fixtures under `src/core/supplier/__tests__/fixtures-live/` are captures of real ERP responses.
2. **By name and by role.** A HH Pro type was matched to a HardscapeOS table or struct when both name the same business object for the same actor. Where one name covers two objects (the word "project" covers three distinct things across the two repositories, the word "quote" covers two documents with different senders and different recipients), the collision is recorded as a conflict rather than resolved by picking one.
3. **By behaviour.** Where the shapes agree but the rules disagree (a quantity break that replaces a base price versus one that stacks on it, a contract price that admits a break versus one that is final), the behaviour was read out of the code, not out of the doc, and both readings are quoted in section 4.

Anything the lane could not confirm by reading a file is marked "not found" rather than assumed absent.


---

# 2. Entity overlap matrix

Overlap values: **same** (both model the same object at comparable grain), **subset** / **superset** (one side is strictly narrower or wider), **disjoint** (both use the name, neither means the other's thing), **absent on one side**.

| Candidate unified entity | HH Pro definition (path) | HardscapeOS definition (path) | Overlap | Notes |
|---|---|---|---|---|
| Customer (the dealer's account) | `Account` in `src/core/domain/account.ts` | `customers` in `docs/design/DATA_MODEL.md` section 6 (migration `0006`) | subset (HH Pro narrower) | HH Pro has no customer type, no hold flag, no tax-exempt flag, no home branch; it does carry contractor branding, which the ERP has nowhere. Conflict C13. |
| Contractor company contacts | not found (HH Pro models crew, not contacts) | `customer_contacts` (`0006`) | absent on HH Pro | The ERP contact row carries a role string ('owner', 'foreman', 'AP') that overlaps HH Pro crew roles conceptually but is a different table. |
| Contractor portal user | `User` in `src/core/domain/account.ts`; `TeamMember` in `src/core/domain/team.ts` | `portal_users` (`0019`, widened `0160`) | subset | Two HH Pro types cover one ERP table; `User.role` ('owner' \| 'estimator' \| 'field') and `TeamMember.role` ('owner' \| 'pm' \| 'ap' \| 'field') do not agree with each other. Conflict C29. |
| Contractor account tier | `PricingTier` entity, `Account.pricingTierId` (`src/core/sim/pricing.ts`, `src/core/domain/account.ts`) | `customers.customer_type` plus `pricing_rules` scope `TIER_DEFAULT` / `TIER_CATEGORY` (`0023`) | disjoint mechanism | An entity with an id and a baseline percentage versus an attribute enum with rule rows. Conflict C40. |
| Product | `Product` in `src/core/domain/catalog.ts` | `products` (`0002`), promoted attributes (`0018`) | subset plus extras on both sides | Overlap on sku, name, description, category, base unit, active flag, list price. HH Pro adds presentation, spec class, stock array, lead time, coverage. ERP adds ten hardscape facets, price unit, metadata escape hatch. Conflicts C5 through C9. |
| Category | `Category` (`src/core/domain/catalog.ts`) | `product_categories` (`0002`) | same | Both are a self-referencing tree with a parent. Only HH Pro's **pricing** walks it. Conflict C39. |
| Brand / manufacturer | `Brand` (`src/core/domain/catalog.ts`), `src/core/data/brands.json` | `products.manufacturer` TEXT plus `products.collection` TEXT; an FK table deliberately deferred (`DATA_MODEL.md` section 2) | subset | HH Pro has a brand entity; the ERP has a text facet and says so. Manufacturer names are internal-only on the ERP side (`0039` header). |
| Unit of measure | `Uom` string union of ten members (`src/core/domain/catalog.ts`) | `uoms` rows plus `product_uoms` factors plus `product_rounding_rules` (`0002`), plus a sales conversion graph (`0113`) | subset, severely | A closed union versus rows. Conflicts C10, C11, C12. |
| Price matrix | `PricingTier`, `PricingRule`, `VolumeBreakRule` in `src/core/sim/pricing.ts` | `pricing_list_prices`, `pricing_rules`, `customer_category_overrides`, `quantity_break_headers` / `_items`, `pricing_promotions`, `pricing_category_policy`, `pricing_product_policy` (`0023`, `0026`, `0111`) | subset | The whole of section 4. |
| Promotions and clearance | not found | `pricing_promotions` and clearance (`0111_pricing_promotions_clearance.sql`; `backend/internal/pricing/promo.go`) | absent on HH Pro | Conflict C38. |
| Rebates | not found | `0112_pricing_rebates.sql`; `backend/internal/pricing/rebate.go` | absent on HH Pro | Vendor-facing, never contractor-facing. |
| Tax | not found in `src/core/`; the adapter routes around it (`src/core/supplier/adapters/erp-map.ts`, `mapSalesOrder`) | `tax_rates`, `tax_status_card_profiles`, `tax_evidence`, decision snapshots (`0042`); `backend/internal/tax/model.go` | absent on HH Pro | Conflict C51. |
| Quote (dealer to contractor) | `Quote` in `src/core/domain/supplier.ts` | `quotes` / `quote_lines` (`0008`); `backend/internal/quote/model.go` | subset, and the lifecycles disagree | Conflicts C15, C16, C17. |
| Customer quote (contractor to homeowner) | `CustomerQuote`, `QuoteAcceptance` in `src/core/domain/customer-quote.ts` | not found | absent on ERP | Conflict C15. |
| Quote share link and acceptance | `shareToken` on `CustomerQuote`; `newShareToken` in `src/core/lib/ids.ts` | `backend/internal/quote/share.go` (hashed token, per-link expiry, revocable, view and accept logged), `backend/internal/quote/acceptcontrols.go` (multi-accept is a dealer switch) | same idea, different document and different artifact | Conflict C18. |
| Plan / procurement board card | `Order` with `stage` in `src/core/domain/project.ts` | `procurement_projects` with lane status (`0031`; `backend/internal/procurement/model.go`) | overlapping, different grain | HH Pro's card is an order inside a project; the ERP's card is the project. Conflict C19. |
| Project (the job site) | `Project` in `src/core/domain/project.ts` | `projects` (`0048`), a rollup keyed to `customers` with `orders.project_id` | same intent, different reach | The ERP project reaches invoices and deliveries through the order link; HH Pro's has no stage and no money. Conflict C19. |
| Order line / scope item | `ScopeItem` with frozen `ItemSnapshot` (`src/core/domain/project.ts`) | `order_lines` and `quote_lines` (`0008`); `procurement_project_lines` (`0031`) | subset plus a mechanism the ERP does not have | Conflicts C20, C21. |
| Sales order | `SalesOrder` in `src/core/domain/supplier.ts` | `orders` (`0008`); `backend/internal/order/` | subset | Status sets differ and the adapter already loses information collapsing them. Conflict C4. |
| Invoice | `Invoice` in `src/core/domain/supplier.ts` | `invoices` / `invoice_lines` (`0009`) | subset | No tax, no holdback, no line table on the HH Pro side. Conflict C25. |
| AR subledger | not found (balance lives on the invoice) | `customer_transactions` (`0009`) | absent on HH Pro | Conflict C25. |
| Payment | `Payment` with allocations (`src/core/domain/payment.ts`) | `payments` plus `payment_allocations` (`0010`) | same shape, different rules | Conflict C27. |
| Payment method | `PaymentMethod` (`src/core/domain/payment.ts`) | `portal_payment_methods` (`0030`) | subset | Conflict C28. |
| Card or tender fee | `cardFeePercent` knob plus `feeFor` (`src/core/domain/payment.ts`, `src/core/domain/config.ts`) | `OntarioSurcharge` in `backend/internal/payment/surcharge.go` | disjoint rules | Conflict C26. This one has a legal dimension. |
| Till | not found | `till_sessions` (`0010`); `backend/internal/till/` | absent on HH Pro | Counter-only; no contractor surface. |
| Inventory and stock | `StockLevel` array on `Product` (`src/core/domain/catalog.ts`) | `stock_moves` plus `stock_balances` plus `cost_layers` plus `product_branch_costs` (`0005`); `backend/internal/inventory/availability.go` | subset, severely | A projection field versus a double-entry ledger. Conflict C5. |
| Reservations and holds | not found | `stock_balances.allocated_base` / reserved (`0005`; `backend/internal/inventory/availability.go`), soft holds on quote lines (`backend/internal/quote/reserve.go`) | absent on HH Pro | HH Pro has no concept of quantity held for a card. |
| Yard tasks | not found | `tasks`, `task_events` (`0073`), pick lines (`0187`); `backend/internal/tasks/` | absent on HH Pro | The full-sync offline scope candidate of section 6. |
| Receiving | not found | `purchase_orders`, `po_lines`, `po_receipts` (`0011`), receipt lines are `stock_moves` rows; `backend/internal/inventory/model.go` | absent on HH Pro | |
| Dispatch and delivery tracking | `TrackingEvent` on `SalesOrder` (`src/core/domain/supplier.ts`) | `manifests` / `manifest_lines` / `manifest_versions` (`0012`); `Run`, `Stop`, `StopDetail` in `backend/internal/dispatch/model.go`; POD photos (`0091`) | subset | The adapter ships an empty tracking array because the timeline route does not exist (`erp-map.ts`, `mapSalesOrder`). Conflict C24. |
| Attachments | signature data URL on `QuoteAcceptance`; assistant attachment blocks pruned to text placeholders (`/home/user/hh_pro_dibbits/CLAUDE.md`, "The session survives failure") | a photo reference on `tasks` (`0073`), dispatch POD photos (`0091`), invoice documents (`0146`), masked evidence references in `tax_evidence` (`0042`) | absent as an entity on both sides | Neither side has one attachment table. Conflict C37. |
| Audit | `activityStore` notifications (`src/core/domain/activity.ts`) | `audit_log` plus `events_outbox` (`0013`), sequence column (`0078`); `backend/internal/audit/model.go`; a second domain trail in `pricing_audit_log` (`0023`, `0114`, `0133`) | absent on HH Pro | An unread-count feed is not an audit trail. Conflict C36. |
| Team and roles (contractor side) | `TeamRole` plus `Capability` matrix in `src/core/domain/team.ts`, gate in `src/core/actions/team.ts` | `portal_users.role` plus `Capabilities` in `backend/internal/portal/rbac.go` | overlapping, ERP finer | Already partly reconciled by `mapCapabilities` in `src/core/supplier/adapters/erp-map.ts`. Conflict C29. |
| Staff roles (dealer side) | not found | 13 roles in 3 tiers plus a platform role, `backend/internal/identity/rbac.go` | absent on HH Pro | Conflict C30. |
| Branches and locations | not found in `src/core/domain/`; a nullable branch id appears only on the supplier snapshot (`src/core/supplier/port.ts`) | `locations` tree with a denormalised branch root (`0003`), `branch_id NOT NULL` on every operational table | absent on HH Pro | Conflict C32. |
| Tenants and orgs | not found | not found (no `org_id` or `tenant_id` column exists in any of the 196 migrations) | absent on both | Conflict C33. The context file's section 4.4 requires both columns. |
| Ordered-stream sequence | not found | only `audit_log.seq` (`0078`) | absent on both, nearly | Conflict C34. |
| Cursor pagination | `SupplierPage` mirrors the ERP envelope (`src/core/supplier/port.ts`) | `httpx.Page[T]` with total, limit, offset (`backend/internal/platform/httpx/httpx.go`) | same, and both are offset-based | Conflict C34. |

---

# 3. Field-by-field comparison for the core entities

## 3.0 Cross-cutting representations (money, ids, time, status)

| Concern | HH Pro | HardscapeOS | Agreement or conflict |
|---|---|---|---|
| Money type | Integer cents. `export type Cents = number` with `toCents` scaling through the decimal string rather than multiplying, `src/core/lib/money.ts` | Integer cents. `BIGINT`, column suffix `_cents`, "No float money, ever", `docs/design/DATA_MODEL.md` section 0 | **Agreement, confirmed by path on both sides.** This is the single cleanest match in the whole map. |
| Currency and presentation | `new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' })`, `src/core/lib/money.ts` | CAD only, stated as a convention in `docs/design/DATA_MODEL.md` section 0; the frontend renders `CA$` (`app/src/price-breakdown.ts`), and the receiving path converts a foreign-currency purchase order at receipt time before any value reaches a stock move (`backend/internal/inventory/model.go` header) | **Conflict C1.** |
| Id scheme | `newId(prefix)` produces `job_x7f2a1`-style prefixed short ids from `crypto.getRandomValues`, explicitly "not cryptographically strong", `src/core/lib/ids.ts`. Share tokens are a separate longer token from the same file | `UUID DEFAULT gen_random_uuid()` on every primary key, `docs/design/DATA_MODEL.md` section 0. Natural keys are `CITEXT` (sku, account number, GL code, email). Share and invite tokens are stored only as a sha256 hash (`backend/internal/quote/share.go`, `backend/migrations/0019_portal_users_and_invites.sql`) | **Conflict C2.** |
| Human-facing document numbers | `formatSequence(prefix, n)` with a caller-owned counter so a seeded demo replays, `src/core/lib/ids.ts`; `Q-1001`, `SO-5001`, `INV-9001`, `CQ-1001` | Per-type sequences seeded in the migration, `CITEXT NOT NULL UNIQUE` columns `quote_no`, `order_no`, `invoice_no`, `payment_no`, `po_no`, `manifest_no`, `docs/design/DATA_MODEL.md` sections 8 through 12 | Same idea, different generator. Folded into C2. |
| Time | ISO 8601 strings everywhere, `export type IsoDateTime = string`, `src/core/lib/time.ts`. Nothing in core reads the wall clock; simulated time comes from `SimClock` (`src/core/sim/clock.ts`). No zone is modelled at all | `TIMESTAMPTZ` everywhere with created and updated stamps on every table, `docs/design/DATA_MODEL.md` section 0. Period boundaries resolve in the house time zone (America/Toronto), argued at length in `backend/internal/gl/businessday.go`, with the same constant repeated in the messaging rollup and the transfer estimate rather than shared | **Conflict C3.** |
| Status | TypeScript string unions in lower case, for example `OrderStage = 'plan' \| 'quote' \| 'order' \| 'invoice'` (`src/core/domain/project.ts`) and `SalesOrderStatus` (`src/core/domain/supplier.ts`) | `TEXT` plus `CHECK (status IN (...))`, in upper case, and "no PostgreSQL ENUMs ever", `docs/design/DATA_MODEL.md` section 0 | Same design instinct (a closed set that is cheap to widen), different casing and different members. **Conflict C4.** |

### C1: currency

HH Pro's only money formatter is bound to `en-US` and `USD` (`src/core/lib/money.ts`, the `USD` constant and `formatCents`). The ERP is CAD-only by convention and prints `CA$` on its own pricing surface (`app/src/price-breakdown.ts`). What each side loses: if the ERP model wins, HH Pro's formatter must become configuration and the seeded catalogue numbers must be reinterpreted; if the portal model wins, a Canadian dealer's portal prints a foreign currency symbol over CAD amounts, which is the "confident wrong number" failure the portal's own doctrine forbids (`/home/user/hh_pro_dibbits/CLAUDE.md`, "Never render a confident wrong number"). Context-file bearing: none directly; this is a product-correctness matter.

### C2: ids

Prefixed short ids are readable in tool payloads, which is load-bearing for the assistant (`src/core/lib/ids.ts` says so). UUIDs are the ERP's binding convention and are what every foreign key in 196 migrations expects. What each side loses: UUIDs everywhere costs the assistant's readable payloads and the demo's replayability of id-keyed random sub-streams (`src/core/lib/rng.ts` keys sub-streams by entity id); prefixed ids cost the ERP its primary-key convention and its ability to accept a client-generated id as an idempotency key without widening every column. Context-file bearing: section 4.5 requires the offline outbox to "replay the outbox in order with client-generated ids as idempotency keys", which means the unified id type must be generatable on a disconnected client. A UUID is; a server sequence is not.

### C3: time

Both sides store an instant. Only one side has a house-period rule, and it is argued as the record of a shipped defect (`backend/internal/gl/businessday.go`): resolving a period from the process zone filed evening receipts into the wrong period. HH Pro has no zone at all, and its AR aging derives everything from one past-due counter function in `src/core/domain/payment.ts` that subtracts two instants and floors. What each side loses: adopting the house-zone rule costs HH Pro nothing except a dependency on a configured zone, and gains it a correct aging bucket boundary; adopting HH Pro's zone-free model costs the ledger its period integrity. Context-file bearing: none explicit, but section 4.4's audit requirement and section 4.5's sync ordering both assume an unambiguous instant.

### C4: status

Two sub-problems. The representational one (lower-case union versus upper-case CHECK) is cosmetic and already bridged in `src/core/supplier/adapters/erp-map.ts`. The lossy one is not. That file's own maps show it:

```
const ORDER_STATUS = { CONFIRMED: 'confirmed', PICKING: 'picking',
  PARTIALLY_FULFILLED: 'out-for-delivery', FULFILLED: 'delivered',
  INVOICED: 'invoiced', CLOSED: 'invoiced', CANCELLED: 'cancelled' }
```

`PARTIALLY_FULFILLED` becomes `out-for-delivery`, `CLOSED` and `INVOICED` collapse to one value, and `READY` is resolved by asking how the order leaves the yard. The quote map is worse: `DRAFT` becomes `in-review`, and `SENT`, `ACCEPTED` and `CONVERTED` all become `priced`. The ERP order set has nine members (`docs/design/DATA_MODEL.md` section 8), HH Pro's has eight and they are not the same eight. What each side loses: the ERP set wins and the contractor sees yard vocabulary; the portal set wins and three distinct ERP states become indistinguishable, which breaks the quote module's own accept guard (`backend/internal/quote/acceptcontrols.go` refuses a second acceptance and names the order the first one became).

## 3.1 Product

| Field | HH Pro (`src/core/domain/catalog.ts`) | HardscapeOS (`0002` per `docs/design/DATA_MODEL.md` section 2; promoted attributes `0018`) | Agreement or conflict |
|---|---|---|---|
| id | `EntityId` prefixed string | `UUID PRIMARY KEY DEFAULT gen_random_uuid()` | C2 |
| sku | `sku: string` | `sku CITEXT NOT NULL UNIQUE` | Agreement |
| name, description | `name`, `description: string` | `name TEXT NOT NULL`, `description TEXT NOT NULL DEFAULT ''` | Agreement |
| category | `categoryId: EntityId` | `category_id UUID REFERENCES product_categories(id) ON DELETE SET NULL` | Agreement. The ERP link is nullable; HH Pro's is required |
| brand | `brandId: EntityId` into a `Brand` entity | `manufacturer TEXT`, `collection TEXT`; an FK table deliberately deferred | Partial |
| base unit | `baseUom: Uom` | `base_uom CITEXT NOT NULL REFERENCES uoms(code)` **and a separate** `price_uom CITEXT NOT NULL REFERENCES uoms(code)` | **C10, C48.** HH Pro has no notion that the price unit can differ from the base unit |
| active | `isActive: boolean` | `is_active BOOLEAN NOT NULL DEFAULT TRUE` | Agreement, and both give the same reason (flag, do not delete) |
| list price | `listPrice: Cents` on the product, plus optional `msrp` | **Not a products column.** The retail anchor is `pricing_list_prices` in the pricing module (`backend/migrations/0023_pricing.sql` block 1b), which exists for a minority of products and falls back to a cost-derived anchor | **C9 and C42** |
| stock | `stock: StockLevel[]` where `StockLevel = { locationId, onHand, onOrder }`, plus `totalOnHand` and `isStocked` helpers | `stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)` maintained in the same transaction as each append-only `stock_moves` row, rebuildable from the ledger (`docs/design/DATA_MODEL.md` section 5). The read seam is `AvailabilityFacts { Found, IsActive, OnHandBase, ReservedBase }` in `backend/internal/inventory/availability.go` | **C5** |
| lead time | a lead-time integer on `Product`, and a copy frozen onto `ItemSnapshot`; described in `src/core/domain/catalog.ts` as driving "the lead-time-vs-delivery-date warnings that are core to the product" | No lead-time column on `products`. Incoming supply is `purchase_orders.expected_at` plus the incoming read in `backend/internal/inventory/incoming.go`, and an inter-branch transfer estimate resolved in the house zone (`backend/internal/inventory/transfer_eta.go`, cited in `backend/internal/gl/businessday.go`) | **C6** |
| presentation | `presentation: Presentation` where `Presentation = 'selection' \| 'commodity'`, derived in the seed from the product NAME rather than its tags (`src/core/data/catalog-seed.ts`, `derivePresentation`) | Not found | **C7** |
| substitution class | `specClass?: string`, derived in `src/core/data/catalog-seed.ts` by `deriveSpecClass` | Not found as a column. The nearest ERP equivalents are the facets `application`, `finish`, `collection` and the add-on pairing map behind `AddonSuggestion` (`backend/internal/pos/model.go`) | **C7** |
| imagery | `imageUrl?: string`, and the catalogue ships generated SVG swatches rather than photographs, one per SKU, with a measured colour and a drawn texture (`/home/user/hh_pro_dibbits/CLAUDE.md`, "The catalogue has swatches, not photographs"; `scripts/generate-swatches.mjs`, `src/core/data/product-colours.json`) | A byte route plus a `HasImage` boolean on the add-on tile (`backend/internal/pos/model.go`) | **C8** |
| coverage | `coverageSqFt?`, `coverageLf?` | `coverage_sf_per_unit DECIMAL(19,8)`, `pieces_per_sf DECIMAL(19,8)`, `weight_per_unit_kg DECIMAL(19,4)`, `pallet_count INTEGER`, all documented as convenience duplicates of `product_uoms`, which is the source of truth on conflict | Partial agreement; the ERP has the weight leg, HH Pro does not. **C12** |
| tags, related, specs | `tags: string[]`, `relatedSkus: string[]`, `specs: SpecPair[]` | Ten typed facets plus `metadata JSONB NOT NULL DEFAULT '{}'` as the declared escape hatch | Partial; the ERP promotes facets to columns, HH Pro keeps a label and value pair list |
| branch scope | none | `products` is explicitly a global master with no branch column; branch-specific stock, cost and pricing live in their own tables (`docs/design/DATA_MODEL.md` section 2) | Agreement on the master being global |

### C5: stock

HH Pro's stock is a value on the product, manufactured deterministically at seed time because the salvaged catalogue only had a stock flag on a minority of rows (`/home/user/hh_pro_dibbits/CLAUDE.md`, "The catalog seed manufactures inventory"). The ERP's stock is a projection of an append-only ledger whose whole purpose is to be able to explain itself (`docs/design/DATA_MODEL.md` section 5: "every movement is one immutable row, like a receipt that can never be erased"), and it carries an allocated or reserved quantity that HH Pro has no field for. What each side loses: the ledger model wins and the portal must ask the server for availability rather than reading a field it already holds, which is a round trip on every catalogue row; the portal model wins and the yard loses reservation, valuation, and the ability to rebuild a balance it doubts.

### C6: lead time

HH Pro treats lead time as a product attribute and freezes a copy onto the line snapshot, so a quote desk answer can carry its own lead time (`Quote.linePrices` in `src/core/domain/supplier.ts` carries one per line). The ERP has no such attribute; supply timing is a purchase-order expectation or a transfer estimate. What each side loses: the ERP model wins and HH Pro's headline "can this arrive in time" warning has no source; the portal model wins and the ERP gains a denormalised attribute that nothing maintains.

### C7: presentation and spec class

Both are HH Pro inventions and both are derived, not authored (`src/core/data/catalog-seed.ts`). `presentation` decides whether a homeowner-facing proposal tells a product story or summarises a line; `specClass` decides what counts as an alternate. The ERP's own comment records a defect of exactly this kind in its classifier (`/home/user/hh_pro_dibbits/CLAUDE.md`, "Outdoor porcelain was offered as interchangeable with a concrete paver"), which is an argument for the classification being data rather than derivation. What each side loses: the ERP model wins and the customer-quote presentation rule has nowhere to live; the portal model wins and the dealer inherits two derived columns they cannot correct without a code change.

### C8: imagery

The swatch decision is a stated ethical position, not a placeholder: "a contractor ordering 640 sf of paver must never be shown an image implying a finish that is not what arrives on the pallet" (`/home/user/hh_pro_dibbits/CLAUDE.md`). A missing swatch throws rather than falling back. The ERP serves real image bytes. A unified model has to decide whether a product carries one image, an ordered set, or a generated swatch plus optional photography, and whether a missing image is an error or an empty state.

## 3.2 UOM and quantity representation

| Concern | HH Pro | HardscapeOS | Agreement or conflict |
|---|---|---|---|
| Unit vocabulary | A closed union of ten members in `src/core/domain/catalog.ts`: `EA`, `SF`, `LF`, `TON`, `CY`, `PLT`, `BG`, `BX`, `RL`, `BD`. A parallel word table in `src/core/domain/units.ts` gives each a short form, a singular, a plural and a "how this is sold" sentence, and the test for it "enumerates the whole `Uom` union: a missing entry renders `/undefined`, it does not throw" (`/home/user/hh_pro_dibbits/CLAUDE.md`) | Rows in `uoms(code, name, dimension, is_active)` where `dimension IN ('count','area','weight','volume','length')`, seeded with at least ten codes including `PC`, `LYR`, `PLT`, `SF`, `M2`, `KG`, `T`, `CYD`, `BAG`, `EA` (`docs/design/DATA_MODEL.md` section 1) | **C10** |
| Vocabulary drift, already measured | `UOM_FROM_WIRE` in `src/core/supplier/adapters/erp-map.ts` maps `PC` to `EA`, `BAG` to `BG`, `BOX` to `BX`, `T` to `TON`, `CYD` to `CY`, and **refuses** `M2`, `KG` and `LYR` because each would need a conversion. The file records that the drift was found by a live read: "every `PC` paver and `BAG` of sand came back `baseUom: null`" | Same table, other direction | **C10.** The refusal list is the exact cost of the closed union |
| Quantity type | `qty: number` on `ScopeItem` (`src/core/domain/project.ts`); `multiplyCents(cents, qty)` in `src/core/lib/money.ts` accepts a fractional quantity | `DECIMAL(19,4)` for quantities and `DECIMAL(19,8)` for conversion factors, both binding (`docs/design/DATA_MODEL.md` section 0, ruling D-008). Serialised over the wire as a string to preserve scale (`backend/internal/quote/model.go`: `QtyEntered string` with the comment "DECIMAL as string"; same in `backend/internal/procurement/model.go`) | **C11** |
| The entered trio | One quantity and one unit per line | Every document line stores `qty_entered`, `uom_entered` **and** `qty_base`, "so the contractor sees 320 SF and the yard sees 7 PLT plus 2 LYR" (`docs/design/DATA_MODEL.md` section 1). Inventory is always in base units | **C11** |
| Conversion | None. `coverageSqFt` and `coverageLf` are takeoff hints, not conversions; `src/core/domain/catalog.ts` calls them "used by takeoff math" | `product_uoms(product_id, uom_code, qty_in_base, is_base, role_hints)` with exactly one base row per product and the rule "any-to-any: qty times from factor divided by to factor, one hop, no graph walk". A second, pricing-owned sales conversion graph (`backend/migrations/0113_pricing_uom_conversions.sql`) adds the weight leg and a round-up-at-pick flag, because "you cannot pick 0.63 of a pallet" | **C12** |
| Rounding to a sellable increment | `qtyStep(uom)` in `src/core/domain/units.ts` steps area and length by ten and everything else by one, and says it is a control affordance, not a rule | `product_rounding_rules(sell_increment_uom, break_pallet_allowed, break_pallet_fee_cents, waste_factor_pct)` (`docs/design/DATA_MODEL.md` section 1) plus the handling-unit flag on the conversion edges (`0113`) | **C12.** One side has a stepper, the other has a policy with a fee |
| Unit words | `src/core/domain/units.ts` is declared the single table of unit words so "the catalog, an order line, a customer quote and (later) an embedded web component must all say 'per tonne' the same way" | `uoms.name` plus the frontend word maps in `app/src/pricing-words-logic.ts`, which exist for the same stated reason: a stored enum is wire format and "a screen that prints one is showing a database column where a sentence belongs" | **Agreement in principle on both sides, independently derived.** Both keep an unmapped value visible rather than inventing one |

### C10: unit vocabulary

A closed TypeScript union cannot represent a unit the dealer adds, and the ERP's design position is explicitly that "conversions are rows, so adding a unit or fixing a factor is data entry, not a code release" (`docs/design/DATA_MODEL.md` section 1). The union's benefit is that it makes the word table total, which is what the `Record<Uom, UnitCopy>` in `src/core/domain/units.ts` and its enumerating test buy. What each side loses: rows win and HH Pro loses compile-time totality on the word table, so an unknown unit must render as itself or as nothing; the union wins and the dealer cannot add `M2`, `KG` or `LYR` without a portal release, and three units already in the ERP seed stay unrepresentable.

### C11: quantity type

A JavaScript number is a binary float. The ERP forbids float money for the reason HH Pro's own `src/core/lib/money.ts` gives, then extends the same argument to quantity with a fixed-scale decimal, and carries the decimal as a string over the wire so the scale survives JSON. HH Pro's line quantity is a float and its extension is `Math.round(item.unitPrice * item.qty)` (`itemExtended` in `src/core/domain/project.ts`). What each side loses: the decimal model wins and HH Pro must adopt a decimal string type and stop doing arithmetic on quantities in the browser; the float model wins and a fractional tonnage or a coverage quantity accumulates the exact error class the money rule exists to prevent. Context-file bearing: none explicit; this is a data-model correctness question.

### C12: conversion, weight and handling units

This is the deepest single gap. HH Pro can express "640 sq ft" and can print "/tonne", but has no way to answer "how many pallets is that and what does it weigh", which the ERP treats as the defining hardscape requirement and solves with rows (`docs/design/DATA_MODEL.md` section 1, and the sales conversion graph in `0113`). The ERP file is explicit that the seeded factors are not authoritative and carry a source marker saying so. What each side loses: the row model wins and the portal gains a dependency on per-product conversion data that may be incomplete, which means a portal line may be unable to render a pallet count; the portal model wins and the yard cannot round a pick to a handling unit, cannot check a truck load, and the break-pallet fee has no home.

## 3.3 Customer and Contractor

| Field | HH Pro `Account` (`src/core/domain/account.ts`) | HardscapeOS `customers` (`docs/design/DATA_MODEL.md` section 6, migration `0006`) | Agreement or conflict |
|---|---|---|---|
| id | `EntityId` | `UUID` | C2 |
| account number | `accountNumber: string` | `account_no CITEXT NOT NULL UNIQUE` | Agreement |
| name | `name: string` | `name TEXT NOT NULL` | Agreement |
| commercial type | `type: 'cash' \| 'charge'` | `customer_type TEXT DEFAULT 'CONTRACTOR' CHECK (customer_type IN ('CONTRACTOR','RETAIL','BUILDER','GOVERNMENT','INTERNAL'))` | **C13.** Two different axes wearing one name: one is "does this account accrue a balance", the other is "which trade segment is this", and the second one is the pricing tier (C40) |
| terms | a terms code union (`COD`, and two net codes) plus a resolver that turns it into a count, `src/core/domain/account.ts` | an integer payment-terms column with a default | Same fact, one as a code and one as a number. Minor; folded into C13 |
| credit limit | `creditLimit?: Cents`, optional and unused by any guard | `credit_limit_cents BIGINT NOT NULL DEFAULT 0`, enforced by a credit gate that also runs inside quote conversion (`backend/internal/quote/credit.go`, and the header of `backend/internal/quote/model.go`: the gate "runs HERE, not upstream") | **C13** |
| hold | not found | `on_hold BOOLEAN NOT NULL DEFAULT FALSE`, with an automatic hold policy switch (`backend/internal/billing/autohold.go`) | **C13** |
| tax exemption | not found | `tax_exempt BOOLEAN NOT NULL DEFAULT FALSE`, extended by evidence-gated relief profiles in `0042` | **C13, C51** |
| home branch | not found | `home_branch_id UUID REFERENCES locations(id)`, with customers global across branches by design | **C32** |
| addresses | `addresses: Address[]` embedded on the account; `Address` carries `state` and `zip` (`src/core/domain/project.ts`) | `billing_address JSONB` on the customer; delivery addresses live on the dispatch stop (`AddressLine`, `City`, `PostalCode`, latitude and longitude in `backend/internal/dispatch/model.go`); the portal has its own address surface (`backend/internal/portal/addresses.go`) | **C13.** Note the field names: a United States state and postal code shape against an Ontario city and postal code |
| contractor branding | `branding: ContractorBranding` with company name, contact, licence number, logo data URL and accent colour, embedded on the account | Not found. The dealer's own branding is configuration (`backend/internal/portal/` config route, consumed by `mapBranding` in `src/core/supplier/adapters/erp-map.ts`), but the contractor's branding has no table | **C14** |
| open balance and available credit | not on `Account`; present on the supplier snapshot the adapter reads (`AccountSnapshot` and `BillingSummary` in `src/core/supplier/port.ts`) | derived from `customer_transactions` and the credit gate; served as the billing summary the adapter consumes | Agreement via the adapter; the ERP is the source |
| contacts | not found | `customer_contacts(name, email, phone, role, is_primary, is_active)` | Absent on HH Pro |

### C13: what an account is

The ERP customer row is a credit instrument and a tax subject. The HH Pro account is a pricing key and a branding carrier. Neither is a subset of the other in intent. What each side loses: the ERP model wins and HH Pro gains fields it must render (hold, available credit, tax status) but loses nothing it had, except that its `type` axis has to be reconstructed from terms; the portal model wins and the dealer loses the hold flag, the tax exemption, the enforced credit limit and the home branch, each of which is wired into a guard somewhere in the ERP.

### C14: contractor branding

`ContractorBranding` exists so the homeowner-facing proposal can carry the contractor's brand and beat the dealer's (`/home/user/hh_pro_dibbits/CLAUDE.md`, design system layer 3: "used only on customer-facing quote pages, where the contractor's brand must beat the dealer's"). The ERP has no place to put it, and the adapter already drops the dealer's support contact fields rather than growing an ERP-shaped variant of a domain type (`src/core/supplier/adapters/erp-map.ts`, `mapBranding`). A unified model must decide whether contractor branding is dealer-held tenant data or contractor-held device data. Context-file bearing: section 4.5 makes brand a per-frontend manifest property (`brand: hh-pro`), which is the **dealer or product** brand, not the contractor's; the contractor's own brand has no home in the App Shape as written.

## 3.4 Quote

Two documents on the HH Pro side, one on the ERP side, and the names collide.

- **HH Pro `Quote`** (`src/core/domain/supplier.ts`) is what the dealer's desk sends back: a status, a desk note, and a set of per-line prices with a lead time each. It is created by the supplier, never by a UI action.
- **HH Pro `CustomerQuote`** (`src/core/domain/customer-quote.ts`) is the contractor's proposal to the homeowner: markup, labour, overhead, a share token, a validity bound and a signed acceptance.
- **HardscapeOS `quotes`** (`0008`, `backend/internal/quote/`) is the dealer's quote to the contractor, with a share link (`share.go`), an acceptance path, soft stock holds (`reserve.go`), conversion to an order, lineage, follow-up and a multi-accept dealer switch (`acceptcontrols.go`).

| Field | HH Pro `Quote` | HH Pro `CustomerQuote` | HardscapeOS `quotes` / `quote_lines` | Agreement or conflict |
|---|---|---|---|---|
| Sender and recipient | dealer to contractor | contractor to homeowner | dealer to contractor | **C15** |
| Status set | `'submitted' \| 'in-review' \| 'priced' \| 'expired' \| 'withdrawn'` | `'draft' \| 'sent' \| 'viewed' \| 'changes-requested' \| 'accepted' \| 'declined' \| 'expired'` | `DRAFT \| SENT \| ACCEPTED \| CONVERTED \| EXPIRED \| LOST` | **C16.** The ERP set is nearest to `CustomerQuote`, not to `Quote` |
| Validity bound | `expiresAt?` on the quote | `validUntil`, capped by the earliest supplier expiry across the order's lines (`supplierPriceHorizon` in `src/core/actions/customer-quote.ts`) and recapped on every update | `valid_until DATE` on the header | **C17** |
| Price validity grain | per line: `priceExpiresAt?` on `ScopeItem` (`src/core/domain/project.ts`), stamped by the desk (`src/core/sim/quote-desk.ts`) from a configured validity window in `src/core/sim/config.ts` | derived from the lines | per document only | **C17** |
| Effect of a lapsed price | `isPriced(item, now)` returns false and `needsQuoteDesk` counts the line again, so an order that was placeable becomes unplaceable (`src/core/domain/project.ts`, `src/core/domain/stage.ts`) | a lapsed supplier price forbids building the proposal at all (`unpricedLines` and `refuseUnpriced` in `src/core/actions/customer-quote.ts`) | an `EXPIRED` status, plus a re-check of expiry under the row lock at acceptance time (`backend/internal/quote/acceptcontrols.go` header), plus per-rule validity bounds in pricing (`valid_from` / `valid_to` in `0023`) | **C17** |
| Stock holds | none | none | soft holds synced from the quote's lines inside the same transaction as the line write, released on reject or expiry, promoted to hard holds on conversion (`backend/internal/quote/reserve.go`) | **C17** |
| Share link | none | `shareToken?`, minted by `newShareToken` (`src/core/lib/ids.ts`), rotated immediately on revision, not at re-send | a sha256-hashed token with a per-link expiry stored on the row, revocable, every view and accept logged, a dead link answering GONE rather than NOT FOUND (`backend/internal/quote/share.go`) | **C18** |
| Acceptance artifact | none | `QuoteAcceptance { signedName, signatureDataUrl, acceptedAt, consentText, acceptedTotal }`, with `CONSENT_TEXT` captured verbatim | a typed full name plus an explicit affirmative, "presented honestly as an electronic attestation", and explicitly **not** a drawn mark, because the existing signature component "flips a boolean on pointerdown and the server seals the literal string SIMULATED" (`backend/internal/quote/share.go` header) | **C18.** The two sides made opposite calls about the same artifact |
| Re-acceptance | a revision rotates the token and reopens the proposal (`reviseCustomerQuote`) | as left | refused by default, and allowed only behind an org switch that records provenance per acceptance (`backend/internal/quote/acceptcontrols.go`) | **C16** |
| Line shape | `linePrices`, an array of scope-item id, unit price and a lead-time field | `CustomerQuoteLine` with `unitCost` (never shown to the customer), presentation, imagery, specs and a contractor note | `quote_lines` with the entered trio, `unit_price_cents`, `price_uom`, `line_total_cents`, a nullable `product_id` for free-text lines, and `metadata JSONB` | **C20.** Note that `linePrices` is keyed by an HH Pro scope-item id the ERP has never seen, which `src/core/supplier/adapters/erp-map.ts` records as the reason `mapQuote` leaves it empty |
| Money on the header | none on `Quote`; `computeQuoteTotals` derives materials, markup, labour, overhead, grand and margin for `CustomerQuote` | as left | `subtotal_cents`, `tax_cents`, `total_cents` stored on the header | **C25, C51** |
| Conversion to an order | HH Pro does not convert; the order card moves stage and a sales order is created | not applicable | `Convert` creates an order carrying `quote_id`, inside a transaction that also runs the credit and hold gate and promotes soft holds to hard (`backend/internal/quote/model.go` header, `reserve.go`) | **C19** |

### C15: two documents named quote

The unified model needs two entities, not one, and needs to name them differently. The risk is not conceptual, it is operational: a screen, an event topic or an API path that says "quote" without saying whose is ambiguous in a merged monorepo. What each side loses: collapsing them onto the ERP quote loses the contractor's markup, labour, overhead and homeowner signature entirely (the ERP has no table for any of it); collapsing them onto `CustomerQuote` loses the desk note, the conversion link, the soft holds and the credit gate.

### C16: quote lifecycle

The ERP lifecycle is a dealer's: it ends in `CONVERTED` or `LOST`, and re-acceptance is a policy switch with an audit trail. HH Pro's supplier-quote lifecycle is a contractor's read of the same thing and collapses three ERP states to one (`erp-map.ts`, `QUOTE_STATUS`). HH Pro's customer-quote lifecycle has states the ERP has no analogue for at all (`viewed`, `changes-requested`). What each side loses: the ERP set wins and the portal cannot distinguish "sent and not yet opened" from "opened", which is the one signal a contractor chasing a homeowner wants; the portal set wins and the dealer loses `CONVERTED` as distinct from `ACCEPTED`, which is what the multi-accept refusal message is built on.

### C17: price validity and holds

HH Pro made price expiry a **domain** concept with per-line grain and made it re-block a previously placeable order; the CLAUDE file calls that out as deliberate ("an expired price re-blocks an order that was previously placeable"). The ERP has document-level validity, rule-level validity bounds, promotion windows, effective-dated price batches and stock holds, but nothing that tells a portal line "the number you were given has lapsed". What each side loses: the ERP model wins and HH Pro's expiry guard has no server fact to read, so either it re-derives expiry from the quote header or the guard is dropped; the portal model wins and the ERP gains a per-line expiry column plus the obligation to keep it true, and the hold lifecycle (soft on quote, hard on order) has no portal counterpart.

### C18: the acceptance artifact

Quoted from `backend/internal/quote/share.go`, with the dash removed per house rule 1: "Acceptance is a typed full name plus an explicit affirmative, presented honestly as an electronic attestation. It does NOT reuse pos-signature.ts: that component is a stub with no canvas, no ink and no image." Quoted from `src/core/domain/customer-quote.ts`: `QuoteAcceptance` is "kept as a record rather than a boolean because 'they accepted' is a claim the contractor may at some point need to stand behind", and it stores a drawn mark as a data URL alongside the typed name, the verbatim consent text and the frozen total. HH Pro also hardened the drawn-mark control specifically (the pad clears only on a genuine width change, `/home/user/hh_pro_dibbits/CLAUDE.md`). What each side loses: the ERP position wins and the homeowner-facing proposal loses the drawn mark that its own doctrine treats as evidence; the portal position wins and the ERP must ship a real canvas signature capture, which its own comment says it deliberately did not build.

## 3.5 Order

| Field | HH Pro `Order` / `ScopeItem` (`src/core/domain/project.ts`) | HardscapeOS `orders` / `order_lines` (`docs/design/DATA_MODEL.md` section 8) and `procurement_projects` / `_lines` (`0031`, `backend/internal/procurement/model.go`) | Agreement or conflict |
|---|---|---|---|
| What the board card is | the `Order`, one per procurement unit, inside a `Project` that has no stage; "a project's orders can sit in four different columns at once" | the `procurement_project`, with lanes `PLANNING`, `QUOTING`, `ORDERED`, `FULFILLED`, `CANCELLED`; committed artifacts (quotes, orders, invoices) are reused downstream and deliberately not modelled on the board (`backend/internal/procurement/model.go` header) | **C19** |
| Project | `Project { accountId, name, clientName?, address?, archivedAt? }`, no stage, no money | two different tables: `projects(customer_id, name, job_address)` with `orders.project_id` as a rollup (`0048`), and `procurement_projects` as the board (`0031`) | **C19.** The word covers three things |
| Stage | `stage: OrderStage` on the card, moved by a guarded drag; `canMoveToStage` returns effects and a refusal sentence (`src/core/domain/stage.ts`) | board lane on the project; order `status` with nine members; the two are separate machines | **C19** |
| Fulfilment | `fulfillment: 'delivery' \| 'willcall'` | `fulfillment TEXT CHECK (fulfillment IN ('PICKUP','DELIVERY','WILL_CALL'))`; the adapter collapses `PICKUP` and `WILL_CALL` to one contractor-facing value (`mapFulfillment` in `erp-map.ts`) | Partial; **C4** |
| Requested date and address | `requestedDate?`, `deliveryAddressId?`, `siteInstructions?`, `poNumber?` | `requested_date DATE` on the order; the address and instructions live on the dispatch stop | Partial |
| Money on the header | none; totals are derived in `src/core/domain/totals.ts` and only over **priced** lines, with `hasKnownSubtotal` deciding whether the number may be shown at all | `subtotal_cents`, `tax_cents`, `total_cents`, `deposit_required_cents` stored on the header | **C25.** Derived versus stored, and the ERP header includes tax |
| Supplier link | `quoteId?`, `salesOrderId?`, `invoiceId?`, set by the simulator, never by a UI action | `orders.quote_id` set at conversion; invoices carry `order_id` | Agreement in shape |
| Line identity | `ScopeItem` with `kind: 'catalog' \| 'special'`, `productId?`, and a frozen `ItemSnapshot { sku, name, imageUrl?, brandName?, leadTime }` | `order_lines` with a nullable `product_id`, a `description` for free-text lines, the entered trio, `allocated_base`, `fulfilled_base`, `is_special_order BOOLEAN`, `metadata JSONB` | **C20, C21** |
| Frozen snapshot | yes, and the reason is stated: "a quote already sent to a homeowner must not silently change because the dealer edited the catalog" | no line-level product snapshot; the frozen artifacts are `manifest_versions` (a full header and lines snapshot per version) and the quote line's own stored price | **C20** |
| Special order | a synthesised sku with a documented prefix (`SPECIAL_ORDER_PREFIX`, `isSpecialOrderSku`), captured unpriced, which is what makes the quote-desk gate fire | a boolean on the line, plus a nullable product reference for free text | **C21** |
| Adding a SKU already present | bumps the existing line's quantity, keyed on product id alone (`addCatalogItem` in `src/core/actions/scope.ts`) | the counter merges on **three** conditions: same product id, same sell unit trimmed and case-insensitive, and a target line carrying no line-level work of its own (`app/src/_pos.ts` `decideAdd`, documented in `/home/user/hardscapeos_dibbits/NOTES-POS-DUPLICATE-LINE.md`). The server writes lines verbatim and owns no line identity | **C22.** Same rule, different predicate, and the ERP note explains why the unit test matters: "250 SF and 3 PC of one slab are not one number" |
| Edit lock | scope locks once the supplier holds the order (stages `quote`, `order`, `invoice`); `updateOrder` refuses logistics changes once the supplier holds it (`/home/user/hh_pro_dibbits/CLAUDE.md`) | order edit is a service with its own history and authority (`backend/internal/quote/edit*.go` for quotes; order edit history keyed by the audit sequence per `backend/migrations/0078_audit_log_seq.sql`) | **C23** |
| Pull back or cancel | a guarded backward drag, refused once the supplier status is out for delivery, delivered or invoiced (`PAST_CANCEL` in `src/core/domain/stage.ts`) | order cancellation is a seam with a billing counterpart (`backend/internal/billing/order_cancel_seam.go`) and hold release | **C23** |
| Delivery tracking | `TrackingEvent { at, status, note }[]` on `SalesOrder`; the adapter ships an empty array because the timeline route does not exist and "inventing a timeline from the status would be fabricating supplier facts" (`erp-map.ts`) | `Run`, `Stop`, `StopDetail` with stop sequence, time window, contact, geocode, made-to-order classification, proof-of-delivery photos (`backend/internal/dispatch/model.go`, `0091`), plus versioned manifests (`0012`) | **C24** |

### C19: the board card, and three things named project

This is the structural decision of the merge. HH Pro's answer is stated as the product: "Board cards are Orders, not Projects... This is why every card has exactly one unambiguous stage, there is no 'which stage is this half-delivered job in' question to answer" (`/home/user/hh_pro_dibbits/CLAUDE.md`). The ERP's answer is the opposite: the board card is the project, and committed artifacts are not modelled on the board at all (`backend/internal/procurement/model.go`). A third `projects` table exists purely as a rollup keyed to orders (`0048`), created because the shipped board was a different feature from the specced one, which that migration says in its own header. What each side loses: the ERP grain wins and a job with material in three different states has one lane and the stage question comes back; the portal grain wins and the ERP's planning board, its lane transitions, its indicative RFQ pricing and its rollup all have to be rebuilt around a finer card.

### C20: the frozen snapshot

HH Pro freezes product identity onto the line. The ERP freezes nothing at line level and relies on a product reference plus a stored price. What each side loses: the reference model wins and a sent proposal can change under the homeowner when the dealer edits the catalogue, which is precisely the defect the snapshot exists to prevent; the snapshot model wins and every line carries duplicated product text that the dealer cannot correct, and the ERP gains a second place product names live.

### C21: special-order lines

HH Pro encodes "the dealer cannot price this" in the **sku** (a documented prefix) so that one predicate, `isSpecialOrderSku`, drives the quote-desk gate. The ERP encodes it as a boolean on the line and separately allows a null product reference for a free-text line. The two are not the same set: an ERP free-text line has no sku at all. What each side loses: the boolean wins and HH Pro's gate predicate changes from a string test to a flag read, which is cheap; the prefix wins and the ERP gains synthetic skus in a `CITEXT UNIQUE` column that is a catalogue key.

### C22: duplicate line merge

Both sides reached the same product rule from opposite directions, and the ERP's is stricter in a way that matters for pricing: merging across units would "invent a quantity in a unit nobody chose, and the engine would then price the invention" (`/home/user/hardscapeos_dibbits/NOTES-POS-DUPLICATE-LINE.md`). HH Pro keys on product id alone and sets the line unit from the product's base unit, so the case cannot arise at present, but it will the moment a line can be entered in a unit other than the base unit (which is exactly what C11 and C48 introduce).

### C23: edit authority

Both sides lock a document once the supplier holds it. HH Pro's lock is a stage predicate in the actions layer; the ERP's is a service with an edit history keyed by a monotonic sequence, and a separate authority for a below-floor price. What each side loses: the ERP model wins and the portal gains an edit-history obligation it does not have at present; the portal model wins and the dealer loses the edit trail that the audit sequence column was added for.

### C24: delivery tracking

The two models are not in conflict so much as one is a stub for the other: `TrackingEvent` is a flat list, `Stop` is a routed, sequenced, geocoded, photographed delivery. The live conflict is that the portal currently renders an empty list rather than the ERP's real stop, by the adapter's own choice, and the honest unified model has to decide whether a contractor sees the stop sequence and the estimate at all.

## 3.6 Invoice and Payment

| Field | HH Pro (`src/core/domain/supplier.ts`, `src/core/domain/payment.ts`) | HardscapeOS (`docs/design/DATA_MODEL.md` sections 9 and 10; `backend/internal/billing/`, `backend/internal/payment/`, `backend/internal/till/`) | Agreement or conflict |
|---|---|---|---|
| Invoice header | `Invoice { id, number, accountId, orderId?, salesOrderId?, origin: 'portal' \| 'counter', issuedAt, dueAt, subtotal, balance, description }` | `invoices { invoice_no, customer_id, order_id, branch_id, status, issue date, due date, subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents, finalized_at, metadata }` | **C25** |
| Tax on the invoice | none | `tax_cents`, owned by the tax module, with a per-line split through the single rounding authority | **C25, C51** |
| Holdback | none | `holdback_cents`, the only footprint in the core model of the Ontario construction-lien regime, with the cockpit built later (`docs/design/DATA_MODEL.md` section 9); the portal plan flags it as a hard precondition on any pay slice, because "a contractor must not pay the legally-retained portion" (`docs/contractor-portal-plan.md` section 4.2, hardening item 7) | **C25** |
| Invoice lines | none. `description: string` only, and the adapter sets it to the empty string because "the ERP invoice header carries none, the lines do, and lists omit them" (`erp-map.ts`, `mapInvoice`) | `invoice_lines` with the entered trio, price unit, line total, and a link back to the order line | **C25** |
| Invoice status | derived: `isOpen(invoice)` is `balance > 0` | `status TEXT CHECK (status IN ('DRAFT','FINALIZED','PARTIALLY_PAID','PAID','VOID'))`, and finalize is one transaction with the GL journal and the subledger row | **C25** |
| AR subledger | none; the balance is a field | `customer_transactions` with a signed amount, a running balance snapshot, a source reference and a type set including NSF and write-off | **C25** |
| GL | none | posting doctrine: a journal entry is written in the same transaction as its source document, resolved through a posting-rules table rather than by account name (`docs/design/DATA_MODEL.md` section 7) | **C25** |
| Origin | `origin: 'portal' \| 'counter'`, derived by the adapter from whether the ERP linked an order | not a column; a counter sale is an invoice with no order | Agreement, derived |
| Aging | `AgingBucket` of four members with labels, all derived from one past-due counter function that is declared "THE definition of late" (`src/core/domain/payment.ts`) | the AR landing surface and reminders (`backend/internal/billing/ar_landing.go`, `ar_reminders.go`) | Partial. HH Pro's derivation discipline is stronger and stated; whether the two bucket boundaries agree was not verified |
| Payment | `Payment { accountId, methodId, allocations: { invoiceId, amount }[], amount, status: 'processing' \| 'succeeded' \| 'failed', initiatedAt, settledAt?, confirmationNumber?, failureReason? }` | `payments { payment_no, customer_id, method TEXT CHECK (method IN ('CASH','CARD','CHEQUE','EFT','OTHER')), amount_cents, status TEXT CHECK (status IN ('RECEIVED','DEPOSITED','BOUNCED','REFUNDED','VOID')), received_at, branch_id, till_session_id, gateway_ref, received_by }` plus `payment_allocations(payment_id, invoice_id, amount_cents)` | **C27** |
| Tender set | two (`ach`, `card`) | five (cash, card, cheque, electronic transfer, other) plus a till session and an over-or-short count | **C27** |
| Partial payment | refused by design: "Partial payment is deliberately not supported: allocations always clear the" balance (`src/core/actions/payments.ts`) | `payment_allocations.amount_cents` is free, and `PARTIALLY_PAID` is an invoice status | **C27** |
| Decline handling | `payInvoices` returns success on a decline because "the action succeeded; the payment failed", so the `Payment` record survives to be rendered (`/home/user/hh_pro_dibbits/CLAUDE.md`) | the processor returns a three-valued result (approved, retry, declined), settlement is a separate action, and a durable pending intent must be written **before** the charge call (`docs/contractor-portal-plan.md` section 4.2, hardening item 1) | **C27** |
| Idempotency | none; the action is synchronous and local | a server-bound idempotency key scoped to the account and the request hash is declared mandatory because the processor exposes no idempotency header (`docs/contractor-portal-plan.md` section 4.2) | **C27** |
| NSF and reversal | none | payment status flips and compensating subledger and GL rows are written (`docs/design/DATA_MODEL.md` section 10) | **C27** |
| Payment method | `PaymentMethod { accountId, kind: 'ach' \| 'card', label, isDefault, expiry? }`, with a comment that it is never a real card number | `portal_payment_methods { customer_id, created_by_portal_user_id, gateway, gateway_token, method_type CHECK IN ('CARD','BANK'), brand, last4, expiry fields, is_default, status CHECK IN ('active','removed'), metadata }`, storing only an opaque vault token (`0030_portal_commerce.sql`) | **C28** |
| Card fee | a flat percentage read through one function so the number shown and the number charged cannot drift (`cardFeePercent` and `feeFor` in `src/core/domain/payment.ts`), configured in `src/core/domain/config.ts` and clamped there, defaulting to a value above two and a half percent | `OntarioSurcharge(amountCents, cardType, mdrBps, enabled)` in `backend/internal/payment/surcharge.go`: credit only, capped at the lesser of the legal cap and the merchant discount rate, **disabled by default**, and debit and prepaid "are NEVER surcharged (prohibited in Ontario)" | **C26** |

### C25: the invoice

The ERP invoice is an accounting document with a tax component, a legally retained component, lines, a finalize transaction and a subledger. The HH Pro invoice is a balance to pay. The adapter has already papered over the largest gap by mapping the ERP total (tax included) into the field HH Pro calls `subtotal`, and says why: HH Pro "has no tax model at all... so mapping the ERP's pre-tax subtotal would understate every order by the tax and turn 'never render a confident wrong number' into rendering a confidently wrong one" (`src/core/supplier/adapters/erp-map.ts`, `mapSalesOrder`). That is a working mitigation and a standing misnomer. What each side loses: the ERP model wins and the portal must render tax, holdback and lines, and must stop calling a tax-inclusive total a subtotal; the portal model wins and the ledger loses the tax split, the holdback carve-out, the subledger and the same-transaction GL guarantee, each of which the ERP design calls out as a defect it was built to avoid.

### C26: the card fee

This is the only conflict in the map with a legal edge, and the two sides disagree on default, scope and ceiling at once.

- HH Pro: a configured percentage applied to any `card` method, shown before the contractor chooses, and the doctrine is that the difference is "real money" and must be visible (`/home/user/hh_pro_dibbits/CLAUDE.md`, Tracking and AR).
- HardscapeOS: off by default, credit only, never debit or prepaid, and clamped to a statutory cap (`backend/internal/payment/surcharge.go`).

What each side loses: the ERP rule wins and the portal's fee disclosure has to become conditional on card type and on a dealer switch that is off, so most contractors see no fee at all, and the "show the difference before choosing" affordance mostly has nothing to show; the portal rule wins and a deployment can surcharge a debit card in Ontario, which the ERP file says is prohibited, and can exceed the cap. Context-file bearing: none directly, but section 4.4's rule that every privileged action is audited applies to a fee decision, and the ERP already routes tender decisions through an audit recorder (`backend/internal/audit/model.go`, the sensitive-access seam names `till.session` and `pos.sale`).

### C27: payment semantics

Three separable disagreements: whether a payment may be partial, what a decline is (data versus an error), and whether a charge is guarded by a durable intent and an idempotency key. HH Pro is right about the decline (an error value would throw away the record, which is exactly the argument in its own CLAUDE file) and has no answer at all on intent and idempotency, because nothing crosses a network. The ERP plan has both and explains the failure mode the intent prevents: charge succeeded, local commit failed.

### C28: payment method custody

The ERP stores only a gateway vault token plus display metadata and says so as a compliance posture; HH Pro stores a label and says it is a prototype and stays one. These are compatible: the unified shape is the ERP's, and HH Pro's `kind` maps onto the ERP's method type. The one genuine gap is that HH Pro's simulator has a method id that always declines, which is a demo affordance with no ERP counterpart (`/home/user/hh_pro_dibbits/CLAUDE.md`, Tracking and AR).

## 3.7 Identity

| Concern | HH Pro | HardscapeOS | Agreement or conflict |
|---|---|---|---|
| Contractor crew role | `TeamRole = 'owner' \| 'pm' \| 'ap' \| 'field'` (`src/core/domain/team.ts`) | `portal_users.role` in `('account_admin','buyer','buyer_no_pay','ap_finance','field_crew')` (`0019` widened by `0160`), and `0160` says in its own header that the two new values exist because "the HH Pro portal models exactly that split (`pm` and `ap`), and until the ERP can express it, the portal must either render a role picker the server cannot enforce, the permission-gate-silently-off bug class, or lie about who its people are" | **C29.** Partly reconciled already, deliberately, from the ERP side |
| A second, conflicting role field | `User.role = 'owner' \| 'estimator' \| 'field'` (`src/core/domain/account.ts`) | none | **C29.** HH Pro carries two disagreeing contractor role enums in its own domain |
| Capability grain | six capabilities: edit scope, move stage, customer quote, pay, confirm pickup, manage team (`src/core/domain/team.ts`) | nine capabilities, never stored, always derived in Go: manage users, view billing, pay invoices, manage payment methods, submit RFQ, create orders, view orders and deliveries, edit delivery instructions, manage files (`backend/internal/portal/rbac.go`) | **C29.** The adapter already carries both grains side by side: `mapCapabilities` collapses as a conjunction and `SupplierIdentity.erpCapabilities` keeps the uncollapsed answer (`src/core/supplier/port.ts`, `erp-map.ts`) |
| Where the gate runs | in the actions layer, so buttons, board drags and every assistant tool pass one `requireCapability` and get one refusal sentence naming who can do it instead (`src/core/actions/team.ts`, `/home/user/hh_pro_dibbits/CLAUDE.md`) | in the server, re-derived from the database on every request, with route guards as the coarse first line and the fine check in the service (`backend/internal/portal/rbac.go`; `backend/internal/quote/acceptcontrols.go` header states the posture) | Compatible in principle, and the portal port already refuses to re-express the server's role as a local one: `SupplierIdentity.role` carries "the supplier's OWN role code... never re-expressed as a HH Pro `TeamRole`" |
| Default when no crew exists | everything is allowed: "With no team seeded, everything is allowed. The gate exists once people exist" (`/home/user/hh_pro_dibbits/CLAUDE.md`) | an unknown role grants nothing (`CapabilitiesFor`: "An unknown role grants nothing") | **C31.** Fail-open versus fail-closed, stated explicitly on both sides |
| Who you are, per surface | per window, deliberately: the crew store is not adopted across tabs, because "`activeId` is who YOU are acting as in THIS tab" (`/home/user/hh_pro_dibbits/CLAUDE.md`) | per session, re-loaded from the database per request so a demotion takes effect immediately (`docs/contractor-portal-plan.md` section 3.1) | Compatible; the ERP is stricter |
| Staff identity | none | 13 business roles across 3 tiers with a global or branch scope, plus a platform role above them (`backend/internal/identity/rbac.go`), branch grants (`0004`), a margin gate, a credit gate, and a four-eyes approval and override queue | **C30** |
| Token custody | the port has no token field by construction, because a token readable above the port would land in the persisted stash or ride a cross-tab storage event (`src/core/supplier/port.ts`) | JWT with a separate portal audience, a configured session ceiling, and one role whose ceiling is a dealer knob (`backend/internal/identity/rbac.go`, the drivers-role constant) | Compatible |

### C29: contractor roles

Three vocabularies for one idea, two of them inside HH Pro. The reconciliation is already half-done and was done by the ERP: `0160` added two roles specifically to express HH Pro's split, and the portal's adapter already refuses to invent a local role. What each side loses: the ERP vocabulary wins and HH Pro's `TeamRole` becomes a display mapping over a server role, and `User.role` (the third enum) has to go; the portal vocabulary wins and the ERP loses `account_admin` versus `buyer` as distinct, and loses the file-management capability, which no HH Pro capability covers.

### C30: staff roles

The portal has no notion of dealer staff at all. In a merged monorepo with role-based micro-apps (context section 4.5, "micro-app per role, shell per audience"), staff roles are the axis that decides which micro-app a person gets, so the unified identity model is the ERP's, with the contractor roles as a tenant-scoped role set beside it. Context-file bearing: section 4.4 requires org resolution against team membership before a database is touched, and section 3 maps roles onto platform teams (`org:<slug>` with operator, org admin, reviewer, member; `tenant:<org>:<slug>` for dealers and customers). Neither repository's role set maps onto that shape at present.

### C31: the fail-open default

HH Pro's default exists so that an embed of the core without the crew feature costs nothing, and it is defended by tests that never had to change. The ERP's default is fail-closed and is argued as the bug class it prevents. What each side loses: fail-closed wins and HH Pro's "no crew seeded" path must seed an implicit owner, which is a behaviour change in the demo and in any embed; fail-open wins and a deployment that fails to load a role grants every capability, which the ERP's own admin gate treats as unacceptable ("undefined must never mean 'no check'", `/home/user/hh_pro_dibbits/CLAUDE.md` on the admin console, describing the same discipline).

## 3.8 Platform conventions against the context file

| Convention (context section 4.4 and 4.5) | HH Pro | HardscapeOS | Conflict |
|---|---|---|---|
| `org_id` on org tables | not found | not found in any of the 196 migrations (`grep` for `org_id` and `tenant_id` over `backend/migrations/` returns nothing) | **C33** |
| `tenant_id` on tenant-scoped tables | not found; tenancy is implicit (one contractor per browser) | tenancy is implicit too, and enforced as a `customer_id` predicate on every portal query (`docs/contractor-portal-plan.md` section 3.1, "tenant isolation by `customer_id` with cross-tenant not-found"); staff identity is documented as single-org with no tenant column | **C33** |
| Per-scope monotonic `seq` on ordered streams | not found | only `audit_log.seq` (`0078`), added for one reader and with an explicit warning not to over-trust the backfilled values | **C34** |
| Cursor pagination, never offset | `SupplierPage { items, total, limit, offset }` (`src/core/supplier/port.ts`), mirroring the server | `httpx.Page[T] { Items, Total, Limit, Offset }` (`backend/internal/platform/httpx/httpx.go`) | **C34.** Both sides are offset-based; the context file forbids it |
| An `audit` table recording every privileged action | not found; `activityStore` is an unread-count feed | `audit_log` as middleware over the whole mux plus an explicit sensitive-access recorder (`backend/internal/audit/model.go`), an outbox that doubles as a replay log (`0013`), and a second domain trail for pricing (`0023`, `0114`, `0133`) | **C36** |
| Tombstone deletes | hard delete with an undo affordance on the toast (`/home/user/hh_pro_dibbits/CLAUDE.md`, mobile pass); persistence is whole-store replacement | flag, do not delete on masters (`is_active`), append-only ledgers with compensating entries rather than corrections (`docs/design/DATA_MODEL.md` sections 0 and 5); no tombstone column | **C35** |
| Branch scoping enforced in middleware | not found | `branch_id UUID NOT NULL` on every operational table from birth, repositories filter by the acting branch, and the rule is "no query without a branch predicate on branch-scoped tables", checked in CI (`docs/design/DATA_MODEL.md` section 0; `docs/design/MODULE_FRAMEWORK.md` section 3) | **C32** |
| Attachments | a signature data URL inside a domain record; assistant attachments pruned to text placeholders after a turn | a photo reference on a task (`0073`), dispatch proof-of-delivery photos (`0091`), invoice documents (`0146`), masked evidence references (`0042`) | **C37.** No unified attachment entity on either side |

---

# 4. The two pricing models, field by field

Sources read for this section.

**HH Pro:** `src/core/sim/pricing.ts` (the engine), `src/core/selectors/pricing.ts` (the single binding of engine plus acting account), `src/core/domain/catalog.ts` (`PriceQuote`, `VolumeBreak`), `src/core/domain/project.ts` (`PriceSource`, the lapse predicates), `src/core/domain/customer-quote.ts` (the proposal validity cap), `src/core/domain/stage.ts` (the guard that reads all of it), `src/core/data/account-seed.ts` (the rule data), `src/core/sim/quote-desk.ts` and `src/core/sim/config.ts` (where a desk price and its validity window come from), and the two defending tests in `src/core/selectors/__tests__/catalog.test.ts` under the heading "every catalog price comes from the ERP engine".

**HardscapeOS:** `backend/internal/pricing/` in full (model, service, repository, handler, counter, promo, rebate, uomplan, override authority, audit reference, price-file import, batches), the pricing migrations `0023`, `0024`, `0025`, `0026`, `0039`, `0098`, `0111`, `0112`, `0113`, `0114`, `0133`, `0198`, the tax engine `0042` and `backend/internal/tax/model.go`, the counter consumers `backend/internal/pos/counterprice.go` and the margin glance in `backend/internal/pos/model.go`, the quote and procurement pricing hooks (`backend/internal/procurement/service.go`), and the frontend consumers `app/src/price-breakdown.ts`, `app/src/pricing-words-logic.ts`, `app/src/customer-pricing.ts`, `app/src/quantity-breaks.ts`, `app/src/promo-*.ts`, `app/src/price-*.ts`.

## 4a. Concept table

| Concept | HH Pro (name, path, shape) | HardscapeOS (name, path, shape) |
|---|---|---|
| List price | `Product.listPrice: Cents` plus optional `msrp`, a stored field on every product, `src/core/domain/catalog.ts`. Always present, so a discount always has an anchor | `pricing_list_prices(product_id PK, list_price_cents, price_uom)`, a pricing-owned table, `backend/migrations/0023_pricing.sql` block 1b. The migration states the anchor is "manufacturer GTA trade price plus FOB freight when given, else cost times 1.85". Most seeded products have no row: the resolver comment says "26 of 30 seeded products have no explicit anchor row" |
| Derived anchor when list is missing | not applicable | `applyMarkup(in.CostCents, retailMarkupBps)` inside `resolve`, `backend/internal/pricing/service.go`. Denominated in the product base unit. With neither an anchor nor a cost, the anchored calculations return `errNoListPrice` rather than computing off zero |
| Account or customer tier | `PricingTier { id, name, percentOffList }` as an entity, referenced by `Account.pricingTierId`, `src/core/sim/pricing.ts` and `src/core/domain/account.ts`. Three seeded tiers in `src/core/data/account-seed.ts` | `customers.customer_type` as an attribute with five allowed values (`0006`), carried into the resolver as `ResolveInput.CustomerType`, `backend/internal/pricing/model.go` |
| Tier baseline discount | `PricingTier.percentOffList`, applied when no category rule matched, `baseUnitPrice` in `src/core/sim/pricing.ts` | a `pricing_rules` row with `scope = 'TIER_DEFAULT'`, keyed on `customer_type`, resolved at the fourth waterfall level. The resolver comment marks it the dominant case ("about 99 percent") |
| Category rule | `PricingRule` variant `{ kind: 'category', tierId?, accountId?, categoryId, percentOffList }`, `src/core/sim/pricing.ts` | two distinct things: a `pricing_rules` row with `scope = 'TIER_CATEGORY'` or `scope = 'CUSTOMER_CATEGORY'`, and a separate table `customer_category_overrides(customer_id, category_id, calc_type, discount_bps or markup_bps, price_uom, notes, version)` with a unique key on the pair |
| Category cascade | present and load-bearing: `categoryChain` walks parents, `matchesCategory` returns depth, nearest depth wins, ties broken by the better discount, cycle-guarded. `categoryParentsFrom(categories)` must always be passed to `createPricingEngine`, `src/core/sim/pricing.ts` | **absent.** The resolver resolves exactly one category id through the catalog seam (`s.cat.GetCategory(ctx, in.ProductID)`) and matches rules on equality of that one id, `backend/internal/pricing/service.go` steps 1 and 4 |
| Contract or customer-specific SKU price | `PricingRule` variant `{ kind: 'contract', accountId, sku, unitPrice }`, an absolute price, `src/core/sim/pricing.ts` | `pricing_rules` with `scope = 'CUSTOMER_PRODUCT'` and `calc_type = 'ABSOLUTE'`. A database CHECK confines absolute prices to that one scope: `CHECK ( scope = 'CUSTOMER_PRODUCT' OR calc_type <> 'ABSOLUTE' )`, `0023` |
| Quantity break, absolute | `{ minQty, unitPrice }`, accepted **only** when the rule names a single sku; the engine drops an absolute break attached to a category, `volumeBreaksFor` in `src/core/sim/pricing.ts` | **removed from the schema.** `0026` drops the replacement-price column and states "a break is an adjustment, never a base price". The nearest survivor is `ABS_OFF`, a flat amount off the base |
| Quantity break, relative | `{ minQty, extraPercentOff }`, resolved against the product's own base price into an absolute unit price before it leaves the engine | `PCT_OFF` with `discount_bps`, applied on top of the resolved base, `backend/internal/pricing/model.go` and `service.go` step 6 |
| Quantity break, surcharge | not found | `SURCHARGE_PCT` and `SURCHARGE_ABS`, positive uplifts, added for the partial-skid case (charge more under a threshold), `0023` and `0026` |
| Break scope and threshold unit | by sku or by category id (with the cascade), threshold expressed in the product's own quantity | `quantity_break_headers(product_id XOR category_id, threshold_uom)` with tiers `[from_qty, to_qty)` in the header's threshold unit, bucketed after converting the entered quantity through the catalog conversion seam. Product-specific headers are preferred over category headers, `selectBreak` in `backend/internal/pricing/service.go` |
| Promo | not found | `pricing_promotions` (`0111`) with calculation types `PCT_OFF`, `AMT_OFF`, `FIXED_PRICE`, scoped and windowed, plus a clearance row that can carry a price or be a badge only. `backend/internal/pricing/promo.go` |
| Rebate | not found | `0112` plus `backend/internal/pricing/rebate.go`: per-vendor accrual on a sale, three rate bases (percent of sale, percent of cost, amount per unit in the product base unit), at-most-once per program enforced by a unique index |
| Cost, margin, floor | not found in the engine at all. The contractor's own margin on their proposal is a different number and lives in `computeQuoteTotals`, `src/core/domain/customer-quote.ts` | `ResolveInput.CostCents` supplied by the caller and basis-agnostic; `pricing_category_policy(margin_target_bps, margin_floor_bps, margin_enforcement_disabled)` and `pricing_product_policy(margin_enforcement_disabled)` (`0023`); the gate delegates to the identity module and a below-floor price is blocked unless a server-resolved authority clears it (`override_authority.go`, `0133`) |
| UOM-aware pricing | absent. One unit per product; a price is per `product.baseUom` | `products.price_uom` distinct from `products.base_uom`; every rule, override and break row carries its own `price_uom`; the resolver computes a result unit (a percentage discount is dimensionless so it resolves in the anchor's unit; an absolute or cost-markup price carries its own), converts the anchor and the cost into it, and reports `PriceUOM` and `QtyBase` on the result |
| Price expiry or hold | `ScopeItem.priceExpiresAt` per line, `isPriced(item, now)`, `isPriceExpired`, `needsQuoteDesk(items, now)` in `src/core/domain/project.ts`; `supplierPriceHorizon` caps the proposal in `src/core/actions/customer-quote.ts`; the desk stamps it from a configured validity window in `src/core/sim/config.ts` | rule-level `valid_from` and `valid_to` on `pricing_rules`; promotion windows; effective-dated price batches (`0039`); document-level validity on `quotes`; soft and hard stock holds (`backend/internal/quote/reserve.go`). **No per-line price-lapse concept** |
| Price source attribution | none on `PriceQuote`. A coarse provenance exists one level up on the line: `PriceSource = 'erp' \| 'quoted' \| 'estimate' \| 'unpriced'`, `src/core/domain/project.ts` | `Breakdown.Source` with seven values (`CUSTOMER_PRODUCT_RULE`, `QUANTITY_BREAK`, `CUSTOMER_CATEGORY_RULE`, `CUSTOMER_OVERRIDE`, `TIER_CATEGORY`, `TIER_DEFAULT`, `LIST`) plus `PROMOTION`, a human `Detail` string composed from the winning rule and any stacked break, and the rule's own `detail_text` column, `backend/internal/pricing/model.go` |
| Tax | absent from the engine and from the domain | a separate module. Reason codes, effective-dated rates, status-card exemption profiles whose relief binds only once evidence is attached, a per-line split through the single rounding authority, and a reconciliation to named GL accounts. `backend/internal/tax/model.go`, `0042`. Pricing "NEVER carries tax_cents (M5 owns tax)", `backend/internal/pricing/model.go` |
| Card or tender fee | `cardFeePercent` and `feeFor`, `src/core/domain/payment.ts` | `OntarioSurcharge`, `backend/internal/payment/surcharge.go`. Not part of pricing on either side |
| Currency | formatted as a foreign currency, `src/core/lib/money.ts` | CAD only, and the receive path converts a foreign purchase currency before any value reaches a move or the ledger, `backend/internal/inventory/model.go` |
| Rounding authority | `Math.round` at each step: `applyPercentOff` in the engine, `multiplyCents` and `applyPercent` in `src/core/lib/money.ts`, `itemExtended` in `src/core/domain/project.ts` | `roundCents` inside the resolver for the price itself, and one declared authority for every **split**: `moneyutil.AllocateLargestRemainder`, "the single rounding authority for both the per-line tax split and the landed-cost allocation", `backend/internal/platform/moneyutil/allocate.go`, whose contract is that the parts sum exactly to the total |
| Price change audit | none; the catalogue is seeded | `pricing_audit_log` with a create, update, delete and bypass action set, a named bypass reason, an actor, and a drill-through reference to the document being priced (`0023`, `0098`, `0114`, `0133`); plus `price_change_log` and staged batches (`0039`) |
| Price file import | none | per-vendor column-mapping profiles created lazily by the first real import rather than seeded as a guess, with the reasoning written into the migration, plus staged batches with a preview before commit (`0039`, `0198`, `backend/internal/pricing/pricefile_service.go`, `xlsx_reader.go`) |
| Next-break hint | `PriceQuote.nextBreak?: VolumeBreak`, surfaced only when the break is both a higher quantity **and** a better price than the contractor is paying now, `quote` in `src/core/sim/pricing.ts` | `CounterQuote.NextBreakQty`, `NextBreakUOM`, `NextBreakUnitCents`, `NextBreakSaveCents`, "present only when one exists AND it actually beats the price they are getting now", `backend/internal/pricing/counter.go`. On a counter-only struct |
| Retail comparison for the customer | `PriceQuote.listPrice` carried alongside "purely so the UI can show the saving", plus `savingsPerUnit` and `discountPercent` helpers | `CounterQuote { RetailCents, CustomerCents, SavingCents, SavingBps, Basis }` where the subtraction is done server-side beside the waterfall, and `Basis` is one of `CONTRACT`, `ACCOUNT`, `PROMO`, `BULK`. The struct carries no cost, no margin and no floor, by construction |

## 4b. The resolution order on each side, quoted from code

### HH Pro, `src/core/sim/pricing.ts`

The doc comment above `createPricingEngine`, verbatim except that the original's em dash before "most specific wins" is rendered as a comma here:

```
Precedence, most specific wins: contract price > account category rule >
tier category rule > tier baseline > list. Volume breaks are evaluated last
and only apply if they beat whatever the rules produced.
```

The code that implements it, `baseUnitPrice`, in order:

1. `contractPrice(product, account)`: first rule with `kind === 'contract'`, matching account id and matching sku. Returns an absolute unit price.
2. Category rules, scanned in one pass, tracking two candidates: `accountPercent` at `accountDepth` for rules bound to the account, and `tierPercent` at `tierDepth` for rules bound to the tier with no account. Within each, the nearer category depth wins and at equal depth the better discount wins. Then: `const bestPercent = accountPercent ?? tierPercent` with the comment "An account-specific rule outranks a tier rule regardless of depth, it was negotiated for this contractor."
3. `const tier = tierById.get(account.tierId); return tier ? applyPercentOff(product.listPrice, tier.percentOffList) : product.listPrice;`

Then, in `quote`:

4. `volumeBreaksFor(product, base, hasContractPrice)` resolves every applicable break to an absolute unit price for this product. A category-scoped break is skipped when a contract price won (`if (hasContractPrice && !bySku) continue;`), and an absolute break is kept only when the rule named the sku (`if (bySku) found.push(...)`).
5. Best qualifying break: `for (const brk of breaks) { if (qty >= brk.minQty && brk.unitPrice < unitPrice) unitPrice = brk.unitPrice; }`. A break therefore **replaces** the base, and only when it is strictly cheaper.
6. `nextBreak` is the first break with a higher minimum quantity and a better price than the one now in force.

### HardscapeOS, `backend/internal/pricing/service.go`

The doc comment above `ResolvePrice`, verbatim except that em dashes are rendered as commas:

```
The BASE price is resolved first-match-wins over the rule levels:

  CUSTOMER_PRODUCT > CUSTOMER_OVERRIDE / CUSTOMER_CATEGORY > TIER_CATEGORY >
  TIER_DEFAULT (~99%) > LIST (retail anchor).

A matching quantity break is then applied as an ADJUSTMENT ON TOP of that base, bulk breaks
STACK on the tier-resolved price (they no longer COMPETE as a waterfall level). So a contractor's
-18% Pavers base then a -5% full-skid break yields retail x 0.82 x 0.95, not retail x 0.95, and a
partial-skid surcharge uplifts the discounted base. PROVISIONAL: the stacking model plus the break%
and threshold values are pending client confirmation. A negotiated CUSTOMER_PRODUCT contract price is
FINAL, no break (or override) applies on top of it.
```

The code that implements it, `resolve`, in order:

1. Resolve the category through the catalog seam. A single id, no ancestor walk.
2. Resolve the retail anchor: an explicit `pricing_list_prices` row, else a cost-derived provisional anchor in the product base unit, else no anchor at all (and then the anchored paths refuse).
3. Fetch candidate rules in one query.
4. Select the base level, first match wins, with quantity breaks and the customer override fetched **lazily** only when no contract price matched, because a contract price is final. Within a level, `selectRule` applies a total order: priority descending, validity start descending, id ascending.
5. Compute the result price unit, convert anchor and cost into it, and apply the base calculation (`ABSOLUTE`, `PCT_OFF_LIST`, or `MARKUP_ON_COST`), refusing rather than discounting off a zero anchor.
6. Promotion and clearance (`0111`, `backend/internal/pricing/promo.go`): a promotion competes for base selection directly below the contract price and above the whole override, tier and list waterfall, but is computed off the **retail anchor**, not off the tier-resolved base, and wins only when it beats that base. The file states the reason in full, quoted with dashes removed: "passing finalCents here compiles, passes a naive test, and quietly gives a contractor 18% off AND 25% off... '20% off' means 20% off the shelf price, that is what the sign in the yard says." When the tier base wins, the source stays the tier level and the detail records that the promo was considered and did not beat it.
7. Apply the single matching quantity break as an adjustment on the resolved base: `PCT_OFF` and `ABS_OFF` discount it (a flat discount is floored at zero, never negative), `SURCHARGE_PCT` and `SURCHARGE_ABS` uplift it. The source stays the base level; the break is recorded in the detail string.
8. Quantities and line total in the result price unit, pre-tax.
9. Margin gate on the **final** resolved price, after promotion and after the break. Either enforcement is disabled (and a bypass row is written unless this is a read-only preview), or the gate runs; a below-floor result is blocked unless a server-resolved override authority clears it, in which case the authority and the reason are recorded on the breakdown and on the audit spine.

### The orders side by side

| Level | HH Pro | HardscapeOS |
|---|---|---|
| 1 | contract price on this sku for this account (absolute) | `CUSTOMER_PRODUCT` rule (absolute, and final) |
| 2 | account category rule, nearest ancestor wins | promotion or priced clearance, if it beats the base below |
| 3 | tier category rule, nearest ancestor wins | `CUSTOMER_OVERRIDE` (the customer plus category table), else `CUSTOMER_CATEGORY` rule |
| 4 | tier baseline | `TIER_CATEGORY` rule |
| 5 | list | `TIER_DEFAULT` rule |
| 6 | volume break replaces the result if strictly cheaper | `LIST` (the retail anchor) |
| 7 | (none) | quantity break stacks on whatever won, up or down |
| 8 | (none) | margin gate on the final number, with a server-resolved override |

## 4c. The output shape on each side, field by field

| Field | HH Pro `PriceQuote` (`src/core/domain/catalog.ts`) | HardscapeOS `Breakdown` (`backend/internal/pricing/model.go`) | HardscapeOS `CounterQuote` (`backend/internal/pricing/counter.go`) | HardscapeOS `LinePrice` (`backend/internal/pricing/service.go`) |
|---|---|---|---|---|
| identity | `sku: string` | none; the caller knows the product | `ProductID` | none |
| the price | `unitPrice: Cents` | `FinalCents` | `CustomerCents` | `UnitPriceCents` |
| the anchor | `listPrice: Cents` | `OriginalCents` (the anchor converted into the result unit) | `RetailCents` | none |
| the saving | derived by `savingsPerUnit` and `discountPercent` on the client | `DiscountBps` (negative when the net effect was a surcharge) | `SavingCents`, `SavingBps`, both omitted when zero | none |
| why this price | none | `Source`, `Detail`, and `PromoCode` plus `PromoName` when a promotion won | `Basis`, one of `CONTRACT`, `ACCOUNT`, `PROMO`, `BULK` | none |
| the unit | implicit (`product.baseUom`) | `PriceUOM` | `PriceUOM` | `PriceUOM` |
| quantity context | `qty: number` (the quantity this quote was computed for) | `QtyBase` (entered quantity converted to the product base unit) | input only | none |
| extended total | not on the quote; `itemExtended` computes it on the line | `LineTotalCents`, pre-tax | none | `LineTotalCents`, pre-tax |
| the next opportunity | `nextBreak?: VolumeBreak { minQty, unitPrice }` | none | `NextBreakQty`, `NextBreakUOM`, `NextBreakUnitCents`, `NextBreakSaveCents` | none |
| dealer economics | none, by construction | `MarginBps`, `FloorBps`, `Blocked`, `BlockPolicy`, `BypassReason`, `OverrideRequested`, `OverrideAuthority` | **absent by construction**, and the file says so: "A CounterQuote carries retail, the customer's price and the difference. Margin, floor, profit and cost are NOT on this struct, not gated, not omitempty, ABSENT" | none |
| clearance badge | none | `Clearance`, `ClearanceReason`, deliberately independent of whether a clearance price was applied | none | none |
| unpriceable | not representable: every product has a list price, so the engine always returns a number | `errNoListPrice` from the resolver | `Unpriceable bool`, so "the counter renders no price rather than a $0.00" | error |
| tax | none | never, by stated rule | never | never |

## 4d. Pricing conflicts

Each entry: what each side does, what HH Pro's contractors lose if the ERP model wins, what the ERP's counter and margin controls lose if the portal model wins, and the context-file convention that bears on it where one does.

### C38: base resolution order, and whether a promotion is a level at all

HH Pro has five base levels and no promotion. HardscapeOS has six and puts promotion second, computed off the retail anchor, guarded so it can never raise a price. Contractors lose, under the ERP model: nothing, they gain a sale price they could not previously be shown. The counter loses, under the portal model: the entire seasonal and clearance apparatus (`0111`, `backend/internal/pricing/promo.go`), including the never-worse guard whose absence the file calls "the single most embarrassing thing a pricing engine can do", and the clearance badge that POS search and quote lines render. Context bearing: none; this is the ADR the context file's section 12 already lists as pending ("Pricing-model resolution (first candidate from HH-1)").

### C39: the category cascade

HH Pro cascades: a rule on a parent category reaches every product beneath it, nearest ancestor wins, and the CLAUDE file calls out that not cascading "would silently drop the discount on every product that sits in a subcategory, which is most of them". The ERP does not cascade: it resolves one category id and matches on equality. The seeded HH Pro data depends on the cascade (`src/core/data/account-seed.ts` writes the account rule against the hardscape parent precisely so it reaches pavers, walls, steps and porcelain). Contractors lose, under the ERP model: a negotiated "eighteen percent off hardscape" stops reaching the products it was negotiated for, silently, and the dealer must author one rule per leaf category and keep authoring them as the catalogue grows. The counter loses, under the portal model: determinism, which the ERP bought deliberately: the unique index `uq_pricing_rules_active` in `0023` normalises null scope dimensions to sentinels so that "two genuinely-equivalent seeded rows" can never both be active, and `selectRule` applies a total order so the winner never depends on row order. Adding an ancestor walk adds a second ranking axis (depth) that the anti-collision index does not cover. Context bearing: none.

### C40: is the tier a customer attribute or a price level

HH Pro: `PricingTier` is an entity with an id and a baseline percentage, and the account points at it. HardscapeOS: there is no tier entity; `customers.customer_type` is an enum attribute and the "tier" is whichever `TIER_DEFAULT` or `TIER_CATEGORY` rows name that attribute value. Contractors lose, under the ERP model: nothing visible, but the portal's `accountPricing()` binding (`src/core/selectors/pricing.ts`) has to stop reading a tier id and start reading a customer type, and that binding exists because a hardcoded tier was "already wrong for any contractor not on Pro". The counter loses, under the portal model: the ability to give one customer a different baseline without minting a tier entity, and the five-value customer-type axis that the tax and credit paths also read. Context bearing: none.

### C41: the customer-plus-category override as its own level

HardscapeOS ranks `CUSTOMER_OVERRIDE` (a row in `customer_category_overrides`, unique per customer and category, with its own optimistic version and a notes field) alongside and effectively above a `CUSTOMER_CATEGORY` rule; HH Pro expresses the same idea as another `PricingRule` in the same flat list with `accountId` set. Contractors lose, under the ERP model: nothing. The counter loses, under the portal model: a distinct, versioned, individually editable and individually auditable per-customer override with a notes column, which is the thing a sales manager actually edits.

### C42: the retail anchor

HH Pro: `listPrice` is a column on the product, always present. HardscapeOS: the anchor is pricing-owned, usually absent, derived from cost when absent, and the resolver refuses to price at all when neither exists, with a comment naming the defect that rule closed ("never discount off a $0 anchor"). Contractors lose, under the ERP model: the saving story on any product with no anchor and no cost, because the row simply cannot be priced and the counter renders nothing rather than a number; the portal's catalogue would have unpriceable rows, which at present it cannot represent. The counter loses, under the portal model: zero-trust table ownership (the anchor would move onto the catalogue's `products` table, which `0023`'s header explicitly refuses to do), the cost-derived fallback, and the refusal that prevents a discount off nothing. Context bearing: none.

### C43: does a quantity break replace the base or stack on it

The sharpest conflict in the section, and both sides changed their minds toward it from opposite directions.

- HH Pro: breaks are resolved to absolute unit prices and the best qualifying one **replaces** the base when strictly cheaper (`quote` in `src/core/sim/pricing.ts`).
- HardscapeOS: breaks **stack** as an adjustment on the resolved base. `0026`'s header records the change: "a quantity break no longer COMPETES at a precedence level (it used to REPLACE the customer's tier/category discount)". The resolver comment gives the arithmetic: an eighteen percent base then a five percent full-skid break yields retail times 0.82 times 0.95.

Both were bugs once, and they are not the same bug. Contractors lose, under the ERP model: nothing in most cases, and they gain the bigger discount (stacking is cheaper than replacing whenever the base already beat the break). But HH Pro's `nextBreak` arithmetic changes completely, because a stacked break is always an improvement and the "the first break is worse than what you already pay" case the portal test pins (`src/core/selectors/__tests__/catalog.test.ts`, "surfaces the next volume break, and only when it is actually cheaper") stops existing, so a portal that migrates without re-deriving that hint will advertise a break that is not the price the ERP would charge. The counter loses, under the portal model: the fix `0026` exists to hold, namely a bulk break wiping a contractor's negotiated tier discount, and the partial-skid surcharge, which has no meaning at all in a best-of model (an uplift can never win a "cheapest wins" contest). Context bearing: none.

### C44: break vocabulary, scope and threshold unit

HH Pro: two shapes, and an absolute price is only trusted on a rule that names one sku, with the reason stated at length ("a category spans a $5 stud and a $30 post"). HardscapeOS: four shapes, none of them a replacement price, thresholds expressed in the header's own unit and bucketed as a half-open interval after conversion, with a missing conversion surfaced as a hard error rather than silently dropping the break ("a misconfigured break must fail loud, never silently undercharge"). Contractors lose, under the ERP model: nothing, and the absolute-break hazard disappears because absolute break prices no longer exist. The counter loses, under the portal model: the surcharge vocabulary, the threshold unit (so a weight-based partial-skid threshold cannot be expressed at all), the exactly-one-tier selection, and the loud failure on a missing conversion. Context bearing: none.

### C45: does a contract price admit a quantity break

Directly contradictory, and both sides wrote the reasoning down.

- HH Pro, `volumeBreaksFor`: a category-wide break is skipped on a contracted sku ("or the dealer is being double-dipped"), but "A break written for THIS sku is different: that is a deliberate volume tier on the contracted item, so it still applies."
- HardscapeOS, `ResolvePrice`: "A negotiated CUSTOMER_PRODUCT contract price is FINAL, no break (or override) applies on top of it", and the implementation does not even fetch the breaks on that path.

Contractors lose, under the ERP model: a volume tier their rep deliberately wrote against the contracted sku stops applying, which in the seeded HH Pro data is a live case (a contract price on the paver they lay most sits in a category that also carries volume breaks, `src/core/data/account-seed.ts`). The counter loses, under the portal model: the finality of a negotiated price, which the promo file treats as a promise ("a 'sale' that changes it is a broken promise in either direction"), and the single-round-trip optimisation the finality buys.

### C46: cost, margin target, floor, the gate and override authority

Absent from HH Pro entirely. Present on the ERP side as: a caller-supplied cost basis, a per-category target and floor with a per-category and per-product enforcement disable, a gate delegated to the identity module, a blocked result the caller must render, a server-resolved override authority with two named sources, and a bypass row written for every skipped or cleared gate so there is no silent bypass. The ERP file records why the override was moved server-side: asking the gate with the caller's flag "let a request body clear the floor". Contractors lose, under the ERP model: nothing directly, but the portal gains a price state it cannot currently represent (blocked), and its catalogue browse would have to handle a row whose price is refused pending a manager. The counter loses, under the portal model: all of it, including the audit answer to "how many below-floor prices went out and on whose authority", which `0114` and `0133` exist to make a query rather than an archaeology exercise. Context bearing: the context file's section 4.4 requires an audit row for every privileged action; a below-floor override is one, and only the ERP side records it.

### C47: price source attribution

HH Pro's `PriceQuote` says what the price is and not why. The ERP's `Breakdown` says why, in a machine field and a human string, and the counter reduces it to a one-word basis chosen server-side "so the wording cannot drift between screens". Meanwhile HH Pro has a provenance the ERP lacks: `PriceSource` on the line distinguishes an engine price, a desk-quoted price, the contractor's own estimate and an unpriced line, and the whole quote-desk gate is built on it. Contractors lose, under the ERP model: nothing, they gain an explanation. The counter loses, under the portal model: the explanation, and with it the low-margin report's ability to tell a promotion-driven thin line from a mis-authored tier rule, which `backend/internal/pricing/promo.go` names as a reason the promotion source is distinct. Note this is not symmetrical: the unified model needs **both** attributions, because they answer different questions (which rule won, versus who stood behind the number).

### C48: unit-aware pricing and the result price unit

HH Pro prices per the product's base unit and has no second unit. The ERP has a price unit distinct from the base unit, a price unit on every rule, override and break row, and a rule for which unit the result lands in: a percentage discount is dimensionless so it resolves in the anchor's unit, and only an absolute or cost-markup price carries its own. The resolver then converts the anchor and the cost into that unit and reports it. Contractors lose, under the ERP model: nothing; they gain correct per-unit prices for the area, weight and volume units this vertical sells in, which is the stated point of `src/core/domain/units.ts` in the first place. The counter loses, under the portal model: the ability to price a pallet-quoted product per square foot, which is described as "the client's number one pain" in the resolver comment. Context bearing: none.

### C49: percent as a float, or basis points as an integer

HH Pro: `percentOffList: number` and `extraPercentOff: number`, applied as `Math.round(listPrice * (1 - percent / 100))`. HardscapeOS: `discount_bps INTEGER` with a defence-in-depth range check (`0025`, and again in `0026` for breaks) so "a stray write can never land a 150%-off" value. Contractors lose, under the ERP model: nothing. The counter loses, under the portal model: the integer invariant and the range constraints, and a float percentage in a `DECIMAL` world reintroduces the representation question the money rule already settled.

### C50: who owns rounding

HH Pro rounds at every step with `Math.round` and has no allocation concept at all. The ERP rounds the price with its own helper and has exactly one authority for every **split**, whose contract is that the parts sum exactly to the total with no penny lost or invented, used by the per-line tax split, the landed-cost allocation, credit memos and the invoice writer. Contractors lose, under the ERP model: nothing. The counter loses, under the portal model: the single split authority, and with it the guarantee that a per-line tax breakdown adds up to the invoice tax. Context bearing: none directly, but a ledger that must reconcile requires the stronger rule.

### C51: where tax applies

HH Pro has no tax. The ERP keeps tax strictly out of pricing ("NEVER carries tax_cents"), applies it at the document, splits it per line through the rounding authority, and gates relief on evidence. The portal's adapter currently compensates by mapping the tax-inclusive total into a field named `subtotal`. Contractors lose, under the ERP model: nothing, they gain an honest tax line instead of a misnamed field. The counter loses, under the portal model: the whole tax engine, including the evidence gate whose absence would let a caller claim a reduced rate with no proof. Context bearing: none directly.

### C52: is price validity a domain concept

HH Pro says yes and makes it re-block an order (C17). The ERP has validity on rules, promotions, batches, quote documents and stock holds, but nothing per line. Contractors lose, under the ERP model: the guard that stops them committing to a number nobody is standing behind any more, and the cap that stops them promising a homeowner longer than the dealer is holding (`supplierPriceHorizon`). The counter loses, under the portal model: nothing it has, but it gains an obligation to stamp and honour a per-line validity bound.

### C53: the next-break hint

Both sides have one and both apply the same honesty rule (only show it when it actually beats what you pay now). The difference is exposure: HH Pro's rides on the object every catalogue row already holds; the ERP's rides on a counter-only struct that no portal route serves. The unified model has to decide whether the contractor-facing price object carries it. Note the interaction with C43: under a stacking model the hint's arithmetic is different, so this is not a pure plumbing question.

### C54: price change audit, batches and file import

ERP-only, and substantial: staged batches with a preview before commit, an append-only change log, per-vendor import profiles created lazily by a real file rather than seeded as a guess (with three specific failure modes written into `0198`'s header), an anchor-derivation policy with named placeholder values, and a variance alert threshold. HH Pro has none of it because its catalogue is a seed. Nothing is lost by adopting it; the note here is that a unified model must carry it, and that its values are marked provisional pending a client answer, which means the merged system inherits an open question, not a settled number.

### C55: who computes a portal price, and is it authoritative

HH Pro computes every catalogue price in the browser, through one binding, and the two defending tests are structural as well as behavioural: one compares every catalogue row against the engine at four quantities, and the other greps the whole source tree for discount arithmetic outside the engine, with the comment that a shortcut "agrees on 40 of the 45 products, it is wrong exactly where the commercial relationship lives" (`src/core/selectors/__tests__/catalog.test.ts`). The ERP requires a resolve round trip with a caller-supplied cost basis, and the portal path that exists at present prices a request for quote **indicatively**, with a provisional zero cost, never authoritative: `provisionalCostCents int64 = 0` with the comment "There is no ERP cost source yet, so it is provisional (0) and EVERY derived price is INDICATIVE, never authoritative" (`backend/internal/procurement/service.go`). Contractors lose, under the ERP model: a price on a catalogue row without a network round trip, and (until a cost source exists) an authoritative price at all on the request-for-quote path. The counter loses, under the portal model: the margin gate, because a price computed in a browser cannot be blocked by a floor the browser must not know, and the zero-trust posture that keeps the dealer's cost off the wire. Context bearing: section 4.5's offline scopes include `price_matrix` in the contractor frontend's read-cache scope, which implies the client holds enough to resolve a price locally; that is the portal model, and it collides with the margin gate unless the cached matrix is the resolved output rather than the rule inputs.

### C56: rebates and vendor-side money

ERP-only (`0112`, `backend/internal/pricing/rebate.go`). Never contractor-facing, and the accrual invariant (at most once per program, enforced by a unique index, with the constraint firing treated as success) is a property of the sale path, not of the portal. The note here is only that a merged pricing module owns it, so a unified order-commit path must fire the accrual.

---

# 5. Proposed unified model

Expressed in the context file's terms (section 4.4): `org_id` on org tables, `tenant_id` on tenant-scoped tables, a per-scope monotonic `seq` on ordered streams, an `audit` table for every privileged action, tombstone deletes, cursor pagination. Types are the ERP's conventions unless a row says otherwise, because those conventions are already binding on 196 migrations and the context file's section 4.1 fixes the data runtime as PostgreSQL.

**Nothing here resolves a conflict.** Where a field depends on an unresolved one, both candidate shapes are listed and the row says so. Source is `HH` (HH Pro), `HOS` (HardscapeOS), `both`, or `new` (required by the context file and present in neither).

Two scoping notes that apply to every table below and are not repeated in each:

- `org_id UUID NOT NULL` on org-owned rows and `tenant_id UUID` on rows belonging to a dealer's customer. Neither column exists in either repository at present (C33). Which tables are org-scoped and which are tenant-scoped is itself a decision: a product is org data, a plan is tenant data, an order is org data with a tenant reference.
- `deleted_at TIMESTAMPTZ` as the tombstone, alongside the ERP's existing `is_active` flags rather than replacing them: the flag is a business state ("we no longer sell this") and the tombstone is a sync fact ("this row is gone"), and collapsing them loses one of the two (C35).

## 5.1 Identity and scope

### `org`

| Field | Type | Source | Note |
|---|---|---|---|
| id | UUID | new | The dealer organisation. Neither repository has one |
| slug | CITEXT UNIQUE | new | Context section 1 names orgs by slug |
| name | TEXT | new | |
| created_at, updated_at | TIMESTAMPTZ | both | |

### `branch` (the `locations` tree)

| Field | Type | Source | Note |
|---|---|---|---|
| id | UUID | HOS | `locations` (`0003`) |
| org_id | UUID | new | C33 |
| branch_id | UUID | HOS | Denormalised root; equals own id for a branch row |
| parent_id | UUID | HOS | Explicit ON DELETE, per convention |
| loc_type | TEXT CHECK | HOS | branch, zone, aisle, bin |
| code, name | CITEXT, TEXT | HOS | Unique per branch |
| address | JSONB | HOS | Carries the Ontario shape; HH Pro's `Address` carries a United States shape (C13) |
| is_active, deleted_at | BOOLEAN, TIMESTAMPTZ | HOS, new | C35 |

### `staff_user`, `staff_role`, `staff_role_grant`, `staff_branch_grant`

| Field | Type | Source | Note |
|---|---|---|---|
| (whole subtree) | as `0004` plus `0015` plus `0043` | HOS | 13 roles in 3 tiers with a global or branch scope, plus the platform role. Absent on the HH Pro side (C30) |
| org_id | UUID | new | C33. At present the ERP is single-org and says so |
| platform identity | text claim | new | Context sections 3 and 4.4 put identity in the platform and resolve the org from a header validated against team membership; neither repository does that at present |

### `tenant` (the dealer's customer, formerly `customers`)

| Field | Type | Source | Note |
|---|---|---|---|
| id | UUID | both | This row **is** the tenant for `tenant_id` |
| org_id | UUID | new | C33 |
| account_no | CITEXT UNIQUE | HOS | |
| name | TEXT | both | |
| customer_type | TEXT CHECK | HOS | C13, and C40: this is also the pricing tier under the ERP model. Candidate shape A: keep the enum and the rule rows. Candidate shape B: add `pricing_tier_id UUID` pointing at a tier entity (HH Pro model). Unresolved |
| terms | TEXT code or INTEGER | conflict | C13. HH Pro carries a code, the ERP an integer count |
| credit_limit_cents | BIGINT | HOS | C13 |
| on_hold | BOOLEAN | HOS | C13 |
| tax_exempt | BOOLEAN | HOS | C13, C51; extended by evidence-gated profiles |
| home_branch_id | UUID | HOS | C32 |
| billing_address | JSONB | HOS | |
| contractor_branding | JSONB or a separate table | HH | C14. Unresolved whether this is dealer-held tenant data or contractor device data |
| is_active, deleted_at, metadata | BOOLEAN, TIMESTAMPTZ, JSONB | HOS, new, HOS | |

### `tenant_user` (the contractor's people)

| Field | Type | Source | Note |
|---|---|---|---|
| id | UUID | both | |
| tenant_id | UUID | HOS as `customer_id` | Rename to the convention; the predicate is already enforced everywhere (C33) |
| email | CITEXT UNIQUE | HOS | |
| name | TEXT | both | |
| role | TEXT CHECK | conflict | C29. Candidate shape A: the ERP's five values (`0160` already added two to express HH Pro's split). Candidate shape B: HH Pro's four. HH Pro's second, disagreeing enum on `User` is dropped either way |
| status | TEXT CHECK | HOS | active, inactive, pending |
| capabilities | **not stored** | HOS | Derived in code, per `backend/internal/portal/rbac.go`. The unified model keeps that rule; the open question is the grain (six versus nine, C29) |
| device unlock | new | new | Context section 4.5 requires a cached session plus a device unlock for shared devices, and that the audit record names the unlocked user. Neither repository has it |

## 5.2 Catalogue and units

### `uom`

| Field | Type | Source | Note |
|---|---|---|---|
| code | CITEXT PK | HOS | Rows, not a union (C10). HH Pro's word table becomes data on this row |
| org_id | UUID | new | A dealer may add a unit |
| name, short_label, singular, plural, sold_sentence | TEXT | HH plus HOS | HH Pro's `UnitCopy` fields (`src/core/domain/units.ts`) promoted to columns so a screen never prints a code |
| dimension | TEXT CHECK | HOS | count, area, weight, volume, length |
| step_hint | DECIMAL | HH | From `qtyStep`; an affordance, not a rule |
| is_active, deleted_at | | | |

### `product`

| Field | Type | Source | Note |
|---|---|---|---|
| id | UUID | HOS | C2 |
| org_id | UUID | new | Global master within the org; no branch column (both sides agree) |
| sku | CITEXT UNIQUE | both | C21: whether synthetic special-order skus live here is unresolved |
| name, description | TEXT | both | |
| category_id | UUID | both | C39 decides whether the pricing engine walks the parent chain |
| brand_id or manufacturer | UUID or TEXT | conflict | HH Pro has an entity, the ERP a text facet it deliberately deferred |
| base_uom, price_uom | CITEXT FK | HOS | C48. HH Pro has only the first |
| facets (manufacturer, collection, color, finish, application, dimensions, pallet count, weight per unit, pieces per area, coverage per unit) | as `0002` | HOS | |
| presentation | TEXT CHECK | HH | C7. Derived at present; becomes authored data or stays derived, unresolved |
| spec_class | TEXT | HH | C7 |
| lead_time | INTEGER or absent | conflict | C6. Candidate A: a column, per HH Pro. Candidate B: absent, derived from incoming supply and transfer estimates, per the ERP |
| image or swatch | reference | conflict | C8. Candidate A: generated swatch, missing is an error. Candidate B: uploaded bytes, missing is an empty state |
| is_active, deleted_at, metadata | | | |

### `product_uom` and `uom_conversion_edge`

| Field | Type | Source | Note |
|---|---|---|---|
| product_id, uom_code, qty_in_base, is_base, role_hints | as `0002` | HOS | C12. Exactly one base row per product |
| conversion edges (from, to, factor, scope, round_up_at_pick, source marker) | as `0113` | HOS | C12. The weight leg and the handling-unit rule. Values are marked provisional in the source migration |
| rounding rules (sell increment, break-pallet allowed and fee, waste factor) | as `0002` | HOS | C12 |

## 5.3 Price matrix

Every row here is contested; the conflict numbers are the point of the table.

| Entity | Fields | Source | Note |
|---|---|---|---|
| `price_anchor` | product_id, org_id, price_cents, price_uom, effective window | conflict | C42. Candidate A: a pricing-owned row with a cost-derived fallback and a refusal when neither exists (ERP). Candidate B: a column on the product, always present (HH Pro) |
| `price_tier` | id, org_id, name, baseline_bps | conflict | C40. Exists only under the HH Pro candidate; under the ERP candidate the tier is `tenant.customer_type` and this table does not exist |
| `price_rule` | id, org_id, scope, tenant_id, customer_type, category_id, product_id, branch_id, calc_type, price_cents, discount_bps, markup_bps, price_uom, priority, valid window, detail_text, is_active, version | HOS | C38, C39, C40, C49. The anti-collision unique index on the normalised selection key comes with it |
| `price_override` | tenant_id, category_id, calc_type, discount_bps or markup_bps, price_uom, notes, version | HOS | C41 |
| `quantity_break_header` | id, org_id, code, name, product_id XOR category_id, threshold_uom, is_active, version | HOS | C44 |
| `quantity_break_item` | header_id, from_qty, to_qty, value_type, discount_bps, discount_cents, surcharge_bps, surcharge_cents, price_uom | conflict | C43, C44. The ERP vocabulary (four adjustment types) versus the HH Pro vocabulary (absolute on a sku, relative elsewhere, replacing the base) |
| `promotion` and `clearance` | as `0111` | HOS | C38 |
| `margin_policy` (category and product) | target_bps, floor_bps, enforcement_disabled | HOS | C46. Never leaves the dealer side |
| `rebate_program` and `rebate_accrual` | as `0112` | HOS | C56 |
| `price_change_log`, `price_batch`, `price_import_profile` | as `0039` and `0198` | HOS | C54 |
| `pricing_audit` | entity ref, action, field, old, new, actor, document ref, bypass reason, `seq` | HOS plus new | C36, C46. Gains a `seq` per the context convention (C34) |

## 5.4 Plan, quote, order, money

### `plan` (the board card) and `plan_line`

| Field | Type | Source | Note |
|---|---|---|---|
| grain | conflict | conflict | **C19.** Candidate A: the card is an order inside a project, one stage per card (HH Pro). Candidate B: the card is the project with lanes, committed artifacts not on the board (ERP). This is the single decision the rest of section 5.4 hangs on |
| id, org_id, tenant_id, project_id | UUID | both | |
| name | TEXT | both | |
| stage or lane | TEXT CHECK | conflict | C19 |
| fulfillment | TEXT CHECK | conflict | C4. Two values or three |
| requested date, delivery address, site instructions, purchase-order reference | | HH | The ERP keeps address and instructions on the dispatch stop |
| sort_order | INTEGER | HH | Board position |
| seq | BIGINT | new | C34. Required for the sync protocol |
| plan_line: product_id, description, snapshot, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, price_source, price_valid_until, is_special_order, notes, added_by, metadata | | both | C11 (the trio), C20 (whether a frozen snapshot exists), C21 (special-order encoding), C47 (`price_source`), C52 (`price_valid_until`) |

### `quote` (dealer to contractor) and `quote_line`

As `0008` plus the share, acceptance, reserve, lineage and follow-up surfaces in `backend/internal/quote/`, with:

| Field | Type | Source | Note |
|---|---|---|---|
| status | TEXT CHECK | conflict | C16 |
| valid_until | DATE or TIMESTAMPTZ | HOS | C17 |
| per-line price validity | TIMESTAMPTZ | HH | C17, C52. Absent on the ERP side at present |
| share token hash, per-link expiry, revoked flag, view and accept log | | HOS | C18 |
| acceptance: typed name, affirmative, consent text, accepted total, optional drawn mark | conflict | conflict | C18. The drawn mark is the contested field |
| soft hold linkage | | HOS | C17 |
| seq | BIGINT | new | C34 |

### `customer_quote` (contractor to homeowner)

HH Pro only (C15). Fields as `src/core/domain/customer-quote.ts`: markup percent, labour lines (with a rate type and a rate), overhead lines (flat or percentage), hide-line-prices flag, frozen line snapshot with presentation, contractor branding, customer contact, share token, validity bound capped by the supplier horizon, view count and first view, change requests, and the acceptance record. Adds `tenant_id`, `seq`, and a tombstone. The contested field is the drawn mark (C18) and the home of the contractor branding (C14).

### `order` and `order_line`

As `0008`, plus `tenant_id`, `seq`, a tombstone, and the C4 status decision. `order_line` carries the entered trio, allocated and fulfilled base quantities, `is_special_order`, `metadata`, and (unresolved, C20) a frozen product snapshot.

### `invoice`, `invoice_line`, `ar_transaction`

As `0009`. The contested additions are none: this is the one area where the ERP model is strictly larger and the portal model is a projection of it (C25). The portal-facing projection must stop calling a tax-inclusive total a subtotal.

### `payment`, `payment_allocation`, `payment_method`

As `0010` plus `0030`, with:

| Field | Type | Source | Note |
|---|---|---|---|
| method set | TEXT CHECK | HOS | Five tenders; HH Pro's two map in (C27) |
| partial allocation | allowed | HOS | C27. HH Pro refuses partial payment by design |
| decline representation | a payment row with a failure reason | both | C27. Compatible: the ERP's three-valued processor result is richer |
| intent row and idempotency key | new to both codebases, specified in `docs/contractor-portal-plan.md` section 4.2 | HOS plan | C27 |
| fee or surcharge | conflict | conflict | **C26.** Candidate A: credit only, capped, off by default (ERP). Candidate B: a configured flat percentage on any card (HH Pro) |
| vault token, brand, last4, expiry | | HOS | C28 |

## 5.5 Operations

| Entity | Fields | Source | Note |
|---|---|---|---|
| `stock_move` | as `0005`: product, from and to location, base and entered quantity, move type, unit cost, value, weight, source reference, reason, actor, branch, occurred stamp | HOS | C5. Append-only; corrections are compensating moves, not updates |
| `stock_balance` | product, location, branch, on hand base, allocated base | HOS | C5 |
| `cost_layer`, `product_branch_cost` | as `0005` | HOS | Feeds the cost basis the pricing gate needs (C46) |
| `yard_task`, `task_event`, `task_pick_line` | as `0073` and `0187`: type, order and line reference, branch, priority, status, claim, completion, photo reference, source | HOS | Absent on HH Pro. Needs `seq` (C34) |
| `purchase_order`, `po_line`, `po_receipt` | as `0011`; receipt lines are `stock_move` rows, with no duplicate line table | HOS | |
| `dispatch_run`, `dispatch_stop`, `manifest`, `manifest_version` | as `0012` plus `backend/internal/dispatch/model.go` plus `0091` | HOS | C24 |
| `attachment` | id, org_id, tenant_id, owner entity type and id, kind, storage reference, content type, size, masked label, created by, `seq` | new | **C37.** Neither side has this. The context file's section 4.5 names an `attachments_queue` in the offline store, which presupposes one entity |
| `audit` | actor, action, entity type and id, branch, changes, request id, `seq`, created stamp | HOS plus new | C36. The ERP's shape already matches the context requirement, including the sequence column; what it lacks is `org_id` and `tenant_id` |
| `event_outbox` | as `0013` | HOS | Written inside the business transaction; doubles as a replay log |
| `sync_state` and `outbox` (device-local) | as context section 4.5 | new | Not a server table; listed so the server-side `seq` obligation is visible |

---

# 6. Offline scope candidates

Vocabulary from the context file, section 4.5: **full** two-way sync is reserved for declared yard-style scopes; everything else is **read cache plus outbox**; the per-entity conflict policy is one of `server_wins`, `last_write_wins_by_field`, or `manual` (which surfaces a review card). The manifest sketch in section 4.3 already declares three frontends: a yard app in full mode with scopes for pick lists, receiving, inventory lookup and photos; a counter app in read-cache-plus-outbox mode with customers, quotes and orders; and the contractor app in read-cache-plus-outbox mode with quotes, price matrix and orders.

| Entity | Scope | Conflict policy | Reason (one line) |
|---|---|---|---|
| `org`, `branch` | read cache | `server_wins` | Configuration a device reads and never writes |
| `staff_user`, `staff_role` | read cache | `server_wins` | Authority is re-resolved server-side on every request by existing design |
| `tenant` (customer) | read cache plus outbox (counter) | `server_wins` | The counter edits a contact or an address; credit, hold and tax status are never client-decided |
| `tenant_user` | read cache | `server_wins` | A role change must take effect immediately; a device must never win that argument (C31) |
| `uom`, `uom_conversion_edge`, `product_uom` | read cache | `server_wins` | Reference data; a stale factor is a wrong quantity, so freshness matters more than write access |
| `product`, `category` | read cache | `server_wins` | Catalogue is authored centrally; the yard and the portal read it |
| `price matrix` (anchor, rules, overrides, breaks, promotions) | read cache | `server_wins` | Named in the context manifest for the contractor app. Note C55: caching the **rules** lets a client resolve a price the margin gate never saw, so the honest cached artifact is the resolved output per product and quantity band, not the inputs |
| `margin_policy`, cost layers, `rebate_*` | **neither** | not applicable | Dealer economics must never reach a device; `CounterQuote` already omits them by construction |
| `plan` and `plan_line` (the board) | full two-way | `last_write_wins_by_field` | It is the contractor's own draft, edited on a phone in a truck, and per-field merge is right because two people editing different lines is the common case, not a conflict |
| `quote` (dealer to contractor) | read cache plus outbox | `server_wins` for the document, `manual` for acceptance | The document is the dealer's; an acceptance recorded offline that collides with an expiry or a prior acceptance is exactly the review-card case (C16, C18) |
| `customer_quote` (contractor to homeowner) | full two-way | `manual` | It is contractor-owned and edited offline, but a signed acceptance must never be silently overwritten; the post-M8 review already records a cross-tab clobber of a signed acceptance as a real defect |
| `order`, `order_line` | read cache plus outbox | `server_wins` | Named in the context manifest for both the counter and the contractor app. The supplier owns the state machine; the outbox carries placement and reschedule requests |
| `invoice`, `ar_transaction` | read cache | `server_wins` | Accounting truth; a device may not author a balance |
| `payment`, `payment_allocation` | outbox only, never a cached write | `server_wins` | A charge needs a durable server intent and an idempotency key before it leaves; replay safety is the whole point (C27) |
| `payment_method` | read cache plus outbox | `server_wins` | Only a vault token round trip can add one; the device holds display metadata |
| `stock_move` | full two-way (yard) | `server_wins` | Append-only, so a merge is a concatenation; the server assigns the sequence and rejects a duplicate client id |
| `stock_balance` | read cache | `server_wins` | A projection; never written by a device |
| `yard_task`, `task_event` | **full two-way** | `last_write_wins_by_field` | The canonical yard-style scope: claimed, progressed and completed on a device with no signal, and the fields written are disjoint per actor |
| `task_pick_line` | full two-way | `last_write_wins_by_field` | Same as its task |
| `purchase_order`, `po_line` | read cache | `server_wins` | Authored centrally |
| `po_receipt` | full two-way (yard) | `manual` | Receiving is the context manifest's named yard scope; a quantity discrepancy between two receivers is a decision, not a merge |
| `dispatch_run`, `dispatch_stop` | full two-way (driver) | `last_write_wins_by_field` | The driver app is offline by nature; arrival, proof and notes are per-stop and per-actor |
| `manifest_version` | read cache | `server_wins` | Immutable snapshots |
| `attachment` | full two-way (queue) | `server_wins` | The context file already names an `attachments_queue`; bytes upload out of band and the row lands when they do |
| `audit` | **neither** (write-only, server side) | not applicable | A device may emit an audited action through the outbox, but never syncs the log back |
| `event_outbox` | **neither** | not applicable | Server-internal |

Two observations that cut across the table.

1. **Nothing on either side has a `seq` at present except one log** (C34), and the sync protocol in context section 4.5 is built on it (`GET /sync/{scope}?since={seq}`, server-assigned). Every scope marked full or outbox above implies a `seq` obligation on the server for that scope.
2. **Neither side has tombstones** (C35), and the protocol requires them ("deletes are tombstones"). HH Pro hard-deletes a line with an undo affordance on the toast; the ERP flags masters inactive and never deletes ledger rows. A tombstone column is additive to both.

---

# 7. Open conflicts list

Fifty-six conflicts, collected for a decision session. Sections 3 and 4 hold the evidence for each.

| # | Title |
|---|---|
| C1 | Currency and money presentation: a foreign currency formatter against CAD-only |
| C2 | Id scheme: prefixed short ids against UUID, and hashed tokens against a raw share token |
| C3 | Time: zone-free instants against a house-zone rule for period boundaries |
| C4 | Status representation, and three lossy status collapses the adapter already performs |
| C5 | Stock: a manufactured field on the product against a double-entry ledger with reservations |
| C6 | Lead time: a product attribute against incoming supply and transfer estimates |
| C7 | Presentation and substitution class: derived portal concepts with no ERP home |
| C8 | Imagery: generated swatches where missing is an error, against uploaded bytes where missing is an empty state |
| C9 | List price carried on the product row against a pricing-owned anchor (see also C42) |
| C10 | Unit vocabulary: a closed union of ten against rows, with three ERP units already unmappable |
| C11 | Quantity: a float and one unit against a fixed-scale decimal and the entered trio |
| C12 | Conversion, weight and handling-unit rounding: absent against two conversion tables and a fee |
| C13 | What an account is: a pricing key against a credit instrument and tax subject |
| C14 | Contractor branding has no home on the ERP side, and none in the App Shape as written |
| C15 | Two documents named quote, with different senders and different recipients |
| C16 | Quote lifecycle status sets, and whether re-acceptance is possible at all |
| C17 | Price validity grain: per line and re-blocking, against per document, plus stock holds |
| C18 | The acceptance artifact: a drawn mark against a deliberate refusal to fake one |
| C19 | What the board card is, and three different things named project |
| C20 | Frozen line snapshot against a live product reference |
| C21 | Special-order lines: a synthetic sku prefix against a boolean and a free-text line |
| C22 | Duplicate line merge: keyed on product id against product id plus unit plus line-work |
| C23 | Edit authority: a stage predicate against a service with an edit history |
| C24 | Delivery tracking: a flat event list (currently empty by choice) against routed, sequenced stops |
| C25 | Invoice shape: tax, holdback, lines and the AR subledger, all absent on the portal side |
| C26 | Card fee: a flat configured percentage against a credit-only, capped, default-off surcharge |
| C27 | Payment semantics: partial payment, decline representation, intent and idempotency |
| C28 | Payment method custody and shape |
| C29 | Contractor roles: three vocabularies, two of them inside HH Pro, and two capability grains |
| C30 | Staff roles and branch scope are absent from the portal model entirely |
| C31 | An empty crew grants everything, against an unknown role granting nothing |
| C32 | Branch scoping: absent against mandatory on every operational table |
| C33 | Tenancy columns: `org_id` and `tenant_id` exist in neither repository |
| C34 | Ordered-stream sequence and cursor pagination: both sides are offset-based and unsequenced |
| C35 | Deletes: hard delete with undo, and flag-do-not-delete, against required tombstones |
| C36 | Audit: an unread-count feed against an append-only trail over the whole surface |
| C37 | Attachments: no unified entity on either side |
| C38 | Base resolution order, and whether a promotion is a level at all |
| C39 | The category cascade: nearest-ancestor-wins against exact category equality |
| C40 | Is the tier an entity the account points at, or a customer attribute rules key on |
| C41 | The customer-plus-category override as its own versioned, auditable level |
| C42 | The retail anchor: a stored product column against a pricing-owned row with a cost fallback and a refusal |
| C43 | Does a quantity break replace the base or stack on it |
| C44 | Break vocabulary, scope and threshold unit, including surcharges |
| C45 | Does a contract price admit a quantity break written for that same sku |
| C46 | Cost, margin target, floor, the gate, and server-resolved override authority |
| C47 | Price source attribution: which rule won, against who stood behind the number |
| C48 | Unit-aware pricing and the result price unit |
| C49 | Percent as a float against basis points as an integer |
| C50 | Who owns rounding, and the single authority for a split |
| C51 | Where tax applies, and the evidence gate on relief |
| C52 | Is price validity a domain concept that can re-block a placeable order |
| C53 | The next-break hint, and whether the contractor-facing price object carries it |
| C54 | Price change audit, staged batches and lazy price-file import profiles |
| C55 | Who computes a portal price, and whether it is authoritative |
| C56 | Rebate accrual on the unified order-commit path |

## Reading order for a decision session

Four of these unblock most of the rest, and they are the ones to take first.

1. **C19** (what the board card is) decides the shape of section 5.4 and therefore of the API.
2. **C43** with **C45** and **C38** (stacking, contract finality, promotion as a level) decide the pricing engine, and the context file's section 12 already has an ADR slot waiting for the answer.
3. **C33** with **C34** and **C35** (tenancy, sequence, tombstones) are the App Shape admission conditions from context section 4.4; neither repository satisfies any of the three at present, so they are additive work regardless of every other answer.
4. **C55** (who computes a price, and whether it is authoritative) sits on top of the context file's own offline scope list, which caches a price matrix on a contractor's device while the margin gate lives on the server.
