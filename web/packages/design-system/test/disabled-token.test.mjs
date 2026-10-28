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
//      [disabled], an [aria-disabled="true"] selector, a compound .disabled
//      or .is-disabled class, or a bare-word state class carrying the
//      hh-disabled-state marker) may not declare an opacity below 1 (a fade
//      is not a state), may not declare a filter, and may not declare a
//      fill, an ink or a line that is not the grey disabled triple or that
//      arrives through a transparent mix (color-mix, rgba, hsla). No
//      container that holds a disabled control may carry an opacity below 1
//      or a filter where the lint can see the relationship. The app-local
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
  if (!src.includes('${')) return src;
  // The common shape first: an interpolation with no brace inside it.
  const blank = (m) => m.replace(/[^\n]/g, ' ');
  let out = src.replace(/\$\{[^{}]*\}/g, blank);
  if (!out.includes('${')) return out;
  // An interpolation with a brace inside it: counted, so the scan still sees
  // whole rules.
  let result = '';
  let i = 0;
  while (i < src.length) {
    if (src[i] === '$' && src[i + 1] === '{') {
      let depth = 1;
      i += 2;
      while (i < src.length && depth > 0) {
        if (src[i] === '{') depth++;
        else if (src[i] === '}') depth--;
        if (src[i] !== '\n') result += ' ';
        else result += '\n';
        i++;
      }
      result += ' ';
      continue;
    }
    result += src[i];
    i++;
  }
  return result;
}

/** The line a character sits on, from one pass over the file's newlines. The
 *  scan reads thousands of rules, so the line of a rule must not cost a walk
 *  over the whole file each time. */
function lineIndexer(src) {
  const starts = [0];
  for (let i = 0; i < src.length; i += 1) {
    if (src[i] === '\n') starts.push(i + 1);
  }
  return (index) => {
    let low = 0;
    let high = starts.length - 1;
    while (low < high) {
      const mid = (low + high + 1) >> 1;
      if (starts[mid] <= index) low = mid;
      else high = mid - 1;
    }
    return low + 1;
  };
}

/** Every innermost rule block in a source, with the selector the block
 *  belongs to. The selector is the text after the last brace (a rule nested
 *  in an at-rule) and after the last statement break (a rule that follows a
 *  declaration in the same template).
 *
 *  The walk is a single pass over the file's braces. A pattern match per rule
 *  would re-read the text between the rules over and over, and the tree is
 *  large enough for that to matter. */
function ruleBlocks(src, lineAt) {
  const out = [];
  const open = [];
  let chunkStart = 0;
  for (let i = 0; i < src.length; i += 1) {
    const ch = src[i];
    if (ch === '{') {
      open.push({ chunkStart, bodyStart: i + 1 });
      chunkStart = i + 1;
    } else if (ch === '}') {
      const rule = open.pop();
      if (rule === undefined) {
        chunkStart = i + 1;
        continue;
      }
      const body = src.slice(rule.bodyStart, i);
      if (!body.includes('{')) {
        const chunk = src.slice(rule.chunkStart, rule.bodyStart - 1);
        let selector = (chunk.split(';').pop() ?? '').split('{').pop() ?? '';
        const tick = selector.lastIndexOf('`');
        if (tick !== -1) selector = selector.slice(tick + 1);
        selector = selector.trim();
        if (selector !== '') {
          // The line the SELECTOR starts on, not the line the block's text
          // starts on: the chunk carries the whitespace and the blanked
          // comment that sit between the previous rule and this one.
          const lead = rule.bodyStart - 1 - rule.chunkStart - chunk.replace(/^\s+/, '').length;
          const selStart = rule.chunkStart + lead;
          // selStart and open are indexes into the scanned source, which is
          // the raw source with comments blanked and interpolations removed,
          // both length preserving, so they are indexes into the raw source
          // too: the marker check reads the raw text of the selector.
          out.push({ selector, body, line: lineAt(selStart), selStart, open: rule.bodyStart - 1 });
        }
      }
      chunkStart = i + 1;
    }
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

/** True when the chunk reads as a selector and not as a piece of JavaScript
 *  that happens to sit before a brace (a test's `if (el.disabled === true) {`
 *  is the shape that must not reach the class tokens). A selector may hold
 *  `=` and quotes only inside brackets and parentheses; at the top level
 *  they are source code, not a selector. */
function plausibleSelector(selector) {
  let round = 0;
  let square = 0;
  let tokenStart = 0;
  const isCombinatorOrSpace = (ch) => ch === ' ' || ch === '>' || ch === '+' || ch === '~' || ch === ',';
  for (let i = 0; i < selector.length; i += 1) {
    const ch = selector[i];
    if (ch === '(') {
      // A top level opening paren in a selector opens a pseudo, so the
      // token it ends is one (:not, :is, :where, :has, :nth-child); an
      // `if (` or a call `foo(` is source code.
      if (round === 0 && square === 0) {
        const token = selector.slice(tokenStart, i);
        if (!/^::?[a-zA-Z-]+$/.test(token)) return false;
      }
      round += 1;
    } else if (ch === ')') {
      round = Math.max(0, round - 1);
      if (round === 0) tokenStart = i + 1;
    } else if (ch === '[') {
      square += 1;
    } else if (ch === ']') {
      square = Math.max(0, square - 1);
      if (square === 0) tokenStart = i + 1;
    } else if (round === 0 && square === 0) {
      if (ch === '=' || ch === "'" || ch === '"' || ch === ';') return false;
      if (isCombinatorOrSpace(ch)) tokenStart = i + 1;
    }
  }
  return true;
}

/** True when the selector styles a disabled state. Round 2 widened the
 *  reach: the .disabled class token is read in any compound position (a
 *  .btn.disabled is a disabled state), and .is-disabled joins it. A bare
 *  class that names no disabled state (.off, .held, .starting) is NOT
 *  matched here on purpose: the same text sits inside JavaScript property
 *  reads (gated.held, control.disabled) the scan cannot tell from a class,
 *  so those rules carry the hh-disabled-state marker instead, which is
 *  unambiguous and lives on the rule it marks. */
function isDisabledSelector(selector) {
  if (!plausibleSelector(selector)) return false;
  const s = stripNotGroups(selector);
  if (/:disabled\b/.test(s)) return true;
  if (/\[disabled\]/.test(s)) return true;
  if (/\[aria-disabled\s*=\s*["']?true/.test(s)) return true;
  if (/\.disabled(?![-\w=])/.test(s) && plausibleSelector(s)) return true;
  if (/\.is-disabled(?![-\w])/.test(s)) return true;
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
// The house has two positions for a disabled edge, and the token file names
// both: the base sheet's hairline (--hh-disabled-line, 1.51:1 on the charcoal
// card) and the kit's own findable edge (--hh-control-line, 3:1 on the card,
// which is what disabled borders wear in the kit's own style). A local rule
// that pins a disabled control's findability keeps the findable edge; either
// is the rule. A brand line is not.
const CONTROL_LINE = 'var(--hh-control-line)';

// A disabled rule may not paint its fill, ink or edge through a transparent
// mix: `color-mix(in srgb, var(--hh-disabled-fill) 45%, transparent)` names
// the token and is transparent anyway, so naming the token is not enough.
const TRANSPARENT_MIX = /color-mix\(|rgba\(|hsla\(|hsl\(|rgb\(|transparent/i;

// The marker a rule carries when its selector names no disabled state but
// the rule styles one (.result.off, .card.starting, .gl-choice-held): a
// bare-word class cannot be matched from the selector alone, because the
// same text sits inside JavaScript property reads the scan cannot tell from
// a class (gated.held, control.disabled). The marker lives inside the rule
// it marks, beside the declarations it guards, and nothing else in the tree
// may use these words.
const STATE_MARKER = 'hh-disabled-state';

/** A rule's selector as its comma-separated compound sequences, for the
 *  container check: whether a rule that fades or filters holds a disabled
 *  control inside it. */
function selectorCompounds(selector) {
  const s = stripNotGroups(selector);
  const out = [];
  for (const part of s.split(',')) {
    const comps = part.trim().split(/[\s>+~]+/).filter(Boolean);
    if (comps.length > 0) out.push(comps);
  }
  return out;
}

/** A compound with its disabled markers removed, so a container rule can be
 *  compared with the disabled rule that styles the same element. */
function stripDisabledFromCompound(comp) {
  return comp
    .replace(/:disabled\b/g, '')
    .replace(/\[disabled\]/g, '')
    .replace(/\[aria-disabled\s*=\s*["']?true["']?\]/g, '')
    .replace(/\.disabled(?![-\w=])/g, '')
    .replace(/\.is-disabled(?![-\w])/g, '');
}

/** The per-rule checks a disabled-matching rule must pass. */
function ruleOffenders(rel, rule, selector, decls) {
  const offenders = [];
  // A rule that styles the control's own surface states the pair, so
  // nothing can arrive through the cascade: a disabled rule that says
  // nothing about its fill leaves the fill to whichever rule wins, and
  // the winner is not always the base sheet. A rule that styles a piece
  // of the control (a pseudo-element) is not asked for a fill.
  const isPiece = /::[a-z-]+$/i.test(selector);
  if (!isPiece && !decls.some(([prop]) => prop === 'background' || prop === 'background-color')) {
    offenders.push(`${rel}:${rule.line} ${selector} -> no fill of its own`);
  }
  if (!isPiece && !decls.some(([prop]) => prop === 'color')) {
    offenders.push(`${rel}:${rule.line} ${selector} -> no ink of its own`);
  }
  for (const [prop, value] of decls) {
    if (prop === 'opacity') {
      const num = parseFloat(value);
      if (Number.isFinite(num) && num < 1) {
        offenders.push(`${rel}:${rule.line} ${selector} -> opacity: ${value}`);
      }
    } else if (prop === 'filter') {
      offenders.push(`${rel}:${rule.line} ${selector} -> filter: ${value}`);
    } else if (prop === 'background' || prop === 'background-color') {
      if (!value.includes(DISABLED_FILL) || TRANSPARENT_MIX.test(value)) {
        offenders.push(`${rel}:${rule.line} ${selector} -> ${prop}: ${value}`);
      }
    } else if (prop === 'color') {
      if (!value.includes(DISABLED_INK) || TRANSPARENT_MIX.test(value)) {
        offenders.push(`${rel}:${rule.line} ${selector} -> color: ${value}`);
      }
    } else if (prop === 'border' || prop === 'border-color') {
      if ((!value.includes(DISABLED_LINE) && !value.includes(CONTROL_LINE)) || TRANSPARENT_MIX.test(value)) {
        offenders.push(`${rel}:${rule.line} ${selector} -> ${prop}: ${value}`);
      }
    }
  }
  return offenders;
}

/** One source scanned: every rule read, the disabled-matching ones held to
 *  the swap, and every fade or filter checked for the disabled controls it
 *  would dim. Returns the offender lines. */
function scanSource(rel, raw) {
  // A file that never says "disabled" cannot carry a disabled rule, and the
  // tree is large: this pre-filter is what keeps the scan inside its budget.
  if (!/disabled/i.test(raw) && !raw.includes(STATE_MARKER)) return [];
  const src = stripInterpolations(blankComments(raw));
  const lineAt = lineIndexer(src);
  const read = [];
  for (const rule of ruleBlocks(src, lineAt)) {
    const rawRule = raw.slice(rule.selStart, rule.open + 1 + rule.body.length);
    const marked = rawRule.includes(STATE_MARKER) && plausibleSelector(rule.selector);
    read.push({ rule, selector: rule.selector.replace(/\s+/g, ' '), decls: declarations(rule.body), marked });
  }
  const offenders = [];
  const disabled = [];
  for (const { rule, selector, decls, marked } of read) {
    const match = marked || isDisabledSelector(rule.selector);
    if (!match) continue;
    disabled.push({ rule, selector });
    offenders.push(...ruleOffenders(rel, rule, selector, decls));
  }
  // The fade bypass a disabled selector cannot see: an ancestor that dims
  // the whole container. Where the lint can see the relationship (the
  // container's compounds are the leading compounds of a disabled rule's
  // selector, in the same sheet), the fade is refused: a disabled control
  // inside an opacity 0.35 wrapper is faded whatever its own rule says.
  for (const { rule, selector, decls } of read) {
    if (disabled.some((d) => d.rule === rule)) continue;
    if (!plausibleSelector(rule.selector)) continue;
    const dims = decls.some(
      ([prop, value]) =>
        prop === 'filter' || (prop === 'opacity' && Number.isFinite(parseFloat(value)) && parseFloat(value) < 1),
    );
    if (!dims) continue;
    for (const comps of selectorCompounds(selector)) {
      for (const d of disabled) {
        for (const dcomps of selectorCompounds(d.selector)) {
          const same =
            dcomps.map(stripDisabledFromCompound).filter((c) => c !== '').join('') === comps.join('') &&
            dcomps.length === comps.length;
          const ancestor =
            dcomps.length > comps.length && dcomps.slice(0, comps.length).join(' ') === comps.join(' ');
          if (same || ancestor) {
            offenders.push(`${rel}:${rule.line} ${selector} -> dims a container that holds a disabled control`);
          }
        }
      }
    }
  }
  return offenders;
}

test('no shipped style fades a disabled control, and no disabled rule keeps a fill of its own', { timeout: 300_000 }, () => {
  const offenders = [];
  for (const rel of webStyleFiles()) {
    offenders.push(...scanSource(rel, readFileSync(join(webRoot, rel), 'utf8')));
  }
  assert.deepEqual(
    offenders.sort(),
    [],
    `a shipped style breaks the disabled rule. A disabled control DROPS ITS FILL AND GOES GREY through\n` +
      `the --hh-disabled-* token swap: the fill is ${DISABLED_FILL}, the ink is ${DISABLED_INK}, and the\n` +
      `opacity is 1. A fade is not a state, and a disabled control never keeps a brand fill. The edge is\n` +
      `either the disabled line (${DISABLED_LINE}) or the kit's findable edge (${CONTROL_LINE});\n` +
      `a brand line is not. A fill, an ink or an edge may not arrive through a transparent mix\n` +
      `(color-mix, rgba, hsla, transparent), and no container that holds a disabled control may\n` +
      `carry an opacity below 1 or a filter:\n  ${offenders.join('\n  ')}`,
  );
});

// ── the bypass legs ─────────────────────────────────────────────────────────────
//
// Each leg feeds one source through the same scanSource the tree gate uses,
// with a scratch rule shaped like a bypass. A leg fails while the bypass
// passes the scan undetected, and passes once the scan names it. The
// ancestor-opacity and colour-mix scratches are the ones the reviewer's
// probe used against the round 1 gate.
const SWAP = `background: ${DISABLED_FILL}; color: ${DISABLED_INK}; border-color: ${DISABLED_LINE}; opacity: 1;`;

test('the gate names a transparent colour mix in a disabled rule', () => {
  const scratch = `
    .thing:disabled {
      background: color-mix(in srgb, ${DISABLED_FILL} 45%, transparent);
      color: ${DISABLED_INK};
      border-color: ${DISABLED_LINE};
      opacity: 1;
    }
  `;
  const found = scanSource('scratch-transparent-mix.css', scratch);
  assert.ok(
    found.some((line) => line.includes('color-mix')),
    `the transparent mix must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
});

test('the gate names a translucent rgba ink in a disabled rule', () => {
  const scratch = `
    .thing:disabled { ${SWAP.replace(`color: ${DISABLED_INK};`, 'color: rgba(81, 97, 111, 0.45);')} }
  `;
  const found = scanSource('scratch-rgba-ink.css', scratch);
  assert.ok(
    found.some((line) => line.includes('rgba')),
    `the translucent ink must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
});

test('the gate names an ancestor opacity that dims disabled controls', () => {
  const scratch = `
    .wrap { opacity: 0.35; }
    .wrap .thing:disabled { ${SWAP} }
  `;
  const found = scanSource('scratch-ancestor-opacity.css', scratch);
  assert.ok(
    found.some((line) => line.includes('dims a container')),
    `the ancestor fade must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
});

test('the gate names an ancestor filter that dims disabled controls', () => {
  const scratch = `
    .wrap { filter: grayscale(1); }
    .wrap .thing:disabled { ${SWAP} }
  `;
  const found = scanSource('scratch-ancestor-filter.css', scratch);
  assert.ok(
    found.some((line) => line.includes('dims a container')),
    `the ancestor filter must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
});

test('the gate names a fade on the same element the disabled rule styles', () => {
  const scratch = `
    .thing { opacity: 0.35; }
    .thing:disabled { ${SWAP} }
  `;
  const found = scanSource('scratch-same-element.css', scratch);
  assert.ok(
    found.some((line) => line.includes('dims a container')),
    `the element fade must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
});

test('the gate reaches a compound .disabled and a .btn.disabled, and .is-disabled', () => {
  const scratch = `
    button.disabled { ${SWAP.replace(`background: ${DISABLED_FILL};`, 'background: #c0ffee;')} }
    .btn.disabled { ${SWAP.replace(`background: ${DISABLED_FILL};`, 'background: #c0ffee;')} }
    .row.is-disabled { ${SWAP.replace(`background: ${DISABLED_FILL};`, 'background: #c0ffee;')} }
  `;
  const found = scanSource('scratch-compound-classes.css', scratch);
  assert.equal(found.filter((line) => line.includes('#c0ffee')).length, 3, `every compound class breach must be named: ${found.join(' | ')}`);
});

test('a bare-word state class is held through the marker, and .result.off cannot regress', () => {
  const clean = `
    .result.off { /* ${STATE_MARKER} */ ${SWAP} }
  `;
  assert.deepEqual(scanSource('scratch-marker-clean.css', clean), [], 'the marked clean swap must pass');
  const regressed = `
    .result.off { /* ${STATE_MARKER} */ background: ${DISABLED_FILL}; color: ${DISABLED_INK}; border-color: ${DISABLED_LINE}; opacity: 0.55; }
  `;
  const found = scanSource('scratch-marker-regressed.css', regressed);
  assert.ok(
    found.some((line) => line.includes('opacity: 0.55')),
    `the marked fade must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
  const unmarked = `
    .result.off { opacity: 0.55; background: ${DISABLED_FILL}; color: ${DISABLED_INK}; }
  `;
  assert.deepEqual(
    scanSource('scratch-unmarked.css', unmarked),
    [],
    'an unmarked bare-word class is not read as a disabled rule: the marker is what holds it',
  );
});

test('a clean swap and a hover companion pass the scan', () => {
  const scratch = `
    .thing:disabled { ${SWAP} }
    .thing:disabled:hover { ${SWAP} }
    .thing [disabled] { ${SWAP} }
  `;
  assert.deepEqual(scanSource('scratch-clean.css', scratch), [], `the clean shapes must pass: ${[]}`);
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
