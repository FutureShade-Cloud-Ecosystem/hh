// HH Drive's framework free logic: what the driver's run is allowed to do and
// how it reads. No UI, no network, no storage: plain values in, plain values
// out, so the screens only draw it and a test can hold it without a browser.
//
// PROVENANCE (each port records its source: repository, commit, path).
// The original HardscapeOS app (repository hardscapeos_dibbits), commit
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

import { isLostSignal } from "@hardscapehouse/api-client";

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
    else if (next === "DELIVERED") out.push({ action: "POD", label: "Delivered" });
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

/** The chip a stop card carries on my run, in the board's words (the style sets the capitals). */
export type StopChip = "Delivered" | "Next stop" | "Waiting" | "Not delivered" | "Cancelled";

export interface RouteStop<T> {
  readonly stop: T;
  readonly chip: StopChip;
  /** True for the one stop the run goes to next: its card is highlighted. */
  readonly next: boolean;
}

const DONE_STATUSES: ReadonlySet<string> = new Set(["DELIVERED", "FAILED", "CANCELLED"]);

/**
 * The stop the run goes to next: the one already en route, else the first stop
 * in route order that is not yet delivered, not delivered or cancelled.
 */
export function nextStopOf<T extends { status: string; stop_no?: number; created_at?: string }>(
  stops: readonly T[] | null | undefined,
): T | null {
  const ordered = stopsInOrder(stops);
  return (
    ordered.find((s) => s.status === "EN_ROUTE") ?? ordered.find((s) => !DONE_STATUSES.has(s.status)) ?? null
  );
}

/** A run's stops in route order, each with its state chip from the core's state; no grouping. */
export function routeStops<T extends { status: string; stop_no?: number; created_at?: string }>(
  stops: readonly T[] | null | undefined,
): RouteStop<T>[] {
  const ordered = stopsInOrder(stops);
  const next = nextStopOf(ordered);
  return ordered.map((stop) => {
    let chip: StopChip = "Waiting";
    if (stop === next) chip = "Next stop";
    else if (stop.status === "DELIVERED") chip = "Delivered";
    else if (stop.status === "FAILED") chip = "Not delivered";
    else if (stop.status === "CANCELLED") chip = "Cancelled";
    return { stop, chip, next: stop === next };
  });
}

/**
 * The yard's load order sentence for a run: the last stop in delivery order
 * goes on the back of the truck first, and the first stop goes on last so it
 * comes off first. Empty when the run has no stops.
 */
export function loadOrderLine(
  stops: ReadonlyArray<{ stop_no?: number; created_at?: string }> | null | undefined,
): string {
  const ordered = stopsInOrder(stops);
  if (ordered.length === 0) return "";
  const first = ordered[ordered.length - 1]?.stop_no ?? ordered.length;
  const last = ordered[0]?.stop_no ?? 1;
  return `Checked once for the whole run. Load order: stop ${first} first, stop ${last} last.`;
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

/** A clock time in the dealer's zone, in the glossary 12-hour AM/PM form; blank when the instant cannot be read. */
export function formatTime(ts: string | null | undefined, zone: string): string {
  return formatGlossaryTime(ts, zone);
}

/** A glossary clock time in the dealer's zone ("8:30 PM"), with a plain space before AM or PM. */
export function formatGlossaryTime(ts: string | null | undefined, zone: string): string {
  if (!ts) return "";
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat("en-US", {
    timeZone: zone,
    hour: "numeric",
    minute: "2-digit",
  })
    .format(d)
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * What the driver reads when a call fails. The core's refusals are sentences
 * written for this person (the module prefix comes off, a capital goes on, a
 * full stop closes it). A failure is never shown as it came: the driver is
 * told what to do at the roadside. `lead` names what failed ("Your run did
 * not load."). Signal advice follows it only when nothing answered
 * (`isLostSignal`); a server that answered with a fault, or answered success
 * with a body that was not JSON, gets a server line instead, because a
 * driver's signal was not the problem. A refusal with no sentence of its own (a 4xx) says it was not accepted. `neutral` is a sentence that claims
 * neither, used as it stands. `failure` is the result's status and
 * code, passed as the result carries them.
 */
export function roadsideError(
  error: string,
  failure: { status?: number; code?: string },
  lead: string,
  words: { notFound?: string; retry?: "try again" | "add it again"; neutral?: string } = {},
): string {
  const { status } = failure;
  const retry = words.retry ?? "try again";
  const fallback = (): string =>
    words.neutral ??
    (isLostSignal({ ok: false, ...failure })
      ? `${lead} Check your signal and ${retry}.`
      : `${lead} The server had a problem. ${retry.charAt(0).toUpperCase()}${retry.slice(1)} in a moment.`);
  if (status === undefined || status >= 500) return fallback();
  if (status === 401) return "Your sign in has ended. Sign out and sign in again.";
  if (status === 403) return "You are not allowed to do that on this run.";
  if (status === 404) return words.notFound ?? "That is no longer on your run.";
  const plain = plainSentence(error);
  if (plain === "") return words.neutral ?? `${lead} That was not accepted. ${retry.charAt(0).toUpperCase()}${retry.slice(1)} in a moment.`;
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

/** The core's bound on a typed name, in bytes (dispatch.maxTypedNameBytes); a test reads the Go constant. */
export const MAX_NAME_BYTES = 200;

/**
 * What the core's attestation door would refuse in the typed name, said before
 * the signature is queued (the poison op lesson): a name over the bound in
 * bytes, or one carrying a control character (C0, DEL) or a bidi control
 * (U+202A to U+202E, U+2066 to U+2069). A queued signature the door refuses
 * only comes back as a card after the driver has left the stop. Empty when the
 * name is fine; a blank name is the form's own prompt, not this check.
 */
export function nameProblem(name: string): string {
  const typed = normalizeName(name);
  if (new TextEncoder().encode(typed).length > MAX_NAME_BYTES) return `That name is too long, type the full name in at most ${MAX_NAME_BYTES} bytes.`;
  for (const ch of typed) {
    const c = ch.codePointAt(0) ?? 0;
    if (c < 0x20 || c === 0x7f || (c >= 0x202a && c <= 0x202e) || (c >= 0x2066 && c <= 0x2069)) {
      return "The name cannot hold control characters, type the full name as plain text.";
    }
  }
  return "";
}

/** Both facts are required and neither is inferred: a typed name, and the recipient's own tick. */
export function canSign(d: PodDraft): boolean {
  return normalizeName(d.receivedBy).length > 0 && nameProblem(d.receivedBy) === "" && d.affirmed && !d.busy;
}

/** Why the sign control is not ready, in the order the driver acts; empty when it is ready. */
export function signBlockedReason(d: PodDraft): string {
  if (d.busy) return "Signing.";
  if (normalizeName(d.receivedBy).length === 0) return "Type the name of the person receiving.";
  const problem = nameProblem(d.receivedBy);
  if (problem !== "") return problem;
  if (!d.affirmed) return "Tick the confirmation to sign.";
  return "";
}

/** What the stop says when its signature is saved on the device and not yet sent. */
export const PROOF_WAITING_WORDS = "Waiting to send: the signature goes as soon as you have signal.";

/** The notice after a signature that could not go yet (no signal): nobody has been told the stop is delivered. */
export const PROOF_QUEUED_NOTICE = "Signature saved on this device. It sends when you have signal.";

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

// ---- could not deliver, and the end of the run ----

/** Said to the driver once a stop is recorded as not delivered. Port of the original's handback words (dispatch-logic.ts), dash free. */
export const HANDBACK_TITLE = "Recorded. This stop is closed.";
export const HANDBACK_BODY =
  "Bring the load back to the yard: the order is free to be booked again, and a dispatcher will set a new date.";
export const HANDBACK_PROMPT = "What stopped this delivery?";
export const FAILURE_REASON_SHEET_PROMPT = "Why could it not be delivered?";
export const FAILURE_REASON_OTHER = "Other, say what";

/** The board's five preset failure reasons on the could not deliver sheet, in board order. */
export const FAILURE_REASON_PRESETS: readonly string[] = [
  "Nobody on site",
  "Gate locked",
  "Site not ready",
  "Customer refused",
  FAILURE_REASON_OTHER,
];

/** Resolves the free-text failure_reason string to record for a chosen preset and optional Other note. */
export function resolveFailureReason(preset: string, otherText: string = ""): string {
  if (preset === FAILURE_REASON_OTHER) return otherText;
  if (FAILURE_REASON_PRESETS.includes(preset)) return preset;
  return "";
}

/** The core refuses a failed stop with no reason (400), so the control stays grey until there is one. */
export function canRecordFailure(reason: string): boolean {
  return reason.trim().length > 0;
}

export interface CloseStop {
  status: string;
  stop_no?: number;
  customer_name?: string;
}

const OUTSTANDING_WORD: Record<string, string> = {
  READY: "not loaded yet",
  LOADED: "on the truck",
  EN_ROUTE: "still en route",
};

/** "Stop 2 · Customer": a stop as a person finds it on the sheet. */
export function stopHandle(stop: CloseStop): string {
  const seq = stop.stop_no ? `Stop ${stop.stop_no}` : "A stop";
  const who = (stop.customer_name ?? "").trim();
  return who ? `${seq} · ${who}` : seq;
}

export const CLOSE_RULE = "Every stop has to be delivered or marked not delivered before the run can be closed.";

/** One line of the end of run recap: the stop and where it ended. */
export function closeLine(stop: CloseStop): string {
  const label = STOP_LABEL[stop.status] ?? "";
  return label ? `${stopHandle(stop)} · ${label}` : stopHandle(stop);
}

export interface RunClose {
  can: boolean;
  /** The sentence shown in place of the control when the run cannot be closed. */
  blocked: string;
  progress: RunProgress;
}

/**
 * May this driver close this run, and if not, why not. It mirrors the core's
 * order (core/internal/dispatch/service.go, CompleteRun): the run must be out,
 * every stop must be settled, and for a driver every stop must be delivered or
 * not delivered. The third gate differs from the original app's rule, which
 * counted a cancelled stop as settled: the core refuses a driver's close on a
 * cancelled stop, so the screen says so up front and names who closes it.
 * Port of the original's drive-logic.ts runClose, same repository and commit
 * as the header, with HH's driver gate added.
 */
export function runClose(
  run: { status?: string } | null | undefined,
  stops: ReadonlyArray<CloseStop> | null | undefined,
): RunClose {
  const list: CloseStop[] = [...(stops ?? [])];
  const progress = runSummary(list);
  const status = run?.status ?? "";
  if (status === "COMPLETED") {
    return { can: false, blocked: "This run is closed. Nothing else is needed from you today.", progress };
  }
  if (status !== "DISPATCHED") {
    const blocked =
      status === "PLANNED"
        ? "This run has not been sent out yet. A dispatcher marks it out for delivery, and it can be closed once they have."
        : status === "CANCELLED"
          ? "This run is cancelled, so there is nothing here to close."
          : "This run is not open, so there is nothing here to close.";
    return { can: false, blocked, progress };
  }
  if (list.length === 0) {
    return { can: false, blocked: "There are no stops on this run yet. A dispatcher puts them here.", progress };
  }
  const open = list.find((s) => s.status !== "DELIVERED" && s.status !== "FAILED" && s.status !== "CANCELLED");
  if (open !== undefined) {
    return { can: false, blocked: `${stopHandle(open)} is ${OUTSTANDING_WORD[open.status] ?? "not finished yet"}. ${CLOSE_RULE}`, progress };
  }
  const cancelled = list.find((s) => s.status === "CANCELLED");
  if (cancelled !== undefined) {
    return {
      can: false,
      blocked: `${stopHandle(cancelled)} is cancelled, and that is not an outcome a driver records. Ask dispatch to close this run.`,
      progress,
    };
  }
  return { can: true, blocked: "", progress };
}
