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
//     ticketIsFixture, priorityWord, mtoLabel, orderHandle:
//     app/src/yard-logic.ts
// HH's own, written against the core and not ported:
//   - the task transition matrix mirrors core/internal/tasks/model.go
//   - yardError mirrors the core's error envelope (httpx.CounterMessage
//     strips the module prefix) and the tasks taxonomy's statuses
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

/** What the screen says after the move landed, one sentence per action. */
export function actionDone(action: PickAction): string {
  if (action === "claim") return "Claimed. It is yours to pull.";
  if (action === "start") return "Picking started.";
  return "Staged.";
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

// ---- refusals ----

/** The core's refusals arrive as "module: sentence" already stripped to the sentence by the envelope; the yard reads the sentence. */
export function plainSentence(msg: string | null | undefined): string {
  const s = (msg ?? "").trim();
  const m = /^([a-z][a-z0-9_]*): (.+)$/s.exec(s);
  return m ? (m[2] ?? "").trim() : s;
}

/**
 * What the yard reads when a call fails. A refusal in the core's own words is
 * shown as it came (a capital goes on, a full stop closes it); a server fault
 * or no answer is never shown as it came. `fallback` is that sentence for the
 * call that failed.
 */
export function yardError(
  error: string,
  status: number | undefined,
  fallback: string,
  words: { notFound?: string } = {},
): string {
  if (status === undefined || status >= 500) return fallback;
  if (status === 401) return "Your sign in has ended. Sign out and sign in again.";
  if (status === 403) return "You are not allowed to do that on this yard.";
  if (status === 404) return words.notFound ?? "That is no longer on the board.";
  const plain = plainSentence(error);
  if (plain === "") return fallback;
  const cased = plain.charAt(0).toUpperCase() + plain.slice(1);
  return /[.!?]$/.test(cased) ? cased : `${cased}.`;
}
