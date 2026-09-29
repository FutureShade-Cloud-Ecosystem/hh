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
// What the gate does not see, stated honestly: a call whose value is a
// template (chip(...), icon(...), kitShell(...)) is judged at the call
// position (text), and the template it returns is audited where it is built,
// when the walk reaches the file that builds it. An expression that contains
// a nested html or svg template literal is judged recursively and its static
// text feeds the outer scanner, so a template composed inside a tag or a
// value is judged where it lands.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

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

// ── the walk: this package (src, harness, and test minus the gates) + dependents ──

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
      if (entry === 'node_modules' || entry === '.git') continue;
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

/** This package's sources the gate judges. */
export const thisPackageSources = () => {
  const out = [];
  for (const root of ['src', 'harness', 'test']) {
    walkSources(join(PKG_DIR, root), root, out, GATE_FILES);
  }
  return out;
};

/** Every workspace package that depends on @hh/design-system, by manifest or
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
      if (!manifest || manifest.name === '@hh/design-system') continue;
      const deps = { ...(manifest.dependencies ?? {}), ...(manifest.devDependencies ?? {}), ...(manifest.peerDependencies ?? {}) };
      const sources = walkSources(abs, `${group}/${name}`, []);
      const imports = sources.some((s) => s.text.includes('@hh/design-system'));
      if (deps['@hh/design-system'] || imports) out.push({ name: manifest.name ?? `${group}/${name}`, sources });
    }
  }
  return out;
};
