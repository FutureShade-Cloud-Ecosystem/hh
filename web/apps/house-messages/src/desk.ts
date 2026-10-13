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
import { MessagesApi } from "./messages-api.js";
import { LiveFeed, type LiveState, type NudgeKind, type StreamSource, type Timers } from "./live.js";
import {
  composerState,
  mergeOlder,
  refusalWords,
  streamOrder,
  totalUnread,
  type Channel,
  type Message,
} from "./messages-logic.js";
import { renderDesk, type ConvoModel, type Model } from "./views.js";

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

export class MessagesDesk {
  readonly #api: MessagesApi;
  readonly #host: DeskHost;
  readonly #meId: string;
  readonly #live: LiveFeed;

  #phase: Model["phase"] = "loading";
  #loadError = "";
  #error = "";
  #notice = "";
  #channels: readonly Channel[] = [];
  #activeId = "";
  #view: Model["view"] = "list";
  #liveState: LiveState | null = null;

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

  #refreshing = false;
  #refreshAgain = false;
  #stopped = false;

  constructor(api: MessagesApi, host: DeskHost, meId: string) {
    this.#api = api;
    this.#host = host;
    this.#meId = meId;
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

  #onNudge(_kind: NudgeKind): void {
    void this.refresh();
  }

  /**
   * Reads again what the screen shows: the channel list with its unread counts and, when a channel
   * is open, its newest page. Reads that arrive while one runs are folded into one more pass.
   */
  async refresh(): Promise<void> {
    if (this.#refreshing) {
      this.#refreshAgain = true;
      return;
    }
    this.#refreshing = true;
    try {
      do {
        this.#refreshAgain = false;
        await this.#readOnce();
      } while (this.#refreshAgain && !this.#stopped);
    } finally {
      this.#refreshing = false;
    }
  }

  async #readOnce(): Promise<void> {
    const channels = await this.#api.channels();
    if (this.#stopped) return;
    if (channels.ok) this.#channels = channels.value;
    const active = this.#activeId;
    if (active !== "" && this.#view === "conversation") {
      const page = await this.#api.messages(active, null, PAGE);
      if (this.#stopped || active !== this.#activeId) return;
      if (page.ok) {
        this.#held = mergeNewest(this.#held, page.value.messages);
        if (!this.#olderLoaded) this.#nextCursor = page.value.nextCursor;
        await this.#refreshOpenThreads(active);
      }
      // The channel is open on screen: what arrived is read, so the core is told.
      const current = this.#channels.find((c) => c.id === active);
      if (current !== undefined && current.unread_count > 0) await this.#markRead(active);
    }
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

  // ---- opening a channel ------------------------------------------------------------------------

  async #open(channelId: string): Promise<void> {
    const leaving = this.#field();
    if (leaving !== null && this.#activeId !== "") this.#drafts.set(this.#activeId, leaving.value);
    this.#activeId = channelId;
    this.#view = "conversation";
    this.#error = "";
    this.#notice = "";
    this.#held = [];
    this.#nextCursor = null;
    this.#olderLoaded = false;
    this.#threads = new Map();
    this.#replyTo = null;
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

  /** The composer's text changed: the words under the box and the Send button follow it. */
  typed(): void {
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
    field.value = "";
    this.#draft = "";
    this.#replyTo = null;
    if (channelId === this.#activeId) {
      if (reply === null) {
        this.#held = mergeNewest(this.#held, [posted.value]);
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

  // ---- presses ----------------------------------------------------------------------------------

  /** Acts on a control carrying a data-action. True when the desk handled it. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const id = control.getAttribute("data-id") ?? "";
    switch (action) {
      case "open-channel":
        void this.#open(id);
        return true;
      case "back":
        this.#view = "list";
        this.#host.repaint();
        return true;
      case "load-older":
        void this.#loadOlder();
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
      case "send":
        void this.#send();
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

  #convo(): ConvoModel | null {
    const channel = this.#channels.find((c) => c.id === this.#activeId);
    if (channel === undefined) return null;
    const state = composerState(this.#draft);
    return {
      channel,
      loading: this.#loadingChannel,
      messages: streamOrder(this.#held),
      hasOlder: this.#nextCursor !== null,
      olderBusy: this.#olderBusy,
      threads: this.#threads,
      replyTo: this.#replyTo,
      composerWords: state.words,
      composerBad: state.over > 0,
      canSend: state.canSend,
      sending: this.#sending,
    };
  }

  render(): TemplateResult {
    return renderDesk({
      phase: this.#phase,
      loadError: this.#loadError,
      meId: this.#meId,
      channels: this.#channels,
      activeId: this.#activeId,
      view: this.#view,
      totalUnread: totalUnread(this.#channels),
      live: this.#liveState,
      now: this.#host.now(),
      error: this.#error,
      notice: this.#notice,
      convo: this.#convo(),
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
