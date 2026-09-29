// A SMALL HAND-WRITTEN RENDERING LAYER, owned by this package. This file is
// not Lit and does not claim to be: it carries no Lit code, no version, no
// licence and no upstream. The source brief names Lit as the kit substrate;
// taking the real library is a dependency decision, and the proposal to make
// it is written up as a draft decision record (docs/adr/0023, status draft)
// for Colton. If the real Lit lands as a workspace dependency, this file is
// deleted and the imports re-point at it; the kits' render functions do not
// change.
//
// What is here, and deliberately what is not:
//   html        the tagged template. It returns a Template branded by a
//               MODULE-PRIVATE SYMBOL, and nothing without that brand is ever
//               emitted as markup: text values are escaped, nested templates
//               and arrays compose, and a caller's object is always rendered
//               as text, never as markup. The scanner over the template's
//               static text FAILS CLOSED: a binding is accepted only where
//               the scanner has positively classified its position as safe,
//               and the safe positions are exactly three. One, text content:
//               the value lands escaped. Two, a quoted attribute value in an
//               attribute on the allow list of attributes that may take a
//               binding (class, role and d, plus the data- and aria-
//               namespaces); an address attribute keeps its own stricter rule
//               instead: wholly one value, and a scheme of http, https or
//               none. Three, a bare attribute name on the allow list of
//               bound names (the boolean flag disabled, the same namespaces,
//               and the address names), whose value, if one follows the
//               equals, is then checked against that name, carried across
//               bindings and trusted sub-templates alike. Everything else is
//               refused with a named HHRenderError: any position the literal
//               text leaves unclassifiable, and every binding after it; a
//               comment body; a script or style element body; the interior
//               of a script or base tag, value or name, whatever the scheme
//               (a caller string must not choose what the parser loads); an
//               unquoted attribute value; an event attribute; srcdoc; the
//               style attribute; the SVG animation attributes values, to and
//               from; the object data attribute; an address split over more
//               than one value; a binding that continues a name mid-word; a
//               bound name standing next to another binding or followed by a
//               name character; and an attribute or name that is on no allow
//               list at all. There is no raw markup escape hatch anywhere in
//               the package; the icon module builds its geometry through the
//               same escaped attribute path.
//   css         a style string. No resolving, no shims.
//   HHElement   reactive properties from `static properties` (attribute
//               reflected), a render() into an open shadow root in the browser,
//               and a static renderToString(props) for Node, the tests and the
//               static harness. No event protocol: these kits are pattern
//               renders, their buttons are real <button> elements, and the
//               shells that adopt them attach listeners on the HOST element
//               (delegation): every update replaces the whole shadow root, so
//               a listener attached to anything inside a kit is gone at the
//               next render, and focus with it. That limit is measured and
//               stated plainly in docs/adr/0023.
//
// Node and the browser are both first-class: Node has no HTMLElement or
// customElements, so the base class degrades to a plain object there and only
// the browser path registers elements.

/** The module-private trust brand. A Template is the only markup this layer
 * emits, and a Template can only be made by html() right here: the symbol is
 * not exported, so no caller can forge one or smuggle one in parsed JSON. */
const TRUSTED: unique symbol = Symbol('hh trusted template');

export interface Template {
  readonly [TRUSTED]: true;
  readonly strings: readonly string[];
  readonly values: readonly unknown[];
}

const ESCAPES: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

const escapeText = (v: string): string => v.replace(/[&<>"']/g, (c) => ESCAPES[c]);

/** The named error this layer throws for a refused template or a failed
 * element render. The name is part of the contract: a shell can catch it, and
 * a gate can assert the refusal happened instead of a quiet empty element. */
export class HHRenderError extends Error {
  constructor(message: string, options?: { cause: unknown }) {
    super(message, options);
    this.name = 'HHRenderError';
  }
}

// The address attributes: a value bound inside one is a NAVIGATION target, not
// text, so the scheme rule below applies to it. Nothing in the kits binds one
// today; the refusal is here so the first caller cannot be the incident.
const ADDRESS_ATTRIBUTES = new Set(['href', 'src', 'action', 'formaction', 'xlink:href', 'poster', 'cite']);
const EVENT_ATTRIBUTE = /^on[a-z]+$/i;
const NAME_BOUNDARY = /[\s=/>"'()]/;
const WHITESPACE = /\s/;

// Attribute VALUES the factory refuses outright: escaping cannot make a
// binding inside one safe, so nothing but a static string may sit there. The
// r5 review's probes ran code through values, to, from and data that no
// scheme check can see (the value is decoded again after escaping: by the
// SMIL animation, by the object's own document), and set arbitrary CSS
// through style. Each entry carries the reason the refusal names.
const REFUSED_VALUE_ATTRIBUTES: Record<string, string> = {
  srcdoc: 'the parser entity-decodes srcdoc back into a document, so escaping cannot make a binding there safe',
  style: 'a value inside a style attribute sets arbitrary CSS, and escaping cannot make that safe',
  values: 'the SVG animation element decodes the value and applies it to its target attribute, escaping and all',
  to: 'the SVG animation element decodes the value and applies it to its target attribute, escaping and all',
  from: 'the SVG animation element decodes the value and applies it to its target attribute, escaping and all',
  data: 'the object element loads the address as a document of its own, so escaping cannot make a binding there safe',
};

/** A bare attribute name: nothing but name characters, so the string can
 * become nothing but a name (never a value, never a handler, never two
 * attributes). The pattern runs before the allow list: a family match alone
 * is not a name, so a bound string carrying an equals or a space is refused
 * even inside an allowed namespace. */
const ATTRIBUTE_NAME = /^[a-zA-Z_][a-zA-Z0-9_:.-]*$/;

// The fail-closed allow lists. A binding is accepted only in a position these
// name; every other position is refused. VALUE: attributes whose quoted value
// may take a binding (the kits bind class, role, the SVG path d, and the data
// and aria namespaces; everything else a kit grows adds an entry here or a
// named refusal above). BOUND NAMES: the bare names a binding may take where
// an attribute name goes: the one boolean flag the kits compose, the same
// namespaces, and the address names, whose value the address rule then judges.
// FAMILIES: the namespace prefixes, matched on the lower cased name.
const ALLOWED_VALUE_ATTRIBUTES = new Set(['class', 'role', 'd']);
const ALLOWED_VALUE_FAMILIES = ['data-', 'aria-'];
const ALLOWED_BOUND_NAMES = new Set(['disabled']);

const isAllowedValueAttribute = (lowerName: string): boolean =>
  ALLOWED_VALUE_ATTRIBUTES.has(lowerName) || ALLOWED_VALUE_FAMILIES.some((f) => lowerName.startsWith(f));

const isAllowedBoundName = (name: string): boolean =>
  ALLOWED_BOUND_NAMES.has(name) ||
  isAllowedValueAttribute(name) ||
  ADDRESS_ATTRIBUTES.has(name);

const describeValue = (v: unknown): string => {
  if (typeof v === 'string') return JSON.stringify(v);
  if (Array.isArray(v)) return 'an array';
  return `a ${typeof v} value`;
};

/** An address is safe when it carries no scheme other than http or https, or
 * no scheme at all (relative, rooted, fragment, protocol relative). The probe
 * folds the characters a URL parser ignores, so a line in the middle of the
 * scheme cannot smuggle it past. */
function isSafeAddress(value: string): boolean {
  const probe = value.replace(/[\t\n\r]/g, '').replace(/^[\x00-\x20]+/, '').replace(/[\x00-\x20]+$/, '');
  const m = probe.match(/^([a-zA-Z][a-zA-Z0-9+.-]*):/);
  return m === null || /^https?:$/i.test(m[0]);
}

/** Where a binding sits, as the HTML parser would see it. `region` is the
 * parser state the binding lands in, `unknown` being the sticky state for
 * markup this scanner cannot classify (every binding in and after it is
 * refused); `name` and `quote` carry the attribute when the binding is
 * inside a value; `valuePrefix` says whether static text already precedes
 * the binding inside that value; `raw` names the RAWTEXT element whose body
 * the binding landed in (script or style); `tag` is the open tag's lower
 * cased name while a tag is open; `nameRun` is the run of name characters
 * the parser would join across this position, so a binding landing against
 * a non-empty run continues a name that was never checked. */
interface BindingContext {
  region: 'text' | 'comment' | 'tag' | 'quoted' | 'unquoted' | 'rawtext' | 'unknown';
  name: string;
  quote: string;
  valuePrefix: boolean;
  raw: string;
  tag: string;
  nameRun: string;
}

/** The attribute name that ends just before the value opening at index `at`
 * (an empty string when the quote does not follow `name =`, so it is not an
 * attribute value opening at all). */
function attributeNameEndingAt(text: string, at: number): string {
  let i = at - 1;
  while (i >= 0 && WHITESPACE.test(text[i])) i--;
  if (i < 0 || text[i] !== '=') return '';
  i--;
  while (i >= 0 && WHITESPACE.test(text[i])) i--;
  const end = i;
  while (i >= 0 && !NAME_BOUNDARY.test(text[i])) i--;
  return text.slice(i + 1, end + 1);
}

/** The attribute name that ends just before the equals at index `eqAt`, when
 * the equals itself sits in the same static chunk. */
function nameEndingBeforeEq(text: string, eqAt: number): string {
  let i = eqAt - 1;
  while (i >= 0 && WHITESPACE.test(text[i])) i--;
  const end = i;
  while (i >= 0 && !NAME_BOUNDARY.test(text[i])) i--;
  return text.slice(i + 1, end + 1);
}

/** The index just past the comment close that ends first at or after `from`:
 * `-->` per the common case, `--!>` per the parser's bang close. The abrupt
 * empty forms `<!-->` and `<!--->` are handled where the comment opens. */
function commentEnd(text: string, from: number): number {
  const plain = text.indexOf('-->', from);
  const bang = text.indexOf('--!>', from);
  if (plain === -1 && bang === -1) return -1;
  if (plain === -1) return bang + 4;
  if (bang === -1) return plain + 3;
  return bang < plain ? bang + 4 : plain + 3;
}

/** A small forward scanner over a template's static text, carried ACROSS the
 * values (an interpolated value emits escaped text or a nested template's own
 * static text, never structure of its own), so it knows what the HTML parser
 * would be parsing at each binding. The states are the parser's: text, a
 * comment, a tag between attributes, a quoted or unquoted attribute value,
 * the body of a RAWTEXT element (script or style, which the parser reads as
 * code or CSS until the close tag), and unknown, the sticky state for
 * anything else: a malformed tag, a quote that does not open a value (the
 * parser folds it into a bogus attribute name and keeps reading attributes
 * after it), a doctype or processing instruction, or a comment inside a
 * script body (the parser's double-escaped state keeps the body open past
 * the first close tag). Nothing is guessed: a state the scanner cannot name
 * refuses every binding from there on. The scanner also carries the
 * attribute name a bound bare name or a trusted sub-template's trailing name
 * emitted, so the value that follows `=` directly after it is checked
 * against THAT name. The r3 review's probes ran script through the check
 * before last, which read only the tail of the text immediately before a
 * value; the r6 rule is that no position is trusted unless the scanner
 * names it. */
function makeBindingScanner() {
  let region: BindingContext['region'] = 'text';
  let name = '';
  let quote = '';
  let valuePrefix = false;
  let raw = '';
  // The open tag's name, lowercased, while a tag's markup is open.
  let tag = '';
  let tagEnded = false;
  // The run of name characters ending at the cursor: extended by name
  // characters and by an accepted bound name, reset by every boundary, so a
  // binding landing against a non-empty run continues a name mid-word.
  let nameRun = '';
  // The last complete run of name characters seen in this tag, kept across
  // whitespace and across chunk ends: the `=` that binds a value to it may
  // sit after spaces or in a later chunk. Reset once the name is consumed
  // into a value, refused, or the tag ends.
  let lastName = '';
  // A consumed `=` waiting for its value, and the attribute name it belongs
  // to: the value may open at a quote after spaces, or in a later chunk.
  let eqPending = false;
  let eqName = '';

  const enterUnknown = (): void => {
    region = 'unknown';
  };

  const feed = (text: string): void => {
    const n = text.length;
    let i = 0;
    while (i < n) {
      const ch = text[i];
      if (region === 'unknown') return;
      if (region === 'text') {
        if (ch === '<') {
          if (text.startsWith('<!--', i)) {
            // The two abrupt empty forms are complete comments per the
            // parser; anything else needs a real close.
            if (text.startsWith('<!-->', i)) {
              i += 5;
              continue;
            }
            if (text.startsWith('<!--->', i)) {
              i += 6;
              continue;
            }
            const end = commentEnd(text, i + 4);
            if (end === -1) {
              region = 'comment';
              break;
            }
            i = end;
            continue;
          }
          if (text[i + 1] === '!' || text[i + 1] === '?') {
            // Doctype, CDATA, bogus comment, processing instruction: parser
            // states this scanner does not model, so it refuses from here on.
            enterUnknown();
            continue;
          }
          region = 'tag';
          tag = '';
          tagEnded = text[i + 1] === '/';
          nameRun = '';
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
          if (tag === 'script' || tag === 'style') {
            region = 'rawtext';
            raw = tag;
          } else {
            region = 'text';
          }
          tag = '';
          tagEnded = false;
          nameRun = '';
          lastName = '';
          eqPending = false;
          eqName = '';
          quote = '';
          name = '';
        } else if (eqPending && (ch === '"' || ch === "'")) {
          // A value opening: `=`, optional whitespace, then the quote. A
          // quote with no equals behind it is NOT a value opening: the
          // parser folds it into a bogus attribute name and keeps reading
          // attributes, so the scanner refuses instead of guessing.
          name = attributeNameEndingAt(text, i) || eqName;
          if (name === '') {
            enterUnknown();
            return;
          }
          quote = ch;
          region = 'quoted';
          valuePrefix = false;
          nameRun = '';
          lastName = '';
          eqPending = false;
          eqName = '';
          i++;
        } else if (eqPending && !WHITESPACE.test(ch)) {
          // An unquoted value starts here (the tag's `>` was handled above).
          name = eqName;
          if (name === '') {
            enterUnknown();
            return;
          }
          region = 'unquoted';
          valuePrefix = true;
          nameRun = '';
          lastName = '';
          eqPending = false;
          eqName = '';
          i++;
        } else if (ch === '=') {
          eqPending = true;
          eqName = nameEndingBeforeEq(text, i) || lastName;
          nameRun = '';
          lastName = '';
          let j = i + 1;
          while (j < n && WHITESPACE.test(text[j])) j++;
          if (j >= n) {
            // The static text ends at or just after the `=`: the binding
            // lands in this attribute's value position, unquoted.
            name = eqName;
            if (name === '') {
              enterUnknown();
              return;
            }
            region = 'unquoted';
            valuePrefix = false;
            eqPending = false;
            eqName = '';
            break;
          }
          if (text[j] !== '"' && text[j] !== "'") {
            // An unquoted value starts in this chunk.
            name = eqName;
            if (name === '') {
              enterUnknown();
              return;
            }
            region = 'unquoted';
            valuePrefix = false;
            eqPending = false;
            eqName = '';
            i = j;
            continue;
          }
          // A quoted value: the `=` waits for the quote branch above.
          i++;
        } else if (ch === '"' || ch === "'") {
          enterUnknown();
          return;
        } else if (ch === '/') {
          tagEnded = true;
          nameRun = '';
          lastName = '';
          eqPending = false;
          eqName = '';
          i++;
        } else if (WHITESPACE.test(ch)) {
          // Whitespace ends the name run at the cursor (a binding here
          // starts a name, it does not join one) but keeps the name an `=`
          // may still bind a value to, across spaces and across chunks.
          if (nameRun !== '') tagEnded = true;
          nameRun = '';
          i++;
        } else if (NAME_BOUNDARY.test(ch)) {
          // Parentheses and the rest of the boundary set are not tag or
          // attribute name characters: whatever the parser does with them,
          // it is not a state this scanner can name.
          enterUnknown();
          return;
        } else {
          // Still between attributes: name characters build the tag's name
          // until the first whitespace or slash ends it.
          if (!tagEnded) tag += ch.toLowerCase();
          nameRun += ch;
          lastName = nameRun;
          eqPending = false;
          eqName = '';
          i++;
        }
      } else if (region === 'rawtext') {
        // The body of a script or style element: the parser reads everything
        // as the element's content until the close tag's own name ends, case
        // insensitively. A `<!--` inside the body puts the parser into the
        // double-escaped state, where it stays open PAST the first close
        // tag: the scanner cannot follow that, so it refuses from there on.
        const close = `</${raw}`;
        const probe = text.slice(i).toLowerCase();
        const commentOpen = probe.indexOf('<!--');
        let at = -1;
        let from = 0;
        for (;;) {
          at = probe.indexOf(close, from);
          if (at === -1) break;
          const after = probe[at + close.length];
          if (after === undefined || WHITESPACE.test(after) || after === '>' || after === '/') break;
          from = at + 1;
        }
        if (commentOpen !== -1 && (at === -1 || commentOpen < at)) {
          enterUnknown();
          return;
        }
        if (at === -1) {
          i = n;
          break;
        }
        // Resume inside the close tag: its '>' ends it in the tag branch.
        region = 'tag';
        tag = '';
        tagEnded = true;
        nameRun = '';
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
          nameRun = '';
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
            region = 'text';
            tag = '';
            tagEnded = false;
          } else {
            region = 'tag';
          }
          name = '';
          nameRun = '';
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
    context: (): BindingContext => ({ region, name, quote, valuePrefix, raw, tag, nameRun }),
    // The factory calls this when a bare attribute name is accepted in the
    // name position: the name's characters are what the parser now sees, so
    // they join any later name characters and bind the value after the next
    // `=` exactly as if the name had been written statically.
    holdBoundName: (bound: string): void => {
      nameRun = bound;
      lastName = bound;
    },
  };
}

/** The position checks, run at the factory across the whole template and
 * recursive through trusted sub-templates, so every binding is judged in the
 * context its markup actually lands in. The scanner classifies each
 * binding's position; the contract is FAIL CLOSED, so the check decides
 * between the three allowed positions and everything else:
 *   text                          allowed: the value lands as escaped text;
 *   a quoted attribute value      allowed only when the attribute is on the
 *                                 allow list (class, role, d, data-*, aria-
 *                                 *); an address attribute is held to its
 *                                 own rule instead: wholly one value, and a
 *                                 scheme of http, https or none;
 *   the attribute name position   allowed only for a bare name on the
 *                                 bound-name allow list, and never
 *                                 mid-word: a binding that continues a
 *                                 name, stands next to another binding, or
 *                                 is followed by a name character is
 *                                 refused;
 *   everything else               refused, and a position the scanner
 *                                 cannot classify refuses every binding
 *                                 after it.
 * The named refusals the reviews probed keep their names: event attributes,
 * srcdoc, the style attribute, the SVG animation values, to and from, the
 * object data attribute, unquoted values, script and style bodies, and, now
 * without exception, the interior of a script or base tag, value or name,
 * whatever the scheme, because a relative or plain http address there still
 * chooses what the parser loads. */
function checkBindingPositions(strings: readonly string[], values: readonly unknown[]): void {
  const scan = makeBindingScanner();

  // A trusted sub-template's static text feeds the SAME scanner, and its own
  // bindings are re-checked at the positions they actually land in, so a
  // template composed inside a tag or a quoted value can never carry a
  // binding that was judged as mere text when it stood alone.
  const checkValue = (v: unknown, nextStatic: string): void => {
    const ctx = scan.context();
    if (ctx.region === 'unknown') {
      throw new HHRenderError(
        'html(): the literal text before this binding leaves the parser in a state this scanner cannot classify, ' +
          'so every binding after it is refused: fix the surrounding markup so the position is plain',
      );
    }
    if (ctx.region === 'comment') {
      throw new HHRenderError(
        'html(): refusing a binding inside a comment: a comment body is not one of the positions a binding may take',
      );
    }
    if (ctx.region === 'rawtext') {
      throw new HHRenderError(
        ctx.raw === 'script'
          ? `html(): refusing a binding inside a script element body: the parser reads the body as code until the close tag, so escaping cannot make a binding there safe`
          : `html(): refusing a binding inside a style element body: the parser reads the body as CSS until the close tag, so escaping cannot make a binding there safe`,
      );
    }
    if (ctx.region === 'text') {
      if (Array.isArray(v)) {
        for (const item of v) checkValue(item, nextStatic);
        return;
      }
      if (isTrusted(v)) feedTrusted(v);
      return;
    }
    if (ctx.region === 'tag') {
      // The attribute NAME position. Inside a script or base tag nothing may
      // bind at all: the element's src or href chooses what the parser
      // loads, and a relative or plain http address there is still a choice.
      if (ctx.tag === 'script' || ctx.tag === 'base') {
        throw new HHRenderError(
          `html(): refusing a binding inside a ${ctx.tag} tag: the element's src or href chooses what the parser loads, so nothing may be bound inside one`,
        );
      }
      if (v == null || v === false || v === '') return;
      if (isTrusted(v)) {
        feedTrusted(v);
        return;
      }
      // A binding against a non-empty name run continues a name that was
      // never checked: static text before it, or another binding's name.
      if (ctx.nameRun !== '') {
        throw new HHRenderError(
          `html(): refusing a binding that continues the name ${JSON.stringify(ctx.nameRun)} inside a tag: ` +
            `write the whole name statically, never complete one from a value`,
        );
      }
      if (typeof v === 'string') {
        const bound = v.trim();
        if (bound !== '' && ATTRIBUTE_NAME.test(bound) && isAllowedBoundName(bound)) {
          scan.holdBoundName(bound);
          // The parser joins the bound name with any name characters that
          // follow it, so the next chunk must open with a boundary.
          if (nextStatic !== '' && !NAME_BOUNDARY.test(nextStatic[0])) {
            throw new HHRenderError(
              `html(): refusing the bound name ${JSON.stringify(bound)} followed by a name character: ` +
                `the parser joins the two into one name that was never checked; write the whole name statically`,
            );
          }
          return;
        }
      }
      throw new HHRenderError(
        `html(): refusing ${describeValue(v)} in the attribute name position: ` +
          `only null, false, the empty string, a trusted template, or a bare attribute name on the allow list ` +
          `may be bound where an attribute name goes; the scanner fails closed on everything else`,
      );
    }
    // The value positions: quoted, or unquoted (refused). Inside a script or
    // base tag nothing may bind, whatever the attribute and whatever the
    // scheme would pass.
    if (ctx.tag === 'script' || ctx.tag === 'base') {
      throw new HHRenderError(
        `html(): refusing a value in the ${ctx.name} attribute of a ${ctx.tag} tag: ` +
          `the element's src or href chooses what the parser loads, so nothing may be bound inside one`,
      );
    }
    if (EVENT_ATTRIBUTE.test(ctx.name)) {
      throw new HHRenderError(`html(): refusing a value in the event attribute ${ctx.name}: these kits take no inline handlers`);
    }
    const refused = REFUSED_VALUE_ATTRIBUTES[ctx.name.toLowerCase()];
    if (refused !== undefined) {
      throw new HHRenderError(`html(): refusing a value in the ${ctx.name} attribute: ${refused}`);
    }
    if (ctx.region === 'unquoted') {
      throw new HHRenderError(
        `html(): refusing a binding in the unquoted attribute ${ctx.name || '(name position)'}: quote the attribute, or pass the value as text`,
      );
    }
    if (ADDRESS_ATTRIBUTES.has(ctx.name.toLowerCase())) {
      // Wholly one value: nothing static before the binding inside the value,
      // and the static text after it opens with the closing quote. An address
      // split over a static prefix or a second binding reassembles a scheme
      // (java + script:) the per-value scheme check can never see.
      if (ctx.valuePrefix || !nextStatic.startsWith(ctx.quote)) {
        throw new HHRenderError(
          `html(): refusing a binding inside the address attribute ${ctx.name} that is not wholly one value: ` +
            `bind the whole address as one value, or write the attribute statically`,
        );
      }
      if (v == null || v === false) return;
      if (typeof v === 'string' && isSafeAddress(v)) return;
      if (typeof v === 'number') return;
      throw new HHRenderError(
        `html(): refusing ${describeValue(v)} as the ${ctx.name} address: ` +
          `only http, https and scheme-free (relative) addresses render`,
      );
    }
    if (isAllowedValueAttribute(ctx.name.toLowerCase())) {
      // An ordinary escaped value, or a trusted sub-template whose own
      // bindings are re-checked in this position by the recursion above.
      if (isTrusted(v)) feedTrusted(v);
      return;
    }
    throw new HHRenderError(
      `html(): refusing a value in the ${ctx.name} attribute: the attribute is not on the allow list of attributes ` +
        `that may take a bound value; the scanner fails closed`,
    );
  };

  const feedTrusted = (tpl: Template): void => {
    for (let i = 0; i < tpl.strings.length; i++) {
      scan.feed(tpl.strings[i]);
      if (i < tpl.values.length) checkValue(tpl.values[i], tpl.strings[i + 1] ?? '');
    }
  };

  for (let i = 0; i < values.length; i++) {
    scan.feed(strings[i] ?? '');
    checkValue(values[i], strings[i + 1] ?? '');
  }
}

export function html(strings: readonly string[], ...values: unknown[]): Template {
  // Only a real tagged-template call may build a Template. The engine hands a
  // real call a TemplateStringsArray, which carries `raw`; a plain array (say
  // parsed from JSON and passed as the first argument) does not, and its items
  // would be emitted as literal markup. Refused here, at the factory.
  const raw = (strings as TemplateStringsArray).raw;
  if (!Array.isArray(strings) || !Array.isArray(raw) || raw.length !== strings.length) {
    throw new HHRenderError(
      'html(): the strings argument must be a real template strings array (call html as a tagged template, html`...`), not a plain array',
    );
  }
  checkBindingPositions(strings, values);
  return { [TRUSTED]: true, strings, values };
}

const isTrusted = (v: unknown): v is Template =>
  typeof v === 'object' && v !== null && (v as Record<symbol, unknown>)[TRUSTED] === true;

/** Emits one interpolated value. Everything that is not a Template branded by
 * the module-private symbol is escaped text, including arrays element-wise and
 * including an object that happens to carry `strings`, `values` or `unsafe`
 * keys: shape is never trust, the brand is. */
function emitValue(v: unknown): string {
  if (v == null || v === false) return '';
  if (Array.isArray(v)) return v.map(emitValue).join('');
  if (isTrusted(v)) return emitTemplate(v);
  return escapeText(String(v));
}

function emitTemplate(tpl: Template): string {
  let out = '';
  for (let i = 0; i < tpl.strings.length; i++) {
    out += tpl.strings[i];
    if (i < tpl.values.length) out += emitValue(tpl.values[i]);
  }
  return out;
}

/** Renders a template to markup. A value that is not a branded template is
 * escaped, never emitted raw. */
export function renderTemplate(tpl: unknown): string {
  return isTrusted(tpl) ? emitTemplate(tpl) : emitValue(tpl);
}

export function css(strings: readonly string[], ...values: unknown[]): string {
  return strings.reduce((out, s, i) => out + s + (i < values.length ? String(values[i]) : ''), '');
}

type PropertySpec = { attribute?: boolean | string } | undefined;

const isBrowser = typeof HTMLElement !== 'undefined';

// The base class is declared against the runtime's HTMLElement when one exists.
const ElementBase: typeof HTMLElement = (isBrowser ? HTMLElement : class {}) as typeof HTMLElement;

export class HHElement extends ElementBase {
  static properties: Record<string, PropertySpec> = {};
  static styles: string = '';

  private _props: Record<string, unknown> = {};
  private _connected = false;

  static get observedAttributes(): string[] {
    return Object.entries(this.properties)
      .filter(([, spec]) => spec && spec.attribute !== false)
      .map(([name]) => name.toLowerCase());
  }

  constructor() {
    super();
    this._props = {};
    // Reactive properties are read through prop(name), never through a
    // compiler-generated accessor: native class fields are defined, not
    // assigned, and an accessor defined here cannot be redefined by the
    // subclass's field initialiser. The field IS the default; _props holds the
    // override from the attribute or renderToString.
  }

  /** The reactive property read: the override first, then the field default. */
  protected prop(name: string): unknown {
    if (name in this._props) return this._props[name];
    return (this as unknown as Record<string, unknown>)[name];
  }

  attributeChangedCallback(name: string, _old: string | null, value: string | null): void {
    // An attribute REMOVED falls back to the field default: keeping a null in
    // _props would shadow the field forever and either render a stale kit or
    // refuse a state the field never named.
    if (value === null) delete this._props[name.toLowerCase()];
    else this._props[name.toLowerCase()] = value;
    this.requestUpdate();
  }

  // The browser path renders into an open shadow root; nothing outside can see
  // in, and the document's tokens inherit through the boundary.
  connectedCallback(): void {
    this._connected = true;
    this.requestUpdate();
  }

  protected requestUpdate(): void {
    if (!isBrowser || !this._connected) return;
    const ctor = this.constructor as typeof HHElement;
    // The markup is built BEFORE the shadow root is attached: a render that
    // throws (an unknown state, a refused template) must never leave an empty
    // element standing. The failure is rethrown NAMED, with the element and the
    // cause, so the browser console says what broke instead of showing a blank.
    let body: string;
    try {
      body = renderTemplate(this.render());
    } catch (e) {
      throw new HHRenderError(
        `<${this.localName}> failed to render: ${e instanceof Error ? e.message : String(e)}`,
        { cause: e },
      );
    }
    const root = this.shadowRoot ?? this.attachShadow({ mode: 'open' });
    root.innerHTML = `<style>${ctor.styles}</style>${body}`;
  }

  render(): Template {
    return html``;
  }

  /** Node-side render, in parts: the sheet and the same markup the element
   * would paint, separately. The static harness uses this to ship each kit's
   * sheet once and bind the markup into a declarative shadow root. */
  static renderParts(props: Record<string, unknown> = {}): { styles: string; markup: string } {
    const ctor = this as typeof HHElement;
    const instance = new this();
    (instance as unknown as { _props: Record<string, unknown> })._props = { ...props };
    return { styles: ctor.styles, markup: renderTemplate(instance.render()) };
  }

  /** Node-side render: the same template the element would paint, as a string. */
  static renderToString(props: Record<string, unknown> = {}): string {
    const parts = this.renderParts(props);
    return `<style>${parts.styles}</style>${parts.markup}`;
  }
}

/** Registers a kit as a custom element in the browser; a no-op in Node. */
export function defineElement(tag: string, ctor: typeof HHElement): void {
  if (typeof customElements === 'undefined') return;
  if (!customElements.get(tag)) customElements.define(tag, ctor as unknown as CustomElementConstructor);
}
