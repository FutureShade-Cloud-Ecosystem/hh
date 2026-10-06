// Test-only module resolution hook for @hardscapehouse/api-client. The package's sources
// import their siblings with the runtime-correct .js extension (what tsc and
// vite emit), and Node's native TypeScript type stripping loads the .ts
// sources directly, so for the test process this hook rewrites a relative
// './x.js' specifier to './x.ts' when that file exists. The same mapping
// covers the workspace packages' deep .js paths (@hardscapehouse/<package>/src/...):
// the packages' sources carry .js specifiers for tsc and vite, their trees
// hold .ts sources, so the hook tries the .ts spelling of a workspace deep
// path first and falls back to the specifier as written when no .ts source
// answers. Package sources are untouched; this file is used ONLY via
//   node --import ./src/_ts-js-loader.mjs --test test/
// following the ERP app's loader pattern (a minimal --import hook, no
// dependencies).
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
    if (specifier.startsWith('@hardscapehouse/') && specifier.endsWith('.js')) {
      try {
        return await next(specifier.slice(0, -3) + '.ts', context);
      } catch {}
    }
    return next(specifier, context);
  }
`),
  pathToFileURL("./"),
);
