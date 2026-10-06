// The tab face the desk navigation and the quote's tab strip share: link tabs in a row, the active one marked by
// aria-current and an accent underline, wrapping rather than scrolling at tablet width. Tokens only.
import { css } from "lit";

export const tabStyles = css`
  :host {
    display: block;
    font-family: var(--hh-font-ui);
  }
  [hidden] {
    display: none;
  }
  .tabs {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-4, 4px) var(--hh-s-8, 8px);
    align-items: center;
  }
  .tab {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-4, 4px);
    padding: var(--hh-s-8, 8px) var(--hh-s-12, 12px);
    border-bottom: 2px solid transparent;
    color: var(--hh-text-dim);
    text-decoration: none;
    font-size: 0.9rem;
    font-weight: 600;
    white-space: nowrap;
  }
  .tab:hover {
    color: var(--hh-text);
  }
  .tab[aria-current="page"] {
    color: var(--hh-accent-text);
    border-bottom-color: var(--hh-accent-text);
  }
  .tab:focus-visible {
    outline: 2px solid var(--hh-accent-text);
    outline-offset: 2px;
  }
  .count {
    display: inline-block;
    min-width: 1.3em;
    padding: 0 6px;
    border-radius: 999px;
    background: var(--hh-accent-text);
    color: var(--hh-page);
    font-size: 0.72rem;
    line-height: 1.5;
    text-align: center;
  }
`;
