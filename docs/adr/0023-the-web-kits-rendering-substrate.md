# ADR-0023: the web kits' rendering substrate

Status: Draft, for Colton. Nothing here is decided: this record lays out the
three live options with their sizes and risks, states what the current
substrate can and cannot do (measured, not estimated), marks one recommendation
as a recommendation, and leaves the choice open.

## Context

The source brief for the web kits (context section 4.5) names Lit as the kit
substrate and Ionic as the primitive library; departing from that naming is the
owner's choice to make, not the lane's. The web workspace carries no npm
dependencies today, and the HH-30 lane was given no grant to add one.

What the kit layer actually needs from its substrate is small and stable:

- a tagged `html` template whose interpolated values are escaped, that composes
  nested templates and arrays, and that never emits a value as markup unless
  the package itself built that value;
- a `css` string helper;
- reactive properties reflected from attributes, and a render into an open
  shadow root in the browser;
- a string render of the same template for Node, so the package tests and the
  static harness page assert and ship exactly what the browser would paint.

The first HH-30 round shipped this need as a file whose header claimed it was
vendored Lit; that description was false and the review returned it. The lane
then shipped the same need as `src/render.ts`, named for what it is (about 260
lines, hand written, owned by the package, held by gates including a
real-browser leg), with this record left open because the substrate choice
belongs to the owner.

## The options

Sizes are in dev-hour equivalents, engineering estimates by the lane, stated so
the options can be compared. They are estimates, not commitments.

### Option 1: keep the owned layer (what the branch ships)

`src/render.ts` stays as it is: one module carrying `html`, `css`, a base
element class and `defineElement`, with the trust brand, the escape rules and
the position refusals in one reviewed place.

- Size: zero dev-hours to keep what ships (it is done and gated). New
  capabilities are hand built when wanted; the two this round added (binding
  position refusals, the named render error) cost about 3 to 5 dev-hours each
  including their gates, and that is the honest unit for the next one. If
  keyed lists and property binding are ever both wanted, expect about 5 to 10
  dev-hours for the pair.
- Risk: the package owns render correctness and escaping forever, with no
  upstream to catch what it misses. The gates (escape and refusal tests, the
  browser leg that asserts the paint, the mutation-tested carried rules) are
  the compensating control, and they are stronger than what most consuming
  apps would write per kit.
- Upkeep: the file is small enough to hold in one head; every change runs the
  package's own gates. No version bumps, no upstream API drift, no licence
  file to carry.
- Cost to the five packages that build on the kits (today: api-client, auth,
  design-system, offline-store, shell-chrome; the app entries land later):
  none to import and render. The listener limit below is the real cost: every
  integration must wire interactions by delegation on the host element,
  because content listeners die at the next render. That is a design habit,
  not work, but it must be learned once and never forgotten.

### Option 2: adopt the Lit library (the brief's named substrate)

`lit` becomes a workspace dependency, `src/render.ts` is deleted, the imports
re-point at the library, and the Node-side render (which the tests and the
static harness hang on) moves to `@lit-labs/ssr`, a second dependency.

- Size: about 5 to 8 dev-hours to land: the dependency grant itself is outside
  the code (a ruling and a workspace change), then the import re-point, the
  Node render path reconciliation, and a full gate re-run. The kits' render
  functions were kept Lit-shaped on purpose, so they do not change.
- Risk: two render paths (browser and SSR) where one string function serves
  both today; Lit SSR emits a different shape (comment markers, style
  adoption) and the harness's byte-exact drift lock would need re-deriving.
  This is the main technical risk of the swap, and it is testable before any
  kit changes.
- Upkeep: escaping and reactive-property correctness are maintained upstream,
  which is the swap's real yield. In exchange the workspace tracks Lit's
  releases for the life of the product, and the zero-dependency posture the
  workspace holds today ends here.
- Cost to the five packages: the smallest going forward. Adoption per package
  is a re-import and a template check, well under a dev-hour each, and each
  package gains keyed lists, property binding and directives the moment it needs
  them, without anyone hand-building them.

### Option 3: plain custom elements, no layer

Delete `src/render.ts`. Each kit speaks the platform directly: `attachShadow`,
`attributeChangedCallback`, template strings, `innerHTML`.

- Size: about 1 to 2 dev-hours to delete and hand-wire the first kit, then
  about 1 to 2 dev-hours per kit to carry the same scaffolding across the
  other four, and the escaping decisions re-made per kit. The string render
  the tests and the static harness depend on gets rebuilt per kit or dropped,
  and dropping it would blind the gates.
- Risk: the highest of the three. The escape rules move into five kits and
  six states each, where a gate covers the layer once today; the markup attack
  surface multiplies by kit, and the reviewer's probes (unquoted attribute,
  event attribute, address scheme) are exactly the doors that reopen.
- Upkeep: every fix is made five times and can be forgotten five ways. The
  gates would need per-kit copies to hold the same line.
- Cost to the five packages: the highest. Every consumer inherits per-kit
  escaping choices, and the harness and the tests lose the one render path
  they trust today.

## What the owned layer can and cannot do (measured, in Chrome)

These are measurements of `src/render.ts` as it ships, not estimates, and they
stand whether or not the substrate changes:

- every update replaces the whole shadow root (`innerHTML = sheet + markup`):
  there is no diff and no keyed list, so a re-render is whole-sale and a long
  list re-creates every row;
- properties are not reactive: only attributes are observed, so
  `el.state = 'error'` does nothing (the element stays ready), and after an
  attribute is set the same-named property is ignored;
- a listener a shell attaches to content inside a kit is lost at the next
  render, even a re-render to the same state, and focus inside the kit goes
  with it; the supported pattern is delegation on the host element, which the
  package README now states and the record above costs into Option 1;
- each instance re-parses its stylesheet on every update: the layer sets no
  `adoptedStyleSheets`, so a page with many kit instances re-parses the sheet
  per instance per update (measured sheet size: about 8 KB per kit);
- the layer attaches nothing, so there is nothing to clean up: no
  `disconnectedCallback` is needed.

These limits are exactly what Lit maintains for its users, which is why the
brief named it; they are survivable for pattern renders, and the gates hold
them in place until the owner rules.

## Recommendation (a recommendation, not the decision)

Keep the owned layer for now, and keep the swap open: the package surface was
deliberately kept Lit-shaped, the gates assert rendered markup and rendered DOM
rather than the file, and if Lit lands later the file is deleted and the
imports re-point without touching a kit. The layer is small, gated, and the
known limits are survivable for pattern renders; a dependency grant spent only
to delete 260 reviewed lines is not obviously well spent while the workspace
holds zero dependencies. Revisit the ruling the first time a kit genuinely
needs keyed lists, property binding, or directives: that is the point where
Option 1's per-capability cost starts paying Option 2's rent.

The choice is Colton's. This record does not merge, and the branch ships the
layer without prejudice to the ruling.

## Consequences, once ruled

- If the ruling is Option 1: this record moves from draft to accepted, and the
  file's header stays the contract for what the layer may grow into: escaping,
  attribute-bound values, the position refusals, no raw markup, no event
  protocol, and the host-delegation pattern for interactions.
- If the ruling is Option 2: `src/render.ts` is deleted, `lit` and
  `@lit-labs/ssr` land as workspace dependencies, the Node render path is
  reconciled, the gates re-run, and the kits' render functions do not change.
- If the ruling is Option 3: this record and the layer's gates are the
  documentation of what each kit must now carry itself, and the escape rules
  move into the kits with per-kit tests.
- Either way, the rule the review rounds added stands regardless of substrate:
  no render path in this package may offer an unbranded raw-markup escape
  hatch, and the escape tests in test/kits.test.mjs keep it that way.
