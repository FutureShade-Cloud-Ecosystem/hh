// Lit's server shim copies its window onto globalThis, and that window's
// fetch is node-fetch: after the first install, every later suite of the
// root bun run (one process for the whole tree) would stream response bodies
// through node-fetch, whose body is no ReadableStream, and a streaming
// consumer behind it reads nothing. Each importer evaluates this module
// before the shim's installer and calls restoreLitShimFetch() in its module
// body, right after the installer: the process keeps the fetch it started
// with, and Lit's render path, which never calls fetch, keeps the rest of
// the shim. Where a window already existed (the browser) the installer is a
// no-op and the restore puts the same fetch back.
const held = globalThis.fetch;
export const restoreLitShimFetch = () => {
  globalThis.fetch = held;
};
