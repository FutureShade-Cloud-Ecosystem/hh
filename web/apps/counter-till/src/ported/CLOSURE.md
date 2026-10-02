DOC-ID: COUNTER-TILL-PORTED-CLOSURE-r1

# The POS surface port: the closure map

WIRED (the second UI parity work item): the surface is mounted.

Two rulings close reference entries in the mount (lead rulings 15 and 16):

- Return (the More menu's entry into `hos-pos-return`) is closed: HH's ruled
  desk (src/views/returns.ts with W8's approval dialog) is the mounted till's
  ONE returns path. The ported dialog does not carry HH's ruled approval flow,
  the simulated word, or the clerk-and-approver receipt, so it stays ported
  (pos-return.ts) and unreachable: nothing in the mount sets showReturn.
- Past sales, Pricing doc and Assess tax relief (the More menu's other three)
  are closed: live call paths outside the counter-sale row, unruled and
  untested in HH; the polish lane may open them with tests if the parity set
  needs them. Nothing in the mount sets showLookup or showRelief, and no
  caller reaches savePricingDoc.
- The till has no margin floor (row 481), so the ring's refusal arms word only
  the credit gate, in HH's reviewed sentence; a price_blocked refusal has no
  arm and would surface as the server's own word, like every other refusal.

Open gap: End Session is closed in the mount. The core refuses a session
complete unless the session is COUNTED (409, core/internal/till/service.go),
answers CLOSED, and a drawer that does not balance needs a manager's sign-off
(row 500). The reference's End Session opened a till-close screen (blind count,
Z report, sign-off) that this release does not carry, so the button stays
visible but closed: `aria-disabled="true"` (so it takes focus), grey and
unfilled, named "End Session", with the reason on its title; the button
points with `aria-describedby` at its own always-present visually hidden
description (the reason), and a press or focus also shows the reason as a
notice beside the control (anchored under the button in the top bar, in the
same neutral notice style, not the success green) that is announced once
(on a press; focus leaves the speaking to the description) ("Ending the
session needs the blind count, the Z report and a manager's sign-off; the
drawer close is not on this surface yet."), and it makes no request. The
notice's place was moved for the ui3 visual round c review (its GAP 1): the
till's foot message line put the answer about 680px from the button, half
under the hotkey bar with the Items step open, so the reason now renders in
the bar's own popover band and every other message keeps the foot line.
Porting the close flow is the
work that opens it; the stub carries no complete route.

Open gap: the printed receipt does not carry the simulated word. The receipt
is the core's document (GET /api/v1/pos/sales/{id}/receipt, printing module)
and says nothing of simulation for a simulated card sale; the till's own
screens (the confirmation's notice, the rail word) carry it. A core change is
the principal's to sequence, and the suite no longer asserts the word on the
stub's receipt text. A second, related gap: the printed receipt window is
the core's own document and is unstyled (a plain default-font page, a table
for the lines, no currency marks, no layout); styling it is the core's
printing module, outside this lane, and the principal sequences it.

One deliberate deviation from the reference, on HH's rule (the visual
review's blocking zero): the reference's empty draft reads Subtotal CA$0.00,
HST CA$0.00, Total CA$0.00, and the ring button carries the zero. HH's rule
is explicit: an empty draft shows a blank marker, never a plausible zero.
The mounted surface shows the app's own `(not priced)` marker in the totals
card, the Complete sale button's amount, the Items
summary and the read-back whenever the cart is empty OR any line is price
pending (a just-added line before its price lookup answers, or a line whose
reprice failed), and the figures return when every line is priced (the old
reviewed till's own marker, views/till.ts's line-unpriced word). The Complete
sale amount carries (est.) once it states a figure.
The cash tile computes nothing from a typed amount (the change due is the
ring's own answer, stated on the confirmation), so the preview's figure, its
estimate mark and its "Less than the total" choice are gone with the total
they were derived from. The split panel states no remaining at all: the legs
settle the server's total at the ring (decision log row 661), so the panel
shows the typed sum, which is arithmetic over the operator's own entries, and
words the shape; Rest fills no leg until a server-stated total returns. The
parked list names the snapshot a SUBTOTAL of the priced lines, unmarked (the
snapshot keeps no figure while any line is price pending, and a parked ticket
with no price memory in this window lists the marker too). The search drop rows render
the contract's per-branch stock (`on_hand_branch`) beside the SKU, as the
reference's rows do; the contract carries the field, so no gap remains.

One ruled truth about money on the mounted surface (decision log row 661,
which unwinds the earlier estimate bend of LEAD RULINGS 14 and 17): HH's
contract gives the counter no rate (PricingBreakdown carries no tax, and the
rate table is money-desk), so the till computes NO tax and NO total before
the ring: no browser rate, no 1300 bps default, and no estimate mark anywhere
on the sale surface. Before the ring the till assembles the priced lines'
subtotal only (the pricing resolve's own line totals, summed over the host
seam), and the tax and the total are named as the ring's own answer, in
words, on the totals card and the read-back; an applied relief's
engine-assessed rate may be NAMED there (a server-stated fact) but is never
multiplied into a figure. The figures come back, exact, when the core serves
the counter its tax rate, and the ring's committed figures are the binding
ones the confirmation renders. `ports.ts` is
implemented over the host (the api seam carries the demo org and bearer and
the two contract shape adaptations, the session facts read the demo mint's
own IdentityUser block, the money seams answer over core-stated figures, the
layout store is tab memory and the print choreography the host's, each named
in the file); the terminal renders as the app's one surface after the entry
cards, in both its modes. The two test halves this file left with the
reference have come home: `_pos-add-census.test.mjs` (this tree's own
add-path census) and `_pos-hotkeys-roster.test.mjs` (the fallback roster
re-pinned against HH's core hotkeys file), beside `ports-money.test.mjs`
(the split and money seam cases against the implementations).

## The polish pass (UI-3)

The third UI parity work item, from the visual review's findings. Each is its
own commit on `till/ui-3-polish`.

- Returns desk placement (GAP 2): the plain "Returns" section under the till
  now wears the till's own card: the terminal's column and gutter, the
  accordion card's face, hairline, radius and shadow, a 15px semibold head,
  13px body copy, 11px caption labels and the till's input face, in both
  themes. One returns path stays (lead ruling 15): the desk's flow, its ruled
  approval dialog and its receipt are unchanged; the ported Return dialog
  stays unreachable.
- Theme (GAP 3): the Theme control sits in the account area beside Pop out and
  Sign out (and on the entry page's header). It cycles Auto, Light and Dark;
  Auto follows the system's light setting live. The reference persists its
  choice in local storage; this app's committed sink gate refuses every
  storage path, so the choice rests in tab memory only and a reload, a
  sign-out and the pop-out window start on Auto (`src/theme.ts`). A press is announced in a polite status (focus stays on the button), and a test
  holds that Auto follows an emulated system colour-scheme change.
- Header (GAP 6): the title, the stepper and the session bar share one row at
  the column cap while no session is open (the bar's gap and the session
  cluster's gap tightened). With a session open the bar gains the dot and End
  Session and wraps, as the reference's own bar does.
- Split banner (GAP 7): the split state's band is the reference's pale yellow
  with the theme-stable ink in both themes. It is a status element (`p`,
  `role="status"`), not a disabled button: a disabled button must look
  disabled. Its fill is the LOCAL role `--hos-band-fill` in `styles/tokens.css`
  (theme-invariant, flagged for promotion), not a colour keyword; the hex ratchet
  test also refuses `white` and `black` in a colour position.
- Margin eye (GAP 8): an outlined control (the utility buttons' hairline), at
  the end of the utility row, so it never shifts Park ticket when the first
  priced line brings margin into view. One constant accessible name ("Hide
  margin and est. profit") with `aria-pressed` (pressed means hidden); pressed
  wears a tint so a hovered eye does not look pressed. The whole margin surface
  (the column, the footer's margin and profit segments, and this eye) renders
  only while the policy word allows the caller: `/pos/policy` answers
  `margin_visibility` (`MANAGER_UP` admits the core resolver's tiers 1 and 2;
  `ALL_STAFF` everyone; `HIDDEN` nobody; absent or unknown fails closed), and
  the surface follows it even while the glance itself keeps answering (P3-10).
- Top bar (visual NOTE 3): the bar above the till is on the till's compact
  scale, about 48px, with the till's small-chrome button face and type size and
  a one-line demo banner. The design system's 44px control floor still governs
  the entry page.
- Disabled controls (visual NOTE 4): every disabled button the till mounts takes the one rule
  Complete sale uses: no fill, grey ink and line, opacity 1, `not-allowed`. That covers the Items Add
  button, the returns desk's buttons, the fulfilment tiles (COD off), the payment tiles and tenders
  buttons, the split panel's and the lookup sheet's buttons, the parked list, the return dialog and the
  customer picker's create form (a test reads the COD tile, Add, Find sale and End Session), and, edited
  to the same rule without a test because no path on this mount shows them: the quick-action start
  guard (`quick-action-start-guard.ts`), the assess-relief dialog (`pos-assess-relief.ts`), the
  signature pad (`pos-signature.ts`), the counter settings dialog (`pos-counter-settings.ts`) and the
  fault state's retry button (`empty-state.ts`). Not seen in a browser: those five.
- Control boundary: the returns desk's inputs and the till's own inputs and
  selects take `--hh-control-line` (3:1 or better in both themes), not the
  hairline, through the `--hos-control-line` alias; a test reads the border.
- Panels Fulfilment row (GAP 9): all four choices share one row at the default
  panel width (the tile floor is 112px). None are left out: Collect now,
  Will-call, Delivery and COD are all sale types of the contract's ring body.
  COD stays gated by the branch policy read (`cod_enabled`): the tile is
  disabled and reads "COD: off" where the policy refuses it, and the COD
  fulfilment select (Pickup, Will-call, Delivery) opens only once COD is
  chosen, as in the reference.
- Quick picks and category chips (NOTE 2): the demo stub curates a three-tile
  hot list (grouped by the catalogue's category word) and the catalogue rows
  carry their category, so the filled tiles, the drill, the add, the curated
  chip row and Favorites are exercised in `test/hotlist.pw.ts`. No contract
  change: the shapes are the contract's `PosQuickPicks` and the catalogue
  list's application word. The empty hot-list note and the COD tile's "off"
  gate are held by tests that answer the one read differently for a page (the
  stub's own defaults stay filled and allowed). The tiles wear the placeholder thumbnail because
  the stub carries no photo bytes (`has_image` false).
- Signature area (NOTE 7): decided, left closed. The signature capture
  (`pos-signature.ts`) rides the tax relief assessment ("Assess tax relief"
  under More), which lead ruling 16 closed in the mount as a live call path
  outside the counter-sale row, unruled and untested in HH. The parity set
  (the linear flow, the detached multi-card layout, the light theme, focus
  rings, the hotkeys legend, empty and loading states) does not name it, so
  opening it would add an unruled path for a surface the parity set does not
  ask for. The Print receipt button is present on both Paid cards.
- Checked and found at parity, no change: focus rings (the keyboard ring on
  the fulfilment tile, the scan box and the stepper match the reference's),
  the empty search state ("No match"), the empty hot-list note, the empty
  ticket's blank marker, the hotkey legend, the help popover and the Counter
  view menu. The Counter settings editor is administrator-gated (`isAdmin`),
  and neither demo role is one, so it is not reachable on this build and was
  not seen (recorded as a gap: neither demo role reaches the Counter settings
  dialog, so its restyle and the hot-list and key editing it carries are not
  checked here).

The reference is `hardscapeos_dibbits` `master` at 87d7941b, read only at
`counter-till/ref-master/`. This directory carries the ported surface code of
`app/src/pos-terminal.ts`'s full import closure, file names mirrored, each file
opening with its provenance line. The reference's components render
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
- `pos-terminal.ts` `tax` and `total`: computed
  `subtotal * rateBps / 10000` and added subtotal and tax in the
  browser, over a rate the contract never serves (the 1300 default, or the
  relief flow's assessed one). NOT rerouted: REMOVED (decision log row 661).
  The seams (`ticketTaxCents`, `ticketTotalCents`) are gone from `ports.ts`
  and the surface carries no browser-computed tax or total; the ring states
  both figures.
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
- `pos-split-tender.ts`, pre-ring (a new seam, not the reference's):
  `splitDraftRead` sums the typed legs, which are the operator's own entries,
  and words the shape refusals, comparing the legs against no total at all;
  the total they settle is the server's, stated at the ring (decision log row
  661). `evaluateSplit` and `autoFillRemainder` stay for a server-stated
  total only.

The scan's stated limits, recorded rather than hidden (the reference's own
scan missed these shapes too): compound assignment (`subtotalCents += x`)
is not read; an operand inside parentheses or split across lines escapes the
line-based read; and a money figure whose name carries no `cents` (a bare
`price` or `amount`) is invisible to any spelling scan. The invariant binds
on the reader, not the regex.

Kept, reviewed as display formatting of figures the core already computed
(integer cents in, string out, no money decided). The named display helpers
are `_money-format.ts` (`centsAmount`, and `plainDollars`, the ungrouped
cents-to-input-string shape that the relief dialog's outcome line and the
split panel's fill render through, consolidated here so the division spelling
lives in one reviewed file) and the app's own `src/money.ts`. The wiring
answers the rerouted seams in `ports.ts` itself (eleven of the reference's
computations, plus the panel's pre-ring `splitDraftRead`; of the reference's
thirteen assemblies the tax line and the total are the two that were removed
outright, row 661), display assembly
over figures the core stated, each pinned by `ports-money.test.mjs`; the
app's money scan names `_money-format.ts` and the seam file `ports.ts` and
walks everything else, so the ported closure, the app's own views and
`src/money.ts` all keep owing it zero hits. The remaining thin wrappers and
word formatters, none of which divide anything: `pos-api.ts` `money`,
`_pos-split.ts` `fmtCents`, `price-breakdown.ts` `priceMoney`,
`tax-api.ts`'s word formatters, `_pos-rev8.ts` `marginPctLabel`.

## Sweeps carried through the port

- No em or en dash survives anywhere in the ported files, comments or strings.
  The reference rendered an em dash as its blank mark, which the house
  forbids, so every blank marker now carries a real word: the ticket footer's
  unknown weight reads `(not weighed)` (pinned by the ported test), a pending
  line's total reads `(not priced)`, a line's absent weight and margin read `(none)`, a branch
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
  token sheet is already the company's gold-on-navy rebrand. That claim was
  narrower than the pass: a later sweep took out two real persons' full names
  and the first dealer's trading name, which had ridden across in comments, one
  shipped placeholder, the relief dialog's option labels and values and test
  fixtures. Role words or the demo stub's own invented names stand in their
  place, and the sweep's method is pinned: a case-insensitive grep for each
  string over this app's tree (src/ported/name-sweep.test.mjs) now hits only
  the provenance headers' source repository name, which the porting rule
  requires and which stays.
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
- The thumbnail route: SETTLED (was P3-3). The contract makes
  `GET /api/v1/products/{id}/image` public by design (`x-role: public`,
  `security: []`: an `<img src>` cannot carry an Authorization header and the
  catalogue is public data), the stub answers it ungated with a plain 404,
  `product-thumb.ts` latches to its placeholder on error, and the README says
  so. Nothing to decide.
- `_money-format.ts` `centsAmount` formats a non-finite input as `0.00`, a
  confident zero, carried unchanged from the reference; the polish item gives
  it the blank marker the house prefers. (P3-6)
- The split panel's problem sentences: CARRIED AND HELD (was P3-7).
  `evaluateSplit` in ports.ts carries the reference's eight sentences word
  for word, and the seam cases bite on them; only the amount formatter
  changed (it routes through `centsAmount`, grouping like every other money
  word).
- The promise shapes: SETTLED (was R1-5). `listProducts` and
  `putProductImage` are implemented as async functions over the host's
  helper, so an unwired or failed call is a rejected promise, the reference's
  shape.
- The quiet empties: SETTLED (was R1-2). `BRANCHES` is filled in place from
  the branches route before anything mounts, `SCREEN_GATE` is a complete
  table, and a branch that does not resolve keeps the terminal down with a
  refusal in words.

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
