// The money fence's machinery. The driver is told what is on the truck, never
// what it is worth: no cost, price, margin, freight or value, and no figure
// that reads as money, may exist in the shipped bytes or the sources.
//
// The word list is the original app's bundle fence (the original HardscapeOS
// app, repository hardscapeos_dibbits, app/scripts/bundle-fence.mjs, commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab: value_cents, freight_cents,
// total_cents, unit_price, margin, cost, price), widened for HH to the cents
// suffix, currency, dollar, amount due, balance and a formatted dollar figure.
//
// CSS spacing is not money: the properties margin and padding (and their
// -side forms) are blanked before the scan, as the original did, so a style
// rule never trips the fence while a field named margin still does.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";

export const MONEY_WORDS = [
  "value_cents",
  "freight_cents",
  "total_cents",
  "unit_price",
  "margin",
  "cost",
  "price",
  "cents",
  "currency",
  "dollar",
  "amount_due",
  "balance",
];

// A figure formatted as money: a dollar sign, digits, and two decimals.
export const MONEY_FIGURE = /\$\s?\d[\d,]*\.\d{2}\b/;

/** Blank the CSS spacing properties so only a real field or word named margin is left to find. */
export function withoutSpacing(text) {
  return text.replace(/\b(margin|padding)(-[a-z]+)*\s*:/gi, "SPACING:");
}

/** Every hit in one text: the word, how often, and a window of context. */
export function scanText(name, text) {
  const body = withoutSpacing(text);
  const lower = body.toLowerCase();
  const hits = [];
  for (const word of MONEY_WORDS) {
    const at = lower.indexOf(word);
    if (at === -1) continue;
    hits.push({
      file: name,
      kind: "word",
      word,
      count: lower.split(word).length - 1,
      window: body.slice(Math.max(0, at - 40), at + word.length + 40).replace(/\s+/g, " "),
    });
  }
  const figure = MONEY_FIGURE.exec(body);
  if (figure !== null) {
    hits.push({ file: name, kind: "figure", word: figure[0], count: 1, window: figure[0] });
  }
  return hits;
}

/** Every file under a directory whose name ends in one of the extensions, as [relative path, text]. */
export function textFiles(root, extensions) {
  const out = [];
  const walk = (dir) => {
    for (const entry of readdirSync(dir)) {
      if (entry === "node_modules" || entry === "dist" || entry.startsWith(".")) continue;
      const path = join(dir, entry);
      if (statSync(path).isDirectory()) walk(path);
      else if (extensions.some((ext) => entry.endsWith(ext))) out.push([relative(root, path), readFileSync(path, "utf8")]);
    }
  };
  walk(root);
  return out;
}

export const describeHits = (hits) => hits.map((h) => `${h.file}: ${h.kind} "${h.word}" x${h.count} ... ${h.window}`);

/**
 * The bundle scan's two named exemptions, each with its reason, so a gate that
 * grew a hole states the hole's shape and the guard test beside it holds the
 * shape narrow:
 *   opfs-worker chunk  the wa-sqlite storage engine's own bytes (its query
 *                      planner names estimatedCost and friends); not the
 *                      yard's payload and not a field the app reads.
 *   the scope registry's price row   the offline store ships the money fence's
 *                      own rows: the scope name price_matrix exists in the
 *                      bytes precisely because the fence refuses writes to it
 *                      by name (money never queues).
 */
export function exemptBundleHit(hit) {
  if (/opfs-worker/.test(hit.file)) return true;
  if (hit.kind === "word" && hit.word === "price" && /price_matrix/.test(hit.window) && /server_wins|pull_only|manual/.test(hit.window)) {
    return true;
  }
  return false;
}

/**
 * The payload scan's one named exemption, with its reason, so a gate that grew
 * a hole states the hole's shape and the guard test beside it holds the shape
 * narrow:
 *   the settings substrate's cost_hidden   the caller's OWN screen layout: the
 *                      user asked for the cost column to be hidden. The flag is
 *                      a boolean, it hides money rather than carrying any, and
 *                      the contract requires it on every surface config answer
 *                      (SettingsWorkspaceView), so the core is right to send it
 *                      and the scan is wrong to read it as money.
 *
 * The guard holds it narrow: the key must be exactly cost_hidden, it must sit
 * directly in the workspace view, and its value must be a boolean. A flag
 * outside that view, a flag carrying a figure, and a field named cost
 * anywhere still trip the fence.
 */
export function exemptPayloadKey(path, key, value) {
  if (key !== "cost_hidden") return false;
  if (!path.endsWith(".workspace")) return false;
  return typeof value === "boolean";
}

/**
 * The same fence over a JSON payload the server sent: the KEYS are scanned
 * for the money words (a word inside a customer's name or a sentence is data,
 * not a field), and the string values for a figure that reads as money. Text
 * that is not JSON falls back to the plain scan.
 */
export function scanPayload(name, text) {
  let parsed;
  try {
    parsed = JSON.parse(text);
  } catch {
    return scanText(name, text);
  }
  const hits = [];
  const walk = (value, path) => {
    if (Array.isArray(value)) {
      value.forEach((item, i) => walk(item, `${path}[${i}]`));
    } else if (value !== null && typeof value === "object") {
      for (const [key, child] of Object.entries(value)) {
        const lower = withoutSpacing(`${key}:`).toLowerCase();
        for (const word of MONEY_WORDS) {
          if (!lower.includes(word)) continue;
          if (exemptPayloadKey(path, key, child)) continue;
          hits.push({ file: name, kind: "word", word, count: 1, window: `${path}.${key}` });
        }
        walk(child, `${path}.${key}`);
      }
    } else if (typeof value === "string" && MONEY_FIGURE.test(value)) {
      hits.push({ file: name, kind: "figure", word: value, count: 1, window: path });
    }
  };
  walk(parsed, "$");
  return hits;
}
