// Yard Pick's framework free logic: what the yard team's pick is allowed to
// do and how it reads. No UI, no network, no storage: plain values in, plain
// values out, so the screens only draw it and a test can hold it without a
// browser.
//
// PROVENANCE (each port records its source: repository, commit, path).
// The original HardscapeOS app (repository hardscapeos_dibbits), commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab, read only:
//   - the task lifecycle and its labels, nextAction, actionLabel, actionDone,
//     statusTone, isOpen, queueOrder, ageLabel, qtyText, shortageSummary,
//     ticketIsFixture, lineIsShort, validatePickQty, pickSummary,
//     priorityWord, mtoLabel, ticketHandles, orderHandle:
//     app/src/yard-logic.ts
// HH's own, written against the core and not ported:
//   - the task transition matrix mirrors core/internal/tasks/model.go
//   - SHORT_REASONS and reasonPhrase mirror core/internal/tasks/yard_short.go
//     and yard_notice.go (reasonPhrase is the core's own sentence form)
//   - the skid rules mirror core/internal/tasks/yard_short.go (RecordSkids)
//   - FORBIDDEN_BODY mirrors the role gate's shared body
//     (core/internal/platform/authz/middleware.go), and RESOLVE_WALL_NAMES the
//     shortage_resolvers list (core/internal/roleconfig/default.json), the
//     labels from the role catalog
// The words use no dashes: where the original wrote one, a comma, a colon or
// a middle dot stands.

export type TaskStatus = "QUEUED" | "PUSHED" | "ACCEPTED" | "IN_PROGRESS" | "STAGED" | "DONE" | "CANCELLED";

/** The forward only matrix, the core's own (model.go): terminal states map to nothing. */
const TASK_NEXT: Record<string, readonly TaskStatus[]> = {
  QUEUED: ["PUSHED", "ACCEPTED", "CANCELLED"],
  PUSHED: ["ACCEPTED", "CANCELLED"],
  ACCEPTED: ["IN_PROGRESS", "CANCELLED"],
  IN_PROGRESS: ["STAGED", "DONE", "CANCELLED"],
  STAGED: ["DONE", "CANCELLED"],
  DONE: [],
  CANCELLED: [],
};

/** The action a picker can take next on a pick, or null when there is nothing to do. */
export type PickAction = "claim" | "start" | "stage";

/**
 * The single source of truth for which control the pick offers, mirroring the
 * server's state machine (core/internal/tasks/model.go): QUEUED/PUSHED offers
 * the claim, ACCEPTED the start, IN_PROGRESS the stage, and a terminal pick
 * offers nothing. A control the server would refuse teaches people to
 * distrust the buttons.
 */
export function nextAction(status: string): PickAction | null {
  switch (status) {
    case "QUEUED":
    case "PUSHED":
      return "claim";
    case "ACCEPTED":
      return "start";
    case "IN_PROGRESS":
      return "stage";
    default:
      return null;
  }
}

/** The legal successors of a status, as a copy. A status this build does not know has none. */
export function taskNextStates(status: string): TaskStatus[] {
  return [...(TASK_NEXT[status] ?? [])];
}

/** Human label for the next action's control. */
export function actionLabel(action: PickAction): string {
  return { claim: "Claim", start: "Start picking", stage: "Stage" }[action];
}

/**
 * What the screen says after the move landed. The stage sentence carries the
 * parking spot and the short handle the counter reads out, the two facts the
 * core's own staged event tells the counter.
 */
export function actionDone(action: PickAction, where?: { location?: string; ref?: string }): string {
  if (action === "claim") return "Claimed. It is yours to pull.";
  if (action === "start") return "Picking started.";
  const at = (where?.location ?? "").trim();
  const ref = (where?.ref ?? "").trim();
  const spot = at !== "" ? ` at ${at}` : "";
  const handle = ref !== "" ? `, ref ${ref}` : "";
  return `Staged${spot}${handle}. The counter has been told it is ready.`;
}

/** Human label for a status chip. */
export const STATUS_LABEL: Record<string, string> = {
  QUEUED: "Queued",
  PUSHED: "Pushed",
  ACCEPTED: "Claimed",
  IN_PROGRESS: "Picking",
  STAGED: "Staged",
  DONE: "Done",
  CANCELLED: "Cancelled",
};

/** The one visual tone a status takes: colour never carries the meaning alone. */
export function statusTone(status: string): "neutral" | "active" | "ready" | "done" | "stopped" {
  switch (status) {
    case "QUEUED":
    case "PUSHED":
      return "neutral";
    case "ACCEPTED":
    case "IN_PROGRESS":
      return "active";
    case "STAGED":
      return "ready";
    case "DONE":
      return "done";
    default:
      return "stopped";
  }
}

/** A pick still needing yard work (not terminal). */
export function isOpen(status: string): boolean {
  return status !== "DONE" && status !== "CANCELLED";
}

/** A pick nobody has taken yet: the queue's unclaimed half. */
export function isUnclaimed(status: string): boolean {
  return status === "QUEUED" || status === "PUSHED";
}

/**
 * queueOrder sorts the board the way a yard lead reads it: open shortage
 * first (somebody at the counter is waiting on an answer), then open work
 * before finished, then priority, then oldest first. A pure comparator over
 * a copied array, it never mutates its input.
 */
export function queueOrder<T extends { task: { open_shortages: number; status: string; priority: string; created_at: string } }>(
  entries: readonly T[] | null | undefined,
): T[] {
  const rank: Record<string, number> = { URGENT: 0, HIGH: 1, NORMAL: 2, LOW: 3 };
  return [...(entries ?? [])].sort((a, b) => {
    const shortA = a.task.open_shortages > 0 ? 0 : 1;
    const shortB = b.task.open_shortages > 0 ? 0 : 1;
    if (shortA !== shortB) return shortA - shortB;
    const openA = isOpen(a.task.status) ? 0 : 1;
    const openB = isOpen(b.task.status) ? 0 : 1;
    if (openA !== openB) return openA - openB;
    const pa = rank[a.task.priority] ?? 2;
    const pb = rank[b.task.priority] ?? 2;
    if (pa !== pb) return pa - pb;
    return a.task.created_at.localeCompare(b.task.created_at);
  });
}

/** How long a pick has waited, in units a person uses out loud. `now` is a parameter so a test pins the output. */
export function ageLabel(createdAt: string, now: Date): string {
  const then = Date.parse(createdAt);
  if (Number.isNaN(then)) return "";
  const mins = Math.floor((now.getTime() - then) / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

/**
 * A quantity with the storage format stripped before a person reads it.
 * Trailing zeros are trimmed, never rounded: 2.5000 is 2.5 and anything that
 * is not a plain fixed point decimal comes back exactly as it arrived.
 */
export function qtyText(raw: string | number | null | undefined): string {
  const s = String(raw ?? "").trim();
  if (!/^-?\d+\.\d+$/.test(s)) return s;
  return s.replace(/0+$/, "").replace(/\.$/, "");
}

/** A quantity in its own unit, or the bare number when the unit is unknown. */
export function qtyLabel(qty: string | number | null | undefined, uom?: string | null): string {
  const q = qtyText(qty);
  if (q === "") return "";
  const unit = (uom ?? "").trim();
  return unit ? `${q} ${unit}` : q;
}

/** The one-line a queue row shows for its open shortages: the count first, then the first item. */
export function shortageSummary(shortages: ReadonlyArray<{ resolved_at?: string | null; qty_short: string; uom: string; description: string }> | undefined): string {
  const open = (shortages ?? []).filter((s) => !s.resolved_at);
  if (open.length === 0) return "";
  const first = open[0];
  return `${open.length} open shortage${open.length === 1 ? "" : "s"} · short ${qtyText(first.qty_short)} ${first.uom} of ${first.description}`;
}

/** True when a ticket came from the fixture feed: a queue fed by fixture material says so on screen. */
export function ticketIsFixture(ticket: { source?: string } | undefined): boolean {
  return (ticket?.source ?? "") === "STUB";
}

/** The feed's grade in words. An unmapped grade degrades to itself lowercased, never an invented phrase. */
export function priorityWord(priority: string | undefined): string {
  switch ((priority ?? "").toUpperCase()) {
    case "URGENT":
      return "Wanted today";
    case "HIGH":
      return "Wanted soon";
    case "NORMAL":
    case "":
      return "";
    case "LOW":
      return "No rush";
    default:
      return (priority ?? "").toLowerCase().replace(/_/g, " ");
  }
}

/** Made to order material, said for the person who must not substitute a pallet. A pickup renders nothing. */
export function mtoLabel(mtoClass: string | undefined, fulfillment?: string): string {
  const f = (fulfillment ?? "").toUpperCase();
  if (f === "PICKUP" || f === "WILL_CALL") return "";
  switch ((mtoClass ?? "").toUpperCase()) {
    case "MTO":
      return "Made to order: do not substitute";
    case "MIXED":
      return "Mixed load: check each pallet";
    default:
      return "";
  }
}

/** A crude uuid shape test: enough to keep one out of a picker's hands. */
function looksLikeUUID(s: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(s);
}

/** What the board calls a row when the feed gave it no order number: never a uuid, never nothing. */
export function orderHandle(ticket: { order_no?: string; reference?: string; customer_name?: string } | null | undefined): string {
  const orderNo = (ticket?.order_no ?? "").trim();
  if (orderNo !== "") return orderNo;
  const reference = (ticket?.reference ?? "").trim();
  if (reference !== "" && !looksLikeUUID(reference)) return reference;
  const customer = (ticket?.customer_name ?? "").trim();
  if (customer !== "") return customer;
  return "Order number not on file";
}

// ---- the short pick reason codes, the core's closed list (0369) ----

export const SHORT_REASONS = ["CONDENSED_SKIDS", "DAMAGED", "NOT_IN_YARD", "COUNT_CORRECTION", "OTHER"] as const;
export type ShortReason = (typeof SHORT_REASONS)[number];

export function isShortReason(code: string): code is ShortReason {
  return (SHORT_REASONS as readonly string[]).includes(code);
}

/** The reason's sentence form, the core's own (yard_notice.go). */
export function reasonPhrase(code: string): string {
  switch (code) {
    case "CONDENSED_SKIDS":
      return "Skids condensed";
    case "DAMAGED":
      return "Damaged";
    case "NOT_IN_YARD":
      return "Not in the yard";
    case "COUNT_CORRECTION":
      return "Count corrected";
    case "OTHER":
      return "Other";
  }
  return code;
}

/**
 * The reason in the picker's own words, the board's pattern: words a person
 * says out loud, never the core's codes. The code rides the wire (the closed
 * CHECK list); it never reaches a screen.
 */
export function reasonWord(code: string): string {
  switch (code) {
    case "CONDENSED_SKIDS":
      return "Skids condensed";
    case "DAMAGED":
      return "Damaged stock";
    case "NOT_IN_YARD":
      return "Not enough on the yard";
    case "COUNT_CORRECTION":
      return "Count corrected";
    case "OTHER":
      return "Something else";
  }
  return code;
}

// ---- recording what was pulled ----

/** The ONE place the client decides what "short" means, mirroring PickedLine.Short() on the server. */
export function lineIsShort(ordered: string, picked: string): boolean {
  const o = Number(ordered);
  const p = Number(picked);
  if (!Number.isFinite(o) || !Number.isFinite(p)) return false;
  return p < o;
}

/**
 * The client's check on a typed picked quantity. It mirrors the server's
 * parseNonNegativeQty exactly, including the part that looks like a bug and
 * is not: zero is valid. "We went to pick it and there was none" is a real
 * outcome; blank is not.
 */
export function validatePickQty(raw: string): string {
  const v = (raw ?? "").trim();
  if (!v) return "Say how many came off the shelf. Zero is an answer, blank is not.";
  const n = Number(v);
  if (!Number.isFinite(n)) return "That is not a number.";
  if (n < 0) return "A picked quantity cannot be less than nothing.";
  if (n >= 1e7) return "That quantity is implausible. Check the number.";
  return "";
}

/** A quantity string in the core's decimal form, or null when it is not one. */
export function toQtyString(raw: string): string | null {
  const v = (raw ?? "").trim();
  if (v === "") return null;
  const n = Number(v);
  if (!Number.isFinite(n) || n < 0) return null;
  return String(n);
}

/** What is short on a line: the ordered less the picked, as a decimal string; empty when nothing is short. */
export function shortOf(ordered: string, picked: string): string {
  const o = Number(ordered);
  const p = Number(picked);
  if (!Number.isFinite(o) || !Number.isFinite(p) || p >= o) return "";
  const d = Number((o - p).toFixed(4));
  return String(d);
}

export interface DraftLine {
  order_line_no: number;
  product_id: string;
  sku: string;
  description: string;
  qty_ordered: string;
  qty_picked: string;
  uom: string;
  short_reason: string;
  short_note: string;
}

/**
 * May the pick be recorded as drafted? Every line needs a number, a short
 * line needs a reason from the closed list, and OTHER needs a note: the
 * server's own rules (RecordPick, cleanShortReason), checked here so the
 * picker hears it before the round trip.
 */
export function recordBlockedReason(lines: ReadonlyArray<{ qty_picked: string; qty_ordered: string; short_reason: string; short_note: string }>): string {
  for (const l of lines) {
    const qtyBad = validatePickQty(l.qty_picked);
    if (qtyBad !== "") return qtyBad;
    if (!lineIsShort(l.qty_ordered, l.qty_picked)) continue;
    if (l.short_reason === "") return "A short line needs the reason. The counter has to tell the customer why.";
    if (!isShortReason(l.short_reason)) return "That is not one of the short pick reasons.";
    if (l.short_reason === "OTHER" && l.short_note.trim() === "") return "Say what happened in the note when the reason is Other.";
  }
  return "";
}

/** The id a recorded line carries while it exists only on this device. A pull
 * replaces the row, and the server's lines carry the server's ids. */
export const LOCAL_LINE = "local:";

/** True for a recorded line this device wrote and the server has not yet answered. */
export function isLocalLine(line: { id: string }): boolean {
  return line.id.startsWith(LOCAL_LINE);
}

/** What the pick says when its record is saved on the device and not yet sent. */
export const RECORD_WAITING_WORDS = "Waiting to send: this pick's record goes as soon as you have signal.";

/** The notice after a record that could not go yet (no signal): honest that the rep is told on send. */
export function pickQueuedWords(picked: ReadonlyArray<{ qty_ordered: string; qty_picked: string }>): string {
  const short = picked.filter((l) => lineIsShort(l.qty_ordered, l.qty_picked)).length;
  const tail = short > 0 ? ` The ${short === 1 ? "short line goes" : "short lines go"} on backorder and the order's rep is told when it sends.` : "";
  return `Pick saved on this device. It sends when you have signal.${tail}`;
}

/** The core's bound on the lines of one pick record (tasks.maxPickedLines); a test reads the Go constant. */
export const MAX_PICKED_LINES = 500;

const PICK_TEXT_LEGS: ReadonlyArray<readonly [string, string, boolean]> = [
  ["product_id", "product id", false],
  ["sku", "sku", false],
  ["description", "description", true],
  ["qty_ordered", "ordered quantity", false],
  ["qty_picked", "picked quantity", false],
  ["uom", "unit", false],
  ["short_reason", "short reason", false],
  ["short_note", "note", true],
];

/** A control (C0, DEL) or a bidi control (U+202A to U+202E, U+2066 to U+2069); `multiline` keeps tab, LF and CR. */
function hasControl(value: string, multiline: boolean): boolean {
  for (const ch of value) {
    const c = ch.codePointAt(0) ?? 0;
    if ((c < 0x20 && !(multiline && (c === 9 || c === 10 || c === 13))) || c === 0x7f || (c >= 0x202a && c <= 0x202e) || (c >= 0x2066 && c <= 0x2069)) return true;
  }
  return false;
}

/**
 * What the core's pick record door would refuse for its shape or its text,
 * said before anything is queued (the core's rule, tasks.checkPickText and the
 * bounds on a record): empty, more than 500 lines, or a control or bidi
 * character in a text leg. A queued op the door refuses only comes back as a
 * card; the picker is told here while the note is still in front of them.
 */
export function pickRecordProblem(lines: ReadonlyArray<object>): string {
  if (lines.length === 0) return "Say what was picked, an empty record is not the same as a pick of nothing.";
  if (lines.length > MAX_PICKED_LINES) return `That is more lines than any one ticket carries (max ${MAX_PICKED_LINES}).`;
  for (const entry of lines) {
    const line = entry as Readonly<Record<string, unknown>>;
    for (const [key, name, multiline] of PICK_TEXT_LEGS) {
      const value = line[key];
      if (typeof value === "string" && hasControl(value, multiline)) {
        return `Line ${String(line["order_line_no"])}: the ${name} cannot hold control characters, enter it as plain text.`;
      }
    }
  }
  return "";
}

/** The one-line the ticket says about itself once a pick has been recorded: what was pulled, not what is missing. */
export function pickSummary(picked: ReadonlyArray<{ qty_ordered: string; qty_picked: string; uom: string; description?: string }> | undefined): string {
  const lines = picked ?? [];
  if (lines.length === 0) return "";
  const short = lines.filter((l) => lineIsShort(l.qty_ordered, l.qty_picked));
  if (short.length === 0) return `All ${lines.length} line${lines.length === 1 ? "" : "s"} pulled in full.`;
  const first = short[0];
  const head = `${qtyText(first.qty_picked)} of ${qtyText(first.qty_ordered)} ${first.uom} pulled on ${first.description || "one line"}`;
  const rest = short.length - 1;
  return rest > 0 ? `${head}, and ${rest} other line${rest === 1 ? "" : "s"} came up short.` : `${head}.`;
}

/**
 * The banner the pick shows after the server took a short pick: the numbers
 * are the server's own answer and the re-read ticket's backorder figure, and
 * the last words name the core's contracted act (Phase 0 item 6): the line is
 * backordered and the order's rep is told.
 */
export function shortPickWords(answer: { qty_picked: string; qty_ordered: string; uom: string }, backordered: string): string {
  const picked = qtyText(answer.qty_picked);
  const ordered = qtyText(answer.qty_ordered);
  const back = backordered === "" ? "" : ` (${qtyText(backordered)} ${answer.uom})`;
  return `Picked ${picked} of ${ordered} ${answer.uom}. The rest${back} is on backorder and the order's rep has been told.`;
}

// ---- the shortage sheet ----

/** The client's check for the shortage sheet: a positive quantity, the server's own rule (FlagShortage). */
export function validateShortage(qty: string): string {
  const v = (qty ?? "").trim();
  if (!v) return "How many are short? The counter has to tell the customer.";
  const n = Number(v);
  if (!Number.isFinite(n)) return "That is not a number.";
  if (n <= 0) return "A shortage has to be greater than zero.";
  return "";
}

/** The words the yard reads once a shortage report lands: the server echoed it, the counter is its audience. */
export function shortageSentWords(s: { qty_short: string; uom: string; description: string }): string {
  return `Shortage sent: ${qtyText(s.qty_short)} ${s.uom} of ${s.description}. The counter has been told.`;
}

// ---- staging ----

/** The client's check for the stage form: the server requires a location (a staged pick nobody can find is worse than an unstaged one). */
export function validateStage(location: string): string {
  const trimmed = location.trim();
  if (!trimmed) return "Where did you put it? A stage location is required.";
  if (trimmed.length > 120) return "That location is too long. 120 characters at most.";
  return "";
}

// ---- skids ----

/** The most skids one order can plausibly leave on (the core's maxSkids). */
export const MAX_SKIDS = 5000;

/** The client's check for a skid count change: a whole number, and a reason from the closed list. */
export function skidBlockedReason(count: number | null, reason: string, note: string): string {
  if (count === null || !Number.isInteger(count)) return "Say how many skids. Zero is an answer, blank is not.";
  if (count < 0) return "A skid count cannot be negative.";
  if (count > MAX_SKIDS) return "That skid count is implausibly large.";
  if (reason === "") return "Give the reason for the skid count.";
  if (!isShortReason(reason)) return "That is not one of the short pick reasons.";
  if (reason === "OTHER" && note.trim() === "") return "Say what happened in the note when the reason is Other.";
  return "";
}

/** The words the yard reads after a skid count change: the count the server holds, and whether it changed. */
export function skidWords(answer: { skid_count: number; previous_skid_count: number | null; changed: boolean; order_no: string }): string {
  const n = answer.skid_count;
  const skids = `${n} skid${n === 1 ? "" : "s"}`;
  if (!answer.changed) return `Still ${skids} on ${answer.order_no}. Nothing changed.`;
  if (answer.previous_skid_count === null) return `${answer.order_no} now holds ${skids}.`;
  return `${answer.order_no} changed from ${answer.previous_skid_count} to ${n} skids.`;
}
// ---- refusals ----

/**
 * The role gate's shared Forbidden body, byte for byte (the gate writes it
 * itself, core/internal/platform/authz/middleware.go): a list walled route
 * answers a caller off the list with this bare body and no sentence at all.
 */
export const FORBIDDEN_BODY = "Forbidden";

/**
 * Who may resolve a shortage, the matrix's own labels for the
 * shortage_resolvers list the core holds (core/internal/roleconfig,
 * default.json: yard_team, yard_manager). The refusal names them, in words,
 * because a picker told only "Forbidden" cannot know whom to fetch.
 */
export const RESOLVE_WALL_NAMES: readonly string[] = ["Yard Team", "Yard Manager"];

/** The plain words a list wall's refusal becomes: who may do the thing, named. */
export function wallWords(who: readonly string[]): string {
  const names = who.filter((n) => n !== "");
  if (names.length === 0) return "That is not yours to settle.";
  const named = names.map((n) => (/^the /i.test(n) ? n : `the ${n}`));
  const list = named.length === 1 ? named[0] : `${named.slice(0, -1).join(", ")} and ${named[named.length - 1]}`;
  return `That is not yours to settle. Only ${list} may resolve a shortage.`;
}

/** The core's refusals arrive as "module: sentence" already stripped to the sentence by the envelope; the yard reads the sentence. */
export function plainSentence(msg: string | null | undefined): string {
  const s = (msg ?? "").trim();
  const m = /^([a-z][a-z0-9_]*): (.+)$/s.exec(s);
  return m ? (m[2] ?? "").trim() : s;
}

export function yardError(
  error: string,
  status: number | undefined,
  fallback: string,
  words: { notFound?: string; wall?: readonly string[] } = {},
): string {
  if (status === undefined || status >= 500) return fallback;
  if (status === 401) return "Your sign in has ended. Sign out and sign in again.";
  if (status === 403) {
    // The gate's bare body is the one 403 with no sentence: a list wall. It
    // is answered with the wall's own words naming who may, never shown as
    // the bare "Forbidden" the gate writes.
    const plain = plainSentence(error);
    if (plain === FORBIDDEN_BODY) return wallWords(words.wall ?? RESOLVE_WALL_NAMES);
    // A 403 that carries the server's own sentence shows it: it names who may
    // act. Only a refusal with no words gets the generic ones, and those name
    // the two roles the matrix admits to the yard surface.
    if (plain !== "") {
      const cased = plain.charAt(0).toUpperCase() + plain.slice(1);
      return /[.!?]$/.test(cased) ? cased : `${cased}.`;
    }
    return `You are not allowed to do that on this yard. Only ${RESOLVE_WALL_NAMES.map((n) => `the ${n}`).join(" and ")} work here.`;
  }
  if (status === 404) return words.notFound ?? "That is no longer on the board.";
  const plain = plainSentence(error);
  if (plain === "") return fallback;
  const cased = plain.charAt(0).toUpperCase() + plain.slice(1);
  return /[.!?]$/.test(cased) ? cased : `${cased}.`;
}
