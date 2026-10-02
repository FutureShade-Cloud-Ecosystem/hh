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
| pricing-words-logic.ts | 176 | the price source words the breakdown chip renders; self-contained |

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
| _pos-panels.ts | 272 | detached layout persistence behind a port seam |
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
- The browser's local store: the reference's detached panel layout persisted
  to it directly. Seams: `readPanelLayoutRaw`, `writePanelLayoutRaw`,
  `clearPanelLayoutRaw`, called with the reference's versioned key.
- Three calls the reference made around its own boundary, now seams: the
  catalogue list (`listProducts`), which the reference fetched with a bare
  browser call carrying no bearer; the product photo upload
  (`putProductImage`), which attached the raw session token by hand; and the
  receipt window opener (`openBlankWindow`), which opened a blank window by
  hand. Each refuses loudly until the wiring item implements it.
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
- `_pos-split.ts` `evaluateSplit` and `autoFillRemainder`: summed and
  subtracted the typed legs against the total, deciding readiness and the
  remainder live. Seams: `evaluateSplit` (with the `SplitReadiness` shape) and
  `autoFillRemainder`.
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
- `pos-terminal.ts` the readback's per-line saving (the sold ticket's line
  rows): multiplied the same per-line figure by quantity for the `Save`
  chip. Seam: `perLineSavingCents`.
- `pos-return.ts` `estimate`: prorated the refund's subtotal and tax per
  picked quantity. Seam: `refundEstimateCents`.
- `pos-customer-strip.ts`, `pos-customer-picker.ts` and `pos-terminal.ts`:
  credit headroom as `credit_limit_cents - open_balance_cents`. Seam:
  `availableCreditCents`.

Kept, reviewed as display formatting of figures the core already computed
(integer cents in, string out, no money decided). The named display helpers
are `_money-format.ts` (`centsAmount`, and `plainDollars`, the ungrouped
cents-to-input-string shape that the relief dialog's outcome line and the
split panel's fill render through, consolidated here so the division spelling
lives in one reviewed file) and the app's own `src/money.ts`; the app's money
scan names both and walks everything else. The remaining thin wrappers and
word formatters, none of which divide anything: `pos-api.ts` `money`,
`_pos-split.ts` `fmtCents`, `price-breakdown.ts` `priceMoney`,
`pos-terminal.ts` `taxLabel`'s basis points to percent, `tax-api.ts`'s word
formatters, `_pos-rev8.ts` `marginPctLabel`.

## Sweeps carried through the port

- No em or en dash survives anywhere in the ported files, comments or strings.
  The reference rendered an em dash as its blank mark, which the house
  forbids, so every blank marker now carries a real word: the ticket footer's
  unknown weight reads `(not weighed)` (pinned by the ported test), a pending
  line's total and a line's absent weight and margin read `(none)`, a branch
  stock cell with no read reads `(no read)`, the returns reason's blank option
  and the tile picker's blank option read `(not set)`, and an unbound keycap
  reads `(not set)`. An earlier pass of this port had collapsed those marks to
  a bare colon; the review caught it and the words above are the repair.
- No calendar reference survives in committed prose; the reference dated its
  rulings in comments and those dates are gone. The one calendar literal left
  in the tree is a date parser's own test fixtures, shifted to a neutral year:
  a parser test has to feed the parser dates.
- The reference's tracker references (`GABLE:slug`) are gone, reworded to name
  the ticket by its slug; the product prefix `hos-` on custom elements and
  tokens is the reference surface's own vocabulary and stays, flagged for the
  polish item.
- No Gable name, logo or colour was found in the ported files: the reference's
  token sheet is already the company's gold-on-navy rebrand.
- This app's credential sink rule (no browser storage, console or channel in
  any source) holds over the port: the reference read and wrote the browser's
  local store for the detached panel layout, and that persistence is now the
  `readPanelLayoutRaw` / `writePanelLayoutRaw` / `clearPanelLayoutRaw` seams
  in ports.ts with the same versioned key, so the wiring item picks the
  backing store.
- The design system's template fence, under the parity ruling (docs/BLOCKERS.md
  holds the ruling and its ground): what a port could satisfy at source it did,
  and the rest is a deliberate, enumerated allowance in the gate for this
  directory alone. Satisfied at source: the receipt window write is the
  `printReceiptInto` seam, the panel layout persistence is the store seams, the
  icon builder opens from static per-size branches, `d`, `class` and `role`
  bindings are quoted, the `html` name keeps to its two places, the
  reference's `title` tooltips stand as they were (the allowance names
  `title`, so the reference's accessible names and their hover both survive;
  the polish item may still move the surface onto a tooltip primitive when it
  adopts the kit discipline), and the panels' style geometry is spelled as a
  flat concatenation so the gate's parser can judge it. Allowed by
  the ruling, inside the ported prefix only and enumerated by name: the
  surface's `@event` and `.property` bindings, its boolean bindings besides
  `?disabled`, bound values on `title`, `tabindex`, `value`, `id`, `label`,
  `alt`, `placeholder`, `name`, `max`, `slot` and the boundary screens'
  `branch-id`, `order-id` and `doc-id`, and a whole-value `src` on the
  thumbnail route, with `style` the one deliberate admission past the gate's
  own security refusal. The gate's narrowness guards hold every edge of that
  shape, and the polish item still converges the surface onto the kit
  discipline.

## Debts recorded with the review

- The app's `tsconfig.json` carries `experimentalDecorators` and
  `useDefineForClassFields: false` for the ported elements' legacy Lit
  decorators. Both are whole-app compiler settings and Vite reads them, so
  the app's own views compile and run under them too; the app's suites stay
  green under the flags, and the wiring item should keep the difference in
  mind if a view ever depends on class-field define semantics. (P3-1)
- `product-thumb.ts` renders `<img src>` straight at the thumbnail route,
  and no ported seam carries that request: the wiring item decides how the
  thumbnail fetch is authorised (bearer through the api helper, a signed
  route, or a seam of its own). (P3-3)
- `_money-format.ts` `centsAmount` formats a non-finite input as `0.00`, a
  confident zero, carried unchanged from the reference; the polish item gives
  it the blank marker the house prefers. (P3-6)
- The split panel's problem sentences travelled with the split arithmetic
  into `evaluateSplit`, so the host seam owns the cashier wording as well as
  the money; the wiring item's implementation carries those words back.
  (P3-7)

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
  `receipt-print.test.mjs`, `field-refusal.test.mjs`,
  `pricing-words-logic.test.mjs`.

Five brought files carried halves that asserted invariants over the reference
repository's own tree rather than over the module they test; those halves
stayed with the reference, each marked where it was cut:

- `_pos-add-dedupe.test.mjs`: the whole-tree add-path census walked the
  reference src and named its quote-line writers; the wiring work item owes
  this app its own census when the surface mounts.
- `_pos-hotkeys.test.mjs`: the default-roster cross-check read the reference
  Go core's hotkey file; the wiring item re-pins the two rosters against each
  other when the seam is live.
- `_pos-rev8.test.mjs`: the pane-head half read the reference app's shell
  element, which HH replaces with its own.
- `_pos-workspace.test.mjs`: the divider's accessibility floor read the
  reference's quote work area, the one screen there that still shipped a
  splitter.
- `modal-focus.test.mjs`: the modal census is rebased onto the six ported
  modal-class components; the non-dismissable-modals gate read
  `kiosk-takeover.ts` and `till-close.ts`, which are not ported, and stayed
  with the reference.

Left out, with reasons:

- `_pos.test.mjs` and `_pos-split.test.mjs`: their cases drive the rerouted
  money seams (`cashTenderedPlan`, the deposit balance, `parseAmountCents`,
  the split verdict and remainder), which refuse until the host implements
  them; the wiring item ports them against its implementations.
- `hotkey-logic.test.mjs` and `_platform-hotkeys.test.mjs`: they read the nav
  gate table and registry, which are host seams now.
- `_money-format.test.mjs`: it is a meta test that walks the reference's own
  source tree on disk; it cannot run against this repository's layout.
- `_icon-consumer.test.mjs`: beyond the drawIcon cases (which would have
  pinned the per-size branches this tree now carries), it asserts the
  reference's whole-tree icon weight gates over files that are not ported;
  the wiring item re-pins the weight gate over this tree when the surface
  mounts.
- `_icon-registry.test.mjs` and `quick-action-registry.test.mjs`: their cases
  enumerate the reference's whole module registry and its launcher surface,
  which are host seams here (the registries' own ported logic is pinned by
  the brought hotkey and quick-action tests).
- `receipt-print.test.mjs` beyond its logic half, `_pos-lookup-rows.test.mjs`,
  `_pos-thumbs.test.mjs`, `_pos-counter-admin.test.mjs`,
  `_hotkey-effective.test.mjs`, `hotkey-pay.test.mjs`, `hotkey-shell.test.mjs`,
  `_pos-fixwave.test.mjs`, `_pos-dupline.test.mjs`,
  `_pos-counterfeedback.test.mjs`, `_pos-reprice.test.mjs`,
  `_qb914-*.test.mjs`: their import closures reach components, boundary
  screens or the api client of the reference app.
