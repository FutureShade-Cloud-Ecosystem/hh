// The admin-rest surfaces' controller: the state the screens draw from, the
// loads, and what each press does. It is not an element; the root element owns
// it, hands it a way to repaint, to read and set the form fields, and to hand
// bytes to the person as a file, and asks it for the active surface's
// template. The Money Desk's receivables desk is the pattern; here it keeps
// the root element's own file small so the registry's next surfaces land one
// file at a time.
//
// Every write is held to the server: a budget, a setting, a save lands only
// when the core answers, and the screen then shows what the core now says (the
// re-read report, the settings it answers), never what the app expected. A
// refusal reads in the core's own words (admin-logic.ts); an amount is parsed
// through its decimal string, never through float arithmetic.
import type { TemplateResult } from "lit";
import { AdminApi } from "./admin-api.js";
import {
  assistBudgetInputWords,
  assistRefusalWords,
  spendTotal,
  usedFraction,
  wholeAmount,
} from "./admin-logic.js";
import { renderAssist, type AssistEditTarget, type AssistModel } from "./admin-assist.js";

/** The surfaces this controller owns, as the registry names them. */
export const ADMIN_WORK_SURFACES: readonly string[] = ["assist"];

/** Is this surface id this controller's? The root's router line asks once. */
export function isAdminWorkSurface(surface: string): boolean {
  return ADMIN_WORK_SURFACES.includes(surface);
}

/** The ways the controller reaches the page: the root element provides them. */
export interface AdminHost {
  /** Asks the root to repaint. */
  readonly repaint: () => void;
  /** The text of a form field the screen carries, or "" when it is not on screen. */
  readonly field: (name: string) => string;
  /** Sets a form field's text; the screens bind no value, so a fill goes through here. */
  readonly setField: (name: string, value: string) => void;
  /** Sets a checkbox's checked state; mirrored in code for the same reason. */
  readonly setChecked: (name: string, value: boolean) => void;
  /** Sets one meter's fill as a fraction of its track. */
  readonly meter: (name: string, fraction: number) => void;
  /** Hands bytes to the person as a file. */
  readonly download: (bytes: Uint8Array, filename: string, type: string) => void;
}

const emptyAssist = (): AssistModel => ({
  phase: "loading",
  error: "",
  report: null,
  limits: null,
  settings: null,
  editing: null,
  editError: "",
  notice: "",
  busy: false,
});

export class AdminWork {
  readonly #api: AdminApi;
  readonly #host: AdminHost;

  // The assist surface's state.
  #assist: AssistModel = emptyAssist();

  constructor(api: AdminApi, host: AdminHost) {
    this.#api = api;
    this.#host = host;
  }

  // -- the root's delegation ---------------------------------------------------------------------

  /** A surface was opened: the first open loads the reads. */
  open(surface: string): void {
    if (surface === "assist" && this.#assist.phase === "loading") void this.#loadAssist();
  }

  /**
   * One press on a control the screens ride a data-action name with. Answers
   * true when the press was this controller's, so the root stops walking its
   * own cases.
   */
  click(action: string, control: Element | undefined): boolean {
    if (action === "reload-assist") {
      void this.#loadAssist();
      return true;
    }
    if (action === "assist-edit") {
      this.#armAssistEdit(
        control?.getAttribute("data-budget-kind") ?? "",
        control?.getAttribute("data-budget-id") ?? "",
      );
      return true;
    }
    if (action === "assist-cancel-edit") {
      this.#assist = { ...this.#assist, editing: null, editError: "" };
      this.#host.repaint();
      return true;
    }
    if (action === "assist-save") {
      void this.#saveAssistBudget(
        control?.getAttribute("data-budget-kind") ?? "",
        control?.getAttribute("data-budget-id") ?? "",
      );
      return true;
    }
    if (action === "assist-reset-limit") {
      void this.#resetAssistUserLimit(control?.getAttribute("data-user-id") ?? "");
      return true;
    }
    return false;
  }

  /**
   * A checkbox or file input answered. Answers true when the change was this
   * controller's.
   */
  change(target: Element): boolean {
    const action = target.getAttribute("data-action") ?? "";
    if (action === "assist-setting") {
      const key = target.getAttribute("data-key") ?? "";
      const checked = target instanceof HTMLInputElement ? target.checked : false;
      void this.#putAssistSetting(key, checked);
      return true;
    }
    return false;
  }

  /** A field's words moved. Answers true when the field was this controller's. */
  input(field: string, value: string): boolean {
    void field;
    void value;
    return false;
  }

  /** The fields and meters the screens bind no value into are mirrored here. */
  mirror(): void {
    // The edit box: seeded from the row's own number when the edit armed, and
    // kept on the glass through the reloads that follow a save.
    if (this.#assist.editing !== null) {
      const current = this.#editSeed();
      this.#host.setField("assist-budget", current);
    }
    // The two setting toggles ride the server's answer, so a refused write
    // snaps back and a fresh read lands as the server now says.
    const settings = this.#assist.settings;
    this.#host.setChecked("assist-toggle-spend_visible_to_non_admin", settings?.spend_visible_to_non_admin === true);
    this.#host.setChecked("assist-toggle-chat_enabled", settings?.chat_enabled === true);
    // The meters' fills: one per spend row.
    const users = this.#assist.report?.users ?? [];
    for (const row of users) {
      this.#host.meter(row.user_id, usedFraction(spendTotal(row), row.budget.tokens));
    }
  }

  /** The active surface's template, for the root's router line. */
  view(surface: string): TemplateResult | null {
    if (surface === "assist") return renderAssist(this.#assist);
    return null;
  }

  /** A sign out empties every surface, so the next sign in starts fresh. */
  signOut(): void {
    this.#assist = emptyAssist();
  }

  // -- the assist surface ------------------------------------------------------------------------

  #editSeed(): string {
    const editing = this.#assist.editing;
    if (editing === null) return "";
    if (editing.kind === "role") {
      return String(this.#assist.limits?.role_budgets[editing.code] ?? 0);
    }
    const row = (this.#assist.report?.users ?? []).find((u) => u.user_id === editing.id);
    return String(row?.budget.tokens ?? 0);
  }

  #armAssistEdit(kind: string, id: string): void {
    if (kind !== "user" && kind !== "role") return;
    if (id === "") return;
    const editing: AssistEditTarget = kind === "role" ? { kind: "role", code: id } : { kind: "user", id };
    this.#assist = { ...this.#assist, editing, editError: "", notice: "" };
    this.#host.repaint();
  }

  async #loadAssist(): Promise<void> {
    // The save's notice outlives the re-read that follows it, as the hotkeys'
    // save does: the reload answers the read, not the write.
    this.#assist = { ...emptyAssist(), notice: this.#assist.notice };
    this.#host.repaint();
    // Three reads, one screen: the spend report is the headline read and its
    // refusal is the screen's phase; the limits and the settings failing only
    // cost their panels their own line.
    const [spend, limits, settings] = await Promise.all([
      this.#api.assistSpend(),
      this.#api.assistLimits(),
      this.#api.assistSettings(),
    ]);
    if (!spend.ok) {
      if (spend.status === 403) {
        this.#assist = { ...this.#assist, phase: "refused", error: assistRefusalWords(spend.error, spend.status) };
      } else {
        this.#assist = {
          ...this.#assist,
          phase: "error",
          error: `The assistant's month did not load. ${assistRefusalWords(spend.error, spend.status)}`,
        };
      }
      this.#host.repaint();
      return;
    }
    this.#assist = {
      ...this.#assist,
      phase: "ready",
      report: spend.value,
      limits: limits.ok ? limits.value : null,
      settings: settings.ok ? settings.value : null,
    };
    this.#host.repaint();
  }

  async #saveAssistBudget(kind: string, id: string): Promise<void> {
    if (this.#assist.busy) return;
    const tokens = wholeAmount(this.#host.field("assist-budget"));
    if (tokens === null) {
      this.#assist = { ...this.#assist, editError: assistBudgetInputWords() };
      this.#host.repaint();
      return;
    }
    this.#assist = { ...this.#assist, busy: true, editError: "", notice: "" };
    this.#host.repaint();
    const result =
      kind === "role" ? await this.#api.putRoleLimit(id, tokens) : await this.#api.putUserLimit(id, tokens);
    if (!result.ok) {
      // The server's own sentence is the message: a 4xx arrives in words.
      this.#assist = {
        ...this.#assist,
        busy: false,
        editError: assistRefusalWords(result.error, result.status),
      };
      this.#host.repaint();
      return;
    }
    this.#assist = { ...this.#assist, busy: false, editing: null, notice: "Saved." };
    this.#host.repaint();
    await this.#loadAssist();
  }

  async #resetAssistUserLimit(id: string): Promise<void> {
    if (id === "" || this.#assist.busy) return;
    this.#assist = { ...this.#assist, busy: true, editError: "", notice: "" };
    this.#host.repaint();
    const result = await this.#api.resetUserLimit(id);
    if (!result.ok) {
      this.#assist = {
        ...this.#assist,
        busy: false,
        editError: assistRefusalWords(result.error, result.status),
      };
      this.#host.repaint();
      return;
    }
    this.#assist = { ...this.#assist, busy: false, notice: "Saved. The person's role budget applies again." };
    this.#host.repaint();
    await this.#loadAssist();
  }

  async #putAssistSetting(key: string, checked: boolean): Promise<void> {
    if (key === "" || this.#assist.busy) return;
    this.#assist = { ...this.#assist, busy: true, editError: "", notice: "" };
    this.#host.repaint();
    const result = await this.#api.putSetting(key, checked ? "true" : "false");
    if (!result.ok) {
      this.#assist = {
        ...this.#assist,
        busy: false,
        editError: assistRefusalWords(result.error, result.status),
      };
      this.#host.repaint();
      return;
    }
    this.#assist = { ...this.#assist, busy: false, notice: "Saved." };
    this.#host.repaint();
    await this.#loadAssist();
  }
}
