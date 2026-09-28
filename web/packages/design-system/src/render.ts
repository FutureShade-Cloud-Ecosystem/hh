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
//               and arrays compose, and a caller's object or a caller's string
//               is always rendered as text, never as markup. The factory also
//               refuses what escaping cannot make safe: a first argument that
//               is not a real template strings array, a binding in an unquoted
//               attribute, a value in an event attribute, and an address whose
//               scheme is not http, https or absent. There is no raw markup
//               escape hatch anywhere in the package; the icon module builds
//               its geometry through the same escaped attribute path.
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
const ATTRIBUTE_TAIL = /([^\s=/>"'()]+)\s*=\s*(["']?)$/;

/** An address is safe when it carries no scheme other than http or https, or
 * no scheme at all (relative, rooted, fragment, protocol relative). The probe
 * folds the characters a URL parser ignores, so a line in the middle of the
 * scheme cannot smuggle it past. */
function isSafeAddress(value: string): boolean {
  const probe = value.replace(/[\t\n\r]/g, '').replace(/^[\x00-\x20]+/, '').replace(/[\x00-\x20]+$/, '');
  const m = probe.match(/^([a-zA-Z][a-zA-Z0-9+.-]*):/);
  return m === null || /^https?:$/i.test(m[0]);
}

/** The position checks, run at the factory against the template's own static
 * text: what sits immediately before each value tells the binding's position.
 *   directly after = (no quote)          refused: the value would be parsed as
 *                                        part of the attribute's unquoted value,
 *                                        where escaping cannot save it;
 *   inside an event attribute (on*)      refused: an escaped string inside an
 *                                        event handler is still code;
 *   inside an address attribute          refused unless the value is http,
 *                                        https or scheme-free (relative).
 * Everything else is the documented text path. */
function checkBindingPositions(strings: readonly string[], values: readonly unknown[]): void {
  for (let i = 0; i < values.length; i++) {
    const before = strings[i] ?? '';
    const m = before.match(ATTRIBUTE_TAIL);
    if (!m) continue;
    const [, name, quote] = m;
    if (quote === '') {
      throw new HHRenderError(
        `html(): refusing a binding in the unquoted attribute ${name}: quote the attribute, or pass the value as text`,
      );
    }
    if (EVENT_ATTRIBUTE.test(name)) {
      throw new HHRenderError(`html(): refusing a value in the event attribute ${name}: these kits take no inline handlers`);
    }
    if (ADDRESS_ATTRIBUTES.has(name.toLowerCase())) {
      const v = values[i];
      if (v == null || v === false) continue;
      if (typeof v === 'string' && isSafeAddress(v)) continue;
      if (typeof v === 'number') continue;
      throw new HHRenderError(
        `html(): refusing ${typeof v === 'string' ? JSON.stringify(v) : `a ${typeof v} value`} as the ${name} address: ` +
          `only http, https and scheme-free (relative) addresses render`,
      );
    }
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
