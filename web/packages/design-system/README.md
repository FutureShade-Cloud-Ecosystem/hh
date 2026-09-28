# @hardscapehouse/design-system

Tokens (base plus per-brand overrides), primitives, patterns, and app kits, per
context section 4.5. Tokens come from `tokens.css` exported out of Penpot;
Penpot components map one-to-one to the kit components here (each kit's doc
comment carries its Penpot mapping name; the element tag is the Penpot
component name). Framework-free domain rules in `AGENTS.md` apply to everything
under `web/packages/`.

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
- `src/render.ts`: the kits' rendering layer, written by this package. It is a
  small hand-written layer, not Lit and not vendored from anything; the draft
  decision record that lays out the substrate options for Colton is
  `docs/adr/0023` (status draft, choice open). Markup can only be produced by
  the package's own `html()` behind a module-private symbol, so a caller's
  object or string is always text and never markup, and icon geometry is bound
  as escaped attribute values. The scanner over each template fails closed: a
  binding is accepted only in a position it has positively classified as safe
  (text content; a quoted attribute value in an attribute on the allow list;
  a bare attribute name on the bound-name allow list, whose value is then
  checked against that name), and every other position is refused with a
  named `HHRenderError`, including any position the literal text leaves
  unclassifiable and everything after it. An unknown state throws the same
  named error at render, in Node and in the browser, and a failed browser
  render never leaves an empty element.
- `src/primitives.ts`: the Ionic seam: `button`, `chip`, `spinner` and `row`
  with the call sites the `ion-*` primitives will occupy. The disabled rule and
  the focus ring live here, once, so no kit can get them wrong.
- The gates, as package tests (`test/`): the measured-contrast gate over the
  base vocabulary, each brand override, and each kit's own state combinations
  (4.5:1 for text, 3.0:1 for non-text boundaries, failures naming pair and
  ratio; the gate holds the focus ring, the state colours as borders, dimmed
  text on the row stripe, the primary hover pair, and the secondary control's
  boundary token), the phantom-token scan over every shipped source, the two
  carried conduct rules (colour never encodes state alone, asserted on the
  shown geometry and every place state is shown; disabled changes colour, not
  opacity, asserted as a token swap with no opacity or filter declaration and
  no inline fade in any rendered control), per-kit per-state render coverage,
  and a headless-browser leg (see below) that registers the real custom
  elements and asserts each kit in each state in a live page.
- `src/icons/README.md`: the icon package seam. Documentation only; it records
  the registry format (DIB-ICONS-1) this package adopts when the ERP lane
  lands, and how the two icon inventories stay in step. No code imports the
  ERP's registry here.
- The harness: `harness/index.html` is the committed static page (built by
  `harness/build.mjs` from the same render functions the elements paint), every
  kit in every state at the two walkthrough widths, 1440 and 1920, with no
  build step at view time and no client scripts; the build commits all four
  theme and brand variants as files beside it (`index-light.html`,
  `index-hh-pro.html`, `index-light-hh-pro.html`), so a designer opens any of
  them by double click and no server is needed to see both themes and the
  hh-pro brand. Each cell paints inside its own declarative shadow root, so
  every kit is styled under its own sheet, and the build writes each kit's
  sheet once to `harness/sheets/<tag>.css`; that folder travels with the
  pages (a page copied without `harness/sheets/` renders unstyled).
  `harness/serve.mjs` serves the same variants from the same builder
  (`/?theme=light`, `/?brand=hh-pro`) plus the sheets, refuses to serve over an
  occupied port, and grades its own page before announcing. The suite holds
  every committed page and every sheet byte-equal to a fresh build.

## Running the suite and the harness

Node 22 only: the sources use type stripping, which is default-on from Node
22.18 (this machine's Node 22 is `/snap/bin/node`; the system `node` may be
older). Zero npm dependencies in the package itself; the dev-only typescript
and type packages come from the workspace install at `web/`.

```
node --import ./src/_ts-js-loader.mjs --test 'test/*.test.mjs'   # the suite
npm run typecheck                                                # strict tsc over the sources
node --import ./src/_ts-js-loader.mjs harness/serve.mjs          # the harness on 127.0.0.1:4173
npm run build:harness                                            # rebuild the committed harness/index.html
```

The headless-browser leg registers the real custom elements in headless
Chromium and asserts each kit in each state in the live DOM, asserts what a
person would see (each state's word and icon with a real box, visible, at
least 12 pixels, in an opaque colour; every disabled control at computed
opacity 1 with an opaque colour), and holds the harness layout at 1920, 1440,
1280, 768 and 400. What that person-view gate does not catch: a break written
through a PARENT selector, because the gate reads each element's own computed
style and box, so a rule on an ancestor can clip the word out (clip-path),
move it off screen, set its colour equal to the card it sits on, or collapse
its container to zero height and still pass. It drives the browser over the
DevTools protocol with
Node 22's own WebSocket (`test/_lib/cdp.mjs`): no Playwright, no new
dependency. It finds a browser at `$HH_DS_BROWSER`, then the usual PATH names,
then the Playwright cache. When none exists the leg fails loudly if `CI` is
set (a skipped gate is a failed gate; the workflow step names google-chrome
for exactly this reason) and skips with a loud, named reason otherwise; under
bun it never runs, and because bun's node:test layer ignores a `{ skip }`
option, the loud statement registers there as a plain passing test: the tally
counts it as a pass (the CI log shows it), with the named reason carried on
its console output, while under Node the same statement registers as a skip
carrying the reason, so no Node tally counts the skipped leg as a pass.

## Measured notes the sheet's readers asked for

- The oklch conversion in the contrast tooling clips per channel when it lands
  an out-of-sRGB colour on the sRGB cube. All four of HH Pro's brand values
  are outside sRGB, so what the shells render for the hh-pro override is the
  clipped neighbour of what HH Pro specifies, not the value itself. The lowest
  hh-pro pair on this package's canvases measures 4.94:1 and passes AA, but
  the rendered colour is not the one HH Pro's sheet names; the sheet carries
  the oklch values verbatim so the ruling about them stays possible.
- The oklch pairs in the gate are measured after the 8-bit clip, so two
  tools measuring the declared values may differ from the gate by up to about
  0.02; the hex pairs agree exactly.
- `--hh-line` is a hairline, not a control boundary: the secondary button's
  outline takes `--hh-control-line`, declared in both halves to clear 3:1
  against the page and the card (the line token clears neither). WCAG exempts
  disabled controls; this package holds its disabled pair to AA anyway.

## Recorded scope decisions (fix-or-record round)

- **Unused tokens are not gated.** The sheet carries the merged role
  vocabulary of the two source sheets, role for role, because the shells that
  adopt the kits will consume roles the five kits do not yet paint. Deleting
  every role a kit does not reference would shrink the vocabulary to this
  cycle's consumers and force the next lane to re-derive the rest. The gate
  holds every pair a kit paints; the sheet keeps the roles.
- **No type scale yet.** Font sizes today reference spacing tokens. A scale is
  a design decision (how many steps, which ratios) that belongs to the Penpot
  side of the seam, not to this package alone; recorded rather than invented
  here.
- **Pixel literals in component sources are recorded.** Layout spacing uses
  the spacing grid; the literals that remain are component-intrinsic optical
  sizes (control heights, icon boxes, stroke widths, the spinner ring) that
  want the same scale decision as the type, not a scatter of new one-off
  tokens.
- **The kit sheets avoid scanner-only declarations.** Headings and summaries
  do not re-declare the card they sit on just so a scanner finds a pair; the
  ink-on-surface matrix holds those pairs instead.

## Recorded deviations

The source brief names Lit and Ionic as the kit substrate. The web workspace
carries no run-time dependencies and this lane may add none (what it installs,
typescript and the Node type packages, is dev-only), so:

- `src/render.ts` is a small hand-written rendering layer, owned here, named
  for what it is (see the header in the file). The decision to own it instead
  of taking the real library, with the alternative and the reasons, is the
  draft `docs/adr/0023` awaiting Colton's ruling; if the real Lit lands, the
  file is deleted and the imports re-point at it, and the kits' render
  functions do not change.
- `src/primitives.ts` is the Ionic seam, as described above. When Ionic lands
  as a workspace dependency, the bodies of these helpers swap and the kits do
  not change.

Both are the locked spec's sanctioned vendor-minimal path, recorded rather than
silent. The kits' interaction wiring is intentionally absent: these are pattern
renders with real `<button>` elements carrying `data-action`. The way to wire
them is NOT to attach listeners to content inside a kit: the layer's measured
limit is that every update replaces the whole shadow root, so a listener
attached to kit content (and focus with it) is gone at the next render, even a
re-render to the same state. The supported pattern is delegation on the HOST
element: attach one listener to the `<hh-*>` element itself and read the
control off `event.composedPath()` inside it:

```js
host.addEventListener('click', (event) => {
  const control = event.composedPath().find((n) => n instanceof Element && n.hasAttribute('data-action'));
  if (control) handle(control.getAttribute('data-action'));
});
```

Not `event.target.closest('[data-action]')`: the shadow boundary retargets
`event.target` to the host itself, so at the host it IS the host and `closest`
finds nothing (measured in the browser leg, whose consumer-listener test
asserts both halves: the retargeting and the action arriving off
`composedPath()`). The host is never replaced by a render, so host listeners
survive. The same limit is stated in `docs/adr/0023`, and it stands whatever
Colton later rules on the substrate.
