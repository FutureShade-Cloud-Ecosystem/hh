// The receivables office's plain logic: the checks made before a request is sent, the words for what the server
// answered, and who may do what the role may not. Nothing here computes money, and no address or number a
// customer gave is ever put into words: a contact reads as what is on file, never as the address itself, and a
// message reads as where it goes by role, since the messaging sink is the only way out in the demo.
//
// The limits and sentences follow the core (core/internal/billing: ar_reminders.go, ar_landing.go,
// register_service.go; core/internal/till; core/internal/payments) so a refusal the browser makes is the one the
// server would have made.
import type { BillingAPContact, BillingAPContactRead, BillingLandingPolicy, BillingReminderSend } from "@hardscapehouse/api-client";
import { refusalWords } from "./ar-logic.js";

// ---- who may, in words, for the walls this slice meets ------------------------------------------------------------

const CONTROLLER_ONLY = "That is for the Financial Controller or the General Manager. Payables and Receivables can read it and work the accounts.";

const WHO_MAY: Record<string, string> = {
  "policy-write": `Changing receivables rules is for the Financial Controller or the General Manager. Payables and Receivables can read the rules the desk runs on.`,
  "auto-hold": `The automatic credit hold switch is for the Financial Controller or the General Manager.`,
  "nsf-policy": `The returned payment fee and its hold are for the Financial Controller or the General Manager to change.`,
  "policy-history": CONTROLLER_ONLY,
  "till-release": `Lifting a hold that a returned payment placed is for the Financial Controller or the General Manager.`,
};

/** A refusal as words: the walls this slice meets name who may, everything else is the receivables desk's own words. */
export function officeRefusal(action: string, error: string, status: number | undefined): string {
  // The gate's shared body is a wall for a role; any other 403 is the core's own sentence (the base plan, a branch
  // the caller cannot reach) and is said as the core wrote it.
  if (status === 403 && error !== "Forbidden" && error.trim() !== "") return refusalWords(action, error, 422);
  if (status === 403 && WHO_MAY[action] !== undefined) return WHO_MAY[action] ?? "";
  return refusalWords(action, error, status);
}

// ---- one key per attempt, kept while the outcome is unknown ------------------------------------------------------

/**
 * Keys for the writes that carry one. A key is minted at the first press of an attempt and sent again on every
 * retry of it; it ends only when the server has answered (a result or a refusal), never on a lost answer or a 5xx.
 * The attempt is named by what it is (the action and the thing it acts on), so the same press retried finds its key
 * and another thing's press never shares it.
 */
export class AttemptKeys {
  readonly #held = new Map<string, string>();
  readonly #mint: () => string;

  constructor(mint: () => string) {
    this.#mint = mint;
  }

  /** The key for this attempt: the one already held, or a fresh one now held. */
  keyFor(attempt: string): string {
    const held = this.#held.get(attempt);
    if (held !== undefined) return held;
    const key = this.#mint();
    this.#held.set(attempt, key);
    return key;
  }

  /** The server answered: the attempt is over and its key ends. */
  settle(attempt: string): void {
    this.#held.delete(attempt);
  }

  held(attempt: string): boolean {
    return this.#held.has(attempt);
  }
}

/** Whether an answer leaves the outcome unknown: the request may have landed and the answer been lost. */
export function outcomeUnknown(status: number | undefined): boolean {
  return status === undefined || status >= 500;
}

// ---- AP contacts ---------------------------------------------------------------------------------------------------

export type ContactCheck = { ok: true; contact: { contact_name: string; email: string; phone: string } } | { ok: false; words: string };

/** The core's ValidateAPContact, said before the request is sent. */
export function validateApContact(input: { name: string; email: string; phone: string }): ContactCheck {
  const name = input.name.trim();
  const email = input.email.trim();
  const phone = input.phone.trim();
  if ([...name].length > 120) return { ok: false, words: "The contact name is at most 120 characters." };
  if (email.length > 254 || phone.length > 32) return { ok: false, words: "An AP email is at most 254 characters and a phone at most 32." };
  if (email !== "" && (!email.includes("@") || /[ \t]/.test(email))) return { ok: false, words: "That AP email address is not an address." };
  if (email === "" && phone === "") return { ok: false, words: "An AP contact needs an email or a phone: one of the two is what makes it a contact." };
  return { ok: true, contact: { contact_name: name, email, phone } };
}

/** What is on file, in words, never the address or the number itself. */
export function contactLine(read: BillingAPContactRead | null): string {
  if (read === null || !read.tagged || read.contact === undefined) return "No AP contact is tagged on this account.";
  const c: BillingAPContact = read.contact;
  const email = c.email.trim() !== "" ? "email on file" : "no email";
  const phone = c.phone.trim() !== "" ? "phone on file" : "no phone";
  const label = c.contact_name.trim();
  return label === "" ? `AP contact on file: ${email}, ${phone}.` : `AP contact on file, ${label}: ${email}, ${phone}.`;
}

/** The channel a contact can be reached on: email first, then phone; nothing when the contact is empty. */
function reach(c: BillingAPContact): string {
  if (c.email.trim() !== "") return "by email";
  if (c.phone.trim() !== "") return "by phone";
  return "";
}

/** Where a statement would go: the AP contact by its role label, or nowhere. */
export function statementGoes(read: BillingAPContactRead | null | "error", delivery: string): string {
  if (read === null) return "Checking the contact";
  if (read === "error") return "The contact could not be read";
  if (!read.tagged || read.contact === undefined) return "Nowhere: no AP contact is tagged";
  const label = read.contact.contact_name.trim();
  const via = reach(read.contact);
  const to = label === "" ? "To the AP contact" : `To the AP contact (${label})`;
  void delivery;
  return via === "" ? to : `${to}, ${via}`;
}

// ---- statement runs ------------------------------------------------------------------------------------------------

export function runModeWord(mode: string): string {
  return mode === "INTERIM" ? "One account" : "Month end, every account";
}

export function runNoticeWords(run: { notices_enabled: boolean; notice_queued_count: number; notice_suppressed_count: number }): string {
  if (!run.notices_enabled) return "Statement notices are switched off for this dealer, so nothing was queued.";
  const n = run.notice_queued_count;
  const queued = `${n} ${n === 1 ? "statement" : "statements"} went to the messaging sink`;
  return run.notice_suppressed_count > 0 ? `${queued}; ${run.notice_suppressed_count} had no AP contact or was held back.` : `${queued}.`;
}

const DELIVERY_WORD: Record<string, string> = { NOT_QUEUED: "Not queued", QUEUED: "Queued", SENT: "Sent", FAILED: "Failed" };

export function deliveryWord(status: string): string {
  return DELIVERY_WORD[status] ?? status;
}

// ---- reminders -----------------------------------------------------------------------------------------------------

export type Channel = "EMAIL" | "SMS";

export function channelWord(channel: string): string {
  return channel === "SMS" ? "text" : "email";
}

const SEND_STATUS_WORD: Record<string, string> = { QUEUED: "Queued", DELIVERED: "Went to the messaging sink", FAILED: "Failed: nothing left the building" };

export function sendStatusWord(status: string): string {
  return SEND_STATUS_WORD[status] ?? status;
}

/** Who a send went to, by the kind of recipient and never by address. */
export function recipientWords(send: Pick<BillingReminderSend, "recipient_kind">): string {
  return send.recipient_kind === "FALLBACK" ? "the account's own address, which you confirmed" : "the AP contact";
}

export function reminderResultWords(send: BillingReminderSend, invoiceNo: string): string {
  if (send.status === "FAILED") return `Reminder for ${invoiceNo} did not go: nothing left the building.`;
  const stamp = send.watermark !== undefined && send.watermark !== "" ? ` It was stamped ${send.watermark}.` : "";
  if (send.status === "QUEUED") return `Reminder for ${invoiceNo} is queued for ${recipientWords(send)} by ${channelWord(send.channel)}.${stamp}`;
  return `Reminder for ${invoiceNo} went to ${recipientWords(send)} by ${channelWord(send.channel)}, through the messaging sink.${stamp}`;
}

export interface ReminderPreview {
  /** Why nothing would go, in words; empty when something would. */
  readonly blocked: string;
  readonly lines: readonly string[];
}

/**
 * What a reminder would do before it is sent, from the dealer's policy and the account's contact. It is a
 * reading of the core's ResolveTarget, never a second judge: the server decides, and a refusal is said as it wrote it.
 */
export function reminderPreview(input: { policy: BillingLandingPolicy; channel: Channel; contact: BillingAPContactRead | null | "error"; confirm: boolean }): ReminderPreview {
  const { policy, channel, contact, confirm } = input;
  const word = channelWord(channel);
  if (policy.ar_tier !== "PRO") return { blocked: "Reminders are part of accelerated AR, and this dealer is on the base plan.", lines: [] };
  if (channel === "EMAIL" && !policy.reminder_email_enabled) return { blocked: "Email reminders are switched off for this dealer.", lines: [] };
  if (channel === "SMS" && !policy.reminder_sms_enabled) return { blocked: "Text reminders are switched off for this dealer.", lines: [] };
  if (contact === null) return { blocked: "", lines: ["Checking the contact."] };
  if (contact === "error") return { blocked: "", lines: ["The contact could not be read. The server will still decide where it goes."] };
  const tagged = contact.tagged && contact.contact !== undefined;
  const reachable = tagged && (channel === "SMS" ? (contact.contact?.phone.trim() ?? "") !== "" : (contact.contact?.email.trim() ?? "") !== "");
  if (reachable) {
    const label = contact.contact?.contact_name.trim() ?? "";
    return { blocked: "", lines: [`Goes to the AP contact${label === "" ? "" : ` (${label})`}, by ${word}, through the messaging sink.`] };
  }
  const who = tagged ? `No AP contact is reachable by ${word}` : "Nobody is tagged for AP on this account";
  if (policy.untagged_fallback === "REFUSE") return { blocked: `${who}, and this dealer does not fall back to the account's own address.`, lines: [] };
  return {
    blocked: "",
    lines: [confirm ? `${who}. The account's own address will be used, as you confirmed.` : `${who}. The account's own address is used only if you confirm it below.`],
  };
}

// ---- the paper path ------------------------------------------------------------------------------------------------

export type PaperResolveCheck = { ok: true; status: "MOVED_TO_PAPER" | "DISMISSED"; note: string } | { ok: false; words: string };

/** The core's ResolvePaperPathFlag, said before the request is sent. */
export function paperResolveCheck(status: string, note: string): PaperResolveCheck {
  if (status !== "MOVED_TO_PAPER" && status !== "DISMISSED") return { ok: false, words: "Choose how the flag closes: moved to paper, or dismissed." };
  const trimmed = note.trim();
  if (trimmed === "") return { ok: false, words: "Say why in a few words: the next person to open this account reads it, not the flag." };
  if ([...trimmed].length > 300) return { ok: false, words: "That note is longer than 300 characters." };
  return { ok: true, status, note: trimmed };
}

// ---- time ----------------------------------------------------------------------------------------------------------

const DAY_MS = 86_400_000;

/**
 * A moment as the days since it, counted from elapsed time: "today" inside the first day, then "1 day ago". A
 * timestamp is an instant, not a calendar date, and the desk does not guess the dealer's zone to cut it into days.
 */
export function relativeStamp(stamp: string, nowMs: number): string {
  const at = Date.parse(stamp);
  if (Number.isNaN(at)) return "";
  const days = Math.max(0, Math.floor((nowMs - at) / DAY_MS));
  if (days === 0) return "today";
  return `${days} ${days === 1 ? "day" : "days"} ago`;
}
