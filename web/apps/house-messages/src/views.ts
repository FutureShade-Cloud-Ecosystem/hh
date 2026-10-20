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
  initialsOf,
  systemEventDetails,
  parseMentions,
  QUICK_REPLIES,
  canEditMessage,
  formatFileSize,
  type Channel,
  type HouseConversation,
  type Message,
  type NotifyMode,
  type StaffMember,
  type ReactionItem,
} from "./messages-logic.js";
import type {
  AttachmentItem,
  ChannelDirectoryItem,
  MembershipRuleItem,
  MemberByHandItem,
  EventClassItem,
  RepAssignmentItem,
  NotificationSettings,
  SearchResults,
} from "./messages-api.js";
import { liveWords, type LiveState } from "./live.js";

/** What the desk hands the screen to draw. */
export interface Model {
  readonly phase: "loading" | "ready" | "error";
  readonly loadError: string;
  readonly meId: string;
  readonly channels: readonly Channel[];
  readonly activeId: string;
  readonly view: "list" | "conversation";
  readonly mainView: "conversation" | "mentions" | "saved" | "search" | "settings" | "notifications-optin";
  readonly totalUnread: number;
  readonly live: LiveState | null;
  readonly stale: string;
  readonly now: number;
  readonly error: string;
  readonly notice: string;
  readonly bubble: BubbleModel | null;
  readonly rules: RulesModel | null;
  readonly convo: ConvoModel | null;
  readonly isAdmin: boolean;
  readonly newMenuOpen: boolean;

  // C2 Modals
  readonly newMsgModal: {
    readonly open: boolean;
    readonly roster: readonly StaffMember[];
    readonly selectedIds: ReadonlySet<string>;
    readonly groupName: string;
  } | null;
  readonly newChannelModal: {
    readonly open: boolean;
    readonly name: string;
    readonly topic: string;
  } | null;
  readonly attachModal: {
    readonly open: boolean;
  } | null;

  // C3 Views
  readonly mentionsList: readonly { readonly message: Message; readonly channel_id: string; readonly channel_name: string }[];
  readonly savedList: readonly { readonly message: Message; readonly channel_id: string; readonly channel_name: string; readonly saved_at: string }[];
  readonly searchResults: SearchResults | null;

  // C4/C5 Views
  readonly settingsView: {
    readonly subtab: "membership-rules" | "event-classes" | "rep-assignments";
    readonly membershipRules: readonly MembershipRuleItem[];
    readonly membersByHand: readonly MemberByHandItem[];
    readonly eventClasses: readonly EventClassItem[];
    readonly repAssignments: readonly RepAssignmentItem[];
  } | null;
  readonly notifyOptInView: {
    readonly settings: NotificationSettings | null;
    readonly directory: readonly ChannelDirectoryItem[];
  } | null;
}

/** The caller's own payment notice rules, drawn in place of the conversation while open. */
export interface RulesModel {
  readonly loading: boolean;
  readonly values: Readonly<Record<string, boolean>> | null;
  readonly busy: string;
  readonly error: string;
}

/** The caller's notification choices for the open room. */
export interface PrefsModel {
  readonly loading: boolean;
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
  readonly progress: string;
  readonly youAcked: boolean;
  readonly needsYou: boolean;
  readonly busy: boolean;
  readonly error: string;
}

export interface ConvoModel {
  readonly channel: Channel;
  readonly loading: boolean;
  readonly messages: readonly Message[];
  readonly hasOlder: boolean;
  readonly olderBusy: boolean;
  readonly threads: ReadonlyMap<string, readonly Message[]>;
  readonly acks: ReadonlyMap<string, AckView>;
  readonly prefs: PrefsModel | null;
  readonly queue: QueueModel | null;
  readonly replyTo: { readonly rootId: string; readonly excerpt: string } | null;
  readonly composerWords: string;
  readonly composerBad: boolean;
  readonly canSend: boolean;
  readonly sending: boolean;
  readonly openThreadId: string | null;
  readonly openThreadRoot: Message | null;
  readonly openThreadReplies: readonly Message[];
  readonly threadSending: boolean;
  readonly activeTab: "messages" | "files";

  // C2 Group DM & Prompt
  readonly groupSubtitle: string;
  readonly dmPrompt: string;
  readonly memberInitials: readonly string[];

  // C3 Reactions & Editing & Seen
  readonly reactions: ReadonlyMap<string, readonly ReactionItem[]>;
  readonly activeReactPickerMsgId: string | null;
  readonly editingMessageId: string | null;
  readonly seenCounts: ReadonlyMap<string, number>;

  // C6 Files
  readonly attachments: readonly AttachmentItem[];
}

// ---- SVGs --------------------------------------------------------------------------------------

function renderGearIcon(): TemplateResult {
  return html`
    <svg class="icon icon-gear" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="3"></circle>
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"></path>
    </svg>
  `;
}

function renderPaperclipIcon(): TemplateResult {
  return html`
    <svg class="icon icon-paperclip" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"></path>
    </svg>
  `;
}

function renderEventIcon(icon: "truck" | "box" | "shield" | "notice"): TemplateResult {
  switch (icon) {
    case "truck":
      return html`
        <svg class="icon" data-icon="truck" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <rect x="1" y="3" width="15" height="13"></rect>
          <polygon points="16 8 20 8 23 11 23 16 16 16 16 8"></polygon>
          <circle cx="5.5" cy="18.5" r="2.5"></circle>
          <circle cx="18.5" cy="18.5" r="2.5"></circle>
        </svg>
      `;
    case "box":
      return html`
        <svg class="icon" data-icon="box" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"></path>
          <polyline points="3.27 6.96 12 12.01 20.73 6.96"></polyline>
          <line x1="12" y1="22.08" x2="12" y2="12"></line>
        </svg>
      `;
    case "shield":
      return html`
        <svg class="icon" data-icon="shield" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"></path>
        </svg>
      `;
    default:
      return html`
        <svg class="icon" data-icon="notice" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="10"></circle>
          <line x1="12" y1="8" x2="12" y2="12"></line>
          <line x1="12" y1="16" x2="12.01" y2="16"></line>
        </svg>
      `;
  }
}

function notifyModeWord(mode?: NotifyMode | null): string {
  if (mode === "MENTIONS") return "Mentions";
  if (mode === "MUTED") return "Muted";
  return "All";
}

// ---- the strip ---------------------------------------------------------------------------------

function liveDot(state: LiveState | null): string {
  if (state === "live" || state === "polling") return "dot dot-live";
  if (state === "offline") return "dot dot-stop";
  return "dot dot-watch";
}

function renderStrip(m: Model): TemplateResult {
  const state = m.live;
  const words = state === null ? "Connecting" : liveWords(state);
  const tone = state === "offline" ? "strip strip-stop" : state === "reconnecting" ? "strip strip-warn" : "strip";
  const retry = state === "reconnecting" || state === "offline";
  return html`
    <div class="${tone}" role="status" data-live>
      <h1 class="strip-title" data-total-unread>${m.totalUnread === 0 ? "Nothing unread" : `${m.totalUnread} unread`}</h1>
      <span class="spacer"></span>
      <span class="live dim small"><span class="${liveDot(state)}"></span><span data-live-words>${words}</span></span>
      ${retry ? html`<button type="button" class="btn btn-secondary small-btn" data-action="retry-live">Retry now</button>` : nothing}
      ${m.stale === "" ? nothing : html`<span class="small words-bad" data-stale>${m.stale}</span>`}
    </div>
  `;
}

// ---- the channel rail --------------------------------------------------------------------------

function renderChannelItem(c: Channel, activeId: string, isDm: boolean): TemplateResult {
  const unread = unreadWord(c);
  return html`
    <li>
      <button
        type="button"
        class="${c.unread_count > 0 ? "ch has-unread" : "ch"}"
        data-action="open-channel"
        data-id=${c.id}
        aria-current=${c.id === activeId ? "true" : "false"}
      >
        ${isDm
          ? html`<span class="avatar avatar-sm" aria-hidden="true">${initialsOf(channelTitle(c))}</span>`
          : html`<span class="ch-prefix" aria-hidden="true">#</span>`}
        <span class="ch-text">
          <span class="ch-name">${channelTitle(c)}</span>
        </span>
        ${c.unread_count > 0
          ? html`<span class="${c.mentioned ? "badge badge-mention" : "badge"}" data-unread aria-label=${unread}>${c.unread_count > 99 ? "99+" : c.unread_count}</span>`
          : nothing}
      </button>
    </li>
  `;
}

function renderRail(m: Model): TemplateResult {
  const dmChannels = m.channels.filter((c) => c.kind === "DM" || c.kind === "GROUP");
  const groups = channelGroups(m.channels.filter((c) => c.kind !== "DM" && c.kind !== "GROUP"));

  return html`
    <aside class="rail" data-channel-list aria-label="Channels">
      <div class="rail-top">
        <button
          type="button"
          class="btn btn-primary btn-new"
          data-action="toggle-new-menu"
          aria-haspopup="menu"
          aria-expanded=${m.newMenuOpen ? "true" : "false"}
        >+ New</button>
        ${m.newMenuOpen
          ? html`
            <div class="new-menu-popover" role="menu" data-new-menu>
              <button type="button" role="menuitem" class="menu-item" data-action="new-message">New message</button>
              ${m.isAdmin ? html`<button type="button" role="menuitem" class="menu-item" data-action="new-channel">New channel</button>` : nothing}
            </div>
          `
          : nothing}
      </div>
      <div class="search-wrap">
        <input type="search" class="search-input" placeholder="Search everything" aria-label="Search everything" />
      </div>
      <div class="nav-links">
        <button type="button" class="nav-row" data-action="nav-mentions">
          <span class="nav-icon" aria-hidden="true">@</span>
          <span>Mentions</span>
        </button>
        <button type="button" class="nav-row" data-action="nav-saved">
          <span class="nav-icon" aria-hidden="true">&#9734;</span>
          <span>Saved</span>
        </button>
      </div>
      <div class="section-head">
        <h3 class="section-title">CHANNELS</h3>
        ${m.isAdmin ? html`<button type="button" class="btn-icon-plus" data-action="add-channel" aria-label="Add channel">+</button>` : nothing}
      </div>
      ${groups.map(
        (g) => html`
          <div class="ch-group">
            <h4 class="group-title">${g.label}</h4>
            <ul class="ch-list">
              ${g.channels.map((c) => renderChannelItem(c, m.activeId, false))}
            </ul>
          </div>
        `,
      )}
      <div class="section-head">
        <h3 class="section-title">DIRECT MESSAGES</h3>
        <button type="button" class="btn-icon-plus" data-action="new-dm" aria-label="Add direct message">+</button>
      </div>
      <ul class="ch-list">
        ${dmChannels.map((c) => renderChannelItem(c, m.activeId, true))}
      </ul>
      <div class="rail-foot">
        <button type="button" class="btn-settings" data-action="open-settings">
          ${renderGearIcon()}
          <span>Settings</span>
        </button>
        <button type="button" class="link" data-action="open-rules">Payment notice rules</button>
        <button type="button" class="link" data-action="open-notify-optin">Notification preferences</button>
      </div>
    </aside>
  `;
}

// ---- the conversation --------------------------------------------------------------------------

function renderMessageBody(msg: Message, c: ConvoModel): TemplateResult {
  if (msg.deleted_at) return html`<p class="text dim" data-body>This message was deleted</p>`;
  if (c.editingMessageId === msg.id) {
    return html`
      <div class="edit-box">
        <textarea data-field="edit-body" data-initial=${msg.body} rows="2"></textarea>
        <div class="edit-actions">
          <button type="button" class="btn btn-primary small-btn" data-action="save-edit" data-id=${msg.id} data-channel=${msg.channel_id}>Save</button>
          <button type="button" class="btn btn-secondary small-btn" data-action="cancel-edit">Cancel</button>
        </div>
      </div>
    `;
  }
  const segments = parseMentions(msg.body);
  return html`
    <p class="text" data-body>
      ${segments.map((seg) => (seg.type === "mention" ? html`<span class="mention-chip">${seg.value}</span>` : seg.value))}
    </p>
  `;
}

function renderMessage(msg: Message, c: ConvoModel, m: Model, isReply: boolean): TemplateResult {
  const mine = msg.author_id !== undefined && msg.author_id !== "" && msg.author_id === m.meId;
  const isEvent = msg.kind === "EVENT";
  const eventDetails = isEvent
    ? systemEventDetails({ kind: msg.kind, ref: msg.ref as { topic?: string; entity?: string }, channel_id: msg.channel_id, channel_slug: c.channel.slug })
    : null;

  const who = mine ? "You" : isEvent ? eventDetails!.source : msg.author_name || "A colleague";
  const kind = !isEvent && msg.kind !== "TEXT" ? messageKindWord(msg) : "";
  const classes = ["msg", mine ? "mine" : "", isEvent ? "event" : "", msg.superseded_by ? "superseded" : ""].filter((x) => x !== "").join(" ");
  const why = isEvent ? eventWhy(msg.ref as Parameters<typeof eventWhy>[0]) : "";
  const open = c.threads.get(msg.id);
  const replies = open ?? [];
  const initials = isEvent ? "" : initialsOf(msg.author_name || (mine ? "You" : "Colleague"));

  const canEdit = canEditMessage(msg, m.meId, m.now);
  const msgReactions = c.reactions.get(msg.id) ?? [];
  const isReactOpen = c.activeReactPickerMsgId === msg.id;
  const seenCount = c.seenCounts.get(msg.id) ?? 0;

  return html`
    <li class="${classes}" data-msg=${msg.id} data-kind=${msg.kind}>
      <div class="msg-head">
        ${isEvent
          ? html`<span class="icon-avatar" aria-hidden="true">${renderEventIcon(eventDetails!.icon)}</span>`
          : html`<span class="avatar" data-avatar aria-hidden="true">${initials}</span>`}
        <span class="who">${who}</span>
        ${kind !== "" && !mine && who !== kind ? html`<span class="kind">${kind}</span>` : nothing}
        <span class="when">${timeWords(msg.created_at, m.now)}</span>
        ${msg.edited_at ? html`<span class="dim small edited-marker">(edited)</span>` : nothing}
      </div>
      ${renderMessageBody(msg, c)}
      ${why === "" ? nothing : html`<p class="why" data-why>${why}</p>`}
      ${eventDetails?.actionPill
        ? html`
          <div class="event-actions">
            <button type="button" class="btn btn-secondary small-btn btn-action-pill" data-action="open-entity" data-entity=${eventDetails.actionPill.entity}>
              ${eventDetails.actionPill.label}
            </button>
          </div>
        `
        : nothing}
      ${msg.kind === "TEXT"
        ? html`
          <div class="reactions-row">
            ${msgReactions.map(
              (item) => html`
                <button
                  type="button"
                  class="reaction-chip ${item.mine ? "mine" : ""}"
                  data-action="toggle-reaction"
                  data-emoji=${item.emoji}
                  data-id=${msg.id}
                  data-channel=${msg.channel_id}
                >
                  <span>${item.emoji}</span>
                  <span>${item.count}</span>
                </button>
              `,
            )}
            <button
              type="button"
              class="btn-react-picker"
              data-action="open-react-picker"
              data-id=${msg.id}
            >+ React</button>
            ${isReactOpen
              ? html`
                <div class="react-popover" role="menu">
                  ${["Thanks", "On it", "Agreed", "Done", "+1"].map(
                    (opt) => html`
                      <button
                        type="button"
                        class="react-opt"
                        data-action="pick-reaction"
                        data-emoji=${opt}
                        data-id=${msg.id}
                        data-channel=${msg.channel_id}
                        data-react-option=${opt}
                      >${opt}</button>
                    `,
                  )}
                </div>
              `
              : nothing}
          </div>
        `
        : nothing}
      ${renderAck(c.acks.get(msg.id), msg)}
      <div class="msg-foot">
        ${canEdit
          ? html`
            <button type="button" class="link small" data-action="edit-msg" data-id=${msg.id}>Edit</button>
            <button type="button" class="link small" data-action="delete-msg" data-id=${msg.id} data-channel=${msg.channel_id}>Delete</button>
          `
          : nothing}
        ${seenCount > 0 ? html`<span class="dim small" data-seen-by>Seen ${seenCount}</span>` : nothing}
        <button type="button" class="link small" data-action="toggle-save-msg" data-id=${msg.id}>Save</button>
        ${isReply
          ? nothing
          : html`
              <button type="button" class="link btn-replies" data-action="toggle-thread" data-id=${msg.id}>${repliesWord(msg.reply_count)}</button>
              <button type="button" class="btn btn-secondary small-btn btn-open-thread" data-action="open-thread" data-id=${msg.id}>${msg.reply_count > 0 ? "Open thread" : "Reply"}</button>
              ${open !== undefined ? html`<button type="button" class="link" data-action="reply" data-id=${msg.id}>Reply in thread</button>` : nothing}
            `}
      </div>
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
      <div class="quick-replies" role="group" aria-label="Quick replies">
        ${QUICK_REPLIES.map(
          (text) => html`
            <button
              type="button"
              class="btn btn-secondary small-btn chip-quick"
              data-action="quick-reply"
              data-text=${text}
              data-quick-reply
            >${text}</button>
          `,
        )}
      </div>
      ${c.replyTo === null
        ? nothing
        : html`
            <div class="replying" data-replying>
              <span>Replying in thread</span>
              <span class="excerpt dim">${c.replyTo.excerpt}</span>
              <button type="button" class="link" data-action="cancel-reply">Cancel</button>
            </div>
          `}
      <div class="composer-box">
        <textarea data-field="draft" rows="3" aria-label="Write a message" placeholder="Write a message"></textarea>
        <div class="composer-actions">
          <button type="button" class="btn btn-secondary small-btn btn-attach" data-action="attach" aria-label="Attach file">
            ${renderPaperclipIcon()}
          </button>
          <span class="spacer"></span>
          <span class="${c.composerBad ? "small words-bad" : "small dim"}" data-composer-words>${c.composerWords}</span>
          <button type="button" class="btn btn-primary small-btn btn-send" data-action="send" ?disabled=${!c.canSend || c.sending}>Send</button>
        </div>
      </div>
      <p class="dim small">Messages send while you are online. Nothing is saved to send later.</p>
    </form>
  `;
}

function renderThreadSidePanel(m: Model, c: ConvoModel): TemplateResult {
  const root = c.openThreadRoot;
  const replies = c.openThreadReplies;

  return html`
    <aside class="thread-panel" data-thread-panel aria-label="Thread">
      <div class="thread-head">
        <h3>Thread</h3>
        <button type="button" class="btn-close" data-action="close-thread" aria-label="Close thread">&times;</button>
      </div>
      <div class="thread-body">
        ${root !== null
          ? html`
            <div class="thread-root">
              <div class="msg-head">
                <span class="avatar avatar-sm" aria-hidden="true">${initialsOf(root.author_name || "House")}</span>
                <span class="who">${root.author_id === m.meId ? "You" : root.author_name || "A colleague"}</span>
                <span class="when">${timeWords(root.created_at, m.now)}</span>
              </div>
              <div class="text">${root.body}</div>
            </div>
          `
          : nothing}
        <div class="thread-divider">
          <span>${replies.length} repl${replies.length === 1 ? "y" : "ies"}</span>
        </div>
        <ul class="thread-replies">
          ${replies.map(
            (r) => html`
              <li class="thread-reply" data-msg=${r.id}>
                <div class="msg-head">
                  <span class="avatar avatar-sm" aria-hidden="true">${initialsOf(r.author_name || "House")}</span>
                  <span class="who">${r.author_id === m.meId ? "You" : r.author_name || "A colleague"}</span>
                  <span class="when">${timeWords(r.created_at, m.now)}</span>
                </div>
                <div class="text">${r.body}</div>
              </li>
            `,
          )}
        </ul>
      </div>
      <form class="thread-composer" data-thread-composer>
        <textarea data-field="thread-draft" rows="2" placeholder="Reply..." aria-label="Reply in thread"></textarea>
        <div class="thread-composer-row">
          <span class="spacer"></span>
          <button type="button" class="btn btn-primary small-btn" data-action="thread-send" ?disabled=${c.threadSending}>Reply</button>
        </div>
      </form>
    </aside>
  `;
}

function renderFilesPane(c: ConvoModel): TemplateResult {
  const photos = c.attachments.filter((a) => a.content_type.startsWith("image/") || a.filename.endsWith(".jpg") || a.filename.endsWith(".png") || a.filename.endsWith(".webp"));
  const files = c.attachments.filter((a) => !photos.includes(a));

  return html`
    <div class="files-pane" data-files-pane>
      <div class="photos-section">
        <h3>Photos (${photos.length})</h3>
        <div class="photos-grid" data-photos-grid>
          ${photos.length === 0 ? html`<p class="dim small">No photos in this room</p>` : nothing}
          ${photos.map(
            (p) => html`
              <div class="photo-card" data-photo-id=${p.id}>
                <div class="photo-thumb">[ Photo ]</div>
                <div class="photo-info">
                  <strong>${p.caption || p.filename}</strong>
                  <div class="dim small">${p.author_name}</div>
                </div>
              </div>
            `,
          )}
        </div>
      </div>
      <div class="files-section">
        <h3>Files (${files.length})</h3>
        <div class="files-list" data-files-list>
          ${files.length === 0 ? html`<p class="dim small">No files in this room</p>` : nothing}
          ${files.map(
            (f) => html`
              <div class="file-row" data-file-id=${f.id}>
                <div>
                  <strong>${f.filename}</strong>
                  <div class="dim small">${f.author_name} \u00b7 ${formatFileSize(f.size_bytes)}</div>
                </div>
                <button type="button" class="btn btn-secondary small-btn" data-action="open-file" data-id=${f.id}>Open full size</button>
              </div>
            `,
          )}
        </div>
      </div>
    </div>
  `;
}

function renderConvo(m: Model, c: ConvoModel): TemplateResult {
  const ch = c.channel;
  const isGroup = ch.kind === "GROUP";
  const tier = ch.tier === "ESCALATION" ? "Escalation" : ch.tier === "FIREHOSE" ? "Firehose" : "";
  const subtitle = isGroup ? c.groupSubtitle : ch.topic ? ch.topic : channelLine(ch);

  return html`
    <section class="convo" data-conversation>
      <div class="convo-head">
        <button type="button" class="btn btn-secondary small-btn back" data-action="back">Back</button>
        <div class="titles">
          <div class="channel-title-row">
            ${isGroup
              ? html`<div class="avatar-stacked">${c.memberInitials.slice(0, 2).map((init) => html`<span class="avatar avatar-sm">${init}</span>`)}</div>`
              : html`<span class="hash" aria-hidden="true">#</span>`}
            <h2>${channelTitle(ch)}</h2>
            ${isGroup
              ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-add-person" style="margin-left: 8px;">Add a person</button>`
              : nothing}
          </div>
          ${subtitle === "" ? nothing : html`<p class="dim small channel-topic ${isGroup ? "group-subtitle" : ""}">${subtitle}</p>`}
        </div>
        <div class="tab-strip" role="tablist">
          <button
            type="button"
            role="tab"
            class="tab ${c.activeTab === "messages" ? "active" : ""}"
            data-action="select-tab"
            data-tab="messages"
            aria-selected=${c.activeTab === "messages" ? "true" : "false"}
          >Messages</button>
          <button
            type="button"
            role="tab"
            class="tab ${c.activeTab === "files" ? "active" : ""}"
            data-action="select-tab"
            data-tab="files"
            aria-selected=${c.activeTab === "files" ? "true" : "false"}
          >Files and photos</button>
        </div>
        ${tier === "" ? nothing : html`<span class="tier">${tier}</span>`}
        <button type="button" class="btn btn-secondary small-btn btn-notify" data-action="open-prefs" aria-label="Notifications">
          Notifications
        </button>
      </div>
      <div class="convo-body">
        ${c.prefs === null ? nothing : renderPrefs(ch, c.prefs)}
        ${c.queue === null ? nothing : renderQueue(c.queue, m.now)}
        ${c.activeTab === "files"
          ? renderFilesPane(c)
          : c.loading
            ? html`<p class="dim" data-loading>Loading messages</p>`
            : html`
                ${c.hasOlder
                  ? html`<button type="button" class="btn btn-secondary small-btn" data-action="load-older" ?disabled=${c.olderBusy}>Earlier messages</button>`
                  : nothing}
                <div class="day-separator" role="separator"><span class="day-pill">Today</span></div>
                ${c.messages.length === 0
                  ? c.dmPrompt !== ""
                    ? html`<div class="empty" data-empty><p class="text" data-dm-prompt>${c.dmPrompt}</p></div>`
                    : html`<div class="empty" data-empty><strong>No messages yet</strong>Write the first one below.</div>`
                  : html`<ul class="stream" data-stream>${c.messages.map((msg) => renderMessage(msg, c, m, false))}</ul>`}
              `}
      </div>
      ${c.activeTab === "messages" ? renderComposer(c) : nothing}
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

// ---- Modals ------------------------------------------------------------------------------------

function renderNewMessageModal(m: Model): TemplateResult | typeof nothing {
  const modal = m.newMsgModal;
  if (!modal || !modal.open) return nothing;
  const count = modal.selectedIds.size;
  const btnText = count >= 2 ? "Start group" : "Start message";
  const selectedList = modal.roster.filter((r) => modal.selectedIds.has(r.user_id));

  return html`
    <div class="modal-backdrop" data-modal-backdrop>
      <div class="modal-dialog" data-modal="new-message" role="dialog" aria-modal="true" aria-labelledby="modal-msg-title">
        <div class="modal-head">
          <h2 id="modal-msg-title">New message</h2>
          <button type="button" class="btn-close" data-action="close-modal" aria-label="Close">&times;</button>
        </div>
        <div class="modal-body">
          <label><strong>To:</strong></label>
          <div class="to-chips-wrap" data-to-chips>
            ${selectedList.map(
              (p) => html`
                <span class="chip-selected">
                  ${p.display_name}
                  <button type="button" class="link small" data-action="remove-person" data-id=${p.user_id}>&times;</button>
                </span>
              `,
            )}
            <input type="text" data-field="roster-search" placeholder="Type a name, role or yard..." aria-label="Search people" />
          </div>
          ${count >= 2
            ? html`
              <label><strong>Group name (optional)</strong></label>
              <input type="text" data-field="group-name" placeholder="e.g. Saturday runs" data-value=${modal.groupName} />
            `
            : nothing}
          <div class="roster-list" data-roster-list>
            ${modal.roster.map(
              (p) => html`
                <button
                  type="button"
                  class="roster-item"
                  data-action="toggle-person"
                  data-person-id=${p.user_id}
                  aria-selected=${modal.selectedIds.has(p.user_id) ? "true" : "false"}
                >
                  <span class="check-indicator" role="checkbox" aria-checked=${modal.selectedIds.has(p.user_id) ? "true" : "false"}>${modal.selectedIds.has(p.user_id) ? "\u2713" : ""}</span>
                  <span class="avatar avatar-sm">${initialsOf(p.display_name)}</span>
                  <span><strong>${p.display_name}</strong> \u00b7 <span class="dim small">${p.role_name} (${p.branch_name})</span></span>
                </button>
              `,
            )}
          </div>
        </div>
        <div class="modal-foot">
          <button type="button" class="btn btn-secondary" data-action="close-modal">Cancel</button>
          <button type="button" class="btn btn-primary" data-action="start-conversation" ?disabled=${count === 0}>${btnText}</button>
        </div>
      </div>
    </div>
  `;
}

function renderNewChannelModal(m: Model): TemplateResult | typeof nothing {
  const modal = m.newChannelModal;
  if (!modal || !modal.open) return nothing;

  return html`
    <div class="modal-backdrop" data-modal-backdrop>
      <div class="modal-dialog" data-modal="new-channel" role="dialog" aria-modal="true" aria-labelledby="modal-ch-title">
        <div class="modal-head">
          <div style="display: flex; align-items: center; gap: 8px;">
            <h2 id="modal-ch-title">New channel</h2>
            <span class="badge-admin">ADMINS ONLY</span>
          </div>
          <button type="button" class="btn-close" data-action="close-modal" aria-label="Close">&times;</button>
        </div>
        <div class="modal-body">
          <label><strong>Channel name</strong></label>
          <input type="text" data-field="channel-name" placeholder="e.g. yard-julian-receiving" data-value=${modal.name} />
          <label><strong>Purpose (topic)</strong></label>
          <input type="text" data-field="channel-purpose" placeholder="What arrives at the dock..." data-value=${modal.topic} />
          <label><strong>Visibility</strong></label>
          <div class="segs">
            <button type="button" class="seg" data-action="set-channel-visibility" data-vis="PUBLIC" aria-pressed="true">Public</button>
            <button type="button" class="seg" data-action="set-channel-visibility" data-vis="PRIVATE" aria-pressed="false">Private</button>
          </div>
          <label><strong>Members by rule</strong></label>
          <p class="dim small">All staff at Point Loma will join automatically.</p>
        </div>
        <div class="modal-foot">
          <button type="button" class="btn btn-secondary" data-action="close-modal">Cancel</button>
          <button type="button" class="btn btn-primary" data-action="create-channel-submit">Create channel</button>
        </div>
      </div>
    </div>
  `;
}

function renderAttachModal(m: Model): TemplateResult | typeof nothing {
  const modal = m.attachModal;
  if (!modal || !modal.open) return nothing;

  return html`
    <div class="modal-backdrop" data-modal-backdrop>
      <div class="modal-dialog" data-modal="attach" role="dialog" aria-modal="true" aria-labelledby="modal-att-title">
        <div class="modal-head">
          <h2 id="modal-att-title">Attach file or photo</h2>
          <button type="button" class="btn-close" data-action="close-modal" aria-label="Close">&times;</button>
        </div>
        <div class="modal-body">
          <label><strong>File name</strong></label>
          <input type="text" data-field="attach-filename" placeholder="e.g. Yard safety walk.pdf" />
          <label><strong>Caption (optional)</strong></label>
          <input type="text" data-field="attach-caption" placeholder="Description of the file..." />
        </div>
        <div class="modal-foot">
          <button type="button" class="btn btn-secondary" data-action="close-modal">Cancel</button>
          <button type="button" class="btn btn-primary" data-action="attach-submit">Attach</button>
        </div>
      </div>
    </div>
  `;
}

// ---- Views (Mentions, Saved, Search, Settings, Opt-in) ------------------------------------------

function renderMentionsView(m: Model): TemplateResult {
  return html`
    <section class="convo" data-view="mentions">
      <div class="convo-head">
        <div class="titles">
          <h2>Mentions</h2>
          <p class="dim small">Messages that name you or your role</p>
        </div>
      </div>
      <div class="convo-body">
        ${m.mentionsList.length === 0
          ? html`<div class="empty"><strong>No mentions</strong>Nobody has mentioned you yet.</div>`
          : html`
            <ul class="stream">
              ${m.mentionsList.map(
                (item) => html`
                  <li class="msg" data-msg=${item.message.id}>
                    <div class="msg-head">
                      <span class="avatar" aria-hidden="true">${initialsOf(item.message.author_name || "Staff")}</span>
                      <span class="who">${item.message.author_name || "Colleague"}</span>
                      <span class="dim small">in #${item.channel_name}</span>
                      <span class="when">${timeWords(item.message.created_at, m.now)}</span>
                    </div>
                    <p class="text">${item.message.body}</p>
                    <div class="msg-foot">
                      <button type="button" class="link" data-action="open-channel" data-id=${item.channel_id}>Jump to channel</button>
                    </div>
                  </li>
                `,
              )}
            </ul>
          `}
      </div>
    </section>
  `;
}

function renderSavedView(m: Model): TemplateResult {
  return html`
    <section class="convo" data-view="saved">
      <div class="convo-head">
        <div class="titles">
          <h2>Saved messages</h2>
          <p class="dim small">Your bookmarked messages</p>
        </div>
      </div>
      <div class="convo-body">
        ${m.savedList.length === 0
          ? html`<div class="empty"><strong>Nothing saved</strong>Use the bookmark action on any message to save it here.</div>`
          : html`
            <ul class="stream">
              ${m.savedList.map(
                (item) => html`
                  <li class="msg" data-msg=${item.message.id}>
                    <div class="msg-head">
                      <span class="avatar" aria-hidden="true">${initialsOf(item.message.author_name || "Staff")}</span>
                      <span class="who">${item.message.author_name || "Colleague"}</span>
                      <span class="dim small">in #${item.channel_name}</span>
                      <span class="when">${timeWords(item.message.created_at, m.now)}</span>
                    </div>
                    <p class="text">${item.message.body}</p>
                    <div class="msg-foot">
                      <button type="button" class="link" data-action="open-channel" data-id=${item.channel_id}>Jump to channel</button>
                      <button type="button" class="link" data-action="unsave-msg" data-id=${item.message.id}>Remove from saved</button>
                    </div>
                  </li>
                `,
              )}
            </ul>
          `}
      </div>
    </section>
  `;
}

function renderSearchView(m: Model): TemplateResult {
  const res = m.searchResults;
  const messages = res?.messages ?? [];
  const people = res?.people ?? [];
  const channels = res?.channels ?? [];

  return html`
    <section class="convo" data-view="search">
      <div class="convo-head">
        <div class="titles">
          <h2>Search results</h2>
          <p class="dim small">${res?.query ? `Showing results for "${res.query}"` : "Type in the search box to find anything"}</p>
        </div>
      </div>
      <div class="convo-body">
        <div class="search-section" data-search-section="messages">
          <h3>Messages (${messages.length})</h3>
          ${messages.length === 0 ? html`<p class="dim small">No messages match.</p>` : nothing}
          <ul class="stream">
            ${messages.map(
              (item) => html`
                <li class="msg" data-msg=${item.message.id}>
                  <div class="msg-head">
                    <span class="who">${item.message.author_name || "Colleague"}</span>
                    <span class="dim small">in #${item.channel_name}</span>
                    <span class="when">${timeWords(item.message.created_at, m.now)}</span>
                  </div>
                  <p class="text">${item.message.body}</p>
                  <button type="button" class="link" data-action="open-channel" data-id=${item.channel_id}>Open message</button>
                </li>
              `,
            )}
          </ul>
        </div>
        <div class="search-section" data-search-section="people">
          <h3>People (${people.length})</h3>
          ${people.length === 0 ? html`<p class="dim small">No people match.</p>` : nothing}
          <div class="roster-list">
            ${people.map(
              (p) => html`
                <div class="roster-item">
                  <span class="avatar avatar-sm">${initialsOf(p.display_name)}</span>
                  <span style="flex: 1;"><strong>${p.display_name}</strong> \u00b7 <span class="dim small">${p.role_name} (${p.branch_name})</span></span>
                  <button type="button" class="btn btn-secondary small-btn" data-action="start-dm-with" data-id=${p.user_id}>Message</button>
                </div>
              `,
            )}
          </div>
        </div>
        <div class="search-section" data-search-section="channels">
          <h3>Channels (${channels.length})</h3>
          ${channels.length === 0 ? html`<p class="dim small">No channels match.</p>` : nothing}
          <ul class="ch-list">
            ${channels.map(
              (c) => html`
                <li>
                  <button type="button" class="ch" data-action="open-channel" data-id=${c.id}>
                    <span class="ch-prefix">#</span>
                    <span class="ch-name">${channelTitle(c)}</span>
                  </button>
                </li>
              `,
            )}
          </ul>
        </div>
      </div>
    </section>
  `;
}

function renderSettingsView(m: Model): TemplateResult {
  const s = m.settingsView;
  const subtab = s?.subtab ?? "membership-rules";

  return html`
    <section class="convo" data-view="settings">
      <div class="convo-head">
        <div class="titles">
          <h2>Settings</h2>
          <p class="dim small">Admin channel settings and routing rules</p>
        </div>
      </div>
      <div class="subtabs-bar">
        <button
          type="button"
          class="subtab-btn ${subtab === "membership-rules" ? "active" : ""}"
          data-action="settings-subtab"
          data-tab="membership-rules"
        >Membership rules</button>
        <button
          type="button"
          class="subtab-btn ${subtab === "event-classes" ? "active" : ""}"
          data-action="settings-subtab"
          data-tab="event-classes"
        >Event classes</button>
        <button
          type="button"
          class="subtab-btn ${subtab === "rep-assignments" ? "active" : ""}"
          data-action="settings-subtab"
          data-tab="rep-assignments"
        >Rep assignment</button>
      </div>
      <div class="view-pane">
        ${subtab === "membership-rules"
          ? html`
            <h3>Membership rules</h3>
            <p class="dim small">Who joins and leaves which channel automatically, by role and employment status.</p>
            <table class="data-table" data-membership-table>
              <thead>
                <tr>
                  <th>Channel</th>
                  <th>Joins automatically</th>
                  <th>Leaves automatically</th>
                  <th>Visibility</th>
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                ${(s?.membershipRules ?? []).map(
                  (r) => html`
                    <tr>
                      <td><strong>${channelTitle({ id: r.channel_id, kind: "TEAM", name: r.channel_name, topic: "", tier: "STANDARD", unread_count: 0, mentioned: false })}</strong></td>
                      <td>${r.joins_automatically}</td>
                      <td>${r.leaves_automatically}</td>
                      <td><span class="tag tag-plain">${r.visibility}</span></td>
                      <td><button type="button" class="link" data-action="edit-rule" data-id=${r.channel_id}>Edit</button></td>
                    </tr>
                  `,
                )}
              </tbody>
            </table>
            <div class="added-by-hand-section" data-added-by-hand style="margin-top: 24px;">
              <h3>Added by hand</h3>
              <p class="dim small">Staff members added explicitly to channels.</p>
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Person</th>
                    <th>Role</th>
                    <th>Added reason</th>
                    <th>Action</th>
                  </tr>
                </thead>
                <tbody>
                  ${(s?.membersByHand ?? []).map(
                    (mb) => html`
                      <tr>
                        <td><strong>${mb.display_name}</strong></td>
                        <td>${mb.role_name}</td>
                        <td>${mb.added_reason}</td>
                        <td>
                          <button type="button" class="btn btn-secondary small-btn" data-action="remove-member-by-hand" data-id=${mb.user_id} data-channel=${mb.channel_id}>Remove</button>
                        </td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `
          : subtab === "event-classes"
            ? html`
              <h3>Event classes</h3>
              <p class="dim small">Event definitions and routing configurations.</p>
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Event class</th>
                    <th>Topic</th>
                    <th>Description</th>
                  </tr>
                </thead>
                <tbody>
                  ${(s?.eventClasses ?? []).map(
                    (ec) => html`
                      <tr>
                        <td><strong>${ec.name}</strong></td>
                        <td class="code">${ec.topic}</td>
                        <td>${ec.description}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            `
            : html`
              <h3>Rep assignment</h3>
              <p class="dim small">Customer representatives and primary house queue handlers.</p>
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Customer</th>
                    <th>Assigned Rep</th>
                    <th>Action</th>
                  </tr>
                </thead>
                <tbody>
                  ${(s?.repAssignments ?? []).map(
                    (ra) => html`
                      <tr>
                        <td><strong>${ra.customer_name}</strong></td>
                        <td>${ra.rep_name}</td>
                        <td>
                          <button type="button" class="btn btn-secondary small-btn" data-action="unassign-rep" data-id=${ra.id}>Unassign</button>
                        </td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            `}
      </div>
    </section>
  `;
}

function renderNotificationsOptInView(m: Model): TemplateResult {
  const notif = m.notifyOptInView;
  const directory = notif?.directory ?? [];
  const settings = notif?.settings;
  const quiet = settings?.quiet_hours;

  return html`
    <section class="convo" data-view="notifications-optin">
      <div class="convo-head">
        <div class="titles">
          <h2>My notifications</h2>
          <p class="dim small">Control your notifications, quiet hours, and channel subscriptions</p>
        </div>
      </div>
      <div class="view-pane">
        <div class="optin-card" data-card="channels">
          <h3>Channel notifications</h3>
          <p class="dim small">Notification mode for each of your joined channels.</p>
          <div class="ch-list">
            ${m.channels.map(
              (c) => html`
                <div class="ch" style="justify-content: space-between;">
                  <span>${channelTitle(c)}</span>
                  <span class="tag tag-plain">${c.tier === "ESCALATION" ? "All (escalation)" : "All"}</span>
                </div>
              `,
            )}
          </div>
        </div>

        <div class="optin-card" data-card="events">
          <h3>Events that reach me</h3>
          <p class="dim small">Choose which automated event alerts trigger in-app and push notifications.</p>
          <table class="data-table">
            <thead>
              <tr>
                <th>Event Class</th>
                <th>In app</th>
                <th>Push</th>
              </tr>
            </thead>
            <tbody>
              ${[
                { id: "orders_released_and_blocked", label: "Orders released and blocked" },
                { id: "stock_received", label: "Stock received" },
                { id: "credit_holds_and_credit_owed", label: "Credit holds and credit owed" },
                { id: "deliveries_scheduled", label: "Deliveries scheduled" },
                { id: "payments", label: "Payments" },
                { id: "approvals_waiting_on_me", label: "Approvals waiting on me" },
              ].map(
                (ev) => html`
                  <tr>
                    <td><strong>${ev.label}</strong></td>
                    <td><button type="button" class="check-btn" role="checkbox" aria-checked=${(settings?.event_classes[ev.id]?.in_app ?? true) ? "true" : "false"} data-action="toggle-event-notif" data-event=${ev.id} data-channel="in_app">${(settings?.event_classes[ev.id]?.in_app ?? true) ? "\u2713" : ""}</button></td>
                    <td><button type="button" class="check-btn" role="checkbox" aria-checked=${(settings?.event_classes[ev.id]?.push ?? false) ? "true" : "false"} data-action="toggle-event-notif" data-event=${ev.id} data-channel="push">${(settings?.event_classes[ev.id]?.push ?? false) ? "\u2713" : ""}</button></td>
                  </tr>
                `,
              )}
            </tbody>
          </table>

          <h3 style="margin-top: 16px;">Quiet hours</h3>
          <p class="dim small">Direct messages, forced acknowledgements, and escalations break through quiet hours.</p>
          <div class="toggle-row">
            <span class="grow">Enable quiet hours</span>
            <button type="button" class="switch" data-action="toggle-quiet-hours" aria-pressed=${quiet?.enabled ? "true" : "false"}>${quiet?.enabled ? "On" : "Off"}</button>
          </div>
          <div style="display: flex; gap: 16px; margin-top: 8px;">
            <div>
              <label class="dim small">Start time</label>
              <input type="time" value="20:00" data-value=${quiet?.start_time ?? "20:00"} data-field="quiet-start" />
            </div>
            <div>
              <label class="dim small">End time</label>
              <input type="time" value="07:00" data-value=${quiet?.end_time ?? "07:00"} data-field="quiet-end" />
            </div>
            <div>
              <label class="dim small">Timezone</label>
              <span class="dim small" style="display: block; padding-top: 8px;">${quiet?.timezone ?? "America/Los_Angeles"}</span>
            </div>
          </div>
        </div>

        <div class="optin-card" data-card="directory">
          <h3>Channel directory</h3>
          <p class="dim small">Browse and join open channels at your yard.</p>
          <table class="data-table">
            <thead>
              <tr>
                <th>Channel</th>
                <th>Members</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              ${directory.map(
                (dc) => html`
                  <tr>
                    <td><strong>${channelTitle({ id: dc.id, kind: "TEAM", slug: dc.slug, name: dc.name, topic: dc.topic, tier: "STANDARD", unread_count: 0, mentioned: false })}</strong></td>
                    <td>${dc.member_count} member${dc.member_count === 1 ? "" : "s"}</td>
                    <td>
                      ${dc.membership_source === "DERIVED"
                        ? html`<span class="tag tag-plain">Joined by rule, you stay</span>`
                        : dc.is_member
                          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="leave-channel" data-id=${dc.id}>Leave</button>`
                          : html`<button type="button" class="btn btn-primary small-btn" data-action="join-channel" data-id=${dc.id}>Join</button>`}
                    </td>
                  </tr>
                `,
              )}
            </tbody>
          </table>
        </div>
      </div>
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

  let centerContent: TemplateResult;
  if (m.mainView === "mentions") {
    centerContent = renderMentionsView(m);
  } else if (m.mainView === "saved") {
    centerContent = renderSavedView(m);
  } else if (m.mainView === "search") {
    centerContent = renderSearchView(m);
  } else if (m.mainView === "settings") {
    centerContent = renderSettingsView(m);
  } else if (m.mainView === "notifications-optin") {
    centerContent = renderNotificationsOptInView(m);
  } else if (m.rules !== null) {
    centerContent = renderRules(m.rules);
  } else if (m.convo === null) {
    centerContent = renderEmptyConvo();
  } else {
    centerContent = renderConvo(m, m.convo);
  }

  return html`
    <div class="${m.view === "conversation" ? "desk view-conv" : "desk view-list"}">
      ${renderStrip(m)}
      ${m.error === "" ? nothing : html`<div class="alert alert-error" role="alert" data-error>${m.error}</div>`}
      ${m.notice === "" ? nothing : html`<div class="alert alert-ok" role="status" data-notice>${m.notice}</div>`}
      ${m.bubble === null ? nothing : renderBubble(m.bubble)}
      <div class="panes ${m.convo?.openThreadId ? "has-thread" : ""}">
        ${renderRail(m)}
        ${centerContent}
        ${m.convo?.openThreadId ? renderThreadSidePanel(m, m.convo) : nothing}
      </div>
      ${renderNewMessageModal(m)}
      ${renderNewChannelModal(m)}
      ${renderAttachModal(m)}
    </div>
  `;
}
