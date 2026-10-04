// Ported from hardscapeos_dibbits master 87d7941b app/src/_quote-followups.ts;
// adapted for HH.
//
// _quote-followups.ts: the follow-up panel's pure presentation logic (DOM-free,
// unit tested on the Node runner). What the salesperson is asking when they
// open the panel is "has this customer heard from us, and when will they hear
// from us next?". Everything here exists to answer that in one list, with past
// and future in the same column: a history that hides the schedule leaves them
// phoning a customer we are about to text anyway. The cadence, the consent
// refusal and the schedule all come from the server; nothing here judges.

export type FollowUpStatus = "SENT" | "SKIPPED" | "CANCELLED";
export type FollowUpChannel = "EMAIL" | "SMS" | "BOTH";

export interface FollowUpEventRow {
  step_day: number;
  channel: FollowUpChannel;
  status: FollowUpStatus;
  reason?: string;
  scheduled_for: string;
  occurred_at: string;
}

export interface FollowUpStepRow {
  step_day: number;
  channel: FollowUpChannel;
  due_at: string;
  scheduled: boolean;
}

export interface FollowUpPlanRow {
  enabled: boolean;
  channel: FollowUpChannel;
  offsets_days: number[];
  started_at: string;
  disabled_at?: string | null;
  disabled_why?: string;
}

export interface FollowUpHistory {
  quote_id: string;
  plan?: FollowUpPlanRow;
  events: FollowUpEventRow[];
  upcoming: FollowUpStepRow[];
  automated: boolean;
}

/** One row of the timeline: a past event or a scheduled step, deliberately the same shape. */
export interface TimelineRow {
  key: string;
  day: number;
  channel: FollowUpChannel;
  state: FollowUpStatus | "SCHEDULED";
  when: string;
  detail: string;
  /** True for a step that has not happened yet, so the panel can dim it. */
  future: boolean;
}

/**
 * timeline merges the recorded events and the upcoming steps into one ordered
 * list: by day, then channel, NOT by when a row was written. A cadence reads
 * as "day 3, day 7, day 14"; sorting by time would put a step skipped
 * instantly above one sent an hour earlier.
 */
export function timeline(h: FollowUpHistory | null | undefined): TimelineRow[] {
  if (!h) return [];
  const rows: TimelineRow[] = [];
  for (const e of h.events ?? []) {
    rows.push({ key: `e:${e.step_day}:${e.channel}`, day: e.step_day, channel: e.channel, state: e.status, when: e.occurred_at, detail: e.reason ?? "", future: false });
  }
  for (const u of h.upcoming ?? []) {
    rows.push({ key: `u:${u.step_day}:${u.channel}`, day: u.step_day, channel: u.channel, state: "SCHEDULED", when: u.due_at, detail: "", future: true });
  }
  rows.sort((a, b) => a.day - b.day || a.channel.localeCompare(b.channel));
  return rows;
}

/**
 * stateLabel is the word a salesperson reads. SENT reads "Queued": with the
 * provider switched off nothing has left the building, and a screen that says
 * "Sent" to somebody about to tell a customer "we texted you" lies on their
 * behalf.
 */
export function stateLabel(state: TimelineRow["state"]): string {
  switch (state) {
    case "SENT":
      return "Queued";
    case "SKIPPED":
      return "Not sent";
    case "CANCELLED":
      return "Cancelled";
    default:
      return "Scheduled";
  }
}

/** channelWords renders the plan channel in words rather than an enum. */
export function channelWords(channel: FollowUpChannel): string {
  switch (channel) {
    case "SMS":
      return "text";
    case "BOTH":
      return "email and text";
    default:
      return "email";
  }
}

/** One channel in a row's own words: a step is one channel, so BOTH only arises on the plan. */
function stepWords(channel: FollowUpChannel): string {
  return channel === "SMS" ? "text" : channelWords(channel);
}

/**
 * summaryLine is the one sentence at the top of the panel. It states the
 * current state plainly and never implies more than is true: no plan at all
 * reads differently from a plan somebody switched off, because those need
 * different next actions.
 */
export function summaryLine(h: FollowUpHistory | null | undefined): string {
  if (!h || !h.plan) return "No follow-up cadence on this quote yet: sending the quote starts one.";
  const steps = h.plan.offsets_days ?? [];
  const cadence = steps.length > 0 ? `day ${steps.join(", day ")}` : "no steps";
  if (!h.plan.enabled) {
    const why = h.plan.disabled_why ? ` (${h.plan.disabled_why})` : "";
    return `Follow-ups are OFF for this quote${why}. Nothing further will be sent.`;
  }
  return `Follow-ups are on: ${cadence} after the quote was sent, by ${channelWords(h.plan.channel)}.`;
}

/**
 * contactLine answers the two halves of the question in one line: the latest
 * step that went out (a skipped or cancelled step is not hearing from us) and
 * the earliest step still to come. Empty when there is no plan and nothing
 * happened, so the summary line alone speaks for a quote with no cadence.
 * `whenLabel` words a timestamp; the caller owns the locale.
 */
export function contactLine(h: FollowUpHistory | null | undefined, whenLabel: (iso: string) => string): string {
  if (!h) return "";
  const events = h.events ?? [];
  const upcoming = h.upcoming ?? [];
  if (h.plan === undefined && events.length === 0 && upcoming.length === 0) return "";
  const sent = events
    .filter((e) => e.status === "SENT")
    .sort((a, b) => Date.parse(b.occurred_at) - Date.parse(a.occurred_at))[0];
  const next = upcoming.slice().sort((a, b) => Date.parse(a.due_at) - Date.parse(b.due_at))[0];
  const heard =
    sent === undefined
      ? "Not heard from us yet"
      : `Heard from us: day ${sent.step_day} ${stepWords(sent.channel)}, queued ${whenLabel(sent.occurred_at)}`;
  // A semicolon, not a full stop: a locale's own timestamp can end in a full stop ("a.m.").
  const after = next === undefined ? "nothing further is scheduled" : `next: day ${next.step_day} ${stepWords(next.channel)}, ${whenLabel(next.due_at)}`;
  return `${heard}; ${after}`;
}

/**
 * shareErrorMessage turns the text leg's refusal into something a salesperson
 * can act on. The server's own words win; the fallbacks are only for a refusal
 * that came with none. The 409 is not a failure, it is the system saying this
 * customer never agreed to be texted, so the fallback names the alternative.
 */
export function shareErrorMessage(status: number | undefined, detail: string): string {
  const d = detail.trim();
  if (status === 409) return d || "This customer has not agreed to receive texts: send the link by email, or ask them to opt in.";
  if (status === 503) return d || "Text messaging is not configured on this server.";
  if (status === 403) return d || "This quote belongs to another branch.";
  if (status === 404) return "That quote no longer exists.";
  return d || "Could not queue the text.";
}

/** The notice after a successful send: queued, never sent (see stateLabel). */
export function queuedNotice(): string {
  return "Text queued.";
}
