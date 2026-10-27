// The harness page builder: every kit in its six states (ready plus the five
// designed states), at the two walkthrough widths, 1440 and 1920. The page is a
// STRING, built by Node from the same render functions the custom elements
// paint, so what the design sync opens is what the kits render, with no build
// toolchain between them. The committed harness pages are four builds of this
// function (harness/build.mjs writes them: brand mode and light, base and hh-pro),
// so every variant opens from a file; serve.mjs serves the same variants from
// the same source.
//
// The Node-side render is Lit's server render (@lit-labs/ssr, the package's
// dev dependency): the kit instance is never connected, so Lit's own update
// loop never runs, and the page renders the template the element's render()
// returns, synchronously, the way the browser will commit it.
//
// Each cell renders into its own DECLARATIVE SHADOW ROOT
// (<template shadowrootmode="open">): the page still has no script, and each
// kit is painted under its own stylesheet, scoped by the shadow boundary, so
// one kit's rules can no longer be overridden or clobbered by another's. Each
// kit's sheet ships once, in harness/sheets/<tag>.css, and every cell of that
// kit links it; the browser fetches and parses the URL once and shares the
// sheet across the kit's shadow roots. The sheet files are written by the same
// build (harnessSheets() below) and are held byte-fresh by the drift test.
//
// The theme and brand are served variants, not client toggles: the page has no
// JavaScript at all. ?theme=light flips the html attribute; ?brand=hh-pro adds
// the standalone shell's brand override. The contractor layer is shown by a
// proposal section that sets --cbrand/--cbrand-on inline, the way the public
// proposal surface will, with values that clear the same AA bar the static
// palette is held to (the gate in test/harness.test.mjs measures them).

// The DOM shim must be installed before any LitElement subclass is defined
// (Node has no HTMLElement of its own); the side-effect import orderings here
// and in the test helper are the contract, and the class definitions below
// depend on it. The guard's import is part of that contract: it holds the
// process's fetch before the installer runs, and the restore below puts it
// back once the shim has installed.
import { restoreLitShimFetch } from './lit-dom-fetch-guard.js';
import '@lit-labs/ssr/lib/install-global-dom-shim.js';
import { render } from '@lit-labs/ssr';
import { collectResultSync } from '@lit-labs/ssr/lib/render-result.js';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { CSSResult, html, type CSSResultGroup, type TemplateResult } from 'lit';
import { KIT_STATES, type KitState } from './state.js';
import { ScanConfirmKit } from './kits/scan-and-confirm.js';
import { ListDetailKit } from './kits/list-detail.js';
import { GuidedFlowKit } from './kits/guided-flow.js';
import { DashboardKit } from './kits/dashboard.js';
import { InboxKit } from './kits/inbox.js';
import { HHSheet } from './sheet.js';

restoreLitShimFetch();

export const HARNESS_TITLE = '@hardscapehouse/design-system harness';
export const WIDTHS = [1440, 1920] as const;
export type HarnessTheme = 'dark' | 'light';
export type HarnessBrand = 'base' | 'hh-pro';

/**
 * The instance contract a kit element satisfies. Structural, so each kit class
 * keeps its private body and action specs private: the map never sees a
 * concrete kit type, and no kit names another.
 */
export interface KitElement {
  render(): TemplateResult;
}

/**
 * The constructor contract the kit map holds: the class object itself. The
 * harness touches one static member on it, the static styles the real element
 * paints, which the sheet files ship.
 */
export interface KitElementCtor {
  new (): KitElement;
  styles: CSSResultGroup;
}

export interface KitEntry {
  tag: string;
  title: string;
  element: KitElementCtor;
}

export const KITS: ReadonlyArray<KitEntry> = [
  { tag: 'hh-scan-and-confirm', title: 'scan-and-confirm', element: ScanConfirmKit },
  { tag: 'hh-list-detail', title: 'list-detail', element: ListDetailKit },
  { tag: 'hh-guided-flow', title: 'guided-flow', element: GuidedFlowKit },
  { tag: 'hh-dashboard', title: 'dashboard', element: DashboardKit },
  { tag: 'hh-inbox', title: 'inbox', element: InboxKit },
];

// The bottom sheet primitive (one entry, no states): it sits on the
// harness page so the design sync sees what the apps will mount, but the
// sheet is open/closed by an attribute, not by a state attribute, so it does
// not iterate like the kits. The static styles ship the same way the kit
// sheets do, so the harness page links an hh-sheet.css that mirrors the
// styles the element paints.
export const SHEET_TAG = 'hh-sheet';
export const SHEET_TITLE = 'hh-sheet';
export const SHEET_ELEMENT: KitElementCtor = HHSheet as unknown as KitElementCtor;

export const HARNESS_STATES: ReadonlyArray<KitState> = ['ready', ...KIT_STATES];

// The example contractor accent for the proposal section. Deliberately the
// package's own light-half success ink, whose white on-ink is measured above
// AA; the real surface takes its values from the quote at runtime.
const PROPOSAL_CBRAND = '#0F6C43';
const PROPOSAL_CBRAND_ON = '#ffffff';

const here = dirname(fileURLToPath(import.meta.url));

/** Flattens a static styles group (a css template, or arrays of them) to the
 * one stylesheet string a kit's shadow root paints. */
function cssTextOf(group: CSSResultGroup | undefined): string {
  if (Array.isArray(group)) return group.map((g) => cssTextOf(g as CSSResultGroup)).join('');
  if (group instanceof CSSResult) return group.cssText;
  return '';
}

/** The kit's markup for the given props, rendered by Lit's server render.
 * The instance is never connected, so the template is the only thing that
 * renders, synchronously, and a refused value throws to the caller. */
function renderKitMarkup(element: KitElementCtor, props: Record<string, unknown> = {}): string {
  const instance = new element();
  for (const [name, value] of Object.entries(props)) {
    (instance as unknown as Record<string, unknown>)[name] = value;
  }
  return collectResultSync(render(instance.render()));
}

/** The sheet's markup, rendered by Lit's server render. The sheet template
 * contains slot elements, and Lit's slot opcode throws when it is asked to
 * emit a slot outside a custom element context; kits never had this issue
 * because their templates do not slot. The fix is to wrap the sheet in a
 * custom element on the way through render: the SSR sees the outer
 * <hh-sheet>, walks its attributes, and emits the inner template with a
 * custom-element host on the stack. The outer element is registered with
 * the SSR's customElements registry once per process.
 *
 * The outer template uses STATIC attributes only: the binding-position gate
 * refuses dynamic attribute bindings the sheet's surface does not use (the
 * sheet's reactive `heading` and `open` are surfaced via attribute reflection
 * at parse time, not via a binding, so static `heading="Pick a reason"` and
 * `open` are the gate-allowed way to set them). */
function renderSheetMarkup(element: KitElementCtor, _props: Record<string, unknown> = {}, children: TemplateResult[] = []): string {
  const tag = SHEET_TAG;
  if (!customElements.get(tag)) {
    customElements.define(tag, element as unknown as CustomElementConstructor);
  }
  // The outer template wraps the element so the SSR has a custom element
  // host on the stack when it emits slots. The heading and open attributes
  // are static literals: the binding-position gate refuses dynamic bindings
  // on attribute names the allow list does not carry, and the harness page
  // wants these two values exactly.
  const tpl = html`
    <hh-sheet heading="Pick a reason" open>${children}</hh-sheet>
  `;
  return collectResultSync(render(tpl));
}

/** Each kit's full stylesheet, once per kit: the sheet the build writes to
 * harness/sheets/<tag>.css and every cell of that kit links. The styles come
 * from the same static member the real element paints, so the page and the
 * element cannot drift apart. The hh-sheet element ships the same way: one
 * stylesheet per tag, no duplication, and the build writes it next to the
 * kit sheets. */
export function harnessSheets(): ReadonlyArray<{ tag: string; css: string }> {
  return [
    ...KITS.map((kit) => ({ tag: kit.tag, css: cssTextOf(kit.element.styles) })),
    { tag: SHEET_TAG, css: cssTextOf(SHEET_ELEMENT.styles) },
  ];
}

function kitFigures(): string {
  const figures: string[] = [];
  for (const kit of KITS) {
    for (const state of HARNESS_STATES) {
      const markup = renderKitMarkup(kit.element, { state });
      figures.push(
        `<figure class="cell" data-cell="${kit.tag}/${state}">` +
          `<figcaption>${kit.title} / ${state}</figcaption>` +
          `<div class="cell-body">` +
          `<${kit.tag} state="${state}">` +
          `<template shadowrootmode="open">` +
          `<link rel="stylesheet" href="./sheets/${kit.tag}.css">` +
          `${markup}` +
          `</template>` +
          `</${kit.tag}>` +
          `</div>` +
          `</figure>`,
      );
    }
  }
  return figures.join('\n');
}

function proposalSection(): string {
  return (
    `<section class="proposal" style="--cbrand:${PROPOSAL_CBRAND}; --cbrand-on:${PROPOSAL_CBRAND_ON}" ` +
    `data-cbrand-example="true" aria-label="The public proposal surface, on the contractor layer">` +
    `<h2>The public proposal surface (the contractor layer)</h2>` +
    `<p>The proposal sets the contractor accent on its own root, from the quote; ` +
    `the dealer brand does not reach this surface.</p>` +
    `<div class="proposal-accent"><span class="proposal-name">Alder Court hardscape proposal</span>` +
    `<button type="button" class="proposal-btn">Open the proposal</button></div>` +
    `</section>`
  );
}

/** The bottom sheet entry: the sheet element rendered open with a heading,
 * description, default-slot rows, and a slotted actions button, all in the
 * sheet's open state. The harness page is script-free, so the open attribute
 * is set on the element directly; the consumer opens and closes the sheet
 * the same way in the apps (the consumer's trigger sets the property, the
 * element re-renders). The entry is NOT a kit cell: kits iterate per state,
 * the sheet does not, so the entry rides its own section above the kit grid,
 * and the harness drift test's cell counter does not see it.
 *
 * The sheet element renders its own shadow root at upgrade time; the static
 * styles ride in the element, so no declarative shadow root is needed in
 * the page chrome. The page links the sheet's CSS only as a fallback for
 * browsers that have not yet parsed the static styles at first paint, and
 * the link lives in the page chrome (not inside the sheet), so the kit grid
 * still owns all 60 cells and all 60 roots. */
function sheetFigure(): string {
  return (
    `<figure class="sheet-figure" aria-label="${SHEET_TITLE} open">` +
      `<figcaption>${SHEET_TITLE} / open</figcaption>` +
      `<div class="cell-body">` +
      // The sheet's inner markup is rendered from the element's own render()
      // via the sheet-specific SSR helper: kits don't slot, so the kit
      // renderer can call render() directly; the sheet template contains
      // slots, and Lit's SSR throws when a slot is emitted outside a custom
      // element context. The helper wraps the sheet in an outer custom
      // element so the SSR has a host on the stack.
      `${renderSheetMarkup(
        SHEET_ELEMENT,
        { open: true, heading: 'Pick a reason' },
        [
          // The slotted children the harness paints: the description, the
          // rows, and the actions button. They ride as the light DOM of
          // the host, where the sheet's slots pick them up at first paint.
          html`<span slot="description">One of these for the change.</span>`,
          html`<ul class="sheet-rows-demo">
            <li><span class="demo-row">Counted wrong</span></li>
            <li><span class="demo-row">Wrong item</span></li>
            <li><span class="demo-row">Damaged on arrival</span></li>
          </ul>`,
          html`<button type="button" class="sheet-action-demo" slot="actions" data-testid="sheet-action-demo">Save</button>`,
        ],
      )}` +
      `</div>` +
    `</figure>`
  );
}

export function buildHarnessPage(opts: { theme?: HarnessTheme; brand?: HarnessBrand } = {}): string {
  const theme: HarnessTheme = opts.theme === 'light' ? 'light' : 'dark';
  const brand: HarnessBrand = opts.brand === 'hh-pro' ? 'hh-pro' : 'base';
  const tokens = readFileSync(join(here, 'tokens.css'), 'utf8');
  const brandAttr = brand === 'hh-pro' ? ' data-brand="hh-pro"' : '';

  return `<!doctype html>
<html lang="en" data-theme="${theme}"${brandAttr}>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${HARNESS_TITLE}</title>
<style>
${tokens}
</style>
<style>
  /* The page's own chrome styles itself here, and ONLY here: the kits' sheets
     are scoped to their shadow roots, so nothing leaks onto the page any more
     (the kit sheets' box-sizing used to reach these elements by accident). */
  *, *::before, *::after { box-sizing: border-box; }
  html, body { margin: 0; background: var(--hh-page); color: var(--hh-text); font-family: var(--hh-font-ui); }
  body { padding: var(--hh-s-24); }
  h1 { font-size: var(--hh-s-24); }
  p.lede { color: var(--hh-text-dim); max-width: var(--hh-measure); }
  .width-frame { border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-16); margin-block-end: var(--hh-s-24); }
  .width-frame > h2 { margin: 0 0 var(--hh-s-12); font-size: var(--hh-s-16); color: var(--hh-text-dim); }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(420px, 100%), 1fr)); gap: var(--hh-s-16); align-items: start; }
  figure.cell { margin: 0; display: grid; gap: var(--hh-s-8); min-width: 0; align-content: start; }
  figure.cell figcaption { color: var(--hh-text-dim); font-size: var(--hh-s-12); overflow-wrap: anywhere; }
  .cell-body { min-width: 0; }
  section.proposal { border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-16); display: grid; gap: var(--hh-s-8); }
  section.proposal h2 { margin: 0; font-size: var(--hh-s-16); }
  section.proposal p { margin: 0; color: var(--hh-text-dim); max-width: var(--hh-measure); }
  .proposal-accent { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-12); padding: var(--hh-s-16); border-radius: var(--hh-radius-control); background: var(--cbrand); color: var(--cbrand-on); }
  .proposal-name { font-weight: 600; }
  .proposal-btn { border: 1px solid var(--cbrand-on); background: transparent; color: var(--cbrand-on); border-radius: var(--hh-radius-control); padding: var(--hh-s-8) var(--hh-s-16); min-height: 44px; }
  .proposal-btn:focus-visible { outline: 2px solid var(--cbrand-on); outline-offset: 2px; }
  /* The sheet's harness entry paints the element open inside a frame so the
     design sync can see the dialog and its scrim. The transform makes the frame
     the containing block of the sheet's position: fixed layers, so the scrim
     and the dialog cover the frame alone, never the rest of the page. */
  section.sheet-frame { position: relative; transform: translateZ(0); min-height: 540px; padding: var(--hh-s-24); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); margin-block-end: var(--hh-s-24); overflow: hidden; }
  section.sheet-frame > h2 { margin: 0 0 var(--hh-s-12); font-size: var(--hh-s-16); color: var(--hh-text-dim); }
  figure.sheet-cell { margin: 0; min-width: 0; }
  figure.sheet-cell figcaption { color: var(--hh-text-dim); font-size: var(--hh-s-12); margin-block-end: var(--hh-s-8); }
  /* The rows demo: the consumer's default-slot content. The styles live on
     the page (not in the sheet's stylesheet) so a consumer can dress their
     rows however they want; the harness shows three reasons a row might be
     picked. */
  .sheet-rows-demo { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--hh-s-4); }
  .demo-row { display: block; padding: var(--hh-s-12) var(--hh-s-16); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); color: var(--hh-text); }
  /* The slotted actions button: the consumer's primary action. The styles
     here are the consumer's, not the sheet's; the harness applies a visible
     fill so the designer can see what the slot is for. */
  .sheet-action-demo { background: var(--brand-fill); color: var(--brand-fill-on); border: 1px solid transparent; border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); font-weight: 600; cursor: pointer; }
</style>
</head>
<body>
<h1>${HARNESS_TITLE}</h1>
<p class="lede">Static page, no build and no scripts: every kit in its ready state and its five
designed states (empty, loading, offline, conflict, error), at the two walkthrough widths.
Rendered by Node from the kit render functions themselves, each cell into its own declarative
shadow root, so every kit is painted under its own stylesheet. The committed
pages are exactly this page in its four builds: <code>index.html</code> (brand mode,
base brand, the double-click default) beside <code>index-light.html</code>,
<code>index-hh-pro.html</code> and <code>index-light-hh-pro.html</code>, each
opening from the file itself, with the per-kit sheets the build writes under
<code>harness/sheets/</code> travelling with them (a page without that folder
renders unstyled);
<code>node --import ./src/_ts-js-loader.mjs harness/serve.mjs</code> serves the
same variants live:
add <code>?theme=light</code> for the light half and <code>?brand=hh-pro</code> for the standalone
shell's brand override. Each width frame is its walkthrough width capped to the window, so the
page never scrolls sideways; in a window narrower than a walkthrough width the capped frames
show at the window width and look the same, which is what their labels say.</p>
${proposalSection()}
<section class="sheet-frame" data-width="sheet-frame" data-section="hh-sheet">
  <h2>The bottom sheet primitive (hh-sheet), one entry per consumer app</h2>
  <link rel="stylesheet" href="./sheets/${SHEET_TAG}.css">
  <p style="margin: 0 0 var(--hh-s-12); color: var(--hh-text-dim); max-width: var(--hh-measure);">One sheet the phone first Field apps use: the Yard Pick skid reasons, the Drive could not deliver reasons, the House Messages New sheet. The page renders it open so the design sync sees the dialog, the scrim, the title, the description, the rows, and the full-width primary action; close paths (Escape, scrim, the imperative close() method) are exercised in the browser test, not here.</p>
  ${sheetFigure()}
</section>
${WIDTHS.map(
    (w) => `<section class="width-frame" data-width="${w}" style="width:min(${w}px, 100%)">
<h2>walkthrough width ${w}px, capped to the window</h2>
<div class="grid">
${kitFigures()}
</div>
</section>`,
  ).join('\n')}
</body>
</html>`;
}
