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

import { CSSResult, html, type CSSResultGroup, type TemplateResult } from 'lit';
import { ShellHeader } from '../src/shell-header.js';
import { ShellRail } from '../src/shell-rail.js';
import { ShellBanner } from '../src/shell-banner.js';
import { ShellUpdate } from '../src/shell-update.js';
import { ShellConflictList, type ShellCardInput } from '../src/shell-cards.js';
import { PhoneHeader } from '../src/phone-header.js';
import { TabBar, type TabSpec, renderTabBar } from '../src/tab-bar.js';
import { ActionBar } from '../src/action-bar.js';
import { FieldSignIn } from '../src/field-sign-in.js';

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

/** The phone frame's three elements, server rendered by their own tags so each emits its own declarative shadow
 * root (the slot needs its host on the render stack). The binding position gate refuses value bindings on the chrome
 * attributes, so every variant a reader compares is its own static template. */
const BACK = html`<button slot="back" aria-label="Back"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M15 18l-6-6 6-6"></path></svg></button>`;
const PHONE_HEADERS = {
  idle: () => html`<hh-phone-header title="Pick queue" sync-state="idle" branch="Point Loma" count="3"></hh-phone-header>`,
  pushing: () => html`<hh-phone-header title="Pick queue" sync-state="pushing" branch="Point Loma" count="3"></hh-phone-header>`,
  conflict: () => html`<hh-phone-header title="Pick queue" sync-state="conflict" branch="Point Loma" count="2"></hh-phone-header>`,
  noBranch: () => html`<hh-phone-header title="Pick queue" sync-state="idle" count="3"></hh-phone-header>`,
  zero: () => html`<hh-phone-header title="Pick queue" sync-state="idle" branch="Point Loma" count="0"></hh-phone-header>`,
  inner: () => html`<hh-phone-header title="SO-1038" sync-state="idle" count="3">${BACK}</hh-phone-header>`,
  tabletIdle: () => html`<hh-phone-header title="Pick queue" sync-state="idle" branch="Point Loma" count="2" org="Harbor Stone Supply" user="Yard Team"></hh-phone-header>`,
  tabletOffline: () => html`<hh-phone-header title="My run" sync-state="offline" branch="Point Loma" count="1" org="Harbor Stone Supply" user="Yard Team"></hh-phone-header>`,
} as const;
type PhoneHeaderVariant = keyof typeof PHONE_HEADERS;

/** The update card mid install, rendered through the element's own click path: a pending
 * updater, the captured click listener, one press on Update now. The in flight flag is
 * private to the element, so the page drives the real code instead of editing the markup. */
function installingMarkup(props: Record<string, unknown>): string {
  const el = new ShellUpdate() as unknown as Record<string, unknown> & {
    render(): TemplateResult;
    connectedCallback(): void;
  };
  let click: ((event: unknown) => void) | undefined;
  el['addEventListener'] = (type: string, fn: (event: unknown) => void) => {
    if (type === 'click') click = fn;
  };
  el['dispatchEvent'] = () => true;
  el['performUpdate'] = () => {};
  el['updater'] = { install: () => new Promise<void>(() => {}) };
  for (const [name, value] of Object.entries(props)) el[name] = value;
  el.connectedCallback();
  const control = Object.create(Element.prototype) as Element;
  control.hasAttribute = (name: string) => name === 'data-action';
  control.getAttribute = (name: string) => (name === 'data-action' ? 'install' : null);
  click?.({ composedPath: () => [control] });
  return collectResultSync(render(el.render()));
}

export function demoSheets(): ReadonlyArray<{ tag: string; css: string }> {
  return [
    { tag: 'hh-shell-header', css: cssTextOf(ShellHeader.styles) },
    { tag: 'hh-shell-rail', css: cssTextOf(ShellRail.styles) },
    { tag: 'hh-shell-offline-banner', css: cssTextOf(ShellBanner.styles) },
    { tag: 'hh-shell-update-prompt', css: cssTextOf(ShellUpdate.styles) },
    { tag: 'hh-shell-conflict-list', css: cssTextOf(ShellConflictList.styles) },
    { tag: 'hh-phone-header', css: cssTextOf(PhoneHeader.styles) },
    { tag: 'hh-tab-bar', css: cssTextOf(TabBar.styles) },
    { tag: 'hh-action-bar', css: cssTextOf(ActionBar.styles) },
    { tag: 'hh-field-sign-in', css: cssTextOf(FieldSignIn.styles) },
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

interface CellOptions {
  /** A phone or tablet width for the host, so its own container query answers at that width. */
  width?: number;
  /** A demo-only rule for a state that needs a pointer or a key: forced here so the page can show it. */
  force?: string;
}

function cell(tag: string, title: string, markup: string, options: CellOptions = {}): string {
  const host = options.width === undefined ? `<${tag}>` : `<${tag} style="display:block;width:min(${options.width}px, 100%)">`;
  const force = options.force === undefined ? '' : `<style>${options.force}</style>`;
  return (
    `<figure class="cell"><figcaption>${title}</figcaption>` +
    host +
    `<template shadowrootmode="open">` +
    `<link rel="stylesheet" href="./sheets/${tag}.css">` +
    `${force}` +
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
function frameCell(
  title: string,
  width: number,
  banner: string,
  headerOver: Record<string, unknown>,
  railCollapsed = false,
): string {
  const header = renderMarkup(ShellHeader, headerProps(headerOver));
  const bannerMarkup =
    banner === ''
    ? ''
    : renderMarkup(ShellBanner, banner === 'read-only' ? { mode: banner } : { mode: banner, waiting: 3 });
  const rail = renderMarkup(ShellRail, { pins: DEMO_PINS, active: DEMO_CURRENT, collapsed: railCollapsed, heading: 'Pinned' });
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

/** The four Field tabs the bar carries, a phone's worth; the demo page's
 * tab-bar renders with a Messages badge so the count badge has something to
 * show. */
const PHONE_TABS: ReadonlyArray<TabSpec> = [
  { key: 'pick', label: 'Pick', icon: 'pick' },
  { key: 'drive', label: 'Drive', icon: 'drive' },
  { key: 'messages', label: 'Messages', icon: 'messages', count: 2 },
  { key: 'more', label: 'More', icon: 'more' },
];

/** One phone frame at the given width: the phone-header, a body and the
 * tab-bar, the action-bar. At 600 px and wider the tab-bar hides itself
 * (its own rule), the action-bar rises to the body's bottom edge. At 390
 * px the bar shows, and the body wraps above it. */
/** The server render emits a tag's own shadow root only for a tag the SSR registry knows, so the two slot bearing
 * elements are registered there once per process (the design system harness does the same for its sheet). */
function registerForRender(): void {
  for (const [tag, ctor] of [['hh-phone-header', PhoneHeader], ['hh-action-bar', ActionBar]] as const) {
    if (!customElements.get(tag)) customElements.define(tag, ctor as unknown as CustomElementConstructor);
  }
}

function phoneFrameCell(title: string, width: number, variant: PhoneHeaderVariant): string {
  registerForRender();
  const header = collectResultSync(render(PHONE_HEADERS[variant]()));
  const actionBar = collectResultSync(render(html`<hh-action-bar><button class="primary">Save pick</button></hh-action-bar>`));
  // The tab bar's tabs are a property, not an attribute, so its shadow root is filled from its own template form.
  const tabBar = `<hh-tab-bar>${shadowed('hh-tab-bar', collectResultSync(render(renderTabBar({ tabs: PHONE_TABS, active: 'pick' }))))}</hh-tab-bar>`;
  return (
    `<figure class="cell"><figcaption>${title}</figcaption>` +
    `<div class="phone-frame" style="width:min(${width}px, 100%)">` +
    header +
    `<div class="phone-body"><div class="phone-ghost">The surface works here; the chrome rides above and below.</div>${actionBar}</div>` +
    tabBar +
    `</div></figure>`
  );
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
  { state: 'idle', over: { stalls: 1 }, note: 'a stall alone: a check needs you' },
  { state: 'conflict', over: { cards: 2, stalls: 1 }, note: 'conflict with a stall' },
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
  const selectorPhone = renderMarkup(
    ShellHeader,
    headerProps({
      selectorOpen: true,
      groups: [
        { app: DEMO_FRONTENDS[0], surfaces: DEMO_SURFACES.get('yard-pick') },
        { app: DEMO_FRONTENDS[1], surfaces: DEMO_SURFACES.get('drive-run') },
        { app: DEMO_FRONTENDS[2], surfaces: [] },
      ],
    }),
  );
  const bandAt = (over: Record<string, unknown> = {}) => renderMarkup(ShellHeader, headerProps(over));
  const railCollapsedPhone = renderMarkup(ShellRail, { pins: DEMO_PINS, active: DEMO_CURRENT, collapsed: true });
  const railExpanded = renderMarkup(ShellRail, { pins: DEMO_PINS, active: DEMO_CURRENT, collapsed: false, heading: 'Pinned' });
  const railEmpty = renderMarkup(ShellRail, { pins: [], active: '', collapsed: false });
  const bannerOffline = renderMarkup(ShellBanner, { mode: 'offline', waiting: 3 });
  const bannerOfflineEmpty = renderMarkup(ShellBanner, { mode: 'offline', waiting: 0 });
  const bannerOfflineOne = renderMarkup(ShellBanner, { mode: 'offline', waiting: 1 });
  const bannerFieldOffline = renderMarkup(ShellBanner, { mode: 'offline', waiting: 3, field: true });
  const bannerUnreachable = renderMarkup(ShellBanner, { mode: 'unreachable', waiting: 2 });
  const bannerReadOnly = renderMarkup(ShellBanner, { mode: 'read-only' });
  const bannerAuth = renderMarkup(ShellBanner, { mode: 'auth-expired' });
  const updateOpen = renderMarkup(ShellUpdate, { open: true, shellName: 'HH Field', version: 'r18' });
  const cardsFull = renderMarkup(ShellConflictList, { cards: DEMO_CARDS });
  const cardsEmpty = renderMarkup(ShellConflictList, { cards: [] });
  const updateNoVersion = renderMarkup(ShellUpdate, { open: true, shellName: 'HH Field' });
  const updateInstalling = installingMarkup({ open: true, shellName: 'HH Field', version: 'r18' });
  const fieldSignIn = renderMarkup(FieldSignIn, { org: 'Harbor Stone Supply', branch: 'Point Loma' });

  // The states that need a pointer or a key are forced here, in this page's own cells only:
  // the same declarations the element's hover and focus rules carry, held to a fixed row.
  const FORCE_TOOLTIP = `.rail[data-collapsed='true'] .row[data-key='${DEMO_CURRENT}'] .label { opacity: 1; }`;
  const FORCE_FOCUS = ".mi[data-key='yard-pick/receiving'] { outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }"

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
  /* The transform makes each frame the containing block of the tab bar's and the action bar's fixed and sticky
     layers, so they sit at the frame's bottom and never cover the rest of the page. */
  .phone-frame { position: relative; transform: translateZ(0); grid-template-columns: minmax(0, 1fr); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); overflow: hidden; display: grid; grid-template-rows: auto 1fr auto; min-height: 480px; background: var(--hh-page); }
  .phone-body { padding: var(--hh-s-16); display: grid; grid-template-rows: 1fr auto; min-height: 0; }
  .phone-frame .phone-body { padding-bottom: 76px; }
  .phone-body .primary { width: 100%; min-height: 48px; border: 1px solid transparent; border-radius: var(--hh-radius-control); background: var(--hh-accent); color: var(--hh-accent-ink); font: inherit; font-weight: 700; }
  .phone-ghost { color: var(--hh-text-dim); font-size: var(--hh-s-12); display: grid; place-items: center; padding: var(--hh-s-16); text-align: center; }
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
${frameCell('the frame, phone 390, offline with three writes waiting, collapsed rail', 390, 'offline', { syncState: 'offline', waiting: 3 }, true)}
${frameCell('the frame, desktop 1280, idle', 1280, '', {})}
</div>
</section>
<section class="width-frame" data-section="band"><h2>The band, state by state</h2>
<div class="grid">
${cell('hh-shell-header', 'band at rest, pinned (the active pin marked)', bandAt())}
${cell('hh-shell-header', 'band at rest, unpinned', bandAt({ pinned: false }))}
${cell('hh-shell-header', 'band, pin disabled: no current surface', bandAt({ currentKey: '', pinned: false }))}
${cell('hh-shell-header', 'band, bell at zero', bandAt({ notifications: 0 }))}
${cell('hh-shell-header', 'band, the Account fallback (no role label)', bandAt({ user: '' }))}
${cell('hh-shell-header', 'band, empty breadcrumb reads Surfaces', bandAt({ breadcrumb: '' }))}
${cell('hh-shell-header', 'band, no org', bandAt({ org: '' }))}
${cell('hh-shell-header', 'band, phone 390: no chip, no org word', bandAt(), { width: 390 })}
</div>
</section>
<section class="width-frame" data-section="selector"><h2>The selector open: the whole map, the current marked</h2>
<div class="grid">
${cell('hh-shell-header', 'the selector panel over the fake manifest, desktop, a keyboard focus ring on Receiving (forced for this page)', selectorOpen, { force: FORCE_FOCUS })}
${cell('hh-shell-header', 'the selector panel, phone 390, House Messages as a front door only row', selectorPhone, { width: 390 })}
</div>
</section>
${syncSection()}
<section class="width-frame" data-section="rail"><h2>The pinned rail: collapsed, expanded, and the empty first impression</h2>
<div class="grid">
${cell('hh-shell-rail', 'collapsed, phone 390, the pins only, icons and tooltips', railCollapsedPhone, { width: 390 })}
${cell('hh-shell-rail', 'collapsed, a tooltip revealed (the hover and focus state, forced for this page), the active bar', railCollapsedPhone, { force: FORCE_TOOLTIP })}
${cell('hh-shell-rail', 'expanded, the words on, the active pin marked', railExpanded)}
${cell('hh-shell-rail', 'empty, expanded: the first impression', railEmpty)}
</div>
</section>
<section class="width-frame" data-section="banner"><h2>The offline banner: the connection's truth, or nothing</h2>
<div class="grid">
${cell('hh-shell-offline-banner', 'offline, three writes in the outbox', bannerOffline)}
${cell('hh-shell-offline-banner', 'offline, empty outbox (writes wait)', bannerOfflineEmpty)}
${cell('hh-shell-offline-banner', 'offline, one write waiting (the plural check)', bannerOfflineOne)}
${cell('hh-shell-offline-banner', 'field offline strip: Offline, three waiting to send and Tap to review', bannerFieldOffline)}
${cell('hh-shell-offline-banner', 'the server can\'t be reached', bannerUnreachable)}
${cell('hh-shell-offline-banner', 'read only: a second tab owns the sync', bannerReadOnly)}
${cell('hh-shell-offline-banner', 'sign in expired, the work safe', bannerAuth)}
</div>
</section>
<section class="width-frame" data-section="update"><h2>The update prompt: quiet until the download is whole</h2>
<div class="grid">
${cell('hh-shell-update-prompt', 'HH Field update ready, Update now and Later', updateOpen)}
${cell('hh-shell-update-prompt', 'HH Field update ready, no version', updateNoVersion)}
${cell('hh-shell-update-prompt', 'update prompt, installing: both buttons disabled', updateInstalling)}
${cell('hh-shell-update-prompt', 'update prompt, phone 390, the card wraps', updateOpen, { width: 390 })}
</div>
</section>
<section class="width-frame" data-section="cards"><h2>The conflict cards: every outcome shows, and a card is dismissed, never deleted</h2>
<div class="grid">
${cell('hh-shell-conflict-list', 'one card of each outcome the sync engine answers', cardsFull)}
${cell('hh-shell-conflict-list', 'the empty list, before the first conflict comes back', cardsEmpty)}
</div>
</section>
<section class="width-frame" data-section="phone-frame"><h2>The shared Field phone frame: phone-header, tab-bar and action-bar ride as one row of chrome on a phone, the rail band on a tablet</h2>
<div class="grid">
${phoneFrameCell('phone 390, synced, three waiting on review', 390, 'idle')}
${phoneFrameCell('phone 390, sending', 390, 'pushing')}
${phoneFrameCell('phone 390, a conflict, two waiting on review', 390, 'conflict')}
${phoneFrameCell('phone 390, no branch chip', 390, 'noBranch')}
${phoneFrameCell('phone 390, nothing waiting on review', 390, 'zero')}
${phoneFrameCell('phone 390, an inner screen with the back control', 390, 'inner')}
${phoneFrameCell('tablet 834, no tab bar, the action at the bottom', 834, 'idle')}
${phoneFrameCell('tablet 834, slim header with tenant lockup from config, synced', 834, 'tabletIdle')}
${phoneFrameCell('tablet 834, slim header with tenant lockup from config, offline', 834, 'tabletOffline')}
</div>
</section>
<section class="width-frame" data-section="field-sign-in"><h2>The Field sign-in card: single centered card with tenant monogram from config and yard PIN hint</h2>
<div class="grid">
${cell('hh-field-sign-in', 'Field sign-in card, phone 390', fieldSignIn, { width: 390 })}
${cell('hh-field-sign-in', 'Field sign-in card, tablet 834', fieldSignIn, { width: 834 })}
</div>
</section>
</body>
</html>`;
}

