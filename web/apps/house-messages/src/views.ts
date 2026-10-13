// House Messages' screens as functions of a plain model. The desk (desk.ts) builds the model and the
// host's listeners act on the data-action names; nothing here holds state. Templates keep to the
// design system's position rules: bound values only on class, role, data- and aria- attributes and
// the disabled flag, no property or event bindings.
import { html, nothing, type TemplateResult } from "lit";
import {
  MUTE_REFUSAL,
  canMute,
  channelGroups,
  modeWords,
  modesFor,
  queueOrder,
  waitedWords,
  channelLine,
  channelTitle,
  eventWhy,
  messageKindWord,
  repliesWord,
  timeWords,
  unreadWord,
  type Channel,
  type ChannelGroup,
  type HouseConversation,
  type Message,
  type NotifyMode,
} from "./messages-logic.js";
import { liveWords, type LiveState } from "./live.js";

/** What the desk hands the screen to draw. */
export interface Model {
  readonly phase: "loading" | "ready" | "error";
  readonly loadError: string;
  readonly meId: string;
  readonly channels: readonly Channel[];
  readonly activeId: string;
  readonly view: "list" | "conversation";
  readonly totalUnread: number;
  readonly live: LiveState | null;
  /** Words for a failed read, empty when the last read worked. */
  readonly stale: string;
  readonly now: number;
  readonly error: string;
  readonly notice: string;
  readonly bubble: BubbleModel | null;
  readonly rules: RulesModel | null;
  readonly convo: ConvoModel | null;
}

/** The caller's own payment notice rules, drawn in place of the conversation while open. */
export interface RulesModel {
  readonly loading: boolean;
  /** Keyed by rule id; the full closed set once read, null while no read has answered. */
  readonly values: Readonly<Record<string, boolean>> | null;
  readonly busy: string;
  readonly error: string;
}

/** The caller's notification choices for the open room. */
export interface PrefsModel {
  readonly loading: boolean;
  /** Whether the core's answer has been read; the controls are drawn only then. */
  readonly read: boolean;
  readonly mode: NotifyMode;
  readonly inherited: boolean;
  readonly desktop: boolean;
  readonly escalationOverride: boolean;
  readonly busy: boolean;
  readonly error: string;
}

/** The persistent forced acknowledgement, above both panes. */
export interface BubbleModel {
  readonly id: string;
  readonly channelId: string;
  readonly channelName: string;
  readonly body: string;
  readonly when: string;
  readonly progress: string;
  readonly busy: boolean;
  readonly error: string;
}

export interface QueueModel {
  readonly rows: readonly HouseConversation[];
  readonly busyId: string;
  readonly note: string;
  readonly error: string;
}

/** What one message says about acknowledgement, when it asks for one. */
export interface AckView {
  readonly superseded: boolean;
  /** The core's arithmetic in words; empty until it has been read. */
  readonly progress: string;
  /** The caller's own acknowledgement is among the core's count. */
  readonly youAcked: boolean;
  /** The core still lists this message as waiting on the caller. */
  readonly needsYou: boolean;
  readonly busy: boolean;
  readonly error: string;
}

export interface ConvoModel {
  readonly channel: Channel;
  readonly loading: boolean;
  /** Top level messages, oldest first. */
  readonly messages: readonly Message[];
  readonly hasOlder: boolean;
  readonly olderBusy: boolean;
  /** Thread roots that are open, with their replies read so far. */
  readonly threads: ReadonlyMap<string, readonly Message[]>;
  readonly acks: ReadonlyMap<string, AckView>;
  /** The notification sheet, open or not. */
  readonly prefs: PrefsModel | null;
  /** The house queue, only in a house room; null elsewhere. */
  readonly queue: QueueModel | null;
  readonly replyTo: { readonly rootId: string; readonly excerpt: string } | null;
  readonly composerWords: string;
  readonly composerBad: boolean;
  readonly canSend: boolean;
  readonly sending: boolean;
}

// ---- the strip ---------------------------------------------------------------------------------

function liveDot(state: LiveState | null): string {
  if (state === "live") return "dot dot-live";
  if (state === "offline") return "dot dot-stop";
  return "dot dot-watch";
}

function renderStrip(m: Model): TemplateResult {
  const state = m.live;
  const words = state === null ? "Connecting" : liveWords(state);
  const tone = state === "offline" ? "strip strip-stop" : state === "reconnecting" || state === "polling" ? "strip strip-warn" : "strip";
  const retry = state === "reconnecting" || state === "offline";
  return html`
    <div class="${tone}" role="status" data-live>
      <span class="live"><span class="${liveDot(state)}"></span><span data-live-words>${words}</span></span>
      <span class="spacer"></span>
      <span class="dim small" data-total-unread>${m.totalUnread === 0 ? "Nothing unread" : `${m.totalUnread} unread`}</span>
      ${retry ? html`<button type="button" class="btn btn-secondary small-btn" data-action="retry-live">Retry now</button>` : nothing}
      ${m.stale === "" ? nothing : html`<span class="small words-bad" data-stale>${m.stale}</span>`}
    </div>
  `;
}

// ---- the channel rail --------------------------------------------------------------------------

function renderChannel(c: Channel, activeId: string): TemplateResult {
  const unread = unreadWord(c);
  const line = channelLine(c);
  return html`
    <li>
      <button
        type="button"
        class="${c.unread_count > 0 ? "ch has-unread" : "ch"}"
        data-action="open-channel"
        data-id=${c.id}
        aria-current=${c.id === activeId ? "true" : "false"}
      >
        <span class="ch-text">
          <span class="ch-name">${channelTitle(c)}</span>
          ${line === "" ? nothing : html`<span class="ch-line">${line}</span>`}
        </span>
        ${c.unread_count > 0
          ? html`<span class="${c.mentioned ? "badge badge-mention" : "badge"}" data-unread aria-label=${unread}>${c.unread_count > 99 ? "99+" : c.unread_count}</span>`
          : nothing}
      </button>
    </li>
  `;
}

function renderGroup(g: ChannelGroup, activeId: string): TemplateResult {
  return html`
    <section class="group">
      <h3>${g.label}</h3>
      <ul>${g.channels.map((c) => renderChannel(c, activeId))}</ul>
    </section>
  `;
}

function renderRail(m: Model): TemplateResult {
  const groups = channelGroups(m.channels);
  return html`
    <aside class="rail" data-channel-list aria-label="Channels">
      <div class="rail-head"><h2>Channels</h2></div>
      ${groups.length === 0
        ? html`<p class="dim small" data-no-channels>You are not in any channel yet.</p>`
        : groups.map((g) => renderGroup(g, m.activeId))}
      <div class="rail-foot"><button type="button" class="link" data-action="open-rules">Payment notice rules</button></div>
    </aside>
  `;
}

// ---- the conversation --------------------------------------------------------------------------

function renderMessageBody(msg: Message): TemplateResult {
  if (msg.deleted_at) return html`<p class="text dim" data-body>This message was removed.</p>`;
  return html`<p class="text" data-body>${msg.body}</p>`;
}

function renderMessage(msg: Message, c: ConvoModel, m: Model, isReply: boolean): TemplateResult {
  const mine = msg.author_id !== undefined && msg.author_id !== "" && msg.author_id === m.meId;
  const system = msg.kind !== "TEXT";
  const who = mine ? "You" : system ? (messageKindWord(msg) || "System notice") : msg.author_name || "A colleague";
  const kind = system ? messageKindWord(msg) : "";
  const classes = ["msg", mine ? "mine" : "", system ? "event" : "", msg.superseded_by ? "superseded" : ""].filter((x) => x !== "").join(" ");
  const why = msg.kind === "EVENT" ? eventWhy(msg.ref as Parameters<typeof eventWhy>[0]) : "";
  const open = c.threads.get(msg.id);
  const replies = open ?? [];
  return html`
    <li class="${classes}" data-msg=${msg.id}>
      <div class="msg-head">
        <span class="who">${who}</span>
        ${kind !== "" && !mine && who !== kind ? html`<span class="kind">${kind}</span>` : nothing}
        <span class="when">${timeWords(msg.created_at, m.now)}</span>
        ${msg.edited_at ? html`<span class="dim small">edited</span>` : nothing}
      </div>
      ${renderMessageBody(msg)}
      ${why === "" ? nothing : html`<p class="why" data-why>${why}</p>`}
      ${renderAck(c.acks.get(msg.id), msg)}
      ${isReply
        ? nothing
        : html`
            <div class="msg-foot">
              <button type="button" class="link" data-action="toggle-thread" data-id=${msg.id}>${repliesWord(msg.reply_count)}</button>
              ${open !== undefined ? html`<button type="button" class="link" data-action="reply" data-id=${msg.id}>Reply in thread</button>` : nothing}
            </div>
          `}
      ${open === undefined
        ? nothing
        : html`<ul class="thread" data-thread>${replies.map((r) => renderMessage(r, c, m, true))}</ul>`}
    </li>
  `;
}

function renderAck(a: AckView | undefined, msg: Message): TemplateResult | typeof nothing {
  if (a === undefined) return nothing;
  if (a.superseded) {
    return html`<p class="msg-foot" data-ack><span class="tag tag-plain">Replaced by a newer notice</span><span class="dim small">It no longer needs acknowledging.</span></p>`;
  }
  return html`
    <div class="msg-foot" data-ack>
      ${a.needsYou
        ? html`<span class="tag tag-stop">Needs your acknowledgement</span>`
        : a.youAcked
          ? html`<span class="tag tag-good">You acknowledged</span>`
          : nothing}
      ${a.progress === "" ? nothing : html`<span class="small" data-ack-progress>${a.progress}</span>`}
      ${a.needsYou
        ? html`<button type="button" class="btn btn-primary small-btn" data-action="ack" data-id=${msg.id} data-channel=${msg.channel_id} ?disabled=${a.busy}>I got it</button>`
        : nothing}
      ${a.error === "" ? nothing : html`<span class="small words-bad" role="alert" data-ack-error>${a.error}</span>`}
    </div>
  `;
}

function renderBubble(b: BubbleModel): TemplateResult {
  return html`
    <section class="bubble" role="alert" aria-live="assertive" data-bubble>
      <div class="body">
        <p class="eyebrow">Needs your acknowledgement</p>
        <p class="text">${b.body}</p>
        <p class="dim small">${b.channelName} \u00b7 ${b.when}</p>
        ${b.progress === "" ? nothing : html`<p class="small" data-ack-progress>${b.progress}</p>`}
        ${b.error === "" ? nothing : html`<p class="small words-bad" role="alert" data-bubble-error>${b.error}</p>`}
      </div>
      <button type="button" class="btn btn-primary" data-action="ack" data-id=${b.id} data-channel=${b.channelId} ?disabled=${b.busy}>I got it</button>
    </section>
  `;
}

const RULES: readonly { id: string; label: string; line: string }[] = [
  { id: "ONLINE", label: "An online payment", line: "A customer paid on the web." },
  { id: "CROSS_BRANCH", label: "A payment at another branch", line: "Money taken at a yard other than your own." },
  { id: "OWN_ORDER", label: "A payment on your own order", line: "A payment against an order you wrote." },
];

function renderPrefs(ch: Channel, p: PrefsModel): TemplateResult {
  const offered = modesFor(ch.tier);
  return html`
    <section class="sheet" data-prefs>
      <div class="sheet-head">
        <h4>Notifications for ${channelTitle(ch)}</h4>
        <button type="button" class="link" data-action="close-panel">Close</button>
      </div>
      ${p.loading
        ? html`<p class="dim" data-loading>Loading your choices</p>`
        : !p.read
          ? html`<button type="button" class="btn btn-secondary" data-action="retry-prefs">Try again</button>`
          : html`
            <div class="segs" role="group" aria-label="Notification mode">
              ${offered.map(
                (mo) => html`<button type="button" class="seg" data-action="set-mode" data-mode=${mo} aria-pressed=${p.mode === mo ? "true" : "false"} ?disabled=${p.busy}>${modeWords(mo)}</button>`,
              )}
            </div>
            ${p.inherited ? html`<p class="dim small" data-inherited>You have not chosen. This is the room's default.</p>` : nothing}
            ${canMute(ch.tier) ? nothing : html`<p class="small" data-mute-rule>${MUTE_REFUSAL}</p>`}
            <div class="toggle-row">
              <span class="grow">Desktop notifications</span>
              <button type="button" class="switch" data-action="toggle-pref" data-pref="desktop_enabled" aria-pressed=${p.desktop ? "true" : "false"} ?disabled=${p.busy}>${p.desktop ? "On" : "Off"}</button>
            </div>
            <div class="toggle-row">
              <span class="grow">A firehose escalation reaches you even when the room is muted</span>
              <button type="button" class="switch" data-action="toggle-pref" data-pref="escalation_override" aria-pressed=${p.escalationOverride ? "true" : "false"} ?disabled=${p.busy}>${p.escalationOverride ? "On" : "Off"}</button>
            </div>
          `}
      ${p.error === "" ? nothing : html`<p class="small words-bad" role="alert" data-prefs-error>${p.error}</p>`}
    </section>
  `;
}

function renderRules(r: RulesModel): TemplateResult {
  const values = r.values;
  return html`
    <section class="convo" data-rules>
      <div class="convo-head">
        <button type="button" class="btn btn-secondary small-btn back" data-action="close-panel">Back</button>
        <div class="titles">
          <h2>Payment notices</h2>
          <p class="dim small">Your own choices. Nobody else's change what reaches you.</p>
        </div>
        <button type="button" class="link" data-action="close-panel">Close</button>
      </div>
      <div class="convo-body">
        ${r.loading
          ? html`<p class="dim" data-loading>Loading your rules</p>`
          : values === null
            ? html`<button type="button" class="btn btn-secondary" data-action="retry-rules">Try again</button>`
            : RULES.map(
              (rule) => html`
                <div class="toggle-row">
                  <span class="grow"><strong>${rule.label}</strong><br /><span class="dim small">${rule.line}</span></span>
                  <button type="button" class="switch" data-action="toggle-rule" data-rule=${rule.id} aria-pressed=${values[rule.id] === true ? "true" : "false"} ?disabled=${r.busy !== ""}>${values[rule.id] === true ? "On" : "Off"}</button>
                </div>
              `,
            )}
        ${r.error === "" ? nothing : html`<p class="small words-bad" role="alert" data-rules-error>${r.error}</p>`}
      </div>
    </section>
  `;
}

function renderQueueRow(r: HouseConversation, q: QueueModel, now: number): TemplateResult {
  const claimed = r.claimed_by !== undefined && r.claimed_by !== "";
  const who = r.customer_name || "A customer";
  const count = `${r.message_count} message${r.message_count === 1 ? "" : "s"}`;
  return html`
    <li class="${claimed ? "queue-row" : "queue-row open"}" data-queue-row data-id=${r.id}>
      <div class="who-line">
        <span class="who">${who}</span>
        <span class="dim small">${count} \u00b7 waiting ${waitedWords(r.first_at, now)}${claimed ? ` \u00b7 Claimed by ${r.claimed_by_name || "a rep"}` : ""}</span>
      </div>
      ${claimed
        ? nothing
        : html`<button type="button" class="btn btn-primary small-btn" data-action="claim" data-id=${r.id} ?disabled=${q.busyId !== ""}>Claim</button>`}
    </li>
  `;
}

function renderQueue(q: QueueModel, now: number): TemplateResult {
  return html`
    <section class="queue" data-queue>
      <h4>Customers waiting</h4>
      ${q.rows.length === 0
        ? html`<p class="dim small" data-queue-empty>Nobody is waiting. Every customer here has an owner.</p>`
        : html`<ul>${queueOrder(q.rows).map((r) => renderQueueRow(r, q, now))}</ul>`}
      ${q.note === "" ? nothing : html`<p class="small" role="status" data-queue-note>${q.note}</p>`}
      ${q.error === "" ? nothing : html`<p class="small words-bad" role="alert" data-queue-error>${q.error}</p>`}
    </section>
  `;
}

function renderComposer(c: ConvoModel): TemplateResult {
  return html`
    <form class="composer" data-composer>
      ${c.replyTo === null
        ? nothing
        : html`
            <div class="replying" data-replying>
              <span>Replying in thread</span>
              <span class="excerpt dim">${c.replyTo.excerpt}</span>
              <button type="button" class="link" data-action="cancel-reply">Cancel</button>
            </div>
          `}
      <textarea data-field="draft" rows="3" aria-label="Write a message" placeholder="Write a message"></textarea>
      <div class="composer-row">
        <span class="${c.composerBad ? "small words-bad" : "small dim"}" data-composer-words>${c.composerWords}</span>
        <span class="spacer"></span>
        <button type="button" class="btn btn-primary" data-action="send" ?disabled=${!c.canSend || c.sending}>Send</button>
      </div>
      <p class="dim small">Messages send while you are online. Nothing is saved to send later.</p>
    </form>
  `;
}

function renderConvo(m: Model, c: ConvoModel): TemplateResult {
  const ch = c.channel;
  const tier = ch.tier === "ESCALATION" ? "Escalation" : ch.tier === "FIREHOSE" ? "Firehose" : "";
  return html`
    <section class="convo" data-conversation>
      <div class="convo-head">
        <button type="button" class="btn btn-secondary small-btn back" data-action="back">Back</button>
        <div class="titles">
          <h2>${channelTitle(ch)}</h2>
          ${channelLine(ch) === "" ? nothing : html`<p class="dim small">${channelLine(ch)}</p>`}
        </div>
        ${tier === "" ? nothing : html`<span class="tier">${tier}</span>`}
        <button type="button" class="btn btn-secondary small-btn" data-action="open-prefs">Notifications</button>
      </div>
      <div class="convo-body">
        ${c.prefs === null ? nothing : renderPrefs(ch, c.prefs)}
        ${c.queue === null ? nothing : renderQueue(c.queue, m.now)}
        ${c.loading
          ? html`<p class="dim" data-loading>Loading messages</p>`
          : html`
              ${c.hasOlder
                ? html`<button type="button" class="btn btn-secondary small-btn" data-action="load-older" ?disabled=${c.olderBusy}>Earlier messages</button>`
                : nothing}
              ${c.messages.length === 0
                ? html`<div class="empty" data-empty><strong>No messages yet</strong>Write the first one below.</div>`
                : html`<ul class="stream" data-stream>${c.messages.map((msg) => renderMessage(msg, c, m, false))}</ul>`}
            `}
      </div>
      ${renderComposer(c)}
    </section>
  `;
}

function renderEmptyConvo(): TemplateResult {
  return html`
    <section class="convo" data-conversation-empty>
      <div class="empty"><strong>Pick a channel</strong>Choose a room on the left to read it and write to it.</div>
    </section>
  `;
}

// ---- the desk ----------------------------------------------------------------------------------

export function renderDesk(m: Model): TemplateResult {
  if (m.phase === "loading") {
    return html`<div class="desk"><h1>House Messages</h1><p class="dim" data-loading>Loading your channels</p></div>`;
  }
  if (m.phase === "error") {
    return html`
      <div class="desk">
        <h1>House Messages</h1>
        <div class="alert alert-error" role="alert" data-load-error>${m.loadError}</div>
        <button type="button" class="btn btn-secondary" data-action="refresh">Try again</button>
      </div>
    `;
  }
  return html`
    <div class="${m.view === "conversation" ? "desk view-conv" : "desk view-list"}">
      ${renderStrip(m)}
      ${m.error === "" ? nothing : html`<div class="alert alert-error" role="alert" data-error>${m.error}</div>`}
      ${m.notice === "" ? nothing : html`<div class="alert alert-ok" role="status" data-notice>${m.notice}</div>`}
      ${m.bubble === null ? nothing : renderBubble(m.bubble)}
      <div class="panes">
        ${renderRail(m)}
        ${m.rules !== null ? renderRules(m.rules) : m.convo === null ? renderEmptyConvo() : renderConvo(m, m.convo)}
      </div>
    </div>
  `;
}
