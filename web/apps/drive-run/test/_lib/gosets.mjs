// The core's sync writable sets, read straight from the Go source, so a stub
// or an app payload that drifts from the server fails a test instead of
// passing every leg against a flattering double.
//
// Two shapes carry a set in the core: a package level map[string]bool
// (`var writableFields = map[string]bool{ "id": true, ... }`) and a function
// that switches on the declared key (`func shortageWritableCreate(...)` with
// `case "id", "task_id", ...:` before `default:`).
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const coreDir = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "..", "..", "core", "internal");

export function goFieldSet(file, name) {
  const src = readFileSync(join(coreDir, file), "utf8");
  const mapAt = src.indexOf(`var ${name} = map[string]bool{`);
  if (mapAt >= 0) {
    const body = src.slice(mapAt, src.indexOf("\n}", mapAt));
    const keys = [...body.matchAll(/"([a-z_]+)"\s*:/g)].map((m) => m[1]);
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

/** The writable set of each synced write the two Field apps make, by scope and op. */
export const GO_SETS = {
  "pick_tasks update": () => goFieldSet("tasks/syncscope.go", "writableFields"),
  "weights update": () => goFieldSet("tasks/syncweights.go", "weightWritable"),
  "shortages create": () => goFieldSet("tasks/syncshortage.go", "shortageWritableCreate"),
  "shortages update": () => goFieldSet("tasks/syncshortage.go", "shortageWritable"),
  "stops update": () => goFieldSet("dispatch/syncscope_stops.go", "stopWriteFields"),
  "pod create": () => goFieldSet("dispatch/syncscope_pod.go", "podCreateFields"),
};

/** Fields the apps once sent that the core refuses, plus the rest of the
 * vocabulary, so the probe covers declared and refused names alike. */
export const PROBE_EXTRAS = [
  "picked", "skid_count", "reason", "note", "reported_at", "resolved_at", "resolved_by", "resolution",
  "received_by", "affirmed", "created_at", "claimed_by", "failure_reason", "status",
];

/** Every key of every op's payload in `requests` (the fake server's log) sits
 * inside the core's set for that scope and op. Returns the offenders. */
export function payloadOffenders(requests) {
  const out = [];
  for (const r of requests.filter((x) => x.kind === "push")) {
    for (const op of r.ops) {
      const key = `${r.scope} ${op.op}`;
      const make = GO_SETS[key];
      if (make === undefined) {
        out.push(`${key}: no core set known`);
        continue;
      }
      const set = make();
      for (const k of Object.keys(op.payload)) if (!set.has(k)) out.push(`${key}: ${k}`);
    }
  }
  return out;
}
