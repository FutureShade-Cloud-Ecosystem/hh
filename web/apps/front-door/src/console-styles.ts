// The console mode's styles: the sign in card, the dealer lockup, the role
// entry and the header's selector zone. Tokens only (the design system's
// sheet and the dealer's brand as the sheet already carries it); the page's
// existing card and chip styles are reused where the entry draws role cards.
import { css } from "lit";

export const consoleStyles = css`
  /* ── the dealer lockup: the tenant's mark and name, the first thing seen ─ */
  .dealer {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    min-width: 0;
    color: var(--hh-text);
  }
  .dealer-mark {
    width: 36px;
    height: 36px;
    flex: none;
  }
  .dealer-mark.initial {
    display: grid;
    place-items: center;
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    border-radius: var(--hh-radius-control);
    font-weight: 700;
    font-size: 16px;
  }
  .dealer-names {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-2);
    min-width: 0;
  }
  .dealer-name {
    font-family: var(--hh-font-ui);
    font-weight: 700;
    font-size: 16px;
    letter-spacing: 0.01em;
    line-height: 1.2;
    text-transform: uppercase;
  }
  .dealer-sub {
    font-family: var(--hh-font-ui);
    font-weight: 600;
    font-size: 10px;
    letter-spacing: 0.12em;
    line-height: 1.2;
    text-transform: uppercase;
    color: var(--hh-text-dim);
    white-space: nowrap;
  }

  /* ── the sign in screen ───────────────────────────────────────────────── */
  .signin-stage {
    display: grid;
    justify-items: center;
    gap: var(--hh-s-16);
    padding: var(--hh-s-64) var(--hh-s-16);
  }
  .signin-card {
    width: 100%;
    max-width: 460px;
    padding: var(--hh-s-28) var(--hh-s-40) var(--hh-s-32);
    background: var(--hh-card);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    box-shadow: var(--hh-widget-shadow);
    display: grid;
    gap: var(--hh-s-16);
  }
  .signin-card .dealer {
    justify-self: center;
  }
  .signin-card .dealer-mark {
    width: 44px;
    height: 44px;
  }
  .signin-card .dealer-name {
    font-size: 20px;
    font-weight: 600;
    line-height: 1.25;
  }
  .signin-title {
    margin: 0;
    text-align: center;
    font-size: 20px;
    line-height: 1.25;
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .signin-lead {
    margin: calc(var(--hh-s-12) * -1) 0 0;
    text-align: center;
    color: var(--hh-text-dim);
    font-size: 13px;
    font-weight: 400;
    line-height: 1.4;
  }
  .signin-form {
    display: grid;
    gap: var(--hh-s-16);
  }
  /* The rule the page shows under the fields: quiet, and never the only place
     the rule is written. */
  .signin-hint {
    margin: 0;
    font-size: 12px;
    font-weight: 400;
    line-height: 1.3;
    color: var(--hh-text-dim);
  }
  .field {
    display: grid;
    gap: var(--hh-s-4);
  }
  .field label {
    font-size: 12px;
    font-weight: 600;
    line-height: 1.3;
  }
  .field input {
    width: 100%;
    min-height: 44px;
    font-size: 14px;
    font-weight: 400;
    line-height: 1.4;
  }
  .field input[readonly] {
    cursor: progress;
  }
  .field input[aria-invalid="true"] {
    border-color: var(--hh-danger);
  }
  .signin-card .btn-wide {
    width: 100%;
    min-height: 44px;
    font-size: 14px;
    font-weight: 600;
    line-height: 1.2;
  }
  .signin-card .btn-primary:disabled {
    cursor: progress;
  }
  /* A link that wears the button's shape: the set password page's done state
     points at the sign in page, and an anchor does not inherit the button's own
     padding, edge or focus ring. */
  .signin-card a.btn {
    font-family: inherit;
    font-size: var(--hh-s-16);
    line-height: 1;
    min-height: var(--hh-h-input);
    padding: 0 var(--hh-s-20);
    border-radius: var(--hh-radius-control);
    border: 1px solid transparent;
    text-decoration: none;
  }
  .signin-card a.btn:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .spinner {
    width: 16px;
    height: 16px;
    flex: none;
    border-radius: 50%;
    border: 2px solid currentColor;
    border-inline-end-color: transparent;
    animation: fd-spin 0.8s linear infinite;
  }
  @keyframes fd-spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .spinner {
      animation-duration: 3s;
    }
  }
  .or {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    color: var(--hh-text-dim);
    font-size: 12px;
    font-weight: 400;
    line-height: 1.3;
  }
  .or::before,
  .or::after {
    content: "";
    flex: 1 1 auto;
    height: 1px;
    background: var(--hh-line);
  }
  .google-mark {
    flex: none;
    width: 18px;
    height: 18px;
    display: block;
  }
  /* The status line: always rendered, empty at rest. An error is ink, with
     the words doing the work and a mark beside them, never colour alone. */
  .status {
    margin: 0;
    min-height: 1.5em;
    font-size: 13px;
    font-weight: 400;
    line-height: 1.4;
    color: var(--hh-text-dim);
  }
  /* Empty at rest it takes no room of its own: the card's row gap beside it is cancelled, so the
     live region stays in the tree without a blank band under the lead. */
  .status:empty {
    margin-top: calc(var(--hh-s-16) * -1);
    min-height: 0;
    padding: 0;
    border: none;
  }
  .status.error {
    color: var(--hh-danger);
    font-weight: 400;
    padding: var(--hh-s-8) var(--hh-s-12);
    border: 1px solid var(--hh-danger);
    border-radius: var(--hh-radius-control);
  }
  .methods-note {
    margin: 0;
    font-size: 14px;
    line-height: 1.5;
    color: var(--hh-text-dim);
  }
  .methods-note + button {
    justify-self: start;
  }
  .offline-banner {
    margin: 0;
    padding: var(--hh-s-8) var(--hh-s-12);
    border: 1px solid var(--hh-danger);
    border-radius: var(--hh-radius-control);
    font-size: 14px;
    color: var(--hh-text);
  }
  .persona-stage {
    width: 100%;
    max-width: 960px;
    display: grid;
    gap: var(--hh-s-12);
  }
  .signin-footer {
    display: grid;
    justify-items: center;
    gap: var(--hh-s-4);
    text-align: center;
    color: var(--hh-text-dim);
    font-size: 12px;
    font-weight: 400;
    line-height: 1.3;
  }
  .signin-footer-copy {
    margin: 0;
  }
  .signin-quiet-controls {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
  }
  .quiet-control {
    appearance: none;
    background: transparent;
    border: none;
    padding: var(--hh-s-4) var(--hh-s-8);
    min-height: 32px;
    font-family: var(--hh-font-ui);
    font-size: 12px;
    font-weight: 400;
    line-height: 1.3;
    color: var(--hh-text-dim);
    border-radius: var(--hh-radius-control);
    cursor: pointer;
  }
  .quiet-control:hover {
    color: var(--hh-text);
    text-decoration: underline;
  }
  .quiet-control:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .quiet-sep {
    color: var(--hh-text-dim);
    font-size: 12px;
    font-weight: 400;
  }

  /* ── the role entry ───────────────────────────────────────────────────── */
  .picker-stage {
    display: grid;
    justify-items: center;
    gap: var(--hh-s-20);
    padding: var(--hh-s-48) var(--hh-s-16) var(--hh-s-64);
  }
  .entry-card {
    width: 100%;
    max-width: 520px;
    display: grid;
    gap: var(--hh-s-20);
    padding: var(--hh-s-28) var(--hh-s-32) var(--hh-s-32);
    background: var(--hh-card);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    box-shadow: var(--hh-widget-shadow);
    margin: 0;
  }
  .entry-card h1,
  .entry-card h2 {
    margin: 0;
    font-size: 20px;
    line-height: 1.25;
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--hh-text);
  }
  .entry-card p {
    margin: 0;
    color: var(--hh-text-dim);
    font-size: 14px;
    font-weight: 400;
    line-height: 1.45;
  }
  .account-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--hh-s-8) var(--hh-s-12);
    padding: var(--hh-s-4) var(--hh-s-12);
    background: var(--hh-row);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-control);
  }
  .account-who {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    min-width: 0;
  }
  .account-avatar {
    width: 24px;
    height: 24px;
    flex: none;
    border-radius: 50%;
    display: grid;
    place-items: center;
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    font-size: 12px;
    font-weight: 700;
    line-height: 1;
  }
  .account-name {
    font-size: 13px;
    font-weight: 400;
    line-height: 1.35;
    color: var(--hh-text);
  }
  .not-you-btn {
    appearance: none;
    background: transparent;
    border: none;
    padding: var(--hh-s-8) var(--hh-s-8);
    min-height: 44px;
    font-family: var(--hh-font-ui);
    font-size: 12px;
    font-weight: 600;
    line-height: 1.2;
    color: var(--hh-text-dim);
    border-radius: var(--hh-radius-control);
    cursor: pointer;
    white-space: nowrap;
  }
  .not-you-btn:hover {
    color: var(--hh-text);
    text-decoration: underline;
  }
  .not-you-btn:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .picker-group {
    display: grid;
    gap: var(--hh-s-8);
  }
  .picker-group-label {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.08em;
    line-height: 1.2;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }
  .picker-radio-list {
    display: grid;
    gap: var(--hh-s-8);
  }
  .picker-branches {
    display: grid;
    gap: var(--hh-s-4);
    margin: 0;
  }
  .picker-branches-names {
    font-size: 14px;
    line-height: 1.4;
    color: var(--hh-text);
  }
  .picker-option {
    appearance: none;
    width: 100%;
    min-height: 48px;
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    padding: var(--hh-s-12) var(--hh-s-16);
    text-align: start;
    background: var(--hh-card);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    cursor: pointer;
    transition: border-color 0.15s ease, background-color 0.15s ease;
  }
  .picker-option:hover {
    border-color: var(--hh-accent);
  }
  .picker-option:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .picker-option.checked {
    border-color: var(--hh-accent);
    background: var(--hh-row);
  }
  .picker-radio-dot {
    width: 18px;
    height: 18px;
    flex: none;
    border-radius: 50%;
    border: 2px solid var(--hh-control-line);
    display: grid;
    place-items: center;
    background: var(--hh-card);
    color: var(--hh-accent-ink);
  }
  .picker-box-dot {
    border-radius: var(--hh-s-4);
  }
  .picker-option.checked .picker-radio-dot {
    border-color: var(--hh-accent);
    background: var(--hh-accent);
  }
  .picker-check-mark {
    width: 12px;
    height: 12px;
    display: block;
  }
  .picker-option-words {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-2);
    min-width: 0;
  }
  .picker-option-title {
    font-size: 14px;
    font-weight: 600;
    line-height: 1.3;
    color: var(--hh-text);
  }
  .picker-option-sub {
    font-size: 12px;
    font-weight: 400;
    line-height: 1.3;
    color: var(--hh-text-dim);
  }
  .entry-card .btn-wide,
  .picker-submit {
    width: 100%;
    min-height: 44px;
    justify-content: center;
    gap: var(--hh-s-8);
    font-size: 14px;
    font-weight: 600;
    line-height: 1.2;
  }
  .entry-subhead {
    margin: 0 0 var(--hh-s-12);
    font-size: 14px;
    font-weight: 600;
  }
  .persona-walk {
    width: 100%;
    max-width: 520px;
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-4) var(--hh-s-20);
  }
  .persona-walk[open] {
    max-width: 960px;
  }
  .persona-walk-summary {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--hh-s-12);
    min-height: 44px;
    cursor: pointer;
    list-style: none;
    font-size: 13px;
    font-weight: 600;
    line-height: 1.3;
    color: var(--hh-text-dim);
  }
  .persona-walk-summary::-webkit-details-marker {
    display: none;
  }
  .persona-walk-summary:hover {
    color: var(--hh-text);
  }
  .persona-walk-summary:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
    border-radius: var(--hh-radius-control);
  }
  .persona-walk[open] .chev {
    transform: rotate(0deg);
  }
  .persona-walk-body {
    padding: var(--hh-s-12) 0 var(--hh-s-8);
    display: grid;
    gap: var(--hh-s-12);
  }

  /* ── the header: the selector zone, the person and Sign out ───────────── */
  .apps-menu {
    position: relative;
  }
  .apps-menu summary {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    min-height: 44px;
    padding: 0 var(--hh-s-12);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    cursor: pointer;
    list-style: none;
    font-size: 14px;
    font-weight: 600;
    color: var(--hh-text);
  }
  .apps-menu summary::-webkit-details-marker {
    display: none;
  }
  .apps-menu summary:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .apps-list {
    position: absolute;
    z-index: 5;
    inset-inline-start: 0;
    margin: var(--hh-s-4) 0 0;
    padding: var(--hh-s-8);
    list-style: none;
    min-width: 220px;
    background: var(--hh-card);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    box-shadow: var(--hh-elev-2);
  }
  .apps-list button {
    width: 100%;
    min-height: 44px;
    text-align: start;
    background: transparent;
    color: var(--hh-text);
    border: none;
    font-size: 14px;
  }
  .apps-list button:hover {
    background: var(--hh-row);
  }
  .person {
    margin: 0;
    justify-self: center;
    text-align: center;
    display: grid;
    gap: 1px;
  }
  .person .person-name {
    font-weight: 600;
    font-size: 15px;
  }
  .person .person-role {
    font-size: 12px;
    color: var(--hh-text-dim);
  }
  .console-controls button {
    min-height: 44px;
    min-width: 44px;
  }
  .config-error {
    display: grid;
    gap: var(--hh-s-16);
    max-width: 520px;
    margin: 0 auto;
    padding: var(--hh-s-32) var(--hh-s-16);
  }
  .config-error h2 {
    margin: 0;
    font-size: 26px;
    font-weight: 600;
    letter-spacing: -0.02em;
  }
  .config-error p {
    margin: 0;
    color: var(--hh-text-dim);
    line-height: 1.5;
  }
  .config-error button {
    justify-self: start;
  }
  /* The stacked header (the same breakpoint the band's own zones stack at): the menu opens as a
     full width sheet under the band instead of hanging off a button near the right edge, where
     it would run past the screen on a phone. */
  @media (max-width: 1200px) {
    header {
      position: relative;
    }
    .apps-menu {
      position: static;
    }
    .apps-list {
      inset-inline: var(--hh-s-16);
      min-width: 0;
    }
  }
  @media (pointer: coarse), (max-width: 768px) {
    .quiet-control {
      min-height: 44px;
      min-width: 44px;
      padding: var(--hh-s-12) var(--hh-s-8);
    }
  }
  @media (max-width: 480px) {
    .signin-stage,
    .picker-stage {
      padding-top: var(--hh-s-24);
    }
    .signin-card {
      padding: var(--hh-s-24) var(--hh-s-16);
    }
    .entry-card {
      padding: var(--hh-s-24) var(--hh-s-16);
    }
  }
`;
