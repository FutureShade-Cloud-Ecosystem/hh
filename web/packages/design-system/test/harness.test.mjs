// The harness is a deliverable, so it is verified like one: the page must carry
// every kit in every state at both walkthrough widths, in all four served
// variants, with the contractor example measured at the same bar as the sheet.
// The serve smoke proves the server starts, grades its own page (the served-title
// check is the "a gate must know it is grading its own program" lesson), and
// refuses a second server on an occupied port.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

import { contrast } from './_lib/sheet.mjs';
import { HARNESS_STATES, HARNESS_TITLE, KITS, WIDTHS, buildHarnessPage } from '../src/harness-page.js';

const here = dirname(fileURLToPath(import.meta.url));
const pkgDir = join(here, '..');

test('the committed static harness is a fresh build of the page, byte for byte', () => {
  // The static file is the deliverable the design sync opens; a stale one
  // shows kits the sources no longer render. The lock is exact equality with
  // the default build (dark, base brand), so the file cannot drift.
  const committed = readFileSync(join(pkgDir, 'harness', 'index.html'), 'utf8');
  assert.equal(
    committed,
    buildHarnessPage({ theme: 'dark', brand: 'base' }),
    'harness/index.html is stale: run npm run build:harness and commit the result',
  );
  assert.ok(!/<script/i.test(committed), 'the static harness must stay script-free');
});

test('the harness page carries every kit in every state at both walkthrough widths', () => {
  for (const theme of ['dark', 'light']) {
    for (const brand of ['base', 'hh-pro']) {
      const page = buildHarnessPage({ theme, brand });
      assert.match(page, new RegExp(`<title>${HARNESS_TITLE}</title>`));

      const cells = [...page.matchAll(/data-cell="([^"]+)"/g)].map((m) => m[1]);
      // One cell per kit and state, PER width: the page holds both walkthrough
      // widths side by side, so the total is the grid twice.
      assert.equal(
        cells.length,
        KITS.length * HARNESS_STATES.length * WIDTHS.length,
        `${theme}/${brand}: expected one cell per kit and state at each width`,
      );
      for (const kit of KITS) {
        for (const state of HARNESS_STATES) {
          assert.ok(
            cells.includes(`${kit.tag}/${state}`),
            `${theme}/${brand}: the page lost ${kit.tag} in its ${state} state`,
          );
        }
      }
      for (const w of WIDTHS) {
        assert.ok(page.includes(`data-width="${w}"`), `${theme}/${brand}: the page lost the ${w}px width`);
      }

      const htmlOpen = page.match(/<html[^>]*>/)[0];
      assert.ok(htmlOpen.includes(`data-theme="${theme}"`), `${theme}/${brand}: wrong served theme`);
      assert.equal(htmlOpen.includes('data-brand="hh-pro"'), brand === 'hh-pro', `${theme}/${brand}: wrong served brand`);
    }
  }
});

test('the contractor example on the page clears AA, measured', () => {
  // The proposal section sets --cbrand/--cbrand-on inline, the way the public
  // proposal surface will at runtime. The static palette cannot know the
  // runtime value, so the example the harness ships is measured here, at the
  // same bar; the pair the page paints through the layer vars is measured in
  // every palette by the contrast gate's declared-pair pass.
  const page = buildHarnessPage();
  const m = page.match(/--cbrand:(#[0-9a-fA-F]{3,8});\s*--cbrand-on:(#[0-9a-fA-F]{3,8})/);
  assert.ok(m, 'the page must carry the contractor example values');
  const ratio = contrast(m[1].toLowerCase(), m[2].toLowerCase());
  assert.ok(
    ratio >= 4.5,
    `the example contractor accent (${m[1]}) under its on-ink (${m[2]}) is ${ratio.toFixed(2)}:1, below AA 4.5:1`,
  );
  assert.match(page, /\.proposal-accent\s*\{[^}]*background:\s*var\(--cbrand\)/, 'the example must consume --cbrand, not a literal');
});

// ── the serve smoke: the server grades its own page ─────────────────────────────

const startServer = (port) =>
  new Promise((resolve, reject) => {
    const child = spawn(process.execPath, ['--import', './src/_ts-js-loader.mjs', 'harness/serve.mjs'], {
      cwd: pkgDir,
      env: { ...process.env, HARNESS_PORT: String(port) },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let out = '';
    child.stdout.on('data', (d) => (out += d));
    child.stderr.on('data', (d) => (out += d));
    child.on('exit', (code, signal) => resolve({ code, signal, out }));

    const poll = async () => {
      for (let i = 0; i < 60; i++) {
        await delay(250);
        if (child.exitCode !== null) return; // the exit handler already resolved
        try {
          const res = await fetch(`http://127.0.0.1:${port}/`);
          const text = await res.text();
          // Only OUR page counts, and only while OUR child is the one running:
          // a left-over server on this port is the exact thing the port guard
          // exists to refuse, so its answers must never read as success here.
          if (res.ok && text.includes(`<title>${HARNESS_TITLE}</title>`) && out.includes('harness on http')) {
            resolve({ code: 0, out, child });
            return;
          }
        } catch {}
      }
      child.kill('SIGKILL');
      reject(new Error(`the server on port ${port} never announced itself. Output:\n${out}`));
    };
    poll();
  });

const freePort = () => 41000 + Math.floor(Math.random() * 2000);

// The serve smoke spawns real servers and awaits them, and bun (CI's drift
// gate) interleaves one file's running async tests with the next file's module
// evaluation, so top-level test() calls made during that overlap throw
// ERR_NOT_IMPLEMENTED (oven-sh/bun#5090) and take innocent files down with
// them. The two server tests therefore register on node only, which is this
// package's authoritative runner; under bun one statement test registers in
// their place so the skip is visible in the count rather than silent.
const underBun = typeof globalThis.Bun !== 'undefined';

if (underBun) {
  test('the serve smoke is a node-suite leg: bun skips the real-server tests', () => {
    assert.ok(underBun, 'this branch registers only under bun');
  });
} else {
test('the harness server serves this package, and a rerun after a clean stop serves again', async () => {
  const port = freePort();
  const first = await startServer(port);
  assert.equal(first.code, 0, `the first server should stay up. Output:\n${first.out}`);

  const page = await (await fetch(`http://127.0.0.1:${port}/`)).text();
  assert.match(page, new RegExp(`<title>${HARNESS_TITLE}</title>`), 'the served page must be this harness');
  const variants = await (await fetch(`http://127.0.0.1:${port}/?theme=light&brand=hh-pro`)).text();
  assert.match(variants, /data-theme="light" data-brand="hh-pro"/, 'the served variants must flip the html attributes');
  const sheet = await (await fetch(`http://127.0.0.1:${port}/tokens.css`)).text();
  assert.match(sheet, /--hh-page:/, 'the server must serve the raw sheet beside the page');

  first.child.kill('SIGKILL');
  await delay(500);

  // A clean stop frees the port for the next run: the guard refuses a LIVE
  // stranger, not the memory of one.
  const second = await startServer(port);
  assert.equal(second.code, 0, `a rerun on the freed port must serve. Output:\n${second.out}`);
  second.child.kill('SIGKILL');
});

test('a second server refuses an occupied port, loudly', async () => {
  const port = freePort();
  const holder = await startServer(port);
  assert.equal(holder.code, 0, 'the holder must be up before the refusal is proven');
  try {
    const second = await startServer(port);
    assert.notEqual(second.code, 0, 'a second server on an occupied port must refuse');
    assert.match(second.out, /already in use/, 'the refusal must say the port is occupied');
  } finally {
    holder.child.kill('SIGKILL');
  }
});
}
