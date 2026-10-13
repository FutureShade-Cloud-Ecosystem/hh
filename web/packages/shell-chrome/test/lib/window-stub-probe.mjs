// Run in a child process by window-stub.test.mjs, never as a test of its own. It
// plays the part of another test file that leaves a stub `window` on the global
// before the shell chrome tests load (the Lit server shim installs itself only
// when no window exists), then loads the helpers the chrome tests use and builds
// what they build. It prints what a person reading a failed run needs.

globalThis.window = {
  dispatchEvent: () => true,
  addEventListener: () => {},
  removeEventListener: () => {},
};

await import('./render.mjs');
const { buildDemoPage } = await import('../../demo/demo-page.ts');

process.stdout.write(
  JSON.stringify({
    element: typeof globalThis.Element,
    page: buildDemoPage({ theme: 'dark' }).length > 0,
    stub: typeof globalThis.window.dispatchEvent,
  }),
);
