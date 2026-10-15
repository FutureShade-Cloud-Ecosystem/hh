# @hardscapehouse/pos-kit

The shared POS train pieces of the ported counter surface, moved once out of
the Counter Till's app (web/apps/counter-till/src/ported/) so the Quote Desk
work area imports the same pieces instead of copying them (QD-0 plan r1,
section 10.1, the row the principal granted). The move kept the till's suites
green and its screens unchanged: the files travel under git mv, keep their
provenance headers, and keep their ported file names under src/ported/, so a
reader comparing the two trees sees the same names in both.

## What is here

The pieces the plan grants, each with the ported name it carries:

- the scan box: pos-scan-box.ts
- the product grid: pos-product-grid.ts
- the product thumb: product-thumb.ts
- the flow steps: pos-flow-steps.ts
- the customer picker: pos-customer-picker.ts, with the customer strip
  (pos-customer-strip.ts) and the picker's own logic (_customer-picker.ts)
- the workspace geometry helpers: _pos-workspace.ts
- the icon registry: _icon-registry.ts, with its consumer and glyph tables
  (_icon-consumer.ts, _glyph.ts, _tile-icons.ts)
- the money format helpers: _money-format.ts
- the way in, the demo entry machinery: entry.ts (the role cards) and
  demo-account.ts (the demo adapter behind the auth package's AccountSurface
  seam)

- the apps' session custody: boot-custody.ts (`bootCustody`, item 7d). The served runtime config
  (`/config.json`, read by `loadAppRuntime` before the app's element exists) picks demo mode, which is
  the controller over the demo account exactly as before, or console mode, where the page recovers its
  own session on load (`resume()`, or a persona id the front door left in session storage), retries
  while the console or the core is unavailable, and goes back to the front door when the session ends.
  `entryFor` is the one place a role code becomes what an app needs (its label, its flow, its tier);
  `APP_ROLES` is the role matrix per app and a front door test holds it to the role catalog's tiles.
  An app's `main.ts` must load its root with a dynamic import after `loadAppRuntime()`, never a static
  one; each app's console boot test holds that line.

and the closed dependencies those pieces cannot run without:

- ports.ts, the ported surface's host seam file (CLOSURE.md in the till's
  tree documents it file by file). It moved whole rather than split: it is
  host wired, one wiring, one request door, and the request door carries the
  ring and resolve body adaptations the shared price breakdown needs. The
  till only seams it implements (the sale money seams, the split, the panel
  layout store, the receipt window) are consumed by the till alone; no
  till only surface moved. Its one import of the app's till-client.ts was
  re-homed onto src/ported/host-facts.ts (the house zone, the house day
  conversion, the demo card token), and the app's till-client.ts re-exports
  the same three names so its own callers are unchanged.
- pos-api.ts, the endpoint calls and wire shapes the moved components read
  (the scan resolve, the catalogue image url, the customer search, the money
  word), with the shapes they pass between them
  (_pos-searchdrop.ts, price-breakdown.ts, _price-breakdown-words.ts,
  pricing-words-logic.ts, _response-shape.ts)
- the shared guards and helpers: _pos-add-dedupe.ts (the double add guard),
  _pos-rev8.ts (the scanner versus typist miss outcome), modal-focus.ts (the
  focus trap), styles/focus-ring.ts (the keyboard ring), styles/tokens.css
  (the ported surface's token sheet, rebased onto the design system: the
  components render through its --hos-* roles, so the host app loads this
  sheet), and receipt-print-logic.ts (the receipt window shape ports.ts
  names)

The reference tests that sit beside their subjects moved with them, and the
kit carries its own copies of the tree gates whose coverage moved with the
files: the name sweep, the credential sink scan, the money and card scans,
the add path census and the modal focus census. The till's copies of those
gates keep their claims over the till's tree and name the kit's copies where
the claim crossed.

## Rules this package keeps

- No reverse dependency: nothing under src/ imports from any app under
  web/apps/. The app imports the kit; the kit never imports the app.
- Consumers import deep paths the way they already import the design system:
  @hardscapehouse/pos-kit/src/ported/<file>.js. The package.json exports map
  points ./src/*.js at the .ts source and passes every other ./src/ subpath
  through, so Vite, tsc (bundler resolution), the node --import loaders and
  Playwright's own loader all land on the same files (the kit's own tests use
  the loader in src/_ts-js-loader.mjs).
- The entry exports nothing of its own yet: the till and the quote work area
  both read the pieces by deep path. An index that re-exports a chosen
  surface is the quote lane's to add when its first consumer lands.
- The ported surface's house gates hold over this tree: integer cents only
  (the money scan names _money-format.ts and ports.ts alone), no credential
  sink, no real person's or dealer's name, no em or en dash, no calendar
  reference in committed prose.

## Development

```
npm --prefix web ci
npm --prefix web/packages/pos-kit run typecheck
npm --prefix web/packages/pos-kit test
```

The suite runs on Node 22 (the CI job pins it; the workstation's default
Node 20 cannot type-strip .ts).
