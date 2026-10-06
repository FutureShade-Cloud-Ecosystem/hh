// Test-only module resolution hook for @hardscapehouse/pos-kit, on the
// api-client and design-system lane pattern (a minimal --import hook, no
// dependencies). The package's sources import their siblings with the
// runtime-correct .js extension (what tsc and vite emit), and Node 22's
// native type stripping loads the .ts sources directly, so for the test
// process this hook rewrites a relative './x.js' specifier to './x.ts' when
// that file exists. Package sources are untouched. This file is used ONLY
// via
//   node --import ./src/_ts-js-loader.mjs --test 'src/ported/*.test.mjs'
// The system Node is 20 and cannot type-strip .ts; the suite runs on
// /snap/bin/node (22), the same binary the CI job pins.
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
    // A deep import of a sibling workspace package's source (the same runtime-correct .js
    // spelling): try the .ts file the specifier names.
    if (specifier.startsWith('@hardscapehouse/') && specifier.endsWith('.js')) {
      try {
        return await next(specifier.slice(0, -3) + '.ts', context);
      } catch {}
    }
    return next(specifier, context);
  }
`),
  pathToFileURL('./'),
);
