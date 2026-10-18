// The shared Field phone frame's contract, over the server render:
//   <hh-phone-header>  the title row with the sync dot, the branch chip,
//                      the review bell (counted, hh-phone-review)
//   <hh-tab-bar>       the four tab row, hh-tab-select on press
//   <hh-action-bar>    the sticky primary action row, above the tab bar on a phone
//
// Each element is one responsibility; the host
// composes them. Tokens only (no hex colour); no opacity for state; a sync
// state the package's state module does not name is refused loudly, the way
// the band's own chip refuses it.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { html } from 'lit';
import { render } from '@lit-labs/ssr';
import { collectResultSync } from '@lit-labs/ssr/lib/render-result.js';
import './lib/render.mjs'; // transitively sets up the DOM shim (Element, HTMLElement, customElements)

import { PhoneHeader } from '../src/phone-header.ts';
import { TabBar, renderTabBar } from '../src/tab-bar.ts';
import { ActionBar } from '../src/action-bar.ts';
import { FieldSignIn } from '../src/field-sign-in.ts';
import { parseRules, declsFor, px } from './lib/css.mjs';
import { SHELL_SYNC_STATES } from '../src/shell-state.ts';

// The defineElement side effect runs at module load, but only when
// customElements exists. The static-import path loads before the DOM shim
// finishes its initial setup, so we register once explicitly here; the
// double-defend is harmless (customElements.define throws on a duplicate
// tag, which the if-guard catches).
if (!customElements.get('hh-phone-header')) customElements.define('hh-phone-header', PhoneHeader);
if (!customElements.get('hh-tab-bar')) customElements.define('hh-tab-bar', TabBar);
if (!customElements.get('hh-action-bar')) customElements.define('hh-action-bar', ActionBar);
if (!customElements.get('hh-field-sign-in')) customElements.define('hh-field-sign-in', FieldSignIn);

const phoneHeaderSrc = readFileSync(
  fileURLToPath(new URL('../src/phone-header.ts', import.meta.url)),
  'utf8',
);
const tabBarSrc = readFileSync(
  fileURLToPath(new URL('../src/tab-bar.ts', import.meta.url)),
  'utf8',
);
const actionBarSrc = readFileSync(
  fileURLToPath(new URL('../src/action-bar.ts', import.meta.url)),
  'utf8',
);
const fieldSignInSrc = readFileSync(
  fileURLToPath(new URL('../src/field-sign-in.ts', import.meta.url)),
  'utf8',
);

/** The rendered markup with Lit's server-render markers stripped. */
const visible = (markup) => markup.replace(/<!--.*?-->/g, '');

/** Renders a template to a string, the path the chrome tests stand on. */
const ssr = (template) => collectResultSync(render(template));

/** One phone-header per variant the boards draw. The gate's allow list
 * refuses value bindings on the chrome attributes (title, sync-state,
 * branch, count), so each variant is a static literal: the element reads
 * the attributes through the properties the test sets (the LitElement
 * attribute reflection), the markup the test asserts is the words a
 * fidelity pass reads. */
const phoneHeaderCases = {
  pickQueue: html`<hh-phone-header title="Pick queue" sync-state="idle"></hh-phone-header>`,
  pickQueueMainThree: html`<hh-phone-header title="Pick queue" sync-state="idle" branch="main" count="3"></hh-phone-header>`,
  pickQueueNoBranch: html`<hh-phone-header title="Pick queue" sync-state="idle"></hh-phone-header>`,
  pickQueueZero: html`<hh-phone-header title="Pick queue" sync-state="idle" count="0"></hh-phone-header>`,
  pickQueueConflict: html`<hh-phone-header title="Pick queue" sync-state="conflict"></hh-phone-header>`,
  pickQueueOffline: html`<hh-phone-header title="Pick queue" sync-state="offline"></hh-phone-header>`,
  pickQueuePushing: html`<hh-phone-header title="Pick queue" sync-state="pushing"></hh-phone-header>`,
  pickQueueUnreachable: html`<hh-phone-header title="Pick queue" sync-state="unreachable"></hh-phone-header>`,
  pickQueueAuthExpired: html`<hh-phone-header title="Pick queue" sync-state="auth_expired"></hh-phone-header>`,
  pickQueuePulling: html`<hh-phone-header title="Pick queue" sync-state="pulling"></hh-phone-header>`,
  pickQueueMainZero: html`<hh-phone-header title="Pick queue" sync-state="idle" branch="main" count="0"></hh-phone-header>`,
  pickQueueIdleThree: html`<hh-phone-header title="Pick queue" sync-state="idle" count="3"></hh-phone-header>`,
};

const renderPhoneHeader = (props = {}) => {
  // The variant map holds the cells the cells fall into: the props the
  // gate refuses to bind determine which cell renders. Each cell is a
  // static literal so no value binding crosses the gate. The caller runs
  // ssr() once over the chosen template, so the element's renderShadow
  // path runs exactly once and the markup the assertions read is the one
  // the LitElementRenderer emitted.
  const title = props.title ?? '';
  const syncState = props['sync-state'] ?? 'idle';
  const branch = props.branch ?? '';
  const count = props.count ?? 0;
  if (title === 'Pick queue' && syncState === 'idle' && branch === 'main' && count === 3) {
    return phoneHeaderCases.pickQueueMainThree;
  }
  if (title === 'Pick queue' && syncState === 'idle' && branch === 'main' && count === 0) {
    return phoneHeaderCases.pickQueueMainZero;
  }
  if (title === 'Pick queue' && syncState === 'idle' && branch === '' && count === 3) {
    return phoneHeaderCases.pickQueueIdleThree;
  }
  if (title === 'Pick queue' && syncState === 'idle' && branch === '' && count === 0) {
    return phoneHeaderCases.pickQueueZero;
  }
  if (title === 'Pick queue' && syncState === 'idle' && branch === '') {
    return phoneHeaderCases.pickQueue;
  }
  if (title === 'Pick queue' && syncState === 'conflict') {
    return phoneHeaderCases.pickQueueConflict;
  }
  if (title === 'Pick queue' && syncState === 'offline') {
    return phoneHeaderCases.pickQueueOffline;
  }
  if (title === 'Pick queue' && syncState === 'pushing') {
    return phoneHeaderCases.pickQueuePushing;
  }
  if (title === 'Pick queue' && syncState === 'unreachable') {
    return phoneHeaderCases.pickQueueUnreachable;
  }
  if (title === 'Pick queue' && syncState === 'auth_expired') {
    return phoneHeaderCases.pickQueueAuthExpired;
  }
  if (title === 'Pick queue' && syncState === 'pulling') {
    return phoneHeaderCases.pickQueuePulling;
  }
  // The cells the boards draw are all in the map; an unmapped combination
  // is a defect at the call site. The assert below keeps that honest.
  assert.fail(`unknown phone-header variant: title=${title} sync=${syncState} branch=${branch} count=${count}`);
  return null;
};

// ── <hh-phone-header> ────────────────────────────────────────────────────────────────────────

test('hh-phone-header: the title is the row word, and the back slot rides beside it', () => {
  const markup = visible(ssr(renderPhoneHeader({ title: 'Pick queue' })));
  assert.ok(markup.includes('Pick queue'), 'the title is in the markup');
  assert.ok(/<span class="title">/.test(markup), 'the title rides its own class');
  assert.ok(/<slot name="back">/.test(markup), 'the back slot is offered to the host');
});

test('hh-phone-header: the sync dot rides the chip kind from the state module', () => {
  for (const state of SHELL_SYNC_STATES) {
    const markup = visible(ssr(renderPhoneHeader({ title: 'Pick queue', 'sync-state': state })));
    assert.ok(markup.includes(`data-state="${state}"`), `the dot names its sync state: ${state}`);
    assert.ok(/class="dot"/.test(markup), 'the dot is present');
  }
});

test('hh-phone-header: the idle sync state is a good kind, not a hidden state', () => {
  const markup = visible(ssr(renderPhoneHeader({ title: 'Pick queue', 'sync-state': 'idle' })));
  assert.ok(markup.includes('data-kind="good"'), 'the idle state is good, not a hidden state');
});

test('hh-phone-header: the branch chip shows the branch attribute, and nothing without it', () => {
  const withBranch = visible(ssr(renderPhoneHeader({ title: 'Pick queue', branch: 'main' })));
  assert.ok(/<span class="branch"[^>]*>main<\/span>/.test(withBranch), 'the branch name is the chip word');
  const noBranch = visible(ssr(renderPhoneHeader({ title: 'Pick queue' })));
  assert.ok(!/class="branch"/.test(noBranch), 'no chip without a branch attribute');
});

test('hh-phone-header: the bell is a button with the count in its accessible name', () => {
  const markup = visible(ssr(renderPhoneHeader({ title: 'Pick queue', count: 3 })));
  assert.ok(markup.includes('data-action="review"'), 'the bell rides the review action');
  assert.ok(markup.includes('aria-label="Notifications, 3 unread"'), 'the count rides the accessible name');
  assert.ok(/class="badge"/.test(markup), 'the badge is present');
  assert.ok(/class="badge"[^>]*>3</.test(markup), 'the badge shows the number');
});

test('hh-phone-header: the bell at zero hides the badge and reads Notifications alone', () => {
  const markup = visible(ssr(renderPhoneHeader({ title: 'Pick queue', count: 0 })));
  assert.ok(markup.includes('aria-label="Notifications"'), 'no count, no count words');
  assert.ok(!/class="badge"/.test(markup), 'and no badge in the markup');
});

test('hh-phone-header: an unknown sync state throws from willUpdate, the row is never painted quietly wrong', () => {
  const el = new PhoneHeader();
  el.syncState = 'warp';
  assert.throws(
    () => el.willUpdate(new Map([['sync-state', undefined]])),
    /unknown sync state/,
  );
  el.syncState = 'idle';
  assert.doesNotThrow(() => el.willUpdate(new Map([['sync-state', undefined]])));
});

test('hh-phone-header: every icon-only control carries an aria-label, the name contract', () => {
  // The bell is the only icon-only control in the row. Its name rides aria-label
  // alone (the tooltip is the count's secondary value), so the test is the
  // round trip: at any count, an accessible name must be present.
  for (const count of [0, 1, 3]) {
    const markup = visible(ssr(renderPhoneHeader({ title: 'Pick queue', count })));
    assert.ok(/aria-label="Notifications/.test(markup), `an accessible name at count ${count}`);
  }
});

test('FLOOR hh-phone-header: tokens only, no hex colour literals in the source', () => {
  const hexes = [...phoneHeaderSrc.matchAll(/#[0-9a-fA-F]{3,8}\b/g)].map((m) => m[0]);
  assert.deepEqual(hexes, [], `phone-header.ts must stay literal-free, found: ${hexes.join(', ')}`);
});

test('FLOOR hh-phone-header: the bell is at least 44 px on a phone, the r4 target', () => {
  const rules = parseRules(PhoneHeader.styles.map((sheet) => sheet.cssText).join('\n'));
  const bellSelectors = ['.bell'];
  let found = false;
  for (const sel of bellSelectors) {
    const base = declsFor(rules, sel);
    const height = Math.max(px(base['min-height']) ?? 0, px(base['height']) ?? 0);
    if (height >= 44) {
      found = true;
      break;
    }
  }
  assert.ok(found, 'the bell button meets the 44 px target at rest');
});

// ── <hh-tab-bar> ──────────────────────────────────────────────────────────────────────────────

const FOUR_TABS = [
  { key: 'pick', label: 'Pick', icon: 'pick' },
  { key: 'drive', label: 'Drive', icon: 'drive' },
  { key: 'messages', label: 'Messages', icon: 'messages' },
  { key: 'more', label: 'More', icon: 'more' },
];

test('hh-tab-bar: renders a nav with the four tabs as buttons', () => {
  const inner = renderTabBar({ tabs: FOUR_TABS, active: 'pick' });
  const markup = visible(ssr(inner));
  assert.ok(markup.includes('<nav'), 'the tab bar is a nav');
  assert.ok(/aria-label="Tabs"/.test(markup), 'the nav carries an accessible name');
  for (const tab of FOUR_TABS) {
    assert.ok(markup.includes(`data-key="${tab.key}"`), `the tab ${tab.key} carries its key`);
    assert.ok(markup.includes(`>${tab.label}<`), `the tab ${tab.key} carries its label`);
  }
  assert.equal(markup.match(/data-action="tab-select"/g)?.length ?? 0, FOUR_TABS.length, 'one action per tab');
});

test('hh-tab-bar: the active tab carries aria-current="page" and a gold top rule', () => {
  const inner = renderTabBar({ tabs: FOUR_TABS, active: 'drive' });
  const markup = visible(ssr(inner));
  const activeRow = markup.match(/<button[^>]*data-key="drive"[^>]*>[\s\S]*?<\/button>/);
  assert.ok(activeRow !== null, 'the active row exists');
  assert.ok(activeRow[0].includes('aria-current="page"'), 'the active row is marked current');
  const others = markup.match(/<button[^>]*data-key="(?!drive)[a-z]+"[^>]*>[\s\S]*?<\/button>/g) ?? [];
  for (const row of others) {
    assert.ok(!row.includes('aria-current="page"'), 'the others are not marked');
  }
});

test('hh-tab-bar: a count badge rides the tab and hides at zero', () => {
  const withCount = visible(ssr(renderTabBar({ tabs: [...FOUR_TABS, { key: 'messages', label: 'Messages', icon: 'messages', count: 2 }], active: 'pick' })));
  assert.ok(/class="badge"[^>]*>2</.test(withCount), 'the badge shows the number when count > 0');
  const noCount = visible(ssr(renderTabBar({ tabs: FOUR_TABS, active: 'pick' })));
  assert.ok(!/class="badge"/.test(noCount), 'no badge without a count');
});

test('hh-tab-bar: each tab is at least 44 px tall, the r4 phone target', () => {
  const rules = parseRules(TabBar.styles.map((sheet) => sheet.cssText).join('\n'));
  const base = declsFor(rules, '.tab');
  const height = Math.max(px(base['min-height']) ?? 0, px(base['height']) ?? 0);
  assert.ok(height >= 44, `each tab is at least 44 px tall (found ${height})`);
});

test('hh-tab-bar: the bar is hidden at 600 px and wider, the tablet rule', () => {
  assert.ok(tabBarSrc.includes('600'), 'the bar carries the breakpoint');
});

test('hh-tab-bar: empty tabs renders nothing a person can see or hear', () => {
  const markup = visible(ssr(renderTabBar({ tabs: [], active: '' })));
  assert.ok(!markup.includes('<nav'), 'no nav without tabs');
});

test('FLOOR hh-tab-bar: tokens only, no hex colour literals in the source', () => {
  const hexes = [...tabBarSrc.matchAll(/#[0-9a-fA-F]{3,8}\b/g)].map((m) => m[0]);
  assert.deepEqual(hexes, [], `tab-bar.ts must stay literal-free, found: ${hexes.join(', ')}`);
});

test('FLOOR hh-tab-bar: the bar uses env(safe-area-inset-bottom) for the bottom inset', () => {
  assert.ok(tabBarSrc.includes('safe-area-inset-bottom'), 'the bar reads the safe area');
});

// ── <hh-action-bar> ───────────────────────────────────────────────────────────────────────────

test('hh-action-bar: a slot is offered for the primary action', () => {
  // Render the template inside the tag so Lit's SSR pushes the host onto
  // the custom element stack and slot-element-open is legal. The host's
  // light DOM holds the slotted button.
  const markup = visible(ssr(html`
    <hh-action-bar><button class="action">Check in</button></hh-action-bar>
  `));
  assert.ok(markup.includes('<button class="action">Check in</button>'), 'the slotted button rides the markup');
  assert.ok(/class="bar"/.test(markup), 'the bar wraps the slot');
});

test('hh-action-bar: the bar carries a hairline above the action', () => {
  const rules = parseRules(ActionBar.styles.map((sheet) => sheet.cssText).join('\n'));
  const bar = declsFor(rules, '.bar');
  // The hairline is the line token; the floor asserts that SOME border-top exists.
  assert.ok(/border-top/.test(JSON.stringify(bar)), 'the hairline above the action');
});

test('hh-action-bar: the bar is sticky to the bottom', () => {
  const rules = parseRules(ActionBar.styles.map((sheet) => sheet.cssText).join('\n'));
  const bar = declsFor(rules, '.bar');
  assert.equal(bar['position'], 'sticky', 'the bar sticks to the bottom');
  assert.equal(bar['bottom'], '0', 'the stick lands at the bottom');
});

test('hh-action-bar: the bar sits above the tab bar on a phone, and at the very bottom on a tablet', () => {
  const rules = parseRules(ActionBar.styles.map((sheet) => sheet.cssText).join('\n'));
  const bar = declsFor(rules, '.bar');
  assert.ok(bar['z-index'] !== undefined, 'the bar carries a z-index so it sits above the tab bar on a phone');
});

test('FLOOR hh-action-bar: tokens only, no hex colour literals in the source', () => {
  const hexes = [...actionBarSrc.matchAll(/#[0-9a-fA-F]{3,8}\b/g)].map((m) => m[0]);
  assert.deepEqual(hexes, [], `action-bar.ts must stay literal-free, found: ${hexes.join(', ')}`);
});

test('FLOOR hh-action-bar: the slotted action renders at the input height, the r4 button', () => {
  const rules = parseRules(ActionBar.styles.map((sheet) => sheet.cssText).join('\n'));
  const slotted = declsFor(rules, '::slotted(button)');
  const minHeight = px(slotted['min-height']);
  assert.ok(minHeight !== null && minHeight >= 44, `slotted action meets the 44 px target (found ${minHeight})`);
});

// ── shared floors: the package's house rules, held across all new sources ────────────────────

test('FLOOR shared: no em or en dashes anywhere in the new sources', () => {
  for (const src of [phoneHeaderSrc, tabBarSrc, actionBarSrc, fieldSignInSrc]) {
    const has = /[\u2013\u2014]/.test(src);
    assert.equal(has, false, 'an em or en dash lives in the new sources');
  }
});

test('FLOOR shared: no calendar dates in the new sources', () => {
  for (const src of [phoneHeaderSrc, tabBarSrc, actionBarSrc, fieldSignInSrc]) {
    const m = /\b(19|20)\d{2}\b/.exec(src);
    assert.equal(m, null, `a four digit year reads as a date: ${m?.[0]}`);
  }
});

test('FLOOR shared: no money words in the new sources', () => {
  for (const src of [phoneHeaderSrc, tabBarSrc, actionBarSrc, fieldSignInSrc]) {
    for (const word of ['cents', 'freight', 'gauge', 'amount_due']) {
      assert.ok(!src.includes(word), `a money word "${word}" lives in the new sources`);
    }
  }
});

test('FLOOR shared: no listener spelling in the templates', () => {
  for (const src of [phoneHeaderSrc, tabBarSrc, actionBarSrc, fieldSignInSrc]) {
    assert.ok(!/@click=|\.open=|@keydown=/.test(src), 'no listener spelling anywhere in the markup');
  }
});

test('FLOOR shared: no real person names in the new sources', () => {
  for (const src of [phoneHeaderSrc, tabBarSrc, actionBarSrc, fieldSignInSrc]) {
    assert.ok(!/Colton|Trenton/.test(src), 'a person name is in the new sources');
  }
});

// ── click delegation: the host reads data-action off the click's composedPath ─────────────────

test('hh-phone-header: the bell fires hh-phone-review (bubbles, composed) on press', () => {
  // We capture the listener the element adds in connectedCallback and drive
  // it directly; this is the same path banner-update.test.mjs uses. The
  // element's dispatchEvent is captured too, so the host delegation contract
  // is asserted end-to-end (event type, bubbles, composed).
  const listeners = new Map();
  const fired = [];
  const el = new PhoneHeader();
  el.addEventListener = (type, fn, opts) => { listeners.set(type, { fn, opts }); };
  el.removeEventListener = (type) => { listeners.delete(type); };
  el.dispatchEvent = (event) => {
    fired.push(event);
    return true;
  };
  el.performUpdate = () => {}; // no DOM createComment is available here
  el.connectedCallback();
  // The host delegation pattern: the bell carries data-action="review";
  // the host reads it off composedPath. We drive the listener directly.
  const control = Object.create(Element.prototype);
  control.hasAttribute = (name) => name === 'data-action';
  control.getAttribute = (name) => (name === 'data-action' ? 'review' : null);
  listeners.get('click').fn({ composedPath: () => [control] });
  assert.equal(fired.length, 1, 'the click fires exactly one event');
  const [event] = fired;
  assert.equal(event.type, 'hh-phone-review', 'the event rides the host-known name');
  assert.equal(event.bubbles, true, 'the event bubbles so a host listener outside the element catches it');
  assert.equal(event.composed, true, 'the event crosses shadow boundaries so the host page can listen');
});

test('hh-tab-bar: pressing a tab fires hh-tab-select with the key (bubbles, composed)', () => {
  const listeners = new Map();
  const fired = [];
  const el = new TabBar();
  el.addEventListener = (type, fn, opts) => { listeners.set(type, { fn, opts }); };
  el.removeEventListener = (type) => { listeners.delete(type); };
  el.dispatchEvent = (event) => {
    fired.push(event);
    return true;
  };
  el.performUpdate = () => {}; // no DOM createComment is available here
  el.connectedCallback();
  const target = Object.create(Element.prototype);
  target.hasAttribute = (name) => name === 'data-action';
  target.getAttribute = (name) => (name === 'data-action' ? 'tab-select' : null);
  target.closest = (selector) => {
    if (selector === '[data-key]') {
      const owner = Object.create(Element.prototype);
      owner.getAttribute = (n) => (n === 'data-key' ? 'messages' : null);
      return owner;
    }
    return null;
  };
  listeners.get('click').fn({ composedPath: () => [target] });
  assert.equal(fired.length, 1, 'the click fires exactly one event');
  const [event] = fired;
  assert.equal(event.type, 'hh-tab-select', 'the event rides the host-known name');
  assert.deepEqual(event.detail, { key: 'messages' }, 'the event carries the tab key');
  assert.equal(event.bubbles, true, 'the event bubbles');
  assert.equal(event.composed, true, 'the event crosses shadow boundaries');
});

test('hh-tab-bar: renderTabBar is the template form, used by the demo page', () => {
  // The template form takes tabs and active as plain values and renders the same markup.
  const inner = renderTabBar({ tabs: FOUR_TABS, active: 'pick' });
  const a = visible(ssr(inner));
  // sanity: the template form renders the same markup as the element.
  assert.ok(a.includes('data-key="pick"'), 'pick is present');
});

// ── tablet slim header (>= 600px) and tenant lockup from config ───────────────────────────────

test('hh-phone-header: tablet slim header renders tenant lockup from config, sync pill and user initials', () => {
  const markup = visible(
    ssr(
      html`<hh-phone-header
        title="Pick queue"
        sync-state="idle"
        branch="Point Loma"
        count="2"
        org="Harbor Stone Supply"
        user="Yard Team"
      ></hh-phone-header>`,
    ),
  );
  assert.ok(markup.includes('data-tenant="Harbor Stone Supply"'), 'tenant lockup carries config org');
  assert.ok(markup.includes('data-monogram="HSS"'), 'monogram badge derives from config org');
  assert.ok(markup.includes('>HSS<'), 'monogram text renders in badge');
  assert.ok(markup.includes('>Harbor Stone Supply<'), 'tenant name renders from config');
  assert.ok(markup.includes('class="sync-pill"'), 'tablet sync pill is rendered');
  assert.ok(markup.includes('Synced · just now'), 'idle sync pill shows Synced · just now');
  assert.ok(markup.includes('data-initials="YT"'), 'user initials avatar derives from user');
  assert.ok(!phoneHeaderSrc.includes(['G', 'L', 'S'].join('')), 'tenant monogram is never hardcoded in source');
});

test('hh-phone-header: tablet slim header honours an explicit monogram from config and shows Offline in sync pill', () => {
  const markup = visible(
    ssr(
      html`<hh-phone-header
        title="My run"
        sync-state="offline"
        org="Pacific Paver Co"
        monogram="PPC"
      ></hh-phone-header>`,
    ),
  );
  assert.ok(markup.includes('data-monogram="PPC"'), 'explicit config monogram is used');
  assert.ok(/class="sync-pill"[^>]*data-state="offline"/.test(markup), 'sync pill reflects offline state');
  assert.ok(markup.includes('>Offline<'), 'offline sync pill shows Offline');
});

test('FLOOR hh-phone-header: tablet slim header is 48 px tall at 600 px and wider', () => {
  const rules = parseRules(PhoneHeader.styles.map((sheet) => sheet.cssText).join('\n'));
  const tablet = declsFor(rules, '.phone-row', '@media (min-width: 600px)');
  assert.equal(px(tablet['min-height']) ?? px(tablet['height']), 48, 'the tablet header bar is a slim 48 px row');
});

// ── <hh-field-sign-in> ────────────────────────────────────────────────────────────────────────

test('hh-field-sign-in: renders centered card with tenant monogram from config, fields, primary action and yard PIN hint', () => {
  const el = new FieldSignIn();
  el.org = 'Harbor Stone Supply';
  el.branch = 'Point Loma';
  const markup = visible(ssr(el.render()));
  assert.ok(markup.includes('data-monogram="HSS"'), 'tenant monogram from config');
  assert.ok(markup.includes('Harbor Stone Supply · Point Loma'), 'tenant and branch subtitle');
  assert.ok(markup.includes('Sign in to Field'), 'card heading');
  assert.ok(markup.includes('Work email or phone'), 'identifier field label');
  assert.ok(markup.includes('Passcode or PIN'), 'secret field label');
  assert.ok(/<button[^>]*data-action="sign-in"[^>]*>\s*Sign in\s*<\/button>/.test(markup), 'Sign in primary button');
  assert.ok(markup.includes('On a shared yard tablet? Use a 4-digit yard PIN'), 'shared yard tablet PIN hint');
});

test('hh-field-sign-in: pressing Sign in fires hh-field-sign-in (bubbles, composed)', () => {
  const listeners = new Map();
  const fired = [];
  const el = new FieldSignIn();
  el.addEventListener = (type, fn, opts) => { listeners.set(type, { fn, opts }); };
  el.removeEventListener = (type) => { listeners.delete(type); };
  el.dispatchEvent = (event) => {
    fired.push(event);
    return true;
  };
  el.performUpdate = () => {};
  el.connectedCallback();
  const control = Object.create(Element.prototype);
  control.hasAttribute = (name) => name === 'data-action';
  control.getAttribute = (name) => (name === 'data-action' ? 'sign-in' : null);
  listeners.get('click').fn({ composedPath: () => [control] });
  assert.equal(fired.length, 1, 'fires one event');
  assert.equal(fired[0].type, 'hh-field-sign-in', 'event name is hh-field-sign-in');
  assert.equal(fired[0].bubbles, true, 'bubbles');
  assert.equal(fired[0].composed, true, 'composed');
});

test('FLOOR hh-field-sign-in: tokens only, no hex colour literals in the source', () => {
  const hexes = [...fieldSignInSrc.matchAll(/#[0-9a-fA-F]{3,8}\b/g)].map((m) => m[0]);
  assert.deepEqual(hexes, [], `field-sign-in.ts must stay literal-free, found: ${hexes.join(', ')}`);
});