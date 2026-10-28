// Styles for Dealer Admin's inline entity pickers (UX-117). Kept in its own
// module so styles.ts stays conflict-free with concurrent mobile layout work.
// Token-only: every surface, border, and ink rides the Gable design system.
import { css } from "lit";

export const pickerStyles = css`
  .hh-entity-picker {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-6, 6px);
    min-width: 220px;
  }

  .picker-selected {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--hh-s-8);
    min-height: 28px;
    padding: var(--hh-s-4) var(--hh-s-8);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    background: var(--hh-widget-raised);
    font-size: 13px;
  }

  .picker-current {
    font-weight: 600;
    color: var(--hh-text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .picker-current.picker-empty {
    font-weight: 400;
    color: var(--hh-text-dim);
  }

  .picker-clear {
    flex-shrink: 0;
  }

  .picker-results {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-2, 2px);
    max-height: 220px;
    overflow-y: auto;
    padding: var(--hh-s-4);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    background: var(--hh-card);
    box-shadow: var(--hh-shadow-pop, 0 8px 20px rgba(0, 0, 0, 0.16));
    z-index: 20;
  }

  .picker-option {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--hh-s-8);
    width: 100%;
    min-height: 36px;
    padding: var(--hh-s-6, 6px) var(--hh-s-8);
    border: 1px solid transparent;
    border-radius: var(--hh-radius-control);
    background: transparent;
    color: var(--hh-text);
    text-align: left;
    font: inherit;
    font-size: 13px;
    cursor: pointer;
  }

  .picker-option:hover,
  .picker-option:focus-visible {
    border-color: var(--hh-accent-text);
    background: var(--hh-widget-raised);
    outline: none;
  }

  .picker-option-name {
    font-weight: 600;
    color: var(--hh-text);
  }

  .picker-option-meta {
    font-family: var(--hh-font-mono);
    font-size: 12px;
    color: var(--hh-text-dim);
    white-space: nowrap;
  }

  .picker-status {
    margin: 0;
    font-size: 12px;
    color: var(--hh-text-dim);
  }
`;
