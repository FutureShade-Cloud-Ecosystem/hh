// House Messages' own styles. Every value is a token of the design system; nothing here is a
// literal colour. Status is a word before it is a colour, figures sit in tabular columns, a control
// is at least 44 pixels tall, and the page never scrolls sideways. One codebase, two widths: from
// 900 pixels the channel rail sits left of the conversation (the Work shell); under it the screen
// is one column and a channel replaces the list, with a Back control (the Field shell).
import { css } from "lit";

export const messagesStyles = css`
  .desk { max-width: 1440px; margin: 0 auto; padding: var(--hh-s-16) var(--hh-s-24) var(--hh-s-24); }
  h1 { margin: 0; font-size: 28px; line-height: 1.2; font-weight: 700; }
  h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; overflow-wrap: anywhere; }
  h3 { margin: 0; font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  h4 { margin: 0; font-size: 14px; font-weight: 700; }
  p { margin: 0 0 var(--hh-s-8); }
  ul { list-style: none; margin: 0; padding: 0; }
  .dim { color: var(--hh-text-dim); }
  /* Words are not columns of figures: the kit's tabular figure setting stretches a hyphen in a room's name. */
  .ch-name, .ch-line, h2, .text, .who, .why, .titles p, .excerpt { font-variant-numeric: normal; font-feature-settings: normal; }
  .small { font-size: 14px; }
  .code { font-family: var(--hh-font-mono); }
  .sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }

  /* Touch target rule: every interactive control is at least 44px tall */
  .desk button, .desk textarea {
    min-height: 44px;
    box-sizing: border-box;
  }

  /* The strip: how the screen is connected, and the unread total. */
  .strip { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-16); padding: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); min-height: 44px; }
  .strip .live { display: inline-flex; align-items: center; gap: var(--hh-s-8); font-weight: 600; }
  .dot { width: 10px; height: 10px; border-radius: var(--hh-radius-pill); background: var(--hh-text-dim); flex: none; }
  .dot-live { background: var(--hh-state-good); }
  .dot-watch { background: var(--hh-state-watch); }
  .dot-stop { background: var(--hh-state-stop); }
  .strip .spacer { flex: 1; }
  .strip-warn { border-inline-start: 4px solid var(--hh-state-watch); }
  .strip-stop { border-inline-start: 4px solid var(--hh-state-stop); }

  .alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-12); border: 1px solid var(--hh-line); background: var(--hh-card); }
  .alert-error { border-color: var(--hh-state-stop); border-inline-start-width: 4px; }
  .alert-ok { border-color: var(--hh-state-good); border-inline-start-width: 4px; }

  /* The forced acknowledgement bubble: it persists until the person acknowledges. */
  .bubble { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-12) var(--hh-s-16); padding: var(--hh-s-16); margin-bottom: var(--hh-s-12); background: var(--hh-card); border: 1px solid var(--hh-state-stop); border-inline-start: 4px solid var(--hh-state-stop); border-radius: var(--hh-radius-card); }
  .bubble .body { flex: 1 1 280px; min-width: 0; }
  .eyebrow { margin: 0 0 var(--hh-s-4); font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; font-weight: 700; color: var(--hh-state-stop); }
  .bubble .text { margin: 0 0 var(--hh-s-4); font-weight: 600; overflow-wrap: anywhere; }

  /* The panes layout */
  .panes { display: grid; grid-template-columns: minmax(280px, 340px) minmax(0, 1fr); gap: var(--hh-s-16); align-items: start; }
  .rail, .convo, .thread-panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); min-width: 0; }
  .rail { padding: var(--hh-s-12); position: sticky; top: var(--hh-s-8); max-height: calc(100vh - var(--hh-s-24)); overflow-y: auto; }

  /* Rail Top: + New button and popover menu */
  .rail-top { position: relative; margin-bottom: var(--hh-s-12); }
  .btn-new { width: 100%; min-height: 44px; font-weight: 700; font-size: 15px; border-radius: var(--hh-radius-control); }
  .new-menu-popover { position: absolute; top: calc(100% + 4px); left: 0; right: 0; background: var(--hh-card); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15); z-index: 50; display: flex; flex-direction: column; overflow: hidden; }
  .menu-item { width: 100%; min-height: 44px; padding: 0 var(--hh-s-16); text-align: start; background: transparent; border: none; border-bottom: 1px solid var(--hh-line); color: var(--hh-text); font: inherit; font-size: 14px; cursor: pointer; display: flex; align-items: center; }
  .menu-item:last-child { border-bottom: none; }
  .menu-item:hover { background: var(--hh-row-alt); }

  /* Rail Search */
  .search-wrap { margin-bottom: var(--hh-s-12); }
  .search-input { width: 100%; min-height: 44px; padding: 0 var(--hh-s-12); border-radius: var(--hh-radius-control); border: 1px solid var(--hh-control-line); background: var(--hh-field); color: var(--hh-text); font: inherit; font-size: 14px; box-sizing: border-box; }

  /* Rail Nav Links (Mentions, Saved) */
  .nav-links { display: flex; flex-direction: column; gap: var(--hh-s-4); margin-bottom: var(--hh-s-12); }
  .nav-row { display: flex; align-items: center; gap: var(--hh-s-8); width: 100%; min-height: 44px; padding: 0 var(--hh-s-12); background: transparent; border: none; border-radius: var(--hh-radius-control); color: var(--hh-text); font: inherit; font-size: 14px; cursor: pointer; text-align: start; }
  .nav-row:hover { background: var(--hh-row-alt); }
  .nav-icon { font-weight: 700; width: 20px; text-align: center; }

  /* Rail Section Headers */
  .section-head { display: flex; align-items: center; justify-content: space-between; padding: var(--hh-s-8) var(--hh-s-8) var(--hh-s-4); }
  .section-title { margin: 0; font-size: 11px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  .btn-icon-plus { min-height: 44px; width: 44px; display: inline-flex; align-items: center; justify-content: center; background: transparent; border: none; color: var(--hh-text-dim); font-size: 18px; font-weight: 700; cursor: pointer; border-radius: var(--hh-radius-control); }
  .btn-icon-plus:hover { background: var(--hh-row-alt); color: var(--hh-text); }

  /* Rail Channel Groups & Items */
  .ch-group { margin-bottom: var(--hh-s-8); }
  .group-title { font-size: 11px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; margin: var(--hh-s-6) var(--hh-s-8) var(--hh-s-2); }
  .ch-list { display: grid; gap: var(--hh-s-2); }
  .ch { display: flex; align-items: center; gap: var(--hh-s-8); width: 100%; min-height: 56px; padding: var(--hh-s-8) var(--hh-s-12); text-align: start; background: transparent; color: var(--hh-text); border: 1px solid transparent; border-radius: var(--hh-radius-control); font: inherit; cursor: pointer; }
  .ch:hover { background: var(--hh-row-alt); }
  .ch[aria-current="true"] { background: var(--hh-row-alt); border-color: var(--hh-control-line); }
  .ch-prefix { font-weight: 700; color: var(--hh-text-dim); margin-right: var(--hh-s-4); font-size: 16px; flex: none; }
  .ch-text { flex: 1 1 auto; min-width: 0; display: grid; gap: var(--hh-s-2); }
  .ch-name { font-weight: 600; overflow-wrap: anywhere; }
  .ch-line { font-size: 13px; color: var(--hh-text-dim); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ch.has-unread .ch-name { font-weight: 700; }
  .badge { flex: none; min-width: 26px; padding: 2px var(--hh-s-8); border-radius: var(--hh-radius-pill); background: var(--hh-text); color: var(--hh-page); font-size: 13px; font-weight: 700; text-align: center; font-variant-numeric: tabular-nums; }
  .badge-mention { background: var(--hh-state-stop); color: var(--hh-page); }

  /* Avatars */
  .avatar { width: 32px; height: 32px; border-radius: var(--hh-radius-pill); background: var(--hh-row-alt); color: var(--hh-text); font-weight: 700; font-size: 12px; display: inline-flex; align-items: center; justify-content: center; flex: none; border: 1px solid var(--hh-control-line); }
  .avatar-sm { width: 24px; height: 24px; font-size: 10px; }
  .icon-avatar { width: 32px; height: 32px; border-radius: var(--hh-radius-pill); background: var(--hh-row-alt); color: var(--hh-text); display: inline-flex; align-items: center; justify-content: center; flex: none; border: 1px solid var(--hh-control-line); }

  /* Rail Footer */
  .rail-foot { padding: var(--hh-s-8) var(--hh-s-8) 0; border-top: 1px solid var(--hh-line); margin-top: var(--hh-s-8); display: flex; flex-direction: column; gap: var(--hh-s-4); }
  .btn-settings { display: flex; align-items: center; gap: var(--hh-s-8); width: 100%; min-height: 44px; padding: 0 var(--hh-s-12); background: transparent; border: none; border-radius: var(--hh-radius-control); color: var(--hh-text); font: inherit; font-size: 14px; cursor: pointer; text-align: start; font-weight: 600; }
  .btn-settings:hover { background: var(--hh-row-alt); }
  .link { background: transparent; border: 0; padding: 0 var(--hh-s-4); min-height: 44px; color: var(--hh-accent-text); font-weight: 700; text-decoration: underline; cursor: pointer; font: inherit; }
  .link:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }

  /* The Conversation Pane */
  .convo { display: flex; flex-direction: column; min-height: 480px; }
  .convo-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-12); padding: var(--hh-s-12) var(--hh-s-16); border-bottom: 1px solid var(--hh-line); }
  .convo-head .titles { flex: 1 1 200px; min-width: 0; }
  .channel-title-row { display: flex; align-items: baseline; gap: var(--hh-s-4); }
  .hash { font-size: 20px; font-weight: 700; color: var(--hh-text-dim); }
  .channel-topic { margin-top: var(--hh-s-2); }
  .back { display: none; }
  .tier { display: inline-flex; align-items: center; padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 12px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; border: 1px solid var(--hh-state-watch); color: var(--hh-state-watch); white-space: nowrap; }

  /* Tabs */
  .tab-strip { display: flex; align-items: center; gap: var(--hh-s-4); }
  .tab { min-height: 44px; padding: 0 var(--hh-s-12); background: transparent; border: none; border-bottom: 2px solid transparent; color: var(--hh-text-dim); font: inherit; font-weight: 600; font-size: 14px; cursor: pointer; }
  .tab:hover { color: var(--hh-text); }
  .tab.active { color: var(--hh-text); border-bottom-color: var(--hh-accent-text, #0284c7); font-weight: 700; }
  .btn-notify { min-height: 44px; }

  /* Convo Body & Stream */
  .convo-body { padding: var(--hh-s-12) var(--hh-s-16); display: grid; gap: var(--hh-s-12); flex: 1; }
  .empty { padding: var(--hh-s-48) var(--hh-s-24); text-align: center; color: var(--hh-text-dim); }
  .empty strong { display: block; color: var(--hh-text); font-size: 18px; margin-bottom: var(--hh-s-4); }

  /* Day Separator */
  .day-separator { display: flex; align-items: center; justify-content: center; position: relative; margin: var(--hh-s-16) 0; }
  .day-separator::before { content: ""; position: absolute; left: 0; right: 0; top: 50%; height: 1px; background: var(--hh-line); z-index: 1; }
  .day-pill { position: relative; z-index: 2; background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-pill); padding: 2px var(--hh-s-12); font-size: 12px; font-weight: 600; color: var(--hh-text-dim); }

  /* Stream & Messages */
  .stream { display: grid; gap: var(--hh-s-8); }
  .msg { padding: var(--hh-s-8) var(--hh-s-12); border-radius: var(--hh-radius-control); border: 1px solid transparent; min-width: 0; }
  .msg.mine { background: var(--hh-row-alt); }
  .msg.event { border: 1px dashed var(--hh-control-line); background: var(--hh-field); }
  .msg.superseded { opacity: 0.7; }
  .msg-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); margin-bottom: var(--hh-s-4); }
  .who { font-weight: 700; }
  .when { font-size: 13px; color: var(--hh-text-dim); font-variant-numeric: tabular-nums; }
  .kind { font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  .text { white-space: pre-wrap; overflow-wrap: anywhere; margin: 0; }
  .why { margin: var(--hh-s-4) 0 0; font-size: 13px; color: var(--hh-text-dim); }
  .mention-chip { display: inline; background: var(--hh-row-alt); color: var(--hh-accent-text, #0284c7); font-weight: 700; padding: 1px var(--hh-s-4); border-radius: var(--hh-radius-control); border: 1px solid var(--hh-line); }
  .edited-marker { font-style: italic; }
  .event-actions { margin-top: var(--hh-s-6); }
  .btn-action-pill { min-height: 44px; font-weight: 600; }
  .msg-foot { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); margin-top: var(--hh-s-6); }
  .btn-replies { min-height: 44px; }
  .btn-open-thread { min-height: 44px; }
  .thread { margin: var(--hh-s-8) 0 0 var(--hh-s-16); padding-inline-start: var(--hh-s-12); border-inline-start: 2px solid var(--hh-line); display: grid; gap: var(--hh-s-8); }
  .tag { display: inline-flex; align-items: center; padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 12px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; border: 1px solid var(--hh-line); white-space: nowrap; }
  .tag-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .tag-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .tag-plain { color: var(--hh-text-dim); }

  /* Composer */
  .composer { padding: var(--hh-s-12) var(--hh-s-16) var(--hh-s-16); border-top: 1px solid var(--hh-line); display: grid; gap: var(--hh-s-8); }
  .quick-replies { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); }
  .chip-quick { min-height: 44px; border-radius: var(--hh-radius-pill); padding: 0 var(--hh-s-16); font-size: 13px; font-weight: 600; }
  .composer-box { border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); background: var(--hh-field); overflow: hidden; display: flex; flex-direction: column; }
  .composer-box textarea { font: inherit; font-size: 15px; width: 100%; min-height: 88px; max-height: 40vh; resize: vertical; padding: var(--hh-s-12); border: none; background: transparent; color: var(--hh-text); box-sizing: border-box; }
  .composer-box textarea:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }
  .composer-actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-8) var(--hh-s-12); border-top: 1px solid var(--hh-line); }
  .btn-attach { min-height: 44px; width: 44px; padding: 0; display: inline-flex; align-items: center; justify-content: center; }
  .btn-send { min-height: 44px; padding: 0 var(--hh-s-20); font-weight: 700; }
  .composer-actions .spacer { flex: 1; }
  .replying { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-8) var(--hh-s-12); background: var(--hh-row-alt); border-radius: var(--hh-radius-control); }
  .replying .excerpt { flex: 1 1 160px; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .words-bad { color: var(--hh-state-stop); font-weight: 600; }

  /* Thread Side Panel */
  .thread-panel { display: flex; flex-direction: column; min-height: 480px; position: sticky; top: var(--hh-s-8); max-height: calc(100vh - var(--hh-s-24)); overflow: hidden; }
  .thread-head { display: flex; align-items: center; justify-content: space-between; padding: var(--hh-s-12) var(--hh-s-16); border-bottom: 1px solid var(--hh-line); }
  .thread-head h3 { margin: 0; font-size: 16px; font-weight: 700; text-transform: none; letter-spacing: normal; color: var(--hh-text); }
  .btn-close { min-height: 44px; width: 44px; background: transparent; border: none; font-size: 24px; line-height: 1; color: var(--hh-text-dim); cursor: pointer; display: inline-flex; align-items: center; justify-content: center; border-radius: var(--hh-radius-control); }
  .btn-close:hover { background: var(--hh-row-alt); color: var(--hh-text); }
  .thread-body { padding: var(--hh-s-12) var(--hh-s-16); flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: var(--hh-s-12); }
  .thread-root { padding: var(--hh-s-8) var(--hh-s-12); background: var(--hh-row-alt); border-radius: var(--hh-radius-control); }
  .thread-divider { display: flex; align-items: center; justify-content: center; position: relative; margin: var(--hh-s-8) 0; }
  .thread-divider::before { content: ""; position: absolute; left: 0; right: 0; top: 50%; height: 1px; background: var(--hh-line); z-index: 1; }
  .thread-divider span { position: relative; z-index: 2; background: var(--hh-card); padding: 0 var(--hh-s-8); font-size: 12px; font-weight: 600; color: var(--hh-text-dim); }
  .thread-replies { display: flex; flex-direction: column; gap: var(--hh-s-8); }
  .thread-reply { padding: var(--hh-s-8) var(--hh-s-12); border-radius: var(--hh-radius-control); border: 1px solid transparent; }
  .thread-reply:hover { background: var(--hh-row-alt); }
  .thread-composer { padding: var(--hh-s-12) var(--hh-s-16); border-top: 1px solid var(--hh-line); display: grid; gap: var(--hh-s-8); }
  .thread-composer textarea { width: 100%; min-height: 60px; padding: var(--hh-s-8); font: inherit; font-size: 14px; border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); background: var(--hh-field); color: var(--hh-text); box-sizing: border-box; }
  .thread-composer-row { display: flex; align-items: center; }
  .thread-composer-row .spacer { flex: 1; }

  /* The house queue */
  .queue { background: var(--hh-row-alt); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-12) var(--hh-s-16); }
  .queue-row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-16); padding: var(--hh-s-8) 0; border-top: 1px solid var(--hh-line); }
  .queue-row:first-of-type { border-top: 0; }
  .queue-row .who-line { flex: 1 1 200px; min-width: 0; }
  .queue-row .who-line .dim { display: block; }
  .queue p { margin-top: var(--hh-s-8); }
  .queue-row.open { border-inline-start: 3px solid var(--hh-state-watch); padding-inline-start: var(--hh-s-12); }

  /* The inline sheet: notify prefs and payment notice rules */
  .sheet { background: var(--hh-row-alt); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-16); display: grid; gap: var(--hh-s-12); }
  .sheet-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8); }
  .segs { display: flex; flex-wrap: wrap; gap: var(--hh-s-4); padding: var(--hh-s-4); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-card); background: var(--hh-card); width: fit-content; max-width: 100%; }
  .seg { min-height: 44px; padding: 0 var(--hh-s-16); white-space: nowrap; border-radius: var(--hh-radius-control); background: transparent; color: var(--hh-text); border: 1px solid transparent; }
  .seg[aria-pressed="true"] { background: var(--hh-text); color: var(--hh-page); font-weight: 700; }
  .toggle-row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-16); }
  .toggle-row .grow { flex: 1 1 220px; min-width: 0; }
  .switch { min-width: 76px; min-height: 44px; padding: 0 var(--hh-s-16); border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); font-weight: 600; }
  .switch[aria-pressed="true"] { background: var(--hh-text); color: var(--hh-page); font-weight: 700; }

  .theme-btn { font: inherit; font-size: 13px; padding: 7px 12px; min-height: 0; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); cursor: pointer; }
  .theme-btn:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }

  /* Desktop layout with thread open: 3 columns */
  @media (min-width: 1181px) {
    .panes.has-thread {
      grid-template-columns: minmax(240px, 280px) minmax(0, 1fr) minmax(320px, 380px);
    }
  }

  /* Tablet layout: thread open collapses the sidebar */
  @media (max-width: 1180px) and (min-width: 900px) {
    .panes.has-thread .rail {
      display: none !important;
    }
    .panes.has-thread {
      grid-template-columns: minmax(0, 1fr) minmax(320px, 400px);
    }
  }

  /* Mobile layout: single column */
  @media (max-width: 899px) {
    .panes { grid-template-columns: minmax(0, 1fr); }
    .panes.has-thread .rail { display: none !important; }
    .panes.has-thread .convo { display: none; }
    .panes.has-thread .thread-panel { display: flex; }
    .rail { position: static; max-height: none; }
    .view-conv .rail { display: none; }
    .view-list .convo { display: none; }
    .back { display: inline-flex; }
    .convo { min-height: 0; }
  }

  @media (max-width: 520px) {
    .desk { padding: var(--hh-s-12) var(--hh-s-12) var(--hh-s-24); }
    h1 { font-size: 24px; }
    .convo-body, .composer, .convo-head { padding-inline: var(--hh-s-12); }
    .bubble .btn { width: 100%; }
    .segs { display: grid; width: 100%; grid-template-columns: minmax(0, 1fr); }
    .seg { width: 100%; text-align: start; }
    .composer-actions .btn-send { flex: 1 1 100%; }
    .thread { margin-inline-start: var(--hh-s-8); }
  }
`;
