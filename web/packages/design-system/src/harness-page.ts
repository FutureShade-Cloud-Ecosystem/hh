// The harness page builder: every kit in its six states (ready plus the five
// designed states), at the two walkthrough widths, 1440 and 1920. The page is a
// STRING, built by Node at serve time from the same render functions the custom
// elements paint, so what the design sync opens is what the kits render, with
// no build toolchain between them.
//
// The theme and brand are served variants, not client toggles: the page has no
// JavaScript at all. ?theme=light flips the html attribute; ?brand=hh-pro adds
// the standalone shell's brand override. The contractor layer is shown by a
// proposal section that sets --cbrand/--cbrand-on inline, the way the public
// proposal surface will, with values that clear the same AA bar the static
// palette is held to (the gate in test/harness.test.mjs measures them).

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { renderTemplate } from './lit.js';
import { KIT_STATES, type KitState } from './state.js';
import { ScanConfirmKit } from './kits/scan-and-confirm.js';
import { ListDetailKit } from './kits/list-detail.js';
import { GuidedFlowKit } from './kits/guided-flow.js';
import { DashboardKit } from './kits/dashboard.js';
import { InboxKit } from './kits/inbox.js';

export const HARNESS_TITLE = '@hh/design-system harness';
export const WIDTHS = [1440, 1920] as const;
export type HarnessTheme = 'dark' | 'light';
export type HarnessBrand = 'base' | 'hh-pro';

export interface KitEntry {
  tag: string;
  title: string;
  element: typeof ScanConfirmKit;
}

export const KITS: ReadonlyArray<KitEntry> = [
  { tag: 'hh-scan-and-confirm', title: 'scan-and-confirm', element: ScanConfirmKit },
  { tag: 'hh-list-detail', title: 'list-detail', element: ListDetailKit },
  { tag: 'hh-guided-flow', title: 'guided-flow', element: GuidedFlowKit },
  { tag: 'hh-dashboard', title: 'dashboard', element: DashboardKit },
  { tag: 'hh-inbox', title: 'inbox', element: InboxKit },
];

export const HARNESS_STATES: ReadonlyArray<KitState> = ['ready', ...KIT_STATES];

// The example contractor accent for the proposal section. Deliberately the
// package's own light-half success ink, whose white on-ink is measured above
// AA; the real surface takes its values from the quote at runtime.
const PROPOSAL_CBRAND = '#0F6C43';
const PROPOSAL_CBRAND_ON = '#ffffff';

const here = dirname(fileURLToPath(import.meta.url));

function kitFigures(): string {
  const figures: string[] = [];
  for (const kit of KITS) {
    for (const state of HARNESS_STATES) {
      figures.push(
        `<figure class="cell" data-cell="${kit.tag}/${state}">` +
          `<figcaption>${kit.title} / ${state}</figcaption>` +
          `<div class="cell-body">${(kit.element as unknown as { renderToString: (p: object) => string }).renderToString({ state })}</div>` +
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
  html, body { margin: 0; background: var(--hh-page); color: var(--hh-text); font-family: var(--hh-font-ui); }
  body { padding: var(--hh-s-24); }
  h1 { font-size: var(--hh-s-24); }
  p.lede { color: var(--hh-text-dim); max-width: var(--hh-measure); }
  .width-frame { border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-16); margin-block-end: var(--hh-s-24); overflow-x: auto; }
  .width-frame > h2 { margin: 0 0 var(--hh-s-12); font-size: var(--hh-s-16); color: var(--hh-text-dim); }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(420px, 100%), 1fr)); gap: var(--hh-s-16); }
  figure.cell { margin: 0; display: grid; gap: var(--hh-s-8); }
  figure.cell figcaption { color: var(--hh-text-dim); font-size: var(--hh-s-12); }
  section.proposal { border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-16); display: grid; gap: var(--hh-s-8); }
  section.proposal h2 { margin: 0; font-size: var(--hh-s-16); }
  section.proposal p { margin: 0; color: var(--hh-text-dim); max-width: var(--hh-measure); }
  .proposal-accent { display: flex; align-items: center; justify-content: space-between; gap: var(--hh-s-12); padding: var(--hh-s-16); border-radius: var(--hh-radius-control); background: var(--cbrand); color: var(--cbrand-on); }
  .proposal-name { font-weight: 600; }
  .proposal-btn { border: 1px solid var(--cbrand-on); background: transparent; color: var(--cbrand-on); border-radius: var(--hh-radius-control); padding: var(--hh-s-8) var(--hh-s-16); min-height: 44px; }
  .proposal-btn:focus-visible { outline: 2px solid var(--cbrand-on); outline-offset: 2px; }
</style>
</head>
<body>
<h1>${HARNESS_TITLE}</h1>
<p class="lede">Static page, no build and no scripts: every kit in its ready state and its five
designed states (empty, loading, offline, conflict, error), at the two walkthrough widths.
Rendered by Node from the kit render functions themselves. Serve with
<code>node --import ./src/_ts-js-loader.mjs harness/serve.mjs</code>; add <code>?theme=light</code>
for the light half and <code>?brand=hh-pro</code> for the standalone shell's brand override.</p>
${proposalSection()}
${WIDTHS.map(
    (w) => `<section class="width-frame" data-width="${w}" style="width:${w}px">
<h2>width ${w}px</h2>
<div class="grid">
${kitFigures()}
</div>
</section>`,
  ).join('\n')}
</body>
</html>`;
}
