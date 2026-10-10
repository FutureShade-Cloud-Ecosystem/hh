# @hardscapehouse/front-door

The demo box's front door: one Vite entry, one bundle (web/apps/README.md).
It replaces the Counter Till's entry view as the box's first screen
(HH-DEMO-1 D2): the phrase gate, then this page. Pick a role, land on that
role's home with its modules as tiles, one header across the states with the
demo badge, the role's name, Switch role and Sign out.

- The six live roles are the demo mint's own (`inside_sales` to flow
  `sales`, `sales_manager` to flow `sales-manager`, `payables_receivables`
  to flow `payables-receivables`, `procurement_manager` to flow
  `procurement-manager`, `driver` to flow `driver`, `financial_controller`
  to flow `financial-controller`), labelled on the cards the way the demo
  adapter's contract names them ("Sales", "Sales manager", "Payables and
  Receivables", "Procurement manager", "Driver", "Financial Controller"),
  the same rule the till's entry view follows.
- The seven other roles of ROLE-ACCESS-r3 stay visible as coming, each with
  the modules the matrix gives it at work, manage or admin level, never
  tappable. The catalog lives in `src/roles.ts`, distilled once from the
  decided record; the record itself is read only and is not carried here.
- The sign-in path is the original demo's pattern (the role picker of
  `futurebuildai/hardscapeos_dibbits`, branch `Demo`, read only): one mint at
  a time, a refusal verbatim inside the card that earned it while the other
  card keeps working, a polite region rendered unconditionally.
- The session crosses to the box in `src/session.ts`: the mint validated
  field by field (the till's demo-account.ts discipline), held under the
  front door's own sessionStorage key with the token the brief's handoff
  rule stores, beside the till custody's own mirror key written in the
  mirror's own payload shape. Sign out and Switch role clear both keys.
- The theme is the token sheet's own contract, the till's theme.ts mechanism:
  the `data-theme` attribute on the document root. Light is the default and
  is applied at boot; dark stays the second opt-in through the same
  attribute. No theme control in the header yet.
- The icons come from the ported registry (the one registry: the Counter
  Till's `src/ported/_icon-consumer.ts` and `_icon-registry.ts`, imported
  across the app boundary). No second registry lives here.
- The Counter Till hangs off `/till/`, the Quote Desk off `/quote/`, the
  Buying and Stock app off `/buying-and-stock/` and the Money Desk off
  `/money-desk/`; the demo image serves those four.
  The manager's three tiles whose apps are not on the box yet open the
  polite coming-soon page.

The app is demo-lane scaffolding, not a catalog frontend: it takes no entry
in `manifest.yaml` (that list is generated from the role catalog, and the
front door is not a role's surface; it is the door in front of them).

## Running the app against the local stub

Without a core, a local stub serves the mint in the contract's shape
(`DemoSessionResponse`, a JWT-shaped token whose exp claim the app reads):

    npm run dev

The dev server proxies `/api` to the stub (`FD_STUB_PORT`, default 4927) and
serves the app on `FD_APP_PORT` (default 4926).

## Tests

    npm run typecheck
    npm test

The node suite (`test/unit.test.mjs`) holds the mint validation, the session
custody's two keys and the catalog's shape. The Playwright suite
(`test/acceptance/front-door.pw.ts`, the app's acceptance spec) walks the
seven behaviours the D2 brief names against the stub harness. The exit test
(`test/acceptance/exit-test.sh`) builds the app, boots a throwaway Postgres
and the core's demo build behind a local stand-in for the image's phrase
gate, and walks the served picker with curl.
