// HH Drive's own styles: one column, one hand, gloves and sunlight. Every
// value is a token of the design system; nothing here is a literal colour.
// Targets are at least 56 px tall (the design system's own floor is 44),
// text never drops under 16 px, and the page never scrolls sideways.
import { css } from "lit";

export const driveStyles = css`
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
  }
  h1 {
    margin: 0 0 var(--hh-s-12);
    font-size: 28px;
    line-height: 1.2;
    font-weight: 700;
  }
`;
