// House Messages' screens as functions of a plain model. The desk (desk.ts) builds the model and the
// host's listeners act on the data-action names; nothing here holds state. Templates keep to the
// design system's position rules: bound values only on class, role, data- and aria- attributes and
// the disabled flag, no property or event bindings.
import { html, nothing, type TemplateResult } from "lit";
import {
  channelGroups,
  channelLine,
  channelTitle,
  eventWhy,
  messageKindWord,
  repliesWord,
  timeWords,
  unreadWord,
  type Channel,
  type ChannelGroup,
  type Message,
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
  readonly now: number;
  readonly error: string;
  readonly notice: string;
  readonly convo: ConvoModel | null;
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
  const retry = state === "reconnecting" || state === "offline" || state === "polling";
  return html`
    <div class="${tone}" role="status" data-live>
      <span class="live"><span class="${liveDot(state)}"></span><span data-live-words>${words}</span></span>
      <span class="spacer"></span>
      <span class="dim small" data-total-unread>${m.totalUnread === 0 ? "Nothing unread" : `${m.totalUnread} unread`}</span>
      ${retry ? html`<button type="button" class="btn btn-secondary small-btn" data-action="retry-live">Retry now</button>` : nothing}
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
      </div>
      <div class="convo-body">
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
      <div class="panes">
        ${renderRail(m)}
        ${m.convo === null ? renderEmptyConvo() : renderConvo(m, m.convo)}
      </div>
    </div>
  `;
}
