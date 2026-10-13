// The compliance slice's plain logic: the words for every deadline and review state the core stores, how a due
// date reads, how the horizon box and the review form become what the core takes, and how a refusal reads out
// loud. Nothing here computes a date: the core says what is due, overdue and upcoming, and the app shows it.
//
// Port provenance: the kind and state words are the original app's (app/src/lien-shared.ts, the calendar tab of the
// lien cockpit) with the stored tokens opened out. The review status banner is the original's shared, data driven
// DRAFT strip: every lien screen reads it from here, so a banner change is a row edit and never a redeploy.
import type { ComplianceReviewStatusInput } from "@hardscapehouse/api-client";

export type Tone = "stop" | "watch" | "good" | "plain";

function opened(value: string): string {
  return value.toLowerCase().replace(/_/g, " ");
}

function wordsFrom(table: Readonly<Record<string, string>>, value: string): string {
  return table[value] ?? opened(value);
}

/** The one subject the lien cockpit's banner is kept under (the original's, kept so the core's rows still match). */
export const COCKPIT_SUBJECT = "00000000-0000-4000-9000-0000000000d6";

const KIND: Record<string, string> = {
  HOLDBACK_RELEASE: "Holdback release",
  LIEN_EXPIRY: "Lien expiry",
  NOTICE_DEADLINE: "Notice deadline",
  PROOF_OF_CLAIM: "Proof of claim",
  OTHER: "Other",
};
export const deadlineKindWord = (kind: string): string => wordsFrom(KIND, kind);

const STATUS: Record<string, string> = { OPEN: "Open", SATISFIED: "Satisfied", WAIVED: "Waived", OVERDUE: "Overdue" };
export const deadlineStatusWord = (status: string): string => wordsFrom(STATUS, status);

export function deadlineTone(status: string): Tone {
  if (status === "OVERDUE") return "stop";
  if (status === "OPEN") return "watch";
  if (status === "SATISFIED") return "good";
  return "plain";
}

/** The calendar's status filter: "" is every status, and the core takes the other four. */
export const STATUS_FILTERS: ReadonlyArray<{ id: "" | "OPEN" | "OVERDUE" | "SATISFIED" | "WAIVED"; word: string }> = [
  { id: "", word: "All" },
  { id: "OPEN", word: "Open" },
  { id: "OVERDUE", word: "Overdue" },
  { id: "SATISFIED", word: "Satisfied" },
  { id: "WAIVED", word: "Waived" },
];

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** An ISO day reads as the month name, the day and the year. A day that is not a real day is left as the core wrote it. */
export function dueWords(day: string): string {
  if (day === "") return "No date";
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  if (m === null) return day;
  const [year, month, date] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const at = new Date(Date.UTC(year, month - 1, date));
  if (at.getUTCFullYear() !== year || at.getUTCMonth() !== month - 1 || at.getUTCDate() !== date) return day;
  return `${MONTHS[month - 1]} ${date}, ${year}`;
}

/** The horizon box: blank leaves the core's own horizon, otherwise a whole number of days from 1 to 365. */
export function horizonDays(text: string): number | "default" | null {
  const trimmed = text.trim();
  if (trimmed === "") return "default";
  if (!/^[0-9]{1,3}$/.test(trimmed)) return null;
  const days = Number(trimmed);
  return days >= 1 && days <= 365 ? days : null;
}

const REVIEW_STATE: Record<string, string> = { DRAFT: "Draft", PENDING: "Pending review", APPROVED: "Approved", REJECTED: "Rejected" };
export const reviewStateWord = (state: string): string => wordsFrom(REVIEW_STATE, state);
export const REVIEW_STATES = ["DRAFT", "PENDING", "APPROVED", "REJECTED"] as const;

export function reviewTone(state: string): Tone {
  if (state === "APPROVED") return "good";
  if (state === "REJECTED") return "stop";
  return "watch";
}

export type ReviewFormResult = { readonly ok: true; readonly body: ComplianceReviewStatusInput } | { readonly ok: false; readonly words: string };

type SubjectType = ComplianceReviewStatusInput["subject_type"];
type ReviewState = ComplianceReviewStatusInput["state"];

/** The review form as the body the core takes. A blank banner is sent blank: the core answers its own default. */
export function reviewForm(subjectType: string, subjectId: string, state: string, banner: string): ReviewFormResult {
  if (subjectId.trim() === "") return { ok: false, words: "Pick what the review is about." };
  if (!(REVIEW_STATES as readonly string[]).includes(state)) return { ok: false, words: "Pick a review state." };
  return { ok: true, body: { subject_type: subjectType as SubjectType, subject_id: subjectId.trim(), state: state as ReviewState, banner_text: banner.trim() } };
}

export interface BannerView {
  readonly text: string;
  readonly tone: Tone;
  readonly draft: boolean;
}

/** The strip every lien screen carries: the core's own words and state, never a banner the app wrote. */
export function banner(status: { state: string; banner_text: string; is_draft: boolean } | null): BannerView {
  if (status === null) return { text: "The review status did not load.", tone: "plain", draft: true };
  return { text: status.banner_text, tone: reviewTone(status.state), draft: status.is_draft };
}

/** The line a role that only reads is shown where the review form would be. */
export const READ_ONLY_REVIEW = "Setting the review status is for the Financial Controller or the General Manager.";

function sentence(message: string): string {
  const trimmed = message.replace(/^(lien|compliance): /, "").trim();
  if (trimmed === "") return "";
  const cap = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
  return /[.!?]$/.test(cap) ? cap : `${cap}.`;
}

/** A refusal in words: the shared Forbidden body names who may do it, any other keeps the core's sentence. */
export function refusalWords(message: string, status: number): string {
  const lower = message.toLowerCase().trim();
  if (status === 403 && (lower === "" || lower === "forbidden")) return "That is for the Financial Controller or the General Manager.";
  const words = sentence(message);
  return words === "" ? "The compliance calendar did not answer. Reload to try again." : words;
}
