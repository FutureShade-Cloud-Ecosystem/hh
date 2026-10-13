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
    font-weight: 800;
    font-size: 18px;
  }
  .dealer-names {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .dealer-name {
    font-family: Bitter, Georgia, serif;
    font-weight: 800;
    font-size: 18px;
    letter-spacing: 0.01em;
    line-height: 1.05;
    text-transform: uppercase;
  }
  .dealer-sub {
    font-family: Bitter, Georgia, serif;
    font-weight: 500;
    font-size: 10px;
    letter-spacing: 0.18em;
    line-height: 1.2;
    text-transform: uppercase;
    color: var(--hh-text-dim);
    white-space: nowrap;
  }

  /* ── the sign in screen ───────────────────────────────────────────────── */
  .signin-stage {
    display: grid;
    justify-items: center;
    gap: var(--hh-s-24);
    padding: var(--hh-s-40) var(--hh-s-16) var(--hh-s-64);
  }
  .signin-card {
    width: 100%;
    max-width: 420px;
    padding: var(--hh-s-32) var(--hh-s-24);
    background: var(--hh-card);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    box-shadow: var(--hh-widget-shadow);
    display: grid;
    gap: var(--hh-s-16);
  }
  .signin-card .dealer-mark {
    width: 44px;
    height: 44px;
  }
  .signin-card .dealer-name {
    font-size: 22px;
  }
  .signin-title {
    margin: var(--hh-s-8) 0 0;
    font-size: 26px;
    line-height: 1.15;
    font-weight: 600;
    letter-spacing: -0.02em;
  }
  .signin-lead {
    margin: calc(var(--hh-s-8) * -1) 0 0;
    color: var(--hh-text-dim);
    font-size: 15px;
    line-height: 1.45;
  }
  .signin-form {
    display: grid;
    gap: var(--hh-s-16);
  }
  .field {
    display: grid;
    gap: var(--hh-s-4);
  }
  .field label {
    font-size: 14px;
    font-weight: 600;
  }
  .field input {
    width: 100%;
    min-height: 44px;
    font-size: 16px;
  }
  .field input[readonly] {
    cursor: progress;
  }
  .field input[aria-invalid="true"] {
    border-color: var(--hh-danger);
  }
  .signin-card .btn-wide {
    width: 100%;
    min-height: 48px;
    font-weight: 600;
  }
  .signin-card .btn-primary:disabled {
    cursor: progress;
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
    font-size: 13px;
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
    display: grid;
    place-items: center;
    border-radius: 50%;
    border: 1px solid var(--hh-control-line);
    font-size: 11px;
    font-weight: 700;
  }
  /* The status line: always rendered, empty at rest. An error is ink, with
     the words doing the work and a mark beside them, never colour alone. */
  .status {
    margin: 0;
    min-height: 1.5em;
    font-size: 14px;
    line-height: 1.5;
    color: var(--hh-text-dim);
  }
  .status.error {
    color: var(--hh-danger);
    font-weight: 600;
    padding-inline-start: var(--hh-s-12);
    border-inline-start: 3px solid var(--hh-danger);
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

  /* ── the role entry ───────────────────────────────────────────────────── */
  .entry-card {
    display: grid;
    gap: var(--hh-s-16);
    max-width: 560px;
    padding: var(--hh-s-32) var(--hh-s-24);
    background: var(--hh-card);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    box-shadow: var(--hh-widget-shadow);
    margin: 0 0 var(--hh-s-32);
  }
  .entry-card h2 {
    margin: 0;
    font-size: 26px;
    line-height: 1.15;
    font-weight: 600;
    letter-spacing: -0.02em;
  }
  .entry-card p {
    margin: 0;
    color: var(--hh-text-dim);
    line-height: 1.45;
  }
  .entry-card .btn-wide {
    min-height: 52px;
    font-size: 17px;
    font-weight: 600;
  }
  .entry-subhead {
    margin: 0 0 var(--hh-s-12);
    font-size: var(--hh-s-16);
    font-weight: 600;
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
    padding: var(--hh-s-32) 0;
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
  @media (max-width: 480px) {
    .signin-stage {
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
