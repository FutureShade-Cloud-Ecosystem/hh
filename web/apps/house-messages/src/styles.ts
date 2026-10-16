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

  /* The two panes. */
  .panes { display: grid; grid-template-columns: minmax(280px, 340px) minmax(0, 1fr); gap: var(--hh-s-16); align-items: start; }
  .rail, .convo { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); min-width: 0; }
  .rail { padding: var(--hh-s-12); position: sticky; top: var(--hh-s-8); max-height: calc(100vh - var(--hh-s-24)); overflow-y: auto; }
  .rail-head { display: flex; align-items: baseline; justify-content: space-between; gap: var(--hh-s-8); padding: var(--hh-s-4) var(--hh-s-8) var(--hh-s-8); }
  .group { margin-bottom: var(--hh-s-12); }
  .group h3 { padding: var(--hh-s-8) var(--hh-s-8) var(--hh-s-4); }
  .ch { display: flex; align-items: center; gap: var(--hh-s-8); width: 100%; min-height: 56px; padding: var(--hh-s-8) var(--hh-s-12); text-align: start; background: transparent; color: var(--hh-text); border: 1px solid transparent; border-radius: var(--hh-radius-control); font: inherit; cursor: pointer; }
  .ch:hover { background: var(--hh-row-alt); }
  .ch[aria-current="true"] { background: var(--hh-row-alt); border-color: var(--hh-control-line); }
  .ch-text { flex: 1 1 auto; min-width: 0; display: grid; gap: var(--hh-s-2); }
  .ch-name { font-weight: 600; overflow-wrap: anywhere; }
  .ch-line { font-size: 13px; color: var(--hh-text-dim); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ch.has-unread .ch-name { font-weight: 700; }
  .badge { flex: none; min-width: 26px; padding: 2px var(--hh-s-8); border-radius: var(--hh-radius-pill); background: var(--hh-text); color: var(--hh-page); font-size: 13px; font-weight: 700; text-align: center; font-variant-numeric: tabular-nums; }
  .badge-mention { background: var(--hh-state-stop); color: var(--hh-page); }
  .rail-foot { padding: var(--hh-s-8) var(--hh-s-8) 0; border-top: 1px solid var(--hh-line); margin-top: var(--hh-s-4); }
  .link { background: transparent; border: 0; padding: 0 var(--hh-s-4); min-height: 44px; color: var(--hh-accent-text); font-weight: 700; text-decoration: underline; cursor: pointer; font: inherit; font-weight: 700; }
  .link:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }

  .convo { display: flex; flex-direction: column; min-height: 480px; }
  .convo-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-12); padding: var(--hh-s-12) var(--hh-s-16); border-bottom: 1px solid var(--hh-line); }
  .convo-head .titles { flex: 1 1 200px; min-width: 0; }
  .back { display: none; }
  .tier { display: inline-flex; align-items: center; padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 12px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; border: 1px solid var(--hh-state-watch); color: var(--hh-state-watch); white-space: nowrap; }
  .convo-body { padding: var(--hh-s-12) var(--hh-s-16); display: grid; gap: var(--hh-s-12); }
  .empty { padding: var(--hh-s-48) var(--hh-s-24); text-align: center; color: var(--hh-text-dim); }
  .empty strong { display: block; color: var(--hh-text); font-size: 18px; margin-bottom: var(--hh-s-4); }

  .stream { display: grid; gap: var(--hh-s-8); }
  .msg { padding: var(--hh-s-8) var(--hh-s-12); border-radius: var(--hh-radius-control); border: 1px solid transparent; min-width: 0; }
  .msg.mine { background: var(--hh-row-alt); }
  .msg.event { border: 1px dashed var(--hh-control-line); }
  .msg.superseded { opacity: 0.7; }
  .msg-head { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--hh-s-4) var(--hh-s-12); margin-bottom: var(--hh-s-2); }
  .who { font-weight: 700; }
  .when { font-size: 13px; color: var(--hh-text-dim); font-variant-numeric: tabular-nums; }
  .kind { font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  .text { white-space: pre-wrap; overflow-wrap: anywhere; margin: 0; }
  .why { margin: var(--hh-s-4) 0 0; font-size: 13px; color: var(--hh-text-dim); }
  .msg-foot { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-4) var(--hh-s-12); margin-top: var(--hh-s-4); }
  .thread { margin: var(--hh-s-8) 0 0 var(--hh-s-16); padding-inline-start: var(--hh-s-12); border-inline-start: 2px solid var(--hh-line); display: grid; gap: var(--hh-s-8); }
  .tag { display: inline-flex; align-items: center; padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 12px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; border: 1px solid var(--hh-line); white-space: nowrap; }
  .tag-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .tag-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .tag-plain { color: var(--hh-text-dim); }

  /* The composer. */
  .composer { padding: var(--hh-s-12) var(--hh-s-16) var(--hh-s-16); border-top: 1px solid var(--hh-line); display: grid; gap: var(--hh-s-8); }
  .composer textarea { font: inherit; font-size: 16px; width: 100%; min-height: 88px; max-height: 40vh; resize: vertical; padding: var(--hh-s-12); border-radius: var(--hh-radius-control); border: 1px solid var(--hh-control-line); background: var(--hh-field); color: var(--hh-text); }
  .composer textarea:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .composer-row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-16); }
  .composer-row .spacer { flex: 1; }
  .replying { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-8) var(--hh-s-12); background: var(--hh-row-alt); border-radius: var(--hh-radius-control); }
  .replying .excerpt { flex: 1 1 160px; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .words-bad { color: var(--hh-state-stop); font-weight: 600; }
  .btn { white-space: nowrap; min-height: 44px; }
  .small-btn { min-height: 44px; padding: 0 var(--hh-s-12); font-size: 14px; white-space: nowrap; }
  .btn:disabled, .small-btn:disabled { background: var(--hh-disabled-fill); color: var(--hh-disabled-ink); border-color: var(--hh-disabled-line); cursor: default; }

  /* The house queue. */
  .queue { background: var(--hh-row-alt); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-12) var(--hh-s-16); }
  .queue-row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-16); padding: var(--hh-s-8) 0; border-top: 1px solid var(--hh-line); }
  .queue-row:first-of-type { border-top: 0; }
  .queue-row .who-line { flex: 1 1 200px; min-width: 0; }
  .queue-row .who-line .dim { display: block; }
  .queue p { margin-top: var(--hh-s-8); }
  .queue-row.open { border-inline-start: 3px solid var(--hh-state-watch); padding-inline-start: var(--hh-s-12); }

  /* The inline sheet: notify prefs and payment notice rules. */
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

  /* One column under 900 pixels: the list or a channel, never both. */
  @media (max-width: 899px) {
    .panes { grid-template-columns: minmax(0, 1fr); }
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
    .composer-row .btn { flex: 1 1 100%; }
    .thread { margin-inline-start: var(--hh-s-8); }
  }
`;
