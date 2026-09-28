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
//               is always rendered as text, never as markup. There is no raw
//               markup escape hatch anywhere in the package; the icon module
//               builds its geometry through the same escaped attribute path.
//   css         a style string. No resolving, no shims.
//   HHElement   reactive properties from `static properties` (attribute
//               reflected), a render() into an open shadow root in the browser,
//               and a static renderToString(props) for Node, the tests and the
//               static harness. No event protocol: these kits are pattern
//               renders, their buttons are real <button> elements, and the
//               shells that adopt them attach listeners the normal way.
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

export function html(strings: readonly string[], ...values: unknown[]): Template {
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
    this._props[name.toLowerCase()] = value;
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
    const root = this.shadowRoot ?? this.attachShadow({ mode: 'open' });
    const ctor = this.constructor as typeof HHElement;
    root.innerHTML = `<style>${ctor.styles}</style>${renderTemplate(this.render())}`;
  }

  render(): Template {
    return html``;
  }

  /** Node-side render: the same template the element would paint, as a string. */
  static renderToString(props: Record<string, unknown> = {}): string {
    const ctor = this as typeof HHElement;
    const instance = new this();
    (instance as unknown as { _props: Record<string, unknown> })._props = { ...props };
    return `<style>${ctor.styles}</style>${renderTemplate(instance.render())}`;
  }
}

/** Registers a kit as a custom element in the browser; a no-op in Node. */
export function defineElement(tag: string, ctor: typeof HHElement): void {
  if (typeof customElements === 'undefined') return;
  if (!customElements.get(tag)) customElements.define(tag, ctor as unknown as CustomElementConstructor);
}
