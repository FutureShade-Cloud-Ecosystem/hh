// The chrome tests carry their own DOM. Under the root `bun test` every file
// shares one process, so a file that leaves a stub `window` on the global (the
// Lit server shim installs itself only when no window exists) once left these
// tests without `Element`. This suite loads the helpers in a fresh process
// behind exactly such a stub and holds that they still get a DOM, and that the
// stub is left as the other file set it.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const probe = fileURLToPath(new URL('./lib/window-stub-probe.mjs', import.meta.url));
const loader = fileURLToPath(new URL('./_ts-js-loader.mjs', import.meta.url));

test('behind another file’s stub window the chrome helpers still get Element, and the stub is untouched', () => {
  const args = typeof Bun === 'undefined' ? ['--import', loader, probe] : [probe];
  const run = spawnSync(process.execPath, args, { encoding: 'utf8', cwd: fileURLToPath(new URL('..', import.meta.url)) });
  assert.equal(run.status, 0, `the probe ran to the end: ${run.stderr.split('\n').slice(0, 8).join(' | ')}`);
  assert.deepEqual(JSON.parse(run.stdout), { element: 'function', page: true, stub: 'function' });
});
