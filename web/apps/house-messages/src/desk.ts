// The House Messages desk's controller: the state the screens draw from, the reads, and what each
// press does. It is not an element; the root element owns it, hands it a way to repaint and a way to
// find the form fields, and asks it for the template.
//
// Every write is held to the server: a message is in the stream only when the core answers, the
// unread count clears only when the core has marked the channel read, an acknowledgement is recorded
// only when the core says so, and the screen then shows what the core now says (the lists it reads
// again), never what the app expected. A refusal reads in the core's own words. A post that fails
// keeps its words in the box. Nothing is stored and nothing is queued: messages send while online.
import type { TemplateResult } from "lit";
import {
  MessagesApi,
  type PaymentRuleId,
  type AttachmentItem,
  type ChannelDirectoryItem,
  type MembershipRuleItem,
  type MemberByHandItem,
  type EventClassItem,
  type RepAssignmentItem,
  type NotificationSettings,
  type SearchResults,
} from "./messages-api.js";
import type { MessagingNotifyPrefs } from "@hardscapehouse/api-client";
import { LiveFeed, type LiveState, type NudgeKind, type StreamSource, type Timers } from "./live.js";
import {
  ackProgressLabel,
  bubbleFor,
  channelTitle,
  composerState,
  isAckable,
  isHouseChannel,
  isSuperseded,
  isAdminCaller,
  mergeOlder,
  refusalWords,
  streamOrder,
  timeWords,
  totalUnread,
  unreadSignature,
  youAcked,
  groupConversationSubtitle,
  dmConversationPrompt,
  filterRoster,
  type AckProgress,
  type CallerInfo,
  type Channel,
  type HouseConversation,
  type Message,
  type NotifyMode,
  type StaffMember,
  type ReactionItem,
} from "./messages-logic.js";
import { renderDesk, type AckView, type BubbleModel, type ConvoModel, type Model, type PrefsModel, type QueueModel, type RulesModel } from "./views.js";

export interface DeskHost {
  /** Asks the root to repaint. */
  readonly repaint: () => void;
  /** A control or field the screen carries, found by a selector in the root's own shadow root. */
  readonly find: (selector: string) => Element | null;
  /** The clock, so a test pins the time words. */
  readonly now: () => number;
  /** Opens the server sent event stream at a path. */
  readonly openStream: (url: string) => StreamSource;
  readonly timers: Timers;
}

const PAGE = 30;
const EXCERPT = 80;
// Progress is read for the newest forced acknowledgements in the open room; a long history of old
// notices would otherwise be a read per row on every refresh.
const PROGRESS_LIMIT = 20;

export class MessagesDesk {
  readonly #api: MessagesApi;
  readonly #host: DeskHost;
  readonly #meId: string;
  readonly #live: LiveFeed;
  readonly #caller: CallerInfo | null;
  readonly #isAdmin: boolean;

  #phase: Model["phase"] = "loading";
  #loadError = "";
  #error = "";
  #notice = "";
  #channels: readonly Channel[] = [];
  #activeId = "";
  #view: Model["view"] = "list";
  #mainView: Model["mainView"] = "conversation";
  #liveState: LiveState | null = null;
  #newMenuOpen = false;
  #openThreadId: string | null = null;
  #threadSending = false;
  #activeTab: "messages" | "files" = "messages";

  // The open channel.
  #loadingChannel = false;
  #held: readonly Message[] = [];
  #nextCursor: string | null = null;
  #olderLoaded = false;
  #olderBusy = false;
  #threads = new Map<string, readonly Message[]>();
  #replyTo: { rootId: string; excerpt: string } | null = null;
  #sending = false;
  // The draft as the composer last saw it, so the words under the box follow every keystroke.
  #draft = "";
  // Each channel keeps its own words, so a draft never travels to another room.
  readonly #drafts = new Map<string, string>();
  #seededFor = "";

  // Acknowledgement: what the core lists as waiting on the caller, its arithmetic per message, and
  // the one write in flight with its refusal. Nothing is shown as acknowledged before the core says.
  #unacked: readonly Message[] = [];
  // Ids the core has asked this caller to acknowledge, kept after the ack: the message pages do not
  // carry the flag, so the ack line on the home message is remembered here.
  readonly #asked = new Set<string>();
  readonly #progress = new Map<string, AckProgress>();
  #ackBusy = "";
  #ackError: { id: string; text: string } | null = null;

  // The house queue of the open room, when it is a house room.
  #queue: readonly HouseConversation[] = [];
  #claimBusy = "";
  #queueNote = "";
  // A refusal of the person's own claim stays until their next action; a failed read clears when a read succeeds.
  #queueError = "";
  #queueReadError = "";

  // The notification sheet of the open room and the caller's payment notice rules. Each control
  // moves only to what the core answers.
  #prefsOpen = false;
  #prefsLoading = false;
  #prefs: MessagingNotifyPrefs | null = null;
  #prefsBusy = false;
  #prefsError = "";
  #rulesOpen = false;
  #rulesLoading = false;
  #rules: Readonly<Record<string, boolean>> | null = null;
  #rulesBusy = "";
  #rulesError = "";

  // C2 Modals & Roster
  #newMsgModalOpen = false;
  #roster: readonly StaffMember[] = [];
  readonly #selectedRosterIds = new Set<string>();
  #newGroupName = "";
  #rosterFilter = "";

  #newChannelModalOpen = false;
  #newChannelName = "";
  #newChannelTopic = "";
  #newChannelVisibility: "PUBLIC" | "PRIVATE" = "PUBLIC";

  // C6 Attach Modal
  #attachModalOpen = false;

  // C3 Reactions, Seen counts, Mentions, Saved, Search
  readonly #reactions = new Map<string, readonly ReactionItem[]>();
  #activeReactPickerMsgId: string | null = null;
  #editingMessageId: string | null = null;
  readonly #seenCounts = new Map<string, number>();
  #mentionsList: readonly { readonly message: Message; readonly channel_id: string; readonly channel_name: string }[] = [];
  #savedList: readonly { readonly message: Message; readonly channel_id: string; readonly channel_name: string; readonly saved_at: string }[] = [];
  #searchQuery = "";
  #searchResults: SearchResults | null = null;

  // C4 & C5 Settings & Notification Opt-in
  #settingsSubtab: "membership-rules" | "event-classes" | "rep-assignments" = "membership-rules";
  #membershipRules: readonly MembershipRuleItem[] = [];
  #membersByHand: readonly MemberByHandItem[] = [];
  #eventClasses: readonly EventClassItem[] = [];
  #repAssignments: readonly RepAssignmentItem[] = [];
  #notifySettings: NotificationSettings | null = null;
  #channelDirectory: readonly ChannelDirectoryItem[] = [];

  // C6 Attachments per channel
  readonly #attachments = new Map<string, readonly AttachmentItem[]>();

  // When the last read failed, so the screen can say it is showing what it read before.
  #staleAt: number | null = null;

  #unreadSignature: string | null = null;
  #refreshing = false;
  #refreshAgain = false;
  #refreshFull = false;
  #stopped = false;

  constructor(api: MessagesApi, host: DeskHost, meId: string, caller?: CallerInfo | null) {
    this.#api = api;
    this.#host = host;
    this.#meId = meId;
    this.#caller = caller ?? null;
    this.#isAdmin = isAdminCaller(this.#caller);
    this.#live = new LiveFeed({
      mintTicket: () => this.#api.streamTicket(),
      open: (url) => this.#host.openStream(url),
      timers: this.#host.timers,
      onState: (state) => this.#onLiveState(state),
      onNudge: (kind) => this.#onNudge(kind),
    });
  }

  // ---- loads ------------------------------------------------------------------------------------

  /** The first read of the desk: the caller's channels, then the live feed. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const channels = await this.#api.channels();
    if (this.#stopped) return;
    if (!channels.ok) {
      this.#phase = "error";
      this.#loadError = refusalWords("The channel list", channels.error, channels.status ?? null);
      this.#host.repaint();
      return;
    }
    this.#channels = channels.value;
    this.#phase = "ready";
    this.#host.repaint();
    this.#live.start();
    await this.#readAcks();
    this.#host.repaint();
  }

  /** Ends the live feed; the root calls it when the desk is dropped. */
  dispose(): void {
    this.#stopped = true;
    this.#live.stop();
  }

  #onLiveState(state: LiveState): void {
    const wasLive = this.#liveState === "live";
    this.#liveState = state;
    this.#host.repaint();
    // A stream that comes back may have missed events: read again once it is live.
    if (state === "live" && !wasLive) void this.refresh();
  }

  #onNudge(kind: NudgeKind): void {
    // The timer's nudge is the cheap read; the stream's says something changed.
    void this.refresh(kind === "poll" ? "poll" : "full");
  }

  /**
   * Reads again what the screen shows: the channel list with its unread counts and, when a channel
   * is open, its newest page. Reads that arrive while one runs are folded into one more pass.
   */
  async refresh(mode: "poll" | "full" = "full"): Promise<void> {
    if (this.#refreshing) {
      this.#refreshAgain = true;
      if (mode === "full") this.#refreshFull = true;
      return;
    }
    this.#refreshing = true;
    let pass: "poll" | "full" = mode;
    try {
      do {
        this.#refreshAgain = false;
        this.#refreshFull = false;
        await this.#readOnce(pass);
        pass = this.#refreshFull ? "full" : "poll";
      } while (this.#refreshAgain && !this.#stopped);
    } finally {
      this.#refreshing = false;
    }
  }

  /**
   * The timer's read goes to the core's unread summary, built for exactly that (it does not resync
   * membership), and reads the lists again only when the summary moved or the last read failed. What
   * the unread summary does not show (who else acknowledged, who claimed) is read on every pass.
   */
  async #pollOnce(): Promise<boolean> {
    const unread = await this.#api.unread();
    if (this.#stopped) return false;
    if (!unread.ok) {
      if (this.#staleAt === null) this.#staleAt = this.#host.now();
      this.#host.repaint();
      return false;
    }
    const signature = unreadSignature(unread.value);
    const moved = signature !== this.#unreadSignature || this.#staleAt !== null;
    this.#unreadSignature = signature;
    if (!moved) {
      await this.#readAcks();
      await this.#readQueue();
      this.#host.repaint();
    }
    return moved;
  }

  async #readOnce(mode: "poll" | "full"): Promise<void> {
    if (mode === "poll" && !(await this.#pollOnce())) return;
    const channels = await this.#api.channels();
    if (this.#stopped) return;
    if (channels.ok) {
      this.#channels = channels.value;
      this.#staleAt = null;
    } else if (this.#staleAt === null) {
      this.#staleAt = this.#host.now();
    }
    const active = this.#activeId;
    if (active !== "" && this.#view === "conversation") {
      const page = await this.#api.messages(active, null, PAGE);
      if (this.#stopped || active !== this.#activeId) return;
      if (page.ok) {
        this.#held = mergeNewest(this.#held, page.value.messages);
        if (!this.#olderLoaded) this.#nextCursor = page.value.nextCursor;
        await this.#refreshOpenThreads(active);
        await this.#loadAuxData(active, this.#held);
      }
      await this.#readAcks();
      await this.#readQueue();
      // The channel is open on screen: what arrived is read, so the core is told.
      const current = this.#channels.find((c) => c.id === active);
      if (current !== undefined && current.unread_count > 0) await this.#markRead(active);
    } else {
      await this.#readAcks();
    }
    this.#host.repaint();
  }

  // The acknowledgements the caller owes, and the arithmetic for the one on the bubble and for the
  // forced acknowledgements in the open room.
  async #readAcks(): Promise<void> {
    const waiting = await this.#api.unacked();
    if (this.#stopped) return;
    if (waiting.ok) {
      this.#unacked = waiting.value;
      for (const m of waiting.value) this.#asked.add(m.id);
    }
    const ask = new Map<string, string>();
    const bubble = bubbleFor(this.#unacked);
    if (bubble !== null) ask.set(bubble.id, bubble.channel_id);
    const channel = this.#channels.find((c) => c.id === this.#activeId);
    if (channel !== undefined) {
      const rows = this.#held.filter((m) => this.#asked.has(m.id) || isAckable(m));
      for (const m of rows.sort((a, b) => (a.created_at < b.created_at ? 1 : -1)).slice(0, PROGRESS_LIMIT)) ask.set(m.id, m.channel_id);
    }
    await Promise.all(
      [...ask.entries()].map(async ([id, channelId]) => {
        const progress = await this.#api.ackProgress(channelId, id);
        if (progress.ok) this.#progress.set(id, progress.value);
      }),
    );
  }

  /** The house queue, read again from the core; a claim is shown only as the core now reports it. */
  async #readQueue(): Promise<void> {
    const channel = this.#channels.find((c) => c.id === this.#activeId);
    if (channel === undefined || !isHouseChannel(channel)) return;
    const rows = await this.#api.houseQueue(channel.id);
    if (this.#stopped || channel.id !== this.#activeId) return;
    if (rows.ok) {
      this.#queue = rows.value;
      this.#queueReadError = "";
    } else {
      this.#queueReadError = refusalWords("The queue", rows.error, rows.status ?? null);
    }
  }

  async #claim(conversationId: string): Promise<void> {
    if (this.#claimBusy !== "") return;
    const row = this.#queue.find((r) => r.id === conversationId);
    this.#claimBusy = conversationId;
    this.#queueNote = "";
    this.#queueError = "";
    this.#host.repaint();
    const done = await this.#api.claim(conversationId);
    this.#claimBusy = "";
    if (this.#stopped) return;
    // A lost race is the core's sentence; either way the queue is read again, so the row shows who
    // owns it now.
    if (done.ok) this.#queueNote = `You now own ${row?.customer_name || "this customer"}. Their next message goes straight to your DM.`;
    else this.#queueError = refusalWords("The claim", done.error, done.status ?? null);
    await this.#readQueue();
    await this.#readOnce("full");
    this.#host.repaint();
  }

  async #openPrefs(): Promise<void> {
    const channelId = this.#activeId;
    this.#prefsOpen = true;
    this.#prefsLoading = true;
    this.#prefs = null;
    this.#prefsError = "";
    this.#host.repaint();
    const prefs = await this.#api.notifyPrefs(channelId);
    if (this.#stopped || channelId !== this.#activeId) return;
    this.#prefsLoading = false;
    if (prefs.ok) this.#prefs = prefs.value;
    else this.#prefsError = refusalWords("Your notification choices", prefs.error, prefs.status ?? null);
    this.#host.repaint();
  }

  async #writePrefs(patch: { mode?: NotifyMode; desktop_enabled?: boolean; escalation_override?: boolean }): Promise<void> {
    const channelId = this.#activeId;
    if (this.#prefsBusy || this.#prefs === null) return;
    this.#prefsBusy = true;
    this.#prefsError = "";
    this.#host.repaint();
    const done = await this.#api.setNotifyPrefs(channelId, patch);
    this.#prefsBusy = false;
    if (this.#stopped || channelId !== this.#activeId) return;
    if (done.ok) this.#prefs = done.value;
    else this.#prefsError = refusalWords("Your choice", done.error, done.status ?? null);
    this.#host.repaint();
    if (done.ok) void this.refresh();
  }

  async #openRules(): Promise<void> {
    this.#rulesOpen = true;
    this.#view = "conversation";
    this.#rulesLoading = true;
    this.#rules = null;
    this.#rulesError = "";
    this.#host.repaint();
    const rules = await this.#api.paymentRules();
    if (this.#stopped) return;
    this.#rulesLoading = false;
    if (rules.ok) this.#rules = rules.value.rules;
    else this.#rulesError = refusalWords("Your payment notice rules", rules.error, rules.status ?? null);
    this.#host.repaint();
  }

  async #toggleRule(rule: string): Promise<void> {
    const current = this.#rules;
    if (this.#rulesBusy !== "" || current === null) return;
    this.#rulesBusy = rule;
    this.#rulesError = "";
    this.#host.repaint();
    const done = await this.#api.setPaymentRule(rule as PaymentRuleId, current[rule] !== true);
    this.#rulesBusy = "";
    if (this.#stopped) return;
    if (done.ok) this.#rules = done.value.rules;
    else this.#rulesError = refusalWords("Your choice", done.error, done.status ?? null);
    this.#host.repaint();
  }

  async #ack(channelId: string, messageId: string): Promise<void> {
    if (this.#ackBusy !== "") return;
    this.#ackBusy = messageId;
    this.#ackError = null;
    this.#host.repaint();
    const done = await this.#api.ack(channelId, messageId);
    this.#ackBusy = "";
    if (this.#stopped) return;
    if (!done.ok) {
      // The bubble stays: a write that did not go through is not an acknowledgement.
      this.#ackError = { id: messageId, text: refusalWords("The acknowledgement", done.error, done.status ?? null) };
      this.#host.repaint();
      return;
    }
    await this.#readAcks();
    this.#host.repaint();
  }

  async #refreshOpenThreads(channelId: string): Promise<void> {
    for (const rootId of [...this.#threads.keys()]) {
      const replies = await this.#api.thread(channelId, rootId);
      if (replies.ok && channelId === this.#activeId) this.#threads.set(rootId, replies.value);
    }
  }

  async #markRead(channelId: string): Promise<void> {
    const marked = await this.#api.markRead(channelId);
    if (!marked.ok) {
      this.#error = refusalWords("Marking the channel read", marked.error, marked.status ?? null);
      return;
    }
    const channels = await this.#api.channels();
    if (channels.ok && !this.#stopped) this.#channels = channels.value;
  }

  // ---- C2-C6 auxiliary data loads ----------------------------------------------------------------

  async #loadAuxData(channelId: string, messages: readonly Message[]): Promise<void> {
    const [attRes, reactRes] = await Promise.all([
      this.#api.attachments(channelId),
      this.#api.channelReactions(channelId),
    ]);
    if (this.#stopped || channelId !== this.#activeId) return;
    if (attRes.ok) this.#attachments.set(channelId, attRes.value.attachments);
    if (reactRes.ok) {
      for (const [mid, rList] of Object.entries(reactRes.value.reactions)) {
        this.#reactions.set(mid, rList);
      }
    }
    // Read seen counts for top level messages
    await Promise.all(
      messages.map(async (m) => {
        const seen = await this.#api.seenBy(channelId, m.id);
        if (this.#stopped || channelId !== this.#activeId) return;
        if (seen.ok) this.#seenCounts.set(m.id, seen.value.count);
      }),
    );
  }

  async #loadAttachments(channelId: string): Promise<void> {
    const res = await this.#api.attachments(channelId);
    if (this.#stopped || channelId !== this.#activeId) return;
    if (res.ok) {
      this.#attachments.set(channelId, res.value.attachments);
      this.#host.repaint();
    }
  }

  async #loadRoster(): Promise<void> {
    if (this.#roster.length > 0) return;
    const res = await this.#api.roster();
    if (this.#stopped) return;
    if (res.ok) {
      this.#roster = res.value.roster;
      this.#host.repaint();
    }
  }

  async #loadMentions(): Promise<void> {
    const res = await this.#api.mentions();
    if (this.#stopped) return;
    if (res.ok) {
      this.#mentionsList = res.value.mentions;
      this.#host.repaint();
    }
  }

  async #loadSaved(): Promise<void> {
    const res = await this.#api.saved();
    if (this.#stopped) return;
    if (res.ok) {
      this.#savedList = res.value.saved;
      this.#host.repaint();
    }
  }

  async #performSearch(query: string): Promise<void> {
    const res = await this.#api.search(query);
    if (this.#stopped) return;
    if (res.ok) {
      this.#searchResults = res.value;
    } else {
      this.#searchResults = { query, messages: [], channels: [], people: [], files: [] };
    }
    this.#host.repaint();
  }

  async #loadSettings(): Promise<void> {
    const [rules, byHand, events, reps] = await Promise.all([
      this.#api.membershipRules(),
      this.#api.membersByHand(),
      this.#api.eventClasses(),
      this.#api.repAssignments(),
    ]);
    if (this.#stopped) return;
    if (rules.ok) this.#membershipRules = rules.value.rules;
    if (byHand.ok) this.#membersByHand = byHand.value.members;
    if (events.ok) this.#eventClasses = events.value.classes;
    if (reps.ok) this.#repAssignments = reps.value.assignments;
    this.#host.repaint();
  }

  async #loadNotifyOptIn(): Promise<void> {
    const [settings, directory] = await Promise.all([
      this.#api.notificationSettings(),
      this.#api.channelDirectory(),
    ]);
    if (this.#stopped) return;
    if (settings.ok) this.#notifySettings = settings.value;
    if (directory.ok) this.#channelDirectory = directory.value.channels;
    this.#host.repaint();
  }

  // ---- opening a channel ------------------------------------------------------------------------

  async #open(channelId: string): Promise<void> {
    const leaving = this.#field();
    if (leaving !== null && this.#activeId !== "") this.#drafts.set(this.#activeId, leaving.value);
    this.#activeId = channelId;
    this.#view = "conversation";
    this.#mainView = "conversation";
    this.#activeTab = "messages";
    this.#activeReactPickerMsgId = null;
    this.#editingMessageId = null;
    this.#error = "";
    this.#notice = "";
    this.#held = [];
    this.#nextCursor = null;
    this.#olderLoaded = false;
    this.#threads = new Map();
    this.#replyTo = null;
    this.#queue = [];
    this.#queueNote = "";
    this.#queueError = "";
    this.#queueReadError = "";
    this.#prefsOpen = false;
    this.#prefs = null;
    this.#prefsError = "";
    this.#rulesOpen = false;
    this.#draft = this.#drafts.get(channelId) ?? "";
    this.#loadingChannel = true;
    this.#host.repaint();
    const page = await this.#api.messages(channelId, null, PAGE);
    if (this.#stopped || channelId !== this.#activeId) return;
    this.#loadingChannel = false;
    if (!page.ok) {
      this.#error = refusalWords("The channel", page.error, page.status ?? null);
      this.#host.repaint();
      return;
    }
    this.#held = page.value.messages;
    this.#nextCursor = page.value.nextCursor;
    this.#host.repaint();
    await this.#markRead(channelId);
    await this.#readAcks();
    await this.#readQueue();
    await this.#loadAuxData(channelId, this.#held);
    this.#host.repaint();
  }

  async #loadOlder(): Promise<void> {
    const channelId = this.#activeId;
    if (this.#nextCursor === null || this.#olderBusy) return;
    this.#olderBusy = true;
    this.#host.repaint();
    const page = await this.#api.messages(channelId, this.#nextCursor, PAGE);
    this.#olderBusy = false;
    if (this.#stopped || channelId !== this.#activeId) return;
    if (!page.ok) {
      this.#error = refusalWords("The earlier messages", page.error, page.status ?? null);
    } else {
      this.#held = mergeOlder(this.#held, page.value.messages);
      this.#nextCursor = page.value.nextCursor;
      this.#olderLoaded = true;
      await this.#loadAuxData(channelId, page.value.messages);
    }
    this.#host.repaint();
  }

  async #toggleThread(rootId: string): Promise<void> {
    if (this.#threads.has(rootId)) {
      this.#threads.delete(rootId);
      this.#host.repaint();
      return;
    }
    const channelId = this.#activeId;
    const replies = await this.#api.thread(channelId, rootId);
    if (this.#stopped || channelId !== this.#activeId) return;
    if (!replies.ok) {
      this.#error = refusalWords("The thread", replies.error, replies.status ?? null);
    } else {
      this.#threads.set(rootId, replies.value);
    }
    this.#host.repaint();
  }

  async #openThread(rootId: string): Promise<void> {
    const channelId = this.#activeId;
    this.#openThreadId = rootId;
    this.#host.repaint();
    const replies = await this.#api.thread(channelId, rootId);
    if (this.#stopped || channelId !== this.#activeId) return;
    if (replies.ok) {
      this.#threads.set(rootId, replies.value);
    }
    this.#host.repaint();
  }

  async #threadSend(): Promise<void> {
    if (this.#openThreadId === null || this.#threadSending) return;
    const rootId = this.#openThreadId;
    const channelId = this.#activeId;
    const el = this.#host.find("textarea[data-field='thread-draft']");
    const draftEl = el instanceof HTMLTextAreaElement ? el : null;
    const text = draftEl?.value.trim() ?? "";
    if (!text) return;
    this.#threadSending = true;
    this.#host.repaint();
    const posted = await this.#api.post(channelId, text, rootId);
    this.#threadSending = false;
    if (this.#stopped) return;
    if (!posted.ok) {
      this.#error = refusalWords("The reply", posted.error, posted.status ?? null);
      this.#host.repaint();
      return;
    }
    if (draftEl) draftEl.value = "";
    const replies = await this.#api.thread(channelId, rootId);
    if (replies.ok) {
      this.#threads.set(rootId, replies.value);
      this.#held = this.#held.map((m) => (m.id === rootId ? { ...m, reply_count: replies.value.length } : m));
    }
    this.#host.repaint();
  }

  // ---- posting ----------------------------------------------------------------------------------

  #field(): HTMLTextAreaElement | null {
    const el = this.#host.find("textarea[data-field='draft']");
    return el instanceof HTMLTextAreaElement ? el : null;
  }

  /** After a render: a channel just opened gets the words it was left with. */
  seed(): void {
    if (this.#seededFor === this.#activeId) return;
    const field = this.#field();
    if (field === null) return;
    field.value = this.#drafts.get(this.#activeId) ?? "";
    this.#draft = field.value;
    this.#seededFor = this.#activeId;
  }

  /** The composer's or search text changed: the words under the box and the view follow it. */
  typed(target?: HTMLInputElement | HTMLTextAreaElement): void {
    if (target?.getAttribute("data-field") === "search-input" || target?.classList.contains("search-input")) {
      this.#searchQuery = target.value;
      if (this.#searchQuery.trim().length > 0) {
        this.#mainView = "search";
        this.#view = "conversation";
        void this.#performSearch(this.#searchQuery.trim());
      } else {
        this.#searchResults = null;
        if (this.#mainView === "search") {
          this.#mainView = "conversation";
        }
        this.#host.repaint();
      }
      return;
    }
    if (target?.getAttribute("data-field") === "roster-search") {
      this.#rosterFilter = target.value;
      this.#host.repaint();
      return;
    }
    if (target?.getAttribute("data-field") === "edit-body") {
      return;
    }
    if (target?.getAttribute("data-field") === "group-name") {
      this.#newGroupName = target.value;
      return;
    }
    if (target?.getAttribute("data-field") === "channel-name") {
      this.#newChannelName = target.value;
      return;
    }
    if (target?.getAttribute("data-field") === "channel-purpose") {
      this.#newChannelTopic = target.value;
      return;
    }
    const field = this.#field();
    this.#draft = field?.value ?? "";
    this.#host.repaint();
  }

  async #send(): Promise<void> {
    const field = this.#field();
    if (field === null || this.#sending) return;
    const state = composerState(field.value);
    if (!state.canSend) return;
    const channelId = this.#activeId;
    const reply = this.#replyTo;
    this.#sending = true;
    this.#error = "";
    this.#host.repaint();
    const posted = await this.#api.post(channelId, field.value.trim(), reply === null ? null : reply.rootId);
    this.#sending = false;
    if (this.#stopped) return;
    if (!posted.ok) {
      // The words stay in the box: nothing was sent and nothing is queued.
      this.#error = refusalWords("The post", posted.error, posted.status ?? null);
      this.#host.repaint();
      return;
    }
    // The sent words are no one's draft now. The box and the reply belong to the room that is open: if
    // the person moved on while the post was in flight, the box holds the other room's words.
    this.#drafts.delete(channelId);
    if (channelId === this.#activeId) {
      field.value = "";
      this.#draft = "";
      this.#replyTo = null;
      if (reply === null) {
        this.#held = mergeNewest(this.#held, [posted.value]);
        this.#reactions.set(posted.value.id, []);
        this.#seenCounts.set(posted.value.id, 0);
      } else {
        const replies = await this.#api.thread(channelId, reply.rootId);
        if (replies.ok) {
          this.#threads.set(reply.rootId, replies.value);
          this.#held = this.#held.map((m) => (m.id === reply.rootId ? { ...m, reply_count: replies.value.length } : m));
        }
      }
    }
    this.#host.repaint();
    void this.refresh();
  }

  // ---- C2-C6 action handlers --------------------------------------------------------------------

  async #submitNewChannel(): Promise<void> {
    const nameInput = this.#host.find("input[data-field='channel-name']") as HTMLInputElement | null;
    const topicInput = this.#host.find("input[data-field='channel-purpose']") as HTMLInputElement | null;
    const name = nameInput?.value.trim() || this.#newChannelName;
    const topic = topicInput?.value.trim() || this.#newChannelTopic;
    if (!name) return;
    const res = await this.#api.createChannel({ name, topic, visibility: this.#newChannelVisibility });
    if (this.#stopped) return;
    if (res.ok) {
      this.#newChannelModalOpen = false;
      this.#newChannelName = "";
      this.#newChannelTopic = "";
      await this.refresh();
      await this.#open(res.value.id);
    } else {
      this.#error = refusalWords("The channel creation", res.error, res.status ?? null);
      this.#host.repaint();
    }
  }

  async #startConversationFromModal(): Promise<void> {
    const count = this.#selectedRosterIds.size;
    if (count === 0) return;
    const groupInput = this.#host.find("input[data-field='group-name']") as HTMLInputElement | null;
    const groupName = groupInput?.value.trim() || this.#newGroupName;
    if (count === 1) {
      const targetUid = [...this.#selectedRosterIds][0];
      const res = await this.#api.createDM(targetUid);
      if (this.#stopped) return;
      if (res.ok) {
        this.#newMsgModalOpen = false;
        await this.refresh();
        await this.#open(res.value.id);
      } else {
        this.#error = refusalWords("Starting the message", res.error, res.status ?? null);
        this.#host.repaint();
      }
    } else {
      const memberIds = [...this.#selectedRosterIds];
      const res = await this.#api.createGroup(groupName || "Group direct message", memberIds);
      if (this.#stopped) return;
      if (res.ok) {
        this.#newMsgModalOpen = false;
        await this.refresh();
        await this.#open(res.value.id);
      } else {
        this.#error = refusalWords("Starting the group", res.error, res.status ?? null);
        this.#host.repaint();
      }
    }
  }

  async #startDmWith(targetUid: string): Promise<void> {
    if (!targetUid) return;
    const res = await this.#api.createDM(targetUid);
    if (this.#stopped) return;
    if (res.ok) {
      this.#mainView = "conversation";
      await this.refresh();
      await this.#open(res.value.id);
    }
  }

  async #toggleReaction(channelId: string, msgId: string, emoji: string): Promise<void> {
    if (!channelId || !msgId || !emoji) return;
    const res = await this.#api.toggleReaction(channelId, msgId, emoji);
    if (this.#stopped) return;
    if (res.ok) {
      this.#reactions.set(msgId, res.value.reactions);
      this.#host.repaint();
    }
  }

  async #saveEdit(channelId: string, msgId: string, body: string): Promise<void> {
    if (!channelId || !msgId || !body) return;
    const res = await this.#api.editMessage(channelId, msgId, body);
    if (this.#stopped) return;
    if (res.ok) {
      this.#held = this.#held.map((m) =>
        m.id === msgId ? { ...m, body: res.value.body, edited_at: (res.value as any).edited_at ?? new Date().toISOString() } : m
      );
      this.#editingMessageId = null;
      this.#host.repaint();
    } else {
      this.#error = refusalWords("The edit", res.error, res.status ?? null);
      this.#host.repaint();
    }
  }

  async #deleteMsg(channelId: string, msgId: string): Promise<void> {
    if (!channelId || !msgId) return;
    const res = await this.#api.deleteMessage(channelId, msgId);
    if (this.#stopped) return;
    if (res.ok) {
      this.#held = this.#held.map((m) =>
        m.id === msgId ? { ...m, body: "This message was deleted", deleted_at: (res.value as any).deleted_at ?? new Date().toISOString() } : m
      );
      this.#host.repaint();
    } else {
      this.#error = refusalWords("The deletion", res.error, res.status ?? null);
      this.#host.repaint();
    }
  }

  async #saveMsg(msgId: string): Promise<void> {
    if (!msgId) return;
    await this.#api.saveMessage(msgId);
    this.#notice = "Message saved";
    this.#host.repaint();
  }

  async #unsaveMsg(msgId: string): Promise<void> {
    if (!msgId) return;
    await this.#api.unsaveMessage(msgId);
    await this.#loadSaved();
  }

  async #removeMemberByHand(channelId: string, userId: string): Promise<void> {
    if (!channelId || !userId) return;
    await this.#api.removeMember(channelId, userId);
    await this.#loadSettings();
  }

  async #unassignRep(repId: string): Promise<void> {
    if (!repId) return;
    await this.#api.unassignRep(repId);
    await this.#loadSettings();
  }

  async #joinChannel(channelId: string): Promise<void> {
    if (!channelId) return;
    await this.#api.joinChannel(channelId);
    await Promise.all([this.refresh(), this.#loadNotifyOptIn()]);
  }

  async #leaveChannel(channelId: string): Promise<void> {
    if (!channelId) return;
    await this.#api.leaveChannel(channelId);
    await Promise.all([this.refresh(), this.#loadNotifyOptIn()]);
  }

  async #toggleEventNotif(eventTopic: string, channel: string, isChecked: boolean): Promise<void> {
    if (!eventTopic) return;
    const cur = this.#notifySettings?.event_classes?.[eventTopic] ?? { in_app: true, push: false };
    const updated = { ...cur, [channel]: isChecked };
    await this.#api.updateEventClassNotif(eventTopic, updated);
    await this.#loadNotifyOptIn();
  }

  async #toggleQuietHours(): Promise<void> {
    const cur = this.#notifySettings?.quiet_hours?.enabled ?? false;
    await this.#api.updateQuietHours({ enabled: !cur });
    await this.#loadNotifyOptIn();
  }

  async #submitAttachment(): Promise<void> {
    const filenameInput = this.#host.find("input[data-field='attach-filename']") as HTMLInputElement | null;
    const captionInput = this.#host.find("input[data-field='attach-caption']") as HTMLInputElement | null;
    const filename = filenameInput?.value.trim() || "attachment.pdf";
    const caption = captionInput?.value.trim() || "";
    this.#attachModalOpen = false;
    this.#host.repaint();
    if (this.#activeId) {
      await this.#api.uploadAttachment(this.#activeId, filename, caption);
      await this.#loadAttachments(this.#activeId);
    }
  }

  // ---- presses ----------------------------------------------------------------------------------

  /** Acts on a control carrying a data-action. True when the desk handled it. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const id = control.getAttribute("data-id") ?? "";
    switch (action) {
      case "toggle-new-menu":
        this.#newMenuOpen = !this.#newMenuOpen;
        this.#host.repaint();
        return true;
      case "new-message":
      case "new-dm":
        this.#newMenuOpen = false;
        this.#newMsgModalOpen = true;
        this.#selectedRosterIds.clear();
        this.#newGroupName = "";
        this.#rosterFilter = "";
        void this.#loadRoster();
        this.#host.repaint();
        return true;
      case "new-channel":
      case "add-channel":
        this.#newMenuOpen = false;
        if (!this.#isAdmin) return true;
        this.#newChannelModalOpen = true;
        this.#newChannelName = "";
        this.#newChannelTopic = "";
        this.#newChannelVisibility = "PUBLIC";
        this.#host.repaint();
        return true;
      case "close-modal":
        this.#newMsgModalOpen = false;
        this.#newChannelModalOpen = false;
        this.#attachModalOpen = false;
        this.#host.repaint();
        return true;
      case "toggle-person": {
        const personId = control.getAttribute("data-person-id") ?? id;
        if (personId) {
          if (this.#selectedRosterIds.has(personId)) {
            this.#selectedRosterIds.delete(personId);
          } else {
            this.#selectedRosterIds.add(personId);
          }
          this.#host.repaint();
        }
        return true;
      }
      case "remove-person": {
        const personId = control.getAttribute("data-id") ?? id;
        if (personId) {
          this.#selectedRosterIds.delete(personId);
          this.#host.repaint();
        }
        return true;
      }
      case "set-channel-visibility": {
        const vis = control.getAttribute("data-vis");
        if (vis === "PRIVATE" || vis === "PUBLIC") {
          this.#newChannelVisibility = vis;
          this.#host.repaint();
        }
        return true;
      }
      case "create-channel-submit":
        void this.#submitNewChannel();
        return true;
      case "start-conversation":
        void this.#startConversationFromModal();
        return true;
      case "start-dm-with":
        void this.#startDmWith(control.getAttribute("data-id") ?? id);
        return true;
      case "open-add-person":
        this.#newMsgModalOpen = true;
        this.#selectedRosterIds.clear();
        this.#newGroupName = "";
        this.#rosterFilter = "";
        void this.#loadRoster();
        this.#host.repaint();
        return true;
      case "open-channel":
        this.#mainView = "conversation";
        void this.#open(id);
        return true;
      case "back":
        if (this.#mainView !== "conversation") {
          this.#mainView = "conversation";
        } else {
          this.#view = "list";
        }
        this.#host.repaint();
        return true;
      case "load-older":
        void this.#loadOlder();
        return true;
      case "open-thread":
        void this.#openThread(id);
        return true;
      case "close-thread":
        this.#openThreadId = null;
        this.#host.repaint();
        return true;
      case "toggle-thread":
        void this.#toggleThread(id);
        return true;
      case "reply": {
        const root = this.#held.find((m) => m.id === id);
        const text = (root?.body ?? "").replace(/\s+/g, " ");
        this.#replyTo = { rootId: id, excerpt: text.length > EXCERPT ? `${text.slice(0, EXCERPT)}...` : text };
        this.#host.repaint();
        this.#field()?.focus();
        return true;
      }
      case "cancel-reply":
        this.#replyTo = null;
        this.#host.repaint();
        return true;
      case "quick-reply": {
        const replyText = control.getAttribute("data-text") ?? "";
        const field = this.#field();
        if (field) {
          field.value = replyText;
          this.#draft = replyText;
          field.focus();
          this.#host.repaint();
        }
        return true;
      }
      case "select-tab": {
        const tab = control.getAttribute("data-tab");
        if (tab === "messages" || tab === "files") {
          this.#activeTab = tab;
          if (tab === "files" && this.#activeId) {
            void this.#loadAttachments(this.#activeId);
          }
          this.#host.repaint();
        }
        return true;
      }
      case "thread-send":
        void this.#threadSend();
        return true;
      case "open-react-picker": {
        const msgId = control.getAttribute("data-id") ?? id;
        this.#activeReactPickerMsgId = this.#activeReactPickerMsgId === msgId ? null : msgId;
        this.#host.repaint();
        return true;
      }
      case "pick-reaction": {
        const emoji = control.getAttribute("data-react-option") ?? control.getAttribute("data-emoji") ?? "";
        const msgId = control.getAttribute("data-id") ?? this.#activeReactPickerMsgId ?? id;
        const channelId = control.getAttribute("data-channel") ?? this.#activeId;
        this.#activeReactPickerMsgId = null;
        void this.#toggleReaction(channelId, msgId, emoji);
        return true;
      }
      case "toggle-reaction": {
        const emoji = control.getAttribute("data-emoji") ?? "";
        const msgId = control.getAttribute("data-id") ?? id;
        const channelId = control.getAttribute("data-channel") ?? this.#activeId;
        void this.#toggleReaction(channelId, msgId, emoji);
        return true;
      }
      case "edit-msg": {
        const msgId = control.getAttribute("data-id") ?? id;
        this.#editingMessageId = msgId;
        this.#host.repaint();
        return true;
      }
      case "cancel-edit":
        this.#editingMessageId = null;
        this.#host.repaint();
        return true;
      case "save-edit": {
        const msgId = control.getAttribute("data-id") ?? id;
        const channelId = control.getAttribute("data-channel") ?? this.#activeId;
        const editField = this.#host.find("textarea[data-field='edit-body']") as HTMLTextAreaElement | null;
        const newBody = editField?.value.trim() ?? "";
        void this.#saveEdit(channelId, msgId, newBody);
        return true;
      }
      case "delete-msg": {
        const msgId = control.getAttribute("data-id") ?? id;
        const channelId = control.getAttribute("data-channel") ?? this.#activeId;
        void this.#deleteMsg(channelId, msgId);
        return true;
      }
      case "toggle-save-msg": {
        const msgId = control.getAttribute("data-id") ?? id;
        void this.#saveMsg(msgId);
        return true;
      }
      case "unsave-msg": {
        const msgId = control.getAttribute("data-id") ?? id;
        void this.#unsaveMsg(msgId);
        return true;
      }
      case "nav-mentions":
        this.#mainView = "mentions";
        this.#view = "conversation";
        void this.#loadMentions();
        this.#host.repaint();
        return true;
      case "nav-saved":
        this.#mainView = "saved";
        this.#view = "conversation";
        void this.#loadSaved();
        this.#host.repaint();
        return true;
      case "open-settings":
        if (this.#isAdmin) {
          this.#mainView = "settings";
          this.#view = "conversation";
          void this.#loadSettings();
          this.#host.repaint();
        }
        return true;
      case "settings-subtab": {
        const subtab = control.getAttribute("data-tab") as any;
        if (subtab === "membership-rules" || subtab === "event-classes" || subtab === "rep-assignments") {
          this.#settingsSubtab = subtab;
          this.#host.repaint();
        }
        return true;
      }
      case "remove-member-by-hand": {
        const userId = control.getAttribute("data-id") ?? "";
        const chId = control.getAttribute("data-channel") ?? "";
        void this.#removeMemberByHand(chId, userId);
        return true;
      }
      case "unassign-rep": {
        const repId = control.getAttribute("data-id") ?? "";
        void this.#unassignRep(repId);
        return true;
      }
      case "open-notify-optin":
        this.#mainView = "notifications-optin";
        this.#view = "conversation";
        void this.#loadNotifyOptIn();
        this.#host.repaint();
        return true;
      case "join-channel": {
        const chId = control.getAttribute("data-id") ?? "";
        void this.#joinChannel(chId);
        return true;
      }
      case "leave-channel": {
        const chId = control.getAttribute("data-id") ?? "";
        void this.#leaveChannel(chId);
        return true;
      }
      case "toggle-event-notif": {
        const eventTopic = control.getAttribute("data-event") ?? "";
        const notifChan = control.getAttribute("data-channel") ?? "in_app";
        const isChecked = control.getAttribute("aria-checked") !== "true";
        void this.#toggleEventNotif(eventTopic, notifChan, isChecked);
        return true;
      }
      case "toggle-quiet-hours":
        void this.#toggleQuietHours();
        return true;
      case "attach":
        this.#attachModalOpen = true;
        this.#host.repaint();
        return true;
      case "attach-submit":
        void this.#submitAttachment();
        return true;
      case "open-file":
        return true;
      case "send":
        void this.#send();
        return true;
      case "open-prefs":
        void this.#openPrefs();
        return true;
      case "close-panel":
        this.#prefsOpen = false;
        if (this.#rulesOpen) {
          this.#rulesOpen = false;
          this.#view = this.#activeId === "" ? "list" : "conversation";
        }
        this.#host.repaint();
        return true;
      case "set-mode":
        void this.#writePrefs({ mode: control.getAttribute("data-mode") as NotifyMode });
        return true;
      case "toggle-pref": {
        const which = control.getAttribute("data-pref");
        if (which === "desktop_enabled") void this.#writePrefs({ desktop_enabled: this.#prefs?.desktop_enabled !== true });
        if (which === "escalation_override") void this.#writePrefs({ escalation_override: this.#prefs?.escalation_override !== true });
        return true;
      }
      case "retry-prefs":
        void this.#openPrefs();
        return true;
      case "retry-rules":
        void this.#openRules();
        return true;
      case "open-rules":
        void this.#openRules();
        return true;
      case "toggle-rule":
        void this.#toggleRule(control.getAttribute("data-rule") ?? "");
        return true;
      case "claim":
        void this.#claim(id);
        return true;
      case "ack":
        void this.#ack(control.getAttribute("data-channel") ?? "", id);
        return true;
      case "retry-live":
        this.#live.retryNow();
        return true;
      case "refresh":
        void this.load();
        return true;
      default:
        return false;
    }
  }

  // ---- the template -----------------------------------------------------------------------------

  #bubble(): BubbleModel | null {
    const message = bubbleFor(this.#unacked);
    if (message === null) return null;
    const channel = this.#channels.find((c) => c.id === message.channel_id);
    const progress = this.#progress.get(message.id);
    return {
      id: message.id,
      channelId: message.channel_id,
      channelName: channel === undefined ? "A channel" : channelTitle(channel),
      body: message.body,
      when: timeWords(message.created_at, this.#host.now()),
      progress: progress === undefined ? "" : ackProgressLabel(progress),
      busy: this.#ackBusy === message.id,
      error: this.#ackError?.id === message.id ? this.#ackError.text : "",
    };
  }

  #ackViews(messages: readonly Message[]): Map<string, AckView> {
    const out = new Map<string, AckView>();
    for (const m of messages) {
      const waiting = this.#unacked.some((u) => u.id === m.id);
      if (!waiting && !this.#asked.has(m.id) && !isAckable(m)) continue;
      const progress = this.#progress.get(m.id);
      out.set(m.id, {
        superseded: isSuperseded(m),
        progress: progress === undefined ? "" : ackProgressLabel(progress),
        youAcked: youAcked(progress, this.#meId),
        needsYou: waiting && !isSuperseded(m),
        busy: this.#ackBusy === m.id,
        error: this.#ackError?.id === m.id ? this.#ackError.text : "",
      });
    }
    return out;
  }

  #convo(): ConvoModel | null {
    const channel = this.#channels.find((c) => c.id === this.#activeId);
    if (channel === undefined) return null;
    const state = composerState(this.#draft);
    const messages = streamOrder(this.#held);
    const prefs: PrefsModel | null = this.#prefsOpen
      ? {
          loading: this.#prefsLoading,
          read: this.#prefs !== null,
          mode: (this.#prefs?.mode ?? "ALL") as NotifyMode,
          inherited: this.#prefs?.inherited === true,
          desktop: this.#prefs?.desktop_enabled !== false,
          escalationOverride: this.#prefs?.escalation_override !== false,
          busy: this.#prefsBusy,
          error: this.#prefsError,
        }
      : null;
    const queue: QueueModel | null = isHouseChannel(channel)
      ? { rows: this.#queue, busyId: this.#claimBusy, note: this.#queueNote, error: this.#queueError || this.#queueReadError }
      : null;
    const openThreadRoot = this.#openThreadId ? this.#held.find((m) => m.id === this.#openThreadId) ?? null : null;
    const openThreadReplies = this.#openThreadId ? (this.#threads.get(this.#openThreadId) ?? []) : [];

    const isGroup = channel.kind === "GROUP";
    const memberCount = (channel as any).members?.length || 3;
    const groupSubtitle = isGroup ? groupConversationSubtitle(memberCount, Boolean(channel.name)) : "";
    const dmPrompt = channel.kind === "DM" ? dmConversationPrompt(channelTitle(channel)).title : "";
    const memberInitials = isGroup ? ["PS", "ER", "TA"] : [];
    const attachments = this.#attachments.get(this.#activeId) ?? [];

    return {
      channel,
      prefs,
      queue,
      loading: this.#loadingChannel,
      messages,
      acks: this.#ackViews(messages),
      hasOlder: this.#nextCursor !== null,
      olderBusy: this.#olderBusy,
      threads: this.#threads,
      replyTo: this.#replyTo,
      composerWords: state.words,
      composerBad: state.over > 0,
      canSend: state.canSend,
      sending: this.#sending,
      openThreadId: this.#openThreadId,
      openThreadRoot,
      openThreadReplies,
      threadSending: this.#threadSending,
      activeTab: this.#activeTab,

      groupSubtitle,
      dmPrompt,
      memberInitials,
      reactions: this.#reactions,
      activeReactPickerMsgId: this.#activeReactPickerMsgId,
      editingMessageId: this.#editingMessageId,
      seenCounts: this.#seenCounts,
      attachments,
    };
  }

  render(): TemplateResult {
    const filteredRoster = filterRoster(this.#roster, this.#rosterFilter, this.#meId);

    const newMsgModal = this.#newMsgModalOpen
      ? {
          open: true,
          roster: filteredRoster,
          selectedIds: this.#selectedRosterIds,
          groupName: this.#newGroupName,
        }
      : null;

    const newChannelModal = this.#newChannelModalOpen
      ? {
          open: true,
          name: this.#newChannelName,
          topic: this.#newChannelTopic,
        }
      : null;

    const attachModal = this.#attachModalOpen
      ? {
          open: true,
        }
      : null;

    const settingsView = this.#mainView === "settings"
      ? {
          subtab: this.#settingsSubtab,
          membershipRules: this.#membershipRules,
          membersByHand: this.#membersByHand,
          eventClasses: this.#eventClasses,
          repAssignments: this.#repAssignments,
        }
      : null;

    const notifyOptInView = this.#mainView === "notifications-optin"
      ? {
          settings: this.#notifySettings,
          directory: this.#channelDirectory,
        }
      : null;

    return renderDesk({
      phase: this.#phase,
      loadError: this.#loadError,
      meId: this.#meId,
      channels: this.#channels,
      activeId: this.#activeId,
      view: this.#view,
      mainView: this.#mainView,
      totalUnread: totalUnread(this.#channels),
      live: this.#liveState,
      stale: this.#staleAt === null ? "" : `The last read failed at ${timeWords(new Date(this.#staleAt).toISOString(), this.#host.now())}. You are looking at what was read before.`,
      now: this.#host.now(),
      error: this.#error,
      notice: this.#notice,
      bubble: this.#bubble(),
      rules: this.#rulesOpen ? { loading: this.#rulesLoading, values: this.#rules, busy: this.#rulesBusy, error: this.#rulesError } : null,
      convo: this.#convo(),
      isAdmin: this.#isAdmin,
      newMenuOpen: this.#newMenuOpen,

      newMsgModal,
      newChannelModal,
      attachModal,
      mentionsList: this.#mentionsList,
      savedList: this.#savedList,
      searchResults: this.#searchResults,
      settingsView,
      notifyOptInView,
    });
  }
}

/** The newest page joins what is held: a message once, the core's copy winning. */
function mergeNewest(held: readonly Message[], fresh: readonly Message[]): Message[] {
  const byId = new Map<string, Message>();
  for (const m of held) byId.set(m.id, m);
  for (const m of fresh) byId.set(m.id, m);
  return [...byId.values()];
}
