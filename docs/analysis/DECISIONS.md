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

## Carried as verification, not decided

Whether Appwrite Team memberships surface as OIDC claims stays a S2 check. D3 stands either way: if claims are absent, a Function enriches userinfo and the engine resolves the coarse gate from Team membership by lookup.

## Still open, by design

- Q8 (per-role landing) at HH-6's mid checkpoint with Colton; default is the switcher seeded from the served row.
- Q11 (warranty micro-app) at HH-9's design sync with Colton.
- The state of the delivery's BisTrack phase at HH-11's start checkpoint.
