// _quote-decision.ts: the pure side of recording the customer's decision on their behalf (PR 247), DOM-free so
// the Node runner can drive it. The app decides nothing about who may decide or when: the offer is the server's
// (can_accept, can_reject) and every refusal is shown in the server's words.
import { moneyWord } from "./_quote-edit.js";

export type DecisionChannel = "phone" | "text" | "email" | "in_person";
export type DeclineReason = "price" | "competitor" | "project_cancelled" | "timing" | "no_response" | "other";

export const DECISION_CHANNELS: ReadonlyArray<{ value: DecisionChannel; label: string }> = [
  { value: "phone", label: "On the phone" },
  { value: "text", label: "By text" },
  { value: "email", label: "By email" },
  { value: "in_person", label: "In person" },
];

export const DECLINE_REASONS: ReadonlyArray<{ value: DeclineReason; label: string }> = [
  { value: "price", label: "The price" },
  { value: "competitor", label: "Went with a competitor" },
  { value: "project_cancelled", label: "The project was cancelled" },
  { value: "timing", label: "The timing" },
  { value: "no_response", label: "No response" },
  { value: "other", label: "Something else (say what)" },
];

export function isChannel(value: string): value is DecisionChannel {
  return DECISION_CHANNELS.some((c) => c.value === value);
}

export function isReason(value: string): value is DeclineReason {
  return DECLINE_REASONS.some((r) => r.value === value);
}

/** What the accept needs before it may be pressed: a channel, and the statement that the customer confirmed the quantities. */
export function acceptReady(channel: string, quantitiesConfirmed: boolean): boolean {
  return isChannel(channel) && quantitiesConfirmed;
}

/** What the reject needs: a channel, a reason, and a note when the reason is something else. */
export function rejectReady(channel: string, reason: string, note: string): boolean {
  return isChannel(channel) && isReason(reason) && (reason !== "other" || note.trim() !== "");
}

export function channelWord(channel: string): string {
  return DECISION_CHANNELS.find((c) => c.value === channel)?.label.toLowerCase() ?? channel;
}

export function acceptedNotice(quoteNo: string, channel: string, totalCents: number): string {
  return `${quoteNo} was accepted for the customer, ${channelWord(channel)}, at ${moneyWord(totalCents)}. It can be converted to an order now.`;
}

export function declinedNotice(quoteNo: string, channel: string): string {
  return `${quoteNo} was closed as lost for the customer, ${channelWord(channel)}. Nothing was sent to them.`;
}

/**
 * One decision's idempotency key. The same key follows the same intent, so a retry after a lost answer replays
 * the first outcome instead of recording a second; a changed intent (another channel, total, note or reason)
 * gets a new key.
 */
export function keyFor(previous: { signature: string; key: string } | null, signature: string, fresh: () => string): { signature: string; key: string } {
  return previous !== null && previous.signature === signature ? previous : { signature, key: fresh() };
}
