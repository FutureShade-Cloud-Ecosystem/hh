// The in desk branch chooser: shown once, inside a desk, when the person is granted several branches and the
// front door's session branch key did not name exactly one. A radio group of the granted branches by name (as the
// core named them) and one confirm. The confirm writes the one chosen id to the session branch key and announces
// it; the desk then resolves its branch again through the key, so the choice is read back through the granted list
// like any other. Nothing is chosen until the person chooses: no branch is invented for them.
import { LitElement, css, html, nothing, type TemplateResult } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import { writeSessionBranchIds } from "./session-branches.js";

export interface ChooserBranch {
  readonly id: string;
  readonly name: string;
}

export const BRANCH_CHOSEN_EVENT = "hh-branch-chosen";

@customElement("hh-branch-chooser")
export class HHBranchChooser extends LitElement {
  /** The person's granted branches, as the core named them. */
  @property({ attribute: false }) branches: readonly ChooserBranch[] = [];
  /** What the confirm says, naming where it goes ("Open the Counter Till"). */
  @property({ attribute: "action-label" }) actionLabel = "Continue";
  @state() private chosen = "";
  @state() private nudge = false;

  static override styles = css`
    :host {
      display: block;
      color: var(--hh-text);
    }
    .stage {
      display: grid;
      place-items: start center;
      padding: var(--hh-s-40) var(--hh-s-16);
    }
    .card {
      width: min(100%, 440px);
      display: grid;
      gap: var(--hh-s-16);
      padding: var(--hh-s-24);
      background: var(--hh-card);
      border: 1px solid var(--hh-line);
      border-radius: var(--hh-radius-card);
    }
    h2 {
      margin: 0;
      font-size: 20px;
      font-weight: 600;
      line-height: 1.25;
    }
    p {
      margin: 0;
      font-size: 14px;
      line-height: 1.4;
      color: var(--hh-text-dim);
    }
    .list {
      display: grid;
      gap: var(--hh-s-8);
    }
    .option {
      appearance: none;
      width: 100%;
      min-height: 48px;
      display: flex;
      align-items: center;
      gap: var(--hh-s-12);
      padding: var(--hh-s-12) var(--hh-s-16);
      text-align: start;
      font: inherit;
      font-size: 14px;
      font-weight: 600;
      background: var(--hh-card);
      color: var(--hh-text);
      border: 1px solid var(--hh-control-line);
      border-radius: var(--hh-radius-control);
      cursor: pointer;
    }
    .option:hover {
      border-color: var(--hh-accent);
    }
    .option:focus-visible,
    .confirm:focus-visible {
      outline: 2px solid var(--hh-focus-ring);
      outline-offset: 2px;
    }
    .option[aria-checked="true"] {
      border-color: var(--hh-accent);
      background: var(--hh-row);
    }
    .dot {
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
    .option[aria-checked="true"] .dot {
      border-color: var(--hh-accent);
      background: var(--hh-accent);
    }
    .mark {
      width: 12px;
      height: 12px;
      display: block;
    }
    .confirm {
      appearance: none;
      width: 100%;
      min-height: 44px;
      font: inherit;
      font-size: 14px;
      font-weight: 600;
      color: var(--hh-accent-ink);
      background: var(--hh-accent);
      border: 1px solid var(--hh-accent);
      border-radius: var(--hh-radius-control);
      cursor: pointer;
    }
    .nudge {
      color: var(--hh-text);
    }
  `;

  protected override render(): TemplateResult {
    const stop = this.branches.some((branch) => branch.id === this.chosen) ? this.chosen : (this.branches[0]?.id ?? "");
    return html`
      <div class="stage" data-testid="branch-chooser">
        <section class="card" aria-labelledby="chooser-title">
          <h2 id="chooser-title">Which branch are you working today?</h2>
          <p>Your role reaches more than one. Pick the branch for this window.</p>
          <div class="list" role="radiogroup" aria-labelledby="chooser-title" @keydown=${this.onKey}>
            ${this.branches.map((branch) => {
              const checked = branch.id === this.chosen;
              return html`
                <button
                  type="button"
                  class="option"
                  role="radio"
                  aria-checked=${checked ? "true" : "false"}
                  tabindex=${branch.id === stop ? "0" : "-1"}
                  data-branch-id=${branch.id}
                  @click=${() => this.choose(branch.id)}
                >
                  <span class="dot" aria-hidden="true">${checked ? this.checkMark() : nothing}</span>
                  <span>${branch.name}</span>
                </button>
              `;
            })}
          </div>
          ${this.nudge ? html`<p class="nudge" role="alert" data-testid="branch-chooser-nudge">Pick a branch to continue.</p>` : nothing}
          <button type="button" class="confirm" data-testid="branch-chooser-confirm" @click=${this.confirm}>${this.actionLabel}</button>
        </section>
      </div>
    `;
  }

  private checkMark(): TemplateResult {
    return html`<svg class="mark" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
      <path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" d="M2.5 6.2 5 8.7 9.5 3.5"></path>
    </svg>`;
  }

  private choose(id: string): void {
    this.chosen = id;
    this.nudge = false;
  }

  /** Arrows move the choice and the focus together, as a radio group does. */
  private readonly onKey = (event: KeyboardEvent): void => {
    const forward = event.key === "ArrowDown" || event.key === "ArrowRight";
    if (!forward && event.key !== "ArrowUp" && event.key !== "ArrowLeft") return;
    if (this.branches.length === 0) return;
    event.preventDefault();
    const focused = (event.target as HTMLElement | null)?.getAttribute?.("data-branch-id") ?? "";
    const at = this.branches.findIndex((branch) => branch.id === focused);
    if (at < 0) return;
    const next = this.branches[(at + (forward ? 1 : -1) + this.branches.length) % this.branches.length];
    if (next === undefined) return;
    this.choose(next.id);
    void this.updateComplete.then(() => {
      this.renderRoot.querySelector<HTMLElement>(`[data-branch-id="${next.id}"]`)?.focus();
    });
  };

  private readonly confirm = (): void => {
    if (!this.branches.some((branch) => branch.id === this.chosen)) {
      this.nudge = true;
      return;
    }
    writeSessionBranchIds([this.chosen]);
    this.dispatchEvent(new CustomEvent(BRANCH_CHOSEN_EVENT, { detail: { id: this.chosen }, bubbles: true, composed: true }));
  };
}
