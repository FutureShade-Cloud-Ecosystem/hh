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
// exist: the import list holds, for every file the gate walks, that every
// import, re-export, dynamic import and require names a specifier and names
// written on the gate's own list, folder by folder, so a lit template tag
// can only enter a file from lit, under its own name; and the tag usage scan
// (on the TypeScript compiler's own parser, which the package already
// carries for its typecheck) holds that an imported html or svg appears in
// exactly two places, its import statement and the tag position of a tagged
// template. Together the two scans make the pattern sufficient: the tag can
// only ever stand as html or svg in tag position, so the pattern above has
// already seen every template there is. A call whose value is a template
// (chip(...), icon(...), kitShell(...)) is judged at the call position
// (text), and the template it returns is audited where it is built, when the
// walk reaches the file that builds it. An expression that contains a nested
// html or svg template literal is judged recursively and its static text
// feeds the outer scanner, so a template composed inside a tag or a value is
// judged where it lands.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
// The TypeScript compiler's own parser: the devDependency the package's
// typecheck already carries, so the import list scan and the two-place scan
// below both walk the syntax tree the language's own parser builds. No new
// dependency.
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

// ── the ported surface's deliberate allowance (the UI parity ruling) ────────────
//
// The Counter Till's ported POS surface (web/apps/counter-till/src/ported/,
// carried from hardscapeos_dibbits master 87d7941b) keeps the reference's own
// Lit idioms: @event and .property bindings in the templates, roving tabindex,
// bound id/value/label and friends. Converting roughly eleven hundred bindings
// to this package's host-delegation discipline is the rewrite of the template
// layer the port's brief forbids, so the lead's parity ruling grows the gate
// for that one directory, by name. The three tables below are the ruling's
// record: each name is a binding position the ported files actually use, a NEW
// position inside the ported prefix is still refused until it is added here on
// purpose, every HH-written file outside the prefix is held exactly as before,
// and the raw-markup refusals (srcdoc and friends, on* handlers, script and
// base) keep biting inside the prefix too. STYLE is the one deliberate
// admission past this gate's own security refusal: the multi-card surface's
// draggable panels position at runtime and the values are the surface's own
// geometry, never wire data; the polish item retires it when the surface
// adopts the kit discipline. SRC is admitted the same way, as one whole value
// only: product-thumb's thumbnail route is the module's own same-origin
// relative path with the id URI-encoded, and the address helper cannot wrap a
// nested call by this gate's own spelling rule.
export const PORTED_SURFACE_PREFIX = 'apps/counter-till/src/ported/';

export const isPortedSurfaceFile = (file) => file.startsWith(PORTED_SURFACE_PREFIX);

/** The . and @ binding names the ported files use, sigils included. */
export const PORTED_PROPERTY_OR_EVENT = new Set([
  '@change', '@click', '@error', '@focus', '@focusin', '@hos-customer-picker-close',
  '@hos-customer-selected', '@input', '@keydown', '@open-order', '@order-changed',
  '@order-lifecycle-loaded', '@order-status-changed', '@pointercancel', '@pointerdown',
  '@pointermove', '@pointerup', '@pos-add-echo', '@pos-add-product', '@pos-drop-exit',
  '@pos-flow-goto', '@pos-legend-dismiss', '@pos-panel-focus', '@pos-parked-close',
  '@pos-parked-discard', '@pos-parked-resume', '@pos-relief-applied', '@pos-relief-close',
  '@pos-return-close',
  '@pos-scan-resolved', '@pos-signature', '@pos-split-cancel', '@pos-split-ring',
  '@pos-step-toggle', '@pos-term-changed', '@pos-term-commit-top', '@pos-term-enter-list',
  '@pricing-doc-close', '@quick-start-clear', '@quick-start-continue', '@quick-start-park',
  '@related-doc-open', '@submit', '@till-close-cancel', '@till-closed',
  '.active', '.alt', '.branchId', '.breakdown', '.busy', '.checked', '.complete',
  '.curated', '.customer', '.customerId', '.customerName', '.doc', '.externalTerm',
  '.fault', '.hasCustomer', '.hasImage', '.hint', '.homeBranchId', '.info', '.label',
  '.n', '.open', '.openLines', '.orderId', '.presentation', '.productId', '.refId',
  '.retry', '.roster', '.rows', '.sessionId', '.showChips', '.size', '.state', '.step',
  '.steps', '.subtotalCents', '.summary', '.term', '.ticket', '.ticketEpoch',
  '.tillSessionId', '.totalCents', '.value', '.vocabulary',
]);

/** The boolean bindings besides ?disabled the ported files use. */
export const PORTED_BOOLEANS = new Set(['autofocus-scan', 'busy', 'hidden', 'selected', 'unified']);

/** The attributes the ported files bind a value onto, beyond this package's allow list. */
export const PORTED_BOUND_VALUES = new Set([
  'alt', 'branch-id', 'doc-id', 'id', 'label', 'max', 'name', 'order-id', 'placeholder',
  'slot', 'style', 'tabindex', 'title', 'value',
]);

/** The address attributes the ported files may bind as one whole value, past the
 * helper-only rule (see the ruling comment above for the src ground). */
export const PORTED_ADDRESS_VALUES = new Set(['src']);

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

// ── the import list: the gate names what is allowed, and refuses the rest ────────
//
// The extractor sees a template only when its tag is spelled html or svg, so
// what may bring the tag into a file is written down, not inferred. In every
// file the gate walks, every import, re-export, dynamic import and require is
// refused unless its specifier AND its imported names are on this list. The
// list is per folder (src, test, harness): what src may import is lit, by
// name,
// the names the package's files use today; the relative paths inside the
// package), and what test and harness may import beyond that (node's built in
// modules, the test tools, @lit-labs/ssr for development). Each entry is ONE
// specifier with its allowed names: no entry is a prefix or a pattern, so a
// subpath of a listed package is a specifier of its own and is listed itself,
// or it is refused. html and svg are allowed from lit only, under their own
// names only: an alias, a namespace, a default binding, a dynamic import or
// a require puts the tag under a name the template pattern does not see, and
// each is refused wherever it appears. Anything not on the list is refused
// with the file, the line and the specifier: another package of the library's
// family (lit-element re-exports the same tags), a subpath nobody listed, a
// new dependency nobody argued for. Adding an entry is a change to the gate
// that a reviewer sees.
//
// The same scan refuses, in every walked file: a call of eval, a call of
// new Function, and a dynamic import or a require whose argument is not a
// string literal, because each builds or loads code the parser never walks.
// A statement the parser cannot read refuses the file (the gate fails
// closed). Type only names are listed beside value names: the list is what
// the folder imports, not only what it runs. The gate's test holds the list
// to the package's actual imports: every entry is exercised by some walked
// file today, and every name on an entry is imported today, except svg, the
// pattern's second tag, which the list carries so the door is named and not
// silent.

export const IMPORT_ALLOW_LIST = {
  src: {
    '@lit-labs/ssr': ['render'],
    '@lit-labs/ssr/lib/install-global-dom-shim.js': [],
    '@lit-labs/ssr/lib/render-result.js': ['collectResultSync'],
    lit: ['CSSResult', 'CSSResultGroup', 'LitElement', 'PropertyValues', 'TemplateResult', 'css', 'html', 'svg', 'nothing'],
    'node:fs': ['readFileSync'],
    'node:module': ['register'],
    'node:path': ['dirname', 'join'],
    'node:url': ['fileURLToPath', 'pathToFileURL'],
    './address.js': ['hhAddress'],
    './base.js': ['HHElement', 'HHRenderError', 'defineElement'],
    './kits/dashboard.js': ['DashboardKit'],
    './kits/guided-flow.js': ['GuidedFlowKit'],
    './kits/inbox.js': ['InboxKit'],
    './kits/list-detail.js': ['ListDetailKit'],
    './kits/scan-and-confirm.js': ['ScanConfirmKit'],
    './primitives.js': ['ButtonSpec', 'baseStyles', 'button', 'chip', 'row', 'spinner', 'select', 'dateInput', 'fieldGroup'],
    './state-icons.js': ['STATE_ICONS', 'StateIconName', 'icon'],
    './state.js': ['DesignedState', 'KIT_STATES', 'STATE_META', 'KitState', 'kitShell', 'stateIndicator', 'stateStyles'],
    '../base.js': ['HHElement', 'defineElement'],
    '../primitives.js': ['baseStyles', 'chip', 'row'],
    '../state-icons.js': ['icon'],
    '../state.js': ['ButtonSpec', 'KitState', 'kitShell', 'stateStyles'],
  },
  test: {
    '@lit-labs/ssr': ['render'],
    '@lit-labs/ssr/lib/install-global-dom-shim.js': [],
    '@lit-labs/ssr/lib/render-result.js': ['collectResultSync'],
    lit: ['html', 'svg'],
    'node:assert/strict': ['default'],
    'node:child_process': ['spawn', 'spawnSync'],
    'node:fs': ['existsSync', 'mkdirSync', 'mkdtempSync', 'readFileSync', 'readdirSync', 'rmSync', 'statSync', 'unlinkSync', 'writeFileSync'],
    'node:http': ['createServer'],
    'node:module': ['stripTypeScriptTypes'],
    'node:os': ['tmpdir'],
    'node:path': ['dirname', 'join', 'relative'],
    'node:test': ['test'],
    'node:timers/promises': ['setTimeout'],
    'node:url': ['fileURLToPath'],
    typescript: ['default'],
    './_lib/cdp.mjs': ['findBrowserBinary', 'launchBrowser'],
    './_lib/color.mjs': ['hexToOklch', 'oklchToHex', 'parseOklch', 'relativeLuminance'],
    './_lib/positions.mjs': ['ADDRESS_HELPER', 'ALLOWED_PROPERTY_OR_EVENT', 'GATE_FILES', 'IMPORT_ALLOW_LIST', 'auditImports', 'auditSource', 'auditTagUsages', 'dependentPackages', 'everyPackageSource', 'extractTemplates', 'importedSpecifiers', 'refusedBy', 'thisPackageSources'],
    './_lib/render.mjs': ['renderKit', 'renderTemplate'],
    './_lib/sheet.mjs': ['PALETTE_NAMES', 'contrast', 'declaredPairs', 'definedTokens', 'parseDeclarations', 'palette', 'palettes', 'pkgDir', 'readPkg', 'referencedTokens', 'resolveColor', 'shippedStyleFiles', 'sheetBlocks', 'stripComments'],
    './color.mjs': ['contrast', 'oklchToHex', 'parseOklch'],
    '../src/harness-page.js': ['HARNESS_STATES', 'HARNESS_TITLE', 'KITS', 'WIDTHS', 'buildHarnessPage', 'harnessSheets'],
    '../src/kits/dashboard.js': ['DashboardKit'],
    '../src/kits/guided-flow.js': ['GuidedFlowKit'],
    '../src/kits/inbox.js': ['InboxKit'],
    '../src/kits/list-detail.js': ['ListDetailKit'],
    '../src/kits/scan-and-confirm.js': ['ScanConfirmKit'],
    '../src/primitives.js': ['baseStyles', 'select', 'dateInput', 'fieldGroup', 'button'],
    '../src/state-icons.js': ['STATE_ICONS', 'icon'],
    '../src/state.js': ['KIT_STATES', 'STATE_META', 'kitShell', 'stateIndicator'],
  },
  harness: {
    'node:fs': ['mkdirSync', 'readFileSync', 'writeFileSync'],
    'node:http': ['createServer'],
    'node:net': ['default'],
    'node:path': ['dirname', 'join'],
    'node:url': ['fileURLToPath'],
    '../src/harness-page.js': ['HARNESS_TITLE', 'buildHarnessPage', 'harnessSheets'],
  },
};

// The folder a file's list comes from: the path's first segment. A file at
// the package root has no folder, so nothing it imports can be on a list,
// and every import is refused.
const LISTED_FOLDERS = ['src', 'test', 'harness'];

const folderOf = (file) => {
  const top = file.split('/')[0];
  return LISTED_FOLDERS.includes(top) ? top : null;
};

/** The names a destructuring binds, as (imported name, local alias) pairs, or
 * null when the pattern is not one the gate can read: a rest element takes
 * the namespace whole, so no name list exists to check. Works for an object
 * binding pattern (const { a } = ...) and for an object literal used as a
 * destructuring assignment target ({ a } = ...). */
const destructuredNames = (pattern) => {
  const elements = pattern.kind === ts.SyntaxKind.ObjectBindingPattern ? pattern.elements : pattern.properties;
  const names = [];
  for (const element of elements) {
    if (ts.isBindingElement(element)) {
      if (element.dotDotDotToken) return null;
      const key = element.propertyName ?? element.name;
      if (!key || !ts.isIdentifier(key)) return null;
      names.push({ name: key.text, alias: element.propertyName ? element.name.text : null });
    } else if (ts.isPropertyAssignment(element)) {
      if (!element.name || !ts.isIdentifier(element.name)) return null;
      const init = element.initializer;
      names.push({ name: element.name.text, alias: init && ts.isIdentifier(init) ? init.text : null });
    } else if (ts.isShorthandPropertyAssignment(element)) {
      if (!element.name || !ts.isIdentifier(element.name)) return null;
      names.push({ name: element.name.text, alias: null });
    } else {
      return null;
    }
  }
  return names;
};

/** The destructuring a dynamic import's or a require's result feeds, or null
 * when the namespace arrives whole (an identifier, a call, an unbound
 * await): then no name list exists in the source, and the call is refused. */
const consumedNames = (node) => {
  let holder = node;
  if (holder.parent && holder.parent.kind === ts.SyntaxKind.AwaitExpression) holder = holder.parent;
  const consumer = holder.parent;
  if (!consumer) return null;
  if (ts.isVariableDeclaration(consumer)) {
    if (consumer.name && consumer.name.kind === ts.SyntaxKind.ObjectBindingPattern) {
      return destructuredNames(consumer.name);
    }
    return null;
  }
  if (
    ts.isBinaryExpression(consumer) &&
    consumer.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
    consumer.left &&
    consumer.left.kind === ts.SyntaxKind.ObjectLiteralExpression
  ) {
    return destructuredNames(consumer.left);
  }
  return null;
};

/** The import list, against one source file, on the parser's own tree: every
 * import, re-export, dynamic import and require must name a specifier and
 * names on the file's folder's list, and eval, new Function and a dynamic
 * import or require whose argument is not a string literal are refused
 * outright. A file the parser cannot read is refused (the gate fails
 * closed). Returns offences in the shape the other scans return, so the
 * gates and their fixtures treat all three alike. */
export const auditImports = (source, file) => {
  const offences = [];
  const at = (line, position, rule) => offences.push({ file, line, position, rule });
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
  const diags = sf.parseDiagnostics ?? [];
  if (diags.length > 0) {
    at(
      lineOf(source, diags[0].start ?? 0),
      'a file the parser cannot read',
      `the parser reports a syntax error, so no module statement in the file can be judged (the gate fails closed): ${ts.flattenDiagnosticMessageText(diags[0].messageText, ' ')}`,
    );
    return offences;
  }
  const folder = folderOf(file);
  const entries = folder === null ? null : IMPORT_ALLOW_LIST[folder];
  const listName = folder === null ? 'the package root (no folder list covers it)' : `the ${folder} folder's list`;

  const refuseSpecifier = (line, spec, statement) => {
    at(
      line,
      `${statement} ${spec}`,
      `the specifier ${spec} is not on ${listName}: the gate names what is allowed and refuses the rest, so adding this specifier is a change to the gate a reviewer sees`,
    );
  };

  const checkNames = (line, spec, names) => {
    for (const { name, alias } of names) {
      if ((name === 'html' || name === 'svg') && alias !== null && alias !== name) {
        at(
          line,
          `an import of ${name} under the name ${alias}`,
          `the template tags html and svg are allowed from lit only, under their own names only: a tag under another name is a template the pattern never sees`,
        );
      } else if (!entries[spec].includes(name)) {
        at(
          line,
          `an import of ${name} from ${spec}`,
          `the name ${name} is not on the ${spec} entry of ${listName}: adding the name is a change to the gate a reviewer sees`,
        );
      }
    }
  };

  const judgeImportOrReexport = (node) => {
    const line = lineOf(source, node.getStart(sf));
    const spec = node.moduleSpecifier.text;
    const word = ts.isImportDeclaration(node) ? 'an import from' : 'a re-export from';
    if (entries === null || entries[spec] === undefined) {
      refuseSpecifier(line, spec, word);
      return;
    }
    if (ts.isImportDeclaration(node)) {
      const clause = node.importClause;
      // A side effect import binds no name, and the specifier is listed.
      if (!clause) return;
      if (clause.name) checkNames(line, spec, [{ name: 'default', alias: clause.name.text }]);
      const named = clause.namedBindings;
      if (named && ts.isNamespaceImport(named)) {
        at(line, `a namespace import from ${spec}`, `a namespace import carries every name the module exports at once, and the list allows names, not everything: import by name`);
      } else if (named) {
        checkNames(line, spec, named.elements.map((e) => ({ name: (e.propertyName ?? e.name).text, alias: e.propertyName ? e.name.text : null })));
      }
      return;
    }
    // export ... from
    if (!node.exportClause) {
      at(line, `a star re-export from ${spec}`, `a star re-export hands on every name the module exports at once, and the list allows names, not everything: re-export by name`);
      return;
    }
    if (ts.isNamespaceExport(node.exportClause)) {
      at(line, `a namespace re-export from ${spec}`, `a namespace re-export hands on every name the module exports as one binding, and the list allows names, not everything`);
      return;
    }
    checkNames(line, spec, node.exportClause.elements.map((e) => ({ name: (e.propertyName ?? e.name).text, alias: e.propertyName ? e.name.text : null })));
  };

  const judgeCall = (node, isDynamicImport) => {
    const line = lineOf(source, node.getStart(sf));
    const word = isDynamicImport ? 'a dynamic import' : 'a require';
    if (node.arguments.length !== 1 || !ts.isStringLiteral(node.arguments[0])) {
      at(
        line,
        `${word} whose specifier is not a string literal`,
        `the specifier could be assembled to name anything, so it cannot be checked against the list (the gate fails closed)`,
      );
      return;
    }
    const spec = node.arguments[0].text;
    if (entries === null || entries[spec] === undefined) {
      refuseSpecifier(line, spec, `${word} of`);
      return;
    }
    const names = consumedNames(node);
    if (names === null) {
      at(
        line,
        `${word} of ${spec}`,
        `the module arrives as a whole namespace, and the list allows names, not everything: bind only the names the file uses, so the list can check them`,
      );
      return;
    }
    checkNames(line, spec, names);
  };

  const walk = (node) => {
    if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier) {
      if (ts.isStringLiteral(node.moduleSpecifier)) judgeImportOrReexport(node);
      else {
        at(
          lineOf(source, node.getStart(sf)),
          'a module specifier the scanner cannot read',
          `the specifier is not a plain string literal, so it cannot be checked against the list (the gate fails closed)`,
        );
      }
    }
    if (ts.isCallExpression(node)) {
      if (node.expression.kind === ts.SyntaxKind.ImportKeyword) judgeCall(node, true);
      else if (ts.isIdentifier(node.expression) && node.expression.text === 'require') judgeCall(node, false);
      else if (ts.isIdentifier(node.expression) && node.expression.text === 'eval') {
        at(lineOf(source, node.getStart(sf)), 'a call of eval', `eval runs a string the parser never walks, so a template it could build is one the pattern has not seen`);
      }
    }
    if (ts.isNewExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'Function') {
      at(lineOf(source, node.getStart(sf)), 'a call of new Function', `new Function compiles a string the parser never walks, so a template it could build is one the pattern has not seen`);
    }
    node.forEachChild(walk);
  };
  walk(sf);
  return offences;
};

/** What one file imports today, read off the parser's tree: one record per
 * specifier, with every imported name ('default' for a default binding), or
 * names: null when the source shows no name list (a side effect import, a
 * namespace, a whole namespace binding). The gate's test holds the list to
 * these records: every entry is exercised by some walked file, every name on
 * an entry is imported today. */
export const importedSpecifiers = (source, file) => {
  const found = new Map();
  const add = (spec, name) => {
    const record = found.get(spec) ?? { spec, names: new Set() };
    if (name === null) record.names = null;
    else if (record.names !== null) record.names.add(name);
    found.set(spec, record);
  };
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
  const visit = (node) => {
    if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
      const spec = node.moduleSpecifier.text;
      if (ts.isImportDeclaration(node)) {
        const clause = node.importClause;
        if (!clause) add(spec, null);
        else {
          if (clause.name) add(spec, 'default');
          const named = clause.namedBindings;
          if (named && ts.isNamespaceImport(named)) add(spec, null);
          else if (named) for (const e of named.elements) add(spec, (e.propertyName ?? e.name).text);
        }
      } else if (!node.exportClause) add(spec, null);
      else if (ts.isNamespaceExport(node.exportClause)) add(spec, null);
      else for (const e of node.exportClause.elements) add(spec, (e.propertyName ?? e.name).text);
    }
    if (ts.isCallExpression(node)) {
      const isDynamicImport = node.expression.kind === ts.SyntaxKind.ImportKeyword;
      const isRequire = ts.isIdentifier(node.expression) && node.expression.text === 'require';
      if ((isDynamicImport || isRequire) && node.arguments.length === 1 && ts.isStringLiteral(node.arguments[0])) {
        const consumed = consumedNames(node);
        if (consumed === null) add(node.arguments[0].text, null);
        else for (const { name } of consumed) add(node.arguments[0].text, name);
      }
    }
    node.forEachChild(visit);
  };
  visit(sf);
  return [...found.values()].map(({ spec, names }) => ({ spec, names: names === null ? null : [...names].sort() }));
};

// ── the tag's two places: the parser rule that closes the value flow ────────────
//
// The import list holds module statements; a tag value copied through local
// code is a value flow, not a statement, and no list of statements can follow
// it. This rule is decided where every copy must begin, at the identifier, on
// the TypeScript compiler's own parser (the devDependency the package's
// typecheck already carries; no new dependency): in every file of the
// package, an imported html or svg may appear in exactly two places, its
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
//   4. The walk: every .ts, .tsx, .mts, .cts, .js, .mjs, .cjs and .jsx file
//      under the package's directory, the package root included, leaving out
//      node_modules and the build output (dist) at the package's root only,
//      by their full path (a folder of either name anywhere else is walked),
//      and .git at any depth. What may be imported into those files at all
//      is the import list's decision; this rule holds the tag's value once
//      it is in.
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

/** The two-place rule, against one source file, on the parser's own tree.
 * Every import into the file is the import list's business; this rule holds
 * the tag's value once it is in. Returns offences in the shape the other
 * scans return, so the gates and their fixtures treat all three alike. */
export const auditTagUsages = (source, file) => {
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
      if (isPortedSurfaceFile(file) && PORTED_BOOLEANS.has(name.slice(1))) {
        at(line, `the boolean binding ${name} (the ported surface's allowance)`, 'allowed');
        return;
      }
      at(line, `the boolean binding ${name}`, `only ?${ALLOWED_BOOLEAN} is on the allow list`);
      return;
    }
    if (name.startsWith('.') || name.startsWith('@')) {
      if (isPortedSurfaceFile(file) && PORTED_PROPERTY_OR_EVENT.has(name)) {
        at(
          line,
          `${name.startsWith('.') ? 'a property' : 'an event'} binding (${name[0]} form, the ported surface's allowance)`,
          'allowed',
        );
        return;
      }
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
    if (REFUSED_VALUE_ATTRIBUTES.has(lowerName) && !(isPortedSurfaceFile(file) && PORTED_BOUND_VALUES.has(lowerName))) {
      at(line, `a value in the ${name} attribute`, `escaping cannot make a binding in the ${name} attribute safe (srcdoc is entity-decoded back into a document, style sets arbitrary CSS, the SVG animation attributes are decoded onto their target, object data loads a document of its own)`);
      return;
    }
    if (ADDRESS_ATTRIBUTES.has(lowerName)) {
      const whole = !ctx.valuePrefix && (nextStatic.startsWith(ctx.quote) || (ctx.region === 'unquoted' && (nextStatic === '' || WHITESPACE.test(nextStatic[0]) || nextStatic[0] === '>' || nextStatic[0] === '/')));
      if (!whole) {
        at(line, `a binding inside the address attribute ${name} that is not wholly one value`, 'an address must be one whole binding: a scheme reassembled from parts is invisible to any per-value check');
        return;
      }
      if (isPortedSurfaceFile(file) && PORTED_ADDRESS_VALUES.has(lowerName)) {
        at(line, `the address attribute ${name} as one whole value (the ported surface's allowance)`, 'allowed');
        return;
      }
      if (!ADDRESS_CALL.test(chunk.text)) {
        at(line, `a binding inside the address attribute ${name}`, `the only source the gate permits in an address attribute is the package's one address helper, ${ADDRESS_HELPER}(), which checks the scheme`);
        return;
      }
      at(line, `the address attribute ${name} through ${ADDRESS_HELPER}()`, 'allowed');
      return;
    }
    if (isPortedSurfaceFile(file) && PORTED_BOUND_VALUES.has(lowerName)) {
      if (ctx.region === 'quoted') {
        at(line, `a quoted value in the ${name} attribute (the ported surface's allowance)`, 'allowed');
        return;
      }
      const whole = !ctx.valuePrefix && (nextStatic === '' || WHITESPACE.test(nextStatic[0]) || nextStatic[0] === '>' || nextStatic[0] === '/');
      if (whole) {
        at(line, `a whole unquoted value in the ${name} attribute (the ported surface's allowance)`, 'allowed');
        return;
      }
      // an unquoted value with static text beside the binding stays refused, as everywhere else
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

export const walkSources = (dir, prefix, out, skipFiles = [], rootDir = dir) => {
  for (const entry of readdirSync(dir)) {
    const rel = prefix ? `${prefix}/${entry}` : entry;
    const abs = join(dir, entry);
    if (statSync(abs).isDirectory()) {
      // node_modules and the build output (dist) are excluded at the walked
      // package's ROOT only, by their full path: a folder of either name
      // anywhere else is walked, so a source file cannot hide inside one.
      // .git is never a source tree, at any depth.
      if (abs === join(rootDir, 'node_modules') || abs === join(rootDir, 'dist') || entry === '.git') continue;
      walkSources(abs, rel, out, skipFiles, rootDir);
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

/** The walk the gates run over: every source file of the package, leaving
 * out node_modules and the build output (dist) at the package's root only,
 * by their full path, and .git at any depth. The two gate files are NOT
 * excluded here: the import list and the two-place rule have no fixture file
 * of their own kind, and the gate files hold their fixtures as strings,
 * which neither scan reads as code. */
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
      const sources = walkSources(abs, `${group}/${name}`, [], [], abs);
      const imports = sources.some((s) => s.text.includes('@hardscapehouse/design-system'));
      if (deps['@hardscapehouse/design-system'] || imports) {
        out.push({ name: manifest.name ?? `${group}/${name}`, dir: abs, sources });
      }
    }
  }
  return out;
};
