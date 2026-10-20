// GAPS-r2 row G1: the board's white band and the rail's pin menu. The band
// variant puts the GABLE lockup at the left, centres the "App > Surface"
// selector, and runs DEMO then Synced in the chip run; the compact variant
// the fixture pages host serves is untouched. The rail gains the pin menu
// the board names ("Pin as icons", "Pin wide"), revealing on hover or focus
// only, persisting at the rail's container state through the injected seam.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { renderKit } from './lib/render.mjs';
import { ShellHeader } from '../src/shell-header.ts';
import { ShellRail } from '../src/shell-rail.ts';
import { parseRules, declsFor } from './lib/css.mjs';
import { RAIL_PIN_KEY, railPinFromStored, railPinToStored } from '../src/shell-state.ts';
import { framedBandAccount, framedBandBreadcrumb } from '../src/framed-band.ts';

const visible = (markup) => markup.replace(/<!--.*?-->/g, '').replace(/&gt;/g, '>').replace(/&lt;/g, '<');

const BAND = {
  org: 'Gable Landscape Supply',
  demo: true,
  user: 'Yard Team',
  breadcrumb: 'Counter Till > New sale',
  groups: [],
  currentKey: 'counter-till',
  syncState: 'idle',
  notifications: 3,
};

const PINS = [
  { key: 'counter-till', label: 'Counter Till', glyph: 'CT' },
  { key: 'quote-desk', label: 'Quote Desk', glyph: 'QD' },
];

const headerRules = parseRules(ShellHeader.styles.map((sheet) => sheet.cssText).join('\n'));
const railRules = parseRules(ShellRail.styles.map((sheet) => sheet.cssText).join('\n'));

test('the shell band is on the left lockup, the selector is centred, the rail pin menu has Pin as icons and Pin wide', () => {
  const band = visible(renderKit(ShellHeader, { ...BAND, variant: 'band' }));
  const bandAt = band.indexOf('class="band"');
  const orgAt = band.indexOf('Gable Landscape Supply');
  const selectorAt = band.indexOf('class="selector"');
  assert.equal(band.includes('class="mark"'), false, 'the band never carries the HH mark: the lockup is the dealer word alone');
  assert.ok(band.includes('class="lockup"'), 'the dealer word renders as the lockup');
  assert.ok(orgAt > bandAt && orgAt < selectorAt, 'the GABLE lockup leads the band, left of the selector');
  assert.ok(band.includes('Counter Till > New sale'), 'the selector names the app and the surface');
  const bandRule = declsFor(headerRules, ".band[data-variant='band']");
  assert.ok(
    (bandRule['grid-template-columns'] ?? '').replace(/\s+/g, ' ').includes('1fr auto 1fr'),
    'the band holds the selector in a centred middle column',
  );
  const selectorRule = declsFor(headerRules, ".band[data-variant='band'] .selector");
  assert.equal(selectorRule['justify-self'], 'center', 'the selector sits centred, not left');
  const demoAt = band.indexOf('>DEMO<');
  const syncedAt = band.indexOf('>Synced<');
  assert.ok(demoAt > selectorAt && demoAt < syncedAt, 'DEMO leads Synced in the chip run, in the order the board names');
  const rail = visible(renderKit(ShellRail, { pins: PINS }));
  assert.ok(rail.includes('Pin as icons'), 'the pin menu offers Pin as icons, the board\u2019s words');
  assert.ok(rail.includes('Pin wide'), 'the pin menu offers Pin wide, the board\u2019s words');
});

test('the band DEMO marker is one outlined chip, the board\u2019s style', () => {
  const demoRule = declsFor(headerRules, ".band[data-variant='band'] .demo");
  assert.equal(demoRule['background'], 'transparent', 'outlined, never a second filled strip');
  assert.ok((demoRule['border'] ?? '').startsWith('1px solid'), 'the outline is the marker');
});

test('the compact variant wears the dealer initial, the Demo badge in the brand, and the selector at the left', () => {
  const compact = visible(renderKit(ShellHeader, { ...BAND, variant: 'compact' }));
  assert.ok(compact.includes('data-variant="compact"'), 'the compact posture is in the markup');
  assert.ok(compact.includes('class="mark"'), 'the compact band keeps its mark chip, now the dealer initial');
  assert.ok(!compact.includes('viewBox="0 0 3000 3000"'), 'and never the HH mark');
  assert.ok(compact.includes('>Demo<'), 'the compact badge words are unchanged');
  assert.ok(!compact.includes('>DEMO<'), 'no DEMO chip in the compact run');
  const brandAt = compact.indexOf('class="brand"');
  const demoAt = compact.indexOf('>Demo<');
  const selectorAt = compact.indexOf('class="selector"');
  assert.ok(demoAt > brandAt && demoAt < selectorAt, 'the badge still rides the brand, before the selector');
  assert.equal(declsFor(headerRules, '.band')['display'], 'flex', 'the compact band keeps its flex row');
});

test('an unknown band variant refuses loudly, never a quietly wrong band', () => {
  const el = new ShellHeader();
  el.variant = 'split';
  assert.throws(() => el.willUpdate(new Map([['variant', undefined]])), /unknown variant "split"/);
  el.variant = 'band';
  assert.doesNotThrow(() => el.willUpdate(new Map([['variant', undefined]])));
  el.variant = 'compact';
  assert.doesNotThrow(() => el.willUpdate(new Map([['variant', undefined]])));
});

// ── the rail pin menu: reveal on hover or focus only, no animated bars ───────────────────────────

test('the pin menu hides at rest and reveals on hover and on focus, with no transition', () => {
  const atRest = declsFor(railRules, '.pin-menu');
  assert.equal(atRest['display'], 'none', 'the pin menu is hidden until the rail is hovered or focused');
  assert.equal(atRest['transition'], undefined, 'no animated reveal: the no extra bars check applies here too');
  const hover = declsFor(railRules, '.rail:hover .pin-menu');
  const focus = declsFor(railRules, '.rail:focus-within .pin-menu');
  assert.equal(hover['display'], 'flex', 'hover reveals the pin menu');
  assert.equal(focus['display'], 'flex', 'focus reveals it too');
});

test('the pin menu paints outside the rail and the pointer crossing never drops the hover', () => {
  const rail = declsFor(railRules, '.rail');
  assert.equal(rail['overflow'], 'visible', 'the rail never clips the menu (the P1-1 clipping defect)');
  assert.equal(rail['overflow-y'], undefined, 'no vertical clip either');
  const rows = declsFor(railRules, '.rows');
  assert.equal(rows['overflow-y'], 'auto', 'the scroll lives on the rows wrapper, so a long pin list still scrolls');
  const atRestMenu = declsFor(railRules, '.pin-menu');
  assert.equal(atRestMenu['position'], 'fixed', 'a fixed popover, anchored from the rail rect, never clipped by an ancestor');
  assert.ok((atRestMenu['left'] ?? '').includes('--pin-left'), 'the rail sets the left from its own rect');
  assert.ok((atRestMenu['top'] ?? '').includes('--pin-top'), 'the rail sets the top from its own rect');
  const bridge = declsFor(railRules, '.pin-menu::before');
  assert.equal(bridge['content'], "''", 'the bridge fills the gap between the rail and the menu');
  assert.equal(bridge['right'], '100%', 'the bridge starts at the menu\u2019s left edge and reaches back to the rail');
  const menuHover = declsFor(railRules, '.pin-menu:hover');
  assert.equal(menuHover['display'], 'flex', 'the menu\u2019s own hover keeps it open across the bridge');
});

test('the unpinned collapsed rail hides its icons until hover or focus; pinned icons stay', () => {
  const hidden = declsFor(railRules, ".rail[data-pin='off'][data-collapsed='true'] .row");
  assert.equal(hidden['display'], 'none', 'the icons column waits for the pointer or the key');
  const revealedHover = declsFor(railRules, ".rail[data-pin='off'][data-collapsed='true']:hover .row");
  const revealedFocus = declsFor(railRules, ".rail[data-pin='off'][data-collapsed='true']:focus-within .row");
  assert.equal(revealedHover['display'], 'flex', 'hover reveals the icon column');
  assert.equal(revealedFocus['display'], 'flex', 'focus reveals it too');
});

// ── the pin mode: markup posture and the container state ─────────────────────────────────────────

test('Pin as icons renders the icons column, Pin wide the wider rail body, each marked pressed', () => {
  const icons = renderKit(ShellRail, { pins: PINS, pin: 'icons' });
  assert.ok(icons.includes('data-pin="icons"'), 'the icons mode is in the markup');
  assert.ok(icons.includes('data-collapsed="true"'), 'the icons mode is the icons column');
  const iconsItem = /<button[^>]*data-mode="icons"[^>]*>/.exec(icons);
  assert.ok(iconsItem !== null && iconsItem[0].includes('aria-pressed="true"'), 'Pin as icons says it is on');
  const wide = renderKit(ShellRail, { pins: PINS, pin: 'wide' });
  assert.ok(wide.includes('data-pin="wide"'), 'the wide mode is in the markup');
  assert.ok(wide.includes('data-collapsed="false"'), 'the wide mode is the wider rail body');
  const wideItem = /<button[^>]*data-mode="wide"[^>]*>/.exec(wide);
  assert.ok(wideItem !== null && wideItem[0].includes('aria-pressed="true"'), 'Pin wide says it is on');
  const off = renderKit(ShellRail, { pins: PINS, pin: 'off' });
  assert.ok(off.includes('data-pin="off"'), 'off is the plain posture');
});

test('the pin mode resolves through the seam: silence means off, a stored mode wins, an attribute stands over storage', () => {
  const stored = new ShellRail();
  stored.storage = { read: (key) => (key === RAIL_PIN_KEY ? 'wide' : null), write: () => {} };
  stored.resolvePosture();
  assert.equal(stored.pin, 'wide', 'the stored pin mode wins');
  const silent = new ShellRail();
  silent.storage = { read: () => null, write: () => {} };
  silent.resolvePosture();
  assert.equal(silent.pin, 'off', 'silence means off');
  const explicit = new ShellRail();
  explicit.storage = { read: () => 'wide', write: () => {} };
  // A browser upgrade reads the attribute into the property before the rail
  // resolves; the pair stands for it here.
  explicit.pin = 'icons';
  explicit.setAttribute('pin', 'icons');
  explicit.resolvePosture();
  assert.equal(explicit.pin, 'icons', 'what the shell asked for, the shell gets');
});

test('the stored pin mode degrades to off, never an error', () => {
  assert.equal(railPinFromStored(null), 'off');
  assert.equal(railPinFromStored(''), 'off');
  assert.equal(railPinFromStored('icons'), 'icons');
  assert.equal(railPinFromStored('wide'), 'wide');
  assert.equal(railPinFromStored('spread'), 'off');
  assert.equal(railPinToStored('off'), 'off');
});

// ── the framed band's words, the composition every desk app hands the band ─

test('the framed band crumb reads App > Surface, and the app alone at its front door', () => {
  assert.equal(framedBandBreadcrumb('Counter Till', 'New sale'), 'Counter Till > New sale');
  assert.equal(framedBandBreadcrumb('Quote Desk', 'New quote'), 'Quote Desk > New quote');
  assert.equal(framedBandBreadcrumb('Manager Home', ''), 'Manager Home');
});

test('the account chip reads the persona name with the role label, and degrades honestly', () => {
  assert.equal(framedBandAccount('Dana', 'General Manager'), 'Dana, General Manager');
  assert.equal(framedBandAccount('', 'Yard Team'), 'Yard Team');
  assert.equal(framedBandAccount('Dana', ''), 'Dana');
  assert.equal(framedBandAccount('Yard Team', 'Yard Team'), 'Yard Team', 'one word, never doubled');
});

// ── round 1 on PR 503: the account chip's menu and the theme the band applies ─

test('the band account chip opens the menu the app hands it, and says so with haspopup and expanded', () => {
  const closed = renderKit(ShellHeader, { ...BAND, variant: 'band' });
  const chip = /<button[^>]*data-action="account"[^>]*>/.exec(closed)?.[0] ?? '';
  assert.ok(chip.includes('aria-haspopup="menu"'), 'the chip names its menu');
  assert.ok(chip.includes('aria-expanded="false"'), 'closed at rest');
  assert.ok(!closed.includes('class="account-menu"'), 'no popover at rest');
  const open = renderKit(ShellHeader, { ...BAND, variant: 'band', accountOpen: true });
  assert.ok(open.includes('class="account-menu"'), 'the popover renders when open');
  const openChip = /<button[^>]*data-action="account"[^>]*>/.exec(open)?.[0] ?? '';
  assert.ok(openChip.includes('aria-expanded="true"'), 'open says open');
  const compact = renderKit(ShellHeader, { ...BAND, accountOpen: true });
  assert.ok(!compact.includes('class="account-menu"'), 'the compact variant keeps the shell-driven menu');
});

test('the band applies the theme to the document root, the contract the app-local header carried', () => {
  const calls = [];
  const root = {
    setAttribute: (name, value) => calls.push(['set', name, value]),
    removeAttribute: (name) => calls.push(['rm', name]),
  };
  const original = globalThis.document;
  globalThis.document = { documentElement: root };
  try {
    const el = new ShellHeader();
    el.theme = 'light';
    el.willUpdate(new Map([['theme', undefined]]));
    assert.deepEqual(calls, [['set', 'data-theme', 'light']], 'light sets the attribute');
    el.theme = 'dark';
    el.willUpdate(new Map([['theme', undefined]]));
    assert.deepEqual(calls[1], ['rm', 'data-theme'], 'dark removes it');
  } finally {
    globalThis.document = original;
  }
});

test('at phone width the band stacks the selector under the lockup and the chip run, and never widens the page', () => {
  const css = ShellHeader.styles.map((sheet) => sheet.cssText).join('\n');
  assert.ok(css.includes('@media (max-width: 640px)'), 'a phone width rule is in the sheet');
  const phone = css.slice(css.indexOf('@media (max-width: 640px)'));
  assert.ok(phone.includes('grid-template-columns: minmax(0, 1fr) auto'), 'two columns: the lockup (truncating) and the run');
  assert.ok(phone.includes('grid-column: 1 / -1'), 'the selector takes the full second row');
  assert.ok(phone.includes('text-overflow: ellipsis'), 'the lockup truncates instead of pushing the run out');
});

test('the account string drops the demo suffix of a persona name and caps a long role', () => {
  assert.equal(framedBandAccount('Sales (demo)', 'Sales'), 'Sales', 'the suffix goes, and the equal word is said once');
  assert.equal(framedBandAccount('Dealer admin (demo)', 'General Manager'), 'Dealer admin, General Manager');
  assert.equal(framedBandAccount('Dana', 'Chief Financial Controller of the Yard'), 'Dana, Chief Financial Cont\u2026', 'a role over 24 characters stops at 20 and an ellipsis');
  assert.equal(framedBandAccount('(demo)', 'Sales'), 'Sales', 'a name that was only the suffix is no name');
  assert.equal(framedBandAccount('Procurement manager (demo)', 'Procurement manager (demo)'), 'Procurement manager', 'the same persona word in both, said once');
});

test('every account menu button wears one style, whatever class the app gave it', () => {
  const rule = declsFor(headerRules, '.account-menu button');
  assert.equal(rule['background'], 'transparent', 'one outlined face, never the grey filled default');
  assert.ok((rule['border'] ?? '').startsWith('1px solid'), 'the outline');
  assert.equal(rule['text-align'], 'left', 'one alignment');
  assert.ok((rule['color'] ?? '').includes('--hh-text'), 'one ink');
});
