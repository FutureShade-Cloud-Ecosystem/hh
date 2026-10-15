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
import { parseRules, declsFor, px } from './lib/css.mjs';
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

test('a read cache only app never promises that writes wait or send later', () => {
  for (const mode of ['offline', 'unreachable']) {
    const markup = visible(renderKit(ShellBanner, { mode, waiting: 0, cacheOnly: true }));
    assert.ok(markup.includes('role="status"'), `${mode}: still a live region`);
    assert.ok(/offline|can't be reached/i.test(markup), `${mode}: still names the state`);
    assert.ok(!/wait|send|outbox|safe/i.test(markup), `${mode}: no word about writes kept for later`);
    assert.ok(/copy on this device/i.test(markup), `${mode}: says what the screen is showing`);
  }
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

// The install seam, driven through the click listener the element registers on itself.
// The Node render never connects an element, so the test captures the listener and the
// events, stops Lit's update loop, and hands the listener a fake event whose path holds a
// control with the wanted data-action. A deferred promise stands in for the updater.

function installRig(updater) {
  const el = new ShellUpdate();
  const listeners = {};
  const events = [];
  el.addEventListener = (type, fn) => {
    listeners[type] = fn;
  };
  el.dispatchEvent = (event) => {
    events.push(event.type);
    return true;
  };
  el.performUpdate = () => {};
  el.open = true;
  el.updater = updater;
  el.connectedCallback();
  const press = (action) => {
    const control = Object.create(Element.prototype);
    control.hasAttribute = (name) => name === 'data-action';
    control.getAttribute = (name) => (name === 'data-action' ? action : null);
    listeners.click({ composedPath: () => [control] });
  };
  const buttons = () => {
    const markup = renderTemplate(el.render());
    const tag = (action) => new RegExp(`<button[^>]*data-action="${action}"[^>]*>`).exec(markup)?.[0] ?? '';
    return { words: visible(markup), install: tag('install'), later: tag('later') };
  };
  return { el, events, press, buttons };
}

const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

test('Update now calls updater.install() once, disables both buttons, and recovers when it settles', async () => {
  const gate = deferred();
  let calls = 0;
  const rig = installRig({
    install: () => {
      calls += 1;
      return gate.promise;
    },
  });
  assert.ok(!/disabled/.test(rig.buttons().install), 'enabled before the press');
  rig.press('install');
  assert.equal(calls, 1, 'the seam is called');
  assert.deepEqual(rig.events, ['hh-shell-update-install'], 'the install event rides out');
  const during = rig.buttons();
  assert.ok(during.words.includes('Installing'), 'the card says Installing');
  assert.ok(/disabled/.test(during.install) && /disabled/.test(during.later), 'both buttons are disabled');
  rig.press('install');
  assert.equal(calls, 1, 'a second press while installing does not call the seam again');
  gate.resolve();
  await gate.promise;
  await Promise.resolve();
  const after = rig.buttons();
  assert.ok(after.words.includes('Update now'), 'the card returns to Update now');
  assert.ok(!/disabled/.test(after.install) && !/disabled/.test(after.later), 'and both buttons come back');
});

test('Later closes the card and emits, and is the other action name', () => {
  const rig = installRig(null);
  rig.press('later');
  assert.deepEqual(rig.events, ['hh-shell-update-later']);
  assert.equal(rig.el.open, false);
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

test('FLOOR: the auth expired Sign in button keeps the 44 px target, at every size', () => {
  const rules = parseRules(ShellBanner.styles.map((sheet) => sheet.cssText).join('\n'));
  const own = declsFor(rules, '.sign-in');
  for (const name of ['min-height', 'height']) {
    if (name in own) assert.ok(px(own[name]) === null || px(own[name]) >= 44, `.sign-in ${name} is ${own[name]}, below 44 px`);
  }
});
