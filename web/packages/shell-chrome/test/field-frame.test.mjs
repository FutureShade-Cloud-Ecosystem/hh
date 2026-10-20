// The Field frame's contract (GAPS-r2 G2): the pure words (the tabs a role holds, the surface chip), and the server
// render of the element, which is the only thing that renders when the instance is never connected. The browser legs
// in the three Field apps hold the rest (the tab bar's breakpoint, the menu keyboard, the 44 px targets).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { renderKit } from './lib/render.mjs';
import { FieldFrame, fieldBreadcrumb, fieldRoleWords, fieldTabsForRole, FIELD_TAB_HREFS } from '../src/field-frame.ts';

const src = readFileSync(fileURLToPath(new URL('../src/field-frame.ts', import.meta.url)), 'utf8');

const TABS = fieldTabsForRole('yard_team');
const props = (over = {}) => ({ org: 'Gable Landscape Supply', appName: 'Drive', surface: 'My run', roleLabel: 'Driver', signedIn: true, demo: true, current: 'drive', tabs: fieldTabsForRole('driver'), ...over });

test('a role holds only the Field tabs the manifest gives it, and always Messages', () => {
  assert.deepEqual(fieldTabsForRole('yard_team').map((t) => t.id), ['pick', 'messages']);
  assert.deepEqual(fieldTabsForRole('driver').map((t) => t.id), ['drive', 'messages']);
  assert.deepEqual(fieldTabsForRole('yard_manager').map((t) => t.id), ['pick', 'drive', 'messages']);
  assert.deepEqual(fieldTabsForRole('inside_sales').map((t) => t.id), ['messages']);
  assert.deepEqual(fieldTabsForRole('').map((t) => t.id), ['messages']);
  assert.equal(TABS[0].href, FIELD_TAB_HREFS.pick);
});

test('the surface chip names the app, then the surface; the role words drop the demo suffix', () => {
  assert.equal(fieldBreadcrumb('Drive', 'My run'), 'Drive > My run');
  assert.equal(fieldBreadcrumb('Drive', '  '), 'Drive');
  assert.equal(fieldRoleWords('Yard Team (demo)'), 'Yard Team');
});

test('signed in, the frame renders the lockup from the tenant, a DEMO chip, the sync word, the account chip and the tab bar', () => {
  const markup = renderKit(FieldFrame, props());
  assert.match(markup, /Gable Landscape Supply/);
  assert.match(markup, /class="demo"[^>]*>DEMO</);
  assert.match(markup, /Drive &gt; My run|Drive > My run/);
  assert.match(markup, /Synced/);
  assert.match(markup, /aria-label="Account, Driver"/);
  assert.match(markup, /<nav class="tabbar" aria-label="Field apps"/);
  assert.match(markup, /aria-current="page"[^>]*data-tab="drive"/);
  assert.equal((markup.match(/data-tab=/g) ?? []).length, 3);
});

test('with no dealer name from the config the lockup shows no name, never an HH mark or a guess', () => {
  const markup = renderKit(FieldFrame, props({ org: '' }));
  assert.doesNotMatch(markup, /lockup-name/);
  assert.doesNotMatch(markup, /<svg[^>]*viewBox="0 0 3000 3000"/);
});

test('signed out, the frame draws no tab bar, no selector and no sync chip', () => {
  const markup = renderKit(FieldFrame, props({ signedIn: false, tabs: [] }));
  assert.doesNotMatch(markup, /tabbar/);
  assert.doesNotMatch(markup, /data-selector/);
  assert.doesNotMatch(markup, /data-sync/);
});

test('the bell draws only where the app names a door for it, and says the count in words', () => {
  assert.doesNotMatch(renderKit(FieldFrame, props()), /Alerts/);
  const bell = renderKit(FieldFrame, props({ bellAction: 'open-changes', bellCount: 2 }));
  assert.match(bell, /data-action="open-changes"/);
  assert.match(bell, /aria-label="Alerts, 2 waiting"/);
});

test('the source holds the floors: tokens only, 44 px targets, safe area, reduced motion, no listener spelling', () => {
  assert.doesNotMatch(src, /(?<![&\w])#[0-9a-fA-F]{6}\b/, 'no raw hex colour');
  assert.match(src, /--hh-h-input/);
  assert.match(src, /--hh-safe-bottom/);
  assert.match(src, /prefers-reduced-motion/);
  assert.doesNotMatch(src, /\b(10|7)px\b/);
});
