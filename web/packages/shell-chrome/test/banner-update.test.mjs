// The offline banner and the update prompt, over the server render. The
// banner words are the r4 offline board's (the outbox count rides the strip,
// the reads answer from the local store), the four states the brief names:
// offline, unreachable, read cache only (a second tab), and auth expired.
// The update prompt is the r4 updater board's card: Update now and Later,
// the injected updater seam behind the install action (the Tauri updater is
// wired by the workspace item, not here).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { renderKit, renderTemplate } from './lib/render.mjs';
import { ShellBanner } from '../src/shell-banner.ts';
import { ShellUpdate } from '../src/shell-update.ts';
import { icon } from '@hardscapehouse/design-system/src/state-icons.js';

const bannerSrc = readFileSync(fileURLToPath(new URL('../src/shell-banner.ts', import.meta.url)), 'utf8');
const updateSrc = readFileSync(fileURLToPath(new URL('../src/shell-update.ts', import.meta.url)), 'utf8');

/** The rendered markup with markers stripped and the server render's escaped
 * entities decoded: the text a person reads. */
const visible = (markup) =>
  markup
    .replace(/<!--.*?-->/g, '')
    .replace(/&#39;/g, "'")
    .replace(/&quot;/g, '"')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&');

// ── the offline banner ───────────────────────────────────────────────────────────────────────────

test('with no mode the banner renders nothing a person can see or hear', () => {
  const markup = renderKit(ShellBanner, { mode: '' });
  assert.ok(!markup.includes('role="status"'), 'an online, owned, signed in device carries no banner');
  assert.ok(!markup.includes('banner-text'), 'and no words');
});

test('offline names itself, carries the outbox depth in words and in the count chip', () => {
  const markup = visible(renderKit(ShellBanner, { mode: 'offline', waiting: 3 }));  assert.ok(markup.includes('role="status"'), 'the banner is a live region');
  assert.ok(markup.includes("You're offline"), 'the offline words');
  assert.ok(markup.includes('3 changes in the outbox'), 'the depth in words');
  assert.ok(markup.includes("they send when you're back"), 'and what happens next');
  assert.ok(markup.includes('>3<'), 'the count chip shows the number');
});

test('offline with an empty outbox says the truth about writes instead of a count', () => {
  const markup = visible(renderKit(ShellBanner, { mode: 'offline', waiting: 0 }));
  assert.ok(markup.includes("You're offline"), 'the offline words');
  assert.ok(!/>\d+</.test(markup), 'no count chip without a count');
  assert.ok(/writes wait/i.test(markup), 'the writes wait words');
});

test('unreachable is its own state with its own words', () => {
  const markup = visible(renderKit(ShellBanner, { mode: 'unreachable', waiting: 2 }));
  assert.ok(/can't be reached/i.test(markup), 'the unreachable words');
  assert.ok(/send in order/i.test(markup), 'the order promise');
});

test('read only (a second tab) says so, and never asks this tab to sync', () => {
  const markup = visible(renderKit(ShellBanner, { mode: 'read-only', waiting: 5 }));
  assert.ok(/read only/i.test(markup), 'the read only words');
  assert.ok(/another tab/i.test(markup), 'names the tab that owns the sync');
});

test('auth expired keeps the person\u2019s work and offers the way back in', () => {
  const markup = visible(renderKit(ShellBanner, { mode: 'auth-expired', waiting: 4 }));
  assert.ok(/Sign in expired/.test(markup), 'the r6 words for the expired session');
  assert.ok(/your offline work is safe/i.test(markup), 'the work is safe words');
  const signIn = markup.match(/<button[^>]*data-action="sign-in"[^>]*>[\s\S]*?<\/button>/);
  assert.ok(signIn !== null, 'the sign in action is present');
  assert.ok(signIn[0].includes('>Sign in<'), 'and reads Sign in');
});

test('an unknown mode is refused loudly, never painted as something else', () => {
  assert.throws(() => renderKit(ShellBanner, { mode: 'warp' }), /unknown mode/);
});

test('the banner pairs its icon with its words: the icon is never the only signal', () => {
  const markup = renderKit(ShellBanner, { mode: 'offline', waiting: 1 });
  assert.ok(markup.includes('aria-hidden="true"'), 'the icon is decoration beside the words');
  assert.ok(markup.includes('class="icon"'), 'the icon rides the design system\u2019s state icons');
});

// ── the update prompt ────────────────────────────────────────────────────────────────────────────

test('closed by default, the prompt renders nothing a person can see or hear', () => {
  const markup = renderKit(ShellUpdate, {});
  assert.ok(!markup.includes('class="prompt"'), 'quiet until the download is whole');
  assert.ok(!markup.includes('Update now'), 'and no actions');
});

test('open, the card names the shell, the version, and what carries over', () => {
  const markup = visible(renderKit(ShellUpdate, { open: true, version: 'r18' }));
  assert.ok(markup.includes('HH Field update ready'), 'the default shell name is HH Field');
  assert.ok(markup.includes('r18 is downloaded'), 'the version line');
  assert.ok(
    markup.includes('Restart to update. Your sign in, your pins and everything in the outbox carry over; nothing queued is lost.'),
    'the carrying-over words, exactly',
  );
});

test('the two actions are Update now (the primary) and Later, under their action names', () => {
  const markup = renderKit(ShellUpdate, { open: true });
  const now = markup.match(/<button[^>]*data-action="install"[^>]*>[\s\S]*?<\/button>/);
  const later = markup.match(/<button[^>]*data-action="later"[^>]*>[\s\S]*?<\/button>/);
  assert.ok(now !== null && now[0].includes('btn-primary'), 'Update now is the primary');
  assert.ok(now !== null && visible(now[0]).includes('Update now'), 'and reads Update now');
  assert.ok(later !== null && visible(later[0]).includes('Later'), 'Later is present');
});

test('the prompt names itself for the shell it updates', () => {
  const markup = visible(renderKit(ShellUpdate, { open: true, shellName: 'HH Work', version: 'r7' }));
  assert.ok(markup.includes('HH Work update ready'), 'the shell name rides the card');
  assert.ok(markup.includes('r7 is downloaded'), 'and the version');
});

test('the updater seam is a public property the shell injects, never fetched here', () => {
  assert.ok(/declare updater/.test(updateSrc), 'the seam is declared on the element');
  assert.ok(!/fetch\(|import\s*\(.?tauri/i.test(updateSrc), 'no updater is reached from inside the package');
});

// ── FLOORS, as source scans in the house split ───────────────────────────────────────────────────

test('FLOOR: tokens only, no hex colour literals in the banner or the prompt', () => {
  for (const [name, src] of [['shell-banner.ts', bannerSrc], ['shell-update.ts', updateSrc]]) {
    const hexes = [...src.matchAll(/#[0-9a-fA-F]{3,8}\b/g)].map((m) => m[0]);
    assert.deepEqual(hexes, [], `${name} must stay literal-free, found: ${hexes.join(', ')}`);
  }
});

test('the state icons render as geometry through the svg tag', () => {
  const markup = renderTemplate(icon('wifiOff'));
  assert.ok(markup.includes('<svg'), 'the icon renders as svg');
});
