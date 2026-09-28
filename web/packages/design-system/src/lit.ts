// The minimal Lit pattern, vendored. The source brief names Lit as the kit
// substrate; the web workspace carries no dependencies and this package may add
// none, so the small part of Lit's model the kits actually use is implemented
// here, dependency-free, with the same surface names. This is the recorded
// deviation the locked spec sanctions: when the real Lit lands as a workspace
// dependency, this file is deleted and the imports re-point at it; the kits'
// render functions do not change.
//
// What is here, and deliberately what is not:
//   html        the tagged template. Builds DOM strings; text values are
//               escaped, nested templates and arrays compose, and raw markup
//               exists only through unsafeSvg, which the icon module owns.
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

export interface Template {
  strings: readonly string[];
  values: readonly unknown[];
}

export interface UnsafeHtml {
  unsafe: string;
}

const ESCAPES: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

const escapeText = (v: string): string => v.replace(/[&<>"']/g, (c) => ESCAPES[c]);

/** Marks trusted markup. Only src/state-icons.ts hands geometry to this. */
export function unsafeSvg(markup: string): UnsafeHtml {
  return { unsafe: markup };
}

export function html(strings: readonly string[], ...values: unknown[]): Template {
  return { strings, values };
}

export function css(strings: readonly string[], ...values: unknown[]): string {
  return strings.reduce((out, s, i) => out + s + (i < values.length ? String(values[i]) : ''), '');
}

/** Renders a template (nested templates, arrays, unsafe marks, plain values). */
export function renderTemplate(tpl: Template | UnsafeHtml | undefined | null): string {
  if (tpl == null) return '';
  if ((tpl as UnsafeHtml).unsafe !== undefined) return (tpl as UnsafeHtml).unsafe;
  let out = '';
  for (let i = 0; i < (tpl as Template).strings.length; i++) {
    out += (tpl as Template).strings[i];
    if (i >= (tpl as Template).values.length) continue;
    const v = (tpl as Template).values[i];
    if (v == null || v === false) continue;
    if (Array.isArray(v)) {
      for (const part of v) out += renderTemplate(part as Template);
    } else if ((v as Template).strings !== undefined) {
      out += renderTemplate(v as Template);
    } else if ((v as UnsafeHtml).unsafe !== undefined) {
      out += (v as UnsafeHtml).unsafe;
    } else {
      out += escapeText(String(v));
    }
  }
  return out;
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
