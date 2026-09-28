// Test-only module resolution hook for @hh/auth. The package's sources
// import their siblings with the runtime-correct .js extension (what tsc and
// vite emit), and Node's native TypeScript type stripping loads the .ts
// sources directly, so for the test process this hook rewrites a relative
// './x.js' specifier to './x.ts' when that file exists. Package sources are
// untouched; this file lives in test/ (it is test-only and must not sit on
// the package's src surface) and is used ONLY via
//   node --import ./test/_ts-js-loader.mjs --test test/
// following the ERP app's loader pattern (a minimal --import hook, no
// dependencies), the same hook the api-client package carries.
import { register } from "node:module";
import { pathToFileURL } from "node:url";

register(
  "data:text/javascript," +
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
  pathToFileURL("./"),
);
