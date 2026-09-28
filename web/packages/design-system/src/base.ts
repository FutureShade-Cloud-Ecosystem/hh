// The reactive base class and the element registration, over Lit.
//
// The substrate is the real Lit library, adopted by ruling: docs/adr/0023
// (option 2, accepted; the ruling's words are "Adopt lit", relay message 046).
// This module is the whole of what this package adds around the library: the
// base class the kits extend, the registration helper they call, and the named
// error a failed kit render carries. Rendering itself is Lit's: the kits build
// their markup only through Lit's html and css tagged templates, and Lit's
// template rules are the escaping contract (an interpolated value is always a
// text node or an attribute value, never markup; there is no raw-markup
// escape hatch, and the gate in test/no-raw-markup.test.mjs refuses the
// library's unsafe directives and its static-html module by name).
//
// Why the state default is assigned in the constructor body rather than a
// class field: Lit's reactive properties are accessor pairs defined on the
// prototype, and a native class field initializer creates an own property
// that shadows the accessor, so attribute changes and property sets would
// write the field and never schedule an update. (The same reason the kits
// read their property through prop(name) in render().)

import { LitElement, type PropertyValues } from 'lit';
import type { KitState } from './state.js';

/** The named error a failed kit render carries. The name is part of the
 * contract: a shell can catch it, and a gate can assert the refusal happened
 * instead of a quiet wrong render. The kit's own scaffold throws it for an
 * unknown state (state.ts), and the base class wraps a render failure with it
 * so the browser console names the element that broke. */
export class HHRenderError extends Error {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'HHRenderError';
  }
}

export class HHElement extends LitElement {
  // The one reactive property every kit carries, declared once here so no kit
  // re-derives it. An attribute REMOVED falls back to 'ready' (the default),
  // not to null: keeping a removed attribute's null would strand the element
  // on an impossible value instead of the field default.
  static properties = {
    state: {
      attribute: true,
      converter: {
        fromAttribute: (value: string | null): string => (value === null ? 'ready' : value),
      },
    },
  };

  declare state: KitState;

  constructor() {
    super();
    // Through Lit's accessor, never a field: see the header.
    this.state = 'ready';
  }

  /** The reactive property read the kits render through. */
  protected prop(name: string): unknown {
    return (this as unknown as Record<string, unknown>)[name];
  }

  // A render that throws (an unknown state above all) is rethrown NAMED, with
  // the element and the cause, so the browser console says what broke instead
  // of showing a blank. Lit never commits a partial render: a failed update
  // leaves the element's previous paint (or nothing, on a first render), so a
  // refused render can never leave a quietly wrong kit.
  protected override update(changed: PropertyValues): void {
    try {
      super.update(changed);
    } catch (e) {
      throw new HHRenderError(
        `<${this.localName}> failed to render: ${e instanceof Error ? e.message : String(e)}`,
        { cause: e },
      );
    }
  }
}

/** Registers a kit as a custom element in the browser; a no-op in Node. */
export function defineElement(tag: string, ctor: typeof HHElement): void {
  if (typeof customElements === 'undefined') return;
  if (!customElements.get(tag)) customElements.define(tag, ctor as unknown as CustomElementConstructor);
}
