// Ported from hardscapeos_dibbits master 87d7941b app/src/_quote-guard-format.ts;
// adapted for HH.
//
// _guard-format.ts: the Check Quote form's pure words (DOM-free, unit tested on
// the Node runner). Basis points are the right storage unit and the wrong word
// for a counter, so this module speaks them as percent at the edge where a
// sentence is rendered and leaves the wire untouched. The ERP's local margin
// arithmetic (marginBps, marginPercent) is NOT ported: this app never computes
// a margin, the server answers it, and the verdict sentence reads the server's
// figure.

/**
 * bpsToPercent renders a basis point integer the way the counter says it out
 * loud: 1200 -> "12%", 2250 -> "22.5%", 625 -> "6.25%". Trailing zeros are
 * trimmed (a floor is a rule, not a measurement); fractions survive to two
 * places, the full precision a basis point carries.
 */
export function bpsToPercent(bps: number): string {
  if (!Number.isFinite(bps)) return "";
  const fixed = (bps / 100).toFixed(2).replace(/\.?0+$/, "");
  return `${fixed === "" || fixed === "-" ? "0" : fixed}%`;
}

/** A basis point quantity in prose: an integer, optionally signed, then the unit as a whole word. */
const BPS_PATTERN = /(-?\d+)\s*bps\b/gi;

/**
 * humanizeBps rewrites every basis point quantity in a sentence as a
 * percentage and leaves the rest exactly as the server wrote it. A sentence
 * with no bps in it passes through unchanged.
 */
export function humanizeBps(detail: string): string {
  if (typeof detail !== "string" || detail === "") return "";
  return detail.replace(BPS_PATTERN, (_match, digits: string) => bpsToPercent(Number(digits)));
}

/** True when a string still shows a basis point quantity. */
export function mentionsBps(text: string): boolean {
  if (typeof text !== "string") return false;
  BPS_PATTERN.lastIndex = 0;
  const found = BPS_PATTERN.test(text);
  BPS_PATTERN.lastIndex = 0;
  return found;
}

/**
 * dollarsToCents reads a typed money amount to integer cents through its
 * decimal string, never by multiplying a float (1.005 * 100 rounds the wrong
 * way). Anything that is not a plain non negative amount of at most two
 * decimals answers null, and the form says so instead of guessing.
 */
export function dollarsToCents(text: string): number | null {
  const s = typeof text === "string" ? text.trim() : "";
  const m = /^(\d+)(?:\.(\d{1,2}))?$/.exec(s);
  if (m === null) return null;
  const whole = Number(m[1]);
  const fraction = Number((m[2] ?? "").padEnd(2, "0"));
  const cents = whole * 100 + fraction;
  return Number.isSafeInteger(cents) ? cents : null;
}

/** The policy check's answer, as the contract carries it. */
export interface GuardCheck {
  allowed: boolean;
  margin_bps: number;
  override: boolean;
}

/** The verdict sentence: the server's answer and the server's margin, in percent. */
export function guardVerdictWords(check: GuardCheck): string {
  const margin = bpsToPercent(check.margin_bps);
  if (check.allowed) return check.override ? `Approved by an override. The margin is ${margin}.` : `Approved. The margin is ${margin}.`;
  return `Blocked. The margin is ${margin}, below the margin policy.`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/**
 * blockFromText reads the action block a 403 envelope carries under its block
 * key (IdentityActionBlocked): the policy type and the server's sentence, with
 * basis points spoken as percent. The transport flattens an error to its text,
 * so this parses that text and refuses anything that is not a block.
 */
export function blockFromText(text: string): { policyType: string; detail: string } | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return null;
  }
  if (!isRecord(parsed)) return null;
  const block = parsed["block"];
  if (!isRecord(block)) return null;
  const policyType = block["policy_type"];
  const detail = block["detail"];
  if (typeof policyType !== "string" || policyType === "" || typeof detail !== "string") return null;
  return { policyType, detail: humanizeBps(detail) };
}
