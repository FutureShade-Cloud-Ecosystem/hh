// The header band's contract, over the same server render the design system's
// element tests use: the element instance is never connected, so the template
// the render() returns is the only thing that renders. What is asserted here
// is the band's own shape (row 1520, the r4 shell frame): one simple band
// holding the mark and org, the app and surface selector and its panel, the
// pin control, the sync state chip, notifications, and the account chip. The
// click contract rides data-action names with no listener spelling in the
// markup at all, and the floors are source scans in the house split.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { renderKit } from './lib/render.mjs';
import { ShellHeader } from '../src/shell-header.ts';
import { parseRules, declsFor, px } from './lib/css.mjs';

const headerSrc = readFileSync(
  fileURLToPath(new URL('../src/shell-header.ts', import.meta.url)),
  'utf8',
);

/** The rendered markup with markers stripped, the text a person reads. */
const visible = (markup) => markup.replace(/<!--.*?-->/g, '');

/** The button markup carrying an action name, if the band rendered one. */
const actionButton = (markup, action) => {
  const m = markup.match(new RegExp(`<button[^>]*data-action="${action}"[^>]*>[\\s\\S]*?</button>`));
  return m === null ? null : m[0];
};

const GROUPS = [
  {
    app: { id: 'yard-pick', name: 'Yard Pick and Receive', roles: ['yard_team'] },
    surfaces: [
      { id: 'pick-queue', label: 'Pick queue' },
      { id: 'receiving', label: 'Receiving' },
    ],
  },
  {
    app: { id: 'drive-run', name: 'HH Drive', roles: ['driver'] },
    surfaces: [{ id: 'my-run', label: 'My run' }],
  },
];

const BASE = {
  org: 'Gable Landscape Supply',
  user: 'Yard Team',
  breadcrumb: 'Yard Pick and Receive › Pick queue',
  groups: GROUPS,
  currentKey: 'yard-pick/pick-queue',
  pinned: false,
  syncState: 'idle',
};

// ── the band: mark, org, selector, chip, notifications, pin, account ─────────────────────────────

test('the compact band leads with the dealer: its initial as the mark chip, its name beside it, none of our mark', () => {
  const markup = visible(renderKit(ShellHeader, BASE));
  assert.ok(markup.includes('<span class="mark" aria-hidden="true">G</span>'), 'the mark chip is the dealer initial');
  assert.ok(!markup.includes('viewBox="0 0 3000 3000"'), 'no HH mark on a dealer screen');
  assert.ok(markup.includes('Gable Landscape Supply'), 'the org word is present');
});

test('the compact band with no dealer known draws no mark chip at all', () => {
  const markup = visible(renderKit(ShellHeader, { ...BASE, org: '' }));
  assert.ok(!markup.includes('class="mark"'), 'no chip before the dealer is known');
  assert.ok(!markup.includes('viewBox="0 0 3000 3000"'), 'no HH mark either');
});

test('the selector shows where the person is, and the quiet default when it knows nothing', () => {
  const markup = visible(renderKit(ShellHeader, BASE));
  assert.ok(markup.includes('Yard Pick and Receive › Pick queue'), 'the breadcrumb words are the control');
  const quiet = visible(renderKit(ShellHeader, { ...BASE, breadcrumb: '' }));
  assert.ok(quiet.includes('>Surfaces<'), 'the quiet default word is Surfaces');
});

test('the selector control is a raised menu control: haspopup, collapsed, no bound listeners', () => {
  const markup = renderKit(ShellHeader, BASE);
  const control = actionButton(markup, 'selector');
  assert.ok(control !== null, 'the selector control is present');
  assert.ok(control.includes('aria-haspopup="menu"'), 'the panel is a menu');
  assert.ok(control.includes('aria-expanded="false"'), 'it starts closed');
  assert.ok(!/@click=|\.open=|@keydown=/.test(markup), 'no listener spelling anywhere in the markup');
});

test('open, the panel is the whole map: every app the roles hold, its surfaces, the current marked', () => {
  const markup = visible(renderKit(ShellHeader, { ...BASE, selectorOpen: true }));
  assert.ok(markup.includes('role="menu"'), 'the panel is a menu');
  assert.ok(markup.includes('Yard Pick and Receive'), 'the first group names its app');
  assert.ok(markup.includes('Pick queue'), 'the surface rows are there');
  assert.ok(markup.includes('Receiving'), 'and the second surface');
  assert.ok(markup.includes('My run'), 'the other app\u2019s surfaces are there');
  const current = markup.match(/<button[^>]*data-key="yard-pick\/pick-queue"[^>]*>[\s\S]*?<\/button>/);
  assert.ok(current !== null, 'the current surface is a row');
  assert.ok(current[0].includes('aria-current="true"'), 'the current row is marked');
  assert.ok(current[0].includes('here'), 'the current row says here in words');
  const other = markup.match(/<button[^>]*data-key="yard-pick\/receiving"[^>]*>[\s\S]*?<\/button>/);
  assert.ok(other !== null && !other[0].includes('aria-current="true"'), 'the other rows are not marked');
});

test('every surface row carries its key and routes through one action name', () => {
  const markup = renderKit(ShellHeader, { ...BASE, selectorOpen: true });
  for (const key of ['yard-pick/pick-queue', 'yard-pick/receiving', 'drive-run/my-run']) {
    assert.ok(markup.includes(`data-key="${key}"`), `the row for ${key} carries its key`);
  }
  const rows = markup.match(/data-action="select"/g) ?? [];
  assert.equal(rows.length, 3, 'every row routes through the one select action');
});

test('a front door only app offers one row named by the app', () => {
  const markup = visible(
    renderKit(ShellHeader, {
      ...BASE,
      selectorOpen: true,
      groups: [{ app: { id: 'house-messages', name: 'House Messages', roles: ['yard_team'] }, surfaces: [] }],
      currentKey: '',
    }),
  );
  assert.ok(markup.includes('House Messages'), 'the app row is present');
  assert.ok(markup.includes('data-key="house-messages"'), 'the front door pins under the bare app key');
});

// ── the pin control ──────────────────────────────────────────────────────────────────────────────

test('the pin control is a pressed toggle that says what it does once', () => {
  const unpinned = actionButton(renderKit(ShellHeader, BASE), 'pin');
  assert.ok(unpinned !== null, 'the pin control is present');
  assert.ok(unpinned.includes('aria-pressed="false"'), 'unpinned is pressed false');
  assert.ok(unpinned.includes('title="Pin this surface"'), 'the icon-only control carries its tooltip');
  assert.ok(unpinned.includes('aria-label="Pin this surface"'), 'and the name says the same words');
  const pinned = actionButton(renderKit(ShellHeader, { ...BASE, pinned: true }), 'pin');
  assert.ok(pinned.includes('aria-pressed="true"'), 'pinned is pressed true');
});

test('with no current surface the pin control stands down, disabled as a token swap', () => {
  const markup = renderKit(ShellHeader, { ...BASE, currentKey: '' });
  const pin = actionButton(markup, 'pin');
  assert.ok(pin !== null && pin.includes('disabled'), 'the pin is disabled without a current surface');
});

// ── the sync state chip ──────────────────────────────────────────────────────────────────────────

test('the chip shows the machine\u2019s state in words, with its kind on the element', () => {
  const markup = renderKit(ShellHeader, BASE);
  assert.ok(markup.includes('data-state="idle"'), 'the chip names its state');
  assert.ok(markup.includes('Synced'), 'the word is the model\u2019s');
  const conflict = renderKit(ShellHeader, { ...BASE, syncState: 'conflict', cards: 2 });
  assert.ok(conflict.includes('data-state="conflict"'), 'the conflict state lands');
  assert.ok(visible(conflict).includes('2 to review'), 'the count is the word');
});

test('the pushing chip names the writes waiting behind it', () => {
  const markup = visible(renderKit(ShellHeader, { ...BASE, syncState: 'pushing', waiting: 3 }));
  assert.ok(markup.includes('3 in the outbox'), 'the note carries the outbox depth');
});

test('two attempts on a stuck write alarm the chip; one attempt never does', () => {
  const alarmed = renderKit(ShellHeader, { ...BASE, syncState: 'pushing', waiting: 4, attempts: 2 });
  assert.ok(alarmed.includes('data-alarm="true"'), 'the alarm rides a data mark for the sheet');
  assert.ok(visible(alarmed).includes('retrying'), 'the word carries the alarm beside the colour');
  const calm = renderKit(ShellHeader, { ...BASE, syncState: 'pushing', waiting: 4, attempts: 1 });
  assert.ok(!calm.includes('data-alarm="true"'), 'one attempt is not an alarm');
});

// ── notifications and the account chip ───────────────────────────────────────────────────────────

test('notifications carry their count in the accessible name, and the badge shows the number', () => {
  const markup = renderKit(ShellHeader, { ...BASE, notifications: 3 });
  const bell = actionButton(markup, 'notifications');
  assert.ok(bell !== null, 'the bell is present');
  assert.ok(bell.includes('aria-label="Notifications, 3 unread"'), 'the name carries the count');
  assert.ok(bell.includes('>3<'), 'the badge shows the number');
  const quiet = actionButton(renderKit(ShellHeader, BASE), 'notifications');
  assert.ok(quiet !== null && quiet.includes('aria-label="Notifications"'), 'no count, no count words');
});

test('the account chip reads the role label words and shows the initials', () => {
  const markup = visible(renderKit(ShellHeader, BASE));
  assert.ok(markup.includes('Yard Team'), 'the role label is the chip\u2019s words');
  assert.ok(markup.includes('data-initials="YT"'), 'the initials ride a data mark');
  const quiet = visible(renderKit(ShellHeader, { ...BASE, user: '' }));
  assert.ok(quiet.includes('>Account<'), 'an empty label falls back to the word Account');
});

// ── FLOORS, as source scans in the house split ───────────────────────────────────────────────────

test('FLOOR: every title in the band is matched, character for character, by an aria-label', () => {
  const titles = [...headerSrc.matchAll(/\btitle="([^"]*)"/g)].map((m) => m[1]);
  assert.ok(titles.length > 0, 'the icon-only controls still need their tooltips');
  for (const t of titles) {
    assert.ok(
      headerSrc.includes(`aria-label="${t}"`),
      `the title "${t}" has no matching aria-label: one control, one name`,
    );
  }
});

test('an unknown sync state throws from willUpdate: the chip is never painted quietly wrong', () => {
  const el = new ShellHeader();
  el.syncState = 'warp';
  assert.throws(() => el.willUpdate(new Map([['syncState', undefined]])), /unknown sync state "warp"/);
  // A state the machine has passes, and an update that leaves syncState alone is not checked.
  el.syncState = 'idle';
  assert.doesNotThrow(() => el.willUpdate(new Map([['syncState', undefined]])));
  el.syncState = 'warp';
  assert.doesNotThrow(() => el.willUpdate(new Map([['breadcrumb', undefined]])));
});

test('FLOOR: the band is drawn in tokens only, no hex colour literals anywhere in the source', () => {
  const hexes = [...headerSrc.matchAll(/#[0-9a-fA-F]{3,8}\b/g)].map((m) => m[0]);
  assert.deepEqual(hexes, [], `shell-header.ts must stay literal-free, found: ${hexes.join(', ')}`);
});

const headerRules = parseRules(ShellHeader.styles.map((sheet) => sheet.cssText).join('\n'));
const NARROW = '@container (max-width: 834px)';

test('FLOOR: phone and tablet targets are at least 44 px, each control named by its selector', () => {
  for (const selector of ['.selector', '.icon-btn', '.account', '.mi']) {
    const narrow = declsFor(headerRules, selector, NARROW);
    assert.ok(px(narrow['min-height']) >= 44, `${selector} is 44 px tall in the narrow container`);
  }
  // The bell and the pin are fixed 34 px squares at rest, so the narrow container must lift the width too.
  const base = declsFor(headerRules, '.icon-btn');
  const narrow = declsFor(headerRules, '.icon-btn', NARROW);
  const widest = Math.max(px(base['width']) ?? 0, px(narrow['width']) ?? 0, px(narrow['min-width']) ?? 0);
  assert.ok(widest >= 44, `.icon-btn is 44 px wide in the narrow container (found ${widest})`);
  assert.ok(px(narrow['min-width']) >= 44 || px(narrow['width']) >= 44, 'the lift is on the narrow container, not the base');
});

test('FLOOR: the phone band drops the sync chip and the org word, the r4 frame\u2019s own rule', () => {
  assert.ok(headerSrc.includes('max-width: 500px'), 'the sub 500px band hides the chip');
});

// ── the TanyAI button (PANEL-API r1 section 5) ───────────────────────────────────────────────────

test('a host that mounts the panel sets assist and the band carries one labelled Ask TanyAI button', () => {
  const markup = visible(renderKit(ShellHeader, { ...BASE, assist: true }));
  const button = actionButton(markup, 'assist');
  assert.ok(button !== null, 'the assist button renders');
  assert.match(button, /aria-label="Ask TanyAI"/);
  assert.equal((markup.match(/data-action="assist"/g) ?? []).length, 1);
  assert.match(headerSrc, /hh-shell-assist/, 'the click emits the one event the shell listens for');
});

test('assist is off by default: a host with no panel gets no dead Ask TanyAI button', () => {
  const markup = visible(renderKit(ShellHeader, BASE));
  assert.equal(actionButton(markup, 'assist'), null);
  assert.equal((markup.match(/data-action="assist"/g) ?? []).length, 0);
  assert.equal(new ShellHeader().assist, false);
});

test('a pinned pin in its disabled state carries a non-brand ring and filled pin head so pinned reads without brand colour', () => {
  const disabledPinned = declsFor(headerRules, ".icon-btn[aria-pressed='true']:disabled");
  assert.equal(
    disabledPinned['box-shadow'],
    'inset 0 0 0 2px var(--hh-disabled-ink)',
    'the disabled pinned button carries a 2px inset ring in --hh-disabled-ink',
  );
  const head = declsFor(headerRules, ".icon-btn[aria-pressed='true'] svg path:last-child");
  assert.equal(head['fill'], 'currentColor', 'the pinned icon fills its head in currentColor');
});

// ── UX-145: nobody signed in, so the band claims no sync and no account ──────────────────────────

test('signedIn false: no sync chip renders and the menu button carries no Account', () => {
  const markup = visible(renderKit(ShellHeader, { ...BASE, user: '', signedIn: false, variant: 'band' }));
  assert.doesNotMatch(markup, /class="chip"/);
  assert.doesNotMatch(markup, /Synced/);
  const button = actionButton(markup, 'account');
  assert.notEqual(button, null, 'the menu stays reachable for Theme and Feedback');
  assert.doesNotMatch(button, /Account/);
  assert.doesNotMatch(button, /class="avatar"/);
  assert.match(button, /class="menu-glyph"/);
  assert.match(button, /aria-label="Menu"/);
  assert.match(button, /aria-haspopup="menu"/);
});

test('signedIn defaults to true: the chip and the account button are unchanged for every other user of the band', () => {
  const markup = visible(renderKit(ShellHeader, { ...BASE, variant: 'band' }));
  assert.match(markup, /class="chip"/);
  assert.match(actionButton(markup, 'account'), /Yard Team account/);
});
