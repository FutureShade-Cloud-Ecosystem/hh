// THE BINDING-POSITION GATE's machinery. The rule the review rounds added to
// the owned renderer stands under the adopted substrate, enforced where the
// substrate changed it: at source, over every html and svg template literal in
// this package and in every package that depends on it.
//
// The scanner below is the one this package's src/render.ts carried at
// main's 81e6f689 (taken from git show 81e6f689:web/packages/design-system/
// src/render.ts, read only), pointed at source text where it used to run at
// render: it walks a template's static parts in the parser's own states (text,
// comment, tag, quoted value, unquoted value, raw text, and unknown, the
// sticky state for markup it cannot classify) and classifies the position of
// each binding. Static parts are taken as WRITTEN (lit parses strings.raw, so
// raw source text is what the template carries).
//
// THE ALLOW LIST, the owned renderer's, unchanged in kind:
//   text content, outside the elements whose content the browser reads as
//     code or raw text (script, style, textarea, title, xmp);
//   a quoted value in class, role, d, and the data- and aria- namespaces;
//   a WHOLE unquoted value in the data- and aria- namespaces only (the
//     attribute-omission form Lit needs for the nothing sentinel; the three
//     uses in the package today are data-action, aria-current, aria-label);
//   the boolean binding ?disabled, and no other ?name;
//   an address attribute (href, src, action, formaction, xlink:href, poster,
//     cite) only as the WHOLE value, and only through the package's one
//     address helper, hhAddress (src/address.ts), which checks the scheme at
//     run time: the gate names that helper as the only permitted source;
//   a property or event binding (Lit's . and @ forms): the allow table holds
//     exactly what the kits need today, which is NOTHING: none exists in the
//     package, so every . and @ binding is refused.
// Everything else is refused, naming the file, the line and the position, and
// a template the scanner cannot classify is refused with everything after it.
// The gate judges the POSITION, never the value: a fixture needs no payload.
//
// What the gate does not see, stated honestly: it finds templates by PATTERN
// and not by parsing. A template is extracted only where its tag is spelled
// html or svg in the source text, and the body is read by the same kind of
// walk, not by a parser: an expression the extractor cannot balance is
// refused rather than guessed at. That limit is why the two scans below
// exist: the tag introduction scan holds, for every file the gate walks, that
// a lit template tag cannot enter a file under any other spelling, and the
// tag usage scan (on the TypeScript compiler's own parser, which the package
// already carries for its typecheck) holds that an imported html or svg
// appears in exactly two places, its import statement and the tag position of
// a tagged template. Together the two scans make the pattern sufficient: the
// tag can only ever stand as html or svg in tag position, so the pattern
// above has already seen every template there is. A call whose value is a
// template
// (chip(...), icon(...), kitShell(...)) is judged at the call position
// (text), and the template it returns is audited where it is built, when the
// walk reaches the file that builds it. An expression that contains a nested
// html or svg template literal is judged recursively and its static text
// feeds the outer scanner, so a template composed inside a tag or a value is
// judged where it lands.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, isAbsolute, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
// The TypeScript compiler's own parser: the devDependency the package's
// typecheck already carries, so the two-place scan below walks the syntax
// tree the language's own parser builds. No new dependency.
import ts from 'typescript';

// ── the allow lists, from render.ts at 81e6f689 ─────────────────────────────────

const ADDRESS_ATTRIBUTES = new Set(['href', 'src', 'action', 'formaction', 'xlink:href', 'poster', 'cite']);
const EVENT_ATTRIBUTE = /^on[a-z]+$/i;
const NAME_BOUNDARY = /[\s=/>"'()]/;
const WHITESPACE = /\s/;

// Attribute VALUES the owned renderer refused outright: escaping cannot make a
// binding inside one safe (srcdoc is entity-decoded back into a document; the
// style attribute sets arbitrary CSS; the SVG animation attributes are decoded
// and applied to their target; object data loads its own document).
const REFUSED_VALUE_ATTRIBUTES = new Set(['srcdoc', 'style', 'values', 'to', 'from', 'data']);

const ALLOWED_VALUE_ATTRIBUTES = new Set(['class', 'role', 'd']);
const ALLOWED_VALUE_FAMILIES = ['data-', 'aria-'];

// Lit's boolean form: the one flag the kits compose, exactly.
const ALLOWED_BOOLEAN = 'disabled';

// The property and event bindings (Lit's . and @ forms) the kits need today:
// none. The table exists so an allowed one has a named home, and its emptiness
// is asserted by the gate's tests.
export const ALLOWED_PROPERTY_OR_EVENT = new Set([]);

// Elements whose content the browser reads as code or raw text: no binding may
// land there, text included.
const RAW_TEXT_ELEMENTS = new Set(['script', 'style', 'textarea', 'title', 'xmp']);
// script and style carry the parser's double-escape state (a comment inside
// keeps the body open past the first close tag); the others do not.
const RAW_COMMENT_SENSITIVE = new Set(['script', 'style']);

const isAllowedValueAttribute = (lowerName) =>
  ALLOWED_VALUE_ATTRIBUTES.has(lowerName) || ALLOWED_VALUE_FAMILIES.some((f) => lowerName.startsWith(f));

const isAllowedUnquoted = (lowerName) => ALLOWED_VALUE_FAMILIES.some((f) => lowerName.startsWith(f));

// The one address helper, by name: the only binding source the gate permits in
// an address attribute.
export const ADDRESS_HELPER = 'hhAddress';
const ADDRESS_CALL = /^\s*hhAddress\s*\(\s*([^(),]*?)\s*\)\s*$/;

// ── the scanner, ported from render.ts at 81e6f689 ──────────────────────────────

/** Where a binding sits, as the HTML parser would see it. Same states and same
 * names as the owned scanner's BindingContext. */
const makeScanner = () => {
  let region = 'text';
  let name = '';
  let quote = '';
  let valuePrefix = false;
  let raw = '';
  let tag = '';
  let tagEnded = false;
  let lastName = '';
  let eqPending = false;
  let eqName = '';

  const enterUnknown = () => {
    region = 'unknown';
  };

  const commentEnd = (text, from) => {
    const plain = text.indexOf('-->', from);
    const bang = text.indexOf('--!>', from);
    if (plain === -1 && bang === -1) return -1;
    if (plain === -1) return bang + 4;
    if (bang === -1) return plain + 3;
    return bang < plain ? bang + 4 : plain + 3;
  };

  const attributeNameEndingAt = (text, at) => {
    let i = at - 1;
    while (i >= 0 && WHITESPACE.test(text[i])) i--;
    if (i < 0 || text[i] !== '=') return '';
    i--;
    while (i >= 0 && WHITESPACE.test(text[i])) i--;
    const end = i;
    while (i >= 0 && !NAME_BOUNDARY.test(text[i])) i--;
    return text.slice(i + 1, end + 1);
  };

  const nameEndingBeforeEq = (text, eqAt) => {
    let i = eqAt - 1;
    while (i >= 0 && WHITESPACE.test(text[i])) i--;
    const end = i;
    while (i >= 0 && !NAME_BOUNDARY.test(text[i])) i--;
    return text.slice(i + 1, end + 1);
  };

  const feed = (text) => {
    const n = text.length;
    let i = 0;
    while (i < n) {
      const ch = text[i];
      if (region === 'unknown') return;
      if (region === 'text') {
        if (ch === '<') {
          if (text.startsWith('<!--', i)) {
            if (text.startsWith('<!-->', i)) { i += 5; continue; }
            if (text.startsWith('<!--->', i)) { i += 6; continue; }
            const end = commentEnd(text, i + 4);
            if (end === -1) { region = 'comment'; break; }
            i = end;
            continue;
          }
          if (text[i + 1] === '!' || text[i + 1] === '?') { enterUnknown(); continue; }
          region = 'tag';
          tag = '';
          tagEnded = text[i + 1] === '/';
          lastName = '';
          eqPending = false;
          eqName = '';
          quote = '';
          name = '';
          valuePrefix = false;
          i++;
        } else {
          i++;
        }
      } else if (region === 'comment') {
        const end = commentEnd(text, i);
        if (end === -1) break;
        region = 'text';
        i = end;
      } else if (region === 'tag') {
        if (ch === '>') {
          if (RAW_TEXT_ELEMENTS.has(tag)) {
            region = 'rawtext';
            raw = tag;
          } else {
            region = 'text';
          }
          tag = '';
          tagEnded = false;
          lastName = '';
          eqPending = false;
          eqName = '';
          quote = '';
          name = '';
        } else if (eqPending && (ch === '"' || ch === "'")) {
          name = attributeNameEndingAt(text, i) || eqName;
          if (name === '') { enterUnknown(); return; }
          quote = ch;
          region = 'quoted';
          valuePrefix = false;
          lastName = '';
          eqPending = false;
          eqName = '';
          i++;
        } else if (eqPending && !WHITESPACE.test(ch)) {
          name = eqName;
          if (name === '') { enterUnknown(); return; }
          region = 'unquoted';
          valuePrefix = true;
          lastName = '';
          eqPending = false;
          eqName = '';
          i++;
        } else if (ch === '=') {
          eqPending = true;
          eqName = nameEndingBeforeEq(text, i) || lastName;
          lastName = '';
          let j = i + 1;
          while (j < n && WHITESPACE.test(text[j])) j++;
          if (j >= n) {
            name = eqName;
            if (name === '') { enterUnknown(); return; }
            region = 'unquoted';
            valuePrefix = false;
            eqPending = false;
            eqName = '';
            break;
          }
          if (text[j] !== '"' && text[j] !== "'") {
            name = eqName;
            if (name === '') { enterUnknown(); return; }
            region = 'unquoted';
            valuePrefix = false;
            eqPending = false;
            eqName = '';
            i = j;
            continue;
          }
          i++;
        } else if (ch === '"' || ch === "'") {
          enterUnknown();
          return;
        } else if (ch === '/') {
          tagEnded = true;
          lastName = '';
          eqPending = false;
          eqName = '';
          i++;
        } else if (WHITESPACE.test(ch)) {
          // Whitespace ends the tag's own name: what follows are attributes.
          tagEnded = true;
          i++;
        } else if (NAME_BOUNDARY.test(ch)) {
          enterUnknown();
          return;
        } else {
          if (!tagEnded) tag += ch.toLowerCase();
          lastName = ch;
          eqPending = false;
          eqName = '';
          i++;
        }
      } else if (region === 'rawtext') {
        // The body of a raw-text element: the parser reads everything as the
        // element's content until the close tag's own name ends. Inside script
        // and style a comment opens the parser's double-escaped state, which
        // stays open past the first close tag: the scanner cannot follow it,
        // so it refuses from there on.
        const close = `</${raw}`;
        const probe = text.slice(i).toLowerCase();
        const commentOpen = RAW_COMMENT_SENSITIVE.has(raw) ? probe.indexOf('<!--') : -1;
        let at = -1;
        let from = 0;
        for (;;) {
          at = probe.indexOf(close, from);
          if (at === -1) break;
          const after = probe[at + close.length];
          if (after === undefined || WHITESPACE.test(after) || after === '>' || after === '/') break;
          from = at + 1;
        }
        if (commentOpen !== -1 && (at === -1 || commentOpen < at)) { enterUnknown(); return; }
        if (at === -1) { i = n; break; }
        region = 'tag';
        tag = '';
        tagEnded = true;
        lastName = '';
        eqPending = false;
        eqName = '';
        quote = '';
        name = '';
        i += at + close.length;
        continue;
      } else if (region === 'quoted') {
        if (ch === quote) {
          region = 'tag';
          quote = '';
          name = '';
          lastName = '';
          eqPending = false;
          eqName = '';
        } else {
          valuePrefix = true;
        }
        i++;
      } else {
        // unquoted value: ends at whitespace or the tag's '>'
        if (ch === '>' || WHITESPACE.test(ch)) {
          if (ch === '>') {
            if (RAW_TEXT_ELEMENTS.has(tag)) { region = 'rawtext'; raw = tag; } else { region = 'text'; }
            tag = '';
            tagEnded = false;
          } else {
            region = 'tag';
          }
          name = '';
          lastName = '';
          eqPending = false;
          eqName = '';
        } else {
          valuePrefix = true;
        }
        i++;
      }
    }
  };

  return {
    feed,
    context: () => ({ region, name, quote, valuePrefix, raw, tag }),
  };
};

// ── the template extractor: source text to static parts and bindings ────────────

/** Scans one template literal's body from its opening backtick. Returns the
 * chunks (static text as written, binding expression text) and the offset just
 * past the closing backtick. Throws 'unbalanced' when the source ends first,
 * which the audit reports as a template it cannot classify. */
/** Skips a ${...} expression, from the index OF the '$'. Returns the index
 * just past the closing brace. Handles nesting, strings, nested template
 * literals and comments; a `/` is ordinary text (no regex literals are
 * expected in template expressions, and one carrying a backtick or a brace
 * would desync the scan into an unbalanced refusal, which fails closed). */
const skipExpression = (source, dollarAt) => {
  let depth = 0;
  let j = dollarAt + 2; // past ${
  for (;;) {
    if (j >= source.length) throw new Error('unbalanced');
    const c = source[j];
    if (c === '\\') { j += 2; continue; }
    if (c === "'" || c === '"') {
      const q = c;
      j++;
      while (j < source.length && source[j] !== q) {
        if (source[j] === '\\') j++;
        j++;
      }
      j++;
      continue;
    }
    if (c === '`') {
      // A nested template literal: skip it wholesale; it is audited on its
      // own when the expression text is walked for nested tagged templates.
      j = skipTemplateLiteral(source, j);
      continue;
    }
    if (c === '/' && source[j + 1] === '/') {
      while (j < source.length && source[j] !== '\n') j++;
      continue;
    }
    if (c === '/' && source[j + 1] === '*') {
      const end = source.indexOf('*/', j + 2);
      j = end === -1 ? source.length : end + 2;
      continue;
    }
    if (c === '{' || c === '(' || c === '[') { depth++; j++; continue; }
    if (c === '}' || c === ')' || c === ']') {
      if (depth === 0 && c === '}') return j + 1;
      depth--;
      j++;
      continue;
    }
    j++;
  }
};

/** Skips a template literal, from the index OF its opening backtick. Returns
 * the index just past the closing backtick. */
const skipTemplateLiteral = (source, at) => {
  let i = at + 1;
  for (;;) {
    if (i >= source.length) throw new Error('unbalanced');
    const c = source[i];
    if (c === '\\') { i += 2; continue; }
    if (c === '$' && source[i + 1] === '{') {
      i = skipExpression(source, i);
      continue;
    }
    if (c === '`') return i + 1;
    i++;
  }
};

const scanTemplateBody = (source, at) => {
  const chunks = [];
  let staticStart = at;
  let i = at;
  const pushStatic = (end) => {
    if (end > staticStart) chunks.push({ type: 'static', text: source.slice(staticStart, end) });
  };
  for (;;) {
    if (i >= source.length) throw new Error('unbalanced');
    const c = source[i];
    if (c === '\\') { i += 2; continue; }
    if (c === '$' && source[i + 1] === '{') {
      pushStatic(i);
      const end = skipExpression(source, i);
      chunks.push({ type: 'binding', text: source.slice(i + 2, end - 1), start: i + 2 });
      i = end;
      staticStart = i;
      continue;
    }
    if (c === '`') {
      pushStatic(i);
      return { chunks, end: i + 1 };
    }
    i++;
  }
};

/** Every html or svg tagged template literal in the source: tag, chunks, and
 * the absolute offsets the line numbers come from. A template whose body the
 * extractor cannot balance is reported as unclassifiable, not skipped. */
export const extractTemplates = (source) => {
  const out = [];
  const tagged = /(?:^|[^\w.$])(html|svg)`/g;
  let m;
  while ((m = tagged.exec(source)) !== null) {
    const backtick = m.index + m[0].length - 1;
    let body;
    try {
      body = scanTemplateBody(source, backtick + 1);
    } catch {
      const line = source.slice(0, backtick).split('\n').length;
      out.push({ tag: m[1], line, unclassifiable: true, chunks: [] });
      // Resume past this occurrence so one broken template does not loop the scan.
      tagged.lastIndex = backtick + 1;
      continue;
    }
    const line = source.slice(0, backtick).split('\n').length;
    out.push({ tag: m[1], line, chunks: body.chunks, end: body.end });
    tagged.lastIndex = body.end - 1;
  }
  return out;
};

// ── the tag's introduction: the import rules that keep the pattern honest ───────
//
// The extractor sees a template only when its tag is spelled html or svg, so
// the gate refuses, in every file it walks, every module statement that could
// put a lit template tag under a name the pattern does not see. Four rules,
// each refused where it happens, and each about lit, lit-html, or any subpath
// of either:
//   1. an aliased named import: html or svg brought in under another local
//      name;
//   2. a namespace import, a default import, a dynamic import and a require
//      of those packages: each carries bindings the source cannot name, and
//      a require whose specifier is not a string literal is refused the same
//      way, because it could be assembled to name them;
//   3. mathml is refused outright, own name or aliased. No kit needs it, so
//      the extractor carries no third tag: with the import refused, no mathml
//      template can exist to be missed, and a future kit that wants one must
//      argue the import here, by name, instead of growing the pattern in
//      secret;
//   4. a re-export of one of the three tags from any file of the package,
//      renamed or not, star or default: a re-export hands the tag to a second
//      file, which can alias it again at its own import of THIS package,
//      where none of these rules look. This is why the package's own surface
//      exports no tag (src/index.ts exports css, never html).
// What this scan does not see, the usage scan below holds instead: a tag
// value copied through local code is a value flow, not a statement, and no
// scan of statements can follow it. The usage scan, on the TypeScript
// compiler's own parser, refuses every appearance of an imported html or svg
// outside its two places (the import statement and the tag position of a
// tagged template), so the copy cannot exist to be missed. That rule is what
// makes the template pattern sufficient: if the tag can only ever appear as
// html or svg in tag position, the pattern sees every template there is.

const TAG_NAMES = new Set(['html', 'svg', 'mathml']);

const isLitCoreSpecifier = (spec) =>
  spec === 'lit' || spec === 'lit-html' || spec.startsWith('lit/') || spec.startsWith('lit-html/');

// Words after which a slash opens a regular expression instead of dividing.
const REGEX_KEYWORDS = new Set([
  'return', 'typeof', 'instanceof', 'in', 'of', 'new', 'delete', 'void',
  'case', 'do', 'else', 'yield', 'await', 'throw',
]);

const isWordChar = (c) => /[A-Za-z0-9_$]/.test(c);

/** One pass over the source: for every character, whether it is code a module
 * statement could start at. Comments, string and template literals and regex
 * literals are not, and they are recorded as tokens, so a statement can be
 * read with its string contents and without its comments. The regex versus
 * division call is the classic look behind (a slash after a value, a closing
 * bracket, or a plain word divides; anywhere else it opens a literal), which
 * is a heuristic and can misread contrived code; the walk reports where it
 * could not finish, and the audit refuses the file there instead of guessing. */
const markCode = (source) => {
  const n = source.length;
  const code = new Array(n).fill(false);
  const tokens = [];
  let unterminated = null;
  // The region stack: code, or the text of a template literal. An expression
  // inside a template pushes a code region of its own, which ends at the
  // matching close brace.
  const stack = [{ template: false, depth: 0, start: -1 }];
  let prev = 'start';
  let word = '';
  let i = 0;
  while (i < n) {
    const top = stack[stack.length - 1];
    const c = source[i];
    if (top.template) {
      if (c === '\\') { i += 2; continue; }
      if (c === '`') {
        tokens.push({ start: top.start, end: i + 1, kind: 'template' });
        stack.pop();
        prev = 'value';
        i++;
        continue;
      }
      if (c === '$' && source[i + 1] === '{') {
        stack.push({ template: false, depth: 0, start: -1 });
        i += 2;
        continue;
      }
      i++;
      continue;
    }
    if (c === '/' && source[i + 1] === '/') {
      const end = source.indexOf('\n', i + 2);
      i = end === -1 ? n : end;
      continue;
    }
    if (c === '/' && source[i + 1] === '*') {
      const end = source.indexOf('*/', i + 2);
      if (end === -1) { if (unterminated === null) unterminated = i; break; }
      i = end + 2;
      continue;
    }
    if (c === "'" || c === '"') {
      const start = i;
      i++;
      while (i < n && source[i] !== c) {
        if (source[i] === '\\') i++;
        i++;
      }
      if (i >= n) { if (unterminated === null) unterminated = start; break; }
      tokens.push({ start, end: i + 1, kind: 'string' });
      prev = 'value';
      i++;
      continue;
    }
    if (c === '`') {
      stack.push({ template: true, depth: 0, start: i });
      i++;
      continue;
    }
    if (c === '/') {
      const divides =
        (prev === 'word' && !REGEX_KEYWORDS.has(word)) ||
        prev === 'value' || prev === ')' || prev === ']' || prev === '}';
      if (!divides) {
        // A regex literal: read to its closing slash, quotes and all.
        const start = i;
        let j = i + 1;
        let inClass = false;
        let closed = false;
        while (j < n) {
          const r = source[j];
          if (r === '\\') { j += 2; continue; }
          if (r === '\n') break;
          if (inClass) {
            if (r === ']') inClass = false;
            j++;
            continue;
          }
          if (r === '[') { inClass = true; j++; continue; }
          if (r === '/') { closed = true; j++; break; }
          j++;
        }
        if (closed) {
          while (j < n && isWordChar(source[j])) j++;
          tokens.push({ start, end: j, kind: 'regex' });
          i = j;
          prev = 'value';
          continue;
        }
        // Not a regex after all: an ordinary slash.
        code[i] = true;
        prev = '/';
        i++;
        continue;
      }
      code[i] = true;
      prev = '/';
      i++;
      continue;
    }
    if (isWordChar(c)) {
      code[i] = true;
      let j = i + 1;
      while (j < n && isWordChar(source[j])) { code[j] = true; j++; }
      prev = 'word';
      word = source.slice(i, j);
      i = j;
      continue;
    }
    if (c === '{') { top.depth++; code[i] = true; prev = '{'; i++; continue; }
    if (c === '}') {
      if (stack.length > 1 && top.depth === 0) {
        stack.pop();
        i++;
        continue;
      }
      top.depth--;
      code[i] = true;
      prev = '}';
      i++;
      continue;
    }
    if (!/\s/.test(c)) { code[i] = true; prev = c; }
    i++;
  }
  if (stack.length > 1 && unterminated === null) unterminated = stack[stack.length - 1].start;
  return { code, tokens, unterminated };
};

const wordStarts = (source, i, word) => {
  if (!source.startsWith(word, i)) return false;
  const after = source[i + word.length];
  if (after !== undefined && isWordChar(after)) return false;
  const before = i > 0 ? source[i - 1] : undefined;
  if (before !== undefined && (isWordChar(before) || before === '.')) return false;
  return true;
};

/** The module statements: every import, export and require keyword the mask
 * calls code, with its clause read to the terminating semicolon. The clause
 * is read from a readable copy of the source in which every character that is
 * not code is a space, so words keep their boundaries and offsets keep their
 * positions; a string literal stays verbatim (a specifier is read out of one),
 * a template literal keeps only its backticks, and a regex literal its
 * opening slash (their contents say nothing about a statement). */
const moduleStatements = (source, marked) => {
  const { code, tokens } = marked;
  const readable = source.split('');
  const inString = new Array(source.length).fill(false);
  for (const t of tokens) {
    if (t.kind === 'string') {
      for (let k = t.start; k < t.end; k++) inString[k] = true;
    } else if (t.kind === 'template') {
      for (let k = t.start + 1; k < t.end - 1; k++) readable[k] = ' ';
    } else {
      readable[t.start] = '/';
      for (let k = t.start + 1; k < t.end; k++) readable[k] = ' ';
    }
  }
  for (let k = 0; k < readable.length; k++) {
    if (!code[k] && !inString[k]) readable[k] = ' ';
  }
  const statements = [];
  const n = source.length;
  let i = 0;
  while (i < n) {
    if (!code[i]) { i++; continue; }
    let kind = null;
    for (const candidate of ['import', 'export', 'require']) {
      if (wordStarts(source, i, candidate)) { kind = candidate; break; }
    }
    if (kind === null) { i++; continue; }
    const line = source.slice(0, i).split('\n').length;
    let text = kind;
    let depth = 0;
    let j = i + kind.length;
    let terminated = false;
    while (j < n) {
      text += readable[j];
      if (code[j]) {
        const ch = source[j];
        if (ch === '(' || ch === '[' || ch === '{') depth++;
        else if (ch === ')' || ch === ']' || ch === '}') depth--;
        else if (ch === ';' && depth <= 0) { j++; terminated = true; break; }
      }
      j++;
    }
    statements.push({ kind, text, line });
    if (!terminated) break;
    i = j;
  }
  return statements;
};

const firstNonSpace = (text, from) => {
  let k = from;
  while (k < text.length && /\s/.test(text[k])) k++;
  return k;
};

const matchParen = (text, openAt) => {
  let depth = 0;
  for (let i = openAt; i < text.length; i++) {
    if (text[i] === '(') depth++;
    else if (text[i] === ')') { depth--; if (depth === 0) return i; }
  }
  return -1;
};

const braceSpan = (text, openAt) => {
  let depth = 0;
  for (let i = openAt; i < text.length; i++) {
    if (text[i] === '{') depth++;
    else if (text[i] === '}') { depth--; if (depth === 0) return i; }
  }
  return -1;
};

/** The string literal occupying the whole of arg (a quote, contents, and
 * nothing else), or null. */
const readWholeString = (arg) => {
  const q = arg[0];
  if (q !== "'" && q !== '"') return null;
  let j = 1;
  while (j < arg.length && arg[j] !== q) {
    if (arg[j] === '\\') j++;
    j++;
  }
  if (j >= arg.length) return null;
  if (arg.slice(j + 1).trim() !== '') return null;
  return arg.slice(1, j);
};

/** The from clause of a statement: the first from word at bracket depth zero
 * and outside string literals, whose specifier follows it. Returns null when
 * there is no from, { spec, at } when it reads, and a marker when it does
 * not, which the audit refuses rather than guesses at. */
const fromSpecifier = (text) => {
  let depth = 0;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === "'" || c === '"') {
      const q = c;
      i++;
      while (i < text.length && text[i] !== q) {
        if (text[i] === '\\') i++;
        i++;
      }
      continue;
    }
    if (c === '(' || c === '[' || c === '{') { depth++; continue; }
    if (c === ')' || c === ']' || c === '}') { depth--; continue; }
    if (c === 'f' && depth === 0 && text.startsWith('from', i)) {
      const before = i > 0 ? text[i - 1] : '';
      const after = text[i + 4];
      if (isWordChar(before) || (after !== undefined && isWordChar(after))) continue;
      const k = firstNonSpace(text, i + 4);
      if (k >= text.length) continue;
      if (text[k] === '`') return { unquoteable: true };
      const spec = wholeStringAfter(text, k);
      if (spec === null) return { bare: true };
      return { spec, at: i };
    }
  }
  return null;
};

/** The string literal starting at from (a quote), or null. */
const wholeStringAfter = (text, from) => {
  const q = text[from];
  if (q !== "'" && q !== '"') return null;
  let j = from + 1;
  while (j < text.length && text[j] !== q) {
    if (text[j] === '\\') j++;
    j++;
  }
  if (j >= text.length) return null;
  return text.slice(from + 1, j);
};

/** An import or export clause's item list, split on top level commas. A
 * leading type marks a type only item, which carries no run time binding.
 * Returns null for an item the scanner cannot read, which refuses. */
const parseItemList = (inner) => {
  const parts = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < inner.length; i++) {
    const c = inner[i];
    if (c === '(' || c === '[' || c === '{') depth++;
    else if (c === ')' || c === ']' || c === '}') depth--;
    else if (c === ',' && depth === 0) { parts.push(inner.slice(start, i)); start = i + 1; }
  }
  parts.push(inner.slice(start));
  const items = [];
  for (const raw of parts) {
    let t = raw.trim();
    if (t === '') continue;
    let isType = false;
    if (t.length > 4 && /^type\s/.test(t)) { isType = true; t = t.slice(4).trim(); }
    const m = /^([\w$]+)(\s+as\s+([\w$]+))?$/.exec(t);
    if (!m) return null;
    items.push({ name: m[1], alias: m[3] ?? null, type: isType });
  }
  return items;
};

/** The four introduction rules, against one source file's module statements.
 * Returns offences in the shape the template audit returns, so the gates and
 * their fixtures treat both halves alike. */
export const auditTagIntroductions = (source, file) => {
  const offences = [];
  const at = (line, position, rule) => offences.push({ file, line, position, rule });
  const marked = markCode(source);
  if (marked.unterminated !== null) {
    at(
      source.slice(0, marked.unterminated).split('\n').length,
      'the module statements',
      `a string, a comment or a template literal that never closes leaves the module statements unreadable, so the file is refused (the gate fails closed)`,
    );
    return offences;
  }
  for (const { kind, text, line } of moduleStatements(source, marked)) {
    if (kind === 'require') {
      const open = text.indexOf('(');
      if (open === -1) continue;
      const closeAt = matchParen(text, open);
      if (closeAt === -1) {
        at(line, 'a require', 'the argument list cannot be parsed, so it is refused (the gate fails closed)');
        continue;
      }
      const spec = readWholeString(text.slice(open + 1, closeAt).trim());
      if (spec === null) {
        at(line, 'a require whose specifier is not a string literal', 'the specifier could be assembled to name the lit packages, so it is refused (the gate fails closed)');
        continue;
      }
      if (isLitCoreSpecifier(spec)) {
        at(line, `a require of ${spec}`, `a require puts the module behind whatever name the calling code chooses, so a tag could arrive under any spelling`);
      }
      continue;
    }
    if (kind === 'import') {
      const k = firstNonSpace(text, 6);
      if (text[k] === '(') {
        const closeAt = matchParen(text, k);
        if (closeAt === -1) {
          at(line, 'a dynamic import', 'the argument list cannot be parsed, so it is refused (the gate fails closed)');
          continue;
        }
        const spec = readWholeString(text.slice(k + 1, closeAt).trim());
        if (spec !== null && isLitCoreSpecifier(spec)) {
          at(line, `a dynamic import of ${spec}`, 'the bindings a dynamic import produces are not in the source, so the gate cannot judge what names a tag arrives under');
        }
        continue;
      }
      if (wordStarts(text, k, 'type')) continue;
      const from = fromSpecifier(text);
      if (from === null) continue;
      if (from.unquoteable || from.bare) {
        at(line, 'a module specifier the scanner cannot read', 'the specifier is not a plain string literal, so it could be assembled to name the lit packages (the gate fails closed)');
        continue;
      }
      if (!isLitCoreSpecifier(from.spec)) continue;
      const clause = text.slice(k, from.at);
      if (clause.includes('*')) {
        at(line, `a namespace import from ${from.spec}`, 'everything the module exports arrives at once, so a tag can be read off the namespace object under a property access the template pattern never sees');
        continue;
      }
      const first = clause[firstNonSpace(clause, 0)];
      if (first !== undefined && isWordChar(first)) {
        at(line, `a default import from ${from.spec}`, 'a default binding carries whatever the module chose, and a tag behind it would wear any local name');
        continue;
      }
      const open = clause.indexOf('{');
      const close = open === -1 ? -1 : braceSpan(clause, open);
      if (open !== -1 && close !== -1) {
        const items = parseItemList(clause.slice(open + 1, close));
        if (items === null) {
          at(line, 'an import clause item the scanner cannot read', 'the clause item cannot be read, so the import is refused (the gate fails closed)');
          continue;
        }
        for (const item of items) {
          if (item.type) continue;
          if (item.name === 'mathml') {
            at(line, 'an import of mathml', 'mathml is refused outright: no kit needs it, so the template pattern carries no third tag, and refusing the import is what keeps a mathml template from existing at all');
          } else if (TAG_NAMES.has(item.name) && item.alias !== null && item.alias !== item.name) {
            at(line, `an import of ${item.name} under the name ${item.alias}`, `the gate finds templates by the tag's spelling, so a lit template tag may not enter a file under another name: import it under its own name or not at all`);
          }
        }
      }
      continue;
    }
    // export
    const k = firstNonSpace(text, 6);
    if (wordStarts(text, k, 'default')) {
      const operand = text.slice(k + 7).trim().replace(/;+$/, '').trim();
      if (TAG_NAMES.has(operand)) {
        at(line, `a default re-export of ${operand}`, `the module's default export is imported under any name the importer chooses, so a tag must not travel on it`);
      }
      continue;
    }
    if (text[k] === '*') {
      const from = fromSpecifier(text);
      if (from === null) {
        at(line, 'an export the scanner cannot read', 'a star export with no readable source is refused (the gate fails closed)');
        continue;
      }
      if (from.unquoteable || from.bare) {
        at(line, 'a module specifier the scanner cannot read', 'the specifier is not a plain string literal, so it could be assembled to name the lit packages (the gate fails closed)');
        continue;
      }
      if (isLitCoreSpecifier(from.spec)) {
        at(line, `a star re-export from ${from.spec}`, 'a star re-export hands on everything the module exports, the three tags included, under whatever names importers choose');
      }
      continue;
    }
    if (text[k] === '{') {
      const close = braceSpan(text, k);
      if (close === -1) {
        at(line, 'an export clause the scanner cannot read', 'the clause does not close, so it is refused (the gate fails closed)');
        continue;
      }
      const items = parseItemList(text.slice(k + 1, close));
      if (items === null) {
        at(line, 'an export clause item the scanner cannot read', 'the clause item cannot be read, so the export is refused (the gate fails closed)');
        continue;
      }
      const from = fromSpecifier(text);
      if (from !== null && (from.unquoteable || from.bare)) {
        at(line, 'a module specifier the scanner cannot read', 'the specifier is not a plain string literal, so it could be assembled to name the lit packages (the gate fails closed)');
        continue;
      }
      if (from !== null && isLitCoreSpecifier(from.spec)) {
        for (const item of items) {
          if (item.type) continue;
          const tag = TAG_NAMES.has(item.name)
            ? item.name
            : item.alias !== null && TAG_NAMES.has(item.alias) ? item.alias : null;
          if (tag !== null) {
            at(line, `a re-export of ${tag} from ${from.spec}`, `a re-export hands the tag to a second file, which can alias it again at its own import of this package, where none of these rules look: the package's surface carries no tag`);
          }
        }
      } else if (from === null) {
        for (const item of items) {
          if (item.type) continue;
          if (TAG_NAMES.has(item.name)) {
            at(line, `a re-export of the local ${item.name} binding`, `this file holds the tag under its own name and hands it on, renamed or not; a second file may alias it at the import, so the hand off is refused`);
          }
        }
      }
    }
    // Any other export form is a declaration, which introduces no tag spelling.
  }
  return offences;
};

// ── the tag's two places: the parser rule that makes the pattern sufficient ─────
//
// The introduction scan reads module statements; a tag value copied through
// local code is a value flow, not a statement, and no scan of statements can
// follow it. This rule is decided where every copy must begin, at the
// identifier, on the TypeScript compiler's own parser (the devDependency the
// package's typecheck already carries; no new dependency): in every file of
// the package, an imported html or svg may appear in exactly two places, its
// import statement and the tag position of a tagged template. Any other
// appearance is refused, with the file and the line: an assignment, an
// argument, a return, a property, an export, a spread, a typeof, a comma
// expression, anything else.
//
//   1. The two places: an import specifier, and the tag of a tagged template
//      expression.
//   2. Shadowing: a local declaration named html or svg, in any scope of a
//      scanned file, is refused, so that the name always means the import.
//   3. A member LABEL is not an appearance of the name: x.html, { html: 1 },
//      a class field or method named html, the key of a destructuring
//      renamed on purpose ({ html: h }). A label neither reads nor binds the
//      lexical name. A shorthand property ({ html }) is NOT a label: it reads
//      the binding, and it is refused.
//   4. The walk: every .ts, .mts, .js and .mjs under the package's directory,
//      the package root included, leaving out only node_modules, the build
//      output (dist) and .git. A relative import that resolves outside the
//      package is refused: it hands the file a module the gate never walked.
//      A require or a dynamic import with a string specifier is held to the
//      same rule.
//   5. A file the parser cannot read is refused (the gate fails closed).
//
// This is what makes the template pattern sufficient: if the tag can only
// ever appear as html or svg in tag position, the pattern has seen every
// template there is.

const TAG_ID_NAMES = new Set(['html', 'svg']);

const TWO_PLACES_RULE = 'an imported html or svg may appear in exactly two places, its import statement and the tag position of a tagged template; every other appearance is refused, so the pattern that finds templates by the spelling of the tag has seen every template there is';

/** An identifier that only LABELS a member (see 3 above): never an appearance
 * of the name. */
const isMemberLabel = (node) => {
  const p = node.parent;
  if (!p) return false;
  if ((ts.isPropertyAccessExpression(p) || ts.isQualifiedName(p)) && p.name === node) return true;
  if (ts.isBindingElement(p) && p.propertyName === node) return true;
  return (
    p.name === node &&
    (ts.isPropertyAssignment(p) ||
      ts.isPropertySignature(p) ||
      ts.isPropertyDeclaration(p) ||
      ts.isMethodDeclaration(p) ||
      ts.isGetAccessorDeclaration(p) ||
      ts.isSetAccessorDeclaration(p) ||
      ts.isEnumMember(p))
  );
};

/** The refusal's words for where the name stands. */
const describeAppearance = (node) => {
  const p = node.parent;
  if (!p) return `a bare occurrence of the name ${node.text}`;
  if (ts.isVariableDeclaration(p)) {
    return p.initializer === node
      ? 'the tag bound to a local name (an initializer)'
      : `a local declaration of the name ${node.text}`;
  }
  if (ts.isParameter(p)) {
    return p.name === node
      ? `a local declaration of the name ${node.text} (a parameter)`
      : 'the tag bound as a parameter';
  }
  if (ts.isReturnStatement(p)) return 'the tag returned from a function';
  if (ts.isCallExpression(p) || ts.isNewExpression(p)) return 'the tag passed as an argument';
  if (ts.isExportSpecifier(p)) return 'the tag exported from this file';
  if (ts.isExportAssignment(p)) return 'the tag on a default export';
  if (ts.isBinaryExpression(p)) {
    if (p.operatorToken.kind === ts.SyntaxKind.EqualsToken) return 'the tag assigned to another name';
    if (p.operatorToken.kind === ts.SyntaxKind.CommaToken) return 'the tag riding a comma expression';
    return `the tag in a ${ts.SyntaxKind[p.operatorToken.kind]} expression`;
  }
  if (p.kind === ts.SyntaxKind.TypeOfExpression || ts.isTypeQueryNode(p)) return 'the tag under a typeof';
  if (ts.isSpreadElement(p) || ts.isSpreadAssignment(p)) return 'the tag spread into an expression';
  if (ts.isShorthandPropertyAssignment(p)) return 'the tag handed on as a shorthand property';
  if (ts.isPropertyAssignment(p)) return 'the tag as a property value';
  if (ts.isBindingElement(p)) return `a local declaration of the name ${node.text} (a destructuring)`;
  if (ts.isImportClause(p) || ts.isNamespaceImport(p)) {
    return `a declaration of the name ${node.text} (a default or namespace import)`;
  }
  if (
    ts.isFunctionDeclaration(p) || ts.isFunctionExpression(p) ||
    ts.isClassDeclaration(p) || ts.isClassExpression(p) ||
    ts.isInterfaceDeclaration(p) || ts.isTypeAliasDeclaration(p) ||
    ts.isEnumDeclaration(p) || ts.isModuleDeclaration(p) || ts.isTypeParameterDeclaration(p)
  ) return `a local declaration of the name ${node.text}`;
  return `an appearance of the name ${node.text} under a ${ts.SyntaxKind[p.kind]}`;
};

/** The string specifier of a require call or a dynamic import with one
 * string argument, or null. The introduction scan holds these to the lit
 * rules; here they are held to the package's edges, like any import. */
const callSpecifier = (node) => {
  if (!ts.isCallExpression(node) || node.arguments.length !== 1 || !ts.isStringLiteral(node.arguments[0])) return null;
  if (node.expression.kind === ts.SyntaxKind.ImportKeyword) return node.arguments[0].text;
  if (ts.isIdentifier(node.expression) && node.expression.text === 'require') return node.arguments[0].text;
  return null;
};

/** The specifiers that can leave the package: a relative path, an absolute
 * path, a file URL. A bare specifier (lit, node:path, a workspace name)
 * resolves into some package root by construction and cannot. Returns the
 * offending specifier, or null when it stays inside rootDir. */
const specifierLeavingThePackage = (spec, baseDir, rootDir) => {
  let resolved;
  if (spec.startsWith('./') || spec.startsWith('../')) resolved = resolve(baseDir, spec);
  else if (isAbsolute(spec)) resolved = resolve(spec);
  else if (spec.startsWith('file:')) {
    try {
      resolved = fileURLToPath(spec);
    } catch {
      return spec;
    }
  } else return null;
  const rel = relative(rootDir, resolved);
  if (rel !== '' && (rel.startsWith('..') || isAbsolute(rel))) return spec;
  return null;
};

/** The two-place rule, against one source file. baseDir is the directory the
 * file's relative imports resolve from; rootDir is the package directory the
 * walk is anchored at. Returns offences in the shape the other scans return,
 * so the gates and their fixtures treat all three alike. */
export const auditTagUsages = (source, file, baseDir, rootDir = PKG_DIR) => {
  const offences = [];
  const at = (line, position, rule) => offences.push({ file, line, position, rule });
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
  const diags = sf.parseDiagnostics ?? [];
  if (diags.length > 0) {
    at(
      lineOf(source, diags[0].start ?? 0),
      'a file the parser cannot read',
      `the parser reports a syntax error, so no identifier in the file can be judged (the gate fails closed): ${ts.flattenDiagnosticMessageText(diags[0].messageText, ' ')}`,
    );
    return offences;
  }
  const walk = (node) => {
    if (
      (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) &&
      node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)
    ) {
      const spec = node.moduleSpecifier.text;
      if (specifierLeavingThePackage(spec, baseDir, rootDir) !== null) {
        at(
          lineOf(source, node.getStart(sf)),
          'a relative import that leaves the package',
          `the specifier ${spec} resolves outside this package, so it hands the file a module the gate never walked; the gate refuses it (the gate fails closed)`,
        );
      }
    }
    const called = callSpecifier(node);
    if (called !== null && specifierLeavingThePackage(called, baseDir, rootDir) !== null) {
      at(
        lineOf(source, node.getStart(sf)),
        'a relative import that leaves the package',
        `the specifier ${called} resolves outside this package, so it hands the file a module the gate never walked; the gate refuses it (the gate fails closed)`,
      );
    }
    if (ts.isIdentifier(node) && TAG_ID_NAMES.has(node.text)) {
      const p = node.parent;
      const atTheImport = p !== undefined && ts.isImportSpecifier(p);
      const atTheTag = p !== undefined && ts.isTaggedTemplateExpression(p) && p.tag === node;
      if (!atTheImport && !atTheTag && !isMemberLabel(node)) {
        at(lineOf(source, node.getStart(sf)), describeAppearance(node), TWO_PLACES_RULE);
      }
    }
    node.forEachChild(walk);
  };
  walk(sf);
  return offences;
};

// ── the decision: the allow list against each binding's position ────────────────

const lineOf = (source, offset) => source.slice(0, offset).split('\n').length;

const describeName = (name) => (name === '' ? '(name position)' : name);

/** Judges one template. Returns offences: { file, line, position, rule }.
 * baseLine shifts line numbers when a nested template is audited out of its
 * expression text, so a nested offence points at the line the binding sits
 * on in the real file. */
const auditTemplate = (source, file, template, offences, baseLine = null) => {
  const at = (line, position, rule) => {
    const effective = baseLine === null ? line : baseLine + line - 1;
    offences.push({ file, line: effective, position, rule });
  };
  if (template.unclassifiable) {
    at(template.line, 'the template body', `the ${template.tag} template's body cannot be parsed into static parts and bindings, so it is refused (the gate fails closed)`);
    return;
  }
  const scan = makeScanner();

  const judgeBinding = (chunkIndex, chunk) => {
    // The static text that follows, if any: two bindings in a row have no
    // static between them, which is exactly how a split address is seen.
    const next = template.chunks[chunkIndex + 1];
    const nextStatic = next && next.type === 'static' ? next.text : '';
    const line = lineOf(source, chunk.start);
    const ctx = scan.context();

    if (ctx.region === 'unknown') {
      at(line, 'a position the scanner cannot classify', 'the literal text before this binding leaves the parser in a state the scanner cannot name, so the binding is refused (the gate fails closed)');
      return;
    }
    if (ctx.region === 'comment') {
      at(line, 'inside a comment', 'a comment body is not a position a binding may take');
      return;
    }
    if (ctx.region === 'rawtext') {
      at(line, `inside the body of the raw-text element ${ctx.raw}`, `the browser reads the body of ${ctx.raw} as ${ctx.raw === 'script' ? 'code' : 'raw text'} until the close tag, so no binding may land there`);
      return;
    }
    if (ctx.region === 'text') {
      // Text content is allowed. A nested template in the expression is
      // audited recursively and its static text feeds THIS scanner, so the
      // markup it carries is judged where it lands.
      const nested = extractTemplates(chunk.text);
      for (const t of nested) {
        auditTemplate(chunk.text, `${file} (nested ${t.tag} template)`, t, offences, baseLine === null ? line : baseLine + line - 1);
      }
      for (const t of nested) for (const c of t.chunks) if (c.type === 'static') scan.feed(c.text);
      return;
    }
    if (ctx.region === 'tag') {
      if (ctx.tag === 'script' || ctx.tag === 'base') {
        at(line, `inside a ${ctx.tag} tag`, `the element's src or href chooses what the parser loads, so nothing may be bound inside one`);
        return;
      }
      at(line, 'where an attribute name goes', 'a binding where an attribute name goes renders nothing under Lit and is on no allow list');
      return;
    }

    // Value positions.
    const name = ctx.name;
    if (name.startsWith('?')) {
      if (name.slice(1) === ALLOWED_BOOLEAN) {
        at(line, `the boolean binding ?${ALLOWED_BOOLEAN}`, 'allowed');
        return;
      }
      at(line, `the boolean binding ${name}`, `only ?${ALLOWED_BOOLEAN} is on the allow list`);
      return;
    }
    if (name.startsWith('.') || name.startsWith('@')) {
      at(line, `${name.startsWith('.') ? 'a property' : 'an event'} binding (${name[0]} form)`, `the property and event allow table is empty: no . or @ binding exists in the package today, so every one is refused`);
      return;
    }
    if (ctx.tag === 'script' || ctx.tag === 'base') {
      at(line, `a value in the ${describeName(name)} attribute of a ${ctx.tag} tag`, `the element's src or href chooses what the parser loads, so nothing may be bound inside one`);
      return;
    }
    const lowerName = name.toLowerCase();
    if (EVENT_ATTRIBUTE.test(lowerName)) {
      at(line, `a value in the event attribute ${name}`, 'these kits take no inline handlers');
      return;
    }
    if (REFUSED_VALUE_ATTRIBUTES.has(lowerName)) {
      at(line, `a value in the ${name} attribute`, `escaping cannot make a binding in the ${name} attribute safe (srcdoc is entity-decoded back into a document, style sets arbitrary CSS, the SVG animation attributes are decoded onto their target, object data loads a document of its own)`);
      return;
    }
    if (ADDRESS_ATTRIBUTES.has(lowerName)) {
      const whole = !ctx.valuePrefix && (nextStatic.startsWith(ctx.quote) || (ctx.region === 'unquoted' && (nextStatic === '' || WHITESPACE.test(nextStatic[0]) || nextStatic[0] === '>' || nextStatic[0] === '/')));
      if (!whole) {
        at(line, `a binding inside the address attribute ${name} that is not wholly one value`, 'an address must be one whole binding: a scheme reassembled from parts is invisible to any per-value check');
        return;
      }
      if (!ADDRESS_CALL.test(chunk.text)) {
        at(line, `a binding inside the address attribute ${name}`, `the only source the gate permits in an address attribute is the package's one address helper, ${ADDRESS_HELPER}(), which checks the scheme`);
        return;
      }
      at(line, `the address attribute ${name} through ${ADDRESS_HELPER}()`, 'allowed');
      return;
    }
    if (ctx.region === 'quoted' && isAllowedValueAttribute(lowerName)) {
      at(line, `a quoted value in ${name}`, 'allowed');
      return;
    }
    if (ctx.region === 'unquoted' && isAllowedUnquoted(lowerName)) {
      const whole = !ctx.valuePrefix && (nextStatic === '' || WHITESPACE.test(nextStatic[0]) || nextStatic[0] === '>' || nextStatic[0] === '/');
      if (whole) {
        at(line, `a whole unquoted value in the ${ALLOWED_VALUE_FAMILIES.find((f) => lowerName.startsWith(f))} namespace (${name})`, 'allowed');
        return;
      }
      at(line, `an unquoted value with static text in it in the ${name} attribute`, 'an unquoted value must be the whole value: static text beside the binding is refused');
      return;
    }
    if (ctx.region === 'unquoted') {
      at(line, `an unquoted value in the ${describeName(name)} attribute`, `unquoted values are allowed only as the whole value in the data- and aria- namespaces`);
      return;
    }
    at(line, `a value in the ${describeName(name)} attribute`, 'the attribute is not on the allow list of attributes that may take a bound value');
  };

  for (let i = 0; i < template.chunks.length; i++) {
    const chunk = template.chunks[i];
    if (chunk.type === 'static') {
      scan.feed(chunk.text);
    } else {
      judgeBinding(i, chunk);
    }
  }
};

/** Audits a source text: every html and svg template in it, against the allow
 * list. Returns the offences (allowed positions are recorded too, as rules
 * naming them 'allowed', so a test can see what the gate classified). */
export const auditSource = (source, file) => {
  const offences = [];
  for (const template of extractTemplates(source)) {
    auditTemplate(source, file, template, offences);
  }
  return offences;
};

export const allowedBy = (offences) => offences.filter((o) => o.rule === 'allowed');
export const refusedBy = (offences) => offences.filter((o) => o.rule !== 'allowed');

// ── the walk: every source file of the package + dependents ─────────────────────

export const PKG_DIR = join(new URL('../..', import.meta.url).pathname);
export const WEB_ROOT = join(PKG_DIR, '..', '..');

// The two gate files hold the fixtures BY DESIGN: a fixture is a binding in a
// refused position. They are excluded BY NAME, here and in the raw-markup
// gate, from their own scans.
export const GATE_FILES = ['test/no-raw-markup.test.mjs', 'test/positions.test.mjs'];

const SOURCE_RE = /\.(ts|tsx|mts|cts|js|mjs|cjs|jsx)$/;

export const walkSources = (dir, prefix, out, skipFiles = []) => {
  for (const entry of readdirSync(dir)) {
    const rel = prefix ? `${prefix}/${entry}` : entry;
    const abs = join(dir, entry);
    if (statSync(abs).isDirectory()) {
      // node_modules and .git are never sources; dist is the build output.
      if (entry === 'node_modules' || entry === '.git' || entry === 'dist') continue;
      walkSources(abs, rel, out, skipFiles);
    } else if (SOURCE_RE.test(entry) && !skipFiles.includes(rel)) {
      out.push({ rel, abs, text: readFileSync(abs, 'utf8') });
    }
  }
  return out;
};

export const manifestOf = (dir) => {
  try {
    return JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'));
  } catch {
    return null;
  }
};

/** This package's sources the gates judge: every source file under the
 * package's directory, the package root included, so no file of the package
 * stands outside the walked folders. The two gate files stay excluded by
 * name (they hold the fixtures, which are offences by design). */
export const thisPackageSources = () => {
  const out = [];
  walkSources(PKG_DIR, '', out, GATE_FILES);
  return out;
};

/** The walk the two-place rule runs over: every source file of the package,
 * leaving out only node_modules, the build output (dist) and .git. The two
 * gate files are NOT excluded here: the rule has no fixture file, and the
 * gate files hold none of its kind. */
export const everyPackageSource = () => walkSources(PKG_DIR, '', [], []);

/** Every workspace package that depends on @hardscapehouse/design-system, by manifest or
 * by import. */
export const dependentPackages = () => {
  const out = [];
  for (const group of ['packages', 'apps']) {
    const dir = join(WEB_ROOT, group);
    let entries = [];
    try {
      entries = readdirSync(dir);
    } catch {
      continue;
    }
    for (const name of entries) {
      const abs = join(dir, name);
      if (!statSync(abs).isDirectory()) continue;
      const manifest = manifestOf(abs);
      if (!manifest || manifest.name === '@hardscapehouse/design-system') continue;
      const deps = { ...(manifest.dependencies ?? {}), ...(manifest.devDependencies ?? {}), ...(manifest.peerDependencies ?? {}) };
      const sources = walkSources(abs, `${group}/${name}`, []);
      const imports = sources.some((s) => s.text.includes('@hardscapehouse/design-system'));
      if (deps['@hardscapehouse/design-system'] || imports) {
        out.push({ name: manifest.name ?? `${group}/${name}`, dir: abs, sources });
      }
    }
  }
  return out;
};
