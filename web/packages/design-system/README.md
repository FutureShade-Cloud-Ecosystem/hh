# @hh/design-system

Tokens (base plus per-brand overrides), primitives, patterns, and app kits, per
context section 4.5. Tokens come from `tokens.css` exported out of Penpot;
Penpot components map one-to-one to Lit components here. Framework-free domain
rules in `AGENTS.md` apply to everything under `web/packages/`.

## What is here (cycle HH-4a, Plane HH-30)

- `src/tokens.css`: the sheet. One base vocabulary for internal apps (dark
  default, light opt-in on `[data-theme="light"]`), the `hh-pro` brand override
  for the standalone shell (`[data-brand="hh-pro"]`, HH Pro's dealer layer in
  its own oklch values), and the contractor `--cbrand-*` layer for the public
  proposal surface only. The header inside records the merge decisions role by
  role, including the gold rule and the two per-theme fill splits this sheet
  carries from the ERP's rulings.
- The five kits: `scan-and-confirm`, `list-detail`, `guided-flow`, `dashboard`,
  `inbox` (`src/kits/`). Every kit renders its ready state and the five states
  designed once per pattern: empty, loading, offline, conflict, error
  (`src/state.ts`). Element tags are the Penpot names, one to one.
- The gates, as package tests (`test/`): the measured-contrast gate over the
  base vocabulary, each brand override, and each kit's state combinations
  (4.5:1 for text, 3.0:1 for non-text boundaries, failures naming pair and
  ratio), the phantom-token scan over every shipped source, the two carried
  conduct rules (colour never encodes state alone, asserted structurally;
  disabled changes colour, not opacity, asserted as a token swap with no
  opacity or filter declaration anywhere), and per-kit per-state render
  coverage.
- `src/icons/README.md`: the icon package seam. Documentation only; it records
  the registry format (DIB-ICONS-1) this package adopts when the ERP lane
  lands. No code imports the ERP's registry here.
- The harness: `harness/serve.mjs` serves a static page with every kit in every
  state at the two walkthrough widths, 1440 and 1920, with no build step and no
  client scripts. Theme and brand are served variants (`/?theme=light`,
  `/?brand=hh-pro`).

## Running the suite and the harness

Node 22 only: the system Node is 20 and cannot type-strip the sources.

```
/snap/bin/node --import ./src/_ts-js-loader.mjs --test 'test/*.test.mjs'   # the suite
/snap/bin/node --import ./src/_ts-js-loader.mjs harness/serve.mjs          # the harness on 127.0.0.1:4173
```

Zero npm dependencies: the suite runs on the Node built-in test runner, and the
`src/_ts-js-loader.mjs` hook (the api-client lane's pattern) maps the sources'
`.js` specifiers onto their `.ts` files for Node's type stripping.

## Recorded deviations

The source brief names Lit and Ionic as the kit substrate. The web workspace
carries no dependencies and this lane may add none, so:

- `src/lit.ts` is the minimal Lit pattern vendored: the `html`/`css` tagged
  templates, reactive properties, a shadow-root render in the browser, and
  `renderToString` for Node, the tests and the harness. When the real Lit lands
  as a workspace dependency, this file is deleted and the imports re-point at
  it; the kits' render functions do not change.
- `src/primitives.ts` is the Ionic seam: `button`, `chip`, `spinner` and `row`
  with the call sites the `ion-*` primitives will occupy. The disabled rule and
  the focus ring live here, once, so no kit can get them wrong. When Ionic
  lands, the bodies of these helpers swap and the kits do not change.

Both are the locked spec's sanctioned vendor-minimal path, recorded rather than
silent. The kits' interaction wiring is intentionally absent: these are pattern
renders with real `<button>` elements carrying `data-action`, and the shells
that adopt them attach the listeners.
