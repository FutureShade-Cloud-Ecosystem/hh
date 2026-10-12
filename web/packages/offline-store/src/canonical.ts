// One operation, one canonical byte string, one SHA-256. The hash is how the
// client notices a key reused with a different body, and a stored body that
// changed on disk between enqueue and replay.

/**
 * JSON with object keys sorted at every depth. Refuses what is not plain JSON:
 * a prototype other than Object's or null, array holes, toJSON values, symbol
 * keys, non finite numbers, and nesting past 64 levels (which is how a cycle ends).
 */
export function canonicalJson(value: unknown): string {
  return canonical(value, 0);
}

const MAX_DEPTH = 64;

function canonical(value: unknown, depth: number): string {
  if (depth > MAX_DEPTH) throw new Error("canonical json: nested too deep (a cycle, or not a payload)");
  if (value === null) return "null";
  switch (typeof value) {
    case "string":
      return JSON.stringify(value);
    case "boolean":
      return value ? "true" : "false";
    case "number":
      if (!Number.isFinite(value)) throw new Error("canonical json: a number must be finite");
      return JSON.stringify(value);
    case "object": {
      if (Array.isArray(value)) {
        if (Object.getPrototypeOf(value) !== Array.prototype) throw new Error("canonical json: an array subclass is not plain JSON");
        const parts: string[] = [];
        for (let i = 0; i < value.length; i++) {
          if (!(i in value)) throw new Error("canonical json: a sparse array has a hole");
          parts.push(canonical(value[i], depth + 1));
        }
        return `[${parts.join(",")}]`;
      }
      const proto = Object.getPrototypeOf(value);
      if (proto !== Object.prototype && proto !== null) throw new Error("canonical json: only plain objects and arrays are carried");
      if (Object.getOwnPropertySymbols(value).length > 0) throw new Error("canonical json: a symbol key is not plain JSON");
      if (typeof (value as { toJSON?: unknown }).toJSON === "function") throw new Error("canonical json: a toJSON value is not plain JSON");
      const entries = Object.entries(value as Record<string, unknown>).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
      return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${canonical(v, depth + 1)}`).join(",")}}`;
    }
    default:
      throw new Error(`canonical json: cannot carry a ${typeof value}`);
  }
}

/** Lower case hex SHA-256 of the canonical JSON of a value. */
export async function bodyHash(value: unknown): Promise<string> {
  const bytes = new TextEncoder().encode(canonicalJson(value));
  const digest = await globalThis.crypto.subtle.digest("SHA-256", bytes);
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}
