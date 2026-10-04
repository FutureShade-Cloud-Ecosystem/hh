// PanelBase: the one base the second coder's panels share. It carries the
// three facts every host passes (the validated client, the quote id and the
// branch id), the busy, error and notice state with their live regions, and
// the host delegation the app's binding discipline needs: the templates carry
// no property or event bindings, so the base listens once on its own render
// root and hands every click, change, input and keydown to the panel by the
// control's data-action or data-field. A panel answers reload() like every
// mounted child the quote-changed fan-out reaches.
import { LitElement } from "lit";
import { property, state } from "lit/decorators.js";
import type { QdClient } from "./qd-client.js";

export type Controls = Record<string, (control: HTMLElement, event: Event) => void>;

export abstract class PanelBase extends LitElement {
  /** The host wires the validated client before the first load. */
  @property({ attribute: false }) client: QdClient | null = null;
  @property({ attribute: "quote-id" }) quoteId = "";
  @property({ attribute: "branch-id" }) branchId = "";

  @state() protected busy = false;
  @state() protected error = "";
  @state() protected notice = "";

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
    this.renderRoot.addEventListener("click", this.handleClick);
    this.renderRoot.addEventListener("input", this.handleInput);
    this.renderRoot.addEventListener("change", this.handleChange);
    this.renderRoot.addEventListener("keydown", this.handleKey);
  }

  override disconnectedCallback(): void {
    super.disconnectedCallback();
    this.renderRoot.removeEventListener("click", this.handleClick);
    this.renderRoot.removeEventListener("input", this.handleInput);
    this.renderRoot.removeEventListener("change", this.handleChange);
    this.renderRoot.removeEventListener("keydown", this.handleKey);
  }

  /** Tell the surface an edit landed so every sibling panel and the list re-read, with no page reload. */
  protected announceChanged(): void {
    this.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: this.quoteId }, bubbles: true, composed: true }));
  }

  /** Set an uncontrolled field to the state's value unless it already holds it. */
  protected syncValue(selector: string, value: string): void {
    const el = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(selector);
    if (el !== null && el.value !== value) el.value = value;
  }
}
