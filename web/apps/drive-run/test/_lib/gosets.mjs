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
  // The typed name attestation is its own form of the pod create: the photo set above and this one
  // do not mix (a payload with received_by or affirmed is judged by this set alone).
  "pod attest create": () => goFieldSet("dispatch/syncscope_pod_attest.go", "attestationFields"),
  // One line of a pick record: the direct door's body keys, one for one.
  "picked line": () => goFieldSet("tasks/syncpick.go", "pickedLineLegs"),
};

/** What may travel beside `picked`: read from checkPickedOnly's own test in syncpick.go. */
export function goPickedCompanions() {
  const src = readFileSync(join(coreDir, "tasks/syncpick.go"), "utf8");
  const at = src.indexOf("func checkPickedOnly(");
  if (at < 0) throw new Error("checkPickedOnly not found in tasks/syncpick.go");
  const fn = src.slice(at, src.indexOf("\n}\n", at));
  const keys = [...fn.matchAll(/k != "([a-z_]+)"/g)].map((m) => m[1]);
  if (keys.length === 0) throw new Error("no keys read from checkPickedOnly");
  return new Set(keys);
}

/** The typed name bound the core holds (dispatch.maxTypedNameBytes), read from the Go source. */
export function goTypedNameBytes() {
  const src = readFileSync(join(coreDir, "dispatch/service.go"), "utf8");
  const m = /const maxTypedNameBytes = (\d+)/.exec(src);
  if (m === null) throw new Error("maxTypedNameBytes not found in dispatch/service.go");
  return Number(m[1]);
}

/** The bound on the lines of one pick record (tasks.maxPickedLines). */
export function goMaxPickedLines() {
  const src = readFileSync(join(coreDir, "tasks/syncpick.go"), "utf8");
  const m = /const maxPickedLines = (\d+)/.exec(src);
  if (m === null) throw new Error("maxPickedLines not found in tasks/syncpick.go");
  return Number(m[1]);
}

/** Fields the apps once sent that the core refuses, plus the rest of the
 * vocabulary, so the probe covers declared and refused names alike. */
export const PROBE_EXTRAS = [
  "picked", "skid_count", "reason", "note", "reported_at", "resolved_at", "resolved_by", "resolution",
  "received_by", "affirmed", "created_at", "claimed_by", "failure_reason", "status",
  "task_id", "picked_at", "picked_by", "short_reason", "short_note", "qty_picked", "order_line_no",
];

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Every key of every op's payload in `requests` (the fake server's log) sits
 * inside the core's set for that scope and op. Returns the offenders. A pick
 * record is judged as the core judges it: `picked` travels with `id` and
 * nothing else, every line leg is one of the line legs, the line number is an
 * integer and every other leg a string. A pod create carrying received_by or
 * affirmed is the attestation form, judged by its own set, with its types. */
export function payloadOffenders(requests) {
  const out = [];
  for (const r of requests.filter((x) => x.kind === "push")) {
    for (const op of r.ops) {
      let key = `${r.scope} ${op.op}`;
      const attest = key === "pod create" && ("received_by" in op.payload || "affirmed" in op.payload);
      if (attest) key = "pod attest create";
      const make = GO_SETS[key];
      if (make === undefined) {
        out.push(`${key}: no core set known`);
        continue;
      }
      const set = make();
      for (const k of Object.keys(op.payload)) if (!set.has(k)) out.push(`${key}: ${k}`);
      if (attest) {
        const p = op.payload;
        if (typeof p.stop_id !== "string" || !UUID.test(p.stop_id)) out.push(`${key}: stop_id is not a uuid`);
        if (typeof p.received_by !== "string") out.push(`${key}: received_by is not text`);
        if (p.affirmed !== true) out.push(`${key}: affirmed is not true`);
        if ("id" in p && (typeof p.id !== "string" || !UUID.test(p.id))) out.push(`${key}: id is not a uuid`);
      }
      if (r.scope === "pick_tasks" && "picked" in op.payload) {
        const companions = goPickedCompanions();
        for (const k of Object.keys(op.payload)) if (!companions.has(k)) out.push(`pick_tasks picked: ${k} travels with picked`);
        const lines = op.payload.picked;
        if (!Array.isArray(lines)) {
          out.push("pick_tasks picked: not an array");
          continue;
        }
        if (lines.length === 0 || lines.length > goMaxPickedLines()) out.push(`pick_tasks picked: ${lines.length} lines`);
        const legs = GO_SETS["picked line"]();
        lines.forEach((line, i) => {
          for (const [k, v] of Object.entries(line)) {
            if (!legs.has(k)) out.push(`picked line ${i + 1}: ${k}`);
            else if (k === "order_line_no" ? !Number.isInteger(v) : typeof v !== "string") out.push(`picked line ${i + 1}: ${k} has the wrong type`);
          }
        });
      }
    }
  }
  return out;
}
