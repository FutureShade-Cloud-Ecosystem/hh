DOC-ID: COUNTER-TILL-PORTED-CLOSURE-r1

# The POS surface port: the closure map

The reference is `hardscapeos_dibbits` `master` at 87d7941b, read only at
`counter-till/ref-master/`. This directory carries the ported surface code of
`app/src/pos-terminal.ts`'s full import closure, file names mirrored, each file
opening with its provenance line. Nothing here is mounted yet: the wiring work
item imports it and implements `ports.ts`. The reference's components render
through the `--hos-*` custom properties, so the port brings the reference's
token sheet rebased onto the HH design system (see Styles below).

## Ported: the surface components

| file | source lines | notes |
| --- | --- | --- |
| pos-terminal.ts | 6407 | the terminal; boundary screens dropped (below); money rerouted (below) |
| pos-flow-steps.ts | 184 | |
| pos-product-grid.ts | 1135 | |
| pos-lookup.ts | 425 | |
| pos-parked.ts | 280 | |
| pos-customer-picker.ts | 653 | credit headroom rerouted |
| pos-customer-strip.ts | 98 | credit headroom rerouted |
| pos-return.ts | 482 | refund estimate rerouted |
| pos-hot-list.ts | 405 | |
| pos-hotkey-legend.ts | 189 | |
| pos-counter-settings.ts | 1161 | |
| pos-panels.ts | 353 | |
| pos-scan-box.ts | 361 | |
| pos-search-drop.ts | 432 | |
| pos-step-card.ts | 219 | |
| pos-split-tender.ts | 324 | typed-amount parse rerouted |
| pos-signature.ts | 215 | |
| pos-assess-relief.ts | 311 | dollar display through a local formatter |
| price-breakdown.ts | 214 | the flagged local money formatter renamed |
| product-thumb.ts | 113 | |
| settings-history.ts | 215 | the change history panel inside counter settings |
| empty-state.ts | 208 | generic empty and fault states, settings history's dependency |
| quick-action-start-guard.ts | 188 | the new-ticket guard element |

## Ported: the surface's data and logic

| file | source lines | notes |
| --- | --- | --- |
| pos-api.ts | 936 | the POS module's endpoint calls and wire shapes; `api` and `getSessionToken` are ports |
| pos-counter-settings-logic.ts | 98 | |
| pos-signature-api.ts | 44 | `api` is a port |
| pos-signature-logic.ts | 40 | |
| receipt-print.ts | 52 | `api` is a port |
| receipt-print-logic.ts | 51 | |
| tax-api.ts | 224 | `api`, `BRANCHES`, `branchName` are ports |
| tender-logic.ts | 87 | |
| order-logic.ts | 153 | the status words the lookup renders; self-contained |

## Ported: the private helpers

| file | source lines | notes |
| --- | --- | --- |
| _pos.ts | 726 | cash-tendered plan and deposit balance rerouted |
| _pos-accordion.ts | 168 | |
| _pos-counter-view.ts | 112 | |
| _pos-flow.ts | 101 | |
| _pos-hotkeys.ts | 291 | |
| _pos-rev8.ts | 215 | |
| _pos-searchdrop.ts | 213 | |
| _pos-stock.ts | 81 | |
| _pos-workspace.ts | 393 | |
| _pos-panels.ts | 272 | |
| _pos-hotlist.ts | 198 | |
| _pos-split.ts | 119 | typed-amount parse rerouted |
| _pos-add-dedupe.ts | 217 | |
| _customer-picker.ts | 171 | |
| modal-focus.ts | 359 | the focus trap controller |
| quick-action-registry.ts | 94 | `hashTargetFor` is a port |
| quick-action-guard.ts | 91 | |
| quick-action-intent.ts | 74 | |
| hotkey-logic.ts | 265 | nav gates are ports |
| _platform-hotkeys.ts | 527 | |
| settings-history-logic.ts | 116 | |
| field-refusal.ts | 111 | |
| _icon-consumer.ts | 189 | nav key types are ports |
| _icon-registry.ts | 140 | |
| _glyph.ts | 19 | |
| _tile-icons.ts | 190 | |
| _date.ts | 42 | |
| _money-format.ts | 43 | display formatting of integer cents only |
| _response-shape.ts | 66 | |
| surface-config-url.ts | 29 | |

## Styles and tokens

| file | notes |
| --- | --- |
| styles/focus-ring.ts | the keyboard ring, unchanged |
| styles/row-stripe.ts | the register stripe, unchanged |
| styles/tokens.css | the reference's token sheet, rebased: every role the HH design system names (`web/packages/design-system/src/tokens.css`) is an alias to the `--hh-*` token; the roles the design system does not carry yet (the loud theme, the series and categorical data hues, the widget pop kit's wash and edge and raised shadow, the hero and stat type bands, the feedback pill's lane, the shell chrome widths, the legacy index spacing scale) keep the reference's own values and are flagged for the polish item to promote or retire |

## The boundary (not ported; the host owns it, declared in ports.ts)

- `api.ts`: the reference's api client and session. Seams: `api`, `BRANCHES`,
  `branchName`, `getCurrentUser`, `isAdmin`, `getSessionToken`, and the
  `AuthUser` and `Branch` shapes.
- `hash-route.ts`: the router. Seam: `hashTargetFor`.
- `dashboard-nav.ts` and `nav-access.ts`: the frozen deep-link registry and its
  RBAC gate table. Seams: the `NavUnitKey` and `NavModuleKey` unions,
  `isNavUnitKey`, `isNavModuleKey`, `Gate`, `AccessRole`, `SCREEN_GATE`,
  `canSeeGate`, `visibleModuleKeys`.
- `_related-docs.ts`: the related-documents drawer's logic belongs to the
  order screens. Only its `RelatedDoc` shape is declared here.
- The neighbouring screens `pos-terminal.ts` side-effect imports and embeds as
  custom elements: `till-close`, `pricing-doc`, `order-history`,
  `order-lifecycle`, `order-edit`, `order-edits`, `order-duplicate`,
  `order-change`, `order-shipments`, `related-docs`, `order-activity`. The
  ported terminal keeps the template tags and drops the registrations; until
  the wiring item decides (port them, or stub them), those tags render as
  unknown elements inside the sold and lookup panels only.
- The remaining closure files, reachable only through the screens above:
  `order-*` components and their `_order-*` helpers, `pricing-doc` and its
  `finstmt-*` chain with `_csv` and `_download`, `pricing-shared`,
  `pricing-doc-logic`, `_quote-edit`, `_status-reason`, `empty-state`'s
  siblings that only those screens use, `_platform-hotkeys`' shell consumers,
  and `_money-format`'s other callers.

## Money: the reference's client-side computations, left out

The reference computed money in the browser; HH's rule is that the core
computes money. Each computation below is gone from the ported code and
reappears as a `ports.ts` seam with the reference's signature:

- `_pos.ts` `cashTenderedPlan`: parsed typed dollars through
  `parseFloat` times 100 and subtracted the change due. Seam:
  `cashTenderedPlan` (with the `CashTenderedPlan` shape moved here).
- `_pos.ts` sale confirmation's balance-after-deposit:
  `total_cents - deposit` twice. Seam: `balanceAfterDepositCents`.
- `_pos-split.ts` `parseAmountCents`: `Number` times 100 on a cleaned string.
  Seam: `parseAmountCents`.
- `pos-terminal.ts` `subtotal`: summed the cart's line totals. Seam:
  `cartSubtotalCents`.
- `pos-terminal.ts` `tax`: computed `subtotal * rateBps / 10000` in the
  browser. Seam: `ticketTaxCents`.
- `pos-terminal.ts` `total`: added subtotal and tax. Seam:
  `ticketTotalCents`.
- `pos-terminal.ts` `depositCents`: parsed the typed deposit through
  `parseFloat` times 100. Seam: `depositCentsFromTyped`.
- `pos-terminal.ts` `ticketSavingCents`: multiplied per-line saving cents by
  quantity and summed. Seam: `sumLineSavingCents`.
- `pos-return.ts` `estimate`: prorated the refund's subtotal and tax per
  picked quantity. Seam: `refundEstimateCents`.
- `pos-customer-strip.ts`, `pos-customer-picker.ts` and `pos-terminal.ts`:
  credit headroom as `credit_limit_cents - open_balance_cents`. Seam:
  `availableCreditCents`.

Kept, reviewed as display formatting of figures the core already computed
(integer cents in, string out, no money decided): `_money-format.ts`
`centsAmount`, `pos-api.ts` `money`, `_pos-split.ts` `fmtCents`,
`price-breakdown.ts`'s local formatter, `pos-split-tender.ts` `fill`'s cents
to input string, `pos-terminal.ts` `taxLabel`'s basis points to percent,
`pos-assess-relief.ts`'s dollar display, `tax-api.ts`'s word formatters,
`_pos-rev8.ts` `marginPctLabel`.

## Sweeps carried through the port

- No em or en dash survives anywhere in the ported files, comments or strings.
- No calendar reference survives in committed prose; the reference dated its
  rulings in comments and those dates are gone.
- The reference's tracker references (`GABLE:slug`) are gone, reworded to name
  the ticket by its slug; the product prefix `hos-` on custom elements and
  tokens is the reference surface's own vocabulary and stays, flagged for the
  polish item.
- No Gable name, logo or colour was found in the ported files: the reference's
  token sheet is already the company's gold-on-navy rebrand.

## Reference tests brought along

Ported as `.test.mjs` siblings under `src/ported/`, wired into the app's test
script; they run on the plain modules only, because a test whose import
closure reaches a `ports.ts` seam would fail on the seam's honest refusal
rather than on a defect:

- `_date.test.mjs`, `_response-shape.test.mjs`, `_customer-picker.test.mjs`,
  `_pos-add-dedupe.test.mjs`, `_pos-hotkeys.test.mjs`, `_pos-flow.test.mjs`,
  `_pos-accordion.test.mjs`, `_pos-stock.test.mjs`, `_pos-counter-view.test.mjs`,
  `_pos-searchdrop.test.mjs`, `_pos-rev8.test.mjs`, `_pos-workspace.test.mjs`,
  `_pos-hotlist.test.mjs`, `_pos-panels.test.mjs`, `order-logic.test.mjs`,
  `tender-words.test.mjs`, `modal-focus.test.mjs`,
  `quick-action-guard.test.mjs`, `pos-signature-logic.test.mjs`,
  `receipt-print.test.mjs`.

Left out, with reasons:

- `_pos.test.mjs` and `_pos-split.test.mjs`: their cases drive the rerouted
  money seams (`cashTenderedPlan`, the deposit balance, `parseAmountCents`),
  which refuse until the host implements them; the wiring item ports them
  against its implementations.
- `hotkey-logic.test.mjs` and `_platform-hotkeys.test.mjs`: they read the nav
  gate table and registry, which are host seams now.
- `_money-format.test.mjs`: it is a meta test that walks the reference's own
  source tree on disk; it cannot run against this repository's layout.
- `receipt-print.test.mjs` beyond its logic half, `_pos-lookup-rows.test.mjs`,
  `_pos-thumbs.test.mjs`, `_pos-counter-admin.test.mjs`,
  `_hotkey-effective.test.mjs`, `hotkey-pay.test.mjs`, `hotkey-shell.test.mjs`,
  `_pos-fixwave.test.mjs`, `_pos-dupline.test.mjs`,
  `_pos-counterfeedback.test.mjs`, `_pos-reprice.test.mjs`,
  `_qb914-*.test.mjs`: their import closures reach components, boundary
  screens or the api client of the reference app.
