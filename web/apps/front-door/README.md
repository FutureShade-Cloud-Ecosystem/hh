# @hardscapehouse/front-door

The demo box's front door: one Vite entry, one bundle (web/apps/README.md).
It replaces the Counter Till's entry view as the box's first screen
(HH-DEMO-1 D2): the phrase gate, then this page. Pick a role, land on that
role's home with its modules as tiles, one header across the states with the
demo badge, the role's name, Switch role and Sign out.

- The thirteen live roles are the demo mint's own with their screens on the box
  (`inside_sales` to flow `sales`, `sales_manager` to flow `sales-manager`,
  `yard_manager` to flow `yard-manager`, `yard_team` to flow `yard-team`,
  `payables_receivables` to flow `payables-receivables`,
  `procurement_manager` to flow `procurement-manager`, `driver` to flow
  `driver`, `financial_controller` to flow `financial-controller`, `general_manager` to
  flow `general-manager`, `hr` to flow `hr`, `branch_manager` to flow `branch-manager`, `logistics_manager` to flow
  `logistics-manager`, `outside_sales` to flow `outside-sales`), labelled
  on the cards the way the demo adapter's contract names them ("Sales",
  "Sales manager", "Yard manager", "Yard Team", "Payables and Receivables",
  "Procurement manager", "Driver", "Financial Controller", "Logistics Manager", "General Manager", "HR", "Branch Manager", "Outside Sales"), the same rule the
  till's entry view follows. The yard manager's live module is Manager Home
  at `/home/`; the Yard Team's live module is the Yard Pick app at `/yard/`;
  the Money Desk at `/money-desk/` is the live module of Payables and
  Receivables and of the Financial Controller; the dispatch board at
  `/dispatch/` is the live module of the Logistics Manager and the yard manager's
  Dispatch tile.
- Every role of ROLE-ACCESS-r3 is live, so no coming row is drawn and the
  coming disclosure does not render; a role the matrix adds later would sit
  in `COMING_ROLES` first, shown with the modules the matrix gives it at
  work, manage or admin level and never tappable. The catalog lives in `src/roles.ts`, distilled once from the
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
  Buying and Stock app off `/buying-and-stock/`, Manager Home off `/home/`
  the Yard Pick app off `/yard/` and the Money Desk off `/money-desk/`; the
  demo image serves those six. The manager's two tiles whose apps are not on
  the box yet open the polite coming-soon page.

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
