// The core's sync writable sets, read straight from the Go source, so a stub
// or an app payload that drifts from the server fails a test instead of
// passing every leg against a flattering double. Plain JavaScript on purpose:
// the apps' stubs run under bare node, with no TypeScript loader.
//
// Two shapes carry a set in the core: a package level map[string]bool
// (`var writableFields = map[string]bool{ "id": true, ... }`) and a function
// that switches on the declared key (`func quoteCreateFields(...)` with
// `case "id", "customer_id", ...:` before `default:`).
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const coreDir = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "..", "core", "internal");

/** The admitted field names of one Go set under core/internal. Fails loudly on a name it cannot find. */
export function goFieldSet(file, name) {
  const src = readFileSync(join(coreDir, file), "utf8");
  const mapAt = src.indexOf(`var ${name} = map[string]bool{`);
  if (mapAt >= 0) {
    const body = src.slice(mapAt, src.indexOf("\n}", mapAt));
    const keys = [...body.matchAll(/"([a-z_0-9]+)"\s*:/g)].map((m) => m[1]);
    if (keys.length === 0) throw new Error(`no keys read from ${name}`);
    return new Set(keys);
  }
  const fnAt = src.indexOf(`func ${name}(`);
  if (fnAt < 0) throw new Error(`${name} not found in ${file}`);
  const fn = src.slice(fnAt, src.indexOf("\n}\n", fnAt));
  const sw = fn.indexOf("switch k {");
  const dflt = fn.indexOf("default:");
  if (sw < 0 || dflt < 0) throw new Error(`${name} has no switch to read`);
  const keys = [...fn.slice(sw, dflt).matchAll(/"([a-z_0-9]+)"/g)].map((m) => m[1]);
  if (keys.length === 0) throw new Error(`no keys read from ${name}`);
  return new Set(keys);
}
