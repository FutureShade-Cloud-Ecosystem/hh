# ADR-0023: the web kits' rendering substrate

Status: Accepted. Option 2, the Lit library. Ruled by Colton, in chat, in the
words "Adopt lit", carried by relay message 046 (HH-4, to-supervisor), which is
the source of record for this decision and for the dependency grant it names.
The three options below are the argument the ruling was made against, kept as
written; the sections after them record what the ruling adopted, what was
measured under the adopted substrate, and what it cost.

## Context

The source brief for the web kits (context section 4.5) names Lit as the kit
substrate and Ionic as the primitive library; departing from that naming is the
owner's choice to make, not the lane's. The web workspace carried no run-time
npm dependencies when this record was drafted (what it installed, typescript
and the Node type packages, was dev-only), and the HH-30 lane was given no
grant to add one.

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

### Option 1: keep the owned layer (what the branch shipped)

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

### Option 2: adopt the Lit library (the brief's named substrate, ADOPTED)

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

## What the owned layer could and could not do (measured, in Chrome; the state option 1 was ruled out of)

These were measurements of `src/render.ts` as it shipped, not estimates. They
are kept because they are why the ruling went the way it did, and because they
are the baseline the re-measurement below is measured against:

- every update replaced the whole shadow root (`innerHTML = sheet + markup`):
  there was no diff and no keyed list, so a re-render was whole-sale and a long
  list re-created every row;
- properties were not reactive: only attributes were observed, so
  `el.state = 'error'` did nothing (the element stayed ready), and after an
  attribute was set the same-named property was ignored;
- a listener a shell attached to content inside a kit was lost at the next
  render, even a re-render to the same state, and focus inside the kit went
  with it; the supported pattern was delegation on the host element, reading
  the control off `event.composedPath()`, because the shadow boundary
  retargets `event.target` to the host itself (measured in Chrome: at the
  host, `event.target` IS the host and `event.target.closest('[data-action]')`
  finds nothing, while `composedPath()` finds the control);
- each instance re-parsed its stylesheet on every update: the layer set no
  `adoptedStyleSheets`, so a page with many kit instances re-parsed the sheet
  per instance per update (measured sheet size: about 8 KB per kit);
- the layer attached nothing, so there was nothing to clean up: no
  `disconnectedCallback` was needed;
- the rendering layer had taken three review rounds so far that probed it as a
  security boundary, and each round found script running through it.

## Measured again under Lit (the same five limits, in Chrome, after the ruling)

The same five limits, re-measured against the adopted substrate by the same
method (a live page over the DevTools protocol, real kit elements, DOM
identity and paint facts read back through Runtime.evaluate). The probe and
limit figures in this section stand as the supervisor's measurement:

- **Whole-root redraw: gone.** No render replaces the shadow root. Element
  identity survives an update (the same list row is the same node after an
  explicit re-render and after an offline round trip, because offline shows
  the cached body and Lit patches the parts in place); a state whose template
  replaces the body (error) rebuilds those nodes, and there is still no keyed
  list, so a long list re-renders its items unless a consumer reaches for
  Lit's own keyed `repeat()` directive.
- **Reactive properties: work.** `el.state = 'error'` re-renders the element
  to the error state, which the owned layer ignored. The property holds
  precedence over the attribute once set directly; a removed state attribute
  falls back to the default ('ready') through the base class's converter.
- **Listeners and focus across a render: partial.** A content listener and
  focus now survive a re-render that keeps the same template (measured: same
  button node, focus kept, listener still firing); a render that swaps the
  template (the designed states) still removes the old nodes, and a content
  listener and its focus go with them. Host delegation remains the supported
  pattern, stated in the package README with the same `composedPath()` note,
  which is a shadow-boundary fact and holds unchanged under Lit.
- **Stylesheet parsing per instance: gone.** Each element adopts three
  constructable stylesheets (the shared base sheet, the shared state sheet,
  the kit's own sheet); the same sheet OBJECTS are adopted by all instances
  measured (30 instances of one kit, across two kit classes), the kit's own
  sheet is distinct per class, and no instance carries a `<style>` tag. The
  sheets are parsed once per class and shared by reference (each kit's sheet
  is about 8 KB, unchanged in content).
- **Clean-up: unchanged.** The kits attach nothing outside their shadow
  roots; removing 20 rendered elements from the live DOM left zero mutations
  and zero errors over the observation window, and no kit needs a
  `disconnectedCallback`.
- Lit's own security posture, probed: the reviewer's 100 renderer probes (all
  97 of the last round plus the three cases that round's findings named) ran
  against the library in Chrome. Zero were refused by Lit; 73 rendered as
  inert text; 24 rendered into a sink that executed (srcdoc, onclick, the
  address attributes, the SVG animation values, a bound SVG script body,
  composed templates from a second module copy); 3 navigated. Lit escapes
  text and attribute-value boundaries; it does not classify sinks. The rule
  the review rounds added stands on top of it, and it binds dependents too:
  the kits bind only closed-union constants, flags and text, and the
  raw-markup gate (test/no-raw-markup.test.mjs) refuses the library's escape
  hatches, by import and by name, in this package and in any package that
  depends on it. The probe table is the reviewer's method and its verdicts
  are recorded in the lane's report for this work.

## The decision

Option 2. "Adopt lit" (Colton, relay message 046, which is the source of
record). The dependency grant is explicit: `lit` as the web workspace's first
run-time dependency, `@lit-labs/ssr` as a development dependency for the
Node-side render the tests and the static harness use. Adopted exactly:
`lit` 3.3.3 (BSD-3-Clause) and `@lit-labs/ssr` 4.1.0 (BSD-3-Clause), no
ranges.

What the adoption landed as:

- `src/render.ts` is deleted. The imports re-point at `lit` and, for the
  Node-side render, `@lit-labs/ssr`.
- The five kits' render functions do not change. The changes the land needed
  around them, each with its reason: the per-kit `static styles` composition
  moved from string concatenation to Lit's array form (Lit's `css` returns a
  `CSSResult`, not a string, and only a `CSSResult` may sit in `static
  styles`); the `state` reactive property and its default moved into the base
  class (a native class field initializer would shadow Lit's accessor and
  kill reactivity, because Lit's instance-property save runs in the
  constructor, before subclass fields initialize); `button()` and `row()` in
  the primitives moved their conditional attributes to Lit's `?attr` and
  `nothing` forms (Lit commits nothing for a plain string where an attribute
  name goes, so the old whole-attribute composition would have been silently
  dropped); and the labelled icon's text alternative moved from a `<title>`
  child to `aria-label` (Lit allows no expression inside a raw-text element's
  body, and a raw-markup path to put the label there is exactly what this
  package refuses).
- The harness's byte drift lock was re-derived: the per-kit sheets are
  byte-identical to the owned layer's (the `css` texts never changed), and the
  four pages change by Lit's server-render comment markers (`<!--lit-part-->`
  and `<!--lit-node N-->`, inert) and the boolean-attribute form. The pages
  still carry no script and still open from the file, measured in Chrome over
  `file://` for all four variants.

## Consequences

- The web workspace no longer has zero run-time dependencies: `lit` is the
  first, and the workspace tracks Lit's releases for the life of the product.
  Updates are a lockfile change plus this package's full gate re-run; the
  exact pins keep that decision deliberate each time.
- The owned layer's gates that asserted its named refusals are retired with
  the file they tested; the rule those reviews added stands regardless of
  substrate and is now held where it belongs: no render path in this package
  may offer raw markup, enforced by test/no-raw-markup.test.mjs against this
  package and any package that depends on it, with Lit's escaping model under
  it. A consumer that needs markup builds a Lit template of its own, in its
  own reviewed source, exactly as it would for any Lit component.
- Escaping and reactive-property correctness are maintained upstream. The
  package's own gates (contrast, phantom tokens, the conduct rules, coverage,
  the raw-markup gate, the browser leg) hold the parts that are this
  package's to hold.
- The Ionic seam (src/primitives.ts) is unaffected: when Ionic lands, the
  helpers' bodies swap and the kits do not change.
