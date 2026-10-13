// The tax slice's state and its presses. The Financial Controller's desk for the sales tax: the rate table, the
// exemption profiles and the review queue, the certificate records behind them, the company wide settings, and the
// two checks (recompute past sales, reconcile to the ledger).
//
// The app holds no authority and computes no money of its own. Who may do what is the server's answer; the screen
// holds back only what it knows before a press (a rate that is not a percent, a day that is not a day, a profile
// whose forced fields are not filled, an order id that is not an id), and the server enforces it again. Every
// change that reaches every sale (a new rate, a profile, the settings, a recompute) asks first, in words that name
// what changes, and sends nothing until the second press.
//
// Every control is a plain element carrying a data-action name; the host's one click listener hands it to press().
// Form fields carry data-field names; the host's input and change listeners hand them to input() and change(),
// which keep the typed values in the desk so a repaint, or a move to another tab, never loses them. The one
// exception is the certificate number: it is read from its field at the press, sent once, and never kept.
import { html, type TemplateResult } from "lit";
import type { TaxConfig, TaxEvidence, TaxReasonCode, TaxReconReport, TaxRecomputeResult, TaxReliefEligibility, TaxRate, TaxStatusCardProfile } from "@hardscapehouse/api-client";
import type { TaxApi, ProfileInput, RateInput } from "./tax-api.js";
import {
  areaForNewRows,
  dayWords,
  forcedEvidence,
  hostDay,
  isDay,
  isProfileCode,
  isUuid,
  profileStatusWord,
  reasonWord,
  refusalWords,
  REASONS,
  ROUND_MODES,
  roundModeWord,
} from "./tax-logic.js";
import { bpsToField, formatRate, parseRateToBps } from "./tax-money.js";
import { renderTax, type ConfirmView, type TaxModel, type TxTab } from "./tax-views.js";

export interface TaxHost {
  readonly repaint: () => void;
  /** Puts a value into a named field of the desk's render root, a select by the id of its option, a box by ticking it. */
  readonly setField: (name: string, value: string) => void;
  /** Reads a named field of the desk's render root. */
  readonly field: (name: string) => string;
  /** Brings the first element matching the selector into view in the desk's render root. */
  readonly reveal: (selector: string) => void;
  /** Puts the keyboard on the first element matching the selector. */
  readonly focus: (selector: string) => void;
}

type Pending =
  | { readonly kind: "rate"; readonly input: RateInput }
  | { readonly kind: "profile"; readonly input: ProfileInput }
  | { readonly kind: "config"; readonly permit: string; readonly mode: TaxConfig["round_mode"]; readonly notes: string }
  | { readonly kind: "recompute"; readonly from: string; readonly to: string };

/** The month before today, from the host's own clock: the month the checks open on. */
function previousMonth(): { year: number; month: number; first: string; last: string } {
  const now = new Date();
  const before = new Date(now.getFullYear(), now.getMonth() - 1, 1);
  const year = before.getFullYear();
  const month = before.getMonth() + 1;
  const end = new Date(year, month, 0).getDate();
  const pad = (n: number): string => String(n).padStart(2, "0");
  return { year, month, first: `${year}-${pad(month)}-01`, last: `${year}-${pad(month)}-${pad(end)}` };
}

// The fields whose typed value the desk keeps. The certificate number is not among them.
const KEPT_FIELDS = [
  "tx-reason",
  "tx-rate",
  "tx-from",
  "tx-to",
  "tx-notes",
  "tx-p-code",
  "tx-p-name",
  "tx-p-rate",
  "tx-p-status",
  "tx-p-photo",
  "tx-p-number",
  "tx-p-attest",
  "tx-p-notes",
  "tx-c-profile",
  "tx-c-order",
  "tx-c-photo",
  "tx-c-expiry",
  "tx-c-attest",
  "tx-e-filter",
  "tx-e-asof",
  "tx-s-permit",
  "tx-s-notes",
  "tx-r-month",
  "tx-r-year",
  "tx-c-from",
  "tx-c-to",
] as const;

export class TaxDesk {
  readonly #api: TaxApi;
  readonly #host: TaxHost;
  #phase: TaxModel["phase"] = "loading";
  #loaded = false;
  #tab: TxTab = "rates";
  #rates: TaxRate[] = [];
  #profiles: TaxStatusCardProfile[] = [];
  #queue: TaxStatusCardProfile[] = [];
  #config: TaxConfig | null = null;
  #evidence: TaxEvidence[] = [];
  #evidenceLoaded = false;
  #selected: TaxEvidence | null = null;
  #check: TaxReliefEligibility | null = null;
  #downloaded = false;
  #recon: TaxReconReport | null = null;
  #recompute: TaxRecomputeResult | null = null;
  #confirm: ConfirmView | null = null;
  #pending: Pending | null = null;
  #busy = false;
  #error = "";
  #notice = "";
  // What the person has typed or chosen. A select holds the id of its chosen option, a box "true" or "false".
  readonly #form: Record<string, string> = {};
  #reveal = "";
  #focus = "";

  constructor(api: TaxApi, host: TaxHost) {
    this.#api = api;
    this.#host = host;
    const prior = previousMonth();
    this.#form["tx-reason"] = REASONS[0] ?? "";
    this.#form["tx-from"] = hostDay();
    this.#form["tx-p-status"] = "ACTIVE";
    this.#form["tx-r-year"] = String(prior.year);
    this.#form["tx-r-month"] = String(prior.month);
    this.#form["tx-c-from"] = prior.first;
    this.#form["tx-c-to"] = prior.last;
  }

  /** The first load, when the controller opens the area: every table the first screen needs. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const [rates, profiles, queue, config] = await Promise.all([this.#api.rates(), this.#api.profiles(), this.#api.reviewQueue(), this.#api.config()]);
    const failed = [rates, profiles, queue, config].find((a) => !a.ok);
    if (failed !== undefined && !failed.ok) {
      this.#phase = "error";
      this.#error = refusalWords("load", failed.error, failed.status ?? 0);
      this.#host.repaint();
      return;
    }
    if (rates.ok) this.#rates = rates.value;
    if (profiles.ok) this.#profiles = profiles.value;
    if (queue.ok) this.#queue = queue.value;
    if (config.ok) {
      this.#config = config.value;
      this.#form["tx-s-permit"] = config.value.hst_registration_no;
      this.#form["tx-s-mode"] = config.value.round_mode;
      this.#form["tx-s-notes"] = "";
    }
    this.#error = "";
    this.#phase = "ready";
    this.#loaded = true;
    this.#host.repaint();
  }

  /** The area was chosen: load once, then keep what is on screen. */
  ensureLoaded(): void {
    if (!this.#loaded && this.#phase !== "error" && !this.#busy) void this.load();
  }

  async #loadRates(): Promise<void> {
    const answer = await this.#api.rates();
    if (answer.ok) this.#rates = answer.value;
    else this.#error = refusalWords("load", answer.error, answer.status ?? 0);
  }

  async #loadProfiles(): Promise<void> {
    const [profiles, queue] = await Promise.all([this.#api.profiles(), this.#api.reviewQueue()]);
    if (profiles.ok) this.#profiles = profiles.value;
    else this.#error = refusalWords("load", profiles.error, profiles.status ?? 0);
    if (queue.ok) this.#queue = queue.value;
    else this.#error = refusalWords("load", queue.error, queue.status ?? 0);
  }

  async #loadConfig(): Promise<void> {
    const answer = await this.#api.config();
    if (answer.ok) {
      this.#config = answer.value;
      this.#form["tx-s-permit"] = answer.value.hst_registration_no;
      this.#form["tx-s-mode"] = answer.value.round_mode;
    } else this.#error = refusalWords("load", answer.error, answer.status ?? 0);
  }

  async #loadEvidence(): Promise<void> {
    const filter = (this.#form["tx-e-filter"] ?? "").trim();
    this.#evidenceLoaded = false;
    this.#host.repaint();
    const answer = await this.#api.evidence(filter);
    if (answer.ok) {
      this.#evidence = answer.value;
      this.#evidenceLoaded = true;
    } else {
      this.#error = refusalWords("evidence", answer.error, answer.status ?? 0);
      this.#evidenceLoaded = true;
    }
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  #say(error: string, notice: string): void {
    this.#error = error;
    this.#notice = notice;
    this.#reveal = error !== "" ? "[data-error]" : notice !== "" ? "[data-notice]" : "";
  }

  #ask(pending: Pending, view: ConfirmView): void {
    this.#clearMessages();
    this.#pending = pending;
    this.#confirm = view;
    this.#reveal = "[data-confirm]";
    this.#focus = "[data-confirm] [data-action='tx-confirm']";
    this.#host.repaint();
  }

  #drop(): void {
    this.#pending = null;
    this.#confirm = null;
  }

  /** A write that got no answer at all: the outcome is unknown, so say that and read what it touched. */
  #lost(what: string): string {
    return `Not confirmed: no answer came back, so ${what} may or may not have gone through. The table below has been read again; check it before you try again.`;
  }

  /** A control was pressed; true when this slice owns it. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    if (action === null || !action.startsWith("tx-")) return false;
    const id = control.getAttribute("data-id") ?? "";
    switch (action) {
      case "tx-tab": {
        const tab = control.getAttribute("data-tab");
        if (tab === "rates" || tab === "profiles" || tab === "certificates" || tab === "settings" || tab === "checks") void this.#openTab(tab);
        return true;
      }
      case "tx-reload":
        this.#loaded = false;
        void this.load();
        return true;
      case "tx-rate-add":
        this.#askRate();
        return true;
      case "tx-profile-save":
        this.#askProfile();
        return true;
      case "tx-profile-edit":
        this.#editProfile(id, false);
        return true;
      case "tx-profile-ratify":
        this.#editProfile(id, true);
        return true;
      case "tx-profile-clear":
        this.#clearProfileForm();
        this.#drop();
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "tx-config-save":
        this.#askConfig();
        return true;
      case "tx-round": {
        const mode = control.getAttribute("data-mode");
        if (mode !== null && ROUND_MODES.includes(mode)) {
          this.#form["tx-s-mode"] = mode;
          this.#host.repaint();
        }
        return true;
      }
      case "tx-recompute":
        this.#askRecompute();
        return true;
      case "tx-confirm":
        void this.#confirmed();
        return true;
      case "tx-cancel":
        this.#drop();
        this.#host.repaint();
        return true;
      case "tx-recon-run":
        void this.#runRecon();
        return true;
      case "tx-evidence-filter":
        void this.#filterEvidence();
        return true;
      case "tx-evidence-unfilter":
        this.#form["tx-e-filter"] = "";
        this.#host.setField("tx-e-filter", "");
        this.#clearMessages();
        void this.#loadEvidence().then(() => this.#host.repaint());
        return true;
      case "tx-evidence-open":
        this.#reveal = "[data-evidence-panel]";
        void this.#openEvidence(id);
        return true;
      case "tx-evidence-close":
        this.#selected = null;
        this.#check = null;
        this.#downloaded = false;
        this.#host.repaint();
        return true;
      case "tx-evidence-check":
        void this.#checkEvidence(id);
        return true;
      case "tx-evidence-download":
        void this.#downloadEvidence(id);
        return true;
      case "tx-capture":
        void this.#capture();
        return true;
      default:
        return false;
    }
  }

  /** A field changed as the person typed: kept in the desk, so nothing typed is lost to a repaint. */
  input(control: Element): void {
    const name = control.getAttribute("data-field");
    if (name === null || !name.startsWith("tx-")) return;
    if (control instanceof HTMLInputElement && control.type !== "checkbox" && (KEPT_FIELDS as readonly string[]).includes(name)) {
      this.#form[name] = control.value;
      // The hints read the typed text; nothing else needs a repaint per key.
      if (name === "tx-rate" || name === "tx-p-rate" || name === "tx-r-year") this.#host.repaint();
    }
  }

  /** A field was left, a select chosen or a box ticked. */
  change(control: Element): void {
    const name = control.getAttribute("data-field");
    if (name === null || !name.startsWith("tx-")) return;
    if (control instanceof HTMLSelectElement) {
      const chosen = control.selectedOptions[0]?.getAttribute("data-id") ?? "";
      this.#form[name] = chosen;
      if (name === "tx-reason" || name === "tx-c-profile") this.#host.repaint();
    } else if (control instanceof HTMLInputElement && control.type === "checkbox") {
      this.#form[name] = control.checked ? "true" : "false";
    }
  }

  /** After each paint: put every kept value back into its field, bring the asked element into view, and take the keyboard to it. */
  afterRender(): void {
    for (const name of KEPT_FIELDS) {
      const value = this.#form[name];
      if (value !== undefined) this.#host.setField(name, value);
    }
    if (this.#reveal !== "") {
      this.#host.reveal(this.#reveal);
      this.#reveal = "";
    }
    if (this.#focus !== "") {
      this.#host.focus(this.#focus);
      this.#focus = "";
    }
  }

  async #openTab(tab: TxTab): Promise<void> {
    this.#tab = tab;
    this.#drop();
    this.#clearMessages();
    this.#host.repaint();
    if (tab === "certificates" && !this.#evidenceLoaded) {
      await this.#loadEvidence();
      this.#host.repaint();
    }
  }

  // ---- rates -------------------------------------------------------------------------------------------

  #askRate(): void {
    if (this.#busy) return;
    const reason = this.#form["tx-reason"] ?? "";
    const bps = parseRateToBps(this.#form["tx-rate"] ?? "");
    const from = (this.#form["tx-from"] ?? "").trim() === "" ? hostDay() : (this.#form["tx-from"] ?? "").trim();
    const to = (this.#form["tx-to"] ?? "").trim();
    if (!REASONS.includes(reason)) return this.#refuse("Choose a reason first.");
    if (bps === null) return this.#refuse("Enter the rate as a percent between 0 and 100 with at most two decimals, such as 7.75. Nothing was sent.");
    if (!isDay(from)) return this.#refuse("The first day must be a real day, written year, month, day with dashes. Nothing was sent.");
    if (to !== "" && !isDay(to)) return this.#refuse("The last day must be a real day, written year, month, day with dashes, or left empty. Nothing was sent.");
    if (to !== "" && to < from) return this.#refuse("The last day cannot come before the first day. Nothing was sent.");
    const area = areaForNewRows(this.#rates, hostDay());
    const input: RateInput = { jurisdiction: area, reasonCode: reason as TaxReasonCode, rateBps: bps, effectiveFrom: from, effectiveTo: to, notes: (this.#form["tx-notes"] ?? "").trim() };
    this.#ask(
      { kind: "rate", input },
      {
        kind: "rate",
        title: `Add ${reasonWord(reason)} at ${formatRate(bps)}?`,
        lines: [
          `It starts ${dayWords(from)} and ${to === "" ? "has no end" : `ends ${dayWords(to)}`}. Sales rung on those days use it.`,
          "Rows are never edited or removed. If this is wrong, add another row that replaces it from a later day.",
        ],
        go: "Add the rate",
      },
    );
  }

  #refuse(words: string): void {
    this.#say(words, "");
    this.#drop();
    this.#host.repaint();
  }

  // ---- profiles ----------------------------------------------------------------------------------------

  #clearProfileForm(): void {
    for (const name of ["tx-p-code", "tx-p-name", "tx-p-rate", "tx-p-notes"]) this.#form[name] = "";
    this.#form["tx-p-status"] = "ACTIVE";
    for (const name of ["tx-p-photo", "tx-p-number", "tx-p-attest"]) this.#form[name] = "false";
    this.#form["tx-p-editing"] = "";
  }

  #editProfile(code: string, ratify: boolean): void {
    const p = this.#profiles.find((x) => x.code === code) ?? this.#queue.find((x) => x.code === code);
    if (p === undefined) return;
    this.#form["tx-p-code"] = p.code;
    this.#form["tx-p-name"] = p.name;
    this.#form["tx-p-rate"] = bpsToField(p.rate_bps);
    this.#form["tx-p-status"] = ratify ? "ACTIVE" : p.status;
    this.#form["tx-p-photo"] = p.requires_photo ? "true" : "false";
    this.#form["tx-p-number"] = p.requires_number ? "true" : "false";
    this.#form["tx-p-attest"] = p.requires_attestation ? "true" : "false";
    this.#form["tx-p-notes"] = p.notes ?? "";
    this.#form["tx-p-editing"] = p.code;
    this.#drop();
    this.#clearMessages();
    this.#reveal = "[data-profile-form]";
    if (ratify) this.#askProfile();
    else this.#host.repaint();
  }

  #askProfile(): void {
    if (this.#busy) return;
    const code = (this.#form["tx-p-code"] ?? "").trim();
    const name = (this.#form["tx-p-name"] ?? "").trim();
    const bps = parseRateToBps(this.#form["tx-p-rate"] ?? "");
    const status = this.#form["tx-p-status"] ?? "ACTIVE";
    if (!isProfileCode(code)) return this.#refuse("Give the profile a code of capitals, digits and underscores, such as RESALE. Nothing was sent.");
    if (name === "") return this.#refuse("Give the profile a name first. Nothing was sent.");
    if (bps === null) return this.#refuse("Enter the rate as a percent between 0 and 100 with at most two decimals, such as 0. Nothing was sent.");
    if (status !== "ACTIVE" && status !== "PENDING" && status !== "RETIRED") return this.#refuse("Choose where the profile stands first.");
    const input: ProfileInput = {
      code,
      name,
      rateBps: bps,
      requiresPhoto: this.#form["tx-p-photo"] === "true",
      requiresNumber: this.#form["tx-p-number"] === "true",
      requiresAttestation: this.#form["tx-p-attest"] === "true",
      status,
      notes: (this.#form["tx-p-notes"] ?? "").trim(),
    };
    const exists = this.#profiles.some((p) => p.code === code);
    this.#ask(
      { kind: "profile", input },
      {
        kind: "profile",
        title: `${exists ? "Replace" : "Save"} the ${name} profile?`,
        lines: [
          `Buyers with this certificate pay ${formatRate(bps)}. ${forcedEvidence({ requires_photo: input.requiresPhoto, requires_number: input.requiresNumber, requires_attestation: input.requiresAttestation })}. Standing: ${profileStatusWord(status).toLowerCase()}.`,
          exists ? `The profile ${code} already exists, and saving replaces its settings for every sale from now on.` : "It applies to sales from now on.",
        ],
        go: exists ? "Replace the profile" : "Save the profile",
      },
    );
  }

  // ---- settings ----------------------------------------------------------------------------------------

  #askConfig(): void {
    if (this.#busy || this.#config === null) return;
    const permit = (this.#form["tx-s-permit"] ?? "").trim();
    const mode = this.#form["tx-s-mode"] ?? "INVOICE_TOTAL";
    if (mode !== "INVOICE_TOTAL" && mode !== "PER_LINE") return this.#refuse("Choose how the tax is rounded first.");
    this.#ask(
      { kind: "config", permit, mode, notes: (this.#form["tx-s-notes"] ?? "").trim() },
      {
        kind: "config",
        title: "Change the company wide tax settings?",
        lines: [
          `Seller's permit number: ${permit === "" ? "none" : permit}. Rounding: ${roundModeWord(mode).toLowerCase()}.`,
          "This changes the settings for every branch. Past sales are not recomputed by it.",
        ],
        go: "Save the settings",
      },
    );
  }

  // ---- recompute and reconcile -------------------------------------------------------------------------

  #askRecompute(): void {
    if (this.#busy) return;
    const from = (this.#form["tx-c-from"] ?? "").trim();
    const to = (this.#form["tx-c-to"] ?? "").trim();
    if (!isDay(from) || !isDay(to)) return this.#refuse("Give both ends of the range as real days, written year, month, day with dashes. Nothing was sent.");
    if (to < from) return this.#refuse("The range cannot end before it starts. Nothing was sent.");
    this.#ask(
      { kind: "recompute", from, to },
      {
        kind: "recompute",
        title: `Recompute sales from ${dayWords(from)} to ${dayWords(to)}?`,
        lines: [
          "A second, audit only tax record is added for each sale in the range that has none. Nothing is rewritten or removed, and the ledger is not corrected.",
          "Running it again adds nothing new.",
        ],
        go: "Recompute",
      },
    );
  }

  async #runRecon(): Promise<void> {
    if (this.#busy) return;
    const year = (this.#form["tx-r-year"] ?? "").trim();
    const month = Number(this.#form["tx-r-month"] ?? "0");
    if (!/^\d{4}$/.test(year)) return this.#refuse("Enter a four digit year. Nothing was sent.");
    this.#clearMessages();
    this.#busy = true;
    this.#host.repaint();
    const answer = await this.#api.reconcile(Number(year), Number.isInteger(month) ? month : 0);
    this.#busy = false;
    if (answer.ok) {
      this.#recon = answer.value;
      this.#say("", "");
    } else {
      this.#say(refusalWords("recon", answer.error, answer.status ?? 0), "");
    }
    this.#host.repaint();
  }

  // ---- the second press --------------------------------------------------------------------------------

  async #confirmed(): Promise<void> {
    const pending = this.#pending;
    if (pending === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    switch (pending.kind) {
      case "rate":
        await this.#sendRate(pending.input);
        break;
      case "profile":
        await this.#sendProfile(pending.input);
        break;
      case "config":
        await this.#sendConfig(pending.permit, pending.mode, pending.notes);
        break;
      case "recompute":
        await this.#sendRecompute(pending.from, pending.to);
        break;
    }
    this.#busy = false;
    this.#host.repaint();
  }

  async #sendRate(input: RateInput): Promise<void> {
    const answer = await this.#api.createRate(input);
    this.#drop();
    if (answer.ok) {
      await this.#loadRates();
      this.#form["tx-rate"] = "";
      this.#form["tx-to"] = "";
      this.#form["tx-notes"] = "";
      this.#say("", `Added: ${reasonWord(input.reasonCode)} at ${formatRate(input.rateBps)} from ${dayWords(input.effectiveFrom)}. It is in the table.`);
      return;
    }
    if (answer.status === undefined) {
      await this.#loadRates();
      this.#say(this.#lost("the rate"), "");
      return;
    }
    this.#say(refusalWords("rate", answer.error, answer.status), "");
  }

  async #sendProfile(input: ProfileInput): Promise<void> {
    const answer = await this.#api.saveProfile(input);
    this.#drop();
    if (answer.ok) {
      await this.#loadProfiles();
      this.#clearProfileForm();
      this.#say("", `Saved: ${answer.value.name}, ${profileStatusWord(answer.value.status).toLowerCase()}.`);
      return;
    }
    if (answer.status === undefined) {
      await this.#loadProfiles();
      this.#say(this.#lost("the profile"), "");
      return;
    }
    this.#say(refusalWords("profile", answer.error, answer.status), "");
  }

  async #sendConfig(permit: string, mode: TaxConfig["round_mode"], notes: string): Promise<void> {
    const answer = await this.#api.saveConfig(permit, mode, notes);
    this.#drop();
    if (answer.ok) {
      this.#config = answer.value;
      this.#form["tx-s-permit"] = answer.value.hst_registration_no;
      this.#form["tx-s-mode"] = answer.value.round_mode;
      this.#say("", "Saved: the company wide tax settings.");
      return;
    }
    if (answer.status === undefined) {
      await this.#loadConfig();
      this.#say(this.#lost("the settings"), "");
      return;
    }
    this.#say(refusalWords("config", answer.error, answer.status), "");
  }

  async #sendRecompute(from: string, to: string): Promise<void> {
    const answer = await this.#api.recompute(from, to);
    this.#drop();
    if (answer.ok) {
      this.#recompute = answer.value;
      this.#say("", "");
      return;
    }
    if (answer.status === undefined) {
      this.#say("Not confirmed: no answer came back, so the recompute may or may not have run. Running it again is safe: it adds nothing that is already there.", "");
      return;
    }
    this.#say(refusalWords("recompute", answer.error, answer.status), "");
  }

  // ---- certificates ------------------------------------------------------------------------------------

  async #filterEvidence(): Promise<void> {
    if (this.#busy) return;
    const order = (this.#form["tx-e-filter"] ?? "").trim();
    if (order !== "" && !isUuid(order)) return this.#refuse("That is not an order id. Nothing was sent.");
    this.#clearMessages();
    await this.#loadEvidence();
    this.#host.repaint();
  }

  async #openEvidence(id: string): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const answer = await this.#api.view(id);
    this.#busy = false;
    if (answer.ok) {
      this.#selected = answer.value;
      this.#check = null;
      this.#downloaded = false;
      // A date typed for one record is not the date for the next.
      this.#form["tx-e-asof"] = "";
    } else this.#say(refusalWords("evidence", answer.error, answer.status ?? 0), "");
    this.#host.repaint();
  }

  async #checkEvidence(id: string): Promise<void> {
    if (this.#busy) return;
    const asOf = (this.#form["tx-e-asof"] ?? "").trim();
    if (asOf !== "" && !isDay(asOf)) return this.#refuse("The check date must be a real day, written year, month, day with dashes. Nothing was sent.");
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const answer = await this.#api.validate(id, asOf);
    this.#busy = false;
    if (answer.ok) this.#check = answer.value;
    else this.#say(refusalWords("evidence", answer.error, answer.status ?? 0), "");
    this.#host.repaint();
  }

  async #downloadEvidence(id: string): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const answer = await this.#api.download(id);
    this.#busy = false;
    if (answer.ok) {
      this.#selected = answer.value;
      this.#downloaded = true;
    } else this.#say(refusalWords("evidence", answer.error, answer.status ?? 0), "");
    this.#host.repaint();
  }

  async #capture(): Promise<void> {
    if (this.#busy) return;
    const code = this.#form["tx-c-profile"] ?? "";
    const profile = this.#profiles.find((p) => p.code === code);
    const rawNumber = this.#host.field("tx-c-number").trim();
    const order = (this.#form["tx-c-order"] ?? "").trim();
    const photo = (this.#form["tx-c-photo"] ?? "").trim();
    const expiry = (this.#form["tx-c-expiry"] ?? "").trim();
    const signed = this.#form["tx-c-attest"] === "true";
    if (profile === undefined) return this.#refuse("Choose the profile this certificate is for. Nothing was sent.");
    if (profile.requires_photo && photo === "") return this.#refuse(`Not recorded: the ${profile.name} profile requires the photo of the certificate. Say where the photo is kept.`);
    if (profile.requires_number && rawNumber === "") return this.#refuse(`Not recorded: the ${profile.name} profile requires the certificate number.`);
    if (profile.requires_attestation && !signed) return this.#refuse(`Not recorded: the ${profile.name} profile requires the signed statement. Tick the box once the buyer has signed.`);
    if (order !== "" && !isUuid(order)) return this.#refuse("That is not an order id. Leave it empty if there is no order. Nothing was sent.");
    if (expiry !== "" && !isDay(expiry)) return this.#refuse("The expiry must be a real day, written year, month, day with dashes. Nothing was sent.");
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const answer = await this.#api.capture({ cardProfileCode: code, orderId: order, photoRef: photo, rawNumber, attestation: signed, expiry });
    this.#busy = false;
    // The number leaves the field whatever the answer: it is sent once and not kept.
    this.#host.setField("tx-c-number", "");
    if (answer.ok) {
      for (const name of ["tx-c-order", "tx-c-photo", "tx-c-expiry"]) this.#form[name] = "";
      this.#form["tx-c-attest"] = "false";
      await this.#loadEvidence();
      this.#selected = answer.value;
      this.#check = null;
      this.#downloaded = false;
      this.#say("", `Recorded. The number is kept as ${answer.value.number_mask}; the full number is not stored.`);
    } else if (answer.status === undefined) {
      await this.#loadEvidence();
      this.#say("Not confirmed: no answer came back, so the certificate may or may not have been recorded. The list has been read again; check it, and type the number again if it is not there.", "");
    } else this.#say(refusalWords("capture", answer.error, answer.status), "");
    this.#host.repaint();
  }

  // ---- the screen --------------------------------------------------------------------------------------

  render(): TemplateResult {
    return html`${renderTax(this.#model())}`;
  }

  #model(): TaxModel {
    return {
      phase: this.#phase,
      tab: this.#tab,
      today: hostDay(),
      rates: this.#rates,
      profiles: this.#profiles,
      queue: this.#queue,
      config: this.#config,
      evidence: this.#evidence,
      evidenceLoaded: this.#evidenceLoaded,
      selected: this.#selected,
      check: this.#check,
      downloaded: this.#downloaded,
      recon: this.#recon,
      recompute: this.#recompute,
      confirm: this.#confirm,
      form: this.#form,
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
    };
  }
}
