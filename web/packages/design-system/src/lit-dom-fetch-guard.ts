// Lit's server shim copies its window onto globalThis, and that window's
// fetch is node-fetch: left in place, the install makes every later suite of
// the root bun run (one process for the whole tree) stream response bodies
// through node-fetch, whose body is no ReadableStream, so a streaming
// consumer behind it reads nothing. This module is the one installer the
// workspace imports. Its evaluation captures the fetch the process has,
// calls the shim package's own install between the capture and the restore,
// and puts the captured fetch back. The three steps share one synchronous
// module body, so nothing can evaluate between them: the restore undoes
// exactly the shim's write, and the order the files are discovered in cannot
// change the outcome. Where a window already existed (the browser) the
// install is a no-op and the restore puts the same fetch back.
import '@lit-labs/ssr-dom-shim/register-css-hook.js';
import { installWindowOnGlobal } from '@lit-labs/ssr/lib/dom-shim.js';
const held = globalThis.fetch;
installWindowOnGlobal();
globalThis.fetch = held;
