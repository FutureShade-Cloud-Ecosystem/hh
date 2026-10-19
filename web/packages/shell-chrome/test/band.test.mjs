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
  const markAt = band.indexOf('class="mark"');
  const orgAt = band.indexOf('Gable Landscape Supply');
  const selectorAt = band.indexOf('class="selector"');
  assert.ok(markAt > bandAt && markAt < selectorAt, 'the GABLE lockup leads the band, left of the selector');
  assert.ok(orgAt > markAt && orgAt < selectorAt, 'the org word rides the lockup');
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

// ── both variants stand: the compact header the fixture pages host serves is untouched ───────────

test('the compact variant keeps the Demo badge in the brand and the selector at the left', () => {
  const compact = visible(renderKit(ShellHeader, { ...BAND, variant: 'compact' }));
  assert.ok(compact.includes('data-variant="compact"'), 'the compact posture is in the markup');
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
