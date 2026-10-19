// The harness is a deliverable, so it is verified like one: the page must carry
// every kit in every state at both walkthrough widths, in all four served
// variants, with the contractor example measured at the same bar as the sheet.
// The serve smoke proves the server starts, grades its own page (the served-title
// check is the "a gate must know it is grading its own program" lesson), and
// refuses a second server on an occupied port.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

import { contrast } from './_lib/sheet.mjs';
import { HARNESS_STATES, HARNESS_TITLE, KITS, SHEET_TAG, WIDTHS, buildHarnessPage, harnessSheets } from '../src/harness-page.js';

const here = dirname(fileURLToPath(import.meta.url));
const pkgDir = join(here, '..');

// The committed harness variants: four files, one per theme and brand. Each is
// locked byte for byte to the same builder, so the double-click pages cannot
// drift from what the kits render.
const VARIANTS = [
  ['index.html', { theme: 'dark', brand: 'base' }],
  ['index-light.html', { theme: 'light', brand: 'base' }],
  ['index-hh-pro.html', { theme: 'dark', brand: 'hh-pro' }],
  ['index-light-hh-pro.html', { theme: 'light', brand: 'hh-pro' }],
];

test('the committed static harness is a fresh build of the page and its sheets, byte for byte', () => {
  // The static files are the deliverable the design sync opens; a stale one
  // shows kits the sources no longer render. The lock is exact equality with a
  // fresh build for each committed variant, page and per-kit sheets both, so
  // neither can drift. A sheet left behind for a kit that no longer exists
  // fails the same lock: the directory must be exactly what the build writes.
  for (const [file, opts] of VARIANTS) {
    const committed = readFileSync(join(pkgDir, 'harness', file), 'utf8');
    assert.equal(
      committed,
      buildHarnessPage(opts),
      `harness/${file} is stale: run npm run build:harness and commit the result`,
    );
    assert.ok(!/<script/i.test(committed), `harness/${file} must stay script-free`);
  }
  const committed = readFileSync(join(pkgDir, 'harness', 'index.html'), 'utf8');

  // Each cell paints inside its own declarative shadow root, so every kit is
  // under its own styles: one open shadow root per cell, each linking the
  // kit's sheet. Asserted on the default page; the other three are locked to
  // the same builder above.
  const cells = committed.match(/data-cell="[^"]+"/g)?.length ?? 0;
  const roots = committed.match(/<template shadowrootmode="open">/g)?.length ?? 0;
  assert.ok(cells > 0, 'the page carries no cells');
  assert.equal(roots, cells, 'every cell must paint inside its own declarative shadow root');

  const sheetsDir = join(pkgDir, 'harness', 'sheets');
  const onDisk = existsSync(sheetsDir) ? readdirSync(sheetsDir).sort() : [];
  const expected = harnessSheets().map((s) => `${s.tag}.css`).sort();
  assert.deepEqual(onDisk, expected, 'harness/sheets must hold exactly one sheet per kit, nothing else');

  // The vendored face is held the same way: the build copies src/fonts beside
  // the pages, the inlined token sheet registers Inter through page-relative
  // ./fonts/ URLs, and a stale or missing copy means the committed pages open
  // in the fallback face. Exact byte equality with src/fonts, nothing else.
  const fontsSrc = join(pkgDir, 'src', 'fonts');
  const fontsDir = join(pkgDir, 'harness', 'fonts');
  const fontsOnDisk = existsSync(fontsDir) ? readdirSync(fontsDir).sort() : [];
  const fontsExpected = readdirSync(fontsSrc).sort();
  assert.deepEqual(fontsOnDisk, fontsExpected, 'harness/fonts must hold exactly the vendored face files, nothing else');
  for (const name of fontsExpected) {
    assert.ok(
      readFileSync(join(fontsDir, name)).equals(readFileSync(join(fontsSrc, name))),
      `harness/fonts/${name} is stale: run npm run build:harness and commit the result`,
    );
  }
  for (const { tag, css } of harnessSheets()) {
    assert.equal(
      readFileSync(join(sheetsDir, `${tag}.css`), 'utf8'),
      css,
      `harness/sheets/${tag}.css is stale: run npm run build:harness and commit the result`,
    );
    // A cell without its link would paint an unstyled kit in the committed
    // page: each kit's cells must link the sheet the build ships. The
    // sheet element is one entry per page variant (it does not iterate per
    // state the way the kits do), so it carries one link per page, and
    // the count is what the kit grid's anchor-style cells say (12 links
    // across the page widths), or 1 link for the sheet, whichever the
    // element's iteration calls for.
    const links = [...committed.matchAll(new RegExp(`<link rel="stylesheet" href="./sheets/${tag}\\.css">`, 'g'))].length;
    const expected = tag === SHEET_TAG
      ? 1
      : HARNESS_STATES.length * WIDTHS.length;
    assert.ok(
      links >= expected,
      `${tag}: only ${links} cells link the kit sheet; expected ${expected} or more`,
    );
  }
  for (const [file] of VARIANTS) {
    assert.ok(statSync(join(pkgDir, 'harness', file)).isFile(), `the committed page ${file} must stay a file`);
  }
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

// Spawns are asked for port 0 unless an explicit port is pinned: port 0 has
// the kernel hand out a genuinely free port, so the suite never races the
// ephemeral range for one. The child announces its actual port on stdout and
// the announce line is the only source of truth for which port came up; the
// parsed port is resolved so every later fetch goes through it. An explicit
// port pins a spawn to a known one, which is how the rerun and refusal legs
// prove their point against a real announced port.
const ANNOUNCED = /harness on http:\/\/127\.0\.0\.1:(\d+)\//;

const startServer = (explicitPort) =>
  new Promise((resolve, reject) => {
    const child = spawn(process.execPath, ['--import', './src/_ts-js-loader.mjs', 'harness/serve.mjs'], {
      cwd: pkgDir,
      env: { ...process.env, HARNESS_PORT: explicitPort === undefined ? '0' : String(explicitPort) },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let out = '';
    child.stdout.on('data', (d) => (out += d));
    child.stderr.on('data', (d) => (out += d));
    child.on('exit', (code, signal) => resolve({ code, signal, out, child }));

    const poll = async () => {
      for (let i = 0; i < 60; i++) {
        await delay(250);
        if (child.exitCode !== null) return; // the exit handler already resolved
        const announced = out.match(ANNOUNCED);
        if (!announced) continue; // keep polling until the announce line appears
        const port = Number(announced[1]);
        try {
          const res = await fetch(`http://127.0.0.1:${port}/`);
          const text = await res.text();
          // Only OUR page counts, and only while OUR child is the one running:
          // a left-over server on this port is the exact thing the port guard
          // exists to refuse, so its answers must never read as success here.
          if (res.ok && text.includes(`<title>${HARNESS_TITLE}</title>`) && out.includes('harness on http')) {
            resolve({ code: 0, out, child, port });
            return;
          }
        } catch {}
      }
      child.kill('SIGKILL');
      reject(new Error(`the server never announced a port it serves. Output:\n${out}`));
    };
    poll();
  });

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
  const first = await startServer();
  assert.equal(first.code, 0, `the first server should stay up. Output:\n${first.out}`);
  assert.ok(first.port > 0, `the announce must carry an actual port, got ${first.port}`);

  const page = await (await fetch(`http://127.0.0.1:${first.port}/`)).text();
  assert.match(page, new RegExp(`<title>${HARNESS_TITLE}</title>`), 'the served page must be this harness');
  const variants = await (await fetch(`http://127.0.0.1:${first.port}/?theme=light&brand=hh-pro`)).text();
  assert.match(variants, /data-theme="light" data-brand="hh-pro"/, 'the served variants must flip the html attributes');
  const sheet = await (await fetch(`http://127.0.0.1:${first.port}/tokens.css`)).text();
  assert.match(sheet, /--hh-page:/, 'the server must serve the raw sheet beside the page');
  const kitSheet = await (await fetch(`http://127.0.0.1:${first.port}/sheets/hh-inbox.css`)).text();
  assert.match(kitSheet, /\.kit-root/, 'the server must serve the per-kit sheets the page links');

  first.child.kill('SIGKILL');
  await delay(500);

  // A clean stop frees the announced port for the next run: the guard refuses
  // a LIVE stranger, not the memory of one. The rerun pins the freed port as
  // an explicit HARNESS_PORT, so the proof is against a real known port.
  const second = await startServer(first.port);
  assert.equal(second.code, 0, `a rerun on the freed port must serve. Output:\n${second.out}`);
  assert.equal(second.port, first.port, 'the rerun must come up on the port it was pinned to');
  second.child.kill('SIGKILL');
});

test('a second server refuses an occupied port, loudly', async () => {
  const holder = await startServer();
  assert.equal(holder.code, 0, 'the holder must be up before the refusal is proven');
  try {
    const second = await startServer(holder.port);
    assert.notEqual(second.code, 0, 'a second server on an occupied port must refuse');
    assert.match(second.out, /already in use/, 'the refusal must say the port is occupied');
  } finally {
    holder.child.kill('SIGKILL');
  }
});

test('two servers asked for port 0 at the same time get different actual ports and both serve', async () => {
  // The shape that used to collide by luck: a random pick inside the ephemeral
  // range can land on a port the kernel already handed out. With port 0 the
  // kernel assigns each spawn its own port, so two simultaneous servers must
  // announce different ports and both must grade as this harness.
  const [a, b] = await Promise.all([startServer(), startServer()]);
  try {
    assert.equal(a.code, 0, `the first of the pair should stay up. Output:\n${a.out}`);
    assert.equal(b.code, 0, `the second of the pair should stay up. Output:\n${b.out}`);
    assert.ok(a.port > 0 && b.port > 0, 'both of the pair must announce actual ports');
    assert.notEqual(a.port, b.port, 'two simultaneous port 0 servers must not share a port');
    for (const { port } of [a, b]) {
      const page = await (await fetch(`http://127.0.0.1:${port}/`)).text();
      assert.match(page, new RegExp(`<title>${HARNESS_TITLE}</title>`), 'each server of the pair must serve this harness');
    }
  } finally {
    a.child.kill('SIGKILL');
    b.child.kill('SIGKILL');
  }
});
}
