// HH Drive's framework free logic: what the driver's run is allowed to do and
// how it reads. No UI, no network, no storage: plain values in, plain values
// out, so the screens only draw it and a test can hold it without a browser.
//
// PROVENANCE (each port records its source: repository, commit, path).
// Repository futurebuildai/hardscapeos_dibbits, commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab, read only:
//   - the stop lifecycle, its labels, runSummary, stopsInOrder, addressLine,
//     qtyLabel, plainSentence, formatTime: app/src/dispatch-logic.ts
//   - the actions a stop offers: stopActions in the same file, narrowed here to
//     the four moves a driver makes (no cancel: a driver cannot record one)
// HH's own, written against the core and not ported:
//   - stopNextStates mirrors core/internal/dispatch/transitions.go
//   - what the driver may do about an unfinished stop mirrors the core's
//     driver gate (core/internal/dispatch/service.go, CompleteRun)
// The words use no dashes: where the original wrote one, a colon, a comma or a
// middle dot stands.

export type StopStatus = "READY" | "LOADED" | "EN_ROUTE" | "DELIVERED" | "FAILED" | "CANCELLED";

/** The forward only matrix, the core's own (transitions.go): terminal states map to nothing. */
const STOP_NEXT: Record<string, readonly StopStatus[]> = {
  READY: ["LOADED", "CANCELLED"],
  LOADED: ["EN_ROUTE", "CANCELLED"],
  EN_ROUTE: ["DELIVERED", "FAILED"],
  DELIVERED: [],
  FAILED: [],
  CANCELLED: [],
};

/** The legal successors of a status, as a copy. A status this build does not know has none. */
export function stopNextStates(status: string): StopStatus[] {
  return [...(STOP_NEXT[status] ?? [])];
}

export const STOP_LABEL: Record<string, string> = {
  READY: "Ready",
  LOADED: "Loaded",
  EN_ROUTE: "En route",
  DELIVERED: "Delivered",
  FAILED: "Not delivered",
  CANCELLED: "Cancelled",
};

/** A status as it reads mid sentence: "en route", "not delivered". */
export function stopWord(status: string): string {
  return (STOP_LABEL[status] ?? status).toLowerCase().replace(/_/g, " ");
}

export interface StopAction {
  /** What the control does: a status move, or the two doors with a form behind them. */
  action: "LOADED" | "EN_ROUTE" | "POD" | "FAILED";
  label: string;
  destructive?: boolean;
}

/**
 * The moves a driver is offered for a stop in this status. Delivered is not a
 * plain move: the core refuses it on the status route and takes it only
 * through proof of delivery, so that is the control a driver gets. Cancel is
 * left out on purpose: a cancelled stop is not an outcome a driver records,
 * and the core refuses to let a driver close a run that carries one.
 */
export function driverActions(status: string): StopAction[] {
  const out: StopAction[] = [];
  for (const next of stopNextStates(status)) {
    if (next === "LOADED") out.push({ action: "LOADED", label: "Mark loaded" });
    else if (next === "EN_ROUTE") out.push({ action: "EN_ROUTE", label: "Mark en route" });
    else if (next === "DELIVERED") out.push({ action: "POD", label: "Proof of delivery" });
    else if (next === "FAILED") out.push({ action: "FAILED", label: "Could not deliver", destructive: true });
  }
  return out;
}

export interface RunProgress {
  delivered: number;
  failed: number;
  open: number;
  sentence: string;
  /** True only when the run has stops and none is still open: an empty run is never done. */
  done: boolean;
}

/** One sentence for where the run stands, and whether every stop has an answer. */
export function runSummary(stops: ReadonlyArray<{ status: string }> | null | undefined): RunProgress {
  const all = stops ?? [];
  let delivered = 0;
  let failed = 0;
  let open = 0;
  for (const s of all) {
    if (s.status === "DELIVERED") delivered += 1;
    else if (s.status === "FAILED") failed += 1;
    else if (s.status !== "CANCELLED") open += 1;
  }
  const done = all.length > 0 && open === 0;
  let sentence: string;
  if (all.length === 0) {
    sentence = "No stops on this run.";
  } else if (open > 0) {
    sentence = `${open} stop${open === 1 ? "" : "s"} left · ${delivered} delivered`;
    if (failed > 0) sentence += ` · ${failed} could not be delivered`;
  } else if (failed > 0) {
    sentence = `Run finished · ${delivered} delivered · ${failed} could not be delivered`;
  } else {
    sentence = `Run finished · all ${delivered} delivered`;
  }
  return { delivered, failed, open, sentence, done };
}

/** Stop order is the server's: by stop number, then by creation. A stop never changes place while it loads. */
export function stopsInOrder<T extends { stop_no?: number; created_at?: string }>(
  stops: readonly T[] | null | undefined,
): T[] {
  return [...(stops ?? [])].sort((a, b) => {
    const an = a.stop_no ?? 0;
    const bn = b.stop_no ?? 0;
    if (an !== bn) return an - bn;
    return (a.created_at ?? "").localeCompare(b.created_at ?? "");
  });
}

/** The address as one line, blanks dropped; empty when nothing is known. */
export function addressLine(parts: { address_line?: string; city?: string; postal_code?: string }): string {
  return [parts.address_line, parts.city, parts.postal_code]
    .map((p) => (p ?? "").trim())
    .filter((p) => p.length > 0)
    .join(", ");
}

/** A quantity in its own unit, trailing zeros trimmed; empty when there is no quantity. */
export function qtyLabel(qty: string | number | null | undefined, uom?: string | null): string {
  let q = String(qty ?? "").trim();
  if (!q) return "";
  if (q.includes(".")) q = q.replace(/0+$/, "").replace(/\.$/, "");
  const unit = (uom ?? "").trim();
  return unit ? `${q} ${unit}` : q;
}

/** A weight in kilograms; an unmeasured stop says so rather than reading zero. */
export function weightLabel(kg: number | undefined | null): string {
  if (typeof kg !== "number" || !Number.isFinite(kg) || kg <= 0) return "weight not recorded";
  return `${kg.toLocaleString("en-CA", { maximumFractionDigits: 1 })} kg`;
}

/** The core's refusals arrive as "module: sentence"; the driver reads the sentence. */
export function plainSentence(msg: string | null | undefined): string {
  const s = (msg ?? "").trim();
  const m = /^([a-z][a-z0-9_]*): (.+)$/s.exec(s);
  return m ? (m[2] ?? "").trim() : s;
}

/** A clock time in the dealer's zone, 24 hour; blank when the instant cannot be read. */
export function formatTime(ts: string | null | undefined, zone: string): string {
  if (!ts) return "";
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat("en-CA", { timeZone: zone, hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(d);
}

/**
 * What the driver reads when a call fails. The core's refusals are sentences
 * written for this person (the module prefix comes off, a capital goes on, a
 * full stop closes it). A failure with no answer from the server, or a server
 * fault, is never shown as it came: the driver is told what to do at the
 * roadside. `fallback` is that sentence for the call that failed.
 */
export function roadsideError(
  error: string,
  status: number | undefined,
  fallback: string,
  words: { notFound?: string } = {},
): string {
  if (status === undefined || status >= 500) return fallback;
  if (status === 401) return "Your sign in has ended. Sign out and sign in again.";
  if (status === 403) return "You are not allowed to do that on this run.";
  if (status === 404) return words.notFound ?? "That is no longer on your run.";
  const plain = plainSentence(error);
  if (plain === "") return fallback;
  const cased = plain.charAt(0).toUpperCase() + plain.slice(1);
  return /[.!?]$/.test(cased) ? cased : `${cased}.`;
}

// ---- proof of delivery ----

export interface PodDraft {
  receivedBy: string;
  affirmed: boolean;
  busy: boolean;
}

/**
 * The words the recipient confirms. They state what the record is: an
 * electronic attestation with a name typed, not a captured signature image
 * (the product's standing rule that an artifact must not imply more than it
 * captured). Port of the original's dispatch-pod.ts wording, same repository
 * and commit as the header, with its dash removed.
 */
export const ATTESTATION =
  "I confirm the goods listed on this order were delivered to the person named above, and that they accepted them. This is recorded as an electronic attestation: it is not a captured signature image.";

export function normalizeName(name: string): string {
  return name.trim();
}

/** Both facts are required and neither is inferred: a typed name, and the recipient's own tick. */
export function canSign(d: PodDraft): boolean {
  return normalizeName(d.receivedBy).length > 0 && d.affirmed && !d.busy;
}

/** Why the sign control is not ready, in the order the driver acts; empty when it is ready. */
export function signBlockedReason(d: PodDraft): string {
  if (d.busy) return "Signing.";
  if (normalizeName(d.receivedBy).length === 0) return "Type the name of the person receiving.";
  if (!d.affirmed) return "Tick the confirmation to sign.";
  return "";
}

export type PhotoType = "image/jpeg" | "image/png" | "image/webp";

/** The core takes exactly these three picture types (dispatch AddPhoto); anything else is refused on the phone. */
export function photoTypeFor(type: string): PhotoType | null {
  const t = type.trim().toLowerCase();
  return t === "image/jpeg" || t === "image/png" || t === "image/webp" ? t : null;
}

/** The core's photo limit (dispatch.MaxPhotoBytes). */
export const PHOTO_LIMIT_BYTES = 4 * 1024 * 1024;

/** A camera photo above this is shrunk before it is sent, so a phone on a bad connection sends less and never meets the limit. */
export const SHRINK_ABOVE_BYTES = 1.5 * 1024 * 1024;

export function needsShrink(bytes: number): boolean {
  return bytes > SHRINK_ABOVE_BYTES;
}
