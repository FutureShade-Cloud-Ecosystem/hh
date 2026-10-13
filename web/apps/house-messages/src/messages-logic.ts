// House Messages rules as plain code, ported from the original app's pure messaging helpers
// (_messaging-conversation.ts, _messaging-error.ts) and its hub, onto HH's generated types. Nothing
// here talks to the network or the page. Every rule has a server twin that is the actual
// enforcement; these exist so the screen does not offer what the core will refuse, and so each
// "what does this say" question has one answer. A figure the core sends is shown as sent; nothing
// here sums, estimates or invents money.
import type {
  MessagingAckProgress,
  MessagingChannel,
  MessagingEventRef,
  MessagingHouseConversation,
  MessagingMessage,
} from "@hardscapehouse/api-client";

export type Channel = MessagingChannel;
export type Message = MessagingMessage;
export type HouseConversation = MessagingHouseConversation;
export type AckProgress = MessagingAckProgress;
export type ChannelTier = MessagingChannel["tier"];
export type NotifyMode = "ALL" | "MENTIONS" | "MUTED";

// ---- the composer ------------------------------------------------------------------------------

/**
 * The core's cap on one message, in bytes (messaging/service.go maxBody). A longer body is not
 * refused by the core, it is cut off, so the screen refuses to send one rather than let a person
 * lose the end of their message without knowing.
 */
export const MAX_BODY_BYTES = 8 * 1024;

/** The room left at which the composer starts saying so. */
const NEARLY_FULL_BYTES = 512;

const ENCODER = new TextEncoder();

/** A text's size in UTF-8 bytes, the unit the core counts. */
export function bodyBytes(text: string): number {
  return ENCODER.encode(text).length;
}

export interface ComposerState {
  /** The trimmed body's size, as the core measures it after it trims. */
  readonly bytes: number;
  /** Bytes past the limit, zero when within it. */
  readonly over: number;
  readonly canSend: boolean;
  /** A sentence for the line under the field; empty when there is nothing to say. */
  readonly words: string;
}

const bytesWord = (n: number): string => `${n} byte${n === 1 ? "" : "s"}`;

/** What the composer can do with this draft, and the sentence under it. */
export function composerState(draft: string): ComposerState {
  const trimmed = draft.trim();
  const bytes = bodyBytes(trimmed);
  if (bytes === 0) return { bytes, over: 0, canSend: false, words: "" };
  if (bytes > MAX_BODY_BYTES) {
    const over = bytes - MAX_BODY_BYTES;
    return {
      bytes,
      over,
      canSend: false,
      words: `This message is ${bytesWord(over)} over the 8 KB limit. The core would cut the end off, so shorten it before you send.`,
    };
  }
  const left = MAX_BODY_BYTES - bytes;
  return { bytes, over: 0, canSend: true, words: left <= NEARLY_FULL_BYTES ? `${bytesWord(left)} left` : "" };
}

// ---- the channel list --------------------------------------------------------------------------

export interface ChannelGroup {
  readonly label: string;
  readonly channels: readonly Channel[];
}

const GROUPS: readonly { label: string; kinds: readonly Channel["kind"][] }[] = [
  { label: "Direct messages", kinds: ["DM"] },
  { label: "Rooms", kinds: ["TEAM", "TOPIC"] },
  { label: "Branch", kinds: ["BRANCH"] },
  { label: "Approvals", kinds: ["APPROVAL"] },
];

/** The list's groups in reading order; a group with no channel is not drawn. The core's order holds inside a group. */
export function channelGroups(channels: readonly Channel[]): ChannelGroup[] {
  return GROUPS.map((g) => ({ label: g.label, channels: channels.filter((c) => g.kinds.includes(c.kind)) })).filter(
    (g) => g.channels.length > 0,
  );
}

export function channelTitle(channel: Channel): string {
  if (channel.name.trim() !== "") return channel.name;
  return channel.kind === "DM" ? "Direct message" : "Untitled room";
}

/** A channel's one line under its name: its topic, or what kind of room it is. */
export function channelLine(channel: Channel): string {
  if (channel.topic.trim() !== "") return channel.topic;
  switch (channel.kind) {
    case "DM":
      return "Between the two of you";
    case "BRANCH":
      return "Everyone at the yard";
    case "APPROVAL":
      return "Decisions waiting on you";
    default:
      return "";
  }
}

/** The core files a branch's unassigned customer messages under house-<yard>; the queue lives there. */
export function isHouseChannel(channel: Channel): boolean {
  return (channel.slug ?? "").toLowerCase().startsWith("house-");
}

export function unreadWord(channel: Pick<Channel, "unread_count" | "mentioned">): string {
  const n = channel.unread_count;
  if (n <= 0) return "";
  const count = n > 99 ? "99+" : String(n);
  return channel.mentioned ? `${count} unread, you are mentioned` : `${count} unread`;
}

export function totalUnread(channels: readonly Pick<Channel, "unread_count">[]): number {
  return channels.reduce((sum, c) => sum + Math.max(0, c.unread_count), 0);
}

// ---- the stream --------------------------------------------------------------------------------

function byTime(a: Message, b: Message): number {
  if (a.created_at !== b.created_at) return a.created_at < b.created_at ? -1 : 1;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/**
 * The stream reads oldest at the top. The core answers newest first, so the page is reordered here,
 * and only top level messages are drawn: a reply belongs to its thread.
 */
export function streamOrder(messages: readonly Message[]): Message[] {
  return messages.filter((m) => !m.thread_root_id).sort(byTime);
}

/** An older page joins what is held, a message once, in reading order. */
export function mergeOlder(held: readonly Message[], older: readonly Message[]): Message[] {
  const seen = new Set<string>();
  const out: Message[] = [];
  for (const m of [...older, ...held]) {
    if (seen.has(m.id)) continue;
    seen.add(m.id);
    out.push(m);
  }
  return out.sort(byTime);
}

const CLOCK = new Intl.DateTimeFormat("en-US", { hour: "numeric", minute: "2-digit" });
const SHORT_DATE = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" });
const LONG_DATE = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric" });

/**
 * When a message was sent, in the dealer's local time: a clock time today, a short date and time
 * before today, and the year only when it is not this one. Empty for a time that does not parse.
 */
export function timeWords(iso: string, now: number): string {
  const then = new Date(iso);
  if (Number.isNaN(then.getTime())) return "";
  const today = new Date(now);
  const clock = CLOCK.format(then);
  if (then.toDateString() === today.toDateString()) return clock;
  const date = then.getFullYear() === today.getFullYear() ? SHORT_DATE.format(then) : LONG_DATE.format(then);
  return `${date}, ${clock}`;
}

export function repliesWord(count: number): string {
  if (count <= 0) return "Reply";
  return count === 1 ? "1 reply" : `${count} replies`;
}

/** A word for the messages that are not a person's own: the system's notices and the approval cards. */
export function messageKindWord(message: Pick<Message, "kind">): string {
  if (message.kind === "EVENT") return "System notice";
  if (message.kind === "APPROVAL") return "Approval";
  return "";
}

const RULE_WORDS: Readonly<Record<string, string>> = {
  ONLINE: "an online payment",
  CROSS_BRANCH: "a payment at another branch",
  OWN_ORDER: "your own order",
};

/** Why a targeted notice reached the caller, from the rules the core named and nothing else. */
export function eventWhy(ref: MessagingEventRef | undefined): string {
  const rules = (ref?.rules ?? []).map((r) => RULE_WORDS[r]).filter((w): w is string => w !== undefined);
  return rules.length === 0 ? "" : `Reached you because: ${rules.join(", ")}`;
}

// ---- acknowledgement ---------------------------------------------------------------------------

/**
 * The one forced acknowledgement message the persistent bubble shows, from the caller's unacked
 * list. A superseded message is never shown (a newer fact about the same record resolved it, and
 * asking someone to acknowledge a stale fact is asking them to certify something untrue). The
 * oldest waits longest, so it is the one shown. Nothing unacked, no bubble.
 */
export function bubbleFor(messages: readonly Message[] | null | undefined): Message | null {
  const live = (messages ?? []).filter((m) => m.requires_ack === true && !m.superseded_at && !m.superseded_by);
  if (live.length === 0) return null;
  return [...live].sort(byTime)[0] ?? null;
}

/** After this message is acknowledged, does the bubble stay up? It does while another waits. */
export function bubblePersistsAfter(messages: readonly Message[], ackedId: string): boolean {
  return bubbleFor(messages.filter((m) => m.id !== ackedId)) !== null;
}

/**
 * The ack arithmetic for a person. Zero required is called out rather than printed as "0 of 0",
 * which reads like completion when it means nobody can acknowledge this at all.
 */
export function ackProgressLabel(progress: AckProgress | null | undefined): string {
  if (!progress || progress.required <= 0) return "No one is assigned to acknowledge this.";
  if (progress.complete) return `Acknowledged by everyone · ${progress.acked} of ${progress.required}`;
  return `${progress.acked} of ${progress.required} acknowledged · ${progress.remaining} to go`;
}

// ---- the house queue ---------------------------------------------------------------------------

/** Unclaimed first (the working set), the longest waiting first inside each group. */
export function queueOrder<T extends Pick<HouseConversation, "first_at" | "claimed_by">>(rows: readonly T[] | null | undefined): T[] {
  return [...(rows ?? [])].sort((a, b) => {
    const aOpen = !a.claimed_by;
    const bOpen = !b.claimed_by;
    if (aOpen !== bOpen) return aOpen ? -1 : 1;
    return a.first_at < b.first_at ? -1 : a.first_at > b.first_at ? 1 : 0;
  });
}

/** How long a conversation has waited, in the largest whole unit; empty for a time that does not parse. */
export function waitedWords(iso: string, now: number): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "";
  const minutes = Math.max(0, Math.round((now - then) / 60_000));
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.round(minutes / 60);
  return hours < 24 ? `${hours} h` : `${Math.round(hours / 24)} d`;
}

// ---- notify prefs ------------------------------------------------------------------------------

const MODES: readonly NotifyMode[] = ["ALL", "MENTIONS", "MUTED"];

/** An escalation channel carries alerts that must never be missed, so it can not be muted. */
export function canMute(tier: ChannelTier | undefined): boolean {
  return tier !== "ESCALATION";
}

export function modesFor(tier: ChannelTier | undefined): NotifyMode[] {
  return canMute(tier) ? [...MODES] : MODES.filter((m) => m !== "MUTED");
}

export function modeWords(mode: NotifyMode): string {
  switch (mode) {
    case "ALL":
      return "All messages";
    case "MENTIONS":
      return "Mentions only";
    case "MUTED":
      return "Muted";
  }
}

/** The rule stated when someone reaches it anyway (a stale tab, a 409): the rule, not the failure. */
export const MUTE_REFUSAL = "This is an escalation channel. It carries alerts that must never be missed, so it cannot be muted.";

// ---- words for a refusal -----------------------------------------------------------------------

/** Strips the module prefix a server sentence can carry ("messaging: ...") before a person reads it. */
export function plainSentence(message: string | null | undefined): string {
  const s = (message ?? "").trim();
  const m = /^([a-z][a-z0-9_]*): (.+)$/s.exec(s);
  return m ? (m[2] ?? "").trim() : s;
}

/**
 * A refusal in words. The core's own sentence is what a person acts on ("escalation channels cannot
 * be muted"), so it is kept. A transport error has no status and says the server was not reached.
 * A server fault says nothing changed, because nothing did.
 */
export function refusalWords(what: string, error: string, status: number | null): string {
  if (status === null || status === 0) return `House Messages could not reach the server. ${what} did not go through.`;
  if (status === 401) return "Your session ended. Sign in again from the front door.";
  if (status >= 500) return `The server could not finish. ${what} did not go through, and nothing changed.`;
  const sentence = plainSentence(error);
  if (sentence !== "") return sentence;
  if (status === 403) return "You are not a member of this channel.";
  if (status === 404) return "That is no longer there.";
  return `${what} was refused.`;
}
