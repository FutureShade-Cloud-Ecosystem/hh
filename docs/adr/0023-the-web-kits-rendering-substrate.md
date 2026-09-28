# ADR-0023: the web kits' rendering substrate

Status: Draft, for Colton. Nothing here is decided: this record lays out the
three live options with their sizes and risks, states what the current
substrate can and cannot do (measured, not estimated), marks one recommendation
as a recommendation, and leaves the choice open.

## Context

The source brief for the web kits (context section 4.5) names Lit as the kit
substrate and Ionic as the primitive library; departing from that naming is the
owner's choice to make, not the lane's. The web workspace carries no run-time
npm dependencies today (what it installs, typescript and the Node type
packages, is dev-only), and the HH-30 lane was given no grant to add one.

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
then shipped the same need as `src/render.ts`, named for what it is (606
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
- Cost to the packages that will build on the kits (today none does: no
  package in the workspace imports design-system yet; auth has its own
  sources and imports nothing from here, offline-store and shell-chrome are a
  package manifest and a README each, and the app entries land later;
  api-client is generated contract code that builds on none of the kits, and
  design-system is the kit package itself): none to import and render. The listener limit
  below is the real cost: every integration must wire interactions by
  delegation on the host element, reading the action off
  `event.composedPath()` (not `event.target`, which arrives retargeted to the
  host), because content listeners die at the next render. That is a design
  habit, not work, but it must be learned once and never forgotten.

### Option 2: adopt the Lit library (the brief's named substrate)

`lit` becomes a workspace dependency (the registry's unpacked size for
`lit` 3.3.3 is 106 KB), `src/render.ts` is deleted, the imports re-point at
the library, and the Node-side render (which the tests and the static harness
hang on) moves to `@lit-labs/ssr`: a second dependency that brings ten direct
dependencies of its own (parse5, enhanced-resolve and node-fetch among them).

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
  releases for the life of the product, and the zero-run-time-dependency
  posture the workspace holds today ends here.
- Cost to the packages that build on the kits: the smallest going forward.
  Adoption per package is a re-import and a template check, well under a
  dev-hour each, and each package gains keyed lists, property binding and
  directives the moment it needs them, without anyone hand-building them.

### Option 3: plain custom elements, no layer

Delete `src/render.ts`. Each kit speaks the platform directly: `attachShadow`,
`attributeChangedCallback`, template strings, `innerHTML`.

- Size: about 1 to 2 dev-hours to delete and hand-wire the first kit, then
  about 1 to 2 dev-hours per kit to carry the scaffolding across the other
  four. Argued at its real strength: the escaping does NOT have to be re-made
  per kit. The honest build of this option keeps the escape and refusal rules
  in one small shared helper every kit imports (what this layer holds, minus
  the element class), so safety is written once; and the platform underneath
  is permanent, with no dependency grant, no upstream, no licence file and no
  version drift, ever.
- Risk: lower than a first reading suggests, and real. Custom elements are
  what Lit compiles down to, so nothing is lost in principle, and the shared
  helper can hold the exact line the reviewer's probes drew. What the option
  gives up is the rest of the layer: reactive properties reflected from
  attributes, the whole-shadow-root render loop, and above all the Node-side
  string render the tests, the drift lock and the static harness hang on.
  Rebuilding those is the bulk of the per-kit cost, and dropping the string
  render blinds the gates. The position refusals exist because a reviewer
  probed for them; a plain `innerHTML` path reopens those doors wherever the
  helper is not used, and nothing but discipline stops the next kit from
  reaching past it.
- Upkeep: the escape rules stay in one place with the helper, but the render
  loop, the property wiring and the string render are five copies that can
  drift five ways. A fix the layer lands once today lands five times there,
  and the gates would need per-kit copies to hold the same line.
- Cost to the packages that build on the kits: near zero to adopt (the
  surface each consumes, a tag, a state attribute, the host-delegation
  pattern, would not change); the cost is the lost guarantees, not the API.

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
  with it; the supported pattern is delegation on the host element, reading
  the control off `event.composedPath()`, because the shadow boundary
  retargets `event.target` to the host itself (measured in Chrome: at the
  host, `event.target` IS the host and `event.target.closest('[data-action]')`
  finds nothing, while `composedPath()` finds the control), which the package
  README states with the working pattern and the record above costs into
  Option 1;
- each instance re-parses its stylesheet on every update: the layer sets no
  `adoptedStyleSheets`, so a page with many kit instances re-parses the sheet
  per instance per update (measured sheet size: about 8 KB per kit);
- the layer attaches nothing, so there is nothing to clean up: no
  `disconnectedCallback` is needed;
- the rendering layer has taken three review rounds so far that probed it as a
  security boundary, and each round found script running through it.

These limits are exactly what Lit maintains for its users, which is why the
brief named it; they are survivable for pattern renders, and the gates hold
them in place until the owner rules.

## Recommendation (a recommendation, not the decision)

Keep the owned layer for now, and keep the swap open: the package surface was
deliberately kept Lit-shaped, the gates assert rendered markup and rendered DOM
rather than the file, and if Lit lands later the file is deleted and the
imports re-point without touching a kit. The layer is small, gated, and the
known limits are survivable for pattern renders; a dependency grant spent only
to delete 606 reviewed lines is not obviously well spent while the workspace
holds zero run-time dependencies. Revisit the ruling the first time a kit
genuinely needs keyed lists, property binding, or directives: that is the
point where Option 1's per-capability cost starts paying Option 2's rent.

The choice is Colton's. The record merges with this work as a draft, and the
branch ships the layer without prejudice to the ruling.

## Consequences, once ruled

- If the ruling is Option 1: this record moves from draft to accepted, and the
  file's header stays the contract for what the layer may grow into: escaping,
  attribute-bound values, the position refusals, no raw markup, no event
  protocol, and the host-delegation pattern for interactions.
- If the ruling is Option 2: `src/render.ts` is deleted, `lit` and
  `@lit-labs/ssr` land as workspace dependencies, the Node render path is
  reconciled, the gates re-run, and the kits' render functions do not change.
- If the ruling is Option 3: this record and the layer's gates are the
  documentation of what the kits must now carry themselves, the escape rules
  move into the shared helper this record's Option 3 describes, with the same
  tests re-pointed at it, and the render loop and the Node-side string render
  are rebuilt per kit.
- Either way, the rule the review rounds added stands regardless of substrate:
  no render path in this package may offer an unbranded raw-markup escape
  hatch, and the escape tests in test/kits.test.mjs keep it that way.
