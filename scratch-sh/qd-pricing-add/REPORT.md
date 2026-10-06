# QD-PRICING-ADD report (decision log row 988)

Branch quote/qd-pricing-add, cut from quote/qd-4 at 25c0d946. Not pushed.

## Precondition
`git diff 25c0d946..origin/quote/qd-4 -- web/apps/quote-desk/src/pricing-panel.ts` is empty (0 lines). The four tabs live in
their own files (pricing-margin.ts, pricing-rules.ts, pricing-customers.ts, pricing-breaks.ts); the diff of
`web/apps/quote-desk/src` between the two refs is also empty. Only pricing-margin.ts changed in this branch;
pricing-panel.ts and app-root.ts are untouched.

## Audit table (OpenAPI at core/api/openapi.yaml against the UI at 25c0d946)

| Tab | Action | API route | API has it | UI had it at 25c0d946 | UI affordance added |
|---|---|---|---|---|---|
| Margin policy | list | GET /pricing/category-policies | yes | yes | no (existed) |
| Margin policy | create (add category) | PUT /pricing/category-policies/{categoryId} (create or replace) | yes | NO | YES: Add category row |
| Margin policy | edit | same PUT | yes | yes (Save per row) | no (existed) |
| Margin policy | remove | none | NO | no | no (not invented) |
| Rules | list / read | GET /pricing/rules, GET /pricing/rules/{id} | yes | yes | no |
| Rules | create | POST /pricing/rules | yes | yes (+ New rule) | no (existed) |
| Rules | edit | PUT /pricing/rules/{id} (sparse edit) | yes | yes (Edit) | no (existed) |
| Rules | retire | DELETE /pricing/rules/{id} (soft retire) | yes | yes (Retire) | no (existed) |
| Customer prices | list | GET /pricing/customers/{customerId}/overrides | yes | yes | no |
| Customer prices | create | POST /pricing/customers/{customerId}/overrides | yes | yes (+ Add price) | no (existed) |
| Customer prices | edit | PUT /pricing/overrides/{id} | yes | yes (Edit) | no (existed) |
| Customer prices | remove | DELETE /pricing/overrides/{id} | yes | yes (Delete) | no (existed) |
| Quantity breaks | list / read | GET /pricing/quantity-breaks, GET /{id} | yes | yes | no |
| Quantity breaks | create | POST /pricing/quantity-breaks | yes | yes (+ New quantity break) | no (existed) |
| Quantity breaks | edit | PUT /pricing/quantity-breaks/{id} (replace) | yes | yes (Edit) | no (existed) |
| Quantity breaks | retire | DELETE /pricing/quantity-breaks/{id} (soft retire) | yes | yes (Retire) | no (existed) |
| Pricing log | read | GET /pricing/audit | yes | yes | read only, confirmed |

### Missing API actions for the principal
1. **Remove a margin policy**: the contract has no DELETE (or any remove) on category-policies. No Remove button was drawn. If
   removal is wanted, the core needs a route first.
2. **A categories read**: the contract has no category list (the existing note in `_pricing-shared.ts` says the same). The Add row's
   picker therefore offers the six seeded categories (CATEGORY_NAMES) that hold no policy. A real category outside those six
   cannot be picked until a categories read exists. This is the one limit on the Add row on the live demo.

Rules, Customer prices and Quantity breaks needed no new controls: every action the API offers already has one. The legs
for B, C, D and E pin that (they were green at the parent too, by design: they are audit pins, not regressions).

## What changed
- `web/apps/quote-desk/src/pricing-margin.ts`: the Add category row at the foot of the grid (picker of un-covered categories,
  target, floor, Enforce floor ticked by default, Save). Save validates (category picked, percent digits, floor at or below
  target), then goes through the same `saveWithFloor` path as a row save (the editors' floor warning first), then PUT. The
  saved category joins the grid as an in-place editable row and leaves the picker. A refusal shows in the Add row; a read-only
  refusal removes the row with every other control. When every category is covered the row is replaced by "Every category has a
  margin policy." The row is drawn only when `canEdit`, the same wall as every other control; Sales is shown the server's
  refusal and no tabs at all.
- `web/apps/quote-desk/AWAIT-LEDGER.md`: the new `saveAdd` await and the second void call on `onClickAction`.
- `web/apps/quote-desk/test/stub/pricing-stub.mjs`: the stub's PUT now creates (as the server does) instead of 404, logging a CREATE
  audit row; a `drop_policies` mode lets a leg start from the live demo's two policies.
- `web/apps/quote-desk/test/pricing-add.pw.ts`: 15 new legs. `pricing-helpers.ts`: the `drop_policies` field type.

## Red then green
Commit 1 (tests and stub only) at the old panel: 9 failed, 6 passed. The 9 reds are the add legs: row renders, picker open, save
and join, enforce unticked, validation, floor decline, refused add, all-covered note, read-only loses the row. The 6 greens are
the audit and role pins (Remove absent, B, C, D, E, Sales). After the implementation: 15 of 15 green.

## New test ids (test/pricing-add.pw.ts)
A: manager sees an Add category row under Pavers and Bulk; picker opened; Save sends the PUT and the row joins, editable in place;
Enforce unticked; validation sends nothing; floor warning decline adds nothing; refused add shown in the row; every category
covered; no Remove on any row. B: Rules New, Edit, Retire. C: Customer prices Add, Edit, Delete. D: Quantity breaks New, Edit,
Retire. E: pricing log read only. F: Sales sees none of the controls on any tab; read-only login loses the Add row.

## Gates (Node 22, CI=true, at the branch head)
- Typecheck clean: api-client, auth, design-system, pos-kit, counter-till, quote-desk. offline-store and shell-chrome have no
  tsconfig and no typecheck script (the call prints tsc help); this is unchanged from the parent and not a result of this branch.
  The brief's "front-door, public-desk" do not exist at this base.
- Quote-desk node tests 305/305 (the await ledger test failed once on the new await and was fixed in the ledger); Playwright 454
  passed (includes the 15 new).
- Design-system 266/266 at this base (the brief's 282 is the count at a later head; no primitive was added). Positions 105 and
  no-raw-markup tests pass in the same run (the brief's 97/97 and 8/8 are different counts at this base; nothing added or changed).
  No design-system file was touched, so the pin and the dependents list are unchanged.
- pos-kit 107/107. Counter-till node 296/296 and Playwright 99 passed (the brief's 307 and 101 are later counts).
- Vite build clean; dist removed.
- Hosted CI is the lane lead's to read; nothing pushed.

## Shots (hh-rd2/shots/qd-pricing-add/, light, 1440x900, manager)
before-margin-policy-add-manager-desktop.png, after-margin-policy-add-manager-desktop.png,
after-margin-policy-add-picker-open-manager-desktop.png, after-rules-tab-manager-desktop.png,
after-customer-prices-tab-manager-desktop.png, after-quantity-breaks-tab-manager-desktop.png.
The picker shot shows the focused select (a native dropdown list does not render in a headless screenshot).

## Not done
- No Remove for a margin policy (no API route). No categories beyond the six seeded names in the picker (no categories read).
