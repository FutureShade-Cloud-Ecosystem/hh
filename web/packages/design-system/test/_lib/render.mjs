// The Node-side render the package tests assert against, over Lit's server
// render. The element instance is never connected, so Lit's own update loop
// never runs (its scheduled update waits forever on connectedCallback): the
// template the kit's render() returns is the only thing that renders, and a
// refused value throws here, synchronously, to the caller.
//
// Import order matters and is a contract: the DOM shim must be on globalThis
// before any LitElement subclass is defined, so anything that renders kit
// classes in Node imports this module BEFORE importing from ../src/. The
// guard IS the installer: it captures the process's fetch, runs the shim's
// own install, and puts the fetch back, so the shim's node-fetch never
// becomes the fetch of the suites that run after this file in the shared
// runner process.

import '../../src/lit-dom-fetch-guard.js';
import { render } from '@lit-labs/ssr';
import { collectResultSync } from '@lit-labs/ssr/lib/render-result.js';

/** Renders a kit in the given props (state above all) to the markup string
 * the browser would commit. Throws synchronously on a refused value. */
export const renderKit = (Kit, props = {}) => {
  const el = new Kit();
  for (const [name, value] of Object.entries(props)) el[name] = value;
  return collectResultSync(render(el.render()));
};

/** Renders a template result to markup, the same path. */
export const renderTemplate = (tpl) => collectResultSync(render(tpl));
