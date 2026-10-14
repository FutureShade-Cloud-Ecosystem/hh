// The Node-side render the package tests assert against, over Lit's server
// render, one folder beside the design system's own helper: the element
// instance is never connected, so Lit's own update loop never runs (its
// scheduled update waits forever on connectedCallback): the template the
// element's render() returns is the only thing that renders, and a refused
// value throws here, synchronously, to the caller.
//
// Import order matters and is a contract: the DOM shim must be on globalThis
// before any LitElement subclass is defined (./dom.mjs fills the names in when
// another file's stub window made the shim a no-op), so anything that renders element
// classes in Node imports this module BEFORE importing from ../src/.

import './dom.mjs';
import '@lit-labs/ssr/lib/install-global-dom-shim.js';
import { render } from '@lit-labs/ssr';
import { collectResultSync } from '@lit-labs/ssr/lib/render-result.js';

/** Renders an element in the given props to the markup string the browser
 * would commit. Throws synchronously on a refused value. */
export const renderKit = (Kit, props = {}) => {
  const el = new Kit();
  for (const [name, value] of Object.entries(props)) el[name] = value;
  return collectResultSync(render(el.render()));
};

/** Renders a template result to markup, the same path. */
export const renderTemplate = (tpl) => collectResultSync(render(tpl));
