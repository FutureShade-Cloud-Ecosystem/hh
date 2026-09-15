# ADR-0019: Contract entities (order and OrderCard, the two quotes, InvoiceView, attachment, product_image, tenant_brand)

Status: Proposed

Drafted in HH-2 lane HH-2.4 from `analysis/DECISIONS.md` D7 to D12, which Colton took at the close of HH-1. These six decisions fix the entity shapes the OpenAPI document (HH-2.3) is authored against. This draft records the decisions; it does not reopen them.

## D7: One canonical order aggregate, the board card as a read projection (resolves C19)

Decision: one canonical `order` aggregate in `core`. The contractor's board card is a read projection, `OrderCard`, defined in the OpenAPI document with an explicit field map and a documented state map from dealer order states to the contractor vocabulary. The state map is total: every value in the dealer order status CHECK set and every value in HH Pro's stage union is mapped, with no unmapped state on either side. No second entity, no second writer. The alias column from D6 carries the contractor-side id through the mapping period, and every deep link `fb://hh/order/<id>` carries the canonical id only.

Options rejected, and what each loses:

- **The ERP grain (the board card is the project).** What it loses, quoted from `domain-map.md` C19: "the ERP grain wins and a job with material in three different states has one lane and the stage question comes back." HH Pro's product rule states what the projection preserves: "Board cards are Orders, not Projects... This is why every card has exactly one unambiguous stage, there is no 'which stage is this half-delivered job in' question to answer" (`domain-map.md` C19, quoting `hh_pro_dibbits/CLAUDE.md`).
- **The portal grain as a second entity (two writers).** What the decision costs the ERP side, unsmoothed: "the ERP's planning board, its lane transitions, its indicative RFQ pricing and its rollup all have to be rebuilt around a finer card" (`domain-map.md` C19). Kept as a projection rather than a second entity, because a second writer would make permanent the status collapse the adapter already performs ("`PARTIALLY_FULFILLED` becomes `out-for-delivery`, `CLOSED` and `INVOICED` collapse to one value", `domain-map.md` C4).

## D8: Two quote resources (resolves C15)

Decision: two resources. `dealer_quote` (dealer to contractor, priced by `core` under D1, carries `valid_until`) and `customer_quote` (contractor to homeowner, carries the acceptance artifact and the magic-link token). They share the `quote_line` shape and nothing else. Different senders, lifecycles and acceptance semantics rule out a `kind` field.

Option rejected: **one quote resource with a `kind` discriminator.** What each collapse loses, quoted from `domain-map.md` C15: "collapsing them onto the ERP quote loses the contractor's markup, labour, overhead and homeowner signature entirely (the ERP has no table for any of it); collapsing them onto `CustomerQuote` loses the desk note, the conversion link, the soft holds and the credit gate." The operational risk the naming rule closes: "a screen, an event topic or an API path that says 'quote' without saying whose is ambiguous in a merged monorepo" (`domain-map.md` C15).

## D9: The ERP invoice canonical, InvoiceView for tenants (resolves C25)

Decision: the ERP invoice is canonical: lines, tax, holdback, the AR subledger tie. The portal exposes `InvoiceView`: lines, tax and holdback totals, balance due, payment history. Contractors never see subledger internals; payments post through `core` and the fee rule from HH-3.6 applies.

Option rejected: **the portal's invoice-as-balance model.** What it loses, quoted from `domain-map.md` C25: "the ledger loses the tax split, the holdback carve-out, the subledger and the same-transaction GL guarantee, each of which the ERP design calls out as a defect it was built to avoid." A standing consequence the view must honour: the adapter at present "paper[s] over the largest gap by mapping the ERP total (tax included) into the field HH Pro calls `subtotal`", which the map calls "a working mitigation and a standing misnomer"; the projection "must stop calling a tax-inclusive total a subtotal" (`domain-map.md` C25 and section 5.4). Holdback is exposed as a total precisely because "a contractor must not pay the legally-retained portion" (`domain-map.md` section 3.6, quoting `docs/contractor-portal-plan.md`).

## D10: attachment and attachment_link (resolves C37)

Decision: `attachment` in `core`: id, `org_id`, nullable `tenant_id`, `kind` (photo, proof_of_delivery, document, signature, product_image), `appwrite_file_id`, bucket, mime, size, checksum, `created_by`, `created_at`, `deleted_at`. Bytes live in Appwrite Storage. A polymorphic `attachment_link` (`attachment_id`, `owner_kind`, `owner_id`, `role`) attaches one file to orders, plans, stops, receiving and quotes without per-owner columns. Offline scopes carry attachments through `attachments_queue` as the offline framework defines.

Options rejected, and what each loses:

- **Per-owner references (the ERP's shape at present).** "a photo reference on a task (`0073`), dispatch proof-of-delivery photos (`0091`), invoice documents (`0146`), masked evidence references in `tax_evidence` (`0042`)" (`domain-map.md` section 3.8). What it loses: the single entity the offline framework presupposes, since "the context file's section 4.5 names an `attachments_queue` in the offline store, which presupposes one entity" (`domain-map.md` section 5.5), and one retention, tombstone and audit story per kind.
- **Bytes embedded in domain records (HH Pro's signature data URL).** What it loses: bytes out of the database, a storage reference the sync protocol can queue, and a checksum the client can verify. The drawn signature survives as `kind = signature`.

## D11: One product_image model, swatch_hex as fallback (resolves C8)

Decision: one `product_image` model. An uploaded image is canonical whenever it exists; the measured swatch is stored as an attribute (`swatch_hex`) and rendered only as a fallback, never as an image when a real one exists. The swatch generator stays a script, not a runtime feature.

Options rejected, and what each loses:

- **Swatches as the only imagery (HH Pro's shape at present).** Loses real photography where it exists (the ERP's `product_images`, `0102`). The swatch doctrine stands as the fallback rule, quoted from `domain-map.md` C8: "a contractor ordering 640 sf of paver must never be shown an image implying a finish that is not what arrives on the pallet." A drawn swatch from a measured colour makes no such implication; a missing swatch in the generator's output remains an error at seed time, not a runtime branch.
- **Uploaded bytes only, missing is an empty state (the ERP's shape at present).** Loses the honest fallback for catalogue rows with no photography, which is most of them at present.

## D12: tenant_brand under tenant scope, inside the HH Pro frame (resolves C14)

Decision: a `tenant_brand` record in `core` under tenant scope: display name, logo attachment, primary colour, footer text. Rendered inside the HH Pro frame on customer quotes and the public desk, per D2's ruling that acceptance renders under HH Pro's brand; the dealer's tokens never appear on a contractor's document.

Options rejected, and what each loses:

- **Contractor branding as contractor-held device data.** The magic-link acceptance page renders with no device to read from, so a device-local brand cannot appear on the one document that needs it most. C14 frames the question: "A unified model must decide whether contractor branding is dealer-held tenant data or contractor-held device data" (`domain-map.md` C14); the decision is dealer-held tenant data, owned by the tenant.
- **Dealer configuration as the only branding (the ERP's shape at present).** Loses the contractor's brand on the proposal, which HH Pro's design doctrine requires: the contractor layer is "used only on customer-facing quote pages, where the contractor's brand must beat the dealer's" (`domain-map.md` C14, quoting `hh_pro_dibbits/CLAUDE.md`). C14 also records the shape-level gap this closes: the App Shape's per-frontend `brand` property "is the dealer or product brand, not the contractor's; the contractor's own brand has no home in the App Shape as written."

## Sessions gated

- HH-2.3: the OpenAPI document defines all six shapes, the `OrderCard` field map and its total state map, and the shared `quote_line` reference.
- HH-3.1: the schema carries the `legacy_contractor_id` alias column (D6), `customer_quote` and its acceptance record, and `attachment` with `attachment_link`.
- HH-3.6: the customer-quote freeze, token rotation on revise, the signed acceptance artifact and the payment fee rule become core behaviour on these entities.
- HH-10: HH-10.1's board reads canonical ids; customer quotes and the public desk render `tenant_brand` inside the HH Pro frame.
