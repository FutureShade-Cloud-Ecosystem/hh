// The receivables office's settings controller: the receivables rules (aging edges, reminder cadence, the paper path
// window, the plan), the register settings, the returned payment fee, and the automatic hold switch.
//
// A manager reads what it may and every write is an administrator's: the controls are held for any other role and say
// who may change them; the server's refusal stays the last word. What an administrator saves is checked before it
// is sent (office-logic.ts, the core's ranges) and then judged by the server; the screen shows what the server answered.
import type { BillingAutoHoldSetting, BillingLandingPolicy, BillingPolicyChange, BillingRegisterPolicy, Result, TillNSFPolicy } from "@hardscapehouse/api-client";
import type { OfficeApi } from "./office-api.js";
import type { OfficeHost } from "./office-desk.js";
import { autoHoldLines, landingPolicyCheck, nsfPolicyCheck, officeRefusal, registerPolicyCheck } from "./office-logic.js";
import { centsToField } from "./ar-money.js";

export type Loaded<T> =
  | { readonly phase: "none" | "loading" | "locked" }
  | { readonly phase: "error"; readonly words: string }
  | { readonly phase: "ready"; readonly value: T };

export interface SettingsModel {
  readonly canAdmin: boolean;
  readonly busy: boolean;
  readonly landing: Loaded<BillingLandingPolicy>;
  readonly register: Loaded<BillingRegisterPolicy>;
  readonly nsf: Loaded<TillNSFPolicy>;
  readonly autoHold: Loaded<BillingAutoHoldSetting>;
  readonly history: Loaded<readonly BillingPolicyChange[]>;
  /** The toggles that are buttons rather than fields: their state is held here. */
  readonly toggles: { readonly email: boolean; readonly sms: boolean; readonly showCredits: boolean; readonly taxable: boolean; readonly nsfHold: boolean };
  readonly tones: readonly string[];
  /** The way the automatic hold switch would go, waiting on its second press. */
  readonly confirmingHold: boolean | null;
  /** The plain lines about the automatic hold switch. */
  readonly holdLines: readonly string[];
}

const ZERO_TOGGLES = { email: false, sms: false, showCredits: false, taxable: false, nsfHold: false };

export class SettingsDesk {
  readonly #api: OfficeApi;
  readonly #host: OfficeHost;
  readonly #canAdmin: boolean;

  #busy = false;
  #landing: Loaded<BillingLandingPolicy> = { phase: "none" };
  #register: Loaded<BillingRegisterPolicy> = { phase: "none" };
  #nsf: Loaded<TillNSFPolicy> = { phase: "none" };
  #autoHold: Loaded<BillingAutoHoldSetting> = { phase: "none" };
  #history: Loaded<readonly BillingPolicyChange[]> = { phase: "none" };
  #toggles = { ...ZERO_TOGGLES };
  #tones: string[] = [];
  #confirmingHold: boolean | null = null;
  #fill = false;

  constructor(api: OfficeApi, host: OfficeHost, canAdmin: boolean) {
    this.#api = api;
    this.#host = host;
    this.#canAdmin = canAdmin;
  }

  model(): SettingsModel {
    return {
      canAdmin: this.#canAdmin,
      busy: this.#busy,
      landing: this.#landing,
      register: this.#register,
      nsf: this.#nsf,
      autoHold: this.#autoHold,
      history: this.#history,
      toggles: this.#toggles,
      tones: this.#tones,
      confirmingHold: this.#confirmingHold,
      holdLines: this.#autoHold.phase === "ready" ? autoHoldLines(this.#autoHold.value) : [],
    };
  }

  static #loaded<T>(got: Result<T>, action: string): Loaded<T> {
    return got.ok ? { phase: "ready", value: got.value } : { phase: "error", words: officeRefusal(action, got.error, got.status) };
  }

  /** Opens the Settings tab: what a manager reads is read afresh, and what only an administrator reads is asked only of one. */
  async open(): Promise<void> {
    this.#landing = this.#register = this.#nsf = { phase: "loading" };
    this.#autoHold = this.#history = this.#canAdmin ? { phase: "loading" } : { phase: "locked" };
    this.#confirmingHold = null;
    this.#host.repaint();
    const [landing, register, nsf, hold, history] = await Promise.all([
      this.#api.landingPolicy(),
      this.#api.registerPolicy(),
      this.#api.nsfPolicy(),
      this.#canAdmin ? this.#api.autoHold() : Promise.resolve(null),
      this.#canAdmin ? this.#api.policyHistory() : Promise.resolve(null),
    ]);
    this.#landing = SettingsDesk.#loaded(landing, "load");
    this.#register = SettingsDesk.#loaded(register, "load");
    this.#nsf = SettingsDesk.#loaded(nsf, "load");
    if (hold !== null) this.#autoHold = SettingsDesk.#loaded(hold, "auto-hold");
    if (history !== null) this.#history = history.ok ? { phase: "ready", value: history.value.items } : { phase: "error", words: officeRefusal("policy-history", history.error, history.status) };
    this.#syncToggles();
    this.#fill = true;
    this.#host.repaint();
  }

  /** The toggles and tones follow what the server last said. */
  #syncToggles(): void {
    const l = this.#landing.phase === "ready" ? this.#landing.value : null;
    const r = this.#register.phase === "ready" ? this.#register.value : null;
    const n = this.#nsf.phase === "ready" ? this.#nsf.value : null;
    this.#toggles = {
      email: l?.reminder_email_enabled ?? false,
      sms: l?.reminder_sms_enabled ?? false,
      showCredits: r?.show_credits_default ?? false,
      taxable: n?.fee_taxable ?? false,
      nsfHold: n?.auto_hold ?? false,
    };
    this.#tones = r === null ? [] : [...r.reminder_tones];
  }

  /** After each paint the boxes take what the server last said. */
  afterRender(): void {
    if (!this.#fill) return;
    this.#fill = false;
    const set = (name: string, value: string): void => this.#host.setField(name, value);
    if (this.#landing.phase === "ready") {
      const l = this.#landing.value;
      set("set-warn", String(l.aging_warn_days));
      set("set-escalate", String(l.aging_escalate_days));
      set("set-cadence", String(l.reminder_cadence_days));
      set("set-max", String(l.reminder_max_per_cycle));
      set("set-window", String(l.black_hole_window_days));
      set("set-minsends", String(l.black_hole_min_sends));
      set("set-fallback", l.untagged_fallback);
      set("set-tier", l.ar_tier);
    }
    if (this.#register.phase === "ready") {
      const r = this.#register.value;
      set("set-visibility", r.account_position_visibility);
      set("set-duesoon", String(r.due_soon_days));
      set("set-cooldown", String(r.reminder_cooldown_hours));
    }
    if (this.#nsf.phase === "ready") {
      const n = this.#nsf.value;
      set("set-fee", centsToField(n.fee_cents));
      set("set-code", n.fee_account_code);
      set("set-notes", n.notes);
    }
  }

  #walled(action: string): boolean {
    if (this.#canAdmin) return false;
    this.#host.error(officeRefusal(action, "Forbidden", 403));
    this.#host.reveal("[data-error]");
    return true;
  }

  #stop(words: string): void {
    this.#host.error(words);
    this.#host.reveal("[data-error]");
  }

  async #saveLanding(): Promise<void> {
    if (this.#walled("policy-write") || this.#busy) return;
    const f = (name: string): string => this.#host.field(name);
    const check = landingPolicyCheck({
      warn: f("set-warn"),
      escalate: f("set-escalate"),
      cadence: f("set-cadence"),
      maxPerCycle: f("set-max"),
      window: f("set-window"),
      minSends: f("set-minsends"),
      email: this.#toggles.email,
      sms: this.#toggles.sms,
      fallback: f("set-fallback"),
      tier: f("set-tier"),
    });
    if (!check.ok) return this.#stop(check.words);
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.putLandingPolicy(check.policy);
    this.#busy = false;
    if (!result.ok) {
      this.#host.error(officeRefusal("policy-write", result.error, result.status));
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#landing = { phase: "ready", value: result.value };
    this.#syncToggles();
    this.#host.notice("The receivables rules are saved. The landing and the reminders use them from now on.");
    this.#host.reveal("[data-notice]");
    const [history] = await Promise.all([this.#api.policyHistory(), this.#host.reloadBook()]);
    this.#history = history.ok ? { phase: "ready", value: history.value.items } : this.#history;
    this.#host.repaint();
  }

  async #saveRegister(): Promise<void> {
    if (this.#walled("policy-write") || this.#busy || this.#register.phase !== "ready") return;
    const current = this.#register.value;
    const check = registerPolicyCheck({
      visibility: this.#host.field("set-visibility"),
      dueSoon: this.#host.field("set-duesoon"),
      cooldown: this.#host.field("set-cooldown"),
      tones: this.#tones,
      showCredits: this.#toggles.showCredits,
    });
    if (!check.ok) return this.#stop(check.words);
    this.#busy = true;
    this.#host.repaint();
    // The layout settings (the panel split and arrangement) ride along as the server last said them.
    const result = await this.#api.putRegisterPolicy({
      ...current,
      account_position_visibility: check.values.account_position_visibility as BillingRegisterPolicy["account_position_visibility"],
      due_soon_days: check.values.due_soon_days,
      reminder_cooldown_hours: check.values.reminder_cooldown_hours,
      reminder_tones: check.values.reminder_tones as BillingRegisterPolicy["reminder_tones"],
      show_credits_default: check.values.show_credits_default,
    });
    this.#busy = false;
    if (!result.ok) {
      this.#host.error(officeRefusal("policy-write", result.error, result.status));
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    const again = await this.#api.registerPolicy();
    if (again.ok) this.#register = { phase: "ready", value: again.value };
    this.#syncToggles();
    this.#host.notice("The register settings are saved.");
    this.#host.reveal("[data-notice]");
    this.#fill = true;
    this.#host.repaint();
  }

  async #saveNsf(): Promise<void> {
    if (this.#walled("nsf-policy") || this.#busy || this.#nsf.phase !== "ready") return;
    const check = nsfPolicyCheck({
      fee: this.#host.field("set-fee"),
      taxable: this.#toggles.taxable,
      code: this.#host.field("set-code"),
      autoHold: this.#toggles.nsfHold,
      notes: this.#host.field("set-notes"),
    });
    if (!check.ok) return this.#stop(check.words);
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.putNsfPolicy({ branch_id: this.#nsf.value.branch_id, ...check.policy });
    this.#busy = false;
    if (!result.ok) {
      this.#host.error(officeRefusal("nsf-policy", result.error, result.status));
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    const again = await this.#api.nsfPolicy();
    if (again.ok) this.#nsf = { phase: "ready", value: again.value };
    this.#syncToggles();
    this.#host.notice("The returned payment fee is saved. The next payment marked bounced is charged it.");
    this.#host.reveal("[data-notice]");
    this.#fill = true;
    this.#host.repaint();
  }

  async #flipHold(): Promise<void> {
    const enabled = this.#confirmingHold;
    if (this.#walled("auto-hold") || this.#busy || enabled === null) return;
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.putAutoHold(enabled, this.#host.field("set-hold-reason").trim());
    this.#busy = false;
    this.#confirmingHold = null;
    if (!result.ok) {
      this.#host.error(officeRefusal("auto-hold", result.error, result.status));
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#autoHold = { phase: "ready", value: result.value };
    this.#host.notice(result.value.auto_hold_enabled ? "Automatic holds are on." : "Automatic holds are off.");
    this.#host.reveal("[data-notice]");
    this.#host.repaint();
  }

  /** Acts on a control carrying data-action. Returns true when the press was this slice's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const choice = control.getAttribute("data-choice") ?? "";
    switch (action) {
      case "set-toggle":
        if (!this.#canAdmin) {
          this.#walled("policy-write");
          return true;
        }
        if (choice === "email" || choice === "sms" || choice === "showCredits" || choice === "taxable" || choice === "nsfHold") this.#toggles = { ...this.#toggles, [choice]: !this.#toggles[choice] };
        this.#host.repaint();
        return true;
      case "set-tone":
        if (!this.#canAdmin) {
          this.#walled("policy-write");
          return true;
        }
        this.#tones = this.#tones.includes(choice) ? this.#tones.filter((t) => t !== choice) : [...this.#tones, choice];
        this.#host.repaint();
        return true;
      case "set-save-landing":
        void this.#saveLanding();
        return true;
      case "set-save-register":
        void this.#saveRegister();
        return true;
      case "set-save-nsf":
        void this.#saveNsf();
        return true;
      case "set-hold-start":
        if (this.#walled("auto-hold")) return true;
        this.#confirmingHold = choice === "on";
        this.#host.repaint();
        return true;
      case "set-hold-cancel":
        this.#confirmingHold = null;
        this.#host.repaint();
        return true;
      case "set-hold-confirm":
        void this.#flipHold();
        return true;
      default:
        return false;
    }
  }

  clear(): void {
    this.#landing = this.#register = this.#nsf = this.#autoHold = this.#history = { phase: "none" };
    this.#toggles = { ...ZERO_TOGGLES };
    this.#tones = [];
    this.#confirmingHold = null;
  }
}
