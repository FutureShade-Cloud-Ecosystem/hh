// THE DISABLED RULE, HELD ACROSS THE WHOLE WEB TREE (UX-113).
//
// The rule, stated once by the design system and carried by every app: a
// disabled control DROPS ITS FILL AND GOES GREY through the --hh-disabled-*
// token swap. It never fades, and it never keeps a brand fill.
//
// This file holds the rule in two legs:
//
//   1. THE SOURCE LEG: a lint over every shipped style under web/apps and
//      web/packages. A rule that styles a disabled state (a :disabled, a
//      [disabled], an [aria-disabled="true"] selector) may not declare an
//      opacity below 1 (a fade is not a state) and may not declare a fill, an
//      ink or a line that is not the grey disabled triple. The app-local
//      rule that faded a gold button, or that kept the gold fill under a
//      reduced opacity, is caught here before anyone sees it.
//
//   2. THE PAINT LEG: a real browser, a real page, the design system's own
//      sheet. A disabled primary button, a disabled secondary button and a
//      disabled field each compute background-color equal to
//      --hh-disabled-fill, color equal to --hh-disabled-ink and opacity 1, in
//      the charcoal half and in the light half. The source leg cannot see a
//      cascade; this leg sees what a person would see.
//
// The source leg reads the web tree, so it is a web-tree gate that lives in
// the package that owns the rule. Its scope is deliberately wider than the
// package's own: the rule is a house rule, and a house rule that only its own
// package obeys is not a house rule.
//
// The comments in a source file are stripped before the scan, so a comment
// that QUOTES the word is not a violation, but a declaration is. The scan
// reads the innermost rule blocks, so a rule nested in a media query is read
// as the rule it is.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { findBrowserBinary, launchBrowser } from './_lib/cdp.mjs';
import { baseStyles } from '../src/primitives.js';

const here = dirname(fileURLToPath(import.meta.url));
const pkgDir = join(here, '..');
const webRoot = join(pkgDir, '..', '..');

// ── the source leg ──────────────────────────────────────────────────────────────

// Everything under web/apps and web/packages that can carry a style. The
// dependency and build directories are skipped by name (they are not the
// house's own source); the test directories are NOT skipped, because a rule a
// test ships is still a rule the tree ships.
const SKIP_DIRS = new Set(['node_modules', 'dist', 'build', 'coverage', '.git']);
const STYLE_EXT = /\.(ts|tsx|js|mjs|css|html)$/;

function webStyleFiles() {
  const out = [];
  const walk = (dir, prefix) => {
    for (const entry of readdirSync(dir).sort()) {
      const rel = prefix ? `${prefix}/${entry}` : entry;
      const abs = join(dir, entry);
      if (statSync(abs).isDirectory()) {
        if (SKIP_DIRS.has(entry)) continue;
        walk(abs, rel);
      } else if (STYLE_EXT.test(entry)) {
        out.push(rel);
      }
    }
  };
  for (const root of ['apps', 'packages']) {
    const abs = join(webRoot, root);
    if (!statSync(abs).isDirectory()) continue;
    walk(abs, root);
  }
  return out.sort();
}

/** Blanks a comment out, keeping every newline it held, so the line numbers
 *  the gate reports are the line numbers in the source file. */
function blankComments(src) {
  return src.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '));
}

/** Removes every template-literal interpolation, braces and all, so a CSS
 *  block's braces are the block's own and the rule scan reads whole rules.
 *  The newlines inside the interpolation are kept, for the same reason. */
function stripInterpolations(src) {
  let out = '';
  let i = 0;
  while (i < src.length) {
    if (src[i] === '$' && src[i + 1] === '{') {
      let depth = 1;
      i += 2;
      while (i < src.length && depth > 0) {
        if (src[i] === '{') depth++;
        else if (src[i] === '}') depth--;
        if (src[i] !== '\n') out += ' ';
        else out += '\n';
        i++;
      }
      out += ' ';
      continue;
    }
    out += src[i];
    i++;
  }
  return out;
}

/** Every innermost rule block in a source, with the selector the block
 *  belongs to. The selector is the text after the last brace (a rule nested
 *  in an at-rule) and after the last statement break (a rule that follows a
 *  declaration in the same template). */
function ruleBlocks(src) {
  const out = [];
  const re = /([^{}]+)\{([^{}]*)\}/g;
  let m;
  while ((m = re.exec(src)) !== null) {
    const chunk = m[1].split(';').pop() ?? '';
    let selector = chunk.split('{').pop() ?? '';
    const tick = selector.lastIndexOf('`');
    if (tick !== -1) selector = selector.slice(tick + 1);
    selector = selector.trim();
    if (selector === '') continue;
    // The line the SELECTOR starts on, not the line the block's text starts
    // on: the chunk carries the whitespace and the blanked comment that sit
    // between the previous rule and this one.
    const lead = m[1].length - m[1].replace(/^\s+/, '').length;
    const line = src.slice(0, m.index + lead).split('\n').length;
    out.push({ selector, body: m[2], line });
  }
  return out;
}

/** Strips every :not(...) group, so a rule that EXCLUDES the disabled state
 *  is not read as a rule that styles it. */
function stripNotGroups(selector) {
  let out = selector;
  let prev;
  do {
    prev = out;
    out = out.replace(/:not\([^()]*\)/g, ' ');
  } while (out !== prev);
  return out;
}

/** True when the selector styles a disabled state. */
function isDisabledSelector(selector) {
  const s = stripNotGroups(selector);
  if (/:disabled\b/.test(s)) return true;
  if (/\[disabled\]/.test(s)) return true;
  if (/\[aria-disabled\s*=\s*["']?true/.test(s)) return true;
  if (/(^|[\s>+~,])\.disabled(?=[\s.:\[>+~,]|$)/.test(s)) return true;
  return false;
}

/** Every declaration in a rule body, as [property, value] pairs. */
function declarations(body) {
  const out = [];
  for (const m of body.matchAll(/(?:^|[;{])\s*([a-z-]+)\s*:\s*([^;]+)/gi)) {
    out.push([m[1].toLowerCase(), m[2].trim()]);
  }
  return out;
}

const DISABLED_FILL = 'var(--hh-disabled-fill)';
const DISABLED_INK = 'var(--hh-disabled-ink)';
const DISABLED_LINE = 'var(--hh-disabled-line)';

test('no shipped style fades a disabled control, and no disabled rule keeps a fill of its own', () => {
  const offenders = [];
  for (const rel of webStyleFiles()) {
    const src = stripInterpolations(blankComments(readFileSync(join(webRoot, rel), 'utf8')));
    for (const rule of ruleBlocks(src)) {
      if (!isDisabledSelector(rule.selector)) continue;
      const selector = rule.selector.replace(/\s+/g, ' ');
      for (const [prop, value] of declarations(rule.body)) {
        if (prop === 'opacity') {
          const num = parseFloat(value);
          if (Number.isFinite(num) && num < 1) {
            offenders.push(`${rel}:${rule.line} ${selector} -> opacity: ${value}`);
          }
        } else if (prop === 'filter') {
          offenders.push(`${rel}:${rule.line} ${selector} -> filter: ${value}`);
        } else if (prop === 'background' || prop === 'background-color') {
          if (!value.includes(DISABLED_FILL)) {
            offenders.push(`${rel}:${rule.line} ${selector} -> ${prop}: ${value}`);
          }
        } else if (prop === 'color') {
          if (!value.includes(DISABLED_INK)) {
            offenders.push(`${rel}:${rule.line} ${selector} -> color: ${value}`);
          }
        } else if (prop === 'border' || prop === 'border-color') {
          if (!value.includes(DISABLED_LINE)) {
            offenders.push(`${rel}:${rule.line} ${selector} -> ${prop}: ${value}`);
          }
        }
      }
    }
  }
  assert.deepEqual(
    offenders.sort(),
    [],
    `a shipped style breaks the disabled rule. A disabled control DROPS ITS FILL AND GOES GREY through\n` +
      `the --hh-disabled-* token swap: the fill is ${DISABLED_FILL}, the ink is ${DISABLED_INK}, the line is\n` +
      `${DISABLED_LINE}, and the opacity is 1. A fade is not a state, and a disabled control never keeps a\n` +
      `brand fill:\n  ${offenders.join('\n  ')}`,
  );
});

test('the design system states the disabled rule once, as the grey token swap', () => {
  const sheet = baseStyles.cssText;
  const rule = sheet.match(/button:disabled[\s\S]*?\}/);
  assert.ok(rule, 'the primitives must carry a button:disabled rule');
  assert.match(rule[0], /var\(--hh-disabled-fill\)/, 'the swap must set the disabled fill token');
  assert.match(rule[0], /var\(--hh-disabled-ink\)/, 'the swap must set the disabled ink token');
  assert.match(rule[0], /var\(--hh-disabled-line\)/, 'the swap must set the disabled line token');
  assert.doesNotMatch(rule[0], /opacity/, 'the swap must not fade');
});

// ── the paint leg ───────────────────────────────────────────────────────────────

const underBun = typeof globalThis.Bun !== 'undefined';
const hasWebSocket = typeof globalThis.WebSocket === 'function';
const binary = !underBun && hasWebSocket ? findBrowserBinary() : null;

function skipReason() {
  if (underBun) return 'the authoritative runner for the paint leg is Node 22 (bun runs the source leg and skips the real-browser leg)';
  if (!hasWebSocket) return `this runtime has no global WebSocket; Node 22 is required, got ${process.version}`;
  if (!binary) {
    return (
      'no headless Chromium binary exists (looked at $HH_DS_BROWSER, then google-chrome, ' +
      'google-chrome-stable, chromium, chromium-browser, chrome on the PATH, then the Playwright cache); ' +
      'point HH_DS_BROWSER at one to run this leg'
    );
  }
  return null;
}

/** The gate's page: the design system's own sheet and the three controls the
 *  rule names, inside one shadow root so the sheet paints as it does in an
 *  app that adopts it. */
function gatePage(theme) {
  const themeAttr = theme === 'light' ? ' data-theme="light"' : '';
  return `<!doctype html>
<html lang="en"${themeAttr}>
<head>
<meta charset="utf-8">
<title>hh disabled token gate</title>
<link rel="stylesheet" href="/tokens.css">
</head>
<body>
<div id="stage">
<template shadowrootmode="open">
<style>${baseStyles.cssText}</style>
<button type="button" class="btn btn-primary" data-testid="primary" disabled>Primary</button>
<button type="button" class="btn btn-secondary" data-testid="secondary" disabled>Secondary</button>
<input class="control" type="text" data-testid="field" disabled>
</template>
</div>
</body>
</html>`;
}

function startServer() {
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://localhost');
    if (url.pathname === '/tokens.css') {
      res.writeHead(200, { 'content-type': 'text/css; charset=utf-8' });
      res.end(readFileSync(join(pkgDir, 'src', 'tokens.css'), 'utf8'));
      return;
    }
    if (url.pathname === '/light' || url.pathname === '/') {
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      res.end(gatePage(url.pathname === '/light' ? 'light' : 'charcoal'));
      return;
    }
    res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' });
    res.end('not found');
  });
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      resolve({ port: server.address().port, close: () => new Promise((r) => server.close(r)) });
    });
  });
}

/** A hex colour to the rgb() string the browser computes. */
const toRgb = (hex) => {
  const m = String(hex).trim().match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/i);
  if (!m) return null;
  const h = m[1].length === 3 ? m[1].split('').map((c) => c + c).join('') : m[1];
  const n = parseInt(h, 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
};

const reason = skipReason();
const inCI = process.env.CI !== undefined && process.env.CI !== '';

if (reason && inCI && !underBun) {
  test('the disabled paint leg refuses to skip when CI is set', () => {
    assert.fail(
      `CI is set and the disabled paint leg cannot run; it fails instead of skipping: ${reason}. ` +
        'Install the browser the leg uses (google-chrome) or point HH_DS_BROWSER at one.',
    );
  });
} else if (reason && underBun) {
  test('the disabled paint leg has a loud, named reason for not running here', () => {
    console.warn(`SKIPPING the disabled paint leg: ${reason}`);
    assert.ok(reason.length > 0, 'the skip reason must be stated, never silent');
  });
} else if (reason) {
  console.warn(`SKIPPING the disabled paint leg: ${reason}`);
  test(
    'the disabled paint leg has a loud, named reason for not running here',
    { skip: reason },
    () => assert.fail('the disabled paint leg must not run where it declared a named skip'),
  );
} else {
  test('a disabled primary, a disabled secondary and a disabled field all paint the grey token swap', async () => {
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      for (const theme of ['charcoal', 'light']) {
        await cdp.navigate(`http://127.0.0.1:${port}/${theme === 'light' ? 'light' : ''}`);
        const read = await cdp.evaluate(`(() => {
          const root = document.documentElement;
          const cs = getComputedStyle(root);
          const stage = document.getElementById('stage');
          const shadow = stage.shadowRoot;
          const paint = (testid) => {
            const el = shadow.querySelector('[data-testid="' + testid + '"]');
            const s = getComputedStyle(el);
            return {
              background: s.backgroundColor,
              color: s.color,
              border: s.borderTopColor,
              opacity: s.opacity,
              disabled: el.disabled === true,
            };
          };
          return {
            fill: cs.getPropertyValue('--hh-disabled-fill').trim(),
            ink: cs.getPropertyValue('--hh-disabled-ink').trim(),
            line: cs.getPropertyValue('--hh-disabled-line').trim(),
            primary: paint('primary'),
            secondary: paint('secondary'),
            field: paint('field'),
          };
        })()`);
        const fill = toRgb(read.fill);
        const ink = toRgb(read.ink);
        const line = toRgb(read.line);
        assert.ok(fill && ink && line, `${theme}: the disabled tokens must resolve to a colour`);
        for (const [name, shape] of [['primary', read.primary], ['secondary', read.secondary], ['field', read.field]]) {
          assert.equal(shape.disabled, true, `${theme}: the ${name} must really be disabled`);
          assert.equal(shape.background, fill, `${theme}: the disabled ${name} must fill with --hh-disabled-fill`);
          assert.equal(shape.color, ink, `${theme}: the disabled ${name} must ink with --hh-disabled-ink`);
          assert.equal(shape.border, line, `${theme}: the disabled ${name} must line with --hh-disabled-line`);
          assert.equal(shape.opacity, '1', `${theme}: the disabled ${name} must not be faded`);
        }
      }
    } finally {
      await cdp.close();
      await close();
    }
  });
}
