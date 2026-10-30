# @hardscapehouse/design-system

Tokens (base plus per-brand overrides), primitives, patterns, and app kits, per
context section 4.5. Tokens come from `tokens.css` exported out of Penpot;
Penpot components map one-to-one to the kit components here (each kit's doc
comment carries its Penpot mapping name; the element tag is the Penpot
component name). Framework-free domain rules in `AGENTS.md` apply to everything
under `web/packages/`.

## The rendering substrate: Lit

The kits render through the Lit library (docs/adr/0023, option 2, accepted;
the ruling's words are "Adopt lit", relay message 046). The kits build markup
only with Lit's `html` and `css` tagged templates and the reactive base class
(`HHElement`, a thin `LitElement` subclass in `src/base.ts`); the browser
render path is LitElement's shadow-root render loop, and the Node-side render
the tests and the static harness hang on is Lit's server render
(`@lit-labs/ssr`).

The dependencies, read from `web/package-lock.json`:

| package | kind | version (exact) | licence | packages it adds to the install |
| --- | --- | --- | --- | --- |
| `lit` | run-time dependency | `3.3.3` | BSD-3-Clause | 6 (`lit`, `lit-html`, `lit-element`, `@lit/reactive-element`, `@lit-labs/ssr-dom-shim`, `@types/trusted-types`) |
| `@lit-labs/ssr` | development dependency | `4.1.0` | BSD-3-Clause | 14 (`@lit-labs/ssr`, `@lit-labs/ssr-client`, `@parse5/tools`, `parse5`, `enhanced-resolve`, `node-fetch`, `entities`, `tapable`, `graceful-fs`, `data-uri-to-buffer`, `fetch-blob`, `formdata-polyfill`, `node-domexception`, `web-streams-polyfill`) |

Both versions are pinned exactly (no caret, no tilde). The web workspace
therefore no longer holds zero run-time dependencies, and it tracks Lit's
releases for the life of the product; that trade is the ruling's, recorded in
docs/adr/0023.

### Writing a template as a consumer

The package's surface exports no template tag (`src/index.ts` records why: a
re-export hands the tag to a second file, which can alias it again where the
gate does not look). To write a template, import the tag from the library by
its own name and tag your template with it, exactly as the kits do:

```js
import { html } from 'lit';

const row = html`<li class="row">${label}</li>`;
```

No other spelling carries a tag the gate can see, because the gate now
names what is allowed and refuses the rest: in every walked file of this
package, every import, re-export, dynamic import and require is refused
unless its specifier and its imported names are on the list written in the
gate (`test/_lib/positions.mjs`, `IMPORT_ALLOW_LIST`), folder by folder, no
entry a prefix or a pattern. `html` and `svg` are allowed from `lit` only,
under their own names only, so an aliased import, a namespace or default
import, a dynamic import or require, a subpath nobody listed, the mathml
tag, another package of the lit family (lit-element re-exports the same
tags), and any local rebinding of the name are all refused, with the file,
the line and the specifier. The position gate (`test/positions.test.mjs`)
then holds an imported `html` or `svg` to exactly two places, its import
statement and the tag position of a tagged template. The tag under its own
name, used only as a template's tag, is the one spelling the gate holds for
you, in this package and in any package that adopts its rules.

## What is here (cycle HH-4a, Plane HH-30)

- `src/tokens.css`: the sheet. One base vocabulary for internal apps (brand mode
  default, light opt-in on `[data-theme="light"]`), the `hh-pro` brand override
  for the standalone shell (`[data-brand="hh-pro"]`, HH Pro's dealer layer in
  its own oklch values), and the contractor `--cbrand-*` layer for the public
  proposal surface only. The header inside records the merge decisions role by
  role, including the gold rule and the two per-theme fill splits this sheet
  carries from the ERP's rulings.
- The quote blue family (`--hh-quote*`, decision log rows 1047 item 7 and 1063):
  quotes and their documents read in an indigo, orders and the till keep gold.
  Five roles that mirror the gold family's, theme-measured by
  `test/quote-accent.test.mjs`. hh-pro keeps the same values.

  | Token | Light | Brand mode | Role |
  |---|---|---|---|
  | `--hh-quote` | #1C45B8 | #A3BCFF | the accent as ink: text, borders, the focus ring's quote twin |
  | `--hh-quote-fill` | #1D4ED8 | #1D4ED8 | the pressable control, one value in both modes |
  | `--hh-quote-fill-on` | #FFFFFF | #FFFFFF | the ink the fill carries (6.70:1) |
  | `--hh-quote-link` | #1C45B8 | #A3BCFF | links and text actions |
  | `--hh-quote-tint` | #E8EDFB | #242938 | the quote header band's ground |

  Measured: quote ink 8.10 on the light card and 6.81 on the page, 7.88 on the
  brand page and 6.74 on the brand card; ink on the tint 6.92 light and 7.72
  brand. On navy the fill is seen by its 1px fill-on border, the `.btn-primary`
  pattern. The gold family is unchanged and the till never uses these tokens.
- The five kits: `scan-and-confirm`, `list-detail`, `guided-flow`, `dashboard`,
  `inbox` (`src/kits/`). Every kit renders its ready state and the five states
  designed once per pattern: empty, loading, offline, conflict, error
  (`src/state.ts`). Element tags are the Penpot names, one to one. The kits'
  render functions are unchanged from the owned-layer drafts: Lit's template
  rules and the base class carry what the old hand-written layer carried.
- `src/base.ts`: the whole of what this package adds around Lit: `HHElement`
  (the `LitElement` subclass the kits extend, with the reactive `state`
  property declared once, its default, and the named `HHRenderError` wrapper
  around a failed render), and `defineElement`. There is no other rendering
  layer in the package; `src/render.ts` is deleted.
- The raw-markup rule: no render path in this package may offer raw markup.
  Lit ships exactly one class of API that turns a string into markup: the
  unsafe directives (unsafeHTML, unsafeSVG, unsafeMathML) and unsafeStatic,
  with the static-html module that fronts it. `test/no-raw-markup.test.mjs`
  refuses any import of those, and any mention of their names, anywhere in
  this package and in any package that depends on this one, and refuses the
  raw DOM paths (`innerHTML =`, `outerHTML =`, `insertAdjacentHTML`,
  `document.write`) beside them. The gate is proven able to fail: its scanners
  are pinned against planted offences in the suite, and removing a refusal
  turns a named test red. Lit's own rules carry the rest: an interpolated
  value lands as a text node or an attribute value, never as markup, and the
  kits bind only closed-union constants, flags, and text.
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
  the raw-markup gate above, and a headless-browser leg (see below) that
  registers the real custom elements and asserts each kit in each state in a
  live page.

## The disabled gate, and what it cannot see

`test/disabled-token.test.mjs` holds the house's disabled rule across the
whole web tree: a disabled control DROPS ITS FILL AND GOES GREY through the
`--hh-disabled-*` token swap, it never fades, and it never keeps a brand
colour. The gate reads every shipped style under `web/apps` and
`web/packages`, and every template those apps render.

The gate's legs, and the limits each leg carries, stated plainly so nobody
reads a green gate as more than it is:

- **The value leg.** A value is parsed into its terms, not searched. The fill,
  the ink and the edge must BE the grey token; no other colour may ride beside
  them, and no image layer rides beside the fill either, so the fill of a
  disabled control cannot be an image. A brand colour is refused in EVERY
  declaration of a disabled rule, whatever the property, so a brand edge in a
  shorthand, a brand glyph in an svg's `fill`, a brand accent and a brand
  shadow are all named. LIMITS: the brand refusal reads the accent family,
  the brand family, the contractor alias of the brand family (`--cbrand`,
  `--cbrand-on`) and the till's stone amber, and a brand token outside those
  families (the quote blue family, or a brand token written without its
  hyphen, such as `--brand2`) is not refused by that leg; the image refusal reads the fill, the background image, the border
  image and the list style image, and an image in another property (a
  `mask-image`, a `border-image-source` longhand) is not refused by that leg.
- **The template leg.** A class that a template proves is a disabled state is
  held to the swap whether or not it carries the `hh-disabled-state` marker.
  The scan reads a class set from a variable, a `classMap`, a `disabled=`
  property binding, a compound (`a || b`, `a && b`) and a negated (`!x`)
  condition, and an unquoted `aria-disabled` or `aria-busy`; it reads a
  condition as an implication, so a class that is on while the control is NOT
  disabled is not a disabled state. The relation is sound in both directions:
  `!x` implies `!y` exactly when `y` implies `x`, and a condition implies a
  conjunction exactly when it implies every conjunct of it, and a
  conjunction implies a condition when one of its conjuncts does, so the same
  conjunction ties to itself even when a conjunct is negated. LIMITS: the
  relation does not distribute (`(a || b) && c` is not tied to
  `a || (b && c)`); a class
  with no condition at all (a static class, or a variable that holds a plain
  string) is never tied; a `classMap` is read only when it is written as an
  object literal; a variable is resolved only inside its own file; an
  expression that runs past the end of its line is cut at the line unless the
  next line opens a continuation (`?`, `:`, `&&`, `||`, `+` or `=` at the start
  of it), so a ternary whose question mark closes the first line is not read;
  a condition the scan cannot compare textually (a call, a member expression)
  is not tied; the derived set is per app, not per file, so a class named in
  one file of an app is held in every file of that app; and the scan reads
  source, not the cascade, so a rule that paints a disabled control without
  naming a disabled state at all is out of its reach.
- **The marker and the pins.** A bare-word state class (`.result.off`,
  `.card.starting`, `.gl-choice-held`) is held by the `hh-disabled-state`
  marker inside the rule it marks, and the three known marked rules are pinned
  to their own file, so a deleted marker fails the gate.
- **The paint leg.** A real browser, the design system's own sheet: a
  disabled primary, a disabled secondary and a disabled field each compute
  background-color equal to `--hh-disabled-fill`, color equal to
  `--hh-disabled-ink` and opacity 1, in both halves. It cannot see a rule
  written through a PARENT selector, for the reason the person-view gate
  states below.

- `src/icons/README.md`: the icon package seam. Documentation only; it records
  the registry format (DIB-ICONS-1) this package adopts when the ERP lane
  lands, and how the two icon inventories stay in step. No code imports the
  ERP's registry here.
- The harness: `harness/index.html` is the committed static page (built by
  `harness/build.mjs` through Lit's server render, from the same render
  functions the elements paint), every kit in every state at the two
  walkthrough widths, 1440 and 1920, with no build step at view time and no
  client scripts; the build commits all four theme and brand variants as files
  beside it (`index-light.html`, `index-hh-pro.html`,
  `index-light-hh-pro.html`), so a designer opens any of them by double click
  and no server is needed to see both themes and the hh-pro brand. Each cell
  paints inside its own declarative shadow root (built by this package's
  builder, as before: Lit's server render is used for the kits' templates, and
  it adds only inert comment markers, never a shadow root of its own on this
  path), so every kit is styled under its own sheet, and the build writes each
  kit's sheet once to `harness/sheets/<tag>.css`; that folder travels with the
  pages (a page copied without `harness/sheets/` renders unstyled).
  `harness/serve.mjs` serves the same variants from the same builder
  (`/?theme=light`, `/?brand=hh-pro`) plus the sheets, refuses to serve over an
  occupied port, and grades its own page before announcing. The suite holds
  every committed page and every sheet byte-equal to a fresh build.

## Running the suite and the harness

Node 22 only: the sources use type stripping, which is default-on from Node
22.18 (this machine's Node 22 is `/snap/bin/node`; the system `node` may be
older). The package's dependencies come from the workspace install at `web/`:
`lit` at run time, and the dev-only `@lit-labs/ssr`, `typescript` and type
packages beside it.

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
dependency beyond the substrate itself. It finds a browser at
`$HH_DS_BROWSER`, then the usual PATH names, then the Playwright cache. When
none exists the leg fails loudly if `CI` is set (a skipped gate is a failed
gate; the workflow step names google-chrome for exactly this reason) and skips
with a loud, named reason otherwise; under bun it never runs, and because bun's
node:test layer ignores a `{ skip }` option, the loud statement registers there
as a plain passing test: the tally counts it as a pass (the CI log shows it),
with the named reason carried on its console output, while under Node the same
statement registers as a skip carrying the reason, so no Node tally counts the
skipped leg as a pass.

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

The source brief names Lit and Ionic as the kit substrate and primitive
library. Lit has landed as the substrate (docs/adr/0023, option 2, accepted).
Ionic has not:

- `src/primitives.ts` is the Ionic seam, as described above. When Ionic lands
  as a workspace dependency, the BODIES of these helpers swap to the ion-*
  primitives and the kits do not change.
- The labelled icon's text alternative moved from a `<title>` child to the
  `aria-label` attribute when Lit landed, because Lit allows no expression
  inside a raw-text element's body and a raw-markup path to put the label
  there is exactly what this package refuses. The trade, stated plainly: the
  accessible name is kept and the hover tooltip is lost.

## Wiring kit interactions: delegate on the host

The kits are pattern renders with real `<button>` elements carrying
`data-action`. Under Lit, the element's shadow DOM is not wiped on a render:
Lit diffs its parts, so DOM a template keeps producing survives from render to
render. A listener attached to CONTENT inside a kit therefore survives a
re-render to the same state (measured: element identity and focus survive an
explicit update). What it does not survive is a render that swaps the template:
the designed states replace a kit's body with a different template, the old
nodes go, and a content listener and its focus go with them.

The rule for consumers: listen on the host. A state change replaces a kit's
content nodes, so a listener that outside code attached to a node inside a kit
is lost across one, while a listener on the host element survives (the browser
leg holds both halves: the host listener still receives the action after a
state change and back, and a content listener does not fire again). So the
supported pattern is delegation on the HOST element: attach one
listener to the `<hh-*>` element itself and read the control off
`event.composedPath()` inside it:

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
`composedPath()`). This half of the advice is unchanged from the owned layer
and it is a shadow-DOM fact, not a renderer choice: it holds for every Lit
component and would hold for any open shadow root. The host is never replaced
by a render, so host listeners survive anything. If a consumer wires a
listener INSIDE a template (`@click=${...}` in its own Lit template), Lit
attaches and detaches it with the part, so it lives and dies with the node it
is on, which is the same fact stated from the template side.
