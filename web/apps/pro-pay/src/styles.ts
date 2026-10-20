// Pro Pay's screen styles: tokens only (no hex, no brand colour of its own; the dealer's gold rides .btn-primary).
// Phone first at 390; the sheet is a bottom sheet on a phone and a centred card from tablet up.
import { css } from "lit";

export const screenStyles = css`
  :host { display: block; min-height: 100vh; }
  h1 { margin: 0 0 var(--hh-s-12); font-size: 1.25rem; }
  h2 { margin: 0; font-size: 1.125rem; }
  h3 { margin: var(--hh-s-20) 0 var(--hh-s-8); font-size: 0.9375rem; }
  p { margin: 0; }
  .screen { padding-bottom: 96px; max-width: 760px; }
  .quiet { color: var(--hh-text-dim); font-size: 0.875rem; }
  .fine { font-size: 0.8125rem; margin-top: var(--hh-s-8); }
  .late { color: var(--hh-danger); font-size: 0.875rem; }
  .ok { color: var(--hh-success); font-size: 0.875rem; margin-top: var(--hh-s-8); }
  .label { color: var(--hh-text-dim); font-size: 0.875rem; }
  /* The board's primary action is the dealer's gold with its own ink (HH: Pro, pay-hosted-phone-light), as the kit's sign in does. */
  .btn-primary { background: var(--hh-accent); color: var(--hh-accent-ink); border-color: var(--hh-accent); font-weight: 600; }
  .btn-primary:hover { background: var(--hh-accent-hover); }
  .btn-primary:disabled { background: var(--hh-disabled-fill); color: var(--hh-disabled-ink); border-color: var(--hh-disabled-line); }
  .big { font-size: 2rem; font-weight: 700; line-height: 1.2; }

  /* Pay home */
  .owed { display: flex; flex-direction: column; gap: var(--hh-s-4); margin-bottom: var(--hh-s-16); }
  .tiles { list-style: none; margin: var(--hh-s-12) 0 0; padding: 0; display: grid; grid-template-columns: repeat(2, 1fr); gap: var(--hh-s-8); }
  .tile { display: flex; flex-direction: column; gap: var(--hh-s-2); padding: var(--hh-s-8) var(--hh-s-12); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); background: var(--hh-card); }
  .tile-label { color: var(--hh-text-dim); font-size: 0.75rem; }
  .tile-sum { font-weight: 600; }
  .notice { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-12); padding: var(--hh-s-12) var(--hh-s-16); margin-bottom: var(--hh-s-12); border: 1px solid var(--hh-state-watch); border-radius: var(--hh-radius-card); background: var(--hh-card); }
  .notice-line { color: var(--hh-text); border-left: 3px solid var(--hh-state-watch); padding-left: var(--hh-s-8); font-size: 0.875rem; margin-top: var(--hh-s-8); }
  .pickbar { display: flex; align-items: center; justify-content: space-between; padding: var(--hh-s-8) 0; font-size: 0.875rem; color: var(--hh-text-dim); }
  .pickbar .btn { min-height: var(--hh-h-input); padding: 0 var(--hh-s-12); font-size: 0.875rem; }
  ul.invoices, ul.methods, ul.payments, ul.lines { list-style: none; margin: 0; padding: 0; border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); overflow: hidden; background: var(--hh-card); }
  li.inv { display: flex; align-items: center; gap: var(--hh-s-12); padding: var(--hh-s-12) var(--hh-s-16); border-bottom: 1px solid var(--hh-line); background: var(--hh-row); cursor: pointer; }
  li.inv:nth-child(even) { background: var(--hh-row-alt); }
  li.inv:last-child, li.method:last-child, li.pay-row:last-child, li.line:last-child { border-bottom: 0; }
  li.inv.is-on { outline: 2px solid var(--hh-accent); outline-offset: -2px; }
  .inv-main { flex: 1; min-width: 0; display: flex; flex-direction: column; align-items: flex-start; justify-content: center; gap: var(--hh-s-4); padding: 0; min-height: var(--hh-h-input); border: 0; background: transparent; color: inherit; text-align: left; font: inherit; }
  .inv-top { display: flex; align-items: center; gap: var(--hh-s-8); flex-wrap: wrap; }
  .inv-no { font-weight: 600; }
  .inv-open { min-height: var(--hh-h-input); min-width: var(--hh-h-input); margin: -10px 0; padding: 0; border: 0; background: transparent; color: inherit; font: inherit; font-weight: 600; display: inline-flex; align-items: center; cursor: pointer; }
  .inv-sub { position: relative; font-size: 0.8125rem; color: var(--hh-text-dim); }
  .inv-sub.late { color: var(--hh-danger); }
  .inv-amount { display: flex; flex-direction: column; align-items: flex-end; font-weight: 600; }
  .inv-of { font-weight: 400; font-size: 0.75rem; color: var(--hh-text-dim); }
  .box-gap { width: 24px; flex: none; }
  .box-hit { flex: none; width: var(--hh-h-input); height: var(--hh-h-input); min-height: var(--hh-h-input); margin: -10px; padding: 0; border: 0; background: transparent; display: inline-flex; align-items: center; justify-content: center; cursor: pointer; }
  .box-hit:focus-visible { outline: none; }
  .box-hit:focus-visible > .box, .inv-open:focus-visible { outline: 2px solid var(--hh-focus); outline-offset: 2px; border-radius: var(--hh-radius-control); }
  .box { flex: none; width: 24px; height: 24px; padding: 0; min-height: 24px; border: 2px solid var(--hh-control-line); border-radius: var(--hh-radius-control); background: var(--hh-field); display: inline-flex; align-items: center; justify-content: center; cursor: pointer; }
  .box[aria-checked="true"], [aria-checked="true"] > .box { background: var(--hh-accent); border-color: var(--hh-accent); }
  .box .mark { width: 10px; height: 6px; border-left: 2px solid var(--hh-accent-ink); border-bottom: 2px solid var(--hh-accent-ink); transform: rotate(-45deg) translate(1px, -1px); opacity: 0; }
  .box[aria-checked="true"] .mark, [aria-checked="true"] > .box .mark { opacity: 1; }
  .box-line { display: inline-flex; align-items: center; gap: var(--hh-s-8); min-height: 44px; padding: 0; border: 0; background: transparent; color: inherit; font: inherit; cursor: pointer; }
  .paybar { position: fixed; left: 0; right: 0; bottom: 0; padding: var(--hh-s-12) var(--hh-page-gutter, var(--hh-s-16)); padding-bottom: calc(var(--hh-s-12) + var(--hh-safe-bottom, 0px)); background: var(--hh-page); border-top: 1px solid var(--hh-line); z-index: var(--hh-z-nav, 10); }
  .paybtn { width: 100%; display: flex; align-items: center; justify-content: space-between; gap: var(--hh-s-12); }
  .paybar .paybtn { max-width: 760px; }

  /* Invoice detail */
  .back { margin-bottom: var(--hh-s-12); }
  .detail-head { display: flex; align-items: center; gap: var(--hh-s-12); flex-wrap: wrap; margin-bottom: var(--hh-s-4); }
  li.line { display: grid; grid-template-columns: 1fr auto auto; gap: var(--hh-s-12); padding: var(--hh-s-8) var(--hh-s-16); border-bottom: 1px solid var(--hh-line); font-size: 0.9375rem; }
  .line-qty { color: var(--hh-text-dim); }
  .sums { margin: var(--hh-s-12) 0 0; display: flex; flex-direction: column; gap: var(--hh-s-4); }
  .sums > div { display: flex; justify-content: space-between; }
  .sums dt, .sums dd { margin: 0; }
  .sum-total { font-weight: 700; }
  .detail-pay { margin-top: var(--hh-s-20); width: 100%; }

  /* The sheet */
  .scrim { position: fixed; inset: 0; background: var(--hh-scrim); z-index: var(--hh-z-overlay, 20); }
  .sheet { position: fixed; left: 0; right: 0; bottom: 0; max-height: 92vh; overflow: auto; box-sizing: border-box; padding: var(--hh-s-8) var(--hh-s-16) calc(var(--hh-s-24) + var(--hh-safe-bottom, 0px)); background: var(--hh-card); color: var(--hh-text); border-radius: var(--hh-radius-card) var(--hh-radius-card) 0 0; box-shadow: var(--hh-elev-3); z-index: var(--hh-z-modal, 30); outline: none; display: flex; flex-direction: column; gap: var(--hh-s-8); }
  .grab { align-self: center; width: 36px; height: 4px; border-radius: var(--hh-radius-pill); background: var(--hh-line); margin-bottom: var(--hh-s-4); }
  .sheet-close { align-self: flex-end; min-height: 36px; padding: 0 var(--hh-s-12); font-size: 0.875rem; }
  .sheet-title { margin-top: var(--hh-s-4); }
  .sandbox { font-size: 0.8125rem; color: var(--hh-text-dim); border: 1px dashed var(--hh-line); border-radius: var(--hh-radius-control); padding: var(--hh-s-4) var(--hh-s-8); align-self: flex-start; }
  .opts { display: flex; flex-direction: column; gap: var(--hh-s-8); }
  .opt { display: flex; align-items: center; gap: var(--hh-s-12); width: 100%; min-height: 56px; padding: var(--hh-s-8) var(--hh-s-12); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); background: var(--hh-card); color: inherit; font: inherit; text-align: left; cursor: pointer; }
  .opt-add { border-style: dashed; }
  .opt.is-on { border: 2px solid var(--hh-accent); background: var(--hh-row-alt); }
  .opt:disabled { cursor: default; opacity: 1; color: var(--hh-disabled-ink); }
  .dot { flex: none; width: 20px; height: 20px; border-radius: var(--hh-radius-pill); border: 2px solid var(--hh-control-line); background: var(--hh-field); box-sizing: border-box; }
  .opt.is-on .dot { border: 6px solid var(--hh-accent); }
  .opt-text { display: flex; flex-direction: column; gap: var(--hh-s-2); min-width: 0; }
  .opt-name { font-weight: 600; }
  .opt-sub { font-size: 0.8125rem; color: var(--hh-text-dim); }
  .frame-wrap { display: flex; flex-direction: column; }
  .card-frame { width: 100%; height: 272px; border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); background: var(--hh-field); }
  .receipt { display: flex; flex-direction: column; gap: var(--hh-s-8); align-items: flex-start; padding-top: var(--hh-s-8); }
  .receipt .btn { align-self: stretch; margin-top: var(--hh-s-8); }
  .receipt-head { font-weight: 600; color: var(--hh-success); }

  /* Payment methods */
  li.method { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-12); padding: var(--hh-s-12) var(--hh-s-16); border-bottom: 1px solid var(--hh-line); }
  .method-text { display: flex; flex-direction: column; gap: var(--hh-s-2); }
  .method-actions, .row-actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); }
  .add { margin-top: var(--hh-s-16); display: flex; flex-direction: column; gap: var(--hh-s-8); align-items: flex-start; }
  .add .frame-wrap { align-self: stretch; }

  /* Statements */
  li.pay-row { display: flex; flex-direction: column; gap: var(--hh-s-4); padding: var(--hh-s-12) var(--hh-s-16); border-bottom: 1px solid var(--hh-line); }
  .pay-top { display: flex; align-items: center; gap: var(--hh-s-8); }
  .pay-amount { margin-left: auto; font-weight: 600; }

  @media (min-width: 720px) {
    .tiles { grid-template-columns: repeat(4, 1fr); }
    .sheet { left: 50%; right: auto; bottom: auto; top: 50%; width: 480px; transform: translate(-50%, -50%); border-radius: var(--hh-radius-card); padding-bottom: var(--hh-s-24); }
    .grab { display: none; }
  }
`;
