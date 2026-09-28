// Shared machinery for the package gates: the palette parser (comments stripped
// FIRST: a token name next to a colon in this sheet's prose must never parse as
// a declaration), the source walker, the declared-pair scanner and the colour
// resolver. Ported from the ERP's _theme-tokens.test.mjs and contrast-check.mjs
// on the same terms they state for themselves: none of this resolves a cascade.
// Each check is a text fact or a dictionary lookup, and where a value is a tint
// or a translucent layer (color-mix, rgba, transparent) the resolver returns
// null and the pair is skipped rather than guessed at.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, relative } from 'node:path';
import { contrast, oklchToHex, parseOklch } from './color.mjs';

const here = dirname(fileURLToPath(import.meta.url));
export const pkgDir = join(here, '..', '..');
export const tokensPath = join(pkgDir, 'src', 'tokens.css');

export const readPkg = (p) => readFileSync(join(pkgDir, p), 'utf8');

export const stripComments = (css) => css.replace(/\/\*[\s\S]*?\*\//g, ' ');

/** Every `--name: value;` declaration in a block body, comments stripped first. */
export function parseDeclarations(body) {
  const out = {};
  for (const m of stripComments(body).matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/g)) out[m[1]] = m[2].trim();
  return out;
}

const grabBlock = (css, selector) => {
  const m = css.match(new RegExp(escapeRegExp(selector) + '\\s*\\{([\\s\\S]*?)\\}'));
  if (!m) throw new Error(`token block not found: ${selector}`);
  return m[1];
};

const escapeRegExp = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

export function sheetBlocks(css = readPkg('src/tokens.css')) {
  return {
    dark: grabBlock(css, ':root'),
    light: grabBlock(css, ':root[data-theme="light"]'),
    hhproDark: grabBlock(css, ':root[data-brand="hh-pro"]'),
    hhproLight: grabBlock(css, ':root[data-brand="hh-pro"][data-theme="light"]'),
  };
}

// THE PALETTES. Four, not two: the base vocabulary in each half, and each half
// WITH the hh-pro brand override applied. The contractor layer needs no fifth
// palette: --cbrand/--cbrand-on are declared on :root and resolve through
// whichever brand the surface carries, so their pairs are measured inside all
// four. An unknown name throws rather than falling back: a gate that answered a
// missing palette with the wrong palette would pass while measuring nothing.
export const PALETTE_NAMES = ['dark', 'light', 'hh-pro dark', 'hh-pro light'];

export function palettes(css = readPkg('src/tokens.css')) {
  const blocks = sheetBlocks(css);
  const dark = parseDeclarations(blocks.dark);
  const light = parseDeclarations(blocks.light);
  const hhproDark = parseDeclarations(blocks.hhproDark);
  const hhproLight = parseDeclarations(blocks.hhproLight);
  return {
    dark: { ...dark },
    light: { ...dark, ...light },
    'hh-pro dark': { ...dark, ...hhproDark },
    'hh-pro light': { ...dark, ...light, ...hhproDark, ...hhproLight },
  };
}

export function palette(name, css) {
  const all = palettes(css);
  if (!(name in all)) throw new Error(`unknown palette ${JSON.stringify(name)}; have ${Object.keys(all).join(', ')}`);
  return all[name];
}

/**
 * Resolves a declared value to a flat sRGB hex, following var() chains through
 * the palette and converting oklch. Returns null for anything it refuses to
 * guess at: color-mix tints, rgba layers, transparent, keywords it does not
 * know. A null pair is skipped by the contrast gate on purpose.
 */
export function resolveColor(value, vars) {
  let s = String(value).trim();
  for (let i = 0; i < 8; i++) {
    const m = s.match(/^var\(\s*(--[a-z0-9-]+)\s*(?:,\s*([\s\S]+))?\)$/);
    if (!m) break;
    s = (vars[m[1]] ?? m[2] ?? '').trim();
  }
  if (/^#[0-9a-f]{3}$/i.test(s)) return '#' + s.slice(1).split('').map((c) => c + c).join('').toLowerCase();
  if (/^#[0-9a-f]{6}$/i.test(s)) return s.toLowerCase();
  if (/^oklch\(/i.test(s)) {
    const o = parseOklch(s);
    return o ? oklchToHex(o) : null;
  }
  if (s === 'white') return '#ffffff';
  if (s === 'black') return '#000000';
  return null;
}

export { contrast };

// ── the source walker ────────────────────────────────────────────────────────────
// Everything that SHIPS a style or a template: src/** and harness/** over
// .ts/.css/.mjs/.html, excluding the tests (test/** and *.test.*) and the
// loader hook. The exclusion is documented at the phantom gate; nothing else is
// excluded, and the anchors below are a floor on discovery, not a ceiling.

const SHIPPED_ROOTS = ['src', 'harness'];
const SKIP_DIRS = new Set(['node_modules', 'dist', 'test']);
const SKIP_FILES = /(_ts-js-loader\.mjs|\.test\.|README\.md$)/;

export function shippedStyleFiles() {
  const out = [];
  const walk = (dir, prefix) => {
    for (const entry of readdirSync(dir)) {
      const rel = prefix ? `${prefix}/${entry}` : entry;
      const abs = join(pkgDir, rel);
      if (statSync(abs).isDirectory()) {
        if (SKIP_DIRS.has(entry)) continue;
        walk(abs, rel);
      } else if (/\.(ts|css|mjs|html)$/.test(entry) && !SKIP_FILES.test(entry)) {
        out.push(rel);
      }
    }
  };
  for (const root of SHIPPED_ROOTS) walk(join(pkgDir, root), root);
  return out.sort();
}

// ── the declared-pair scanner ────────────────────────────────────────────────────
// Reads every rule block in every shipped source that declares BOTH a color and
// a background, and reports them for the gate to resolve and measure. A block
// that declares only one of the two is not a pair; inherited colour is a
// cascade fact and is covered by the ink-on-surface matrix instead.

export function declaredPairs() {
  const pairs = [];
  for (const file of shippedStyleFiles()) {
    const src = readPkg(file);
    for (const m of src.matchAll(/\{([^{}]*)\}/g)) {
      const body = stripComments(m[1]);
      const grab = (prop) => {
        const d = body.match(new RegExp(`(?:^|[;\\s])${prop}\\s*:\\s*([^;]+)`));
        return d ? d[1].trim() : null;
      };
      const color = grab('color');
      const background = grab('background(?:-color)?');
      if (!color || !background) continue;
      pairs.push({ file, color, background, line: src.slice(0, m.index).split('\n').length });
    }
  }
  return pairs;
}

/** Every var() token reference in a file's raw text. */
export function referencedTokens(src) {
  const refs = new Set();
  for (const m of src.matchAll(/var\(\s*(--[a-z0-9-]+)/g)) refs.add(m[1]);
  return refs;
}

/** Every token DEFINED in tokens.css (declaration lines only, comments
 * stripped FIRST: this header itself names tokens next to colons, and a
 * definition that exists only inside a comment is not a definition, so a
 * phantom whose "definition" is commented out must still fail the gate). */
export function definedTokens() {
  const css = stripComments(readPkg('src/tokens.css'));
  const defined = new Set();
  for (const m of css.matchAll(/^\s*(--[a-z0-9-]+)\s*:/gm)) defined.add(m[1]);
  return defined;
}
