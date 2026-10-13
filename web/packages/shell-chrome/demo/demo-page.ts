// The demo harness page: the chrome over a fake manifest, so the fidelity
// pass and the shells can see every state the package paints, and so the
// wiring contract is written down in markup. The page is a STRING, built by
// Node from the same render functions the elements paint (Lit's server
// render, each cell into its own declarative shadow root, no scripts); the
// committed pages are the two builds, demo/index.html (brand mode, the
// default) and demo/index-light.html, with each element's sheet under
// demo/sheets/<tag>.css. test/harness.test.mjs holds the committed bytes to
// a fresh build.
//
// The fake manifest is the demo tenant's Field world (Gable Landscape
// Supply, the GLS r2 branches and role labels): Yard Pick and Receive, HH
// Drive and House Messages for the yard manager, the current surface Pick
// queue, and three pins across the three micro apps. No existing app is
// touched; the shells integrate the chrome by mounting it and wiring the
// model, which is what this page documents in markup.
//
// One honest limit, stated plainly: the page carries no client scripts, so
// what a browser shows on upgrade is what the ATTRIBUTES carry (the band's
// words, the chip's state, the banner's mode, the prompt's card). The model
// objects (the selector's groups, the rail's pins) exist in the rendered
// markup of this page and in the package's tests; a shell wires them in
// code, as the README's wiring section records.

import '@lit-labs/ssr/lib/install-global-dom-shim.js';
import { render } from '@lit-labs/ssr';
import { collectResultSync } from '@lit-labs/ssr/lib/render-result.js';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { CSSResult, type CSSResultGroup, type TemplateResult } from 'lit';
import { ShellHeader } from '../src/shell-header.js';
import { ShellRail } from '../src/shell-rail.js';
import { ShellBanner } from '../src/shell-banner.js';
import { ShellUpdate } from '../src/shell-update.js';
import { ShellConflictList, type ShellCardInput } from '../src/shell-cards.js';

export const DEMO_TITLE = '@hardscapehouse/shell-chrome demo';

export type DemoTheme = 'dark' | 'light';

export const DEMO_WIDTHS = [390, 834] as const;

/** The fake manifest slice the demo wires: the Field shell's three micro
 * apps for the yard manager, verbatim shaped like manifest.yaml frontends. */
export const DEMO_FRONTENDS = [
  { id: 'yard-pick', name: 'Yard Pick and Receive', roles: ['yard_team', 'yard_manager'] },
  { id: 'drive-run', name: 'HH Drive', roles: ['driver', 'yard_manager'] },
  { id: 'house-messages', name: 'House Messages', roles: ['yard_team', 'driver', 'yard_manager'] },
] as const;

export const DEMO_SURFACES: ReadonlyMap<string, ReadonlyArray<{ id: string; label: string }>> = new Map([
  ['yard-pick', [{ id: 'pick-queue', label: 'Pick queue' }, { id: 'receiving', label: 'Receiving' }]],
  ['drive-run', [{ id: 'my-run', label: 'My run' }]],
  ['house-messages', [{ id: 'inbox', label: 'Inbox' }]],
]);

export const DEMO_PINS = [
  { key: 'yard-pick/pick-queue', label: 'Pick queue', glyph: 'P' },
  { key: 'drive-run/my-run', label: 'My run', glyph: 'D' },
  { key: 'house-messages/inbox', label: 'House Messages', glyph: 'H' },
] as const;

const DEMO_ROLES = ['yard_manager'];
const DEMO_ORG = 'Gable Landscape Supply';
const DEMO_USER = 'Yard Team';
const DEMO_CURRENT = 'yard-pick/pick-queue';

const here = dirname(fileURLToPath(import.meta.url));

/** Flattens a static styles group to the one stylesheet string an element's
 * shadow root paints. */
function cssTextOf(group: CSSResultGroup | undefined): string {
  if (Array.isArray(group)) return group.map((g) => cssTextOf(g as CSSResult)).join('');
  if (group instanceof CSSResult) return group.cssText;
  return '';
}

/** The element's markup for the given props, rendered by Lit's server render. */
function renderMarkup(element: new () => { render(): TemplateResult }, props: Record<string, unknown> = {}): string {
  const instance = new element() as unknown as Record<string, unknown> & { render(): TemplateResult };
  for (const [name, value] of Object.entries(props)) instance[name] = value;
  return collectResultSync(render(instance.render()));
}

export function demoSheets(): ReadonlyArray<{ tag: string; css: string }> {
  return [
    { tag: 'hh-shell-header', css: cssTextOf(ShellHeader.styles) },
    { tag: 'hh-shell-rail', css: cssTextOf(ShellRail.styles) },
    { tag: 'hh-shell-offline-banner', css: cssTextOf(ShellBanner.styles) },
    { tag: 'hh-shell-update-prompt', css: cssTextOf(ShellUpdate.styles) },
    { tag: 'hh-shell-conflict-list', css: cssTextOf(ShellConflictList.styles) },
  ];
}

/** The demo cards: one outcome of each family the sync engine answers, the
 * words the elements themselves paint. */
export const DEMO_CARDS: ReadonlyArray<ShellCardInput> = [
  {
    id: 'card:demo-stop',
    kind: 'superseded',
    scope: 'stops',
    policy: 'server_wins',
    serverState: { id: 'st-demo', stop_no: 3, customer_name: 'Northline Hardscapes (demo)' },
    clientPayload: { id: 'st-demo', time_window: 'morning' },
    error: null,
  },
  {
    id: 'card:demo-shortage',
    kind: 'review',
    scope: 'shortages',
    policy: 'manual',
    serverState: { id: 'sh-demo', description: 'Flagstone, random, mixed grey', qty_short: '22' },
    clientPayload: { id: 'sh-demo', resolution: 'Settled from Bulk 2' },
    error: null,
  },
  {
    id: 'card:demo-merge',
    kind: 'merge_report',
    scope: 'attachments',
    policy: 'last_write_wins_by_field',
    serverState: { id: 'at-demo', checksum: 'sha256:demo' },
    clientPayload: { id: 'at-demo', mime: 'image/jpeg' },
    error: null,
  },
  {
    id: 'card:demo-refused',
    kind: 'rejected',
    scope: 'pick_tasks',
    policy: null,
    serverState: {},
    clientPayload: { id: 'tk-demo' },
    error: 'line 2 is short, give the reason',
  },
];

function cell(tag: string, title: string, markup: string): string {
  return (
    `<figure class="cell"><figcaption>${title}</figcaption>` +
    `<${tag}>` +
    `<template shadowrootmode="open">` +
    `<link rel="stylesheet" href="./sheets/${tag}.css">` +
    `${markup}` +
    `</template>` +
    `</${tag}>` +
    `</figure>`
  );
}

/** The header's props over the fake manifest, as a plain object the element
 * takes through properties; the attribute twins ride the tags below. */
function headerProps(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    org: DEMO_ORG,
    user: DEMO_USER,
    breadcrumb: 'Yard Pick and Receive › Pick queue',
    groups: [
      { app: DEMO_FRONTENDS[0], surfaces: DEMO_SURFACES.get('yard-pick') },
      { app: DEMO_FRONTENDS[1], surfaces: DEMO_SURFACES.get('drive-run') },
      { app: DEMO_FRONTENDS[2], surfaces: DEMO_SURFACES.get('house-messages') },
    ],
    currentKey: DEMO_CURRENT,
    pinned: true,
    syncState: 'idle',
    cards: 0,
    waiting: 3,
    attempts: 0,
    stalls: 0,
    notifications: 2,
    ...over,
  };
}

/** One shell frame: band, offline banner, and the rail over a working
 * surface, composed the way a shell composes them. */
function frameCell(title: string, width: number, banner: string, headerOver: Record<string, unknown>): string {
  const header = renderMarkup(ShellHeader, headerProps(headerOver));
  const bannerMarkup =
    banner === ''
    ? ''
    : renderMarkup(ShellBanner, banner === 'read-only' ? { mode: banner } : { mode: banner, waiting: 3 });
  const rail = renderMarkup(ShellRail, { pins: DEMO_PINS, active: DEMO_CURRENT, collapsed: false, heading: 'Pinned' });
  return (
    `<figure class="cell"><figcaption>${title}</figcaption>` +
    `<div class="shell-frame" style="width:min(${width}px, 100%)">` +
    `<hh-shell-header>${shadowed('hh-shell-header', header)}</hh-shell-header>` +
    (bannerMarkup === '' ? '' : `<hh-shell-offline-banner>${shadowed('hh-shell-offline-banner', bannerMarkup)}</hh-shell-offline-banner>`) +
    `<div class="frame-body">` +
    `<hh-shell-rail>${shadowed('hh-shell-rail', rail)}</hh-shell-rail>` +
    `<div class="ghost" aria-hidden="true">The surface works here; the rail borrows the edge and the band stays one band.</div>` +
    `</div></div></figure>`
  );
}

function shadowed(tag: string, markup: string): string {
  return `<template shadowrootmode="open"><link rel="stylesheet" href="./sheets/${tag}.css">${markup}</template>`;
}

const SYNC_STATES_CELLS: ReadonlyArray<{ state: string; over?: Record<string, unknown>; note: string }> = [
  { state: 'idle', note: 'at rest, outbox draining done' },
  { state: 'pulling', note: 'the scopes are being pulled' },
  { state: 'pushing', over: { waiting: 3 }, note: 'the outbox is replaying, in order' },
  { state: 'offline', over: { waiting: 3 }, note: 'the platform says no network' },
  { state: 'unreachable', over: { attempts: 1 }, note: 'online, but the request did not land' },
  { state: 'conflict', over: { cards: 1 }, note: 'cards wait for a person' },
  { state: 'auth_expired', over: { attempts: 0 }, note: 'the session is gone; the outbox is parked' },
  { state: 'pushing', over: { waiting: 4, attempts: 2 }, note: 'stuck: two attempts, the chip alarms in words' },
];

function syncSection(): string {
  const cells = SYNC_STATES_CELLS.map((c) => {
    const markup = renderMarkup(ShellHeader, headerProps({ syncState: c.state, ...c.over }));
    return cell('hh-shell-header', `sync chip / ${c.state} (${c.note})`, markup);
  });
  return `<section class="width-frame" data-section="sync-states"><h2>The sync statechart's states, as the band shows them</h2><div class="grid">${cells.join('\n')}</div></section>`;
}

export function buildDemoPage(opts: { theme?: DemoTheme } = {}): string {
  const theme: DemoTheme = opts.theme === 'light' ? 'light' : 'dark';
  const tokens = readFileSync(join(here, '..', '..', 'design-system', 'src', 'tokens.css'), 'utf8');
  const themeAttr = theme === 'light' ? ' data-theme="light"' : '';

  const selectorOpen = renderMarkup(ShellHeader, headerProps({ selectorOpen: true }));
  const railCollapsedPhone = renderMarkup(ShellRail, { pins: DEMO_PINS, active: DEMO_CURRENT, collapsed: true });
  const railEmpty = renderMarkup(ShellRail, { pins: [], active: '', collapsed: false });
  const bannerOffline = renderMarkup(ShellBanner, { mode: 'offline', waiting: 3 });
  const bannerUnreachable = renderMarkup(ShellBanner, { mode: 'unreachable', waiting: 2 });
  const bannerReadOnly = renderMarkup(ShellBanner, { mode: 'read-only' });
  const bannerAuth = renderMarkup(ShellBanner, { mode: 'auth-expired' });
  const updateOpen = renderMarkup(ShellUpdate, { open: true, shellName: 'HH Field', version: 'r18' });
  const cardsFull = renderMarkup(ShellConflictList, { cards: DEMO_CARDS });
  const cardsEmpty = renderMarkup(ShellConflictList, { cards: [] });

  return `<!doctype html>
<html lang="en"${themeAttr}>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${DEMO_TITLE}</title>
<style>
${tokens}
</style>
<style>
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
  .shell-frame { border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); overflow: hidden; display: grid; grid-template-rows: auto auto 1fr; min-height: 260px; }
  .frame-body { position: relative; display: flex; min-height: 240px; }
  .ghost { flex: 1; display: grid; place-items: center; color: var(--hh-text-dim); font-size: var(--hh-s-12); padding: var(--hh-s-16); text-align: center; }
</style>
</head>
<body>
<h1>${DEMO_TITLE}</h1>
<p class="lede">Static page, no scripts: the shell chrome over the demo tenant's fake Field manifest
(Yard Pick and Receive, HH Drive and House Messages for the yard manager), every state the package
paints, at the phone and tablet widths. Rendered by Node from the element render functions
themselves, each cell into its own declarative shadow root. The committed pages are the two builds:
<code>index.html</code> (brand mode, the default) beside <code>index-light.html</code>, with the
element sheets under <code>demo/sheets/</code> travelling with them. Serve it live with
<code>npm run demo</code> (a port in the field cycle's range, refused if taken). The model objects
(the selector's groups, the rail's pins) are wired in code by a shell; this page carries their
rendered output and the package's tests hold the wiring contract.</p>
<section class="width-frame" data-section="frames"><h2>The frame: one band, the banner, the rail over a working surface</h2>
<div class="grid">
${frameCell('the frame, tablet 834, offline with three writes waiting', 834, 'offline', { syncState: 'offline', waiting: 3 })}
${frameCell('the frame, phone 390, brand idle (the phone band drops the chip and the org word)', 390, '', {})}
</div>
</section>
<section class="width-frame" data-section="selector"><h2>The selector open: the whole map, the current marked</h2>
<div class="grid">
${cell('hh-shell-header', 'the selector panel over the fake manifest', selectorOpen)}
</div>
</section>
${syncSection()}
<section class="width-frame" data-section="rail"><h2>The pinned rail: collapsed, expanded, and the empty first impression</h2>
<div class="grid">
${cell('hh-shell-rail', 'collapsed, the pins only, icons and tooltips', railCollapsedPhone)}
${cell('hh-shell-rail', 'expanded, the words on, the active pin marked', railEmpty)}
</div>
</section>
<section class="width-frame" data-section="banner"><h2>The offline banner: the connection's truth, or nothing</h2>
<div class="grid">
${cell('hh-shell-offline-banner', 'offline, three writes in the outbox', bannerOffline)}
${cell('hh-shell-offline-banner', 'the server can\'t be reached', bannerUnreachable)}
${cell('hh-shell-offline-banner', 'read only: a second tab owns the sync', bannerReadOnly)}
${cell('hh-shell-offline-banner', 'sign in expired, the work safe', bannerAuth)}
</div>
</section>
<section class="width-frame" data-section="update"><h2>The update prompt: quiet until the download is whole</h2>
<div class="grid">
${cell('hh-shell-update-prompt', 'HH Field update ready, Update now and Later', updateOpen)}
</div>
</section>
<section class="width-frame" data-section="cards"><h2>The conflict cards: every outcome shows, and a card is dismissed, never deleted</h2>
<div class="grid">
${cell('hh-shell-conflict-list', 'one card of each outcome the sync engine answers', cardsFull)}
${cell('hh-shell-conflict-list', 'the empty list, before the first conflict comes back', cardsEmpty)}
</div>
</section>
</body>
</html>`;
}
