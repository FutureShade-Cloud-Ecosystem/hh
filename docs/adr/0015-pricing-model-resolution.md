# ADR-0015: Pricing-model resolution

Status: Proposed

Drafted in HH-2 lane HH-2.4 from `analysis/DECISIONS.md` D1, which Colton took at the close of HH-1. The context file's ADR index (section 12) already holds a pending slot for this decision ("Pricing-model resolution, first candidate from HH-1"). This draft records the decision; it does not reopen it.

## Decision

One deterministic resolution order, computed by the core and authoritative:

1. A contract price, if one exists, is final and beats everything.
2. Otherwise the base price, then a quantity break that **replaces** the tier rather than stacking on it.
3. Promotions are a separate, time-bounded level that never stacks.

The customer gets the best single price, never a pile of discounts. HH Pro shows a cached price band offline, labelled as an estimate until the core confirms on sync.

Implementation consequences, as recorded in D1:

- The core's resolver implements this order from HH-3. The running ERP's resolver is untouched until its roles switch in HH-7, and no pricing rule row changes.
- Where the two resolvers would answer differently for the same customer, product and quantity, HH-7.3's parity test lists the differences as a report for the sales manager before the sales switch, so nobody discovers a changed price at the counter.

## Conflicts resolved

From `analysis/domain-map.md`: C45 (contract finality, the ERP's rule), C43 (replace rather than stack, HH Pro's rule, reversing the ERP's `0026` change), C38 (promotion as a separate never-stacking level), C55 (the core computes; the client's band is an estimate, never authoritative), and by consequence C46 (the margin gate applies inside the core's order), C52 (validity is a property of the confirmed price, so the band's estimate label expires with it) and C53 (the next-break hint is computed by the core under the replace rule).

## Options rejected, and what each loses

### A quantity break stacks on the resolved base (the ERP's `0026` model)

Rejected in favour of replacement. The ERP's own header records its change in the other direction: "a quantity break no longer COMPETES at a precedence level (it used to REPLACE the customer's tier/category discount)", and its resolver comment gives the stacking arithmetic: an eighteen percent base then a five percent full-skid break yields retail times 0.82 times 0.95 (`domain-map.md` C43, quoting migration `0026`'s header and the resolver comment).

What rejecting stacking loses, stated plainly because this is the side the decision reverses: "Contractors lose, under the ERP model: nothing in most cases, and they gain the bigger discount (stacking is cheaper than replacing whenever the base already beat the break)" (`domain-map.md` C43). Under the decided replace model the customer gets the best single price, which can be higher than the stacked result in exactly those cases. The counter loses the other side of the same conflict, quoted in full: "the fix `0026` exists to hold, namely a bulk break wiping a contractor's negotiated tier discount, and the partial-skid surcharge, which has no meaning at all in a best-of model (an uplift can never win a 'cheapest wins' contest)" (`domain-map.md` C43).

What rejecting stacking protects: the replace rule is the one HH Pro's contractor-facing hints are built on, and the decision keeps them honest: "a portal that migrates without re-deriving that hint will advertise a break that is not the price the ERP would charge" (`domain-map.md` C43) is the failure the core-computed hint (C53 below) exists to prevent.

### A contract price admits a break written for the same sku (HH Pro's position)

Rejected in favour of finality. HH Pro's `volumeBreaksFor` argues: "A break written for THIS sku is different: that is a deliberate volume tier on the contracted item, so it still applies" (`domain-map.md` C45).

What rejecting it loses: "a volume tier their rep deliberately wrote against the contracted sku stops applying, which in the seeded HH Pro data is a live case (a contract price on the paver they lay most sits in a category that also carries volume breaks)" (`domain-map.md` C45).

What rejecting it protects, in the ERP's words: "A negotiated CUSTOMER_PRODUCT contract price is FINAL, no break (or override) applies on top of it", and the finality is treated as a promise: "a 'sale' that changes it is a broken promise in either direction" (`domain-map.md` C45).

### Promotions stack on the tier-resolved base

Rejected. The ERP's promo file records why, quoted in `domain-map.md` section 4b: "passing finalCents here compiles, passes a naive test, and quietly gives a contractor 18% off AND 25% off... '20% off' means 20% off the shelf price, that is what the sign in the yard says." Promotions are a separate level, computed off the retail anchor, that wins only when it beats the base and never combines with it.

### No promotion level at all (HH Pro's model)

Rejected. What it loses: "the entire seasonal and clearance apparatus (`0111`, `backend/internal/pricing/promo.go`), including the never-worse guard whose absence the file calls 'the single most embarrassing thing a pricing engine can do', and the clearance badge that POS search and quote lines render" (`domain-map.md` C38).

### The browser computes the authoritative price (HH Pro's model)

Rejected in favour of core-computed authority. What contractors lose under the decision: "a price on a catalogue row without a network round trip, and (until a cost source exists) an authoritative price at all on the request-for-quote path" (`domain-map.md` C55); the cached price band (labelled an estimate until sync confirms) is the deliberate mitigation for the first of the two. What rejecting browser authority protects: "the margin gate, because a price computed in a browser cannot be blocked by a floor the browser must not know, and the zero-trust posture that keeps the dealer's cost off the wire" (`domain-map.md` C55).

Offline consequence, from `domain-map.md` section 6: the honest cached artifact is the resolved output per product and quantity band, never the rule inputs, because "caching the rules lets a client resolve a price the margin gate never saw."

## Consequences for the related conflicts

- C46: the margin gate, the floor and the override authority apply inside the core's order flow, with a bypass row for every skipped or cleared gate, because only the server side can answer "how many below-floor prices went out and on whose authority" (`domain-map.md` C46).
- C52: validity is a property of the confirmed price, so the band's estimate label expires with it. The validity grain itself (document-level `dealer_quote.valid_until`, per-line `price_snapshot` for audit) is D14, resolved in the schema session.
- C53: the next-break hint is computed by the core under the replace rule, and keeps both sides' shared honesty rule: "present only when one exists AND it actually beats the price they are getting now" (`domain-map.md` section 4a, quoting `backend/internal/pricing/counter.go`).

## Sessions gated

- HH-3: the schema lane cannot freeze the `price_matrix` offline scope, and HH-3.6 cannot implement the resolver, without this decision (`analysis/gap-analysis.md` section 9 names pricing the first ADR for exactly this reason).
- HH-7: the counter's resolved price band and HH-7.3's parity report before the sales switch.
- HH-10: the contractor shell's cached band, labelled an estimate until the core confirms on sync.
