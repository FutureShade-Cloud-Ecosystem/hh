// Test-only module resolution hook for @hh/design-system, on the api-client
// lane's pattern (a minimal --import hook, no dependencies). Package sources
// import their siblings with the runtime-correct .js extension (what tsc and
// vite emit), and Node 22's native type stripping loads the .ts sources
// directly, so for the test and harness processes this hook rewrites a relative
// './x.js' specifier to './x.ts' when that file exists. Package sources are
// untouched. This file is used ONLY via
//   node --import ./src/_ts-js-loader.mjs --test 'test/*.test.mjs'
// and via harness/serve.mjs. The system Node is 20 and cannot type-strip .ts;
// the suite runs on /snap/bin/node (22).
import { register } from 'node:module';
import { pathToFileURL } from 'node:url';

register(
  'data:text/javascript,' +
    encodeURIComponent(`
  import { existsSync } from 'node:fs';
  import { fileURLToPath } from 'node:url';
  export async function resolve(specifier, context, next) {
    if ((specifier.startsWith('./') || specifier.startsWith('../')) && specifier.endsWith('.js')) {
      const tsSpec = specifier.slice(0, -3) + '.ts';
      try {
        const resolved = new URL(tsSpec, context.parentURL);
        if (existsSync(fileURLToPath(resolved))) {
          return next(tsSpec, context);
        }
      } catch {}
    }
    return next(specifier, context);
  }
`),
  pathToFileURL('./'),
);
