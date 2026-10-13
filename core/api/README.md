# core/api: the hh core contract

`openapi.yaml` is the contract for the hh core, authored before any Go code
exists (API first, per the HH migration path in the context file). Lane
HH-2.3 of Session HH-2 owns this directory.

## Layout

- `openapi.yaml`: the assembled document. Generated; do not edit by hand.
- `skeleton.yaml`: the document header, shared components, the canonical
  contract entities from `analysis/DECISIONS.md` (D6 to D12), the D4 operator
  surface, the sync surface (context 4.5), and the two modules whose routes
  are registered non-literally (demo, bistrack).
- `fragments/`: one YAML file per ERP module group, transcribed from
  `hardscapeos_dibbits/backend/internal/<module>/`. `CONVENTIONS.md` is the
  binding style guide.
- `drop-list.yaml`: the parity ledger. Every ERP route absent from the
  document (drops) and every document route with no ERP source (additions)
  appears here with a reason.
- `tools/`: the assembly and acceptance scripts (below).

## The three parts

1. **Tenant API** (`/api/v1/tenant/`, tags `tenant-*`): the contractor portal
   surface of the HardscapeOS ERP, renamed from `/api/portal/v1`. Schemas are
   transcribed from the portal Go structs and cross-checked against
   `hh_pro_dibbits/src/core/supplier/adapters/erp-wire.ts` and the recorded
   live fixtures in `hh_pro_dibbits/src/core/supplier/__tests__/fixtures-live/`.
   Order resources carry the canonical core id as `id` and the contractor-side
   alias `legacy_contractor_id` for the mapping period (D6); deep links
   `fb://hh/order/<id>` carry the canonical id only. Tenant routes take no
   `X-Org` header: the org comes from the Team, the tenant from the token (D3).
   List envelopes keep `total` and `offset` as deprecated properties because
   the recorded legacy captures carry them; the new core paginates by cursor
   and never emits them.
2. **Staff API** (`/api/v1/<module>`, tags `staff-*`): one path group per ERP
   module from each `RegisterRoutes`, schemas from `model.go` and the handler
   structs. Every operation carries its gate in `x-role` (resolved from the
   call sites in `cmd/server/main.go`), every staff operation takes the
   `X-Org` header, and every list uses cursor pagination (`cursor`, `limit`,
   `next_cursor`), never offset. The org-scoped operator endpoints the
   launcher section needs are under `/api/v1/operator/` (D4): the platform
   key roster, the write-only dealer credential route, the per-account usage
   counter.
3. **Sync surface** (`/api/v1/sync/{scope}`, tag `sync`): one pull and one
   push per offline scope, the union of every `offline.scopes` list in
   `analysis/role-catalog.yaml` (which `hh/manifest.yaml` must match). Pull by
   `since={seq}`, push replays the outbox in order with client-generated ids
   as idempotency keys, the core assigns `seq`, deletes are tombstones, and
   conflicts return the conflict envelope under the manifest's per-entity
   policy (context 4.5).

## Canonical entities (DECISIONS.md)

- `OrderCard` (D7): the contractor's board card as a read projection of the
  one canonical order aggregate, with an explicit field map
  (`x-field-map`) and a total state map (`x-state-map`) from the dealer order
  status CHECK set to the contractor vocabulary (the HH Pro stage union and
  the tracking union). The map covers every value on both sides; the
  `statemap.mjs` check fails otherwise.
- `DealerQuote` and `CustomerQuote` (D8): two quote resources that share
  `QuoteLine` by schema reference and nothing else; neither has a `kind`
  field. Dealer quotes are priced by the core under D1 and carry
  `valid_until`; customer quotes carry the acceptance artifact and the
  magic-link token.
- `InvoiceView` (D9): lines, tax and holdback totals, balance due, payment
  history; never subledger internals.
- `Attachment` and `AttachmentLink` (D10): bytes in Appwrite Storage, a
  polymorphic link instead of per-owner columns.
- `ProductImage` (D11): one image model; `swatch_hex` is a fallback
  attribute, rendered only when no uploaded image exists.
- `TenantBrand` (D12): the contractor's brand under tenant scope, rendered
  inside the HH Pro frame; the dealer's tokens never appear on a
  contractor's document.

## Checks

`node core/api/tools/check-all.mjs` runs, in order:

1. `merge.mjs`: assembles `openapi.yaml` from the skeleton and fragments and
   fails on duplicate schema names, duplicate operationIds, missing `x-role`,
   unknown tags, undeclared path parameters, templated-path collisions, and
   unresolved `$ref`s.
2. `gen-types.mjs`: generates the Go server types and route table from
   `openapi.yaml` into `core/internal/platform/apigen/` (`types.gen.go` and
   `routes.gen.go`), checking against the committed copies; document drift
   without regeneration fails the suite.
3. `parity.mjs`: the vendored route list holds the grep of every literal
   route registered in `hardscapeos_dibbits/backend/internal` (the same grep
   `analysis/hardscapeos-inventory.md` section 2.6 used), produced by
   `refresh-erp-routes.mjs`; parity applies the portal rename and diffs
   against the document. The diff must be empty apart from
   `drop-list.yaml` entries. The route list is read from the vendored
   `core/api/erp-routes.txt`, so no sibling checkout is needed; `HH_ERP_ROOT`
   or the first argument points the step at a live ERP checkout instead.
4. `fixtures.mjs`: validates every recorded live fixture against the tenant
   schema of the route it was captured from. A fixture that does not validate
   is a finding to record, never a schema to loosen.
5. `statemap.mjs`: the D7 coverage check (both vocabularies quoted from
   their sources).
6. D8 isolation: `DealerQuote` and `CustomerQuote` share only `QuoteLine` by
   schema reference, and neither carries `kind`.
7. House rules: no em or en dashes anywhere in `core/api`.

`gen-readme.mjs` regenerates the source map below from the ERP tree.
Structural lint is clean under `@redocly/cli lint` with zero errors.

## Route source map

Every route in the document and the ERP file and line that registers it
(generated by `gen-readme.mjs`; addition rows quote their `drop-list.yaml`
reason instead).

<!-- SOURCE-MAP:BEGIN -->

| Document route | ERP source |
|---|---|
| `DELETE /api/v1/ar/customers/{customerID}/ap-contact` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:68 |
| `DELETE /api/v1/assist/admin/limits/user/{user}` | hardscapeos_dibbits/backend/internal/assist/handler.go:67 |
| `DELETE /api/v1/dashboard/layout` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:52 |
| `DELETE /api/v1/dashboard/tabs` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:56 |
| `DELETE /api/v1/dashboard/widgets` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:54 |
| `DELETE /api/v1/finstmt/schedules/{id}` | hardscapeos_dibbits/backend/internal/finstmt/module.go:176 |
| `DELETE /api/v1/messaging/rep-assignments` | hardscapeos_dibbits/backend/internal/messaging/repassign_handler.go:29 |
| `DELETE /api/v1/orders/{id}/lines/{lineNo}` | hardscapeos_dibbits/backend/internal/order/edit_handler.go:31 |
| `DELETE /api/v1/pos/parked/{id}` | hardscapeos_dibbits/backend/internal/pos/handler.go:84 |
| `DELETE /api/v1/pos/workspace` | hardscapeos_dibbits/backend/internal/pos/handler.go:75 |
| `DELETE /api/v1/pricing/clearance/{productId}` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:41 |
| `DELETE /api/v1/pricing/overrides/{id}` | hardscapeos_dibbits/backend/internal/pricing/handler.go:83 |
| `DELETE /api/v1/pricing/promotions/{id}` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:36 |
| `DELETE /api/v1/pricing/quantity-breaks/{id}` | hardscapeos_dibbits/backend/internal/pricing/handler.go:99 |
| `DELETE /api/v1/pricing/rebates/programs/{id}` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:47 |
| `DELETE /api/v1/pricing/rules/{id}` | hardscapeos_dibbits/backend/internal/pricing/handler.go:73 |
| `DELETE /api/v1/pricing/uom/conversions/{id}` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:55 |
| `DELETE /api/v1/products/{id}/image` | hardscapeos_dibbits/backend/internal/product/handler.go:103 |
| `DELETE /api/v1/purchasing/pos/{id}/lines/{lineID}` | hardscapeos_dibbits/backend/internal/purchasing/module.go:345 |
| `DELETE /api/v1/purchasing/vendors/{vendorID}/contact` | hardscapeos_dibbits/backend/internal/purchasing/module.go:387 |
| `DELETE /api/v1/purchasing/vendors/{vendorID}/skus/{productID}` | hardscapeos_dibbits/backend/internal/purchasing/module.go:390 |
| `DELETE /api/v1/quotes/{id}/lines/{lineNo}` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:40 |
| `DELETE /api/v1/quotes/{id}/share-links/{linkId}` | hardscapeos_dibbits/backend/internal/quote/share_handler.go:25 |
| `DELETE /api/v1/reporting/commission/rules/{id}` | hardscapeos_dibbits/backend/internal/reporting/handler.go:54 |
| `DELETE /api/v1/reporting/saved-views/{id}` | hardscapeos_dibbits/backend/internal/reporting/handler.go:50 |
| `DELETE /api/v1/surfaces/{surface}/items` | hardscapeos_dibbits/backend/internal/settings/handler.go:32 |
| `DELETE /api/v1/surfaces/{surface}/my-items` | hardscapeos_dibbits/backend/internal/settings/handler.go:34 |
| `DELETE /api/v1/surfaces/{surface}/my-workspace` | hardscapeos_dibbits/backend/internal/settings/handler.go:36 |
| `DELETE /api/v1/tenant/payment-methods/{id}` | hardscapeos_dibbits/backend/internal/portal/payment_methods.go:17 |
| `DELETE /api/v1/tenant/procurement/projects/{id}` | hardscapeos_dibbits/backend/internal/portal/procurement.go:27 |
| `DELETE /api/v1/tenant/procurement/projects/{id}/lines/{lineId}` | hardscapeos_dibbits/backend/internal/portal/procurement.go:30 |
| `DELETE /api/v1/tour/progress/{chapter_key}` | hardscapeos_dibbits/backend/internal/tour/handler.go:46 |
| `GET /api/public/v1/ar/invoice-opened/{deliveryID}` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:96 |
| `GET /api/public/v1/ar/track/{token}/click` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:72 |
| `GET /api/public/v1/ar/track/{token}/open` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:71 |
| `GET /api/public/v1/customer-quotes/{token}` | (addition) G1 customer-quote public view, modelled on the dealer public quote pair; the D10 and D12 token ruling lands here; no ERP source route. |
| `GET /api/public/v1/quotes/{token}` | hardscapeos_dibbits/backend/internal/quote/share_handler.go:35 |
| `GET /api/public/v1/will-call/{token}` | hardscapeos_dibbits/backend/internal/dispatch/module.go:305 |
| `GET /api/v1/ar/aging` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:46 |
| `GET /api/v1/ar/auto-hold` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:55 |
| `GET /api/v1/ar/bounced` | hardscapeos_dibbits/backend/internal/till/handler.go:49 |
| `GET /api/v1/ar/credit-holds` | hardscapeos_dibbits/backend/internal/till/handler.go:51 |
| `GET /api/v1/ar/customers/{customerID}/ap-contact` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:66 |
| `GET /api/v1/ar/customers/{customerID}/statement` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:60 |
| `GET /api/v1/ar/dispatch-gate/{orderID}` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:57 |
| `GET /api/v1/ar/holds` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:47 |
| `GET /api/v1/ar/invoices/{id}/payments` | hardscapeos_dibbits/backend/internal/till/history.go:366 |
| `GET /api/v1/ar/landing` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:51 |
| `GET /api/v1/ar/landing/policy` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:52 |
| `GET /api/v1/ar/landing/policy/history` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:57 |
| `GET /api/v1/ar/nsf-policy` | hardscapeos_dibbits/backend/internal/till/handler.go:53 |
| `GET /api/v1/ar/paper-path` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:62 |
| `GET /api/v1/ar/payments/{id}/history` | hardscapeos_dibbits/backend/internal/till/history.go:365 |
| `GET /api/v1/ar/register` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:76 |
| `GET /api/v1/ar/register/export` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:79 |
| `GET /api/v1/ar/register/invoices/{id}` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:80 |
| `GET /api/v1/ar/register/invoices/{id}/document` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:81 |
| `GET /api/v1/ar/register/policy` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:77 |
| `GET /api/v1/ar/reminders` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:60 |
| `GET /api/v1/ar/statement-runs/{id}` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:59 |
| `GET /api/v1/assist/admin/limits` | hardscapeos_dibbits/backend/internal/assist/handler.go:64 |
| `GET /api/v1/assist/admin/settings` | hardscapeos_dibbits/backend/internal/assist/handler.go:68 |
| `GET /api/v1/assist/admin/spend` | hardscapeos_dibbits/backend/internal/assist/handler.go:63 |
| `GET /api/v1/assist/commands` | hardscapeos_dibbits/backend/internal/assist/handler.go:58 |
| `GET /api/v1/assist/me` | hardscapeos_dibbits/backend/internal/assist/handler.go:61 |
| `GET /api/v1/attachments/{id}` | (addition) D10 canonical attachment entity; the ERP has only per-owner file routes. |
| `GET /api/v1/audit` | hardscapeos_dibbits/backend/internal/audit/handler.go:30 |
| `GET /api/v1/audit/{id}` | hardscapeos_dibbits/backend/internal/audit/handler.go:31 |
| `GET /api/v1/audit/transaction/{entityType}/{entityId}` | hardscapeos_dibbits/backend/internal/audit/handler.go:32 |
| `GET /api/v1/auth/google/callback` | hardscapeos_dibbits/backend/internal/identity/google_oauth.go:78 |
| `GET /api/v1/auth/google/start` | hardscapeos_dibbits/backend/internal/identity/google_oauth.go:77 |
| `GET /api/v1/bankrec/accounts` | hardscapeos_dibbits/backend/internal/bankrec/module.go:167 |
| `GET /api/v1/bankrec/lines` | hardscapeos_dibbits/backend/internal/bankrec/module.go:169 |
| `GET /api/v1/bankrec/matches` | hardscapeos_dibbits/backend/internal/bankrec/module.go:170 |
| `GET /api/v1/bankrec/reconciliations` | hardscapeos_dibbits/backend/internal/bankrec/module.go:175 |
| `GET /api/v1/bankrec/rules` | hardscapeos_dibbits/backend/internal/bankrec/module.go:173 |
| `GET /api/v1/bistrack/status` | (addition) Registered from a route-descriptor table (bistrack/handler.go:45), so the literal grep cannot see it; staff-gated read-only status. |
| `GET /api/v1/branches` | hardscapeos_dibbits/backend/internal/branches/handler.go:34 |
| `GET /api/v1/compliance/calendar` | hardscapeos_dibbits/backend/internal/compliance/module.go:114 |
| `GET /api/v1/compliance/review-status` | hardscapeos_dibbits/backend/internal/compliance/module.go:119 |
| `GET /api/v1/compliance/upcoming` | hardscapeos_dibbits/backend/internal/compliance/module.go:115 |
| `GET /api/v1/counts/negative-stock` | hardscapeos_dibbits/backend/internal/invcount/module.go:165 |
| `GET /api/v1/counts/reason-codes` | hardscapeos_dibbits/backend/internal/invcount/module.go:164 |
| `GET /api/v1/counts/recon` | hardscapeos_dibbits/backend/internal/invcount/module.go:166 |
| `GET /api/v1/counts/sheets` | hardscapeos_dibbits/backend/internal/invcount/module.go:156 |
| `GET /api/v1/counts/sheets/{id}` | hardscapeos_dibbits/backend/internal/invcount/module.go:158 |
| `GET /api/v1/customers/{id}/sms-consent` | hardscapeos_dibbits/backend/internal/customer/handler.go:39 |
| `GET /api/v1/customers/search` | hardscapeos_dibbits/backend/internal/customer/handler.go:36 |
| `GET /api/v1/dashboard` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:49 |
| `GET /api/v1/dashboard/catalog` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:50 |
| `GET /api/v1/dashboard/metrics` | hardscapeos_dibbits/backend/internal/dashboard/viz_handler.go:27 |
| `GET /api/v1/dashboard/needs` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:58 |
| `GET /api/v1/dashboard/profiles` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:59 |
| `GET /api/v1/dashboard/profiles/{profile}` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:60 |
| `GET /api/v1/dashboard/profiles/{profile}/gallery` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:61 |
| `GET /api/v1/dashboard/profiles/{profile}/tabs` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:57 |
| `GET /api/v1/dashboard/profiles/{profile}/widgets` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:59 |
| `GET /api/v1/dashboard/viz` | hardscapeos_dibbits/backend/internal/dashboard/viz_handler.go:28 |
| `GET /api/v1/dashboard/widgets` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:52 |
| `GET /api/v1/dashboard/widgets/catalog` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:55 |
| `GET /api/v1/dashboard/widgets/data` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:57 |
| `GET /api/v1/dashboard/widgets/gallery` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:56 |
| `GET /api/v1/demo/flows` | (addition) Registered via string concatenation (demo/handler.go RouteFlows constant), so the literal grep cannot see it; source hardscapeos_dibbits/backend/internal/demo/handler.go. |
| `GET /api/v1/dispatch/board` | hardscapeos_dibbits/backend/internal/dispatch/module.go:216 |
| `GET /api/v1/dispatch/drivers` | hardscapeos_dibbits/backend/internal/dispatch/module.go:212 |
| `GET /api/v1/dispatch/freight-raises/{id}` | hardscapeos_dibbits/backend/internal/dispatch/module.go:285 |
| `GET /api/v1/dispatch/loadboard` | hardscapeos_dibbits/backend/internal/dispatch/module.go:258 |
| `GET /api/v1/dispatch/loadboard/calendar` | hardscapeos_dibbits/backend/internal/dispatch/module.go:259 |
| `GET /api/v1/dispatch/loadboard/gaps` | hardscapeos_dibbits/backend/internal/dispatch/module.go:260 |
| `GET /api/v1/dispatch/my-runs` | hardscapeos_dibbits/backend/internal/dispatch/module.go:291 |
| `GET /api/v1/dispatch/orders/{id}/gaps` | hardscapeos_dibbits/backend/internal/dispatch/module.go:263 |
| `GET /api/v1/dispatch/photos/{id}` | hardscapeos_dibbits/backend/internal/dispatch/module.go:223 |
| `GET /api/v1/dispatch/policy` | hardscapeos_dibbits/backend/internal/dispatch/module.go:270 |
| `GET /api/v1/dispatch/policy/history` | hardscapeos_dibbits/backend/internal/dispatch/module.go:269 |
| `GET /api/v1/dispatch/run-by-no/{runNo}` | hardscapeos_dibbits/backend/internal/dispatch/module.go:237 |
| `GET /api/v1/dispatch/runs` | hardscapeos_dibbits/backend/internal/dispatch/module.go:226 |
| `GET /api/v1/dispatch/runs/{id}` | hardscapeos_dibbits/backend/internal/dispatch/module.go:228 |
| `GET /api/v1/dispatch/runs/{id}/manifest` | hardscapeos_dibbits/backend/internal/dispatch/module.go:242 |
| `GET /api/v1/dispatch/runs/{id}/payment-holds` | hardscapeos_dibbits/backend/internal/dispatch/handler_w3.go:42 |
| `GET /api/v1/dispatch/runs/{id}/reschedules` | hardscapeos_dibbits/backend/internal/dispatch/handler_w3.go:41 |
| `GET /api/v1/dispatch/scan-trail` | hardscapeos_dibbits/backend/internal/dispatch/handler_w3.go:49 |
| `GET /api/v1/dispatch/stops/{id}` | hardscapeos_dibbits/backend/internal/dispatch/module.go:218 |
| `GET /api/v1/dispatch/stops/{id}/payment-hold` | hardscapeos_dibbits/backend/internal/dispatch/handler_w3.go:38 |
| `GET /api/v1/dispatch/stops/{id}/reschedules` | hardscapeos_dibbits/backend/internal/dispatch/handler_w3.go:37 |
| `GET /api/v1/dispatch/trucks` | hardscapeos_dibbits/backend/internal/dispatch/module.go:210 |
| `GET /api/v1/dispatch/will-call` | hardscapeos_dibbits/backend/internal/dispatch/module.go:295 |
| `GET /api/v1/exports/audit.xlsx` | hardscapeos_dibbits/backend/internal/export/handler.go:88 |
| `GET /api/v1/exports/catalogue.xlsx` | hardscapeos_dibbits/backend/internal/export/handler.go:80 |
| `GET /api/v1/exports/statement.xlsx` | hardscapeos_dibbits/backend/internal/export/handler.go:82 |
| `GET /api/v1/exports/transactions.xlsx` | hardscapeos_dibbits/backend/internal/export/handler.go:85 |
| `GET /api/v1/finstmt/ar-aging` | hardscapeos_dibbits/backend/internal/finstmt/module.go:172 |
| `GET /api/v1/finstmt/balance-sheet` | hardscapeos_dibbits/backend/internal/finstmt/module.go:169 |
| `GET /api/v1/finstmt/gl-detail` | hardscapeos_dibbits/backend/internal/finstmt/module.go:171 |
| `GET /api/v1/finstmt/income-statement` | hardscapeos_dibbits/backend/internal/finstmt/module.go:168 |
| `GET /api/v1/finstmt/schedules` | hardscapeos_dibbits/backend/internal/finstmt/module.go:173 |
| `GET /api/v1/finstmt/trial-balance` | hardscapeos_dibbits/backend/internal/finstmt/module.go:170 |
| `GET /api/v1/gl/accounts` | hardscapeos_dibbits/backend/internal/gl/module.go:384 |
| `GET /api/v1/gl/journals` | hardscapeos_dibbits/backend/internal/gl/module.go:388 |
| `GET /api/v1/gl/journals/{id}` | hardscapeos_dibbits/backend/internal/gl/module.go:389 |
| `GET /api/v1/gl/periods` | hardscapeos_dibbits/backend/internal/gl/module.go:403 |
| `GET /api/v1/gl/policy` | hardscapeos_dibbits/backend/internal/gl/module.go:408 |
| `GET /api/v1/gl/recurring` | hardscapeos_dibbits/backend/internal/gl/module.go:395 |
| `GET /api/v1/gl/tie-out` | hardscapeos_dibbits/backend/internal/gl/module.go:399 |
| `GET /api/v1/gl/tieout/bistrack` | hardscapeos_dibbits/backend/internal/gl/module.go:401 |
| `GET /api/v1/gl/trial-balance` | hardscapeos_dibbits/backend/internal/gl/module.go:398 |
| `GET /api/v1/identity/ping` | hardscapeos_dibbits/backend/internal/identity/handler.go:41 |
| `GET /api/v1/inventory/allocation-policy` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:40 |
| `GET /api/v1/inventory/branch-stock` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:32 |
| `GET /api/v1/inventory/browse` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:33 |
| `GET /api/v1/inventory/incoming-transfers` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:71 |
| `GET /api/v1/inventory/moves` | hardscapeos_dibbits/backend/internal/inventory/module.go:278 |
| `GET /api/v1/inventory/on-hand` | hardscapeos_dibbits/backend/internal/inventory/module.go:277 |
| `GET /api/v1/inventory/pending-fulfilment` | hardscapeos_dibbits/backend/internal/inventory/module.go:279 |
| `GET /api/v1/inventory/reorder-points` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:53 |
| `GET /api/v1/inventory/reservations` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:36 |
| `GET /api/v1/inventory/shortfall` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:52 |
| `GET /api/v1/inventory/transfers` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:44 |
| `GET /api/v1/inventory/transfers/{id}` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:45 |
| `GET /api/v1/inventory/transit-days` | hardscapeos_dibbits/backend/internal/inventory/module.go:294 |
| `GET /api/v1/landed-costs` | hardscapeos_dibbits/backend/internal/landedcost/module.go:179 |
| `GET /api/v1/landed-costs/{id}` | hardscapeos_dibbits/backend/internal/landedcost/module.go:181 |
| `GET /api/v1/landed-costs/{id}/preview` | hardscapeos_dibbits/backend/internal/landedcost/module.go:182 |
| `GET /api/v1/lien/documents/{id}/versions` | hardscapeos_dibbits/backend/internal/lien/module.go:190 |
| `GET /api/v1/lien/holdback/aging` | hardscapeos_dibbits/backend/internal/lien/module.go:188 |
| `GET /api/v1/lien/jobs` | hardscapeos_dibbits/backend/internal/lien/module.go:185 |
| `GET /api/v1/lien/jobs/{id}` | hardscapeos_dibbits/backend/internal/lien/module.go:186 |
| `GET /api/v1/lien/jobs/{id}/documents` | hardscapeos_dibbits/backend/internal/lien/module.go:189 |
| `GET /api/v1/lien/triage` | hardscapeos_dibbits/backend/internal/lien/module.go:187 |
| `GET /api/v1/load/batches/{id}` | (addition) HH-LOAD-1 load doors: the machine batch read behind the load doors deployment switch; no ERP source route. |
| `GET /api/v1/messaging/channels` | hardscapeos_dibbits/backend/internal/messaging/handler.go:57 |
| `GET /api/v1/messaging/channels/{id}/house-queue` | hardscapeos_dibbits/backend/internal/messaging/repassign_handler.go:37 |
| `GET /api/v1/messaging/channels/{id}/messages` | hardscapeos_dibbits/backend/internal/messaging/handler.go:58 |
| `GET /api/v1/messaging/channels/{id}/messages/{msgID}/ack-progress` | hardscapeos_dibbits/backend/internal/messaging/repassign_handler.go:40 |
| `GET /api/v1/messaging/channels/{id}/notify-prefs` | hardscapeos_dibbits/backend/internal/messaging/handler.go:67 |
| `GET /api/v1/messaging/channels/{id}/threads/{rootID}` | hardscapeos_dibbits/backend/internal/messaging/handler.go:60 |
| `GET /api/v1/messaging/event-classes` | hardscapeos_dibbits/backend/internal/messaging/handler.go:87 |
| `GET /api/v1/messaging/export` | hardscapeos_dibbits/backend/internal/messaging/handler.go:83 |
| `GET /api/v1/messaging/payment-rules` | hardscapeos_dibbits/backend/internal/messaging/handler.go:93 |
| `GET /api/v1/messaging/rep-assignments` | hardscapeos_dibbits/backend/internal/messaging/repassign_handler.go:27 |
| `GET /api/v1/messaging/rep-route` | hardscapeos_dibbits/backend/internal/messaging/repassign_handler.go:32 |
| `GET /api/v1/messaging/stream` | hardscapeos_dibbits/backend/internal/messaging/handler.go:75 |
| `GET /api/v1/messaging/unacked` | hardscapeos_dibbits/backend/internal/messaging/handler.go:65 |
| `GET /api/v1/messaging/unread` | hardscapeos_dibbits/backend/internal/messaging/handler.go:69 |
| `GET /api/v1/operator/platform-keys` | (addition) D4 operator read for the launcher (the platform key roster); source hh_pro_dibbits/server/admin-store.ts, not the ERP. |
| `GET /api/v1/operator/usage` | (addition) D4 per-account usage counter read for the launcher; source hh_pro_dibbits/server/admin-store.ts. |
| `GET /api/v1/order-changes/{changeId}` | hardscapeos_dibbits/backend/internal/order/change_handler.go:47 |
| `GET /api/v1/orders/{id}` | hardscapeos_dibbits/backend/internal/order/history.go:210 |
| `GET /api/v1/orders/{id}/activity` | hardscapeos_dibbits/backend/internal/order/history.go:219 |
| `GET /api/v1/orders/{id}/changes` | hardscapeos_dibbits/backend/internal/order/change_handler.go:45 |
| `GET /api/v1/orders/{id}/duplicate-check` | hardscapeos_dibbits/backend/internal/order/duplicate.go:587 |
| `GET /api/v1/orders/{id}/duplicate-preview` | hardscapeos_dibbits/backend/internal/order/duplicate.go:585 |
| `GET /api/v1/orders/{id}/edits` | hardscapeos_dibbits/backend/internal/order/edit_handler.go:33 |
| `GET /api/v1/orders/{id}/history` | hardscapeos_dibbits/backend/internal/order/history.go:208 |
| `GET /api/v1/orders/{id}/related-documents` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:56 |
| `GET /api/v1/orders/{id}/reservations` | hardscapeos_dibbits/backend/internal/order/change_handler.go:41 |
| `GET /api/v1/orders/{id}/shipments` | hardscapeos_dibbits/backend/internal/order/change_handler.go:55 |
| `GET /api/v1/payments/config` | hardscapeos_dibbits/backend/internal/payment/handler.go:48 |
| `GET /api/v1/platform/hotkeys` | hardscapeos_dibbits/backend/internal/settings/handler.go:44 |
| `GET /api/v1/pos/account-standing` | hardscapeos_dibbits/backend/internal/pos/handler.go:59 |
| `GET /api/v1/pos/addon-suggestions` | hardscapeos_dibbits/backend/internal/pos/handler.go:61 |
| `GET /api/v1/pos/hotkeys` | hardscapeos_dibbits/backend/internal/pos/handler.go:56 |
| `GET /api/v1/pos/parked` | hardscapeos_dibbits/backend/internal/pos/handler.go:82 |
| `GET /api/v1/pos/policy` | hardscapeos_dibbits/backend/internal/pos/handler.go:64 |
| `GET /api/v1/pos/policy/history` | hardscapeos_dibbits/backend/internal/pos/handler.go:69 |
| `GET /api/v1/pos/pricing-orders` | hardscapeos_dibbits/backend/internal/pos/handler.go:87 |
| `GET /api/v1/pos/pricing-orders/{id}` | hardscapeos_dibbits/backend/internal/pos/handler.go:86 |
| `GET /api/v1/pos/quick-picks` | hardscapeos_dibbits/backend/internal/pos/handler.go:54 |
| `GET /api/v1/pos/return-requests` | hardscapeos_dibbits/backend/internal/pos/return_requests.go:372 |
| `GET /api/v1/pos/returns/lookup` | hardscapeos_dibbits/backend/internal/pos/handler.go:78 |
| `GET /api/v1/pos/returns/pickups` | hardscapeos_dibbits/backend/internal/pos/handler.go:79 |
| `GET /api/v1/pos/sales` | hardscapeos_dibbits/backend/internal/pos/handler.go:37 |
| `GET /api/v1/pos/sales/{id}` | hardscapeos_dibbits/backend/internal/pos/handler.go:38 |
| `GET /api/v1/pos/sales/{id}/receipt` | hardscapeos_dibbits/backend/internal/printing/handler.go:34 |
| `GET /api/v1/pos/salespeople` | hardscapeos_dibbits/backend/internal/pos/handler.go:43 |
| `GET /api/v1/pos/stock-glance` | hardscapeos_dibbits/backend/internal/pos/handler.go:41 |
| `GET /api/v1/pos/tax-rate` | (addition) Row 661: the counter-readable tax rate; the rate the ring binds, read through the ring's own assessor, with the stored round rule beside it; no ERP source route. |
| `GET /api/v1/pos/workspace` | hardscapeos_dibbits/backend/internal/pos/handler.go:73 |
| `GET /api/v1/pricing/audit` | hardscapeos_dibbits/backend/internal/pricing/handler.go:86 |
| `GET /api/v1/pricing/batches` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:17 |
| `GET /api/v1/pricing/batches/{id}` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:19 |
| `GET /api/v1/pricing/batches/{id}/lines` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:20 |
| `GET /api/v1/pricing/category-policies` | hardscapeos_dibbits/backend/internal/pricing/handler.go:76 |
| `GET /api/v1/pricing/change-log` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:30 |
| `GET /api/v1/pricing/clearance` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:39 |
| `GET /api/v1/pricing/customers/{customerId}/overrides` | hardscapeos_dibbits/backend/internal/pricing/handler.go:80 |
| `GET /api/v1/pricing/import-profiles` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:27 |
| `GET /api/v1/pricing/price-file/runs` | hardscapeos_dibbits/backend/internal/pricing/pricefile_handler.go:46 |
| `GET /api/v1/pricing/price-file/vendors` | hardscapeos_dibbits/backend/internal/pricing/pricefile_handler.go:43 |
| `GET /api/v1/pricing/promotions` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:33 |
| `GET /api/v1/pricing/quantity-breaks` | hardscapeos_dibbits/backend/internal/pricing/handler.go:95 |
| `GET /api/v1/pricing/quantity-breaks/{id}` | hardscapeos_dibbits/backend/internal/pricing/handler.go:96 |
| `GET /api/v1/pricing/rebates/programs` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:44 |
| `GET /api/v1/pricing/rebates/vendors/{vendorId}/ledger` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:48 |
| `GET /api/v1/pricing/reports/low-margin/scan` | hardscapeos_dibbits/backend/internal/pricing/handler.go:90 |
| `GET /api/v1/pricing/rules` | hardscapeos_dibbits/backend/internal/pricing/handler.go:69 |
| `GET /api/v1/pricing/rules/{id}` | hardscapeos_dibbits/backend/internal/pricing/handler.go:70 |
| `GET /api/v1/pricing/uom/conversions` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:53 |
| `GET /api/v1/printing/jobs` | hardscapeos_dibbits/backend/internal/printing/handler.go:35 |
| `GET /api/v1/products` | hardscapeos_dibbits/backend/internal/product/handler.go:39 |
| `GET /api/v1/products/{id}` | hardscapeos_dibbits/backend/internal/product/handler.go:41 |
| `GET /api/v1/products/{id}/image` | hardscapeos_dibbits/backend/internal/product/handler.go:101 |
| `GET /api/v1/products/counter-search` | hardscapeos_dibbits/backend/internal/product/handler.go:93 |
| `GET /api/v1/products/coverage` | hardscapeos_dibbits/backend/internal/product/handler.go:100 |
| `GET /api/v1/products/facets` | hardscapeos_dibbits/backend/internal/product/handler.go:40 |
| `GET /api/v1/products/scan` | hardscapeos_dibbits/backend/internal/product/handler.go:94 |
| `GET /api/v1/products/search` | hardscapeos_dibbits/backend/internal/product/handler.go:65 |
| `GET /api/v1/purchasing/bills` | hardscapeos_dibbits/backend/internal/purchasing/module.go:363 |
| `GET /api/v1/purchasing/bills/{id}` | hardscapeos_dibbits/backend/internal/purchasing/module.go:365 |
| `GET /api/v1/purchasing/incoming` | hardscapeos_dibbits/backend/internal/purchasing/module.go:352 |
| `GET /api/v1/purchasing/policy` | hardscapeos_dibbits/backend/internal/purchasing/module.go:382 |
| `GET /api/v1/purchasing/pos` | hardscapeos_dibbits/backend/internal/purchasing/module.go:341 |
| `GET /api/v1/purchasing/pos/{id}` | hardscapeos_dibbits/backend/internal/purchasing/module.go:343 |
| `GET /api/v1/purchasing/pos/{id}/costs` | hardscapeos_dibbits/backend/internal/purchasing/module.go:394 |
| `GET /api/v1/purchasing/pos/{id}/document` | hardscapeos_dibbits/backend/internal/purchasing/module.go:392 |
| `GET /api/v1/purchasing/pos/{id}/document.csv` | hardscapeos_dibbits/backend/internal/purchasing/module.go:393 |
| `GET /api/v1/purchasing/pos/{id}/sends` | hardscapeos_dibbits/backend/internal/purchasing/module.go:395 |
| `GET /api/v1/purchasing/receipts/{id}` | hardscapeos_dibbits/backend/internal/purchasing/module.go:360 |
| `GET /api/v1/purchasing/receiving` | hardscapeos_dibbits/backend/internal/purchasing/module.go:359 |
| `GET /api/v1/purchasing/tolerance` | hardscapeos_dibbits/backend/internal/purchasing/module.go:368 |
| `GET /api/v1/purchasing/vendor-ref` | hardscapeos_dibbits/backend/internal/purchasing/module.go:398 |
| `GET /api/v1/purchasing/vendors` | hardscapeos_dibbits/backend/internal/purchasing/module.go:339 |
| `GET /api/v1/purchasing/vendors/{vendorID}/contact` | hardscapeos_dibbits/backend/internal/purchasing/module.go:385 |
| `GET /api/v1/purchasing/vendors/{vendorID}/skus` | hardscapeos_dibbits/backend/internal/purchasing/module.go:388 |
| `GET /api/v1/quick-actions` | hardscapeos_dibbits/backend/internal/dashboard/quickaction_handler.go:39 |
| `GET /api/v1/quick-actions/config` | hardscapeos_dibbits/backend/internal/dashboard/quickaction_handler.go:40 |
| `GET /api/v1/quote-assist/scope-gaps` | hardscapeos_dibbits/backend/internal/quoteassist/handler.go:56 |
| `GET /api/v1/quote-assist/scope-rules` | hardscapeos_dibbits/backend/internal/quoteassist/handler.go:57 |
| `GET /api/v1/quote-availability/{productId}/branches` | hardscapeos_dibbits/backend/internal/quote/reserve_handler.go:29 |
| `GET /api/v1/quotes` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:35 |
| `GET /api/v1/quotes/{id}` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:36 |
| `GET /api/v1/quotes/{id}/availability` | hardscapeos_dibbits/backend/internal/quote/reserve_handler.go:24 |
| `GET /api/v1/quotes/{id}/edits` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:41 |
| `GET /api/v1/quotes/{id}/followups` | hardscapeos_dibbits/backend/internal/quote/followup_handler.go:34 |
| `GET /api/v1/quotes/{id}/hold-expiry` | hardscapeos_dibbits/backend/internal/quote/reserve_handler.go:26 |
| `GET /api/v1/quotes/{id}/holds` | hardscapeos_dibbits/backend/internal/quote/reserve_handler.go:25 |
| `GET /api/v1/quotes/{id}/margin-glance` | hardscapeos_dibbits/backend/internal/quote/builder_handler.go:28 |
| `GET /api/v1/quotes/{id}/related-documents` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:55 |
| `GET /api/v1/quotes/{id}/share-links` | hardscapeos_dibbits/backend/internal/quote/share_handler.go:24 |
| `GET /api/v1/quotes/{id}/terms` | hardscapeos_dibbits/backend/internal/quote/builder_handler.go:26 |
| `GET /api/v1/quotes/policy` | hardscapeos_dibbits/backend/internal/quote/builder_handler.go:24 |
| `GET /api/v1/quotes/settings/acceptance` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:61 |
| `GET /api/v1/reporting/commission/accruals` | hardscapeos_dibbits/backend/internal/reporting/handler.go:61 |
| `GET /api/v1/reporting/commission/attribution-check` | hardscapeos_dibbits/backend/internal/reporting/handler.go:62 |
| `GET /api/v1/reporting/commission/preview` | hardscapeos_dibbits/backend/internal/reporting/handler.go:55 |
| `GET /api/v1/reporting/commission/rules` | hardscapeos_dibbits/backend/internal/reporting/handler.go:52 |
| `GET /api/v1/reporting/commission/runs` | hardscapeos_dibbits/backend/internal/reporting/handler.go:57 |
| `GET /api/v1/reporting/commission/runs/{id}` | hardscapeos_dibbits/backend/internal/reporting/handler.go:58 |
| `GET /api/v1/reporting/commission/runs/{id}/export.csv` | hardscapeos_dibbits/backend/internal/reporting/handler.go:59 |
| `GET /api/v1/reporting/runs` | hardscapeos_dibbits/backend/internal/reporting/handler.go:44 |
| `GET /api/v1/reporting/runs/{id}` | hardscapeos_dibbits/backend/internal/reporting/handler.go:45 |
| `GET /api/v1/reporting/runs/{id}/document` | hardscapeos_dibbits/backend/internal/reporting/handler.go:46 |
| `GET /api/v1/reporting/saved-views` | hardscapeos_dibbits/backend/internal/reporting/handler.go:48 |
| `GET /api/v1/reporting/templates` | hardscapeos_dibbits/backend/internal/reporting/handler.go:37 |
| `GET /api/v1/reporting/templates/{code}` | hardscapeos_dibbits/backend/internal/reporting/handler.go:38 |
| `GET /api/v1/reporting/templates/{code}/export.csv` | hardscapeos_dibbits/backend/internal/reporting/handler.go:41 |
| `GET /api/v1/reporting/templates/{code}/run` | hardscapeos_dibbits/backend/internal/reporting/handler.go:40 |
| `GET /api/v1/staff` | hardscapeos_dibbits/backend/internal/identity/handler.go:53 |
| `GET /api/v1/staff/approvals` | hardscapeos_dibbits/backend/internal/identity/handler.go:68 |
| `GET /api/v1/staff/roles` | hardscapeos_dibbits/backend/internal/identity/handler.go:49 |
| `GET /api/v1/surfaces/{surface}/config` | hardscapeos_dibbits/backend/internal/settings/handler.go:30 |
| `GET /api/v1/sync/{scope}` | (addition) Context 4.5 sync protocol, one pull per manifest offline scope; the ERP has no sync endpoint. |
| `GET /api/v1/tasks` | hardscapeos_dibbits/backend/internal/tasks/handler.go:39 |
| `GET /api/v1/tax/config` | hardscapeos_dibbits/backend/internal/tax/module.go:176 |
| `GET /api/v1/tax/evidence` | hardscapeos_dibbits/backend/internal/tax/module.go:183 |
| `GET /api/v1/tax/evidence/{id}` | hardscapeos_dibbits/backend/internal/tax/module.go:185 |
| `GET /api/v1/tax/profiles` | hardscapeos_dibbits/backend/internal/tax/module.go:174 |
| `GET /api/v1/tax/rates` | hardscapeos_dibbits/backend/internal/tax/module.go:172 |
| `GET /api/v1/tax/recon` | hardscapeos_dibbits/backend/internal/tax/module.go:179 |
| `GET /api/v1/tax/review-queue` | hardscapeos_dibbits/backend/internal/tax/module.go:190 |
| `GET /api/v1/tenant/addresses` | hardscapeos_dibbits/backend/internal/portal/addresses.go:138 |
| `GET /api/v1/tenant/addresses/{id}` | hardscapeos_dibbits/backend/internal/portal/addresses.go:139 |
| `GET /api/v1/tenant/billing/summary` | hardscapeos_dibbits/backend/internal/portal/dashboard.go:39 |
| `GET /api/v1/tenant/catalog/products/{id}` | hardscapeos_dibbits/backend/internal/portal/catalog.go:25 |
| `GET /api/v1/tenant/catalog/search` | hardscapeos_dibbits/backend/internal/portal/catalog.go:24 |
| `GET /api/v1/tenant/config` | hardscapeos_dibbits/backend/internal/portal/handler.go:91 |
| `GET /api/v1/tenant/dashboard` | hardscapeos_dibbits/backend/internal/portal/dashboard.go:38 |
| `GET /api/v1/tenant/invites` | hardscapeos_dibbits/backend/internal/portal/handler.go:95 |
| `GET /api/v1/tenant/invoices` | hardscapeos_dibbits/backend/internal/portal/invoices.go:12 |
| `GET /api/v1/tenant/invoices/{id}` | hardscapeos_dibbits/backend/internal/portal/invoices.go:13 |
| `GET /api/v1/tenant/me` | hardscapeos_dibbits/backend/internal/portal/settings.go:16 |
| `GET /api/v1/tenant/orders` | hardscapeos_dibbits/backend/internal/portal/orders.go:20 |
| `GET /api/v1/tenant/orders/{id}` | hardscapeos_dibbits/backend/internal/portal/orders.go:21 |
| `GET /api/v1/tenant/orders/{id}/reorder-preview` | hardscapeos_dibbits/backend/internal/portal/reorder.go:36 |
| `GET /api/v1/tenant/orders/{id}/return-eligibility` | hardscapeos_dibbits/backend/internal/portal/returns.go:69 |
| `GET /api/v1/tenant/payment-methods` | hardscapeos_dibbits/backend/internal/portal/payment_methods.go:15 |
| `GET /api/v1/tenant/payments` | hardscapeos_dibbits/backend/internal/portal/payments.go:54 |
| `GET /api/v1/tenant/procurement/projects` | hardscapeos_dibbits/backend/internal/portal/procurement.go:23 |
| `GET /api/v1/tenant/procurement/projects/{id}` | hardscapeos_dibbits/backend/internal/portal/procurement.go:25 |
| `GET /api/v1/tenant/projects` | hardscapeos_dibbits/backend/internal/portal/projects.go:362 |
| `GET /api/v1/tenant/projects/{id}` | hardscapeos_dibbits/backend/internal/portal/projects.go:363 |
| `GET /api/v1/tenant/quotes` | hardscapeos_dibbits/backend/internal/portal/quotes.go:15 |
| `GET /api/v1/tenant/quotes/{id}` | hardscapeos_dibbits/backend/internal/portal/quotes.go:16 |
| `GET /api/v1/tenant/return-requests` | hardscapeos_dibbits/backend/internal/portal/returns.go:71 |
| `GET /api/v1/tenant/users` | hardscapeos_dibbits/backend/internal/portal/handler.go:94 |
| `GET /api/v1/tenants/{tenantId}/brand` | (addition) D12 tenant_brand staff read; the ERP keeps dealer branding in portal config only. |
| `GET /api/v1/till/policy` | hardscapeos_dibbits/backend/internal/till/handler.go:43 |
| `GET /api/v1/till/sessions/{id}` | hardscapeos_dibbits/backend/internal/till/handler.go:38 |
| `GET /api/v1/till/sessions/{id}/zreport` | hardscapeos_dibbits/backend/internal/till/handler.go:41 |
| `GET /api/v1/till/sessions/open` | hardscapeos_dibbits/backend/internal/till/handler.go:37 |
| `GET /api/v1/tour` | hardscapeos_dibbits/backend/internal/tour/handler.go:44 |
| `GET /api/v1/uoms` | (addition) HH-LOAD-1 load doors (LD-3): the staff read of the unit catalogue, Dealer Admin at read; no ERP source route. |
| `GET /api/v1/warranty/board` | hardscapeos_dibbits/backend/internal/warranty/module.go:155 |
| `GET /api/v1/warranty/claims` | hardscapeos_dibbits/backend/internal/warranty/module.go:136 |
| `GET /api/v1/warranty/claims/{id}` | hardscapeos_dibbits/backend/internal/warranty/module.go:137 |
| `GET /api/v1/warranty/claims/{id}/checklist` | hardscapeos_dibbits/backend/internal/warranty/module.go:139 |
| `GET /api/v1/warranty/claims/{id}/events` | hardscapeos_dibbits/backend/internal/warranty/module.go:138 |
| `GET /api/v1/warranty/claims/{id}/photos` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:34 |
| `GET /api/v1/warranty/claims/{id}/photos/{photoId}` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:35 |
| `GET /api/v1/warranty/claims/{id}/postings` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:26 |
| `GET /api/v1/warranty/claims/{id}/recovery` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:30 |
| `GET /api/v1/warranty/recovery` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:29 |
| `GET /api/v1/warranty/reports` | hardscapeos_dibbits/backend/internal/warranty/module.go:152 |
| `GET /api/v1/warranty/sla` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:38 |
| `GET /api/v1/warranty/sla/targets` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:39 |
| `GET /api/v1/warranty/vendors` | hardscapeos_dibbits/backend/internal/warranty/module.go:130 |
| `GET /api/v1/warranty/vendors/{vendorID}` | hardscapeos_dibbits/backend/internal/warranty/module.go:131 |
| `GET /api/v1/yard/advance-rules/{taskType}` | hardscapeos_dibbits/backend/internal/tasks/weight_handler.go:31 |
| `GET /api/v1/yard/pending-tickets` | hardscapeos_dibbits/backend/internal/tasks/yard_handler.go:30 |
| `GET /api/v1/yard/pick-queue` | hardscapeos_dibbits/backend/internal/tasks/yard_handler.go:29 |
| `GET /api/v1/yard/picks/{id}` | hardscapeos_dibbits/backend/internal/tasks/yard_handler.go:31 |
| `PATCH /api/v1/orders/{id}` | hardscapeos_dibbits/backend/internal/order/edit_handler.go:32 |
| `PATCH /api/v1/orders/{id}/lines/{lineNo}` | hardscapeos_dibbits/backend/internal/order/edit_handler.go:29 |
| `PATCH /api/v1/orders/{id}/notes/{noteId}` | hardscapeos_dibbits/backend/internal/order/history.go:221 |
| `PATCH /api/v1/quote-assist/scope-rules/{code}` | hardscapeos_dibbits/backend/internal/quoteassist/handler.go:58 |
| `PATCH /api/v1/quotes/{id}` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:37 |
| `PATCH /api/v1/quotes/{id}/lines/{lineNo}` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:39 |
| `POST /api/public/v1/customer-quotes/{token}/accept` | (addition) G1 customer-quote public acceptance (D8, D10, D12): typed-name attestation with the 404, 410 and 409 semantics; no ERP source route. |
| `POST /api/public/v1/quotes/{token}/accept` | hardscapeos_dibbits/backend/internal/quote/share_handler.go:36 |
| `POST /api/public/v1/warranty/intake` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:56 |
| `POST /api/public/v1/will-call/{token}/confirm` | hardscapeos_dibbits/backend/internal/dispatch/module.go:306 |
| `POST /api/v1/ar/credit-holds/{id}/release` | hardscapeos_dibbits/backend/internal/till/handler.go:52 |
| `POST /api/v1/ar/credit-sweep` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:50 |
| `POST /api/v1/ar/holds/{id}/ar-release` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:49 |
| `POST /api/v1/ar/holds/{id}/override` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:48 |
| `POST /api/v1/ar/paper-path/{id}/resolve` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:64 |
| `POST /api/v1/ar/paper-path/sweep` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:63 |
| `POST /api/v1/ar/payment-attempts` | hardscapeos_dibbits/backend/internal/till/handler.go:55 |
| `POST /api/v1/ar/payments/{id}/nsf` | hardscapeos_dibbits/backend/internal/till/handler.go:50 |
| `POST /api/v1/ar/payments/{id}/reverse` | hardscapeos_dibbits/backend/internal/payments/reversal_handler.go:38 |
| `POST /api/v1/ar/register/invoices/{id}/dispute` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:85 |
| `POST /api/v1/ar/register/invoices/{id}/dispute/resolve` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:86 |
| `POST /api/v1/ar/register/invoices/{id}/payments` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:82 |
| `POST /api/v1/ar/register/invoices/{id}/promise` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:84 |
| `POST /api/v1/ar/register/invoices/{id}/send` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:83 |
| `POST /api/v1/ar/register/reminders` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:89 |
| `POST /api/v1/ar/register/selection` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:78 |
| `POST /api/v1/ar/register/statements` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:90 |
| `POST /api/v1/ar/reminders` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:59 |
| `POST /api/v1/ar/statement-runs` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:58 |
| `POST /api/v1/assist/chat` | hardscapeos_dibbits/backend/internal/assist/handler.go:60 |
| `POST /api/v1/assist/command` | hardscapeos_dibbits/backend/internal/assist/handler.go:59 |
| `POST /api/v1/attachments` | (addition) D10 canonical attachment entity (metadata record; bytes in Appwrite Storage); the ERP has only per-owner file routes. |
| `POST /api/v1/attachments/{id}/links` | (addition) D10 polymorphic attachment_link; replaces per-owner attachment columns. |
| `POST /api/v1/auth/change-password` | hardscapeos_dibbits/backend/internal/identity/handler.go:48 |
| `POST /api/v1/auth/login` | hardscapeos_dibbits/backend/internal/identity/handler.go:42 |
| `POST /api/v1/auth/password-reset/confirm` | hardscapeos_dibbits/backend/internal/identity/handler.go:45 |
| `POST /api/v1/bankrec/imports` | hardscapeos_dibbits/backend/internal/bankrec/module.go:168 |
| `POST /api/v1/bankrec/match` | hardscapeos_dibbits/backend/internal/bankrec/module.go:171 |
| `POST /api/v1/bankrec/reconciliations` | hardscapeos_dibbits/backend/internal/bankrec/module.go:176 |
| `POST /api/v1/bankrec/reconciliations/{id}/complete` | hardscapeos_dibbits/backend/internal/bankrec/module.go:177 |
| `POST /api/v1/bankrec/rules` | hardscapeos_dibbits/backend/internal/bankrec/module.go:174 |
| `POST /api/v1/bankrec/unmatch` | hardscapeos_dibbits/backend/internal/bankrec/module.go:172 |
| `POST /api/v1/counts/sheets` | hardscapeos_dibbits/backend/internal/invcount/module.go:157 |
| `POST /api/v1/counts/sheets/{id}/approve` | hardscapeos_dibbits/backend/internal/invcount/module.go:161 |
| `POST /api/v1/counts/sheets/{id}/cancel` | hardscapeos_dibbits/backend/internal/invcount/module.go:163 |
| `POST /api/v1/counts/sheets/{id}/counts` | hardscapeos_dibbits/backend/internal/invcount/module.go:160 |
| `POST /api/v1/counts/sheets/{id}/freeze` | hardscapeos_dibbits/backend/internal/invcount/module.go:159 |
| `POST /api/v1/counts/sheets/{id}/post` | hardscapeos_dibbits/backend/internal/invcount/module.go:162 |
| `POST /api/v1/customer-quotes/{id}/send` | (addition) G1 customer-quote send (D8): the dealer-side mint of the homeowner magic link; the raw token appears in this response only; no ERP source route. |
| `POST /api/v1/customers` | hardscapeos_dibbits/backend/internal/customer/handler.go:43 |
| `POST /api/v1/demo/session` | (addition) Registered via string concatenation (demo/handler.go RouteSession constant), so the literal grep cannot see it; source hardscapeos_dibbits/backend/internal/demo/handler.go. |
| `POST /api/v1/dispatch/auto-assign` | hardscapeos_dibbits/backend/internal/dispatch/module.go:288 |
| `POST /api/v1/dispatch/drivers` | hardscapeos_dibbits/backend/internal/dispatch/module.go:213 |
| `POST /api/v1/dispatch/freight-split` | hardscapeos_dibbits/backend/internal/dispatch/module.go:280 |
| `POST /api/v1/dispatch/loads/mark-loaded` | hardscapeos_dibbits/backend/internal/dispatch/module.go:266 |
| `POST /api/v1/dispatch/runs` | hardscapeos_dibbits/backend/internal/dispatch/module.go:227 |
| `POST /api/v1/dispatch/runs/{id}/cancel` | hardscapeos_dibbits/backend/internal/dispatch/module.go:241 |
| `POST /api/v1/dispatch/runs/{id}/complete` | hardscapeos_dibbits/backend/internal/dispatch/module.go:240 |
| `POST /api/v1/dispatch/runs/{id}/crew` | hardscapeos_dibbits/backend/internal/dispatch/module.go:238 |
| `POST /api/v1/dispatch/runs/{id}/dispatch` | hardscapeos_dibbits/backend/internal/dispatch/module.go:239 |
| `POST /api/v1/dispatch/runs/{id}/manifest/print` | hardscapeos_dibbits/backend/internal/dispatch/module.go:243 |
| `POST /api/v1/dispatch/runs/{id}/reopen` | hardscapeos_dibbits/backend/internal/dispatch/module.go:276 |
| `POST /api/v1/dispatch/runs/{id}/reopen/clear` | hardscapeos_dibbits/backend/internal/dispatch/module.go:277 |
| `POST /api/v1/dispatch/runs/{id}/reschedule` | hardscapeos_dibbits/backend/internal/dispatch/handler_w3.go:40 |
| `POST /api/v1/dispatch/scans` | hardscapeos_dibbits/backend/internal/dispatch/handler_w3.go:44 |
| `POST /api/v1/dispatch/stops` | hardscapeos_dibbits/backend/internal/dispatch/module.go:217 |
| `POST /api/v1/dispatch/stops/{id}/assign` | hardscapeos_dibbits/backend/internal/dispatch/module.go:219 |
| `POST /api/v1/dispatch/stops/{id}/attach-photos` | hardscapeos_dibbits/backend/internal/dispatch/handler_w3.go:35 |
| `POST /api/v1/dispatch/stops/{id}/commit` | hardscapeos_dibbits/backend/internal/dispatch/module.go:278 |
| `POST /api/v1/dispatch/stops/{id}/load-facts` | hardscapeos_dibbits/backend/internal/dispatch/module.go:279 |
| `POST /api/v1/dispatch/stops/{id}/mto` | hardscapeos_dibbits/backend/internal/dispatch/handler_w3.go:34 |
| `POST /api/v1/dispatch/stops/{id}/photos` | hardscapeos_dibbits/backend/internal/dispatch/module.go:222 |
| `POST /api/v1/dispatch/stops/{id}/pod` | hardscapeos_dibbits/backend/internal/dispatch/module.go:221 |
| `POST /api/v1/dispatch/stops/{id}/reopen` | hardscapeos_dibbits/backend/internal/dispatch/module.go:275 |
| `POST /api/v1/dispatch/stops/{id}/reschedule` | hardscapeos_dibbits/backend/internal/dispatch/handler_w3.go:36 |
| `POST /api/v1/dispatch/stops/{id}/status` | hardscapeos_dibbits/backend/internal/dispatch/module.go:220 |
| `POST /api/v1/dispatch/trucks` | hardscapeos_dibbits/backend/internal/dispatch/module.go:211 |
| `POST /api/v1/dispatch/will-call` | hardscapeos_dibbits/backend/internal/dispatch/module.go:294 |
| `POST /api/v1/dispatch/will-call/{id}/revoke` | hardscapeos_dibbits/backend/internal/dispatch/module.go:296 |
| `POST /api/v1/feedback` | hardscapeos_dibbits/backend/internal/feedback/handler.go:36 |
| `POST /api/v1/finstmt/schedules` | hardscapeos_dibbits/backend/internal/finstmt/module.go:174 |
| `POST /api/v1/gl/accounts` | hardscapeos_dibbits/backend/internal/gl/module.go:385 |
| `POST /api/v1/gl/accounts/{code}/deactivate` | hardscapeos_dibbits/backend/internal/gl/module.go:386 |
| `POST /api/v1/gl/export` | hardscapeos_dibbits/backend/internal/gl/module.go:409 |
| `POST /api/v1/gl/journals` | hardscapeos_dibbits/backend/internal/gl/module.go:390 |
| `POST /api/v1/gl/journals/{id}/approve` | hardscapeos_dibbits/backend/internal/gl/module.go:391 |
| `POST /api/v1/gl/journals/{id}/reverse` | hardscapeos_dibbits/backend/internal/gl/module.go:393 |
| `POST /api/v1/gl/journals/{id}/void` | hardscapeos_dibbits/backend/internal/gl/module.go:392 |
| `POST /api/v1/gl/periods/{id}/close` | hardscapeos_dibbits/backend/internal/gl/module.go:404 |
| `POST /api/v1/gl/periods/{id}/reopen` | hardscapeos_dibbits/backend/internal/gl/module.go:405 |
| `POST /api/v1/gl/periods/close-year` | hardscapeos_dibbits/backend/internal/gl/module.go:406 |
| `POST /api/v1/gl/recurring/{id}/run` | hardscapeos_dibbits/backend/internal/gl/module.go:396 |
| `POST /api/v1/inventory/nudge-procurement` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:73 |
| `POST /api/v1/inventory/receive` | hardscapeos_dibbits/backend/internal/inventory/module.go:280 |
| `POST /api/v1/inventory/reorder-po` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:72 |
| `POST /api/v1/inventory/reorder-points/bootstrap` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:55 |
| `POST /api/v1/inventory/reservations` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:37 |
| `POST /api/v1/inventory/reservations/expire` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:39 |
| `POST /api/v1/inventory/reservations/release` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:38 |
| `POST /api/v1/inventory/shortfall/create-po` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:56 |
| `POST /api/v1/inventory/transfers` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:46 |
| `POST /api/v1/inventory/transfers/{id}/cancel` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:49 |
| `POST /api/v1/inventory/transfers/{id}/receive` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:48 |
| `POST /api/v1/inventory/transfers/{id}/ship` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:47 |
| `POST /api/v1/landed-costs` | hardscapeos_dibbits/backend/internal/landedcost/module.go:180 |
| `POST /api/v1/landed-costs/{id}/apply` | hardscapeos_dibbits/backend/internal/landedcost/module.go:183 |
| `POST /api/v1/landed-costs/{id}/retro` | hardscapeos_dibbits/backend/internal/landedcost/module.go:184 |
| `POST /api/v1/landed-costs/{id}/void` | hardscapeos_dibbits/backend/internal/landedcost/module.go:185 |
| `POST /api/v1/lien/documents` | hardscapeos_dibbits/backend/internal/lien/module.go:194 |
| `POST /api/v1/lien/holdback/{jobID}/release` | hardscapeos_dibbits/backend/internal/lien/module.go:195 |
| `POST /api/v1/lien/jobs` | hardscapeos_dibbits/backend/internal/lien/module.go:192 |
| `POST /api/v1/lien/jobs/{id}/sync` | hardscapeos_dibbits/backend/internal/lien/module.go:193 |
| `POST /api/v1/load/keys/resolve` | (addition) HH-LOAD-1 load doors: the machine legacy key resolution behind the load doors deployment switch; no ERP source route. |
| `POST /api/v1/messaging/channels/{id}/messages` | hardscapeos_dibbits/backend/internal/messaging/handler.go:59 |
| `POST /api/v1/messaging/channels/{id}/messages/{msgID}/ack` | hardscapeos_dibbits/backend/internal/messaging/handler.go:64 |
| `POST /api/v1/messaging/channels/{id}/read` | hardscapeos_dibbits/backend/internal/messaging/handler.go:61 |
| `POST /api/v1/messaging/house-conversations/{convID}/claim` | hardscapeos_dibbits/backend/internal/messaging/repassign_handler.go:38 |
| `POST /api/v1/messaging/inbound/customer` | hardscapeos_dibbits/backend/internal/messaging/repassign_handler.go:35 |
| `POST /api/v1/messaging/rep-assignments` | hardscapeos_dibbits/backend/internal/messaging/repassign_handler.go:28 |
| `POST /api/v1/messaging/stream-ticket` | hardscapeos_dibbits/backend/internal/messaging/handler.go:73 |
| `POST /api/v1/order-changes/{changeId}/apply` | hardscapeos_dibbits/backend/internal/order/change_handler.go:52 |
| `POST /api/v1/order-changes/{changeId}/cancel` | hardscapeos_dibbits/backend/internal/order/change_handler.go:51 |
| `POST /api/v1/order-changes/{changeId}/finance-approve` | hardscapeos_dibbits/backend/internal/order/change_handler.go:49 |
| `POST /api/v1/order-changes/{changeId}/reject` | hardscapeos_dibbits/backend/internal/order/change_handler.go:50 |
| `POST /api/v1/order-changes/{changeId}/yard-ack` | hardscapeos_dibbits/backend/internal/order/change_handler.go:48 |
| `POST /api/v1/orders/{id}/cancel` | hardscapeos_dibbits/backend/internal/order/handler.go:36 |
| `POST /api/v1/orders/{id}/changes` | hardscapeos_dibbits/backend/internal/order/change_handler.go:46 |
| `POST /api/v1/orders/{id}/confirm` | hardscapeos_dibbits/backend/internal/order/handler.go:33 |
| `POST /api/v1/orders/{id}/duplicate` | hardscapeos_dibbits/backend/internal/order/duplicate.go:586 |
| `POST /api/v1/orders/{id}/lines` | hardscapeos_dibbits/backend/internal/order/edit_handler.go:30 |
| `POST /api/v1/orders/{id}/notes` | hardscapeos_dibbits/backend/internal/order/history.go:220 |
| `POST /api/v1/orders/{id}/picking` | hardscapeos_dibbits/backend/internal/order/handler.go:34 |
| `POST /api/v1/orders/{id}/ready` | hardscapeos_dibbits/backend/internal/order/handler.go:35 |
| `POST /api/v1/orders/{id}/reservations/resync` | hardscapeos_dibbits/backend/internal/order/change_handler.go:42 |
| `POST /api/v1/orders/{id}/shipments/invoice` | hardscapeos_dibbits/backend/internal/order/change_handler.go:58 |
| `POST /api/v1/orders/{id}/shipments/invoice-policy` | hardscapeos_dibbits/backend/internal/order/change_handler.go:57 |
| `POST /api/v1/pos/assess-relief` | hardscapeos_dibbits/backend/internal/pos/handler.go:39 |
| `POST /api/v1/pos/counter-prices` | hardscapeos_dibbits/backend/internal/pos/handler.go:50 |
| `POST /api/v1/pos/margin-glance` | hardscapeos_dibbits/backend/internal/pos/handler.go:46 |
| `POST /api/v1/pos/parked` | hardscapeos_dibbits/backend/internal/pos/handler.go:81 |
| `POST /api/v1/pos/parked/{id}/resume` | hardscapeos_dibbits/backend/internal/pos/handler.go:83 |
| `POST /api/v1/pos/pricing-orders` | hardscapeos_dibbits/backend/internal/pos/handler.go:85 |
| `POST /api/v1/pos/return-requests/{id}/approve` | hardscapeos_dibbits/backend/internal/pos/return_requests.go:373 |
| `POST /api/v1/pos/return-requests/{id}/reject` | hardscapeos_dibbits/backend/internal/pos/return_requests.go:374 |
| `POST /api/v1/pos/returns` | hardscapeos_dibbits/backend/internal/pos/handler.go:76 |
| `POST /api/v1/pos/returns/{id}/pickup` | hardscapeos_dibbits/backend/internal/pos/handler.go:80 |
| `POST /api/v1/pos/sales` | hardscapeos_dibbits/backend/internal/pos/handler.go:34 |
| `POST /api/v1/pos/signatures` | hardscapeos_dibbits/backend/internal/pos/handler.go:40 |
| `POST /api/v1/pos/signatures/typed` | hardscapeos_dibbits/backend/internal/pos/signature_typed_handler.go:18 |
| `POST /api/v1/pricing/batches` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:18 |
| `POST /api/v1/pricing/batches/{id}/apply` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:24 |
| `POST /api/v1/pricing/batches/{id}/approve` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:23 |
| `POST /api/v1/pricing/batches/{id}/import` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:22 |
| `POST /api/v1/pricing/batches/apply-due` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:25 |
| `POST /api/v1/pricing/customers/{customerId}/overrides` | hardscapeos_dibbits/backend/internal/pricing/handler.go:81 |
| `POST /api/v1/pricing/price-file/apply` | hardscapeos_dibbits/backend/internal/pricing/pricefile_handler.go:45 |
| `POST /api/v1/pricing/price-file/preview` | hardscapeos_dibbits/backend/internal/pricing/pricefile_handler.go:44 |
| `POST /api/v1/pricing/promotions` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:34 |
| `POST /api/v1/pricing/quantity-breaks` | hardscapeos_dibbits/backend/internal/pricing/handler.go:97 |
| `POST /api/v1/pricing/rebates/accrue` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:49 |
| `POST /api/v1/pricing/rebates/programs` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:45 |
| `POST /api/v1/pricing/reports/low-margin` | hardscapeos_dibbits/backend/internal/pricing/handler.go:87 |
| `POST /api/v1/pricing/resolve` | hardscapeos_dibbits/backend/internal/pricing/handler.go:66 |
| `POST /api/v1/pricing/rules` | hardscapeos_dibbits/backend/internal/pricing/handler.go:71 |
| `POST /api/v1/pricing/uom/plan` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:52 |
| `POST /api/v1/purchasing/bills` | hardscapeos_dibbits/backend/internal/purchasing/module.go:364 |
| `POST /api/v1/purchasing/bills/{id}/post` | hardscapeos_dibbits/backend/internal/purchasing/module.go:366 |
| `POST /api/v1/purchasing/pos` | hardscapeos_dibbits/backend/internal/purchasing/module.go:342 |
| `POST /api/v1/purchasing/pos/{id}/lines` | hardscapeos_dibbits/backend/internal/purchasing/module.go:344 |
| `POST /api/v1/purchasing/pos/{id}/receive` | hardscapeos_dibbits/backend/internal/purchasing/module.go:347 |
| `POST /api/v1/purchasing/pos/{id}/send` | hardscapeos_dibbits/backend/internal/purchasing/module.go:396 |
| `POST /api/v1/purchasing/pos/{id}/transition` | hardscapeos_dibbits/backend/internal/purchasing/module.go:346 |
| `POST /api/v1/purchasing/receipts/{id}/landed-cost` | hardscapeos_dibbits/backend/internal/purchasing/module.go:361 |
| `POST /api/v1/quote-assist/extract` | hardscapeos_dibbits/backend/internal/quoteassist/handler.go:55 |
| `POST /api/v1/quotes` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:34 |
| `POST /api/v1/quotes/{id}/convert` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:47 |
| `POST /api/v1/quotes/{id}/followups/disable` | hardscapeos_dibbits/backend/internal/quote/followup_handler.go:35 |
| `POST /api/v1/quotes/{id}/followups/enable` | hardscapeos_dibbits/backend/internal/quote/followup_handler.go:36 |
| `POST /api/v1/quotes/{id}/holds/release` | hardscapeos_dibbits/backend/internal/quote/reserve_handler.go:28 |
| `POST /api/v1/quotes/{id}/holds/resync` | hardscapeos_dibbits/backend/internal/quote/reserve_handler.go:27 |
| `POST /api/v1/quotes/{id}/lines` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:38 |
| `POST /api/v1/quotes/{id}/lines/{lineNo}/compare` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:50 |
| `POST /api/v1/quotes/{id}/share-links` | hardscapeos_dibbits/backend/internal/quote/share_handler.go:23 |
| `POST /api/v1/quotes/{id}/sms-share` | hardscapeos_dibbits/backend/internal/quote/followup_handler.go:33 |
| `POST /api/v1/reporting/commission/rules` | hardscapeos_dibbits/backend/internal/reporting/handler.go:53 |
| `POST /api/v1/reporting/commission/runs` | hardscapeos_dibbits/backend/internal/reporting/handler.go:56 |
| `POST /api/v1/reporting/commission/runs/{id}/print` | hardscapeos_dibbits/backend/internal/reporting/handler.go:60 |
| `POST /api/v1/reporting/saved-views` | hardscapeos_dibbits/backend/internal/reporting/handler.go:49 |
| `POST /api/v1/reporting/templates/{code}/print` | hardscapeos_dibbits/backend/internal/reporting/handler.go:42 |
| `POST /api/v1/shipment-groups/{groupId}/status` | hardscapeos_dibbits/backend/internal/order/change_handler.go:59 |
| `POST /api/v1/staff` | hardscapeos_dibbits/backend/internal/identity/handler.go:59 |
| `POST /api/v1/staff/{id}/password-reset` | hardscapeos_dibbits/backend/internal/identity/handler.go:63 |
| `POST /api/v1/staff/approvals` | hardscapeos_dibbits/backend/internal/identity/handler.go:67 |
| `POST /api/v1/staff/approvals/{id}/decide` | hardscapeos_dibbits/backend/internal/identity/handler.go:69 |
| `POST /api/v1/staff/policy/check` | hardscapeos_dibbits/backend/internal/identity/handler.go:50 |
| `POST /api/v1/sync/{scope}` | (addition) Context 4.5 sync protocol, the outbox replay; the ERP has no sync endpoint. |
| `POST /api/v1/tasks` | hardscapeos_dibbits/backend/internal/tasks/handler.go:38 |
| `POST /api/v1/tasks/{id}/accept` | hardscapeos_dibbits/backend/internal/tasks/handler.go:41 |
| `POST /api/v1/tasks/{id}/cancel` | hardscapeos_dibbits/backend/internal/tasks/handler.go:43 |
| `POST /api/v1/tasks/{id}/claim` | hardscapeos_dibbits/backend/internal/tasks/handler.go:40 |
| `POST /api/v1/tasks/{id}/done` | hardscapeos_dibbits/backend/internal/tasks/handler.go:42 |
| `POST /api/v1/tax/config` | hardscapeos_dibbits/backend/internal/tax/module.go:177 |
| `POST /api/v1/tax/evidence` | hardscapeos_dibbits/backend/internal/tax/module.go:184 |
| `POST /api/v1/tax/evidence/{id}/download` | hardscapeos_dibbits/backend/internal/tax/module.go:186 |
| `POST /api/v1/tax/evidence/{id}/validate` | hardscapeos_dibbits/backend/internal/tax/module.go:187 |
| `POST /api/v1/tax/profiles` | hardscapeos_dibbits/backend/internal/tax/module.go:175 |
| `POST /api/v1/tax/rates` | hardscapeos_dibbits/backend/internal/tax/module.go:173 |
| `POST /api/v1/tax/recompute` | hardscapeos_dibbits/backend/internal/tax/module.go:178 |
| `POST /api/v1/tenant/accept-invite` | hardscapeos_dibbits/backend/internal/portal/handler.go:90 |
| `POST /api/v1/tenant/invites` | hardscapeos_dibbits/backend/internal/portal/handler.go:96 |
| `POST /api/v1/tenant/login` | hardscapeos_dibbits/backend/internal/portal/handler.go:89 |
| `POST /api/v1/tenant/me/password` | hardscapeos_dibbits/backend/internal/portal/settings.go:18 |
| `POST /api/v1/tenant/orders/{id}/reorder` | hardscapeos_dibbits/backend/internal/portal/reorder.go:37 |
| `POST /api/v1/tenant/orders/{id}/return-requests` | hardscapeos_dibbits/backend/internal/portal/returns.go:70 |
| `POST /api/v1/tenant/password-reset/confirm` | hardscapeos_dibbits/backend/internal/portal/password_reset.go:22 |
| `POST /api/v1/tenant/password-reset/request` | hardscapeos_dibbits/backend/internal/portal/password_reset.go:21 |
| `POST /api/v1/tenant/payment-methods` | hardscapeos_dibbits/backend/internal/portal/payment_methods.go:16 |
| `POST /api/v1/tenant/payments` | hardscapeos_dibbits/backend/internal/portal/payments.go:55 |
| `POST /api/v1/tenant/procurement/projects` | hardscapeos_dibbits/backend/internal/portal/procurement.go:24 |
| `POST /api/v1/tenant/procurement/projects/{id}/lines` | hardscapeos_dibbits/backend/internal/portal/procurement.go:28 |
| `POST /api/v1/tenant/procurement/projects/{id}/submit-rfq` | hardscapeos_dibbits/backend/internal/portal/procurement.go:31 |
| `POST /api/v1/tenant/quotes/{id}/accept` | hardscapeos_dibbits/backend/internal/portal/quotes.go:17 |
| `POST /api/v1/tenant/quotes/{id}/convert` | hardscapeos_dibbits/backend/internal/portal/quotes.go:19 |
| `POST /api/v1/tenant/quotes/{id}/reject` | hardscapeos_dibbits/backend/internal/portal/quotes.go:18 |
| `POST /api/v1/tenant/token/refresh` | hardscapeos_dibbits/backend/internal/portal/refresh.go:106 |
| `POST /api/v1/till/sessions` | hardscapeos_dibbits/backend/internal/till/handler.go:34 |
| `POST /api/v1/till/sessions/{id}/complete` | hardscapeos_dibbits/backend/internal/till/handler.go:42 |
| `POST /api/v1/till/sessions/{id}/count` | hardscapeos_dibbits/backend/internal/till/handler.go:40 |
| `POST /api/v1/till/sessions/{id}/movements` | hardscapeos_dibbits/backend/internal/till/handler.go:39 |
| `POST /api/v1/warranty/claims` | hardscapeos_dibbits/backend/internal/warranty/module.go:140 |
| `POST /api/v1/warranty/claims/{id}/advance` | hardscapeos_dibbits/backend/internal/warranty/module.go:145 |
| `POST /api/v1/warranty/claims/{id}/assign` | hardscapeos_dibbits/backend/internal/warranty/module.go:143 |
| `POST /api/v1/warranty/claims/{id}/checklist` | hardscapeos_dibbits/backend/internal/warranty/module.go:144 |
| `POST /api/v1/warranty/claims/{id}/evidence` | hardscapeos_dibbits/backend/internal/warranty/module.go:146 |
| `POST /api/v1/warranty/claims/{id}/recovery` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:31 |
| `POST /api/v1/warranty/claims/{id}/settle` | hardscapeos_dibbits/backend/internal/warranty/module.go:149 |
| `POST /api/v1/warranty/claims/{id}/sla/{alertId}/ack` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:43 |
| `POST /api/v1/warranty/sla/sweep` | hardscapeos_dibbits/backend/internal/warranty/handler_w5b.go:42 |
| `POST /api/v1/warranty/vendors/{vendorID}/overrides` | hardscapeos_dibbits/backend/internal/warranty/module.go:133 |
| `POST /api/v1/yard/picks/{id}/shortages` | hardscapeos_dibbits/backend/internal/tasks/yard_handler.go:34 |
| `POST /api/v1/yard/picks/{id}/shortages/{sid}/resolve` | hardscapeos_dibbits/backend/internal/tasks/yard_handler.go:35 |
| `POST /api/v1/yard/picks/{id}/stage` | hardscapeos_dibbits/backend/internal/tasks/yard_handler.go:32 |
| `POST /api/v1/yard/picks/{id}/weight` | hardscapeos_dibbits/backend/internal/tasks/weight_handler.go:30 |
| `POST /api/v1/yard/weight-requests` | hardscapeos_dibbits/backend/internal/tasks/weight_handler.go:29 |
| `PUT /api/v1/ar/auto-hold` | hardscapeos_dibbits/backend/internal/billing/monthend_handler.go:56 |
| `PUT /api/v1/ar/customers/{customerID}/ap-contact` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:67 |
| `PUT /api/v1/ar/landing/policy` | hardscapeos_dibbits/backend/internal/billing/ar_landing_handler.go:53 |
| `PUT /api/v1/ar/nsf-policy` | hardscapeos_dibbits/backend/internal/till/handler.go:54 |
| `PUT /api/v1/ar/register/policy` | hardscapeos_dibbits/backend/internal/billing/register_handler.go:93 |
| `PUT /api/v1/assist/admin/limits/role/{role}` | hardscapeos_dibbits/backend/internal/assist/handler.go:65 |
| `PUT /api/v1/assist/admin/limits/user/{user}` | hardscapeos_dibbits/backend/internal/assist/handler.go:66 |
| `PUT /api/v1/assist/admin/settings` | hardscapeos_dibbits/backend/internal/assist/handler.go:69 |
| `PUT /api/v1/compliance/review-status` | hardscapeos_dibbits/backend/internal/compliance/module.go:120 |
| `PUT /api/v1/customers/{id}/billing-address` | hardscapeos_dibbits/backend/internal/customer/handler.go:44 |
| `PUT /api/v1/customers/{id}/sms-consent` | hardscapeos_dibbits/backend/internal/customer/handler.go:48 |
| `PUT /api/v1/dashboard/layout` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:51 |
| `PUT /api/v1/dashboard/profiles/{profile}` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:61 |
| `PUT /api/v1/dashboard/profiles/{profile}/gallery` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:62 |
| `PUT /api/v1/dashboard/profiles/{profile}/home` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:62 |
| `PUT /api/v1/dashboard/profiles/{profile}/tabs` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:58 |
| `PUT /api/v1/dashboard/profiles/{profile}/widgets` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:60 |
| `PUT /api/v1/dashboard/tabs` | hardscapeos_dibbits/backend/internal/dashboard/handler.go:55 |
| `PUT /api/v1/dashboard/widgets` | hardscapeos_dibbits/backend/internal/dashboard/widget_handler.go:53 |
| `PUT /api/v1/dispatch/policy` | hardscapeos_dibbits/backend/internal/dispatch/module.go:271 |
| `PUT /api/v1/finstmt/schedules/{id}` | hardscapeos_dibbits/backend/internal/finstmt/module.go:175 |
| `PUT /api/v1/inventory/allocation-policy` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:41 |
| `PUT /api/v1/inventory/reorder-points` | hardscapeos_dibbits/backend/internal/inventory/stock_truth_handler.go:54 |
| `PUT /api/v1/inventory/transit-days` | hardscapeos_dibbits/backend/internal/inventory/module.go:295 |
| `PUT /api/v1/load/branches` | (addition) HH-LOAD-1 load doors: the branch map, names against existing branch rows, behind the load doors deployment switch; no ERP source route. |
| `PUT /api/v1/load/categories` | (addition) HH-LOAD-1 load doors: the product category upsert, the parent named by legacy key, behind the load doors deployment switch; no ERP source route. |
| `PUT /api/v1/load/products` | (addition) HH-LOAD-1 load doors (LD-5): the product master upsert by legacy key, the SKU the natural key and the category by key, behind the load doors deployment switch; no ERP source route. |
| `PUT /api/v1/load/products/{legacyId}/image` | (addition) HH-LOAD-1 load doors (LD-5): the product image put through the product module's own image store and size cap, the product named by legacy key; no ERP source route. |
| `PUT /api/v1/load/products/unit-sets` | (addition) HH-LOAD-1 load doors (LD-3): the product unit set replace, the product named by legacy key, behind the load doors deployment switch; no ERP source route. |
| `PUT /api/v1/load/supplier-skus` | (addition) HH-LOAD-1 load doors (LD-5): the supplier SKU upsert through the purchasing module's writer, the product and supplier each named by legacy key; no ERP source route. |
| `PUT /api/v1/load/suppliers` | (addition) HH-LOAD-1 load doors: the supplier upsert with its contact through the purchasing contact store, behind the load doors deployment switch; no ERP source route. |
| `PUT /api/v1/load/units` | (addition) HH-LOAD-1 load doors (LD-3): the unit catalogue upsert, a unit that already stands with the same code and dimension attached, behind the load doors deployment switch; no ERP source route. |
| `PUT /api/v1/load/users` | (addition) HH-LOAD-1 load doors: the user map, names against existing staff rows by verified email, behind the load doors deployment switch; no ERP source route. |
| `PUT /api/v1/messaging/channels/{id}/notify-prefs` | hardscapeos_dibbits/backend/internal/messaging/handler.go:68 |
| `PUT /api/v1/messaging/event-classes/{topic}` | hardscapeos_dibbits/backend/internal/messaging/handler.go:88 |
| `PUT /api/v1/messaging/payment-rules/{rule}` | hardscapeos_dibbits/backend/internal/messaging/handler.go:94 |
| `PUT /api/v1/operator/dealer-credential` | (addition) D4 write-only dealer credential route (validated before stored, never returned); source hh_pro_dibbits/server/admin-store.ts storeKey. |
| `PUT /api/v1/operator/platform-keys/{key}` | (addition) D4 operator write for the launcher; the server marks the write; no ERP source. |
| `PUT /api/v1/orders/{id}/shipments` | hardscapeos_dibbits/backend/internal/order/change_handler.go:56 |
| `PUT /api/v1/payments/config` | hardscapeos_dibbits/backend/internal/payment/handler.go:49 |
| `PUT /api/v1/platform/hotkeys/{surface}` | hardscapeos_dibbits/backend/internal/settings/handler.go:45 |
| `PUT /api/v1/platform/tour/policy` | hardscapeos_dibbits/backend/internal/tour/handler.go:50 |
| `PUT /api/v1/platform/tour/steps` | hardscapeos_dibbits/backend/internal/tour/handler.go:49 |
| `PUT /api/v1/pos/policy` | hardscapeos_dibbits/backend/internal/pos/handler.go:65 |
| `PUT /api/v1/pos/workspace` | hardscapeos_dibbits/backend/internal/pos/handler.go:74 |
| `PUT /api/v1/pricing/category-policies/{categoryId}` | hardscapeos_dibbits/backend/internal/pricing/handler.go:77 |
| `PUT /api/v1/pricing/clearance/{productId}` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:40 |
| `PUT /api/v1/pricing/import-profiles/{id}` | hardscapeos_dibbits/backend/internal/pricing/batch_handler.go:28 |
| `PUT /api/v1/pricing/overrides/{id}` | hardscapeos_dibbits/backend/internal/pricing/handler.go:82 |
| `PUT /api/v1/pricing/promotions/{id}` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:35 |
| `PUT /api/v1/pricing/quantity-breaks/{id}` | hardscapeos_dibbits/backend/internal/pricing/handler.go:98 |
| `PUT /api/v1/pricing/rebates/programs/{id}` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:46 |
| `PUT /api/v1/pricing/rules/{id}` | hardscapeos_dibbits/backend/internal/pricing/handler.go:72 |
| `PUT /api/v1/pricing/uom/conversions` | hardscapeos_dibbits/backend/internal/pricing/w4_handler.go:54 |
| `PUT /api/v1/products/{id}` | hardscapeos_dibbits/backend/internal/product/handler.go:42 |
| `PUT /api/v1/products/{id}/image` | hardscapeos_dibbits/backend/internal/product/handler.go:102 |
| `PUT /api/v1/purchasing/policy` | hardscapeos_dibbits/backend/internal/purchasing/module.go:383 |
| `PUT /api/v1/purchasing/pos/{id}/delivery-method` | hardscapeos_dibbits/backend/internal/purchasing/module.go:357 |
| `PUT /api/v1/purchasing/pos/{id}/expected-date` | hardscapeos_dibbits/backend/internal/purchasing/module.go:353 |
| `PUT /api/v1/purchasing/tolerance` | hardscapeos_dibbits/backend/internal/purchasing/module.go:369 |
| `PUT /api/v1/purchasing/vendors/{vendorID}/contact` | hardscapeos_dibbits/backend/internal/purchasing/module.go:386 |
| `PUT /api/v1/purchasing/vendors/{vendorID}/skus` | hardscapeos_dibbits/backend/internal/purchasing/module.go:389 |
| `PUT /api/v1/quick-actions/config/{action}` | hardscapeos_dibbits/backend/internal/dashboard/quickaction_handler.go:41 |
| `PUT /api/v1/quotes/{id}/terms` | hardscapeos_dibbits/backend/internal/quote/builder_handler.go:27 |
| `PUT /api/v1/quotes/policy` | hardscapeos_dibbits/backend/internal/quote/builder_handler.go:25 |
| `PUT /api/v1/quotes/settings/acceptance` | hardscapeos_dibbits/backend/internal/quote/edit_handler.go:62 |
| `PUT /api/v1/staff/{id}/role` | hardscapeos_dibbits/backend/internal/identity/handler.go:60 |
| `PUT /api/v1/surfaces/{surface}/items` | hardscapeos_dibbits/backend/internal/settings/handler.go:31 |
| `PUT /api/v1/surfaces/{surface}/my-items` | hardscapeos_dibbits/backend/internal/settings/handler.go:33 |
| `PUT /api/v1/surfaces/{surface}/my-workspace` | hardscapeos_dibbits/backend/internal/settings/handler.go:35 |
| `PUT /api/v1/tenant/me` | hardscapeos_dibbits/backend/internal/portal/settings.go:17 |
| `PUT /api/v1/tenant/orders/{id}/delivery-instructions` | hardscapeos_dibbits/backend/internal/portal/orders.go:22 |
| `PUT /api/v1/tenant/payment-methods/{id}/default` | hardscapeos_dibbits/backend/internal/portal/payment_methods.go:18 |
| `PUT /api/v1/tenant/procurement/projects/{id}` | hardscapeos_dibbits/backend/internal/portal/procurement.go:26 |
| `PUT /api/v1/tenant/procurement/projects/{id}/lines/{lineId}` | hardscapeos_dibbits/backend/internal/portal/procurement.go:29 |
| `PUT /api/v1/tenant/users/{id}/role` | hardscapeos_dibbits/backend/internal/portal/handler.go:97 |
| `PUT /api/v1/tenant/users/{id}/status` | hardscapeos_dibbits/backend/internal/portal/handler.go:98 |
| `PUT /api/v1/tenants/{tenantId}/brand` | (addition) D12 tenant_brand staff write for the dealer-admin surface; no ERP source. |
| `PUT /api/v1/tour/progress` | hardscapeos_dibbits/backend/internal/tour/handler.go:45 |
| `PUT /api/v1/warranty/vendors/{vendorID}` | hardscapeos_dibbits/backend/internal/warranty/module.go:132 |
| `PUT /api/v1/yard/picks/{id}/lines` | hardscapeos_dibbits/backend/internal/tasks/yard_handler.go:33 |

<!-- SOURCE-MAP:END -->
