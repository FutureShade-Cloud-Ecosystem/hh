// hh-pro-state: the four states a Pro surface shows in place of its list. Three
// are the design system's own (empty, loading, error: the icon, the word and
// the pattern marker, so colour never carries the state alone); the fourth,
// refusal, is Pro's: the server answered 401 or 403, or the role does not hold
// what the surface needs. A refusal is not an error and not an empty list, and
// it never offers "try again", because trying again changes nothing.
//
// The words come from the element's light DOM (the slot), so each app says what
// its own surface means; the slot's fallback is the design system's default.
// The element carries no handlers: the retry button rides data-action="retry"
// and the app root's host click listener acts on it, the till's delegation.
import { html, css, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles, button, spinner } from "@hardscapehouse/design-system/src/primitives.js";
import { STATE_META, stateIndicator, stateStyles } from "@hardscapehouse/design-system/src/state.js";
import { icon } from "@hardscapehouse/design-system/src/state-icons.js";

export const PRO_STATES = ["empty", "loading", "error", "refusal"] as const;
export type ProState = (typeof PRO_STATES)[number];

const REFUSAL_LABEL = "Your role does not open this";
const REFUSAL_NOTE = "Ask the Owner on your account to change your access.";

export class HHProState extends HHElement {
  static properties = {
    ...HHElement.properties,
    mode: { attribute: "data-state" },
  };

  static styles = [
    baseStyles,
    stateStyles,
    css`
      :host { display: block; }
      .block {
        display: flex;
        flex-direction: column;
        align-items: flex-start;
        gap: var(--hh-s-12);
        padding: var(--hh-s-20);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-card);
        background: var(--hh-card);
        color: var(--hh-text);
      }
      .note { margin: 0; color: var(--hh-text-dim); }
      .refusal-head { display: flex; align-items: center; gap: var(--hh-s-8); font-weight: 600; }
    `,
  ];

  declare mode: string;

  constructor() {
    super();
    this.mode = "empty";
  }

  render(): TemplateResult {
    switch (this.mode) {
      case "loading":
        return html`<div class="block" data-pattern="progress" role="status">
          ${stateIndicator("loading")}
          ${spinner()}
          <p class="note"><slot>${STATE_META.loading.note}</slot></p>
        </div>`;
      case "error":
        return html`<div class="block" data-pattern="fault" role="alert">
          ${stateIndicator("error")}
          <p class="note"><slot>${STATE_META.error.note}</slot></p>
          ${button({ label: "Try again", action: "retry" })}
        </div>`;
      case "refusal":
        return html`<div class="block" data-pattern="refusal" role="alert">
          <div class="refusal-head">${icon("alertOctagon")}<span>${REFUSAL_LABEL}</span></div>
          <p class="note"><slot>${REFUSAL_NOTE}</slot></p>
        </div>`;
      default:
        return html`<div class="block" data-pattern="prompt">
          ${stateIndicator("empty")}
          <p class="note"><slot>${STATE_META.empty.note}</slot></p>
        </div>`;
    }
  }
}

defineElement("hh-pro-state", HHProState);
