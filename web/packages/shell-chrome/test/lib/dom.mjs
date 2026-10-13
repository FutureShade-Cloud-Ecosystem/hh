// The DOM the chrome tests stand on, set up by the tests themselves. The Lit
// server shim (@lit-labs/ssr/lib/install-global-dom-shim.js) installs itself
// only when no `window` exists on the global, and under the root `bun test`
// every test file shares one process: a file that leaves a stub `window`
// behind (the Manager Home quick action suite does) made the shim a silent
// no-op, so `Element`, `HTMLElement` and `customElements` were never defined
// and these tests died with "Element is not defined", depending on which files
// ran before them.
//
// This module is imported FIRST, before anything that loads Lit, and fills in
// every DOM name the shim carries that the global lacks. A `window` another
// file set is never replaced and nothing another file set is overwritten; the
// shim's own `window` is left out. With no window at all it does nothing, and
// the shim installs itself as before.

import { getWindow } from '@lit-labs/ssr/lib/dom-shim.js';

if (globalThis.window !== undefined) {
  for (const [name, value] of Object.entries(getWindow({}))) {
    if (name !== 'window' && !(name in globalThis)) globalThis[name] = value;
  }
}
