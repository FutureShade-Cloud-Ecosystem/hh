// PanelBase: the one base the second coder's panels share. It carries the
// three facts every host passes (the validated client, the quote id and the
// branch id), the busy, error and notice state with their live regions, and
// the host delegation the app's binding discipline needs: the templates carry
// no property or event bindings, so the base listens once on its own render
// root and hands every click, change, input and keydown to the panel by the
// control's data-action or data-field. A panel answers reload() like every
// mounted child the quote-changed fan-out reaches.
import { LitElement, type PropertyValues } from "lit";
import { property, state } from "lit/decorators.js";
import type { QdClient } from "./qd-client.js";
import type { Result } from "@hardscapehouse/api-client";
import { QuoteContext, QuoteScope } from "./quote-context.js";

export type Controls = Record<string, (control: HTMLElement, event: Event) => void>;

export abstract class PanelBase extends LitElement {
  /** The host wires the validated client before the first load. */
  @property({ attribute: false }) client: QdClient | null = null;
  @property({ attribute: "quote-id" }) quoteId = "";
  @property({ attribute: "branch-id" }) branchId = "";

  @state() protected busy = false;
  @state() protected error = "";
  @state() protected notice = "";

  /** Holds the context of the quote this panel shows; see quote-context.ts. */
  private readonly quoteScope = new QuoteScope();

  /** True from the moment this panel leaves the page (sign out removes it): its context is then cancelled for good. */
  private detached = false;

  /**
   * The context of the quote now shown. Every read and write captures it at its start, binds itself to its
   * id (never to this.quoteId read after an await) and checks it after every await.
   */
  protected get ctx(): QuoteContext {
    if (this.detached) return QuoteContext.cancelled(this.quoteId);
    const live = this.quoteScope.current;
    return live.id === this.quoteId && !live.stale ? live : this.quoteScope.open(this.quoteId);
  }

  /**
   * A new quote on screen is a new context: the old one is cancelled before any panel's own willUpdate runs, so
   * nothing still out for the quote just left can touch this one, and its busy flag, error and notice go with it.
   */
  protected override shouldUpdate(changed: PropertyValues): boolean {
    if (changed.has("quoteId")) {
      this.quoteScope.open(this.quoteId);
      this.busy = false;
      this.error = "";
      this.notice = "";
    }
    return true;
  }

  /** Re-read whatever this panel shows. */
  abstract reload(): void;

  protected onClickAction(_action: string, _control: HTMLElement, _event: MouseEvent): void {}
  protected onFieldInput(_field: string, _control: HTMLElement, _event: Event): void {}
  protected onFieldChange(_field: string, _control: HTMLElement, _event: Event): void {}
  protected onFieldKey(_field: string, _control: HTMLElement, _event: KeyboardEvent): void {}

  private static controlOf(event: Event, attribute: string): HTMLElement | null {
    const hit = event.composedPath().find((node): node is HTMLElement => node instanceof HTMLElement && node.hasAttribute(attribute));
    return hit ?? null;
  }

  private readonly handleClick = (event: Event): void => {
    const control = PanelBase.controlOf(event, "data-action");
    if (control === null || (control as HTMLButtonElement).disabled === true) return;
    this.onClickAction(control.getAttribute("data-action") ?? "", control, event as MouseEvent);
  };
  private readonly handleInput = (event: Event): void => {
    const control = PanelBase.controlOf(event, "data-field");
    if (control !== null) this.onFieldInput(control.getAttribute("data-field") ?? "", control, event);
  };
  private readonly handleChange = (event: Event): void => {
    const control = PanelBase.controlOf(event, "data-field");
    if (control !== null) this.onFieldChange(control.getAttribute("data-field") ?? "", control, event);
  };
  private readonly handleKey = (event: Event): void => {
    const control = PanelBase.controlOf(event, "data-field");
    if (control !== null) this.onFieldKey(control.getAttribute("data-field") ?? "", control, event as KeyboardEvent);
  };

  override connectedCallback(): void {
    super.connectedCallback();
    this.detached = false;
    this.renderRoot.addEventListener("click", this.handleClick);
    this.renderRoot.addEventListener("input", this.handleInput);
    this.renderRoot.addEventListener("change", this.handleChange);
    this.renderRoot.addEventListener("keydown", this.handleKey);
  }

  override disconnectedCallback(): void {
    super.disconnectedCallback();
    // A write still out when the panel leaves (sign out) must not go on to send follow-ups under the next session.
    this.detached = true;
    this.quoteScope.open("");
    this.renderRoot.removeEventListener("click", this.handleClick);
    this.renderRoot.removeEventListener("input", this.handleInput);
    this.renderRoot.removeEventListener("change", this.handleChange);
    this.renderRoot.removeEventListener("keydown", this.handleKey);
  }

  /** Tell the surface an edit landed so every sibling panel and the list re-read, with no page reload. */
  protected announceChanged(): void {
    this.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: this.quoteId }, bubbles: true, composed: true }));
  }

  /** Tell the surface the quote the context names changed, whichever quote is open now: every view of it re-reads. */
  protected announceChangedFor(ctx: QuoteContext): void {
    this.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: ctx.id }, bubbles: true, composed: true }));
  }

  /**
   * What a guarded write does with its answer when the quote was left while it was out. It is never applied to the
   * quote on screen, but a write that succeeded did change its quote, so the screens are told and the person who
   * comes back finds what the server holds. A refusal is said only on the quote it belongs to (when the person is on
   * it again) and says nothing on any other. The announcement is made from the panel itself, which is why the builder
   * keeps every panel mounted through New Quote: a panel taken off the page there would take its late answers with it.
   */
  protected lateWrite(ctx: QuoteContext, say: (refusal: Extract<Result<unknown>, { ok: false }>) => string): (late: Result<unknown>) => void {
    return (late) => {
      if (late.ok) {
        this.announceChangedFor(ctx);
        return;
      }
      if (this.ctx.id !== ctx.id) return;
      this.error = say(late);
    };
  }

  /** Set an uncontrolled field to the state's value unless it already holds it. */
  protected syncValue(selector: string, value: string): void {
    const el = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(selector);
    if (el !== null && el.value !== value) el.value = value;
  }
}
