# ADR-0023: The web kits render through a small hand-written layer, not the Lit library

Status: Draft, for Colton (written in the HH-30 review round; the design system
review returned the previous state of this decision with the instruction to
write it down rather than ship it silently)

## Context

The source brief for the web kits (context section 4.5) names Lit as the kit
substrate and Ionic as the primitive library. The web workspace carries no npm
dependencies today, and the HH-30 lane was given no grant to add one.

What the kit layer actually needs from its substrate is small and stable:

- a tagged `html` template whose interpolated values are escaped, that composes
  nested templates and arrays, and that never emits a value as markup unless
  the package itself built that value;
- a `css` string helper;
- reactive properties reflected from attributes, and a render into an open
  shadow root in the browser;
- a string render of the same template for Node, so the package tests and the
  static harness page can assert and ship what the browser would paint.

The first round of HH-30 shipped this as `src/lit.ts`, about 150 hand-written
lines whose header called the code "vendored Lit". That description was not
true and the review returned it: the file has no version, no licence and no
upstream; it contains no Lit code. Writing a rendering layer of our own is a
build decision, and the rules say a build decision needs a decision record,
not a header that borrows someone else's name.

## Decision

Own the layer and name it. The file is `src/render.ts`, its header says it is
a small hand-written rendering layer owned by this package, and the package
README says the same. Its binding properties, held by the package tests:

- markup can only be produced by `html()` inside the package; the trust brand
  is a module-private symbol, so a caller's object or a caller's string is
  always rendered as text, never as markup (a label that is really parsed JSON
  cannot become script in the page);
- there is no raw-markup escape hatch at all: icon geometry is bound through
  the same escaped attribute path as any other value;
- the surface (`html`, `css`, `renderTemplate`, `HHElement`, `defineElement`)
  is kept compatible with the brief's named substrate, so a later swap does
  not touch the kits' render functions.

The Ionic seam in `src/primitives.ts` is the same class of decision on the
primitives side; it is recorded in the package README and is not re-argued
here.

## Alternatives considered

**Take the real Lit as a workspace dependency** (the brief's named substrate).
This is the alternative the ruling is about, and it may well be where this
ends up. For it: a maintained upstream with a real version and licence; a
spec-compliant reactive-property implementation; directives and controllers if
the kits ever grow beyond pattern renders. Against it, today: the lane has no
dependency grant; the workspace posture is zero-dependency and the review
round did not move that; server-side rendering of the same templates (which
the Node tests and the static harness rely on) would add `@lit-labs/ssr`, a
second dependency and a second render path to keep in agreement with the
browser path; and the kits use a fraction of Lit's surface, so the first
increment of the dependency buys little this package does not already carry
in 150 reviewed lines.

**Keep the file and keep the Lit names** (the state the review returned).
Rejected: the header claimed a version, licence and upstream the file does not
have, and the claim is exactly what a decision record exists to prevent.

## Consequences

- If the ruling is to take the real Lit: `src/render.ts` is deleted, the
  imports re-point at the library (plus the chosen server-render path), and
  the kits' render functions do not change. The package tests assert rendered
  markup and rendered DOM, not this file, so the gates carry over.
- If the ruling is to keep the hand-written layer: this record moves from
  draft to accepted, and the file's header stays the contract for what the
  layer may grow into: escaping, attribute-bound values, no raw markup, and
  no event protocol (the kits are pattern renders; the shells that adopt them
  attach listeners the normal way).
- Either way, the rule the review round added stands regardless of substrate:
  no render path in this package may offer an unbranded raw-markup escape
  hatch, and the escape tests in test/kits.test.mjs keep it that way.
