// Yard Pick's own styles: one column on a phone, two on a tablet, gloves and
// sunlight. Every value is a token of the design system; nothing here is a
// literal colour. Targets are at least 56 px tall (the design system's own
// floor is 44), body text is never under 16 px, status is a word before it is
// a colour, and the page never scrolls sideways.
import { css } from "lit";

export const yardStyles = css`
  :host {
    display: block;
    min-height: 100vh;
    background: var(--hh-page);
    color: var(--hh-text);
    padding-bottom: calc(var(--hh-s-32) + var(--hh-safe-bottom));
  }
  main {
    max-width: 620px;
    margin: 0 auto;
    padding: var(--hh-s-16) var(--hh-s-16) 0;
    overflow-wrap: anywhere;
    font-size: 16px;
    line-height: 1.45;
  }
  /* A tablet reads the same screens wider: the board and the pick take the
     room without becoming a desk tool. */
  @media (min-width: 900px) {
    main {
      max-width: 920px;
      padding-top: var(--hh-s-24);
      font-size: 17px;
    }
  }
  h1 {
    margin: 0 0 var(--hh-s-12);
    font-size: 28px;
    line-height: 1.2;
    font-weight: 700;
  }
  h2, h3 { margin: 0; line-height: 1.25; }
  h3 { font-size: 16px; font-weight: 700; margin-bottom: var(--hh-s-8); }
  p { margin: 0 0 var(--hh-s-8); }
  .dim { color: var(--hh-text-dim); }
  .small { font-size: 14px; }
  .section-label {
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--hh-text-dim);
    font-size: 14px;
    font-weight: 700;
    margin: var(--hh-s-16) 0 var(--hh-s-8);
  }

  .card {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-16);
    margin-bottom: var(--hh-s-16);
  }

  /* The board. */
  .branch-row { display: flex; align-items: center; gap: var(--hh-s-8); margin-bottom: var(--hh-s-8); flex-wrap: wrap; }
  .count-row { color: var(--hh-text-dim); margin: 0 0 var(--hh-s-4); }
  .pick-grid { display: grid; grid-template-columns: 1fr; gap: var(--hh-s-12); }
  @media (min-width: 900px) {
    .pick-grid { grid-template-columns: 1fr 1fr; gap: var(--hh-s-16); }
  }
  .pick-card {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    border-left: 6px solid var(--hh-control-line);
    padding-left: var(--hh-s-16);
  }
  .pick-card.mine { border-left-color: var(--hh-state-good); }
  .pick-head { display: flex; justify-content: space-between; align-items: baseline; gap: var(--hh-s-8); flex-wrap: wrap; }
  .pick-no { font-size: 20px; font-weight: 700; }
  .pick-who { color: var(--hh-text-dim); }
  .pick-facts { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); color: var(--hh-text-dim); font-size: 14px; }
  .mine-tag { color: var(--hh-state-good); font-weight: 700; }
  .held {
    padding: var(--hh-s-8) var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-state-watch);
    color: var(--hh-state-watch);
    font-weight: 600;
  }

  .status {
    display: inline-block;
    padding: var(--hh-s-4) var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    border: 2px solid var(--hh-control-line);
    font-size: 14px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    white-space: nowrap;
  }
  .status-ready { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .status-active { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .status-stopped { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .status-done { color: var(--hh-text-dim); border-color: var(--hh-control-line); }

  .pill {
    display: inline-block;
    padding: var(--hh-s-4) var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    background: var(--hh-row-alt);
    font-size: 14px;
    font-weight: 600;
    line-height: 1.45;
  }
  .pill-watch { background: transparent; color: var(--hh-state-watch); border: 2px solid var(--hh-state-watch); }

  .empty-head { font-size: 20px; font-weight: 700; }

  /* A pick's detail. */
  .back {
    display: inline-flex;
    align-items: center;
    min-height: 56px;
    padding: 0 var(--hh-s-16);
    margin-bottom: var(--hh-s-8);
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-control-line);
    background: transparent;
    color: var(--hh-text);
    font: inherit;
    font-weight: 700;
    cursor: pointer;
  }
  .back:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .detail-head { display: flex; justify-content: space-between; align-items: center; gap: var(--hh-s-12); flex-wrap: wrap; }
  .detail-who { font-size: 20px; font-weight: 700; margin: 0; }
  .detail-grid { display: grid; grid-template-columns: 1fr; gap: var(--hh-s-16); }
  @media (min-width: 900px) {
    .detail-grid { grid-template-columns: 3fr 2fr; align-items: start; }
  }
  /* A grid or flex item defaults to min-width auto, so one wide child (an
     input's intrinsic size, a select's widest option) stretches the whole
     column past the frame: the round 2 sideways scroll. Every item may
     shrink; the fields inside keep their own floors. */
  .detail-grid > *,
  .pick-grid > *,
  .qty-row,
  .qty-quick {
    min-width: 0;
  }

  /* The pull list. */
  .lines { list-style: none; margin: 0; padding: 0; }
  .lines li { border-bottom: 1px solid var(--hh-line); }
  .lines li:last-child { border-bottom: 0; }
  .line-row {
    display: grid;
    grid-template-columns: 44px minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--hh-s-8);
    width: 100%;
    min-height: 72px;
    padding: var(--hh-s-8) 0;
    background: transparent;
    border: 0;
    border-radius: 0;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .line-row:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .line-check {
    width: 40px;
    height: 40px;
    border-radius: 50%;
    border: 3px solid var(--hh-control-line);
    display: grid;
    place-items: center;
    font-size: 20px;
    font-weight: 800;
  }
  .line-check.full { background: var(--brand-fill); border-color: var(--brand-fill-on); color: var(--brand-fill-on); }
  .line-check.short { border-color: var(--hh-state-watch); color: var(--hh-state-watch); }
  .line-main { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .line-desc { font-weight: 600; overflow-wrap: anywhere; }
  .line-sub { color: var(--hh-text-dim); font-size: 14px; overflow-wrap: anywhere; }
  .line-sub.backordered { color: var(--hh-state-watch); font-weight: 600; }
  .line-qty { font-weight: 700; white-space: nowrap; font-size: 18px; text-align: right; }
  .line-qty .picked-short { color: var(--hh-state-watch); }

  /* The quantity editor: big, one hand, gloves. */
  .qty-edit { display: flex; flex-direction: column; gap: var(--hh-s-12); padding: var(--hh-s-12) 0 var(--hh-s-4); }
  .qty-row { display: flex; align-items: center; gap: var(--hh-s-12); }
  .qty-field {
    flex: 1;
    min-width: 0;
    min-height: 72px;
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-control-line);
    background: var(--hh-field);
    color: var(--hh-text);
    font: inherit;
    font-size: 28px;
    font-weight: 700;
    text-align: center;
  }
  .qty-field:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .stepper {
    width: 72px;
    height: 72px;
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
    font-size: 30px;
    font-weight: 700;
    cursor: pointer;
  }
  .stepper:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .stepper:disabled { border: 2px solid var(--hh-control-line); color: var(--hh-text-dim); }
  .qty-quick { display: flex; gap: var(--hh-s-12); }
  .qty-quick .stepper { flex: 1; width: auto; font-size: 18px; }

  /* The skid stepper. */
  .skid-row { display: flex; align-items: center; gap: var(--hh-s-16); margin-bottom: var(--hh-s-12); min-width: 0; }
  .skid-count { font-size: 28px; font-weight: 800; flex: 1; text-align: center; overflow-wrap: anywhere; }
  .skid-count.unknown { font-size: 18px; font-weight: 700; color: var(--hh-text-dim); }

  .field-label { display: block; font-weight: 700; margin: var(--hh-s-12) 0 var(--hh-s-8); }
  .text-field {
    width: 100%;
    min-height: 56px;
    padding: 0 var(--hh-s-16);
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-control-line);
    background: var(--hh-field);
    color: var(--hh-text);
    font: inherit;
    font-size: 18px;
  }
  .text-field:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .text-area { padding: var(--hh-s-12) var(--hh-s-16); min-height: 96px; resize: vertical; }
  .hint { color: var(--hh-text-dim); margin: var(--hh-s-8) 0 var(--hh-s-12); }

  /* The board's reason rows: one big tappable row per reason, a radio circle
     at the left, the chosen one outlined. Words only, never a code. */
  .reason-rows { display: flex; flex-direction: column; gap: var(--hh-s-8); margin: 0 0 var(--hh-s-12); }
  .reason-row {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    min-height: 56px;
    padding: 0 var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
    font: inherit;
    font-size: 17px;
    font-weight: 600;
    text-align: left;
    cursor: pointer;
  }
  .reason-row:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .reason-row.chosen { border-color: var(--hh-state-watch); background: var(--hh-row-alt); }
  .reason-dot {
    width: 22px;
    height: 22px;
    border-radius: 50%;
    border: 3px solid var(--hh-control-line);
    flex: none;
  }
  .reason-row.chosen .reason-dot { border-color: var(--hh-state-watch); background: var(--hh-state-watch); }

  /* The short pick sheet's two labelled figures, one row, the short one in
     the watch colour: the board's ASKED and SHORT tiles. */
  .figure-tiles { display: grid; grid-template-columns: 1fr 1fr; gap: var(--hh-s-12); margin: var(--hh-s-8) 0 var(--hh-s-12); }
  .tile {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: var(--hh-s-8) var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-control-line);
  }
  .tile-label { font-size: 12px; font-weight: 700; letter-spacing: 0.06em; color: var(--hh-text-dim); }
  .tile-value { font-size: 20px; font-weight: 700; overflow-wrap: anywhere; }
  .tile-short { border-color: var(--hh-state-watch); }
  .tile-short .tile-value { color: var(--hh-state-watch); }

  /* The moves. Big, full width, one hand. */
  .actions { display: flex; flex-direction: column; gap: var(--hh-s-12); margin: var(--hh-s-12) 0 var(--hh-s-16); }
  button.big { display: block; width: 100%; min-height: 64px; font-size: 18px; font-weight: 700; }
  button.big:disabled { border: 2px solid var(--hh-control-line); }
  button.big + button.big { margin-top: var(--hh-s-12); }
  /* The door's words carry its meaning; the amber outline keeps the accent.
     The watch colour itself misses 4.5 to 1 on the light surfaces when it
     paints 18 px bold text, so the ink is the page's own. */
  .btn-door { background: transparent; color: var(--hh-text); border: 2px solid var(--hh-state-watch); }
  .btn-door:hover:not(:disabled) { background: var(--hh-row-alt); }

  /* Messages. */
  .alert { padding: var(--hh-s-12) var(--hh-s-16); border-radius: var(--hh-radius-control); border: 2px solid; font-weight: 600; }
  .alert-error { color: var(--hh-danger); border-color: var(--hh-danger); background: var(--hh-card); }
  .alert-ok { color: var(--hh-success); border-color: var(--hh-success); background: var(--hh-card); }

  /* The shortage sheet. */
  .sheet { border: 2px solid var(--hh-state-good); }
  .sheet-head { font-weight: 700; color: var(--hh-state-good); margin-bottom: var(--hh-s-8); }
  .sheet-banner {
    margin: var(--hh-s-8) 0 var(--hh-s-4);
    padding: var(--hh-s-8) var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    background: var(--hh-row-alt);
    border-left: 6px solid var(--hh-state-watch);
    font-weight: 600;
  }

  /* The shortages list. */
  .shortage-list { list-style: none; margin: 0; padding: 0; }
  .shortage-list li { padding: var(--hh-s-12) 0; border-bottom: 1px solid var(--hh-line); }
  .shortage-list li:last-child { border-bottom: 0; }
  .shortage-what { font-weight: 700; }
  .shortage-open { color: var(--hh-state-watch); font-weight: 700; }
  .shortage-cleared { color: var(--hh-success); font-weight: 700; }

  /* Staged. */
  .staged-box { border: 2px solid var(--hh-state-good); padding: var(--hh-s-12); border-radius: var(--hh-radius-control); }
  .staged-where { font-size: 20px; font-weight: 700; }
  .staged-ref { color: var(--hh-text-dim); }

  /* Every control this app draws sits at its own 56 px floor (the design
     system's own floor is 44). The shared header's Switch role and Sign out
     take the floor through its one host knob. */
  hos-app-header { --hh-header-control-min: 56px; }
  .theme-button { min-height: 56px; }
  main .btn { min-height: 56px; }
  .card .big + .big { margin-top: var(--hh-s-12); }
  .queue-footnote { margin-top: var(--hh-s-16); }
  .branch-pill { background: transparent; border: 2px solid var(--hh-control-line); }
  .pick-no, [data-pick-order] { font-variant-numeric: normal; font-feature-settings: normal; }
  .pick-head-pills { display: inline-flex; gap: var(--hh-s-8); align-items: center; flex-wrap: wrap; }
`;
