# Quote Desk search and add (decision log row 991)

Branch `quote/qd-search-add`, cut from `quote/qd-3b-app` at 79eb611a. Not pushed. The workarea swap is not done (left for the split list coder's push).

## What changed

- `web/apps/quote-desk/src/quote-search-add.ts` (new): the `quote-search-add` element (HHElement, `defineElement`).
- `web/apps/quote-desk/src/index.ts` (new): the registration export; `main.ts` imports it. `app-root.ts` untouched.
- `web/apps/quote-desk/test/search-add.pw.ts` (new): five legs, listed below.
- `web/apps/quote-desk/test/stub/qd-stub.mjs`: a `POST /api/v1/pos/counter-prices` answer (the account's rule as the customer price, retail beside it).
- `web/apps/quote-desk/AWAIT-LEDGER.md`: seven SAFE rows for the new element (census test required them).

## The component's API

- Attributes: `branch-id` (stock shown is this branch's), `customer-id` (price shown is this customer's; empty is walk-in), `quote-id` (a change clears the box). The workarea's wiring pass hands them over.
- Event: `quote-line-add`, bubbles and composed, detail `{ productId, code, unit, quantity: 1 }`; unit is the sell unit, else the base unit.
- Reads: pos-kit's `counterSearch` (GET counter-search) and `counterPrices` (POST counter-prices), the same two the till's `pos-search-drop.ts` makes. Nothing is priced in the browser. Row: name, code, unit, customer price per unit (or "Priced on add" when the server cannot price), stock "N on hand" at the branch.
- Behaviour: debounced (160 ms) from two characters; first row highlighted; ArrowUp/ArrowDown move, Enter or click adds; the box empties and keeps focus after each add, with an "Added <name>" status line; Escape clears; a code typed or pasted that equals exactly one product's code adds at once; Enter inside the debounce wait searches now and adds the top row; empty answer reads `No products match "<term>"` with a way forward; a failed read says the search is not answering.
- Microphone slot: an HTML comment in the template beside the input naming `<quote-search-add-mic data-testid="search-mic">` (HH-221, HH-222). No DOM.
- Binding discipline: no property or event bindings in the template (position gate); the host listens for input, keydown, mousedown, click.

## Deviations from the brief, stated

- The brief names `name`/`code`/`category` filters on the api-client; at source the till's reads are one `q` term on counter-search plus counter-prices, so those are mirrored. Category matching is whatever the server's `q` covers; the stub's search does not match category words.
- The brief names `index.ts` in the app; it did not exist, so it was created and imported by `main.ts`.
- `customerCode` from a context: the till's read takes a customer id, so the element takes `customer-id` as an attribute.
- A new design-system primitive was not needed (the existing base styles and tokens carry it), so the design-system package is unchanged.

## Red then green

Red (component file absent, commit d3259484): 5 failed, each waiting on the combobox named "Search products by name, code or category". Green after the component: 5 passed.

Legs (`test/search-add.pw.ts`): search by name shows products and adds the row on click; search by code adds at once; keyboard arrow + Enter adds the highlighted row; three lines in a row without leaving the box; empty results says so in words. Test ids: `search-add-host` (test's mount), `search-add-input`, `search-add-list`, `search-add-row`, `search-add-price`, `search-add-stock`, `search-add-empty`, `search-add-busy`, `search-add-status`; future `search-mic`.

The acceptance suite's line legs still run on the scan box because the workarea still mounts it; their update belongs to the swap.

## Gates (CI=true, Node 22)

- Typecheck: api-client, auth, design-system, pos-kit, counter-till, front-door, public-desk, quote-desk, all clean.
- quote-desk: 226 node tests and 367 Playwright (362 before plus the 5 new), all green (one run after the ledger fix; the first run failed only the await ledger census, fixed by the seven rows).
- design-system: 282/282; positions 97/97; no-raw-markup 8/8, dependents list unchanged (counter-till, front-door, public-desk, quote-desk).
- counter-till: 307 node and 101 Playwright. front-door: 7 node and 10 Playwright. public-desk: 24 node and 65 Playwright.
- Vite build of quote-desk: clean; dist removed.
- Hosted CI: not run (no push).

## Shots

`hh-rd2/shots/quote-search-add/`: `before-search-add-{light,dark}-desktop.png` (the scan first box mounted) and `after-search-add-{light,dark}-desktop.png` (the new box swapped in on the page for the picture only, one result row with the customer's price and branch stock). The microphone slot is a comment, so it is not visible.

## Not done

The workarea swap and the subscription to `quote-line-add` (one line, split list coder's push). No MiniMax review run by this coder.
