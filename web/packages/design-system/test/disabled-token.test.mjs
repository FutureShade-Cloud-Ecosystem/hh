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
//      [disabled], an [aria-disabled="true"] or [aria-busy="true"] selector, a
//      compound .disabled or .is-disabled class, a class the templates show
//      is a disabled state, or a bare-word state class carrying the
//      hh-disabled-state marker) may not declare an opacity below 1 (a fade
//      is not a state), may not declare a filter, and may not declare a
//      fill, an ink or a line that is not the grey disabled triple or that
//      arrives through a transparent mix (color-mix, rgba, hsla). Every
//      *-color property, every background-image and every box-shadow on a
//      disabled rule is read against the same tokens. No container that
//      holds a disabled control may carry an opacity below 1 or a filter
//      where the lint can see the relationship. The app-local rule that faded
//      a gold button, or that kept the gold fill under a reduced opacity, is
//      caught here before anyone sees it.
//
//      ROUND 3 ADDITIONS. The marker was opt-in, so a new state class, or a
//      deleted marker, was invisible. Three things close that:
//
//        - THE PINS: the known marked rules (.result.off, .card.starting,
//          .gl-choice-held) are asserted to still exist, in their own file,
//          with their marker.
//        - THE TEMPLATE SCAN: a class that disables a control is found in
//          the markup, where the evidence is a class attribute and a
//          disabled binding and nothing else. The element's condition and
//          the class's condition must be the same one, in the same arm of
//          the same ternary, so a variant or a scope tab is not read as a
//          disabled state. A class the templates prove is a disabled state
//          is held to the swap whether or not it carries the marker.
//        - THE BUSY REACH: [aria-busy="true"] is a disabled state. A busy
//          control refuses input, and the house has one vocabulary for that.
//
//      A rule that styles a PIECE of a disabled control (a pseudo-element, a
//      descendant of a disabled control, a descendant of a control the marker
//      holds) is not asked for a fill and an ink of its own, but every colour
//      it states is one of the disabled tokens: a piece of a disabled control
//      never wears brand colour. That is the form the selection marks take,
//      so the gold dot inside a locked option is named here too.
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
      } else if (STYLE_EXT.test(entry) && rel !== 'packages/design-system/test/disabled-token.test.mjs') {
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
 *  .btn.disabled is a disabled state), and .is-disabled joins it. Round 3
 *  adds the busy reach: [aria-busy="true"] is a disabled state, because a
 *  busy control refuses input and the house has one vocabulary for that. A
 *  bare class that names no disabled state (.off, .held, .starting) is NOT
 *  matched here on purpose: the same text sits inside JavaScript property
 *  reads (gated.held, control.disabled) the scan cannot tell from a class,
 *  so those rules carry the hh-disabled-state marker instead, which is
 *  unambiguous and lives on the rule it marks, or are found by the template
 *  scan, which reads the class out of the markup. */
function isDisabledSelector(selector) {
  if (!plausibleSelector(selector)) return false;
  const s = stripNotGroups(selector);
  if (/:disabled\b/.test(s)) return true;
  if (/\[disabled\]/.test(s)) return true;
  if (/\[aria-disabled\s*=\s*["']?true/.test(s)) return true;
  if (/\[aria-busy\s*=\s*["']?true/.test(s)) return true;
  if (/\.disabled(?![-\w=])/.test(s) && plausibleSelector(s)) return true;
  if (/\.is-disabled(?![-\w])/.test(s)) return true;
  return false;
}

/** The class tokens a compound names, as a set. The tokens are whole: the
 *  class `held` is not the class `held-form`, and `gl-choice-held` is not
 *  `held`. */
function classTokens(compound) {
  const out = new Set();
  for (const m of compound.matchAll(/\.([a-zA-Z][\w-]*)/g)) out.add(m[1]);
  return out;
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

// The house brand colours. A disabled control, and every piece of one, wears
// the grey tokens: a brand fill, a brand ink or a brand edge in a disabled
// state is the defect the rule names. The accent family counts (the accent
// itself, its ink, its text), the brand family counts, and the till's own
// stone amber counts, because it is the same gold under another name.
const BRAND_COLOUR =
  /var\(--(?:hh-accent(?:-hover|-ink|-text)?|brand(?:-[a-z-]+)?|hos-stone-amber(?:-text)?)\b/;

// The marker a rule carries when its selector names no disabled state but
// the rule styles one (.result.off, .card.starting, .gl-choice-held): a
// bare-word class cannot be matched from the selector alone, because the
// same text sits inside JavaScript property reads the scan cannot tell from
// a class (gated.held, control.disabled). The marker lives inside the rule
// it marks, beside the declarations it guards, and nothing else in the tree
// may use these words.
const STATE_MARKER = 'hh-disabled-state';

// ── the template scan ───────────────────────────────────────────────────────────
//
// The marker is opt-in, so a state class nobody marked was invisible to the
// lint. The templates are where a state class can be FOUND, because there the
// evidence is a class attribute and a disabled binding, and nothing else: a
// class token read out of markup cannot be a JavaScript property read, which
// is the reason the stylesheet scan cannot use a bare class on its own.
//
// The rule the scan holds: an element that is disabled under a condition, and
// that carries a class toggled under that SAME condition, in that SAME arm of
// the same ternary, is a disabled state, and the class names it. The condition
// must match: a class that means something else (a variant, a scope tab, a
// flash) is not a disabled state and must not be read as one.

/** The index just past the balanced close of an interpolation that starts at
 *  src[i] === '$' && src[i + 1] === '{'. */
function interpolationEnd(src, i) {
  let depth = 0;
  let j = i + 1;
  while (j < src.length) {
    const ch = src[j];
    if (ch === '{') depth += 1;
    else if (ch === '}') {
      depth -= 1;
      if (depth === 0) return j + 1;
    } else if (ch === '"' || ch === "'" || ch === '`') {
      const q = ch;
      j += 1;
      while (j < src.length && src[j] !== q) {
        if (src[j] === '\\') j += 1;
        j += 1;
      }
    }
    j += 1;
  }
  return src.length;
}

/** Every markup tag in a source, as its text. */
function markupTags(src) {
  const out = [];
  let i = 0;
  while (i < src.length) {
    const lt = src.indexOf('<', i);
    if (lt === -1) break;
    if (!/[a-zA-Z]/.test(src[lt + 1] ?? '')) {
      i = lt + 1;
      continue;
    }
    let j = lt + 1;
    while (j < src.length) {
      const ch = src[j];
      if (ch === '"' || ch === "'") {
        const q = ch;
        j += 1;
        while (j < src.length && src[j] !== q) j += 1;
        j += 1;
        continue;
      }
      if (ch === '$' && src[j + 1] === '{') {
        j = interpolationEnd(src, j);
        continue;
      }
      if (ch === '>') break;
      j += 1;
    }
    out.push(src.slice(lt, j + 1));
    i = j + 1;
  }
  return out;
}

/** The value of every class attribute of a tag. */
function classAttributeValues(tag) {
  const out = [];
  const re = /class\s*=\s*/g;
  let m;
  while ((m = re.exec(tag)) !== null) {
    let i = m.index + m[0].length;
    if (tag[i] === '"' || tag[i] === "'") {
      const q = tag[i];
      let j = i + 1;
      while (j < tag.length) {
        if (tag[j] === '$' && tag[j + 1] === '{') {
          j = interpolationEnd(tag, j);
          continue;
        }
        if (tag[j] === q) break;
        j += 1;
      }
      out.push(tag.slice(i + 1, j));
      re.lastIndex = j + 1;
    } else if (tag[i] === '$' && tag[i + 1] === '{') {
      const end = interpolationEnd(tag, i);
      out.push(tag.slice(i + 2, end - 1));
      re.lastIndex = end;
    } else {
      const sp = tag.slice(i).search(/\s/);
      out.push(tag.slice(i, sp === -1 ? undefined : i + sp));
      re.lastIndex = i + 1;
    }
  }
  return out;
}

/** The string literals inside an expression. */
function stringLiterals(expr) {
  const out = [];
  const re = /"([^"]*)"|'([^']*)'/g;
  let m;
  while ((m = re.exec(expr)) !== null) out.push(m[1] ?? m[2] ?? '');
  return out;
}

/** The two arms of a ternary, split at the top level colon. */
function ternaryArms(expr) {
  const q = expr.indexOf('?');
  if (q === -1) return null;
  const cond = expr.slice(0, q).trim();
  const rest = expr.slice(q + 1);
  let depth = 0;
  for (let k = 0; k < rest.length; k += 1) {
    const ch = rest[k];
    if (ch === '(' || ch === '[' || ch === '{') depth += 1;
    else if (ch === ')' || ch === ']' || ch === '}') depth -= 1;
    else if (ch === ':' && depth === 0) {
      return { cond, then: rest.slice(0, k), else: rest.slice(k + 1) };
    }
  }
  return null;
}

/** The class tokens a class attribute value toggles, with the condition that
 *  carries them and the arm of the ternary they sit in. */
function conditionalClassTokens(value) {
  const out = [];
  let i = 0;
  while (i < value.length) {
    if (value[i] === '$' && value[i + 1] === '{') {
      const end = interpolationEnd(value, i);
      const expr = value.slice(i + 2, end - 1);
      const arms = ternaryArms(expr);
      if (arms) {
        for (const tok of stringLiterals(arms.then).join(' ').split(/\s+/)) {
          if (tok !== '') out.push({ tok, cond: arms.cond, arm: 'then' });
        }
        for (const tok of stringLiterals(arms.else).join(' ').split(/\s+/)) {
          if (tok !== '') out.push({ tok, cond: arms.cond, arm: 'else' });
        }
      }
      i = end;
      continue;
    }
    i += 1;
  }
  return out;
}

/** The disabled bindings of a tag: { cond, arm }, where the arm is the arm of
 *  the ternary that carries the disabled value. */
function disabledBindings(tag) {
  const out = [];
  const q = /\?disabled\s*=\s*\$\{/g;
  let m;
  while ((m = q.exec(tag)) !== null) {
    const end = interpolationEnd(tag, m.index + m[0].length - 2);
    out.push({ cond: tag.slice(m.index + m[0].length, end - 1).trim(), arm: 'then' });
  }
  const a = /aria-disabled\s*=\s*"\$\{/g;
  while ((m = a.exec(tag)) !== null) {
    const end = interpolationEnd(tag, m.index + m[0].length - 2);
    const expr = tag.slice(m.index + m[0].length, end - 1);
    const arms = ternaryArms(expr);
    if (!arms) continue;
    if (stringLiterals(arms.then).join(' ').includes('true')) out.push({ cond: arms.cond, arm: 'then' });
    if (stringLiterals(arms.else).join(' ').includes('true')) out.push({ cond: arms.cond, arm: 'else' });
  }
  return out;
}

/** The state classes a source proves are disabled states: the class tokens a
 *  template toggles on an element under the same condition that disables that
 *  element, in the same arm. */
function derivedStateClasses(raw) {
  const out = new Set();
  const src = blankComments(raw);
  for (const tag of markupTags(src)) {
    const binds = disabledBindings(tag);
    if (binds.length === 0) continue;
    for (const value of classAttributeValues(tag)) {
      for (const { tok, cond, arm } of conditionalClassTokens(value)) {
        for (const bind of binds) {
          if (bind.cond === cond && bind.arm === arm) out.add(tok);
        }
      }
    }
  }
  return out;
}

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

/** The edge colours a disabled rule may paint: every side of the border, the
 *  outline, the decoration and the column rule. Round 3 reads each one
 *  separately, because `border-color` being grey says nothing about
 *  `border-top-color`. */
const EDGE_COLOURS = new Set([
  'border-color',
  'border-top-color',
  'border-right-color',
  'border-bottom-color',
  'border-left-color',
  'outline-color',
  'text-decoration-color',
  'column-rule-color',
]);

/** True when a compound names a disabled state of its own. */
function carriesDisabledMarker(compound) {
  return /:disabled\b/.test(compound) ||
    /\[disabled\]/.test(compound) ||
    /\[aria-disabled\s*=\s*["']?true/.test(compound) ||
    /\[aria-busy\s*=\s*["']?true/.test(compound) ||
    /\.disabled(?![-\w=])/.test(compound) ||
    /\.is-disabled(?![-\w])/.test(compound);
}

/** A rule that styles a PIECE of a disabled control rather than the control
 *  itself: a pseudo-element, a descendant of a disabled control, or a
 *  descendant of a control the marker holds. Such a rule is not asked for a
 *  fill and an ink of its own, but every colour it states is one of the
 *  disabled tokens: a piece of a disabled control never wears brand colour.
 *  This is the form a selection mark takes inside a disabled control, so the
 *  gold dot in a locked option is named here. */
function isPieceOfDisabled(selector, markedSequences) {
  if (/::[a-z-]+$/i.test(selector)) return true;
  for (const seq of selectorCompounds(selector)) {
    for (let i = 0; i < seq.length - 1; i += 1) {
      if (carriesDisabledMarker(seq[i])) return true;
    }
    for (const marked of markedSequences) {
      if (seq.length > marked.length && marked.every((c, i) => c === seq[i])) return true;
    }
  }
  return false;
}

/** The per-rule checks a disabled-matching rule must pass. */
function ruleOffenders(rel, rule, selector, decls, isPiece) {
  const offenders = [];
  // A rule that styles the control's own surface states the pair, so
  // nothing can arrive through the cascade: a disabled rule that says
  // nothing about its fill leaves the fill to whichever rule wins, and
  // the winner is not always the base sheet. A rule that styles a piece
  // of the control (a pseudo-element, a descendant of the control) is not
  // asked for a fill.
  if (!isPiece && !decls.some(([prop]) => prop === 'background' || prop === 'background-color')) {
    offenders.push(`${rel}:${rule.line} ${selector} -> no fill of its own`);
  }
  if (!isPiece && !decls.some(([prop]) => prop === 'color')) {
    offenders.push(`${rel}:${rule.line} ${selector} -> no ink of its own`);
  }
  for (const [prop, value] of decls) {
    if (prop === 'opacity') {
      // The declared opacity is exactly 1 (or 100%). A value below 1 is a
      // fade, and a value the lint cannot read (var(--x), 50%) is a fade
      // the lint would wave through.
      const trimmed = value.trim();
      if (trimmed !== '1' && trimmed !== '100%') {
        offenders.push(`${rel}:${rule.line} ${selector} -> opacity: ${value}`);
      }
    } else if (prop === 'filter') {
      offenders.push(`${rel}:${rule.line} ${selector} -> filter: ${value}`);
    } else if (prop === 'background' || prop === 'background-color') {
      if (!value.includes(DISABLED_FILL) || TRANSPARENT_MIX.test(value)) {
        offenders.push(`${rel}:${rule.line} ${selector} -> ${prop}: ${value}`);
      }
    } else if (prop === 'background-image') {
      // A disabled control's fill is the flat grey token; a gradient or any
      // other image is a fill of its own.
      if (value.trim() !== 'none') {
        offenders.push(`${rel}:${rule.line} ${selector} -> ${prop}: ${value}`);
      }
    } else if (prop === 'color') {
      if (!value.includes(DISABLED_INK) || TRANSPARENT_MIX.test(value)) {
        offenders.push(`${rel}:${rule.line} ${selector} -> ${prop}: ${value}`);
      }
    } else if (prop === 'border' || EDGE_COLOURS.has(prop)) {
      // A piece of a disabled control is painted with the ink as well as the
      // line: a mark inside a locked control is a ring of the disabled ink,
      // and a ring of the disabled line. The control itself wears the line.
      const allowed = isPiece
        ? [DISABLED_LINE, CONTROL_LINE, DISABLED_INK]
        : [DISABLED_LINE, CONTROL_LINE];
      if (!allowed.some((token) => value.includes(token)) || TRANSPARENT_MIX.test(value)) {
        offenders.push(`${rel}:${rule.line} ${selector} -> ${prop}: ${value}`);
      }
    } else if (prop === 'box-shadow') {
      // A shadow is an edge too: no brand colour in it, and no translucent
      // mix. The disabled line inside a border is the mark the rule admits.
      if (BRAND_COLOUR.test(value) || TRANSPARENT_MIX.test(value)) {
        offenders.push(`${rel}:${rule.line} ${selector} -> ${prop}: ${value}`);
      }
    }
  }
  return offenders;
}

/** One source scanned: every rule read, the disabled-matching ones held to
 *  the swap, and every fade or filter checked for the disabled controls it
 *  would dim. Returns the offender lines.
 *
 *  `derived` is the set of class names the templates prove are disabled
 *  states. A rule that names one of them in its last compound is held to the
 *  swap whether or not it carries the marker: the markup is the evidence, and
 *  the marker is only the older way of saying so. */
function scanSource(rel, raw, derived = new Set()) {
  // A file that never says "disabled" cannot carry a disabled rule, and the
  // tree is large: this pre-filter is what keeps the scan inside its budget.
  if (!/disabled|aria-busy/i.test(raw) && !raw.includes(STATE_MARKER)) return [];
  const src = stripInterpolations(blankComments(raw));
  const lineAt = lineIndexer(src);
  const read = [];
  for (const rule of ruleBlocks(src, lineAt)) {
    const rawRule = raw.slice(rule.selStart, rule.open + 1 + rule.body.length);
    const marked = rawRule.includes(STATE_MARKER) && plausibleSelector(rule.selector);
    read.push({ rule, selector: rule.selector.replace(/\s+/g, ' '), decls: declarations(rule.body), marked });
  }
  // The marked rules first: a rule that extends one of them styles a piece
  // of the control the marker holds.
  const markedSequences = [];
  for (const { selector, marked } of read) {
    if (!marked) continue;
    for (const seq of selectorCompounds(selector)) markedSequences.push(seq);
  }
  const offenders = [];
  const disabled = [];
  for (const { rule, selector, decls, marked } of read) {
    const derivedMatch = derived.size > 0 && selectorCompounds(selector).some((seq) => {
      const last = seq[seq.length - 1];
      if (last === undefined) return false;
      for (const token of classTokens(last)) if (derived.has(token)) return true;
      return false;
    });
    const match = marked || derivedMatch || isDisabledSelector(rule.selector);
    if (!match) continue;
    const isPiece = isPieceOfDisabled(selector, markedSequences);
    disabled.push({ rule, selector });
    offenders.push(...ruleOffenders(rel, rule, selector, decls, isPiece));
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

/** The app a file belongs to: apps/<name> or packages/<name>. A state class
 *  is proved in the app that proves it, because the same class name means
 *  different things in different apps: money-desk's `held` is a control a
 *  person cannot take, and yard-pick's `held` is an amber note that says an
 *  order waits. A name is read where it was shown. */
function appRoot(rel) {
  const parts = rel.split('/');
  return parts.length >= 2 ? `${parts[0]}/${parts[1]}` : parts[0];
}

/** The class names the templates of the tree prove are disabled states, by
 *  app. The scan is a pass over the tree before the scan of the tree, because
 *  a template and the rule that paints the class it toggles do not always
 *  live in the same file. */
function treeDerivedStateClasses() {
  const byApp = new Map();
  for (const rel of webStyleFiles()) {
    const app = appRoot(rel);
    if (!byApp.has(app)) byApp.set(app, new Set());
    const set = byApp.get(app);
    for (const token of derivedStateClasses(readFileSync(join(webRoot, rel), 'utf8'))) set.add(token);
  }
  return byApp;
}

test('no shipped style fades a disabled control, and no disabled rule keeps a fill of its own', { timeout: 300_000 }, () => {
  const derived = treeDerivedStateClasses();
  const offenders = [];
  for (const rel of webStyleFiles()) {
    offenders.push(...scanSource(rel, readFileSync(join(webRoot, rel), 'utf8'), derived.get(appRoot(rel))));
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

// ── the pins ───────────────────────────────────────────────────────────────────
//
// The marker is opt-in, so a deleted marker was invisible: the rule it held
// would go back to a brand fill and nothing would say so. Each known marked
// rule is pinned to its own file, with its marker, so the pin fails the day
// the marker goes.
const PINS = [
  { rel: 'apps/pro-procurement/src/app-root.ts', selector: '.result.off' },
  { rel: 'apps/front-door/src/app-root.ts', selector: '.card.starting' },
  { rel: 'apps/money-desk/src/gl/gl-styles.ts', selector: '.gl-choice-held' },
];

test('the known marked rules still exist, in their own file, with their marker', () => {
  for (const pin of PINS) {
    const raw = readFileSync(join(webRoot, pin.rel), 'utf8');
    const src = stripInterpolations(blankComments(raw));
    const lineAt = lineIndexer(src);
    const hit = ruleBlocks(src, lineAt).find((rule) => {
      const selector = rule.selector.replace(/\s+/g, ' ');
      if (!selector.split(',').some((part) => part.trim() === pin.selector)) return false;
      const rawRule = raw.slice(rule.selStart, rule.open + 1 + rule.body.length);
      return rawRule.includes(STATE_MARKER) && plausibleSelector(rule.selector);
    });
    assert.ok(
      hit,
      `${pin.rel} must still hold ${pin.selector} with the ${STATE_MARKER} marker: the marker is what holds a\n` +
        `bare-word state class, and a rule that loses it goes back to a brand fill unseen.`,
    );
  }
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

// ── the round 3 legs ───────────────────────────────────────────────────────────
//
// The reviewer's two probes, and the value holes each one found. Every leg
// here feeds the reader's own shape through the same scanSource the tree gate
// uses, and asserts that the shape is NAMED.

test('the gate finds a new state class in the template, so a rule nobody marked is still named', () => {
  // The reader's first probe: a state class with no marker. A class that no
  // template uses cannot disable anything, so the evidence has to come from
  // the markup: the element is disabled under a condition, and the class is
  // toggled under that same condition, so the class IS the disabled state.
  const scratch = [
    'const view = html`<button class="wait ${busy ? "waiting" : ""}" ?disabled=${busy}>Wait</button>`;',
    'const styles = css`',
    '  .waiting { opacity: 0.4; background: var(--hh-accent); }',
    '`;',
  ].join('\n');
  const derived = derivedStateClasses(scratch);
  assert.ok(
    derived.has('waiting'),
    `the template scan must find the state class the markup toggles: it found ${[...derived].join(', ') || 'nothing'}`,
  );
  const found = scanSource('scratch-new-state-class.ts', scratch, derived);
  assert.ok(
    found.some((line) => line.includes('opacity: 0.4')),
    `the fade must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
  assert.ok(
    found.some((line) => line.includes('--hh-accent')),
    `the brand fill must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
});

test('the gate names a state class the template does not tie to a disabled binding', () => {
  // The other half of the reader's probe: a class toggled by a condition that
  // is NOT the one that disables the element means something else (a variant,
  // a scope tab), and must not be read as a disabled state.
  const scratch = [
    'const view = html`<button class="wait ${variant ? "waiting" : ""}" ?disabled=${busy}>Wait</button>`;',
    'const styles = css`',
    '  .waiting { opacity: 0.4; background: var(--hh-accent); }',
    '`;',
  ].join('\n');
  const derived = derivedStateClasses(scratch);
  assert.deepEqual([...derived], [], 'a class toggled by another condition is not a disabled state');
  assert.deepEqual(
    scanSource('scratch-unrelated-class.ts', scratch, derived),
    [],
    'and so its rule is not read as a disabled rule',
  );
});

test('the gate reads [aria-busy="true"] as a disabled state', () => {
  // The reader's second probe: a busy control keeps a brand fill and fades.
  const scratch = `
    .btn[aria-busy="true"] { opacity: .5; background: var(--hh-accent); }
  `;
  const found = scanSource('scratch-aria-busy.css', scratch);
  assert.ok(
    found.some((line) => line.includes('opacity: .5')),
    `the fade must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
  assert.ok(
    found.some((line) => line.includes('--hh-accent')),
    `the brand fill must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
});

test('the gate names an opacity it cannot read, and an opacity in per cent', () => {
  for (const value of ['50%', 'var(--hh-thing)']) {
    const scratch = `
      .thing:disabled { ${SWAP.replace('opacity: 1;', `opacity: ${value};`)} }
    `;
    const found = scanSource(`scratch-opacity-${value}.css`, scratch);
    assert.ok(
      found.some((line) => line.includes(`opacity: ${value}`)),
      `opacity ${value} must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
    );
  }
});

test('the gate names a brand colour in any *-color property of a disabled rule', () => {
  for (const prop of ['border-top-color', 'border-left-color', 'outline-color', 'text-decoration-color']) {
    const scratch = `
      .thing:disabled { ${SWAP.replace(`border-color: ${DISABLED_LINE};`, `border-color: ${DISABLED_LINE}; ${prop}: var(--hh-accent);`)} }
    `;
    const found = scanSource(`scratch-${prop}.css`, scratch);
    assert.ok(
      found.some((line) => line.includes(prop)),
      `the brand ${prop} must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
    );
  }
});

test('the gate names a background image and a brand box-shadow on a disabled rule', () => {
  const image = `
    .thing:disabled { ${SWAP.replace(`background: ${DISABLED_FILL};`, `background: ${DISABLED_FILL}; background-image: linear-gradient(90deg, var(--hh-accent), var(--hh-accent));`)} }
  `;
  const foundImage = scanSource('scratch-background-image.css', image);
  assert.ok(
    foundImage.some((line) => line.includes('background-image')),
    `the background image must be named, the scan saw only: ${foundImage.join(' | ') || 'nothing'}`,
  );
  const shadow = `
    .thing:disabled { ${SWAP.replace('opacity: 1;', 'box-shadow: 0 0 0 1px rgba(255, 195, 19, 0.8); opacity: 1;')} }
  `;
  const foundShadow = scanSource('scratch-box-shadow.css', shadow);
  assert.ok(
    foundShadow.some((line) => line.includes('box-shadow')),
    `the brand shadow must be named, the scan saw only: ${foundShadow.join(' | ') || 'nothing'}`,
  );
});

test('the gate names a brand colour in a piece of a disabled control', () => {
  // The form the fix takes: the mark inside a locked control is a rule of
  // its own, and a brand colour in it is the defect the rule names.
  const scratch = `
    .opt:disabled .dot { border: 6px solid var(--hh-accent); }
  `;
  const found = scanSource('scratch-piece-mark.css', scratch);
  assert.ok(
    found.some((line) => line.includes('--hh-accent')),
    `the brand mark must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
  const clean = `
    .opt:disabled .dot { border: 6px solid ${DISABLED_INK}; }
    .opt.is-on:disabled .dot { border: 6px solid ${DISABLED_INK}; }
  `;
  assert.deepEqual(
    scanSource('scratch-piece-mark-clean.css', clean),
    [],
    'a mark that takes the disabled ink must pass',
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

// ── the round 4 legs ────────────────────────────────────────────────────────────
//
// The three holes the round 3 review named, with the probe row that proves
// each one. Every probe below is a shape the round 3 gate PASSED, so against
// the round 3 gate each of these tests fails; against the fixed gate each one
// names the shape.
//
//   P2-A  the value leg: the fill, the ink and the edge were checked with
//         `includes`, so a value that carries the grey token BESIDE a brand
//         colour passed.
//   P2-B  the paint leg: the shorthands and the paint properties were never
//         read, so a brand edge, a brand glyph or a brand shadow passed.
//   P2-C  the template leg: the scan read one syntax, so a class set from a
//         variable, a classMap, a `disabled=` binding, a compound or a
//         negated condition, and an unquoted aria-disabled all passed.

/** A disabled rule with one declaration swapped for the probe's. */
const probeRule = (from, to) => `
  .thing:disabled {
    ${SWAP.replace(from, to)}
  }
`;

test('the value leg: a brand colour beside the grey token is named', () => {
  // The reader's three value probes. A value is not a search string: the
  // grey token must BE the fill, the ink or the edge, and no brand colour
  // may ride beside it.
  const probes = [
    {
      from: `background: ${DISABLED_FILL};`,
      to: 'background: var(--hh-accent), var(--hh-disabled-fill);',
    },
    {
      from: `border-color: ${DISABLED_LINE};`,
      to: 'border-color: var(--hh-accent) var(--hh-disabled-line);',
    },
    {
      from: `color: ${DISABLED_INK};`,
      to: 'color: var(--hh-accent, var(--hh-disabled-ink));',
    },
  ];
  for (const probe of probes) {
    const found = scanSource('scratch-value-leg.css', probeRule(probe.from, probe.to));
    assert.ok(
      found.some((line) => line.includes(probe.to)),
      `the value ${probe.to} must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
    );
  }
});

test('the paint leg: a brand colour is named in every paint property of a disabled rule', () => {
  // The reader's paint probes. The property does not matter: whatever a
  // disabled rule declares, a brand colour in its value is the defect.
  const probes = [
    { prop: 'border-bottom', value: '3px solid var(--hh-accent)' },
    { prop: 'border', value: '2px solid var(--hh-accent) var(--hh-disabled-line)' },
    { prop: 'outline', value: '2px solid var(--hh-accent)' },
    { prop: 'text-shadow', value: '0 0 4px var(--hh-accent)' },
    { prop: 'fill', value: 'var(--hh-accent)' },
    { prop: 'stroke', value: 'var(--hh-accent)' },
    { prop: 'accent-color', value: 'var(--hh-accent)' },
    { prop: 'backdrop-filter', value: 'blur(4px) drop-shadow(0 0 2px var(--hh-accent))' },
  ];
  for (const probe of probes) {
    const scratch = `
      .thing:disabled { ${SWAP} ${probe.prop}: ${probe.value}; }
    `;
    const found = scanSource('scratch-paint-leg.css', scratch);
    assert.ok(
      found.some((line) => line.includes(`${probe.prop}: ${probe.value}`)),
      `the brand ${probe.prop} must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
    );
  }
});

test('the template leg: the scan reaches the shapes that hold a disabled state', () => {
  // The reader's six template probes. Each one is a class that IS a disabled
  // state, written in a shape the round 3 scan could not tie to the disabled
  // binding.
  const probes = [
    {
      name: 'a class set from a variable',
      scratch: [
        'const c = busy ? "waiting" : "";',
        'const view = html`<button class="w ${c}" ?disabled=${busy}>Wait</button>`;',
      ].join('\n'),
    },
    {
      name: 'a classMap',
      scratch: 'const view = html`<button class=${classMap({ waiting: busy })} ?disabled=${busy}>Wait</button>`;',
    },
    {
      name: 'a disabled property binding',
      scratch: 'const view = html`<button class="w ${busy ? "waiting" : ""}" disabled=${busy}>Wait</button>`;',
    },
    {
      name: 'a compound condition',
      scratch: 'const view = html`<button class="w ${busy ? "waiting" : ""}" ?disabled=${busy || x}>Wait</button>`;',
    },
    {
      name: 'an unquoted aria-disabled',
      scratch:
        'const view = html`<button class="w ${busy ? "waiting" : ""}" aria-disabled=${busy ? "true" : "false"}>Wait</button>`;',
    },
    {
      name: 'a negated condition',
      scratch: 'const view = html`<button class="w ${ok ? "" : "waiting"}" ?disabled=${!ok}>Wait</button>`;',
    },
  ];
  for (const probe of probes) {
    const derived = derivedStateClasses(probe.scratch);
    assert.ok(
      derived.has('waiting'),
      `${probe.name}: the template scan must find the state class, it found ${[...derived].join(', ') || 'nothing'}`,
    );
  }
});

test('the template leg: a class the scan reaches is held to the disabled swap', () => {
  // The leg end to end: the class the markup proves is a disabled state, and
  // the rule that paints it.
  const scratch = [
    'const c = busy ? "waiting" : "";',
    'const view = html`<button class="w ${c}" ?disabled=${busy}>Wait</button>`;',
    'const styles = css`',
    '  .waiting { opacity: 0.4; background: var(--hh-accent); }',
    '`;',
  ].join('\n');
  const derived = derivedStateClasses(scratch);
  const found = scanSource('scratch-template-leg.ts', scratch, derived);
  assert.ok(
    found.some((line) => line.includes('opacity: 0.4')),
    `the fade must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
  assert.ok(
    found.some((line) => line.includes('--hh-accent')),
    `the brand fill must be named, the scan saw only: ${found.join(' | ') || 'nothing'}`,
  );
});

test('the template leg: a class that is on while the control is enabled is not a disabled state', () => {
  // The other half of the reader's probe: the class rides on a condition that
  // does not reach the disabled binding, so it means something else and must
  // not be read as a disabled state.
  const probes = [
    {
      name: 'another condition',
      scratch: 'const view = html`<button class="w ${variant ? "waiting" : ""}" ?disabled=${busy}>Wait</button>`;',
    },
    {
      name: 'the negation of the disabled condition',
      scratch: 'const view = html`<button class="w ${busy ? "waiting" : ""}" ?disabled=${!busy}>Wait</button>`;',
    },
    {
      name: 'a condition the disabled binding only sometimes reaches',
      scratch: 'const view = html`<button class="w ${busy ? "waiting" : ""}" ?disabled=${busy && x}>Wait</button>`;',
    },
  ];
  for (const probe of probes) {
    const derived = derivedStateClasses(probe.scratch);
    assert.deepEqual([...derived], [], `${probe.name}: this class is not a disabled state`);
  }
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
